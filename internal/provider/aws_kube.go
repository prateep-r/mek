package provider

import (
	"encoding/json"
	"fmt"

	"github.com/prateep-r/mek/internal/config"
)

var _ KubeProvider = (*AWS)(nil)

// DescribeCluster reads an EKS cluster's endpoint and CA.
func (a *AWS) DescribeCluster(c config.Cluster, q Query) (KubeCluster, error) {
	region := c.Region
	if region == "" {
		region = a.ctx.Region
	}
	out, err := q([]string{"aws", "eks", "describe-cluster", "--name", c.Name, "--region", region,
		"--query", "cluster.{endpoint:endpoint,ca:certificateAuthority.data,status:status}", "--output", "json"})
	if err != nil {
		return KubeCluster{}, err
	}
	var d struct{ Endpoint, CA, Status string }
	if err := json.Unmarshal(out, &d); err != nil {
		return KubeCluster{}, fmt.Errorf("eks describe-cluster %s: %w", c.Name, err)
	}
	if d.Status != "ACTIVE" || d.Endpoint == "" {
		return KubeCluster{}, fmt.Errorf("eks cluster %s is %s, not ACTIVE", c.Name, orDash(d.Status))
	}
	return KubeCluster{Server: d.Endpoint, CAData: d.CA, Location: region}, nil
}

func (a *AWS) TokenCommand(name, region string) []string {
	return []string{"aws", "eks", "get-token", "--cluster-name", name, "--region", region, "--output", "json"}
}
