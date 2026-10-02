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
		return nil
	},
	Tool: Tool{VersionArgs: []string{"--version"}, URL: "https://cloud.google.com/sdk/docs/install"},
}

// GCP wraps gcloud. Each context gets its own CLOUDSDK_CONFIG directory so
// accounts, projects and credentials never leak between contexts or into the
// user's default ~/.config/gcloud.
type GCP struct {
	ctx *config.Context
	dir string
}

func (g *GCP) configDir() string { return filepath.Join(g.dir, "gcloud", g.ctx.Name) }

func (g *GCP) Prepare() (Env, error) {
	dir := g.configDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Env{}, err
	}
	env := Env{Set: map[string]string{
		"CLOUDSDK_CONFIG":       dir,
		"CLOUDSDK_CORE_PROJECT": g.ctx.Project,
		"GOOGLE_CLOUD_PROJECT":  g.ctx.Project,
	}, Unset: []string{"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_ACTIVE_CONFIG_NAME"}}
	if g.ctx.Account != "" {
		env.Set["CLOUDSDK_CORE_ACCOUNT"] = g.ctx.Account
	}
	if g.ctx.Region != "" {
		env.Set["CLOUDSDK_COMPUTE_REGION"] = g.ctx.Region
	}
	// Client libraries / terraform pick up ADC from here once `mek login --adc` has run.
	adc := filepath.Join(dir, "application_default_credentials.json")
	if _, err := os.Stat(adc); err == nil {
		env.Set["GOOGLE_APPLICATION_CREDENTIALS"] = adc
	}
	return env, nil
}

func (g *GCP) LoginCommands(adc bool) ([][]string, error) {
	login := []string{"gcloud", "auth", "login"}
	if g.ctx.Account != "" {
		login = append(login, g.ctx.Account)
	}
	cmds := [][]string{login}
	if adc {
		cmds = append(cmds, []string{"gcloud", "auth", "application-default", "login"})
	}
	return cmds, nil
}

func (g *GCP) WhoAmICommand() []string {
	return []string{"gcloud", "auth", "list", "--filter=status:ACTIVE", "--format=value(account)"}
}

func (g *GCP) Describe() string {
	return fmt.Sprintf("gcp %s / %s", g.ctx.Project, orDash(g.ctx.Region))
}
