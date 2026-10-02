package provider

import (
	"errors"
	"fmt"
	"os"
	"os/user"
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
		if got := sessionName(in); got != want {
			t.Errorf("sessionName(%q)=%q want %q", in, got, want)
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
    account_id: "111122223333"
    role: Dev
    region: ap-southeast-1
  prod:
    provider: aws
    sso_start_url: https://my-org.awsapps.com/start
    sso_region: ap-southeast-1
    account_id: "444455556666"
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
	p := For(cfg, cfg.Contexts["uat"])
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
	for _, want := range []string{"[profile mek-uat]", "[profile mek-prod]", "sso_account_id = 444455556666"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "legacy") {
		t.Errorf("aws_profile contexts must not be rendered:\n%s", out)
	}

	lp := For(cfg, cfg.Contexts["legacy"])
	lenv, _ := lp.Prepare()
	if lenv.Set["AWS_PROFILE"] != "old" || lenv.Set["AWS_CONFIG_FILE"] != "" {
		t.Errorf("legacy env: %+v", lenv.Set)
	}
}

func TestAzurePrepare(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	ctx := &config.Context{Name: "dev", Provider: config.ProviderAzure, TenantID: "t1",
		SubscriptionID: "00000000-1111-2222-3333-444444444444", Region: "southeastasia"}
	p := For(&config.Config{}, ctx)
	env, err := p.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"AZURE_CONFIG_DIR":        filepath.Join(home, "azure", "dev"),
		"ARM_SUBSCRIPTION_ID":     ctx.SubscriptionID,
		"ARM_TENANT_ID":           "t1",
		"AZURE_DEFAULTS_LOCATION": "southeastasia",
	}
	for k, v := range want {
		if env.Set[k] != v {
			t.Errorf("%s = %q, want %q", k, env.Set[k], v)
		}
	}
	if fi, err := os.Stat(want["AZURE_CONFIG_DIR"]); err != nil || !fi.IsDir() {
		t.Errorf("config dir not created: %v", err)
	}
	cmds, _ := p.LoginCommands(false)
	if got := fmt.Sprint(cmds); got != "[[az login --tenant t1] [az account set --subscription "+ctx.SubscriptionID+"]]" {
		t.Errorf("login: %s", got)
	}
}

func TestHuawei(t *testing.T) {
	h := &Huawei{ctx: &config.Context{Name: "hw", Provider: config.ProviderHuawei, HcloudProfile: "sso-prod", Region: "ap-southeast-2"}}

	env, _ := h.Prepare()
	if env.Set["HW_PROFILE"] != "sso-prod" || env.Set["HW_REGION_NAME"] != "ap-southeast-2" {
		t.Errorf("env: %+v", env.Set)
	}

	rewrite := map[string]string{
		"ECS ListServersDetails":                         "ECS ListServersDetails --cli-profile=sso-prod --cli-region=ap-southeast-2",
		"ECS ListServersDetails --cli-region=cn-north-4": "ECS ListServersDetails --cli-region=cn-north-4 --cli-profile=sso-prod",
		"--cli-profile other ECS ListServersDetails":     "--cli-profile other ECS ListServersDetails --cli-region=ap-southeast-2",
		"configure list":                                 "configure list", // KooCLI's own commands are left alone
		"version":                                        "version",
		"--help":                                         "--help",
	}
	for in, want := range rewrite {
		if got := strings.Join(h.RewriteArgs(strings.Fields(in)), " "); got != want {
			t.Errorf("RewriteArgs(%q) = %q, want %q", in, got, want)
		}
	}

	// Login depends on the profile's mode in KooCLI's config (read-only).
	cfg := filepath.Join(t.TempDir(), "config.json")
	old := hcloudConfigPath
	hcloudConfigPath = func() string { return cfg }
	t.Cleanup(func() { hcloudConfigPath = old })

	if _, err := h.LoginCommands(false); err == nil || !strings.Contains(err.Error(), "no KooCLI config") {
		t.Errorf("missing config: %v", err)
	}
	if err := os.WriteFile(cfg, []byte(`{"current":"x","profiles":[{"name":"sso-prod","mode":"SSO"},{"name":"keys","mode":"AKSK"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cmds, err := h.LoginCommands(false); err != nil || fmt.Sprint(cmds) != "[[hcloud configure sso --cli-profile=sso-prod]]" {
		t.Errorf("sso login: %v %v", cmds, err)
	}
	h.ctx.HcloudProfile = "keys"
	if _, err := h.LoginCommands(false); err == nil || !strings.Contains(err.Error(), "AKSK") {
		t.Errorf("aksk login: %v", err)
	}
	h.ctx.HcloudProfile = "nope"
	if _, err := h.LoginCommands(false); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown profile: %v", err)
	}
}

func TestAWSProvider(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Contexts: map[string]*config.Context{}}
	sso := &config.Context{Name: "uat", Provider: config.ProviderAWS, SSOStartURL: "https://o.awsapps.com/start",
		SSORegion: "ap-southeast-1", AccountID: "111122223333", Role: "Dev"}
	cfg.Contexts["uat"] = sso
	a := &AWS{cfg: cfg, ctx: sso, dir: home}

	if got := fmt.Sprint(must(a.LoginCommands(false))); got != "[[aws sso login --profile mek-uat]]" {
		t.Errorf("login: %s", got)
	}
	if got := strings.Join(a.WhoAmICommand(), " "); got != "aws sts get-caller-identity --output table" {
		t.Errorf("whoami: %s", got)
	}
	if got := a.Describe(); got != "aws 111122223333 / Dev / -" {
		t.Errorf("describe: %s", got)
	}
	a.ctx = &config.Context{Name: "old", Provider: config.ProviderAWS, AWSProfile: "legacy"}
	if got := a.Describe(); got != "aws profile legacy" {
		t.Errorf("describe profile: %s", got)
	}

	// The generated config can't be written: Prepare reports it.
	a.ctx = sso
	os.WriteFile(filepath.Join(home, "aws"), nil, 0o600) // "aws" is a file, not a dir
	if _, err := a.Prepare(); err == nil {
		t.Error("expected an error writing the AWS config")
	}
}

func TestGCPProvider(t *testing.T) {
	home := t.TempDir()
	g := &GCP{ctx: &config.Context{Name: "g", Provider: config.ProviderGCP, Project: "p", Account: "me@x.com", Region: "asia-southeast1"}, dir: home}
	env, err := g.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "gcloud", "g")
	if env.Set["CLOUDSDK_CONFIG"] != dir || env.Set["CLOUDSDK_CORE_ACCOUNT"] != "me@x.com" ||
		env.Set["CLOUDSDK_COMPUTE_REGION"] != "asia-southeast1" || env.Set["GOOGLE_APPLICATION_CREDENTIALS"] != "" {
		t.Errorf("env: %+v", env.Set)
	}
	// Once `mek login --adc` created ADC, SDKs get pointed at it.
	adc := filepath.Join(dir, "application_default_credentials.json")
	os.WriteFile(adc, []byte("{}"), 0o600)
	if env, _ := g.Prepare(); env.Set["GOOGLE_APPLICATION_CREDENTIALS"] != adc {
		t.Errorf("ADC not used: %+v", env.Set)
	}
	if got := fmt.Sprint(must(g.LoginCommands(true))); got != "[[gcloud auth login me@x.com] [gcloud auth application-default login]]" {
		t.Errorf("login --adc: %s", got)
	}
	g.ctx.Account = ""
	if got := fmt.Sprint(must(g.LoginCommands(false))); got != "[[gcloud auth login]]" {
		t.Errorf("login: %s", got)
	}
	if got := g.WhoAmICommand()[0]; got != "gcloud" {
		t.Errorf("whoami: %s", got)
	}
	if got := g.Describe(); got != "gcp p / asia-southeast1" {
		t.Errorf("describe: %s", got)
	}

	g.dir = filepath.Join(adc, "under-a-file")
	if _, err := g.Prepare(); err == nil {
		t.Error("expected MkdirAll error")
	}
}

func TestAzureProviderErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	z := &Azure{ctx: &config.Context{Name: "z", Provider: config.ProviderAzure, TenantID: "t", SubscriptionID: "s"}, dir: file}
	if _, err := z.Prepare(); err == nil {
		t.Error("expected MkdirAll error")
	}
	if got := strings.Join(z.WhoAmICommand(), " "); got != "az account show --output table" {
		t.Errorf("whoami: %s", got)
	}
	if got := z.Describe(); got != "azure s / -" {
		t.Errorf("describe: %s", got)
	}
}

func TestHuaweiConfigErrors(t *testing.T) {
	h := &Huawei{ctx: &config.Context{Name: "hw", Provider: config.ProviderHuawei, HcloudProfile: "p"}}
	if got := strings.Join(h.WhoAmICommand(), " "); got != "hcloud configure show --cli-profile=p" {
		t.Errorf("whoami: %s", got)
	}
	if env, _ := h.Prepare(); env.Set["HW_REGION_NAME"] != "" {
		t.Errorf("no region: %+v", env.Set)
	}
	dir := t.TempDir()
	old := hcloudConfigPath
	t.Cleanup(func() { hcloudConfigPath = old })

	hcloudConfigPath = func() string { return dir } // a directory: read fails
	if _, err := h.LoginCommands(false); err == nil || strings.Contains(err.Error(), "no KooCLI config") {
		t.Errorf("read error: %v", err)
	}
	bad := filepath.Join(dir, "config.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	hcloudConfigPath = func() string { return bad }
	if _, err := h.LoginCommands(false); err == nil || !strings.Contains(err.Error(), "read") {
		t.Errorf("bad json: %v", err)
	}
	// Default: the account's home from the user database, like KooCLI —
	// not $HOME; $HOME only if the lookup fails.
	oldLookup := lookupUser
	t.Cleanup(func() { lookupUser = oldLookup })
	t.Setenv("MEK_HCLOUD_CONFIG", "")
	t.Setenv("HOME", "/env-home")
	lookupUser = func() (*user.User, error) { return &user.User{HomeDir: "/passwd-home"}, nil }
	if got := old(); got != filepath.Join("/passwd-home", ".hcloud", "config.json") {
		t.Errorf("default path: %s", got)
	}
	lookupUser = func() (*user.User, error) { return nil, errors.New("no entry") }
	if got := old(); got != filepath.Join("/env-home", ".hcloud", "config.json") {
		t.Errorf("fallback path: %s", got)
	}
	t.Setenv("MEK_HCLOUD_CONFIG", "/custom/config.json")
	if got := old(); got != "/custom/config.json" {
		t.Errorf("MEK_HCLOUD_CONFIG: %s", got)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
