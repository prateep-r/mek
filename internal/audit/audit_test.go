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
		"aws ssh -p 2222": "aws ssh -p 2222", // -p is a secret only for az
		"":                "",
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
