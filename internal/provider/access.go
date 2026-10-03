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
