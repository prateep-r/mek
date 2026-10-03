package provider

import (
	"fmt"
	"slices"

	"github.com/goccy/go-yaml"

	"github.com/prateep-r/mek/internal/config"
)

var _ KubeProvider = (*Azure)(nil)

// aksServerID is the Entra ID application every AKS cluster's API accepts
// tokens for, used when az doesn't say.
const aksServerID = "6dae42f8-4368-4678-94ff-3960e28e3630"

// DescribeCluster reads an AKS cluster's API server and CA from the
// kubeconfig az prints (written nowhere), and the server id its tokens are
// for. Clusters with local accounts only have long-lived certificates, so
// mek doesn't support them.
func (z *Azure) DescribeCluster(c config.Cluster, q Query) (KubeCluster, error) {
	out, err := q([]string{"az", "aks", "get-credentials", "--resource-group", c.ResourceGroup, "--name", c.Name,
		"--file", "-", "--format", "exec", "--only-show-errors"})
	if err != nil {
		return KubeCluster{}, err
	}
	var kc struct {
		Clusters []struct {
			Cluster struct {
				Server string `yaml:"server"`
				CA     string `yaml:"certificate-authority-data"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users []struct {
			User struct {
				Exec *struct {
					Args []string `yaml:"args"`
				} `yaml:"exec"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal(out, &kc); err != nil || len(kc.Clusters) == 0 || len(kc.Users) == 0 {
		return KubeCluster{}, fmt.Errorf("aks get-credentials %s: unexpected output (%v)", c.Name, err)
	}
	exec := kc.Users[0].User.Exec
	if exec == nil {
		return KubeCluster{}, fmt.Errorf("aks cluster %s uses local accounts (long-lived certificates) — mek supports clusters with Microsoft Entra ID integration", c.Name)
	}
	serverID := aksServerID
	if i := slices.Index(exec.Args, "--server-id"); i >= 0 && i+1 < len(exec.Args) {
		serverID = exec.Args[i+1]
	}
	return KubeCluster{Server: kc.Clusters[0].Cluster.Server, CAData: kc.Clusters[0].Cluster.CA, Location: serverID}, nil
}

// TokenCommand: kubelogin gets the token through the context's az login.
// The "location" a kubeconfig passes is the cluster's server id.
func (z *Azure) TokenCommand(_, serverID string) []string {
	return []string{"kubelogin", "get-token", "--login", "azurecli", "--server-id", serverID}
}
