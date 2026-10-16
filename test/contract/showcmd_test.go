//go:build contract

package contract

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/prateep-r/mek/test/testkit"
)

// What show-cmd prints is a real command: run in sh, with the real CLI, it
// sees the same profile or project mek gave it.
func TestShowCmdIsRunnable(t *testing.T) {
	for _, c := range []struct {
		name, cli, config string
		args              []string
		extra             func(t *testing.T) []string
	}{
		{"aws", "aws", `contexts:
  uat: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Dev, region: ap-southeast-1}
`, []string{"-c", "uat", "aws", "configure", "get", "sso_account_id"}, func(*testing.T) []string { return nil }},
		{"gcloud", "gcloud", "contexts:\n  g: {provider: gcp, project: acme-dev, region: asia-southeast1}\n",
			[]string{"-c", "g", "gcloud", "config", "get", "core/project"}, gcloudPython},
	} {
		t.Run(c.name, func(t *testing.T) {
			u := newUser(t, c.config, []string{realCLI(t, c.cli)}, c.extra(t)...)
			r := testkit.Run(t, mek, u.vars, append([]string{"--show-cmd"}, c.args...)...)
			if r.Code != 0 {
				t.Fatalf("mek: %+v", r)
			}
			_, block, ok := strings.Cut(r.Stderr, "↳ runs:\n")
			if !ok {
				t.Fatalf("no command shown:\n%s", r.Stderr)
			}
			var script []string
			for _, l := range strings.Split(block, "\n") {
				if l = strings.TrimPrefix(l, "    "); strings.HasPrefix(l, "#") {
					break
				}
				script = append(script, l)
			}
			sh := exec.Command("sh", "-c", strings.Join(script, "\n"))
			sh.Env = append(u.vars, "NO_COLOR=1")
			out, err := sh.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != strings.TrimSpace(r.Stdout) {
				t.Errorf("typed yourself: %v %q, mek: %q\nscript:\n%s", err, out, r.Stdout, strings.Join(script, "\n"))
			}
		})
	}
}
