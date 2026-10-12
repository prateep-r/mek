//go:build integration

package integration

import (
	"runtime"
	"github.com/prateep-r/mek/test/testkit"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `mek use -s` switches the shell it is typed in, once `mek shell-init` is
// loaded — in each real shell that is installed. Everything else goes to mek.
func TestShellInitInRealShells(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell; PowerShell has its own test")
	}
	e := setup(t, `contexts:
  prod: {provider: aws, aws_profile: admin, clusters: {main: {name: prod-eks, region: us-east-1}}}
  dev:  {provider: aws, aws_profile: dev}
`)
	posix := `eval "$(mek shell-init SHELL)"
mek use -s prod 2>/dev/null
echo "ctx=$MEK_CONTEXT kube=${KUBECONFIG-}"
mek ctx --short
mek use --shell dev 2>/dev/null
echo "ctx=$MEK_CONTEXT kube=${KUBECONFIG-unset}"
mek use -s nope 2>/dev/null
echo "status=$? ctx=$MEK_CONTEXT"
`
	fish := `mek shell-init fish | source
mek use -s prod 2>/dev/null
echo "ctx=$MEK_CONTEXT kube=$KUBECONFIG"
mek ctx --short
mek use --shell dev 2>/dev/null
set -q KUBECONFIG; and echo "ctx=$MEK_CONTEXT kube=$KUBECONFIG"; or echo "ctx=$MEK_CONTEXT kube=unset"
mek use -s nope 2>/dev/null
echo "status=$status ctx=$MEK_CONTEXT"
`
	kube := filepath.Join(e.mekHome, "kube", "prod.yaml")
	want := []string{"ctx=prod kube=" + kube, "prod", "ctx=dev kube=unset", "status=1 ctx=dev"}
	for _, sh := range []struct{ name, args, script string }{
		{"bash", "--norc --noprofile -c", strings.ReplaceAll(posix, "SHELL", "bash")},
		{"zsh", "-f -c", strings.ReplaceAll(posix, "SHELL", "zsh")},
		{"fish", "--no-config -c", fish},
	} {
		t.Run(sh.name, func(t *testing.T) {
			bin, err := exec.LookPath(sh.name)
			if err != nil {
				t.Skipf("%s is not installed", sh.name)
			}
			cmd := exec.Command(bin, append(strings.Fields(sh.args), sh.script)...)
			cmd.Env = append(e.vars[:2:2], e.vars[3:]...)
			cmd.Env = append(cmd.Env, e.pathWith(filepath.Dir(mek)), "NO_COLOR=1", "MEK_CONTEXT=", "KUBECONFIG=")
			if d := testkit.CoverDir(); d != "" { // coverage runs: the instrumented mek writes its counters there
				cmd.Env = append(cmd.Env, "GOCOVERDIR="+d)
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%v\n%s%s", err, out, stderr.String())
			}
			got := strings.Split(strings.TrimSpace(string(out)), "\n")
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("%s:\n%s\nwant:\n%s", sh.name, out, strings.Join(want, "\n"))
			}
		})
	}
}
