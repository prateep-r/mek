// Package provider turns a mek context into the environment and commands
// that the official cloud CLIs (aws, gcloud, ...) understand.
//
// Adding a new cloud = implementing Provider and registering it in For().
package provider

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/prateep-r/mek/internal/config"
)

// Env describes how to change the process environment for a context.
type Env struct {
	Set   map[string]string
	Unset []string // removed so they can't override the context (e.g. static AWS keys)
}

// Apply returns base with Unset removed and Set applied.
func (e Env) Apply(base []string) []string {
	drop := map[string]bool{}
	for _, k := range e.Unset {
		drop[k] = true
	}
	for k := range e.Set {
		drop[k] = true
	}
	out := make([]string, 0, len(base)+len(e.Set))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			out = append(out, kv)
		}
	}
	keys := make([]string, 0, len(e.Set))
	for k := range e.Set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+e.Set[k])
	}
	return out
}

// Shell renders the env as POSIX shell statements for `eval "$(mek env)"`.
func (e Env) Shell() string {
	var b strings.Builder
	for _, k := range e.Unset {
		fmt.Fprintf(&b, "unset %s\n", k)
	}
	keys := make([]string, 0, len(e.Set))
	for k := range e.Set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(e.Set[k]))
	}
	return b.String()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Provider is implemented once per cloud.
type Provider interface {
	// CLI is the official command-line tool this provider wraps ("aws", "gcloud").
	CLI() string
	// Prepare writes any files the CLI needs and returns the environment for the context.
	Prepare() (Env, error)
	// LoginCommands returns the CLI invocations that log the user in.
	LoginCommands(adc bool) [][]string
	// WhoAmICommand prints the active identity, used after login.
	WhoAmICommand() []string
	// Describe is a one-line human summary of the target.
	Describe() string
}

// For returns the provider for a context.
func For(cfg *config.Config, ctx *config.Context) (Provider, error) {
	switch ctx.Provider {
	case config.ProviderAWS:
		return &AWS{cfg: cfg, ctx: ctx, dir: config.Dir()}, nil
	case config.ProviderGCP:
		return &GCP{ctx: ctx, dir: config.Dir()}, nil
	}
	return nil, fmt.Errorf("unsupported provider %q", ctx.Provider)
}

func mkdirFor(path string) error {
	return os.MkdirAll(path, 0o700)
}
