package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

const subID = "00000000-1111-2222-3333-444444444444"

func TestAzurePrepare(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	azureHome(t)
	ctx := &config.Context{Name: "dev", Provider: config.ProviderAzure, TenantID: "t1", SubscriptionID: subID, Region: "southeastasia"}
	p := For(&config.Config{}, ctx)
	env, err := p.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"AZURE_CONFIG_DIR":               filepath.Join(home, "azure", "dev"),
		"AZURE_SUBSCRIPTION_ID":          subID,
		"ARM_SUBSCRIPTION_ID":            subID,
		"ARM_TENANT_ID":                  "t1",
		"AZURE_TENANT_ID":                "t1",
		"AZURE_DEFAULTS_LOCATION":        "southeastasia",
		"AZURE_CORE_LOGIN_EXPERIENCE_V2": "off",
	}
	for k, v := range want {
		if env.Set[k] != v {
			t.Errorf("%s = %q, want %q", k, env.Set[k], v)
		}
	}
	if !strings.HasSuffix(env.Set["AZURE_EXTENSION_DIR"], filepath.Join(".azure", "cliextensions")) {
		t.Errorf("extensions: %s", env.Set["AZURE_EXTENSION_DIR"])
	}
	if fi, err := os.Stat(want["AZURE_CONFIG_DIR"]); err != nil || !fi.IsDir() {
		t.Errorf("config dir not created: %v", err)
	}
	if AzureDir(ctx) != want["AZURE_CONFIG_DIR"] || AzureWizardDir() != filepath.Join(home, "azure", ".wizard") {
		t.Errorf("dirs: %s %s", AzureDir(ctx), AzureWizardDir())
	}
	if AzureLoggedIn(AzureDir(ctx)) {
		t.Error("logged in before az login")
	}
	os.WriteFile(filepath.Join(AzureDir(ctx), "azureProfile.json"), []byte("{}"), 0o600)
	if !AzureLoggedIn(AzureDir(ctx)) {
		t.Error("not logged in after az login")
	}
	if got := fmt.Sprint(p.LoginCommands(false)); got != "[[az login --tenant t1] [az account set --subscription "+subID+"]]" {
		t.Errorf("login: %s", got)
	}

	ctx.Region = ""
	env, _ = p.Prepare()
	if _, ok := env.Set["AZURE_DEFAULTS_LOCATION"]; ok || !strings.Contains(strings.Join(env.Unset, " "), "AZURE_DEFAULTS_LOCATION") {
		t.Errorf("no region: %+v", env)
	}
}

func TestAzureProviderErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	z := &Azure{ctx: &config.Context{Name: "z", Provider: config.ProviderAzure, TenantID: "t", SubscriptionID: "s"}, dir: file}
	if _, err := z.Prepare(); err == nil {
		t.Error("expected MkdirAll error")
	}
	z.dir = t.TempDir()
	t.Setenv("AZURE_EXTENSION_DIR", "")
	old := userHome
	userHome = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { userHome = old })
	if _, err := z.Prepare(); err == nil {
		t.Error("expected no-home error")
	}
	if got := strings.Join(z.WhoAmICommand(), " "); !strings.HasPrefix(got, "az account show --query") {
		t.Errorf("whoami: %s", got)
	}
	if got := z.Describe(); got != "azure s / -" {
		t.Errorf("describe: %s", got)
	}
}

func TestValidateAzure(t *testing.T) {
	head := "contexts:\n  a:\n    provider: azure\n"
	for in, want := range map[string]string{
		head + "    tenant_id: t\n":                                                             "needs tenant_id and subscription_id",
		head + "    tenant_id: t\n    subscription_id: nope\n":                                  "subscription_id must be a GUID",
		head + "    tenant_id: t\n    subscription_id: " + subID + "\n    project: p\n":         "project is not for azure contexts",
		head + "    tenant_id: t\n    subscription_id: " + subID + "\n    use_metadata: true\n": "use_metadata is not for azure contexts",
		"contexts:\n  a:\n    provider: gcp\n    project: p\n    tenant_id: t\n":                "tenant_id and subscription_id are for azure contexts",
	} {
		if err := parse(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parse(%q) = %v, want %q", in, err, want)
		}
	}
}
