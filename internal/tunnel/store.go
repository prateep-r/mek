package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/fsutil"
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
	Phase      string    `json:"phase"`     // starting | running | exited, as last written
	Owner      int       `json:"owner_pid"` // holds the lock: the supervisor, or mek in the foreground
	Child      int       `json:"child_pid"` // the tunnel's process (its own process group)
	ExitCode   int       `json:"exit_code"`
	Started    time.Time `json:"started"`
	Ended      time.Time `json:"ended,omitzero"`
}

// Phases a record is written with; State adds "dead" for owners that vanished.
const (
	phaseStarting = "starting"
	phaseRunning  = "running"
	phaseExited   = "exited"
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

var (
	errLocked = errors.New("locked")
	flock     = syscall.Flock // test seam
)

// tryLock opens a lock file and takes an exclusive flock without waiting.
// The lock lives as long as any descriptor of that open file — so it can be
// handed to a child process, which then holds it alone.
func tryLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLocked
		}
		return nil, err
	}
	return f, nil
}

// held reports whether someone holds a lock: the liveness test for a
// tunnel's owner. Unlike a pid, it can't be fooled by pid reuse or a reboot.
func held(path string) bool {
	f, err := tryLock(path)
	if err != nil {
		return errors.Is(err, errLocked)
	}
	f.Close()
	return false
}
