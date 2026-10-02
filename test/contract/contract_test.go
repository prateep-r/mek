//go:build contract

// Contract tests run mek with the REAL cloud CLIs (no cloud account needed):
// they prove each CLI understands what mek hands it — the generated AWS
// config, gcloud and az config dirs, KooCLI flags — using only offline,
// read-only commands. Run with `make test-contract`.
//
// A missing CLI skips its test, unless MEK_CONTRACT_REQUIRE=1 (CI) makes it
// fail. The Huawei test also needs MEK_CONTRACT_HCLOUD=1, because KooCLI
// always writes ~/.hcloud of the real user, ignoring $HOME: only enable it
// on a throwaway machine (CI runner, container).
package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/test/testkit"
)

var mek string

func TestMain(m *testing.M) { os.Exit(testkit.Main(m, &mek, "")) }

// realCLI finds the installed CLI, resolving asdf shims (which need the
// user's real HOME) to the binary behind them, or skips the test.
func realCLI(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv("MEK_CONTRACT_REQUIRE") != "" {
			t.Fatalf("%s is not installed", name)
		}
		t.Skipf("%s is not installed", name)
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

// user is an isolated user: their own MEK_HOME and HOME, and a PATH with
// the real CLIs' directories (not symlinks: wrappers like Debian's az find
// their runtime relative to their own location) plus the system dirs.
type user struct {
	t             *testing.T
	mekHome, home string
	vars          []string
}

func newUser(t *testing.T, config string, clis []string, extra ...string) *user {
	t.Helper()
	u := &user{t: t, mekHome: t.TempDir(), home: t.TempDir()}
	var path []string
	for _, cli := range clis {
		path = append(path, filepath.Dir(cli))
	}
	os.WriteFile(filepath.Join(u.mekHome, "config.yaml"), []byte(config), 0o600)
	u.vars = append([]string{"MEK_HOME=" + u.mekHome, "HOME=" + u.home,
		"PATH=" + strings.Join(append(path, "/usr/bin", "/bin"), ":")}, extra...)
	return u
}

// run runs mek and returns stdout+stderr (CLIs disagree on which they use).
func (u *user) run(args ...string) (string, int) {
	u.t.Helper()
	r := testkit.Run(u.t, mek, u.vars, args...)
	return strings.TrimSpace(r.Stdout + r.Stderr), r.Code
}

func (u *user) want(substr string, args ...string) {
	u.t.Helper()
	if out, _ := u.run(args...); !strings.Contains(out, substr) {
		u.t.Errorf("mek %s: output does not contain %q:\n%s", strings.Join(args, " "), substr, out)
	}
}

// homeUntouched fails if a CLI wrote its default config under HOME.
func (u *user) homeUntouched(dirs ...string) {
	u.t.Helper()
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(u.home, d)); err == nil {
			u.t.Errorf("CLI wrote to ~/%s instead of the context's own config", d)
		}
	}
}

func TestAWSCLIReadsGeneratedConfig(t *testing.T) {
	u := newUser(t, `contexts:
  uat: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Dev, region: ap-southeast-1}
`, []string{realCLI(t, "aws")},
		"AWS_ACCESS_KEY_ID=AKIALEAKEDKEY0000000", "AWS_SECRET_ACCESS_KEY=leaked")

	for key, want := range map[string]string{
		"sso_account_id": "111122223333", "sso_role_name": "Dev", "sso_session": "mek-acme", "region": "ap-southeast-1",
	} {
		if out, code := u.run("-c", "uat", "aws", "configure", "get", key); code != 0 || out != want {
			t.Errorf("aws configure get %s = %q (exit %d), want %q", key, out, code, want)
		}
	}
	out, _ := u.run("-c", "uat", "aws", "configure", "list")
	if !strings.Contains(out, "mek-uat") {
		t.Errorf("aws configure list should use profile mek-uat:\n%s", out)
	}
	if strings.Contains(out, "0000") { // last chars of the leaked key would show if it were used
		t.Errorf("the AWS CLI saw the leaked AWS_ACCESS_KEY_ID:\n%s", out)
	}
	u.homeUntouched(".aws/config")
}

func TestGcloudUsesContextConfigDir(t *testing.T) {
	gcloud := realCLI(t, "gcloud")
	var extra []string
	if py := os.Getenv("CLOUDSDK_PYTHON"); py != "" {
		extra = append(extra, "CLOUDSDK_PYTHON="+py)
	} else if py, err := exec.LookPath("python3"); err == nil { // gcloud needs a modern Python
		if strings.Contains(py, "/.asdf/shims/") {
			py = realCLI(t, "python3")
		}
		extra = append(extra, "CLOUDSDK_PYTHON="+py)
	}
	u := newUser(t, `contexts:
  gcp: {provider: gcp, project: acme-dev, region: asia-southeast1}
`, []string{gcloud}, append(extra, "GOOGLE_APPLICATION_CREDENTIALS=/tmp/leaked.json")...)

	u.want("acme-dev", "-c", "gcp", "gcloud", "config", "get", "core/project")
	u.want("asia-southeast1", "-c", "gcp", "gcloud", "config", "get", "compute/region")
	u.want(filepath.Join(u.mekHome, "gcloud", "gcp"), "-c", "gcp", "gcloud", "info", "--format=value(config.paths.global_config_dir)")
	if out, _ := u.run("-c", "gcp", "gcloud", "auth", "list", "--format=value(account)"); strings.Contains(out, "@") {
		t.Errorf("a fresh context must have no accounts (isolated from the user's gcloud):\n%s", out)
	}
	u.homeUntouched(".config/gcloud")
}

func TestAzureCLIUsesContextConfigDir(t *testing.T) {
	u := newUser(t, `contexts:
  az-dev: {provider: azure, tenant_id: acme.onmicrosoft.com, subscription_id: 00000000-1111-2222-3333-444444444444}
`, []string{realCLI(t, "az")}, "AZURE_CORE_COLLECT_TELEMETRY=no")

	if out, code := u.run("-c", "az-dev", "az", "config", "set", "core.output=table"); code != 0 {
		t.Fatalf("az config set: exit %d\n%s", code, out)
	}
	b, err := os.ReadFile(filepath.Join(u.mekHome, "azure", "az-dev", "config"))
	if err != nil || !strings.Contains(string(b), "output = table") {
		t.Errorf("az did not write the context's AZURE_CONFIG_DIR: %v\n%s", err, b)
	}
	// Not logged in to this context, whatever the user's own az state is.
	if out, code := u.run("-c", "az-dev", "az", "account", "show"); code == 0 || !strings.Contains(out, "az login") {
		t.Errorf("az account show in a fresh context should ask for az login (exit %d):\n%s", code, out)
	}
	u.homeUntouched(".azure")
}

func TestKooCLIAcceptsMekFlags(t *testing.T) {
	if os.Getenv("MEK_CONTRACT_HCLOUD") == "" {
		t.Skip("set MEK_CONTRACT_HCLOUD=1 on a throwaway machine: KooCLI writes the real ~/.hcloud")
	}
	hcloud := realCLI(t, "hcloud")
	// KooCLI setup a user does once: accept the privacy statement, create profiles.
	for _, args := range [][]string{
		{"configure", "set", "--cli-agree-privacy-statement=true"},
		{"configure", "set", "--cli-profile=mek-keys", "--cli-mode=AKSK", "--cli-region=ap-southeast-2", "--cli-access-key=AKFAKE", "--cli-secret-key=SKFAKE"},
	} {
		if out, err := exec.Command(hcloud, args...).CombinedOutput(); err != nil {
			t.Fatalf("hcloud %v: %v\n%s", args, err, out)
		}
	}
	u := newUser(t, `contexts:
  hw:      {provider: huawei, hcloud_profile: mek-keys, region: ap-southeast-2}
  hw-gone: {provider: huawei, hcloud_profile: mek-no-such-profile}
`, []string{hcloud})

	// KooCLI honours the --cli-profile mek appends after the operation...
	u.want("Profile mek-no-such-profile does not exist", "-c", "hw-gone", "hcloud", "ECS", "ListServersDetails")
	// ...and rejects it on its own commands, which is why mek leaves those alone.
	if out, _ := u.run("-c", "hw", "hcloud", "version"); strings.Contains(out, "Invalid parameter") || !strings.Contains(out, "KooCLI version") {
		t.Errorf("mek must not add flags to `hcloud version`:\n%s", out)
	}
	u.want(`"name": "mek-keys"`, "-c", "hw", "hcloud", "configure", "show", "--cli-profile=mek-keys")
	// mek reads KooCLI's real config format to decide how to log in.
	u.want("uses AKSK mode", "login", "hw")
}
