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
		return nil
	},
	Tool: Tool{VersionArgs: []string{"version", "--output", "tsv", "--query", `"azure-cli"`},
		Brew: "brew install azure-cli", URL: "https://learn.microsoft.com/cli/azure/install-azure-cli"},
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
