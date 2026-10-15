package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/tunnel"
	"github.com/prateep-r/mek/internal/ui"
)

func tags(c *Context) string {
	var t []string
	if c.Protected {
		t = append(t, red.Render("protected"))
	}
	if c.ReadOnly {
		t = append(t, yellow.Render("readonly"))
	}
	return strings.Join(t, " ")
}

// contextsTab lists the contexts; its cursor is the context the other tabs use.
type contextsTab struct{ l list }

func (*contextsTab) title() string { return "Contexts" }
func (*contextsTab) help() string {
	return "↑↓ choose · enter use (saved current) · l log in · a add · e edit · d remove"
}

func (t *contextsTab) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	t.l.cursor = m.index(m.selected)
	if t.l.key(k.String(), len(m.contexts)) {
		m.selected = m.contexts[t.l.cursor].Name
		return nil
	}
	if k.String() == "a" { // the first context too
		return m.start(action{label: "add a context", args: []string{"ctx", "add"}, interactive: true})
	}
	c := m.context(m.selected)
	if c == nil {
		return nil
	}
	switch k.String() {
	case "enter":
		return m.start(action{label: "use " + c.Name, args: []string{"use", c.Name}})
	case "l":
		return m.start(action{label: "log in to " + c.Name, args: []string{"login", c.Name}, interactive: true})
	case "e":
		return m.start(action{label: "edit " + c.Name, args: []string{"ctx", "edit", c.Name}, interactive: true})
	case "d":
		return m.start(action{label: "remove " + c.Name, args: []string{"ctx", "rm", c.Name, "--yes"},
			confirm: fmt.Sprintf("Remove context %s from its file?", c.Name)})
	}
	return nil
}

func (t *contextsTab) view(m *model, w, h int) string {
	t.l.cursor = m.index(m.selected)
	var rows [][]string
	for i := range m.contexts {
		c := &m.contexts[i]
		mark := " "
		if c.Name == m.current {
			mark = green.Render("*")
		}
		rows = append(rows, []string{mark, bold.Render(c.Name), c.Summary, tags(c)})
	}
	return t.l.render([]string{" ", "CONTEXT", "ACCOUNT", ""}, rows, w, h)
}

// index is the row of a context on the Contexts tab.
func (m *model) index(name string) int {
	for i, c := range m.contexts {
		if c.Name == name {
			return i
		}
	}
	return 0
}

// tunnelsTab lists every context's tunnels, and the named ones not running.
type tunnelsTab struct{ l list }

// tunnelRow is a tunnel mek knows of (info), or one in the config that isn't
// running (named).
type tunnelRow struct {
	info  *tunnel.Info
	ctx   *Context
	named *Tunnel
}

func (m *model) tunnelRows() []tunnelRow {
	var rows []tunnelRow
	running := map[string]bool{}
	for i := range m.tunnels {
		in := &m.tunnels[i]
		rows = append(rows, tunnelRow{info: in, ctx: m.context(in.Context)})
		if in.State.Alive() {
			running[in.Context+"/"+in.Name] = true
		}
	}
	for i := range m.contexts {
		c := &m.contexts[i]
		for j := range c.Tunnels {
			if !running[c.Name+"/"+c.Tunnels[j].Name] {
				rows = append(rows, tunnelRow{ctx: c, named: &c.Tunnels[j]})
			}
		}
	}
	return rows
}

func (*tunnelsTab) title() string { return "Tunnels" }
func (*tunnelsTab) help() string {
	return "enter start (named) / logs · x stop · l logs · a add (to the chosen context)"
}

func (t *tunnelsTab) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	if c := m.context(m.selected); c != nil && k.String() == "a" {
		return m.start(action{label: "add a tunnel to " + c.Name, args: []string{"-c", c.Name, "ctx", "tunnel", "add"}, interactive: true})
	}
	rows := m.tunnelRows()
	if len(rows) == 0 || t.l.key(k.String(), len(rows)) {
		return nil
	}
	t.l.clamp(len(rows))
	r := rows[t.l.cursor]
	switch k.String() {
	case "enter":
		if r.named != nil {
			return m.start(startTunnel(r.ctx, r.named.Name))
		}
		return m.logs(r.info)
	case "l":
		if r.info != nil {
			return m.logs(r.info)
		}
	case "x":
		if r.info != nil {
			return m.start(action{label: "stop tunnel " + r.info.Label(), args: []string{"tunnel", "stop", r.info.ID}})
		}
	}
	return nil
}

func startTunnel(c *Context, name string) action {
	a := action{label: fmt.Sprintf("start tunnel %s (%s)", name, c.Name), args: []string{"-c", c.Name, "tunnel", name, "-b"}}
	if c.Protected {
		a.confirm = fmt.Sprintf("%s is protected — start tunnel %s?", c.Name, name)
	}
	return a
}

// logs opens a background tunnel's log.
func (m *model) logs(in *tunnel.Info) tea.Cmd {
	if !in.Background {
		m.status = yellow.Render(in.Label() + " runs in the foreground: its output is in the terminal that started it")
		return nil
	}
	m.overlay = &logsScreen{id: in.ID, label: fmt.Sprintf("%s (%s)", in.Label(), in.Context), text: m.src.Log(in.ID), follow: true}
	return nil
}

func (t *tunnelsTab) view(m *model, w, h int) string {
	var rows [][]string
	for _, r := range m.tunnelRows() {
		if r.named != nil {
			rows = append(rows, []string{"-", r.ctx.Name, r.named.Name, fmt.Sprint(r.named.Local), dim.Render("not running"), "", r.named.Remote})
			continue
		}
		in := r.info
		state := in.State.Name()
		switch {
		case !in.State.Alive():
			state = yellow.Render(state)
		case in.State.Name() == "running":
			state = green.Render(state)
		}
		if !in.Background {
			state += " (foreground)"
		}
		if in.Restarts > 0 {
			state += fmt.Sprintf(" ↻%d", in.Restarts)
		}
		name := in.Name
		if name == "" {
			name = "-"
		}
		rows = append(rows, []string{in.ShortID(), in.Context, name, fmt.Sprint(in.Local), state, ui.Age(in.Started), in.Target})
	}
	return t.l.render([]string{"ID", "CONTEXT", "NAME", "LOCAL", "STATE", "AGE", "TARGET"}, rows, w, h)
}

// accessTab is the chosen context's shells and clusters.
type accessTab struct{ l list }

func (*accessTab) title() string { return "Access" }
func (*accessTab) help() string {
	return "enter shell / write kubeconfig · k k9s (clusters)"
}

// accessRows is the chosen context's targets, then its clusters.
func accessRows(c *Context) (rows [][]string, targets int) {
	for _, t := range c.Targets {
		rows = append(rows, []string{"shell", t.Name, t.Instance})
	}
	for _, cl := range c.Clusters {
		rows = append(rows, []string{"cluster", cl.Alias, cl.Name})
	}
	return rows, len(c.Targets)
}

func (t *accessTab) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	c := m.context(m.selected)
	if c == nil {
		return nil
	}
	rows, targets := accessRows(c)
	if len(rows) == 0 || t.l.key(k.String(), len(rows)) {
		return nil
	}
	t.l.clamp(len(rows))
	if t.l.cursor < targets {
		if k.String() != "enter" {
			return nil
		}
		name := c.Targets[t.l.cursor].Name
		return m.start(action{label: fmt.Sprintf("shell %s (%s)", name, c.Name), args: []string{"-c", c.Name, "shell", name}, interactive: true})
	}
	alias := c.Clusters[t.l.cursor-targets].Alias
	kube := action{label: fmt.Sprintf("kubeconfig %s (%s)", alias, c.Name), args: []string{"-c", c.Name, "kube", alias}}
	switch k.String() {
	case "enter":
		return m.start(kube)
	case "k":
		if _, err := lookPath("k9s"); err != nil {
			m.status = yellow.Render("k9s isn't installed — https://k9scli.io/topics/install/")
			return nil
		}
		kube.then = &action{label: fmt.Sprintf("k9s %s (%s)", alias, c.Name), interactive: true,
			args: []string{"-c", c.Name, "exec", "--", "k9s", "--context", c.Name + "/" + alias}}
		return m.start(kube)
	}
	return nil
}

func (t *accessTab) view(m *model, w, h int) string {
	c := m.context(m.selected)
	if c == nil {
		return dim.Render("  no contexts")
	}
	rows, _ := accessRows(c)
	head := "  " + bold.Render(c.Name) + " " + tags(c) + dim.Render("  (choose another on tab 1)")
	return head + "\n\n" + t.l.render([]string{"KIND", "NAME", "WHAT"}, rows, w, h-2)
}

// auditTab is the audit log, newest first, with filters.
type auditTab struct {
	l        list
	query    string // case-insensitive, over context, command and target
	problems bool   // blocked and declined only
	mine     bool   // the chosen context only
}

func (*auditTab) title() string { return "Audit" }
func (*auditTab) help() string {
	return "/ search · b blocked/declined only · c this context only · esc clear"
}

func (t *auditTab) entries(m *model) []audit.Entry {
	var out []audit.Entry
	q := strings.ToLower(t.query)
	for i := len(m.audit) - 1; i >= 0; i-- {
		e := m.audit[i]
		hay := strings.ToLower(e.Context + " " + strings.Join(e.Command, " ") + " " + e.Target)
		switch {
		case t.problems && e.Decision != "blocked" && e.Decision != "declined":
		case t.mine && e.Context != m.selected:
		case !strings.Contains(hay, q):
		default:
			out = append(out, e)
		}
	}
	return out
}

func (t *auditTab) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	if t.l.key(k.String(), len(t.entries(m))) {
		return nil
	}
	switch k.String() {
	case "/":
		m.overlay = &searchScreen{t}
	case "b":
		t.problems = !t.problems
	case "c":
		t.mine = !t.mine
	case "esc":
		t.query, t.problems, t.mine = "", false, false
	}
	return nil
}

func (t *auditTab) view(m *model, w, h int) string {
	var rows [][]string
	for _, e := range t.entries(m) {
		decision := e.Decision
		switch decision {
		case "blocked", "declined":
			decision = red.Render(decision)
		case "confirmed":
			decision = yellow.Render(decision)
		}
		class := e.Class
		if e.Event != "" {
			class += " " + e.Event
		}
		exit := fmt.Sprint(e.ExitCode)
		if e.Event == "start" {
			exit = "-"
		}
		rows = append(rows, []string{when(e.Time), e.Context, decision, class, exit, strings.Join(e.Command, " ")})
	}
	var filters []string
	if t.query != "" {
		filters = append(filters, fmt.Sprintf("%q", t.query))
	}
	if t.problems {
		filters = append(filters, "blocked/declined")
	}
	if t.mine {
		filters = append(filters, "context "+m.selected)
	}
	head := dim.Render(fmt.Sprintf("  %d of the last %d entries", len(rows), len(m.audit)))
	if len(filters) > 0 {
		head += dim.Render(" · ") + yellow.Render(strings.Join(filters, " · "))
	}
	return head + "\n\n" + t.l.render([]string{"TIME", "CONTEXT", "DECISION", "CLASS", "EXIT", "COMMAND"}, rows, w, h-2)
}

var now = time.Now // test seam

// when is a short time: today's as 15:04:05, older ones with the date.
func when(t time.Time) string {
	t = t.Local()
	if y, mo, d := now().Date(); t.Year() == y && t.Month() == mo && t.Day() == d {
		return t.Format("15:04:05")
	}
	return t.Format("Jan 02 15:04")
}
