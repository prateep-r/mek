package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/guard"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/ui"
)

// runPlain runs a helper command (login, whoami) without guard or audit.
func (a *app) runPlain(argv, env []string) error { return a.plainTo(argv, env, nil) }

// plain runs an invocation outside the guard and the audit log.
func (a *app) plain(inv *runner.Invocation) error { return inv.Err(a.showCmd(a.exec).Run(inv)) }

// plainTo is runPlain with stdout sent to w (nil: the terminal).
func (a *app) plainTo(argv, env []string, w io.Writer) error {
	return a.plain(&runner.Invocation{Argv: argv, Env: env, Stdout: w})
}

// newPassthroughCmd forwards everything after `mek <cli>` to the real CLI.
// mek's own flags (-c/--context, -y/--yes, --confirm) come before <cli>;
// cobra parses those (the root traverses to its children), and every
// flag after <cli> is the CLI's, even one named like mek's (--project).
func (a *app) newPassthroughCmd(c provider.Cloud) *cobra.Command {
	return &cobra.Command{
		Use:                c.CLI + " [args...]",
		Short:              "Run any " + c.Title + " command in the current context",
		DisableFlagParsing: true, // every flag belongs to the wrapped CLI
		RunE: func(_ *cobra.Command, args []string) error {
			l, err := a.load("")
			if err != nil {
				return err
			}
			if l.ctx.Provider != c.Name {
				return fmt.Errorf("context %s is %s — `mek %s` needs a context with provider %s (try: mek -c <name> %s ...)",
					l.ctx.Name, l.ctx.Provider, c.CLI, c.Name, c.CLI)
			}
			class := c.Classify(args)
			if rw, ok := l.prov.(provider.ArgsRewriter); ok {
				args = rw.RewriteArgs(args)
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
// audited just like one that ran. Sessions also get a start entry once the
// guard let them through.
func (a *app) pipeline() runner.Runner { return a.pipelineTo(a.exec) }

// pipelineTo is the pipeline ending in terminal instead of exec (a Strategy
// for where the command finally runs: here, or under a tunnel supervisor).
func (a *app) pipelineTo(terminal runner.Runner) runner.Runner {
	return runner.Chain(terminal, audit.Recorder(warnAudit), a.guard, audit.SessionStart(warnAudit), a.showCmd)
}

// guarded runs argv in the context through the pipeline.
func (a *app) guarded(l *loaded, argv []string, class guard.Class) error {
	return a.run(l, &runner.Invocation{Argv: argv, Env: l.env.Apply(os.Environ()), Class: class})
}

// run sends an invocation in the context through the pipeline. Shells and
// tunnels — `mek shell` or e.g. `mek kubectl exec` — are sessions.
func (a *app) run(l *loaded, inv *runner.Invocation) error { return a.runTo(l, inv, a.exec) }

// runTo is run with the pipeline ending in terminal.
func (a *app) runTo(l *loaded, inv *runner.Invocation, terminal runner.Runner) error {
	banner(l)
	inv.Context = l.ctx
	if inv.Class == guard.Shell || inv.Class == guard.Tunnel {
		inv.Session = newSessionID()
	}
	return inv.Err(a.pipelineTo(terminal).Run(inv))
}

// newSessionID is a random id that ties a session's audit entries together.
func newSessionID() string {
	b := make([]byte, 8)
	rand.Read(b) // never fails (crypto/rand panics instead)
	return hex.EncodeToString(b)
}

// query is the provider.Query for a context: lookups (describe calls) run
// through the same pipeline as a read, with their output captured.
func (a *app) query(l *loaded) provider.Query {
	return func(argv []string) ([]byte, error) {
		var out, stderr bytes.Buffer
		inv := &runner.Invocation{Context: l.ctx, Argv: argv, Env: l.env.Apply(os.Environ()), Class: guard.Read, Stdout: &out}
		if l.quiet || l.draft { // a form is on the screen: the CLI may neither print nor ask
			inv.Stdin, inv.Stderr, inv.Quiet = bytes.NewReader(nil), &stderr, true
			inv.Env = append(inv.Env, "CLOUDSDK_CORE_DISABLE_PROMPTS=1")
		}
		if err := inv.Err(a.pipeline().Run(inv)); err != nil {
			what := strings.Join(argv[:min(3, len(argv))], " ")
			var exit *runner.ExitError
			if !errors.As(err, &exit) { // it never ran (not installed…): logging in won't help
				return nil, fmt.Errorf("%s: %w", what, err)
			}
			if l.draft {
				return nil, fmt.Errorf("%s: %v%s", what, err, cliReason(stderr.String()))
			}
			// Not %w: an *ExitError would make main exit quietly, without the hint.
			return nil, fmt.Errorf("%s: %v%s (logged in? try: mek -c %s login)", what, err, cliReason(stderr.String()), l.ctx.Name)
		}
		return out.Bytes(), nil
	}
}

var (
	errorLine   = regexp.MustCompile(`(?i)\berror\b`)
	gcloudError = regexp.MustCompile(`^ERROR:\s*(\([^)]*\)\s*)?`)
)

// cliReason is why a CLI failed, from its stderr, as ": reason" ("" when
// silent): its first error line — gcloud explains below it how to fix it,
// which isn't the reason — else its last line.
func cliReason(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	reason := strings.TrimSpace(lines[len(lines)-1])
	for _, l := range lines {
		if errorLine.MatchString(l) {
			reason = gcloudError.ReplaceAllString(strings.TrimSpace(l), "")
			break
		}
	}
	if reason == "" {
		return ""
	}
	return ": " + reason
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

