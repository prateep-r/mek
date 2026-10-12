package provider

import (
	"encoding/json"
	"fmt"

	"github.com/prateep-r/mek/internal/config"
)

var _ KubeProvider = (*GCP)(nil)

// DescribeCluster reads a GKE cluster's endpoint and CA. It picks the
// endpoint as `gcloud container clusters get-credentials` does: the
// cluster's default, its internal IP (endpoint: private, like --internal-ip),
// or its DNS-based endpoint (endpoint: dns, like --dns-endpoint; also when
// the cluster's IP endpoints are off). The DNS endpoint has a publicly
// trusted certificate, so it gets no CA.
func (g *GCP) DescribeCluster(c config.Cluster, q Query) (KubeCluster, error) {
	out, err := q([]string{"gcloud", "container", "clusters", "describe", c.Name, "--location", c.Location,
		"--format", "json(endpoint,masterAuth.clusterCaCertificate,status,privateClusterConfig.privateEndpoint," +
			"controlPlaneEndpointsConfig.dnsEndpointConfig,controlPlaneEndpointsConfig.ipEndpointsConfig.enabled)"})
	if err != nil {
		return KubeCluster{}, err
	}
	var d struct {
		Endpoint             string
		Status               string
		MasterAuth           struct{ ClusterCaCertificate string }
		PrivateClusterConfig struct{ PrivateEndpoint string }
		Endpoints            struct {
			DNS struct {
				Endpoint             string
				AllowExternalTraffic bool
			} `json:"dnsEndpointConfig"`
			IP struct{ Enabled *bool } `json:"ipEndpointsConfig"`
		} `json:"controlPlaneEndpointsConfig"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return KubeCluster{}, fmt.Errorf("gke describe %s: %w", c.Name, err)
	}
	if d.Status != "RUNNING" {
		return KubeCluster{}, fmt.Errorf("gke cluster %s is %s, not RUNNING", c.Name, orDash(d.Status))
	}
	ipOff := d.Endpoints.IP.Enabled != nil && !*d.Endpoints.IP.Enabled
	k := KubeCluster{CAData: d.MasterAuth.ClusterCaCertificate, Location: c.Location}
	switch {
	case c.Endpoint == "dns" || ipOff && c.Endpoint == "":
		dns := d.Endpoints.DNS
		if dns.Endpoint == "" {
			return KubeCluster{}, fmt.Errorf("gke cluster %s has no DNS-based endpoint", c.Name)
		}
		if !dns.AllowExternalTraffic {
			return KubeCluster{}, fmt.Errorf("gke cluster %s's DNS-based endpoint doesn't allow external traffic", c.Name)
		}
		k.Server, k.CAData = "https://"+dns.Endpoint, ""
	case ipOff:
		return KubeCluster{}, fmt.Errorf("gke cluster %s has its IP endpoints off — use endpoint: dns", c.Name)
	case c.Endpoint == "private":
		if d.PrivateClusterConfig.PrivateEndpoint == "" {
			return KubeCluster{}, fmt.Errorf("gke cluster %s has no private endpoint", c.Name)
		}
		k.Server = "https://" + d.PrivateClusterConfig.PrivateEndpoint
	case d.Endpoint == "":
		return KubeCluster{}, fmt.Errorf("gke cluster %s has no endpoint", c.Name)
	default:
		k.Server = "https://" + d.Endpoint
	}
	return k, nil
}

// TokenCommand: gke-gcloud-auth-plugin reads the context's CLOUDSDK_CONFIG.
func (g *GCP) TokenCommand(string, string) []string { return []string{"gke-gcloud-auth-plugin"} }
