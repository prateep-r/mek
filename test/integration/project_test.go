//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/test/testkit"
)

// runIn runs mek in dir, as a user working there would.
func (e *env) runIn(dir string, args ...string) testkit.Result {
	e.t.Helper()
	cmd := testkit.Command(mek, e.vars, args...)
	cmd.Dir = dir
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return testkit.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
}

// repo makes a git repository with a project's files.
func repo(t *testing.T, team, local string) string {
	t.Helper()
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".git"), 0o700)
	os.MkdirAll(filepath.Join(dir, "src"), 0o700)
	os.WriteFile(filepath.Join(dir, "mek.yaml"), []byte(team), 0o600)
	if local != "" {
		os.WriteFile(filepath.Join(dir, "mek.local.yaml"), []byte(local), 0o600)
	}
	return dir
}

const teamDev = `contexts:
  dev: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "%s", role: Developer}
`

func TestProjectConfig(t *testing.T) {
	e := setup(t, "contexts:\n  dev: {provider: aws, aws_profile: me-dev}\n  prod: {provider: aws, aws_profile: prod, protected: true}\n")
	a := repo(t, strings.Replace(teamDev, "%s", "111122223333", 1), "")
	b := repo(t, strings.Replace(teamDev, "%s", "444455556666", 1),
		strings.Replace(strings.Replace(teamDev, "%s", "444455556666", 1), "Developer", "Admin", 1))

	// From a subdirectory of each project, dev is that project's.
	ok(t, e.runIn(filepath.Join(a, "src"), "-c", "dev", "aws", "sts", "get-caller-identity"))
	ok(t, e.runIn(filepath.Join(b, "src"), "-c", "dev", "aws", "sts", "get-caller-identity"))
	ok(t, e.runIn(t.TempDir(), "-c", "dev", "aws", "sts", "get-caller-identity")) // outside: yours
	calls := e.calls()
	if len(calls) != 3 {
		t.Fatalf("calls: %+v", calls)
	}
	profA, profB, mine := calls[0].Env["AWS_PROFILE"], calls[1].Env["AWS_PROFILE"], calls[2].Env["AWS_PROFILE"]
	if profA == profB || !strings.HasPrefix(profA, "mek-dev-") || mine != "me-dev" ||
		calls[0].Env["AWS_CONFIG_FILE"] == calls[1].Env["AWS_CONFIG_FILE"] {
		t.Errorf("profiles %s %s %s, files %s %s", profA, profB, mine, calls[0].Env["AWS_CONFIG_FILE"], calls[1].Env["AWS_CONFIG_FILE"])
	}
	// mek.local.yaml won in b: its role is in b's generated config.
	cfgB, _ := os.ReadFile(calls[1].Env["AWS_CONFIG_FILE"])
	if !strings.Contains(string(cfgB), "sso_role_name = Admin") || !strings.Contains(string(cfgB), "sso_account_id = 444455556666") {
		t.Errorf("b's config:\n%s", cfgB)
	}
	// --project reaches a project from anywhere (kubectl runs mek kube token
	// in whatever directory it has).
	ok(t, e.runIn(t.TempDir(), "--project", a, "-c", "dev", "aws", "sts", "get-caller-identity"))
	if c := e.calls()[3]; c.Env["AWS_PROFILE"] != profA {
		t.Errorf("--project: %s", c.Env["AWS_PROFILE"])
	}
	// --no-local: only your own contexts.
	ok(t, e.runIn(filepath.Join(a, "src"), "--no-local", "-c", "dev", "aws", "sts", "get-caller-identity"))
	if c := e.calls()[4]; c.Env["AWS_PROFILE"] != "me-dev" {
		t.Errorf("--no-local: %s", c.Env["AWS_PROFILE"])
	}
}
