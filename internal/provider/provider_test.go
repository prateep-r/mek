package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

func TestAWSProvider(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Contexts: map[string]*config.Context{}}
	sso := &config.Context{Name: "uat", Provider: config.ProviderAWS, SSOStartURL: "https://o.awsapps.com/start",
		SSORegion: "ap-southeast-1", AccountID: "111122223333", Role: "Dev"}
	cfg.Contexts["uat"] = sso
	a := &AWS{cfg: cfg, ctx: sso, dir: home}

	if got := fmt.Sprint(a.LoginCommands(false)); got != "[[aws sso login --profile mek-uat]]" {
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
		env.Set["CLOUDSDK_COMPUTE_REGION"] != "asia-southeast1" || env.Set["GOOGLE_APPLICATION_CREDENTIALS"] != "" ||
		env.Set["CLOUDSDK_CORE_CHECK_GCE_METADATA"] != "false" { // never the machine's service account by surprise
		t.Errorf("env: %+v", env.Set)
	}
	vm := &GCP{ctx: &config.Context{Name: "vm", Provider: config.ProviderGCP, Project: "p", UseMetadata: true}, dir: home}
	if env, _ := vm.Prepare(); env.Set["CLOUDSDK_CORE_CHECK_GCE_METADATA"] != "" || !slices.Contains(env.Unset, "CLOUDSDK_CORE_CHECK_GCE_METADATA") {
		t.Errorf("use_metadata: %+v", env)
	}
	// Once `mek login --adc` created ADC, SDKs get pointed at it.
	adc := filepath.Join(dir, "application_default_credentials.json")
	os.WriteFile(adc, []byte("{}"), 0o600)
	if env, _ := g.Prepare(); env.Set["GOOGLE_APPLICATION_CREDENTIALS"] != adc {
		t.Errorf("ADC not used: %+v", env.Set)
	}
	if got := fmt.Sprint(g.LoginCommands(true)); got != "[[gcloud auth login me@x.com] [gcloud auth application-default login]]" {
		t.Errorf("login --adc: %s", got)
	}
	g.ctx.Account = ""
	if got := fmt.Sprint(g.LoginCommands(false)); got != "[[gcloud auth login]]" {
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

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// What another context's `eval "$(mek env …)"` left in the shell must not
// leak into a context that doesn't set it.
func TestEnvDoesNotLeakBetweenContexts(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Contexts: map[string]*config.Context{}}
	legacy := &AWS{cfg: cfg, dir: dir, ctx: &config.Context{Name: "legacy", Provider: config.ProviderAWS, AWSProfile: "mine"}}

	t.Setenv("AWS_CONFIG_FILE", legacy.configPath()) // from an SSO context's mek env
	env, _ := legacy.Prepare()
	got := strings.Join(env.Apply([]string{"AWS_REGION=us-east-1", "AWS_DEFAULT_REGION=us-east-1", "AWS_CONFIG_FILE=" + legacy.configPath()}), " ")
	if strings.Contains(got, "AWS_REGION") || strings.Contains(got, "AWS_CONFIG_FILE") {
		t.Errorf("leaked into an aws_profile context without region: %s", got)
	}
	t.Setenv("AWS_CONFIG_FILE", "/home/you/my-aws-config") // the user's own: kept
	if env, _ := legacy.Prepare(); slices.Contains(env.Unset, "AWS_CONFIG_FILE") {
		t.Error("the user's own AWS_CONFIG_FILE was removed")
	}
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "aws", "config-0a1b2c3d")) // another project's
	if env, _ := legacy.Prepare(); !slices.Contains(env.Unset, "AWS_CONFIG_FILE") {
		t.Error("another project's generated AWS config was kept")
	}
	if len(awsUnset) != 8 || awsUnset[len(awsUnset)-1] != "AWS_ROLE_SESSION_NAME" {
		t.Errorf("Prepare changed the shared awsUnset: %v", awsUnset)
	}

	g := &GCP{dir: dir, ctx: &config.Context{Name: "g", Provider: config.ProviderGCP, Project: "p"}}
	genv, _ := g.Prepare()
	if !slices.Contains(genv.Unset, "CLOUDSDK_CORE_ACCOUNT") || !slices.Contains(genv.Unset, "CLOUDSDK_COMPUTE_REGION") {
		t.Errorf("gcp: %v", genv.Unset)
	}
}

// A shell that ran a GCP context's mek use -s, then moves to an AWS one,
// loses the GCP variables; to another GCP context, they are replaced.
func TestDropPrevious(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]*config.Context{
		"g":  {Name: "g", Provider: config.ProviderGCP},
		"g2": {Name: "g2", Provider: config.ProviderGCP},
		"a":  {Name: "a", Provider: config.ProviderAWS},
	}}
	env := Env{Set: map[string]string{"AWS_PROFILE": "p"}, Unset: []string{"AWS_REGION"}}
	DropPrevious(cfg, cfg.Contexts["a"], &env, "g")
	if !slices.Contains(env.Unset, "CLOUDSDK_CONFIG") || !slices.Contains(env.Unset, "GOOGLE_PROJECT") {
		t.Errorf("gcp variables kept: %v", env.Unset)
	}
	n := len(env.Unset)
	DropPrevious(cfg, cfg.Contexts["a"], &env, "g") // twice: nothing listed twice
	DropPrevious(cfg, cfg.Contexts["g2"], &env, "g")
	DropPrevious(cfg, cfg.Contexts["g"], &env, "unknown")
	if len(env.Unset) != n {
		t.Errorf("unset grew: %v", env.Unset)
	}
	aws := Env{Set: map[string]string{"AWS_PROFILE": "p"}}
	DropPrevious(cfg, cfg.Contexts["g"], &aws, "a")
	if slices.Contains(aws.Unset, "AWS_PROFILE") || !slices.Contains(aws.Unset, "AWS_REGION") {
		t.Errorf("a variable the context sets was unset, or one it doesn't was kept: %v", aws.Unset)
	}
}

// Every variable a cloud's Prepare sets is in its Exports, so DropPrevious
// can't miss one.
func TestExportsCoverPrepare(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	t.Setenv("AZURE_EXTENSION_DIR", filepath.Join(home, "ext"))
	full := []*config.Context{
		{Name: "a", Provider: config.ProviderAWS, Region: "r", SSOStartURL: "https://x.awsapps.com/start", SSORegion: "r", AccountID: "111122223333", Role: "R"},
		{Name: "g", Provider: config.ProviderGCP, Project: "p", Account: "a@x", Region: "r"},
		{Name: "z", Provider: config.ProviderAzure, TenantID: "t", SubscriptionID: "00000000-1111-2222-3333-444444444444", Region: "r"},
		{Name: "h", Provider: config.ProviderHuawei, HcloudProfile: "p", Region: "r"},
	}
	cfg := &config.Config{Contexts: map[string]*config.Context{}}
	for _, c := range full {
		cfg.Contexts[c.Name] = c
	}
	os.MkdirAll(GCloudDir(full[1]), 0o700)
	os.WriteFile(filepath.Join(GCloudDir(full[1]), "application_default_credentials.json"), nil, 0o600)
	for _, c := range full {
		env, err := For(cfg, c).Prepare()
		if err != nil {
			t.Fatalf("%s: %v", c.Provider, err)
		}
		cl, _ := Lookup(c.Provider)
		for k := range env.Set {
			if !slices.Contains(cl.Exports, k) {
				t.Errorf("%s sets %s, not in its Exports", c.Provider, k)
			}
		}
	}
}

// Project contexts are stored by Key: two projects' "dev" on different
// accounts get their own AWS profile, config file, gcloud login and SSH key.
func TestProjectContextsKeepApart(t *testing.T) {
	home := t.TempDir()
	devA := &config.Context{Name: "dev", Provider: config.ProviderAWS, SSOStartURL: "https://o.awsapps.com/start",
		SSORegion: "ap-southeast-1", AccountID: "111122223333", Role: "Dev", InProject: true}
	devB := *devA
	devB.AccountID = "444455556666"
	a := &AWS{cfg: &config.Config{Contexts: map[string]*config.Context{"dev": devA}, Project: "/work/a"}, ctx: devA, dir: home}
	b := &AWS{cfg: &config.Config{Contexts: map[string]*config.Context{"dev": &devB}, Project: "/work/b"}, ctx: &devB, dir: home}
	envA, errA := a.Prepare()
	envB, errB := b.Prepare()
	if errA != nil || errB != nil || envA.Set["AWS_PROFILE"] == envB.Set["AWS_PROFILE"] || envA.Set["AWS_CONFIG_FILE"] == envB.Set["AWS_CONFIG_FILE"] ||
		envA.Set["AWS_PROFILE"] != "mek-"+devA.Key() || !strings.HasPrefix(filepath.Base(envA.Set["AWS_CONFIG_FILE"]), "config-") {
		t.Errorf("aws: %v %v / %v %v", envA.Set, errA, envB.Set, errB)
	}
	if body, _ := os.ReadFile(envA.Set["AWS_CONFIG_FILE"]); !strings.Contains(string(body), "[profile mek-"+devA.Key()+"]") {
		t.Errorf("config a:\n%s", body)
	}
	if got := fmt.Sprint(a.LoginCommands(false)); got != "[[aws sso login --profile mek-"+devA.Key()+"]]" {
		t.Errorf("login: %s", got)
	}

	gA := &GCP{ctx: &config.Context{Name: "dev", Provider: config.ProviderGCP, Project: "a", InProject: true}, dir: home}
	gB := &GCP{ctx: &config.Context{Name: "dev", Provider: config.ProviderGCP, Project: "b", InProject: true}, dir: home}
	if gA.configDir() == gB.configDir() || gA.sshDir() == gB.sshDir() || filepath.Base(gA.configDir()) != gA.ctx.Key() {
		t.Errorf("gcp: %s %s / %s %s", gA.configDir(), gA.sshDir(), gB.configDir(), gB.sshDir())
	}
}
