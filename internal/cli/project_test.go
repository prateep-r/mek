package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

// projectHarness is testConfig plus a project, worked in from its root.
func projectHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t, testConfig)
	repo := t.TempDir()
	os.Mkdir(filepath.Join(repo, ".git"), 0o700)
	os.WriteFile(filepath.Join(repo, config.TeamFile), []byte(`contexts:
  prod: {provider: aws, aws_profile: team-prod}
  orders-dev: {provider: gcp, project: orders-dev}
`), 0o644)
	os.WriteFile(filepath.Join(repo, config.LocalFile), []byte(`contexts:
  gcp: {provider: gcp, project: my-sandbox}
`), 0o644)
	t.Chdir(repo)
	return h, repo
}

func TestProjectContexts(t *testing.T) {
	h, _ := projectHarness(t)

	// The team's prod replaces yours — said every time — and stays protected.
	out := stderr(t, func() { h.mustRun("-c", "prod", "aws", "s3", "ls") })
	for _, want := range []string{"⚠ prod from ./mek.yaml replaces your prod in", "● prod (./mek.yaml) [protected] aws profile team-prod"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q:\n%s", want, out)
		}
	}
	// Your mek.local.yaml replacing your own: shown, not warned about.
	out = stderr(t, func() { h.mustRun("-c", "gcp", "gcloud", "config", "list") })
	if !strings.Contains(out, "● gcp (./mek.local.yaml) gcp my-sandbox / -") || strings.Contains(out, "⚠") {
		t.Errorf("local banner:\n%s", out)
	}
	// ~/.mek/config.yaml's contexts look as before.
	if out := stderr(t, func() { h.mustRun("-c", "uat", "aws", "s3", "ls") }); out != "" {
		t.Errorf("global context: %q", out)
	}

	es := readAudit(t)
	if len(es) != 3 || !strings.HasSuffix(es[0].Config, config.TeamFile) || !strings.HasSuffix(es[1].Config, config.LocalFile) || es[2].Config != "" {
		t.Errorf("audit config: %+v", es)
	}

	ls := h.mustRun("ctx", "ls")
	for _, want := range []string{"orders-dev", "(./mek.yaml)", "(./mek.local.yaml)"} {
		if !strings.Contains(ls, want) {
			t.Errorf("ctx ls missing %q:\n%s", want, ls)
		}
	}
	if out := h.mustRun("-c", "orders-dev", "ctx"); !strings.Contains(out, "orders-dev (./mek.yaml)") {
		t.Errorf("ctx: %q", out)
	}
	if out := h.mustRun("--no-local", "ctx", "ls"); strings.Contains(out, "orders-dev") {
		t.Errorf("--no-local:\n%s", out)
	}
}

func TestInitProjectFiles(t *testing.T) {
	h, repo := projectHarness(t)
	os.Remove(filepath.Join(repo, config.TeamFile))
	os.Remove(filepath.Join(repo, config.LocalFile))
	out := stderr(t, func() { h.mustRun("init", "--project") })
	if !strings.Contains(out, "created ./mek.yaml") || !strings.Contains(out, "commit it") {
		t.Errorf("--project: %s", out)
	}
	out = stderr(t, func() { h.mustRun("init", "--local") })
	if !strings.Contains(out, "created ./mek.local.yaml") || !strings.Contains(out, "added mek.local.yaml to ./.gitignore") {
		t.Errorf("--local: %s", out)
	}
	_, err := h.run("init", "--local")
	wantErr(t, err, "already exists")
	_, err = h.run("init", "--project", "--local")
	wantErr(t, err, "pick one")
}

func TestDoctorProjectFiles(t *testing.T) {
	h, _ := projectHarness(t)
	stubTools(t, nil)
	old := gitTracked
	t.Cleanup(func() { gitTracked = old })
	gitTracked = func(string, string) bool { return false }
	out, _ := h.run("doctor")
	for _, want := range []string{"✓ ./mek.local.yaml — 1 contexts, replacing gcp from below", "✓ ./mek.yaml — 2 contexts, replacing prod from below",
		"config.yaml — 2 contexts"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor missing %q:\n%s", want, out)
		}
	}
	gitTracked = func(string, string) bool { return true }
	out, err := h.run("doctor")
	if !strings.Contains(out, "✗ ./mek.local.yaml is committed") {
		t.Errorf("tracked:\n%s", out)
	}
	wantErr(t, err, "problem(s)")

	// A teammate with no config of their own.
	os.Remove(config.Path())
	gitTracked = func(string, string) bool { return false }
	if out, _ := h.run("doctor"); !strings.Contains(out, "config.yaml — none (the project's files have the contexts)") {
		t.Errorf("no global:\n%s", out)
	}
	if gitTracked = old; old("/nonexistent", config.LocalFile) { // the real one, outside any repository
		t.Error("tracked outside a repository")
	}
}
