package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

func TestValidateTunnelsPerCloud(t *testing.T) {
	aws := "contexts:\n  a:\n    provider: aws\n    aws_profile: p\n    targets: {bastion: {instance: i-0123abcd}}\n    tunnels:\n"
	gcp := "contexts:\n  a:\n    provider: gcp\n    project: p\n    targets: {web: {instance: web-1}}\n    tunnels:\n"
	cases := []struct{ in, want string }{
		{aws + "      db: {cloudsql: \"p:r:i\", local_port: 1}\n", "cloudsql tunnels are gcp only"},
		{aws + "      db: {via: nope, port: 1}\n", "via is not a target name, and \"nope\" is not an instance id"},
		{gcp + "      db: {via: Nope, port: 1}\n", `via "Nope" is not a target name or VM name`},
		{"contexts:\n  a:\n    provider: huawei\n    hcloud_profile: p\n    tunnels:\n      db: {via: x, port: 1}\n", "tunnels are not supported on huawei"},
	}
	for _, c := range cases {
		if err := parse(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	for _, ok := range []string{
		aws + "      db: {via: bastion, host: db.internal, port: 5432}\n      web: {via: \"tag:Name=web\", port: 80}\n      raw: {via: i-0abcdef12, port: 22}\n",
		gcp + "      db: {via: web, port: 5432}\n      vm: {via: other-vm, port: 22}\n      sql: {cloudsql: \"p:r:i\", local_port: 1}\n",
	} {
		if err := parse(ok); err != nil {
			t.Errorf("parse(%q) should be valid: %v", ok, err)
		}
	}
	sql, _ := FindPlugin("cloud-sql-proxy")
	ssm, _ := FindPlugin("session-manager-plugin")
	tunnels := &config.Context{Tunnels: map[string]*config.Tunnel{"db": {Via: "x", Port: 1}}}
	if sql.Needed(tunnels) || !ssm.Needed(tunnels) {
		t.Error("an SSM tunnel needs session-manager-plugin, not cloud-sql-proxy")
	}
	tunnels.Tunnels["sql"] = &config.Tunnel{CloudSQL: "p:r:i"}
	if !sql.Needed(tunnels) || sql.Needed(&config.Context{}) || ssm.Needed(&config.Context{}) {
		t.Error("cloud-sql-proxy is needed only with a cloudsql tunnel")
	}
}

func argv(t *testing.T, m TunnelMethod, in Instance, local int) Command {
	t.Helper()
	c, err := m.Command(in, local)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAWSTunnelMethods(t *testing.T) {
	a := &AWS{ctx: &config.Context{Name: "prod"}}
	in := Instance{ID: "i-1"}

	m, _ := a.TunnelMethod(config.Tunnel{Via: "bastion", Port: 8080})
	c := argv(t, m, in, 18080)
	if m.Via() != "bastion" || m.Class() != guard.Tunnel || m.Remote() != "port 8080 on the instance" ||
		strings.Join(c.Argv, " ") != `aws ssm start-session --target i-1 --document-name AWS-StartPortForwardingSession --parameters {"localPortNumber":["18080"],"portNumber":["8080"]}` ||
		strings.Join(c.Requires, ",") != "session-manager-plugin" {
		t.Errorf("instance port: %+v %+v", m, c)
	}

	m, _ = a.TunnelMethod(config.Tunnel{Via: "bastion", Host: "db.x.rds.amazonaws.com", Port: 5432})
	c = argv(t, m, in, 15432)
	if m.Remote() != "db.x.rds.amazonaws.com:5432" || m.Class() != guard.Tunnel ||
		strings.Join(c.Argv, " ") != `aws ssm start-session --target i-1 --document-name AWS-StartPortForwardingSessionToRemoteHost --parameters {"host":["db.x.rds.amazonaws.com"],"localPortNumber":["15432"],"portNumber":["5432"]}` {
		t.Errorf("remote host: %+v %+v", m, c)
	}

	if _, err := a.TunnelMethod(config.Tunnel{CloudSQL: "p:r:i"}); err == nil {
		t.Error("cloudsql on aws")
	}
}

func TestGCPTunnelMethods(t *testing.T) {
	dir := t.TempDir()
	g := &GCP{dir: dir, ctx: &config.Context{Name: "g", Provider: config.ProviderGCP, Project: "p"}}
	in := Instance{ID: "web-1", Zone: "z", User: "ops"}

	m, _ := g.TunnelMethod(config.Tunnel{Via: "web", Port: 8080})
	c := argv(t, m, in, 18080)
	if m.Class() != guard.Tunnel || strings.Join(c.Argv, " ") != "gcloud compute start-iap-tunnel web-1 8080 --local-host-port=localhost:18080 --zone z" {
		t.Errorf("iap port: %+v %+v", m, c)
	}

	m, _ = g.TunnelMethod(config.Tunnel{Via: "web", Host: "10.0.0.9", Port: 5432})
	c = argv(t, m, in, 15432)
	ssh := filepath.Join(dir, "ssh", "g")
	if m.Class() != guard.Shell || m.Remote() != "10.0.0.9:5432" || c.Env["HOME"] != ssh ||
		strings.Join(c.Argv, " ") != "gcloud compute ssh ops@web-1 --zone z --tunnel-through-iap --ssh-key-file "+filepath.Join(ssh, "google_compute_engine")+" -- -N -L 127.0.0.1:15432:10.0.0.9:5432" {
		t.Errorf("iap ssh: %+v %+v", m, c)
	}
	g.dir = filepath.Join(ssh, "google_compute_engine-dir")
	os.WriteFile(g.dir, nil, 0o600)
	m, _ = g.TunnelMethod(config.Tunnel{Via: "web", Host: "h", Port: 1})
	if _, err := m.Command(in, 10001); err == nil {
		t.Error("ssh dir error not reported")
	}

	g.dir = dir
	m, _ = g.TunnelMethod(config.Tunnel{CloudSQL: "p:r:db", PrivateIP: true, LocalPort: 15432})
	if _, err := m.Command(Instance{}, 15432); err == nil || !strings.Contains(err.Error(), "mek login g --adc") {
		t.Errorf("no ADC: %v", err)
	}
	os.MkdirAll(filepath.Join(dir, "gcloud", "g"), 0o700)
	os.WriteFile(filepath.Join(dir, "gcloud", "g", "application_default_credentials.json"), []byte("{}"), 0o600)
	c = argv(t, m, Instance{}, 15432)
	if m.Via() != "" || m.Class() != guard.Tunnel || m.Remote() != "Cloud SQL p:r:db" ||
		strings.Join(c.Argv, " ") != "cloud-sql-proxy p:r:db --port 15432 --address 127.0.0.1 --private-ip" ||
		strings.Join(c.Requires, ",") != "cloud-sql-proxy" {
		t.Errorf("cloud sql: %+v %+v", m, c)
	}
	m, _ = g.TunnelMethod(config.Tunnel{CloudSQL: "p:r:db", LocalPort: 1})
	if c := argv(t, m, Instance{}, 1); strings.Contains(strings.Join(c.Argv, " "), "private-ip") {
		t.Errorf("public IP: %v", c.Argv)
	}
}
