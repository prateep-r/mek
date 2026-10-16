package cli

import (
	"errors"
	"os"
	"strings"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/wizard"
)

// azureFlow asks for the tenant, the subscription, the Bastion and the
// region, listed once logged in.
type azureFlow struct{}

func (azureFlow) option() wizard.Option {
	return wizard.Option{Value: config.ProviderAzure, Label: "Azure"}
}

var azureRegions = [][2]string{
	{"southeastasia", "Singapore"}, {"eastasia", "Hong Kong"}, {"japaneast", "Tokyo"}, {"japanwest", "Osaka"},
	{"koreacentral", "Seoul"}, {"centralindia", "Pune"}, {"australiaeast", "New South Wales"},
	{"indonesiacentral", "Jakarta"}, {"malaysiawest", "Kuala Lumpur"},
	{"northeurope", "Ireland"}, {"westeurope", "Netherlands"}, {"uksouth", "London"},
	{"germanywestcentral", "Frankfurt"}, {"francecentral", "Paris"}, {"swedencentral", "Gävle"},
	{"switzerlandnorth", "Zurich"}, {"uaenorth", "Dubai"},
	{"eastus", "Virginia"}, {"eastus2", "Virginia"}, {"centralus", "Iowa"}, {"westus2", "Washington"},
	{"westus3", "Phoenix"}, {"canadacentral", "Toronto"}, {"brazilsouth", "São Paulo"},
}

func (azureFlow) steps(f *wizard.Form, w *ctxWizard) {
	on := cloudIs(config.ProviderAzure)
	f.Exec("az_login", "Log in to Azure", func(wizard.Answers) string {
		return strings.Join(provider.AzureLoginCommand(""), " ") + "  (a browser opens)"
	}, w.azureLogin).When(func(a wizard.Answers) bool { return on(a) && !provider.AzureLoggedIn(w.azureDir()) }).
		Help("to list your tenants and subscriptions — or skip, and type them")
	f.Load("tenant", "Tenant", w.azureTenants).When(on).Check(required("a tenant id or domain")).
		Help("the Microsoft Entra tenants your login reaches")
	f.Load("subscription", "Subscription", w.azureSubscriptions).When(on).Check(checkSubscription).
		Help("the tenant's subscriptions you can see")
	f.Load("bastion", "Bastion", w.azureBastions).When(on).Check(checkBastion(true)).
		Help("the Azure Bastion that shells and tunnels go through — none: add it later")
	f.Select("az_region", "Region", wizard.Options(regionOptions(azureRegions, "az's default")...), true).
		When(on).Help("where new resources go (AZURE_DEFAULTS_LOCATION)")
}

func checkSubscription(v string, _ wizard.Answers) error {
	if !provider.IsGUID(v) {
		return errors.New("a subscription id (a GUID), please")
	}
	return nil
}

func (azureFlow) prefill(c *config.Context, a wizard.Answers) {
	a["tenant"], a["subscription"], a["az_region"] = c.TenantID, c.SubscriptionID, c.Region
	if c.Bastion != nil {
		a["bastion"] = c.Bastion.ResourceGroup + "/" + c.Bastion.Name
	}
}

func (azureFlow) apply(_ *ctxWizard, a wizard.Answers, c *config.Context) {
	c.TenantID, c.SubscriptionID, c.Region = a["tenant"], a["subscription"], a["az_region"]
	c.Bastion = bastionOf(a["bastion"])
}

// azureDir is where the wizard's az commands run: the edited context's own
// login, another Azure context's (lookups only: the new context logs in
// with mek login), or a fresh one (azure/.wizard) that becomes the new
// context's when it is saved.
func (w *ctxWizard) azureDir() string {
	if w.old != nil && w.old.Provider == config.ProviderAzure {
		return provider.AzureDir(w.old)
	}
	for _, n := range w.cfg.Names() {
		if c := w.cfg.Contexts[n]; c.Provider == config.ProviderAzure && provider.AzureLoggedIn(provider.AzureDir(c)) {
			return provider.AzureDir(c)
		}
	}
	return provider.AzureWizardDir()
}

func (w *ctxWizard) azureDraft(a wizard.Answers) (*loaded, error) {
	env, err := provider.AzureEnv(w.azureDir())
	if err != nil {
		return nil, err
	}
	return &loaded{cfg: w.cfg, ctx: w.draftCtx(a), env: env, draft: true}, nil
}

func (w *ctxWizard) azureLogin(a wizard.Answers) error {
	l, err := w.azureDraft(a)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(w.azureDir(), 0o700); err != nil {
		return err
	}
	return w.app.runPlain(provider.AzureLoginCommand(""), l.env.Apply(os.Environ()))
}

func (w *ctxWizard) azureList(a wizard.Answers, argv []string, parse func([]byte) ([]provider.Found, error)) ([]wizard.Option, error) {
	l, err := w.azureDraft(a)
	if err != nil {
		return nil, err
	}
	out, err := w.app.query(l)(argv)
	if err != nil {
		return nil, err
	}
	found, err := parse(out)
	return toOptions(found), err
}

func (w *ctxWizard) azureTenants(a wizard.Answers) ([]wizard.Option, error) {
	return w.azureList(a, provider.AzureTenantsCommand(), provider.ParseAzureTenants)
}

func (w *ctxWizard) azureSubscriptions(a wizard.Answers) ([]wizard.Option, error) {
	return w.azureList(a, provider.AzureSubscriptionsCommand(a["tenant"]), provider.ParseAzureSubscriptions)
}

// azureBastions are the subscription's Bastion hosts, after "none"; a
// value is resource-group/name.
func (w *ctxWizard) azureBastions(a wizard.Answers) ([]wizard.Option, error) {
	none := wizard.Option{Value: "", Label: "none", Hint: "no shells or tunnels yet"}
	l, err := w.azureDraft(a)
	if err != nil {
		return nil, err
	}
	out, err := w.app.query(l)(provider.AzureBastionsCommand(a["subscription"]))
	if err != nil {
		return nil, err
	}
	found, err := provider.ParseAzureBastions(out)
	opts := []wizard.Option{none}
	for _, f := range found {
		opts = append(opts, wizard.Option{Value: f.Extra + "/" + f.Value, Label: f.Value, Hint: f.Hint})
	}
	return opts, err
}
