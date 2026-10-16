package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

var azureCloud = Cloud{
	Name: config.ProviderAzure, CLI: "az", Title: "Azure CLI",
	New: func(_ *config.Config, ctx *config.Context, dir string) Provider {
		return &Azure{ctx: ctx, dir: dir}
	},
	Classify: guard.ClassifyAzure,
	Validate: validateAzure,
	Tool: Tool{VersionArgs: []string{"version", "--output", "tsv", "--query", `"azure-cli"`},
		Brew: "brew install azure-cli", URL: "https://learn.microsoft.com/cli/azure/install-azure-cli"},
	Plugins: []Plugin{{
		Bin: "kubelogin", Purpose: "AKS credentials for kubectl (mek kube)", Needed: hasClusters,
		Tool: Tool{VersionArgs: []string{"--version"}, Brew: "brew install Azure/kubelogin/kubelogin",
			URL: "https://azure.github.io/kubelogin/install.html"},
	}},
}

var guid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsGUID is whether s is a GUID, as subscription ids are.
func IsGUID(s string) bool { return guid.MatchString(s) }

func validateAzure(c *config.Context) error {
	if err := notHuawei(c); err != nil {
		return err
	}
	if c.TenantID == "" || c.SubscriptionID == "" {
		return errors.New("azure context needs tenant_id and subscription_id")
	}
	if !guid.MatchString(c.SubscriptionID) {
		return fmt.Errorf("subscription_id must be a GUID, got %q", c.SubscriptionID)
	}
	for _, f := range []struct {
		key string
		set bool
	}{
		{"sso_start_url", c.SSOStartURL != ""}, {"sso_region", c.SSORegion != ""}, {"account_id", c.AccountID != ""},
		{"role", c.Role != ""}, {"aws_profile", c.AWSProfile != ""}, {"project", c.Project != ""},
		{"account", c.Account != ""}, {"use_metadata", c.UseMetadata},
	} {
		if f.set {
			return fmt.Errorf("%s is not for azure contexts", f.key)
		}
	}
	for alias, cl := range c.Clusters {
		if cl.Region != "" || cl.Location != "" || cl.Endpoint != "" {
			return fmt.Errorf("clusters.%s: azure clusters take resource_group, not region, location or endpoint", alias)
		}
		if cl.ResourceGroup == "" {
			return fmt.Errorf("clusters.%s needs a resource_group", alias)
		}
	}
	for alias, t := range c.Targets {
		if t.Zone != "" {
			return fmt.Errorf("targets.%s: azure targets take resource_group, not zone", alias)
		}
		if !azureVMID.MatchString(t.Instance) && !azureVMName.MatchString(t.Instance) {
			return fmt.Errorf("targets.%s: instance %q is not a VM name or resource id", alias, t.Instance)
		}
		if t.Bastion == nil && c.Bastion == nil {
			return fmt.Errorf("targets.%s: set bastion on the target or the context", alias)
		}
	}
	for alias, t := range c.Tunnels {
		target, named := c.Targets[t.Via]
		if t.Via != "" && !named && !azureVMID.MatchString(t.Via) && !azureVMName.MatchString(t.Via) {
			return fmt.Errorf("tunnels.%s: via %q is not a target name, VM name or resource id", alias, t.Via)
		}
		if t.Via != "" && (!named || target.Bastion == nil) && c.Bastion == nil {
			return fmt.Errorf("tunnels.%s: set the context's bastion", alias)
		}
	}
	return nil
}

// cmpErr is the first error that isn't nil.
func cmpErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// notAzure rejects the Azure-only fields on another cloud's context.
func notAzure(c *config.Context) error {
	if c.TenantID != "" || c.SubscriptionID != "" {
		return errors.New("tenant_id and subscription_id are for azure contexts")
	}
	if c.Bastion != nil {
		return errors.New("bastion is for azure contexts")
	}
	for alias, t := range c.Targets {
		if t.ResourceGroup != "" || t.Auth != "" || t.Bastion != nil {
			return fmt.Errorf("targets.%s: resource_group, auth and bastion are for azure", alias)
		}
	}
	for alias, cl := range c.Clusters {
		if cl.ResourceGroup != "" {
			return fmt.Errorf("clusters.%s: resource_group is for azure", alias)
		}
	}
	return nil
}

// Azure wraps az. Each context gets its own AZURE_CONFIG_DIR, so the login,
// token cache and default subscription never leak between contexts or into
// the user's ~/.azure.
type Azure struct {
	ctx *config.Context
	dir string
}

// Service-principal and managed-identity variables would quietly win over
// the context's az login in SDKs and terraform.
var azureUnset = []string{
	"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_CLIENT_CERTIFICATE_PATH",
	"AZURE_USERNAME", "AZURE_PASSWORD",
	"ARM_CLIENT_ID", "ARM_CLIENT_SECRET", "ARM_CLIENT_CERTIFICATE_PATH", "ARM_USE_MSI", "ARM_USE_OIDC",
}

func (z *Azure) configDir() string { return filepath.Join(z.dir, "azure", z.ctx.Key()) }

// AzureDir is a context's own az config directory: its login.
func AzureDir(c *config.Context) string { return (&Azure{ctx: c, dir: config.Dir()}).configDir() }

// AzureWizardDir is where mek ctx add logs in before the new context has a
// key; saving moves it to the context's AzureDir.
func AzureWizardDir() string { return filepath.Join(config.Dir(), "azure", ".wizard") }

func (z *Azure) Prepare() (Env, error) {
	dir := z.configDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Env{}, err
	}
	env, err := AzureEnv(dir)
	if err != nil {
		return Env{}, err
	}
	for k, v := range map[string]string{
		// SDKs (DefaultAzureCredential) and azd
		"AZURE_TENANT_ID":       z.ctx.TenantID,
		"AZURE_SUBSCRIPTION_ID": z.ctx.SubscriptionID,
		// terraform azurerm
		"ARM_TENANT_ID":       z.ctx.TenantID,
		"ARM_SUBSCRIPTION_ID": z.ctx.SubscriptionID,
	} {
		env.Set[k] = v
	}
	if z.ctx.Region != "" {
		env.Set["AZURE_DEFAULTS_LOCATION"] = z.ctx.Region
	} else {
		env.Unset = append(env.Unset, "AZURE_DEFAULTS_LOCATION")
	}
	return env, nil
}

// AzureEnv is the environment for az with its config in dir: the
// extensions are the user's own, shared by every context (they are code,
// not credentials), and az login never asks which subscription to use —
// mek sets it.
func AzureEnv(dir string) (Env, error) {
	ext, err := extensionDir()
	if err != nil {
		return Env{}, err
	}
	return Env{Set: map[string]string{
		"AZURE_CONFIG_DIR":               dir,
		"AZURE_EXTENSION_DIR":            ext,
		"AZURE_CORE_LOGIN_EXPERIENCE_V2": "off",
	}, Unset: azureUnset}, nil
}

// AzureLoggedIn reports whether an az config directory has a login.
func AzureLoggedIn(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "azureProfile.json"))
	return err == nil
}

func (z *Azure) LoginCommands(bool) [][]string {
	return [][]string{
		{"az", "login", "--tenant", z.ctx.TenantID},
		{"az", "account", "set", "--subscription", z.ctx.SubscriptionID},
	}
}

func (z *Azure) WhoAmICommand() []string {
	return []string{"az", "account", "show", "--query", "{subscription: name, id: id, user: user.name}", "--output", "tsv"}
}

func (z *Azure) Describe() string {
	return fmt.Sprintf("azure %s / %s", z.ctx.SubscriptionID, orDash(z.ctx.Region))
}
