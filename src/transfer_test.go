package main

import (
	"bufio"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

// memSFTP returns a client talking to an in-memory SFTP server.
func memSFTP(t *testing.T) *sftp.Client {
	t.Helper()
	c1, c2 := net.Pipe()
	srv := sftp.NewRequestServer(c1, sftp.InMemHandler())
	go srv.Serve()
	client, err := sftp.NewClientPipe(c2, c2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); srv.Close() })
	return client
}

func TestRemotePathForSameNames(t *testing.T) {
	client := memSFTP(t)
	cfg = Config{RemoteDir: "/up"}
	logger = log.New(io.Discard, "", 0)
	remoteNames.byLocal, remoteNames.owner = map[string]string{}, map[string]string{}
	if err := client.Mkdir("/up"); err != nil {
		t.Fatal(err)
	}
	if f, err := client.Create("/up/old.txt"); err == nil {
		f.Close()
	}

	a, _ := remotePathFor(client, "/w/x/same.bin")
	b, _ := remotePathFor(client, "/w/y/same.bin")
	if a != "/up/same.bin" || b != "/up/same (1).bin" {
		t.Fatalf("same names: %q %q", a, b)
	}
	// Uploading the same local file again keeps its remote name.
	if again, _ := remotePathFor(client, "/w/x/same.bin"); again != a {
		t.Fatalf("re-upload went to %q, want %q", again, a)
	}
	// A file that already exists on the server is never overwritten.
	if p, _ := remotePathFor(client, "/w/old.txt"); p != "/up/old (1).txt" {
		t.Fatalf("existing remote file: %q", p)
	}
	// Once the local file is moved or deleted, a new file at that path is
	// a different file.
	forgetRemoteName("/w/x/same.bin")
	if f, err := client.Create(a); err == nil {
		f.Close()
	}
	if p, _ := remotePathFor(client, "/w/x/same.bin"); p == a {
		t.Fatalf("new file at the old path overwrote %q", a)
	}
}

func TestCleanPartialDownloads(t *testing.T) {
	dir := t.TempDir()
	cfg = Config{WatchPath: dir}
	logger = log.New(io.Discard, "", 0)
	interruptedDownloads = map[string]bool{}
	for _, name := range []string{".movie.v2.mkv.123456" + partSuffix, "keep.txt", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cleanPartialDownloads()
	if !interruptedDownloads["movie.v2.mkv"] || len(interruptedDownloads) != 1 {
		t.Fatalf("interrupted = %v", interruptedDownloads)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 2 {
		t.Fatalf("left %d files, want keep.txt and .hidden", len(left))
	}
}

func TestReadSideKeepsCurrentOnEnter(t *testing.T) {
	cases := []struct{ input, current, want string }{
		{"\n", "remote", "remote"}, // Enter keeps the shown choice
		{"\n", "local", "local"},
		{"1\n", "remote", "local"},
		{"2\n", "local", "remote"},
		{"remote\n", "local", "remote"},
		{"bogus\n", "remote", "remote"},
		{"", "remote", "remote"}, // end of input
	}
	for _, c := range cases {
		r := bufio.NewReader(strings.NewReader(c.input))
		if got := readSide(r, "local", "remote", c.current); got != c.want {
			t.Errorf("readSide(%q, current %q) = %q, want %q", c.input, c.current, got, c.want)
		}
	}
}

func TestMaskWebhookOddShapes(t *testing.T) {
	for _, u := range []string{
		"https://h/SECRET/webhooks/1/x",
		"https://discord.com/api/webhooks/SECRET",
		"https://discord.com/api/webhooks/123;SECRET/x",
		"https://discord.com/api/webhooks/123SECRET",
		"https://SECRET.m.pipedream.net/x",
		"https://u:SECRET@discord.com/api/webhooks/1/x",
	} {
		if got := maskWebhook(u); strings.Contains(got, "SECRET") {
			t.Errorf("maskWebhook(%q) = %q", u, got)
		}
	}
	if got := maskWebhook("https://discord.com/api/v10/webhooks/42/tok/slack"); got != "https://discord.com/api/v10/webhooks/42/••••••••" {
		t.Errorf("versioned Discord URL shown as %q", got)
	}
}

func TestDashboardStoppedBySignal(t *testing.T) {
	var d dashboard
	d.apply(logLines("INFO | Monitoring local /w")[0])
	d.apply(logLines("INFO | Stopped (terminated)")[0])
	if d.watcher != watcherStopped {
		t.Fatalf("watcher = %v, want stopped", d.watcher)
	}
}

func TestConfigProblemsDirection(t *testing.T) {
	m := configModel{keys: &keyCheckCache{}}
	cfg = Config{IP: "h", WatchSide: "Local", DestSide: "remote"}
	if _, ok := m.problems()["watch_side"]; !ok {
		t.Error("mis-cased watch_side not flagged")
	}
	cfg = Config{IP: "h", WatchSide: "remote", DestSide: "remote"}
	if _, ok := m.problems()["dest_side"]; !ok {
		t.Error("watch == dest not flagged")
	}
}
