// Package testkit holds helpers shared by mek's integration and e2e tests:
// building the real binary, running it the way a user's shell would, and
// stub cloud CLIs that record how mek called them.
package testkit

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prateep-r/mek/internal/proc"
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

// baseEnv is what every OS needs a process to have: on Windows, Go and the
// cloud CLIs need SystemRoot, PATHEXT, the temp and profile directories.
var baseEnv = []string{"HOME", "PATH", "SystemRoot", "PATHEXT", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "ComSpec"}

// Command prepares bin with args and env (added to a minimal base env). It
// runs detached (a new session; on Windows no console), so like a CI job it
// has no terminal and mek's confirmation prompts fail closed instead of
// waiting for input.
func Command(bin string, env []string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"NO_COLOR=1"}
	for _, k := range baseEnv {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	if d := CoverDir(); d != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+d)
	}
	proc.Detach(cmd)
	return cmd
}

// Exe is a program's file name on this OS (aws, or aws.exe on Windows).
func Exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
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
		proc.KillGroup(cmd.Process.Pid)
		proc.Kill(cmd.Process.Pid)
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

// Stubs puts recording fake CLIs (cmd/recordstub) named names into a new
// directory and returns it (put it first on PATH) and the log file they
// append to (pass it to the stubs as STUB_LOG); Calls reads it. Each stub
// prints $STUB_OUT and exits with $STUB_EXIT.
func Stubs(t testing.TB, names ...string) (dir, log string) {
	t.Helper()
	stub := Tool(t, "recordstub")
	dir = t.TempDir()
	for _, n := range names {
		if err := Install(stub, filepath.Join(dir, Exe(n))); err != nil {
			t.Fatal(err)
		}
	}
	return dir, filepath.Join(t.TempDir(), "calls.log")
}

// Install puts a copy of the program src at dst. A copy, not a link: tests
// overwrite stubs with scripts of their own.
func Install(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
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
	toolsMu sync.Mutex
	tools   = map[string]string{}
)

// Tool builds test/testkit/cmd/<name> once per test binary and returns its
// path.
func Tool(t testing.TB, name string) string {
	t.Helper()
	toolsMu.Lock()
	defer toolsMu.Unlock()
	if p, ok := tools[name]; ok {
		return p
	}
	root, err := ModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", name)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, Exe(name))
	cmd := exec.Command("go", "build", "-o", p, "./test/testkit/cmd/"+name)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", name, err, out)
	}
	tools[name] = p
	return p
}

// Listenstub is cmd/listenstub, a fake tunnel CLI that really listens.
func Listenstub(t testing.TB) string { return Tool(t, "listenstub") }

// FreePorts maps each fixed port number a test's strings mention to a port
// that is free on this machine right now, so a port another program happens
// to use can't fail the test: apply the replacer to the test's config and to
// the output it expects.
func FreePorts(t testing.TB, fixed ...string) *strings.Replacer {
	t.Helper()
	var pairs []string
	var held []net.Listener
	for _, f := range fixed {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, ln) // held until all are picked: no duplicates
		pairs = append(pairs, f, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	}
	for _, ln := range held {
		ln.Close()
	}
	return strings.NewReplacer(pairs...)
}
