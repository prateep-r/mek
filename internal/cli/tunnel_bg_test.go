package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/tunnel"
)

// stderr runs f and returns what it wrote to stderr (mek's status lines).
func stderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// writeRecord puts a tunnel record on disk, as a supervisor would.
func writeRecord(t *testing.T, r tunnel.Record) {
	t.Helper()
	os.MkdirAll(tunnel.Dir(), 0o700)
	b, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(tunnel.Dir(), r.ID+".json"), b, 0o600)
}

func TestTunnelBackground(t *testing.T) {
	h := newTunnelHarness(t)
	var got tunnel.Spec
	old := startTunnel
	t.Cleanup(func() { startTunnel = old })
	startTunnel = func(s tunnel.Spec, wait time.Duration) (tunnel.Record, error) {
		got = s
		if wait != 5*time.Second {
			t.Errorf("wait = %s", wait)
		}
		return s.Record, nil
	}
	h.mustRun("-c", "prod", "-y", "tunnel", "db", "-b", "--wait", "5s")
	if got.Name != "db" || got.Context != "prod" || got.Local != 25432 || got.ID == "" || got.Decision != "confirmed" ||
		got.Class != "tunnel" || !strings.HasPrefix(strings.Join(got.Command, " "), "aws ssm start-session --target i-0bastion0") ||
		len(got.Env) == 0 || len(h.exec.invs) != 1 { // only the lookup ran here; the supervisor runs the session
		t.Errorf("spec: %+v, calls %q", got, h.exec.argv())
	}
	// The supervisor writes the end entry: here only the lookup and the start.
	if es := readAudit(t); len(es) != 2 || es[1].Event != "start" || es[1].Session != got.ID {
		t.Errorf("audit: %+v", es)
	}

	// It failed after the supervisor ran: still its end entry, not ours.
	startTunnel = func(s tunnel.Spec, _ time.Duration) (tunnel.Record, error) { return s.Record, errors.New("not ready") }
	_, err := h.run("-c", "prod", "-y", "tunnel", "db", "-b")
	wantErr(t, err, "not ready")
	if es := readAudit(t); len(es) != 4 {
		t.Errorf("audit after a late failure: %+v", es)
	}
	// It failed before: mek ends the session it started.
	startTunnel = func(tunnel.Spec, time.Duration) (tunnel.Record, error) {
		return tunnel.Record{}, errors.New("no supervisor")
	}
	_, err = h.run("-c", "prod", "-y", "tunnel", "db", "-b")
	wantErr(t, err, "no supervisor")
	if es := readAudit(t); len(es) != 7 || es[6].Event != "end" || es[6].ExitCode != 1 {
		t.Errorf("audit after an early failure: %+v", es)
	}
}

func TestTunnelForegroundClaimsPort(t *testing.T) {
	h := newTunnelHarness(t)
	done, err := tunnel.Foreground(tunnel.Record{ID: "fg0000000000", Context: "prod", Name: "db", Local: 25432})
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	_, err = h.run("-c", "prod", "-y", "tunnel", "db")
	wantErr(t, err, "localhost:25432 is already forwarded by mek tunnel db (prod) — mek tunnel stop db")
	// Raced past the first check: the claim itself refuses.
	err = foreground("db", 25432)(h.exec).Run(&runner.Invocation{Context: &config.Context{Name: "prod"}})
	if err == nil || !strings.Contains(err.Error(), "already forwarded") {
		t.Errorf("claim: %v", err)
	}
}

func TestTunnelLsStopLogs(t *testing.T) {
	h := newTunnelHarness(t)
	if out := stderr(t, func() { h.mustRun("tunnel", "ls") }); !strings.Contains(out, "no tunnels") {
		t.Errorf("empty ls: %q", out)
	}
	done, _ := tunnel.Foreground(tunnel.Record{ID: "aaaa1111aaaa1111", Context: "prod", Name: "db", Local: 25432, Target: "localhost:25432 → db:5432"})
	defer done()
	writeRecord(t, tunnel.Record{ID: "bbbb2222bbbb2222", Context: "gcp", Local: 28080, Background: true, Phase: "exited", ExitCode: 255,
		Target: "localhost:28080 → port 8080", Started: time.Now().Add(-90 * time.Minute)})
	os.WriteFile(filepath.Join(tunnel.Dir(), "bbbb2222bbbb2222.log"), []byte("session closed\n"), 0o600)

	out := h.mustRun("tunnel", "ls")
	for _, want := range []string{"ID", "aaaa1111", "running (foreground)", "db", "bbbb2222", "exited (255)", "1h", "-  ", "localhost:28080 → port 8080"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls missing %q:\n%s", want, out)
		}
	}
	if out := h.mustRun("-c", "gcp", "tunnel", "ls"); strings.Contains(out, "aaaa1111") { // -c narrows
		t.Errorf("ls -c gcp:\n%s", out)
	}
	if out := h.mustRun("tunnel", "logs", "bbbb"); out != "session closed\n" {
		t.Errorf("logs: %q", out)
	}
	_, err := h.run("tunnel", "logs", "nope")
	wantErr(t, err, `no tunnel "nope"`)

	_, err = h.run("tunnel", "stop")
	wantErr(t, err, "name the tunnels to stop, or pass --all")
	h.mustRun("tunnel", "stop", "bbbb2222")
	if out := h.mustRun("tunnel", "ls"); strings.Contains(out, "bbbb2222") {
		t.Errorf("stopped tunnel still listed:\n%s", out)
	}
	if out := stderr(t, func() { h.mustRun("-c", "gcp", "tunnel", "stop", "--all") }); !strings.Contains(out, "no tunnels") {
		t.Errorf("stop --all with nothing: %q", out)
	}
	_, err = h.run("tunnel", "stop", "nope")
	wantErr(t, err, `no tunnel "nope"`)
}

// A dead tunnel whose process still holds the port: stop says so.
func TestTunnelStopDeadNote(t *testing.T) {
	h := newTunnelHarness(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	writeRecord(t, tunnel.Record{ID: "dead0000dead0000", Context: "prod", Local: port, Phase: "running"})
	out := stderr(t, func() { h.mustRun("tunnel", "stop", "dead0000") })
	if !strings.Contains(out, "localhost:"+strconv.Itoa(port)+" is still in use") {
		t.Errorf("stop of a dead tunnel: %q", out)
	}
}

func TestAge(t *testing.T) {
	now := time.Now()
	for d, want := range map[time.Duration]string{5 * time.Second: "5s", 12 * time.Minute: "12m", 3 * time.Hour: "3h", 72 * time.Hour: "3d"} {
		if got := age(now.Add(-d)); got != want {
			t.Errorf("age(%s) = %s, want %s", d, got, want)
		}
	}
}
