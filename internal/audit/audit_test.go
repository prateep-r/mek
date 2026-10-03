package audit

import (
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
	"github.com/prateep-r/mek/internal/runner"
)

func TestMask(t *testing.T) {
	in := []string{"rds", "modify-db-instance", "--master-user-password", "hunter2",
		"--secret-string=abc", "--db-instance-identifier", "db1", "--no-paginate"}
	got := strings.Join(Mask(in), " ")
	want := "rds modify-db-instance --master-user-password **** --secret-string=**** --db-instance-identifier db1 --no-paginate"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if in[3] != "hunter2" {
		t.Fatal("Mask must not modify its input")
	}
}

func TestWrite(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	if err := Write(Entry{Context: "uat", Command: []string{"aws", "--token", "x"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"--token","****"`) || strings.Contains(string(b), `"x"`) {
		t.Fatalf("audit line not masked: %s", b)
	}
}

func TestMaskCLISpecificShortFlags(t *testing.T) {
	cases := map[string]string{
		"az login --service-principal -u app -p S3cret --tenant t":     "az login --service-principal -u app -p **** --tenant t",
		"az storage blob list --account-key KEY== --account-name a":    "az storage blob list --account-key **** --account-name a",
		"az storage blob list --connection-string=DefaultEndpoints...": "az storage blob list --connection-string=****",
		"hcloud obs config -i=AK -k=SK -t=TOKEN":                       "hcloud obs config -i=AK -k=**** -t=****",
		"/usr/local/bin/az acr login -n reg -p pw":                     "/usr/local/bin/az acr login -n reg -p ****",
		"kubectl create secret generic db --from-literal=password=x":   "kubectl create secret generic db --from-literal=****",
		"kubectl get pods --token abc":                                 "kubectl get pods --token ****",
		"aws ssh -p 2222":                                              "aws ssh -p 2222", // -p is a secret only for az
		"":                                                             "",
	}
	for in, want := range cases {
		if got := strings.Join(Mask(strings.Fields(in)), " "); got != want {
			t.Errorf("Mask(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestRecorder(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	ctx := &config.Context{Name: "prod", Provider: "aws"}
	boom := errors.New("blocked")
	next := runner.Func(func(inv *runner.Invocation) error {
		inv.Decision, inv.ExitCode = "blocked", -1
		return boom
	})
	inv := &runner.Invocation{Context: ctx, Argv: []string{"aws", "ec2", "terminate-instances"}, Class: guard.Destructive}
	if err := Recorder(nil)(next).Run(inv); err != boom {
		t.Fatalf("the chain's error must pass through: %v", err)
	}
	b, _ := os.ReadFile(Path())
	var e Entry
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	if e.Context != "prod" || e.Decision != "blocked" || e.ExitCode != -1 || e.Class != "destructive" || e.Time.IsZero() {
		t.Errorf("entry: %+v", e)
	}

	// A failed write is reported, and never fails the command.
	t.Setenv("MEK_HOME", filepath.Join(Path(), "not-a-dir"))
	var reported error
	ok := runner.Func(func(*runner.Invocation) error { return nil })
	if err := Recorder(func(err error) { reported = err })(ok).Run(inv); err != nil || reported == nil {
		t.Errorf("err=%v reported=%v", err, reported)
	}
	if err := Recorder(nil)(ok).Run(inv); err != nil {
		t.Errorf("nil onError: %v", err)
	}
}

func TestWriteErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("MEK_HOME", filepath.Join(file, "sub")) // MkdirAll fails under a file
	if err := Write(Entry{}); err == nil {
		t.Error("expected MkdirAll error")
	}
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	os.Mkdir(Path(), 0o700) // audit.jsonl is a directory: OpenFile fails
	if err := Write(Entry{}); err == nil {
		t.Error("expected OpenFile error")
	}
}

func TestUsername(t *testing.T) {
	t.Setenv("USER", "alice")
	if got := username(); got != "alice" {
		t.Errorf("USER: %q", got)
	}
	t.Setenv("USER", "")
	t.Setenv("LOGNAME", "bob")
	if got := username(); got != "bob" {
		t.Errorf("LOGNAME: %q", got)
	}
	t.Setenv("LOGNAME", "")
	old := currentUser
	t.Cleanup(func() { currentUser = old })
	currentUser = func() (*user.User, error) { return &user.User{Username: "carol"}, nil }
	if got := username(); got != "carol" {
		t.Errorf("fallback: %q", got)
	}
	currentUser = func() (*user.User, error) { return nil, errors.New("no passwd entry") }
	if got := username(); got != "" {
		t.Errorf("unknown user: %q", got)
	}
}

func TestMaskDashValues(t *testing.T) {
	cases := map[string]string{
		"aws rds create-db-instance --master-user-password -h4x0r":   "aws rds create-db-instance --master-user-password ****",
		"az keyvault secret set --value x --secret-string --vault v": "az keyvault secret set --value x --secret-string --vault v", // next is a long flag
	}
	for in, want := range cases {
		if got := strings.Join(Mask(strings.Fields(in)), " "); got != want {
			t.Errorf("Mask(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

// small makes the log rotate after a few bytes, for the length of the test.
func small(t *testing.T, size int64, files int) {
	t.Helper()
	oldSize, oldKeep := maxSize, keep
	maxSize, keep = size, files
	t.Cleanup(func() { maxSize, keep = oldSize, oldKeep })
}

func TestRotation(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	small(t, 1, 2) // every write finds a full log; keep audit.jsonl.1 and .2
	for _, ctx := range []string{"a", "b", "c", "d"} {
		if err := Write(Entry{Context: ctx}); err != nil {
			t.Fatal(err)
		}
	}
	for file, want := range map[string]string{"": `"context":"d"`, ".1": `"context":"c"`, ".2": `"context":"b"`} {
		b, err := os.ReadFile(Path() + file)
		if err != nil || !strings.Contains(string(b), want) || strings.Count(string(b), "\n") != 1 {
			t.Errorf("audit.jsonl%s = %q (%v), want one entry with %s", file, b, err, want)
		}
	}
	if _, err := os.Stat(Path() + ".3"); err == nil {
		t.Error("only keep=2 rotated files may be kept")
	}
}

func TestRotationUnderLimitDoesNothing(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	Write(Entry{Context: "a"})
	Write(Entry{Context: "b"})
	if _, err := os.Stat(Path() + ".1"); err == nil {
		t.Error("a log under maxSize must not rotate")
	}
}

func TestRotationErrorsKeepTheEntry(t *testing.T) {
	small(t, 1, 3)
	oldFlock := flock
	t.Cleanup(func() { flock = oldFlock })
	cases := []struct {
		name  string
		setup func(home string)
	}{
		{"lock file can't be opened", func(home string) { os.Mkdir(filepath.Join(home, "audit.jsonl.lock"), 0o700) }},
		{"lock can't be taken", func(string) { flock = func(int, int) error { return errors.New("no lock") } }},
		{"rename fails", func(home string) { // every slot is a non-empty dir: nothing can move
			for _, n := range []string{"1", "2", "3"} {
				os.MkdirAll(filepath.Join(home, "audit.jsonl."+n, "x"), 0o700)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("MEK_HOME", home)
			flock = oldFlock
			os.WriteFile(Path(), []byte("{}\n"), 0o600)
			c.setup(home)
			if err := Write(Entry{Context: "kept"}); err == nil {
				t.Error("rotation failure should be reported")
			}
			if b, _ := os.ReadFile(Path()); !strings.Contains(string(b), `"context":"kept"`) {
				t.Errorf("the entry was lost: %q", b)
			}
		})
	}
}

// Two mek processes find a full log; the second gets the lock after the
// first rotated and must not rotate again.
func TestRotationRaceRotatesOnce(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	small(t, 1, 3)
	os.WriteFile(Path(), []byte("{}\n"), 0o600)
	oldFlock := flock
	t.Cleanup(func() { flock = oldFlock })
	flock = func(fd, how int) error {
		os.Rename(Path(), Path()+".1") // the other process rotated meanwhile
		return oldFlock(fd, how)
	}
	if err := rotate(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path() + ".2"); err == nil {
		t.Error("rotated twice")
	}
}

func readEntries(t *testing.T) []Entry {
	t.Helper()
	b, _ := os.ReadFile(Path())
	var out []Entry
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// A session that runs is logged when it starts and when it ends; one the
// guard stops only once, without an event.
func TestSessionEntries(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	ctx := &config.Context{Name: "prod", Provider: "aws"}
	decide := func(d string) runner.Decorator {
		return func(next runner.Runner) runner.Runner {
			return runner.Func(func(inv *runner.Invocation) error {
				inv.Decision = d
				if d == "blocked" {
					inv.ExitCode = -1
					return errors.New("blocked")
				}
				return next.Run(inv)
			})
		}
	}
	exec := runner.Func(func(inv *runner.Invocation) error {
		if len(readEntries(t)) == 0 || readEntries(t)[0].Event != "start" {
			t.Error("start entry must be written before the session runs")
		}
		inv.ExitCode = 130
		return nil
	})
	inv := &runner.Invocation{Context: ctx, Argv: []string{"aws", "ssm", "start-session", "--target", "i-1"},
		Class: guard.Shell, Session: "abc", Target: "bastion → i-1"}
	runner.Chain(exec, Recorder(nil), decide("confirmed"), SessionStart(nil)).Run(inv)
	es := readEntries(t)
	if len(es) != 2 {
		t.Fatalf("entries: %+v", es)
	}
	start, end := es[0], es[1]
	if start.Event != "start" || start.Session != "abc" || start.Target != "bastion → i-1" || start.Decision != "confirmed" || start.ExitCode != 0 || start.Class != "shell" {
		t.Errorf("start: %+v", start)
	}
	if end.Event != "end" || end.Session != "abc" || end.ExitCode != 130 {
		t.Errorf("end: %+v", end)
	}

	os.Remove(Path())
	inv = &runner.Invocation{Context: ctx, Argv: []string{"aws"}, Class: guard.Shell, Session: "def"}
	runner.Chain(exec, Recorder(nil), decide("blocked"), SessionStart(nil)).Run(inv)
	if es := readEntries(t); len(es) != 1 || es[0].Event != "" || es[0].Decision != "blocked" || es[0].Session != "def" {
		t.Errorf("blocked session: %+v", es)
	}

	// Not a session: SessionStart passes through without writing.
	os.Remove(Path())
	ran := false
	SessionStart(nil)(runner.Func(func(*runner.Invocation) error { ran = true; return nil })).Run(&runner.Invocation{Context: ctx})
	if _, err := os.Stat(Path()); !ran || !os.IsNotExist(err) {
		t.Errorf("non-session: ran=%v err=%v", ran, err)
	}

	// A failed start write is reported and the session still runs.
	os.WriteFile(Path(), nil, 0o600)
	t.Setenv("MEK_HOME", filepath.Join(Path(), "not-a-dir"))
	var reported error
	ran = false
	ok := runner.Func(func(*runner.Invocation) error { ran = true; return nil })
	SessionStart(func(err error) { reported = err })(ok).Run(&runner.Invocation{Context: ctx, Session: "x"})
	if !ran || reported == nil {
		t.Errorf("ran=%v reported=%v", ran, reported)
	}
	SessionStart(nil)(ok).Run(&runner.Invocation{Context: ctx, Session: "x"})
}
