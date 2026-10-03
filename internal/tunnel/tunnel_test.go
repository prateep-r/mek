package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/prateep-r/mek/internal/audit"
)

// The test binary doubles as the supervisor and as tunnel processes.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "helper-supervise":
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGTERM)
			os.Exit(Supervise(os.Stdin, os.NewFile(3, "life"), os.NewFile(4, "port"), os.NewFile(5, "log"), sigs))
		case "helper-listen": // listen on a port; TERM ends it with 143
			ln, err := net.Listen("tcp", "127.0.0.1:"+os.Args[2])
			if err != nil {
				os.Exit(2)
			}
			fmt.Println("helper: listening")
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGTERM)
			go func() {
				for {
					if c, err := ln.Accept(); err == nil {
						c.Close()
					}
				}
			}()
			<-sigs
			os.Exit(143)
		case "helper-stubborn": // ignores TERM
			signal.Ignore(syscall.SIGTERM)
			fmt.Println("helper: stubborn")
			time.Sleep(time.Minute)
			os.Exit(0)
		case "helper-exit":
			fmt.Println("helper: failing")
			code, _ := strconv.Atoi(os.Args[2])
			os.Exit(code)
		}
	}
	os.Exit(m.Run())
}

func setup(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	old := supervisorCommand
	supervisorCommand = func(id string) (*exec.Cmd, error) { return exec.Command(os.Args[0], "helper-supervise", id), nil }
	t.Cleanup(func() { supervisorCommand = old })
	return home
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func spec(id, name string, local int, cmd ...string) Spec {
	return Spec{Record: Record{ID: id, Context: "prod", Provider: "aws", Name: name, Local: local, Class: "tunnel",
		Decision: "allowed", Target: "localhost:" + strconv.Itoa(local) + " → db:5432", Command: append([]string{os.Args[0]}, cmd...)},
		Env: os.Environ()}
}

func auditEntries(t *testing.T) []audit.Entry {
	t.Helper()
	b, _ := os.ReadFile(audit.Path())
	var out []audit.Entry
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var e audit.Entry
		json.Unmarshal([]byte(line), &e)
		out = append(out, e)
	}
	return out
}

func TestBackgroundLifecycle(t *testing.T) {
	setup(t)
	port := freePort(t)
	r, err := Start(spec("abcdef0123456789", "db", port, "helper-listen", strconv.Itoa(port)), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Phase != phaseRunning || r.Owner == 0 || r.Child == 0 || !r.Background {
		t.Errorf("started: %+v", r)
	}
	infos := List("")
	if len(infos) != 1 || infos[0].State.Name() != "running" {
		t.Fatalf("list: %+v", infos)
	}
	if infos := List("other"); len(infos) != 0 {
		t.Errorf("context filter: %+v", infos)
	}
	// The port is taken while it runs, and the error says by whom.
	if _, err := Start(spec("1111222233334444", "", port, "helper-listen", strconv.Itoa(port)), time.Second); err == nil ||
		!strings.Contains(err.Error(), "already forwarded by mek tunnel db (prod) — mek tunnel stop db") {
		t.Errorf("second start: %v", err)
	}
	var log bytes.Buffer
	if err := Logs(infos[0], &log, false); err != nil || !strings.Contains(log.String(), "mek: started") || !strings.Contains(log.String(), "helper: listening") {
		t.Errorf("logs: %q %v", log.String(), err)
	}

	stopped, err := Stop([]string{"abcd"}, false, "") // an id prefix
	if err != nil || len(stopped) != 1 {
		t.Fatalf("stop: %+v %v", stopped, err)
	}
	if _, err := os.Stat(file(r.ID, ".json")); !os.IsNotExist(err) {
		t.Error("record not removed")
	}
	es := auditEntries(t)
	if len(es) != 1 || es[0].Event != "end" || es[0].ExitCode != 143 || es[0].Session != r.ID || es[0].Target != r.Target {
		t.Errorf("audit end: %+v", es)
	}
	if err := PortFree(port); err != nil {
		t.Errorf("port after stop: %v", err)
	}
}

func TestStartFailures(t *testing.T) {
	home := setup(t)
	port := freePort(t)

	_, err := Start(Spec{Record: Record{ID: "x", Command: []string{"no-such-binary-mek"}}}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "no-such-binary-mek not found") {
		t.Errorf("missing binary: %v", err)
	}

	// The process dies before the port is ready: its log is in the error.
	r, err := Start(spec("dead0000dead0000", "", port, "helper-exit", "3"), 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "exited (code 3)") || !strings.Contains(err.Error(), "helper: failing") || r.ID == "" {
		t.Errorf("early exit: %+v %v", r, err)
	}
	if i, _ := Find("dead0000", ""); i.State.Name() != "exited (3)" {
		t.Errorf("state after early exit: %v", i.State.Name())
	}

	// Never ready: stopped after --wait.
	other := freePort(t)
	r, err = Start(spec("slow0000slow0000", "", port, "helper-listen", strconv.Itoa(other)), 500*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not ready after 500ms — stopped it") {
		t.Errorf("timeout: %v", err)
	}
	if _, err := os.Stat(file("slow0000slow0000", ".json")); !os.IsNotExist(err) {
		t.Error("timed-out tunnel not cleaned up")
	}

	// The supervisor can't start.
	supervisorCommand = func(string) (*exec.Cmd, error) { return nil, errors.New("no exe") }
	if _, err := Start(spec("s1", "", port), time.Second); err == nil || err.Error() != "no exe" {
		t.Errorf("supervisor command: %v", err)
	}
	supervisorCommand = func(string) (*exec.Cmd, error) { return exec.Command("/nonexistent/mek"), nil }
	if _, err := Start(spec("s2", "", port), time.Second); err == nil {
		t.Error("supervisor start error not reported")
	}

	// The lock or log can't be opened, or the record can't be saved.
	lock, _ := tryLock(file("held", ".lock"))
	if _, err := Start(spec("held", "", port), time.Second); !errors.Is(err, errLocked) {
		t.Errorf("held lock: %v", err)
	}
	lock.Close()
	os.MkdirAll(LogPath("logdir"), 0o700) // a directory where the log goes
	if _, err := Start(spec("logdir", "", port), time.Second); err == nil {
		t.Error("log open error not reported")
	}
	os.MkdirAll(file("savedir", ".json"), 0o700)
	if _, err := Start(spec("savedir", "", port), time.Second); err == nil {
		t.Error("save error not reported")
	}
	os.WriteFile(filepath.Join(home, "tunnels-file"), nil, 0o600)
	t.Setenv("MEK_HOME", filepath.Join(home, "tunnels-file"))
	if _, err := Start(spec("p", "", port), time.Second); err == nil {
		t.Error("port lock error not reported")
	}
}

func TestForeground(t *testing.T) {
	setup(t)
	port := freePort(t)
	done, err := Foreground(Record{ID: "fg00fg00fg00fg00", Context: "prod", Name: "db", Local: port})
	if err != nil {
		t.Fatal(err)
	}
	i, err := Find("db", "prod")
	if err != nil || i.Background || i.Owner != os.Getpid() || i.State.Name() != "running" {
		t.Errorf("foreground: %+v %v", i, err)
	}
	if _, err := Foreground(Record{ID: "other", Local: port}); err == nil || !strings.Contains(err.Error(), "already forwarded by mek tunnel db") {
		t.Errorf("same port: %v", err)
	}
	done()
	if infos := List(""); len(infos) != 0 {
		t.Errorf("after done: %+v", infos)
	}

	lock, _ := tryLock(file("held", ".lock"))
	if _, err := Foreground(Record{ID: "held", Local: port}); !errors.Is(err, errLocked) {
		t.Errorf("held lock: %v", err)
	}
	lock.Close()
	os.MkdirAll(file("savedir", ".json"), 0o700)
	if _, err := Foreground(Record{ID: "savedir", Local: port}); err == nil {
		t.Error("save error not reported")
	}
	if _, err := Foreground(Record{ID: "ok", Local: port}); err != nil { // the failed ones released the port
		t.Errorf("port not released: %v", err)
	}
}

// A port lock held without a record: some other mek is about to use it.
func TestClaimPortWithoutRecord(t *testing.T) {
	setup(t)
	f, _ := tryLock(portLockPath(5555))
	defer f.Close()
	if _, err := claimPort(5555); err == nil || err.Error() != "localhost:5555 is already forwarded by another mek tunnel" {
		t.Errorf("claim: %v", err)
	}
}

func TestStates(t *testing.T) {
	setup(t)
	var signals []string
	kill = func(pid int, sig syscall.Signal) error {
		signals = append(signals, fmt.Sprintf("%d:%d", pid, sig))
		return nil
	}
	oldTimeout, oldPoll := stopTimeout, pollEvery
	stopTimeout, pollEvery = 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { kill, stopTimeout, pollEvery = syscall.Kill, oldTimeout, oldPoll })

	save(Record{ID: "ex", Phase: phaseExited, ExitCode: 255})
	save(Record{ID: "dd", Phase: phaseRunning})
	lock, _ := tryLock(file("st", ".lock")) // an owner that is alive
	save(Record{ID: "st", Phase: phaseStarting, Owner: 4242, Child: 4343})
	for id, want := range map[string]string{"ex": "exited (255)", "dd": "dead", "st": "starting"} {
		r, _ := load(id)
		if got := stateOf(r).Name(); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
	for _, id := range []string{"ex", "dd"} { // nothing to signal: just cleaned up
		r, _ := load(id)
		if err := stateOf(r).Stop(r); err != nil {
			t.Error(err)
		}
	}
	if len(signals) != 0 {
		t.Errorf("signalled a finished/dead tunnel: %v", signals)
	}

	// An owner that ignores TERM is killed with its process group.
	r, _ := load("st")
	err := stateOf(r).Stop(r)
	if err == nil || !strings.Contains(err.Error(), "did not stop within 200ms — killed") ||
		strings.Join(signals, ",") != "4242:15,-4343:9,4242:9" {
		t.Errorf("forced stop: %v %v", err, signals)
	}
	lock.Close()

	// An owner that stops when asked.
	signals = nil
	lock, _ = tryLock(file("ok", ".lock"))
	save(Record{ID: "ok", Phase: phaseRunning, Owner: 77})
	kill = func(pid int, sig syscall.Signal) error { lock.Close(); return nil }
	r, _ = load("ok")
	if err := stateOf(r).Stop(r); err != nil {
		t.Errorf("graceful stop: %v", err)
	}
	if _, err := os.Stat(file("ok", ".json")); !os.IsNotExist(err) {
		t.Error("not cleaned up")
	}
	r = Record{ID: "nochild", Phase: phaseRunning, Owner: 1}
	lock, _ = tryLock(file("nochild", ".lock"))
	kill = func(int, syscall.Signal) error { return nil }
	signals = nil
	kill = func(pid int, sig syscall.Signal) error {
		signals = append(signals, fmt.Sprintf("%d:%d", pid, sig))
		return nil
	}
	if err := (running{}).Stop(r); err == nil || len(signals) != 0 { // pid 1 and no child: nothing is signalled
		t.Errorf("forced stop without pids: %v %v", err, signals)
	}
	lock.Close()
}

func TestFindAndStop(t *testing.T) {
	setup(t)
	save(Record{ID: "aaaa1111aaaa1111", Context: "prod", Name: "db", Phase: phaseExited})
	save(Record{ID: "bbbb2222bbbb2222", Context: "uat", Name: "db", Phase: phaseExited, Started: time.Now()})
	os.WriteFile(file("broken", ".json"), []byte("{"), 0o600) // skipped
	if _, err := Find("db", ""); err == nil || !strings.Contains(err.Error(), `"db" matches 2 tunnels: aaaa1111 (prod), bbbb2222 (uat)`) {
		t.Errorf("ambiguous: %v", err)
	}
	if i, err := Find("db", "uat"); err != nil || i.ID != "bbbb2222bbbb2222" {
		t.Errorf("by context: %+v %v", i, err)
	}
	if _, err := Find("aaa", ""); err == nil || !strings.Contains(err.Error(), `no tunnel "aaa"`) { // too short a prefix
		t.Errorf("short prefix: %v", err)
	}
	stopped, err := Stop([]string{"nope"}, true, "prod")
	if len(stopped) != 1 || stopped[0].Context != "prod" || err == nil {
		t.Errorf("stop --all -c prod plus a miss: %+v %v", stopped, err)
	}
	if infos := List(""); len(infos) != 1 || infos[0].Context != "uat" {
		t.Errorf("left: %+v", infos)
	}
	if (Record{ID: "abc"}).ShortID() != "abc" || (Record{ID: "abcdefghij"}).Label() != "abcdefgh" || (Record{Name: "db"}).Label() != "db" {
		t.Error("labels")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestLogs(t *testing.T) {
	setup(t)
	save(Record{ID: "lg"})
	os.WriteFile(LogPath("lg"), []byte("one\n"), 0o600)
	lock, _ := tryLock(file("lg", ".lock"))
	go func() {
		time.Sleep(50 * time.Millisecond)
		f, _ := os.OpenFile(LogPath("lg"), os.O_APPEND|os.O_WRONLY, 0o600)
		f.WriteString("two\n")
		f.Close()
		time.Sleep(50 * time.Millisecond)
		lock.Close() // the tunnel ends: follow returns
	}()
	var out bytes.Buffer
	if err := Logs(Info{Record: Record{ID: "lg"}}, &out, true); err != nil || out.String() != "one\ntwo\n" {
		t.Errorf("follow: %q %v", out.String(), err)
	}
	if err := Logs(Info{Record: Record{ID: "nolog"}}, io.Discard, false); err == nil {
		t.Error("missing log")
	}
	if err := Logs(Info{Record: Record{ID: "lg"}}, failWriter{}, false); err == nil {
		t.Error("write error")
	}
	if got := logTail("lg"); got != "  one\n  two" {
		t.Errorf("tail: %q", got)
	}
}

// The supervisor run in-process: lifecycle, signals, grace kill.
func TestSupervise(t *testing.T) {
	setup(t)
	files := func() (*os.File, *os.File, *os.File) {
		life, _ := tryLock(file("sv", ".lock"))
		port, _ := tryLock(portLockPath(1))
		log, _ := os.OpenFile(LogPath("sv"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		return life, port, log
	}
	in := func(s Spec) io.Reader {
		s.Path = s.Command[0]
		b, _ := json.Marshal(s)
		return bytes.NewReader(b)
	}
	port := freePort(t)
	sigs := make(chan os.Signal, 1)
	go func() {
		for {
			if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
				c.Close()
				sigs <- syscall.SIGTERM
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	life, p, log := files()
	if code := Supervise(in(spec("sv", "db", port, "helper-listen", strconv.Itoa(port))), life, p, log, sigs); code != 143 {
		t.Errorf("stopped: exit %d", code)
	}
	r, _ := load("sv")
	if r.Phase != phaseExited || r.ExitCode != 143 || r.Owner != os.Getpid() || r.Ended.IsZero() {
		t.Errorf("record: %+v", r)
	}
	if held(file("sv", ".lock")) {
		t.Error("lock still held after Supervise")
	}
	b, _ := os.ReadFile(LogPath("sv"))
	if !strings.Contains(string(b), "mek: exited with code 143") {
		t.Errorf("log: %s", b)
	}

	// A process that ignores TERM is killed after the grace period.
	old := grace
	grace = 100 * time.Millisecond
	t.Cleanup(func() { grace = old })
	go func() { // signal once the helper ignores TERM
		for {
			if b, _ := os.ReadFile(LogPath("sv")); strings.Contains(string(b), "helper: stubborn") {
				sigs <- syscall.SIGTERM
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	life, p, log = files()
	if code := Supervise(in(spec("sv", "", 0, "helper-stubborn")), life, p, log, sigs); code != 128+9 {
		t.Errorf("grace kill: exit %d", code)
	}

	life, p, log = files()
	bad := spec("sv", "", 0)
	bad.Command = []string{"/nonexistent/tool"}
	if code := Supervise(in(bad), life, p, log, sigs); code != 127 {
		t.Errorf("start failure: exit %d", code)
	}
	life, p, log = files()
	if code := Supervise(strings.NewReader("{"), life, p, log, sigs); code != 1 {
		t.Errorf("bad spec: exit %d", code)
	}
}

func TestObserverErrors(t *testing.T) {
	home := setup(t)
	var got []error
	note := func(err error) { got = append(got, err) }
	os.WriteFile(filepath.Join(home, "file"), nil, 0o600)
	t.Setenv("MEK_HOME", filepath.Join(home, "file"))
	stateStore{note}.Notify(Event{Kind: Started, Record: Record{ID: "x"}})
	auditSink{note}.Notify(Event{Kind: Exited, Record: Record{ID: "x"}})
	auditSink{note}.Notify(Event{Kind: Started}) // only exits are audited
	stateStore{}.Notify(Event{Record: Record{ID: "x"}})
	auditSink{}.Notify(Event{Kind: Exited})
	if len(got) != 2 {
		t.Errorf("reported: %v", got)
	}
}

func TestLockErrors(t *testing.T) {
	home := setup(t)
	os.WriteFile(filepath.Join(home, "file"), nil, 0o600)
	if _, err := tryLock(filepath.Join(home, "file", "x.lock")); err == nil {
		t.Error("mkdir error")
	}
	os.MkdirAll(filepath.Join(home, "dir.lock"), 0o700)
	if _, err := tryLock(filepath.Join(home, "dir.lock")); err == nil {
		t.Error("open error")
	}
	if held(filepath.Join(home, "dir.lock")) {
		t.Error("an unopenable lock is not held")
	}
}

func TestRemainingPaths(t *testing.T) {
	home := setup(t)
	if _, err := load("missing"); err == nil {
		t.Error("load of a missing record")
	}

	old := flock
	flock = func(int, int) error { return syscall.EINVAL }
	if _, err := tryLock(filepath.Join(home, "x.lock")); !errors.Is(err, syscall.EINVAL) {
		t.Errorf("flock error: %v", err)
	}
	flock = old

	// The default supervisor command is this mek, `tunnel _supervise <id>`.
	cmd, err := defaultSupervisor("abc")
	if exe, _ := os.Executable(); err != nil || cmd.Path != exe || strings.Join(cmd.Args[1:], " ") != "tunnel _supervise abc" {
		t.Errorf("supervisor command: %v %v", cmd, err)
	}
	executable = func() (string, error) { return "", errors.New("no exe") }
	if _, err := defaultSupervisor("abc"); err == nil {
		t.Error("executable error")
	}
	executable = os.Executable

	// Timing out before the supervisor wrote its record: its pid is used.
	var signals []int
	kill = func(pid int, _ syscall.Signal) error { signals = append(signals, pid); return nil }
	t.Cleanup(func() { kill = syscall.Kill })
	lock, _ := tryLock(file("early", ".lock"))
	stopTimeout = 50 * time.Millisecond
	t.Cleanup(func() { stopTimeout = 10 * time.Second })
	if _, err := waitReady(Record{ID: "early", Local: freePort(t)}, 4242, 50*time.Millisecond, nil); err == nil || len(signals) == 0 || signals[0] != 4242 {
		t.Errorf("early timeout: %v %v", err, signals)
	}
	lock.Close()

	// Stop reports a tunnel that wouldn't stop.
	lock, _ = tryLock(file("stuck0000", ".lock"))
	save(Record{ID: "stuck0000", Phase: phaseRunning, Owner: 4242})
	if _, err := Stop([]string{"stuck0000"}, false, ""); err == nil {
		t.Error("stuck tunnel")
	}
	lock.Close()

	// The supervisor reports failures to save its state in the log.
	os.WriteFile(filepath.Join(home, "file"), nil, 0o600)
	logf, _ := os.CreateTemp(t.TempDir(), "log")
	life, _ := os.CreateTemp(t.TempDir(), "life")
	port, _ := os.CreateTemp(t.TempDir(), "port")
	t.Setenv("MEK_HOME", filepath.Join(home, "file"))
	s := spec("sv2", "", 0, "helper-exit", "0")
	s.Path = s.Command[0]
	b, _ := json.Marshal(s)
	Supervise(bytes.NewReader(b), life, port, logf, make(chan os.Signal))
	out, _ := os.ReadFile(logf.Name())
	if !strings.Contains(string(out), "mek: ") || !strings.Contains(string(out), "not a directory") {
		t.Errorf("log: %s", out)
	}
}

// Finished and dead tunnels don't hold their port.
func TestPortFreeIgnoresEndedTunnels(t *testing.T) {
	setup(t)
	save(Record{ID: "ex", Local: 6000, Phase: phaseExited})
	save(Record{ID: "dd", Local: 6000, Phase: phaseRunning}) // no owner holds its lock
	if err := PortFree(6000); err != nil {
		t.Errorf("ended tunnels block the port: %v", err)
	}
}
