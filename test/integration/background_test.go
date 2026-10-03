//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/prateep-r/mek/test/testkit"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// bgSetup is a user whose aws really listens on the tunnel's local port.
func bgSetup(t *testing.T) (*env, int) {
	t.Helper()
	port := freePort(t)
	e := setup(t, fmt.Sprintf(`contexts:
  dev: {provider: aws, aws_profile: dev, region: ap-southeast-1, tunnels: {db: {via: i-0123abcd, host: db.internal, port: 5432, local_port: %d}}}
  ro:  {provider: aws, aws_profile: viewer, readonly: true, tunnels: {db: {via: i-0123abcd, port: 5432, local_port: %d}}}
`, port, port))
	stubs := strings.SplitN(strings.TrimPrefix(e.vars[2], "PATH="), ":", 2)[0]
	b, _ := os.ReadFile(testkit.Listenstub(t))
	for _, name := range []string{"aws", "session-manager-plugin"} {
		os.WriteFile(filepath.Join(stubs, name), b, 0o755)
	}
	t.Cleanup(func() { e.run("tunnel", "stop", "--all") }) // no stray processes
	return e, port
}

func listening(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err == nil {
		c.Close()
	}
	return err == nil
}

func record(t *testing.T, e *env) map[string]any {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(e.mekHome, "tunnels", "*.json"))
	if len(paths) != 1 {
		t.Fatalf("records: %v", paths)
	}
	var r map[string]any
	b, _ := os.ReadFile(paths[0])
	json.Unmarshal(b, &r)
	return r
}

func TestBackgroundTunnel(t *testing.T) {
	e, port := bgSetup(t)
	r := e.run("-c", "dev", "tunnel", "db", "-b")
	if r.Code != 0 || !strings.Contains(r.Stderr, "running in the background") {
		t.Fatalf("start: %+v", r)
	}
	// mek has exited; the tunnel lives on under its supervisor.
	if !listening(port) {
		t.Fatal("the tunnel's port is not open")
	}
	if r := e.run("tunnel", "ls"); !strings.Contains(r.Stdout, "running") || !strings.Contains(r.Stdout, "db") {
		t.Errorf("ls: %+v", r)
	}
	if r := e.run("-c", "dev", "tunnel", "db", "-b"); r.Code == 0 || !strings.Contains(r.Stderr, "already forwarded by mek tunnel db (dev)") {
		t.Errorf("second start: %+v", r)
	}
	if r := e.run("tunnel", "logs", "db"); !strings.Contains(r.Stdout, "listenstub: listening") || !strings.Contains(r.Stdout, "mek: started aws ssm start-session") {
		t.Errorf("logs: %+v", r)
	}
	a := e.audit()
	if len(a) != 1 || a[0]["event"] != "start" {
		t.Errorf("audit while running: %v", a)
	}

	if r := e.run("tunnel", "stop", "db"); r.Code != 0 {
		t.Fatalf("stop: %+v", r)
	}
	if listening(port) {
		t.Error("port still open after stop")
	}
	a = e.audit()
	if len(a) != 2 || a[1]["event"] != "end" || a[1]["exit_code"] != float64(143) || a[1]["session"] != a[0]["session"] {
		t.Errorf("audit after stop: %v", a)
	}
	if r := e.run("tunnel", "ls"); !strings.Contains(r.Stderr, "no tunnels") {
		t.Errorf("ls after stop: %+v", r)
	}
}

// kill -9 of the supervisor: ls says dead (no pid guesswork), stop cleans up.
func TestBackgroundDeadSupervisor(t *testing.T) {
	e, port := bgSetup(t)
	if r := e.run("-c", "dev", "tunnel", "db", "-b"); r.Code != 0 {
		t.Fatalf("start: %+v", r)
	}
	rec := record(t, e)
	owner, child := int(rec["owner_pid"].(float64)), int(rec["child_pid"].(float64))
	syscall.Kill(owner, syscall.SIGKILL)
	t.Cleanup(func() { syscall.Kill(-child, syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(e.run("tunnel", "ls").Stdout, "dead") {
		if time.Now().After(deadline) {
			t.Fatalf("ls never showed dead: %s", e.run("tunnel", "ls").Stdout)
		}
		time.Sleep(50 * time.Millisecond)
	}
	r := e.run("tunnel", "stop", "db")
	if r.Code != 0 || !strings.Contains(r.Stderr, fmt.Sprintf("localhost:%d is still in use", port)) {
		t.Errorf("stop of a dead tunnel: %+v", r)
	}
}

func TestBackgroundNeverReady(t *testing.T) {
	e, _ := bgSetup(t)
	r := e.with("STUB_NOLISTEN=1").run("-c", "dev", "tunnel", "db", "-b", "--wait", "1s")
	if r.Code == 0 || !strings.Contains(r.Stderr, "not ready after 1s — stopped it") {
		t.Fatalf("never ready: %+v", r)
	}
	if r := e.run("tunnel", "ls"); !strings.Contains(r.Stderr, "no tunnels") {
		t.Errorf("left behind: %+v", r)
	}
	deadline := time.Now().Add(5 * time.Second) // the supervisor writes the end entry as it exits
	for len(e.audit()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if a := e.audit(); len(a) != 2 || a[1]["event"] != "end" {
		t.Errorf("audit: %v", a)
	}
}

func TestBackgroundEarlyExit(t *testing.T) {
	e, _ := bgSetup(t)
	r := e.with("STUB_EXIT=3").run("-c", "dev", "tunnel", "db", "-b")
	if r.Code == 0 || !strings.Contains(r.Stderr, "exited (code 3)") || !strings.Contains(r.Stderr, "listenstub: exiting 3") {
		t.Errorf("early exit: %+v", r)
	}
	if r := e.run("tunnel", "ls"); !strings.Contains(r.Stdout, "exited (3)") {
		t.Errorf("ls: %+v", r)
	}
}

// Ten starts at once: the port claim lets exactly one through.
func TestBackgroundConcurrentStarts(t *testing.T) {
	e, port := bgSetup(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := e.run("-c", "dev", "tunnel", "db", "-b"); r.Code == 0 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 || !listening(port) {
		t.Errorf("%d starts succeeded, want 1", ok)
	}
	if n := strings.Count(e.run("tunnel", "ls").Stdout, "running"); n != 1 {
		t.Errorf("%d running tunnels listed", n)
	}
}

// A foreground tunnel is listed, and `stop` from another terminal ends it.
func TestForegroundTunnelListedAndStopped(t *testing.T) {
	e, port := bgSetup(t)
	cmd := testkit.Command(mek, e.vars, "-c", "ro", "tunnel", "db")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for !listening(port) || !strings.Contains(e.run("tunnel", "ls").Stdout, "(foreground)") {
		if time.Now().After(deadline) {
			t.Fatalf("foreground tunnel never listed: %s", e.run("tunnel", "ls").Stdout)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r := e.run("tunnel", "stop", "db"); r.Code != 0 {
		t.Fatalf("stop: %+v", r)
	}
	select {
	case <-exited:
		if code := cmd.ProcessState.ExitCode(); code != 143 {
			t.Errorf("foreground mek exit %d, want the tunnel's 143", code)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("foreground mek did not exit after stop")
	}
	if r := e.run("tunnel", "ls"); !strings.Contains(r.Stderr, "no tunnels") {
		t.Errorf("ls after: %+v", r)
	}
}
