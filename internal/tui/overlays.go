package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// confirmScreen asks before an action on a protected context; yes passes
// --yes, as typing it would.
type confirmScreen struct{ a action }

func (*confirmScreen) help() string { return "y yes · any other key no" }

func (c *confirmScreen) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	m.overlay = nil
	if k.String() == "y" {
		return m.run(c.a, true)
	}
	m.status = dim.Render("cancelled: " + c.a.label)
	return nil
}

func (c *confirmScreen) view(*model, int, int) string {
	return "\n  " + yellow.Render(c.a.confirm) + " [y/N]"
}

// typedScreen asks before a destructive action on a protected context:
// the context's name must be typed, as mek asks, and is passed as --confirm.
type typedScreen struct {
	a     action
	input string
}

func (*typedScreen) help() string { return "type the context's name · enter run · esc cancel" }

func (s *typedScreen) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		m.overlay = nil
		if s.input == s.a.typed {
			return m.run(s.a, true)
		}
		m.status = dim.Render("cancelled: " + s.a.label + " (the name didn't match)")
	case "esc":
		m.overlay = nil
		m.status = dim.Render("cancelled: " + s.a.label)
	case "backspace":
		if r := []rune(s.input); len(r) > 0 {
			s.input = string(r[:len(r)-1])
		}
	default:
		s.input += k.Text
	}
	return nil
}

func (s *typedScreen) view(*model, int, int) string {
	return "\n  " + red.Render(s.a.confirm) + "\n\n  Type " + bold.Render(s.a.typed) + " to run it: " + s.input + "▏"
}

// logsScreen is a background tunnel's log, following new lines unless the
// view is scrolled up.
type logsScreen struct {
	id, label string
	text      string // reloaded every refresh
	offset    int    // first line shown, when not following
	follow    bool
}

func (*logsScreen) help() string { return "↑↓ scroll · G follow · esc/q back" }

func (l *logsScreen) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc", "q":
		m.overlay = nil
	case "up", "k":
		l.follow, l.offset = false, max(0, l.offset-1)
	case "down", "j":
		l.offset++
	case "G", "end":
		l.follow = true
	}
	return nil
}

func (l *logsScreen) view(_ *model, w, h int) string {
	lines := strings.Split(strings.TrimRight(l.text, "\n"), "\n")
	fit := max(1, h-1)
	last := max(0, len(lines)-fit)
	if l.follow || l.offset > last {
		l.offset = last
	}
	state := "following"
	if !l.follow {
		state = "scrolled — G to follow"
	}
	out := []string{bold.Render("  log of "+l.label) + dim.Render("  "+state)}
	for _, line := range lines[l.offset:min(len(lines), l.offset+fit)] {
		out = append(out, ansi.Truncate("  "+line, w, "…"))
	}
	return strings.Join(out, "\n")
}

// searchScreen edits the Audit tab's search while showing its results.
type searchScreen struct{ t *auditTab }

func (*searchScreen) help() string { return "type to search · enter keep · esc clear" }

func (s *searchScreen) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		m.overlay = nil
	case "esc":
		s.t.query, m.overlay = "", nil
	case "backspace":
		if r := []rune(s.t.query); len(r) > 0 {
			s.t.query = string(r[:len(r)-1])
		}
	default:
		s.t.query += k.Text
	}
	s.t.l.cursor = 0
	return nil
}

func (s *searchScreen) view(m *model, w, h int) string {
	return s.t.view(m, w, h-1) + "\n" + bold.Render("/ ") + s.t.query + "▏"
}

// keysScreen lists every key; any key closes it.
type keysScreen struct{}

func (keysScreen) help() string { return "any key: back" }

func (keysScreen) key(m *model, _ tea.KeyPressMsg) tea.Cmd {
	m.overlay = nil
	return nil
}

func (keysScreen) view(m *model, _, _ int) string {
	out := []string{bold.Render("  Keys"), "", "  1-5, tab   switch tabs", "  ↑↓         move · g G first / last · pgup pgdown",
		"  r          refresh now", "  q          quit", ""}
	for _, t := range m.tabs {
		out = append(out, "  "+bold.Render(fmt.Sprintf("%-10s", t.title()))+dim.Render(t.help()))
	}
	return strings.Join(out, "\n")
}
