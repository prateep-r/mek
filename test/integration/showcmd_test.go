//go:build integration

package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

// show-cmd prints the native command on stderr, never on stdout, and never
// into what kubectl reads from mek kube token.
func TestShowCmd(t *testing.T) {
	e := setup(t, `contexts:
  uat: {provider: aws, sso_start_url: "https://o.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Dev, region: ap-southeast-1,
        clusters: {main: {name: uat-eks}}}
`)
	r := e.run("--show-cmd", "-c", "uat", "aws", "s3", "ls")
	ok(t, r)
	if !strings.Contains(r.Stderr, "↳ runs:\n    export AWS_CONFIG_FILE='"+filepath.Join(e.mekHome, "aws", "config")+"'") ||
		!strings.Contains(r.Stderr, "\n    aws s3 ls\n") || strings.Contains(r.Stdout, "runs:") {
		t.Errorf("stdout %q\nstderr %q", r.Stdout, r.Stderr)
	}
	ok(t, e.run("show-cmd", "on"))
	if r := e.run("-c", "uat", "aws", "s3", "ls"); !strings.Contains(r.Stderr, "↳ runs:") {
		t.Errorf("after show-cmd on: %q", r.Stderr)
	}
	if r := e.with("MEK_SHOW_CMD=0").run("-c", "uat", "aws", "s3", "ls"); strings.Contains(r.Stderr, "↳ runs:") {
		t.Errorf("MEK_SHOW_CMD=0: %q", r.Stderr)
	}
	if r := e.with("MEK_SHOW_CMD=1").run("--context", "uat", "kube", "token", "--name", "uat-eks", "--location", "ap-southeast-1"); r.Stderr != "" {
		t.Errorf("kube token: %+v", r)
	}
}
