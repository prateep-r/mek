package provider

import "github.com/prateep-r/mek/internal/config"

// Query runs a read-only CLI command for a provider and returns its stdout
// (describe calls). The caller decides how it runs and is audited, so
// providers build commands and parse output but never execute anything.
type Query func(argv []string) ([]byte, error)

// KubeCluster is what a kubeconfig needs about one cluster.
type KubeCluster struct {
	Server   string // https URL of the API server
	CAData   string // base64 PEM of the cluster CA
	Location string // region or zone the token command needs
}

// KubeProvider is implemented by providers whose clusters `mek kube` supports.
// It supplies the cloud-specific steps of kube.Write (a Template Method).
type KubeProvider interface {
	// DescribeCluster looks the cluster up (through q) for the kubeconfig.
	DescribeCluster(c config.Cluster, q Query) (KubeCluster, error)
	// TokenCommand prints a client.authentication.k8s.io ExecCredential for
	// the cluster; location is KubeCluster.Location.
	TokenCommand(name, location string) []string
}

// Plugin is an extra tool a cloud needs for some features (doctor reports it
// as required only when a context uses them).
type Plugin struct {
	Bin, Purpose string
	Tool         Tool
	Needed       func(*config.Context) bool
}

func hasClusters(c *config.Context) bool { return len(c.Clusters) > 0 }

func hasTargets(c *config.Context) bool { return len(c.Targets) > 0 }

// FindPlugin looks a plugin up by binary name, for install hints.
func FindPlugin(bin string) (Plugin, bool) {
	for _, c := range clouds {
		for _, p := range c.Plugins {
			if p.Bin == bin {
				return p, true
			}
		}
	}
	return Plugin{}, false
}

// Instance is a resolved host that a session (or tunnel) connects to.
type Instance struct {
	ID    string // aws instance id, gcp VM name
	Zone  string // gcp
	User  string // gcp SSH user ("" = gcloud's default)
	Alias string // the configured target name, if it came from config
}

// Label names the instance for messages and the audit log.
func (i Instance) Label() string {
	s := i.ID
	if i.User != "" {
		s = i.User + "@" + s
	}
	if i.Zone != "" {
		s += " (" + i.Zone + ")"
	}
	if i.Alias != "" {
		s = i.Alias + " → " + s
	}
	return s
}

// TargetOptions are per-command overrides of a target (mek shell flags).
type TargetOptions struct{ Zone, User string }

// Command is a process mek runs for an access path.
type Command struct {
	Argv     []string
	Env      map[string]string // set for this process only (e.g. HOME)
	Requires []string          // plugins it needs on PATH
}

// Sessioner is implemented by providers that `mek shell` supports.
type Sessioner interface {
	// ResolveTarget turns what the user typed (a configured name, an instance
	// id, a tag or VM name) into an instance, looking it up through q.
	ResolveTarget(spec string, o TargetOptions, q Query) (Instance, error)
	// ShellCommand opens an interactive session on the instance.
	ShellCommand(in Instance) (Command, error)
}
