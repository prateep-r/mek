// Package runner executes a child process with the terminal attached and
// returns its exit code.
package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// ExitError carries a child's exit code up to main without extra output.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Run starts argv with env and the current stdio, waits, and returns the exit code.
// Ctrl-C goes to the child (same process group); mek itself ignores it while waiting.
func Run(argv []string, env []string) (int, error) {
	if len(argv) == 0 {
		return 1, errors.New("no command given")
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return 127, fmt.Errorf("%s not found in PATH — run `mek doctor`", argv[0])
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer func() { signal.Stop(sig); close(sig) }()
	go func() {
		for s := range sig {
			if s == syscall.SIGTERM && cmd.Process != nil {
				_ = cmd.Process.Signal(s)
			}
			// SIGINT already reached the child via the terminal; just don't die first.
		}
	}()

	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
