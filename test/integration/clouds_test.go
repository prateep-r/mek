//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/prateep-r/mek/test/testkit"
)

// cloud describes one supported cloud for tests that must cover them all.
type cloud struct {
	name, cli                string
	ctx                      string // fields of a context for this cloud (YAML flow mapping body)
	read, write, destructive []string
	secret                   []string // a command carrying the secret value "S3cret-<name>"
}

var clouds = []cloud{
	{name: "aws", cli: "aws",
		ctx:         `provider: aws, sso_start_url: "https://o.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Dev`,
		read:        []string{"ec2", "describe-instances"},
		write:       []string{"ec2", "run-instances", "--image-id", "ami-1"},
		destructive: []string{"ec2", "terminate-instances", "--instance-ids", "i-1"},
		secret:      []string{"rds", "modify-db-instance", "--master-user-password=S3cret-aws"}},
	{name: "gcp", cli: "gcloud",
		ctx:         `provider: gcp, project: my-project`,
		read:        []string{"compute", "instances", "list"},
		write:       []string{"compute", "instances", "create", "vm-1"},
		destructive: []string{"compute", "instances", "delete", "vm-1"},
		secret:      []string{"sql", "users", "set-password", "root", "--instance=db", "--password=S3cret-gcp"}},
	{name: "azure", cli: "az",
		ctx:         `provider: azure, tenant_id: contoso.onmicrosoft.com, subscription_id: 00000000-1111-2222-3333-444444444444`,
		read:        []string{"vm", "list"},
		write:       []string{"vm", "create", "-n", "vm-1", "--image", "Ubuntu2204"},
		destructive: []string{"vm", "delete", "-n", "vm-1", "--yes"},
		secret:      []string{"login", "--service-principal", "-u", "app", "-p", "S3cret-azure", "--tenant", "t"}},
	{name: "huawei", cli: "hcloud",
		ctx:         `provider: huawei, hcloud_profile: sso-prod, region: ap-southeast-2`,
		read:        []string{"ECS", "ListServersDetails"},
		write:       []string{"ECS", "CreateServers", "--cli-jsonInput=server.json"},
		destructive: []string{"ECS", "DeleteServers", "--servers.1.id=x"},
		secret:      []string{"obs", "config", "-i=AK", "-k=S3cret-huawei"}},
}

// allClouds is a config with, per cloud, a plain, a protected and a readonly context.
func allClouds() string {
	var b strings.Builder
	b.WriteString("contexts:\n")
	for _, c := range clouds {
		fmt.Fprintf(&b, "  %s: {%s}\n  %s-prod: {%s, protected: true}\n  %s-ro: {%s, readonly: true}\n",
			c.name, c.ctx, c.name, c.ctx, c.name, c.ctx)
	}
	return b.String()
}

func cmdline(ctx string, flags []string, cli string, args []string) []string {
	out := append([]string{"-c", ctx}, flags...)
	return append(append(out, cli), args...)
}

// The guard behaves the same on every cloud: readonly blocks writes,
// protected asks (and fails closed without a terminal), flags confirm.
func TestGuardEveryCloud(t *testing.T) {
	for _, c := range clouds {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, allClouds())
			steps := []struct {
				ctx      string
				flags    []string
				args     []string
				code     int
				decision string
			}{
				{c.name + "-ro", nil, c.read, 0, "allowed"},
				{c.name + "-ro", []string{"-y"}, c.write, 1, "blocked"},
				{c.name + "-ro", []string{"--confirm", c.name + "-ro"}, c.destructive, 1, "blocked"},
				{c.name + "-prod", nil, c.read, 0, "allowed"},
				{c.name + "-prod", nil, c.write, 1, "blocked"}, // no terminal to ask
				{c.name + "-prod", []string{"-y"}, c.write, 0, "confirmed"},
				{c.name + "-prod", []string{"-y"}, c.destructive, 1, "blocked"}, // --yes is not enough
				{c.name + "-prod", []string{"--confirm", c.name + "-prod"}, c.destructive, 0, "confirmed"},
				{c.name, nil, c.destructive, 0, "allowed"}, // unguarded context
			}
			ran := 0
			for _, s := range steps {
				r := e.run(cmdline(s.ctx, s.flags, c.cli, s.args)...)
				if r.Code != s.code {
					t.Errorf("%s %v: exit %d, want %d (%s)", s.ctx, s.args, r.Code, s.code, r.Stderr)
				}
				if s.code == 0 {
					ran++
				}
			}
			if got := len(e.calls()); got != ran {
				t.Errorf("%s ran %d times, want %d", c.cli, got, ran)
			}
			entries := e.audit()
			if len(entries) != len(steps) {
				t.Fatalf("audit has %d entries, want %d", len(entries), len(steps))
			}
			for i, s := range steps {
				if entries[i]["decision"] != s.decision || entries[i]["provider"] != c.name {
					t.Errorf("audit[%d] = %v/%v, want %s/%s", i, entries[i]["provider"], entries[i]["decision"], c.name, s.decision)
				}
			}
		})
	}
}

// Secrets passed on the command line reach the CLI but never the audit log.
func TestAuditMasksSecretsEveryCloud(t *testing.T) {
	e := setup(t, allClouds())
	for _, c := range clouds {
		ok(t, e.run(cmdline(c.name, nil, c.cli, c.secret)...))
	}
	audit, _ := os.ReadFile(filepath.Join(e.mekHome, "audit.jsonl"))
	calls := e.calls()
	for i, c := range clouds {
		secret := "S3cret-" + c.name
		if strings.Contains(string(audit), secret) {
			t.Errorf("%s: audit log contains %q", c.name, secret)
		}
		if !strings.Contains(calls[i].ArgLine(), secret) {
			t.Errorf("%s: the CLI did not get the real value: %s", c.name, calls[i].ArgLine())
		}
	}
}

// Many mek processes on different clouds at once: each CLI call must get its
// own context's environment, never another one's.
func TestConcurrentAcrossClouds(t *testing.T) {
	e := setup(t, allClouds())

	const perCloud = 10
	var wg sync.WaitGroup
	errs := make(chan string, perCloud*len(clouds))
	for _, c := range clouds {
		for range perCloud {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r := testkit.Run(t, mek, e.vars, cmdline(c.name, nil, c.cli, c.read)...); r.Code != 0 {
					errs <- c.name + ": " + r.Stderr
				}
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// What identifies each cloud's context in its CLI's environment.
	marker := map[string]func(map[string]string) bool{
		"aws": func(env map[string]string) bool { return env["AWS_PROFILE"] == "mek-aws" },
		"gcloud": func(env map[string]string) bool {
			return env["CLOUDSDK_CONFIG"] == filepath.Join(e.mekHome, "gcloud", "gcp")
		},
		"az": func(env map[string]string) bool {
			return env["AZURE_CONFIG_DIR"] == filepath.Join(e.mekHome, "azure", "azure")
		},
		"hcloud": func(env map[string]string) bool { return env["HW_PROFILE"] == "sso-prod" },
	}
	ctxFor := map[string]string{"aws": "aws", "gcloud": "gcp", "az": "azure", "hcloud": "huawei"}
	calls := e.calls()
	if len(calls) != perCloud*len(clouds) {
		t.Fatalf("%d calls, want %d", len(calls), perCloud*len(clouds))
	}
	counts := map[string]int{}
	for _, call := range calls {
		counts[call.Name]++
		if call.Env["MEK_CONTEXT"] != ctxFor[call.Name] || !marker[call.Name](call.Env) {
			t.Errorf("%s got another context's environment: MEK_CONTEXT=%s", call.Name, call.Env["MEK_CONTEXT"])
		}
	}
	for cli, n := range counts {
		if n != perCloud {
			t.Errorf("%s ran %d times, want %d", cli, n, perCloud)
		}
	}
	entries := e.audit()
	if len(entries) != perCloud*len(clouds) {
		t.Errorf("audit has %d entries, want %d (lost or merged lines)", len(entries), perCloud*len(clouds))
	}
}
