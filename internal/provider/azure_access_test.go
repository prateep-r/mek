package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

const vmID = "/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/rg-app/providers/Microsoft.Compute/virtualMachines/vm-jump"

// azureHome gives the az extensions dir its own temp home, with exts installed.
func azureHome(t *testing.T, exts ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("AZURE_EXTENSION_DIR", "")
	old := userHome
	userHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHome = old })
	for _, e := range exts {
		os.MkdirAll(filepath.Join(home, ".azure", "cliextensions", e), 0o700)
	}
	return home
}

func TestValidateAzureAccess(t *testing.T) {
	ctx := "contexts:\n  a:\n    provider: azure\n    tenant_id: t\n    subscription_id: 00000000-1111-2222-3333-444444444444\n"
	b := "    bastion: {name: bas, resource_group: rg-net}\n"
	cases := []struct{ in, want string }{
		{ctx + "    clusters: {aks: {name: x, region: r, resource_group: g}}\n", "azure clusters take resource_group, not region or location"},
		{ctx + "    clusters: {aks: {name: x}}\n", "clusters.aks needs a resource_group"},
		{ctx + b + "    targets: {vm: {instance: x, zone: z}}\n", "azure targets take resource_group, not zone"},
		{ctx + b + "    targets: {vm: {instance: \"bad name!\"}}\n", "is not a VM name or resource id"},
		{ctx + "    targets: {vm: {instance: x}}\n", "targets.vm: set bastion on the target or the context"},
		{ctx + b + "    tunnels: {db: {via: \"bad name!\", port: 1}}\n", "is not a target name, VM name or resource id"},
		{ctx + "    tunnels: {db: {via: vm-x, port: 1}}\n", "tunnels.db: set the context's bastion"},
		{ctx + b + "    tunnels: {db: {via: vm-x, host: 10.0.0.4, port: 1}}\n", "drop via, keep host"},
		{ctx + b + "    tunnels: {db: {host: db.internal, port: 1}}\n", "Bastion connects to an IP address, not a name"},
		{ctx + "    tunnels: {db: {host: 10.0.0.4, port: 1}}\n", "tunnels to a host need the context's bastion"},
		{ctx + "    tunnels: {db: {cloudsql: \"p:r:i\", local_port: 1}}\n", "cloudsql tunnels are gcp only"},
		{"contexts:\n  a:\n    provider: aws\n    aws_profile: p\n    bastion: {name: b, resource_group: g}\n", "bastion is for azure contexts"},
		{"contexts:\n  a:\n    provider: gcp\n    project: p\n    targets: {vm: {instance: vm-1, auth: aad}}\n", "resource_group, auth and bastion are for azure"},
		{"contexts:\n  a:\n    provider: aws\n    aws_profile: p\n    region: r\n    clusters: {m: {name: x, resource_group: g}}\n", "clusters.m: resource_group is for azure"},
		{"contexts:\n  a:\n    provider: aws\n    aws_profile: p\n    tunnels: {db: {host: 10.0.0.4, port: 1}}\n", "needs via: the instance to go through"},
		{"contexts:\n  a:\n    provider: gcp\n    project: p\n    tunnels: {db: {host: 10.0.0.4, port: 1}}\n", "needs via: the VM to go through"},
	}
	for _, c := range cases {
		if err := parse(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	for _, ok := range []string{
		ctx + b + "    targets: {jump: {instance: vm-jump, resource_group: rg-app}, byid: {instance: \"" + vmID + "\", auth: ssh-key, user: ops}}\n" +
			"    tunnels: {db: {host: 10.1.2.3, port: 5432}, web: {via: jump, port: 80}, raw: {via: vm-x, port: 22}}\n    clusters: {aks: {name: aks-prod, resource_group: rg-aks}}\n",
		ctx + "    targets: {own: {instance: vm-1, bastion: {name: b, resource_group: g}}}\n    tunnels: {web: {via: own, port: 80}}\n",
	} {
		if err := parse(ok); err != nil {
			t.Errorf("parse(%q) should be valid: %v", ok, err)
		}
	}
	kl, _ := FindPlugin("kubelogin")
	if kl.Needed(&config.Context{}) || !kl.Needed(&config.Context{Clusters: map[string]*config.Cluster{"a": {}}}) {
		t.Error("kubelogin is needed only with clusters")
	}
}

func TestAzureResolveTarget(t *testing.T) {
	z := &Azure{ctx: &config.Context{Name: "az", Provider: config.ProviderAzure, Targets: map[string]*config.Target{
		"jump": {Instance: "vm-jump", ResourceGroup: "rg-app", Auth: "ssh-key", User: "ops", Bastion: &config.Bastion{Name: "b2", ResourceGroup: "g2"}},
		"byid": {Instance: vmID},
	}}}
	var argv []string
	q := fakeQuery(`["`+vmID+`"]`, nil, &argv)
	got, err := z.ResolveTarget("jump", TargetOptions{}, q)
	if err != nil || got.ID != vmID || got.Auth != "ssh-key" || got.User != "ops" || got.Bastion.Name != "b2" || got.Alias != "jump" ||
		strings.Join(argv, " ") != "az vm list --query [?name=='vm-jump'].id --output json --resource-group rg-app" {
		t.Errorf("configured: %+v %v %q", got, err, argv)
	}
	argv = nil
	if got, _ := z.ResolveTarget("byid", TargetOptions{User: "me"}, q); got.ID != vmID || got.User != "me" || argv != nil {
		t.Errorf("resource id needs no lookup: %+v %q", got, argv)
	}
	if got, _ := z.ResolveTarget("vm-jump", TargetOptions{}, q); got.ID != vmID || strings.Contains(strings.Join(argv, " "), "--resource-group") {
		t.Errorf("subscription-wide lookup: %+v %q", got, argv)
	}
	boom := errors.New("boom")
	for _, c := range []struct {
		spec string
		o    TargetOptions
		q    Query
		want string
	}{
		{"vm-1", TargetOptions{Zone: "z"}, q, "--zone is for gcp"},
		{"bad name!", TargetOptions{}, q, `unknown target "bad name!" — use a name under targets:, a VM resource id, or a VM name`},
		{"vm-1", TargetOptions{}, fakeQuery(`[]`, nil, &argv), "VM vm-1: 0 found () — pass --resource-group"},
		{"vm-1", TargetOptions{}, fakeQuery(`["a","b"]`, nil, &argv), "VM vm-1: 2 found (a, b)"},
		{"vm-1", TargetOptions{}, fakeQuery(`{`, nil, &argv), "vm list"},
		{"vm-1", TargetOptions{}, fakeQuery("", boom, &argv), "boom"},
	} {
		if _, err := z.ResolveTarget(c.spec, c.o, c.q); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.spec, err, c.want)
		}
	}
}

func TestAzureShell(t *testing.T) {
	dir := t.TempDir()
	z := &Azure{dir: dir, ctx: &config.Context{Name: "az", Provider: config.ProviderAzure, Bastion: &config.Bastion{Name: "bas", ResourceGroup: "rg-net"}}}

	azureHome(t)
	if _, err := z.ShellCommand(Instance{ID: vmID}); err == nil || err.Error() != "az extension bastion and ssh is needed — install it: az extension add --name bastion --name ssh" {
		t.Errorf("no extensions: %v", err)
	}
	azureHome(t, "bastion", "ssh")
	c, err := z.ShellCommand(Instance{ID: vmID})
	if err != nil || strings.Join(c.Argv, " ") != "az network bastion ssh --name bas --resource-group rg-net --target-resource-id "+vmID+" --auth-type AAD" {
		t.Errorf("aad: %+v %v", c, err)
	}

	// ssh-key: the context's key, which the user creates and puts on the VM.
	key := filepath.Join(dir, "ssh", "az", "id_ed25519")
	in := Instance{ID: vmID, Auth: config.AuthSSHKey, User: "ops", Bastion: &config.Bastion{Name: "b2", ResourceGroup: "g2"}}
	if _, err := z.ShellCommand(in); err == nil || !strings.Contains(err.Error(), "ssh-keygen -t ed25519 -f "+key) {
		t.Errorf("missing key: %v", err)
	}
	os.MkdirAll(filepath.Dir(key), 0o700)
	os.WriteFile(key, nil, 0o600)
	c, err = z.ShellCommand(in)
	if err != nil || strings.Join(c.Argv, " ") != "az network bastion ssh --name b2 --resource-group g2 --target-resource-id "+vmID+" --auth-type ssh-key --username ops --ssh-key "+key {
		t.Errorf("ssh-key: %+v %v", c, err)
	}
	azureHome(t, "ssh") // ssh-key needs only the bastion extension
	if _, err := z.ShellCommand(in); err == nil || !strings.Contains(err.Error(), "az extension add --name bastion") {
		t.Errorf("ssh-key without bastion ext: %v", err)
	}

	z.ctx.Bastion = nil
	if _, err := z.ShellCommand(Instance{ID: vmID}); err == nil || !strings.Contains(err.Error(), "set bastion: {name, resource_group} on context az") {
		t.Errorf("no bastion: %v", err)
	}
	t.Setenv("AZURE_EXTENSION_DIR", "")
	userHome = func() (string, error) { return "", errors.New("no home") }
	if err := needExtensions("bastion"); err == nil {
		t.Error("home error")
	}
	t.Setenv("AZURE_EXTENSION_DIR", "/opt/az-ext")
	if d, _ := extensionDir(); d != "/opt/az-ext" {
		t.Errorf("AZURE_EXTENSION_DIR: %s", d)
	}
}

func TestAzureTunnels(t *testing.T) {
	azureHome(t, "bastion")
	z := &Azure{ctx: &config.Context{Name: "az", Provider: config.ProviderAzure, Bastion: &config.Bastion{Name: "bas", ResourceGroup: "rg-net"}}}

	m, _ := z.TunnelMethod(config.Tunnel{Via: "jump", Port: 80})
	c, err := m.Command(Instance{ID: vmID}, 10080)
	if err != nil || m.Class() != guard.Tunnel || m.Via() != "jump" ||
		strings.Join(c.Argv, " ") != "az network bastion tunnel --name bas --resource-group rg-net --target-resource-id "+vmID+" --resource-port 80 --port 10080" {
		t.Errorf("vm port: %+v %v", c, err)
	}
	m, _ = z.TunnelMethod(config.Tunnel{Host: "10.1.2.3", Port: 5432})
	c, err = m.Command(Instance{}, 15432)
	if err != nil || m.Via() != "" || m.Class() != guard.Tunnel || m.Remote() != "10.1.2.3:5432 via Bastion bas" ||
		strings.Join(c.Argv, " ") != "az network bastion tunnel --name bas --resource-group rg-net --target-ip-address 10.1.2.3 --resource-port 5432 --port 15432" {
		t.Errorf("ip: %+v %v", c, err)
	}

	azureHome(t)
	if _, err := m.Command(Instance{}, 1); err == nil {
		t.Error("ip tunnel without the extension")
	}
	m, _ = z.TunnelMethod(config.Tunnel{Via: "jump", Port: 80})
	if _, err := m.Command(Instance{ID: vmID}, 1); err == nil {
		t.Error("vm tunnel without the extension")
	}
	z.ctx.Bastion = nil
	if _, err := m.Command(Instance{ID: vmID}, 1); err == nil || !strings.Contains(err.Error(), "Azure Bastion") {
		t.Errorf("vm tunnel without a bastion: %v", err)
	}
}

func TestAzurePrepareSharesExtensions(t *testing.T) {
	home := azureHome(t)
	z := &Azure{dir: t.TempDir(), ctx: &config.Context{Name: "az", TenantID: "t", SubscriptionID: "s"}}
	env, err := z.Prepare()
	if err != nil || env.Set["AZURE_EXTENSION_DIR"] != filepath.Join(home, ".azure", "cliextensions") {
		t.Errorf("extension dir: %v %v", env.Set, err)
	}
	userHome = func() (string, error) { return "", errors.New("no home") }
	if _, err := z.Prepare(); err == nil {
		t.Error("home error")
	}
}
