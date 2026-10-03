package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/ui"
)

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create ~/.config/mek/config.yaml from a commented example",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := config.Init(force)
			if err != nil {
				return err
			}
			ui.Info("%s created %s", ui.Green("✓"), p)
			ui.Info("edit it, then run: mek ctx ls && mek login <context>")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	return cmd
}

func newUseCmd() *cobra.Command {
	var shell bool
	cmd := &cobra.Command{
		Use:   "use <context>",
		Short: "Switch the current context (every shell), or only this shell with --shell",
		Long: `Switch the current context.

  mek use prod                     # saved: every shell without $MEK_CONTEXT
  eval "$(mek use --shell prod)"   # this shell only (sets $MEK_CONTEXT)

The saved context is shared by all your terminals; --shell leaves it alone,
so terminals can work on different contexts at the same time.`,
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
				// Context names are [A-Za-z0-9._-]: safe to print for eval.
				fmt.Fprintf(cmd.OutOrStdout(), "export MEK_CONTEXT=%s\n", ctx.Name)
				env := provider.Env{Set: map[string]string{}}
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
	cmd.Flags().BoolVar(&shell, "shell", false, `switch only this shell: print an export for eval "$(mek use --shell <context>)"`)
	return cmd
}

func (a *app) newCtxCmd() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "ctx",
		Short: "Show the current context (use `mek ctx ls` to list all)",
		Args:  cobra.NoArgs,
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
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s %s\n", out.Bold(l.ctx.Name), l.prov.Describe(), tags(out, l.ctx))
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print only the context name")

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
				fmt.Fprintf(cmd.OutOrStdout(), "%s %-20s %-45s %s\n", mark, n, provider.For(cfg, c).Describe(), tags(out, c))
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
		Short: "Log in to a context (AWS IAM Identity Center / gcloud / az / hcloud SSO)",
		Long: `Log in to a context using the official CLI's own login flow.

AWS: runs "aws sso login". Contexts that share the same sso_start_url share
one login, so you usually log in once per day for all accounts.
GCP: runs "gcloud auth login" in the context's isolated config dir;
add --adc to also create Application Default Credentials for SDKs/terraform.
Azure: runs "az login --tenant" and "az account set --subscription" in the
context's isolated AZURE_CONFIG_DIR.
Huawei Cloud: runs "hcloud configure sso" for the context's KooCLI profile.
mek never edits KooCLI's profiles; create an SSO profile once with:

  hcloud configure set --cli-profile=<name> --cli-mode=SSO --cli-region=<region> \
    --cli-sso-start-url=<portal-url> --cli-sso-region=<region> \
    --cli-sso-account-name=<account> --cli-sso-permission-set-name=<permission-set>

Flags after -- go to the CLI's own login command, e.g. without a browser or
in CI (the login still lands in the context's isolated config):

  mek login dev    -- --no-browser                   # aws sso login
  mek login ci-gcp -- --cred-file=key.json           # gcloud auth login
  mek login ci-az  -- --service-principal -u "$APP_ID" -p "$SECRET"   # az login`,
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
			cmds, err := l.prov.LoginCommands(adc)
			if err != nil {
				return err
			}
			// Extra flags belong to the actual login (the first command), not to
			// follow-ups like `az account set`.
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
	cmd.Flags().BoolVar(&adc, "adc", false, "GCP: also run `gcloud auth application-default login`")
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
