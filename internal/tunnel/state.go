package tunnel

import (
	"fmt"
	"time"

	"github.com/prateep-r/mek/internal/proc"
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
	case alive && r.Phase == phaseReconnecting:
		return reconnecting{}
	case alive:
		return running{}
	case r.Phase == phaseExited:
		return exited{r.ExitCode}
	}
	return dead{}
}

// Test seams.
var (
	requestStop = proc.RequestStop
	killPID     = proc.Kill
	killGroup   = proc.KillGroup
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
	if r.Owner <= 1 { // still starting: its supervisor hasn't written its pid yet
		return fmt.Errorf("tunnel %s is still starting — try again in a moment", r.ShortID())
	}
	lock := file(r.ID, ".lock")
	requestStop(r.ID, r.Owner)
	deadline := time.Now().Add(stopTimeout)
	for held(lock) {
		if time.Now().After(deadline) {
			killGroup(r.Child)
			killPID(r.Owner)
			if !r.Background { // a foreground mek leads its job's process group: its tunnel too
				killGroup(r.Owner)
			}
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

// reconnecting: the process ended on its own and its supervisor will start
// it again. Stopping it is stopping the supervisor, like running.
type reconnecting struct{ running }

func (reconnecting) Name() string { return "reconnecting" }

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
