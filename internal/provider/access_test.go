package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

func TestInstanceLabel(t *testing.T) {
	for in, want := range map[Instance]string{
		{ID: "i-1"}:                           "i-1",
		{ID: "vm-1", Zone: "z", User: "ops"}:  "ops@vm-1 (z)",
		{ID: "i-1", Alias: "bastion"}:         "bastion → i-1",
		{ID: "vm-1", Zone: "z", Alias: "web"}: "web → vm-1 (z)",
	} {
		if got := in.Label(); got != want {
			t.Errorf("%+v: %q, want %q", in, got, want)
		}
	}
}

func TestFindPlugin(t *testing.T) {
	if p, ok := FindPlugin("session-manager-plugin"); !ok || !strings.Contains(p.Tool.Brew, "session-manager-plugin") {
		t.Errorf("session-manager-plugin: %+v %v", p, ok)
	}
	if _, ok := FindPlugin("nope"); ok {
		t.Error("unknown plugin found")
	}
	if awsCloud.Plugins[0].Needed(&config.Context{}) {
		t.Error("needed without targets")
	}
	if !awsCloud.Plugins[0].Needed(&config.Context{Targets: map[string]*config.Target{"b": {}}}) {
		t.Error("not needed with targets")
	}
}

func TestCmdBuilder(t *testing.T) {
	b := command("x", "y").opt("--a", "1").opt("--skipped", "").args("--flag").env("K", "V").needs("p")
	c := b.build()
	if strings.Join(c.Argv, " ") != "x y --a 1 --flag" || c.Env["K"] != "V" || len(c.Requires) != 1 {
		t.Fatalf("built: %+v", c)
	}
	b.args("more").env("K", "changed") // a built Command doesn't share the builder's state
	if len(c.Argv) != 5 || c.Env["K"] != "V" {
		t.Errorf("builder leaked into the built command: %+v", c)
	}
	if c := command("x").build(); c.Env != nil || c.Requires != nil {
		t.Errorf("empty parts: %+v", c)
	}
}

func TestValidateTargets(t *testing.T) {
	aws := "contexts:\n  a:\n    provider: aws\n    aws_profile: p\n    targets:\n"
	gcp := "contexts:\n  a:\n    provider: gcp\n    project: p\n    targets:\n"
	cases := []struct{ in, want string }{
		{aws + "      b: {instance: bastion}\n", "not an instance id (i-…) or tag:Key=Value"},
		{aws + "      b: {instance: \"tag:Name\"}\n", "tag targets look like tag:Key=Value"},
		{aws + "      b: {instance: i-0123456789abcdef0, zone: z}\n", "aws targets take only an instance"},
		{gcp + "      b: {instance: Not_A_VM}\n", "is not a VM name"},
		{"contexts:\n  a:\n    provider: huawei\n    hcloud_profile: p\n    targets:\n      b: {instance: x}\n", "targets are not supported on huawei"},
	}
	for _, c := range cases {
		if err := parse(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	for _, ok := range []string{
		aws + "      b: {instance: i-0123abcd}\n      m: {instance: mi-0123456789abcdef0}\n      t: {instance: \"tag:Name=bastion host\"}\n",
		gcp + "      b: {instance: vm-1, zone: z, user: ops}\n",
	} {
		if err := parse(ok); err != nil {
			t.Errorf("parse(%q) should be valid: %v", ok, err)
		}
	}
}

func TestAWSShell(t *testing.T) {
	a := &AWS{ctx: &config.Context{Name: "prod", Provider: config.ProviderAWS, Targets: map[string]*config.Target{
		"bastion": {Instance: "tag:Name=bastion"},
		"db":      {Instance: "i-0123abcd"},
	}}}
	var argv []string
	q := fakeQuery(`["i-0bastion0"]`, nil, &argv)
	cases := []struct {
		spec string
		want Instance
	}{
		{"i-0123456789abcdef0", Instance{ID: "i-0123456789abcdef0"}},
		{"db", Instance{ID: "i-0123abcd", Alias: "db"}},
		{"bastion", Instance{ID: "i-0bastion0", Alias: "bastion"}},
		{"tag:Team=a,b c", Instance{ID: "i-0bastion0"}},
	}
	for _, c := range cases {
		got, err := a.ResolveTarget(c.spec, TargetOptions{}, q)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v %v", c.spec, got, err)
		}
	}
	// The tag filter is JSON: the comma and space stay in the value.
	if s := strings.Join(argv, " "); !strings.Contains(s, `[{"Name":"tag:Team","Values":["a,b c"]},{"Name":"instance-state-name","Values":["running"]}]`) ||
		!strings.HasPrefix(s, "aws ec2 describe-instances --filters") {
		t.Errorf("lookup: %s", s)
	}

	boom := errors.New("boom")
	for _, c := range []struct {
		spec string
		o    TargetOptions
		q    Query
		want string
	}{
		{"web", TargetOptions{}, q, `unknown target "web" — use a name under targets:`},
		{"i-1", TargetOptions{Zone: "z"}, q, "--zone, --user and --resource-group are for gcp and azure"},
		{"tag:Name", TargetOptions{}, q, "tag:Key=Value"},
		{"tag:Name=x", TargetOptions{}, fakeQuery(`[]`, nil, &argv), "matches 0 running instances"},
		{"tag:Name=x", TargetOptions{}, fakeQuery(`["i-1","i-2"]`, nil, &argv), "matches 2 running instances, want exactly 1 (i-1, i-2)"},
		{"tag:Name=x", TargetOptions{}, fakeQuery(`{`, nil, &argv), "ec2 describe-instances"},
		{"tag:Name=x", TargetOptions{}, fakeQuery("", boom, &argv), "boom"},
	} {
		if _, err := a.ResolveTarget(c.spec, c.o, c.q); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.spec, err, c.want)
		}
	}

	c, _ := a.ShellCommand(Instance{ID: "i-1"})
	if strings.Join(c.Argv, " ") != "aws ssm start-session --target i-1" || strings.Join(c.Requires, ",") != "session-manager-plugin" || c.Env != nil {
		t.Errorf("shell: %+v", c)
	}
}

func TestGCPShell(t *testing.T) {
	dir := t.TempDir()
	g := &GCP{dir: dir, ctx: &config.Context{Name: "g", Provider: config.ProviderGCP, Project: "p", Targets: map[string]*config.Target{
		"web":  {Instance: "web-1", Zone: "asia-southeast1-b", User: "ops"},
		"auto": {Instance: "auto-1"},
	}}}
	var argv []string
	q := fakeQuery(`[{"name":"auto-1","zone":"https://www.googleapis.com/compute/v1/projects/p/zones/asia-southeast1-a","status":"RUNNING"},
		{"name":"auto-1","zone":"https://x/zones/us-central1-a","status":"TERMINATED"},{"name":"auto-10","zone":"x/zones/b","status":"RUNNING"}]`, nil, &argv)
	cases := []struct {
		spec string
		o    TargetOptions
		want Instance
	}{
		{"web", TargetOptions{}, Instance{ID: "web-1", Zone: "asia-southeast1-b", User: "ops", Alias: "web"}},
		{"web", TargetOptions{Zone: "z", User: "me"}, Instance{ID: "web-1", Zone: "z", User: "me", Alias: "web"}}, // flags win
		{"vm-9", TargetOptions{Zone: "z"}, Instance{ID: "vm-9", Zone: "z"}},
		{"auto", TargetOptions{}, Instance{ID: "auto-1", Zone: "asia-southeast1-a", Alias: "auto"}},
	}
	for _, c := range cases {
		argv = nil
		got, err := g.ResolveTarget(c.spec, c.o, q)
		if err != nil || got != c.want {
			t.Errorf("%s %+v: %+v %v", c.spec, c.o, got, err)
		}
	}
	if s := strings.Join(argv, " "); s != `gcloud compute instances list --filter name=("auto-1") --format json(name,zone,status)` {
		t.Errorf("lookup: %s", s)
	}
	boom := errors.New("boom")
	for _, c := range []struct {
		spec string
		q    Query
		want string
	}{
		{"Bad_Name", q, `unknown target "Bad_Name" — use a name under targets: or a VM name`},
		{"vm-1", fakeQuery(`[]`, nil, &argv), "VM vm-1: 0 running in the project () — pass --zone"},
		{"vm-1", fakeQuery(`[{"name":"vm-1","zone":"a/zones/z1","status":"RUNNING"},{"name":"vm-1","zone":"a/zones/z2","status":"RUNNING"}]`, nil, &argv), "2 running in the project (z1, z2)"},
		{"vm-1", fakeQuery(`nope`, nil, &argv), "compute instances list"},
		{"vm-1", fakeQuery("", boom, &argv), "boom"},
	} {
		if _, err := g.ResolveTarget(c.spec, TargetOptions{}, c.q); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.spec, err, c.want)
		}
	}
	if _, err := g.ResolveTarget("vm-1", TargetOptions{ResourceGroup: "rg"}, q); err == nil || !strings.Contains(err.Error(), "--resource-group is for azure") {
		t.Errorf("resource group on gcp: %v", err)
	}

	c, err := g.ShellCommand(Instance{ID: "web-1", Zone: "z", User: "ops"})
	ssh := filepath.Join(dir, "ssh", "g")
	if err != nil || strings.Join(c.Argv, " ") != "gcloud compute ssh ops@web-1 --zone z --tunnel-through-iap --ssh-key-file "+filepath.Join(ssh, "google_compute_engine") ||
		c.Env["HOME"] != ssh || c.Requires != nil {
		t.Errorf("shell: %+v %v", c, err)
	}
	if fi, err := os.Stat(ssh); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("ssh dir: %v %v", fi, err)
	}
	if c, _ := g.ShellCommand(Instance{ID: "vm-1", Zone: "z"}); c.Argv[3] != "vm-1" {
		t.Errorf("no user: %v", c.Argv)
	}
	g.dir = filepath.Join(ssh, "google_compute_engine-is-not-a-dir")
	os.WriteFile(g.dir, nil, 0o600)
	if _, err := g.ShellCommand(Instance{ID: "vm-1"}); err == nil {
		t.Error("mkdir error not reported")
	}
}
