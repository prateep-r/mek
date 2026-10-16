package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/kube"
)

const kubeConfig = `contexts:
  prod: {provider: aws, aws_profile: p, region: ap-southeast-1, protected: true, clusters: {main: {name: prod-eks, namespace: app}, data: {name: data-eks, region: us-east-1}}}
  g:    {provider: gcp, project: p, readonly: true, clusters: {gke: {name: gke-1, location: asia-southeast1-a}}}
  bare: {provider: aws, aws_profile: p, region: ap-southeast-1}
`

// cloudAPI scripts the describe calls: every cluster exists and is up, except
// ones named "down"; "badca" has a CA that isn't base64.
func cloudAPI(argv []string) (string, int) {
	cmd := strings.Join(argv, " ")
	switch {
	case strings.Contains(cmd, " down "):
		return "", 254
	case strings.HasPrefix(cmd, "aws eks describe-cluster"):
		ca := "Q0E="
		if strings.Contains(cmd, "badca") {
			ca = "%%%"
		}
		return `{"endpoint":"https://` + argv[4] + `.eks","ca":"` + ca + `","status":"ACTIVE"}`, 0
	case strings.HasPrefix(cmd, "gcloud container clusters describe"):
		return `{"endpoint":"10.0.0.1","masterAuth":{"clusterCaCertificate":"Q0E="},"status":"RUNNING"}`, 0
	}
	return "", 0
}

func newKubeHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, kubeConfig)
	h.exec.respond = cloudAPI
	t.Setenv("KUBECONFIG", "")
	old := mekPath
	mekPath = func() (string, error) { return "/bin/mek", nil }
	t.Cleanup(func() { mekPath = old })
	return h
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestKube(t *testing.T) {
	h := newKubeHarness(t)
	out := h.mustRun("-c", "prod", "kube")
	path := filepath.Join(h.home, "kube", "prod.yaml")
	if out != path+"\n" {
		t.Errorf("stdout: %q", out)
	}
	got := h.exec.argv()
	if len(got) != 2 || !strings.HasPrefix(got[0], "aws eks describe-cluster --name data-eks --region us-east-1") ||
		!strings.HasPrefix(got[1], "aws eks describe-cluster --name prod-eks --region ap-southeast-1") {
		t.Errorf("describe calls: %q", got)
	}
	// Lookups are audited reads: allowed on a protected context without asking.
	if e := lastAudit(t); e.Class != "read" || e.Decision != "allowed" || e.Context != "prod" {
		t.Errorf("audit: %+v", e)
	}
	kc := readFile(t, path)
	for _, want := range []string{"server: https://data-eks.eks", "current-context: prod/data", "command: /bin/mek", "- prod-eks", "namespace: app"} {
		if !strings.Contains(kc, want) {
			t.Errorf("kubeconfig missing %q:\n%s", want, kc)
		}
	}

	h.mustRun("-c", "prod", "kube", "main")
	if kc := readFile(t, path); !strings.Contains(kc, "current-context: prod/main") {
		t.Errorf("kube main:\n%s", kc)
	}
	h.mustRun("-c", "g", "kube") // readonly: lookups are reads
	if kc := readFile(t, filepath.Join(h.home, "kube", "g.yaml")); !strings.Contains(kc, "server: https://10.0.0.1") {
		t.Errorf("gke:\n%s", kc)
	}

	// An ad-hoc cluster, with the context's region.
	h.exec.invs = nil
	h.mustRun("-c", "bare", "kube", "--name", "adhoc")
	if got := h.exec.argv(); len(got) != 1 || !strings.Contains(got[0], "--name adhoc --region ap-southeast-1") {
		t.Errorf("ad-hoc: %q", got)
	}
	if kc := readFile(t, filepath.Join(h.home, "kube", "bare.yaml")); !strings.Contains(kc, "current-context: bare/adhoc") {
		t.Errorf("ad-hoc current:\n%s", kc)
	}
	h.mustRun("-c", "prod", "kube", "--name", "extra", "--region", "eu-west-1") // added to the configured ones
	if kc := readFile(t, path); !strings.Contains(kc, "prod/extra") || !strings.Contains(kc, "prod/main") {
		t.Errorf("ad-hoc + configured:\n%s", kc)
	}
}

func TestKubeErrors(t *testing.T) {
	h := newKubeHarness(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", "prod", "kube", "nope"}, `no cluster "nope" (clusters: data, main)`},
		{[]string{"-c", "bare", "kube"}, "has no clusters"},
		{[]string{"-c", "bare", "kube", "--location", "x"}, "need --name"},
		{[]string{"-c", "g", "kube", "--endpoint", "dns"}, "--region, --location, --endpoint and --resource-group need --name"},
		{[]string{"-c", "g", "kube", "--name", "x", "--location", "z", "--endpoint", "internal"}, `endpoint is private or dns, not "internal"`},
		{[]string{"-c", "bare", "kube", "--name", "x", "--region=-r"}, "clusters.x.region must not start with '-'"},
		{[]string{"-c", "bare", "kube", "--name", "x", "--location", "z"}, "use region, not location"},
		{[]string{"-c", "nope", "kube"}, "unknown context"},
		{[]string{"-c", "bare", "kube", "--name", "down"}, "aws eks describe-cluster: exit status 254 (logged in? try: mek -c bare login)"},
	} {
		_, err := h.run(c.args...)
		wantErr(t, err, c.want)
	}

	mekPath = func() (string, error) { return "", errors.New("no mek") }
	_, err := h.run("-c", "prod", "kube")
	wantErr(t, err, "no mek")
	mekPath = func() (string, error) { return "/bin/mek", nil }

	os.WriteFile(filepath.Join(h.home, "kube"), nil, 0o600) // kube dir is a file
	_, err = h.run("-c", "prod", "kube")
	if err == nil {
		t.Error("write error not reported")
	}
}

func TestKubeToken(t *testing.T) {
	h := newKubeHarness(t)
	_, err := h.run("kube", "token", "--name", "x", "--location", "y")
	wantErr(t, err, "needs --context")

	h.mustRun("--context", "prod", "kube", "token", "--name", "prod-eks", "--location", "ap-southeast-1")
	if got := h.exec.argv(); len(got) != 1 || got[0] != "aws eks get-token --cluster-name prod-eks --region ap-southeast-1 --output json" {
		t.Errorf("token: %q", got)
	}
	if !slices.Contains(h.exec.invs[0].Env, "AWS_PROFILE=p") {
		t.Errorf("token needs the context env: %q", h.exec.invs[0].Env)
	}
	if _, err := os.Stat(audit.Path()); !os.IsNotExist(err) {
		t.Error("kube token must not be audited")
	}
	h.mustRun("-c", "g", "kube", "token", "--name", "gke-1", "--location", "z")
	if got := h.exec.argv()[1]; got != "gke-gcloud-auth-plugin" {
		t.Errorf("gke token: %q", got)
	}

	_, err = h.run("-c", "nope", "kube", "token", "--name", "x", "--location", "y")
	wantErr(t, err, "unknown context")
}

func TestKubectl(t *testing.T) {
	h := newKubeHarness(t)
	h.mustRun("kubectl", "-c", "g", "get", "pods")
	inv := h.exec.invs[0]
	if strings.Join(inv.Argv, " ") != "kubectl get pods" || !slices.Contains(inv.Env, "KUBECONFIG="+filepath.Join(h.home, "kube", "g.yaml")) {
		t.Errorf("kubectl: %q %q", inv.Argv, inv.Env)
	}
	if e := lastAudit(t); e.Class != "read" || strings.Join(e.Command, " ") != "kubectl get pods" {
		t.Errorf("audit: %+v", e)
	}

	_, err := h.run("kubectl", "-c", "g", "exec", "-it", "p", "--", "sh") // readonly: no shells
	wantErr(t, err, "readonly — blocked shell command")
	fakePrompts(t, false, nil)
	_, err = h.run("kubectl", "-c", "prod", "delete", "pod", "x")
	wantErr(t, err, "aborted")
	if e := lastAudit(t); e.Class != "destructive" || e.Decision != "declined" {
		t.Errorf("audit: %+v", e)
	}

	_, err = h.run("kubectl", "-c", "bare", "get", "pods")
	wantErr(t, err, "context bare has no clusters")
	_, err = h.run("kubectl", "-c")
	wantErr(t, err, "needs a value")
	_, err = h.run("kubectl", "-c", "nope", "get")
	wantErr(t, err, "unknown context")
}

func TestKubeconfigEnv(t *testing.T) {
	h := newKubeHarness(t)
	mine := kube.Path("prod")
	out := h.mustRun("use", "--shell", "prod")
	if out != "export KUBECONFIG='"+mine+"'\nexport MEK_CONTEXT='prod'\n" {
		t.Errorf("use --shell prod: %q", out)
	}
	// Switching to a context without clusters drops mek's KUBECONFIG...
	t.Setenv("KUBECONFIG", mine)
	if out := h.mustRun("use", "--shell", "bare"); out != "unset KUBECONFIG\nexport MEK_CONTEXT='bare'\n" {
		t.Errorf("use --shell bare: %q", out)
	}
	if out := h.mustRun("env", "bare"); !strings.Contains(out, "unset KUBECONFIG") {
		t.Errorf("env bare: %q", out)
	}
	// ...but never one the user set.
	t.Setenv("KUBECONFIG", "/u/.kube/config")
	if out := h.mustRun("use", "--shell", "bare"); out != "export MEK_CONTEXT='bare'\n" {
		t.Errorf("user KUBECONFIG touched: %q", out)
	}
}

func TestCompleteClusters(t *testing.T) {
	newKubeHarness(t)
	a := &app{opts: globalOpts{context: "prod"}}
	if got, _ := a.completeClusters(nil, nil, ""); strings.Join(got, ",") != "data,main" {
		t.Errorf("clusters: %q", got)
	}
	a.opts.context = "nope"
	if got, _ := a.completeClusters(nil, nil, ""); got != nil {
		t.Errorf("unknown context: %q", got)
	}
}

// A lookup that never ran (the CLI isn't installed) gets no "logged in?" hint.
func TestKubeLookupThatCouldNotRun(t *testing.T) {
	h := newKubeHarness(t)
	h.exec.respond = nil
	h.exec.fail = errors.New("aws not found in PATH — run `mek doctor`")
	_, err := h.run("-c", "prod", "kube")
	wantErr(t, err, "aws eks describe-cluster: aws not found in PATH")
	if err != nil && strings.Contains(err.Error(), "logged in?") {
		t.Errorf("hint for a missing CLI: %v", err)
	}
}
