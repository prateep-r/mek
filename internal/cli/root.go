// Package cli wires mek's commands together.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/kube"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/ui"
)

// globalOpts are flags accepted before any subcommand.
type globalOpts struct {
	context string // --context / -c
	yes     bool   // --yes / -y : accept y/N confirmations
	confirm string // --confirm <ctx> : accept typed confirmations non-interactively
}

// app holds the state one command tree shares, instead of package globals, so
// every NewRoot (each test, each run) starts clean.
type app struct {
	opts globalOpts
	exec runner.Runner // runs processes; tests swap in a fake
}

// NewRoot builds the command tree.
func NewRoot() *cobra.Command { return newRoot(&app{exec: runner.Exec}) }

func newRoot(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:   "mek",
		Short: "Log in, switch and run commands across AWS and GCP",
		Long: `mek (เมฆ, "cloud") manages cloud contexts — an AWS account+role or a GCP
project — and runs the official CLIs (aws, gcloud) with the right
credentials, plus a safety guard and audit log for protected contexts.

Contexts come from ~/.mek/config.yaml and, inside a project, its mek.yaml
(the team's) and mek.local.yaml (yours): mek help config.
Add one by answering questions: mek ctx add (mek help wizard).
What protected and readonly do: mek help guard.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&a.opts.context, "context", "c", "", "context for this command (overrides the current context and $MEK_CONTEXT)")
	pf.BoolVarP(&a.opts.yes, "yes", "y", false, "answer yes to write confirmations on protected contexts")
	pf.StringVar(&a.opts.confirm, "confirm", "", "confirm destructive commands non-interactively by passing the context name")
	pf.StringVar(&config.ProjectFlag, "project", "", "read the project config (mek.yaml, mek.local.yaml) from this directory instead of looking up from here")
	pf.BoolVar(&config.NoProject, "no-local", false, "ignore project config files (mek.yaml, mek.local.yaml); also MEK_NO_LOCAL=1")
	_ = root.RegisterFlagCompletionFunc("context", completeContexts)

	// Groups for `mek help`; cobra lists each group's commands alphabetically.
	root.AddGroup(
		&cobra.Group{ID: "contexts", Title: "Contexts:"},
		&cobra.Group{ID: "run", Title: "Run commands:"},
		&cobra.Group{ID: "access", Title: "Access (shells, tunnels, Kubernetes):"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add("contexts", newInitCmd(), newUseCmd(), a.newCtxCmd(), a.newLoginCmd(), a.newUICmd())
	for _, c := range provider.Clouds() {
		add("run", a.newPassthroughCmd(c))
	}
	add("run", a.newKubectlCmd(), a.newExecCmd(), a.newEnvCmd())
	add("access", a.newShellCmd(), a.newTunnelCmd(), a.newKubeCmd())
	add("setup", newShellInitCmd(), newCompletionCmd(root), newDoctorCmd(), newVersionCmd(), newSelfUpdateCmd())
	root.AddCommand(
		helpTopic("config", "Where contexts come from: ~/.mek/config.yaml, a project's mek.yaml and mek.local.yaml", configHelp),
		helpTopic("guard", "What protected and readonly do, command by command", guardHelp()),
		helpTopic("wizard", "mek ctx add / edit: the keys, what it looks up in the cloud, how it saves", wizardHelp()),
	)
	root.SetHelpCommandGroupID("setup")
	root.CompletionOptions.DisableDefaultCmd = true // ours: zsh and bash
	return root
}

// loaded bundles what most commands need. env is only set by load.
type loaded struct {
	cfg  *config.Config
	ctx  *config.Context
	prov provider.Provider
	env  provider.Env
	// quiet: lookups for a wizard, whose form is on the screen — the CLI's
	// stderr is kept, and its last line goes in the error.
	quiet bool
	// draft: a context the wizard is making, in no file yet (quiet, and no
	// "mek login" hint: there is nothing to log in to yet).
	draft bool
}

// resolve loads the config and picks the context (explicit name > --context >
// $MEK_CONTEXT > saved) without touching disk, for commands that only display.
func (a *app) resolve(name string) (*loaded, error) {
	cfg, err := provider.Load()
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = a.opts.context
	}
	ctx, err := cfg.Resolve(name)
	if err != nil {
		return nil, err
	}
	return &loaded{cfg: cfg, ctx: ctx, prov: provider.For(cfg, ctx)}, nil
}

// load resolves the context and prepares its environment (writing provider
// files such as the generated AWS config), for commands that run a CLI.
func (a *app) load(name string) (*loaded, error) {
	l, err := a.resolve(name)
	if err != nil {
		return nil, err
	}
	if l.env, err = l.prov.Prepare(); err != nil {
		return nil, err
	}
	l.env.Set["MEK_CONTEXT"] = l.ctx.Name
	kubeEnv(l.ctx, &l.env)
	return l, nil
}

// kubeEnv points KUBECONFIG at the context's mek kubeconfig, or unsets one
// left over from another context's — never a KUBECONFIG the user chose.
func kubeEnv(ctx *config.Context, env *provider.Env) {
	if p, ok := kube.Env(ctx); ok {
		env.Set["KUBECONFIG"] = p
	} else if kube.Owned(os.Getenv("KUBECONFIG")) {
		env.Unset = append(env.Unset, "KUBECONFIG")
	}
}

func completeContexts(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	cfg, err := config.Load() // lenient: list names even if a context is invalid
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cfg.Names(), cobra.ShellCompDirectiveNoFileComp
}

// tags renders [protected] [readonly] markers.
func tags(p ui.Palette, c *config.Context) string {
	var t []string
	if c.Protected {
		t = append(t, p.Red("[protected]"))
	}
	if c.ReadOnly {
		t = append(t, p.Yellow("[readonly]"))
	}
	return strings.Join(t, " ")
}

// from is where a project's context comes from, " (./mek.yaml)"; "" for
// one from ~/.mek/config.yaml.
func from(p ui.Palette, c *config.Context) string {
	if !c.InProject {
		return ""
	}
	return " " + p.Dim("("+config.Show(c.Source)+")")
}

// banner says on stderr which context a command acts on when it matters:
// a guarded one, or one from a project's files (always, with the file) — and
// warns when the team's mek.yaml replaces a context of your own.
func banner(l *loaded) {
	c := l.ctx
	if c.InProject && filepath.Base(c.Source) == config.TeamFile && c.Shadowed == config.Path() {
		ui.Info("%s %s from %s replaces your %s in %s", ui.Yellow("⚠"), c.Name, config.Show(c.Source), c.Name, config.Show(c.Shadowed))
	}
	if !c.Protected && !c.ReadOnly && !c.InProject {
		return
	}
	dot := ui.Dim("●")
	if c.Protected || c.ReadOnly {
		dot = ui.Red("●")
	}
	parts := []string{dot, ui.Bold(c.Name) + from(ui.Err(), c), tags(ui.Err(), c), ui.Dim(l.prov.Describe())}
	fmt.Fprintln(os.Stderr, strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), " "))
}
