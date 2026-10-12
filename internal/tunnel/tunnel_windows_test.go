//go:build windows

package tunnel

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/proc"
)

// The test binary doubles as the supervisor and the tunnel's process.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "helper-supervise":
			os.Exit(SuperviseMain(os.Stdin, os.Args[2]))
		case "helper-listen": // listen until killed
			ln, err := net.Listen("tcp", "127.0.0.1:"+os.Args[2])
			if err != nil {
				os.Exit(2)
			}
			for {
				if c, err := ln.Accept(); err == nil {
					c.Close()
				}
			}
		}
	}
	os.Exit(m.Run())
}

func winSetup(t *testing.T) {
	t.Helper()
	t.Setenv("MEK_HOME", t.TempDir())
	old := supervisorCommand
	supervisorCommand = func(id string) (*exec.Cmd, error) { return exec.Command(os.Args[0], "helper-supervise", id), nil }
	t.Cleanup(func() { supervisorCommand = old })
}

func winFreePort(t *testing.T) int {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func winSpec(id string, local int, args ...string) Spec {
	return Spec{Record: Record{ID: id, Context: "prod", Provider: "aws", Name: "db", Local: local, Class: "tunnel",
		Decision: "allowed", Target: "localhost:" + strconv.Itoa(local) + " → db", Command: append([]string{os.Args[0]}, args...)},
		Env: os.Environ()}
}

// A background tunnel on Windows: a detached supervisor takes the locks,
// runs the process in a Job Object, and `stop` ends it through its event.
func TestBackgroundOnWindows(t *testing.T) {
	winSetup(t)
	port := winFreePort(t)
	r, err := Start(winSpec("win0win0win0win0", port, "helper-listen", strconv.Itoa(port)), 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if is := List(""); len(is) != 1 || is[0].State.Name() != "running" || r.Owner == 0 {
		t.Fatalf("list: %+v", is)
	}
	if err := PortFree(port); err == nil {
		t.Error("the port isn't claimed")
	}
	if _, err := Stop([]string{"db"}, false, ""); err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second); err == nil {
		c.Close()
		t.Error("still listening after stop")
	}
	b, _ := os.ReadFile(audit.Path())
	var e audit.Entry
	json.Unmarshal(bytes.TrimSpace(b), &e)
	if e.Event != "end" || e.ExitCode != 1 || e.Session != r.ID {
		t.Errorf("audit end: %s", b)
	}
}

func TestSuperviseMainErrors(t *testing.T) {
	winSetup(t)
	old := takeOver
	takeOver = 100 * time.Millisecond
	t.Cleanup(func() { takeOver = old })
	in := func(local int) *strings.Reader {
		b, _ := json.Marshal(winSpec("e", local))
		return strings.NewReader(string(b))
	}
	logOf := func(id string) string { b, _ := os.ReadFile(LogPath(id)); return string(b) }

	life, _ := tryLock(file("held", ".lock")) // the starting mek never lets go
	if code := SuperviseMain(in(1), "held"); code != 1 || !strings.Contains(logOf("held"), "taking the tunnel's lock") {
		t.Errorf("life lock: %d %q", code, logOf("held"))
	}
	life.Close()

	port, _ := tryLock(portLockPath(7))
	if code := SuperviseMain(in(7), "busy"); code != 1 || !strings.Contains(logOf("busy"), "localhost:7 is already forwarded") {
		t.Errorf("port lock: %d %q", code, logOf("busy"))
	}
	port.Close()

	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("MEK_HOME", filepath.Join(file, "x")) // no log can be opened
	if code := SuperviseMain(in(1), "nolog"); code != 1 {
		t.Errorf("no log: %d", code)
	}
}

// A foreground tunnel on Windows listens for its stop event.
func TestForegroundStopOnWindows(t *testing.T) {
	winSetup(t)
	stop, done, err := Foreground(Record{ID: "fgwinfgwin", Context: "prod", Local: winFreePort(t)})
	if err != nil || stop == nil {
		t.Fatalf("foreground: %v %v", stop, err)
	}
	defer done()
	proc.RequestStop("fgwinfgwin", 0)
	select {
	case <-stop:
	case <-time.After(5 * time.Second):
		t.Fatal("stop not seen")
	}
}
