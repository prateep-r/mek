package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

const (
	azSub  = "00000000-1111-2222-3333-444444444444"
	azSub2 = "55555555-6666-7777-8888-999999999999"
)

// azureCloud answers az's discovery commands; login logs in, in dir.
func azureCloud(t *testing.T, dir *string) func([]string) (string, int) {
	return func(argv []string) (string, int) {
		switch cmd := strings.Join(argv, " "); {
		case cmd == "az login":
			azLogin(t, *dir)
			return "", 0
		case strings.HasPrefix(cmd, "az account tenant list"):
			return `[{"id":"t-1","name":"Acme","domain":"acme.onmicrosoft.com"}]`, 0
		case strings.HasPrefix(cmd, "az account list --all --query [?tenantId=='t-1']"):
			return `[{"id":"` + azSub + `","name":"Prod","state":"Enabled"},{"id":"` + azSub2 + `","name":"Old","state":"Disabled"}]`, 0
		case strings.HasPrefix(cmd, "az network bastion list"):
			return `[{"name":"bas","group":"rg-net","sku":"Standard"}]`, 0
		case strings.HasPrefix(cmd, "az vm list"):
			return `[{"name":"vm-jump","group":"rg-app","ip":"10.2.0.4"}]`, 0
		case strings.HasPrefix(cmd, "az aks list"):
			return `[{"name":"aks-1","group":"rg-aks","location":"southeastasia"}]`, 0
		}
		return "", 1
	}
}

func azLogin(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, "azureProfile.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCtxAddAzure(t *testing.T) {
	h := newHarness(t, testConfig)
	dir := filepath.Join(config.Dir(), "azure", ".wizard")
	h.exec.respond = azureCloud(t, &dir)
	screens := answering(t, []string{"type:az1", "enter", "down", "down", "enter", // Azure
		"enter",         // log in
		"enter",         // Acme
		"down", "enter", // Prod (Old, disabled, sorts first)
		"down", "enter", // bas
		"enter", "enter", "enter"}) // no region, normal, Save
	h.mustRun("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "✓ Tenant: Acme") || !strings.Contains(s, "✓ Subscription: Prod") {
		t.Errorf("answers:\n%s", s)
	}
	got := readFile(t, config.Path())
	if !strings.Contains(got, "az1:\n    provider: azure\n    tenant_id: t-1\n    subscription_id: "+azSub+"\n    bastion:\n      name: bas\n      resource_group: rg-net\n") {
		t.Errorf("file:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(config.Dir(), "azure", "az1", "azureProfile.json")); err != nil {
		t.Errorf("the login didn't move to the new context: %v", err)
	}
	for _, inv := range h.exec.invs {
		if !slices.Contains(inv.Env, "AZURE_CORE_LOGIN_EXPERIENCE_V2=off") {
			t.Errorf("%v ran without az's login picker off", inv.Argv)
		}
	}

	// Edit: its own login; renamed, the login goes with it.
	h.exec.invs = nil
	answering(t, []string{"ctrl+u", "type:az2", "enter", "enter", "enter", "enter", "enter", "type:southeastasia", "enter", "enter", "enter"})
	h.mustRun("ctx", "edit", "az1")
	if _, err := os.Stat(filepath.Join(config.Dir(), "azure", "az2", "azureProfile.json")); err != nil {
		t.Errorf("the login didn't follow the rename: %v", err)
	}
	if got := readFile(t, config.Path()); !strings.Contains(got, "region: southeastasia") || !strings.Contains(got, "name: bas") {
		t.Errorf("edit:\n%s", got)
	}
	if strings.Contains(strings.Join(h.exec.argv(), "\n"), "az login") {
		t.Error("logged in again, editing a logged-in context")
	}

	// Another: az2's login lists, and stays az2's.
	answering(t, []string{"type:az3", "enter", "down", "down", "enter", "enter", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("ctx", "add")
	if _, err := os.Stat(filepath.Join(config.Dir(), "azure", "az2", "azureProfile.json")); err != nil {
		t.Errorf("az2's login moved: %v", err)
	}
	if got := readFile(t, config.Path()); !strings.Contains(got, "az3:\n    provider: azure\n    tenant_id: t-1\n    subscription_id: "+azSub2+"\n") {
		t.Errorf("az3:\n%s", got)
	}
}

func TestCtxAddAzureLookupsFail(t *testing.T) {
	h := newHarness(t, testConfig)
	h.exec.code = 1
	screens := answering(t, []string{"type:z", "enter", "down", "down", "enter",
		"down", "enter", // skip the login
		"type:acme.onmicrosoft.com", "enter",
		"type:nope", "enter", "ctrl+u", "type:"+azSub, "enter",
		"type:rg-net/bas", "enter"})
	h.run("ctx", "add")
	s := (*screens)[0]
	for _, want := range []string{"✓ Log in to Azure: skipped", "✓ Tenant: acme.onmicrosoft.com",
		"✓ Subscription: " + azSub, "✓ Bastion: rg-net/bas"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
}

const azureEntryConfig = `contexts:
  az: {provider: azure, tenant_id: t-1, subscription_id: "` + azSub + `", bastion: {name: bas, resource_group: rg-net}}
  bare: {provider: azure, tenant_id: t-1, subscription_id: "` + azSub + `"}
`

func TestAzureEntryWizards(t *testing.T) {
	h := newHarness(t, azureEntryConfig)
	dir := ""
	h.exec.respond = azureCloud(t, &dir)

	// A target: Entra ID login, the context's Bastion.
	answering(t, []string{"enter", "enter", "enter", "enter", "enter"}) // vm-jump, rg-app, Entra ID, name vm-jump, Save
	h.mustRun("-c", "az", "ctx", "target", "add")
	got := readFile(t, config.Path())
	if !strings.Contains(got, "vm-jump:\n        instance: vm-jump\n        resource_group: rg-app\n") {
		t.Errorf("target:\n%s", got)
	}
	// With a key, on a context with no Bastion: the target's own.
	answering(t, []string{"enter", "enter", "down", "enter", "type:ops", "enter", "enter", "ctrl+u", "type:jump", "enter", "enter"})
	h.mustRun("-c", "bare", "ctx", "target", "add")
	got = readFile(t, config.Path())
	if !strings.Contains(got, "jump:\n        instance: vm-jump\n        resource_group: rg-app\n        user: ops\n        auth: ssh-key\n        bastion:\n          name: bas\n          resource_group: rg-net\n") {
		t.Errorf("target with a key:\n%s", got)
	}
	// Edited back to Entra ID: the user goes.
	answering(t, []string{"enter", "enter", "up", "enter", "enter", "enter", "enter"})
	h.mustRun("-c", "bare", "ctx", "target", "edit", "jump")
	if got = readFile(t, config.Path()); strings.Contains(got, "user: ops") || !strings.Contains(got, "name: bas") {
		t.Errorf("edited:\n%s", got)
	}

	// A cluster: its resource group.
	answering(t, []string{"enter", "enter", "enter", "enter", "enter"})
	h.mustRun("-c", "az", "ctx", "cluster", "add")
	if got = readFile(t, config.Path()); !strings.Contains(got, "aks-1:\n        name: aks-1\n        resource_group: rg-aks\n") {
		t.Errorf("cluster:\n%s", got)
	}
	answering(t, []string{"enter", "enter", "enter", "enter", "enter"})
	h.mustRun("-c", "az", "ctx", "cluster", "edit", "aks-1")

	// Tunnels: an IP through Bastion (no VM), and a port on a VM.
	port := freePort(t)
	screens := answering(t, []string{"enter", "type:db.internal", "enter", "ctrl+u", "type:10.3.0.5", "enter", "type:5432", "enter",
		"ctrl+u", "type:" + port, "enter", "type:db", "enter", "enter"})
	h.mustRun("-c", "az", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "✓ Forward to: an IP, through Bastion") || strings.Contains(s, "Through") {
		t.Errorf("ip tunnel:\n%s", s)
	}
	if got = readFile(t, config.Path()); !strings.Contains(got, "db:\n        host: 10.3.0.5\n        port: 5432\n        local_port: "+port+"\n") {
		t.Errorf("ip tunnel:\n%s", got)
	}
	answering(t, []string{"down", "enter", "down", "enter", "type:8080", "enter", "enter", "type:web", "enter", "enter"}) // port 8080 on vm-jump
	h.mustRun("-c", "az", "ctx", "tunnel", "add")
	if got = readFile(t, config.Path()); !strings.Contains(got, "web:\n        via: vm-jump\n        port: 8080\n") {
		t.Errorf("port tunnel:\n%s", got)
	}
	screens = answering(t, []string{"down"})
	h.run("-c", "az", "ctx", "tunnel", "edit") // the list to pick from
	if s := (*screens)[0]; !strings.Contains(s, "localhost:"+port+" → 10.3.0.5:5432 via Bastion") {
		t.Errorf("pick:\n%s", s)
	}

	// No Bastion lookup: typed.
	h.exec.respond = func(argv []string) (string, int) {
		if strings.Contains(strings.Join(argv, " "), "bastion") {
			return "", 1
		}
		return azureCloud(t, &dir)(argv)
	}
	screens = answering(t, []string{"enter", "enter", "enter", "enter"})
	h.run("-c", "bare", "ctx", "target", "add")
	if s := (*screens)[0]; !strings.Contains(s, "couldn't list them: az network bastion") {
		t.Errorf("bastion lookup:\n%s", s)
	}
}

func TestCtxAddAzureLogin(t *testing.T) {
	h := newHarness(t, testConfig)
	screens := answering(t, []string{"type:z", "enter", "down", "down", "enter"}, // stops at the login
		[]string{"type:z", "enter", "down", "down", "enter", "enter"}) // logs in: it fails
	h.run("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "runs: az login  (a browser opens)") {
		t.Errorf("login step:\n%s", s)
	}
	os.WriteFile(filepath.Join(config.Dir(), "azure"), nil, 0o600) // no directory for the login
	h.run("ctx", "add")
	if s := (*screens)[1]; !strings.Contains(s, "not a directory") {
		t.Errorf("login dir:\n%s", s)
	}
	os.Remove(filepath.Join(config.Dir(), "azure"))

	// No home for az's extensions: nothing runs, each step says why.
	t.Setenv("HOME", "")
	t.Setenv("AZURE_EXTENSION_DIR", "")
	screens = answering(t, []string{"type:z", "enter", "down", "down", "enter", "enter", "type:t", "enter", "type:" + azSub, "enter", "enter"},
		[]string{"type:z", "enter", "down", "down", "enter", "down", "enter", "type:t", "enter", "type:" + azSub, "enter"})
	h.run("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "$HOME is not defined") {
		t.Errorf("no home:\n%s", s)
	}
	h.run("ctx", "add")
	if s := (*screens)[1]; !strings.Contains(s, "couldn't list them: $HOME is not defined") {
		t.Errorf("no home, lookups:\n%s", s)
	}
}
