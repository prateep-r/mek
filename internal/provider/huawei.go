package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

var huaweiCloud = Cloud{
	Name: config.ProviderHuawei, CLI: "hcloud", Title: "Huawei Cloud KooCLI",
	New: func(_ *config.Config, ctx *config.Context, _ string) Provider {
		return &Huawei{ctx: ctx}
	},
	Classify: guard.ClassifyHuawei,
	Validate: validateHuawei,
	Tool:     Tool{VersionArgs: []string{"version"}, URL: "https://support.huaweicloud.com/intl/en-us/qs-hcli/hcli_02_003.html"},
	Exports:  []string{"HW_PROFILE", "HW_REGION_NAME"},
}

func validateHuawei(c *config.Context) error {
	if c.HcloudProfile == "" {
		return errors.New("huawei context needs hcloud_profile (a KooCLI profile: hcloud configure list)")
	}
	if len(c.Clusters) > 0 || len(c.Targets) > 0 {
		return errors.New("clusters and targets are not for huawei contexts")
	}
	return nil
}

// Huawei wraps Huawei Cloud's KooCLI (hcloud).
//
// KooCLI keeps every profile in ~/.hcloud/config.json, ignores $HOME, and
// `hcloud configure set` also switches the user's current profile, so mek
// never writes that file. A context names a profile the user created; mek
// adds --cli-profile and --cli-region to each command (KooCLI has no
// environment variable for them) and sets HW_* for terraform.
type Huawei struct {
	ctx  *config.Context
	mode string // the profile's KooCLI mode, once CheckLogin found it
}

var _ ArgsRewriter = (*Huawei)(nil)

// Static keys would win over the profile in terraform and the SDKs.
var huaweiUnset = []string{"HW_ACCESS_KEY", "HW_SECRET_KEY", "HW_SECURITY_TOKEN"}

func (h *Huawei) Prepare() (Env, error) {
	env := Env{Set: map[string]string{"HW_PROFILE": h.ctx.HcloudProfile}, Unset: huaweiUnset}
	if h.ctx.Region != "" {
		env.Set["HW_REGION_NAME"] = h.ctx.Region
	} else {
		env.Unset = append(env.Unset, "HW_REGION_NAME")
	}
	return env, nil
}

// RewriteArgs points an hcloud API command at the context's profile and
// region, unless the command names its own.
func (h *Huawei) RewriteArgs(args []string) []string {
	if !guard.IsHcloudAPICommand(args) {
		return args // KooCLI's own commands (configure, version, …) take no profile
	}
	out := append([]string(nil), args...)
	if !guard.HasFlag(args, "--cli-profile") {
		out = append(out, "--cli-profile="+h.ctx.HcloudProfile)
	}
	if h.ctx.Region != "" && !guard.HasFlag(args, "--cli-region") {
		out = append(out, "--cli-region="+h.ctx.Region)
	}
	return out
}

// HcloudConfigPath is where KooCLI keeps its profiles (read only, by mek):
// $MEK_HCLOUD_CONFIG, else ~/.hcloud/config.json under the account's home
// directory. KooCLI looks the home up in the user database and ignores
// $HOME, so mek doesn't trust $HOME either (they differ under sudo -E, in
// containers and CI).
var HcloudConfigPath = func() string {
	if p := os.Getenv("MEK_HCLOUD_CONFIG"); p != "" {
		return p
	}
	home := ""
	if u, err := lookupUser(); err == nil {
		home = u.HomeDir
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".hcloud", "config.json")
}

var lookupUser = user.Current // test seam

// HcloudProfile is a KooCLI profile as its config file has it.
type HcloudProfile struct{ Name, Mode, Region string }

// HcloudProfiles reads KooCLI's profiles.
func HcloudProfiles() ([]HcloudProfile, error) {
	path := HcloudConfigPath()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no KooCLI config at %s — create a profile first: hcloud configure init", path)
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Profiles []HcloudProfile `json:"profiles"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return cfg.Profiles, nil
}

// CheckLogin finds the context's profile, so mek login can say what is
// wrong before hcloud does.
func (h *Huawei) CheckLogin() error {
	profiles, err := HcloudProfiles()
	if err != nil {
		return err
	}
	for _, p := range profiles {
		if p.Name == h.ctx.HcloudProfile {
			h.mode = p.Mode
			return nil
		}
	}
	return fmt.Errorf("KooCLI profile %q not found in %s — create it first: hcloud configure init --cli-profile=%s",
		h.ctx.HcloudProfile, HcloudConfigPath(), h.ctx.HcloudProfile)
}

// LoginCommands logs an SSO profile in; other modes (keys, an agency) have
// no login step, so there is nothing to run.
func (h *Huawei) LoginCommands(bool) [][]string {
	if !strings.EqualFold(h.mode, "SSO") {
		return nil
	}
	return [][]string{{"hcloud", "configure", "sso", "--cli-profile=" + h.ctx.HcloudProfile}}
}

func (h *Huawei) WhoAmICommand() []string {
	return []string{"hcloud", "configure", "show", "--cli-profile=" + h.ctx.HcloudProfile}
}

func (h *Huawei) Describe() string {
	return fmt.Sprintf("huawei profile %s / %s", h.ctx.HcloudProfile, orDash(h.ctx.Region))
}

// errHuaweiAccess is what mek kube, mek shell and mek tunnel say on Huawei
// Cloud: CCE hands out long-lived client certificates (mek keeps no keys),
// and there is no session service like SSM, IAP or Bastion to go through.
var errHuaweiAccess = errors.New("huawei contexts have no clusters, shells or tunnels: CCE hands out long-lived certificates, and there is no session service to go through")

func (h *Huawei) DescribeCluster(config.Cluster, Query) (KubeCluster, error) {
	return KubeCluster{}, errHuaweiAccess
}

func (h *Huawei) TokenCommand(string, string) []string { return nil }

func (h *Huawei) ResolveTarget(string, TargetOptions, Query) (Instance, error) {
	return Instance{}, errHuaweiAccess
}

func (h *Huawei) ShellCommand(Instance) (Command, error) { return Command{}, errHuaweiAccess }

func (h *Huawei) TunnelMethod(config.Tunnel) (TunnelMethod, error) { return nil, errHuaweiAccess }
