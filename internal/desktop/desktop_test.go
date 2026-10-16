package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/tunnel"
	"github.com/prateep-r/mek/internal/view"
)

type fakeSource struct {
	contexts []view.Context
	current  string
	err      error
	tunnels  []tunnel.Info
	audit    []audit.Entry
}

func (f *fakeSource) Contexts() ([]view.Context, string, error) { return f.contexts, f.current, f.err }
func (f *fakeSource) Tunnels() []tunnel.Info                    { return f.tunnels }
func (f *fakeSource) Audit(int) ([]audit.Entry, error)          { return slices.Clone(f.audit), nil }
func (f *fakeSource) Log(id string) string                      { return "log of " + id }

type state struct {
	name  string
	alive bool
}

func (s state) Name() string           { return s.name }
func (s state) Alive() bool            { return s.alive }
func (state) Stop(tunnel.Record) error { return nil }

func sample() *fakeSource {
	return &fakeSource{
		current: "dev",
		contexts: []view.Context{
			{Name: "dev", Cloud: "aws", Tunnels: []view.Tunnel{{Name: "db", Remote: "db:5432 via web", Local: 15432}, {Name: "app", Remote: "port 80 on web", Local: 10080}}},
			{Name: "prod", Cloud: "aws", Protected: true},
			{Name: "ro", Cloud: "gcp", ReadOnly: true},
			{Name: "az", Cloud: "azure"},
		},
	}
}

// ran records what mek was run with, and answers.
type ran struct {
	args [][]string
	out  string
	err  error
}

func fakeRun(t *testing.T) *ran {
	r := &ran{}
	old := run
	t.Cleanup(func() { run = old })
	run = func(mek string, args []string) ([]byte, error) {
		if mek != "/bin/mek" {
			t.Errorf("ran %s", mek)
		}
		r.args = append(r.args, args)
		return []byte(r.out), r.err
	}
	return r
}

func (r *ran) last() string { return strings.Join(r.args[len(r.args)-1], " ") }

func TestOverview(t *testing.T) {
	old := now
	t.Cleanup(func() { now = old })
	base := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return base }
	src := sample()
	src.tunnels = []tunnel.Info{
		{Record: tunnel.Record{ID: "16f8", Context: "dev", Name: "db", Local: 15432, Target: "t", Background: true, Started: base.Add(-3 * time.Hour), Restarts: 2}, State: state{"running", true}},
		{Record: tunnel.Record{ID: "3b1c", Context: "prod", Local: 18080, Started: base.Add(-30 * time.Second)}, State: state{"exited (1)", false}},
	}
	s := New(src, "/bin/mek")
	o := s.Overview()
	if o.Current != "dev" || len(o.Contexts) != 4 || o.Error != "" || len(o.Tunnels) != 3 {
		t.Fatalf("overview: %+v", o)
	}
	if got := o.Tunnels[0]; got.ID != "16f8" || !got.Running || got.Restarts != 2 || got.Age != "3h" || !got.Background {
		t.Errorf("running: %+v", got)
	}
	if got := o.Tunnels[1]; got.Running || got.Age != "30s" {
		t.Errorf("exited: %+v", got)
	}
	if got := o.Tunnels[2]; !got.Named || got.Name != "app" || got.State != "not running" || got.Local != 10080 {
		t.Errorf("named: %+v", got)
	}
	for d, want := range map[time.Duration]string{12 * time.Minute: "12m", 72 * time.Hour: "3d"} {
		if got := age(base.Add(-d)); got != want {
			t.Errorf("age(%v) = %s", d, got)
		}
	}

	src.err = errors.New("config.yaml: line 3: unknown field")
	if o := s.Overview(); o.Error != src.err.Error() {
		t.Errorf("error: %+v", o)
	}
	src.audit = []audit.Entry{{Context: "a"}, {Context: "b"}}
	if es, _ := s.Audit(10); es[0].Context != "b" {
		t.Errorf("audit, newest first: %+v", es)
	}
	if s.TunnelLog("16f8") != "log of 16f8" {
		t.Error("log")
	}
}

func TestActions(t *testing.T) {
	r := fakeRun(t)
	s := New(sample(), "/bin/mek")
	r.out = "✓ switched to prod\n"
	if res := s.UseContext("prod"); !res.OK || res.Output != "✓ switched to prod" || r.last() != "use prod" {
		t.Errorf("use: %+v %s", res, r.last())
	}
	if p := s.StartTunnel("dev", "db"); p.Ask != "" || strings.Join(p.Args, " ") != "-c dev tunnel db -b" {
		t.Errorf("start: %+v", p)
	}
	p := s.StartTunnel("prod", "db")
	if p.Ask != "yes" || p.Text != "prod is protected — start tunnel db?" {
		t.Errorf("protected start: %+v", p)
	}
	if res := s.Run(p, "no"); res.OK || res.Output != "cancelled: start tunnel db (prod)" || len(r.args) != 1 {
		t.Errorf("declined: %+v", res)
	}
	if res := s.Run(p, "yes"); !res.OK || r.last() != "--yes -c prod tunnel db -b" {
		t.Errorf("confirmed: %+v %s", res, r.last())
	}
	typed := Plan{Label: "x", Args: []string{"-c", "prod", "aws", "ec2", "terminate-instances"}, Ask: "typed", Typed: "prod"}
	if res := s.Run(typed, "pro"); res.OK || !strings.Contains(res.Output, "didn't match") {
		t.Errorf("mistyped: %+v", res)
	}
	s.Run(typed, "prod")
	if r.last() != "--confirm prod -c prod aws ec2 terminate-instances" {
		t.Errorf("typed: %s", r.last())
	}
	s.StopTunnel("16f8")
	if r.last() != "tunnel stop 16f8" {
		t.Errorf("stop: %s", r.last())
	}
	s.WriteKubeconfig("dev", "main")
	if r.last() != "-c dev kube main" {
		t.Errorf("kube: %s", r.last())
	}
	if p := s.RemoveContext("dev"); p.Ask != "yes" || strings.Join(p.Args, " ") != "ctx rm dev --yes" {
		t.Errorf("remove: %+v", p)
	}
	r.err = errors.New("mek: dev is readonly — blocked")
	if res := s.UseContext("dev"); res.OK || res.Output != r.err.Error() {
		t.Errorf("failed: %+v", res)
	}
}

func TestTerminal(t *testing.T) {
	var lines []string
	old := openTerminal
	t.Cleanup(func() { openTerminal = old })
	openTerminal = func(line string) error { lines = append(lines, line); return nil }
	s := New(sample(), "/Applications/mek desktop.app/Contents/MacOS/mek")
	s.Login("dev")
	s.Shell("dev", "web")
	s.K9s("dev", "main")
	s.AddContext()
	s.EditContext("dev")
	s.AddEntry("dev", "tunnel")
	if err := s.AddEntry("dev", "bucket"); err == nil {
		t.Error("a bucket entry")
	}
	mek := `'/Applications/mek desktop.app/Contents/MacOS/mek'`
	want := []string{mek + " login dev", mek + " -c dev shell web", mek + " -c dev exec -- k9s --context dev/main",
		mek + " ctx add", mek + " ctx edit dev", mek + " -c dev ctx tunnel add"}
	if !slices.Equal(lines, want) {
		t.Errorf("terminal lines:\n%q\nwant\n%q", lines, want)
	}
	if shellQuote("it's") != `'it'\''s'` {
		t.Errorf("quote: %s", shellQuote("it's"))
	}
}

func TestTerminalCommand(t *testing.T) {
	var started [][]string
	oldStart, oldOS, oldLook := start, goos, lookPath
	t.Cleanup(func() { start, goos, lookPath = oldStart, oldOS, oldLook })
	start = func(c *exec.Cmd) error { started = append(started, c.Args); return nil }

	goos = func() string { return "darwin" }
	terminalCommand("mek login dev")
	if got := strings.Join(started[0], " "); !strings.HasPrefix(got, `osascript -e tell application "Terminal" to do script "mek login dev"`) {
		t.Errorf("macOS: %s", got)
	}
	goos = func() string { return "linux" }
	lookPath = func(name string) (string, error) {
		if name == "konsole" {
			return "/usr/bin/konsole", nil
		}
		return "", exec.ErrNotFound
	}
	terminalCommand("mek login dev")
	if got := strings.Join(started[1], " "); !strings.HasPrefix(got, "/usr/bin/konsole -e sh -c mek login dev;") {
		t.Errorf("linux: %s", got)
	}
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if err := terminalCommand("mek login dev"); err == nil || !strings.Contains(err.Error(), "run it yourself: mek login dev") {
		t.Errorf("no terminal: %v", err)
	}
	if goOS() == "" {
		t.Error("no GOOS")
	}
	c := exec.Command("true")
	if err := oldStart(c); err != nil || c.Wait() != nil {
		t.Errorf("start: %v", err)
	}
}

func TestRunMek(t *testing.T) {
	out, err := run("/bin/sh", []string{"-c", `echo "$MEK_SHOW_CMD $CLOUDSDK_CORE_DISABLE_PROMPTS"`})
	if err != nil || string(out) != "0 1\n" {
		t.Errorf("run: %q %v", out, err)
	}
	for script, want := range map[string]string{
		"echo 'ERROR: (gcloud) bad' >&2; echo hint >&2; exit 1": "ERROR: (gcloud) bad",
		"echo first >&2; echo 'An occurred' >&2; exit 1":        "An occurred",
		"exit 3": "exit status 3",
	} {
		if _, err := run("/bin/sh", []string{"-c", script}); err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %s", script, err, want)
		}
	}
	if _, err := run("/nonexistent/mek", nil); err == nil {
		t.Error("ran a missing mek")
	}
}

func TestFindMek(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "mek-desktop")
	old := lookPath
	t.Cleanup(func() { lookPath = old })
	lookPath = func(string) (string, error) { return "/usr/local/bin/mek", nil }
	if got, _ := FindMek(self); got != "/usr/local/bin/mek" {
		t.Errorf("on PATH: %s", got)
	}
	os.WriteFile(filepath.Join(dir, "mek"), nil, 0o755)
	if got, _ := FindMek(self); got != filepath.Join(dir, "mek") {
		t.Errorf("bundled: %s", got)
	}
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if _, err := FindMek(filepath.Join(t.TempDir(), "x")); err == nil || !strings.Contains(err.Error(), "brew install") {
		t.Errorf("missing: %v", err)
	}
}

func TestUseLoginPath(t *testing.T) {
	old := loginPath
	t.Cleanup(func() { loginPath = old })
	var shells []string
	loginPath = func(shell string) (string, error) {
		shells = append(shells, shell)
		return "/opt/homebrew/bin:/usr/bin", nil
	}
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/usr/bin")
	UseLoginPath()
	if os.Getenv("PATH") != "/opt/homebrew/bin:/usr/bin" {
		t.Errorf("PATH: %s", os.Getenv("PATH"))
	}
	t.Setenv("SHELL", "")
	loginPath = func(shell string) (string, error) { shells = append(shells, shell); return "", errors.New("no") }
	UseLoginPath()
	if os.Getenv("PATH") != "/opt/homebrew/bin:/usr/bin" || !slices.Equal(shells, []string{"/bin/zsh", "/bin/sh"}) {
		t.Errorf("failed: PATH %s, shells %v", os.Getenv("PATH"), shells)
	}
	if p, err := old("/bin/sh"); err != nil || p == "" {
		t.Errorf("the login shell's PATH: %q %v", p, err)
	}
}
