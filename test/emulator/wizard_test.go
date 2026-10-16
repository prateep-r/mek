//go:build emulator

package emulator

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"

	"github.com/prateep-r/mek/test/testkit"
)

// session is mek on a pseudo-terminal, driven by keys.
type session struct {
	t    *testing.T
	tty  *os.File
	mu   sync.Mutex
	out  bytes.Buffer
	done chan int
}

func startSession(t *testing.T, env []string, args ...string) *session {
	t.Helper()
	cmd := testkit.Command(mek, env, args...)
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 140})
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
			if err != nil {
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

func (s *session) screen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansi.Strip(s.out.String())
}

// step waits until the terminal shows text, then types keys.
func (s *session) step(text string, keys ...string) {
	s.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !strings.Contains(s.screen(), text); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			s.t.Fatalf("the terminal never showed %q:\n%s", text, s.screen())
		}
	}
	named := map[string]string{"enter": "\r", "down": "\x1b[B", "up": "\x1b[A", "ctrl+u": "\x15"}
	for _, k := range keys {
		if v, ok := named[k]; ok {
			k = v
		}
		io.WriteString(s.tty, k)
		time.Sleep(60 * time.Millisecond)
	}
}

func (s *session) exited() {
	s.t.Helper()
	select {
	case code := <-s.done:
		if code != 0 {
			s.t.Fatalf("mek exited %d:\n%s", code, s.screen())
		}
	case <-time.After(30 * time.Second):
		s.t.Fatalf("mek did not exit:\n%s", s.screen())
	}
}

// The config wizard against Floci with the real aws CLI: the accounts and
// roles of a real IAM Identity Center login, and the instances, databases
// and clusters of that account, listed in the forms and saved; show-cmd's
// output runs by hand; the portal token never leaks.
func TestWizardWithFloci(t *testing.T) {
	aws := realCLI(t, "aws")
	floci := emulator(t, flociAWS, 4566, "MEK_FLOCI_AWS_URL", flociAWSEnv...)
	endpoint := awsCompatProxy(t, floci)
	n := time.Now().UnixNano() % 1e9
	dev, adm := fmt.Sprintf("Dev%d", n), fmt.Sprintf("Admin%d", n)
	assign(t, aws, floci, dev, "111122223333", "444455556666")
	assign(t, aws, floci, adm, "444455556666")

	// A teammate's context on the portal, and a login through it: the portal
	// is offered and its login shared.
	u := newUser(t, fmt.Sprintf(`# mine
contexts:
  seed: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: us-east-1, account_id: "111122223333", role: %s, region: us-east-1}
`, dev), aws, "AWS_ENDPOINT_URL="+endpoint)
	deviceLogin(t, u, floci, "seed")

	s := startSession(t, u.vars, "ctx", "add", "dev")
	s.step("? Name", "enter")
	s.step("? Cloud", "enter")
	s.step("? Sign in with", "enter")
	s.step("https://acme.awsapps.com/start", "enter") // seed's portal: its region and login come with it
	s.step("444455556666", "4444")                    // the accounts the login reaches, listed
	s.step("filter: 4444", "enter")
	s.step(adm, adm) // the roles in that account
	s.step("filter: "+adm, "enter")
	s.step("? Region", "us-east-1")
	s.step("filter: us-east-1", "enter")
	s.step("? Guard", "enter")
	s.step(`account_id: "444455556666"`, "enter")
	s.exited()
	cfg := readFile(t, filepath.Join(u.mekHome, "config.yaml"))
	if !strings.HasPrefix(cfg, "# mine\n") || !strings.Contains(cfg, "account_id: \"444455556666\"\n    role: "+adm) {
		t.Fatalf("config:\n%s", cfg)
	}
	if got := u.ok("-c", "dev", "aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text"); !strings.Contains(got, "444455556666") {
		t.Fatalf("the new context's identity: %s", got)
	}

	// Things in that account, made through the new context.
	bastion, db, eks := fmt.Sprintf("mek-bastion-%d", n), fmt.Sprintf("orders-%d", n), fmt.Sprintf("mek-eks-%d", n)
	u.ok("-c", "dev", "aws", "ec2", "run-instances", "--image-id", "ami-12345678", "--instance-type", "t3.micro",
		"--tag-specifications", "ResourceType=instance,Tags=[{Key=Name,Value="+bastion+"}]")
	u.ok("-c", "dev", "aws", "rds", "create-db-instance", "--db-instance-identifier", db, "--engine", "postgres",
		"--db-instance-class", "db.t3.micro", "--master-username", "app", "--master-user-password", "secret123", "--allocated-storage", "20")
	vpc := strings.TrimSpace(u.ok("-c", "dev", "aws", "ec2", "create-vpc", "--cidr-block", "10.0.0.0/16", "--query", "Vpc.VpcId", "--output", "text"))
	subnet := strings.TrimSpace(u.ok("-c", "dev", "aws", "ec2", "create-subnet", "--vpc-id", vpc, "--cidr-block", "10.0.1.0/24",
		"--query", "Subnet.SubnetId", "--output", "text"))
	u.ok("-c", "dev", "aws", "eks", "create-cluster", "--name", eks, "--role-arn", "arn:aws:iam::444455556666:role/eks",
		"--resources-vpc-config", "subnetIds="+subnet)

	// A tunnel to the database through the instance, both picked from lists.
	s = startSession(t, u.vars, "-c", "dev", "ctx", "tunnel", "add")
	s.step("? Forward to", "enter")
	s.step(bastion, bastion)
	s.step("filter: "+bastion, "enter")
	s.step(db, db)
	s.step("filter: "+db, "enter")
	s.step("? Port", "enter") // the database's, filled in
	s.step("? Local port", "ctrl+u", "25432", "enter")
	s.step("? Name", "enter") // the database's name, filled in
	s.step("[Save]", "enter")
	s.exited()

	// A target and a cluster, the same way.
	s = startSession(t, u.vars, "-c", "dev", "ctx", "target", "add")
	s.step(bastion, bastion)
	s.step("filter: "+bastion, "enter")
	s.step("? Refer to it by", "enter") // its Name tag
	s.step("? Name", "ctrl+u", "jump", "enter")
	s.step("[Save]", "enter")
	s.exited()
	s = startSession(t, u.vars, "-c", "dev", "ctx", "cluster", "add")
	s.step(eks, eks)
	s.step("filter: "+eks, "enter")
	s.step("? Namespace", "enter")
	s.step("? Name", "ctrl+u", "main", "enter")
	s.step("[Save]", "enter")
	s.exited()

	cfg = readFile(t, filepath.Join(u.mekHome, "config.yaml"))
	for _, want := range []string{"via: tag:Name=" + bastion, "local_port: 25432", "instance: tag:Name=" + bastion, "name: " + eks} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	if out := u.ok("ctx", "ls"); !strings.Contains(out, "dev") {
		t.Errorf("ctx ls: %s", out)
	}

	// show-cmd: what it prints, run by hand, gets what mek got.
	r := testkit.Run(t, mek, u.vars, "--show-cmd", "-c", "dev", "aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text")
	_, block, ok := strings.Cut(r.Stderr, "↳ runs:\n")
	if r.Code != 0 || !ok {
		t.Fatalf("show-cmd: %+v", r)
	}
	var script []string
	for _, l := range strings.Split(block, "\n") {
		if l = strings.TrimPrefix(l, "    "); strings.HasPrefix(l, "#") {
			break
		}
		script = append(script, l)
	}
	sh := exec.Command("sh", "-c", strings.Join(script, "\n"))
	sh.Env = u.vars
	if out, err := sh.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != strings.TrimSpace(r.Stdout) {
		t.Errorf("typed by hand: %v %q, mek: %q\n%s", err, out, r.Stdout, strings.Join(script, "\n"))
	}

	// The portal token: never in the audit log, no file of it left.
	token := readFile(t, filepath.Join(u.home, ".aws", "sso", "cache", ssoCacheFile("mek-acme")))
	audit := readFile(t, filepath.Join(u.mekHome, "audit.jsonl"))
	if !strings.Contains(audit, `"list-accounts","--cli-input-json","****"`) || strings.Contains(audit, accessToken(t, token)) {
		t.Errorf("audit log:\n%s", audit)
	}
	if left, _ := filepath.Glob(filepath.Join(u.mekHome, "sso-*.json")); len(left) > 0 {
		t.Errorf("token files left: %v", left)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ssoCacheFile is the AWS CLI's token cache file for an sso-session.
func ssoCacheFile(session string) string {
	sum := sha1.Sum([]byte(session))
	return hex.EncodeToString(sum[:]) + ".json"
}

func accessToken(t *testing.T, cache string) string {
	t.Helper()
	var c struct{ AccessToken string }
	if err := json.Unmarshal([]byte(cache), &c); err != nil || c.AccessToken == "" {
		t.Fatalf("token cache: %v %q", err, cache)
	}
	return c.AccessToken
}
