package cli

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/runner"
)

// fakeExec stands in for runner.Exec: it records calls instead of starting processes.
type fakeExec struct {
	calls int
	code  int
}

func (f *fakeExec) Run(inv *runner.Invocation) error {
	f.calls++
	inv.ExitCode = f.code
	return nil
}

func lastAudit(t *testing.T) audit.Entry {
	t.Helper()
	b, err := os.ReadFile(audit.Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var e audit.Entry
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestGuardedPipeline(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	cases := []struct {
		name     string
		ctx      config.Context
		opts     globalOpts
		class    guard.Class
		code     int
		ran      bool
		decision string
		exit     int
		errExit  int // expected *runner.ExitError code, 0 = none
		errText  string
	}{
		{"plain context runs anything", config.Context{Name: "dev"}, globalOpts{}, guard.Destructive, 0, true, "allowed", 0, 0, ""},
		{"child exit code reaches main", config.Context{Name: "dev"}, globalOpts{}, guard.Read, 3, true, "allowed", 3, 3, ""},
		{"protected write with --yes", config.Context{Name: "prod", Protected: true}, globalOpts{yes: true}, guard.Write, 0, true, "confirmed", 0, 0, ""},
		{"protected destructive with --confirm", config.Context{Name: "prod", Protected: true}, globalOpts{confirm: "prod"}, guard.Destructive, 0, true, "confirmed", 0, 0, ""},
		{"readonly blocks write", config.Context{Name: "ro", ReadOnly: true}, globalOpts{yes: true}, guard.Write, 0, false, "blocked", -1, 0, "readonly"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeExec{code: c.code}
			a := &app{opts: c.opts, exec: fake}
			ctx := c.ctx
			err := a.guarded(&loaded{ctx: &ctx, prov: describer{}}, []string{"aws", "s3", "ls"}, c.class)

			if (fake.calls == 1) != c.ran {
				t.Errorf("exec calls = %d, want ran=%v", fake.calls, c.ran)
			}
			var exit *runner.ExitError
			switch {
			case c.errText != "":
				if err == nil || !strings.Contains(err.Error(), c.errText) {
					t.Errorf("err = %v, want containing %q", err, c.errText)
				}
			case c.errExit != 0:
				if !errors.As(err, &exit) || exit.Code != c.errExit {
					t.Errorf("err = %v, want exit %d", err, c.errExit)
				}
			case err != nil:
				t.Errorf("unexpected err: %v", err)
			}
			// The audit decorator records every case, blocked ones included.
			e := lastAudit(t)
			if e.Context != ctx.Name || e.Decision != c.decision || e.ExitCode != c.exit || e.Class != c.class.String() {
				t.Errorf("audit = %+v, want decision=%s exit=%d", e, c.decision, c.exit)
			}
		})
	}
}

// describer is the minimal Provider banner() needs.
type describer struct{}

func (describer) CLI() string                            { return "aws" }
func (describer) Prepare() (provider.Env, error)         { return provider.Env{}, nil }
func (describer) LoginCommands(bool) ([][]string, error) { return nil, nil }
func (describer) WhoAmICommand() []string                { return nil }
func (describer) Describe() string                       { return "test" }
