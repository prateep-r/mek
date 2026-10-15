//go:build contract

package contract

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prateep-r/mek/internal/provider"
)

// The wizard's portal lookups with the real AWS CLI: the access token goes
// in a file (--cli-input-json), reaches the portal API as its bearer
// token, and never appears in argv; the output parses.
func TestDiscoveryWithRealAWS(t *testing.T) {
	aws := realCLI(t, "aws")
	var mu sync.Mutex
	var tokens, accounts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokens = append(tokens, r.Header.Get("X-Amz-Sso_bearer_token"))
		accounts = append(accounts, r.URL.Query().Get("account_id"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/assignment/accounts":
			w.Write([]byte(`{"accountList":[{"accountId":"111122223333","accountName":"dev","emailAddress":"d@example.com"}]}`))
		case "/assignment/roles":
			w.Write([]byte(`{"roleList":[{"roleName":"Developer","accountId":"111122223333"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u := newUser(t, `contexts:
  dev: {provider: aws, sso_start_url: "https://acme.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Dev}
`, []string{aws}, "AWS_ENDPOINT_URL_SSO="+srv.URL)

	var found []provider.Found
	err := provider.WithTokenFile(t.TempDir(), "fake-portal-token", func(input string) error {
		out, code := u.run(append([]string{"-c", "dev"}, provider.ListAccountsCommand(input, "ap-southeast-1")...)...)
		if code != 0 {
			t.Fatalf("list-accounts (exit %d):\n%s", code, out)
		}
		var err error
		if found, err = provider.ParseAccounts([]byte(out)); err != nil {
			return err
		}
		out, code = u.run(append([]string{"-c", "dev"}, provider.ListAccountRolesCommand(input, "ap-southeast-1", "111122223333")...)...)
		if code != 0 {
			t.Fatalf("list-account-roles (exit %d):\n%s", code, out)
		}
		roles, err := provider.ParseRoles([]byte(out))
		found = append(found, roles...)
		return err
	})
	if err != nil || len(found) != 2 || found[0].Label != "dev" || found[1].Value != "Developer" {
		t.Fatalf("found %v, %v", found, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(tokens, ",") != "fake-portal-token,fake-portal-token" || accounts[1] != "111122223333" {
		t.Errorf("the portal saw tokens %q, accounts %q", tokens, accounts)
	}
}

// The wizard's project lookup with the real gcloud.
func TestDiscoveryWithRealGcloud(t *testing.T) {
	gcloud := realCLI(t, "gcloud")
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/projects") {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"projects":[{"projectId":"acme-dev","name":"Acme Dev","projectNumber":"1","lifecycleState":"ACTIVE"}]}`))
	}))
	t.Cleanup(srv.Close)
	u := newUser(t, "contexts:\n  g: {provider: gcp, project: acme-dev}\n", []string{gcloud}, append(gcloudPython(t),
		"CLOUDSDK_AUTH_ACCESS_TOKEN=ya29.fake", "CLOUDSDK_API_ENDPOINT_OVERRIDES_CLOUDRESOURCEMANAGER="+srv.URL+"/")...)
	out, code := u.run(append([]string{"-c", "g"}, provider.ListProjectsCommand()...)...)
	if code != 0 {
		t.Fatalf("projects list (exit %d):\n%s", code, out)
	}
	found, err := provider.ParseProjects([]byte(out))
	if err != nil || len(found) != 1 || found[0] != (provider.Found{Value: "acme-dev", Hint: "Acme Dev"}) {
		t.Errorf("found %v, %v:\n%s", found, err, out)
	}
	if auth != "Bearer ya29.fake" {
		t.Errorf("auth: %q", auth)
	}
}
