package provider

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
		{EC2InstancesCommand(), "aws ec2 describe-instances --filters Name=instance-state-name,Values=running --query Reservations[].Instances[].{id:InstanceId,name:Tags[?Key=='Name']|[0].Value,ip:PrivateIpAddress} --output json"},
		{RDSInstancesCommand(), "aws rds describe-db-instances --query DBInstances[].{id:DBInstanceIdentifier,host:Endpoint.Address,port:Endpoint.Port,engine:Engine} --output json"},
		{RDSClustersCommand(), "aws rds describe-db-clusters --query DBClusters[].{id:DBClusterIdentifier,host:Endpoint,port:Port,engine:Engine} --output json"},
		{EKSClustersCommand("us-east-1"), "aws eks list-clusters --region us-east-1 --output json"},
		{EKSClustersCommand(""), "aws eks list-clusters --output json"},
		{GCEInstancesCommand(), "gcloud compute instances list --filter=status=RUNNING --format=json(name,zone.basename(),networkInterfaces[0].networkIP)"},
		{CloudSQLInstancesCommand(), "gcloud sql instances list --format=json(connectionName,databaseVersion)"},
		{GKEClustersCommand(), "gcloud container clusters list --format=json(name,location)"},
		{AzureLoginCommand(""), "az login"},
		{AzureLoginCommand("acme.onmicrosoft.com"), "az login --tenant acme.onmicrosoft.com"},
		{AzureTenantsCommand(), "az account tenant list --query [].{id:tenantId,name:displayName,domain:defaultDomain} --output json"},
		{AzureSubscriptionsCommand("t-1"), "az account list --all --query [?tenantId=='t-1'].{id:id,name:name,state:state} --output json"},
		{AzureBastionsCommand("s-1"), "az network bastion list --subscription s-1 --query [].{name:name,group:resourceGroup,sku:sku.name} --output json"},
		{AzureBastionsCommand(""), "az network bastion list --query [].{name:name,group:resourceGroup,sku:sku.name} --output json"},
		{AzureVMsCommand(), "az vm list --show-details --query [?powerState=='VM running'].{name:name,group:resourceGroup,ip:privateIps} --output json"},
		{AKSClustersCommand(), "az aks list --query [].{name:name,group:resourceGroup,location:location} --output json"},
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
	if err != nil || !reflect.DeepEqual(accts, []Found{{Value: "111122223333", Label: "dev", Hint: "111122223333"}, {Value: "444455556666", Label: "prod", Hint: "444455556666"}}) {
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
	ec2, err := ParseEC2Instances([]byte(`[{"id":"i-2","name":"bastion","ip":"10.0.0.2"},{"id":"i-1","name":null,"ip":"10.0.0.1"}]`))
	if err != nil || !reflect.DeepEqual(ec2, []Found{{Value: "i-1", Hint: "10.0.0.1"}, {Value: "i-2", Label: "bastion", Hint: "i-2 10.0.0.2", Extra: "bastion"}}) {
		t.Errorf("ec2: %v %v", ec2, err)
	}
	rds, err := ParseRDS([]byte(`[{"id":"orders","host":"orders.x.rds.amazonaws.com","port":5432,"engine":"postgres"},{"id":"new","host":null,"port":null,"engine":"mysql"}]`))
	if err != nil || !reflect.DeepEqual(rds, []Found{{Value: "orders.x.rds.amazonaws.com", Label: "orders", Hint: "postgres:5432", Extra: "5432"}}) {
		t.Errorf("rds: %v %v", rds, err)
	}
	eks, err := ParseEKSClusters([]byte(`{"clusters":["b","a"]}`))
	if err != nil || !reflect.DeepEqual(eks, []Found{{Value: "a"}, {Value: "b"}}) {
		t.Errorf("eks: %v %v", eks, err)
	}
	vms, err := ParseGCEInstances([]byte(`[{"name":"vm-1","zone":"asia-southeast1-a","networkInterfaces":[{"networkIP":"10.1.0.2"}]},{"name":"bare","zone":"us-central1-b"}]`))
	if err != nil || !reflect.DeepEqual(vms, []Found{{Value: "bare", Hint: "us-central1-b", Extra: "us-central1-b"}, {Value: "vm-1", Hint: "asia-southeast1-a 10.1.0.2", Extra: "asia-southeast1-a"}}) {
		t.Errorf("vms: %v %v", vms, err)
	}
	sql, err := ParseCloudSQL([]byte(`[{"connectionName":"p:asia-southeast1:db","databaseVersion":"POSTGRES_16"}]`))
	if err != nil || !reflect.DeepEqual(sql, []Found{{Value: "p:asia-southeast1:db", Hint: "POSTGRES_16", Extra: "POSTGRES_16"}}) {
		t.Errorf("cloud sql: %v %v", sql, err)
	}
	gke, err := ParseGKEClusters([]byte(`[{"name":"main","location":"asia-southeast1"}]`))
	if err != nil || !reflect.DeepEqual(gke, []Found{{Value: "main", Hint: "asia-southeast1", Extra: "asia-southeast1"}}) {
		t.Errorf("gke: %v %v", gke, err)
	}
	unnamed, _ := ParseAccounts([]byte(`{"accountList":[{"accountId":"111122223333","accountName":"111122223333"},{"accountId":"444455556666"}]}`))
	if !reflect.DeepEqual(unnamed, []Found{{Value: "111122223333"}, {Value: "444455556666"}}) {
		t.Errorf("accounts without names: %v", unnamed)
	}
	tenants, err := ParseAzureTenants([]byte(`[{"id":"t-2","name":"Zeta","domain":"z.onmicrosoft.com"},{"id":"t-1","name":"Acme","domain":"acme.onmicrosoft.com"}]`))
	if err != nil || !reflect.DeepEqual(tenants, []Found{{Value: "t-1", Label: "Acme", Hint: "acme.onmicrosoft.com"}, {Value: "t-2", Label: "Zeta", Hint: "z.onmicrosoft.com"}}) {
		t.Errorf("tenants: %v %v", tenants, err)
	}
	subs, err := ParseAzureSubscriptions([]byte(`[{"id":"s-1","name":"Prod","state":"Enabled"},{"id":"s-2","name":"Old","state":"Disabled"},{"id":"s-3","name":"New"}]`))
	if err != nil || !reflect.DeepEqual(subs, []Found{{Value: "s-3", Label: "New", Hint: "s-3"}, {Value: "s-2", Label: "Old", Hint: "s-2 (Disabled)"}, {Value: "s-1", Label: "Prod", Hint: "s-1"}}) {
		t.Errorf("subscriptions: %v %v", subs, err)
	}
	bastions, err := ParseAzureBastions([]byte(`[{"name":"bas","group":"rg-net","sku":"Standard"},{"name":"old","group":"rg-x","sku":null}]`))
	if err != nil || !reflect.DeepEqual(bastions, []Found{{Value: "bas", Hint: "rg-net · Standard", Extra: "rg-net"}, {Value: "old", Hint: "rg-x", Extra: "rg-x"}}) {
		t.Errorf("bastions: %v %v", bastions, err)
	}
	azvms, err := ParseAzureVMs([]byte(`[{"name":"vm-jump","group":"rg-app","ip":"10.2.0.4"},{"name":"bare","group":"rg-app","ip":""}]`))
	if err != nil || !reflect.DeepEqual(azvms, []Found{{Value: "bare", Hint: "rg-app", Extra: "rg-app"}, {Value: "vm-jump", Hint: "rg-app 10.2.0.4", Extra: "rg-app"}}) {
		t.Errorf("azure vms: %v %v", azvms, err)
	}
	aks, err := ParseAKSClusters([]byte(`[{"name":"aks-1","group":"rg-aks","location":"southeastasia"}]`))
	if err != nil || !reflect.DeepEqual(aks, []Found{{Value: "aks-1", Hint: "rg-aks · southeastasia", Extra: "rg-aks"}}) {
		t.Errorf("aks: %v %v", aks, err)
	}
	for _, f := range []func([]byte) ([]Found, error){ParseAccounts, ParseRoles, ParseProjects, ParseEC2Instances, ParseRDS, ParseEKSClusters, ParseGCEInstances, ParseCloudSQL, ParseGKEClusters,
		ParseAzureTenants, ParseAzureSubscriptions, ParseAzureBastions, ParseAzureVMs, ParseAKSClusters} {
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
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
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
