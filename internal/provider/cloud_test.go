package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

// parse runs both validation layers: config's generic rules, then each cloud's.
func parse(in string) error {
	cfg, err := config.Parse([]byte(in))
	if err != nil {
		return err
	}
	return Validate(cfg)
}

// sso renders a minimal AWS SSO context on the shared test portal.
func sso(name, account, ssoRegion string) string {
	return "contexts:\n  " + name + ":\n    provider: aws\n    sso_start_url: https://x.awsapps.com/start\n" +
		"    account_id: \"" + account + "\"\n    role: r\n    sso_region: \"" + ssoRegion + "\"\n"
}

func TestValidate(t *testing.T) {
	// two joins SSO contexts into one config.
	two := func(a, b string) string { return a + strings.TrimPrefix(b, "contexts:\n") }
	cases := []struct{ in, want string }{
		{"contexts:\n  a:\n    provider: aws\n", "sso_start_url"},
		{"contexts:\n  a:\n    provider: gcp\n", "project"},
		{"contexts:\n  a:\n    provider: oracle\n", "unknown provider"},
		{"contexts:\n  a:\n    region: x\n", "provider is required"},
		{"contexts:\n  a:\n    provider: azure\n    tenant_id: t\n", "tenant_id and subscription_id"},
		{"contexts:\n  a:\n    provider: azure\n    tenant_id: t\n    subscription_id: sub-1\n", "GUID"},
		{"contexts:\n  a:\n    provider: huawei\n", "hcloud_profile"},
		{sso("a", "123", "ap-southeast-1"), "12 digits"},
		{sso("a", "111122223333", ""), "sso_region"},
		{two(sso("a", "111122223333", "us-east-1"), sso("b", "444455556666", "eu-west-1")), "different sso_region"},
	}
	for _, c := range cases {
		if err := parse(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	for _, ok := range []string{
		"contexts:\n  a:\n    provider: aws\n    aws_profile: p\n",
		two(sso("team.prod_1", "111122223333", "ap-southeast-1"), sso("b", "444455556666", "ap-southeast-1")),
		"contexts:\n  a:\n    provider: azure\n    tenant_id: contoso.onmicrosoft.com\n    subscription_id: 00000000-1111-2222-3333-444444444444\n",
		"contexts:\n  a:\n    provider: huawei\n    hcloud_profile: my-sso\n",
	} {
		if err := parse(ok); err != nil {
			t.Errorf("parse(%q) should be valid: %v", ok, err)
		}
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := config.Parse(config.Example())
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("example config breaks a cloud rule: %v", err)
	}
}

// Every cloud must be fully wired: a factory, a classifier and validation.
func TestRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Clouds() {
		if c.Name == "" || c.CLI == "" || c.Title == "" || c.New == nil || c.Classify == nil || c.Validate == nil || c.Tool.URL == "" {
			t.Errorf("cloud %q is missing a field: %+v", c.Name, c)
		}
		if seen[c.Name] || seen["cli:"+c.CLI] {
			t.Errorf("duplicate cloud %q / CLI %q", c.Name, c.CLI)
		}
		seen[c.Name], seen["cli:"+c.CLI] = true, true
		if p := c.New(&config.Config{}, &config.Context{Name: "x", Provider: c.Name}, t.TempDir()); p.Describe() == "" {
			t.Errorf("%s: New() made a provider with no description", c.Name)
		}
	}
	// Each cloud's Strategy is the right classifier.
	destructive := map[string]string{"aws": "ec2 terminate-instances", "gcloud": "compute instances delete vm",
		"az": "vm delete -n vm", "hcloud": "ECS DeleteServers"}
	for _, c := range Clouds() {
		args, ok := destructive[c.CLI]
		if !ok {
			t.Fatalf("no destructive example for %s", c.CLI)
		}
		if got := c.Classify(strings.Fields(args)); got != guard.Destructive {
			t.Errorf("%s %s = %s, want destructive", c.CLI, args, got)
		}
	}
}

func TestForPanicsOnUnvalidatedContext(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("For must panic for an unknown provider")
		}
	}()
	For(&config.Config{}, &config.Context{Name: "x", Provider: "oracle"})
}

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	if _, err := Load(); err == nil {
		t.Error("missing config must fail")
	}
	write := func(s string) { os.WriteFile(filepath.Join(home, "config.yaml"), []byte(s), 0o600) }
	write("contexts:\n  a: {provider: gcp}\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "project") {
		t.Errorf("cloud rule: %v", err)
	}
	write("contexts:\n  a: {provider: gcp, project: p}\n")
	if cfg, err := Load(); err != nil || cfg.Contexts["a"] == nil {
		t.Errorf("valid: %v", err)
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{"": "", "a": "a", "a b": "a or b", "a b c": "a, b or c"} {
		if got := joinOr(strings.Fields(in)); got != want {
			t.Errorf("joinOr(%q) = %q", in, got)
		}
	}
	if orDash("") != "-" || orDash("x") != "x" {
		t.Error("orDash")
	}
}
