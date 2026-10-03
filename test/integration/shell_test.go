//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const shellConfig = `contexts:
  prod: {provider: aws, aws_profile: admin, region: ap-southeast-1, protected: true, targets: {bastion: {instance: "tag:Name=bastion"}}}
  ro:   {provider: aws, aws_profile: viewer, readonly: true}
  gcp:  {provider: gcp, project: my-project, targets: {web: {instance: web-1, zone: asia-southeast1-b}}}
`

// shellSetup is setup plus a session-manager-plugin stub.
func shellSetup(t *testing.T) *env {
	t.Helper()
	e := setup(t, shellConfig)
	stubs := strings.SplitN(strings.TrimPrefix(e.vars[2], "PATH="), ":", 2)[0]
	b, _ := os.ReadFile(filepath.Join(stubs, "aws"))
	os.WriteFile(filepath.Join(stubs, "session-manager-plugin"), b, 0o755)
	return e
}

func TestShellAWSSession(t *testing.T) {
	e := shellSetup(t)
	r := e.with(`STUB_OUT=["i-0bastion0"]`, "STUB_EXIT=0").run("-c", "prod", "--yes", "shell", "bastion")
	if r.Code != 0 {
		t.Fatalf("shell: %+v", r)
	}
	calls := e.calls()
	if len(calls) != 2 || !strings.HasPrefix(calls[0].ArgLine(), "ec2 describe-instances --filters [{") ||
		calls[1].ArgLine() != "ssm start-session --target i-0bastion0" || calls[1].Env["AWS_PROFILE"] != "admin" {
		t.Fatalf("calls: %+v", calls)
	}
	a := e.audit()
	if len(a) != 3 || a[1]["event"] != "start" || a[2]["event"] != "end" || a[1]["session"] != a[2]["session"] ||
		a[2]["target"] != "bastion → i-0bastion0" || a[2]["class"] != "shell" {
		t.Errorf("audit: %v", a)
	}

	// The session's exit code reaches the caller and the end entry.
	r = e.with("STUB_EXIT=7").run("-c", "prod", "--yes", "shell", "i-0123456789abcdef0")
	if r.Code != 7 || e.audit()[4]["exit_code"] != float64(7) {
		t.Errorf("exit code: %+v %v", r, e.audit()[4])
	}
}

func TestShellGuardFailsClosed(t *testing.T) {
	e := shellSetup(t)
	if r := e.run("-c", "prod", "shell", "i-0123456789abcdef0"); r.Code == 0 || !strings.Contains(r.Stderr, "--yes") {
		t.Errorf("protected shell without a terminal must not run: %+v", r)
	}
	if r := e.run("-c", "ro", "shell", "i-0123456789abcdef0"); r.Code == 0 || !strings.Contains(r.Stderr, "readonly") {
		t.Errorf("readonly shell: %+v", r)
	}
	if c := e.calls(); len(c) != 0 {
		t.Errorf("nothing may run: %+v", c)
	}
}

func TestShellPluginMissing(t *testing.T) {
	e := setup(t, shellConfig) // no session-manager-plugin
	r := e.run("-c", "prod", "--yes", "shell", "i-0123456789abcdef0")
	if r.Code == 0 || !strings.Contains(r.Stderr, "session-manager-plugin is needed") {
		t.Errorf("missing plugin: %+v", r)
	}
}

// gcloud compute ssh gets HOME = the context's SSH dir (known hosts) and
// keeps CLOUDSDK_CONFIG on the context's gcloud config.
func TestShellGCPKeepsSSHFilesInMek(t *testing.T) {
	e := shellSetup(t)
	if r := e.run("-c", "gcp", "shell", "web"); r.Code != 0 {
		t.Fatalf("shell: %+v", r)
	}
	c := e.calls()[0]
	ssh := filepath.Join(e.mekHome, "ssh", "gcp")
	if c.Name != "gcloud" || c.ArgLine() != "compute ssh web-1 --zone asia-southeast1-b --tunnel-through-iap --ssh-key-file "+filepath.Join(ssh, "google_compute_engine") ||
		c.Env["HOME"] != ssh || c.Env["CLOUDSDK_CONFIG"] != filepath.Join(e.mekHome, "gcloud", "gcp") {
		t.Errorf("gcloud ssh: %+v", c)
	}
}
