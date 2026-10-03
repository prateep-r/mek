package runner

import (
	"io"
	"os"
	"time"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

// Invocation is one command mek runs for the user (a GoF Command): the request
// travels through a chain of Runners, each of which fills in its results.
type Invocation struct {
	Context *config.Context
	Argv    []string
	Env     []string
	Class   guard.Class
	Stdout  io.Writer // nil: the terminal
	Session string    // set for sessions (shell, tunnel): audited at start and end
	Target  string    // what a session connects to, for the audit log

	Decision string        // set by the guard: allowed | confirmed | blocked | declined
	ExitCode int           // set by Exec (-1 when the command never ran)
	Duration time.Duration // set by Exec
}

// Err turns a finished invocation into the error main expects: err itself, or
// an *ExitError carrying the child's non-zero exit code.
func (inv *Invocation) Err(err error) error {
	if err != nil {
		return err
	}
	if inv.ExitCode != 0 {
		return &ExitError{Code: inv.ExitCode}
	}
	return nil
}

// Runner executes an invocation.
type Runner interface {
	Run(inv *Invocation) error
}

// Func adapts a function to a Runner.
type Func func(inv *Invocation) error

func (f Func) Run(inv *Invocation) error { return f(inv) }

// Decorator wraps a Runner with one concern (guard, audit, ...): a GoF Decorator.
type Decorator func(next Runner) Runner

// Chain wraps r so the first decorator is the outermost:
// Chain(Exec, audit, guard) runs audit → guard → Exec.
func Chain(r Runner, ds ...Decorator) Runner {
	for i := len(ds) - 1; i >= 0; i-- {
		r = ds[i](r)
	}
	return r
}

// Exec is the concrete component: it starts the process and waits for it.
var Exec Runner = Func(func(inv *Invocation) error {
	start := time.Now()
	stdout := inv.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	code, err := RunIO(inv.Argv, inv.Env, stdout)
	inv.ExitCode, inv.Duration = code, time.Since(start)
	return err
})
