package tui

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// action is one thing the UI does — a GoF Command: mek's own arguments, so
// the guard, the audit log and the confirmation prompts work as when typed.
// Interactive ones (shells, logins, k9s) get the terminal while the UI steps
// aside; the others run in the background and report in the status line.
type action struct {
	label       string   // what it does, for the status line: "start tunnel db (prod)"
	args        []string // mek's arguments
	interactive bool
	confirm     string  // asked first, then --yes is passed (protected contexts)
	then        *action // runs once this one succeeds
}

// doneMsg reports a finished action; out is the last line it printed.
type doneMsg struct {
	a   action
	out string
	err error
}

// Test seams.
var (
	execInTerminal = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd { return tea.Exec(pausing{c}, fn) }
	output         = func(c *exec.Cmd) ([]byte, error) { return c.CombinedOutput() }
	lookPath       = exec.LookPath
)

// start runs an action, asking first when it says so.
func (m *model) start(a action) tea.Cmd {
	if a.confirm != "" {
		m.overlay = &confirmScreen{a}
		return nil
	}
	return m.run(a, false)
}

func (m *model) run(a action, yes bool) tea.Cmd {
	args := a.args
	if yes {
		args = append([]string{"--yes"}, args...)
	}
	cmd := exec.Command(m.mek, args...)
	m.status = a.label + "…"
	if a.interactive {
		return execInTerminal(cmd, func(err error) tea.Msg { return doneMsg{a: a, err: err} })
	}
	return func() tea.Msg {
		out, err := output(cmd)
		return doneMsg{a: a, out: lastLine(string(out)), err: err}
	}
}

// done shows how an action went, and runs what comes after it.
func (m *model) done(d doneMsg) tea.Cmd {
	m.reload()
	if d.err != nil {
		m.status = red.Render("✗ " + d.a.label + ": " + cmp(d.out, d.err.Error()))
		return nil
	}
	m.status = green.Render("✓ ") + d.a.label
	if d.out != "" {
		m.status += dim.Render(" — " + d.out)
	}
	if d.a.then != nil {
		return m.run(*d.a.then, false)
	}
	return nil
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// pausing runs a command in the terminal; when it fails it waits for Enter,
// so its error stays on screen until it has been read.
type pausing struct{ *exec.Cmd }

func (p pausing) SetStdin(r io.Reader)  { p.Stdin = r }
func (p pausing) SetStdout(w io.Writer) { p.Stdout = w }
func (p pausing) SetStderr(w io.Writer) { p.Stderr = w }

func (p pausing) Run() error {
	err := p.Cmd.Run()
	if err != nil {
		fmt.Fprintf(p.Stderr, "\n%v — press Enter to return to mek ui", err)
		bufio.NewReader(p.Stdin).ReadString('\n')
	}
	return err
}
