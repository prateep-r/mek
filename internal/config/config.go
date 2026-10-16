// Package config loads mek's user configuration and current-context state.
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/prateep-r/mek/internal/fsutil"
)

const (
	ProviderAWS    = "aws"
	ProviderGCP    = "gcp"
	ProviderAzure  = "azure"
	ProviderHuawei = "huawei"
)

// Context is one cloud target: an AWS account+role, a GCP project, an
// Azure subscription or a Huawei Cloud KooCLI profile.
type Context struct {
	Name     string `yaml:"-"`
	Provider string `yaml:"provider"`

	// Where it was read from (set by Load, never in YAML).
	Source    string `yaml:"-"` // the file
	InProject bool   `yaml:"-"` // from a project's mek.yaml / mek.local.yaml
	Shadowed  string `yaml:"-"` // the file of a same-named context it replaces

	// Safety
	Protected bool `yaml:"protected,omitempty"` // confirm write/destructive operations
	ReadOnly  bool `yaml:"readonly,omitempty"`  // block write/destructive operations

	Region string `yaml:"region,omitempty"`

	// AWS — either IAM Identity Center (SSO) fields, or an existing profile.
	SSOStartURL string `yaml:"sso_start_url,omitempty"`
	SSORegion   string `yaml:"sso_region,omitempty"`
	AccountID   string `yaml:"account_id,omitempty"`
	Role        string `yaml:"role,omitempty"`
	AWSProfile  string `yaml:"aws_profile,omitempty"` // use a profile from ~/.aws/config as-is

	// GCP
	Project string `yaml:"project,omitempty"`
	Account string `yaml:"account,omitempty"` // optional, e.g. you@example.com
	// UseMetadata lets gcloud use the GCE metadata server's service account
	// (on a GCP VM) when the context has no login of its own.
	UseMetadata bool `yaml:"use_metadata,omitempty"`

	// Azure
	TenantID       string `yaml:"tenant_id,omitempty"` // Microsoft Entra tenant ID or domain
	SubscriptionID string `yaml:"subscription_id,omitempty"`
	// Bastion is the Azure Bastion that shells and tunnels go through.
	Bastion *Bastion `yaml:"bastion,omitempty"`

	// Huawei Cloud — a KooCLI profile you created (hcloud configure set).
	HcloudProfile string `yaml:"hcloud_profile,omitempty"`

	// Kubernetes clusters reachable from this context, by alias (mek kube).
	Clusters map[string]*Cluster `yaml:"clusters,omitempty"`
	// Hosts for `mek shell` (and as tunnel hops), by alias.
	Targets map[string]*Target `yaml:"targets,omitempty"`
	// Port forwards for `mek tunnel`, by alias.
	Tunnels map[string]*Tunnel `yaml:"tunnels,omitempty"`
}

type Config struct {
	Contexts map[string]*Context `yaml:"contexts"`
	// Settings are how mek behaves for you; only ~/.mek/config.yaml has them.
	Settings *Settings `yaml:"settings,omitempty"`

	Files   []string `yaml:"-"` // the files read, top layer first
	Project string   `yaml:"-"` // the project directory ("" outside a project)
}

// Settings are mek's own options. A field left out is unset (nil), so a
// flag or an environment variable can tell "off" from "not said".
type Settings struct {
	ShowCmd *bool `yaml:"show_cmd,omitempty"` // print the native command before running it
}

// ReadSettings reads the settings: of a config file's bytes, whatever the
// rest of it holds (a context with a typo doesn't hide them). Missing: nil.
func ReadSettings(b []byte) (*Settings, error) {
	var c struct {
		Settings *Settings `yaml:"settings"`
	}
	err := yaml.Unmarshal(b, &c)
	return c.Settings, err
}

type state struct {
	Current string `yaml:"current"`
}

// Dir is where mek keeps everything: $MEK_HOME, else ~/.mek.
func Dir() string {
	if d := os.Getenv("MEK_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mek")
}

func Path() string      { return filepath.Join(Dir(), "config.yaml") }
func statePath() string { return filepath.Join(Dir(), "state.yaml") }

var ErrNoConfig = errors.New("no config found — run `mek init` first")

// Load reads every layer (Sources: the project's mek.local.yaml and
// mek.yaml over ~/.mek/config.yaml), merges them and validates the result —
// a GoF Facade over the files, the layering and the guard rule.
func Load() (*Config, error) {
	src, project := Sources()
	c, err := src.Read()
	if err != nil {
		return nil, err
	}
	if len(c.Files) == 0 {
		return nil, ErrNoConfig
	}
	c.Project = project
	return c, c.validate()
}

// Parse decodes and validates the bytes of one config file (the global one).
func Parse(b []byte) (*Config, error) {
	c, err := decode(b, Path(), false)
	if err != nil {
		return nil, err
	}
	return c, c.validate()
}

// decode reads one file's contexts. Unknown keys are errors: a typo such as
// `protcted: true` must not leave a context silently unprotected.
func decode(b []byte, source string, inProject bool) (*Config, error) {
	var c Config
	if err := yaml.UnmarshalWithOptions(b, &c, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Show(source), err)
	}
	if c.Contexts == nil {
		c.Contexts = map[string]*Context{}
	}
	if c.Settings != nil && inProject { // a repository doesn't change how mek behaves for you
		return nil, fmt.Errorf("%s: settings: belong in %s, not in a project's file", Show(source), Show(Path()))
	}
	for name, ctx := range c.Contexts {
		if ctx == nil {
			return nil, fmt.Errorf("%s: context %q is empty", Show(source), name)
		}
		ctx.Name, ctx.Source, ctx.InProject = name, source, inProject
	}
	return &c, nil
}

// validate applies the rules every context shares; each cloud's own rules
// live with it (provider.Validate).
func (c *Config) validate() error {
	for _, name := range c.Names() {
		ctx := c.Contexts[name]
		if err := ctx.Validate(); err != nil {
			if ctx.InProject {
				return fmt.Errorf("context %q (%s): %w", name, Show(ctx.Source), err)
			}
			return fmt.Errorf("context %q: %w", name, err)
		}
	}
	return nil
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Validate checks the rules every context shares, whatever its cloud.
func (c *Context) Validate() error {
	// The name becomes a file path (gcloud config dir) and an AWS profile name.
	if !validName.MatchString(c.Name) {
		return errors.New("name may only contain letters, digits, '.', '_' and '-'")
	}
	// Values are written into a generated AWS config file, one per line.
	for _, f := range []struct{ key, val string }{
		{"region", c.Region}, {"sso_start_url", c.SSOStartURL}, {"sso_region", c.SSORegion},
		{"account_id", c.AccountID}, {"role", c.Role}, {"aws_profile", c.AWSProfile},
		{"project", c.Project}, {"account", c.Account},
		{"tenant_id", c.TenantID}, {"subscription_id", c.SubscriptionID}, {"hcloud_profile", c.HcloudProfile},
	} {
		if strings.ContainsAny(f.val, "\r\n") {
			return fmt.Errorf("%s must not contain line breaks", f.key)
		}
	}
	return c.validateAccess()
}

// Names returns context names sorted.
func (c *Config) Names() []string { return slices.Sorted(maps.Keys(c.Contexts)) }

// Get returns a context by name.
func (c *Config) Get(name string) (*Context, error) {
	ctx, ok := c.Contexts[name]
	if !ok {
		return nil, fmt.Errorf("unknown context %q (have: %s)", name, strings.Join(c.Names(), ", "))
	}
	return ctx, nil
}

// Resolve picks the context: explicit flag > $MEK_CONTEXT > saved current.
func (c *Config) Resolve(flag string) (*Context, error) {
	name := flag
	if name == "" {
		name = os.Getenv("MEK_CONTEXT")
	}
	if name == "" {
		name = Current()
	}
	if name == "" {
		return nil, errors.New("no context selected — run `mek use <context>` or pass --context")
	}
	ctx, err := c.Get(name)
	if err != nil && flag == "" { // chosen earlier, maybe in a project: say where to look
		return nil, fmt.Errorf("context %q isn't defined here — it may be a project's (%s); mek use <context>, or -c <context>", name, TeamFile)
	}
	return ctx, err
}

// Current returns the saved current context name ("" if none).
func Current() string {
	b, err := os.ReadFile(statePath())
	if err != nil {
		return ""
	}
	var s state
	if yaml.Unmarshal(b, &s) != nil {
		return ""
	}
	return s.Current
}

// SetCurrent persists the current context name.
// Atomic, so a shell prompt running `mek ctx --short` never reads a half-written file.
func SetCurrent(name string) error {
	b, _ := yaml.Marshal(state{Current: name}) // a struct of one string always marshals
	return fsutil.WriteFileAtomic(statePath(), b, 0o600)
}
