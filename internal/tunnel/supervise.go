package tunnel

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/runner"
)

// Spec is everything a supervisor needs: the record, the resolved program
// and the tunnel process's environment (which never touches disk).
type Spec struct {
	Record
	Path string   `json:"path"`
	Env  []string `json:"env"`
}

// Supervise runs a background tunnel's process (`mek tunnel _supervise`).
// life and port are the tunnel's locks, inherited from the mek that started
// it; holding them until the process ends is what makes the tunnel alive
// for `ls` and claims its port. A signal stops the process: TERM to its
// process group, KILL after a grace period.
func Supervise(in io.Reader, life, port, log *os.File, sigs <-chan os.Signal) int {
	defer life.Close() // releases the locks
	defer port.Close()
	defer log.Close()
	for _, f := range []*os.File{life, port, log} {
		syscall.CloseOnExec(int(f.Fd())) // inherited without it: the tunnel's process must not hold the locks
	}
	var s Spec
	if err := json.NewDecoder(in).Decode(&s); err != nil {
		fmt.Fprintf(log, "mek: reading the tunnel spec: %v\n", err)
		return 1
	}
	return supervisor{log: log, grace: grace, retry: retry}.run(s, sigs)
}

// grace is how long a stopped tunnel's process gets before SIGKILL.
var grace = 5 * time.Second

// backoff decides when a background tunnel whose process ended on its own
// (the cloud closed an idle session, the network dropped) starts it again.
// A run that lasted stable worked: the next failure waits first and starts
// a new outage. Runs shorter than that double the wait, up to most; after
// giveUp of nothing but short runs (an expired login, a deleted instance)
// the supervisor stops trying.
type backoff struct {
	first, most, stable, giveUp time.Duration
}

var retry = backoff{first: time.Second, most: 30 * time.Second, stable: 10 * time.Second, giveUp: 5 * time.Minute}

// supervisor runs one tunnel's process into log, again whenever it ends on
// its own, and records its lifecycle: the record file and log lines as it
// starts, ends and reconnects, and the audit end entry once it is over (the
// start entry was written before the supervisor was spawned). Failures to
// record go to the log too.
type supervisor struct {
	log   io.Writer
	grace time.Duration
	retry backoff
}

func (s supervisor) note(err error) {
	if err != nil {
		fmt.Fprintf(s.log, "mek: %v\n", err)
	}
}

func (s supervisor) logf(format string, args ...any) {
	fmt.Fprintf(s.log, "%s mek: %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func (s supervisor) exited(r Record) {
	r.Phase, r.Child, r.Ended = phaseExited, 0, time.Now()
	s.note(save(r))
	took := r.Ended.Sub(r.Started)
	s.note(audit.Write(audit.Entry{
		Time: r.Started, Context: r.Context, Provider: r.Provider, Command: r.Command,
		Class: r.Class, Decision: r.Decision, ExitCode: r.ExitCode,
		DurationMS: took.Milliseconds(), Event: "end", Session: r.ID, Target: r.Target,
	}))
}

func (s supervisor) run(spec Spec, sigs <-chan os.Signal) int {
	r := spec.Record
	r.Owner = os.Getpid()
	var wait time.Duration
	outage := time.Now()
	for {
		began := time.Now()
		code, stopped := s.once(spec, &r, sigs)
		r.ExitCode = code
		ran := time.Since(began)
		s.logf("exited with code %d after %s", code, ran.Round(time.Second))
		switch {
		case stopped || !r.Restart:
			s.exited(r)
			return code
		case ran >= s.retry.stable: // it worked: a new outage starts now
			wait, outage = s.retry.first, time.Now()
		case r.Restarts == 0 || time.Since(outage) >= s.retry.giveUp:
			// The first run never worked (Start reports that), or it keeps failing.
			if r.Restarts > 0 {
				s.logf("gave up reconnecting after %s of failures — if the login expired: mek login %s, then start the tunnel again",
					time.Since(outage).Round(time.Second), r.Context)
			}
			s.exited(r)
			return code
		default: // failing again: wait longer
			wait = min(2*wait, s.retry.most)
		}
		r.Restarts++
		r.Phase, r.Child = phaseReconnecting, 0
		s.note(save(r))
		s.logf("reconnecting in %s (restart %d)", wait, r.Restarts)
		select {
		case <-sigs:
			s.exited(r)
			return code
		case <-time.After(wait):
		}
	}
}

// once runs the tunnel's process until it ends, or a signal stops it
// (TERM to its process group, KILL after the grace period). stopped is
// true when it was stopped, or couldn't start at all.
func (s supervisor) once(spec Spec, r *Record, sigs <-chan os.Signal) (code int, stopped bool) {
	cmd := exec.Command(spec.Path, r.Command[1:]...)
	cmd.Args[0] = r.Command[0]
	cmd.Env, cmd.Stdout, cmd.Stderr = spec.Env, s.log, s.log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // so a stop reaches its children too
	if err := cmd.Start(); err != nil {
		s.note(err)
		return 127, true
	}
	r.Phase, r.Child = phaseRunning, cmd.Process.Pid
	s.note(save(*r))
	s.logf("started %s (pid %d)", strings.Join(audit.Mask(r.Command), " "), r.Child)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return runner.ExitCode(err), false
	case <-sigs:
	}
	sendSignal(-r.Child, syscall.SIGTERM)
	select {
	case err := <-done:
		return runner.ExitCode(err), true
	case <-time.After(s.grace):
		sendSignal(-r.Child, syscall.SIGKILL)
		return runner.ExitCode(<-done), true
	}
}
