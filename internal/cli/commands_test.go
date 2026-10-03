package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/selfupdate"
	"github.com/prateep-r/mek/internal/ui"
	"github.com/prateep-r/mek/internal/version"
)

const testConfig = `contexts:
  uat:  {provider: aws, sso_start_url: "https://o.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Dev}
  prod: {provider: aws, aws_profile: prod-admin, protected: true}
  ro:   {provider: gcp, project: p, readonly: true}
  az:   {provider: azure, tenant_id: t, subscription_id: 00000000-1111-2222-3333-444444444444}
  hw:   {provider: huawei, hcloud_profile: sso-prod, region: ap-southeast-2}
`

// recorder is a fake runner: it records invocations instead of starting processes.
type recorder struct {
	invs []*runner.Invocation
	code int
}

func (r *recorder) Run(inv *runner.Invocation) error {
	r.invs = append(r.invs, inv)
	inv.ExitCode = r.code
	return nil
}

func (r *recorder) argv() []string {
	var out []string
	for _, inv := range r.invs {
		out = append(out, strings.Join(inv.Argv, " "))
	}
	return out
}

type harness struct {
	t    *testing.T
	home string
	exec *recorder
}

// newHarness sets up MEK_HOME with config (none if config is ""), an empty
// HOME, and no inherited context.
func newHarness(t *testing.T, cfg string) *harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MEK_CONTEXT", "")
	t.Setenv("MEK_HCLOUD_CONFIG", filepath.Join(t.TempDir(), "no-hcloud-config.json"))
	if cfg != "" {
		if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &harness{t: t, home: home, exec: &recorder{}}
}

func (h *harness) run(args ...string) (string, error) {
	h.t.Helper()
	root := newRoot(&app{exec: h.exec})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func (h *harness) mustRun(args ...string) string {
	h.t.Helper()
	out, err := h.run(args...)
	if err != nil {
		h.t.Fatalf("mek %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func wantErr(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Errorf("err = %v, want containing %q", err, substr)
	}
}

func TestInitCmd(t *testing.T) {
	h := newHarness(t, "")
	h.mustRun("init")
	if _, err := os.Stat(config.Path()); err != nil {
		t.Fatal(err)
	}
	_, err := h.run("init")
	wantErr(t, err, "already exists")
}

func TestUseAndCtx(t *testing.T) {
	h := newHarness(t, testConfig)
	h.mustRun("use", "uat")
	if config.Current() != "uat" {
		t.Fatalf("current = %q", config.Current())
	}
	if out := h.mustRun("ctx"); !strings.Contains(out, "uat") || !strings.Contains(out, "111122223333") {
		t.Errorf("ctx: %q", out)
	}
	if out := h.mustRun("ctx", "--short"); out != "uat\n" {
		t.Errorf("ctx --short: %q", out)
	}
	out := h.mustRun("ctx", "ls")
	for _, want := range []string{"* uat", "prod", "[protected]", "[readonly]", "huawei profile sso-prod"} {
		if !strings.Contains(out, want) {
			t.Errorf("ctx ls missing %q:\n%s", want, out)
		}
	}

	t.Setenv("MEK_CONTEXT", "prod") // still wins after `use`; mek says so
	h.mustRun("use", "az")
	if out := h.mustRun("ctx", "--short"); out != "prod\n" {
		t.Errorf("MEK_CONTEXT should take precedence: %q", out)
	}

	_, err := h.run("use", "nope")
	wantErr(t, err, "unknown context")

	os.Chmod(h.home, 0o500) // config readable, state can't be saved
	t.Cleanup(func() { os.Chmod(h.home, 0o700) })
	_, err = h.run("use", "uat")
	if err == nil {
		t.Error("use must report a failed state write")
	}
}

func TestCommandsWithoutConfig(t *testing.T) {
	h := newHarness(t, "")
	for _, args := range [][]string{{"use", "uat"}, {"ctx"}, {"ctx", "ls"}, {"login"}, {"exec", "--", "true"}, {"env"}, {"aws", "s3", "ls"}} {
		_, err := h.run(args...)
		if !errors.Is(err, config.ErrNoConfig) {
			t.Errorf("mek %v: %v, want ErrNoConfig", args, err)
		}
	}
	h2 := newHarness(t, testConfig) // config, but no context selected
	_, err := h2.run("ctx")
	wantErr(t, err, "no context selected")
}

func TestLogin(t *testing.T) {
	h := newHarness(t, testConfig)
	h.mustRun("login", "uat")
	if got := h.exec.argv(); !slices.Equal(got, []string{"aws sso login --profile mek-uat", "aws sts get-caller-identity --output table"}) {
		t.Errorf("login ran %q", got)
	}
	if env := h.exec.invs[0].Env; !slices.Contains(env, "MEK_CONTEXT=uat") {
		t.Error("login must run with the context's environment")
	}

	_, err := h.run("login", "hw") // no KooCLI config
	wantErr(t, err, "no KooCLI config")

	h.exec.code = 1 // the CLI's login failed: its exit code reaches main
	_, err = h.run("login", "az")
	var exit *runner.ExitError
	if !errors.As(err, &exit) || exit.Code != 1 || len(h.exec.invs) != 3 {
		t.Errorf("failed login: %v (ran %q)", err, h.exec.argv())
	}
}

func TestPassthrough(t *testing.T) {
	h := newHarness(t, testConfig)
	h.mustRun("-c", "uat", "aws", "s3", "ls")
	inv := h.exec.invs[0]
	if strings.Join(inv.Argv, " ") != "aws s3 ls" || !slices.Contains(inv.Env, "AWS_PROFILE=mek-uat") || inv.Decision != "allowed" {
		t.Errorf("aws: %+v", inv)
	}

	h.mustRun("-c", "hw", "hcloud", "ECS", "ListServersDetails")
	if got := h.exec.argv()[1]; got != "hcloud ECS ListServersDetails --cli-profile=sso-prod --cli-region=ap-southeast-2" {
		t.Errorf("hcloud args not rewritten: %q", got)
	}

	_, err := h.run("-c", "uat", "gcloud", "projects", "list")
	wantErr(t, err, "needs a context with provider gcp")
	_, err = h.run("aws", "-c")
	wantErr(t, err, "needs a value")
	_, err = h.run("-c", "nope", "aws", "s3", "ls")
	wantErr(t, err, "unknown context")

	h = newHarness(t, testConfig)
	os.WriteFile(filepath.Join(h.home, "aws"), nil, 0o600) // a file where the AWS config dir goes
	_, err = h.run("-c", "uat", "aws", "s3", "ls")
	if err == nil {
		t.Error("Prepare error must stop the command")
	}
}

func TestExecAndEnv(t *testing.T) {
	h := newHarness(t, testConfig)
	h.mustRun("-c", "az", "exec", "--", "terraform", "plan")
	if inv := h.exec.invs[0]; strings.Join(inv.Argv, " ") != "terraform plan" || inv.Class.String() != "unknown" {
		t.Errorf("exec: %+v", inv)
	}
	out := h.mustRun("env", "az")
	if !strings.Contains(out, "export AZURE_CONFIG_DIR=") || !strings.Contains(out, "unset AZURE_CLIENT_SECRET") {
		t.Errorf("env: %s", out)
	}
	_, err := h.run("env", "nope")
	wantErr(t, err, "unknown context")
}

func TestAuditFailureDoesNotFailCommand(t *testing.T) {
	h := newHarness(t, testConfig)
	os.Mkdir(filepath.Join(h.home, "audit.jsonl"), 0o700) // can't append
	if _, err := h.run("-c", "uat", "aws", "s3", "ls"); err != nil {
		t.Errorf("audit failure leaked into the command: %v", err)
	}
	if len(h.exec.invs) != 1 {
		t.Error("command did not run")
	}
}

// fakePrompts answers confirmations without a terminal.
func fakePrompts(t *testing.T, ok bool, err error) {
	t.Helper()
	oc, ot := confirm, confirmTyped
	confirm = func(string) (bool, error) { return ok, err }
	confirmTyped = func(string, string) (bool, error) { return ok, err }
	t.Cleanup(func() { confirm, confirmTyped = oc, ot })
}

func TestProtectedPrompts(t *testing.T) {
	cases := []struct {
		name     string
		ok       bool
		err      error
		ran      bool
		errText  string
		decision string
	}{
		{"answered yes", true, nil, true, "", "confirmed"},
		{"answered no", false, nil, false, "aborted", "declined"},
		{"no terminal", false, ui.ErrNoTTY, false, "pass --", "blocked"},
		{"read error", false, errors.New("tty broke"), false, "tty broke", "declined"},
	}
	for _, class := range []string{"run-instances", "terminate-instances"} { // write, destructive
		for _, c := range cases {
			t.Run(class+"/"+c.name, func(t *testing.T) {
				h := newHarness(t, testConfig)
				fakePrompts(t, c.ok, c.err)
				_, err := h.run("-c", "prod", "aws", "ec2", class)
				if (len(h.exec.invs) == 1) != c.ran {
					t.Errorf("ran = %v, want %v", len(h.exec.invs) == 1, c.ran)
				}
				if c.errText == "" && err != nil || c.errText != "" {
					if c.errText != "" {
						wantErr(t, err, c.errText)
					} else {
						t.Errorf("unexpected err: %v", err)
					}
				}
				if e := lastAudit(t); e.Decision != c.decision {
					t.Errorf("audited decision %q, want %q", e.Decision, c.decision)
				}
			})
		}
	}
}

func TestVersionCmd(t *testing.T) {
	h := newHarness(t, "")
	if out := h.mustRun("version"); !strings.HasPrefix(out, "mek "+version.Version) {
		t.Errorf("version: %q", out)
	}
}

func TestSelfUpdateCmd(t *testing.T) {
	oldL, oldA, oldV := latestRelease, applyUpdate, version.Version
	t.Cleanup(func() { latestRelease, applyUpdate, version.Version = oldL, oldA, oldV })
	var applied bool
	latestRelease = func(string) (string, error) { return "v1.0.0", nil }
	applyUpdate = func(string, string) (string, error) { applied = true; return "/bin/mek", nil }
	h := newHarness(t, "")

	version.Version = "v1.0.0"
	h.mustRun("self-update")
	if applied {
		t.Error("up to date: must not update")
	}
	version.Version = "v0.9.0"
	h.mustRun("self-update", "--check")
	if applied {
		t.Error("--check must not update")
	}
	h.mustRun("self-update")
	if !applied {
		t.Error("newer release was not applied")
	}

	applyUpdate = func(string, string) (string, error) { return "", selfupdate.ErrHomebrew }
	h.mustRun("self-update") // Homebrew installs get a hint, not an error
	applyUpdate = func(string, string) (string, error) { return "", errors.New("disk full") }
	_, err := h.run("self-update")
	wantErr(t, err, "disk full")
	latestRelease = func(string) (string, error) { return "", errors.New("offline") }
	_, err = h.run("self-update")
	wantErr(t, err, "offline")
}

func TestCompleteContexts(t *testing.T) {
	newHarness(t, testConfig)
	names, _ := completeContexts(nil, nil, "")
	if !slices.Equal(names, []string{"az", "hw", "prod", "ro", "uat"}) {
		t.Errorf("names: %v", names)
	}
	newHarness(t, "")
	if names, _ := completeContexts(nil, nil, ""); names != nil {
		t.Errorf("no config: %v", names)
	}
}

func TestTakeGlobalFlagsConsumesAll(t *testing.T) {
	a := &app{}
	rest, err := a.takeGlobalFlags([]string{"-c", "uat", "-y"})
	if err != nil || len(rest) != 0 || a.opts.context != "uat" || !a.opts.yes {
		t.Errorf("rest=%v opts=%+v err=%v", rest, a.opts, err)
	}
}

func TestUseShell(t *testing.T) {
	h := newHarness(t, testConfig)
	h.mustRun("use", "uat")
	out := h.mustRun("use", "--shell", "prod")
	if out != "export MEK_CONTEXT=prod\n" {
		t.Errorf("use --shell printed %q", out)
	}
	if config.Current() != "uat" {
		t.Errorf("use --shell must not change the saved context, now %q", config.Current())
	}
	_, err := h.run("use", "--shell", "nope")
	wantErr(t, err, "unknown context")
}

func TestLoginPassesFlagsToTheCLI(t *testing.T) {
	h := newHarness(t, testConfig)
	h.mustRun("login", "az", "--", "--service-principal", "-u", "app", "-p", "s3cret")
	got := h.exec.argv()
	if got[0] != "az login --tenant t --service-principal -u app -p s3cret" || got[1] != "az account set --subscription 00000000-1111-2222-3333-444444444444" {
		t.Errorf("flags must go to the login command only: %q", got)
	}

	h.exec.invs = nil
	t.Setenv("MEK_CONTEXT", "uat") // no context argument: the current one
	h.mustRun("login", "--", "--no-browser")
	if got := h.exec.argv()[0]; got != "aws sso login --profile mek-uat --no-browser" {
		t.Errorf("login -- without context: %q", got)
	}

	_, err := h.run("login", "uat", "az")
	wantErr(t, err, "at most 1 context")
}
