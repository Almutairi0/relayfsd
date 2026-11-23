package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	logFile        = "relayfsd.log"
	tailInterval   = 500 * time.Millisecond
	tailBacklog    = 256 << 10 // bytes read from the end of the log on start
	maxLogLines    = 2000
	maxTransfers   = 50
	logTimeLayout  = "2006/01/02 15:04:05"
	shownTransfers = 8
)

// ---- Log parsing ----

type logEntry struct {
	Time  time.Time // zero if the line has no timestamp
	Level string    // INFO, WARN, ERROR, FATAL or "" if unknown
	Msg   string
	Raw   string
}

// parseLogLine splits a line written by logger ("2006/01/02 15:04:05 INFO | msg").
func parseLogLine(line string) logEntry {
	e := logEntry{Raw: line, Msg: line}
	rest := line
	if len(line) >= len(logTimeLayout) {
		if t, err := time.ParseInLocation(logTimeLayout, line[:len(logTimeLayout)], time.Local); err == nil {
			e.Time = t
			rest = strings.TrimPrefix(line[len(logTimeLayout):], " ")
		}
	}
	e.Msg = rest
	if level, msg, ok := strings.Cut(rest, " | "); ok && isLogLevel(level) {
		e.Level, e.Msg = level, msg
	}
	return e
}

func isLogLevel(s string) bool {
	switch s {
	case "INFO", "WARN", "ERROR", "FATAL":
		return true
	}
	return false
}

// ---- Dashboard state built from log entries ----

type transferStatus int

const (
	transferQueued transferStatus = iota
	transferSyncing
	transferDone
	transferFailed
	transferRetrying // changed while uploading; a new transfer follows
)

type transfer struct {
	Name, Path, Side string
	Status           transferStatus
	Started, Ended   time.Time
	Err              string
	After            string // "moved", "deleted" or "kept (…)" for local uploads
}

func (t *transfer) active() bool {
	return t.Status == transferQueued || t.Status == transferSyncing
}

type connState int

const (
	connUnknown connState = iota
	connOnline
	connOffline
	connUntrusted
	connLoginFailed
)

// connFromError sets the server state from the reason a transfer failed.
// Errors that aren't about the connection (e.g. a missing remote folder)
// leave it as it is.
func (d *dashboard) connFromError(reason string) {
	switch {
	case strings.Contains(reason, "HOST KEY REJECTED"):
		d.conn, d.connDetail = connUntrusted, "Server identity changed: relayfsd refuses to connect. Check the server, then trust it in the Config tab."
	case strings.Contains(reason, "host key not trusted"):
		d.conn, d.connDetail = connUntrusted, "Server is not trusted yet: check its host key in the Config tab."
	case strings.Contains(reason, "SSH authentication failed"):
		d.conn, d.connDetail = connLoginFailed, "Login failed: "+strings.TrimPrefix(reason, "SSH authentication failed for ")
	case strings.Contains(reason, "SSH connection failed"):
		d.conn, d.connDetail = connOffline, strings.TrimPrefix(reason, "SSH connection failed: ")
	}
}

type watcherState int

const (
	watcherUnknown watcherState = iota
	watcherRunning
	watcherStopped
)

type dashboard struct {
	transfers    []*transfer
	synced       int
	failed       int
	conn         connState
	connDetail   string
	watcher      watcherState
	watcherSince time.Time
	lastActivity time.Time
	remote       bool // watching the remote side (it retries; local doesn't)
}

// apply updates the dashboard with one log entry. It matches the messages
// the daemon writes in sync.go, remote_watcher.go, after_upload.go and
// relayfsd.go.
func (d *dashboard) apply(e logEntry) {
	if !e.Time.IsZero() {
		d.lastActivity = e.Time
	}
	msg := e.Msg

	switch {
	case strings.HasPrefix(msg, "Monitoring"):
		// "Monitoring local <dir>" or "Monitoring remote: <dir>"
		d.remote = strings.HasPrefix(msg, "Monitoring remote")
		d.watcher = watcherRunning
		d.watcherSince = e.Time
		d.conn, d.connDetail = connUnknown, ""
		d.synced, d.failed = 0, 0 // the counts cover the current run
		// Transfers still open when the daemon restarted never finished.
		for _, t := range d.transfers {
			if t.active() {
				t.Status, t.Err, t.Ended = transferFailed, "interrupted by restart", e.Time
			}
		}

	case e.Level == "FATAL" || msg == "Done" || strings.HasPrefix(msg, "Stopped ("):
		d.watcher = watcherStopped
		d.watcherSince = e.Time
		// Only a FATAL line explains the stop; an old connection error
		// isn't the reason for a normal stop.
		d.connDetail = ""
		if e.Level == "FATAL" {
			d.connDetail = msg
		}

	case strings.HasPrefix(msg, "Found: "):
		path := strings.TrimPrefix(msg, "Found: ")
		side := ""
		if i := strings.LastIndex(path, " (on "); i >= 0 && strings.HasSuffix(path, ")") {
			side = path[i+len(" (on ") : len(path)-1]
			path = path[:i]
		}
		d.transfers = append(d.transfers, &transfer{
			Name: filepath.Base(path), Path: path, Side: side,
			Status: transferQueued, Started: e.Time,
		})
		if len(d.transfers) > maxTransfers {
			d.transfers = d.transfers[len(d.transfers)-maxTransfers:]
		}

	// "Now Syncing" and "Sync complete" only name the file, not its folder.
	// With two same-named files in flight, the oldest is matched first.
	case strings.HasPrefix(msg, "Now Syncing: "):
		if t := d.findOldest(strings.TrimPrefix(msg, "Now Syncing: "), transferQueued); t != nil {
			t.Status = transferSyncing
		}

	case strings.HasPrefix(msg, "Sync complete: "):
		name := strings.TrimPrefix(msg, "Sync complete: ")
		t := d.findOldest(name, transferSyncing)
		if t == nil {
			t = d.findOldest(name, transferQueued)
		}
		if t != nil {
			t.Status, t.Ended = transferDone, e.Time
		}
		d.synced++
		d.conn, d.connDetail = connOnline, ""

	case strings.HasPrefix(msg, "Sync failed for "):
		rest := strings.TrimPrefix(msg, "Sync failed for ")
		t := d.findActiveByPathPrefix(rest)
		if t == nil {
			t = d.fixMismatchedCompletion(rest)
		}
		reason := rest
		if t != nil {
			t.Status, t.Ended = transferFailed, e.Time
			t.Err = strings.TrimPrefix(rest[len(t.Path):], ": ")
			reason = t.Err
		}
		d.failed++
		// The local watcher has no other way to report that the server is
		// down or untrusted than its failed transfers.
		d.connFromError(reason)

	case e.Level == "ERROR" && (strings.Contains(msg, "HOST KEY REJECTED") || strings.Contains(msg, "host key not trusted")):
		// Logged once by reportHostKeyError when the problem starts,
		// e.g. "ssh: handshake failed: HOST KEY REJECTED …".
		d.connFromError(msg)

	case strings.HasPrefix(msg, "Retrying "):
		// "Retrying <path>: it changed during the upload". The upload
		// itself connected fine.
		d.conn, d.connDetail = connOnline, ""
		rest := strings.TrimPrefix(msg, "Retrying ")
		if t := d.findActiveByPathPrefix(rest); t != nil {
			t.Status, t.Ended = transferRetrying, e.Time
			t.Err = strings.TrimPrefix(rest[len(t.Path):], ": ") + ", will upload again"
		}

	case strings.HasPrefix(msg, "Unsupported direction: "):
		// sync.go gives up on the file it just started without naming it.
		for i := len(d.transfers) - 1; i >= 0; i-- {
			if t := d.transfers[i]; t.active() {
				t.Status, t.Ended = transferFailed, e.Time
				t.Err = "unsupported direction (" + strings.TrimPrefix(msg, "Unsupported direction: ") + ")"
				d.failed++
				break
			}
		}

	case strings.HasPrefix(msg, "Remote ") && strings.Contains(msg, " back online"):
		d.conn, d.connDetail = connOnline, ""

	case strings.HasPrefix(msg, "Remote ") && strings.Contains(msg, "refusing to connect"):
		d.conn, d.connDetail = connUntrusted, "Server identity could not be verified"
		if i, j := strings.Index(msg, "verified ("), strings.LastIndex(msg, "); retrying"); i >= 0 && j > i {
			d.connDetail += ": " + msg[i+len("verified ("):j]
		}

	case strings.HasPrefix(msg, "Remote ") && strings.Contains(msg, " offline ("):
		d.conn = connOffline
		if i, j := strings.Index(msg, " offline ("), strings.LastIndex(msg, "), retrying"); i >= 0 && j > i {
			d.connDetail = msg[i+len(" offline (") : j]
		}

	case strings.HasPrefix(msg, "Moved local file: "):
		src, _, _ := strings.Cut(strings.TrimPrefix(msg, "Moved local file: "), " -> ")
		d.annotate(src, "moved")

	case strings.HasPrefix(msg, "Deleted local file: "):
		d.annotate(strings.TrimPrefix(msg, "Deleted local file: "), "deleted")

	case strings.HasPrefix(msg, "Skipping "):
		// "Skipping <action> of <path>: <reason>"
		if _, rest, ok := strings.Cut(msg, " of "); ok {
			for i := len(d.transfers) - 1; i >= 0; i-- {
				if t := d.transfers[i]; strings.HasPrefix(rest, t.Path+":") {
					t.After = "kept (" + strings.TrimSpace(rest[len(t.Path)+1:]) + ")"
					break
				}
			}
		}
	}
}

// findOldest returns the oldest transfer called name with the status.
func (d *dashboard) findOldest(name string, status transferStatus) *transfer {
	for _, t := range d.transfers {
		if t.Name == name && t.Status == status {
			return t
		}
	}
	return nil
}

// fixMismatchedCompletion handles a failure for a file that an earlier
// "Sync complete" for the same name was credited to. The completion
// belonged to another, still unfinished, file of that name: move it there
// and return the failed transfer.
func (d *dashboard) fixMismatchedCompletion(s string) *transfer {
	for i := len(d.transfers) - 1; i >= 0; i-- {
		t := d.transfers[i]
		if t.Status != transferDone || !strings.HasPrefix(s, t.Path+": ") {
			continue
		}
		for _, other := range d.transfers {
			if other != t && other.Name == t.Name && other.active() {
				other.Status, other.Ended = transferDone, t.Ended
				return t
			}
		}
		return nil
	}
	return nil
}

// findActiveByPathPrefix finds the unfinished transfer whose path starts
// s; paths may contain ": ", so the error can't be split off blindly.
func (d *dashboard) findActiveByPathPrefix(s string) *transfer {
	for i := len(d.transfers) - 1; i >= 0; i-- {
		t := d.transfers[i]
		if t.active() && strings.HasPrefix(s, t.Path+": ") {
			return t
		}
	}
	return nil
}

func (d *dashboard) annotate(path, after string) {
	for i := len(d.transfers) - 1; i >= 0; i-- {
		if d.transfers[i].Path == path {
			d.transfers[i].After = after
			return
		}
	}
}

func (d *dashboard) inProgress() int {
	n := 0
	for _, t := range d.transfers {
		if t.active() {
			n++
		}
	}
	return n
}

// ---- Log tailing ----

type tailTickMsg struct{}

// tailMsg carries what was read from the log since the last tick.
type tailMsg struct {
	lines   []string
	offset  int64
	partial string
	head    string // the first bytes of the file, to notice it was rewritten
	file    os.FileInfo
	reset   bool       // the log was truncated or replaced; start over
	seed    *dashboard // built from the part of the log before lines
	missing error
}

const headSize = 64

func tailTick() tea.Cmd {
	return tea.Tick(tailInterval, func(time.Time) tea.Msg { return tailTickMsg{} })
}

// readLog reads new bytes from the log starting at offset. A first read
// (prev == nil) shows only the last tailBacklog bytes, but builds the
// dashboard from the whole file so a long-running daemon is still seen.
func readLog(path string, prev os.FileInfo, offset int64, partial, head string) tea.Cmd {
	return func() tea.Msg {
		info, err := os.Stat(path)
		if err != nil {
			return tailMsg{missing: err, offset: offset, partial: partial, head: head, file: prev}
		}
		msg := tailMsg{file: info, offset: offset, partial: partial, head: head}

		f, err := os.Open(path)
		if err != nil {
			msg.missing = err
			return msg
		}
		defer f.Close()

		// Truncated and written again since the last read? Then the size
		// alone may not show it, but the start of the file changed.
		buf := make([]byte, headSize)
		n, _ := io.ReadFull(f, buf)
		newHead := string(buf[:n])
		rewritten := len(newHead) < len(head) || newHead[:len(head)] != head
		if prev != nil && (!os.SameFile(prev, info) || info.Size() < offset || rewritten) {
			msg.reset, msg.offset, msg.partial = true, 0, ""
		}
		msg.head = newHead
		first := prev == nil || msg.reset

		if first && info.Size() > tailBacklog {
			// Start the backlog on a line boundary, so every line is seen
			// exactly once: before it by the seed, or after it as a line.
			msg.offset = info.Size() - tailBacklog
			if _, err := f.Seek(msg.offset, io.SeekStart); err == nil {
				r := bufio.NewReader(f)
				skipped, _ := r.ReadString('\n')
				msg.offset += int64(len(skipped))
			}
			seed := &dashboard{}
			if _, err := f.Seek(0, io.SeekStart); err == nil {
				sc := bufio.NewScanner(io.LimitReader(f, msg.offset))
				sc.Buffer(make([]byte, 64<<10), 1<<20)
				for sc.Scan() {
					seed.apply(parseLogLine(sc.Text()))
				}
			}
			msg.seed = seed
		}
		if info.Size() == msg.offset {
			return msg
		}

		if _, err := f.Seek(msg.offset, io.SeekStart); err != nil {
			msg.missing = err
			return msg
		}
		data, err := io.ReadAll(io.LimitReader(f, 4<<20))
		if err != nil {
			msg.missing = err
			return msg
		}
		msg.offset += int64(len(data))

		parts := strings.Split(msg.partial+string(data), "\n")
		msg.partial = parts[len(parts)-1]
		msg.lines = parts[:len(parts)-1]
		return msg
	}
}

// ---- Model ----

type statusModel struct {
	path    string
	file    os.FileInfo
	offset  int64
	partial string
	head    string
	missing error

	dash   dashboard
	lines  []logEntry
	log    viewport.Model
	follow bool
	width  int
	height int
}

func newStatusModel() statusModel {
	path, err := filepath.Abs(logFile)
	if err != nil {
		path = logFile
	}
	return statusModel{path: path, follow: true, log: viewport.New(80, 10)}
}

func (m statusModel) Init() tea.Cmd {
	return readLog(m.path, nil, 0, "", "")
}

func (m statusModel) Update(msg tea.Msg) (statusModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tailTickMsg:
		return m, readLog(m.path, m.file, m.offset, m.partial, m.head)

	case tailMsg:
		m.missing = msg.missing
		if msg.reset {
			m.dash, m.lines = dashboard{}, nil
		}
		if msg.seed != nil {
			m.dash = *msg.seed
		}
		m.file, m.offset, m.partial, m.head = msg.file, msg.offset, msg.partial, msg.head
		if len(msg.lines) > 0 {
			for _, l := range msg.lines {
				e := parseLogLine(l)
				m.dash.apply(e)
				m.lines = append(m.lines, e)
			}
			if len(m.lines) > maxLogLines {
				m.lines = m.lines[len(m.lines)-maxLogLines:]
			}
			m.refreshLog()
		} else if msg.reset {
			m.refreshLog()
		}
		return m, tailTick()

	case tea.KeyMsg:
		switch msg.String() {
		case "f":
			m.follow = !m.follow
			if m.follow {
				m.log.GotoBottom()
			}
			return m, nil
		case "g", "home":
			m.follow = false
			m.log.GotoTop()
			return m, nil
		case "G", "end":
			m.follow = true
			m.log.GotoBottom()
			return m, nil
		}
		var cmd tea.Cmd
		m.log, cmd = m.log.Update(msg)
		m.follow = m.log.AtBottom()
		return m, cmd
	}
	return m, nil
}

// fixed rows above the log: 3 for tiles, 1 hint, 1 title + transfers, 1 log title.
func (m *statusModel) layout() {
	used := m.tilesHeight() + 1 + 1 + shownTransfers + 1 + 1
	m.log.Width = max(m.width, 20)
	m.log.Height = max(m.height-used, 3)
	m.refreshLog()
}

func (m *statusModel) refreshLog() {
	var b strings.Builder
	for i, e := range m.lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(truncate(renderLogLine(e), m.log.Width))
	}
	m.log.SetContent(b.String())
	if m.follow {
		m.log.GotoBottom()
	}
}

func renderLogLine(e logEntry) string {
	ts := ""
	if !e.Time.IsZero() {
		ts = styleMuted.Render(e.Time.Format("01-02 15:04:05")) + " "
	}
	lvl := e.Level
	switch e.Level {
	case "INFO":
		lvl = styleOK.Render("INFO ")
	case "WARN":
		lvl = styleWarn.Render("WARN ")
	case "ERROR", "FATAL":
		lvl = styleErr.Render(fmt.Sprintf("%-5s", e.Level))
	default:
		return ts + e.Msg
	}
	return ts + lvl + " " + e.Msg
}

func (m statusModel) help() string {
	follow := "off"
	if m.follow {
		follow = "on"
	}
	return fmt.Sprintf("↑/↓ pgup/pgdn scroll log · f follow (%s) · g/G top/bottom", follow)
}

// Four boxed tiles need 64 columns; narrower terminals get one plain line.
const minTileWidth = 64

func (m *statusModel) tilesHeight() int {
	if m.width >= minTileWidth {
		return 4
	}
	return 1
}

func (m statusModel) View() string {
	d := m.dash
	tileW := (m.width - 8) / 4

	var conn string
	switch d.conn {
	case connOnline:
		conn = styleOK.Render("● Online")
	case connOffline:
		conn = styleWarn.Render("● Offline")
	case connUntrusted:
		conn = styleErr.Render("● Untrusted")
	case connLoginFailed:
		conn = styleErr.Render("● Login failed")
	default:
		conn = styleMuted.Render("○ Unknown")
	}
	var watcher string
	switch d.watcher {
	case watcherRunning:
		watcher = styleOK.Render("● Running")
	case watcherStopped:
		watcher = styleErr.Render("● Stopped")
	default:
		watcher = styleMuted.Render("○ Not seen")
	}
	synced := styleBold.Render(fmt.Sprint(d.synced))
	if n := d.inProgress(); n > 0 {
		synced += styleWarn.Render(fmt.Sprintf(" +%d active", n))
	}
	var tiles string
	if m.width >= minTileWidth {
		tile := func(label, value string) string {
			return styleBox.Width(tileW).Render(styleMuted.Render(label) + "\n" + truncate(value, tileW-2))
		}
		tiles = lipgloss.JoinHorizontal(lipgloss.Top,
			tile("Watcher", watcher),
			tile("Server", conn),
			tile("Synced this run", synced),
			tile("Failed this run", failedCount(d.failed)),
		)
	} else {
		tiles = truncate(watcher+"  "+conn+"  "+styleMuted.Render("synced ")+synced+"  "+styleMuted.Render("failed ")+failedCount(d.failed), m.width)
	}

	hint := styleMuted.Render("Log: " + m.path)
	switch {
	case m.missing != nil:
		hint = styleWarn.Render("No log at " + m.path + ". Run --ui from the daemon's working directory (~ for the systemd service).")
	case d.watcher != watcherRunning:
		hint = styleWarn.Render("Daemon not running? Start it with: systemctl --user start relayfsd")
		if d.connDetail != "" {
			hint = styleErr.Render("Stopped: "+d.connDetail) + styleMuted.Render(" · fix it, then: systemctl --user restart relayfsd")
		}
	case d.conn == connOffline:
		if d.remote {
			hint = styleWarn.Render("Server offline: " + d.connDetail + " · retrying")
		} else {
			hint = styleWarn.Render("Server offline: " + d.connDetail + " · failed uploads are not retried")
		}
	case d.connDetail != "":
		hint = styleErr.Render(d.connDetail)
	case !d.lastActivity.IsZero():
		hint = styleMuted.Render("Watcher started " + ago(d.watcherSince) + " · last activity " + ago(d.lastActivity) + " · " + m.path)
	}

	rows := []string{styleSection.MarginTop(0).Render("Recent transfers")}
	start := max(len(d.transfers)-shownTransfers, 0)
	if len(d.transfers) == 0 {
		rows = append(rows, styleMuted.Render("  Nothing transferred yet."))
	}
	// Show the folder for files whose names appear more than once.
	names := map[string]int{}
	for _, t := range d.transfers[start:] {
		names[t.Name]++
	}
	for i := len(d.transfers) - 1; i >= start; i-- {
		t := d.transfers[i]
		rows = append(rows, truncate(renderTransfer(t, names[t.Name] > 1), m.width))
	}
	for len(rows) < shownTransfers+1 {
		rows = append(rows, "")
	}

	logTitle := styleSection.MarginTop(0).Render("Log")
	if !m.follow {
		logTitle += styleMuted.Render("  (paused, press f to follow)")
	}

	return strings.Join([]string{
		tiles,
		truncate(hint, m.width),
		strings.Join(rows, "\n"),
		logTitle,
		m.log.View(),
	}, "\n")
}

func failedCount(n int) string {
	if n == 0 {
		return styleBold.Render("0")
	}
	return styleErr.Bold(true).Render(fmt.Sprint(n))
}

func renderTransfer(t *transfer, showDir bool) string {
	var icon string
	switch t.Status {
	case transferQueued:
		icon = styleMuted.Render("◌ queued ")
	case transferSyncing:
		icon = styleWarn.Render("⟳ syncing")
	case transferDone:
		icon = styleOK.Render("✓ synced ")
	case transferFailed:
		icon = styleErr.Render("✗ failed ")
	case transferRetrying:
		icon = styleWarn.Render("↻ retry  ")
	}
	dir := "↑"
	if t.Side == "remote" {
		dir = "↓"
	}
	when := "        "
	if !t.Started.IsZero() {
		when = t.Started.Format("15:04:05")
	}
	line := fmt.Sprintf("  %s  %s  %s %s", styleMuted.Render(when), icon, dir, t.Name)
	if showDir {
		line += styleMuted.Render(" in " + filepath.Dir(t.Path))
	}
	if t.Status == transferDone && !t.Ended.IsZero() && !t.Started.IsZero() {
		line += styleMuted.Render("  " + t.Ended.Sub(t.Started).Round(time.Second).String())
	}
	if t.After != "" {
		line += styleMuted.Render("  · " + t.After)
	}
	if t.Err != "" {
		errStyle := styleErr
		if t.Status == transferRetrying {
			errStyle = styleWarn
		}
		line += errStyle.Render("  " + t.Err)
	}
	return line
}

// ago formats t relative to now, e.g. "5m ago".
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("Jan 2")
	}
}
