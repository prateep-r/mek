package cli

import (
	"strings"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/wizard"
)

// huaweiFlow asks for a KooCLI profile — mek uses one you made with
// hcloud, never writing KooCLI's file — and a region.
type huaweiFlow struct{}

func (huaweiFlow) option() wizard.Option {
	return wizard.Option{Value: config.ProviderHuawei, Label: "Huawei Cloud"}
}

var huaweiRegions = [][2]string{
	{"ap-southeast-1", "Hong Kong"}, {"ap-southeast-2", "Bangkok"}, {"ap-southeast-3", "Singapore"},
	{"ap-southeast-4", "Jakarta"}, {"ap-southeast-5", "Manila"}, {"cn-north-4", "Beijing 4"},
	{"cn-east-3", "Shanghai 1"}, {"cn-south-1", "Guangzhou"}, {"af-south-1", "Johannesburg"},
	{"la-south-2", "Santiago"}, {"sa-brazil-1", "São Paulo"}, {"me-east-1", "Riyadh"}, {"tr-west-1", "Istanbul"},
}

func (huaweiFlow) steps(f *wizard.Form, w *ctxWizard) {
	on := cloudIs(config.ProviderHuawei)
	f.Load("hcloud_profile", "KooCLI profile", hcloudProfiles).When(on).Check(required("a profile name")).
		Help("from ~/.hcloud/config.json — make one with: hcloud configure init")
	f.Select("hw_region", "Region", wizard.Options(regionOptions(huaweiRegions, "the profile's own")...), true).
		When(on).Help("where commands go (--cli-region); none: the profile's")
}

// hcloudProfiles are KooCLI's profiles, with their mode and region.
func hcloudProfiles(wizard.Answers) ([]wizard.Option, error) {
	profiles, err := provider.HcloudProfiles()
	var opts []wizard.Option
	for _, p := range profiles {
		opts = append(opts, wizard.Option{Value: p.Name, Hint: strings.TrimSpace(p.Mode + " " + p.Region)})
	}
	return opts, err
}

func (huaweiFlow) prefill(c *config.Context, a wizard.Answers) {
	a["hcloud_profile"], a["hw_region"] = c.HcloudProfile, c.Region
}

func (huaweiFlow) apply(_ *ctxWizard, a wizard.Answers, c *config.Context) {
	c.HcloudProfile, c.Region = a["hcloud_profile"], a["hw_region"]
}
