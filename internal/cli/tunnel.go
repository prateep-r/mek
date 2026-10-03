package cli

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/ui"
)

type tunnelOpts struct {
	via, to, cloudSQL string // an ad-hoc tunnel
	privateIP         bool
	local             int
	target            provider.TargetOptions // --zone, --user of the hop
}

func (o tunnelOpts) adhoc() bool { return o.via != "" || o.to != "" || o.cloudSQL != "" || o.privateIP }

func (a *app) newTunnelCmd() *cobra.Command {
	var o tunnelOpts
	cmd := &cobra.Command{
		Use:   "tunnel [name]",
		Short: "Forward a local port to a private host (AWS SSM, GCP IAP, Cloud SQL)",
		Long: `Forward a port on localhost to a host in a private network, through the
cloud's own session service. It stays in the foreground; Ctrl-C closes it.

  mek -c prod tunnel db                                     # a name under the context's tunnels:
  mek -c prod tunnel --via bastion --to mydb.xyz.rds.amazonaws.com:5432
  mek -c prod tunnel --via i-0abc1234def567890 --to :8080   # a port on the instance itself
  mek -c gcp tunnel --cloudsql my-project:asia-southeast1:db --local 15432

The local port defaults to the remote port + 10000. A tunnel only moves bytes,
so readonly contexts allow it; protected contexts ask first. GCP tunnels to
another host log in to the VM over SSH, so they count as a shell.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.completeTunnels,
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return a.tunnel(name, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.via, "via", "", "the instance to go through: a target name, instance id, tag:Key=Value or VM name")
	f.StringVar(&o.to, "to", "", "the remote end: host:port, or :port on the instance itself")
	f.StringVar(&o.cloudSQL, "cloudsql", "", "gcp: a Cloud SQL instance, PROJECT:REGION:INSTANCE")
	f.BoolVar(&o.privateIP, "private-ip", false, "gcp: reach Cloud SQL over its private IP")
	f.IntVar(&o.local, "local", 0, "local port (default: remote port + 10000)")
	f.StringVar(&o.target.Zone, "zone", "", "gcp: the VM's zone (default: looked up)")
	f.StringVar(&o.target.User, "user", "", "gcp: SSH user for tunnels to another host")
	return cmd
}

func (a *app) tunnel(name string, o tunnelOpts) error {
	l, err := a.load("")
	if err != nil {
		return err
	}
	tp, ok := l.prov.(provider.Tunneler)
	if !ok {
		return fmt.Errorf("mek tunnel doesn't support %s yet", l.ctx.Provider)
	}
	spec, err := tunnelSpec(l, name, o)
	if err != nil {
		return err
	}
	m, err := tp.TunnelMethod(spec)
	if err != nil {
		return err
	}
	var in provider.Instance
	target := fmt.Sprintf("localhost:%d → %s", spec.Local(), m.Remote())
	if m.Via() != "" {
		if in, err = l.prov.(provider.Sessioner).ResolveTarget(m.Via(), o.target, a.query(l)); err != nil {
			return err
		}
		target += " via " + in.Label()
	}
	if err := portFree(spec.Local()); err != nil {
		return err
	}
	c, err := m.Command(in, spec.Local())
	if err != nil {
		return err
	}
	ui.Info("%s %s %s", ui.Green("⇄"), target, ui.Dim("(Ctrl-C to close)"))
	return a.session(l, c, m.Class(), target)
}

// tunnelSpec is the named tunnel (with --local applied) or the ad-hoc one
// from the flags, checked with the config's shared rules.
func tunnelSpec(l *loaded, name string, o tunnelOpts) (config.Tunnel, error) {
	switch {
	case name != "" && o.adhoc():
		return config.Tunnel{}, errors.New("a named tunnel takes only --local, --zone and --user")
	case name != "":
		t, ok := l.ctx.Tunnels[name]
		if !ok {
			return config.Tunnel{}, fmt.Errorf("context %s has no tunnel %q (tunnels: %s)", l.ctx.Name, name,
				strings.Join(slices.Sorted(maps.Keys(l.ctx.Tunnels)), ", "))
		}
		spec := *t
		if o.local != 0 {
			spec.LocalPort = o.local
		}
		return spec, spec.Validate()
	case !o.adhoc():
		return config.Tunnel{}, errors.New("name a tunnel from the context's tunnels:, or pass --via and --to, or --cloudsql")
	}
	spec := config.Tunnel{Via: o.via, CloudSQL: o.cloudSQL, PrivateIP: o.privateIP, LocalPort: o.local}
	if o.to != "" {
		host, port, err := net.SplitHostPort(o.to)
		if err != nil {
			return config.Tunnel{}, fmt.Errorf("--to %q: want host:port or :port", o.to)
		}
		if spec.Port, err = strconv.Atoi(port); err != nil {
			return config.Tunnel{}, fmt.Errorf("--to %q: port %q is not a number", o.to, port)
		}
		spec.Host = host
	}
	// The cloud's rules come from its TunnelMethod factory; a bad --via fails
	// when it is resolved.
	return spec, spec.Validate()
}

// portFree fails early, and clearly, when the local port is taken.
func portFree(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("localhost:%d is in use — pick another port with --local", port)
	}
	return ln.Close()
}

func (a *app) completeTunnels(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	l, err := a.resolve("")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return slices.Sorted(maps.Keys(l.ctx.Tunnels)), cobra.ShellCompDirectiveNoFileComp
}
