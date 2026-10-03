//go:build integration

package integration

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
         tunnels: {db: {via: i-0123abcd, host: db.x.rds.amazonaws.com, port: 5432, local_port: 35432}}}
  ro:   {provider: aws, aws_profile: viewer, readonly: true, tunnels: {db: {via: i-0123abcd, port: 5432, local_port: 35433}}}
  gcp:  {provider: gcp, project: my-project,
         tunnels: {app: {via: web-1, port: 8080, local_port: 38080}, sql: {cloudsql: "my-project:asia-southeast1:db", local_port: 35434}}}
`

// tunnelSetup is setup plus session-manager-plugin and cloud-sql-proxy stubs.
func tunnelSetup(t *testing.T) *env {
	t.Helper()
	e := setup(t, tunnelConfig)
	stubs := strings.SplitN(strings.TrimPrefix(e.vars[2], "PATH="), ":", 2)[0]
	b, _ := os.ReadFile(filepath.Join(stubs, "aws"))
	for _, p := range []string{"session-manager-plugin", "cloud-sql-proxy"} {
		os.WriteFile(filepath.Join(stubs, p), b, 0o755)
	}
	return e
}

func TestTunnelSSMRemoteHost(t *testing.T) {
	e := tunnelSetup(t)
	r := e.run("-c", "prod", "--yes", "tunnel", "db")
	if r.Code != 0 || !strings.Contains(r.Stderr, "localhost:35432 → db.x.rds.amazonaws.com:5432 via i-0123abcd") {
		t.Fatalf("tunnel: %+v", r)
	}
	c := e.calls()[0]
	if c.ArgLine() != `ssm start-session --target i-0123abcd --document-name AWS-StartPortForwardingSessionToRemoteHost --parameters {"host":["db.x.rds.amazonaws.com"],"localPortNumber":["35432"],"portNumber":["5432"]}` ||
		c.Env["AWS_PROFILE"] != "admin" {
		t.Errorf("call: %+v", c)
	}
	a := e.audit()
	if len(a) != 2 || a[0]["event"] != "start" || a[1]["event"] != "end" || a[0]["class"] != "tunnel" {
		t.Errorf("audit: %v", a)
	}
}

func TestTunnelGuard(t *testing.T) {
	e := tunnelSetup(t)
	if r := e.run("-c", "ro", "tunnel", "db"); r.Code != 0 {
		t.Errorf("readonly allows tunnels: %+v", r)
	}
	if r := e.run("-c", "prod", "tunnel", "db"); r.Code == 0 || !strings.Contains(r.Stderr, "--yes") {
		t.Errorf("protected tunnel without a terminal must not run: %+v", r)
	}
	if n := len(e.calls()); n != 1 {
		t.Errorf("calls = %d, want 1 (the readonly one)", n)
	}
}

func TestTunnelLocalPortInUse(t *testing.T) {
	e := tunnelSetup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	r := e.run("-c", "prod", "--yes", "tunnel", "db", "--local", port)
	if r.Code == 0 || !strings.Contains(r.Stderr, "localhost:"+port+" is in use") || len(e.calls()) != 0 {
		t.Errorf("busy port: %+v", r)
	}
}

func TestTunnelGCP(t *testing.T) {
	e := tunnelSetup(t)
	if r := e.run("-c", "gcp", "tunnel", "app", "--zone", "asia-southeast1-b"); r.Code != 0 {
		t.Fatalf("iap: %+v", r)
	}
	if c := e.calls()[0]; c.Name != "gcloud" || c.ArgLine() != "compute start-iap-tunnel web-1 8080 --local-host-port=localhost:38080 --zone asia-southeast1-b" {
		t.Errorf("iap call: %+v", c)
	}

	if r := e.run("-c", "gcp", "tunnel", "sql"); r.Code == 0 || !strings.Contains(r.Stderr, "mek login gcp --adc") {
		t.Errorf("cloud sql without ADC: %+v", r)
	}
	adc := filepath.Join(e.mekHome, "gcloud", "gcp", "application_default_credentials.json")
	os.WriteFile(adc, []byte("{}"), 0o600)
	if r := e.run("-c", "gcp", "tunnel", "sql"); r.Code != 0 {
		t.Fatalf("cloud sql: %+v", r)
	}
	c := e.calls()[1]
	if c.Name != "cloud-sql-proxy" || c.ArgLine() != "my-project:asia-southeast1:db --port 35434 --address 127.0.0.1" ||
		c.Env["GOOGLE_APPLICATION_CREDENTIALS"] != adc {
		t.Errorf("proxy call: %+v", c)
	}
}
