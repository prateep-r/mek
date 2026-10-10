package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/kube"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/ui"
)

type kubeOpts struct{ name, region, location string } // an ad-hoc cluster

func (a *app) newKubeCmd() *cobra.Command {
	var o kubeOpts
	cmd := &cobra.Command{
		Use:   "kube [cluster]",
		Short: "Write a kubeconfig for the context's clusters (kubectl, K9s, FreeLens)",
		Long: `Write <MEK_HOME>/kube/<context>.yaml for the clusters in the context's config.
Its credentials come from mek on every request, so it never holds a key.

  mek -c prod kube                      # every cluster under clusters:
  mek -c prod kube main                 # ...with main as the current one
  mek -c prod kube --name my-eks        # a cluster not in the config
  mek -c prod kubectl get pods          # kubectl, guarded and audited
  eval "$(mek use --shell prod)"        # KUBECONFIG for kubectl/k9s in this shell

~/.kube/config is left alone; to see these clusters from it too, list both:
  export KUBECONFIG=~/.kube/config:<MEK_HOME>/kube/prod.yaml`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.completeClusters,
		RunE: func(cmd *cobra.Command, args []string) error {
			current := ""
			if len(args) == 1 {
				current = args[0]
			}
			return a.kube(cmd.OutOrStdout(), current, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.name, "name", "", "a cluster that isn't in the config: its name in the cloud")
	f.StringVar(&o.region, "region", "", "aws: the ad-hoc cluster's region (default: the context's)")
	f.StringVar(&o.location, "location", "", "gcp: the ad-hoc cluster's zone or region")
	cmd.AddCommand(a.newKubeTokenCmd())
	return cmd
}

func (a *app) kube(out io.Writer, current string, o kubeOpts) error {
	l, err := a.load("")
	if err != nil {
		return err
	}
	clusters, err := kubeClusters(l.cfg, l.ctx, o)
	if err != nil {
		return err
	}
	if current != "" && clusters[current] == nil {
		return fmt.Errorf("context %s has no cluster %q (clusters: %s)", l.ctx.Name, current, strings.Join(slices.Sorted(maps.Keys(clusters)), ", "))
	}
	if current == "" && o.name != "" {
		current = o.name
	}
	entries, err := kube.Describe(l.prov, clusters, a.query(l))
	if err != nil {
		return err
	}
	mek, err := mekPath()
	if err != nil {
		return err
	}
	pathEnv := os.Getenv("PATH")
	path, err := kube.Write(l.ctx.Name, entries, current, mek, pathEnv)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, path)
	aliases := make([]string, len(entries))
	for i, e := range entries {
		aliases[i] = e.Alias
	}
	ui.Info("%s kubeconfig for %s: %s", ui.Green("✓"), ui.Bold(l.ctx.Name), strings.Join(aliases, ", "))
	ui.Info("  mek -c %s kubectl get pods\n  eval \"$(mek use --shell %s)\"   # then kubectl, k9s, ...", l.ctx.Name, l.ctx.Name)
	return nil
}

// kubeClusters is the context's clusters plus an ad-hoc one from the flags,
// validated with the config's rules.
func kubeClusters(cfg *config.Config, ctx *config.Context, o kubeOpts) (map[string]*config.Cluster, error) {
	if o.name == "" {
		if o.region != "" || o.location != "" {
			return nil, errors.New("--region and --location need --name")
		}
		if len(ctx.Clusters) == 0 {
			return nil, fmt.Errorf("context %s has no clusters — add `clusters:` to its config, or pass --name", ctx.Name)
		}
		return ctx.Clusters, nil
	}
	c := *ctx
	c.Clusters = maps.Clone(ctx.Clusters)
	if c.Clusters == nil {
		c.Clusters = map[string]*config.Cluster{}
	}
	c.Clusters[o.name] = &config.Cluster{Name: o.name, Region: o.region, Location: o.location}
	if err := provider.CheckContext(cfg, &c); err != nil {
		return nil, err
	}
	return c.Clusters, nil
}

// mekPath is the mek the kubeconfig runs (a test seam).
var mekPath = kube.MekPath

// newKubeTokenCmd is the exec credential plugin the kubeconfig runs. It is
// not audited (kubectl calls it on every request) and needs the context
// named explicitly, so a kubeconfig never follows `mek use`.
func (a *app) newKubeTokenCmd() *cobra.Command {
	var name, location string
	cmd := &cobra.Command{
		Use:    "token --name <cluster> --location <region|zone>",
		Short:  "Print a cluster token for kubectl (used by mek's kubeconfig)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if a.opts.context == "" {
				return errors.New("mek kube token needs --context")
			}
			l, err := a.load("")
			if err != nil {
				return err
			}
			return a.runPlain(l.prov.TokenCommand(name, location), l.env.Apply(os.Environ()))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "cluster name")
	cmd.Flags().StringVar(&location, "location", "", "cluster region or zone")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("location")
	return cmd
}

func (a *app) completeClusters(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	l, err := a.resolve("")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return slices.Sorted(maps.Keys(l.ctx.Clusters)), cobra.ShellCompDirectiveNoFileComp
}
