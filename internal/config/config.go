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

// Context is one cloud target: an AWS account+role or a GCP project.
type Context struct {
	Name     string `yaml:"-"`
	Provider string `yaml:"provider"`

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

	// Azure
	TenantID       string `yaml:"tenant_id,omitempty"` // Entra tenant ID or domain
	SubscriptionID string `yaml:"subscription_id,omitempty"`

	// Huawei Cloud — an existing KooCLI profile (created with `hcloud configure set`)
	HcloudProfile string `yaml:"hcloud_profile,omitempty"`

	// Kubernetes clusters reachable from this context, by alias (mek kube).
	Clusters map[string]*Cluster `yaml:"clusters,omitempty"`
}

type Config struct {
	Contexts map[string]*Context `yaml:"contexts"`
}

type state struct {
	Current string `yaml:"current"`
}

// Dir returns the mek config directory ($MEK_HOME, $XDG_CONFIG_HOME/mek or ~/.config/mek).
func Dir() string {
	if d := os.Getenv("MEK_HOME"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "mek")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mek")
}

func Path() string      { return filepath.Join(Dir(), "config.yaml") }
func statePath() string { return filepath.Join(Dir(), "state.yaml") }

var ErrNoConfig = errors.New("no config found — run `mek init` first")

// Load reads and validates the config file.
func Load() (*Config, error) {
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoConfig
	}
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes and validates config bytes.
func Parse(b []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(), err)
	}
	if c.Contexts == nil {
		c.Contexts = map[string]*Context{}
	}
	// Generic rules only; each cloud's rules live with it (provider.Validate).
	for _, name := range c.Names() {
		ctx := c.Contexts[name]
		if ctx == nil {
			return nil, fmt.Errorf("context %q is empty", name)
		}
		ctx.Name = name
		if err := ctx.Validate(); err != nil {
			return nil, fmt.Errorf("context %q: %w", name, err)
		}
	}
	return &c, nil
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
	return c.Get(name)
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
