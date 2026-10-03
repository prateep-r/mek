package tunnel

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/prateep-r/mek/internal/runner"
)

// Spec is everything a supervisor needs: the record, the resolved program
// and the process's environment (which never touches disk).
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
	note := func(err error) { fmt.Fprintf(log, "mek: %v\n", err) }
	sup := supervisor{observers: []Observer{stateStore{note}, auditSink{note}, logSink{log}}, grace: grace}
	return sup.run(s, log, sigs)
}

// grace is how long a stopped tunnel's process gets before SIGKILL.
var grace = 5 * time.Second

type supervisor struct {
	observers []Observer
	grace     time.Duration
}

func (s supervisor) notify(e Event) {
	for _, o := range s.observers {
		o.Notify(e)
	}
}

func (s supervisor) run(spec Spec, out io.Writer, sigs <-chan os.Signal) int {
	r := spec.Record
	r.Owner = os.Getpid()
	cmd := exec.Command(spec.Path, r.Command[1:]...)
	cmd.Args[0] = r.Command[0]
	cmd.Env, cmd.Stdout, cmd.Stderr = spec.Env, out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // so a stop reaches its children too
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "mek: %v\n", err)
		r.Phase, r.ExitCode, r.Ended = phaseExited, 127, time.Now()
		s.notify(Event{Kind: Exited, Record: r, ExitCode: 127})
		return 127
	}
	r.Phase, r.Child = phaseRunning, cmd.Process.Pid
	s.notify(Event{Kind: Started, Record: r})

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
	code := runner.ExitCode(err)
	r.Phase, r.ExitCode, r.Ended = phaseExited, code, time.Now()
	s.notify(Event{Kind: Exited, Record: r, ExitCode: code})
	return code
}
