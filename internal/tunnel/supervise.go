package tunnel

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/proc"
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
// life and port are the tunnel's locks; holding them until the process ends
// is what makes the tunnel alive for `ls` and claims its port. stop ends the
// process: its whole group, gracefully first where the OS allows.
func Supervise(in io.Reader, life, port, log *os.File, stop <-chan struct{}) int {
	defer life.Close() // releases the locks
	defer port.Close()
	defer log.Close()
	var s Spec
	if err := json.NewDecoder(in).Decode(&s); err != nil {
		fmt.Fprintf(log, "mek: reading the tunnel spec: %v\n", err)
		return 1
	}
	return supervisor{log: log, grace: grace, retry: retry}.run(s, stop)
}

// grace is how long a stopped tunnel's process gets before SIGKILL.
var grace = 5 * time.Second

// backoff decides when a background tunnel whose process ended on its own
// (the cloud closed an idle session, the network dropped) starts it again.
// A run that lasted stable worked, and so did a first run whose port
// answered: the next failure waits first and starts a new outage. Runs
// shorter than that double the wait, up to most; after giveUp of nothing
// but short runs (an expired login, a deleted instance) the supervisor
// stops trying.
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

func (s supervisor) run(spec Spec, stop <-chan struct{}) int {
	r := spec.Record
	r.Owner = os.Getpid()
	var wait time.Duration
	outage := time.Now()
	for {
		began := time.Now()
		code, stopped, answered := s.once(spec, &r, stop)
		r.ExitCode = code
		ran := time.Since(began)
		s.logf("exited with code %d after %s", code, ran.Round(time.Second))
		switch {
		case stopped || !r.Restart, r.Restarts == 0 && ran < s.retry.stable && !answered:
			s.exited(r) // the first run never worked: Start reports that
			return code
		case ran >= s.retry.stable || r.Restarts == 0: // it worked: a new outage starts now
			wait, outage = s.retry.first, time.Now()
		case time.Since(outage) >= s.retry.giveUp:
			s.logf("gave up reconnecting after %s of failures — if the login expired: mek login %s, then start the tunnel again",
				time.Since(outage).Round(time.Second), r.Context)
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
		case <-stop:
			s.exited(r)
			return code
		case <-time.After(wait):
		}
	}
}

// start starts the tunnel's process in a group of its own, so a stop
// reaches its children too.
func (s supervisor) start(spec Spec, argv []string) (*proc.Group, error) {
	cmd := proc.Command(spec.Path, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Env, cmd.Stdout, cmd.Stderr = spec.Env, s.log, s.log
	return proc.StartGroup(cmd)
}

// once runs the tunnel's process until it ends, or stop ends it (its whole
// group: TERM, then KILL after the grace period where TERM is polite).
// stopped is true when it was stopped, or couldn't start at all; answered,
// when its local port accepted a connection.
func (s supervisor) once(spec Spec, r *Record, stop <-chan struct{}) (code int, stopped, answered bool) {
	g, err := s.start(spec, r.Command)
	if err != nil {
		s.note(err)
		return 127, true, false
	}
	r.Phase, r.Child = phaseRunning, g.Pid()
	s.note(save(*r))
	s.logf("started %s (pid %d)", strings.Join(audit.Mask(r.Command), " "), r.Child)

	done := make(chan error, 1)
	go func() { done <- g.Wait() }()
	up := make(chan struct{})
	quit := make(chan struct{})
	defer close(quit)
	go watchPort(r.Local, up, quit)
	isUp := func() bool {
		select {
		case <-up:
			return true
		default:
			return false
		}
	}
	select {
	case err := <-done:
		g.Kill() // what it started (ssh, a plugin) must not keep the port
		return runner.ExitCode(err), false, isUp()
	case <-stop:
	}
	g.Terminate()
	select {
	case err := <-done:
		return runner.ExitCode(err), true, isUp()
	case <-time.After(s.grace):
		g.Kill()
		return runner.ExitCode(<-done), true, isUp()
	}
}

// watchPort closes up once localhost:port accepts a connection, or gives up
// when quit closes.
func watchPort(port int, up chan<- struct{}, quit <-chan struct{}) {
	addr := fmt.Sprintf("localhost:%d", port)
	for {
		if c, err := net.DialTimeout("tcp", addr, pollEvery); err == nil {
			c.Close()
			close(up)
			return
		}
		select {
		case <-quit:
			return
		case <-time.After(pollEvery):
		}
	}
}
