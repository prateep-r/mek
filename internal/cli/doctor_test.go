package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/prateep-r/mek/test/testkit"
)

// fakeTool is what a fake CLI prints and exits with.
type fakeTool struct {
	out, err string
	code     int
}

// stubTools puts fake CLIs (testkit's recordstub, on every OS) alone on PATH
// and returns their directory.
func stubTools(t *testing.T, tools map[string]fakeTool) string {
	t.Helper()
	bin := t.TempDir()
	stub := testkit.Tool(t, "recordstub")
	for name, tl := range tools {
		exe := filepath.Join(bin, testkit.Exe(name))
		if err := testkit.Install(stub, exe); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(exe+".stdout", []byte(tl.out), 0o600)
		os.WriteFile(exe+".stderr", []byte(tl.err), 0o600)
		os.WriteFile(exe+".exit", []byte(strconv.Itoa(tl.code)), 0o600)
	}
	t.Setenv("PATH", bin)
	return bin
}

func setGOOS(t *testing.T, os string) {
	t.Helper()
	old := goos
	goos = os
	t.Cleanup(func() { goos = old })
}

func TestDoctorAllGood(t *testing.T) {
	h := newHarness(t, "contexts:\n  uat: {provider: aws, aws_profile: p}\n")
	stubTools(t, map[string]fakeTool{
		"aws":     {out: "aws-cli/2.17.0 Python/3.12"},
		"kubectl": {code: 1},                      // no output: version unknown
		"k9s":     {out: strings.Repeat("v", 80)}, // long: truncated
	})
	h.mustRun("use", "uat")
	out := h.mustRun("doctor")
	for _, want := range []string{"1 contexts, current: uat", "aws-cli/2.17.0", "(version unknown)", strings.Repeat("v", 60) + "…", "all good"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorProblems(t *testing.T) {
	stubTools(t, nil) // nothing installed

	setGOOS(t, "darwin")
	h := newHarness(t, testConfig) // needs aws and gcloud
	out, err := h.run("doctor")
	wantErr(t, err, "2 problem(s)")
	for _, want := range []string{"current: (none", "✗ aws", "required", "brew install awscli", "https://cloud.google.com/sdk/docs/install"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}

	setGOOS(t, "linux") // no brew hints off macOS
	h = newHarness(t, "")
	out, err = h.run("doctor")
	wantErr(t, err, "1 problem(s)") // just the missing config; CLIs are optional
	if !strings.Contains(out, "run `mek init`") || strings.Contains(out, "brew install") {
		t.Errorf("no-config output:\n%s", out)
	}

	h = newHarness(t, "contexts:\n  a: {provider: gcp}\n")
	_, err = h.run("doctor")
	wantErr(t, err, "1 problem(s)")

	setGOOS(t, "windows") // winget hints, where there is a package
	h = newHarness(t, testConfig)
	out, _ = h.run("doctor")
	for _, want := range []string{"winget install Amazon.AWSCLI", "winget install Google.CloudSDK", "winget install Derailed.k9s"} {
		if !strings.Contains(out, want) {
			t.Errorf("windows: missing %q:\n%s", want, out)
		}
	}
}

// doctor finds tools it must never run bare (ssh-keygen would start making a
// key), checks versions (AWS CLI v1 can't do SSO logins), and requires ssh
// only when GCP shells or host tunnels are configured.
func TestDoctorChecks(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("STUB_LOG", log) // every stub that runs records itself
	bin := stubTools(t, map[string]fakeTool{
		"aws":        {out: "aws-cli/1.33.0 Python/3.11"},
		"gcloud":     {err: "WARNING: login expired", out: "Google Cloud SDK 500.0.0"},
		"ssh":        {err: "OpenSSH_9.8p1"},
		"ssh-keygen": {},
	})
	h := newHarness(t, "contexts:\n  a: {provider: aws, sso_start_url: \"https://o.awsapps.com/start\", sso_region: r, account_id: \"111122223333\", role: R}\n  g: {provider: gcp, project: p, targets: {web: {instance: web-1}}}\n")
	out, err := h.run("doctor")
	wantErr(t, err, "1 problem(s)") // only the AWS CLI version: ssh and ssh-keygen are there (or not needed: Windows)
	for _, want := range []string{"✗ aws", "AWS CLI v1 can't do IAM Identity Center logins", "✓ ssh ", "OpenSSH_9.8p1", "✓ ssh-keygen               " + bin, "Google Cloud SDK 500.0.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "login expired") {
		t.Errorf("doctor showed a stderr warning as the version:\n%s", out)
	}
	for _, c := range testkit.Calls(t, log) {
		if c.Name == "ssh-keygen" {
			t.Error("doctor ran ssh-keygen")
		}
	}

	// Not needed: a version problem is a warning, not a failure.
	h = newHarness(t, "contexts:\n  g: {provider: gcp, project: p}\n")
	out, err = h.run("doctor")
	if err != nil || !strings.Contains(out, "! aws") {
		t.Errorf("optional aws v1: %v\n%s", err, out)
	}
}

func TestDoctorRequiresSSHForGCPShells(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gcloud brings its own PuTTY on Windows")
	}
	stubTools(t, map[string]fakeTool{"gcloud": {out: "gcloud"}})
	h := newHarness(t, "contexts:\n  g: {provider: gcp, project: p, tunnels: {pg: {via: web-1, host: 10.0.0.5, port: 5432}}}\n")
	out, err := h.run("doctor")
	wantErr(t, err, "2 problem(s)")
	if !strings.Contains(out, "✗ ssh ") || !strings.Contains(out, "✗ ssh-keygen") || !strings.Contains(out, "openssh") {
		t.Errorf("ssh required:\n%s", out)
	}
	h = newHarness(t, "contexts:\n  g: {provider: gcp, project: p, tunnels: {app: {via: web-1, port: 8080}}}\n")
	if _, err := h.run("doctor"); err != nil { // IAP port tunnels don't use ssh
		t.Errorf("ssh needed for an IAP port tunnel: %v", err)
	}
}
