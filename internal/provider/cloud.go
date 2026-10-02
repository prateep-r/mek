package provider

import (
	"fmt"
	"strings"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

// Cloud describes everything mek knows about one cloud. Each Cloud is an
// Abstract Factory for that cloud's family of objects: the Provider adapting a
// context to the official CLI (New), the Strategy that classifies its commands
// (Classify), and its config rules (Validate). Supporting a new cloud means
// adding one file that defines a Cloud and listing it in clouds below.
type Cloud struct {
	Name  string // provider name in config.yaml ("aws")
	CLI   string // official CLI wrapped as `mek <cli> ...` ("aws")
	Title string // human name of the CLI ("aws CLI")

	// New creates the Provider for one context (Factory Method).
	New func(cfg *config.Config, ctx *config.Context, dir string) Provider
	// Classify sorts the CLI's arguments into read / write / destructive (Strategy).
	Classify func(args []string) guard.Class
	// Validate checks one context's cloud-specific fields.
	Validate func(ctx *config.Context) error
	// ValidateAll checks rules that span contexts (optional).
	ValidateAll func(cfg *config.Config) error

	Tool Tool // how `mek doctor` checks for the CLI
}

// Tool tells `mek doctor` how to find a CLI and how to install it.
type Tool struct {
	VersionArgs []string
	Brew        string // macOS install command ("" = none)
	URL         string // install docs
}

// clouds is the registry, in the order mek lists them.
var clouds = []Cloud{awsCloud, gcpCloud, azureCloud, huaweiCloud}

// Clouds returns every supported cloud.
func Clouds() []Cloud { return clouds }

// Lookup finds a cloud by its provider name.
func Lookup(name string) (Cloud, bool) {
	for _, c := range clouds {
		if c.Name == name {
			return c, true
		}
	}
	return Cloud{}, false
}

// LookupCLI finds a cloud by the CLI it wraps.
func LookupCLI(cli string) (Cloud, bool) {
	for _, c := range clouds {
		if c.CLI == cli {
			return c, true
		}
	}
	return Cloud{}, false
}

func names() []string {
	n := make([]string, len(clouds))
	for i, c := range clouds {
		n[i] = c.Name
	}
	return n
}

// For returns the provider for a context.
func For(cfg *config.Config, ctx *config.Context) (Provider, error) {
	c, ok := Lookup(ctx.Provider)
	if !ok {
		return nil, fmt.Errorf("unsupported provider %q", ctx.Provider)
	}
	return c.New(cfg, ctx, config.Dir()), nil
}

// Validate checks every context against its cloud's rules, in name order so
// the same bad config always reports the same error.
func Validate(cfg *config.Config) error {
	for _, name := range cfg.Names() {
		ctx := cfg.Contexts[name]
		if ctx.Provider == "" {
			return fmt.Errorf("context %q: provider is required (%s)", name, joinOr(names()))
		}
		c, ok := Lookup(ctx.Provider)
		if !ok {
			return fmt.Errorf("context %q: unknown provider %q (supported: %s)", name, ctx.Provider, joinOr(names()))
		}
		if err := c.Validate(ctx); err != nil {
			return fmt.Errorf("context %q: %w", name, err)
		}
	}
	for _, c := range clouds {
		if c.ValidateAll != nil {
			if err := c.ValidateAll(cfg); err != nil {
				return err
			}
		}
	}
	return nil
}

// Load reads the config file and validates it against every cloud's rules.
func Load() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// joinOr renders [a b c] as "a, b or c".
func joinOr(s []string) string {
	if len(s) < 2 {
		return strings.Join(s, "")
	}
	return strings.Join(s[:len(s)-1], ", ") + " or " + s[len(s)-1]
}
