package runner

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		argv []string
		want int
	}{
		{[]string{"true"}, 0},
		{[]string{"sh", "-c", "exit 3"}, 3},
		{[]string{"sh", "-c", "kill -TERM $$"}, 128 + 15}, // killed by a signal
		{[]string{"sh", "-c", `test "$MEK_TEST" = yes`}, 0},
	}
	for _, c := range cases {
		code, err := Run(c.argv, append(os.Environ(), "MEK_TEST=yes"))
		if err != nil || code != c.want {
			t.Errorf("Run(%q) = %d, %v; want %d", c.argv, code, err, c.want)
		}
	}
	if code, err := Run([]string{"mek-no-such-command"}, nil); err == nil || code != 127 {
		t.Errorf("missing command: code=%d err=%v, want 127 and an error", code, err)
	}
}

func TestRunErrors(t *testing.T) {
	if code, err := Run(nil, nil); err == nil || code != 1 {
		t.Errorf("empty argv: %d %v", code, err)
	}
	// Found and executable, but not a program: the kernel refuses to start it.
	bad := filepath.Join(t.TempDir(), "not-a-program")
	if err := os.WriteFile(bad, []byte("garbage\x00\x01"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, err := Run([]string{bad}, nil); err == nil || code != 1 {
		t.Errorf("start failure: %d %v", code, err)
	}
}

func TestResult(t *testing.T) {
	if code, err := result(nil); code != 0 || err != nil {
		t.Errorf("nil: %d %v", code, err)
	}
	boom := errors.New("boom")
	if code, err := result(boom); code != 1 || err != boom {
		t.Errorf("non-exit error: %d %v", code, err)
	}
	if got := (&ExitError{Code: 3}).Error(); got != "exit status 3" {
		t.Errorf("ExitError: %q", got)
	}
}

// mek ignores Ctrl-C while the child runs (the terminal already sent it to the
// child) and forwards SIGTERM, so `timeout`/k8s stop the child, not just mek.
func TestSignals(t *testing.T) {
	type res struct {
		code int
		err  error
	}
	done := make(chan res, 1)
	go func() {
		code, err := Run([]string{"sh", "-c", `trap "exit 7" TERM; while :; do sleep 0.05; done`}, os.Environ())
		done <- res{code, err}
	}()
	time.Sleep(300 * time.Millisecond) // let Run subscribe and start the child
	self, _ := os.FindProcess(os.Getpid())
	_ = self.Signal(os.Interrupt) // swallowed: not forwarded, child keeps running
	time.Sleep(200 * time.Millisecond)
	select {
	case r := <-done:
		t.Fatalf("SIGINT ended the run: %+v", r)
	default:
	}
	_ = self.Signal(syscall.SIGTERM) // forwarded: child's trap exits 7
	select {
	case r := <-done:
		if r.code != 7 || r.err != nil {
			t.Errorf("after SIGTERM: %+v, want code 7", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM was not forwarded to the child")
	}
}
