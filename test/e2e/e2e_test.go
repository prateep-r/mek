//go:build e2e

// End-to-end tests: build release artifacts exactly as GoReleaser publishes
// them, serve them over HTTP, install with the real install.sh, then use the
// installed binary the way a new user would. Run with `make test-e2e`.
package e2e

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/prateep-r/mek/test/testkit"
)

const releaseVersion = "v9.9.9-e2e"

var mek string // the freshly built binary that goes into the release archive

func TestMain(m *testing.M) {
	os.Exit(testkit.Main(m, &mek, "-s -w -X github.com/prateep-r/mek/internal/version.Version="+releaseVersion))
}

var asset = fmt.Sprintf("mek_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH) // .goreleaser.yaml name_template

// archive packs mek and README.md like GoReleaser's archives section.
func archive(t *testing.T) []byte {
	t.Helper()
	bin, err := os.ReadFile(mek)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, file := range []struct {
		name string
		mode int64
		body []byte
	}{{"README.md", 0o644, []byte("# mek\n")}, {"mek", 0o755, bin}} {
		tw.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.body)), Typeflag: tar.TypeReg})
		tw.Write(file.body)
	}
	tw.Close()
	gz.Close()
	f.Close()
	b, _ := os.ReadFile(f.Name())
	return b
}

func checksums(files map[string][]byte) []byte {
	var b strings.Builder
	for name, body := range files {
		sum := sha256.Sum256(body)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return []byte(b.String())
}

// serve publishes files the way a release download URL serves them.
func serve(t *testing.T, files map[string][]byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// install runs the repository's install.sh against url into dir.
func install(t *testing.T, url, dir string) testkit.Result {
	t.Helper()
	root, err := testkit.ModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	return testkit.Run(t, "/bin/sh", []string{"MEK_DOWNLOAD_URL=" + url, "MEK_INSTALL_DIR=" + dir, "HOME=" + t.TempDir()},
		filepath.Join(root, "install.sh"))
}

func TestInstallScript(t *testing.T) {
	good := archive(t)
	sums := checksums(map[string][]byte{asset: good})

	t.Run("installs and verifies", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "bin")
		r := install(t, serve(t, map[string][]byte{asset: good, "checksums.txt": sums}), dir)
		if r.Code != 0 || !strings.Contains(r.Stdout, "checksum ok") || !strings.Contains(r.Stdout, releaseVersion) {
			t.Fatalf("install: %+v", r)
		}
		if fi, err := os.Stat(filepath.Join(dir, "mek")); err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("installed binary: %v %v", fi, err)
		}
		if !strings.Contains(r.Stdout, "is not in your PATH") {
			t.Error("install.sh should tell the user to add the dir to PATH")
		}
	})

	refused := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"tampered archive", map[string][]byte{asset: append(append([]byte{}, good...), 0), "checksums.txt": sums}, "checksum verification FAILED"},
		// Regression: macOS's sha256sum -c exits 0 when no line matches, which
		// used to install an unverified binary.
		{"archive not in checksums", map[string][]byte{asset: good, "checksums.txt": checksums(map[string][]byte{"other.tar.gz": good})}, "not listed in checksums.txt"},
		{"empty checksums", map[string][]byte{asset: good, "checksums.txt": nil}, "not listed in checksums.txt"},
		{"archive missing", map[string][]byte{"checksums.txt": sums}, ""},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "bin")
			r := install(t, serve(t, c.files), dir)
			if r.Code == 0 || !strings.Contains(r.Stderr, c.want) {
				t.Errorf("install should fail with %q: %+v", c.want, r)
			}
			if _, err := os.Stat(filepath.Join(dir, "mek")); err == nil {
				t.Error("nothing may be installed when verification fails")
			}
		})
	}
}

// A new user's first session on all four clouds, using only the installed
// binary and stub CLIs.
func TestUserJourney(t *testing.T) {
	installed := installRelease(t)

	mekHome, home := t.TempDir(), t.TempDir()
	stubs, log := testkit.Stubs(t, "aws", "gcloud", "az", "hcloud")
	hcloudConfig := filepath.Join(home, ".hcloud", "config.json")
	vars := []string{"MEK_HOME=" + mekHome, "HOME=" + home, "PATH=" + stubs + ":/usr/bin:/bin", "STUB_LOG=" + log, "STUB_OUT=stub 1.0",
		"MEK_HCLOUD_CONFIG=" + hcloudConfig}
	step := func(wantCode int, args ...string) testkit.Result {
		t.Helper()
		r := testkit.Run(t, installed, vars, args...)
		if r.Code != wantCode {
			t.Fatalf("mek %s: exit %d (want %d)\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.Code, wantCode, r.Stdout, r.Stderr)
		}
		return r
	}

	// 1. First contact: help, version, doctor tells them to init.
	if r := step(0, "--help"); !strings.Contains(r.Stdout, "az") || !strings.Contains(r.Stdout, "hcloud") {
		t.Errorf("help should list every cloud CLI:\n%s", r.Stdout)
	}
	if r := step(0, "version"); !strings.Contains(r.Stdout, releaseVersion) {
		t.Errorf("version: %s", r.Stdout)
	}
	if r := step(1, "doctor"); !strings.Contains(r.Stdout, "mek init") {
		t.Errorf("doctor before init:\n%s", r.Stdout)
	}

	// 2. Configure: init writes the example, the user replaces it with theirs.
	step(0, "init")
	cfg := `contexts:
  dev:    {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Dev}
  prod:   {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "444455556666", role: Admin, protected: true}
  gcp:    {provider: gcp, project: acme-dev, region: asia-southeast1}
  az-dev: {provider: azure, tenant_id: acme.onmicrosoft.com, subscription_id: 00000000-1111-2222-3333-444444444444}
  hw:     {provider: huawei, hcloud_profile: acme-sso, region: ap-southeast-2}
`
	os.WriteFile(filepath.Join(mekHome, "config.yaml"), []byte(cfg), 0o600)
	// The Huawei user created their KooCLI SSO profile beforehand.
	os.MkdirAll(filepath.Dir(hcloudConfig), 0o700)
	os.WriteFile(hcloudConfig, []byte(`{"profiles":[{"name":"acme-sso","mode":"SSO"}]}`), 0o600)
	if r := step(0, "doctor"); !strings.Contains(r.Stdout, "all good") {
		t.Errorf("doctor with CLIs present:\n%s", r.Stdout)
	}

	// 3. Log in to each cloud, switch, work.
	for _, ctx := range []string{"dev", "gcp", "az-dev", "hw"} {
		step(0, "login", ctx)
	}
	step(0, "use", "dev")
	if r := step(0, "ctx", "ls"); !strings.Contains(r.Stdout, "* dev") || !strings.Contains(r.Stdout, "[protected]") {
		t.Errorf("ctx ls:\n%s", r.Stdout)
	}
	step(0, "aws", "s3", "ls")
	step(0, "-c", "gcp", "gcloud", "compute", "instances", "list")
	step(0, "-c", "az-dev", "az", "group", "list")
	step(0, "-c", "hw", "hcloud", "ECS", "ListServersDetails")

	// 4. Prod guard: blocked without confirmation, runs with it.
	if r := step(1, "-c", "prod", "aws", "ec2", "terminate-instances", "--instance-ids", "i-1"); !strings.Contains(r.Stderr, "--confirm prod") {
		t.Errorf("guard message: %s", r.Stderr)
	}
	step(0, "-c", "prod", "--confirm", "prod", "aws", "ec2", "terminate-instances", "--instance-ids", "i-1")

	// 5. What the CLIs saw. doctor (run twice) probes every CLI in parallel,
	// so its version calls are checked as a set; the rest are in order.
	var probes, got []string
	for _, c := range testkit.Calls(t, log) {
		line := c.Name + " " + c.ArgLine()
		if strings.Contains(c.ArgLine(), "version") {
			probes = append(probes, line)
			continue
		}
		got = append(got, line+" @"+c.Env["MEK_CONTEXT"])
	}
	slices.Sort(probes)
	wantProbes := []string{"aws --version", "aws --version", "az version --output tsv --query \"azure-cli\"", "az version --output tsv --query \"azure-cli\"",
		"gcloud --version", "gcloud --version", "hcloud version", "hcloud version"}
	if !slices.Equal(probes, wantProbes) {
		t.Errorf("doctor probes: %q", probes)
	}
	want := []string{
		"aws sso login --profile mek-dev @dev",
		"aws sts get-caller-identity --output table @dev",
		"gcloud auth login @gcp",
		"gcloud auth list --filter=status:ACTIVE --format=value(account) @gcp",
		"az login --tenant acme.onmicrosoft.com @az-dev",
		"az account set --subscription 00000000-1111-2222-3333-444444444444 @az-dev",
		"az account show --output table @az-dev",
		"hcloud configure sso --cli-profile=acme-sso @hw",
		"hcloud configure show --cli-profile=acme-sso @hw",
		"aws s3 ls @dev",
		"gcloud compute instances list @gcp",
		"az group list @az-dev",
		"hcloud ECS ListServersDetails --cli-profile=acme-sso --cli-region=ap-southeast-2 @hw",
		"aws ec2 terminate-instances --instance-ids i-1 @prod",
	}
	if !slices.Equal(got, want) {
		t.Errorf("CLI calls:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	// 6. The audit log kept the guarded commands, blocked ones included.
	audit, _ := os.ReadFile(filepath.Join(mekHome, "audit.jsonl"))
	if n := strings.Count(string(audit), "\n"); n != 6 {
		t.Errorf("audit entries = %d, want 6 (four clouds + blocked and confirmed terminate):\n%s", n, audit)
	}
	for _, want := range []string{`"provider":"gcp"`, `"provider":"azure"`, `"provider":"huawei"`, `"decision":"blocked"`, `"decision":"confirmed"`} {
		if !strings.Contains(string(audit), want) {
			t.Errorf("audit missing %s", want)
		}
	}
}

// installRelease builds the release artifacts, installs them with install.sh
// and returns the installed binary.
func installRelease(t *testing.T) string {
	t.Helper()
	good := archive(t)
	bin := filepath.Join(t.TempDir(), "bin")
	if r := install(t, serve(t, map[string][]byte{asset: good, "checksums.txt": checksums(map[string][]byte{asset: good})}), bin); r.Code != 0 {
		t.Fatalf("install: %+v", r)
	}
	return filepath.Join(bin, "mek")
}

// A user adds a cluster, generates its kubeconfig with the installed mek and
// uses kubectl through it; the kubeconfig runs the installed binary.
func TestKubeJourney(t *testing.T) {
	installed := installRelease(t)
	mekHome, home := t.TempDir(), t.TempDir()
	stubs, log := testkit.Stubs(t, "aws", "kubectl")
	os.WriteFile(filepath.Join(mekHome, "config.yaml"), []byte(`contexts:
  prod: {provider: aws, aws_profile: admin, region: ap-southeast-1, protected: true, clusters: {main: {name: prod-eks}}}
`), 0o600)
	vars := []string{"MEK_HOME=" + mekHome, "HOME=" + home, "PATH=" + stubs + ":/usr/bin:/bin", "STUB_LOG=" + log,
		`STUB_OUT={"endpoint":"https://ABC.eks.amazonaws.com","ca":"Q0E=","status":"ACTIVE"}`}
	run := func(args ...string) testkit.Result { t.Helper(); return testkit.Run(t, installed, vars, args...) }

	if r := run("doctor"); r.Code != 0 || !strings.Contains(r.Stdout, "kubectl") {
		t.Errorf("doctor with kubectl present:\n%s%s", r.Stdout, r.Stderr)
	}
	r := run("-c", "prod", "kube")
	if r.Code != 0 {
		t.Fatalf("mek kube: %+v", r)
	}
	kc, _ := os.ReadFile(strings.TrimSpace(r.Stdout))
	if !strings.Contains(string(kc), "command: "+installed) {
		t.Errorf("kubeconfig should run the installed mek:\n%s", kc)
	}
	if r := run("-c", "prod", "kubectl", "get", "pods"); r.Code != 0 {
		t.Errorf("kubectl get: %+v", r)
	}
	if r := run("-c", "prod", "kubectl", "delete", "pod", "web-1"); r.Code == 0 || !strings.Contains(r.Stderr, "--confirm prod") {
		t.Errorf("protected delete must ask for --confirm: %+v", r)
	}
	var got []string
	for _, c := range testkit.Calls(t, log) {
		if !strings.Contains(c.ArgLine(), "version") {
			got = append(got, c.Name+" "+strings.Join(c.Args[:2], " "))
		}
	}
	if want := []string{"aws eks describe-cluster", "kubectl get pods"}; !slices.Equal(got, want) {
		t.Errorf("calls: %q, want %q", got, want)
	}
}
