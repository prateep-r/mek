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
	// two joins SSO contexts into one config.
	two := func(a, b string) string { return a + strings.TrimPrefix(b, "contexts:\n") }
	cases := []struct{ in, want string }{
		{"contexts:\n  a:\n    provider: aws\n", "sso_start_url"},
		{"contexts:\n  a:\n    provider: gcp\n", "project"},
		{"contexts:\n  a:\n    provider: azure\n", "unknown provider"},
		{"contexts:\n  a:\n    region: x\n", "provider is required"},
		{"contexts:\n  a b:\n    provider: aws\n    aws_profile: p\n", "may only contain"},
		{"contexts:\n  ..:\n    provider: gcp\n    project: p\n", "may only contain"},
		{"contexts:\n  a:\n    provider: gcp\n    project: \"p\\nx = y\"\n", "line breaks"},
		{sso("a", "123", "ap-southeast-1"), "12 digits"},
		{sso("a", "111122223333", ""), "sso_region"},
		{two(sso("a", "111122223333", "us-east-1"), sso("b", "444455556666", "eu-west-1")), "different sso_region"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	for _, ok := range []string{
		"contexts:\n  a:\n    provider: aws\n    aws_profile: p\n",
		two(sso("team.prod_1", "111122223333", "ap-southeast-1"), sso("b", "444455556666", "ap-southeast-1")),
	} {
		if _, err := Parse([]byte(ok)); err != nil {
			t.Errorf("Parse(%q) should be valid: %v", ok, err)
		}
	}
}

// sso renders a minimal SSO context on the shared test portal.
func sso(name, account, ssoRegion string) string {
	return "contexts:\n  " + name + ":\n    provider: aws\n    sso_start_url: https://x.awsapps.com/start\n" +
		"    account_id: \"" + account + "\"\n    role: r\n    sso_region: \"" + ssoRegion + "\"\n"
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
