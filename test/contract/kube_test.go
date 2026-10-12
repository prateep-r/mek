//go:build contract

package contract

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
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
				"masterAuth":           map[string]string{"clusterCaCertificate": base64.StdEncoding.EncodeToString(ca)},
				"privateClusterConfig": map[string]any{"enablePrivateNodes": true, "privateEndpoint": "10.9.8.7"},
				"controlPlaneEndpointsConfig": map[string]any{
					"dnsEndpointConfig": map[string]any{"endpoint": "gke-fake.asia-southeast1.gke.goog", "allowExternalTraffic": true},
					"ipEndpointsConfig": map[string]any{"enabled": true},
				},
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

// Real gcloud returns the private and DNS-based endpoints mek asks for.
func TestKubeGKEEndpointsWithRealGcloud(t *testing.T) {
	gcloud := realCLI(t, "gcloud")
	srv, caFile, _ := fakeGKE(t, "unused")
	u := newUser(t, `contexts:
  gke:
    provider: gcp
    project: my-project
    clusters:
      priv: {name: apps-1, location: asia-southeast1, endpoint: private}
      dns: {name: apps-1, location: asia-southeast1, endpoint: dns}
`, []string{gcloud}, append(gcloudPython(t),
		"CLOUDSDK_AUTH_ACCESS_TOKEN=ya29.fake-access-token",
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_CONTAINER="+srv.URL+"/",
		"CLOUDSDK_CORE_CUSTOM_CA_CERTS_FILE="+caFile,
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1", "CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=1")...)
	if out, code := u.run("-c", "gke", "kube"); code != 0 {
		t.Fatalf("mek kube (exit %d):\n%s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(u.mekHome, "kube", "gke.yaml"))
	cfg := string(b)
	if !strings.Contains(cfg, "server: https://10.9.8.7") || !strings.Contains(cfg, "server: https://gke-fake.asia-southeast1.gke.goog") ||
		strings.Count(cfg, "certificate-authority-data") != 1 { // the DNS endpoint's certificate is publicly trusted
		t.Errorf("kubeconfig:\n%s", cfg)
	}
}
