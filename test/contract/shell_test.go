//go:build contract

package contract

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeCompute plays the Compute Engine API for `gcloud compute ssh`: the
// VM, the project's metadata (where gcloud adds the SSH key) and the
// operation that adds it.
func fakeCompute(t *testing.T) (srv *httptest.Server, keyAdded func() bool) {
	t.Helper()
	var mu sync.Mutex
	added := false
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
		op := map[string]any{"kind": "compute#operation", "id": "1", "name": "op-1", "status": "DONE", "progress": 100,
			"operationType": "setMetadata", "targetLink": srv.URL + "/compute/v1/projects/my-project",
			"selfLink": srv.URL + "/compute/v1/projects/my-project/global/operations/op-1"}
		switch p := r.URL.Path; {
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/setCommonInstanceMetadata"):
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			added = strings.Contains(string(body), "ssh-rsa")
			mu.Unlock()
			reply(op)
		case strings.HasSuffix(p, "/zones/asia-southeast1-b/instances/web-1"):
			reply(map[string]any{"name": "web-1", "id": "4242", "status": "RUNNING",
				"zone":              srv.URL + "/compute/v1/projects/my-project/zones/asia-southeast1-b",
				"networkInterfaces": []any{map[string]any{"networkIP": "10.0.0.5"}}})
		case strings.HasSuffix(p, "/projects/my-project"):
			reply(map[string]any{"name": "my-project", "commonInstanceMetadata": map[string]any{"fingerprint": "ZjE=", "items": []any{}}})
		case strings.Contains(p, "/operations/"):
			reply(op)
		default:
			t.Logf("fake compute: unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() bool { mu.Lock(); defer mu.Unlock(); return added }
}

func TestShellWithRealGcloud(t *testing.T) {
	gcloud := realCLI(t, "gcloud")
	srv, keyAdded := fakeCompute(t)
	// A fake ssh records how gcloud runs it (the real one would connect).
	bin, sshLog := t.TempDir(), filepath.Join(t.TempDir(), "ssh.log")
	os.WriteFile(filepath.Join(bin, "ssh"), []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\n", sshLog)), 0o755)
	for _, tool := range []string{"ssh-keygen"} { // gcloud makes the key with the real one
		if p, err := exec.LookPath(tool); err == nil {
			os.Symlink(p, filepath.Join(bin, tool))
		}
	}
	u := newUser(t, `contexts:
  gcp: {provider: gcp, project: my-project, targets: {web: {instance: web-1, zone: asia-southeast1-b}}}
`, []string{filepath.Join(bin, "ssh"), gcloud}, append(gcloudPython(t),
		"CLOUDSDK_AUTH_ACCESS_TOKEN=ya29.fake", "CLOUDSDK_CORE_ACCOUNT=me@example.com",
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_COMPUTE="+srv.URL+"/compute/v1/",
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1", "CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=1")...)

	if out, code := u.run("-c", "gcp", "shell", "web"); code != 0 {
		t.Fatalf("mek shell (exit %d):\n%s", code, out)
	}
	b, err := os.ReadFile(sshLog)
	if err != nil {
		t.Fatalf("gcloud never ran ssh: %v", err)
	}
	ssh := string(b)
	dir := filepath.Join(u.mekHome, "ssh", "gcp")
	if real, err := filepath.EvalSymlinks(u.mekHome); err == nil { // gcloud reports macOS's /private/var paths
		ssh = strings.ReplaceAll(ssh, real, u.mekHome)
	}
	for _, want := range []string{
		filepath.Join(dir, "google_compute_engine"),                                      // -i: the context's key
		"UserKnownHostsFile=" + filepath.Join(dir, ".ssh", "google_compute_known_hosts"), // not ~/.ssh
		"start-iap-tunnel web-1",                                                         // through IAP
	} {
		if !strings.Contains(ssh, want) {
			t.Errorf("ssh args missing %q:\n%s", want, ssh)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "google_compute_engine.pub")); err != nil {
		t.Errorf("key not generated in the context's dir: %v", err)
	}
	if !keyAdded() {
		t.Error("gcloud did not add the key to the project's metadata")
	}
	u.homeUntouched(".ssh", ".config/gcloud")
}
