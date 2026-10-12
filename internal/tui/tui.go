// Package tui is `mek ui`: contexts, tunnels, shells, clusters and the audit
// log in one terminal screen. It reads mek's files to show them, and runs
// mek itself for anything that acts (see action), so the guard, audit log
// and confirmations are the ones the commands already have.
package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/tunnel"
)

var (
	bold   = lipgloss.NewStyle().Bold(true)
	dim    = lipgloss.NewStyle().Faint(true)
	red    = lipgloss.NewStyle().Foreground(lipgloss.Red)
	green  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	active = lipgloss.NewStyle().Bold(true).Reverse(true)
)

// screen is what the UI is showing — a GoF State. Each tab and overlay
// handles its own keys and draws its own body, so the root needs no switch
// over what is on screen.
type screen interface {
	key(m *model, k tea.KeyPressMsg) tea.Cmd
	view(m *model, w, h int) string
	help() string
}

// tab is a screen with a place in the tab bar.
type tab interface {
	screen
	title() string
}

// auditSize is how many audit entries the Audit tab holds.
const auditSize = 500

type model struct {
	src     Source
	mek     string // this binary, run for every action
	width   int
	height  int
	tabs    []tab
	active  int
	overlay screen // logs, confirm, search, keys: takes every key while open

	contexts []Context
	current  string // the saved current context
	selected string // the context chosen on the Contexts tab
	tunnels  []tunnel.Info
	audit    []audit.Entry
	loadErr  error
	status   string
}

type tickMsg struct{}

// refreshEvery is how often tunnels and the audit log are read again.
var refreshEvery = time.Second

func tick() tea.Cmd { return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} }) }

func newModel(src Source, mek, selected string) *model {
	m := &model{src: src, mek: mek, selected: selected}
	m.tabs = []tab{&contextsTab{}, &tunnelsTab{}, &accessTab{}, &auditTab{}}
	m.reload()
	return m
}

// reload reads everything the UI shows. Reading files and probing tunnel
// locks is cheap enough to do every second.
func (m *model) reload() {
	m.contexts, m.current, m.loadErr = m.src.Contexts()
	if m.selected == "" || m.context(m.selected) == nil {
		m.selected = m.current
	}
	if m.context(m.selected) == nil && len(m.contexts) > 0 {
		m.selected = m.contexts[0].Name
	}
	m.tunnels = m.src.Tunnels()
	if es, err := m.src.Audit(auditSize); err == nil {
		m.audit = es
	}
	if l, ok := m.overlay.(*logsScreen); ok {
		l.text = m.src.Log(l.id)
	}
}

// context finds a context by name.
func (m *model) context(name string) *Context {
	for i := range m.contexts {
		if m.contexts[i].Name == name {
			return &m.contexts[i]
		}
	}
	return nil
}

func (m *model) Init() tea.Cmd { return tick() }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		m.reload()
		return m, tick()
	case doneMsg:
		return m, m.done(msg)
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *model) key(k tea.KeyPressMsg) tea.Cmd {
	if k.String() == "ctrl+c" {
		return tea.Quit
	}
	if m.overlay != nil {
		return m.overlay.key(m, k)
	}
	switch s := k.String(); s {
	case "q":
		return tea.Quit
	case "1", "2", "3", "4":
		m.active = int(s[0] - '1')
	case "tab":
		m.active = (m.active + 1) % len(m.tabs)
	case "shift+tab":
		m.active = (m.active + len(m.tabs) - 1) % len(m.tabs)
	case "?":
		m.overlay = keysScreen{}
	case "r":
		m.reload()
	default:
		return m.tabs[m.active].key(m, k)
	}
	return nil
}

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "mek ui"
	return v
}

// render lays out the tab bar, the body, the status line and the keys.
func (m *model) render() string {
	if m.width == 0 {
		return "" // until the terminal's size is known
	}
	var bar []string
	for i, t := range m.tabs {
		label := " " + string(rune('1'+i)) + " " + t.title() + " "
		if i == m.active {
			label = active.Render(label)
		}
		bar = append(bar, label)
	}
	s := screen(m.tabs[m.active])
	if m.overlay != nil {
		s = m.overlay
	}
	h := max(1, m.height-3)
	body := s.view(m, m.width, h)
	if m.loadErr != nil {
		body = red.Render("✗ " + m.loadErr.Error())
	}
	lines := strings.Split(body, "\n")
	lines = append(lines[:min(len(lines), h)], make([]string, max(0, h-len(lines)))...)
	keys := s.help()
	if m.overlay == nil {
		keys += " · 1-4/tab switch · ? keys · q quit"
	}
	out := append([]string{bold.Render("mek") + "  " + strings.Join(bar, " ")}, lines...)
	out = append(out, ansi.Truncate(m.status, m.width, "…"), dim.Render(ansi.Truncate(keys, m.width, "…")))
	return strings.Join(out, "\n")
}

// Run starts the UI; mek is this binary, selected the context to start on
// ("" for the current one).
func Run(src Source, mek, selected string) error {
	_, err := tea.NewProgram(newModel(src, mek, selected), programOptions...).Run()
	return err
}

var programOptions []tea.ProgramOption // test seam: input and output
