package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

func TestSessionName(t *testing.T) {
	cases := map[string]string{
		"https://my-org.awsapps.com/start":          "mek-my-org",
		"https://d-1234567890.awsapps.com/start/#/": "mek-d-1234567890",
		"https://sso.Example.com/start":             "mek-sso-example-com",
	}
	for in, want := range cases {
		if got := SessionName(in); got != want {
			t.Errorf("SessionName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestEnvApply(t *testing.T) {
	e := Env{Set: map[string]string{"AWS_PROFILE": "mek-a"}, Unset: []string{"AWS_ACCESS_KEY_ID"}}
	got := strings.Join(e.Apply([]string{"PATH=/bin", "AWS_ACCESS_KEY_ID=AKIA", "AWS_PROFILE=old"}), " ")
	if got != "PATH=/bin AWS_PROFILE=mek-a" {
		t.Fatalf("Apply: %s", got)
	}
	if !strings.Contains(e.Shell(), "unset AWS_ACCESS_KEY_ID") || !strings.Contains(e.Shell(), "export AWS_PROFILE='mek-a'") {
		t.Fatalf("Shell: %s", e.Shell())
	}
}

func TestAWSPrepareWritesSharedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	yml := `contexts:
  uat:
    provider: aws
    sso_start_url: https://my-org.awsapps.com/start
    sso_region: ap-southeast-1
    account_id: "111"
    role: Dev
    region: ap-southeast-1
  prod:
    provider: aws
    sso_start_url: https://my-org.awsapps.com/start
    sso_region: ap-southeast-1
    account_id: "222"
    role: ReadOnly
  legacy:
    provider: aws
    aws_profile: old
`
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := For(cfg, cfg.Contexts["uat"])
	env, err := p.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if env.Set["AWS_PROFILE"] != "mek-uat" || env.Set["AWS_REGION"] != "ap-southeast-1" {
		t.Fatalf("env: %+v", env.Set)
	}
	b, err := os.ReadFile(env.Set["AWS_CONFIG_FILE"])
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if strings.Count(out, "[sso-session mek-my-org]") != 1 {
		t.Errorf("want exactly one shared sso-session:\n%s", out)
	}
	for _, want := range []string{"[profile mek-uat]", "[profile mek-prod]", "sso_account_id = 222"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "legacy") {
		t.Errorf("aws_profile contexts must not be rendered:\n%s", out)
	}

	lp, _ := For(cfg, cfg.Contexts["legacy"])
	lenv, _ := lp.Prepare()
	if lenv.Set["AWS_PROFILE"] != "old" || lenv.Set["AWS_CONFIG_FILE"] != "" {
		t.Errorf("legacy env: %+v", lenv.Set)
	}
}
