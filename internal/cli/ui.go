package cli

import (
	"errors"
	"os"

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
		Short: "Browse and use contexts, tunnels, shells, clusters and the audit log in one screen",
		Long: `A terminal UI over mek: switch and log in to contexts, start, stop and read
tunnels, open shells, write kubeconfigs or start k9s, and search the audit log.

Everything it does runs mek itself, so protected contexts still ask, readonly
ones still block, and the audit log records it as if typed.

On the Contexts tab, a adds a context, e edits the chosen one and d removes
it; on the Tunnels tab, a adds a tunnel to the chosen context (the mek ctx
wizards; see: mek help wizard).

  mek ui              # starts on the current context
  mek -c prod ui      # starts on prod`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if !isTerminal() {
				return errors.New("mek ui needs a terminal")
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
			if f := a.opts.showCmd; f.set { // the mek commands the UI runs follow the flag
				os.Setenv("MEK_SHOW_CMD", map[bool]string{true: "1", false: "0"}[f.on])
			}
			return runUI(tui.Live{}, exe, a.opts.context)
		},
	}
}
