package main

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/crypto/ssh"
)

// The Config tab edits the global cfg directly as a draft: the UI never
// runs a watcher, and the host key helpers read the server from cfg.
// saved holds what is in data.json, to tell whether there are changes.

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldSecret
	fieldChoice
	fieldAction
)

type cfgField struct {
	key         string
	section     string
	kind        fieldKind
	label       func(c *Config) string
	options     []string
	visible     func(c *Config) bool
	get         func(c *Config) string
	set         func(c *Config, v string)
	placeholder string
	display     func(v string) string // how a set secret is shown; default dots
}

func static(s string) func(*Config) string { return func(*Config) string { return s } }
func always(*Config) bool                  { return true }

// draftAfterUploadApplies is afterUploadApplies for a config being edited.
func draftAfterUploadApplies(c *Config) bool {
	return c.WatchSide != "remote" && c.DestSide != "local"
}

func configFields() []cfgField {
	return []cfgField{
		{key: "ip", section: "Server", kind: fieldText, label: static("Server IP"), visible: always,
			get: func(c *Config) string { return c.IP }, set: func(c *Config, v string) { c.IP = v },
			placeholder: "192.168.1.10"},
		{key: "username", section: "Server", kind: fieldText, label: static("SSH username"), visible: always,
			get: func(c *Config) string { return c.Username }, set: func(c *Config, v string) { c.Username = v }},
		{key: "hostkey", section: "Server", kind: fieldAction, label: static("Server identity"), visible: always},

		{key: "auth", section: "Login", kind: fieldChoice, label: static("Auth method"), visible: always,
			options: []string{"key", "password"},
			get:     func(c *Config) string { return c.AuthMethod }, set: func(c *Config, v string) { c.AuthMethod = v }},
		{key: "key_path", section: "Login", kind: fieldText, label: static("Private key"),
			visible: func(c *Config) bool { return c.AuthMethod == "key" },
			get:     func(c *Config) string { return c.KeyPath }, set: func(c *Config, v string) { c.KeyPath = v },
			placeholder: "~/.ssh/id_ed25519"},
		{key: "key_passphrase", section: "Login", kind: fieldSecret, label: static("Key passphrase"),
			visible: func(c *Config) bool { return c.AuthMethod == "key" },
			get:     func(c *Config) string { return c.KeyPassphrase }, set: func(c *Config, v string) { c.KeyPassphrase = v }},
		{key: "password", section: "Login", kind: fieldSecret, label: static("SSH password"),
			visible: func(c *Config) bool { return c.AuthMethod == "password" },
			get:     func(c *Config) string { return c.Password }, set: func(c *Config, v string) { c.Password = v }},

		{key: "watch_side", section: "Transfer", kind: fieldChoice, label: static("Watch for new files"), visible: always,
			options: []string{"local", "remote"},
			get:     func(c *Config) string { return c.WatchSide }, set: func(c *Config, v string) { c.WatchSide = v }},
		{key: "dest_side", section: "Transfer", kind: fieldChoice, label: static("Send them to"), visible: always,
			options: []string{"remote", "local"},
			get:     func(c *Config) string { return c.DestSide }, set: func(c *Config, v string) { c.DestSide = v }},
		{key: "watch_path", section: "Transfer", kind: fieldText, visible: always,
			label: func(c *Config) string {
				if c.WatchSide == "remote" {
					return "Local folder to save to"
				}
				return "Local folder to watch"
			},
			get: func(c *Config) string { return c.WatchPath }, set: func(c *Config, v string) { c.WatchPath = v },
			placeholder: "/home/user/torrents"},
		{key: "remote_dir", section: "Transfer", kind: fieldText, visible: always,
			label: func(c *Config) string {
				if c.WatchSide == "remote" {
					return "Remote folder to watch"
				}
				return "Remote folder to send to"
			},
			get: func(c *Config) string { return c.RemoteDir }, set: func(c *Config, v string) { c.RemoteDir = v },
			placeholder: "/home/user/downloads"},

		{key: "after_upload", section: "After upload", kind: fieldChoice, label: static("Local file"),
			visible: draftAfterUploadApplies,
			options: []string{afterUploadKeep, afterUploadMove, afterUploadDelete},
			get:     func(c *Config) string { return c.AfterUpload }, set: func(c *Config, v string) { c.AfterUpload = v }},
		{key: "move_dir", section: "After upload", kind: fieldText, label: static("Move to folder"),
			visible: func(c *Config) bool { return draftAfterUploadApplies(c) && c.AfterUpload == afterUploadMove },
			get:     func(c *Config) string { return c.MoveDir }, set: func(c *Config, v string) { c.MoveDir = v },
			placeholder: "outside the watch folder"},

		{key: "discord", section: "Notifications", kind: fieldChoice, label: static("Discord"), visible: always,
			options: []string{"off", "on"},
			get: func(c *Config) string {
				if c.Notifications.Discord.Enabled {
					return "on"
				}
				return "off"
			},
			set: func(c *Config, v string) { c.Notifications.Discord.Enabled = v == "on" }},
		{key: "webhook", section: "Notifications", kind: fieldSecret, display: maskWebhook, label: static("Webhook URL"),
			visible:     func(c *Config) bool { return c.Notifications.Discord.Enabled },
			get:         func(c *Config) string { return c.Notifications.Discord.WebhookURL },
			set:         func(c *Config, v string) { c.Notifications.Discord.WebhookURL = v },
			placeholder: "https://discord.com/api/webhooks/…"},
	}
}

// discordWebhook matches a well-formed Discord webhook path: the numeric
// id may be shown, the token after it is secret.
var discordWebhook = regexp.MustCompile(`^/api/(v\d+/)?webhooks/(\d+)/`)

// maskWebhook hides the secret parts of a webhook URL. For a Discord
// webhook (…/api/webhooks/<id>/<token>) the token and anything after it
// are hidden, so the id still tells webhooks apart. Any other URL can
// carry a secret anywhere, even in the host name, so only its scheme is
// shown.
func maskWebhook(raw string) string {
	const mask = "••••••••"
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return mask
	}
	host := strings.ToLower(u.Host)
	isDiscord := host == "discord.com" || host == "discordapp.com" ||
		strings.HasSuffix(host, ".discord.com") || strings.HasSuffix(host, ".discordapp.com")
	if isDiscord && u.User == nil {
		if m := discordWebhook.FindStringSubmatch(u.Path); m != nil {
			return u.Scheme + "://" + u.Host + m[0] + mask
		}
	}
	return u.Scheme + "://" + mask
}

// withDefaults fills in what the wizard would offer as defaults.
func withDefaults(c Config) Config {
	c.AuthMethod = defaultAuthMethod(c)
	if c.AuthMethod == "key" && c.KeyPath == "" {
		c.KeyPath = "~/.ssh/id_ed25519"
	}
	if c.WatchSide == "" {
		c.WatchSide = "local"
	}
	if c.DestSide == "" {
		c.DestSide = defaultDest(c.WatchSide)
	}
	if c.AfterUpload == "" && draftAfterUploadApplies(&c) {
		c.AfterUpload = afterUploadKeep
	}
	return c
}

// normalized drops settings that don't apply, like the wizard does when it
// writes a fresh config: switching to key auth removes the stored password.
func normalized(c Config) Config {
	if c.AuthMethod == "key" {
		c.Password = ""
	} else {
		c.KeyPath, c.KeyPassphrase = "", ""
	}
	if !draftAfterUploadApplies(&c) {
		c.AfterUpload, c.MoveDir = "", ""
	} else if c.AfterUpload != afterUploadMove {
		c.MoveDir = ""
	}
	if !c.Notifications.Discord.Enabled {
		c.Notifications.Discord.WebhookURL = ""
	}
	return c
}

// ---- Host key check ----

type hostKeyState int

const (
	hkIdle hostKeyState = iota
	hkChecking
	hkResult
	hkError
)

type hostKeyResultMsg struct {
	key    ssh.PublicKey
	status hostKeyStatus
	err    error
}

func checkHostKeyCmd() tea.Cmd {
	return func() tea.Msg {
		key, status, err := inspectHostKey()
		return hostKeyResultMsg{key, status, err}
	}
}

// ---- Model ----

type configModel struct {
	fields  []cfgField
	cursor  int
	editing bool
	input   textinput.Model
	saved   Config

	notice      string
	noticeStyle lipgloss.Style

	hk           hostKeyState
	hkKey        ssh.PublicKey
	hkReport     hostKeyReport
	hkErr        error
	hkFor        string // the IP the summary is about
	hkSummary    string
	hkInput      textinput.Model
	hkBackground bool // esc was pressed while checking
	spinner      spinner.Model

	confirmDelete bool
	deleteInput   textinput.Model

	// loadErr is set when data.json exists but couldn't be read. Saving
	// would replace it, so that takes a second press of s.
	loadErr        error
	overwriteArmed bool
	reloadArmed    bool

	keys *keyCheckCache

	width, height int
}

// keyCheckCache remembers the last private key check. Decrypting a key
// with a passphrase is slow on purpose (bcrypt), far too slow for every
// redraw. It is a pointer so it is shared by copies of the model.
type keyCheckCache struct {
	path, passphrase string
	file             keyFileState
	err              error
	valid            bool
}

// keyFileState tells whether the key file was created or replaced since
// the last check. Stat is cheap enough for every redraw.
type keyFileState struct {
	exists  bool
	size    int64
	modTime time.Time
}

func (c *keyCheckCache) check(path, passphrase string) error {
	var file keyFileState
	if info, err := os.Stat(expandHome(path)); err == nil {
		file = keyFileState{true, info.Size(), info.ModTime()}
	}
	if !c.valid || c.path != path || c.passphrase != passphrase || c.file != file {
		_, c.err = loadPrivateKey(path, passphrase)
		c.path, c.passphrase, c.file, c.valid = path, passphrase, file, true
	}
	return c.err
}

func newConfigModel() configModel {
	loaded, loadErr := loadConfigChecked()
	cfg = withDefaults(loaded)
	m := configModel{
		loadErr:     loadErr,
		keys:        &keyCheckCache{},
		fields:      configFields(),
		saved:       cfg,
		input:       textinput.New(),
		hkInput:     textinput.New(),
		deleteInput: textinput.New(),
		spinner:     spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(colAccent))),
	}
	return m
}

func (m configModel) Init() tea.Cmd { return nil }

func (m configModel) dirty() bool { return cfg != m.saved }

// capturesKeys reports whether keys must go to this tab, not the tab bar.
func (m configModel) capturesKeys() bool {
	return m.editing || m.confirmDelete || m.hkModal()
}

// hkModal reports whether the host key screen is shown. A check that was
// sent to the background with esc keeps running without it.
func (m configModel) hkModal() bool {
	return m.hk != hkIdle && !m.hkBackground
}

// clampCursor keeps the cursor on a visible field after fields hide.
func (m *configModel) clampCursor() {
	if n := len(m.visibleFields()); m.cursor >= n {
		m.cursor = max(n-1, 0)
	}
}

func (m configModel) visibleFields() []int {
	var idx []int
	for i, f := range m.fields {
		if f.visible(&cfg) {
			idx = append(idx, i)
		}
	}
	return idx
}

// current returns the selected field, keeping the cursor on a visible one.
func (m *configModel) current() *cfgField {
	vis := m.visibleFields()
	if m.cursor >= len(vis) {
		m.cursor = len(vis) - 1
	}
	return &m.fields[vis[m.cursor]]
}

func (m *configModel) setNotice(s string, style lipgloss.Style) {
	m.notice, m.noticeStyle = s, style
}

func (m configModel) Update(msg tea.Msg) (configModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(msg.Width-32, 20)
		return m, nil

	case spinner.TickMsg:
		if m.hk != hkChecking {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case hostKeyResultMsg:
		background := m.hkBackground
		m.hkBackground = false
		m.hkFor = cfg.IP
		if msg.err != nil {
			m.hk, m.hkErr = hkError, msg.err
			m.hkSummary = styleWarn.Render("⚠ could not check: ") + msg.err.Error()
			if background {
				m.hk = hkIdle
			}
			return m, nil
		}
		m.hk, m.hkKey, m.hkReport = hkResult, msg.key, hostKeyReportFor(msg.status, msg.key)
		switch {
		case msg.status == hostKeyTrusted:
			m.hkSummary = styleOK.Render("✓ trusted ") + styleMuted.Render(ssh.FingerprintSHA256(msg.key))
		case m.hkReport.Danger:
			m.hkSummary = styleErr.Render("✗ identity changed, will not connect")
		default:
			m.hkSummary = styleWarn.Render("not trusted yet") + styleMuted.Render(" · press enter to review")
		}
		if background {
			// Don't pop up over whatever the user is doing now.
			m.hk = hkIdle
			return m, nil
		}
		m.hkInput.SetValue("")
		if m.hkReport.NeedYes {
			return m, m.hkInput.Focus()
		}
		return m, nil

	case tea.KeyMsg:
		var cmd tea.Cmd
		switch {
		case m.confirmDelete:
			m, cmd = m.updateConfirmDelete(msg)
		case m.hkModal():
			m, cmd = m.updateHostKey(msg)
		case m.editing:
			m, cmd = m.updateEditing(msg)
		default:
			m, cmd = m.updateBrowsing(msg)
		}
		m.clampCursor()
		return m, cmd
	}
	return m, nil
}

func (m configModel) updateBrowsing(msg tea.KeyMsg) (configModel, tea.Cmd) {
	vis := m.visibleFields()
	f := m.current()
	key := msg.String()
	if key != "s" && key != "ctrl+s" {
		m.overwriteArmed = false
	}
	if key != "r" {
		m.reloadArmed = false
	}
	// The running check reads cfg, so it can't change under it.
	if m.hk == hkChecking {
		switch key {
		case "up", "k", "shift+tab", "down", "j":
		default:
			m.setNotice("Wait a few seconds for the host key check to finish.", styleWarn)
			return m, nil
		}
	}
	switch key {
	case "up", "k", "shift+tab":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(vis)-1 {
			m.cursor++
		}
	case "enter", " ":
		switch f.kind {
		case fieldText, fieldSecret:
			return m.startEditing(f)
		case fieldChoice:
			m.cycle(f, 1)
		case fieldAction:
			return m.startHostKeyCheck()
		}
	case "left", "h":
		if f.kind == fieldChoice {
			m.cycle(f, -1)
		}
	case "right", "l":
		if f.kind == fieldChoice {
			m.cycle(f, 1)
		}
	case "ctrl+s", "s":
		return m.save(false)
	case "r":
		if m.dirty() && !m.reloadArmed {
			m.reloadArmed = true
			m.setNotice("Discard your changes and reload data.json? Press r again.", styleWarn)
			return m, nil
		}
		m.reloadArmed = false
		loaded, err := loadConfigChecked()
		cfg = withDefaults(loaded)
		m.saved, m.loadErr, m.overwriteArmed = cfg, err, false
		m.setNotice("Reloaded data.json.", styleMuted)
	}
	return m, nil
}

func (m *configModel) cycle(f *cfgField, step int) {
	cur := f.get(&cfg)
	i := 0
	for j, o := range f.options {
		if o == cur {
			i = j
		}
	}
	i = (i + step + len(f.options)) % len(f.options)
	f.set(&cfg, f.options[i])
	// A switch can bring a field into view that the wizard defaults.
	if f.key == "auth" && cfg.AuthMethod == "key" && cfg.KeyPath == "" {
		cfg.KeyPath = "~/.ssh/id_ed25519"
	}
	if draftAfterUploadApplies(&cfg) && cfg.AfterUpload == "" {
		cfg.AfterUpload = afterUploadKeep
	}
	m.notice = ""
}

func (m configModel) startEditing(f *cfgField) (configModel, tea.Cmd) {
	m.editing = true
	m.input.Placeholder = f.placeholder
	m.input.EchoMode = textinput.EchoNormal
	m.input.SetValue(f.get(&cfg))
	if f.kind == fieldSecret {
		// Like the wizard: never show the secret, empty keeps it.
		m.input.EchoMode = textinput.EchoPassword
		m.input.SetValue("")
		if f.get(&cfg) != "" {
			m.input.Placeholder = "leave empty to keep the current one"
		} else {
			m.input.Placeholder = ""
		}
	}
	m.input.CursorEnd()
	m.notice = ""
	return m, m.input.Focus()
}

func (m configModel) updateEditing(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editing = false
		m.input.Blur()
		return m, nil
	case "enter", "tab", "down", "up":
		f := m.current()
		v := strings.TrimSpace(m.input.Value())
		if !(f.kind == fieldSecret && v == "") {
			f.set(&cfg, v)
		}
		m.editing = false
		m.input.Blur()
		// Enter moves on to the next field, like filling in a form.
		vis := m.visibleFields()
		switch msg.String() {
		case "up":
			m.cursor = max(m.cursor-1, 0)
		case "enter", "tab", "down":
			m.cursor = min(m.cursor+1, len(vis)-1)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m configModel) startHostKeyCheck() (configModel, tea.Cmd) {
	if strings.TrimSpace(cfg.IP) == "" {
		m.setNotice("Enter the server IP first.", styleWarn)
		return m, nil
	}
	m.hk = hkChecking
	return m, tea.Batch(m.spinner.Tick, checkHostKeyCmd())
}

func (m configModel) updateHostKey(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch m.hk {
	case hkChecking:
		// The dial can't be cancelled, but it times out after 10s; let
		// the user look around meanwhile.
		if msg.String() == "esc" || msg.String() == "q" {
			m.hkBackground = true
			m.setNotice("Still checking the host key in the background…", styleMuted)
		}
		return m, nil
	case hkError:
		m.hk = hkIdle
		return m, nil
	}

	r := m.hkReport
	if !r.CanTrust {
		if msg.String() == "esc" || msg.String() == "enter" || msg.String() == "q" {
			m.hk = hkIdle
		}
		return m, nil
	}

	trust := false
	if r.NeedYes {
		switch msg.String() {
		case "esc":
		case "enter":
			trust = strings.ToLower(strings.TrimSpace(m.hkInput.Value())) == "yes"
		default:
			var cmd tea.Cmd
			m.hkInput, cmd = m.hkInput.Update(msg)
			return m, cmd
		}
	} else {
		switch msg.String() {
		case "y", "Y":
			trust = true
		case "n", "N", "esc", "enter":
		default:
			return m, nil
		}
	}

	m.hk = hkIdle
	m.hkInput.Blur()
	if !trust {
		m.setNotice("Not trusted. Relayfsd won't connect until you trust the server.", styleWarn)
		return m, nil
	}
	if err := trustHostKey(m.hkKey); err != nil {
		m.setNotice("Could not save the key: "+err.Error(), styleErr)
		return m, nil
	}
	m.hkSummary = styleOK.Render("✓ trusted ") + styleMuted.Render(ssh.FingerprintSHA256(m.hkKey))
	m.setNotice("Saved to "+knownHostsPath(), styleOK)
	return m, nil
}

// problems lists what blocks saving, keyed by field.
func (m configModel) problems() map[string]string {
	p := map[string]string{}
	if strings.TrimSpace(cfg.IP) == "" {
		p["ip"] = "required"
	}
	// The daemon refuses to start with an invalid direction, e.g. a
	// mis-cased "Local" from a hand-edited data.json.
	if err := validateDirection(cfg.WatchSide, cfg.DestSide); err != nil {
		key := "dest_side"
		if strings.Contains(err.Error(), "watch_side") && !strings.Contains(err.Error(), "dest_side") {
			key = "watch_side"
		}
		p[key] = err.Error()
	}
	if draftAfterUploadApplies(&cfg) && cfg.AfterUpload == afterUploadMove {
		if err := validateMoveDir(cfg.MoveDir); err != nil {
			p["move_dir"] = err.Error()
		}
	}
	return p
}

// warnings lists what is saved anyway but won't work yet, keyed by field.
func (m configModel) warnings() map[string]string {
	w := map[string]string{}
	if cfg.AuthMethod == "key" {
		if err := m.keys.check(cfg.KeyPath, cfg.KeyPassphrase); err != nil {
			w["key_path"] = fmt.Sprintf("%v. Create one with ssh-keygen -t ed25519, then ssh-copy-id %s@%s", err, cfg.Username, cfg.IP)
		}
	} else if cfg.AuthMethod == "password" && cfg.Password == "" {
		w["password"] = "not set"
	}
	switch {
	case cfg.AuthMethod != "key" && cfg.AuthMethod != "password":
		w["auth"] = fmt.Sprintf("unknown auth_method %q", cfg.AuthMethod)
	}
	if strings.TrimSpace(cfg.WatchPath) == "" {
		w["watch_path"] = "required"
	}
	if strings.TrimSpace(cfg.RemoteDir) == "" {
		w["remote_dir"] = "required"
	}
	if cfg.Notifications.Discord.Enabled && cfg.Notifications.Discord.WebhookURL == "" {
		w["webhook"] = "required for notifications"
	}
	return w
}

func (m configModel) save(deleteConfirmed bool) (configModel, tea.Cmd) {
	if m.hk == hkChecking {
		m.setNotice("Wait a few seconds for the host key check to finish.", styleWarn)
		return m, nil
	}
	if p := m.problems(); len(p) > 0 {
		m.setNotice("Fix the fields marked ✗ before saving.", styleErr)
		return m, nil
	}
	if m.loadErr != nil && !m.overwriteArmed {
		m.overwriteArmed = true
		m.setNotice("data.json could not be read, so saving replaces it. Press s again to overwrite it.", styleErr)
		return m, nil
	}
	if !deleteConfirmed && draftAfterUploadApplies(&cfg) &&
		cfg.AfterUpload == afterUploadDelete && m.saved.AfterUpload != afterUploadDelete {
		m.confirmDelete = true
		m.deleteInput.SetValue("")
		return m, m.deleteInput.Focus()
	}

	cfg = normalized(cfg)
	if err := saveConfig(); err != nil {
		m.setNotice("Failed to save config: "+err.Error(), styleErr)
		return m, nil
	}
	cfg = withDefaults(cfg)
	m.saved, m.loadErr, m.overwriteArmed = cfg, nil, false
	m.setNotice("✓ Saved to data.json. Restart the daemon to apply: systemctl --user restart relayfsd", styleOK)
	return m, nil
}

func (m configModel) updateConfirmDelete(msg tea.KeyMsg) (configModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter":
		m.confirmDelete = false
		m.deleteInput.Blur()
		if msg.String() == "enter" && strings.ToLower(strings.TrimSpace(m.deleteInput.Value())) == "yes" {
			return m.save(true)
		}
		cfg.AfterUpload = m.saved.AfterUpload
		if cfg.AfterUpload == "" || cfg.AfterUpload == afterUploadDelete {
			cfg.AfterUpload = afterUploadKeep
		}
		m.setNotice("Not confirmed, after upload is back to \""+cfg.AfterUpload+"\". Nothing was saved.", styleWarn)
		return m, nil
	}
	var cmd tea.Cmd
	m.deleteInput, cmd = m.deleteInput.Update(msg)
	return m, cmd
}

func (m configModel) help() string {
	switch {
	case m.confirmDelete:
		return "type yes + enter to confirm · esc cancel"
	case m.hk == hkResult && m.hkReport.NeedYes:
		return "type yes + enter to trust · esc cancel"
	case m.hk == hkResult && m.hkReport.CanTrust:
		return "y trust · n don't trust"
	case m.hk == hkChecking && !m.hkBackground:
		return "esc continue in the background"
	case m.hkModal():
		return "esc close"
	case m.editing:
		return "enter save field · esc cancel"
	}
	return "↑/↓ move · enter edit · ←/→ change · s save · r reload"
}

// ---- View ----

func (m configModel) View() string {
	switch {
	case m.confirmDelete:
		return m.viewConfirmDelete()
	case m.hkModal():
		return m.viewHostKey()
	}

	problems, warnings := m.problems(), m.warnings()
	cursor := min(m.cursor, len(m.visibleFields())-1)
	labelW := 26
	vis := m.visibleFields()
	var lines []string
	cursorLine := 0
	section := ""
	for vi, i := range vis {
		f := &m.fields[i]
		if f.section != section {
			section = f.section
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, styleSection.MarginTop(0).Render(section))
		}

		selected := vi == cursor
		marker, label := "  ", f.label(&cfg)
		if selected {
			cursorLine = len(lines)
			marker = styleSelected.Render("› ")
			label = styleSelected.Render(label)
		}
		label = lipgloss.NewStyle().Width(labelW).Render(label)

		var value string
		switch {
		case selected && m.editing:
			value = m.input.View()
		case f.kind == fieldChoice:
			value = renderChoice(f.options, f.get(&cfg), selected)
		case f.kind == fieldSecret:
			if v := f.get(&cfg); v != "" && f.display != nil {
				value = f.display(v)
			} else if v != "" {
				value = "••••••••"
			} else {
				value = styleMuted.Render("(not set)")
			}
		case f.kind == fieldAction:
			value = m.identitySummary()
		default:
			value = f.get(&cfg)
			if value == "" {
				value = styleMuted.Render("(empty)")
			}
		}
		if changed := f.get != nil && f.get(&cfg) != f.get(&m.saved); changed {
			value += styleWarn.Render(" ●")
		}
		lines = append(lines, truncate(marker+label+value, m.width))

		indent := strings.Repeat(" ", labelW+2)
		if p, ok := problems[f.key]; ok {
			lines = append(lines, wrapIndented(styleErr, "✗ "+p, indent, m.width)...)
		} else if w, ok := warnings[f.key]; ok {
			lines = append(lines, wrapIndented(styleWarn, "⚠ "+w, indent, m.width)...)
		}
	}

	var top, bottom []string
	if m.loadErr != nil {
		top = append(wrapIndented(styleErr, "✗ "+m.loadErr.Error()+". Fields start empty; fix the file and press r, or save to replace it.", "", m.width), "")
	}
	if m.notice != "" {
		bottom = append([]string{""}, wrapIndented(m.noticeStyle, m.notice, "", m.width)...)
	}

	// Keep the selected row in view.
	h := max(m.height-len(top)-len(bottom), 3)
	if len(lines) > h {
		start := min(max(cursorLine-h/2, 0), len(lines)-h)
		lines = lines[start : start+h]
	}
	return strings.Join(append(append(top, lines...), bottom...), "\n")
}

// wrapIndented wraps text to width, starting every line with indent.
func wrapIndented(style lipgloss.Style, text, indent string, width int) []string {
	w := max(width-len(indent), 20)
	wrapped := style.Width(w).Render(text)
	lines := strings.Split(wrapped, "\n")
	for i, l := range lines {
		lines[i] = indent + strings.TrimRight(l, " ")
	}
	return lines
}

func renderChoice(options []string, current string, selected bool) string {
	parts := make([]string, len(options))
	for i, o := range options {
		if o == current {
			if selected {
				parts[i] = styleSelected.Render("‹ " + o + " ›")
			} else {
				parts[i] = styleBold.Render(o)
			}
		} else {
			parts[i] = styleMuted.Render(o)
		}
	}
	return strings.Join(parts, styleMuted.Render("  "))
}

func (m configModel) identitySummary() string {
	if m.hk == hkChecking {
		return m.spinner.View() + styleMuted.Render(" checking…")
	}
	if m.hkSummary != "" && m.hkFor == cfg.IP {
		return m.hkSummary
	}
	return styleMuted.Render("press enter to check the host key")
}

func (m configModel) viewHostKey() string {
	// Paths in the report can be long; wrap rather than cut them.
	return lipgloss.NewStyle().Width(max(m.width, 20)).Render(m.hostKeyText())
}

func (m configModel) hostKeyText() string {
	var b strings.Builder
	b.WriteString(styleSection.MarginTop(0).Render("Server identity: "+cfg.IP) + "\n\n")

	switch m.hk {
	case hkChecking:
		b.WriteString(m.spinner.View() + " Connecting to " + sshAddr() + " (no login is attempted)…\n")
		return b.String()
	case hkError:
		b.WriteString(styleWarn.Render("⚠ "+m.hkErr.Error()) + "\n\n")
		b.WriteString("The config can still be saved, but Relayfsd won't connect until\n")
		b.WriteString("the server is trusted. Check again once it's reachable.\n")
		return b.String()
	}

	r := m.hkReport
	if r.Title != "" {
		style := styleWarn
		if r.Danger {
			style = styleErr
		}
		b.WriteString(style.Bold(true).Render(r.Title) + "\n\n")
	}
	for _, l := range r.Lines {
		if strings.HasPrefix(l, "  ") {
			l = styleBold.Render(l)
		}
		b.WriteString(l + "\n")
	}
	if r.Danger {
		// The report tells wizard users to run --config again.
		b.WriteString(styleMuted.Render("(Then press enter on Server identity here to check again.)") + "\n")
	}
	b.WriteString("\n")
	switch {
	case r.NeedYes:
		b.WriteString("Type 'yes' to trust this key: " + m.hkInput.View() + "\n")
	case r.CanTrust:
		b.WriteString(styleBold.Render("Trust this server? (y/N)") + "\n")
	default:
		b.WriteString(styleMuted.Render("Press enter to go back.") + "\n")
	}
	return b.String()
}

func (m configModel) viewConfirmDelete() string {
	var b strings.Builder
	b.WriteString(styleErr.Bold(true).Render("⚠ Delete local files after upload?") + "\n\n")
	b.WriteString("Local files will be permanently deleted once their upload is verified.\n")
	b.WriteString("Files that change during upload are never deleted.\n\n")
	b.WriteString("Type 'yes' to confirm: " + m.deleteInput.View() + "\n")
	return b.String()
}
