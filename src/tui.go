package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The terminal UI (relayfsd --ui) has two tabs: Status, which follows
// relayfsd.log written by the running daemon, and Config, which edits
// data.json. It never runs a watcher itself, so it can't upload a file
// twice next to the systemd service.

// Shared styles. Adaptive colors keep it readable on light and dark terminals.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "#5A3FC0", Dark: "#A78BFA"}
	colOK     = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#4ADE80"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#FACC15"}
	colErr    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F87171"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#8B949E"}
	colBorder = lipgloss.AdaptiveColor{Light: "#D0D7DE", Dark: "#3F444C"}

	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleMuted    = lipgloss.NewStyle().Foreground(colMuted)
	styleOK       = lipgloss.NewStyle().Foreground(colOK)
	styleWarn     = lipgloss.NewStyle().Foreground(colWarn)
	styleErr      = lipgloss.NewStyle().Foreground(colErr)
	styleBold     = lipgloss.NewStyle().Bold(true)
	styleSection  = lipgloss.NewStyle().Bold(true).Foreground(colAccent).MarginTop(1)
	styleTabOn    = lipgloss.NewStyle().Bold(true).Foreground(colAccent).Underline(true).Padding(0, 1)
	styleTabOff   = lipgloss.NewStyle().Foreground(colMuted).Padding(0, 1)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1)
)

type tuiTab int

const (
	tabStatus tuiTab = iota
	tabConfig
)

type rootModel struct {
	tab         tuiTab
	status      statusModel
	config      configModel
	width       int
	height      int
	confirmQuit bool
}

func runTUI() {
	// Without a logger, warnOnce prints to stdout and would draw over
	// the screen. Append to the daemon's log if it exists, but don't
	// create it: a missing log tells the Status tab it's in the wrong folder.
	logger = log.New(io.Discard, "", log.LstdFlags)
	if f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0); err == nil {
		defer f.Close()
		logger = log.New(f, "", log.LstdFlags)
	}

	m := rootModel{
		status: newStatusModel(),
		config: newConfigModel(),
	}
	// Nothing to show on the dashboard before the first setup.
	if _, err := os.Stat(configFile); err != nil {
		m.tab = tabConfig
	}

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Println("❌", err)
		os.Exit(1)
	}
}

func (m rootModel) Init() tea.Cmd {
	return tea.Batch(m.status.Init(), m.config.Init())
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		body := tea.WindowSizeMsg{Width: msg.Width, Height: m.bodyHeight()}
		var c1, c2 tea.Cmd
		m.status, c1 = m.status.Update(body)
		m.config, c2 = m.config.Update(body)
		return m, tea.Batch(c1, c2)

	case tea.KeyMsg:
		if m.confirmQuit {
			switch msg.String() {
			case "y", "Y", "ctrl+c":
				return m, tea.Quit
			default:
				m.confirmQuit = false
				return m, nil
			}
		}
		if msg.String() == "ctrl+c" {
			return m.quit()
		}
		// While a config field or prompt has the keyboard, every other key
		// belongs to it.
		if !(m.tab == tabConfig && m.config.capturesKeys()) {
			switch msg.String() {
			case "q":
				return m.quit()
			case "tab":
				m.tab = (m.tab + 1) % 2
				return m, nil
			case "1":
				m.tab = tabStatus
				return m, nil
			case "2":
				m.tab = tabConfig
				return m, nil
			}
		}
		var cmd tea.Cmd
		if m.tab == tabStatus {
			m.status, cmd = m.status.Update(msg)
		} else {
			m.config, cmd = m.config.Update(msg)
		}
		return m, cmd

	}

	// Everything else (log ticks, spinner, host key results) goes to both;
	// each tab ignores what isn't its own.
	var c1, c2 tea.Cmd
	m.status, c1 = m.status.Update(msg)
	m.config, c2 = m.config.Update(msg)
	return m, tea.Batch(c1, c2)
}

func (m rootModel) quit() (tea.Model, tea.Cmd) {
	if m.config.dirty() {
		m.confirmQuit = true
		return m, nil
	}
	return m, tea.Quit
}

// bodyHeight is the space left for a tab below the header and above the footer.
func (m rootModel) bodyHeight() int {
	return max(m.height-4, 5)
}

func (m rootModel) View() string {
	if m.width == 0 {
		return ""
	}

	saved := m.config.saved
	server := styleMuted.Render("not configured")
	if saved.IP != "" {
		user := saved.Username
		if user != "" {
			user += "@"
		}
		server = fmt.Sprintf("%s%s  %s", user, saved.IP, styleMuted.Render(directionLabel(saved)))
	}
	header := styleTitle.Render("relayfsd") + styleMuted.Render(" · ") + server

	tabs := []string{"1 Status", "2 Config"}
	for i, t := range tabs {
		if tuiTab(i) == m.tab {
			tabs[i] = styleTabOn.Render(t)
		} else {
			tabs[i] = styleTabOff.Render(t)
		}
	}
	if m.config.dirty() {
		tabs[1] += styleWarn.Render("●")
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	var body, help string
	if m.tab == tabStatus {
		body, help = m.status.View(), m.status.help()
	} else {
		body, help = m.config.View(), m.config.help()
	}
	body = lipgloss.NewStyle().Height(m.bodyHeight()).MaxHeight(m.bodyHeight()).Render(body)

	footer := styleMuted.Render(help)
	if m.confirmQuit {
		footer = styleWarn.Render("Unsaved config changes. Quit anyway? (y/N)")
	} else if !(m.tab == tabConfig && m.config.capturesKeys()) {
		footer = styleMuted.Render("tab switch · q quit · ") + footer
	}

	return strings.Join([]string{header, tabBar, body, truncate(footer, m.width)}, "\n")
}

// directionLabel describes the sync direction of c, e.g. "local → remote:/srv".
func directionLabel(c Config) string {
	watch, dest := c.WatchSide, c.DestSide
	if watch == "" {
		watch = "local"
	}
	if dest == "" {
		dest = defaultDest(watch)
	}
	if watch == "remote" {
		return fmt.Sprintf("remote:%s → local:%s", c.RemoteDir, c.WatchPath)
	}
	if dest == "local" {
		return fmt.Sprintf("local:%s → local", c.WatchPath)
	}
	return fmt.Sprintf("local:%s → remote:%s", c.WatchPath, c.RemoteDir)
}

// truncate shortens s (which may contain colors) to at most w cells.
func truncate(s string, w int) string {
	if w <= 0 {
		return s
	}
	return ansi.Truncate(s, w, "…")
}
