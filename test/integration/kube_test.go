//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const kubeConfig = `contexts:
  prod: {provider: aws, aws_profile: prod-admin, region: ap-southeast-1, protected: true, clusters: {main: {name: prod-eks}}}
  gke:  {provider: gcp, project: my-project, clusters: {apps: {name: apps-1, location: asia-southeast1}}}
  bare: {provider: aws, aws_profile: dev}
`

// kubeSetup is setup plus a kubectl stub (it shares the stubs' log).
func kubeSetup(t *testing.T) *env {
	t.Helper()
	e := setup(t, kubeConfig)
	stubs := strings.SplitN(strings.TrimPrefix(e.vars[2], "PATH="), ":", 2)[0]
	b, err := os.ReadFile(filepath.Join(stubs, "aws"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stubs, "kubectl"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	return e
}

// mek kube writes a kubeconfig whose exec plugin is this mek binary, and
// kube token runs the cloud's token command in the context, unaudited.
func TestKubeWritesKubeconfig(t *testing.T) {
	e := kubeSetup(t)
	eks := e.with(`STUB_OUT={"endpoint":"https://ABC.eks.amazonaws.com","ca":"Q0E=","status":"ACTIVE"}`)
	r := eks.run("-c", "prod", "kube")
	path := filepath.Join(e.mekHome, "kube", "prod.yaml")
	if r.Code != 0 || strings.TrimSpace(r.Stdout) != path {
		t.Fatalf("mek kube: %+v", r)
	}
	kc, _ := os.ReadFile(path)
	for _, want := range []string{"server: https://ABC.eks.amazonaws.com", "command: " + mek, "current-context: prod/main", "interactiveMode: Never"} {
		if !strings.Contains(string(kc), want) {
			t.Errorf("kubeconfig missing %q:\n%s", want, kc)
		}
	}
	calls := e.calls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0].ArgLine(), "eks describe-cluster --name prod-eks --region ap-southeast-1") ||
		calls[0].Env["AWS_PROFILE"] != "prod-admin" {
		t.Fatalf("describe: %+v", calls)
	}
	if a := e.audit(); len(a) != 1 || a[0]["class"] != "read" || a[0]["decision"] != "allowed" {
		t.Errorf("audit: %v", a)
	}

	r = e.with(`STUB_OUT={"kind":"ExecCredential"}`).run("--context", "prod", "kube", "token", "--name", "prod-eks", "--location", "ap-southeast-1")
	if r.Code != 0 || strings.TrimSpace(r.Stdout) != `{"kind":"ExecCredential"}` || r.Stderr != "" {
		t.Fatalf("kube token must print only the credential: %+v", r)
	}
	if c := e.calls()[1]; c.ArgLine() != "eks get-token --cluster-name prod-eks --region ap-southeast-1 --output json" || c.Env["AWS_PROFILE"] != "prod-admin" {
		t.Errorf("token call: %+v", c)
	}
	if a := e.audit(); len(a) != 1 {
		t.Errorf("kube token must not be audited: %v", a)
	}

	r = e.with("STUB_EXIT=254").run("-c", "prod", "kube")
	if r.Code == 0 || !strings.Contains(r.Stderr, "mek -c prod login") {
		t.Errorf("failed describe should suggest login: %+v", r)
	}
}

func TestKubeGKE(t *testing.T) {
	e := kubeSetup(t)
	r := e.with(`STUB_OUT={"endpoint":"34.1.2.3","masterAuth":{"clusterCaCertificate":"Q0E="},"status":"RUNNING"}`).run("-c", "gke", "kube")
	if r.Code != 0 {
		t.Fatalf("mek kube: %+v", r)
	}
	c := e.calls()[0]
	if c.Name != "gcloud" || c.ArgLine() != "container clusters describe apps-1 --location asia-southeast1 --format json(endpoint,masterAuth.clusterCaCertificate,status)" ||
		c.Env["CLOUDSDK_CONFIG"] != filepath.Join(e.mekHome, "gcloud", "gke") {
		t.Errorf("describe: %+v", c)
	}
	kc, _ := os.ReadFile(filepath.Join(e.mekHome, "kube", "gke.yaml"))
	if !strings.Contains(string(kc), "server: https://34.1.2.3") {
		t.Errorf("kubeconfig:\n%s", kc)
	}
}

// mek kubectl runs kubectl with the context's KUBECONFIG, guarded and audited.
func TestKubectlPassthrough(t *testing.T) {
	e := kubeSetup(t)
	r := e.run("kubectl", "-c", "prod", "get", "pods")
	if r.Code != 0 {
		t.Fatalf("get: %+v", r)
	}
	c := e.calls()[0]
	if c.Name != "kubectl" || c.ArgLine() != "get pods" || c.Env["KUBECONFIG"] != filepath.Join(e.mekHome, "kube", "prod.yaml") {
		t.Errorf("kubectl: %+v", c)
	}

	// Protected: a delete without --confirm fails closed (no terminal).
	r = e.run("kubectl", "-c", "prod", "delete", "pod", "web-1")
	if r.Code == 0 || len(e.calls()) != 1 {
		t.Errorf("delete must not run: %+v", r)
	}
	r = e.run("-c", "prod", "--confirm", "prod", "kubectl", "create", "secret", "generic", "db", "--from-literal=password=S3cret-kube")
	if r.Code != 0 {
		t.Fatalf("create with --confirm: %+v", r)
	}
	a := e.audit()
	if a[1]["decision"] != "blocked" || a[1]["class"] != "destructive" {
		t.Errorf("delete audit: %v", a[1])
	}
	if b, _ := os.ReadFile(filepath.Join(e.mekHome, "audit.jsonl")); strings.Contains(string(b), "S3cret-kube") {
		t.Errorf("--from-literal value leaked into the audit log:\n%s", b)
	}

	if r := e.run("kubectl", "-c", "bare", "get", "pods"); r.Code == 0 || !strings.Contains(r.Stderr, "has no clusters") {
		t.Errorf("no clusters: %+v", r)
	}
}

// eval "$(mek use --shell ...)" in a real shell: kubectl typed directly
// follows the context, and mek only ever unsets its own KUBECONFIG.
func TestUseShellKubeconfig(t *testing.T) {
	e := kubeSetup(t)
	script := `
eval "$(mek use --shell prod)"; kubectl get ns
eval "$(mek use --shell bare)"; echo "after-bare=${KUBECONFIG-unset}"
export KUBECONFIG=/mine/config
eval "$(mek use --shell bare)"; echo "user=${KUBECONFIG}"
`
	bin := filepath.Dir(mek)
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = append(e.vars[:2:2], e.vars[3:]...)
	cmd.Env = append(cmd.Env, "PATH="+bin+":"+strings.TrimPrefix(e.vars[2], "PATH="), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if c := e.calls(); len(c) != 1 || c[0].Env["KUBECONFIG"] != filepath.Join(e.mekHome, "kube", "prod.yaml") || c[0].Env["MEK_CONTEXT"] != "prod" {
		t.Errorf("kubectl in the shell: %+v", c)
	}
	for _, want := range []string{"after-bare=unset", "user=/mine/config"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
