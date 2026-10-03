// Package tunnel runs and tracks mek's tunnels: background ones under a
// supervisor process, and foreground ones while their mek runs. It is a GoF
// Facade — Start, Foreground, List, Find and Stop — over the record files,
// flock-based liveness, port claims, spawning and signals.
package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Info is a tunnel with its current state.
type Info struct {
	Record
	State State
}

// Test seams: supervisorCommand starts `mek tunnel _supervise`.
var (
	executable        = os.Executable
	supervisorCommand = defaultSupervisor
)

func defaultSupervisor(id string) (*exec.Cmd, error) {
	exe, err := executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(exe, "tunnel", "_supervise", id), nil
}

// Start runs a tunnel in the background under a supervisor, then waits up to
// wait for its local port to accept connections.
func Start(s Spec, wait time.Duration) (Record, error) {
	path, err := exec.LookPath(s.Command[0])
	if err != nil {
		return Record{}, fmt.Errorf("%s not found in PATH — run `mek doctor`", s.Command[0])
	}
	s.Path = path
	port, err := claimPort(s.Local)
	if err != nil {
		return Record{}, err
	}
	defer port.Close()
	life, err := tryLock(file(s.ID, ".lock"))
	if err != nil {
		return Record{}, err
	}
	defer life.Close()
	log, err := os.OpenFile(LogPath(s.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Record{}, err
	}
	defer log.Close()

	s.Background, s.Phase, s.Started = true, phaseStarting, time.Now()
	if err := save(s.Record); err != nil {
		return Record{}, err
	}
	cmd, err := supervisorCommand(s.ID)
	if err != nil {
		remove(s.ID)
		return Record{}, err
	}
	spec, _ := json.Marshal(s) // a Spec always marshals
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = s.Env, bytes.NewReader(spec), log, log
	cmd.ExtraFiles = []*os.File{life, port, log}         // fds 3, 4, 5: the supervisor keeps the locks
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives this terminal
	if err := cmd.Start(); err != nil {
		remove(s.ID)
		return Record{}, err
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }() // reap it if it dies while we wait
	life.Close()                              // from here on only the supervisor holds the locks
	port.Close()
	return waitReady(s.Record, cmd.Process.Pid, wait, exited)
}

// waitReady waits for the tunnel's port; owner is the supervisor's pid,
// for stopping it before it has written its record.
func waitReady(r Record, owner int, wait time.Duration, exited <-chan struct{}) (Record, error) {
	deadline := time.After(wait)
	addr := fmt.Sprintf("localhost:%d", r.Local)
	for {
		if c, err := net.DialTimeout("tcp", addr, pollEvery); err == nil {
			c.Close()
			if cur, err := load(r.ID); err == nil {
				r = cur
			}
			return r, nil
		}
		select {
		case <-exited:
			if cur, err := load(r.ID); err == nil {
				r = cur
			}
			return r, fmt.Errorf("tunnel %s exited (code %d) before %s was ready:\n%s", r.ID, r.ExitCode, addr, logTail(r.ID))
		case <-deadline:
			tail := logTail(r.ID)
			if cur, err := load(r.ID); err == nil {
				r = cur
			}
			if r.Owner == 0 {
				r.Owner = owner
			}
			stateOf(r).Stop(r)
			return r, fmt.Errorf("tunnel %s: %s not ready after %s — stopped it:\n%s", r.ID, addr, wait, tail)
		case <-time.After(pollEvery):
		}
	}
}

// logTail is the last lines of a tunnel's log, for error messages.
func logTail(id string) string {
	b, _ := os.ReadFile(LogPath(id))
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return "  " + strings.Join(lines[max(0, len(lines)-10):], "\n  ")
}

// claimPort locks a local port for one tunnel, across every mek process.
func claimPort(port int) (*os.File, error) {
	f, err := tryLock(portLockPath(port))
	if !errors.Is(err, errLocked) {
		return f, err
	}
	if err := PortFree(port); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("localhost:%d is already forwarded by another mek tunnel", port)
}

// PortFree fails when a live mek tunnel forwards the port, naming it.
func PortFree(port int) error {
	for _, i := range List("") {
		if i.State.Alive() && i.Local == port {
			return fmt.Errorf("localhost:%d is already forwarded by mek tunnel %s (%s) — mek tunnel stop %s", port, i.Label(), i.Context, i.Label())
		}
	}
	return nil
}

// Foreground records a tunnel that runs in this mek process, so `ls` shows
// it and `stop` can end it; call done when it ends.
func Foreground(r Record) (done func(), err error) {
	port, err := claimPort(r.Local)
	if err != nil {
		return nil, err
	}
	life, err := tryLock(file(r.ID, ".lock"))
	if err != nil {
		port.Close()
		return nil, err
	}
	r.Phase, r.Owner, r.Started = phaseRunning, os.Getpid(), time.Now()
	if err := save(r); err != nil {
		life.Close()
		port.Close()
		return nil, err
	}
	return func() {
		remove(r.ID)
		life.Close()
		port.Close()
	}, nil
}

// List is every tunnel (of one context, unless ctx is ""), oldest first.
func List(ctx string) []Info {
	var out []Info
	for _, r := range records() {
		if ctx == "" || r.Context == ctx {
			out = append(out, Info{r, stateOf(r)})
		}
	}
	return out
}

// Label is how listings and messages name a tunnel: its name, else its id.
func (r Record) Label() string {
	if r.Name != "" {
		return r.Name
	}
	return r.ShortID()
}

// ShortID is the id as listings show it.
func (r Record) ShortID() string { return r.ID[:min(8, len(r.ID))] }

// Find picks one tunnel by name, id or id prefix (at least 4 characters).
func Find(sel, ctx string) (Info, error) {
	var hits []Info
	for _, i := range List(ctx) {
		if i.ID == sel || i.Name == sel || len(sel) >= 4 && strings.HasPrefix(i.ID, sel) {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 0:
		return Info{}, fmt.Errorf("no tunnel %q — see `mek tunnel ls`", sel)
	case 1:
		return hits[0], nil
	}
	var where []string
	for _, h := range hits {
		where = append(where, h.ShortID()+" ("+h.Context+")")
	}
	return Info{}, fmt.Errorf("%q matches %d tunnels: %s — use an id, or -c <context>", sel, len(hits), strings.Join(where, ", "))
}

// Stop stops the selected tunnels (or all of them, within ctx when set)
// and returns what it stopped. Stopping is idempotent: finished and dead
// tunnels are just cleaned up.
func Stop(sels []string, all bool, ctx string) ([]Info, error) {
	var targets []Info
	if all {
		targets = List(ctx)
	}
	var errs []error
	for _, sel := range sels {
		i, err := Find(sel, ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		targets = append(targets, i)
	}
	for _, i := range targets {
		if err := i.State.Stop(i.Record); err != nil {
			errs = append(errs, err)
		}
	}
	return targets, errors.Join(errs...)
}

// Logs writes a tunnel's log to w; with follow, it keeps writing new lines
// while the tunnel runs.
func Logs(i Info, w io.Writer, follow bool) error {
	f, err := os.Open(LogPath(i.ID))
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		if _, err := io.Copy(w, f); err != nil {
			return err
		}
		if !follow || !held(file(i.ID, ".lock")) {
			return nil
		}
		time.Sleep(pollEvery)
	}
}
