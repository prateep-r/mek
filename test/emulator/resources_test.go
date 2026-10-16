//go:build emulator

package emulator

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mek ui's Resources tab against Floci with the real aws CLI: the account's
// instances listed from the generated catalog, one stopped and terminated
// through the actions menu, and the same action blocked on a readonly
// context — each run through mek, so the audit log has them.
func TestResourcesWithFloci(t *testing.T) {
	aws := realCLI(t, "aws")
	floci := emulator(t, flociAWS, 4566, "MEK_FLOCI_AWS_URL", flociAWSEnv...)
	endpoint := awsCompatProxy(t, floci)
	n := time.Now().UnixNano() % 1e9
	role := fmt.Sprintf("Ops%d", n)
	assign(t, aws, floci, role, "111122223333")
	u := newUser(t, fmt.Sprintf(`contexts:
  dev: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: us-east-1, account_id: "111122223333", role: %s, region: us-east-1}
  ro: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: us-east-1, account_id: "111122223333", role: %s, region: us-east-1, readonly: true}
`, role, role), aws, "AWS_ENDPOINT_URL="+endpoint)
	deviceLogin(t, u, floci, "dev")

	name := fmt.Sprintf("mek-web-%d", n)
	id := strings.TrimSpace(u.ok("-c", "dev", "aws", "ec2", "run-instances", "--image-id", "ami-12345678", "--instance-type", "t3.micro",
		"--tag-specifications", "ResourceType=instance,Tags=[{Key=Name,Value="+name+"}]", "--query", "Instances[0].InstanceId", "--output", "text"))
	state := func() string {
		return strings.TrimSpace(u.ok("-c", "dev", "aws", "ec2", "describe-instances", "--instance-ids", id,
			"--query", "Reservations[0].Instances[0].State.Name", "--output", "text"))
	}

	// open goes to the instance on the Resources tab, and opens its actions.
	open := func(s *session) {
		s.step("5 Resources", "5")
		s.step("SERVICE", "/", "ec2")
		s.settled("enter", "enter")
		s.step("KIND", "/", "instances")
		s.settled("enter", "enter")
		s.step(name, "/", name)
		s.settled("enter", "a")
		s.step("actions on " + id)
	}
	// choose picks an action from the menu (sorted by command).
	choose := func(s *session, command string) {
		for _, c := range []string{"reboot-instances", "start-instances", "stop-instances", "terminate-instances"} {
			if c == command {
				s.step("", "enter")
				return
			}
			s.step("", "down")
		}
		t.Fatalf("no action %s", command)
	}

	// Readonly: no question; mek blocks it.
	s := startSession(t, u.vars, "-c", "ro", "ui")
	open(s)
	choose(s, "stop-instances")
	s.step("ro is readonly — blocked", "q")
	s.exited()
	if got := state(); got != "running" {
		t.Fatalf("after the blocked stop: %s", got)
	}

	s = startSession(t, u.vars, "-c", "dev", "ui")
	open(s)
	choose(s, "stop-instances")
	s.step("destructive: aws ec2 stop-instances --instance-ids "+id+"? [y/N]", "y")
	s.step("✓ stop-instances " + id + " (dev)")
	s.step("stopped", "a") // listed again
	choose(s, "terminate-instances")
	s.step("terminate-instances --instance-ids "+id+"? [y/N]", "y")
	s.step("✓ terminate-instances "+id+" (dev)", "q")
	s.exited()
	if got := state(); got != "terminated" && got != "shutting-down" {
		t.Fatalf("after terminate: %s", got)
	}

	audit := readFile(t, filepath.Join(u.mekHome, "audit.jsonl"))
	for _, want := range []string{`"context":"ro"`, `"decision":"blocked"`, `"stop-instances","--instance-ids","` + id, `"terminate-instances","--instance-ids","` + id} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit log has no %q:\n%s", want, audit)
		}
	}
}
