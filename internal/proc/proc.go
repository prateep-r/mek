// Package proc is mek's process and file-lock plumbing: file locks, running
// a program, detaching a process from the terminal, process groups, and
// asking a tunnel's owner to stop — flock and signals underneath, so
// internal/tunnel and internal/audit need neither.
package proc

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

// ErrLocked: another process holds the lock.
var ErrLocked = errors.New("locked")

// Group is a started process and everything it starts, so a stop reaches
// all of them: a process group.
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

// CommandContext is Command, killed when ctx is done.
func CommandContext(ctx context.Context, path string, args ...string) *exec.Cmd {
	c := Command(path, args...)
	cc := exec.CommandContext(ctx, c.Path)
	cc.Args, cc.SysProcAttr, cc.Err = c.Args, c.SysProcAttr, c.Err
	return cc
}
