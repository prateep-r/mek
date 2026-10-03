package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/guard"
	"github.com/prateep-r/mek/internal/kube"
)

// newKubectlCmd forwards `mek kubectl ...` to kubectl with the context's
// kubeconfig, guarded and audited like the cloud CLIs.
func (a *app) newKubectlCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "kubectl [args...]",
		Short:              "Run kubectl on the current context's clusters (see `mek kube`)",
		DisableFlagParsing: true, // every flag belongs to kubectl
		RunE: func(_ *cobra.Command, args []string) error {
			args, err := a.takeGlobalFlags(args)
			if err != nil {
				return err
			}
			l, err := a.load("")
			if err != nil {
				return err
			}
			if _, ok := kube.Env(l.ctx); !ok {
				return fmt.Errorf("context %s has no clusters — add `clusters:` to its config, or run: mek -c %s kube --name <cluster>",
					l.ctx.Name, l.ctx.Name)
			}
			return a.guarded(l, append([]string{"kubectl"}, args...), guard.ClassifyKubectl(args))
		},
	}
}
