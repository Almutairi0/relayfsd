package main

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/crypto/ssh"
)

// logLines builds lines in the exact formats the daemon writes.
func logLines(msgs ...string) []logEntry {
	var out []logEntry
	for i, m := range msgs {
		out = append(out, parseLogLine(fmt.Sprintf("2026/11/21 10:00:%02d %s", i, m)))
	}
	return out
}

func TestParseLogLine(t *testing.T) {
	e := parseLogLine("2026/11/21 10:15:02 INFO | Found: /srv/a b: c.mkv (on local)")
	if e.Time.IsZero() || e.Time.Minute() != 15 || e.Level != "INFO" || e.Msg != "Found: /srv/a b: c.mkv (on local)" {
		t.Fatalf("unexpected parse: %+v", e)
	}
	// Lines from other tools or without a level are kept as they are.
	e = parseLogLine("garbage | here")
	if !e.Time.IsZero() || e.Level != "" || e.Msg != "garbage | here" {
		t.Fatalf("unexpected parse: %+v", e)
	}
}

func TestDashboardTransfers(t *testing.T) {
	var d dashboard
	for _, e := range logLines(
		"INFO | Monitoring local /home/u/watch",
		"INFO | Found: /home/u/watch/a: b.mkv (on local)",
		"INFO | Now Syncing: a: b.mkv",
		"INFO | Found: /home/u/watch/c.iso (on local)",
		"INFO | Now Syncing: c.iso",
		"INFO | Sync complete: a: b.mkv",
		"INFO | Moved local file: /home/u/watch/a: b.mkv -> /home/u/done/a: b.mkv",
		"ERROR | Sync failed for /home/u/watch/c.iso: file copy failed: EOF",
	) {
		d.apply(e)
	}
	if d.watcher != watcherRunning || d.synced != 1 || d.failed != 1 || d.inProgress() != 0 {
		t.Fatalf("counts: %+v", d)
	}
	a, c := d.transfers[0], d.transfers[1]
	if a.Status != transferDone || a.After != "moved" || a.Side != "local" {
		t.Errorf("first transfer: %+v", a)
	}
	if c.Status != transferFailed || c.Err != "file copy failed: EOF" {
		t.Errorf("second transfer: %+v", c)
	}
	if d.conn != connOnline {
		t.Errorf("conn = %v, want online after a sync", d.conn)
	}
}

func TestDashboardConnection(t *testing.T) {
	var d dashboard
	steps := []struct {
		msg  string
		want connState
	}{
		{"INFO | Monitoring remote: /srv/dl", connUnknown},
		{"WARN | Remote 10.0.0.2 offline (SSH port unreachable), retrying with backoff up to 5m0s", connOffline},
		{"INFO | Remote 10.0.0.2 back online", connOnline},
		{"ERROR | Remote 10.0.0.2: refusing to connect, its identity could not be verified (x); retrying with backoff up to 5m0s", connUntrusted},
	}
	for _, s := range steps {
		d.apply(logLines(s.msg)[0])
		if d.conn != s.want {
			t.Fatalf("after %q: conn = %v, want %v", s.msg, d.conn, s.want)
		}
	}
	d.apply(logLines("INFO | Monitoring remote: /srv/dl")[0])
	d.apply(logLines("WARN | Remote 10.0.0.2 offline (SSH port unreachable), retrying with backoff up to 5m0s")[0])
	if d.connDetail != "SSH port unreachable" {
		t.Errorf("connDetail = %q", d.connDetail)
	}
	d.apply(logLines("FATAL | Host key check failed: nope")[0])
	if d.watcher != watcherStopped {
		t.Errorf("watcher = %v, want stopped", d.watcher)
	}
}

func TestDashboardRestartClosesOpenTransfers(t *testing.T) {
	var d dashboard
	for _, e := range logLines(
		"INFO | Found: /w/x (on local)",
		"INFO | Now Syncing: x",
		"INFO | Monitoring local /w",
	) {
		d.apply(e)
	}
	if d.transfers[0].Status != transferFailed || d.inProgress() != 0 {
		t.Fatalf("transfer left open across a restart: %+v", d.transfers[0])
	}
}

func TestDefaultAuthMethod(t *testing.T) {
	cases := []struct {
		c    Config
		want string
	}{
		{Config{}, "key"},
		{Config{Password: "x"}, "password"},
		{Config{AuthMethod: "key", Password: "x"}, "key"},
		{Config{AuthMethod: "password"}, "password"},
	}
	for _, c := range cases {
		if got := defaultAuthMethod(c.c); got != c.want {
			t.Errorf("defaultAuthMethod(%+v) = %q, want %q", c.c, got, c.want)
		}
	}
}

func TestHostKeyReport(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	cfg = Config{IP: "10.0.0.2"}
	fp := ssh.FingerprintSHA256(key)

	r := hostKeyReportFor(hostKeyUnknown, key)
	if !r.CanTrust || r.NeedYes || r.Danger || r.Title != "" || !strings.Contains(strings.Join(r.Lines, "\n"), fp) {
		t.Errorf("unknown: %+v", r)
	}
	if !strings.Contains(strings.Join(r.Lines, "\n"), "ssh_host_ed25519_key.pub") {
		t.Errorf("unknown: missing verify hint: %v", r.Lines)
	}
	r = hostKeyReportFor(hostKeyUnverifiable, key)
	if !r.CanTrust || !r.NeedYes || r.Title == "" {
		t.Errorf("unverifiable: %+v", r)
	}
	for _, s := range []hostKeyStatus{hostKeyMismatch, hostKeyRevoked, hostKeyNoCommon} {
		r = hostKeyReportFor(s, key)
		if r.CanTrust || !r.Danger || r.Title == "" {
			t.Errorf("status %v: %+v", s, r)
		}
	}
	r = hostKeyReportFor(hostKeyTrusted, key)
	if r.CanTrust || r.Danger || len(r.Lines) != 1 {
		t.Errorf("trusted: %+v", r)
	}
}

func TestNormalized(t *testing.T) {
	c := Config{AuthMethod: "key", Password: "secret", KeyPath: "~/.ssh/k",
		WatchSide: "remote", DestSide: "local", AfterUpload: "delete", MoveDir: "/x"}
	n := normalized(c)
	if n.Password != "" || n.KeyPath != "~/.ssh/k" || n.AfterUpload != "" || n.MoveDir != "" {
		t.Errorf("normalized = %+v", n)
	}
}

func TestMaskWebhook(t *testing.T) {
	got := maskWebhook("https://discord.com/api/webhooks/123/s3cr3t-token")
	if strings.Contains(got, "s3cr3t") || !strings.HasPrefix(got, "https://discord.com/api/webhooks/123/") {
		t.Errorf("maskWebhook = %q", got)
	}
	if got := maskWebhook("s3cr3t"); strings.Contains(got, "s3cr3t") {
		t.Errorf("maskWebhook without slash = %q", got)
	}
}

func TestMaskWebhookSuffix(t *testing.T) {
	for _, u := range []string{
		"https://discord.com/api/webhooks/123/SECRET/slack",
		"https://discord.com/api/webhooks/123/SECRET/github",
		"https://example.com/hook/SECRET",
	} {
		if got := maskWebhook(u); strings.Contains(got, "SECRET") {
			t.Errorf("maskWebhook(%q) = %q", u, got)
		}
	}
}

func TestDashboardUnsupportedDirection(t *testing.T) {
	var d dashboard
	for _, e := range logLines(
		"INFO | Found: /srv/b.iso (on remote)",
		"INFO | Now Syncing: b.iso",
		"WARN | Unsupported direction: watch=remote dest=remote",
	) {
		d.apply(e)
	}
	if d.inProgress() != 0 || d.transfers[0].Status != transferFailed || d.failed != 1 {
		t.Fatalf("transfer left open: %+v", d.transfers[0])
	}
}

func TestDashboardSameName(t *testing.T) {
	var d dashboard
	for _, e := range logLines(
		"INFO | Found: /w/a/x.txt (on local)",
		"INFO | Now Syncing: x.txt",
		"INFO | Found: /w/b/x.txt (on local)",
		"INFO | Now Syncing: x.txt",
		"INFO | Sync complete: x.txt",
		"ERROR | Sync failed for /w/b/x.txt: boom",
	) {
		d.apply(e)
	}
	a, b := d.transfers[0], d.transfers[1]
	if a.Status != transferDone || b.Status != transferFailed || b.Err != "boom" {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	// Completion credited to the wrong file is moved when the failure
	// names the other one.
	d = dashboard{}
	for _, e := range logLines(
		"INFO | Found: /w/a/x.txt (on local)",
		"INFO | Now Syncing: x.txt",
		"INFO | Found: /w/b/x.txt (on local)",
		"INFO | Now Syncing: x.txt",
		"INFO | Sync complete: x.txt",
		"ERROR | Sync failed for /w/a/x.txt: boom",
	) {
		d.apply(e)
	}
	a, b = d.transfers[0], d.transfers[1]
	if a.Status != transferFailed || b.Status != transferDone || d.inProgress() != 0 {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

func runRead(t *testing.T, path string, prev tailMsg, first bool) tailMsg {
	t.Helper()
	var msg tea.Msg
	if first {
		msg = readLog(path, nil, 0, "", "")()
	} else {
		msg = readLog(path, prev.file, prev.offset, prev.partial, prev.head)()
	}
	return msg.(tailMsg)
}

func TestReadLogSeedsFromWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relayfsd.log")
	var b strings.Builder
	b.WriteString("2026/11/21 10:00:00 INFO | Monitoring local /w\n")
	for i := 0; b.Len() < tailBacklog*2; i++ {
		fmt.Fprintf(&b, "2026/11/21 10:00:01 INFO | Found: /w/f%d (on local)\n", i)
		fmt.Fprintf(&b, "2026/11/21 10:00:01 INFO | Now Syncing: f%d\n", i)
		fmt.Fprintf(&b, "2026/11/21 10:00:02 INFO | Sync complete: f%d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	msg := runRead(t, path, tailMsg{}, true)
	if msg.seed == nil || msg.seed.watcher != watcherRunning {
		t.Fatalf("seed missing the Monitoring line: %+v", msg.seed)
	}
	d := *msg.seed
	for _, l := range msg.lines {
		d.apply(parseLogLine(l))
	}
	want := strings.Count(b.String(), "Sync complete")
	if d.synced != want {
		t.Errorf("synced = %d, want %d", d.synced, want)
	}
}

func TestReadLogNoticesRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relayfsd.log")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("2026/11/21 10:00:00 INFO | Found: /w/old.bin (on local)\n")
	msg := runRead(t, path, tailMsg{}, true)
	// Truncate and refill past the old offset before the next tick.
	write("2026/11/21 10:00:05 INFO | Found: /w/fast1.txt (on local)\n2026/11/21 10:00:05 INFO | Found: /w/fast2.txt (on local)\n")
	msg = runRead(t, path, msg, false)
	if !msg.reset || len(msg.lines) != 2 || !strings.Contains(msg.lines[0], "fast1") {
		t.Fatalf("rewrite not noticed: reset=%v lines=%q", msg.reset, msg.lines)
	}
}

func TestKeyCheckCache(t *testing.T) {
	c := &keyCheckCache{}
	err1 := c.check("/nonexistent/key", "")
	err2 := c.check("/nonexistent/key", "")
	if err1 == nil || err1 != err2 {
		t.Fatalf("expected the cached error to be reused: %v %v", err1, err2)
	}
	if err3 := c.check("/nonexistent/other", ""); err3 == err1 {
		t.Fatal("a new path must be checked again")
	}
}
