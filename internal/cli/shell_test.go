package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/provider"
)

const shellConfig = `contexts:
  prod: {provider: aws, aws_profile: admin, region: ap-southeast-1, protected: true, targets: {bastion: {instance: "tag:Name=bastion"}, db: {instance: i-0123abcd}}}
  ro:   {provider: aws, aws_profile: viewer, readonly: true}
  gcp:  {provider: gcp, project: p, targets: {web: {instance: web-1, zone: asia-southeast1-b}}}
  az:   {provider: azure, tenant_id: t, subscription_id: 00000000-1111-2222-3333-444444444444}
`

func newShellHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, shellConfig)
	h.exec.respond = func(argv []string) (string, int) {
		switch strings.Join(argv[:3], " ") {
		case "aws ec2 describe-instances":
			return `["i-0bastion0"]`, 0
		case "gcloud compute instances":
			return `[{"name":"vm-1","zone":"x/zones/asia-southeast1-a","status":"RUNNING"}]`, 0
		}
		return "", 0
	}
	old := lookPath
	lookPath = func(string) (string, error) { return "/bin/plugin", nil }
	t.Cleanup(func() { lookPath = old })
	return h
}

func readAudit(t *testing.T) []audit.Entry {
	t.Helper()
	var out []audit.Entry
	b, _ := os.ReadFile(audit.Path())
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e audit.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestShellAWS(t *testing.T) {
	h := newShellHarness(t)
	h.mustRun("-c", "prod", "-y", "shell", "bastion")
	if got := h.exec.argv(); len(got) != 2 || !strings.HasPrefix(got[0], "aws ec2 describe-instances --filters") ||
		got[1] != "aws ssm start-session --target i-0bastion0" {
		t.Fatalf("calls: %q", got)
	}
	if inv := h.exec.invs[1]; !slices.Contains(inv.Env, "AWS_PROFILE=admin") || inv.Session == "" || inv.Target != "bastion → i-0bastion0" {
		t.Errorf("session: %+v", inv)
	}
	// lookup (read), then the session's start and end, all with one id.
	es := readAudit(t)
	if len(es) != 3 || es[0].Class != "read" || es[1].Event != "start" || es[2].Event != "end" ||
		es[1].Session != es[2].Session || es[1].Decision != "confirmed" || es[2].Target != "bastion → i-0bastion0" {
		t.Errorf("audit: %+v", es)
	}

	h.exec.invs = nil
	h.mustRun("-c", "prod", "-y", "shell", "i-0123456789abcdef0") // a literal id needs no lookup
	if got := h.exec.argv(); len(got) != 1 || got[0] != "aws ssm start-session --target i-0123456789abcdef0" {
		t.Errorf("literal id: %q", got)
	}
}

func TestShellGuard(t *testing.T) {
	h := newShellHarness(t)
	fakePrompts(t, false, nil)
	_, err := h.run("-c", "prod", "shell", "db")
	wantErr(t, err, "aborted")
	_, err = h.run("-c", "ro", "shell", "i-0123abcd")
	wantErr(t, err, "ro is readonly — blocked shell command")
	if len(h.exec.invs) != 0 {
		t.Errorf("a stopped shell must not run: %q", h.exec.argv())
	}
	for _, e := range readAudit(t) {
		if e.Event != "" { // no start (and so no end) for sessions that never started
			t.Errorf("entry with event: %+v", e)
		}
	}
}

func TestShellGCP(t *testing.T) {
	h := newShellHarness(t)
	h.mustRun("-c", "gcp", "shell", "web")
	ssh := filepath.Join(h.home, "ssh", "gcp")
	inv := h.exec.invs[0]
	if strings.Join(inv.Argv, " ") != "gcloud compute ssh web-1 --zone asia-southeast1-b --tunnel-through-iap --ssh-key-file "+filepath.Join(ssh, "google_compute_engine") ||
		!slices.Contains(inv.Env, "HOME="+ssh) || slices.Contains(inv.Env, "HOME="+os.Getenv("HOME")) {
		t.Errorf("gcp shell: %q %q", inv.Argv, inv.Env)
	}
	if !slices.Contains(inv.Env, "CLOUDSDK_CONFIG="+filepath.Join(h.home, "gcloud", "gcp")) {
		t.Errorf("gcloud must keep the context's config: %q", inv.Env)
	}

	h.exec.invs = nil
	h.mustRun("-c", "gcp", "shell", "vm-1", "--user", "ops")
	if got := h.exec.argv(); len(got) != 2 || !strings.HasPrefix(got[0], "gcloud compute instances list") ||
		!strings.HasPrefix(got[1], "gcloud compute ssh ops@vm-1 --zone asia-southeast1-a") {
		t.Errorf("lookup + shell: %q", got)
	}
}

func TestShellErrors(t *testing.T) {
	h := newShellHarness(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", "az", "shell", "x"}, "mek shell doesn't support azure yet"},
		{[]string{"-c", "prod", "shell", "web"}, `unknown target "web"`},
		{[]string{"-c", "nope", "shell", "x"}, "unknown context"},
		{[]string{"-c", "prod", "shell"}, "accepts 1 arg"},
	} {
		_, err := h.run(c.args...)
		wantErr(t, err, c.want)
	}

	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	_, err := h.run("-c", "prod", "-y", "shell", "db")
	wantErr(t, err, "session-manager-plugin is needed for this (install: ")
	if len(h.exec.invs) != 0 {
		t.Errorf("ran without the plugin: %q", h.exec.argv())
	}
	if err := (&app{exec: h.exec}).session(nil, providerCommand("nope-plugin"), 0, ""); err == nil || !strings.Contains(err.Error(), "see `mek doctor`") {
		t.Errorf("unknown plugin: %v", err)
	}

	os.WriteFile(filepath.Join(h.home, "ssh"), nil, 0o600) // the ssh dir can't be created
	lookPath = func(string) (string, error) { return "/bin/x", nil }
	_, err = h.run("-c", "gcp", "shell", "web")
	if err == nil {
		t.Error("ssh dir error not reported")
	}
}

// kubectl exec is a session too: start and end entries.
func TestKubectlExecIsASession(t *testing.T) {
	h := newKubeHarness(t)
	h.mustRun("kubectl", "-c", "prod", "-y", "exec", "web-1", "--", "sh")
	if es := readAudit(t); len(es) != 2 || es[0].Event != "start" || es[1].Event != "end" || es[0].Class != "shell" {
		t.Errorf("audit: %+v", es)
	}
}

func TestCompleteTargets(t *testing.T) {
	newShellHarness(t)
	a := &app{opts: globalOpts{context: "prod"}}
	if got, _ := a.completeTargets(nil, nil, ""); strings.Join(got, ",") != "bastion,db" {
		t.Errorf("targets: %q", got)
	}
	a.opts.context = "nope"
	if got, _ := a.completeTargets(nil, nil, ""); got != nil {
		t.Errorf("unknown context: %q", got)
	}
}

func providerCommand(requires ...string) provider.Command {
	return provider.Command{Argv: []string{"x"}, Requires: requires}
}
