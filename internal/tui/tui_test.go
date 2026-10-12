package tui

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/tunnel"
)

// fakeSource is a Source with fixed data.
type fakeSource struct {
	contexts []Context
	current  string
	err      error
	tunnels  []tunnel.Info
	audit    []audit.Entry
	auditErr error
	log      string
}

func (f *fakeSource) Contexts() ([]Context, string, error) { return f.contexts, f.current, f.err }
func (f *fakeSource) Tunnels() []tunnel.Info               { return f.tunnels }
func (f *fakeSource) Audit(int) ([]audit.Entry, error)     { return f.audit, f.auditErr }
func (f *fakeSource) Log(string) string                    { return f.log }

// state is a tunnel.State with a fixed name.
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
		contexts: []Context{
			{Name: "dev", Summary: "aws profile dev",
				Targets:  []Target{{"bastion", "i-0abc"}},
				Clusters: []Cluster{{"main", "dev-eks"}},
				Tunnels:  []Tunnel{{"db", "db.internal:5432 via bastion", 15432}, {"cache", "port 6379 on bastion", 16379}}},
			{Name: "gcp", Summary: "gcp p", ReadOnly: true},
			{Name: "prod", Summary: "aws profile prod", Protected: true, Tunnels: []Tunnel{{"db", "prod-db:5432 via i-0abc", 25432}}},
		},
	}
}

// key builds a key press as the terminal would send it.
func key(s string) tea.KeyPressMsg {
	special := map[string]tea.KeyPressMsg{
		"enter": {Code: tea.KeyEnter}, "esc": {Code: tea.KeyEscape}, "up": {Code: tea.KeyUp}, "down": {Code: tea.KeyDown},
		"tab": {Code: tea.KeyTab}, "shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift}, "ctrl+c": {Code: 'c', Mod: tea.ModCtrl},
		"backspace": {Code: tea.KeyBackspace}, "home": {Code: tea.KeyHome}, "end": {Code: tea.KeyEnd},
		"pgup": {Code: tea.KeyPgUp}, "pgdown": {Code: tea.KeyPgDown},
	}
	if k, ok := special[s]; ok {
		return k
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// ran records what the UI ran.
type ran struct {
	args        []string
	interactive bool
}

// harness is a model on a fake source, with what it runs recorded and
// answered by out / fail.
type harness struct {
	t    *testing.T
	m    *model
	src  *fakeSource
	runs []ran
	out  string
	fail error
}

func newHarness(t *testing.T, src *fakeSource, selected string) *harness {
	t.Helper()
	h := &harness{t: t, src: src}
	oldExec, oldOut := execInTerminal, output
	t.Cleanup(func() { execInTerminal, output = oldExec, oldOut })
	execInTerminal = func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		return func() tea.Msg {
			h.runs = append(h.runs, ran{c.Args[1:], true})
			return fn(h.fail)
		}
	}
	output = func(c *exec.Cmd) ([]byte, error) {
		h.runs = append(h.runs, ran{c.Args[1:], false})
		return []byte(h.out), h.fail
	}
	h.m = newModel(src, "/bin/mek", selected)
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	return h
}

// press sends keys, running the commands they return (and what those
// return, like a program would).
func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		_, cmd := h.m.Update(key(k))
		h.drain(cmd)
	}
}

func (h *harness) drain(cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			return
		}
		_, cmd = h.m.Update(msg)
	}
}

func (h *harness) screen() string { return ansi.Strip(h.m.render()) }

func (h *harness) want(subs ...string) {
	h.t.Helper()
	s := h.screen()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			h.t.Errorf("screen missing %q:\n%s", sub, s)
		}
	}
}

func (h *harness) lastRun() ran {
	h.t.Helper()
	if len(h.runs) == 0 {
		h.t.Fatal("nothing ran")
	}
	return h.runs[len(h.runs)-1]
}

func (h *harness) wantRun(interactive bool, args ...string) {
	h.t.Helper()
	r := h.lastRun()
	if strings.Join(r.args, " ") != strings.Join(args, " ") || r.interactive != interactive {
		h.t.Errorf("ran %v (interactive %v), want %v (interactive %v)", r.args, r.interactive, args, interactive)
	}
}

func TestContextsTab(t *testing.T) {
	h := newHarness(t, sample(), "")
	h.want("1 Contexts", "▸ *  dev", "readonly", "protected", "enter use")
	h.press("down")
	if h.m.selected != "gcp" {
		t.Errorf("selected %q", h.m.selected)
	}
	h.out = "✓ now using gcp\n"
	h.press("enter")
	h.wantRun(false, "use", "gcp")
	h.want("✓ use gcp — ✓ now using gcp")
	h.press("l")
	h.wantRun(true, "login", "gcp")
	h.press("x") // not a key of this tab
	if len(h.runs) != 2 {
		t.Errorf("runs: %v", h.runs)
	}

	h.fail, h.out = errors.New("exit status 1"), "mek: no SSO session\n"
	h.press("enter")
	h.want("✗ use gcp: mek: no SSO session")
	h.out = ""
	h.press("enter")
	h.want("✗ use gcp: exit status 1")

	empty := newHarness(t, &fakeSource{}, "")
	empty.press("enter", "down", "l", "3", "enter", "k")
	empty.want("no contexts")
	empty.press("2", "enter", "x", "4", "enter", "/")
	if len(empty.runs) != 0 {
		t.Errorf("acted with no contexts: %v", empty.runs)
	}
}

func TestSelection(t *testing.T) {
	src := sample()
	if h := newHarness(t, src, "prod"); h.m.selected != "prod" {
		t.Errorf("-c prod: %q", h.m.selected)
	}
	if h := newHarness(t, src, "gone"); h.m.selected != "dev" { // falls back to the current one
		t.Errorf("unknown: %q", h.m.selected)
	}
	src.current = ""
	if h := newHarness(t, src, ""); h.m.selected != "dev" { // then the first
		t.Errorf("no current: %q", h.m.selected)
	}
	src.err = errors.New("config.yaml: line 3: unknown field")
	h := newHarness(t, src, "")
	h.want("✗ config.yaml: line 3: unknown field")
}

func TestRoot(t *testing.T) {
	h := newHarness(t, sample(), "")
	old := refreshEvery
	refreshEvery = time.Millisecond
	t.Cleanup(func() { refreshEvery = old })
	if _, ok := h.m.Init()().(tickMsg); !ok {
		t.Error("no refresh tick")
	}
	if v := h.m.View(); !v.AltScreen || v.WindowTitle != "mek ui" {
		t.Errorf("view: %+v", v)
	}
	for _, c := range []struct {
		key  string
		want int
	}{{"2", 1}, {"4", 3}, {"tab", 0}, {"shift+tab", 3}, {"shift+tab", 2}, {"1", 0}} {
		h.press(c.key)
		if h.m.active != c.want {
			t.Errorf("after %s: tab %d, want %d", c.key, h.m.active, c.want)
		}
	}
	h.press("?")
	h.want("Keys", "Tunnels   enter start", "any key: back")
	h.press("z")
	if h.m.overlay != nil {
		t.Error("keys screen stayed open")
	}
	for _, k := range []string{"q", "ctrl+c"} {
		if _, cmd := h.m.Update(key(k)); cmd == nil {
			t.Errorf("%s: no quit", k)
		} else if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s: not quit", k)
		}
	}
	h.m.overlay = keysScreen{}
	if _, cmd := h.m.Update(key("ctrl+c")); cmd == nil { // even with an overlay open
		t.Error("ctrl+c in an overlay")
	}

	// Refreshing: a tick reloads and asks for the next one; r reloads now.
	h.src.current = "prod"
	if _, cmd := h.m.Update(tickMsg{}); cmd == nil || h.m.current != "prod" {
		t.Errorf("tick: %v %q", cmd, h.m.current)
	}
	h.src.current = "gcp"
	h.m.overlay = nil
	h.press("r")
	if h.m.current != "gcp" {
		t.Error("r did not reload")
	}
	if _, cmd := h.m.Update("something else"); cmd != nil {
		t.Error("unknown message")
	}
	h.m.width = 0
	if h.m.render() != "" {
		t.Error("rendered before the size was known")
	}
}

func TestTunnelsTab(t *testing.T) {
	src := sample()
	src.tunnels = []tunnel.Info{
		{Record: tunnel.Record{ID: "aaaa1111aaaa1111", Context: "dev", Name: "db", Local: 15432, Background: true, Restarts: 2,
			Target: "localhost:15432 → db", Started: time.Now().Add(-3 * time.Minute)}, State: state{"running", true}},
		{Record: tunnel.Record{ID: "bbbb2222bbbb2222", Context: "dev", Local: 18080, Target: "localhost:18080 → app",
			Started: time.Now()}, State: state{"running", true}},
		{Record: tunnel.Record{ID: "cccc3333cccc3333", Context: "gcp", Local: 18081, Background: true, Started: time.Now()},
			State: state{"exited (255)", false}},
		{Record: tunnel.Record{ID: "dddd4444dddd4444", Context: "dev", Local: 18082, Background: true, Started: time.Now()},
			State: state{"reconnecting", true}},
	}
	src.log = "line 1\nline 2\nline 3\n"
	h := newHarness(t, src, "")
	h.press("2")
	h.want("aaaa1111  dev      db", "running ↻2", "3m", "running (foreground)", "exited (255)", "reconnecting",
		"cache  16379  not running", "prod     db     25432  not running")
	if strings.Contains(h.screen(), "dev      db    15432  not running") {
		t.Error("a running named tunnel also listed as not running")
	}

	h.press("enter") // a running one: its log
	h.want("log of db (dev)", "following", "line 3")
	h.src.log = "line 1\nline 2\nline 3\nline 4\n"
	h.m.Update(tickMsg{})
	h.want("line 4")
	h.press("esc")

	h.press("x")
	h.wantRun(false, "tunnel", "stop", "aaaa1111aaaa1111")
	h.want("✓ stop tunnel db")

	h.press("down", "enter") // foreground: no log here
	h.want("runs in the foreground")
	h.press("l")
	h.want("runs in the foreground")

	h.press("end", "enter") // prod db: protected, asks first
	h.want("prod is protected — start tunnel db? [y/N]", "y yes")
	h.press("n")
	h.want("cancelled: start tunnel db (prod)")
	n := len(h.runs)
	h.press("enter", "y")
	h.wantRun(false, "--yes", "-c", "prod", "tunnel", "db", "-b")
	if len(h.runs) != n+1 {
		t.Errorf("runs: %v", h.runs[n:])
	}
	h.press("up", "enter") // dev cache: not protected
	h.wantRun(false, "-c", "dev", "tunnel", "cache", "-b")
	n = len(h.runs)
	h.press("x", "l") // nothing to stop or read for a tunnel that isn't running
	if len(h.runs) != n || h.m.overlay != nil {
		t.Errorf("acted on a not-running tunnel: %v %v", h.runs[n:], h.m.overlay)
	}

	none := newHarness(t, &fakeSource{}, "")
	none.press("2", "enter", "x")
	none.want("(none)")
}

func TestLogsScreen(t *testing.T) {
	var lines []string
	for i := range 40 {
		lines = append(lines, "log line "+string(rune('A'+i%26)))
	}
	h := newHarness(t, sample(), "")
	h.m.overlay = &logsScreen{id: "x", label: "db (dev)", text: strings.Join(lines, "\n"), follow: true}
	h.want("log line N", "following") // the last line: 40th is 'N'
	h.press("up")
	h.want("scrolled — G to follow")
	h.press("down", "down", "down")
	h.press("G")
	h.want("following")
	h.press("k", "j", "end", "q")
	if h.m.overlay != nil {
		t.Error("q did not close the log")
	}
}

func TestAccessTab(t *testing.T) {
	h := newHarness(t, sample(), "")
	h.press("3")
	h.want("dev", "shell    bastion  i-0abc", "cluster  main     dev-eks", "choose another on tab 1")
	h.press("enter")
	h.wantRun(true, "-c", "dev", "shell", "bastion")
	n := len(h.runs)
	h.press("k") // k9s is for clusters
	if len(h.runs) != n {
		t.Error("k on a target ran something")
	}
	h.press("down", "enter")
	h.wantRun(false, "-c", "dev", "kube", "main")
	h.press("z")

	old := lookPath
	t.Cleanup(func() { lookPath = old })
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	h.press("k")
	h.want("k9s isn't installed")
	lookPath = func(string) (string, error) { return "/usr/bin/k9s", nil }
	n = len(h.runs)
	h.press("k") // a fresh kubeconfig first, then k9s on it
	if len(h.runs) != n+2 || strings.Join(h.runs[n].args, " ") != "-c dev kube main" {
		t.Errorf("runs: %v", h.runs[n:])
	}
	h.wantRun(true, "-c", "dev", "exec", "--", "k9s", "--context", "dev/main")

	h.press("1", "down", "3") // gcp has neither
	h.want("gcp readonly", "(none)")
	h.press("enter")
}

func TestAuditTab(t *testing.T) {
	src := sample()
	today := time.Date(2026, 10, 13, 9, 30, 0, 0, time.Local)
	old := now
	now = func() time.Time { return today }
	t.Cleanup(func() { now = old })
	src.audit = []audit.Entry{
		{Time: today.AddDate(0, 0, -2), Context: "dev", Decision: "allowed", Class: "read", Command: []string{"aws", "s3", "ls"}},
		{Time: today, Context: "prod", Decision: "blocked", Class: "destructive", ExitCode: 1, Command: []string{"aws", "rds", "delete-db-instance"}},
		{Time: today, Context: "prod", Decision: "confirmed", Class: "tunnel", Event: "start", Command: []string{"aws", "ssm", "start-session"}},
		{Time: today, Context: "dev", Decision: "declined", Class: "write", Command: []string{"kubectl", "apply"}},
	}
	h := newHarness(t, src, "")
	h.press("4")
	h.want("4 of the last 4 entries", "09:30:00", "Oct 11 09:30", "blocked", "tunnel start  -", "aws rds delete-db-instance")
	s := h.screen()
	if strings.Index(s, "kubectl apply") > strings.Index(s, "aws s3 ls") {
		t.Error("not newest first")
	}
	h.press("b")
	h.want("2 of the last 4", "blocked/declined")
	h.press("c")
	h.want("1 of the last 4", "context dev", "kubectl apply")
	h.press("esc")
	h.want("4 of the last 4")

	h.press("/", "s", "3", "x", "backspace")
	h.want("/ s3▏", "type to search")
	h.press("enter")
	h.want(`1 of the last 4 entries · "s3"`, "aws s3 ls")
	h.press("/", "backspace", "backspace", "backspace", "esc")
	h.want("4 of the last 4")
	h.press("down", "up")

	// A failed read keeps what was shown.
	src.auditErr = errors.New("unreadable")
	h.m.reload()
	if len(h.m.audit) != 4 {
		t.Error("lost the entries on a failed read")
	}
}

func TestList(t *testing.T) {
	var l list
	for k, want := range map[string]int{"down": 1, "pgdown": 12, "G": 29, "end": 29, "pgup": 19, "up": 18, "g": 0, "home": 0} {
		l.cursor = map[string]int{"down": 0, "pgdown": 2, "G": 3, "end": 0, "pgup": 29, "up": 19, "g": 9, "home": 4}[k]
		if !l.key(k, 30) || l.cursor != want {
			t.Errorf("%s: cursor %d, want %d", k, l.cursor, want)
		}
	}
	if l.key("x", 30) || l.key("down", 0) {
		t.Error("moved")
	}
	var rows [][]string
	for i := range 30 {
		rows = append(rows, []string{string(rune('a' + i%26)), strings.Repeat("w", 50)})
	}
	l.cursor = 25
	out := ansi.Strip(l.render([]string{"A", "B"}, rows, 30, 6))
	if lines := strings.Split(out, "\n"); len(lines) != 6 || !strings.HasPrefix(lines[5], "▸ z") || !strings.HasSuffix(lines[5], "…") {
		t.Errorf("scrolled to the cursor, cut to the width:\n%s", out)
	}
}

func TestActions(t *testing.T) {
	if lastLine("a\nb\n\n") != "b" || cmp("", "x") != "x" || cmp("y", "x") != "y" {
		t.Error("helpers")
	}
	var stderr bytes.Buffer
	p := pausing{exec.Command("false")}
	p.SetStdin(strings.NewReader("\n"))
	p.SetStdout(io.Discard)
	p.SetStderr(&stderr)
	if err := p.Run(); err == nil || !strings.Contains(stderr.String(), "press Enter to return to mek ui") {
		t.Errorf("failed command: %v %q", err, stderr.String())
	}
	stderr.Reset()
	p = pausing{exec.Command("true")}
	p.SetStdin(strings.NewReader(""))
	p.SetStdout(io.Discard)
	p.SetStderr(&stderr)
	if err := p.Run(); err != nil || stderr.Len() != 0 {
		t.Errorf("good command: %v %q", err, stderr.String())
	}
	// The real seams build what they're given.
	if execInTerminal(exec.Command("true"), func(error) tea.Msg { return nil }) == nil {
		t.Error("execInTerminal")
	}
	if out, err := output(exec.Command("echo", "hi")); err != nil || string(out) != "hi\n" {
		t.Errorf("output: %q %v", out, err)
	}
}

func TestRun(t *testing.T) {
	old := programOptions
	t.Cleanup(func() { programOptions = old })
	programOptions = []tea.ProgramOption{tea.WithInput(strings.NewReader("q")), tea.WithOutput(io.Discard), tea.WithWindowSize(80, 20)}
	if err := Run(sample(), "/bin/mek", ""); err != nil {
		t.Fatal(err)
	}
}

func TestLive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	if _, _, err := (Live{}).Contexts(); err == nil {
		t.Error("no config: no error")
	}
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte(`contexts:
  dev:
    provider: aws
    aws_profile: dev
    protected: true
    targets: {web: {instance: i-0123456789abcdef0}}
    clusters: {main: {name: dev-eks, region: r}}
    tunnels:
      db: {via: web, host: db.internal, port: 5432}
      app: {via: web, port: 8080, local_port: 18080}
  g:
    provider: gcp
    project: p
    tunnels: {sql: {cloudsql: "p:r:db", local_port: 15432}}
`), 0o600)
	cs, current, err := (Live{}).Contexts()
	if err != nil || len(cs) != 2 || current != "" {
		t.Fatalf("contexts: %+v %q %v", cs, current, err)
	}
	d := cs[0]
	if d.Name != "dev" || !d.Protected || d.Summary == "" || d.Targets[0] != (Target{"web", "i-0123456789abcdef0"}) ||
		d.Clusters[0] != (Cluster{"main", "dev-eks"}) ||
		d.Tunnels[0] != (Tunnel{"app", "port 8080 on web", 18080}) || d.Tunnels[1] != (Tunnel{"db", "db.internal:5432 via web", 15432}) {
		t.Errorf("dev: %+v", d)
	}
	if cs[1].Tunnels[0] != (Tunnel{"sql", "Cloud SQL p:r:db", 15432}) {
		t.Errorf("g: %+v", cs[1])
	}
	if len((Live{}).Tunnels()) != 0 || (Live{}).Log("nope") != "" {
		t.Error("tunnels")
	}
	audit.Write(audit.Entry{Context: "dev", Command: []string{"aws"}})
	if es, err := (Live{}).Audit(10); err != nil || len(es) != 1 {
		t.Errorf("audit: %v %v", es, err)
	}
}
