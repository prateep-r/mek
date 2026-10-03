package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/guard"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/ui"
)

// runPlain runs a helper command (login, whoami) without guard or audit.
func (a *app) runPlain(argv, env []string) error { return a.runTo(argv, env, nil) }

// runTo is runPlain with stdout sent to w (nil: the terminal).
func (a *app) runTo(argv, env []string, w io.Writer) error {
	inv := &runner.Invocation{Argv: argv, Env: env, Stdout: w}
	return inv.Err(a.exec.Run(inv))
}

// newPassthroughCmd forwards everything after `mek <cli>` to the real CLI.
// mek's own flags (-c/--context, -y/--yes, --confirm) must come before <cli>.
func (a *app) newPassthroughCmd(c provider.Cloud) *cobra.Command {
	return &cobra.Command{
		Use:                c.CLI + " [args...]",
		Short:              "Run any " + c.Title + " command in the current context",
		DisableFlagParsing: true, // every flag belongs to the wrapped CLI
		RunE: func(_ *cobra.Command, args []string) error {
			args, err := a.takeGlobalFlags(args)
			if err != nil {
				return err
			}
			l, err := a.load("")
			if err != nil {
				return err
			}
			if l.ctx.Provider != c.Name {
				return fmt.Errorf("context %s is %s — `mek %s` needs a context with provider %s (try: mek -c <name> %s ...)",
					l.ctx.Name, l.ctx.Provider, c.CLI, c.Name, c.CLI)
			}
			class := c.Classify(args)
			if r, ok := l.prov.(provider.ArgsRewriter); ok {
				args = r.RewriteArgs(args)
			}
			return a.guarded(l, append([]string{c.CLI}, args...), class)
		},
	}
}

func (a *app) newExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec -- <command> [args...]",
		Short: "Run any command (terraform, kubectl, scripts...) with the context's credentials",
		Long: `Run any command with the current context's environment, e.g.

  mek exec -- terraform plan
  mek -c baas-uat exec -- ./deploy.sh

mek cannot tell whether an arbitrary command reads or writes, so on a
protected context it always asks first, and on a readonly context it refuses.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			l, err := a.load("")
			if err != nil {
				return err
			}
			return a.guarded(l, args, guard.Unknown)
		},
	}
}

func (a *app) newEnvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "env [context]",
		Short: `Print shell exports for a context: eval "$(mek env)"`,
		Long: `Print environment variables for a context so any tool in your shell uses it:

  eval "$(mek env)"            # current context
  eval "$(mek env baas-uat)"

Note: commands run this way bypass mek's guard and audit log.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeContexts,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			l, err := a.load(name)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), l.env.Shell())
			return nil
		},
	}
}

// pipeline is audit → guard → exec, so a blocked or declined command is
// audited just like one that ran.
func (a *app) pipeline() runner.Runner {
	return runner.Chain(a.exec, audit.Recorder(warnAudit), a.guard)
}

// guarded runs argv in the context through the pipeline.
func (a *app) guarded(l *loaded, argv []string, class guard.Class) error {
	banner(l)
	inv := &runner.Invocation{Context: l.ctx, Argv: argv, Env: l.env.Apply(os.Environ()), Class: class}
	return inv.Err(a.pipeline().Run(inv))
}

// query is the provider.Query for a context: lookups (describe calls) run
// through the same pipeline as a read, with their output captured.
func (a *app) query(l *loaded) provider.Query {
	return func(argv []string) ([]byte, error) {
		var out bytes.Buffer
		inv := &runner.Invocation{Context: l.ctx, Argv: argv, Env: l.env.Apply(os.Environ()), Class: guard.Read, Stdout: &out}
		if err := inv.Err(a.pipeline().Run(inv)); err != nil {
			// Not %w: an *ExitError would make main exit quietly, without the hint.
			return nil, fmt.Errorf("%s: %v (logged in? try: mek -c %s login)", strings.Join(argv[:min(3, len(argv))], " "), err, l.ctx.Name)
		}
		return out.Bytes(), nil
	}
}

func warnAudit(err error) { ui.Info("%s audit log: %v", ui.Yellow("warning:"), err) }

// Prompts, swapped out in tests.
var (
	confirm      = ui.Confirm
	confirmTyped = ui.ConfirmTyped
)

// guard is the runner.Decorator applying the context's safety policy: it
// records the decision and stops the chain unless the command may run.
func (a *app) guard(next runner.Runner) runner.Runner {
	return runner.Func(func(inv *runner.Invocation) error {
		decision, err := a.decide(inv)
		inv.Decision = decision
		if err != nil {
			inv.ExitCode = -1 // never ran
			return err
		}
		return next.Run(inv)
	})
}

// decide returns the audit decision label and an error if the command must not run.
func (a *app) decide(inv *runner.Invocation) (string, error) {
	name, class := inv.Context.Name, inv.Class
	cmdline := strings.Join(audit.Mask(inv.Argv), " ")
	switch guard.Decide(inv.Context, class) {
	case guard.Allow:
		return "allowed", nil
	case guard.Block:
		return "blocked", fmt.Errorf("%s is readonly — blocked %s command: %s", name, class, cmdline)
	case guard.Confirm:
		if a.opts.yes || a.opts.confirm == name {
			return "confirmed", nil
		}
		ok, err := confirm(fmt.Sprintf("%s %s command on %s:\n  %s\nContinue?",
			ui.Yellow("⚠"), class, ui.Bold(name), cmdline))
		return confirmResult(ok, err, "--yes")
	default: // guard.ConfirmTyped
		if a.opts.confirm == name {
			return "confirmed", nil
		}
		ok, err := confirmTyped(fmt.Sprintf("%s DESTRUCTIVE command on %s:\n  %s",
			ui.Red("⚠"), ui.Bold(name), cmdline), name)
		return confirmResult(ok, err, "--confirm "+name)
	}
}

func confirmResult(ok bool, err error, flag string) (string, error) {
	if errors.Is(err, ui.ErrNoTTY) {
		return "blocked", fmt.Errorf("%w — pass %s to run non-interactively", err, flag)
	}
	if err != nil {
		return "declined", err
	}
	if !ok {
		return "declined", errors.New("aborted")
	}
	return "confirmed", nil
}

// takeGlobalFlags parses mek flags that appear before the wrapped CLI's
// arguments (cobra hands them to us raw because DisableFlagParsing is on).
func (a *app) takeGlobalFlags(args []string) ([]string, error) {
	for len(args) > 0 {
		name, val, hasVal := strings.Cut(args[0], "=")
		switch name {
		case "-c", "--context", "--confirm":
			if !hasVal {
				if len(args) < 2 {
					return nil, fmt.Errorf("%s needs a value", name)
				}
				val, args = args[1], args[1:]
			}
			if name == "--confirm" {
				a.opts.confirm = val
			} else {
				a.opts.context = val
			}
		case "-y", "--yes":
			a.opts.yes = true
		default:
			return args, nil
		}
		args = args[1:]
	}
	return args, nil
}
