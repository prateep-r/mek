// Package runner executes a child process with the terminal attached and
// returns its exit code.
package runner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/prateep-r/mek/internal/proc"
)

// ExitError carries a child's exit code up to main without extra output.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// RunIO starts argv with env, stdin/stderr of mek and stdout sent to stdout,
// waits, and returns the exit code. Ctrl-C goes to the child (same process
// group); mek itself ignores it while waiting.
func RunIO(argv []string, env []string, stdout io.Writer) (int, error) {
	return RunStoppable(argv, env, stdout, nil, nil)
}

// RunStoppable is RunIO that also ends the child when stop fires (a
// foreground tunnel stopped from another terminal, on Windows), and sends
// its stderr to stderr (nil: mek's).
func RunStoppable(argv []string, env []string, stdout, stderr io.Writer, stop <-chan struct{}) (int, error) {
	if len(argv) == 0 {
		return 1, errors.New("no command given")
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return 127, fmt.Errorf("%s not found in PATH — run `mek doctor`", argv[0])
	}
	// Subscribe before starting so a signal can't slip through in between.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)

	g, err := start(path, argv, env, stdout, stderr)
	if err != nil {
		return 1, err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case s := <-sig:
				// SIGINT already reached the child via the terminal: just don't die
				// first. SIGTERM (`timeout`, k8s) and SIGHUP (the terminal closing)
				// go to the child too, and mek outlives it to write the audit entry.
				if s != os.Interrupt {
					g.Signal(s)
				}
			case <-stop:
				g.Terminate()
				stop = nil // once
			case <-done:
				return
			}
		}
	}()

	return result(g.Wait())
}

// start starts the program at path as argv[0] with the terminal attached.
func start(path string, argv, env []string, stdout, stderr io.Writer) (*proc.Group, error) {
	cmd := proc.Command(path, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Env = env
	if stderr == nil {
		stderr = os.Stderr
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	return proc.StartAttached(cmd)
}

// ExitCode is the exit code of a process that Wait returned err for.
func ExitCode(err error) int {
	code, _ := result(err)
	return code
}

// result turns Wait's error into an exit code, following the shell
// convention of 128+N when the child died from signal N. A non-exit error
// (the child never really ran) is returned with code 1.
func result(err error) (int, error) {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case !errors.As(err, &ee):
		return 1, err
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), nil
	}
	return ee.ExitCode(), nil
}
