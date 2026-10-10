package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubTools puts fake CLIs on an otherwise empty PATH. Each value is the
// shell body of the stub.
func stubTools(t *testing.T, tools map[string]string) {
	t.Helper()
	bin := t.TempDir()
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

func setGOOS(t *testing.T, os string) {
	t.Helper()
	old := goos
	goos = os
	t.Cleanup(func() { goos = old })
}

func TestDoctorAllGood(t *testing.T) {
	h := newHarness(t, "contexts:\n  uat: {provider: aws, aws_profile: p}\n")
	stubTools(t, map[string]string{
		"aws":     `echo "aws-cli/2.17.0 Python/3.12"`,
		"kubectl": "exit 1",                                 // no output: version unknown
		"k9s":     `echo "` + strings.Repeat("v", 80) + `"`, // long: truncated
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
}

// doctor finds tools it must never run bare (ssh-keygen would start making a
// key), checks versions (AWS CLI v1 can't do SSO logins), and requires ssh
// only when GCP shells or host tunnels are configured.
func TestDoctorChecks(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	stubTools(t, map[string]string{
		"aws":        `echo "aws-cli/1.33.0 Python/3.11"`,
		"gcloud":     `echo "WARNING: login expired" >&2; echo "Google Cloud SDK 500.0.0"`,
		"ssh":        `echo "OpenSSH_9.8p1" >&2`,
		"ssh-keygen": "touch " + marker,
	})
	h := newHarness(t, "contexts:\n  a: {provider: aws, sso_start_url: \"https://o.awsapps.com/start\", sso_region: r, account_id: \"111122223333\", role: R}\n  g: {provider: gcp, project: p, targets: {web: {instance: web-1}}}\n")
	out, err := h.run("doctor")
	wantErr(t, err, "1 problem(s)") // only the AWS CLI version: ssh and ssh-keygen are there
	for _, want := range []string{"✗ aws", "AWS CLI v1 can't do IAM Identity Center logins", "✓ ssh ", "OpenSSH_9.8p1", "✓ ssh-keygen               /", "Google Cloud SDK 500.0.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "login expired") {
		t.Errorf("doctor showed a stderr warning as the version:\n%s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("doctor ran ssh-keygen")
	}

	// Not needed: a version problem is a warning, not a failure.
	h = newHarness(t, "contexts:\n  g: {provider: gcp, project: p}\n")
	out, err = h.run("doctor")
	if err != nil || !strings.Contains(out, "! aws") {
		t.Errorf("optional aws v1: %v\n%s", err, out)
	}
}

func TestDoctorRequiresSSHForGCPShells(t *testing.T) {
	stubTools(t, map[string]string{"gcloud": "echo gcloud"})
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
