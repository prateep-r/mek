package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// shellInit is a shell function named mek: `mek use -s <context>` (or
// --shell) runs the exports mek prints, so this shell switches; anything else
// goes to the mek program unchanged.
const shellInit = `# mek shell integration — https://github.com/prateep-r/mek
mek() {
  if [ "${1-}" = use ]; then
    local __mek_arg __mek_out
    for __mek_arg in "$@"; do
      case "$__mek_arg" in
        -s|--shell|--shell=true)
          __mek_out="$(command mek "$@")" || return
          eval "$__mek_out"
          return ;;
      esac
    done
  fi
  command mek "$@"
}
`

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell-init zsh|bash",
		Short: "Set up mek use -s to switch this shell: eval \"$(mek shell-init zsh)\"",
		Long: `Print a shell function that makes "mek use -s <context>" switch the
current shell by itself, without eval. Add one line to your shell's startup file:

  eval "$(mek shell-init zsh)"     # ~/.zshrc
  eval "$(mek shell-init bash)"    # ~/.bashrc

Every other mek command runs unchanged.`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"zsh", "bash"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "zsh" && args[0] != "bash" {
				return fmt.Errorf("shell-init %s: want zsh or bash", args[0])
			}
			fmt.Fprint(cmd.OutOrStdout(), shellInit)
			return nil
		},
	}
}

// newCompletionCmd prints the completion script for zsh or bash (contexts,
// clusters, targets and tunnels complete too).
func newCompletionCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "completion zsh|bash",
		Short: "Print the shell completion script for zsh or bash",
		Long: `Print the shell completion script; contexts, clusters, targets and tunnels
complete too.

  mek completion zsh > "${fpath[1]}/_mek"
  mek completion bash > ~/.local/share/bash-completion/completions/mek`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"zsh", "bash"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "bash":
				return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
			}
			return fmt.Errorf("completion %s: want zsh or bash", args[0])
		},
	}
}
