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
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 { // its mode kept
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
	if got := string(e.Bytes()); got != "contexts:\n  # examples\n\n  g:\n    provider: gcp\n    project: p\n" {
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

	// contexts: null (or ~) too; a nested comment above an entry stays.
	for _, null := range []string{"null", "~"} {
		e, _ = editFile(t, "contexts: "+null+"\n")
		e.Set("g", c)
		if got := string(e.Bytes()); got != "contexts:\n  g:\n    provider: gcp\n    project: p\n" {
			t.Errorf("contexts: %s: %q", null, got)
		}
	}
	e, _ = editFile(t, "contexts:\n  a:\n    provider: gcp\n    project: x\n    # about a\n  # about g\n  g: {provider: gcp, project: p}\n")
	if err := e.Delete("g"); err != nil || string(e.Bytes()) != "contexts:\n  a:\n    provider: gcp\n    project: x\n    # about a\n" {
		t.Errorf("delete took another's comment: %q %v", e.Bytes(), err)
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
	if _, err := Preview(filepath.Join(repo, "sub", LocalFile), []byte("contexts:\n  x: {provider: gcp, project: [\n")); err == nil {
		t.Error("a file mek doesn't read yet went unchecked")
	}
	if c, _ := Preview(filepath.Join(repo, "elsewhere.yaml"), nil); c.Contexts["orders-dev"] == nil { // not a layer: on top of the others
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

func TestCheckAliasAndSnippetOf(t *testing.T) {
	for _, c := range []struct{ section, alias, want string }{
		{"tunnels", "db", ""}, {"tunnels", "ls", "reserved"}, {"clusters", "token", "reserved"}, {"targets", "a b", "may only contain"},
	} {
		if err := CheckAlias(c.section, c.alias); (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("CheckAlias(%s, %s) = %v", c.section, c.alias, err)
		}
	}
	if got := SnippetOf(map[string]any{"tunnels": map[string]*Tunnel{"db": {Via: "b", Port: 5432}}}); got != "tunnels:\n  db:\n    via: b\n    port: 5432" {
		t.Errorf("snippet:\n%s", got)
	}
}

func TestSetSetting(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"no section: added at the end", "# mine\ncontexts: {}\n", "# mine\ncontexts: {}\n\nsettings:\n  show_cmd: true\n"},
		{"empty file", "", "settings:\n  show_cmd: true\n"},
		{"replaced in place, comments kept", "settings:\n  # say what runs\n  show_cmd: false # for now\ncontexts: {}\n",
			"settings:\n  # say what runs\n  show_cmd: true\ncontexts: {}\n"},
		{"another key: added under it", "settings:\n    other: 1\n# end\n", "settings:\n    other: 1\n    show_cmd: true\n# end\n"},
		{"flow style: rewritten as a block", "contexts: {}\nsettings: {show_cmd: false, other: x}\n# end\n",
			"contexts: {}\nsettings:\n  other: x\n  show_cmd: true\n# end\n"},
		{"empty section", "settings:\ncontexts: {}\n", "settings:\n  show_cmd: true\ncontexts: {}\n"},
	} {
		e := &Editor{Path: "c.yaml", src: []byte(c.in)}
		if err := e.SetSetting("show_cmd", true); err != nil || string(e.Bytes()) != c.want {
			t.Errorf("%s: %v\n got %q\nwant %q", c.name, err, e.Bytes(), c.want)
		}
	}
	e := &Editor{Path: "c.yaml", src: []byte("contexts: [\n")}
	if err := e.SetSetting("show_cmd", true); err == nil {
		t.Error("broken file: no error")
	}
}

func TestSettingsLayers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	t.Setenv("HOME", t.TempDir())
	os.WriteFile(Path(), []byte("settings: {show_cmd: true}\ncontexts:\n  a: {provider: aws, aws_profile: a}\n"), 0o600)
	if _, err := Load(); err != nil { // settings: is a key ~/.mek/config.yaml may have
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path())
	if st, err := ReadSettings(b); err != nil || st == nil || st.ShowCmd == nil || !*st.ShowCmd {
		t.Fatalf("settings: %+v %v", st, err)
	}
	if st, err := ReadSettings([]byte("contexts: {}\n")); st != nil || err != nil {
		t.Errorf("none: %+v %v", st, err)
	}
	repo := t.TempDir()
	os.Mkdir(filepath.Join(repo, ".git"), 0o700)
	os.WriteFile(filepath.Join(repo, TeamFile), []byte("settings: {show_cmd: false}\ncontexts: {}\n"), 0o644)
	t.Chdir(repo)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "settings: belong in") {
		t.Errorf("a project's settings: %v", err)
	}
}

// Removing the context a template got leaves the template's examples.
func TestEditorTemplateKept(t *testing.T) {
	tpl := "# mek.local.yaml\ncontexts:\n  # sandbox:\n  #   provider: aws\n"
	e := &Editor{Path: "l.yaml", src: []byte(tpl)}
	e.Set("mine", &Context{Provider: "gcp", Project: "p"})
	e.Delete("mine")
	if got := string(e.Bytes()); !strings.HasPrefix(got, tpl) {
		t.Errorf("template's comments gone:\n%s", got)
	}
}

func TestEditorRename(t *testing.T) {
	src := "contexts:\n  # mine, keep\n  dev: {provider: gcp, project: p}\n  # other\n  b: {provider: gcp, project: q}\n"
	e := &Editor{Path: "c.yaml", src: []byte(src)}
	if err := e.Rename("dev", "dev2", &Context{Provider: "gcp", Project: "p"}); err != nil {
		t.Fatal(err)
	}
	if got := string(e.Bytes()); got != "contexts:\n  # mine, keep\n  dev2:\n    provider: gcp\n    project: p\n  # other\n  b: {provider: gcp, project: q}\n" {
		t.Errorf("rename:\n%s", got)
	}
	if err := e.Rename("nope", "x", &Context{}); err == nil {
		t.Error("renamed a context that isn't there")
	}
	flow := &Editor{Path: "c.yaml", src: []byte("contexts: {dev: {provider: gcp, project: p}}\n")}
	flow.Rename("dev", "dev2", &Context{Provider: "gcp", Project: "p"})
	if got := string(flow.Bytes()); !strings.Contains(got, "dev2:") || strings.Contains(got, "dev:") {
		t.Errorf("flow rename:\n%s", got)
	}
	bad := &Editor{Path: "c.yaml", src: []byte("contexts: [\n")}
	if bad.Rename("a", "b", &Context{}) == nil {
		t.Error("broken file")
	}
}

func TestEditorEntries(t *testing.T) {
	src := `contexts:
  dev:
    provider: aws # mine
    aws_profile: dev
    tunnels:
      # the database
      db: {via: bastion, host: db.internal, port: 5432}
      cache:
        via: bastion
        port: 6379
    targets:
      bastion: {instance: "tag:Name=bastion"}   # keep
  flowy: {provider: aws, aws_profile: x}
  half:
    provider: aws
    aws_profile: y
    tunnels: {db: {via: b, port: 1}}
`
	e := &Editor{Path: "c.yaml", src: []byte(src)}
	set := func(ctx, sec, key string, v any) bool {
		t.Helper()
		ok, err := e.SetEntry(ctx, sec, key, v)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !set("dev", "tunnels", "db", &Tunnel{Via: "bastion", Host: "db2.internal", Port: 5432}) ||
		!set("dev", "tunnels", "web", &Tunnel{Via: "bastion", Port: 80}) ||
		!set("dev", "clusters", "main", &Cluster{Name: "eks"}) {
		t.Fatal("block style: not set in place")
	}
	want := `contexts:
  dev:
    provider: aws # mine
    aws_profile: dev
    tunnels:
      # the database
      db:
        via: bastion
        host: db2.internal
        port: 5432
      cache:
        via: bastion
        port: 6379
      web:
        via: bastion
        port: 80
    targets:
      bastion: {instance: "tag:Name=bastion"}   # keep
    clusters:
      main:
        name: eks
  flowy: {provider: aws, aws_profile: x}
`
	if got := string(e.Bytes()); !strings.HasPrefix(got, want) {
		t.Errorf("set:\n%s", got)
	}
	ok, err := e.DeleteEntry("dev", "tunnels", "cache")
	if !ok || err != nil || strings.Contains(string(e.Bytes()), "6379") || !strings.Contains(string(e.Bytes()), "web:") {
		t.Errorf("delete one: %v %v\n%s", ok, err, e.Bytes())
	}
	if ok, _ := e.DeleteEntry("dev", "clusters", "main"); !ok || strings.Contains(string(e.Bytes()), "clusters:") {
		t.Errorf("delete the last one, the section too:\n%s", e.Bytes())
	}
	if _, err := e.DeleteEntry("dev", "tunnels", "nope"); err == nil {
		t.Error("deleted an entry that isn't there")
	}
	if _, err := Parse(e.Bytes()); err != nil {
		t.Errorf("result doesn't load: %v\n%s", err, e.Bytes())
	}
	// Flow style on the way: the caller rewrites the context.
	for _, c := range [][2]string{{"flowy", "tunnels"}, {"half", "tunnels"}, {"nope", "tunnels"}} {
		if ok, err := e.SetEntry(c[0], c[1], "x", &Tunnel{}); ok || err != nil {
			t.Errorf("%v: %v %v", c, ok, err)
		}
		if ok, err := e.DeleteEntry(c[0], c[1], "x"); ok || err != nil {
			t.Errorf("delete %v: %v %v", c, ok, err)
		}
	}
	for _, src := range []string{"contexts: {a: {provider: aws}}\n", "contexts: [\n"} {
		f := &Editor{Path: "c.yaml", src: []byte(src)}
		if ok, _ := f.SetEntry("a", "tunnels", "x", &Tunnel{}); ok {
			t.Errorf("%q: set in place", src)
		}
	}
}

func TestSaveThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "mek.yaml")
	os.MkdirAll(filepath.Dir(real), 0o700)
	os.WriteFile(real, []byte("contexts: {}\n"), 0o600)
	link := filepath.Join(dir, "config.yaml")
	os.Symlink(real, link)
	e, err := Open(link, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	e.Set("g", &Context{Provider: "gcp", Project: "p"})
	if err := e.Save(); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link became a file")
	}
	if b, _ := os.ReadFile(real); !strings.Contains(string(b), "g:") {
		t.Errorf("the file it points to wasn't written:\n%s", b)
	}
}

func TestSetSettingMultiline(t *testing.T) {
	for in, want := range map[string]string{
		"settings:\n  show_cmd:\n    false\ncontexts: {}\n": "settings:\n  show_cmd: true\ncontexts: {}\n",
		"settings:\n  other:\n    sub: 1\n":                 "settings:\n  other:\n    sub: 1\n  show_cmd: true\n",
	} {
		e := &Editor{Path: "c.yaml", src: []byte(in)}
		if err := e.SetSetting("show_cmd", true); err != nil || string(e.Bytes()) != want {
			t.Errorf("%q: %v\n got %q\nwant %q", in, err, e.Bytes(), want)
		}
	}
}

// mek init still works after mek show-cmd on made a file of settings only.
func TestInitKeepsSettings(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	os.WriteFile(Path(), []byte("contexts:\n\nsettings:\n  show_cmd: true\n"), 0o600)
	if _, err := Init(false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path())
	if !strings.Contains(string(b), "example-uat:") || !strings.Contains(string(b), "settings:\n  show_cmd: true\n") {
		t.Errorf("init over settings:\n%s", b)
	}
	if _, err := Init(false); err == nil || !strings.Contains(err.Error(), "already exists") { // contexts now: kept
		t.Errorf("second init: %v", err)
	}
	os.WriteFile(Path(), []byte("contexts: [\n"), 0o600)
	if _, err := Init(false); err == nil {
		t.Error("init over a broken file")
	}
	os.Remove(Path())
	os.Mkdir(Path(), 0o700)
	if _, err := Init(false); err == nil {
		t.Error("init over a directory")
	}
}
