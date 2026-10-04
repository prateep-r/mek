package cli

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const tunnelConfig = `contexts:
  prod: {provider: aws, aws_profile: admin, region: ap-southeast-1, protected: true,
         targets: {bastion: {instance: "tag:Name=bastion"}},
         tunnels: {db: {via: bastion, host: db.x.rds.amazonaws.com, port: 5432, local_port: 25432}, web: {via: i-0123abcd, port: 80}}}
  ro:   {provider: aws, aws_profile: viewer, readonly: true, tunnels: {db: {via: i-0123abcd, port: 5432, local_port: 25433}}}
  gcp:  {provider: gcp, project: p, targets: {web: {instance: web-1, zone: asia-southeast1-b}},
         tunnels: {app: {via: web, port: 8080, local_port: 28080}, pg: {via: web, host: 10.0.0.9, port: 5432, local_port: 25434},
                   sql: {cloudsql: "p:asia-southeast1:db", local_port: 25435}}}
`

func newTunnelHarness(t *testing.T) *harness {
	t.Helper()
	h := newShellHarness(t) // scripted lookups, plugins "installed"
	os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte(tunnelConfig), 0o600)
	return h
}

func TestTunnelAWS(t *testing.T) {
	h := newTunnelHarness(t)
	h.mustRun("-c", "prod", "-y", "tunnel", "db")
	got := h.exec.argv()
	if len(got) != 2 || !strings.HasPrefix(got[0], "aws ec2 describe-instances") ||
		got[1] != `aws ssm start-session --target i-0bastion0 --document-name AWS-StartPortForwardingSessionToRemoteHost --parameters {"host":["db.x.rds.amazonaws.com"],"localPortNumber":["25432"],"portNumber":["5432"]}` {
		t.Fatalf("calls: %q", got)
	}
	es := readAudit(t)
	if len(es) != 3 || es[1].Event != "start" || es[1].Class != "tunnel" ||
		es[2].Target != "localhost:25432 → db.x.rds.amazonaws.com:5432 via bastion → i-0bastion0" {
		t.Errorf("audit: %+v", es)
	}

	// Ad hoc, and --local on a named tunnel.
	h.exec.invs = nil
	h.mustRun("-c", "prod", "-y", "tunnel", "--via", "i-0123456789abcdef0", "--to", ":8080")
	h.mustRun("-c", "prod", "-y", "tunnel", "web", "--local", "28081")
	got = h.exec.argv()
	if len(got) != 2 || !strings.Contains(got[0], `--document-name AWS-StartPortForwardingSession --parameters {"localPortNumber":["18080"],"portNumber":["8080"]}`) ||
		!strings.Contains(got[1], `"localPortNumber":["28081"],"portNumber":["80"]`) {
		t.Errorf("ad hoc / --local: %q", got)
	}
}

func TestTunnelGuard(t *testing.T) {
	h := newTunnelHarness(t)
	h.mustRun("-c", "ro", "tunnel", "db") // readonly: a tunnel only moves bytes
	if len(h.exec.invs) != 1 {
		t.Fatalf("readonly tunnel should run: %q", h.exec.argv())
	}
	fakePrompts(t, false, nil)
	_, err := h.run("-c", "prod", "tunnel", "web")
	wantErr(t, err, "aborted")
	// A GCP tunnel to another host logs in over SSH: blocked like a shell.
	h2 := newTunnelHarness(t)
	os.WriteFile(filepath.Join(h2.home, "config.yaml"), []byte(strings.Replace(tunnelConfig, "gcp:  {provider: gcp, project: p,", "gcp:  {provider: gcp, project: p, readonly: true,", 1)), 0o600)
	_, err = h2.run("-c", "gcp", "tunnel", "pg")
	wantErr(t, err, "readonly — blocked shell command")
	h2.mustRun("-c", "gcp", "tunnel", "app") // IAP port forwarding is a tunnel
}

func TestTunnelGCP(t *testing.T) {
	h := newTunnelHarness(t)
	h.mustRun("-c", "gcp", "tunnel", "app")
	h.mustRun("-c", "gcp", "tunnel", "pg")
	got := h.exec.argv()
	if got[0] != "gcloud compute start-iap-tunnel web-1 8080 --local-host-port=localhost:28080 --zone asia-southeast1-b" ||
		!strings.HasSuffix(got[1], "-- -N -L 127.0.0.1:25434:10.0.0.9:5432") || !strings.HasPrefix(got[1], "gcloud compute ssh web-1 --zone asia-southeast1-b") {
		t.Errorf("gcp tunnels: %q", got)
	}

	_, err := h.run("-c", "gcp", "tunnel", "sql")
	wantErr(t, err, "mek login gcp --adc")
	adc := filepath.Join(h.home, "gcloud", "gcp", "application_default_credentials.json")
	os.WriteFile(adc, []byte("{}"), 0o600)
	h.mustRun("-c", "gcp", "tunnel", "sql")
	inv := h.exec.invs[len(h.exec.invs)-1]
	if strings.Join(inv.Argv, " ") != "cloud-sql-proxy p:asia-southeast1:db --port 25435 --address 127.0.0.1" {
		t.Errorf("cloud sql: %q", inv.Argv)
	}
	h.mustRun("-c", "gcp", "tunnel", "--cloudsql", "p:r:other", "--private-ip", "--local", "25436")
	if got := h.exec.argv(); !strings.HasSuffix(got[len(got)-1], "--port 25436 --address 127.0.0.1 --private-ip") {
		t.Errorf("ad hoc cloud sql: %q", got)
	}
}

func TestTunnelErrors(t *testing.T) {
	h := newTunnelHarness(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", "prod", "tunnel"}, "name a tunnel from the context's tunnels:"},
		{[]string{"-c", "prod", "tunnel", "nope"}, `context prod has no tunnel "nope" (tunnels: db, web)`},
		{[]string{"-c", "prod", "tunnel", "db", "--via", "x"}, "a named tunnel takes only --local"},
		{[]string{"-c", "prod", "tunnel", "db", "--local", "70000"}, "local_port 70000 is not a port"},
		{[]string{"-c", "prod", "tunnel", "--via", "bastion", "--to", "db"}, `--to "db": want host:port or :port`},
		{[]string{"-c", "prod", "tunnel", "--via", "bastion", "--to", "db:pg"}, `port "pg" is not a number`},
		{[]string{"-c", "prod", "tunnel", "--via", "bastion"}, "port 0 is not a port"},
		{[]string{"-c", "prod", "tunnel", "--cloudsql", "p:r:i", "--local", "1"}, "cloudsql tunnels are gcp only"},
		{[]string{"-c", "prod", "tunnel", "--via", "web-1", "--to", ":80"}, `unknown target "web-1"`},
		{[]string{"-c", "prod", "-y", "tunnel", "web", "--local", busy}, "localhost:" + busy + " is in use — pick another port with --local"},
		{[]string{"-c", "nope", "tunnel", "x"}, "unknown context"},
	} {
		_, err := h.run(c.args...)
		wantErr(t, err, c.want)
	}

	// The hop can't be resolved.
	h.exec.respond = func([]string) (string, int) { return `[]`, 0 }
	_, err = h.run("-c", "prod", "tunnel", "db")
	wantErr(t, err, "matches 0 running instances")
}

func TestCompleteTunnels(t *testing.T) {
	newTunnelHarness(t)
	a := &app{opts: globalOpts{context: "gcp"}}
	if got, _ := a.completeTunnels(nil, nil, ""); strings.Join(got, ",") != "app,pg,sql" {
		t.Errorf("tunnels: %q", got)
	}
	a.opts.context = "nope"
	if got, _ := a.completeTunnels(nil, nil, ""); got != nil {
		t.Errorf("unknown context: %q", got)
	}
}
