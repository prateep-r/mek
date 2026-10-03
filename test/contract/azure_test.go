//go:build contract

package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// azureExtensions finds the user's az extensions dir with bastion and ssh
// installed (install-clis.sh adds them), or skips.
func azureExtensions(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("AZURE_EXTENSION_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".azure", "cliextensions")
	}
	for _, x := range []string{"bastion", "ssh"} {
		if _, err := os.Stat(filepath.Join(dir, x)); err != nil {
			if os.Getenv("MEK_CONTRACT_REQUIRE") != "" {
				t.Fatalf("az extension %s is not installed in %s", x, dir)
			}
			t.Skipf("az extension %s is not installed", x)
		}
	}
	return dir
}

// The real az parses every Bastion and AKS command mek builds: with no
// login it gets as far as asking for one, instead of rejecting an argument.
func TestAzureCommandsParseWithRealAZ(t *testing.T) {
	az := realCLI(t, "az")
	ext := azureExtensions(t)
	u := newUser(t, `contexts:
  az: {provider: azure, tenant_id: contoso.onmicrosoft.com, subscription_id: 00000000-1111-2222-3333-444444444444,
       bastion: {name: bas, resource_group: rg-net},
       targets: {jump: {instance: "/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm-jump"},
                 key: {instance: "/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm-key", auth: ssh-key, user: ops}},
       tunnels: {db: {host: 10.1.2.3, port: 5432, local_port: 35700}, web: {via: jump, port: 80, local_port: 35701}},
       clusters: {aks: {name: aks-prod, resource_group: rg-aks}}}
`, []string{az}, "AZURE_EXTENSION_DIR="+ext, "AZURE_CORE_COLLECT_TELEMETRY=no")
	key := filepath.Join(u.mekHome, "ssh", "az", "id_ed25519")
	os.MkdirAll(filepath.Dir(key), 0o700)
	os.WriteFile(key, []byte("not a real key"), 0o600)

	for _, args := range [][]string{
		{"shell", "jump"},
		{"shell", "key"},
		{"tunnel", "db"},
		{"tunnel", "web"},
		{"kube"},
	} {
		out, code := u.run(append([]string{"-c", "az"}, args...)...)
		if code == 0 || !strings.Contains(out, "az login") {
			t.Errorf("mek %s: want az asking for a login (exit %d):\n%s", strings.Join(args, " "), code, out)
		}
		for _, bad := range []string{"unrecognized arguments", "the following arguments are required", "is not in the", "invalid choice"} {
			if strings.Contains(out, bad) {
				t.Errorf("mek %s: az rejected the command (%s):\n%s", strings.Join(args, " "), bad, out)
			}
		}
	}
	u.homeUntouched(".azure", ".ssh")
}
