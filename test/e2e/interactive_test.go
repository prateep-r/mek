//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/prateep-r/mek/test/testkit"
)

// session is mek running on a pseudo-terminal, so it sees a real
// controlling terminal and asks its confirmation questions there — the way
// a person at a keyboard experiences it.
type session struct {
	t    *testing.T
	tty  *os.File
	mu   sync.Mutex
	out  bytes.Buffer
	done chan int
}

func startSession(t *testing.T, bin string, env []string, args ...string) *session {
	t.Helper()
	cmd := testkit.Command(bin, env, args...)
	tty, err := pty.Start(cmd) // new session with the pty as controlling terminal
	if err != nil {
		t.Fatal(err)
	}
	s := &session{t: t, tty: tty, done: make(chan int, 1)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := tty.Read(buf)
			s.mu.Lock()
			s.out.Write(buf[:n])
			s.mu.Unlock()
			if err != nil { // EOF on macOS, EIO on Linux once mek exits
				return
			}
		}
	}()
	go func() {
		cmd.Wait()
		s.done <- cmd.ProcessState.ExitCode()
	}()
	t.Cleanup(func() { tty.Close(); cmd.Process.Kill() })
	return s
}

func (s *session) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// expect waits until the terminal shows text.
func (s *session) expect(text string) {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(s.output(), text) {
		if time.Now().After(deadline) {
			s.t.Fatalf("terminal never showed %q; got:\n%s", text, s.output())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// send types keys into the terminal.
func (s *session) send(keys string) {
	s.t.Helper()
	if _, err := io.WriteString(s.tty, keys); err != nil && !errors.Is(err, os.ErrClosed) {
		s.t.Fatal(err)
	}
}

func (s *session) exitCode() int {
	s.t.Helper()
	select {
	case code := <-s.done:
		time.Sleep(50 * time.Millisecond) // let the reader drain the last output
		return code
	case <-time.After(10 * time.Second):
		s.t.Fatalf("mek did not exit; terminal:\n%s", s.output())
		return -1
	}
}

func TestInteractivePrompts(t *testing.T) {
	installed := installRelease(t)
	cases := []struct {
		name     string
		args     []string
		prompt   string
		keys     string
		code     int
		ran      bool
		decision string
	}{
		{"write, answer y", []string{"-c", "prod", "aws", "ec2", "run-instances"}, "Continue? [y/N]:", "y\r", 0, true, "confirmed"},
		{"write, answer n", []string{"-c", "prod", "aws", "ec2", "run-instances"}, "Continue? [y/N]:", "n\r", 1, false, "declined"},
		{"write, just Enter", []string{"-c", "prod", "aws", "ec2", "run-instances"}, "Continue? [y/N]:", "\r", 1, false, "declined"},
		{"destructive, wrong name", []string{"-c", "prod", "aws", "ec2", "terminate-instances"}, "Type prod to continue:", "dev\r", 1, false, "declined"},
		{"destructive, y is not enough", []string{"-c", "prod", "aws", "ec2", "terminate-instances"}, "Type prod to continue:", "y\r", 1, false, "declined"},
		{"destructive, typed name", []string{"-c", "prod", "aws", "ec2", "terminate-instances"}, "Type prod to continue:", "prod\r", 0, true, "confirmed"},
		{"Ctrl-D", []string{"-c", "prod", "aws", "ec2", "run-instances"}, "Continue? [y/N]:", "\x04", 1, false, "declined"},
		{"gcloud on a protected project", []string{"-c", "gcp-prod", "gcloud", "compute", "instances", "delete", "vm-1"}, "Type gcp-prod to continue:", "gcp-prod\r", 0, true, "confirmed"},
		{"exec asks too", []string{"-c", "prod", "exec", "--", "aws", "s3", "ls"}, "Continue? [y/N]:", "yes\r", 0, true, "confirmed"},
		{"shell, answer y", []string{"-c", "prod", "shell", "i-0123456789abcdef0"}, "shell command on prod", "y\r", 0, true, "confirmed"},
		{"shell, answer n", []string{"-c", "prod", "shell", "i-0123456789abcdef0"}, "Continue? [y/N]:", "n\r", 1, false, "declined"},
		{"gcp shell asks too", []string{"-c", "gcp-prod", "shell", "vm-1", "--zone", "z"}, "Continue? [y/N]:", "y\r", 0, true, "confirmed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mekHome := t.TempDir()
			stubs, log := testkit.Stubs(t, "aws", "gcloud", "session-manager-plugin")
			os.WriteFile(filepath.Join(mekHome, "config.yaml"), []byte(`contexts:
  prod:     {provider: aws, aws_profile: prod-admin, protected: true}
  gcp-prod: {provider: gcp, project: acme-prod, protected: true}
`), 0o600)
			env := []string{"MEK_HOME=" + mekHome, "HOME=" + t.TempDir(), "PATH=" + stubs + ":/usr/bin:/bin", "STUB_LOG=" + log}

			s := startSession(t, installed, env, c.args...)
			s.expect(c.prompt)
			s.send(c.keys)
			if code := s.exitCode(); code != c.code {
				t.Errorf("exit %d, want %d; terminal:\n%s", code, c.code, s.output())
			}
			if ran := len(testkit.Calls(t, log)) == 1; ran != c.ran {
				t.Errorf("CLI ran = %v, want %v", ran, c.ran)
			}
			audit, _ := os.ReadFile(filepath.Join(mekHome, "audit.jsonl"))
			if !strings.Contains(string(audit), `"decision":"`+c.decision+`"`) {
				t.Errorf("audit: %s, want decision %s", audit, c.decision)
			}
		})
	}
}
