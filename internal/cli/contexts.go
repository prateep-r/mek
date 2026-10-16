package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/ui"
)

func newInitCmd() *cobra.Command {
	var force, project, local bool
	cmd := &cobra.Command{
		Use:   "init [--project | --local]",
		Short: "Create ~/.mek/config.yaml (or a project's mek.yaml / mek.local.yaml) from a commented example",
		Long: `Create a config file from a commented example.

  mek init             ~/.mek/config.yaml: your contexts, on every project
  mek init --project   ./mek.yaml: this project's contexts, for the team — commit it
  mek init --local     ./mek.local.yaml: your own changes for this project; inside a
                       git repository it is added to .gitignore

The three files have the same shape. Inside a project, mek.local.yaml wins over
mek.yaml, which wins over ~/.mek/config.yaml, a context coming whole from one
file. See: mek help config

Or skip the example: mek ctx add asks for a context and creates the file
(inside a project, the file of your choice). See: mek help wizard`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if project && local {
				return errors.New("--project and --local: pick one")
			}
			if !project && !local {
				p, err := config.Init(force)
				if err != nil {
					return err
				}
				ui.Info("%s created %s", ui.Green("✓"), config.Show(p))
				ui.Info("edit it (or add contexts with: mek ctx add), then run: mek ctx ls && mek login <context>")
				return nil
			}
			cwd, _ := os.Getwd() // "": relative to wherever this is, if the directory is gone
			file, ignore, err := config.InitProject(cwd, local, force)
			if err != nil {
				return err
			}
			ui.Info("%s created %s", ui.Green("✓"), config.Show(file))
			if ignore != "" {
				ui.Info("%s added %s to %s", ui.Green("✓"), config.LocalFile, config.Show(ignore))
			}
			if project {
				ui.Info("add the project's contexts (mek ctx add helps), then commit it; teammates see them with: mek ctx ls")
			} else {
				ui.Info("copy a context from %s and change what is yours (mek ctx edit <context> does it); check with: mek ctx ls", config.TeamFile)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	cmd.Flags().BoolVar(&project, "project", false, "create ./mek.yaml, the project's config for the team")
	cmd.Flags().BoolVar(&local, "local", false, "create ./mek.local.yaml, your own config for the project (gitignored)")
	return cmd
}

func newUseCmd() *cobra.Command {
	var shell bool
	cmd := &cobra.Command{
		Use:   "use <context>",
		Short: "Switch the current context (every shell), or only this shell with -s",
		Long: `Switch the current context.

  mek use prod                     # saved: every shell without $MEK_CONTEXT
  mek use -s prod                  # this shell only, with mek shell-init set up
  eval "$(mek use -s prod)"        # this shell only, without it

The saved context is shared by all your terminals; -s (--shell) leaves it
alone, so terminals can work on different contexts at the same time. A
program can't change its shell's variables, so -s prints them for the shell
to run: put eval "$(mek shell-init zsh)" in ~/.zshrc (or ~/.bashrc) and
mek use -s does that by itself.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeContexts,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := provider.Load()
			if err != nil {
				return err
			}
			ctx, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			if shell {
				env := provider.Env{Set: map[string]string{"MEK_CONTEXT": ctx.Name}}
				kubeEnv(ctx, &env) // kubectl and K9s typed in this shell follow the context
				fmt.Fprint(cmd.OutOrStdout(), env.Shell())
				ui.Info("%s this shell now uses %s %s %s", ui.Green("✓"), ui.Bold(ctx.Name),
					ui.Dim(provider.For(cfg, ctx).Describe()), tags(ui.Err(), ctx))
				return nil
			}
			if err := config.SetCurrent(ctx.Name); err != nil {
				return err
			}
			ui.Info("%s switched to %s %s %s", ui.Green("✓"), ui.Bold(ctx.Name),
				ui.Dim(provider.For(cfg, ctx).Describe()), tags(ui.Err(), ctx))
			// $MEK_CONTEXT wins over the saved context (e.g. after eval "$(mek env x)").
			if env := os.Getenv("MEK_CONTEXT"); env != "" && env != ctx.Name {
				ui.Info("%s MEK_CONTEXT=%s is set in this shell and still takes precedence — run: unset MEK_CONTEXT",
					ui.Yellow("note:"), env)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&shell, "shell", "s", false, `switch only this shell (prints exports; mek shell-init runs them for you)`)
	return cmd
}

func (a *app) newCtxCmd() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:     "ctx",
		Aliases: []string{"context"},
		Short:   "Show the current context (use `mek ctx ls` to list all)",
		Long: `Show the current context; mek ctx ls lists them all (* marks the current one).

A context from a project's files is shown with its file:
  orders-dev (./mek.yaml)  aws 111122223333 / Developer / ap-southeast-1
Contexts from ~/.mek/config.yaml show none. See: mek help config

  mek ctx --short       just the name, for a shell prompt
  mek ctx ls            every context
  mek ctx add [name]    add one, a question at a time
  mek ctx edit [name]   change one (no name: pick it from a list)
  mek ctx rm <name>     remove one (--yes: don't ask)
  mek ctx target  add|edit|rm [name]   a context's hosts for mek shell
  mek ctx cluster add|edit|rm [name]   its Kubernetes clusters for mek kube
  mek ctx tunnel  add|edit|rm [name]   its port forwards for mek tunnel

mek context is the same command: mek context add, mek context ls, …`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := a.resolve("") // runs on every prompt: no disk writes
			if err != nil {
				return err
			}
			if short { // for shell prompts: PS1='$(mek ctx --short) $ '
				fmt.Fprintln(cmd.OutOrStdout(), l.ctx.Name)
				return nil
			}
			out := ui.Out()
			fmt.Fprintf(cmd.OutOrStdout(), "%s%s  %s %s\n", out.Bold(l.ctx.Name), from(out, l.ctx), l.prov.Describe(), tags(out, l.ctx))
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print only the context name")
	cmd.AddCommand(a.newCtxAddCmd(), a.newCtxEditCmd(), a.newCtxRmCmd(),
		a.newEntryCmd(targetKind{}, "Add, change or remove a context's targets (hosts for mek shell)", targetHelp),
		a.newEntryCmd(clusterKind{}, "Add, change or remove a context's clusters (for mek kube)", clusterHelp),
		a.newEntryCmd(tunnelKind{}, "Add, change or remove a context's tunnels (for mek tunnel)", tunnelHelp))

	cmd.AddCommand(&cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List all contexts",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := provider.Load()
			if err != nil {
				return err
			}
			cur, out := config.Current(), ui.Out()
			for _, n := range cfg.Names() {
				c := cfg.Contexts[n]
				mark := " "
				if n == cur {
					mark = out.Green("*")
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %-20s %-45s %s%s\n", mark, n, provider.For(cfg, c).Describe(), tags(out, c), from(out, c))
			}
			return nil
		},
	})
	return cmd
}

func (a *app) newLoginCmd() *cobra.Command {
	var adc bool
	cmd := &cobra.Command{
		Use:   "login [context] [-- <login flags>]",
		Short: "Log in to a context (AWS IAM Identity Center / gcloud / az)",
		Long: `Log in to a context using the official CLI's own login flow.

AWS: runs "aws sso login". Contexts that share the same sso_start_url share
one login, so you usually log in once per day for all accounts.
GCP: runs "gcloud auth login" in the context's isolated config dir;
add --adc to also create Application Default Credentials for SDKs/terraform.
Azure: runs "az login --tenant" in the context's isolated config dir, then
"az account set" to the context's subscription.

Flags after -- go to the CLI's own login command, e.g. without a browser or
in CI (the login still lands in the context's isolated config):

  mek login dev    -- --no-browser                   # aws sso login
  mek login ci-gcp -- --cred-file=key.json           # gcloud auth login
  mek login ci-az  -- --identity                     # az login, a managed identity`,
		Args: func(cmd *cobra.Command, args []string) error {
			if n := len(contextArgs(cmd, args)); n > 1 {
				return fmt.Errorf("accepts at most 1 context, got %d (put login flags after --)", n)
			}
			return nil
		},
		ValidArgsFunction: completeContexts,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if ctxArgs := contextArgs(cmd, args); len(ctxArgs) == 1 {
				name = ctxArgs[0]
			}
			l, err := a.load(name)
			if err != nil {
				return err
			}
			ui.Info("→ logging in to %s %s", ui.Bold(l.ctx.Name), ui.Dim(l.prov.Describe()))
			cmds := l.prov.LoginCommands(adc)
			// Extra flags belong to the actual login (the first command), not to
			// follow-ups like `gcloud auth application-default login`.
			cmds[0] = append(cmds[0], loginFlags(cmd, args)...)
			env := l.env.Apply(os.Environ())
			for _, argv := range cmds {
				if err := a.runPlain(argv, env); err != nil {
					return err
				}
			}
			ui.Info("%s logged in — identity:", ui.Green("✓"))
			return a.runPlain(l.prov.WhoAmICommand(), env)
		},
	}
	cmd.Flags().BoolVar(&adc, "adc", false, "GCP: also run gcloud auth application-default login")
	return cmd
}

// contextArgs are the arguments before "--"; loginFlags the ones after it.
func contextArgs(cmd *cobra.Command, args []string) []string {
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		return args[:dash]
	}
	return args
}

func loginFlags(cmd *cobra.Command, args []string) []string {
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		return args[dash:]
	}
	return nil
}
