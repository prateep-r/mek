//go:build emulator

// Emulator tests run mek with the real cloud CLIs against Floci, a free local
// emulator of each cloud's APIs (floci.io): real API calls, no cloud account.
// They prove what stub CLIs can't — a command the guard blocks never reaches
// the API, a confirmed one really changes cloud state — on AWS, GCP and
// Azure. Huawei Cloud has no emulator. Run with `make test-emulator`.
//
// Each test starts its own Floci container on a random 127.0.0.1 port and
// removes it afterwards; it never touches an emulator already running on the
// machine. Set MEK_FLOCI_{AWS,GCP,AZ}_URL to use a running one instead.
// Without docker or a CLI the test is skipped, unless MEK_EMULATOR_REQUIRE=1
// (CI) makes that a failure.
package emulator

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateep-r/mek/test/testkit"
)

// Pinned so emulator upgrades never change results on their own.
const (
	flociAWS = "floci/floci:2.1.0"
	flociGCP = "floci/floci-gcp:0.9.0"
	flociAz  = "floci/floci-az:0.13.0"
)

var mek string

func TestMain(m *testing.M) { os.Exit(testkit.Main(m, &mek, "")) }

func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("MEK_EMULATOR_REQUIRE") != "" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

// emulator returns the base URL of a Floci for image: $envVar if set, else a
// fresh container (removed when the test ends) on a random local port.
func emulator(t *testing.T, image string, port int, envVar string) string {
	t.Helper()
	if url := os.Getenv(envVar); url != "" {
		url = strings.TrimRight(url, "/")
		waitUp(t, url, func() string { return "" }) // e.g. just started by docker compose
		return url
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		skipOrFail(t, "docker is not available: %v", err)
	}
	name := fmt.Sprintf("mek-test-%s-%d", strings.NewReplacer("/", "-", ":", "-").Replace(image), time.Now().UnixNano())
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1::%d", port), image).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s: %v\n%s", image, err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	hostPort, err := exec.Command("docker", "port", name, fmt.Sprintf("%d/tcp", port)).Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	url := "http://" + strings.TrimSpace(strings.Split(string(hostPort), "\n")[0])
	waitUp(t, url, func() string {
		logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
		return string(logs)
	})
	return url
}

// waitUp waits until the emulator at url answers HTTP; logs explains a failure.
func waitUp(t *testing.T, url string, logs func() string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if resp, err := http.Get(url + "/"); err == nil {
			resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("emulator at %s did not answer within 30s\n%s", url, logs())
		}
	}
}

// realCLI finds an installed CLI, resolving asdf shims (they need the real
// HOME) to the binary behind them, or skips.
func realCLI(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		skipOrFail(t, "%s is not installed", name)
	}
	if strings.Contains(p, "/.asdf/shims/") {
		out, err := exec.Command("asdf", "which", name).Output()
		if err != nil {
			t.Fatalf("asdf which %s: %v", name, err)
		}
		p = strings.TrimSpace(string(out))
	}
	return p
}

// user is an isolated user with their own MEK_HOME, HOME and PATH.
type user struct {
	t             *testing.T
	mekHome, home string
	vars          []string
}

func newUser(t *testing.T, config string, cli string, extra ...string) *user {
	t.Helper()
	u := &user{t: t, mekHome: t.TempDir(), home: t.TempDir()}
	os.WriteFile(filepath.Join(u.mekHome, "config.yaml"), []byte(config), 0o600)
	u.vars = append([]string{"MEK_HOME=" + u.mekHome, "HOME=" + u.home,
		"PATH=" + filepath.Dir(cli) + ":/usr/bin:/bin"}, extra...)
	return u
}

func (u *user) run(args ...string) testkit.Result {
	u.t.Helper()
	return testkit.Run(u.t, mek, u.vars, args...)
}

func (u *user) ok(args ...string) string {
	u.t.Helper()
	r := u.run(args...)
	if r.Code != 0 {
		u.t.Fatalf("mek %s: exit %d\n%s%s", strings.Join(args, " "), r.Code, r.Stdout, r.Stderr)
	}
	return r.Stdout + r.Stderr
}

func (u *user) blocked(args ...string) {
	u.t.Helper()
	if r := u.run(args...); r.Code == 0 || !strings.Contains(r.Stderr, "readonly — blocked") {
		u.t.Fatalf("mek %s should be blocked: exit %d\n%s", strings.Join(args, " "), r.Code, r.Stderr)
	}
}

func (u *user) audit() string {
	b, _ := os.ReadFile(filepath.Join(u.mekHome, "audit.jsonl"))
	return string(b)
}

// AWS: contexts reuse a profile (static test keys + the emulator endpoint),
// since Floci's SSO login can't yet hand out role credentials (see README).
func TestAWS(t *testing.T) {
	aws := realCLI(t, "aws")
	url := emulator(t, flociAWS, 4566, "MEK_FLOCI_AWS_URL")
	u := newUser(t, `contexts:
  dev:  {provider: aws, aws_profile: floci, region: ap-southeast-1}
  ro:   {provider: aws, aws_profile: floci, readonly: true}
  prod: {provider: aws, aws_profile: floci, protected: true}
`, aws, "AWS_ACCESS_KEY_ID=leaked-should-be-removed")
	os.MkdirAll(filepath.Join(u.home, ".aws"), 0o700)
	os.WriteFile(filepath.Join(u.home, ".aws", "config"), []byte("[profile floci]\nregion = ap-southeast-1\nendpoint_url = "+url+"\n"), 0o600)
	os.WriteFile(filepath.Join(u.home, ".aws", "credentials"), []byte("[floci]\naws_access_key_id = test\naws_secret_access_key = test\n"), 0o600)

	// The call reaches the emulator with the profile's keys, not the leaked env key.
	if out := u.ok("-c", "dev", "aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text"); !strings.Contains(out, "000000000000") {
		t.Errorf("sts get-caller-identity: %s", out)
	}
	u.ok("-c", "dev", "aws", "s3", "mb", "s3://mek-e2e")
	u.blocked("-c", "ro", "aws", "s3", "rb", "s3://mek-e2e")
	if out := u.ok("-c", "ro", "aws", "s3", "ls"); !strings.Contains(out, "mek-e2e") { // reads are fine
		t.Fatalf("the blocked rb reached the API: bucket is gone\n%s", out)
	}
	if r := u.run("-c", "prod", "aws", "s3", "rb", "s3://mek-e2e"); r.Code == 0 {
		t.Fatal("protected: destructive without --confirm must not run")
	}
	u.ok("-c", "prod", "--confirm", "prod", "aws", "s3", "rb", "s3://mek-e2e")
	if out := u.ok("-c", "dev", "aws", "s3", "ls"); strings.Contains(out, "mek-e2e") {
		t.Errorf("confirmed rb did not delete the bucket:\n%s", out)
	}
	// An error from the real API reaches the user with the CLI's exit code.
	if r := u.run("-c", "dev", "aws", "s3", "rb", "s3://mek-no-such-bucket"); r.Code == 0 || !strings.Contains(r.Stderr, "NoSuchBucket") {
		t.Errorf("API error: exit %d stderr %q", r.Code, r.Stderr)
	}
}

func TestGCP(t *testing.T) {
	gcloud := realCLI(t, "gcloud")
	url := emulator(t, flociGCP, 4588, "MEK_FLOCI_GCP_URL")
	extra := []string{"CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE=" + url + "/", "CLOUDSDK_AUTH_ACCESS_TOKEN=floci"}
	if py := os.Getenv("CLOUDSDK_PYTHON"); py != "" {
		extra = append(extra, "CLOUDSDK_PYTHON="+py)
	} else if py, err := exec.LookPath("python3"); err == nil && strings.Contains(py, "/.asdf/shims/") {
		extra = append(extra, "CLOUDSDK_PYTHON="+realCLI(t, "python3")) // gcloud needs a modern Python
	}
	u := newUser(t, `contexts:
  dev: {provider: gcp, project: mek-e2e}
  ro:  {provider: gcp, project: mek-e2e, readonly: true}
`, gcloud, extra...)

	u.ok("-c", "dev", "gcloud", "storage", "buckets", "create", "gs://mek-e2e")
	u.blocked("-c", "ro", "gcloud", "storage", "rm", "--recursive", "gs://mek-e2e")
	u.blocked("-c", "ro", "gcloud", "storage", "buckets", "delete", "gs://mek-e2e")
	if out := u.ok("-c", "ro", "gcloud", "storage", "ls"); !strings.Contains(out, "gs://mek-e2e/") {
		t.Fatalf("the blocked delete reached the API: bucket is gone\n%s", out)
	}
	u.ok("-c", "dev", "gcloud", "storage", "buckets", "delete", "gs://mek-e2e")
	if r := u.run("-c", "dev", "gcloud", "storage", "ls"); strings.Contains(r.Stdout, "gs://mek-e2e/") {
		t.Errorf("bucket still listed after delete:\n%s", r.Stdout)
	}
}

func TestAzure(t *testing.T) {
	az := realCLI(t, "az")
	url := emulator(t, flociAz, 4577, "MEK_FLOCI_AZ_URL")
	const key = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMh0=="
	conn := "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=" + key + ";BlobEndpoint=" + url + "/devstoreaccount1;"
	u := newUser(t, `contexts:
  dev: {provider: azure, tenant_id: t, subscription_id: 00000000-1111-2222-3333-444444444444}
  ro:  {provider: azure, tenant_id: t, subscription_id: 00000000-1111-2222-3333-444444444444, readonly: true}
`, az, "AZURE_CORE_COLLECT_TELEMETRY=no")
	list := func(ctx string) string {
		return u.ok("-c", ctx, "az", "storage", "container", "list", "--connection-string", conn, "--query", "[].name", "-o", "tsv")
	}

	u.ok("-c", "dev", "az", "storage", "container", "create", "-n", "mek-e2e", "--connection-string", conn)
	u.blocked("-c", "ro", "az", "storage", "container", "delete", "-n", "mek-e2e", "--connection-string", conn)
	if !strings.Contains(list("ro"), "mek-e2e") {
		t.Fatal("the blocked delete reached the API: container is gone")
	}
	u.ok("-c", "dev", "az", "storage", "container", "delete", "-n", "mek-e2e", "--connection-string", conn)
	if strings.Contains(list("dev"), "mek-e2e") {
		t.Error("container still listed after delete")
	}
	if strings.Contains(u.audit(), "Eby8vdM02x") {
		t.Error("the storage account key leaked into the audit log")
	}
}
