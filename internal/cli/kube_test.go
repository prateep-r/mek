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
		{[]string{"-c", "bare", "kube", "--name", "x", "--region=-r"}, "clusters.x.region must not start with '-'"},
		{[]string{"-c", "bare", "kube", "--name", "x", "--location", "z"}, "use region, not location"},
		{[]string{"-c", "nope", "kube"}, "unknown context"},
		{[]string{"-c", "prod", "kube", "--use"}, "--use needs --merge"},
		{[]string{"-c", "bare", "kube", "--name", "down"}, "aws eks describe-cluster: exit status 254 (logged in? try: mek -c bare login)"},
		{[]string{"-c", "prod", "kube", "--merge", "--unmerge"}, "none of the others can be"},
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

func TestKubeMerge(t *testing.T) {
	h := newKubeHarness(t)
	target := filepath.Join(t.TempDir(), "user", "config")
	t.Setenv("KUBECONFIG", target)

	h.mustRun("-c", "prod", "kube", "main", "--merge", "--use")
	var cfgCmds []string
	for _, a := range h.exec.argv() {
		if strings.HasPrefix(a, "kubectl config") {
			cfgCmds = append(cfgCmds, a)
		}
	}
	if len(cfgCmds) != 7 || !strings.Contains(cfgCmds[0], "set-cluster prod/main") ||
		cfgCmds[6] != "kubectl config use-context prod/main --kubeconfig "+target {
		t.Errorf("merge commands:\n%s", strings.Join(cfgCmds, "\n"))
	}
	if !strings.Contains(cfgCmds[1], "--exec-command=/bin/mek") {
		t.Errorf("credentials: %s", cfgCmds[1])
	}
	if _, err := os.Stat(filepath.Dir(target)); err != nil {
		t.Errorf("target dir not created: %v", err)
	}

	os.WriteFile(target, []byte("mine"), 0o600) // the user's file is backed up first
	h.mustRun("-c", "prod", "kube", "--merge")
	if readFile(t, target+".mek-backup") != "mine" {
		t.Error("no backup")
	}

	h.exec.respond = func(argv []string) (string, int) {
		if len(argv) > 2 && argv[2] == "set-credentials" {
			return "", 1
		}
		return cloudAPI(argv)
	}
	_, err := h.run("-c", "prod", "kube", "--merge")
	wantErr(t, err, "kubectl config set-credentials: exit status 1")
	h.exec.respond = cloudAPI

	_, err = h.run("-c", "bare", "kube", "--name", "badca", "--merge")
	wantErr(t, err, "cluster badca: certificate-authority-data")

	t.Setenv("KUBECONFIG", filepath.Dir(target)) // a directory can't be backed up
	_, err = h.run("-c", "prod", "kube", "--merge")
	if err == nil {
		t.Error("backup error not reported")
	}
	t.Setenv("KUBECONFIG", filepath.Join(target, "under-a-file", "config"))
	_, err = h.run("-c", "prod", "kube", "--merge")
	if err == nil {
		t.Error("mkdir error not reported")
	}
	t.Setenv("KUBECONFIG", target)
	t.Setenv("TMPDIR", filepath.Join(target, "no-tmp"))
	_, err = h.run("-c", "prod", "kube", "--merge")
	if err == nil {
		t.Error("temp dir error not reported")
	}
}

func TestKubeMergeTargetError(t *testing.T) {
	h := newKubeHarness(t)
	old := userHome
	userHome = func() (string, error) { return "", errors.New("no home") }
	t.Cleanup(func() { userHome = old })
	_, err := h.run("-c", "prod", "kube", "--merge")
	wantErr(t, err, "no home")
	_, err = h.run("-c", "prod", "kube", "--unmerge")
	wantErr(t, err, "no home")
}

func TestKubeUnmerge(t *testing.T) {
	h := newKubeHarness(t)
	t.Setenv("KUBECONFIG", "/u/config")
	contexts := "prod/main\nprod/data\nother/x\nprodx/y\n"
	failUser := false
	h.exec.respond = func(argv []string) (string, int) {
		switch {
		case argv[2] == "get-contexts":
			return contexts, 0
		case failUser && argv[2] == "delete-user":
			return "", 1
		}
		return "", 0
	}

	h.mustRun("-c", "prod", "kube", "--unmerge")
	got := h.exec.argv()
	if len(got) != 7 || got[0] != "kubectl config get-contexts -o name --kubeconfig /u/config" ||
		got[1] != "kubectl config delete-context prod/main --kubeconfig /u/config" || got[6] != "kubectl config delete-user prod/data --kubeconfig /u/config" {
		t.Errorf("unmerge:\n%s", strings.Join(got, "\n"))
	}
	if _, err := os.Stat(audit.Path()); !os.IsNotExist(err) {
		t.Error("unmerge talks to no cloud: nothing to audit")
	}

	h.exec.invs = nil
	h.mustRun("-c", "prod", "kube", "data", "--unmerge")
	if got := h.exec.argv(); len(got) != 4 || !strings.Contains(got[1], "prod/data") {
		t.Errorf("unmerge one:\n%s", strings.Join(got, "\n"))
	}

	h.exec.invs = nil
	h.mustRun("-c", "g", "kube", "--unmerge") // nothing merged
	if got := h.exec.argv(); len(got) != 1 {
		t.Errorf("nothing to unmerge: %q", got)
	}

	failUser = true
	_, err := h.run("-c", "prod", "kube", "--unmerge")
	wantErr(t, err, "2 kubectl config command(s) failed")

	contexts = ""
	h.exec.respond = func([]string) (string, int) { return "", 1 }
	_, err = h.run("-c", "prod", "kube", "--unmerge")
	wantErr(t, err, "exit status 1")
	_, err = h.run("-c", "nope", "kube", "--unmerge")
	wantErr(t, err, "unknown context")
}

func TestKubeconfigEnv(t *testing.T) {
	h := newKubeHarness(t)
	mine := kube.Path("prod")
	out := h.mustRun("use", "--shell", "prod")
	if out != "export MEK_CONTEXT=prod\nexport KUBECONFIG='"+mine+"'\n" {
		t.Errorf("use --shell prod: %q", out)
	}
	// Switching to a context without clusters drops mek's KUBECONFIG...
	t.Setenv("KUBECONFIG", mine)
	if out := h.mustRun("use", "--shell", "bare"); out != "export MEK_CONTEXT=bare\nunset KUBECONFIG\n" {
		t.Errorf("use --shell bare: %q", out)
	}
	if out := h.mustRun("env", "bare"); !strings.Contains(out, "unset KUBECONFIG") {
		t.Errorf("env bare: %q", out)
	}
	// ...but never one the user set.
	t.Setenv("KUBECONFIG", "/u/.kube/config")
	if out := h.mustRun("use", "--shell", "bare"); out != "export MEK_CONTEXT=bare\n" {
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
