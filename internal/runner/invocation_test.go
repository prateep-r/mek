package runner

import (
	"errors"
	"strings"
	"testing"
)

func TestChainOrder(t *testing.T) {
	var trail []string
	tag := func(name string) Decorator {
		return func(next Runner) Runner {
			return Func(func(inv *Invocation) error {
				trail = append(trail, name+">")
				err := next.Run(inv)
				trail = append(trail, "<"+name)
				return err
			})
		}
	}
	core := Func(func(*Invocation) error { trail = append(trail, "exec"); return nil })
	if err := Chain(core, tag("audit"), tag("guard")).Run(&Invocation{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(trail, " "); got != "audit> guard> exec <guard <audit" {
		t.Errorf("order: %s", got)
	}
}

func TestInvocationErr(t *testing.T) {
	boom := errors.New("boom")
	if err := (&Invocation{ExitCode: 2}).Err(boom); err != boom {
		t.Errorf("an error wins over the exit code: %v", err)
	}
	var exit *ExitError
	if err := (&Invocation{ExitCode: 2}).Err(nil); !errors.As(err, &exit) || exit.Code != 2 {
		t.Errorf("non-zero exit: %v", err)
	}
	if err := (&Invocation{}).Err(nil); err != nil {
		t.Errorf("success: %v", err)
	}
}

func TestExecFillsResults(t *testing.T) {
	inv := &Invocation{Argv: []string{"sh", "-c", "exit 4"}}
	if err := Exec.Run(inv); err != nil {
		t.Fatal(err)
	}
	if inv.ExitCode != 4 || inv.Duration <= 0 {
		t.Errorf("exit=%d duration=%v", inv.ExitCode, inv.Duration)
	}
}
