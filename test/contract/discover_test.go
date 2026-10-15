//go:build contract

package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

// The wizard's lookups for targets, tunnels and clusters with the real AWS
// CLI: its --query expressions turn each API's answer into what mek parses.
func TestEntryDiscoveryWithRealAWS(t *testing.T) {
	aws := realCLI(t, "aws")
	var mu sync.Mutex
	var filters []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		xml := func(s string) { w.Header().Set("Content-Type", "text/xml"); w.Write([]byte(s)) }
		switch {
		case r.URL.Path == "/clusters":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"clusters":["main-eks"]}`))
		case r.Form.Get("Action") == "DescribeInstances":
			mu.Lock()
			filters = append(filters, r.Form.Get("Filter.1.Name")+"="+r.Form.Get("Filter.1.Value.1"))
			mu.Unlock()
			xml(`<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><requestId>1</requestId>
<reservationSet><item><reservationId>r-1</reservationId><instancesSet>
<item><instanceId>i-0aaa1111bbbb2222c</instanceId><privateIpAddress>10.0.0.5</privateIpAddress><tagSet><item><key>Env</key><value>dev</value></item><item><key>Name</key><value>bastion</value></item></tagSet></item>
<item><instanceId>i-0ddd3333eeee4444f</instanceId><privateIpAddress>10.0.0.6</privateIpAddress></item>
</instancesSet></item></reservationSet></DescribeInstancesResponse>`)
		case r.Form.Get("Action") == "DescribeDBInstances":
			xml(`<DescribeDBInstancesResponse xmlns="http://rds.amazonaws.com/doc/2014-10-31/"><DescribeDBInstancesResult><DBInstances>
<DBInstance><DBInstanceIdentifier>orders</DBInstanceIdentifier><Engine>postgres</Engine><Endpoint><Address>orders.x.rds.amazonaws.com</Address><Port>5432</Port></Endpoint></DBInstance>
</DBInstances></DescribeDBInstancesResult><ResponseMetadata><RequestId>1</RequestId></ResponseMetadata></DescribeDBInstancesResponse>`)
		case r.Form.Get("Action") == "DescribeDBClusters":
			xml(`<DescribeDBClustersResponse xmlns="http://rds.amazonaws.com/doc/2014-10-31/"><DescribeDBClustersResult><DBClusters>
<DBCluster><DBClusterIdentifier>aurora</DBClusterIdentifier><Engine>aurora-mysql</Engine><Endpoint>aurora.cluster-x.rds.amazonaws.com</Endpoint><Port>3306</Port></DBCluster>
</DBClusters></DescribeDBClustersResult><ResponseMetadata><RequestId>1</RequestId></ResponseMetadata></DescribeDBClustersResponse>`)
		default:
			t.Logf("fake aws: unexpected %s %s %v", r.Method, r.URL, r.Form)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u := newUser(t, "contexts:\n  dev: {provider: aws, aws_profile: fake, region: us-east-1}\n", []string{aws},
		"AWS_ENDPOINT_URL_EC2="+srv.URL, "AWS_ENDPOINT_URL_RDS="+srv.URL, "AWS_ENDPOINT_URL_EKS="+srv.URL)
	os.MkdirAll(filepath.Join(u.home, ".aws"), 0o700)
	os.WriteFile(filepath.Join(u.home, ".aws", "credentials"),
		[]byte("[fake]\naws_access_key_id = AKIAFAKEFAKEFAKEFAKE\naws_secret_access_key = fake\n"), 0o600)

	for _, c := range []struct {
		argv  []string
		parse func([]byte) ([]provider.Found, error)
		want  []provider.Found
	}{
		{provider.EC2InstancesCommand(), provider.ParseEC2Instances, []provider.Found{
			{Value: "i-0ddd3333eeee4444f", Hint: "i-0ddd3333eeee4444f 10.0.0.6"},
			{Value: "i-0aaa1111bbbb2222c", Label: "bastion", Hint: "i-0aaa1111bbbb2222c 10.0.0.5", Extra: "bastion"}}},
		{provider.RDSInstancesCommand(), provider.ParseRDS, []provider.Found{
			{Value: "orders.x.rds.amazonaws.com", Label: "orders", Hint: "postgres:5432", Extra: "5432"}}},
		{provider.RDSClustersCommand(), provider.ParseRDS, []provider.Found{
			{Value: "aurora.cluster-x.rds.amazonaws.com", Label: "aurora", Hint: "aurora-mysql:3306", Extra: "3306"}}},
		{provider.EKSClustersCommand(""), provider.ParseEKSClusters, []provider.Found{{Value: "main-eks"}}},
	} {
		out, code := u.run(append([]string{"-c", "dev"}, c.argv...)...)
		found, err := c.parse([]byte(out))
		if code != 0 || err != nil || !reflect.DeepEqual(found, c.want) {
			t.Errorf("%s (exit %d): %v %v\n%s", strings.Join(c.argv[:3], " "), code, found, err, out)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(filters) != 1 || filters[0] != "instance-state-name=running" {
		t.Errorf("ec2 filters: %v", filters)
	}
}

// The same with the real gcloud: its --format projections.
func TestEntryDiscoveryWithRealGcloud(t *testing.T) {
	gcloud := realCLI(t, "gcloud")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/aggregated/instances"):
			reply(map[string]any{"items": map[string]any{"zones/asia-southeast1-a": map[string]any{"instances": []any{
				map[string]any{"name": "vm-1", "status": "RUNNING", "zone": "https://www.googleapis.com/compute/v1/projects/p/zones/asia-southeast1-a",
					"networkInterfaces": []any{map[string]any{"networkIP": "10.1.0.2"}}},
				map[string]any{"name": "off", "status": "TERMINATED", "zone": "https://www.googleapis.com/compute/v1/projects/p/zones/asia-southeast1-a"},
			}}}})
		case strings.HasSuffix(p, "/projects/p/instances"):
			reply(map[string]any{"items": []any{map[string]any{"name": "orders", "connectionName": "p:asia-southeast1:orders",
				"databaseVersion": "POSTGRES_16", "region": "asia-southeast1", "state": "RUNNABLE"}}})
		case strings.HasSuffix(p, "/locations/-/clusters"):
			reply(map[string]any{"clusters": []any{map[string]any{"name": "gke-1", "location": "asia-southeast1", "status": "RUNNING"}}})
		default:
			t.Logf("fake gcp: unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u := newUser(t, "contexts:\n  g: {provider: gcp, project: p}\n", []string{gcloud}, append(gcloudPython(t),
		"CLOUDSDK_AUTH_ACCESS_TOKEN=ya29.fake", "CLOUDSDK_CORE_ACCOUNT=me@example.com",
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_COMPUTE="+srv.URL+"/compute/v1/",
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_SQL="+srv.URL+"/",
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_CONTAINER="+srv.URL+"/")...)

	for _, c := range []struct {
		argv  []string
		parse func([]byte) ([]provider.Found, error)
		want  []provider.Found
	}{
		{provider.GCEInstancesCommand(), provider.ParseGCEInstances, []provider.Found{{Value: "vm-1", Hint: "asia-southeast1-a 10.1.0.2", Extra: "asia-southeast1-a"}}},
		{provider.CloudSQLInstancesCommand(), provider.ParseCloudSQL, []provider.Found{{Value: "p:asia-southeast1:orders", Hint: "POSTGRES_16", Extra: "POSTGRES_16"}}},
		{provider.GKEClustersCommand(), provider.ParseGKEClusters, []provider.Found{{Value: "gke-1", Hint: "asia-southeast1", Extra: "asia-southeast1"}}},
	} {
		out, code := u.run(append([]string{"-c", "g"}, c.argv...)...)
		found, err := c.parse([]byte(out))
		if code != 0 || err != nil || !reflect.DeepEqual(found, c.want) {
			t.Errorf("%s (exit %d): %v %v\n%s", strings.Join(c.argv[:3], " "), code, found, err, out)
		}
	}
}
