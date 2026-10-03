package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const azVMID = "/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/rg-app/providers/Microsoft.Compute/virtualMachines/vm-jump"

const azureConfig = `contexts:
  az: {provider: azure, tenant_id: t, subscription_id: 00000000-1111-2222-3333-444444444444, protected: true,
       bastion: {name: bas, resource_group: rg-net},
       targets: {jump: {instance: vm-jump, resource_group: rg-app}, key: {instance: vm-key, auth: ssh-key, user: ops}},
       tunnels: {db: {host: 10.1.2.3, port: 5432, local_port: 35500}, web: {via: jump, port: 80, local_port: 35501}},
       clusters: {aks: {name: aks-prod, resource_group: rg-aks}}}
`

const aksCredentials = `clusters:
- cluster: {certificate-authority-data: Q0E=, server: "https://aks.hcp.azmk8s.io:443"}
  name: aks-prod
users:
- name: u
  user: {exec: {command: kubelogin, args: [get-token, --server-id, 6dae42f8-4368-4678-94ff-3960e28e3630]}}
`

func newAzureHarness(t *testing.T, exts ...string) *harness {
	t.Helper()
	h := newHarness(t, azureConfig)
	t.Setenv("AZURE_EXTENSION_DIR", "")
	for _, e := range exts {
		os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".azure", "cliextensions", e), 0o700)
	}
	h.exec.respond = func(argv []string) (string, int) {
		switch strings.Join(argv[:3], " ") {
		case "az vm list":
			return `["` + strings.Replace(azVMID, "vm-jump", argv[4][len("[?name=='"):len(argv[4])-len("'].id")], 1) + `"]`, 0
		case "az aks get-credentials":
			return aksCredentials, 0
		}
		return "", 0
	}
	mekPath = func() (string, error) { return "/bin/mek", nil }
	t.Cleanup(func() { mekPath = func() (string, error) { return "/bin/mek", nil } })
	return h
}

func TestAzureShell(t *testing.T) {
	h := newAzureHarness(t, "bastion", "ssh")
	h.mustRun("-c", "az", "-y", "shell", "jump")
	got := h.exec.argv()
	if len(got) != 2 || got[0] != "az vm list --query [?name=='vm-jump'].id --output json --resource-group rg-app" ||
		got[1] != "az network bastion ssh --name bas --resource-group rg-net --target-resource-id "+azVMID+" --auth-type AAD" {
		t.Fatalf("calls: %q", got)
	}
	if inv := h.exec.invs[1]; !slices.Contains(inv.Env, "AZURE_EXTENSION_DIR="+filepath.Join(os.Getenv("HOME"), ".azure", "cliextensions")) ||
		!slices.Contains(inv.Env, "AZURE_CONFIG_DIR="+filepath.Join(h.home, "azure", "az")) {
		t.Errorf("env: %q", inv.Env)
	}

	key := filepath.Join(h.home, "ssh", "az", "id_ed25519")
	_, err := h.run("-c", "az", "-y", "shell", "key")
	wantErr(t, err, "ssh-keygen -t ed25519 -f "+key)
	os.MkdirAll(filepath.Dir(key), 0o700)
	os.WriteFile(key, nil, 0o600)
	h.exec.invs = nil
	h.mustRun("-c", "az", "-y", "shell", "key")
	if got := h.exec.argv(); !strings.HasSuffix(got[1], "--auth-type ssh-key --username ops --ssh-key "+key) {
		t.Errorf("ssh-key shell: %q", got)
	}
	h.exec.invs = nil
	h.mustRun("-c", "az", "-y", "shell", azVMID, "--user", "me")
	if got := h.exec.argv(); len(got) != 1 { // a resource id needs no lookup
		t.Errorf("by id: %q", got)
	}
}

func TestAzureShellNeedsExtensions(t *testing.T) {
	h := newAzureHarness(t) // none installed
	_, err := h.run("-c", "az", "-y", "shell", "jump")
	wantErr(t, err, "az extension add --name bastion --name ssh")
	if got := h.exec.argv(); len(got) != 1 { // only the lookup ran
		t.Errorf("calls: %q", got)
	}
}

func TestAzureTunnels(t *testing.T) {
	h := newAzureHarness(t, "bastion")
	h.mustRun("-c", "az", "-y", "tunnel", "db")
	h.mustRun("-c", "az", "-y", "tunnel", "web")
	h.mustRun("-c", "az", "-y", "tunnel", "--to", "10.9.9.9:6379", "--local", "35502")
	got := h.exec.argv()
	want := []string{
		"az network bastion tunnel --name bas --resource-group rg-net --target-ip-address 10.1.2.3 --resource-port 5432 --port 35500",
		"az vm list --query [?name=='vm-jump'].id --output json --resource-group rg-app",
		"az network bastion tunnel --name bas --resource-group rg-net --target-resource-id " + azVMID + " --resource-port 80 --port 35501",
		"az network bastion tunnel --name bas --resource-group rg-net --target-ip-address 10.9.9.9 --resource-port 6379 --port 35502",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tunnels:\n%s", strings.Join(got, "\n"))
	}
	es := readAudit(t)
	if last := es[len(es)-1]; last.Event != "end" || last.Target != "localhost:35502 → 10.9.9.9:6379 via Bastion bas" {
		t.Errorf("audit: %+v", last)
	}
	_, err := h.run("-c", "az", "tunnel", "--to", "db.internal:5432")
	wantErr(t, err, "Bastion connects to an IP address, not a name")
}

func TestAzureKube(t *testing.T) {
	h := newAzureHarness(t)
	h.mustRun("-c", "az", "kube")
	if got := h.exec.argv(); len(got) != 1 || got[0] != "az aks get-credentials --resource-group rg-aks --name aks-prod --file - --format exec --only-show-errors" {
		t.Errorf("describe: %q", got)
	}
	kc := readFile(t, filepath.Join(h.home, "kube", "az.yaml"))
	for _, want := range []string{"server: https://aks.hcp.azmk8s.io:443", "- --location", "- 6dae42f8-4368-4678-94ff-3960e28e3630", "current-context: az/aks"} {
		if !strings.Contains(kc, want) {
			t.Errorf("kubeconfig missing %q:\n%s", want, kc)
		}
	}
	h.mustRun("--context", "az", "kube", "token", "--name", "aks-prod", "--location", "6dae42f8-4368-4678-94ff-3960e28e3630")
	if got := h.exec.argv()[1]; got != "kubelogin get-token --login azurecli --server-id 6dae42f8-4368-4678-94ff-3960e28e3630" {
		t.Errorf("token: %q", got)
	}
	h.mustRun("-c", "az", "kube", "--name", "other-aks", "--resource-group", "rg-x")
	if got := strings.Join(h.exec.argv(), "\n"); !strings.Contains(got, "--resource-group rg-x --name other-aks") {
		t.Errorf("ad hoc:\n%s", got)
	}
}
