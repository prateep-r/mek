//go:build contract

package contract

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeEKS is one TLS server playing both the EKS API (DescribeCluster) and
// the cluster's Kubernetes API, which only answers requests carrying an EKS
// token (k8s-aws-v1.…) — so a /version reply proves the whole chain: the
// CA in the kubeconfig, kubectl running mek's exec plugin, and mek running
// the real `aws eks get-token` with the context's credentials.
func fakeEKS(t *testing.T) (srv *httptest.Server, caFile string, authorized *atomic.Int32) {
	t.Helper()
	authorized = &atomic.Int32{}
	srv = httptest.NewUnstartedServer(nil)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/clusters/prod-eks": // aws eks describe-cluster
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
			json.NewEncoder(w).Encode(map[string]any{"cluster": map[string]any{
				"name": "prod-eks", "endpoint": srv.URL, "status": "ACTIVE",
				"certificateAuthority": map[string]string{"data": base64.StdEncoding.EncodeToString(ca)},
			}})
		case r.URL.Path == "/version":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer k8s-aws-v1.") {
				http.Error(w, `{"kind":"Status","code":401}`, http.StatusUnauthorized)
				return
			}
			authorized.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"major":"1","minor":"31","gitVersion":"v1.31.0-fake"}`))
		default:
			http.NotFound(w, r)
		}
	})
	srv.StartTLS()
	t.Cleanup(srv.Close)
	caFile = filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
	return srv, caFile, authorized
}

func kubeUser(t *testing.T) (*user, *atomic.Int32, string) {
	t.Helper()
	aws, kubectl := realCLI(t, "aws"), realCLI(t, "kubectl")
	srv, caFile, authorized := fakeEKS(t)
	u := newUser(t, `contexts:
  prod: {provider: aws, aws_profile: fake, region: us-east-1, clusters: {main: {name: prod-eks, namespace: app}}}
`, []string{aws, kubectl}, "AWS_ENDPOINT_URL_EKS="+srv.URL, "AWS_CA_BUNDLE="+caFile)
	// Static fake keys: get-token only signs a URL, it never calls AWS.
	os.MkdirAll(filepath.Join(u.home, ".aws"), 0o700)
	os.WriteFile(filepath.Join(u.home, ".aws", "credentials"),
		[]byte("[fake]\naws_access_key_id = AKIAFAKEFAKEFAKEFAKE\naws_secret_access_key = fake\n"), 0o600)
	return u, authorized, kubectl
}

func TestKubeWithRealKubectlAndAWS(t *testing.T) {
	u, authorized, _ := kubeUser(t)
	if out, code := u.run("-c", "prod", "kube"); code != 0 {
		t.Fatalf("mek kube (exit %d):\n%s", code, out)
	}
	// kubectl through mek, and kubectl from a shell after `mek env`.
	u.want("v1.31.0-fake", "-c", "prod", "kubectl", "version", "-o", "json")
	u.want(`"gitVersion":"v1.31.0-fake"`, "-c", "prod", "exec", "--", "kubectl", "get", "--raw", "/version")
	u.want("app", "-c", "prod", "kubectl", "config", "view", "--minify", "-o", "jsonpath={..namespace}")
	if authorized.Load() < 2 {
		t.Errorf("the API server saw %d EKS tokens, want ≥ 2", authorized.Load())
	}
	u.homeUntouched(".kube/config") // kubectl's own ~/.kube/cache holds no credentials
}

func TestKubeMergeWithRealKubectl(t *testing.T) {
	u, authorized, kubectl := kubeUser(t)
	kc := filepath.Join(u.home, ".kube", "config")
	plain := func(args ...string) string { // the user's own kubectl, no mek
		t.Helper()
		cmd := exec.Command(kubectl, append(args, "--kubeconfig", kc)...)
		cmd.Env = u.vars
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	plain("config", "set-context", "mine", "--cluster=elsewhere")
	plain("config", "use-context", "mine")

	if out, code := u.run("-c", "prod", "kube", "--merge"); code != 0 {
		t.Fatalf("merge (exit %d):\n%s", code, out)
	}
	if got := plain("config", "get-contexts", "-o", "name"); got != "mine\nprod/main\n" {
		t.Errorf("contexts after merge:\n%s", got)
	}
	if got := plain("config", "current-context"); got != "mine\n" {
		t.Errorf("--merge without --use changed current-context: %s", got)
	}
	if b, _ := os.ReadFile(kc + ".mek-backup"); !strings.Contains(string(b), "mine") || strings.Contains(string(b), "prod/main") {
		t.Errorf("backup should be the file before mek:\n%s", b)
	}
	// The merged entry works on its own: kubectl → mek kube token → aws.
	if got := plain("--context", "prod/main", "get", "--raw", "/version"); !strings.Contains(got, "v1.31.0-fake") || authorized.Load() == 0 {
		t.Errorf("merged credentials: %s (authorized %d)", got, authorized.Load())
	}

	if out, code := u.run("-c", "prod", "kube", "--merge", "--use"); code != 0 || plain("config", "current-context") != "prod/main\n" {
		t.Errorf("--use (exit %d):\n%s", code, out)
	}
	if out, code := u.run("-c", "prod", "kube", "--unmerge"); code != 0 {
		t.Fatalf("unmerge (exit %d):\n%s", code, out)
	}
	if got := plain("config", "get-contexts", "-o", "name"); got != "mine\n" {
		t.Errorf("contexts after unmerge:\n%s", got)
	}
	if got := plain("config", "view", "-o", "jsonpath={.users[*].name} {.clusters[*].name}"); strings.Contains(got, "prod/main") {
		t.Errorf("unmerge left entries: %s", got)
	}
}

// fakeGKE plays the GKE API (clusters.get) and the cluster's API server,
// which only accepts the access token gke-gcloud-auth-plugin hands out.
func fakeGKE(t *testing.T, token string) (srv *httptest.Server, caFile string, authorized *atomic.Int32) {
	t.Helper()
	authorized = &atomic.Int32{}
	srv = httptest.NewUnstartedServer(nil)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/projects/my-project/locations/asia-southeast1/clusters/apps-1"):
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
			json.NewEncoder(w).Encode(map[string]any{
				"name": "apps-1", "status": "RUNNING", "endpoint": strings.TrimPrefix(srv.URL, "https://"),
				"masterAuth": map[string]string{"clusterCaCertificate": base64.StdEncoding.EncodeToString(ca)},
			})
		case r.URL.Path == "/version":
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, `{"kind":"Status","code":401}`, http.StatusUnauthorized)
				return
			}
			authorized.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"major":"1","minor":"31","gitVersion":"v1.31.0-gke-fake"}`))
		default:
			t.Logf("fake GKE: unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	srv.StartTLS()
	t.Cleanup(srv.Close)
	caFile = filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
	return srv, caFile, authorized
}

func TestKubeWithRealGcloudAndPlugin(t *testing.T) {
	gcloud, plugin, kubectl := realCLI(t, "gcloud"), realCLI(t, "gke-gcloud-auth-plugin"), realCLI(t, "kubectl")
	const token = "ya29.fake-access-token"
	srv, caFile, authorized := fakeGKE(t, token)
	u := newUser(t, `contexts:
  gke: {provider: gcp, project: my-project, clusters: {apps: {name: apps-1, location: asia-southeast1}}}
`, []string{gcloud, plugin, kubectl}, append(gcloudPython(t),
		"CLOUDSDK_AUTH_ACCESS_TOKEN="+token, // stands in for `mek login`
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_CONTAINER="+srv.URL+"/",
		"CLOUDSDK_CORE_CUSTOM_CA_CERTS_FILE="+caFile,
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1", "CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=1")...)
	if out, code := u.run("-c", "gke", "kube"); code != 0 {
		t.Fatalf("mek kube (exit %d):\n%s", code, out)
	}
	u.want(`"gitVersion":"v1.31.0-gke-fake"`, "-c", "gke", "kubectl", "get", "--raw", "/version")
	if authorized.Load() == 0 {
		t.Error("the API server saw no plugin token")
	}
	u.homeUntouched(".kube/config", ".config/gcloud")
}
