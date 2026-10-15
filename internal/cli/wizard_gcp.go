package cli

import (
	"slices"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/wizard"
)

// gcpFlow asks for the Google account and the project.
type gcpFlow struct{}

func (gcpFlow) option() wizard.Option {
	return wizard.Option{Value: config.ProviderGCP, Label: "GCP"}
}

var gcpRegions = [][2]string{
	{"asia-southeast1", "Singapore"}, {"asia-southeast2", "Jakarta"}, {"asia-east1", "Taiwan"},
	{"asia-east2", "Hong Kong"}, {"asia-northeast1", "Tokyo"}, {"asia-northeast2", "Osaka"},
	{"asia-northeast3", "Seoul"}, {"asia-south1", "Mumbai"}, {"asia-south2", "Delhi"},
	{"australia-southeast1", "Sydney"}, {"australia-southeast2", "Melbourne"},
	{"europe-west1", "Belgium"}, {"europe-west2", "London"}, {"europe-west3", "Frankfurt"},
	{"europe-west4", "Netherlands"}, {"europe-west6", "Zurich"}, {"europe-north1", "Finland"},
	{"europe-central2", "Warsaw"}, {"me-central1", "Doha"}, {"me-west1", "Tel Aviv"},
	{"northamerica-northeast1", "Montréal"}, {"southamerica-east1", "São Paulo"},
	{"us-central1", "Iowa"}, {"us-east1", "South Carolina"}, {"us-east4", "N. Virginia"},
	{"us-west1", "Oregon"}, {"us-west2", "Los Angeles"},
}

func (gcpFlow) steps(f *wizard.Form, w *ctxWizard) {
	on := cloudIs(config.ProviderGCP)
	f.Select("gcp_account", "Google account", w.googleAccounts, true).When(on).
		Help("who mek login signs in as")
	f.Input("project", "Project", nil, required("a project id")).When(on).Help("the project id, e.g. acme-dev")
	f.Select("gcp_region", "Region", wizard.Options(regionOptions(gcpRegions, "gcloud's default")...), true).
		When(on).Help("where commands go")
}

// googleAccounts are the accounts other contexts use, after "none".
func (w *ctxWizard) googleAccounts(wizard.Answers) []wizard.Option {
	out := []wizard.Option{{Value: "", Label: "none", Hint: "mek login asks which account"}}
	for _, n := range w.cfg.Names() {
		c := w.cfg.Contexts[n]
		if c.Account == "" || slices.ContainsFunc(out, func(o wizard.Option) bool { return o.Value == c.Account }) {
			continue
		}
		out = append(out, wizard.Option{Value: c.Account, Hint: w.usedBy(func(o *config.Context) bool { return o.Account == c.Account })})
	}
	return out
}

func (gcpFlow) prefill(c *config.Context, a wizard.Answers) {
	a["gcp_account"], a["project"], a["gcp_region"] = c.Account, c.Project, c.Region
}

func (gcpFlow) apply(_ *ctxWizard, a wizard.Answers, c *config.Context) {
	c.Account, c.Project, c.Region = a["gcp_account"], a["project"], a["gcp_region"]
}
