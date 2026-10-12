//go:build windows

package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/prateep-r/mek/internal/proc"
)

// handOver: Windows can't pass the locks to a child, so the supervisor
// takes them itself (see SuperviseMain) once this mek lets go.
func handOver(*exec.Cmd, *os.File, *os.File, *os.File) {}

// takeOver is how long the supervisor waits for the starting mek to let go
// of the locks.
var takeOver = 5 * time.Second

// SuperviseMain is `mek tunnel _supervise`: it opens the tunnel's log and
// takes its locks, waiting for the mek that started it to let go of them.
func SuperviseMain(in io.Reader, id string) int {
	log, err := os.OpenFile(LogPath(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 1
	}
	b, _ := io.ReadAll(in)
	var s Spec
	json.Unmarshal(b, &s) // a bad spec is reported by Supervise
	life, err := takeLock(file(id, ".lock"))
	if err != nil {
		fmt.Fprintf(log, "mek: taking the tunnel's lock: %v\n", err)
		log.Close()
		return 1
	}
	port, err := takeLock(portLockPath(s.Local))
	if err != nil {
		fmt.Fprintf(log, "mek: localhost:%d is already forwarded by another mek tunnel\n", s.Local)
		life.Close()
		log.Close()
		return 1
	}
	stop, done := proc.StopRequests(id)
	defer done()
	return Supervise(bytes.NewReader(b), life, port, log, stop)
}

func takeLock(path string) (*os.File, error) {
	deadline := time.Now().Add(takeOver)
	for {
		f, err := tryLock(path)
		if !errors.Is(err, proc.ErrLocked) || time.Now().After(deadline) {
			return f, err
		}
		time.Sleep(pollEvery)
	}
}

// foregroundStop: Windows has no SIGTERM, so a foreground mek listens for
// the tunnel's stop event and the runner ends the tunnel's process.
func foregroundStop(id string) (<-chan struct{}, func()) { return proc.StopRequests(id) }
