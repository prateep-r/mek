package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes the mek command tree in-process and returns stdout.
func run(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("mek %s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

func TestCtxDoesNotWriteButEnvDoes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	t.Setenv("MEK_CONTEXT", "")
	cfg := `contexts:
  uat:
    provider: aws
    sso_start_url: https://my-org.awsapps.com/start
    sso_region: ap-southeast-1
    account_id: "111122223333"
    role: Dev
`
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	awsConfig := filepath.Join(home, "aws", "config")

	run(t, "use", "uat")
	if got := run(t, "ctx", "--short"); got != "uat\n" {
		t.Errorf("ctx --short = %q", got)
	}
	run(t, "ctx", "ls")
	if _, err := os.Stat(awsConfig); !os.IsNotExist(err) {
		t.Fatalf("display commands must not write %s (err=%v)", awsConfig, err)
	}

	if got := run(t, "env"); !strings.Contains(got, "export AWS_CONFIG_FILE=") || !strings.Contains(got, "export MEK_CONTEXT='uat'") {
		t.Errorf("env output: %s", got)
	}
	if _, err := os.Stat(awsConfig); err != nil {
		t.Fatalf("env should generate the AWS config: %v", err)
	}
}
