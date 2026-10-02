// Package cli wires mek's commands together.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
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
		Short: "Log in, switch and run commands across AWS, GCP, Azure and Huawei Cloud",
		Long: `mek (เมฆ, "cloud") manages cloud contexts — an AWS account+role, a GCP
project, an Azure subscription or a Huawei Cloud profile — and runs the
official CLIs (aws, gcloud, az, hcloud) with the right credentials, plus a
safety guard and audit log for protected contexts.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&a.opts.context, "context", "c", "", "context for this command (overrides the current context and $MEK_CONTEXT)")
	pf.BoolVarP(&a.opts.yes, "yes", "y", false, "answer yes to write confirmations on protected contexts")
	pf.StringVar(&a.opts.confirm, "confirm", "", "confirm destructive commands non-interactively by passing the context name")
	_ = root.RegisterFlagCompletionFunc("context", completeContexts)

	root.AddCommand(
		newInitCmd(),
		newUseCmd(),
		a.newCtxCmd(),
		a.newLoginCmd(),
	)
	for _, c := range provider.Clouds() {
		root.AddCommand(a.newPassthroughCmd(c))
	}
	root.AddCommand(
		a.newExecCmd(),
		a.newEnvCmd(),
		newDoctorCmd(),
		newVersionCmd(),
		newSelfUpdateCmd(),
	)
	return root
}

// loaded bundles what most commands need. env is only set by load.
type loaded struct {
	ctx  *config.Context
	prov provider.Provider
	env  provider.Env
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
	return &loaded{ctx: ctx, prov: provider.For(cfg, ctx)}, nil
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
	return l, nil
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

// banner warns on stderr when acting on a protected context.
func banner(l *loaded) {
	if !l.ctx.Protected && !l.ctx.ReadOnly {
		return
	}
	fmt.Fprintf(os.Stderr, "%s %s %s %s\n", ui.Red("●"), ui.Bold(l.ctx.Name), tags(ui.Err(), l.ctx), ui.Dim(l.prov.Describe()))
}
