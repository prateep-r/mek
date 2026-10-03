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
