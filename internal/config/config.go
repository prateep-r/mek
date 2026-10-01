// Package config loads mek's user configuration and current-context state.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

const (
	ProviderAWS = "aws"
	ProviderGCP = "gcp"
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
	for name, ctx := range c.Contexts {
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

func (c *Context) Validate() error {
	if strings.ContainsAny(c.Name, " /\\:") {
		return errors.New("name must not contain spaces, slashes or colons")
	}
	switch c.Provider {
	case ProviderAWS:
		if c.AWSProfile != "" {
			return nil
		}
		var missing []string
		for k, v := range map[string]string{"sso_start_url": c.SSOStartURL, "account_id": c.AccountID, "role": c.Role} {
			if v == "" {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("aws context needs aws_profile, or %s", strings.Join(missing, ", "))
		}
	case ProviderGCP:
		if c.Project == "" {
			return errors.New("gcp context needs project")
		}
	case "":
		return errors.New("provider is required (aws or gcp)")
	default:
		return fmt.Errorf("unknown provider %q (supported: aws, gcp)", c.Provider)
	}
	return nil
}

// Names returns context names sorted.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Contexts))
	for n := range c.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

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
func SetCurrent(name string) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(state{Current: name})
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), b, 0o600)
}
