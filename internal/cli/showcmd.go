package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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
		// Quiet: a wizard's lookup, or output a program reads (mek kube token).
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
		words[i] = provider.ShellWord(w)
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

// showCmdState says whether show-cmd is on, and what decided it: the last
// of --show-cmd / --no-show-cmd, then $MEK_SHOW_CMD, then settings.show_cmd
// in ~/.mek/config.yaml, else off.
func (a *app) showCmdState() (on bool, why string) {
	if f := a.opts.showCmd; f.set {
		return f.on, f.name
	}
	if v := os.Getenv("MEK_SHOW_CMD"); v != "" {
		if on, ok := onOff(v); ok {
			return on, "MEK_SHOW_CMD=" + v + " in this shell"
		}
	}
	if s, _ := a.savedShowCmd(); s != nil {
		return *s, "settings.show_cmd in " + config.Show(config.Path())
	}
	return false, "the default"
}

func (a *app) showCmdOn() bool {
	on, _ := a.showCmdState()
	return on
}

// savedShowCmd is settings.show_cmd from ~/.mek/config.yaml (nil: not set),
// read once — and only once a command runs — even if the rest of the file
// has a problem. A config loaded already has it: the file isn't parsed again.
func (a *app) savedShowCmd() (*bool, error) {
	a.settings.Do(func() {
		if a.cfg != nil {
			if a.cfg.Settings != nil {
				a.settingShowCmd = a.cfg.Settings.ShowCmd
			}
			return
		}
		b, err := os.ReadFile(config.Path())
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		var s *config.Settings
		if err == nil {
			s, err = config.ReadSettings(b)
		}
		if s != nil {
			a.settingShowCmd = s.ShowCmd
		}
		a.settingsErr = err
	})
	return a.settingShowCmd, a.settingsErr
}

// showCmdFlag is --show-cmd or --no-show-cmd: one setting, so of the two
// the one given last wins (pflag reads flags in order).
type showCmdFlag struct {
	set, on bool
	name    string // the flag that set it
}

type showCmdValue struct {
	f    *showCmdFlag
	name string
	on   bool // what the flag means, given without a value
}

func (v showCmdValue) String() string { return "false" }
func (v showCmdValue) Type() string   { return "bool" }

func (v showCmdValue) Set(s string) error {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	*v.f = showCmdFlag{set: true, on: b == v.on, name: v.name}
	if !b {
		v.f.name += "=false"
	}
	return nil
}

func showCmdFlags(pf *pflag.FlagSet, f *showCmdFlag) {
	for _, d := range []struct {
		name, usage string
		on          bool
	}{
		{"show-cmd", "print each command mek runs as you would type it yourself (see mek show-cmd)", true},
		{"no-show-cmd", "don't print it, whatever MEK_SHOW_CMD or settings.show_cmd say", false},
	} {
		pf.VarPF(showCmdValue{f, "--" + d.name, d.on}, d.name, "", d.usage).NoOptDefVal = "true"
	}
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
      export AWS_DEFAULT_REGION='ap-southeast-1'
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
				switch s, err := a.savedShowCmd(); {
				case err != nil:
					fmt.Fprintf(cmd.OutOrStdout(), "  (can't read settings.show_cmd from %s: %v)\n", config.Show(config.Path()), err)
				case s != nil && *s != on:
					fmt.Fprintf(cmd.OutOrStdout(), "  (%s says %s)\n", config.Show(config.Path()), word(*s))
				}
				return nil
			}
			on, ok := onOff(args[0])
			if !ok {
				return fmt.Errorf("show-cmd %s: want on or off", args[0])
			}
			if err := saveShowCmd(on); err != nil {
				return err
			}
			path := config.Path()
			a.settings, a.settingsErr = sync.Once{}, nil
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

// saveShowCmd sets settings.show_cmd in ~/.mek/config.yaml. The config isn't
// validated as a whole: a problem elsewhere (a context, a project's file) is
// no reason to refuse a setting of your own; the file must parse (SetSetting).
func saveShowCmd(on bool) error {
	path := config.Path()
	e, err := config.Open(path, config.Template(path), 0o600)
	if err == nil {
		err = e.SetSetting("show_cmd", on)
	}
	if err != nil {
		return err
	}
	return e.Save()
}
