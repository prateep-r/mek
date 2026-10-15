package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
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
	f.Exec("sso_login", "Log in to the portal", func(a wizard.Answers) string {
		return strings.Join(provider.SSOLoginCommand(a["sso_start_url"]), " ") + "  (a browser opens)"
	}, w.ssoLogin).When(func(a wizard.Answers) bool { return sso(a) && provider.SSOToken(a["sso_start_url"]) == "" }).
		Help("to list the portal's accounts and roles — or skip, and type them")
	f.Load("account_id", "Account", w.accounts).When(sso).Check(checkAccountID).Help("the accounts your login can reach")
	f.Load("role", "Role", w.roles).When(sso).Check(required("a role")).Help("your roles (permission sets) in that account")
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

// awsDraft loads the draft for the portal commands: mek's generated AWS
// config gets its [sso-session]. Its profile isn't complete yet, and the
// commands don't need one.
func (w *ctxWizard) awsDraft(a wizard.Answers) (*loaded, error) {
	c := w.draftCtx(a)
	cfg := w.draftConfig(c)
	env, err := provider.For(cfg, c).Prepare()
	if err != nil {
		return nil, err
	}
	delete(env.Set, "AWS_PROFILE")
	env.Unset = append(env.Unset, "AWS_PROFILE")
	return &loaded{cfg: cfg, ctx: c, env: env, draft: true}, nil
}

func (w *ctxWizard) ssoLogin(a wizard.Answers) error {
	l, err := w.awsDraft(a)
	if err != nil {
		return err
	}
	return w.app.runPlain(provider.SSOLoginCommand(a["sso_start_url"]), l.env.Apply(os.Environ()))
}

// ssoList runs a portal command with the cached token in a file only you
// can read, removed straight after.
func (w *ctxWizard) ssoList(a wizard.Answers, argv func(input, region string) []string, parse func([]byte) ([]provider.Found, error)) ([]wizard.Option, error) {
	token := provider.SSOToken(a["sso_start_url"])
	if token == "" {
		return nil, fmt.Errorf("not logged in to %s", a["sso_start_url"])
	}
	l, err := w.awsDraft(a)
	if err != nil {
		return nil, err
	}
	region := w.draftCtx(a).SSORegion
	var out []byte
	err = provider.WithTokenFile(config.Dir(), token, func(input string) error {
		out, err = w.app.query(l)(argv(input, region))
		return err
	})
	if err != nil {
		return nil, err
	}
	found, err := parse(out)
	return toOptions(found), err
}

func (w *ctxWizard) accounts(a wizard.Answers) ([]wizard.Option, error) {
	return w.ssoList(a, provider.ListAccountsCommand, provider.ParseAccounts)
}

func (w *ctxWizard) roles(a wizard.Answers) ([]wizard.Option, error) {
	return w.ssoList(a, func(input, region string) []string {
		return provider.ListAccountRolesCommand(input, region, a["account_id"])
	}, provider.ParseRoles)
}
