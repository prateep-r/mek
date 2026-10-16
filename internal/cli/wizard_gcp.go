package cli

import (
	"os"
	"slices"
	"strings"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
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
	f.Exec("gcp_login", "Log in to Google", func(a wizard.Answers) string {
		return strings.Join(provider.GCloudLoginCommand(a["gcp_account"]), " ") + "  (a browser opens)"
	}, w.gcpLogin).When(func(a wizard.Answers) bool { return on(a) && w.needsGCPLogin(a) }).
		Help("to list your projects — or skip, and type the project id")
	f.Load("project", "Project", w.projects).When(on).Check(required("a project id")).Help("the projects your account can see")
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

// gcloudDir is where the wizard's gcloud commands run: the edited context's
// own login (another account logs in there too), another context's login of
// the same account, or a fresh one (gcloud/.wizard) that becomes the new
// context's when it is saved.
func (w *ctxWizard) gcloudDir(a wizard.Answers) string {
	acct := a["gcp_account"]
	if w.old != nil && w.old.Provider == config.ProviderGCP {
		return provider.GCloudDir(w.old)
	}
	for _, n := range w.cfg.Names() {
		if c := w.cfg.Contexts[n]; c.Provider == config.ProviderGCP && acct != "" && c.Account == acct {
			return provider.GCloudDir(c)
		}
	}
	return provider.GCloudWizardDir()
}

// needsGCPLogin: no login in gcloudDir yet, or the edited context now names
// an account it hasn't logged in as.
func (w *ctxWizard) needsGCPLogin(a wizard.Answers) bool {
	if !provider.GCloudLoggedIn(w.gcloudDir(a)) {
		return true
	}
	acct := a["gcp_account"]
	return w.old != nil && w.old.Provider == config.ProviderGCP && acct != "" && acct != w.old.Account
}

func (w *ctxWizard) gcpDraft(a wizard.Answers) *loaded {
	c := w.draftCtx(a)
	env := provider.GCloudEnv(c, w.gcloudDir(a))
	if c.Project == "" { // not chosen yet: no project, not an empty one
		delete(env.Set, "CLOUDSDK_CORE_PROJECT")
		delete(env.Set, "GOOGLE_CLOUD_PROJECT")
	}
	return &loaded{cfg: w.cfg, ctx: c, env: env, draft: true}
}

func (w *ctxWizard) gcpLogin(a wizard.Answers) error {
	return w.app.runPlain(provider.GCloudLoginCommand(a["gcp_account"]), w.gcpDraft(a).env.Apply(os.Environ()))
}

func (w *ctxWizard) projects(a wizard.Answers) ([]wizard.Option, error) {
	out, err := w.app.query(w.gcpDraft(a))(provider.ListProjectsCommand())
	if err != nil {
		return nil, err
	}
	found, err := provider.ParseProjects(out)
	return toOptions(found), err
}

// keepLogin moves the gcloud login the wizard used to the saved context's
// own directory — from gcloud/.wizard, or from the edited context's old key
// (renamed, or a project's context whose project changed) — so it needn't
// log in again. Another context's login stays where it is. Best effort: at
// worst, mek login.
func (w *ctxWizard) keepLogin(a wizard.Answers, path string) {
	if a["cloud"] != config.ProviderGCP {
		return
	}
	from := w.gcloudDir(a)
	own := w.old != nil && w.old.Provider == config.ProviderGCP && path == w.old.Source && from == provider.GCloudDir(w.old)
	if from != provider.GCloudWizardDir() && !own {
		return
	}
	c := w.draftCtx(a)
	to := provider.GCloudDir(c)
	if from == to {
		return
	}
	os.Remove(to) // only when empty
	os.Rename(from, to)
}
