package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/ui"
)

// osPath writes ./x with this OS's separator, as mek shows paths.
func osPath(s string) string { return strings.ReplaceAll(s, "./", "."+string(filepath.Separator)) }

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
	for _, want := range []string{osPath("⚠ prod from ./mek.yaml replaces your prod in"), osPath("● prod (./mek.yaml) [protected] aws profile team-prod")} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q:\n%s", want, out)
		}
	}
	// Your mek.local.yaml replacing your own: shown, not warned about.
	out = stderr(t, func() { h.mustRun("-c", "gcp", "gcloud", "config", "list") })
	if !strings.Contains(out, osPath("● gcp (./mek.local.yaml) gcp my-sandbox / -")) || strings.Contains(out, "⚠") {
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
	for _, want := range []string{"orders-dev", osPath("(./mek.yaml)"), osPath("(./mek.local.yaml)")} {
		if !strings.Contains(ls, want) {
			t.Errorf("ctx ls missing %q:\n%s", want, ls)
		}
	}
	if out := h.mustRun("-c", "orders-dev", "ctx"); !strings.Contains(out, osPath("orders-dev (./mek.yaml)")) {
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
	if !strings.Contains(out, osPath("created ./mek.yaml")) || !strings.Contains(out, "commit it") {
		t.Errorf("--project: %s", out)
	}
	out = stderr(t, func() { h.mustRun("init", "--local") })
	if !strings.Contains(out, osPath("created ./mek.local.yaml")) || !strings.Contains(out, osPath("added mek.local.yaml to ./.gitignore")) {
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
	for _, want := range []string{osPath("✓ ./mek.local.yaml — 1 contexts, replacing gcp from below"), osPath("✓ ./mek.yaml — 2 contexts, replacing prod from below"),
		"config.yaml — 2 contexts"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor missing %q:\n%s", want, out)
		}
	}
	gitTracked = func(string, string) bool { return true }
	out, err := h.run("doctor")
	if !strings.Contains(out, osPath("✗ ./mek.local.yaml is committed")) {
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

func TestCtxRm(t *testing.T) {
	h, repo := projectHarness(t)
	old := confirm
	t.Cleanup(func() { confirm = old })
	answers := []bool{false, true}
	confirm = func(q string) (bool, error) {
		if !strings.Contains(q, "Remove context gcp from ") {
			t.Errorf("question: %s", q)
		}
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	h.mustRun("ctx", "rm", "gcp") // declined: kept
	if b, _ := os.ReadFile(filepath.Join(repo, config.LocalFile)); !strings.Contains(string(b), "gcp:") {
		t.Error("removed though declined")
	}
	out := stderr(t, func() { h.mustRun("context", "rm", "gcp") }) // "context" is ctx too
	if !strings.Contains(out, osPath("removed gcp from ./mek.local.yaml")) || !strings.Contains(out, "is used again") {
		t.Errorf("rm: %s", out)
	}
	if out := h.mustRun("-c", "gcp", "ctx"); strings.Contains(out, "my-sandbox") || !strings.Contains(out, "acme-dev") {
		t.Errorf("after: %s", out) // yours from ~/.mek/config.yaml again
	}

	confirm = func(string) (bool, error) { return false, ui.ErrNoTTY }
	_, err := h.run("ctx", "rm", "orders-dev")
	wantErr(t, err, "pass --yes")
	out = stderr(t, func() { h.mustRun("ctx", "rm", "orders-dev", "--yes") })
	if !strings.Contains(out, osPath("removed orders-dev from ./mek.yaml")) {
		t.Errorf("--yes: %s", out)
	}
	h.mustRun("use", "uat")
	if out := stderr(t, func() { h.mustRun("ctx", "rm", "uat", "--yes") }); !strings.Contains(out, "it was the current context") {
		t.Errorf("current: %s", out)
	}
	_, err = h.run("ctx", "rm", "nope", "--yes")
	wantErr(t, err, "unknown context")

}

func TestSaveEdit(t *testing.T) {
	_, repo := projectHarness(t)
	os.Remove(filepath.Join(repo, config.LocalFile))
	local := filepath.Join(repo, config.LocalFile)

	// Invalid as a whole: nothing written.
	e, _ := config.Open(local, nil, 0o644)
	e.Set("bad", &config.Context{Provider: "aws"}) // no profile, no SSO
	if err := saveEdit(e); err == nil || !strings.Contains(err.Error(), "not saved") {
		t.Errorf("invalid: %v", err)
	}
	if _, err := os.Stat(local); err == nil {
		t.Error("written anyway")
	}

	// A new mek.local.yaml goes into .gitignore.
	e, _ = config.Open(local, nil, 0o644)
	e.Set("mine", &config.Context{Provider: "gcp", Project: "m"})
	out := stderr(t, func() {
		if err := saveEdit(e); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "added mek.local.yaml to") {
		t.Errorf("gitignore: %q", out)
	}
	e.Set("more", &config.Context{Provider: "gcp", Project: "n"}) // not new any more: .gitignore alone
	if out := stderr(t, func() { saveEdit(e) }); out != "" {
		t.Errorf("second save: %q", out)
	}

	// A .gitignore that can't be read is reported.
	os.Remove(local)
	os.Remove(filepath.Join(repo, ".gitignore"))
	os.Mkdir(filepath.Join(repo, ".gitignore"), 0o700)
	e, _ = config.Open(local, nil, 0o644)
	e.Set("mine", &config.Context{Provider: "gcp", Project: "m"})
	if err := saveEdit(e); err == nil {
		t.Error(".gitignore error")
	}
	// And a file that can't be written (a read-only directory: not on Windows).
	if runtime.GOOS != "windows" {
		ro := t.TempDir()
		os.Chmod(ro, 0o500)
		t.Cleanup(func() { os.Chmod(ro, 0o700) })
		e, _ = config.Open(filepath.Join(ro, "other.yaml"), nil, 0o644)
		if err := saveEdit(e); err == nil {
			t.Error("unwritable")
		}
	}
}

func TestEditConfig(t *testing.T) {
	_, repo := projectHarness(t)
	boom := errors.New("boom")
	dir := filepath.Join(repo, "adir")
	os.Mkdir(dir, 0o700)
	if err := editConfig(dir, nil, func(*config.Editor) error { return nil }); err == nil { // can't be read
		t.Error("open error")
	}
	if err := editConfig(filepath.Join(repo, config.TeamFile), nil, func(*config.Editor) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("change error: %v", err)
	}
	h, _ := projectHarness(t) // a broken config: rm can't load it
	os.WriteFile(config.Path(), []byte("contexts: ["), 0o600)
	_, err := h.run("ctx", "rm", "uat", "--yes")
	wantErr(t, err, "parse")
}
