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
	h := newHarness(t, testConfig) // needs aws, gcloud, az, hcloud
	out, err := h.run("doctor")
	wantErr(t, err, "4 problem(s)")
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
