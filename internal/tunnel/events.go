package tunnel

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/prateep-r/mek/internal/audit"
)

// Event is something that happened to a supervised tunnel.
type Event struct {
	Kind     EventKind
	Record   Record // as of the event
	ExitCode int    // Exited
}

type EventKind int

const (
	Started EventKind = iota
	Exited
)

// Observer is told about a tunnel's lifecycle — a GoF Observer. The
// supervisor only runs the process; what happens around it (state file,
// audit entry, log lines) are observers, and adding one doesn't touch it.
type Observer interface {
	Notify(Event)
}

// stateStore keeps the record file current.
type stateStore struct{ onError func(error) }

func (s stateStore) Notify(e Event) {
	if err := save(e.Record); err != nil && s.onError != nil {
		s.onError(err)
	}
}

// auditSink writes the session's "end" entry (its "start" entry was written
// before the supervisor was spawned).
type auditSink struct{ onError func(error) }

func (s auditSink) Notify(e Event) {
	if e.Kind != Exited {
		return
	}
	r := e.Record
	err := audit.Write(audit.Entry{
		Time: r.Started, Context: r.Context, Provider: r.Provider, Command: r.Command,
		Class: r.Class, Decision: r.Decision, ExitCode: e.ExitCode,
		DurationMS: r.Ended.Sub(r.Started).Milliseconds(), Event: "end", Session: r.ID, Target: r.Target,
	})
	if err != nil && s.onError != nil {
		s.onError(err)
	}
}

// logSink notes the lifecycle in the tunnel's log, between the process's
// own output.
type logSink struct{ w io.Writer }

func (s logSink) Notify(e Event) {
	ts := time.Now().Format(time.RFC3339)
	switch e.Kind {
	case Started:
		fmt.Fprintf(s.w, "%s mek: started %s (pid %d)\n", ts, strings.Join(audit.Mask(e.Record.Command), " "), e.Record.Child)
	case Exited:
		fmt.Fprintf(s.w, "%s mek: exited with code %d after %s\n", ts, e.ExitCode, e.Record.Ended.Sub(e.Record.Started).Round(time.Second))
	}
}
