package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	a, _ := os.Create(path)
	b, _ := os.Open(path)
	defer b.Close()
	if err := TryLock(a); err != nil {
		t.Fatal(err)
	}
	if err := TryLock(b); !errors.Is(err, ErrLocked) {
		t.Errorf("second lock: %v", err)
	}
	if err := TryShared(b); !errors.Is(err, ErrLocked) {
		t.Errorf("shared, while held: %v", err)
	}
	a.Close() // releases it
	if err := Lock(b); err != nil {
		t.Errorf("lock after release: %v", err)
	}
	if err := TryLock(a); err == nil || errors.Is(err, ErrLocked) { // closed: an error, not "locked"
		t.Errorf("closed file: %v", err)
	}
	c, _ := os.Open(path)
	defer c.Close()
	b.Close()
	if err := TryShared(c); err != nil {
		t.Errorf("shared, when free: %v", err)
	}
}

func TestCommandAndDetach(t *testing.T) {
	cmd := Command("/bin/echo", "a b")
	if cmd.Err != nil || strings.Join(cmd.Args, "|") != "/bin/echo|a b" {
		t.Errorf("command: %v %v", cmd.Args, cmd.Err)
	}
	Detach(cmd)
	if !cmd.SysProcAttr.Setsid {
		t.Error("not detached")
	}
	if out, err := CommandContext(context.Background(), "/bin/echo", "hi").Output(); err != nil || string(out) != "hi\n" {
		t.Errorf("CommandContext: %q %v", out, err)
	}
}

// groupOf starts sh with a background child that writes its pid, so a test
// can tell whether a stop reached the whole group.
func groupOf(t *testing.T, start func(*exec.Cmd) (*Group, error), script string) (*Group, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	g, err := start(exec.Command("sh", "-c", fmt.Sprintf("%s; sleep 30 & echo $! > %s; wait", script, pidFile)))
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if b, _ := os.ReadFile(pidFile); len(b) > 0 {
			var pid int
			fmt.Sscan(string(b), &pid)
			return g, pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the group never started its child")
	return nil, 0
}

func gone(pid int) bool {
	for range 100 {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestGroups(t *testing.T) {
	g, child := groupOf(t, StartGroup, "true")
	if g.Pid() <= 0 || !Graceful {
		t.Errorf("pid %d", g.Pid())
	}
	g.Terminate()
	if err := g.Wait(); err == nil || !gone(child) {
		t.Errorf("terminate: %v, child gone %v", err, gone(child))
	}

	g, child = groupOf(t, StartGroup, "trap '' TERM")
	g.Kill()
	if err := g.Wait(); err == nil || !gone(child) {
		t.Errorf("kill: %v", err)
	}

	// Attached: only the first process is signalled; the terminal's group
	// (ours) is left alone.
	g, child = groupOf(t, StartAttached, "true")
	g.Terminate()
	if err := g.Wait(); err == nil || syscall.Kill(child, 0) != nil {
		t.Errorf("attached terminate: %v (its child must be left alone)", err)
	}
	syscall.Kill(child, syscall.SIGKILL)
	g, _ = groupOf(t, StartAttached, "trap '' TERM")
	g.Kill()
	if err := g.Wait(); err == nil {
		t.Error("attached kill")
	}
}

func TestSignals(t *testing.T) {
	var got []string
	kill = func(pid int, sig syscall.Signal) error { got = append(got, fmt.Sprintf("%d:%d", pid, sig)); return nil }
	t.Cleanup(func() { kill = syscall.Kill })
	RequestStop("id", 4242)
	Kill(4242)
	KillGroup(4242)
	for _, pid := range []int{0, 1, -1} { // never every process
		RequestStop("id", pid)
		Kill(pid)
	}
	KillGroup(0)
	KillGroup(1)
	if strings.Join(got, ",") != "4242:15,4242:9,-4242:9" {
		t.Errorf("signals: %v", got)
	}
}

func TestStopRequests(t *testing.T) {
	stop, done := StopRequests("id")
	syscall.Kill(os.Getpid(), syscall.SIGHUP)
	select {
	case <-stop:
	case <-time.After(5 * time.Second):
		t.Fatal("SIGHUP did not ask to stop")
	}
	done()
	stop, done = StopRequests("id")
	done() // no request: just stops listening
	select {
	case <-stop:
		t.Error("stopped without a request")
	case <-time.After(50 * time.Millisecond):
	}
}
