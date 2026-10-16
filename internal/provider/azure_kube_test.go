package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

const aadKubeconfig = `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: Q0E=
    server: https://aks-prod-dns.hcp.southeastasia.azmk8s.io:443
  name: aks-prod
users:
- name: clusterUser_rg-aks_aks-prod
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: kubelogin
      args: [get-token, --login, azurecli, --server-id, 11111111-2222-3333-4444-555555555555]
`

func TestAKS(t *testing.T) {
	z := &Azure{ctx: &config.Context{Name: "az", Provider: config.ProviderAzure}}
	var argv []string
	c := config.Cluster{Name: "aks-prod", ResourceGroup: "rg-aks"}
	got, err := z.DescribeCluster(c, fakeQuery(aadKubeconfig, nil, &argv))
	if err != nil || got != (KubeCluster{Server: "https://aks-prod-dns.hcp.southeastasia.azmk8s.io:443", CAData: "Q0E=", Location: "11111111-2222-3333-4444-555555555555"}) {
		t.Fatalf("describe: %+v %v", got, err)
	}
	if s := strings.Join(argv, " "); s != "az aks get-credentials --resource-group rg-aks --name aks-prod --file - --format exec --only-show-errors" {
		t.Errorf("argv: %s", s)
	}
	noServerID := strings.Replace(aadKubeconfig, ", --server-id, 11111111-2222-3333-4444-555555555555", "", 1)
	if got, _ := z.DescribeCluster(c, fakeQuery(noServerID, nil, &argv)); got.Location != aksServerID {
		t.Errorf("default server id: %+v", got)
	}
	local := strings.Replace(aadKubeconfig, aadKubeconfig[strings.Index(aadKubeconfig, "    exec:"):], "    client-certificate-data: Q0VSVA==\n", 1)
	for out, want := range map[string]string{
		local:       "uses local accounts",
		"{":         "unexpected output",
		"users: []": "unexpected output",
	} {
		if _, err := z.DescribeCluster(c, fakeQuery(out, nil, &argv)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", out, err)
		}
	}
	boom := errors.New("boom")
	if _, err := z.DescribeCluster(c, fakeQuery("", boom, &argv)); !errors.Is(err, boom) {
		t.Errorf("query error: %v", err)
	}
	if s := strings.Join(z.TokenCommand("aks-prod", "sid"), " "); s != "kubelogin get-token --login azurecli --server-id sid" {
		t.Errorf("token: %s", s)
	}
}
