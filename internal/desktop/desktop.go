// Package desktop is what mek-desktop's window calls: the same contexts,
// tunnels, audit log and resources as mek ui, read through internal/view
// and internal/catalog. Everything that acts runs mek itself, so the guard,
// the audit log and the confirmations are the ones typed commands get; the
// window asks the guard's question first, as mek ui does. It imports no GUI
// toolkit, so it is tested like any package (cmd/mek-desktop binds it).
package desktop

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/view"
)

// Service is bound to the window: each exported method is callable from it.
type Service struct {
	src view.Source
	mek string // the mek binary every action runs
}

// New is a Service over src that runs mek.
func New(src view.Source, mek string) *Service { return &Service{src: src, mek: mek} }

// Overview is the contexts and every tunnel: what the window shows first.
type Overview struct {
	Contexts []view.Context
	Current  string // the saved current context
	Tunnels  []Tunnel
	Error    string // the config doesn't load: why
}

// Tunnel is a tunnel mek knows of, or a named one in the config that isn't
// running (Named, with no ID).
type Tunnel struct {
	ID, Context, Name, State, Target string
	Local                            int
	Running, Background, Named       bool
	Restarts                         int
	Age                              string
}

// Overview reads the contexts and the tunnels.
func (s *Service) Overview() Overview {
	cs, current, err := s.src.Contexts()
	o := Overview{Contexts: cs, Current: current}
	if err != nil {
		o.Error = err.Error()
	}
	running := map[string]bool{}
	for _, in := range s.src.Tunnels() {
		t := Tunnel{ID: in.ID, Context: in.Context, Name: in.Name, State: in.State.Name(), Target: in.Target,
			Local: in.Local, Running: in.State.Alive(), Background: in.Background, Restarts: in.Restarts, Age: age(in.Started)}
		o.Tunnels = append(o.Tunnels, t)
		if t.Running {
			running[in.Context+"/"+in.Name] = true
		}
	}
	for _, c := range cs {
		for _, t := range c.Tunnels {
			if !running[c.Name+"/"+t.Name] {
				o.Tunnels = append(o.Tunnels, Tunnel{Context: c.Name, Name: t.Name, State: "not running", Target: t.Remote, Local: t.Local, Named: true})
			}
		}
	}
	return o
}

var now = time.Now // test seam

// age is how long ago, short: 45s, 12m, 3h, 2d.
func age(t time.Time) string {
	d := now().Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Audit is the last n audit entries, newest first.
func (s *Service) Audit(n int) ([]audit.Entry, error) {
	es, err := s.src.Audit(n)
	slices.Reverse(es)
	return es, err
}

// TunnelLog is a background tunnel's log.
func (s *Service) TunnelLog(id string) string { return s.src.Log(id) }

// Plan is a mek command and what must be asked before it runs.
type Plan struct {
	Label string   // what it does: "stop tunnel db (prod)"
	Args  []string // mek's arguments
	Ask   string   // "": just run it; "yes": yes or no; "typed": the context's name typed
	Typed string   // with Ask "typed": what must be typed
	Text  string   // the question
}

// Result is how a command went: its last line of output, or why it failed.
type Result struct {
	OK     bool
	Output string
}

// Run runs a plan once its question, if any, is answered: answer is "yes",
// or the typed name. A wrong answer runs nothing.
func (s *Service) Run(p Plan, answer string) Result {
	args := p.Args
	switch p.Ask {
	case "yes":
		if answer != "yes" {
			return Result{Output: "cancelled: " + p.Label}
		}
		args = append([]string{"--yes"}, args...)
	case "typed":
		if answer != p.Typed {
			return Result{Output: "cancelled: " + p.Label + " (the name didn't match)"}
		}
		args = append([]string{"--confirm", p.Typed}, args...)
	}
	out, err := run(s.mek, args)
	if err != nil {
		return Result{Output: err.Error()}
	}
	return Result{OK: true, Output: lastLine(string(out))}
}

// UseContext makes a context the saved current one.
func (s *Service) UseContext(name string) Result {
	return s.Run(Plan{Label: "use " + name, Args: []string{"use", name}}, "")
}

// StartTunnel plans starting a named tunnel in the background; a protected
// context asks first.
func (s *Service) StartTunnel(ctx, name string) Plan {
	p := Plan{Label: fmt.Sprintf("start tunnel %s (%s)", name, ctx), Args: []string{"-c", ctx, "tunnel", name, "-b"}}
	if s.protected(ctx) {
		p.Ask, p.Text = "yes", fmt.Sprintf("%s is protected — start tunnel %s?", ctx, name)
	}
	return p
}

// StopTunnel stops a running tunnel.
func (s *Service) StopTunnel(id string) Result {
	return s.Run(Plan{Label: "stop tunnel " + id, Args: []string{"tunnel", "stop", id}}, "")
}

// WriteKubeconfig writes the context's kubeconfig, with alias current.
func (s *Service) WriteKubeconfig(ctx, alias string) Result {
	return s.Run(Plan{Label: fmt.Sprintf("kubeconfig %s (%s)", alias, ctx), Args: []string{"-c", ctx, "kube", alias}}, "")
}

// RemoveContext plans removing a context from its file.
func (s *Service) RemoveContext(name string) Plan {
	return Plan{Label: "remove " + name, Args: []string{"ctx", "rm", name, "--yes"}, Ask: "yes",
		Text: fmt.Sprintf("Remove context %s from its file?", name)}
}

func (s *Service) context(name string) *view.Context {
	cs, _, _ := s.src.Contexts()
	for i := range cs {
		if cs[i].Name == name {
			return &cs[i]
		}
	}
	return nil
}

func (s *Service) protected(name string) bool {
	c := s.context(name)
	return c != nil && c.Protected
}

// In a terminal: what needs one — logging in, shells, k9s, the wizard.

// Login opens mek login for a context in a terminal.
func (s *Service) Login(ctx string) error { return s.terminal("login", ctx) }

// Shell opens a shell on a target in a terminal.
func (s *Service) Shell(ctx, target string) error { return s.terminal("-c", ctx, "shell", target) }

// K9s opens k9s on a cluster in a terminal.
func (s *Service) K9s(ctx, alias string) error {
	return s.terminal("-c", ctx, "exec", "--", "k9s", "--context", ctx+"/"+alias)
}

// AddContext opens the wizard for a new context in a terminal.
func (s *Service) AddContext() error { return s.terminal("ctx", "add") }

// EditContext opens the wizard on a context in a terminal.
func (s *Service) EditContext(name string) error { return s.terminal("ctx", "edit", name) }

// AddEntry opens the wizard for a context's tunnel, target or cluster.
func (s *Service) AddEntry(ctx, kind string) error {
	switch kind {
	case "tunnel", "target", "cluster":
		return s.terminal("-c", ctx, "ctx", kind, "add")
	}
	return fmt.Errorf("no such entry: %q", kind)
}

func (s *Service) terminal(args ...string) error {
	line := []string{shellQuote(s.mek)}
	for _, a := range args {
		line = append(line, shellQuote(a))
	}
	return openTerminal(strings.Join(line, " "))
}

var plain = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if plain.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Test seams: running mek, a terminal, the login shell.
var (
	run = func(mek string, args []string) ([]byte, error) {
		c := exec.Command(mek, args...)
		c.Env = append(os.Environ(), "MEK_SHOW_CMD=0", "CLOUDSDK_CORE_DISABLE_PROMPTS=1", "AWS_PAGER=")
		var stderr bytes.Buffer
		c.Stderr = &stderr
		out, err := c.Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out, errors.New(reason(stderr.String(), err))
		}
		return out, err
	}
	openTerminal = terminalCommand
	goos         = goOS
)

var errorLine = regexp.MustCompile(`(?i)\berror\b`)

// reason is why a run failed, from what it printed: its first line that
// says error (gcloud explains below it), else its last.
func reason(stderr string, err error) string {
	for _, l := range strings.Split(stderr, "\n") {
		if errorLine.MatchString(l) {
			return strings.TrimSpace(l)
		}
	}
	if l := lastLine(stderr); l != "" {
		return l
	}
	return err.Error()
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// terminals are what opens a command in a terminal on Linux, in order.
var terminals = [][]string{
	{"x-terminal-emulator", "-e"}, {"gnome-terminal", "--"}, {"konsole", "-e"}, {"xfce4-terminal", "-x"}, {"xterm", "-e"},
}

var (
	lookPath = exec.LookPath
	start    = func(c *exec.Cmd) error { return c.Start() }
)

// terminalCommand opens line in the system's terminal: Terminal on macOS,
// the first terminal found on Linux. It returns once the terminal starts.
func terminalCommand(line string) error {
	if goos() == "darwin" {
		script := fmt.Sprintf(`tell application "Terminal" to do script %q`, line)
		return start(exec.Command("osascript", "-e", script, "-e", `tell application "Terminal" to activate`))
	}
	for _, t := range terminals {
		if p, err := lookPath(t[0]); err == nil {
			return start(exec.Command(p, append(t[1:], "sh", "-c", line+`; printf '\n(press enter to close)'; read _`)...))
		}
	}
	return errors.New("no terminal found (x-terminal-emulator, gnome-terminal, konsole, xfce4-terminal, xterm): run it yourself: " + line)
}

// FindMek is the mek a desktop app runs: next to it (the app bundles one),
// else on PATH.
func FindMek(self string) (string, error) {
	sibling := filepath.Join(filepath.Dir(self), "mek")
	if fi, err := os.Stat(sibling); err == nil && !fi.IsDir() {
		return sibling, nil
	}
	p, err := lookPath("mek")
	if err != nil {
		return "", errors.New("mek isn't installed: brew install prateep-r/tap/mek")
	}
	return p, nil
}

// loginPath is the PATH the user's login shell sets (test seam). An app
// started from the Dock or a menu gets a bare PATH, without Homebrew, asdf
// or ~/.local/bin, so the cloud CLIs wouldn't be found.
var loginPath = func(shell string) (string, error) {
	out, err := exec.Command(shell, "-l", "-c", `printf %s "$PATH"`).Output()
	return strings.TrimSpace(string(out)), err
}

// UseLoginPath sets PATH to the login shell's ($SHELL, else /bin/sh) when
// it has one; on failure PATH stays as it is.
func UseLoginPath() {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	if p, err := loginPath(shell); err == nil && p != "" {
		os.Setenv("PATH", p)
	}
}

func goOS() string { return runtime.GOOS }
