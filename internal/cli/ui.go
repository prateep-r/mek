package cli

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/tui"
)

// Test seams: the UI needs a real terminal, and runs this binary.
var (
	isTerminal = func() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }
	runUI      = tui.Run
	executable = os.Executable
)

func (a *app) newUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Browse and use contexts, tunnels, shells, clusters, cloud resources and the audit log in one screen",
		Long: `A terminal UI over mek: switch and log in to contexts, start, stop and read
tunnels, open shells, write kubeconfigs or start k9s, search the audit log, and
browse the chosen context's cloud resources: list them, read one's details,
and start, stop, reboot or delete one.

Everything it does runs mek itself, so protected contexts still ask, readonly
ones still block, and the audit log records it as if typed.

On the Contexts tab, a adds a context, e edits the chosen one and d removes
it; on the Tunnels tab, a adds a tunnel to the chosen context (the mek ctx
wizards; see: mek help wizard). On the Resources tab, a opens the actions
of the chosen resource; mek ui asks before each one, on any context.

  mek ui              # starts on the current context
  mek -c prod ui      # starts on prod`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if !isTerminal() {
				return errors.New("ui needs a terminal")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if a.opts.context != "" {
				if _, err := cfg.Get(a.opts.context); err != nil {
					return err
				}
			}
			exe, err := executable()
			if err != nil {
				return err
			}
			// The mek commands the UI runs follow its flags.
			if f := a.opts.showCmd; f.set {
				os.Setenv("MEK_SHOW_CMD", map[bool]string{true: "1", false: "0"}[f.on])
			}
			if config.NoProject {
				os.Setenv("MEK_NO_LOCAL", "1")
			}
			if config.ProjectFlag != "" {
				abs, _ := filepath.Abs(config.ProjectFlag) // checked: a directory
				os.Setenv("MEK_PROJECT", abs)
			}
			return runUI(tui.Live{}, exe, a.opts.context)
		},
	}
}
