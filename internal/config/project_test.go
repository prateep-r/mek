package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// layered is a home with ~/.mek/config.yaml and a git repository with a
// project; it starts in the project's subdirectory.
func layered(t *testing.T, global, team, local string) (repo string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MEK_HOME", filepath.Join(home, ".mek"))
	t.Setenv("MEK_NO_LOCAL", "")
	oldFlag, oldNo := ProjectFlag, NoProject
	t.Cleanup(func() { ProjectFlag, NoProject = oldFlag, oldNo })
	ProjectFlag, NoProject = "", false
	os.MkdirAll(filepath.Join(home, ".mek"), 0o700)
	repo = filepath.Join(home, "work", "orders")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o700)
	os.MkdirAll(filepath.Join(repo, "src", "api"), 0o700)
	for path, body := range map[string]string{filepath.Join(home, ".mek", "config.yaml"): global,
		filepath.Join(repo, TeamFile): team, filepath.Join(repo, LocalFile): local} {
		if body != "" {
			os.WriteFile(path, []byte(body), 0o600)
		}
	}
	t.Chdir(filepath.Join(repo, "src", "api"))
	return repo
}

const (
	globalYAML = `contexts:
  dev:  {provider: aws, aws_profile: me-dev}
  prod: {provider: aws, aws_profile: prod, protected: true}
  mine: {provider: gcp, project: p}
`
	teamYAML = `contexts:
  dev:  {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Developer}
  prod: {provider: aws, aws_profile: prod}
  orders-dev: {provider: gcp, project: orders-dev}
`
	localYAML = `contexts:
  dev:  {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Admin}
`
)

func TestLayers(t *testing.T) {
	repo := layered(t, globalYAML, teamYAML, localYAML)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != repo || len(c.Files) != 3 || c.Files[0] != filepath.Join(repo, LocalFile) {
		t.Errorf("project %s, files %v", c.Project, c.Files)
	}
	dev, prod, orders, mine := c.Contexts["dev"], c.Contexts["prod"], c.Contexts["orders-dev"], c.Contexts["mine"]
	// Whole from the top layer: the local role, nothing of the global profile.
	if dev.Role != "Admin" || dev.AWSProfile != "" || dev.Source != filepath.Join(repo, LocalFile) || !dev.InProject ||
		dev.Shadowed != filepath.Join(repo, TeamFile) {
		t.Errorf("dev: %+v", dev)
	}
	// The team's prod replaces yours, but your guard stays on.
	if !prod.Protected || prod.Source != filepath.Join(repo, TeamFile) || prod.Shadowed != Path() {
		t.Errorf("prod: %+v", prod)
	}
	if orders.Source != filepath.Join(repo, TeamFile) || orders.Shadowed != "" || mine.InProject || mine.Source != Path() {
		t.Errorf("orders %+v, mine %+v", orders, mine)
	}

	// A higher layer can add a guard.
	layered(t, globalYAML, "contexts:\n  mine: {provider: gcp, project: p, readonly: true}\n", "")
	if c, _ := Load(); !c.Contexts["mine"].ReadOnly {
		t.Error("readonly from the project")
	}
}

func TestProjectOptions(t *testing.T) {
	repo := layered(t, globalYAML, teamYAML, "")
	NoProject = true
	if c, _ := Load(); c.Project != "" || c.Contexts["orders-dev"] != nil {
		t.Error("--no-local read the project")
	}
	NoProject = false
	t.Setenv("MEK_NO_LOCAL", "1")
	if c, _ := Load(); c.Contexts["orders-dev"] != nil {
		t.Error("MEK_NO_LOCAL read the project")
	}
	t.Setenv("MEK_NO_LOCAL", "")
	t.Chdir(t.TempDir()) // elsewhere: --project names it
	ProjectFlag = repo
	if c, _ := Load(); c.Contexts["orders-dev"] == nil || c.Project != repo {
		t.Error("--project")
	}
	ProjectFlag = ""
	if c, _ := Load(); c.Contexts["orders-dev"] != nil {
		t.Error("project read from outside it")
	}
}

func TestOnlyProjectFiles(t *testing.T) {
	layered(t, "", teamYAML, "") // a teammate without a config of their own
	c, err := Load()
	if err != nil || c.Contexts["orders-dev"] == nil {
		t.Fatalf("%+v %v", c, err)
	}
	t.Chdir(t.TempDir())
	if _, err := Load(); !errors.Is(err, ErrNoConfig) {
		t.Errorf("nothing anywhere: %v", err)
	}
}

func TestProjectErrorsNameTheFile(t *testing.T) {
	layered(t, globalYAML, "contexts:\n  dev: {provider: aws, aws_profile: x, tunel: {}}\n", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), TeamFile) || !strings.Contains(err.Error(), "tunel") {
		t.Errorf("unknown field: %v", err)
	}
	layered(t, globalYAML, "contexts:\n  bad name: {provider: aws, aws_profile: x}\n", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), `context "bad name" (`) || !strings.Contains(err.Error(), TeamFile) {
		t.Errorf("invalid context: %v", err)
	}
	layered(t, globalYAML, "contexts:\n  empty:\n", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), `context "empty" is empty`) {
		t.Errorf("empty context: %v", err)
	}
	repo := layered(t, globalYAML, "", "")
	os.Mkdir(filepath.Join(repo, LocalFile), 0o700) // unreadable as a file
	if _, err := Load(); err == nil {
		t.Error("unreadable local file")
	}
}

func TestFindProject(t *testing.T) {
	repo := layered(t, globalYAML, teamYAML, "")
	home, _ := os.UserHomeDir()
	for start, want := range map[string]string{
		filepath.Join(repo, "src", "api"): repo, // up from a subdirectory
		repo:                              repo,
		filepath.Join(home, "work"):       "", // no project, not a repository: stops before home
		home:                              "", // never home itself
	} {
		if got := FindProject(start); got != want {
			t.Errorf("FindProject(%s) = %q, want %q", start, got, want)
		}
	}
	// A repository's root ends the search: a mek.yaml above it isn't this one's.
	inner := filepath.Join(repo, "vendor", "lib")
	os.MkdirAll(filepath.Join(inner, ".git"), 0o700)
	if got := FindProject(inner); got != "" {
		t.Errorf("past a repository root: %q", got)
	}
	if got := FindProject(filepath.VolumeName(home) + string(filepath.Separator)); got != "" { // the root: nothing above
		t.Errorf("root: %q", got)
	}
}

func TestKey(t *testing.T) {
	a := &Context{Name: "dev", Provider: "aws", SSOStartURL: "u", AccountID: "111122223333", Role: "R", InProject: true}
	b := *a
	b.Tunnels = map[string]*Tunnel{"db": {}} // not who it is
	c := *a
	c.AccountID = "444455556666"
	if a.Key() == "dev" || a.Key() != b.Key() || a.Key() == c.Key() || !strings.HasPrefix(a.Key(), "dev-") || len(a.Key()) != len("dev-")+8 {
		t.Errorf("keys: %s %s %s", a.Key(), b.Key(), c.Key())
	}
	if (&Context{Name: "dev"}).Key() != "dev" { // ~/.mek/config.yaml: the name, as before
		t.Error("global key")
	}
}

func TestShow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(home)
	if got := Show(filepath.Join(home, "a", "mek.yaml")); got != "."+string(filepath.Separator)+filepath.Join("a", "mek.yaml") {
		t.Errorf("under cwd: %s", got)
	}
	t.Chdir(t.TempDir())
	if got := Show(filepath.Join(home, ".mek", "config.yaml")); got != "~"+string(filepath.Separator)+filepath.Join(".mek", "config.yaml") {
		t.Errorf("home: %s", got)
	}
	if got := Show(string(filepath.Separator) + "x"); !strings.HasSuffix(got, "x") {
		t.Errorf("elsewhere: %s", got)
	}
}

func TestResolveOutsideTheProject(t *testing.T) {
	layered(t, globalYAML, teamYAML, "")
	c, _ := Load()
	t.Setenv("MEK_CONTEXT", "nowhere")
	if _, err := c.Resolve(""); err == nil || !strings.Contains(err.Error(), "may be a project's (mek.yaml)") {
		t.Errorf("saved elsewhere: %v", err)
	}
	if _, err := c.Resolve("nope"); err == nil || !strings.Contains(err.Error(), `unknown context "nope"`) {
		t.Errorf("-c: %v", err)
	}
}
