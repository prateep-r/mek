//go:build contract

package contract

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// freePort picks a local port nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// recorder writes a script that logs its arguments to a file and exits 0.
func recorder(t *testing.T, dir, name, extra string) (log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), name+".log")
	os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("#!/bin/sh\n%s\nprintf '%%s\\n' \"$@\" >> %q\n", extra, log)), 0o755)
	return log
}

// The real aws CLI sends the StartSession mek asked for, then hands the
// session to session-manager-plugin (here a recorder: the data channel is a
// websocket nothing emulates).
func TestTunnelWithRealAWS(t *testing.T) {
	aws := realCLI(t, "aws")
	var mu sync.Mutex
	var start map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Amz-Target") == "AmazonSSM.StartSession" {
			mu.Lock()
			json.Unmarshal(body, &start)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.Write([]byte(`{"SessionId":"s-mek-1","StreamUrl":"wss://ssm.example/s-mek-1","TokenValue":"tok"}`))
	}))
	t.Cleanup(srv.Close)

	bin := t.TempDir()
	plugin := recorder(t, bin, "session-manager-plugin", `[ "$1" = --version ] && { echo 1.2.835.0; exit 0; }`)
	local := freePort(t)
	u := newUser(t, fmt.Sprintf(`contexts:
  prod: {provider: aws, aws_profile: fake, region: us-east-1,
         tunnels: {db: {via: i-0123abcd, host: db.internal, port: 5432, local_port: %d}}}
`, local), []string{aws, filepath.Join(bin, "session-manager-plugin")}, "AWS_ENDPOINT_URL_SSM="+srv.URL)
	os.MkdirAll(filepath.Join(u.home, ".aws"), 0o700)
	os.WriteFile(filepath.Join(u.home, ".aws", "credentials"),
		[]byte("[fake]\naws_access_key_id = AKIAFAKEFAKEFAKEFAKE\naws_secret_access_key = fake\n"), 0o600)

	if out, code := u.run("-c", "prod", "tunnel", "db"); code != 0 {
		t.Fatalf("mek tunnel (exit %d):\n%s", code, out)
	}
	mu.Lock()
	got, _ := json.Marshal(start)
	mu.Unlock()
	want := fmt.Sprintf(`{"DocumentName":"AWS-StartPortForwardingSessionToRemoteHost","Parameters":{"host":["db.internal"],"localPortNumber":["%d"],"portNumber":["5432"]},"Target":"i-0123abcd"}`, local)
	if string(got) != want {
		t.Errorf("StartSession:\n got  %s\n want %s", got, want)
	}
	// aws hands over the session (in AWS_SSM_START_SESSION_RESPONSE, or as an
	// argument in older versions), the region and the forwarding parameters.
	b, err := os.ReadFile(plugin)
	for _, want := range []string{"us-east-1", "StartSession", fmt.Sprintf(`"localPortNumber": ["%d"]`, local)} {
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("plugin args missing %q (%v):\n%s", want, err, b)
		}
	}
}

// A GCP tunnel to another host: the real gcloud runs ssh -N -L through IAP,
// with the context's key and known hosts.
func TestTunnelWithRealGcloudSSH(t *testing.T) {
	gcloud := realCLI(t, "gcloud")
	srv, _ := fakeCompute(t)
	bin := t.TempDir()
	sshLog := recorder(t, bin, "ssh", "")
	local := freePort(t)
	u := newUser(t, `contexts:
  gcp: {provider: gcp, project: my-project, targets: {web: {instance: web-1, zone: asia-southeast1-b}}}
`, []string{filepath.Join(bin, "ssh"), gcloud}, append(gcloudPython(t),
		"CLOUDSDK_AUTH_ACCESS_TOKEN=ya29.fake", "CLOUDSDK_CORE_ACCOUNT=me@example.com",
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_COMPUTE="+srv.URL+"/compute/v1/",
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1", "CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=1")...)

	if out, code := u.run("-c", "gcp", "tunnel", "--via", "web", "--to", "10.0.0.9:5432", "--local", fmt.Sprint(local)); code != 0 {
		t.Fatalf("mek tunnel (exit %d):\n%s", code, out)
	}
	b, _ := os.ReadFile(sshLog)
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	if !strings.Contains(string(b), "start-iap-tunnel web-1") ||
		!strings.HasSuffix(strings.Join(args, " "), fmt.Sprintf("-N -L 127.0.0.1:%d:10.0.0.9:5432", local)) {
		t.Errorf("ssh args:\n%s", b)
	}
	u.homeUntouched(".ssh")
}
