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
	Validate: func(c *config.Context) error {
		if c.HcloudProfile == "" {
			return errors.New("huawei context needs hcloud_profile (a KooCLI profile, see `hcloud configure list`)")
		}
		return nil
	},
	Tool: Tool{VersionArgs: []string{"version"}, URL: "https://support.huaweicloud.com/intl/en-us/qs-hcli/hcli_02_003.html"},
}

// Huawei wraps Huawei Cloud's KooCLI (hcloud).
//
// KooCLI keeps every profile in ~/.hcloud/config.json, ignores $HOME, and
// `hcloud configure set` also switches the user's current profile, so mek
// never writes that file. A context points at a profile the user created;
// mek adds --cli-profile / --cli-region to each command (KooCLI has no
// environment variable for them) and sets HW_* for terraform.
type Huawei struct {
	ctx *config.Context
}

// Static keys would win over the profile in terraform / SDKs.
var huaweiUnset = []string{"HW_ACCESS_KEY", "HW_SECRET_KEY", "HW_SECURITY_TOKEN"}

func (h *Huawei) Prepare() (Env, error) {
	env := Env{Set: map[string]string{"HW_PROFILE": h.ctx.HcloudProfile}, Unset: huaweiUnset}
	if h.ctx.Region != "" {
		env.Set["HW_REGION_NAME"] = h.ctx.Region
	}
	return env, nil
}

// RewriteArgs points an hcloud API command at the context's profile and
// region, unless the user passed those flags explicitly.
func (h *Huawei) RewriteArgs(args []string) []string {
	if !guard.IsHcloudAPICommand(args) {
		return args // KooCLI's own commands (configure, version, ...) take no profile
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

// hcloudConfigPath is where KooCLI keeps its profiles (read-only for mek):
// $MEK_HCLOUD_CONFIG, else ~/.hcloud/config.json under the account's home
// directory. KooCLI looks the home up in the user database and ignores
// $HOME, so mek must not trust $HOME either (they differ under sudo -E,
// in containers and CI).
var hcloudConfigPath = func() string {
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

// profileMode returns the KooCLI auth mode (SSO, AKSK, ...) of profile.
func profileMode(profile string) (string, error) {
	b, err := os.ReadFile(hcloudConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("no KooCLI config at %s — create the profile first (see `mek login --help`)", hcloudConfigPath())
	}
	if err != nil {
		return "", err
	}
	var cfg struct {
		Profiles []struct{ Name, Mode string } `json:"profiles"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return "", fmt.Errorf("read %s: %w", hcloudConfigPath(), err)
	}
	for _, p := range cfg.Profiles {
		if p.Name == profile {
			return p.Mode, nil
		}
	}
	return "", fmt.Errorf("KooCLI profile %q not found in %s — create it first (see `mek login --help`)", profile, hcloudConfigPath())
}

func (h *Huawei) LoginCommands(bool) ([][]string, error) {
	mode, err := profileMode(h.ctx.HcloudProfile)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(mode, "SSO") {
		return nil, fmt.Errorf("KooCLI profile %q uses %s mode, which has no login step — mek uses it as-is", h.ctx.HcloudProfile, mode)
	}
	return [][]string{{"hcloud", "configure", "sso", "--cli-profile=" + h.ctx.HcloudProfile}}, nil
}

func (h *Huawei) WhoAmICommand() []string {
	return []string{"hcloud", "configure", "show", "--cli-profile=" + h.ctx.HcloudProfile}
}

func (h *Huawei) Describe() string {
	return fmt.Sprintf("huawei profile %s / %s", h.ctx.HcloudProfile, orDash(h.ctx.Region))
}
