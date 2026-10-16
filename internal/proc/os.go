package proc

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Test seam.
var kill = syscall.Kill

// TryLock takes an exclusive lock on f without waiting.
func TryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrLocked
	}
	return err
}

// TryShared takes a shared lock on f without waiting: ErrLocked while
// another file holds it exclusively. Shared locks don't stop each other.
func TryShared(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrLocked
	}
	return err
}

// Lock waits for an exclusive lock on f.
func Lock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

// Command runs path with args.
func Command(path string, args ...string) *exec.Cmd { return exec.Command(path, args...) }

// Detach makes cmd outlive this terminal: its own session.
func Detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

type groupSys struct{ own bool }

func (*Group) close() {}

// StartGroup starts cmd in a process group of its own (for a background
// tunnel: nothing it starts is left behind).
func StartGroup(cmd *exec.Cmd) (*Group, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &Group{cmd, groupSys{own: true}}, cmd.Start()
}

// StartAttached starts cmd in the terminal's process group, so Ctrl-C and
// the terminal reach it as usual.
func StartAttached(cmd *exec.Cmd) (*Group, error) { return &Group{cmd: cmd}, cmd.Start() }

func (g *Group) signal(sig syscall.Signal) {
	if g.sys.own {
		signalPID(-g.Pid(), sig)
		return
	}
	signalPID(g.Pid(), sig)
}

// Terminate asks the group to end (SIGTERM).
func (g *Group) Terminate() { g.signal(syscall.SIGTERM) }

// Kill ends the group now (SIGKILL).
func (g *Group) Kill() { g.signal(syscall.SIGKILL) }

// Graceful: Terminate lets processes clean up, so wait before Kill.
const Graceful = true

// signalPID sends sig to pid, or to process group -pid; never to pid 0 or
// -1, which would reach every process of ours or every process we may.
func signalPID(pid int, sig syscall.Signal) {
	if pid > 1 || pid < -1 {
		kill(pid, sig)
	}
}

// StopRequests fires when this process is asked to stop: SIGTERM (mek
// tunnel stop), SIGINT or SIGHUP.
func StopRequests(string) (<-chan struct{}, func()) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		select {
		case <-sigs:
			close(stop)
		case <-done:
		}
	}()
	return stop, func() { signal.Stop(sigs); close(done) }
}

// RequestStop asks the owner of tunnel id (process pid) to stop.
func RequestStop(_ string, pid int) { signalPID(pid, syscall.SIGTERM) }

// Kill ends process pid now.
func Kill(pid int) { signalPID(pid, syscall.SIGKILL) }

// KillGroup ends process group pid now.
func KillGroup(pid int) { signalPID(-pid, syscall.SIGKILL) }
