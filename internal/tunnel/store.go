package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/fsutil"
	"github.com/prateep-r/mek/internal/proc"
)

// Record is what mek keeps about a tunnel in <MEK_HOME>/tunnels/<id>.json.
// It holds no environment, so nothing secret: the supervisor gets that on
// its stdin.
type Record struct {
	ID         string    `json:"id"`
	Context    string    `json:"context"`
	Provider   string    `json:"provider"`
	Name       string    `json:"name,omitempty"` // "" for an ad-hoc tunnel
	Target     string    `json:"target"`
	Local      int       `json:"local_port"`
	Class      string    `json:"class"`
	Decision   string    `json:"decision"`
	Command    []string  `json:"command"`
	Background bool      `json:"background"`
	Restart    bool      `json:"restart,omitempty"`  // background: start the process again when it ends on its own
	Restarts   int       `json:"restarts,omitempty"` // how many times it was started again
	Phase      string    `json:"phase"`              // starting | running | reconnecting | exited, as last written
	Owner      int       `json:"owner_pid"`          // holds the lock: the supervisor, or mek in the foreground
	Child      int       `json:"child_pid"`          // the tunnel's process (its own process group)
	ExitCode   int       `json:"exit_code"`
	Started    time.Time `json:"started"`
	Ended      time.Time `json:"ended,omitzero"`
}

// Phases a record is written with; State adds "dead" for owners that vanished.
const (
	phaseStarting     = "starting"
	phaseRunning      = "running"
	phaseReconnecting = "reconnecting"
	phaseExited       = "exited"
)

// Dir holds the tunnels' records, locks and logs.
func Dir() string { return filepath.Join(config.Dir(), "tunnels") }

func file(id, ext string) string { return filepath.Join(Dir(), id+ext) }

// LogPath is a tunnel's log: its process's output and the supervisor's notes.
func LogPath(id string) string { return file(id, ".log") }

func portLockPath(port int) string { return filepath.Join(Dir(), fmt.Sprintf("port-%d.lock", port)) }

func save(r Record) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(r) // a Record always marshals
	return fsutil.WriteFileAtomic(file(r.ID, ".json"), b, 0o600)
}

func load(id string) (Record, error) {
	var r Record
	b, err := os.ReadFile(file(id, ".json"))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

// records reads every record, oldest first; unreadable ones are skipped.
func records() []Record {
	paths, _ := filepath.Glob(filepath.Join(Dir(), "*.json")) // the pattern is valid
	var out []Record
	for _, p := range paths {
		if r, err := load(strings.TrimSuffix(filepath.Base(p), ".json")); err == nil {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b Record) int { return a.Started.Compare(b.Started) })
	return out
}

func remove(id string) {
	for _, ext := range []string{".json", ".lock", ".log"} {
		os.Remove(file(id, ext))
	}
}

// Test seams.
var (
	lockFile  = proc.TryLock
	probeLock = proc.TryShared
)

// tryLock opens a lock file and takes an exclusive lock without waiting.
// On Unix the lock lives as long as any descriptor of that open file — so
// it can be handed to a child process, which then holds it alone.
func tryLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// held reports whether someone holds a lock: the liveness test for a
// tunnel's owner. Unlike a pid, it can't be fooled by pid reuse or a reboot.
// It probes with a shared lock, so two probes (two mek tunnel ls) never
// look like an owner to each other.
func held(path string) bool {
	f, err := os.OpenFile(path, os.O_RDONLY, 0) // never creates: no lock file, no owner
	if err != nil {
		return false
	}
	defer f.Close() // and with it the probe's lock
	return errors.Is(probeLock(f), proc.ErrLocked)
}
