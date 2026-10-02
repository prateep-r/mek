package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExampleConfigIsValid(t *testing.T) {
	c, err := Parse(Example())
	if err != nil {
		t.Fatalf("example config invalid: %v", err)
	}
	if len(c.Contexts) == 0 {
		t.Fatal("example config has no contexts")
	}
	prod, err := c.Get("example-prod")
	if err != nil || !prod.Protected || !prod.ReadOnly {
		t.Fatalf("example-prod should be protected+readonly: %+v %v", prod, err)
	}
}

func TestValidate(t *testing.T) {
	// Generic rules only; each cloud's rules are tested in package provider.
	cases := []struct{ in, want string }{
		{"contexts:\n  a b:\n    provider: aws\n    aws_profile: p\n", "may only contain"},
		{"contexts:\n  ..:\n    provider: gcp\n    project: p\n", "may only contain"},
		{"contexts:\n  a:\n    provider: gcp\n    project: \"p\\nx = y\"\n", "line breaks"},
		{"contexts:\n  a:\n", "is empty"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	if _, err := Parse([]byte("contexts:\n  team.prod_1:\n    provider: aws\n    aws_profile: p\n")); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
}

func TestResolvePrecedence(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	c, _ := Parse([]byte("contexts:\n  a:\n    provider: gcp\n    project: p\n  b:\n    provider: gcp\n    project: q\n"))

	if _, err := c.Resolve(""); err == nil {
		t.Fatal("expected error with no context selected")
	}
	if err := SetCurrent("a"); err != nil {
		t.Fatal(err)
	}
	if ctx, _ := c.Resolve(""); ctx.Name != "a" {
		t.Fatalf("saved current: got %s", ctx.Name)
	}
	t.Setenv("MEK_CONTEXT", "b")
	if ctx, _ := c.Resolve(""); ctx.Name != "b" {
		t.Fatalf("env override: got %s", ctx.Name)
	}
	if ctx, _ := c.Resolve("a"); ctx.Name != "a" {
		t.Fatalf("flag override: got %s", ctx.Name)
	}
}

func TestDir(t *testing.T) {
	t.Setenv("MEK_HOME", "/m")
	if Dir() != "/m" || Path() != filepath.Join("/m", "config.yaml") {
		t.Errorf("MEK_HOME: %s %s", Dir(), Path())
	}
	t.Setenv("MEK_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "/x")
	if Dir() != filepath.Join("/x", "mek") {
		t.Errorf("XDG: %s", Dir())
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/h")
	if Dir() != filepath.Join("/h", ".config", "mek") {
		t.Errorf("home: %s", Dir())
	}
}

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	if _, err := Load(); !errors.Is(err, ErrNoConfig) {
		t.Errorf("missing: %v", err)
	}
	os.WriteFile(Path(), []byte("contexts:\n  a: {provider: gcp, project: p}\n"), 0o600)
	if c, err := Load(); err != nil || c.Contexts["a"].Name != "a" {
		t.Errorf("valid: %v", err)
	}
	os.WriteFile(Path(), []byte("contexts: [oops"), 0o600)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("bad yaml: %v", err)
	}
	os.Remove(Path())
	os.Mkdir(Path(), 0o700) // read error other than not-exist
	if _, err := Load(); err == nil || errors.Is(err, ErrNoConfig) {
		t.Errorf("unreadable: %v", err)
	}
	c, _ := Parse([]byte(""))
	if c.Contexts == nil {
		t.Error("empty config must have an empty contexts map")
	}
	if _, err := c.Get("nope"); err == nil || !strings.Contains(err.Error(), "unknown context") {
		t.Errorf("Get: %v", err)
	}
}

func TestCurrent(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	if Current() != "" {
		t.Error("no state file: want empty")
	}
	if err := SetCurrent("prod"); err != nil || Current() != "prod" {
		t.Errorf("SetCurrent: %v %q", err, Current())
	}
	os.WriteFile(statePath(), []byte("current: [oops"), 0o600)
	if Current() != "" {
		t.Error("corrupt state file: want empty")
	}
}

func TestInit(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mek")
	t.Setenv("MEK_HOME", home)
	p, err := Init(false)
	if err != nil || p != Path() {
		t.Fatalf("Init: %s %v", p, err)
	}
	if b, _ := os.ReadFile(p); string(b) != string(Example()) {
		t.Error("Init must write the example config")
	}
	if fi, _ := os.Stat(home); fi.Mode().Perm() != 0o700 {
		t.Errorf("config dir perm %v", fi.Mode().Perm())
	}
	if _, err := Init(false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second Init: %v", err)
	}
	os.WriteFile(p, []byte("mine"), 0o600)
	if _, err := Init(true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) == "mine" {
		t.Error("--force must overwrite")
	}

	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("MEK_HOME", filepath.Join(file, "mek")) // Stat fails with ENOTDIR
	if _, err := Init(false); err == nil {
		t.Error("Init under a file must fail")
	}
	locked := filepath.Join(t.TempDir(), "locked")
	os.Mkdir(locked, 0o500)
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	t.Setenv("MEK_HOME", filepath.Join(locked, "mek")) // not there yet, can't create
	if _, err := Init(false); err == nil {
		t.Error("Init in a read-only parent must fail")
	}
}
