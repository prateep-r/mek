// Package testkit holds helpers shared by mek's integration and e2e tests:
// building the real binary, running it the way a user's shell would, and
// stub cloud CLIs that record how mek called them.
package testkit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// ModuleRoot is the repository root (where go.mod lives).
func ModuleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out))), nil
}

// CoverDir is where coverage-instrumented mek binaries write their data
// ($MEK_COVERDIR, set by `make cover`), or "" when not measuring.
func CoverDir() string { return os.Getenv("MEK_COVERDIR") }

// Build compiles ./cmd/mek into dir with ldflags (call it from TestMain).
// When CoverDir is set the binary is built with -cover, so its runs add to
// the combined coverage report.
func Build(dir, ldflags string) (string, error) {
	root, err := ModuleRoot()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "mek")
	args := []string{"build", "-trimpath", "-ldflags", ldflags, "-o", bin}
	if CoverDir() != "" {
		args = append(args, "-cover", "-coverpkg=./cmd/...,./internal/...")
	}
	cmd := exec.Command("go", append(args, "./cmd/mek")...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, out)
	}
	return bin, nil
}

// Main builds mek into a temp dir, runs the tests, and cleans up; use it as
// `func TestMain(m *testing.M) { os.Exit(testkit.Main(m, &mek, ldflags)) }`.
func Main(m *testing.M, bin *string, ldflags string) int {
	dir, err := os.MkdirTemp("", "mek-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	if *bin, err = Build(dir, ldflags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// Result is one finished command.
type Result struct {
	Code           int
	Stdout, Stderr string
}

// Command prepares bin with args and env (added to a minimal base env). It
// runs in a new session, so like a CI job it has no controlling terminal and
// mek's confirmation prompts fail closed instead of waiting for input.
func Command(bin string, env []string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	cmd.Env = append([]string{"HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH"), "NO_COLOR=1"}, env...)
	if d := CoverDir(); d != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+d)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// RunTimeout bounds every Run.
var RunTimeout = 3 * time.Minute

// Run runs bin and waits for it.
func Run(t testing.TB, bin string, env []string, args ...string) Result {
	t.Helper()
	cmd := Command(bin, env, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("run %s %v: %v", bin, args, err)
	}
	// A hung child fails its own test, named, instead of the whole run
	// timing out: kill its process group (Command starts a new session).
	timer := time.AfterFunc(RunTimeout, func() {
		t.Errorf("run %s %v: still running after %s, killed", bin, args, RunTimeout)
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	err := cmd.Wait()
	timer.Stop()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatalf("run %s %v: %v", bin, args, err)
	}
	return Result{Code: cmd.ProcessState.ExitCode(), Stdout: stdout.String(), Stderr: stderr.String()}
}

// Call is one recorded invocation of a stub CLI.
type Call struct {
	Name string
	Args []string
	Env  map[string]string
}

// ArgLine is the call's arguments joined by spaces.
func (c Call) ArgLine() string { return strings.Join(c.Args, " ") }

// stub records "name\037arg\037arg...\n" followed by `env` and a \036 line,
// then prints $STUB_OUT and exits with $STUB_EXIT (default 0). A mkdir lock
// keeps records whole when mek runs several stubs at once (mek doctor).
const stub = `#!/bin/sh
until mkdir "$STUB_LOG.lock" 2>/dev/null; do sleep 0.01; done
{
  printf '%s' "$(basename "$0")"
  for a in "$@"; do printf '\037%s' "$a"; done
  printf '\n'
  env
  printf '\036\n'
} >> "$STUB_LOG"
rmdir "$STUB_LOG.lock"
[ -n "$STUB_OUT" ] && printf '%s\n' "$STUB_OUT"
exit "${STUB_EXIT:-0}"
`

// Stubs writes recording fake CLIs named names into a new directory and
// returns it (put it first on PATH) and the log file they append to (pass it
// to the stubs as STUB_LOG).
func Stubs(t testing.TB, names ...string) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, filepath.Join(t.TempDir(), "calls.log")
}

// Calls parses a stub log; a missing log means no calls.
func Calls(t testing.TB, log string) []Call {
	t.Helper()
	b, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []Call
	for _, rec := range strings.Split(string(b), "\036\n") {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		head, envText, _ := strings.Cut(rec, "\n")
		parts := strings.Split(head, "\037")
		c := Call{Name: parts[0], Args: parts[1:], Env: map[string]string{}}
		for _, kv := range strings.Split(envText, "\n") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				c.Env[k] = v
			}
		}
		calls = append(calls, c)
	}
	return calls
}

var (
	listenstubOnce sync.Once
	listenstubPath string
	listenstubErr  error
)

// Listenstub builds cmd/listenstub (a fake tunnel CLI that really listens)
// once per test binary and returns its path.
func Listenstub(t testing.TB) string {
	t.Helper()
	listenstubOnce.Do(func() {
		root, err := ModuleRoot()
		if err != nil {
			listenstubErr = err
			return
		}
		dir, err := os.MkdirTemp("", "listenstub")
		if err != nil {
			listenstubErr = err
			return
		}
		listenstubPath = filepath.Join(dir, "listenstub")
		cmd := exec.Command("go", "build", "-o", listenstubPath, "./test/testkit/cmd/listenstub")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			listenstubErr = fmt.Errorf("go build listenstub: %w\n%s", err, out)
		}
	})
	if listenstubErr != nil {
		t.Fatal(listenstubErr)
	}
	return listenstubPath
}
