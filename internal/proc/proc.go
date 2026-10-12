// Package proc is what mek does differently on each OS: file locks, running
// a program, detaching a process from the terminal, process groups, and
// asking a tunnel's owner to stop. It is the implementation side of a GoF
// Bridge: internal/tunnel and internal/audit call this API, and
// proc_unix.go / proc_windows.go implement it with flock and signals, or
// LockFileEx, Job Objects and named events.
package proc

import (
	"errors"
	"os"
	"os/exec"
)

// ErrLocked: another process holds the lock.
var ErrLocked = errors.New("locked")

// Group is a started process and everything it starts, so a stop reaches
// all of them: a process group on Unix, a Job Object on Windows.
type Group struct {
	cmd *exec.Cmd
	sys groupSys
}

// Pid is the group's first process.
func (g *Group) Pid() int { return g.cmd.Process.Pid }

// Wait waits for the first process to exit.
func (g *Group) Wait() error {
	defer g.close()
	return g.cmd.Wait()
}

// Signal passes sig on to the first process (where the OS has signals).
func (g *Group) Signal(sig os.Signal) { _ = g.cmd.Process.Signal(sig) }
