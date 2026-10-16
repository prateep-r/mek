package cli

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/ui"
)

// showCmd is a runner.Decorator: when show-cmd is on, it prints each command
// mek runs the way you would type it yourself — the variables mek sets or
// removes, then the command. It sits inside the guard, so only commands
// that really run are shown.
func (a *app) showCmd(next runner.Runner) runner.Runner {
	return runner.Func(func(inv *runner.Invocation) error {
		if !inv.Quiet && a.showCmdOn() {
			ui.Info("%s", ui.Dim(nativeCommand(inv.Argv, inv.Env, os.Environ())))
		}
		return next.Run(inv)
	})
}

// nativeCommand is argv with what env changes from base (this shell's
// environment) as exports and unsets before it. mek's own MEK_* variables
// are left out: nothing but mek reads them.
func nativeCommand(argv, env, base []string) string {
	if env == nil { // inherits this shell's environment as it is
		env = base
	}
	have, want := envMap(base), envMap(env)
	diff := provider.Env{Set: map[string]string{}}
	for k, v := range want {
		if hv, ok := have[k]; (!ok || hv != v) && !strings.HasPrefix(k, "MEK_") {
			diff.Set[k] = v
		}
	}
	for k := range have {
		if _, ok := want[k]; !ok && !strings.HasPrefix(k, "MEK_") {
			diff.Unset = append(diff.Unset, k)
		}
	}
	slices.Sort(diff.Unset)
	var b strings.Builder
	b.WriteString("↳ runs:\n")
	for _, l := range strings.Split(diff.Shell(), "\n") {
		if l != "" {
			b.WriteString("    " + l + "\n")
		}
	}
	words := make([]string, len(argv))
	for i, w := range argv {
		words[i] = shellWord(w)
	}
	b.WriteString("    " + strings.Join(words, " ") + "\n")
	b.WriteString("    # typed yourself, it skips mek's guard and audit log")
	return b.String()
}

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellWord quotes a word for a POSIX shell, only when it needs it.
func shellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// showCmdState says whether show-cmd is on, and what decided it:
// --show-cmd / --no-show-cmd, then $MEK_SHOW_CMD, then settings.show_cmd in
// ~/.mek/config.yaml, else off. Output meant for a program (mek kube token,
// which kubectl runs) never has it.
func (a *app) showCmdState() (on bool, why string) {
	switch {
	case a.machine:
		return false, "output for a program"
	case a.opts.noShowCmd:
		return false, "--no-show-cmd"
	case a.opts.showCmd:
		return true, "--show-cmd"
	}
	if v := os.Getenv("MEK_SHOW_CMD"); v != "" {
		if on, ok := onOff(v); ok {
			return on, "MEK_SHOW_CMD=" + v + " in this shell"
		}
	}
	if s := a.savedShowCmd(); s != nil {
		return *s, "settings.show_cmd in " + config.Show(config.Path())
	}
	return false, "the default"
}

func (a *app) showCmdOn() bool {
	on, _ := a.showCmdState()
	return on
}

// savedShowCmd is settings.show_cmd from ~/.mek/config.yaml (nil: not set),
// read once, and only when a command runs.
func (a *app) savedShowCmd() *bool {
	if !a.settingsRead {
		a.settingsRead = true
		if c, err := (config.File{Path: config.Path()}).Read(); err == nil && c.Settings != nil {
			a.settingShowCmd = c.Settings.ShowCmd
		}
	}
	return a.settingShowCmd
}

func onOff(v string) (on, ok bool) {
	switch strings.ToLower(v) {
	case "1", "on", "true", "yes":
		return true, true
	case "0", "off", "false", "no":
		return false, true
	}
	return false, false
}

func (a *app) newShowCmdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show-cmd [on|off]",
		Short: "Print each command mek runs as you would type it yourself (on, off, or say which)",
		Long: `Before each command mek runs, print it as you would type it yourself: the
variables mek sets or removes, then the command. Only what differs from this
shell is shown, as POSIX exports you can copy.

  $ mek -c prod aws s3 ls
  ↳ runs:
      export AWS_CONFIG_FILE='/Users/you/.mek/aws/config'
      export AWS_PROFILE='mek-prod'
      export AWS_REGION='ap-southeast-1'
      aws s3 ls
      # typed yourself, it skips mek's guard and audit log

It goes to stderr, so pipes get only the command's output. It shows the
commands mek runs on its way too (finding an instance by tag, a login).

On or off, the first that says wins:
  mek --show-cmd …  /  mek --no-show-cmd …   this command
  MEK_SHOW_CMD=1  /  MEK_SHOW_CMD=0          this shell, or a script
  mek show-cmd on  /  mek show-cmd off       every terminal: sets settings.show_cmd
                                             in ~/.mek/config.yaml (comments kept)
  otherwise                                  off

  mek show-cmd          # on or off, and what decided it
  mek show-cmd on
  mek --show-cmd -c dev kubectl get pods`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				on, why := a.showCmdState()
				fmt.Fprintf(cmd.OutOrStdout(), "show-cmd is %s — %s\n", word(on), why)
				if s := a.savedShowCmd(); s != nil && *s != on {
					fmt.Fprintf(cmd.OutOrStdout(), "  (%s says %s)\n", config.Show(config.Path()), word(*s))
				}
				return nil
			}
			on, ok := onOff(args[0])
			if !ok {
				return fmt.Errorf("mek show-cmd %s: want on or off", args[0])
			}
			path := config.Path()
			if err := editConfig(path, config.Template(path), func(e *config.Editor) error { return e.SetSetting("show_cmd", on) }); err != nil {
				return err
			}
			a.settingsRead = false
			ui.Info("%s show-cmd %s, in %s", ui.Green("✓"), word(on), config.Show(path))
			if now, why := a.showCmdState(); now != on {
				ui.Info("  but %s turns it %s here", why, word(now))
			}
			return nil
		},
	}
}

func word(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
