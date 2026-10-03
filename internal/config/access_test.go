package config

import (
	"strings"
	"testing"
)

func TestValidateClusters(t *testing.T) {
	ctx := "contexts:\n  a:\n    provider: aws\n    aws_profile: p\n    clusters:\n"
	cases := []struct{ in, want string }{
		{"      a b: {name: x}\n", "may only contain"},
		{"      token: {name: x}\n", "reserved"},
		{"      main: {region: r}\n", "needs a name"},
		{"      main:\n", "needs a name"},
		{"      main: {name: --kubeconfig=/x}\n", "clusters.main.name must not start with '-'"},
		{"      main: {name: x, namespace: \"a\\tb\"}\n", "clusters.main.namespace must not contain control"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(ctx + c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	cfg, err := Parse([]byte(ctx + "      main: {name: prod-eks, region: ap-southeast-1, namespace: app}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := *cfg.Contexts["a"].Clusters["main"]; got != (Cluster{Name: "prod-eks", Region: "ap-southeast-1", Namespace: "app"}) {
		t.Errorf("cluster: %+v", got)
	}
}

func TestValidateTargets(t *testing.T) {
	ctx := "contexts:\n  a:\n    provider: gcp\n    project: p\n    targets:\n"
	cases := []struct{ in, want string }{
		{"      a b: {instance: x}\n", "targets: name \"a b\""},
		{"      ls: {instance: x}\n", "reserved"},
		{"      stop: {instance: x}\n", "reserved"},
		{"      vm: {zone: z}\n", "targets.vm needs an instance"},
		{"      vm:\n", "targets.vm needs an instance"},
		{"      vm: {instance: -oProxyCommand=x}\n", "targets.vm.instance must not start with '-'"},
		{"      vm: {instance: x, user: \"-l\"}\n", "targets.vm.user must not start"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(ctx + c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	cfg, err := Parse([]byte(ctx + "      vm: {instance: vm-1, zone: asia-southeast1-a, user: ops}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := *cfg.Contexts["a"].Targets["vm"]; got != (Target{Instance: "vm-1", Zone: "asia-southeast1-a", User: "ops"}) {
		t.Errorf("target: %+v", got)
	}
}

func TestValidateTunnels(t *testing.T) {
	ctx := "contexts:\n  a:\n    provider: gcp\n    project: p\n    tunnels:\n"
	cases := []struct{ in, want string }{
		{"      ls: {via: x, port: 1}\n", "reserved"},
		{"      db:\n", "tunnels.db is empty"},
		{"      db: {port: 5432}\n", "tunnels.db: needs via (a target), host or cloudsql"},
		{"      db: {via: x}\n", "port 0 is not a port"},
		{"      db: {via: x, port: 70000}\n", "port 70000 is not a port"},
		{"      db: {via: x, port: 60000}\n", "port 60000 + 10000 is not a port: set local_port"},
		{"      db: {via: x, port: 1, local_port: -1}\n", "local_port -1 is not a port"},
		{"      db: {via: x, host: \"-oProxyCommand=x\", port: 1}\n", "tunnels.db: host must not start with '-'"},
		{"      db: {via: x, port: 1, private_ip: true}\n", "private_ip is only for cloudsql"},
		{"      db: {cloudsql: \"p:r:i\"}\n", "cloudsql tunnels need local_port"},
		{"      db: {cloudsql: \"p:r:i\", via: x, local_port: 1}\n", "cloudsql tunnels take only local_port and private_ip"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(ctx + c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	if _, err := Parse([]byte(ctx + "      db: {via: vm-1, host: 10.0.0.5, port: 5432}\n      sql: {cloudsql: \"p:r:i\", local_port: 15432, private_ip: true}\n      big: {via: vm-1, port: 60000, local_port: 6000}\n")); err != nil {
		t.Errorf("valid: %v", err)
	}
	if got := (&Tunnel{Port: 5432}).Local(); got != 15432 {
		t.Errorf("default local: %d", got)
	}
	if got := (&Tunnel{Port: 5432, LocalPort: 5433}).Local(); got != 5433 {
		t.Errorf("local_port: %d", got)
	}
}

func TestValidateAzureFields(t *testing.T) {
	ctx := "contexts:\n  a:\n    provider: azure\n    tenant_id: t\n    subscription_id: s\n"
	cases := []struct{ in, want string }{
		{"    bastion: {name: b}\n", "bastion needs name and resource_group"},
		{"    bastion: {name: b, resource_group: \"-rg\"}\n", "bastion.resource_group must not start"},
		{"    targets: {vm: {instance: x, auth: password}}\n", "targets.vm.auth must be aad or ssh-key"},
		{"    targets: {vm: {instance: x, auth: ssh-key}}\n", "targets.vm: auth ssh-key needs a user"},
		{"    targets: {vm: {instance: x, bastion: {name: b}}}\n", "targets.vm.bastion needs name and resource_group"},
		{"    targets: {vm: {instance: x, resource_group: \"-g\"}}\n", "targets.vm.resource_group must not start"},
		{"    clusters: {aks: {name: x, resource_group: \"-g\"}}\n", "clusters.aks.resource_group must not start"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(ctx + c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	if _, err := Parse([]byte(ctx + "    bastion: {name: b, resource_group: rg}\n    targets: {vm: {instance: x, auth: ssh-key, user: ops, bastion: {name: b2, resource_group: rg2}}, v2: {instance: y, auth: aad}}\n    tunnels: {db: {host: 10.0.0.4, port: 5432}}\n")); err != nil {
		t.Errorf("valid: %v", err)
	}
}
