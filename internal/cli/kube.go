package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/kube"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/ui"
)

type kubeOpts struct {
	name, region, location string // an ad-hoc cluster
	merge, use, unmerge    bool
}

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
  mek -c prod kube --merge [--use]      # also add it to ~/.kube/config (opt-in)
  mek -c prod kube --unmerge            # remove mek's entries from it again`,
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
	f.BoolVar(&o.merge, "merge", false, "also add the clusters to your kubeconfig ($KUBECONFIG or ~/.kube/config)")
	f.BoolVar(&o.use, "use", false, "with --merge: switch kubectl's current-context to the cluster")
	f.BoolVar(&o.unmerge, "unmerge", false, "remove this context's clusters from your kubeconfig")
	cmd.MarkFlagsMutuallyExclusive("merge", "unmerge")
	cmd.AddCommand(a.newKubeTokenCmd())
	return cmd
}

func (a *app) kube(out io.Writer, current string, o kubeOpts) error {
	if o.use && !o.merge {
		return errors.New("--use needs --merge")
	}
	if o.unmerge {
		return a.kubeUnmerge(current)
	}
	l, err := a.load("")
	if err != nil {
		return err
	}
	kp, err := capability[provider.KubeProvider](l, "mek kube")
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
	entries, err := kube.Describe(kp, clusters, a.query(l))
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
	if o.merge {
		return a.kubeMerge(l.ctx.Name, entries, current, mek, pathEnv, o.use)
	}
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

// kubeMerge adds the entries to the user's kubeconfig with `kubectl config`.
func (a *app) kubeMerge(ctx string, entries []kube.Entry, current, mek, pathEnv string, use bool) error {
	target, err := mergeTarget()
	if err != nil {
		return err
	}
	if use && current != "" { // use-context switches to the first entry
		i := slices.IndexFunc(entries, func(e kube.Entry) bool { return e.Alias == current })
		entries = append([]kube.Entry{entries[i]}, slices.Delete(slices.Clone(entries), i, i+1)...)
	}
	tmp, err := os.MkdirTemp("", "mek-kube-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	cmds, err := kube.MergeCommands(ctx, target, entries, mek, pathEnv, use, func(e kube.Entry) (string, error) { return kube.WriteCA(tmp, e) })
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := kube.Backup(target); err != nil {
		return err
	}
	for _, argv := range cmds {
		if err := a.plainTo(argv, os.Environ(), io.Discard); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(argv[:3], " "), err)
		}
	}
	ui.Info("%s merged into %s %s", ui.Green("✓"), target, ui.Dim("(backup: "+target+".mek-backup)"))
	return nil
}

// kubeUnmerge removes the context's entries (named <ctx>/...) from the
// user's kubeconfig, or only one cluster's when given.
func (a *app) kubeUnmerge(only string) error {
	l, err := a.resolve("")
	if err != nil {
		return err
	}
	target, err := mergeTarget()
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := a.plainTo([]string{"kubectl", "config", "get-contexts", "-o", "name", "--kubeconfig", target}, os.Environ(), &buf); err != nil {
		return err
	}
	var aliases []string
	for _, name := range strings.Fields(buf.String()) {
		if alias, ok := strings.CutPrefix(name, l.ctx.Name+"/"); ok && (only == "" || alias == only) {
			aliases = append(aliases, alias)
		}
	}
	if len(aliases) == 0 {
		ui.Info("%s no %s clusters in %s", ui.Dim("–"), l.ctx.Name, target)
		return nil
	}
	failed := 0
	for _, argv := range kube.UnmergeCommands(l.ctx.Name, target, aliases) {
		if a.plainTo(argv, os.Environ(), io.Discard) != nil {
			failed++ // e.g. a cluster entry already removed by hand: kubectl said so on stderr
		}
	}
	ui.Info("%s removed %s from %s", ui.Green("✓"), strings.Join(aliases, ", "), target)
	if failed > 0 {
		return fmt.Errorf("%d kubectl config command(s) failed", failed)
	}
	return nil
}

// Test seams.
var (
	userHome = os.UserHomeDir
	mekPath  = kube.MekPath
)

func mergeTarget() (string, error) {
	home, err := userHome()
	if err != nil {
		return "", err
	}
	return kube.MergeTarget(os.Getenv("KUBECONFIG"), home), nil
}

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
			kp, err := capability[provider.KubeProvider](l, "mek kube")
			if err != nil {
				return err
			}
			return a.runPlain(kp.TokenCommand(name, location), l.env.Apply(os.Environ()))
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
