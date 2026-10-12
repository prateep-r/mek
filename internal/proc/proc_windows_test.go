//go:build windows

package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The test binary doubles as the processes the tests start.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "helper-args": // print the arguments it got, as JSON
			json.NewEncoder(os.Stdout).Encode(os.Args[2:])
			os.Exit(0)
		case "helper-tree": // start a child, write its pid, then wait
			child := exec.Command(os.Args[0], "helper-sleep")
			child.Start()
			os.WriteFile(os.Args[2], []byte(strconv.Itoa(child.Process.Pid)), 0o600)
			time.Sleep(time.Minute)
			os.Exit(0)
		case "helper-sleep":
			time.Sleep(time.Minute)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func TestLocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	a, _ := os.Create(path)
	b, _ := os.OpenFile(path, os.O_RDWR, 0)
	defer b.Close()
	if err := TryLock(a); err != nil {
		t.Fatal(err)
	}
	if err := TryLock(b); !errors.Is(err, ErrLocked) {
		t.Errorf("second lock: %v", err)
	}
	a.Close() // releases it
	if err := Lock(b); err != nil {
		t.Errorf("lock after release: %v", err)
	}
	if err := TryLock(a); err == nil || errors.Is(err, ErrLocked) {
		t.Errorf("closed file: %v", err)
	}
}

// A .cmd gets every argument exactly, special characters and all.
func TestCommandThroughCmd(t *testing.T) {
	bat := filepath.Join(t.TempDir(), "args tool.cmd") // a space in the path too
	os.WriteFile(bat, []byte("@\""+os.Args[0]+"\" helper-args %*\r\n"), 0o700)
	args := []string{"json(endpoint,masterAuth.clusterCaCertificate,status)", "x&y", "a|b", "^caret", "(paren)", "with space",
		`trail\`, `back\slash`, "<in>", "!bang", "", `C:\Program Files\x\\`, "--format=value(a)", "--filter=name='x y'"}
	cmd := CommandContext(context.Background(), bat, args...) // as Command, killable
	out, err := cmd.Output()
	var got []string
	json.Unmarshal(out, &got)
	if err != nil || strings.Join(got, "\x00") != strings.Join(args, "\x00") {
		t.Errorf("args through cmd.exe:\n got %q\nwant %q (%v)", got, args, err)
	}
	if cmd := Command(bat, "100%"); cmd.Err == nil || !strings.Contains(cmd.Err.Error(), "cmd.exe would change") {
		t.Errorf("percent: %v", cmd.Err)
	}
	for _, bad := range []string{"a\nb", `say "hi"`} {
		if cmd := Command(bat, bad); cmd.Err == nil {
			t.Errorf("%q: not refused", bad)
		}
	}
	if cmd := Command(os.Args[0], "x"); cmd.SysProcAttr != nil { // not a .cmd: run directly
		t.Errorf("exe: %+v", cmd.SysProcAttr)
	}
	t.Setenv("ComSpec", "")
	if cmd := Command(bat); !strings.HasSuffix(cmd.Path, `cmd.exe`) {
		t.Errorf("no ComSpec: %s", cmd.Path)
	}
}

func TestCmdQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":         `""`,
		"a b":      `"a b"`,
		`a\b`:      `"a\b"`,
		`a\`:       `"a\\"`,
		`a\\`:      `"a\\\\"`,
		"x&y|z(1)": `"x&y|z(1)"`,
	} {
		if got := cmdQuote(in); got != want {
			t.Errorf("cmdQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestDetach(t *testing.T) {
	cmd := exec.Command("x")
	Detach(cmd)
	if cmd.SysProcAttr.CreationFlags&windows.DETACHED_PROCESS == 0 {
		t.Error("not detached")
	}
}

// exited waits for a process to end.
func exited(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true // gone already
	}
	defer windows.CloseHandle(h)
	r, _ := windows.WaitForSingleObject(h, 5000)
	return r == windows.WAIT_OBJECT_0
}

func tree(t *testing.T, start func(*exec.Cmd) (*Group, error)) (*Group, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	g, err := start(exec.Command(os.Args[0], "helper-tree", pidFile))
	if err != nil {
		t.Fatal(err)
	}
	for range 250 {
		if b, _ := os.ReadFile(pidFile); len(b) > 0 {
			pid, _ := strconv.Atoi(string(b))
			return g, pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no child")
	return nil, 0
}

func TestGroups(t *testing.T) {
	if Graceful {
		t.Error("Windows can't stop gracefully")
	}
	for name, start := range map[string]func(*exec.Cmd) (*Group, error){"group": StartGroup, "attached": StartAttached} {
		g, child := tree(t, start)
		g.Terminate() // the whole job: the child too
		if err := g.Wait(); err == nil || !exited(child) {
			t.Errorf("%s: %v, child exited %v", name, err, exited(child))
		}
		g.Kill() // closed: nothing left to do
		if g.Pid() <= 0 {
			t.Errorf("%s: pid", name)
		}
	}
	if _, err := StartGroup(exec.Command(filepath.Join(t.TempDir(), "missing.exe"))); err == nil {
		t.Error("start failure")
	}
	cmd := exec.Command(os.Args[0], "helper-sleep")
	cmd.SysProcAttr = nil
	g, err := StartGroup(cmd) // no SysProcAttr yet: one is made
	if err != nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Errorf("hidden: %v", err)
	}
	g.Kill()
	g.Wait()
}

func TestJobErrors(t *testing.T) {
	boom := errors.New("boom")
	oldNew, oldAssign := newJob, assignToJob
	t.Cleanup(func() { newJob, assignToJob = oldNew, oldAssign })
	newJob = func() (windows.Handle, error) { return 0, boom }
	if _, err := StartAttached(exec.Command(os.Args[0], "helper-sleep")); !errors.Is(err, boom) {
		t.Errorf("job: %v", err)
	}
	newJob = oldNew
	assignToJob = func(windows.Handle, int) error { return boom }
	if _, err := StartAttached(exec.Command(os.Args[0], "helper-sleep")); !errors.Is(err, boom) {
		t.Errorf("assign: %v", err)
	}
	assignToJob = oldAssign
	if err := defaultAssign(0, 0); err == nil {
		t.Error("assigning a process that isn't there")
	}

	oldCreate, oldLimits := createJobObject, setJobLimits
	t.Cleanup(func() { createJobObject, setJobLimits = oldCreate, oldLimits })
	createJobObject = func(*windows.SecurityAttributes, *uint16) (windows.Handle, error) { return 0, boom }
	if _, err := defaultNewJob(); !errors.Is(err, boom) {
		t.Errorf("create: %v", err)
	}
	createJobObject = oldCreate
	setJobLimits = func(windows.Handle, uint32, uintptr, uint32) (int, error) { return 0, boom }
	if _, err := defaultNewJob(); !errors.Is(err, boom) {
		t.Errorf("limits: %v", err)
	}
}

func TestStopRequests(t *testing.T) {
	id := fmt.Sprintf("test-%d", os.Getpid())
	stop, done := StopRequests(id)
	again, doneAgain := StopRequests(id) // the same event, already there
	RequestStop(id, 0)
	for _, ch := range []<-chan struct{}{stop, again} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("stop event not seen")
		}
	}
	done()
	doneAgain()
	RequestStop("nobody-listens", 0) // no event: nothing to do

	quiet, d := StopRequests(id + "-quiet")
	d()
	select {
	case <-quiet:
		t.Error("stopped without a request")
	case <-time.After(50 * time.Millisecond):
	}
	bad, d := StopRequests(`no\such\namespace`) // can't be made: never fires
	d()
	select {
	case <-bad:
		t.Error("a broken event fired")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestKill(t *testing.T) {
	cmd := exec.Command(os.Args[0], "helper-sleep")
	cmd.Start()
	Kill(cmd.Process.Pid)
	if err := cmd.Wait(); err == nil {
		t.Error("not killed")
	}
	Kill(0)
	Kill(1<<30 + 2) // no such process (Windows pids are multiples of 4): OpenProcess fails
	KillGroup(cmd.Process.Pid)
}
