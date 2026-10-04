// Package provider turns a mek context into the environment and commands
// that the official cloud CLIs (aws, gcloud, ...) understand.
//
// Adding a new cloud = one file that implements Provider (an Adapter from a
// mek context to that CLI) and defines its Cloud, listed in clouds (cloud.go).
package provider

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Env describes how to change the process environment for a context.
type Env struct {
	Set   map[string]string
	Unset []string // removed so they can't override the context (e.g. static AWS keys)
}

// Apply returns base with Unset removed and Set applied.
func (e Env) Apply(base []string) []string {
	drop := make(map[string]bool, len(e.Unset)+len(e.Set))
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
	for _, k := range slices.Sorted(maps.Keys(e.Set)) {
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
	for _, k := range slices.Sorted(maps.Keys(e.Set)) {
		fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(e.Set[k]))
	}
	return b.String()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// orDash shows an unset value as "-" in one-line summaries.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Provider is implemented once per cloud.
type Provider interface {
	// Prepare writes any files the CLI needs and returns the environment for the context.
	Prepare() (Env, error)
	// LoginCommands returns the CLI invocations that log the user in.
	LoginCommands(adc bool) [][]string
	// WhoAmICommand prints the active identity, used after login.
	WhoAmICommand() []string
	// Describe is a one-line human summary of the target.
	Describe() string
}
