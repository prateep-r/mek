package provider

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryCommands(t *testing.T) {
	for _, c := range []struct {
		got  []string
		want string
	}{
		{SSOLoginCommand("https://acme.awsapps.com/start"), "aws sso login --sso-session mek-acme"},
		{ListAccountsCommand("/t/sso-1.json", "ap-southeast-1"), "aws sso list-accounts --cli-input-json file:///t/sso-1.json --region ap-southeast-1 --output json"},
		{ListAccountRolesCommand("/t/sso-1.json", "ap-southeast-1", "111122223333"), "aws sso list-account-roles --cli-input-json file:///t/sso-1.json --account-id 111122223333 --region ap-southeast-1 --output json"},
		{ListProjectsCommand(), "gcloud projects list --format=json(projectId,name)"},
	} {
		if got := strings.Join(c.got, " "); got != c.want {
			t.Errorf("got  %s\nwant %s", got, c.want)
		}
	}
	for _, d := range Discoveries() {
		if d.Cloud == "" || d.What == "" || d.Permission == "" || len(d.Argv) < 3 {
			t.Errorf("discovery: %+v", d)
		}
		if strings.Contains(strings.Join(d.Argv, " "), "accessToken") {
			t.Errorf("a token in argv: %v", d.Argv)
		}
	}
}

func TestParseDiscoveries(t *testing.T) {
	accts, err := ParseAccounts([]byte(`{"accountList":[{"accountId":"444455556666","accountName":"prod","emailAddress":"p@x"},{"accountId":"111122223333","accountName":"dev","emailAddress":"d@x"}]}`))
	if err != nil || !reflect.DeepEqual(accts, []Found{{"111122223333", "dev", "111122223333"}, {"444455556666", "prod", "444455556666"}}) {
		t.Errorf("accounts: %v %v", accts, err)
	}
	roles, err := ParseRoles([]byte(`{"roleList":[{"roleName":"ReadOnly","accountId":"1"},{"roleName":"Admin","accountId":"1"}]}`))
	if err != nil || !reflect.DeepEqual(roles, []Found{{Value: "Admin"}, {Value: "ReadOnly"}}) {
		t.Errorf("roles: %v %v", roles, err)
	}
	projects, err := ParseProjects([]byte(`[{"projectId":"b-2","name":"Billing"},{"projectId":"a-1","name":"a-1"}]`))
	if err != nil || !reflect.DeepEqual(projects, []Found{{Value: "a-1"}, {Value: "b-2", Hint: "Billing"}}) {
		t.Errorf("projects: %v %v", projects, err)
	}
	for _, f := range []func([]byte) ([]Found, error){ParseAccounts, ParseRoles, ParseProjects} {
		if _, err := f([]byte("<html>")); err == nil || !strings.Contains(err.Error(), "unexpected output") {
			t.Errorf("bad output: %v", err)
		}
	}
}

// cacheToken writes the AWS CLI's cached token for key, as aws sso login would.
func cacheToken(t *testing.T, key, token, expires string) {
	t.Helper()
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".aws", "sso", "cache")
	os.MkdirAll(dir, 0o700)
	sum := sha1.Sum([]byte(key))
	body := `{"startUrl":"x","accessToken":"` + token + `","expiresAt":"` + expires + `"}`
	if err := os.WriteFile(filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSSOToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	url := "https://acme.awsapps.com/start"
	if got := SSOToken(url); got != "" {
		t.Errorf("no cache: %q", got)
	}
	later, past := time.Now().Add(time.Hour).UTC(), time.Now().Add(-time.Hour).UTC()
	cacheToken(t, "mek-acme", "old", past.Format(time.RFC3339))
	cacheToken(t, url, "legacy", later.Format("2006-01-02T15:04:05UTC")) // a login without an sso-session
	if got := SSOToken(url); got != "legacy" {
		t.Errorf("expired session token, legacy one: %q", got)
	}
	cacheToken(t, "mek-acme", "fresh", later.Format(time.RFC3339))
	if got := SSOToken(url); got != "fresh" {
		t.Errorf("session token: %q", got)
	}
	cacheToken(t, "mek-acme", "x", "soon")
	cacheToken(t, url, "y", time.Now().Add(30*time.Second).UTC().Format(time.RFC3339)) // too close to expiry
	if got := SSOToken(url); got != "" {
		t.Errorf("unusable: %q", got)
	}
}

func TestWithTokenFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mek")
	var seen string
	err := WithTokenFile(dir, "s3cr3t", func(path string) error {
		seen = path
		b, _ := os.ReadFile(path)
		if string(b) != `{"accessToken":"s3cr3t"}` {
			t.Errorf("file: %s", b)
		}
		if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode().Perm())
		}
		return errors.New("aws failed")
	})
	if err == nil || err.Error() != "aws failed" {
		t.Errorf("err: %v", err)
	}
	if _, err := os.Stat(seen); !os.IsNotExist(err) {
		t.Errorf("token file left behind: %v", err)
	}
	notDir := filepath.Join(t.TempDir(), "file")
	os.WriteFile(notDir, nil, 0o600)
	if err := WithTokenFile(notDir, "x", func(string) error { t.Error("ran"); return nil }); err == nil {
		t.Error("no place for the file, and no error")
	}
}
