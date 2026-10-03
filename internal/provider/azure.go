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
	Validate: func(c *config.Context) error {
		if c.TenantID == "" || c.SubscriptionID == "" {
			return errors.New("azure context needs tenant_id and subscription_id")
		}
		if !guid.MatchString(c.SubscriptionID) {
			return fmt.Errorf("subscription_id must be a GUID, got %q", c.SubscriptionID)
		}
		return validateAzureAccess(c)
	},
	Tool: Tool{VersionArgs: []string{"version", "--output", "tsv", "--query", `"azure-cli"`},
		Brew: "brew install azure-cli", URL: "https://learn.microsoft.com/cli/azure/install-azure-cli"},
	Plugins: []Plugin{{
		Bin: "kubelogin", Purpose: "AKS credentials for kubectl (mek kube)", Needed: hasClusters,
		Tool: Tool{VersionArgs: []string{"--version"}, Brew: "brew install Azure/kubelogin/kubelogin",
			URL: "https://azure.github.io/kubelogin/install.html"},
	}},
}

// validateAzureAccess checks clusters, targets and the Bastion they need.
func validateAzureAccess(c *config.Context) error {
	for alias, cl := range c.Clusters {
		if cl.Region != "" || cl.Location != "" {
			return fmt.Errorf("clusters.%s: azure clusters take resource_group, not region or location", alias)
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
		if _, ok := c.Targets[t.Via]; t.Via != "" && !ok && !azureVMID.MatchString(t.Via) && !azureVMName.MatchString(t.Via) {
			return fmt.Errorf("tunnels.%s: via %q is not a target name, VM name or resource id", alias, t.Via)
		}
		if t.Via != "" && c.Targets[t.Via] == nil && c.Bastion == nil {
			return fmt.Errorf("tunnels.%s: set the context's bastion", alias)
		}
	}
	return nil
}

var guid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Azure wraps the az CLI. Each context gets its own AZURE_CONFIG_DIR, so the
// login, token cache and default subscription never leak between contexts or
// into the user's ~/.azure.
type Azure struct {
	ctx *config.Context
	dir string
}

// Service-principal / managed-identity variables would silently win over the
// context's az login in SDKs and terraform.
var azureUnset = []string{
	"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_CLIENT_CERTIFICATE_PATH",
	"AZURE_USERNAME", "AZURE_PASSWORD",
	"ARM_CLIENT_ID", "ARM_CLIENT_SECRET", "ARM_CLIENT_CERTIFICATE_PATH", "ARM_USE_MSI", "ARM_USE_OIDC",
}

func (z *Azure) configDir() string { return filepath.Join(z.dir, "azure", z.ctx.Name) }

func (z *Azure) Prepare() (Env, error) {
	dir := z.configDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Env{}, err
	}
	env := Env{Set: map[string]string{
		"AZURE_CONFIG_DIR": dir,
		// SDKs (DefaultAzureCredential) and azd
		"AZURE_TENANT_ID":       z.ctx.TenantID,
		"AZURE_SUBSCRIPTION_ID": z.ctx.SubscriptionID,
		// terraform azurerm
		"ARM_TENANT_ID":       z.ctx.TenantID,
		"ARM_SUBSCRIPTION_ID": z.ctx.SubscriptionID,
		// mek picks the subscription itself; skip az login's interactive picker
		"AZURE_CORE_LOGIN_EXPERIENCE_V2": "off",
	}, Unset: azureUnset}
	// Extensions (bastion, ssh) are shared with the user's own az.
	ext, err := extensionDir()
	if err != nil {
		return Env{}, err
	}
	env.Set["AZURE_EXTENSION_DIR"] = ext
	if z.ctx.Region != "" {
		env.Set["AZURE_DEFAULTS_LOCATION"] = z.ctx.Region
	}
	return env, nil
}

func (z *Azure) LoginCommands(bool) ([][]string, error) {
	return [][]string{
		{"az", "login", "--tenant", z.ctx.TenantID},
		{"az", "account", "set", "--subscription", z.ctx.SubscriptionID},
	}, nil
}

func (z *Azure) WhoAmICommand() []string {
	return []string{"az", "account", "show", "--output", "table"}
}

func (z *Azure) Describe() string {
	return fmt.Sprintf("azure %s / %s", z.ctx.SubscriptionID, orDash(z.ctx.Region))
}
