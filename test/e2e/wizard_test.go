//go:build e2e

package e2e

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"

	"github.com/prateep-r/mek/test/testkit"
)

const (
	down  = "\x1b[B"
	enter = "\r"
)

// expectText waits until the terminal shows text, styles aside (Bubble Tea
// colours its screens).
func (s *session) expectText(text string) {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(ansi.Strip(s.output()), text) {
		if time.Now().After(deadline) {
			s.t.Fatalf("terminal never showed %q; got:\n%s", text, ansi.Strip(s.output()))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The wizard on a real terminal: arrow keys and Enter, the accounts and
// roles listed from a stub portal, saved where expected; a team context
// copied into mek.local.yaml; and mek ui opening the wizard and coming back.
func TestWizard(t *testing.T) {
	installed := installRelease(t)
	mekHome, home, bin := t.TempDir(), t.TempDir(), t.TempDir()
	calls := filepath.Join(t.TempDir(), "aws-calls")
	// aws: the portal's accounts and roles, and a copy of what each call was handed.
	os.WriteFile(filepath.Join(bin, "aws"), []byte(fmt.Sprintf(`#!/bin/sh
echo "$*" >> %[1]q
for a in "$@"; do case "$a" in file://*) cat "${a#file://}" >> %[1]q; echo >> %[1]q;; esac; done
case "$*" in
*list-accounts*) echo '{"accountList":[{"accountId":"111122223333","accountName":"acme-dev"},{"accountId":"444455556666","accountName":"acme-prod"}]}';;
*list-account-roles*) echo '{"roleList":[{"roleName":"Admin"},{"roleName":"Developer"}]}';;
esac
`, calls)), 0o755)
	// Logged in to the portal already: the AWS CLI's cached token.
	cache := filepath.Join(home, ".aws", "sso", "cache")
	os.MkdirAll(cache, 0o700)
	sum := sha1.Sum([]byte("mek-acme"))
	os.WriteFile(filepath.Join(cache, hex.EncodeToString(sum[:])+".json"), []byte(fmt.Sprintf(`{"accessToken":"e2e-portal-token","expiresAt":%q}`,
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339))), 0o600)
	os.WriteFile(filepath.Join(mekHome, "config.yaml"), []byte(`# mine
contexts:
  acme-dev: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Developer}
`), 0o600)
	env := []string{"MEK_HOME=" + mekHome, "HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "TERM=xterm-256color"}

	s := startSession(t, installed, env, "ctx", "add", "acme-prod")
	pty.Setsize(s.tty, &pty.Winsize{Rows: 40, Cols: 120})
	step := func(screen, keys string) { t.Helper(); s.expectText(screen); s.send(keys) }
	step("? Name", enter)
	step("? Cloud", enter)                        // AWS
	step("? Sign in with", enter)                 // IAM Identity Center
	step("https://acme.awsapps.com/start", enter) // acme-dev's portal: its region is known
	step("acme-prod  444455556666", down+enter)   // logged in: the accounts, listed
	step("Developer", down+enter)                 // the roles in acme-prod
	step("? Region", enter)                       // none
	step("? Guard", down+enter)                   // protected
	step(`account_id: "444455556666"`, enter)     // Save
	if code := s.exitCode(); code != 0 {
		t.Fatalf("exit %d; terminal:\n%s", code, s.output())
	}
	s.expectText("added acme-prod to")
	cfg, _ := os.ReadFile(filepath.Join(mekHome, "config.yaml"))
	if !strings.HasPrefix(string(cfg), "# mine\n") || !strings.Contains(string(cfg), "  acme-prod:\n    provider: aws\n    protected: true\n") ||
		!strings.Contains(string(cfg), "role: Developer") {
		t.Errorf("config:\n%s", cfg)
	}
	got, _ := os.ReadFile(calls)
	if !strings.Contains(string(got), "sso list-accounts --cli-input-json file://"+mekHome) || !strings.Contains(string(got), `{"accessToken":"e2e-portal-token"}`) ||
		!strings.Contains(string(got), "--account-id 444455556666") {
		t.Errorf("aws calls:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(mekHome, "sso-*.json")); len(left) > 0 {
		t.Errorf("token files left: %v", left)
	}
	if audit, _ := os.ReadFile(filepath.Join(mekHome, "audit.jsonl")); strings.Contains(string(audit), "e2e-portal-token") {
		t.Errorf("the token is in the audit log:\n%s", audit)
	}
	if r := testkit.Run(t, installed, env, "ctx", "ls"); !strings.Contains(r.Stdout, "acme-prod") {
		t.Errorf("ctx ls:\n%s", r.Stdout)
	}

	// A team context, copied into mek.local.yaml.
	repo := t.TempDir()
	os.Mkdir(filepath.Join(repo, ".git"), 0o700)
	os.WriteFile(filepath.Join(repo, "mek.yaml"), []byte(`contexts:
  orders-dev: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Developer}
`), 0o644)
	s = startSession(t, installed, env, "--project", repo, "ctx", "edit", "orders-dev")
	pty.Setsize(s.tty, &pty.Winsize{Rows: 40, Cols: 120})
	step("? Name", enter)
	step("your own copy", down+enter)
	step("? Cloud", enter)
	step("? Sign in with", enter)
	step("? Access portal", enter) // its region is acme-dev's: not asked
	step("acme-dev  111122223333", enter)
	step("Developer", "\x1b[A"+enter) // Admin
	step("? Region", enter)
	step("? Guard", enter)
	step("role: Admin", enter)
	if code := s.exitCode(); code != 0 {
		t.Fatalf("edit: exit %d; terminal:\n%s", code, s.output())
	}
	local, _ := os.ReadFile(filepath.Join(repo, "mek.local.yaml"))
	ignore, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	team, _ := os.ReadFile(filepath.Join(repo, "mek.yaml"))
	if !strings.Contains(string(local), "role: Admin") || !strings.Contains(string(ignore), "mek.local.yaml") || strings.Contains(string(team), "Admin") {
		t.Errorf("copy:\nlocal %s\nignore %s\nteam %s", local, ignore, team)
	}

	// mek ui: a opens the wizard, Ctrl-C leaves it, the UI comes back.
	s = startSession(t, installed, env, "ui")
	pty.Setsize(s.tty, &pty.Winsize{Rows: 30, Cols: 120})
	step("a add · e edit · d remove", "a")
	step("Add a context", "\x03")
	step("press Enter to return to mek ui", enter) // canceled: it says so, waits to be read
	s.expectText("✗ add a context")
	s.send("q")
	if code := s.exitCode(); code != 0 {
		t.Fatalf("ui: exit %d; terminal:\n%s", code, s.output())
	}
}
