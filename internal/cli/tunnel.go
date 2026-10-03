package cli

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/tunnel"
	"github.com/prateep-r/mek/internal/ui"
)

type tunnelOpts struct {
	via, to, cloudSQL string // an ad-hoc tunnel
	privateIP         bool
	local             int
	target            provider.TargetOptions // --zone, --user of the hop
	background        bool
	wait              time.Duration
}

func (o tunnelOpts) adhoc() bool { return o.via != "" || o.to != "" || o.cloudSQL != "" || o.privateIP }

func (a *app) newTunnelCmd() *cobra.Command {
	var o tunnelOpts
	cmd := &cobra.Command{
		Use:   "tunnel [name]",
		Short: "Forward a local port to a private host (AWS SSM, GCP IAP, Cloud SQL, Azure Bastion)",
		Long: `Forward a port on localhost to a host in a private network, through the
cloud's own session service. It stays in the foreground; Ctrl-C closes it.

  mek -c prod tunnel db                                     # a name under the context's tunnels:
  mek -c prod tunnel --via bastion --to mydb.xyz.rds.amazonaws.com:5432
  mek -c prod tunnel --via i-0abc1234def567890 --to :8080   # a port on the instance itself
  mek -c gcp tunnel --cloudsql my-project:asia-southeast1:db --local 15432
  mek -c az tunnel --to 10.1.2.3:5432                       # azure: Bastion connects to the IP

  mek -c prod tunnel db -b                                  # in the background
  mek tunnel ls                                             # every context's tunnels
  mek tunnel stop db                                        # or an id, or --all
  mek tunnel logs db [-f]

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
	f.StringVar(&o.target.ResourceGroup, "resource-group", "", "azure: the VM's resource group (default: looked up)")
	f.StringVar(&o.target.User, "user", "", "gcp: SSH user for tunnels to another host")
	f.BoolVarP(&o.background, "background", "b", false, "run in the background (see mek tunnel ls / stop / logs)")
	f.DurationVar(&o.wait, "wait", 30*time.Second, "with --background: how long to wait for the local port")
	cmd.AddCommand(a.newTunnelLsCmd(), a.newTunnelStopCmd(), a.newTunnelLogsCmd(), newSuperviseCmd())
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
	if err := tunnel.PortFree(spec.Local()); err != nil { // a mek tunnel: say which
		return err
	}
	if err := portFree(spec.Local()); err != nil {
		return err
	}
	c, err := m.Command(in, spec.Local())
	if err != nil {
		return err
	}
	terminal := runner.Chain(a.exec, foreground(name, spec.Local()))
	if o.background {
		terminal = background(name, spec.Local(), o.wait)
	} else {
		ui.Info("%s %s %s", ui.Green("⇄"), target, ui.Dim("(Ctrl-C to close)"))
	}
	return a.session(l, c, m.Class(), target, terminal)
}

// tunnelRecord is what mek keeps about a tunnel that passed the guard.
func tunnelRecord(inv *runner.Invocation, name string, local int) tunnel.Record {
	return tunnel.Record{ID: inv.Session, Context: inv.Context.Name, Provider: inv.Context.Provider, Name: name,
		Target: inv.Target, Local: local, Class: inv.Class.String(), Decision: inv.Decision, Command: inv.Argv}
}

// foreground registers a tunnel that runs in this process, so `mek tunnel
// ls` shows it and `stop` can end it.
func foreground(name string, local int) runner.Decorator {
	return func(next runner.Runner) runner.Runner {
		return runner.Func(func(inv *runner.Invocation) error {
			done, err := tunnel.Foreground(tunnelRecord(inv, name, local))
			if err != nil {
				inv.ExitCode = 1
				return err
			}
			defer done()
			return next.Run(inv)
		})
	}
}

// background is the pipeline's terminal for -b: it hands the tunnel to a
// supervisor, which also writes the audit end entry once it really ends.
func background(name string, local int, wait time.Duration) runner.Runner {
	return runner.Func(func(inv *runner.Invocation) error {
		r, err := startTunnel(tunnel.Spec{Record: tunnelRecord(inv, name, local), Env: inv.Env}, wait)
		if r.ID != "" {
			inv.Detached = true // the supervisor ran: the end entry is its job
		}
		if err != nil {
			inv.ExitCode = 1
			return err
		}
		ui.Info("%s tunnel %s is running in the background: %s\n  mek tunnel ls · mek tunnel logs %s · mek tunnel stop %s",
			ui.Green("✓"), ui.Bold(r.Label()), inv.Target, r.Label(), r.Label())
		return nil
	})
}

var startTunnel = tunnel.Start // test seam: it spawns this binary as the supervisor

// tunnelFilter is the context `ls` and `stop` look at: only an explicit
// -c narrows them; by default they cover every context.
func (a *app) tunnelFilter() string { return a.opts.context }

func (a *app) newTunnelLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List tunnels, background and foreground, of every context (-c for one)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			infos := tunnel.List(a.tunnelFilter())
			if len(infos) == 0 {
				ui.Info("%s no tunnels", ui.Dim("–"))
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tCONTEXT\tNAME\tLOCAL\tSTATE\tAGE\tTARGET")
			for _, i := range infos {
				mode := ""
				if !i.Background {
					mode = " (foreground)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s%s\t%s\t%s\n", i.ShortID(), i.Context, orDash(i.Name), i.Local,
					i.State.Name(), mode, age(i.Started), i.Target)
			}
			return w.Flush()
		},
	}
}

func (a *app) newTunnelStopCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "stop <name|id>... | --all",
		Short: "Stop tunnels (finished and dead ones are cleaned up)",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 && !all {
				return errors.New("name the tunnels to stop, or pass --all")
			}
			stopped, err := tunnel.Stop(args, all, a.tunnelFilter())
			for _, i := range stopped {
				ui.Info("%s stopped %s (%s, localhost:%d)", ui.Green("✓"), i.Label(), i.Context, i.Local)
				if i.State.Name() == "dead" && portFree(i.Local) != nil {
					ui.Info("  %s localhost:%d is still in use: its process may have outlived its supervisor", ui.Yellow("note:"), i.Local)
				}
			}
			if len(stopped) == 0 && err == nil {
				ui.Info("%s no tunnels", ui.Dim("–"))
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "stop every tunnel (of the -c context, if given)")
	return cmd
}

func (a *app) newTunnelLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs <name|id>",
		Short: "Show a tunnel's log",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			i, err := tunnel.Find(args[0], a.tunnelFilter())
			if err != nil {
				return err
			}
			return tunnel.Logs(i, cmd.OutOrStdout(), follow)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing while the tunnel runs")
	return cmd
}

// newSuperviseCmd is the background tunnel's supervisor process, started by
// tunnel.Start with the tunnel's locks and log as fds 3-5.
func newSuperviseCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "_supervise <id>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error {
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
			tunnel.Supervise(os.Stdin, os.NewFile(3, "life.lock"), os.NewFile(4, "port.lock"), os.NewFile(5, "tunnel.log"), sigs)
			return nil
		},
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// age is a short duration since t, like 5s, 12m or 3h.
func age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// tunnelSpec is the named tunnel (with --local applied) or the ad-hoc one
// from the flags, checked with the config's shared rules.
func tunnelSpec(l *loaded, name string, o tunnelOpts) (config.Tunnel, error) {
	switch {
	case name != "" && o.adhoc():
		return config.Tunnel{}, errors.New("a named tunnel takes only --local, --zone, --resource-group and --user")
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
		return config.Tunnel{}, errors.New("name a tunnel from the context's tunnels:, or pass --via and/or --to, or --cloudsql")
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
