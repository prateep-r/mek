package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const commented = `# my contexts — keep this comment
contexts:
  # dev is mine
  dev: {provider: aws, aws_profile: dev}

  # prod: careful
  prod:
    provider: aws
    aws_profile: prod
    protected: true   # always
# a foot comment
`

func editFile(t *testing.T, body string) (*Editor, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if body != "" {
		os.WriteFile(p, []byte(body), 0o640)
	}
	e, err := Open(p, []byte("contexts:\n  # examples\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return e, p
}

func TestEditorKeepsTheRest(t *testing.T) {
	e, p := editFile(t, commented)
	sso := &Context{Provider: "aws", SSOStartURL: "https://a.awsapps.com/start", SSORegion: "ap-southeast-1", AccountID: "011122223333", Role: "Dev"}
	if err := e.Set("uat", sso); err != nil {
		t.Fatal(err)
	}
	if err := e.Set("dev", &Context{Provider: "aws", AWSProfile: "dev2"}); err != nil {
		t.Fatal(err)
	}
	got := string(e.Bytes())
	for _, keep := range []string{"# my contexts — keep this comment\n", "  # dev is mine\n", "  # prod: careful\n",
		"    protected: true   # always\n", "# a foot comment\n", "    aws_profile: dev2\n", `account_id: "011122223333"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("missing %q:\n%s", keep, got)
		}
	}
	if strings.Index(got, "uat:") < strings.Index(got, "protected: true") || strings.Index(got, "uat:") > strings.Index(got, "# a foot") {
		t.Errorf("uat not after prod, before the foot comment:\n%s", got)
	}
	c, err := decode(e.Bytes(), p, false)
	if err != nil || c.Contexts["uat"].AccountID != "011122223333" || c.Contexts["dev"].AWSProfile != "dev2" || !c.Contexts["prod"].Protected {
		t.Fatalf("decode: %+v %v", c, err)
	}

	if err := e.Delete("prod"); err != nil {
		t.Fatal(err)
	}
	got = string(e.Bytes())
	if strings.Contains(got, "prod") || !strings.Contains(got, "# a foot comment") || !strings.Contains(got, "# dev is mine") {
		t.Errorf("after delete (its own comment goes, others stay):\n%s", got)
	}
	if err := e.Delete("prod"); err == nil || !strings.Contains(err.Error(), `has no context "prod"`) {
		t.Errorf("delete twice: %v", err)
	}
	if err := e.Save(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != got {
		t.Error("saved bytes differ")
	}
	if fi, _ := os.Stat(p); goos != "windows" && fi.Mode().Perm() != 0o640 { // its mode kept
		t.Errorf("mode %v", fi.Mode().Perm())
	}
}

func TestEditorShapes(t *testing.T) {
	c := &Context{Provider: "gcp", Project: "p"}
	// A new file starts from the template; its comments stay above.
	e, p := editFile(t, "")
	if !e.IsNew() {
		t.Error("not new")
	}
	e.Set("g", c)
	if got := string(e.Bytes()); got != "contexts:\n  # examples\n  g:\n    provider: gcp\n    project: p\n" {
		t.Errorf("template: %q", got)
	}
	if err := e.Save(); err != nil || e.IsNew() {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("not saved")
	}

	// No contexts: key at all.
	e, _ = editFile(t, "# just a comment\n")
	e.Set("g", c)
	if got := string(e.Bytes()); got != "# just a comment\ncontexts:\n  g:\n    provider: gcp\n    project: p\n" {
		t.Errorf("no key: %q", got)
	}
	e, _ = editFile(t, " ")
	e.Set("g", c)
	if !strings.HasPrefix(string(e.Bytes()), " \ncontexts:") && !strings.HasPrefix(string(e.Bytes()), "contexts:") {
		t.Errorf("blank: %q", e.Bytes())
	}

	// Flow style is rewritten as blocks.
	e, _ = editFile(t, "# head\ncontexts: {a: {provider: gcp, project: x}}\n")
	e.Set("g", c)
	if got := string(e.Bytes()); got != "# head\ncontexts:\n  a:\n    provider: gcp\n    project: x\n  g:\n    provider: gcp\n    project: p\n" {
		t.Errorf("flow set: %q", got)
	}
	e, _ = editFile(t, "contexts: {a: {provider: gcp, project: x}}\n")
	if err := e.Delete("a"); err != nil || string(e.Bytes()) != "contexts: {}\n" {
		t.Errorf("flow delete: %q %v", e.Bytes(), err)
	}
	if err := e.Delete("a"); err == nil {
		t.Error("flow delete twice")
	}

	// Other keys around contexts: stay where they are.
	e, _ = editFile(t, "first: 1\ncontexts:\n  a: {provider: gcp, project: x}\n\n# about later\nlater: 2\n")
	e.Set("g", c)
	if got := string(e.Bytes()); got != "first: 1\ncontexts:\n  a: {provider: gcp, project: x}\n  g:\n    provider: gcp\n    project: p\n\n# about later\nlater: 2\n" {
		t.Errorf("around: %q", got)
	}

	// A one-context mapping, and deeper indentation, are kept.
	e, _ = editFile(t, "contexts:\n    only: {provider: gcp, project: o}\n")
	e.Set("g", c)
	if got := string(e.Bytes()); !strings.Contains(got, "\n    g:\n      provider: gcp\n") {
		t.Errorf("indent: %q", got)
	}
}

func TestEditorErrors(t *testing.T) {
	e, _ := editFile(t, "contexts: [a, b\n") // not YAML
	if err := e.Set("g", &Context{}); err == nil || !strings.Contains(err.Error(), "can't edit") {
		t.Errorf("set: %v", err)
	}
	if err := e.Delete("g"); err == nil {
		t.Error("delete")
	}
	e, _ = editFile(t, "contexts: 3\n")
	if err := e.Set("g", &Context{}); err == nil || !strings.Contains(err.Error(), "isn't a mapping") {
		t.Errorf("scalar contexts: %v", err)
	}
	e, _ = editFile(t, "just text\n") // not a mapping at all: no contexts: key
	if err := e.Set("g", &Context{Provider: "gcp"}); err != nil || !strings.HasSuffix(string(e.Bytes()), "contexts:\n  g:\n    provider: gcp\n") {
		t.Errorf("text: %q %v", e.Bytes(), err)
	}
	e, _ = editFile(t, "contexts: {a: {bad: 1}}\n") // flow, but not a valid config
	if err := e.Set("g", &Context{}); err == nil {
		t.Error("flow decode")
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "d"), 0o700)
	if _, err := Open(filepath.Join(dir, "d"), nil, 0o600); err == nil { // a directory
		t.Error("open")
	}
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o600)
	e = &Editor{Path: filepath.Join(file, "x", "config.yaml")}
	if err := e.Save(); err == nil {
		t.Error("save under a file")
	}
	e = &Editor{Path: filepath.Join(dir, "d")}
	if err := e.Save(); err == nil {
		t.Error("save over a directory")
	}
	_ = errors.New
}

func TestPreview(t *testing.T) {
	repo := layered(t, globalYAML, teamYAML, "")
	team := filepath.Join(repo, TeamFile)
	c, err := Preview(team, []byte("contexts:\n  fresh: {provider: gcp, project: f}\n"))
	if err != nil || c.Contexts["fresh"] == nil || c.Contexts["orders-dev"] != nil || c.Project != repo {
		t.Errorf("preview: %+v %v", c, err)
	}
	if _, err := Preview(team, []byte("contexts:\n  bad name: {provider: gcp, project: f}\n")); err == nil {
		t.Error("invalid preview")
	}
	if _, err := Preview(team, []byte("contexts: [")); err == nil {
		t.Error("broken preview")
	}
	if c, _ := Preview(filepath.Join(repo, "elsewhere.yaml"), nil); c.Contexts["orders-dev"] == nil { // not a layer: as on disk
		t.Error("unrelated path")
	}
}

func TestTemplateAndSnippet(t *testing.T) {
	for path, want := range map[string]string{
		filepath.Join("p", TeamFile): "shared by the team", filepath.Join("p", LocalFile): "your own mek contexts",
		Path(): "# Reference: mek help config\ncontexts:\n",
	} {
		if got := string(Template(path)); !strings.Contains(got, want) {
			t.Errorf("Template(%s) = %q", path, got)
		}
		if _, err := Parse(Template(path)); err != nil {
			t.Errorf("Template(%s): %v", path, err)
		}
	}
	got := Snippet("dev", &Context{Provider: "aws", AccountID: "011122223333", Role: "Dev"})
	if got != "dev:\n  provider: aws\n  account_id: \"011122223333\"\n  role: Dev" {
		t.Errorf("snippet:\n%s", got)
	}
}
