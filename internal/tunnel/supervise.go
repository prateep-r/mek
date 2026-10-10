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
	return supervisor{log: log, grace: grace}.run(s, sigs)
}

// grace is how long a stopped tunnel's process gets before SIGKILL.
var grace = 5 * time.Second

// supervisor runs one tunnel's process into log and records its lifecycle:
// the record file and a log line when it starts and ends, and the audit
// end entry (the start entry was written before the supervisor was
// spawned). Failures to record go to the log too.
type supervisor struct {
	log   io.Writer
	grace time.Duration
}

func (s supervisor) note(err error) {
	if err != nil {
		fmt.Fprintf(s.log, "mek: %v\n", err)
	}
}

func (s supervisor) started(r Record) {
	s.note(save(r))
	fmt.Fprintf(s.log, "%s mek: started %s (pid %d)\n", time.Now().Format(time.RFC3339), strings.Join(audit.Mask(r.Command), " "), r.Child)
}

func (s supervisor) exited(r Record) {
	s.note(save(r))
	took := r.Ended.Sub(r.Started)
	fmt.Fprintf(s.log, "%s mek: exited with code %d after %s\n", time.Now().Format(time.RFC3339), r.ExitCode, took.Round(time.Second))
	s.note(audit.Write(audit.Entry{
		Time: r.Started, Context: r.Context, Provider: r.Provider, Command: r.Command,
		Class: r.Class, Decision: r.Decision, ExitCode: r.ExitCode,
		DurationMS: took.Milliseconds(), Event: "end", Session: r.ID, Target: r.Target,
	}))
}

func (s supervisor) run(spec Spec, sigs <-chan os.Signal) int {
	r := spec.Record
	r.Owner = os.Getpid()
	cmd := exec.Command(spec.Path, r.Command[1:]...)
	cmd.Args[0] = r.Command[0]
	cmd.Env, cmd.Stdout, cmd.Stderr = spec.Env, s.log, s.log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // so a stop reaches its children too
	if err := cmd.Start(); err != nil {
		s.note(err)
		r.Phase, r.ExitCode, r.Ended = phaseExited, 127, time.Now()
		s.exited(r)
		return 127
	}
	r.Phase, r.Child = phaseRunning, cmd.Process.Pid
	s.started(r)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-sigs:
		sendSignal(-r.Child, syscall.SIGTERM)
		select {
		case err = <-done:
		case <-time.After(s.grace):
			sendSignal(-r.Child, syscall.SIGKILL)
			err = <-done
		}
	}
	r.Phase, r.ExitCode, r.Ended = phaseExited, runner.ExitCode(err), time.Now()
	s.exited(r)
	return r.ExitCode
}
