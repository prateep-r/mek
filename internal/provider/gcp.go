package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

var gcpCloud = Cloud{
	Name: config.ProviderGCP, CLI: "gcloud", Title: "Google Cloud CLI",
	New: func(_ *config.Config, ctx *config.Context, dir string) Provider {
		return &GCP{ctx: ctx, dir: dir}
	},
	Classify: guard.ClassifyGCloud,
	Validate: func(c *config.Context) error {
		if c.Project == "" {
			return errors.New("gcp context needs project")
		}
		for alias, cl := range c.Clusters {
			if cl.Region != "" {
				return fmt.Errorf("clusters.%s: gcp clusters use location (zone or region), not region", alias)
			}
			if cl.Location == "" {
				return fmt.Errorf("clusters.%s needs a location (zone or region)", alias)
			}
			if cl.Endpoint != "" && cl.Endpoint != "private" && cl.Endpoint != "dns" {
				return fmt.Errorf("clusters.%s: endpoint is private or dns, not %q", alias, cl.Endpoint)
			}
		}
		for alias, t := range c.Targets {
			if !gcpVMName.MatchString(t.Instance) {
				return fmt.Errorf("targets.%s: instance %q is not a VM name", alias, t.Instance)
			}
		}
		for alias, t := range c.Tunnels {
			if _, ok := c.Targets[t.Via]; t.Via != "" && !ok && !gcpVMName.MatchString(t.Via) {
				return fmt.Errorf("tunnels.%s: via %q is not a target name or VM name", alias, t.Via)
			}
		}
		return nil
	},
	Tool: Tool{VersionArgs: []string{"--version"}, Winget: "winget install Google.CloudSDK", URL: "https://cloud.google.com/sdk/docs/install"},
	Plugins: []Plugin{{
		Bin: "ssh", Purpose: "GCP shells and tunnels to another host (gcloud compute ssh)", Needed: needsOpenSSH,
		Tool: Tool{VersionArgs: []string{"-V"}, URL: "https://www.openssh.com (Debian/Ubuntu: apt install openssh-client)"},
	}, {
		Bin: "ssh-keygen", Purpose: "the SSH key gcloud compute ssh creates", Needed: needsOpenSSH,
		Tool: Tool{URL: "https://www.openssh.com (Debian/Ubuntu: apt install openssh-client)"}, // no version flag: only found, never run
	}, {
		Bin: "cloud-sql-proxy", Purpose: "Cloud SQL tunnels (mek tunnel)", Needed: hasCloudSQL,
		Tool: Tool{VersionArgs: []string{"--version"}, Brew: "brew install cloud-sql-proxy",
			URL: "https://cloud.google.com/sql/docs/postgres/connect-auth-proxy#install"},
	}, {
		Bin: "gke-gcloud-auth-plugin", Purpose: "GKE credentials for kubectl (mek kube)", Needed: hasClusters,
		Tool: Tool{VersionArgs: []string{"--version"}, Brew: "gcloud components install gke-gcloud-auth-plugin",
			Winget: "gcloud components install gke-gcloud-auth-plugin",
			URL:    "https://cloud.google.com/kubernetes-engine/docs/how-to/cluster-access-for-kubectl#install_plugin"},
	}},
}

// GCP wraps gcloud. Each context gets its own CLOUDSDK_CONFIG directory so
// accounts, projects and credentials never leak between contexts or into the
// user's default ~/.config/gcloud.
type GCP struct {
	ctx *config.Context
	dir string
}

func (g *GCP) configDir() string { return filepath.Join(g.dir, "gcloud", g.ctx.Key()) }

// GCloudDir is a context's own gcloud config directory: its login.
func GCloudDir(c *config.Context) string { return (&GCP{ctx: c, dir: config.Dir()}).configDir() }

// GCloudWizardDir is where mek ctx add logs in before the new context has a
// key; saving moves it to the context's GCloudDir.
func GCloudWizardDir() string { return filepath.Join(config.Dir(), "gcloud", ".wizard") }

func (g *GCP) Prepare() (Env, error) {
	dir := g.configDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Env{}, err
	}
	return GCloudEnv(g.ctx, dir), nil
}

// GCloudEnv is a context's environment for gcloud, its config in dir.
func GCloudEnv(c *config.Context, dir string) Env {
	env := Env{Set: map[string]string{
		"CLOUDSDK_CONFIG":       dir,
		"CLOUDSDK_CORE_PROJECT": c.Project,
		"GOOGLE_CLOUD_PROJECT":  c.Project,
	}, Unset: []string{"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_ACTIVE_CONFIG_NAME"}}
	// Unset what this context doesn't set, so another context's `mek env`
	// can't leak its account or region in.
	if c.Account != "" {
		env.Set["CLOUDSDK_CORE_ACCOUNT"] = c.Account
	} else {
		env.Unset = append(env.Unset, "CLOUDSDK_CORE_ACCOUNT")
	}
	if c.Region != "" {
		env.Set["CLOUDSDK_COMPUTE_REGION"] = c.Region
	} else {
		env.Unset = append(env.Unset, "CLOUDSDK_COMPUTE_REGION")
	}
	// On a GCP VM (or a network with a metadata server) gcloud would quietly
	// act as the machine's service account in a context that isn't logged
	// in. Only contexts that ask for it get that identity.
	if c.UseMetadata {
		env.Unset = append(env.Unset, "CLOUDSDK_CORE_CHECK_GCE_METADATA")
	} else {
		env.Set["CLOUDSDK_CORE_CHECK_GCE_METADATA"] = "false"
	}
	// Client libraries / terraform pick up ADC from here once `mek login --adc` has run.
	adc := filepath.Join(dir, "application_default_credentials.json")
	if _, err := os.Stat(adc); err == nil {
		env.Set["GOOGLE_APPLICATION_CREDENTIALS"] = adc
	}
	return env
}

// GCloudLoggedIn reports whether a gcloud config directory has a login.
func GCloudLoggedIn(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "credentials.db"))
	return err == nil
}

// GCloudLoginCommand logs in to Google, as account when it is given.
func GCloudLoginCommand(account string) []string {
	login := []string{"gcloud", "auth", "login"}
	if account != "" {
		login = append(login, account)
	}
	return login
}

func (g *GCP) LoginCommands(adc bool) [][]string {
	cmds := [][]string{GCloudLoginCommand(g.ctx.Account)}
	if adc {
		cmds = append(cmds, []string{"gcloud", "auth", "application-default", "login"})
	}
	return cmds
}

func (g *GCP) WhoAmICommand() []string {
	return []string{"gcloud", "auth", "list", "--filter=status:ACTIVE", "--format=value(account)"}
}

func (g *GCP) Describe() string {
	return fmt.Sprintf("gcp %s / %s", g.ctx.Project, orDash(g.ctx.Region))
}
