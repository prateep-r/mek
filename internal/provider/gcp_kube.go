package provider

import (
	"encoding/json"
	"fmt"

	"github.com/prateep-r/mek/internal/config"
)

var _ KubeProvider = (*GCP)(nil)

// DescribeCluster reads a GKE cluster's endpoint and CA.
func (g *GCP) DescribeCluster(c config.Cluster, q Query) (KubeCluster, error) {
	out, err := q([]string{"gcloud", "container", "clusters", "describe", c.Name, "--location", c.Location,
		"--format", "json(endpoint,masterAuth.clusterCaCertificate,status)"})
	if err != nil {
		return KubeCluster{}, err
	}
	var d struct {
		Endpoint   string
		Status     string
		MasterAuth struct{ ClusterCaCertificate string }
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return KubeCluster{}, fmt.Errorf("gke describe %s: %w", c.Name, err)
	}
	if d.Status != "RUNNING" || d.Endpoint == "" {
		return KubeCluster{}, fmt.Errorf("gke cluster %s is %s, not RUNNING", c.Name, orDash(d.Status))
	}
	return KubeCluster{Server: "https://" + d.Endpoint, CAData: d.MasterAuth.ClusterCaCertificate, Location: c.Location}, nil
}

// TokenCommand: gke-gcloud-auth-plugin reads the context's CLOUDSDK_CONFIG.
func (g *GCP) TokenCommand(string, string) []string { return []string{"gke-gcloud-auth-plugin"} }
