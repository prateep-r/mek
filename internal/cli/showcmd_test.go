package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/runner"
)

func TestNativeCommand(t *testing.T) {
	base := []string{"HOME=/h", "AWS_PROFILE=old", "AWS_ACCESS_KEY_ID=leaked", "MEK_CONTEXT=x", "KEEP=1"}
	env := []string{"HOME=/h", "AWS_PROFILE=mek-prod", "AWS_CONFIG_FILE=/h/.mek/aws/config", "MEK_CONTEXT=prod", "KEEP=1"}
	got := nativeCommand([]string{"aws", "s3", "cp", "it's here", "s3://b/k"}, env, base)
	want := `↳ runs:
    unset AWS_ACCESS_KEY_ID
    export AWS_CONFIG_FILE='/h/.mek/aws/config'
    export AWS_PROFILE='mek-prod'
    aws s3 cp 'it'\''s here' s3://b/k
    # typed yourself, it skips mek's guard and audit log`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// Exported already (mek env, mek use -s), or inherited: the command alone.
	for _, e := range [][]string{base, nil} {
		if got := nativeCommand([]string{"aws", "sts", "get-caller-identity"}, e, base); got != "↳ runs:\n    aws sts get-caller-identity\n    # typed yourself, it skips mek's guard and audit log" {
			t.Errorf("no difference:\n%s", got)
		}
	}
}

func TestShowCmdState(t *testing.T) {
	h := newHarness(t, testConfig)
	t.Setenv("MEK_SHOW_CMD", "")
	state := func(a *app) string { on, why := a.showCmdState(); return word(on) + " — " + why }
	a := &app{}
	if got := state(a); got != "off — the default" {
		t.Errorf("default: %s", got)
	}
	os.WriteFile(config.Path(), []byte("settings: {show_cmd: true}\n"+testConfig), 0o600)
	if got := state(&app{}); got != "on — settings.show_cmd in "+config.Show(config.Path()) {
		t.Errorf("config: %s", got)
	}
	t.Setenv("MEK_SHOW_CMD", "off")
	if got := state(&app{}); got != "off — MEK_SHOW_CMD=off in this shell" {
		t.Errorf("env: %s", got)
	}
	t.Setenv("MEK_SHOW_CMD", "maybe") // not on or off: as if unset
	if got := state(&app{}); !strings.HasPrefix(got, "on — settings.show_cmd") {
		t.Errorf("bad env: %s", got)
	}
	t.Setenv("MEK_SHOW_CMD", "0")
	if got := state(&app{opts: globalOpts{showCmd: true}}); got != "on — --show-cmd" {
		t.Errorf("flag: %s", got)
	}
	if got := state(&app{opts: globalOpts{showCmd: true, noShowCmd: true}}); got != "off — --no-show-cmd" {
		t.Errorf("both flags: %s", got)
	}
	if got := state(&app{machine: true, opts: globalOpts{showCmd: true}}); got != "off — output for a program" {
		t.Errorf("machine: %s", got)
	}
	os.WriteFile(config.Path(), []byte("contexts: [\n"), 0o600) // unreadable: as if unset
	t.Setenv("MEK_SHOW_CMD", "")
	if got := state(&app{}); got != "off — the default" {
		t.Errorf("broken config: %s", got)
	}
	_ = h
}

func TestShowCmdRuns(t *testing.T) {
	h := newHarness(t, testConfig)
	t.Setenv("MEK_SHOW_CMD", "1")
	t.Setenv("AWS_PROFILE", "")
	out := stderr(t, func() { h.mustRun("-c", "uat", "aws", "s3", "ls") })
	if !strings.Contains(out, "↳ runs:\n    export AWS_CONFIG_FILE='"+filepath.Join(config.Dir(), "aws", "config")+"'") ||
		!strings.Contains(out, "export AWS_PROFILE='mek-uat'") || !strings.Contains(out, "\n    aws s3 ls\n") {
		t.Errorf("passthrough:\n%s", out)
	}
	// mek's flags after the CLI name, as passthrough takes them.
	t.Setenv("MEK_SHOW_CMD", "")
	if out := stderr(t, func() { h.mustRun("aws", "--show-cmd", "-c", "uat", "s3", "ls") }); !strings.Contains(out, "↳ runs:") {
		t.Errorf("--show-cmd after aws:\n%s", out)
	}
	if out := stderr(t, func() { h.mustRun("--no-show-cmd", "-c", "uat", "aws", "s3", "ls") }); strings.Contains(out, "↳ runs:") {
		t.Errorf("--no-show-cmd:\n%s", out)
	}
	t.Setenv("MEK_SHOW_CMD", "1")
	// Login, outside the guard: shown too.
	if out := stderr(t, func() { h.mustRun("login", "uat") }); !strings.Contains(out, "aws sso login --profile mek-uat") {
		t.Errorf("login:\n%s", out)
	}
	// Blocked by the guard: not shown, it doesn't run.
	if out := stderr(t, func() { h.run("-c", "ro", "gcloud", "compute", "instances", "delete", "x") }); strings.Contains(out, "↳ runs:") {
		t.Errorf("blocked:\n%s", out)
	}
	// A lookup for a wizard's form: never.
	r := &recorder{}
	if out := stderr(t, func() { (&app{exec: r}).showCmd(r).Run(&runner.Invocation{Argv: []string{"aws"}, Quiet: true}) }); out != "" {
		t.Errorf("quiet: %q", out)
	}
	// kube token: kubectl reads it.
	h.exec.respond = func([]string) (string, int) { return `{"status":{"token":"t"}}`, 0 }
	if out := stderr(t, func() {
		h.mustRun("--show-cmd", "--context", "prod", "kube", "token", "--name", "prod-eks", "--location", "ap-southeast-1")
	}); strings.Contains(out, "↳ runs:") {
		t.Errorf("kube token:\n%s", out)
	}
}

func TestShowCmdCommand(t *testing.T) {
	h := newHarness(t, "# mine\n"+testConfig)
	t.Setenv("MEK_SHOW_CMD", "")
	if out := h.mustRun("show-cmd"); out != "show-cmd is off — the default\n" {
		t.Errorf("status: %q", out)
	}
	out := stderr(t, func() { h.mustRun("show-cmd", "on") })
	if !strings.Contains(out, "✓ show-cmd on, in "+config.Show(config.Path())) {
		t.Errorf("on: %s", out)
	}
	if b := readFile(t, config.Path()); !strings.HasPrefix(b, "# mine\n") || !strings.HasSuffix(b, "\nsettings:\n  show_cmd: true\n") {
		t.Errorf("file:\n%s", b)
	}
	t.Setenv("MEK_SHOW_CMD", "0")
	if out := h.mustRun("show-cmd"); !strings.Contains(out, "off — MEK_SHOW_CMD=0 in this shell") || !strings.Contains(out, "says on") {
		t.Errorf("overridden: %q", out)
	}
	if out := stderr(t, func() { h.mustRun("show-cmd", "on") }); !strings.Contains(out, "but MEK_SHOW_CMD=0 in this shell turns it off here") {
		t.Errorf("on, but: %s", out)
	}
	t.Setenv("MEK_SHOW_CMD", "")
	h.mustRun("show-cmd", "off")
	if b := readFile(t, config.Path()); !strings.HasSuffix(b, "\nsettings:\n  show_cmd: false\n") {
		t.Errorf("off:\n%s", b)
	}
	_, err := h.run("show-cmd", "maybe")
	wantErr(t, err, "want on or off")

	// No config yet: show-cmd on starts ~/.mek/config.yaml.
	h = newHarness(t, "")
	h.mustRun("show-cmd", "on")
	if b := readFile(t, config.Path()); !strings.Contains(b, "contexts:\n") || !strings.HasSuffix(b, "settings:\n  show_cmd: true\n") {
		t.Errorf("new file:\n%s", b)
	}
	os.Remove(config.Path())
	os.Mkdir(config.Path(), 0o700)
	_, err = h.run("show-cmd", "on")
	if err == nil {
		t.Error("wrote into a directory")
	}
}
