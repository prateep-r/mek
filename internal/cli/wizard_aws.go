package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/wizard"
)

// awsFlow asks how you sign in to AWS: IAM Identity Center, or a profile
// the AWS CLI already has.
type awsFlow struct{}

func (awsFlow) option() wizard.Option {
	return wizard.Option{Value: config.ProviderAWS, Label: "AWS"}
}

var awsRegions = [][2]string{
	{"us-east-1", "N. Virginia"}, {"us-east-2", "Ohio"}, {"us-west-1", "N. California"}, {"us-west-2", "Oregon"},
	{"ca-central-1", "Canada"}, {"ca-west-1", "Calgary"}, {"mx-central-1", "Mexico"}, {"sa-east-1", "São Paulo"},
	{"eu-west-1", "Ireland"}, {"eu-west-2", "London"}, {"eu-west-3", "Paris"}, {"eu-central-1", "Frankfurt"},
	{"eu-central-2", "Zurich"}, {"eu-north-1", "Stockholm"}, {"eu-south-1", "Milan"}, {"eu-south-2", "Spain"},
	{"ap-southeast-1", "Singapore"}, {"ap-southeast-2", "Sydney"}, {"ap-southeast-3", "Jakarta"},
	{"ap-southeast-4", "Melbourne"}, {"ap-southeast-5", "Malaysia"}, {"ap-southeast-7", "Thailand"},
	{"ap-east-1", "Hong Kong"}, {"ap-east-2", "Taipei"}, {"ap-northeast-1", "Tokyo"}, {"ap-northeast-2", "Seoul"},
	{"ap-northeast-3", "Osaka"}, {"ap-south-1", "Mumbai"}, {"ap-south-2", "Hyderabad"},
	{"me-south-1", "Bahrain"}, {"me-central-1", "UAE"}, {"il-central-1", "Tel Aviv"}, {"af-south-1", "Cape Town"},
}

var twelveDigits = regexp.MustCompile(`^[0-9]{12}$`)

func checkAccountID(v string, _ wizard.Answers) error {
	if !twelveDigits.MatchString(v) {
		return errors.New("an AWS account id is 12 digits")
	}
	return nil
}

func checkPortal(v string, _ wizard.Answers) error {
	if !strings.HasPrefix(v, "https://") {
		return errors.New("the portal's URL starts with https://, e.g. https://acme.awsapps.com/start")
	}
	return nil
}

func (awsFlow) steps(f *wizard.Form, w *ctxWizard) {
	on := cloudIs(config.ProviderAWS)
	sso := func(a wizard.Answers) bool { return on(a) && a["aws_auth"] == "sso" }
	f.Select("aws_auth", "Sign in with", wizard.Options(
		wizard.Option{Value: "sso", Label: "IAM Identity Center (SSO)", Hint: "your organisation's AWS access portal"},
		wizard.Option{Value: "profile", Label: "a profile from ~/.aws/config", Hint: "access keys, or anything the AWS CLI already knows"},
	), false).When(on)
	f.Select("aws_profile", "Profile", func(wizard.Answers) []wizard.Option { return awsProfiles() }, true).
		When(func(a wizard.Answers) bool { return on(a) && a["aws_auth"] == "profile" }).
		Check(required("a profile name")).Help("the profiles in ~/.aws/config, used as they are")
	f.Select("sso_start_url", "Access portal", w.portals, true).When(sso).Check(checkPortal).
		Help("your AWS access portal's URL, e.g. https://acme.awsapps.com/start")
	f.Select("sso_region", "Portal region", wizard.Options(regionOptions(awsRegions, "")...), true).
		When(func(a wizard.Answers) bool { return sso(a) && w.portalRegion(a["sso_start_url"]) == "" }).
		Check(required("a region")).Help("the region IAM Identity Center runs in")
	f.Input("account_id", "Account", nil, checkAccountID).When(sso).Help("the 12-digit account id")
	f.Input("role", "Role", nil, required("a role")).When(sso).Help("the permission set's name, e.g. Developer")
	f.Select("aws_region", "Region", wizard.Options(regionOptions(awsRegions, "the AWS CLI's default")...), true).
		When(on).Help("where commands go")
}

// portals are the access portals other contexts use.
func (w *ctxWizard) portals(wizard.Answers) []wizard.Option {
	var out []wizard.Option
	for _, n := range w.cfg.Names() {
		c := w.cfg.Contexts[n]
		if c.Provider != config.ProviderAWS || c.SSOStartURL == "" || slices.ContainsFunc(out, func(o wizard.Option) bool { return o.Value == c.SSOStartURL }) {
			continue
		}
		hint := w.usedBy(func(o *config.Context) bool { return o.SSOStartURL == c.SSOStartURL })
		out = append(out, wizard.Option{Value: c.SSOStartURL, Hint: hint})
	}
	return out
}

// portalRegion is the region another context gives a portal: one portal has
// one region, so it isn't asked again ("" when no other context uses it).
func (w *ctxWizard) portalRegion(url string) string {
	for _, n := range w.cfg.Names() {
		c := w.cfg.Contexts[n]
		if c.SSOStartURL == url && (w.old == nil || n != w.old.Name) {
			if c.SSORegion != "" {
				return c.SSORegion
			}
			return c.Region
		}
	}
	return ""
}

// awsProfiles are the profile names in the AWS CLI's own config file — not
// the one mek writes, when $AWS_CONFIG_FILE points there from mek env.
func awsProfiles() []wizard.Option {
	path := os.Getenv("AWS_CONFIG_FILE")
	if path == "" || strings.HasPrefix(path, config.Dir()) {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".aws", "config")
	}
	b, _ := os.ReadFile(path) // none: type one
	var out []wizard.Option
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
			continue
		}
		name := strings.TrimSpace(line[1 : len(line)-1])
		if p, ok := strings.CutPrefix(name, "profile "); ok {
			name = strings.TrimSpace(p)
		} else if name != "default" {
			continue // [sso-session …], [services …]
		}
		out = append(out, wizard.Option{Value: name})
	}
	return out
}

func (awsFlow) prefill(c *config.Context, a wizard.Answers) {
	a["aws_region"] = c.Region
	if c.AWSProfile != "" {
		a["aws_auth"], a["aws_profile"] = "profile", c.AWSProfile
		return
	}
	a["aws_auth"], a["sso_start_url"], a["sso_region"], a["account_id"], a["role"] = "sso", c.SSOStartURL, c.SSORegion, c.AccountID, c.Role
}

func (awsFlow) apply(w *ctxWizard, a wizard.Answers, c *config.Context) {
	c.Region = a["aws_region"]
	if a["aws_auth"] == "profile" {
		c.AWSProfile = a["aws_profile"]
		return
	}
	c.SSOStartURL, c.SSORegion, c.AccountID, c.Role = a["sso_start_url"], a["sso_region"], a["account_id"], a["role"]
	if r := w.portalRegion(c.SSOStartURL); r != "" {
		c.SSORegion = r
	}
}
