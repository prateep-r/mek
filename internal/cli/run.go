package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/guard"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/ui"
)

func environ() []string { return os.Environ() }

// runPlain runs a helper command (login, whoami) without guard or audit.
func runPlain(argv, env []string) error {
	code, err := runner.Run(argv, env)
	if err != nil {
		return err
	}
	if code != 0 {
		return &runner.ExitError{Code: code}
	}
	return nil
}

// newPassthroughCmd forwards everything after `mek <cli>` to the real CLI.
// mek's own flags (-c/--context, -y/--yes, --confirm) must come before <cli>.
func newPassthroughCmd(cli, short string) *cobra.Command {
	return &cobra.Command{
		Use:                cli + " [args...]",
		Short:              short,
		DisableFlagParsing: true, // every flag belongs to the wrapped CLI
		RunE: func(_ *cobra.Command, args []string) error {
			args, err := takeGlobalFlags(args)
			if err != nil {
				return err
			}
			l, err := load("")
			if err != nil {
				return err
			}
			if l.prov.CLI() != cli {
				return fmt.Errorf("context %s is %s — `mek %s` needs a context with provider %s (try: mek -c <name> %s ...)",
					l.ctx.Name, l.ctx.Provider, cli, cliProvider(cli), cli)
			}
			argv := append([]string{cli}, args...)
			return guarded(l, argv, guard.Classify(cli, args))
		},
	}
}

func cliProvider(cli string) string {
	if cli == "gcloud" {
		return "gcp"
	}
	return cli
}

func newExecCmd() *cobra.Command {
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
			l, err := load("")
			if err != nil {
				return err
			}
			return guarded(l, args, guard.Unknown)
		},
	}
}

func newEnvCmd() *cobra.Command {
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
			l, err := load(name)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), l.env.Shell())
			return nil
		},
	}
}

// guarded applies the context's safety policy, runs argv and writes an audit entry.
func guarded(l *loaded, argv []string, class guard.Class) error {
	banner(l)
	entry := audit.Entry{Context: l.ctx.Name, Provider: l.ctx.Provider, Command: argv, Class: class.String()}

	decision, err := decide(l, argv, class)
	entry.Decision = decision
	if err != nil {
		entry.ExitCode = -1
		_ = audit.Write(entry)
		return err
	}

	start := time.Now()
	code, err := runner.Run(argv, l.env.Apply(environ()))
	entry.ExitCode = code
	entry.DurationMS = time.Since(start).Milliseconds()
	if werr := audit.Write(entry); werr != nil {
		ui.Info("%s audit log: %v", ui.Yellow("warning:"), werr)
	}
	if err != nil {
		return err
	}
	if code != 0 {
		return &runner.ExitError{Code: code}
	}
	return nil
}

// decide returns the audit decision label and an error if the command must not run.
func decide(l *loaded, argv []string, class guard.Class) (string, error) {
	cmdline := strings.Join(audit.Mask(argv), " ")
	switch guard.Decide(l.ctx, class) {
	case guard.Allow:
		return "allowed", nil
	case guard.Block:
		return "blocked", fmt.Errorf("%s is readonly — blocked %s command: %s", l.ctx.Name, class, cmdline)
	case guard.Confirm:
		if opts.yes || opts.confirm == l.ctx.Name {
			return "confirmed", nil
		}
		ok, err := ui.Confirm(fmt.Sprintf("%s %s command on %s:\n  %s\nContinue?",
			ui.Yellow("⚠"), class, ui.Bold(l.ctx.Name), cmdline))
		return confirmResult(ok, err, "--yes")
	case guard.ConfirmTyped:
		if opts.confirm == l.ctx.Name {
			return "confirmed", nil
		}
		ok, err := ui.ConfirmTyped(fmt.Sprintf("%s DESTRUCTIVE command on %s:\n  %s",
			ui.Red("⚠"), ui.Bold(l.ctx.Name), cmdline), l.ctx.Name)
		return confirmResult(ok, err, "--confirm "+l.ctx.Name)
	}
	return "blocked", errors.New("unknown guard decision")
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
func takeGlobalFlags(args []string) ([]string, error) {
	for len(args) > 0 {
		a := args[0]
		name, val, hasVal := strings.Cut(a, "=")
		switch name {
		case "-c", "--context", "--confirm":
			if !hasVal {
				if len(args) < 2 {
					return nil, fmt.Errorf("%s needs a value", name)
				}
				val, args = args[1], args[1:]
			}
			if name == "--confirm" {
				opts.confirm = val
			} else {
				opts.context = val
			}
		case "-y", "--yes":
			opts.yes = true
		default:
			return args, nil
		}
		args = args[1:]
	}
	return args, nil
}
