package tunnel

import (
	"fmt"
	"syscall"
	"time"
)

// State is what a tunnel is doing now — a GoF State. Each state knows what
// listing it shows and what stopping it means, instead of if/else on
// phases and liveness spread through List and Stop.
type State interface {
	Name() string
	// Alive reports whether its owner still runs it.
	Alive() bool
	// Stop ends the tunnel (if it still runs) and removes its files.
	Stop(r Record) error
}

// stateOf combines the last written phase with whether the owner still
// holds the tunnel's lock.
func stateOf(r Record) State {
	alive := held(file(r.ID, ".lock"))
	switch {
	case alive && r.Phase == phaseStarting:
		return starting{}
	case alive:
		return running{}
	case r.Phase == phaseExited:
		return exited{r.ExitCode}
	}
	return dead{}
}

// sendSignal sends sig to pid, or to process group -pid; never to pid 0 or
// -1, which would reach every process of ours or every process we may.
func sendSignal(pid int, sig syscall.Signal) {
	if pid > 1 || pid < -1 {
		kill(pid, sig)
	}
}

// Test seams.
var (
	kill        = syscall.Kill
	stopTimeout = 10 * time.Second
	pollEvery   = 100 * time.Millisecond
)

type running struct{}

func (running) Name() string { return "running" }
func (running) Alive() bool  { return true } // starting embeds it

// Stop asks the owner to end the tunnel and waits for it to let go of the
// lock. While the lock is held the pids are surely still ours, so a
// forced kill is safe.
func (running) Stop(r Record) error {
	lock := file(r.ID, ".lock")
	sendSignal(r.Owner, syscall.SIGTERM)
	deadline := time.Now().Add(stopTimeout)
	for held(lock) {
		if time.Now().After(deadline) {
			sendSignal(-r.Child, syscall.SIGKILL)
			sendSignal(r.Owner, syscall.SIGKILL)
			remove(r.ID)
			return fmt.Errorf("tunnel %s did not stop within %s — killed", r.ID, stopTimeout)
		}
		time.Sleep(pollEvery)
	}
	remove(r.ID)
	return nil
}

type starting struct{ running }

func (starting) Name() string { return "starting" }

type exited struct{ code int }

func (exited) Alive() bool         { return false }
func (s exited) Name() string      { return fmt.Sprintf("exited (%d)", s.code) }
func (exited) Stop(r Record) error { remove(r.ID); return nil }

// dead: the owner vanished without recording an exit (killed -9, reboot).
// Its pids may belong to someone else by now, so nothing is signalled.
type dead struct{}

func (dead) Name() string { return "dead" }
func (dead) Alive() bool  { return false }

func (dead) Stop(r Record) error {
	remove(r.ID)
	return nil
}
