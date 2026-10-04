package cli

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/guard"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/ui"
)

func (a *app) newShellCmd() *cobra.Command {
	var o provider.TargetOptions
	cmd := &cobra.Command{
		Use:   "shell <target>",
		Short: "Open a shell on an instance (AWS SSM, GCP IAP + SSH)",
		Long: `Open an interactive shell on an instance through the cloud's own
session service — no public IP, bastion key or open port needed.

  mek -c prod shell bastion             # a name under the context's targets:
  mek -c prod shell i-0abc1234def567890 # aws: an instance id
  mek -c prod shell tag:Name=bastion    # aws: the one running instance with that tag
  mek -c gcp shell vm-1 [--zone Z]      # gcp: a VM name (the zone is looked up)

On a protected context mek asks first; on a readonly context shells are blocked.
GCP keeps the context's SSH key and known hosts under <MEK_HOME>/ssh/<context>,
not ~/.ssh.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.completeTargets,
		RunE: func(_ *cobra.Command, args []string) error {
			return a.shell(args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.Zone, "zone", "", "gcp: the VM's zone (default: looked up)")
	cmd.Flags().StringVar(&o.User, "user", "", "gcp: SSH user (default: gcloud's)")
	return cmd
}

func (a *app) shell(spec string, o provider.TargetOptions) error {
	l, err := a.load("")
	if err != nil {
		return err
	}
	s, err := capability[provider.Sessioner](l, "mek shell")
	if err != nil {
		return err
	}
	in, err := s.ResolveTarget(spec, o, a.query(l))
	if err != nil {
		return err
	}
	c, err := s.ShellCommand(in)
	if err != nil {
		return err
	}
	return a.session(l, c, guard.Shell, in.Label(), a.exec)
}

// session runs an access command (shell or tunnel) through the pipeline
// ending in terminal, once the plugins it needs are installed.
func (a *app) session(l *loaded, c provider.Command, class guard.Class, target string, terminal runner.Runner) error {
	for _, bin := range c.Requires {
		if _, err := lookPath(bin); err != nil {
			how := "see `mek doctor`"
			if p, ok := provider.FindPlugin(bin); ok {
				how = "install: " + hint(ui.Palette{}, tool{bin: p.Bin, brew: p.Tool.Brew, url: p.Tool.URL})
			}
			return fmt.Errorf("%s is needed for this (%s)", bin, how)
		}
	}
	env := provider.Env{Set: c.Env}.Apply(l.env.Apply(os.Environ()))
	return a.runTo(l, &runner.Invocation{Argv: c.Argv, Env: env, Class: class, Target: target}, terminal)
}

var lookPath = exec.LookPath // test seam

func (a *app) completeTargets(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	l, err := a.resolve("")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return slices.Sorted(maps.Keys(l.ctx.Targets)), cobra.ShellCompDirectiveNoFileComp
}
