//go:build integration

// Integration tests run the real mek binary as a separate process against
// stub cloud CLIs: real config files, environment, process exec, exit codes,
// signals, audit log and concurrency. Run with `make test-integration`.
package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/prateep-r/mek/test/testkit"
)

var mek string

func TestMain(m *testing.M) {
	os.Exit(testkit.Main(m, &mek, "-X github.com/prateep-r/mek/internal/version.Version=v0.0.0-integration"))
}

const config = `contexts:
  uat:  {provider: aws, sso_start_url: "https://o.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Dev, region: ap-southeast-1}
  prod: {provider: aws, aws_profile: prod-admin, protected: true}
  ro:   {provider: aws, aws_profile: viewer, readonly: true}
  gcp:  {provider: gcp, project: my-project, region: asia-southeast1}
`

// env is one isolated user: their own MEK_HOME, HOME and stub CLIs on PATH.
type env struct {
	t                  *testing.T
	mekHome, home, log string
	vars               []string
}

func setup(t *testing.T, cfg string) *env {
	t.Helper()
	e := &env{t: t, mekHome: t.TempDir(), home: t.TempDir()}
	if cfg != "" {
		if err := os.WriteFile(filepath.Join(e.mekHome, "config.yaml"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stubs, log := testkit.Stubs(t, "aws", "gcloud")
	e.log = log
	e.vars = []string{"MEK_HOME=" + e.mekHome, "HOME=" + e.home, "PATH=" + stubs + ":/usr/bin:/bin", "STUB_LOG=" + log}
	return e
}

func (e *env) run(args ...string) testkit.Result {
	e.t.Helper()
	return testkit.Run(e.t, mek, e.vars, args...)
}

func (e *env) with(kv ...string) *env {
	c := *e
	c.vars = append(slices.Clone(e.vars), kv...)
	return &c
}

func (e *env) calls() []testkit.Call { return testkit.Calls(e.t, e.log) }

func (e *env) audit() []map[string]any {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.mekHome, "audit.jsonl"))
	if err != nil {
		e.t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			e.t.Fatalf("audit line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func ok(t *testing.T, r testkit.Result) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", r.Code, r.Stdout, r.Stderr)
	}
}

// Each cloud's CLI gets the context's environment, and credentials from the
// user's shell that would override the context are removed.
func TestCloudEnvironments(t *testing.T) {
	leaky := []string{"AWS_ACCESS_KEY_ID=AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY=s", "GOOGLE_APPLICATION_CREDENTIALS=/tmp/sa.json"}
	cases := []struct {
		ctx, cli string
		args     []string
		wantArgs string
		want     map[string]string
		unset    []string
	}{
		{"uat", "aws", []string{"s3", "ls"}, "s3 ls",
			map[string]string{"AWS_PROFILE": "mek-uat", "AWS_REGION": "ap-southeast-1", "MEK_CONTEXT": "uat"},
			[]string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}},
		{"gcp", "gcloud", []string{"compute", "instances", "list"}, "compute instances list",
			map[string]string{"CLOUDSDK_CORE_PROJECT": "my-project", "CLOUDSDK_COMPUTE_REGION": "asia-southeast1"},
			[]string{"GOOGLE_APPLICATION_CREDENTIALS"}},
	}
	for _, c := range cases {
		t.Run(c.cli, func(t *testing.T) {
			e := setup(t, config).with(leaky...)
			ok(t, e.run(append([]string{"-c", c.ctx, c.cli}, c.args...)...))
			calls := e.calls()
			if len(calls) != 1 || calls[0].Name != c.cli || calls[0].ArgLine() != c.wantArgs {
				t.Fatalf("calls: %+v", calls)
			}
			for k, v := range c.want {
				if calls[0].Env[k] != v {
					t.Errorf("%s = %q, want %q", k, calls[0].Env[k], v)
				}
			}
			for _, k := range c.unset {
				if _, set := calls[0].Env[k]; set {
					t.Errorf("%s leaked into the context", k)
				}
			}
		})
	}
}

func TestIsolatedConfigDirs(t *testing.T) {
	e := setup(t, config)
	ok(t, e.run("-c", "uat", "aws", "sts", "get-caller-identity"))
	ok(t, e.run("-c", "gcp", "gcloud", "config", "list"))
	calls := e.calls()
	if got := calls[0].Env["AWS_CONFIG_FILE"]; got != filepath.Join(e.mekHome, "aws", "config") {
		t.Errorf("AWS_CONFIG_FILE = %s", got)
	}
	if got := calls[1].Env["CLOUDSDK_CONFIG"]; got != filepath.Join(e.mekHome, "gcloud", "gcp") {
		t.Errorf("CLOUDSDK_CONFIG = %s", got)
	}
	b, err := os.ReadFile(filepath.Join(e.mekHome, "aws", "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[profile mek-uat]", "sso_account_id = 111122223333", "[sso-session mek-o]", "sso_region = ap-southeast-1"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("generated AWS config missing %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "prod-admin") {
		t.Error("aws_profile contexts must not be written to the generated config")
	}
}

func TestGuardAndAudit(t *testing.T) {
	e := setup(t, config)
	steps := []struct {
		args     []string
		code     int
		stderr   string
		decision string
	}{
		{[]string{"-c", "ro", "aws", "ec2", "describe-instances"}, 0, "", "allowed"},
		{[]string{"-c", "ro", "aws", "ec2", "run-instances"}, 1, "is readonly", "blocked"},
		{[]string{"-c", "prod", "aws", "ecs", "update-service"}, 1, "pass --yes", "blocked"},
		{[]string{"-c", "prod", "-y", "aws", "ecs", "update-service"}, 0, "", "confirmed"},
		{[]string{"-c", "prod", "aws", "ec2", "terminate-instances"}, 1, "pass --confirm prod", "blocked"},
		{[]string{"-c", "prod", "-y", "aws", "ec2", "terminate-instances"}, 1, "pass --confirm prod", "blocked"}, // --yes is not enough
		{[]string{"-c", "prod", "--confirm", "prod", "aws", "ec2", "terminate-instances"}, 0, "", "confirmed"},
		{[]string{"-c", "prod", "exec", "--", "terraform", "apply"}, 1, "pass --yes", "blocked"},
	}
	for _, s := range steps {
		r := e.run(s.args...)
		if r.Code != s.code || !strings.Contains(r.Stderr, s.stderr) {
			t.Errorf("mek %v: exit %d stderr %q; want exit %d containing %q", s.args, r.Code, r.Stderr, s.code, s.stderr)
		}
	}
	if ran := len(e.calls()); ran != 3 {
		t.Errorf("CLI ran %d times, want 3 (the allowed/confirmed ones)", ran)
	}
	entries := e.audit()
	if len(entries) != len(steps) {
		t.Fatalf("audit has %d entries, want %d", len(entries), len(steps))
	}
	for i, s := range steps {
		if entries[i]["decision"] != s.decision {
			t.Errorf("audit[%d] decision = %v, want %s", i, entries[i]["decision"], s.decision)
		}
	}
}

func TestExitCodes(t *testing.T) {
	e := setup(t, config)
	r := e.with("STUB_EXIT=5").run("-c", "uat", "aws", "s3", "ls")
	if r.Code != 5 || strings.Contains(r.Stderr, "mek:") {
		t.Errorf("child exit: code %d stderr %q, want 5 and no mek error", r.Code, r.Stderr)
	}
	r = e.with("PATH="+t.TempDir()).run("-c", "gcp", "gcloud", "compute", "instances", "list") // no CLIs at all
	if r.Code != 1 || !strings.Contains(r.Stderr, "gcloud not found in PATH") {
		t.Errorf("missing CLI: code %d stderr %q", r.Code, r.Stderr)
	}
	if r := e.run("nope"); r.Code != 1 || !strings.Contains(r.Stderr, "unknown command") {
		t.Errorf("unknown command: %+v", r)
	}
}

// mek ignores SIGINT while the child runs (in a terminal, Ctrl-C reaches the
// child directly) and forwards SIGTERM, so `timeout` or a CI cancel stops the
// child too instead of orphaning it.
func TestSignals(t *testing.T) {
	e := setup(t, config)
	cmd := testkit.Command(mek, e.vars, "-c", "uat", "exec", "--", "sh", "-c",
		`trap "exit 9" TERM; while :; do sleep 0.05; done`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	time.Sleep(500 * time.Millisecond)

	cmd.Process.Signal(syscall.SIGINT)
	select {
	case err := <-done:
		t.Fatalf("SIGINT stopped mek: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
		if code := cmd.ProcessState.ExitCode(); code != 9 {
			t.Errorf("exit %d, want 9 (child's TERM trap)", code)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("SIGTERM was not forwarded")
	}
}

// Many mek processes at once (parallel scripts, several terminals) must not
// trip over the shared generated AWS config.
func TestConcurrentInvocations(t *testing.T) {
	e := setup(t, config)
	var wg sync.WaitGroup
	errs := make(chan string, 40)
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := testkit.Run(t, mek, e.vars, "-c", "uat", "env"); r.Code != 0 {
				errs <- r.Stderr
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent run failed: %s", err)
	}
	entries, _ := os.ReadDir(filepath.Join(e.mekHome, "aws"))
	if len(entries) != 1 {
		t.Errorf("aws dir should hold only the config, has %v", entries)
	}
}

func TestShellIntegration(t *testing.T) {
	e := setup(t, config)
	script := `eval "$("$MEK" env uat)"; printf '%s %s' "$AWS_PROFILE" "$MEK_CONTEXT"`
	r := testkit.Run(t, "/bin/sh", append(e.vars, "MEK="+mek), "-c", script)
	if r.Stdout != "mek-uat uat" {
		t.Errorf("eval mek env: %q (stderr %q)", r.Stdout, r.Stderr)
	}

	// The shell-prompt command must stay read-only: no files written.
	fresh := setup(t, config).with("MEK_CONTEXT=uat")
	r = fresh.run("ctx", "--short")
	if r.Stdout != "uat\n" {
		t.Errorf("ctx --short: %q", r.Stdout)
	}
	if entries, _ := os.ReadDir(fresh.mekHome); len(entries) != 1 {
		t.Errorf("ctx --short wrote files: %v", entries)
	}
}

func TestLoginFlows(t *testing.T) {
	cases := []struct {
		ctx  string
		args []string
		want []string
	}{
		{"uat", nil, []string{"aws sso login --profile mek-uat", "aws sts get-caller-identity --output table"}},
		{"gcp", []string{"--adc"}, []string{"gcloud auth login", "gcloud auth application-default login", "gcloud auth list --filter=status:ACTIVE --format=value(account)"}},
	}
	for _, c := range cases {
		t.Run(c.ctx, func(t *testing.T) {
			e := setup(t, config)
			ok(t, e.run(append([]string{"login", c.ctx}, c.args...)...))
			var got []string
			for _, call := range e.calls() {
				got = append(got, call.Name+" "+call.ArgLine())
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("login ran:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(c.want, "\n  "))
			}
		})
	}
}

func TestFirstRun(t *testing.T) {
	e := setup(t, "")
	if r := e.run("ctx"); r.Code != 1 || !strings.Contains(r.Stderr, "mek init") {
		t.Errorf("before init: %+v", r)
	}
	ok(t, e.run("init"))
	ok(t, e.run("use", "example-uat"))
	if r := e.run("ctx", "--short"); r.Stdout != "example-uat\n" {
		t.Errorf("after use: %q", r.Stdout)
	}
	if r := e.run("ctx", "ls"); !strings.Contains(r.Stdout, "* example-uat") || !strings.Contains(r.Stdout, "[readonly]") {
		t.Errorf("ctx ls:\n%s", r.Stdout)
	}
	if r := e.run("version"); !strings.Contains(r.Stdout, "v0.0.0-integration") {
		t.Errorf("version: %q", r.Stdout)
	}
}

func TestDoctor(t *testing.T) {
	e := setup(t, "contexts:\n  uat: {provider: aws, aws_profile: p}\n  g: {provider: gcp, project: p}\n").
		with("STUB_OUT=stub-cli 1.0")
	r := e.run("doctor")
	if !strings.Contains(r.Stdout, "stub-cli 1.0") || !strings.Contains(r.Stdout, "2 contexts") {
		t.Errorf("doctor:\n%s", r.Stdout)
	}
	if r.Code != 0 {
		t.Errorf("all required CLIs are stubbed, want exit 0: %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	r = e.with("PATH=" + t.TempDir()).run("doctor") // runners preinstall some cloud CLIs; hide them all
	if r.Code != 1 || !strings.Contains(r.Stderr, "2 problem(s)") {
		t.Errorf("missing aws+gcloud: code %d stderr %q", r.Code, r.Stderr)
	}
}

// Each terminal can pin its own context without touching the saved one.
func TestUseShellPerTerminal(t *testing.T) {
	e := setup(t, config)
	ok(t, e.run("use", "uat"))
	script := `eval "$("$MEK" use --shell gcp)" && "$MEK" ctx --short && "$MEK" -c gcp gcloud config list`
	r := testkit.Run(t, "/bin/sh", append(e.vars, "MEK="+mek), "-c", script)
	if r.Stdout != "gcp\n" || r.Code != 0 {
		t.Errorf("this shell should use gcp: %q (exit %d, %s)", r.Stdout, r.Code, r.Stderr)
	}
	if r := e.run("ctx", "--short"); r.Stdout != "uat\n" { // another terminal
		t.Errorf("the saved context changed: %q", r.Stdout)
	}
}

func TestLoginFlagsReachTheCLI(t *testing.T) {
	e := setup(t, config)
	ok(t, e.run("login", "uat", "--", "--no-browser"))
	ok(t, e.run("login", "gcp", "--adc", "--", "--cred-file=key.json"))
	var got []string
	for _, c := range e.calls() {
		got = append(got, c.Name+" "+c.ArgLine())
	}
	want := []string{
		"aws sso login --profile mek-uat --no-browser",
		"aws sts get-caller-identity --output table",
		"gcloud auth login --cred-file=key.json", // only the login gets the flags
		"gcloud auth application-default login",
		"gcloud auth list --filter=status:ACTIVE --format=value(account)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("login ran:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
