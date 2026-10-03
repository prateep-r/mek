//go:build emulator

package emulator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prateep-r/mek/test/testkit"
)

// ssoPrincipal is who Floci's SSO device approval authenticates as. Floci
// only compares it with account assignments, so no Identity Store user is
// needed (users don't survive a restart: floci-io/floci#4993).
const ssoPrincipal = "11111111-2222-3333-4444-555555555555"

var flociAWSEnv = []string{"FLOCI_SERVICES_SSOOIDC_LOCAL_PRINCIPAL_ID=" + ssoPrincipal}

// oidcErrorTypes maps OAuth error codes in a CreateToken response body to the
// x-amzn-ErrorType header real AWS also sends. Floci 2.1.0 omits the header,
// so botocore can't tell "authorization pending" from a failure and the AWS
// CLI stops polling at once.
var oidcErrorTypes = map[string]string{
	"authorization_pending": "AuthorizationPendingException",
	"slow_down":             "SlowDownException",
	"expired_token":         "ExpiredTokenException",
	"access_denied":         "AccessDeniedException",
	"invalid_grant":         "InvalidGrantException",
}

// awsCompatProxy fronts Floci and adds the missing x-amzn-ErrorType header.
func awsCompatProxy(t *testing.T, floci string) string {
	t.Helper()
	target, _ := url.Parse(floci)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("x-amzn-ErrorType") != "" {
			return nil
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		var oauth struct{ Error string }
		if json.Unmarshal(body, &oauth) == nil && oidcErrorTypes[oauth.Error] != "" {
			resp.Header.Set("x-amzn-ErrorType", oidcErrorTypes[oauth.Error])
		}
		return nil
	}
	srv := httptest.NewServer(proxy)
	t.Cleanup(srv.Close)
	return srv.URL
}

var deviceURL = regexp.MustCompile(`https?://\S+/device\?user_code=[A-Za-z0-9-]+`)

// The whole IAM Identity Center flow through a context mek generated:
// `mek login` (aws sso login, device code), approving the device the way a
// browser would, then commands using the cached SSO token's role credentials.
func TestAWSSSOLogin(t *testing.T) {
	aws := realCLI(t, "aws")
	floci := emulator(t, flociAWS, 4566, "MEK_FLOCI_AWS_URL", flociAWSEnv...)
	endpoint := awsCompatProxy(t, floci)

	// Identity Center admin, as the organisation would set it up: a permission
	// set assigned to the principal on an account. Unique names, so a shared
	// emulator (make docker-up) can run this repeatedly.
	role := fmt.Sprintf("Dev%d", time.Now().UnixNano()%1e9)
	admin := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(aws, args...)
		cmd.Env = append(os.Environ(), "AWS_ACCESS_KEY_ID=test", "AWS_SECRET_ACCESS_KEY=test", "AWS_REGION=us-east-1",
			"AWS_ENDPOINT_URL="+floci, "AWS_CONFIG_FILE=/dev/null", "AWS_SHARED_CREDENTIALS_FILE=/dev/null")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("aws %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	instance := admin("sso-admin", "list-instances", "--query", "Instances[0].InstanceArn", "--output", "text")
	ps := admin("sso-admin", "create-permission-set", "--instance-arn", instance, "--name", role,
		"--query", "PermissionSet.PermissionSetArn", "--output", "text")
	admin("sso-admin", "create-account-assignment", "--instance-arn", instance, "--target-id", "111122223333",
		"--target-type", "AWS_ACCOUNT", "--permission-set-arn", ps, "--principal-type", "USER", "--principal-id", ssoPrincipal)

	u := newUser(t, fmt.Sprintf(`contexts:
  sso: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: us-east-1, account_id: "111122223333", role: %s, region: us-east-1}
  sso-ro: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: us-east-1, account_id: "111122223333", role: %s, region: us-east-1, readonly: true}
`, role, role), aws, "AWS_ENDPOINT_URL="+endpoint)

	// mek login → aws sso login prints a verification URL and polls.
	cmd := testkit.Command(mek, u.vars, "login", "sso", "--", "--use-device-code", "--no-browser")
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var transcript strings.Builder
	approved := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		sent := false
		for sc.Scan() {
			transcript.WriteString(sc.Text() + "\n")
			if m := deviceURL.FindString(sc.Text()); m != "" && !sent {
				sent = true
				approved <- m
			}
		}
		close(approved)
	}()
	select {
	case link, ok := <-approved:
		if !ok {
			cmd.Wait()
			t.Fatalf("aws sso login printed no verification URL:\n%s", transcript.String())
		}
		// Floci prints its default public URL (localhost:4566). Approve on the
		// emulator this test started instead — never on whatever else might be
		// listening on 4566 on this machine.
		printed, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Get(floci + printed.RequestURI()) // what the user's browser does
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(b), "authorized") {
			t.Fatalf("device approval: %s", b)
		}
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("no verification URL within 30s:\n%s", transcript.String())
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mek login: %v\n%s", err, transcript.String())
		}
	case <-time.After(60 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("mek login did not finish:\n%s", transcript.String())
	}
	// mek runs `aws sts get-caller-identity` after login: it already used the token.
	if !strings.Contains(transcript.String(), "111122223333") {
		t.Errorf("login's identity check should show the SSO account:\n%s", transcript.String())
	}

	// Later commands use the cached token through mek's generated profile.
	if out := u.ok("-c", "sso", "aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text"); !strings.Contains(out, "111122223333") {
		t.Errorf("sts via the SSO profile: %s", out)
	}
	bucket := fmt.Sprintf("mek-sso-%d", time.Now().UnixNano()%1e9)
	u.ok("-c", "sso", "aws", "s3", "mb", "s3://"+bucket)
	u.blocked("-c", "sso-ro", "aws", "s3", "rb", "s3://"+bucket) // same login, readonly context
	u.ok("-c", "sso", "aws", "s3", "rb", "s3://"+bucket)
}
