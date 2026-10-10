package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// shellInit is a shell function named mek: `mek use -s <context>` (or
// --shell) runs the exports mek prints, so this shell switches; anything else
// goes to the mek program unchanged.
var shellInit = map[string]string{
	"posix": `# mek shell integration — https://github.com/prateep-r/mek
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
`,
	"fish": `# mek shell integration — https://github.com/prateep-r/mek
function mek --wraps mek --description 'mek; mek use -s switches this shell'
    if test (count $argv) -gt 0; and test $argv[1] = use; and begin; contains -- -s $argv; or contains -- --shell $argv; end
        set -l out (command mek $argv --syntax fish); or return
        printf '%s\n' $out | source
        return
    end
    command mek $argv
end
`,
}

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell-init zsh|bash|fish",
		Short: "Set up mek use -s to switch this shell: eval \"$(mek shell-init zsh)\"",
		Long: `Print a shell function that makes "mek use -s <context>" switch the
current shell by itself, without eval. Add one line to your shell's startup file:

  eval "$(mek shell-init zsh)"     # ~/.zshrc
  eval "$(mek shell-init bash)"    # ~/.bashrc
  mek shell-init fish | source     # ~/.config/fish/config.fish

Every other mek command runs unchanged.`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"zsh", "bash", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			syntax := map[string]string{"zsh": "posix", "bash": "posix", "fish": "fish"}[args[0]]
			if syntax == "" {
				return fmt.Errorf("mek shell-init %s: want zsh, bash or fish", args[0])
			}
			fmt.Fprint(cmd.OutOrStdout(), shellInit[syntax])
			return nil
		},
	}
}
