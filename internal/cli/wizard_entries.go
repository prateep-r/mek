package cli

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/wizard"
)

func (e *entryWizard) aws() bool { return e.ctx.Provider == config.ProviderAWS }

// ---------- targets: hosts for mek shell ----------

type targetKind struct{}

func (targetKind) section() string { return "targets" }
func (targetKind) noun() string    { return "target" }
func (targetKind) names(c *config.Context) []string {
	return slices.Sorted(maps.Keys(c.Targets))
}
func (targetKind) describe(c *config.Context, name string) string { return c.Targets[name].Instance }
func (targetKind) remove(c *config.Context, name string)          { delete(c.Targets, name) }
func (targetKind) next(ctx, name string) string                   { return "mek -c " + ctx + " shell " + name }

func (targetKind) entry(c *config.Context, name string) any {
	if t, ok := c.Targets[name]; ok {
		return t
	}
	return nil
}

func (targetKind) steps(f *wizard.Form, e *entryWizard) {
	if e.aws() {
		f.Load("instance", "Instance", func(wizard.Answers) ([]wizard.Option, error) {
			return e.lookup(provider.EC2InstancesCommand(), provider.ParseEC2Instances)
		}).Check(required("an instance")).Help("running instances — or type i-… or tag:Key=Value")
		f.Select("by", "Refer to it by", func(a wizard.Answers) []wizard.Option {
			return []wizard.Option{
				{Value: "tag:Name=" + e.extra[a["instance"]], Hint: "its Name tag: still right when the instance is replaced"},
				{Value: a["instance"], Hint: "its id: this instance only"},
			}
		}, false).When(func(a wizard.Answers) bool { return e.extra[a["instance"]] != "" })
		e.nameStep(f, func(a wizard.Answers) string { return alias(e.extra[a["instance"]]) })
		return
	}
	f.Load("instance", "VM", func(wizard.Answers) ([]wizard.Option, error) {
		return e.lookup(provider.GCEInstancesCommand(), provider.ParseGCEInstances)
	}).Check(required("a VM name")).Help("running VMs — or type a name")
	f.Input("zone", "Zone", func(a wizard.Answers) string { return e.extra[a["instance"]] }, nil).Help("empty: looked up when it is used")
	f.Input("user", "SSH user", nil, nil).Help("empty: gcloud's (your user name here)")
	e.nameStep(f, func(a wizard.Answers) string { return alias(a["instance"]) })
}

func (targetKind) prefill(c *config.Context, name string, a wizard.Answers) {
	t := c.Targets[name]
	a["instance"], a["by"], a["zone"], a["user"] = t.Instance, t.Instance, t.Zone, t.User
}

func (targetKind) apply(e *entryWizard, a wizard.Answers, c *config.Context) {
	t := &config.Target{Instance: a["instance"], Zone: a["zone"], User: a["user"]}
	if e.aws() {
		t.Zone, t.User = "", ""
		if e.extra[a["instance"]] != "" {
			t.Instance = a["by"]
		}
	}
	if c.Targets == nil {
		c.Targets = map[string]*config.Target{}
	}
	c.Targets[a["name"]] = t
}

// ---------- clusters: Kubernetes for mek kube ----------

type clusterKind struct{}

func (clusterKind) section() string { return "clusters" }
func (clusterKind) noun() string    { return "cluster" }
func (clusterKind) names(c *config.Context) []string {
	return slices.Sorted(maps.Keys(c.Clusters))
}
func (clusterKind) describe(c *config.Context, name string) string { return c.Clusters[name].Name }
func (clusterKind) remove(c *config.Context, name string)          { delete(c.Clusters, name) }
func (clusterKind) next(ctx, name string) string                   { return "mek -c " + ctx + " kube " + name }

func (clusterKind) entry(c *config.Context, name string) any {
	if cl, ok := c.Clusters[name]; ok {
		return cl
	}
	return nil
}

func (clusterKind) steps(f *wizard.Form, e *entryWizard) {
	if e.aws() {
		none := ""
		if e.ctx.Region != "" {
			none = "the context's, " + e.ctx.Region
		}
		f.Select("region", "Region", wizard.Options(regionOptions(awsRegions, none)...), true).
			When(func(a wizard.Answers) bool { return e.ctx.Region == "" || a["region"] != "" }).
			Check(func(v string, _ wizard.Answers) error {
				if v == "" && e.ctx.Region == "" {
					return errors.New("a region, please: the context has none")
				}
				return nil
			}).Help("where the cluster is")
		f.Load("cluster", "EKS cluster", func(a wizard.Answers) ([]wizard.Option, error) {
			return e.lookup(provider.EKSClustersCommand(a["region"]), provider.ParseEKSClusters)
		}).Check(required("a cluster name"))
	} else {
		f.Load("cluster", "GKE cluster", func(wizard.Answers) ([]wizard.Option, error) {
			return e.lookup(provider.GKEClustersCommand(), provider.ParseGKEClusters)
		}).Check(required("a cluster name"))
		f.Input("location", "Location", func(a wizard.Answers) string { return e.extra[a["cluster"]] }, required("a zone or region")).
			Help("the cluster's zone or region")
		f.Select("endpoint", "Reach it at", wizard.Options(
			wizard.Option{Value: "", Label: "its endpoint", Hint: "the cluster's own (public, if it has one)"},
			wizard.Option{Value: "private", Hint: "its internal IP — from inside the VPC"},
			wizard.Option{Value: "dns", Hint: "its DNS-based endpoint — from anywhere IAM allows"},
		), false)
	}
	f.Input("namespace", "Namespace", nil, nil).Help("kubectl's default namespace for it; empty: default")
	e.nameStep(f, func(a wizard.Answers) string { return alias(a["cluster"]) })
}

func (clusterKind) prefill(c *config.Context, name string, a wizard.Answers) {
	cl := c.Clusters[name]
	a["cluster"], a["region"], a["location"], a["endpoint"], a["namespace"] = cl.Name, cl.Region, cl.Location, cl.Endpoint, cl.Namespace
}

func (clusterKind) apply(e *entryWizard, a wizard.Answers, c *config.Context) {
	cl := &config.Cluster{Name: a["cluster"], Namespace: a["namespace"]}
	if e.aws() {
		cl.Region = a["region"]
	} else {
		cl.Location, cl.Endpoint = a["location"], a["endpoint"]
	}
	if c.Clusters == nil {
		c.Clusters = map[string]*config.Cluster{}
	}
	c.Clusters[a["name"]] = cl
}

// ---------- tunnels: port forwards for mek tunnel ----------

type tunnelKind struct{}

func (tunnelKind) section() string { return "tunnels" }
func (tunnelKind) noun() string    { return "tunnel" }
func (tunnelKind) names(c *config.Context) []string {
	return slices.Sorted(maps.Keys(c.Tunnels))
}
func (tunnelKind) remove(c *config.Context, name string) { delete(c.Tunnels, name) }
func (tunnelKind) next(ctx, name string) string          { return "mek -c " + ctx + " tunnel " + name }

func (tunnelKind) entry(c *config.Context, name string) any {
	if t, ok := c.Tunnels[name]; ok {
		return t
	}
	return nil
}

func (tunnelKind) describe(c *config.Context, name string) string {
	t := c.Tunnels[name]
	switch {
	case t.CloudSQL != "":
		return fmt.Sprintf("localhost:%d → %s", t.Local(), t.CloudSQL)
	case t.Host != "":
		return fmt.Sprintf("localhost:%d → %s:%d via %s", t.Local(), t.Host, t.Port, t.Via)
	}
	return fmt.Sprintf("localhost:%d → %s:%d", t.Local(), t.Via, t.Port)
}

func (tunnelKind) steps(f *wizard.Form, e *entryWizard) {
	kinds := []wizard.Option{
		{Value: "host", Label: "a host, through an instance", Hint: "e.g. a database: RDS, a private IP, Redis"},
		{Value: "port", Label: "a port on an instance", Hint: "a service on the instance itself"},
	}
	if !e.aws() {
		kinds = append(kinds, wizard.Option{Value: "cloudsql", Label: "Cloud SQL", Hint: "through the Cloud SQL Auth Proxy"})
	}
	is := func(k string) func(wizard.Answers) bool { return func(a wizard.Answers) bool { return a["kind"] == k } }
	via := func(a wizard.Answers) bool { return a["kind"] != "cloudsql" }
	f.Select("kind", "Forward to", wizard.Options(kinds...), false)
	f.Load("via", "Through", e.instancesAndTargets).When(via).Check(required("an instance")).
		Help("one of the context's targets, or a running instance")
	if e.aws() {
		f.Load("host", "Host", e.databases).When(is("host")).Check(required("a host")).Help("RDS and Aurora endpoints — or type a host")
	} else {
		f.Input("host", "Host", nil, required("a host")).When(is("host")).Help("its address, as the instance reaches it")
	}
	f.Input("port", "Port", func(a wizard.Answers) string { return e.extra[a["host"]] }, checkPort).When(via)
	if !e.aws() {
		f.Load("cloudsql", "Instance", func(wizard.Answers) ([]wizard.Option, error) {
			return e.lookup(provider.CloudSQLInstancesCommand(), provider.ParseCloudSQL)
		}).When(is("cloudsql")).Check(required("PROJECT:REGION:INSTANCE")).Help("its connection name")
		f.Select("private_ip", "Connect over", wizard.Options(
			wizard.Option{Value: "", Label: "its public IP", Hint: "the proxy's default"},
			wizard.Option{Value: "yes", Label: "its private IP", Hint: "from inside the VPC"},
		), false).When(is("cloudsql"))
	}
	f.Input("local_port", "Local port", e.defaultLocal, e.checkLocal).Help("localhost:<port> on this machine")
	e.nameStep(f, func(a wizard.Answers) string {
		switch a["kind"] {
		case "host":
			return alias(e.labels[a["host"]])
		case "cloudsql":
			return alias(a["cloudsql"][strings.LastIndex(a["cloudsql"], ":")+1:])
		}
		return ""
	})
}

// instancesAndTargets are the context's targets, then running instances
// (named ones by their Name tag). The targets alone when the lookup fails.
func (e *entryWizard) instancesAndTargets(wizard.Answers) ([]wizard.Option, error) {
	var opts []wizard.Option
	for _, n := range (targetKind{}).names(e.ctx) {
		opts = append(opts, wizard.Option{Value: n, Hint: "target · " + e.ctx.Targets[n].Instance})
	}
	argv, parse := provider.GCEInstancesCommand(), provider.ParseGCEInstances
	if e.aws() {
		argv, parse = provider.EC2InstancesCommand(), provider.ParseEC2Instances
	}
	found, err := e.lookup(argv, parse)
	if err != nil && len(opts) == 0 {
		return nil, err
	}
	for _, o := range found {
		if e.aws() && o.Label != "" {
			o.Value = "tag:Name=" + o.Label
		}
		opts = append(opts, o)
	}
	return opts, nil
}

// databases are RDS instances and Aurora clusters, if either lookup works.
func (e *entryWizard) databases(wizard.Answers) ([]wizard.Option, error) {
	dbs, err1 := e.lookup(provider.RDSInstancesCommand(), provider.ParseRDS)
	clusters, err2 := e.lookup(provider.RDSClustersCommand(), provider.ParseRDS)
	if err1 != nil && err2 != nil {
		return nil, err1
	}
	return append(clusters, dbs...), nil
}

func checkPort(v string, _ wizard.Answers) error {
	if p, err := strconv.Atoi(v); err != nil || p < 1 || p > 65535 {
		return errors.New("a port is a number from 1 to 65535")
	}
	return nil
}

// cloudSQLPorts are local ports for Cloud SQL by engine: the usual port +
// 10000, like other tunnels.
var cloudSQLPorts = map[string]string{"POSTGRES": "15432", "MYSQL": "13306", "SQLSERVER": "11433"}

func (e *entryWizard) defaultLocal(a wizard.Answers) string {
	if a["kind"] == "cloudsql" {
		engine, _, _ := strings.Cut(e.extra[a["cloudsql"]], "_")
		return cloudSQLPorts[engine]
	}
	if p, err := strconv.Atoi(a["port"]); err == nil && p+10000 <= 65535 {
		return strconv.Itoa(p + 10000)
	}
	return ""
}

// checkLocal wants a port nothing here listens on — the entry's own, when
// editing, may be its running tunnel.
func (e *entryWizard) checkLocal(v string, a wizard.Answers) error {
	if err := checkPort(v, a); err != nil {
		return err
	}
	if t, ok := e.ctx.Tunnels[e.old]; ok && strconv.Itoa(t.Local()) == v {
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+v)
	if err != nil {
		return fmt.Errorf("localhost:%s is in use — pick another", v)
	}
	ln.Close()
	return nil
}

func (tunnelKind) prefill(c *config.Context, name string, a wizard.Answers) {
	t := c.Tunnels[name]
	a["kind"] = "port"
	switch {
	case t.CloudSQL != "":
		a["kind"] = "cloudsql"
	case t.Host != "":
		a["kind"] = "host"
	}
	a["via"], a["host"], a["cloudsql"], a["local_port"] = t.Via, t.Host, t.CloudSQL, strconv.Itoa(t.Local())
	if t.Port != 0 {
		a["port"] = strconv.Itoa(t.Port)
	}
	if t.PrivateIP {
		a["private_ip"] = "yes"
	}
}

func (tunnelKind) apply(_ *entryWizard, a wizard.Answers, c *config.Context) {
	t := &config.Tunnel{}
	local, _ := strconv.Atoi(a["local_port"])
	if a["kind"] == "cloudsql" {
		t.CloudSQL, t.PrivateIP, t.LocalPort = a["cloudsql"], a["private_ip"] == "yes", local
	} else {
		t.Via, t.Port = a["via"], atoi(a["port"])
		if a["kind"] == "host" {
			t.Host = a["host"]
		}
		if local != t.Port+10000 {
			t.LocalPort = local
		}
	}
	if c.Tunnels == nil {
		c.Tunnels = map[string]*config.Tunnel{}
	}
	c.Tunnels[a["name"]] = t
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s) // checked by the form
	return n
}
