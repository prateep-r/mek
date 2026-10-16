//go:build emulator

// The emulator tests run mek with the real aws CLI against Floci, a free
// local emulator of AWS's APIs (floci.io), for what stub CLIs can't show:
// the whole IAM Identity Center login (device code, approval, role
// credentials through the profile mek generates), and the config wizard on
// a real terminal listing that login's accounts and roles and the account's
// instances, databases and clusters. Run with `make test-emulator`.
//
// It starts its own Floci container on a random 127.0.0.1 port and removes
// it afterwards; it never touches an emulator already running on the
// machine. Set MEK_FLOCI_AWS_URL to use a running one instead.
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
func emulator(t *testing.T, image string, port int, envVar string, dockerEnv ...string) string {
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
	args := []string{"run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1::%d", port)}
	for _, e := range dockerEnv {
		args = append(args, "-e", e)
	}
	out, err := exec.Command("docker", append(args, image)...).CombinedOutput()
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
