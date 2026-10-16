package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/config"
)

func TestLive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	if _, _, err := (Live{}).Contexts(); err == nil {
		t.Error("no config: no error")
	}
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte(`contexts:
  dev:
    provider: aws
    aws_profile: dev
    protected: true
    targets: {web: {instance: i-0123456789abcdef0}}
    clusters: {main: {name: dev-eks, region: r}}
    tunnels:
      db: {via: web, host: db.internal, port: 5432}
      app: {via: web, port: 8080, local_port: 18080}
  g:
    provider: gcp
    project: p
    tunnels: {sql: {cloudsql: "p:r:db", local_port: 15432}}
`), 0o600)
	cs, current, err := (Live{}).Contexts()
	if err != nil || len(cs) != 2 || current != "" {
		t.Fatalf("contexts: %+v %q %v", cs, current, err)
	}
	d := cs[0]
	if d.Name != "dev" || !d.Protected || d.Summary == "" || d.Targets[0] != (Target{"web", "i-0123456789abcdef0"}) ||
		d.Clusters[0] != (Cluster{"main", "dev-eks"}) ||
		d.Tunnels[0] != (Tunnel{"app", "port 8080 on web", 18080}) || d.Tunnels[1] != (Tunnel{"db", "db.internal:5432 via web", 15432}) {
		t.Errorf("dev: %+v", d)
	}
	if cs[1].Tunnels[0] != (Tunnel{"sql", "Cloud SQL p:r:db", 15432}) {
		t.Errorf("g: %+v", cs[1])
	}
	if len((Live{}).Tunnels()) != 0 || (Live{}).Log("nope") != "" {
		t.Error("tunnels")
	}
	audit.Write(audit.Entry{Context: "dev", Command: []string{"aws"}})
	if es, err := (Live{}).Audit(10); err != nil || len(es) != 1 {
		t.Errorf("audit: %v %v", es, err)
	}
}

func TestRemote(t *testing.T) {
	if got := remote(&config.Tunnel{Host: "10.0.0.5", Port: 5432}); got != "10.0.0.5:5432 via Bastion" {
		t.Errorf("bastion: %s", got)
	}
}
