package config

import (
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
