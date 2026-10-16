//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const azureAccessConfig = `contexts:
  az: {provider: azure, tenant_id: t, subscription_id: 00000000-1111-2222-3333-444444444444,
       bastion: {name: bas, resource_group: rg-net},
       targets: {jump: {instance: "/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm-jump"}},
       tunnels: {db: {host: 10.1.2.3, port: 5432, local_port: 35600}}}
  az-ro: {provider: azure, tenant_id: t, subscription_id: 00000000-1111-2222-3333-444444444444, readonly: true,
          bastion: {name: bas, resource_group: rg-net}, tunnels: {db: {host: 10.1.2.3, port: 5432, local_port: 35601}}}
`

// azureSetup is a user with the bastion and ssh extensions in ~/.azure.
func azureSetup(t *testing.T, exts ...string) *env {
	t.Helper()
	e := setup(t, azureAccessConfig)
	for _, x := range exts {
		os.MkdirAll(filepath.Join(e.home, ".azure", "cliextensions", x), 0o700)
	}
	return e
}

func TestAzureBastionShellAndTunnel(t *testing.T) {
	e := azureSetup(t, "bastion", "ssh")
	if r := e.run("-c", "az", "shell", "jump"); r.Code != 0 {
		t.Fatalf("shell: %+v", r)
	}
	if r := e.run("-c", "az", "tunnel", "db"); r.Code != 0 {
		t.Fatalf("tunnel: %+v", r)
	}
	calls := e.calls()
	if len(calls) != 2 ||
		calls[0].ArgLine() != "network bastion ssh --name bas --resource-group rg-net --target-resource-id /subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm-jump --auth-type AAD" ||
		calls[1].ArgLine() != "network bastion tunnel --name bas --resource-group rg-net --target-ip-address 10.1.2.3 --resource-port 5432 --port 35600" {
		t.Fatalf("calls: %+v", calls)
	}
	// The context's own config dir, the user's extensions.
	for _, c := range calls {
		if c.Env["AZURE_CONFIG_DIR"] != filepath.Join(e.mekHome, "azure", "az") || c.Env["AZURE_EXTENSION_DIR"] != filepath.Join(e.home, ".azure", "cliextensions") {
			t.Errorf("env: %v", c.Env)
		}
	}
	a := e.audit()
	if len(a) != 4 || a[0]["class"] != "shell" || a[2]["class"] != "tunnel" || a[3]["event"] != "end" {
		t.Errorf("audit: %v", a)
	}
	// Readonly: tunnels yes, shells no.
	if r := e.run("-c", "az-ro", "tunnel", "db"); r.Code != 0 {
		t.Errorf("readonly tunnel: %+v", r)
	}
}

func TestAzureMissingExtensions(t *testing.T) {
	e := azureSetup(t) // none
	r := e.run("-c", "az", "shell", "jump")
	if r.Code == 0 || !strings.Contains(r.Stderr, "az extension add --name bastion --name ssh") || len(e.calls()) != 0 {
		t.Errorf("missing extensions: %+v", r)
	}
}
