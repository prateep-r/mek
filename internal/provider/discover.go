package provider

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
)

// Discovery is a command the config wizard runs to offer a cloud's values
// instead of asking you to type them. Every one only reads; each goes
// through mek's guard and audit log like any other command.
type Discovery struct {
	Cloud, What, Permission string
	Argv                    []string // as mek help wizard shows it
}

// Found is one value a discovery found; Extra is what comes with it (a
// VM's zone, a database's port, a cluster's location).
type Found struct{ Value, Label, Hint, Extra string }

// SSOLoginCommand logs in to a portal's IAM Identity Center session — the
// one mek's generated profiles share — so its accounts can be listed.
func SSOLoginCommand(startURL string) []string {
	return []string{"aws", "sso", "login", "--sso-session", sessionName(startURL)}
}

// ListAccountsCommand lists the accounts a portal login can reach; input is
// the file holding the access token (WithTokenFile).
func ListAccountsCommand(input, ssoRegion string) []string {
	return []string{"aws", "sso", "list-accounts", "--cli-input-json", "file://" + input, "--region", ssoRegion, "--output", "json"}
}

// ListAccountRolesCommand lists the roles a portal login has in an account.
func ListAccountRolesCommand(input, ssoRegion, account string) []string {
	return []string{"aws", "sso", "list-account-roles", "--cli-input-json", "file://" + input, "--account-id", account,
		"--region", ssoRegion, "--output", "json"}
}

// ListProjectsCommand lists the GCP projects the logged-in account can see.
func ListProjectsCommand() []string {
	return []string{"gcloud", "projects", "list", "--format=json(projectId,name)"}
}

// EC2InstancesCommand lists running instances: id, Name tag, private IP.
func EC2InstancesCommand() []string {
	return []string{"aws", "ec2", "describe-instances", "--filters", "Name=instance-state-name,Values=running",
		"--query", "Reservations[].Instances[].{id:InstanceId,name:Tags[?Key=='Name']|[0].Value,ip:PrivateIpAddress}", "--output", "json"}
}

// RDSInstancesCommand lists RDS databases with their endpoints.
func RDSInstancesCommand() []string {
	return []string{"aws", "rds", "describe-db-instances",
		"--query", "DBInstances[].{id:DBInstanceIdentifier,host:Endpoint.Address,port:Endpoint.Port,engine:Engine}", "--output", "json"}
}

// RDSClustersCommand lists Aurora clusters with their writer endpoints.
func RDSClustersCommand() []string {
	return []string{"aws", "rds", "describe-db-clusters",
		"--query", "DBClusters[].{id:DBClusterIdentifier,host:Endpoint,port:Port,engine:Engine}", "--output", "json"}
}

// EKSClustersCommand lists EKS clusters in region ("" : the context's).
func EKSClustersCommand(region string) []string {
	return command("aws", "eks", "list-clusters").opt("--region", region).args("--output", "json").build().Argv
}

// GCEInstancesCommand lists running VMs: name, zone, internal IP.
func GCEInstancesCommand() []string {
	return []string{"gcloud", "compute", "instances", "list", "--filter=status=RUNNING",
		"--format=json(name,zone.basename(),networkInterfaces[0].networkIP)"}
}

// CloudSQLInstancesCommand lists Cloud SQL instances.
func CloudSQLInstancesCommand() []string {
	return []string{"gcloud", "sql", "instances", "list", "--format=json(connectionName,databaseVersion)"}
}

// GKEClustersCommand lists GKE clusters in every location.
func GKEClustersCommand() []string {
	return []string{"gcloud", "container", "clusters", "list", "--format=json(name,location)"}
}

// Discoveries are every discovery command, for mek help wizard.
func Discoveries() []Discovery {
	return []Discovery{
		{"aws", "accounts", "your IAM Identity Center login (no IAM permission)", ListAccountsCommand("<token file>", "<sso region>")},
		{"aws", "roles in an account", "your IAM Identity Center login (no IAM permission)", ListAccountRolesCommand("<token file>", "<sso region>", "<account>")},
		{"aws", "instances (targets, tunnels)", "ec2:DescribeInstances", EC2InstancesCommand()},
		{"aws", "databases (tunnels)", "rds:DescribeDBInstances", RDSInstancesCommand()},
		{"aws", "Aurora clusters (tunnels)", "rds:DescribeDBClusters", RDSClustersCommand()},
		{"aws", "EKS clusters", "eks:ListClusters", EKSClustersCommand("")},
		{"gcp", "projects", "resourcemanager.projects.get on each (any role on it)", ListProjectsCommand()},
		{"gcp", "VMs (targets, tunnels)", "compute.instances.list", GCEInstancesCommand()},
		{"gcp", "Cloud SQL instances (tunnels)", "cloudsql.instances.list", CloudSQLInstancesCommand()},
		{"gcp", "GKE clusters", "container.clusters.list", GKEClustersCommand()},
	}
}

func decodeJSON[T any](b []byte, what string) (T, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("%s: unexpected output: %w", what, err)
	}
	return v, nil
}

// ParseAccounts reads aws sso list-accounts, by account name.
func ParseAccounts(b []byte) ([]Found, error) {
	r, err := decodeJSON[struct {
		AccountList []struct{ AccountID, AccountName, EmailAddress string }
	}](b, "aws sso list-accounts")
	var out []Found
	for _, a := range r.AccountList {
		f := Found{Value: a.AccountID, Label: a.AccountName, Hint: a.AccountID}
		if a.AccountName == "" || a.AccountName == a.AccountID { // no name of its own: the id, once
			f.Label, f.Hint = "", ""
		}
		out = append(out, f)
	}
	slices.SortFunc(out, func(x, y Found) int { return cmp.Compare(x.Label, y.Label) })
	return out, err
}

// ParseRoles reads aws sso list-account-roles, by name.
func ParseRoles(b []byte) ([]Found, error) {
	r, err := decodeJSON[struct{ RoleList []struct{ RoleName string } }](b, "aws sso list-account-roles")
	var out []Found
	for _, x := range r.RoleList {
		out = append(out, Found{Value: x.RoleName})
	}
	slices.SortFunc(out, func(x, y Found) int { return cmp.Compare(x.Value, y.Value) })
	return out, err
}

// ParseProjects reads gcloud projects list, by id; a name that differs from
// the id is the hint.
func ParseProjects(b []byte) ([]Found, error) {
	r, err := decodeJSON[[]struct{ ProjectID, Name string }](b, "gcloud projects list")
	var out []Found
	for _, p := range r {
		f := Found{Value: p.ProjectID}
		if p.Name != p.ProjectID {
			f.Hint = p.Name
		}
		out = append(out, f)
	}
	slices.SortFunc(out, func(x, y Found) int { return cmp.Compare(x.Value, y.Value) })
	return out, err
}

func byLabel(out []Found) []Found {
	slices.SortFunc(out, func(x, y Found) int { return cmp.Or(cmp.Compare(x.Label, y.Label), cmp.Compare(x.Value, y.Value)) })
	return out
}

// ParseEC2Instances reads EC2InstancesCommand: by Name tag, then id.
func ParseEC2Instances(b []byte) ([]Found, error) {
	r, err := decodeJSON[[]struct{ ID, Name, IP string }](b, "aws ec2 describe-instances")
	var out []Found
	for _, i := range r {
		f := Found{Value: i.ID, Label: i.Name, Hint: i.IP, Extra: i.Name}
		if i.Name != "" { // the id shows when there is no name
			f.Hint = i.ID + " " + i.IP
		}
		out = append(out, f)
	}
	return byLabel(out), err
}

// ParseRDS reads RDSInstancesCommand or RDSClustersCommand: the endpoint,
// by identifier; Extra is the port.
func ParseRDS(b []byte) ([]Found, error) {
	r, err := decodeJSON[[]struct {
		ID, Host, Engine string
		Port             int
	}](b, "aws rds")
	var out []Found
	for _, d := range r {
		if d.Host == "" { // still being created
			continue
		}
		out = append(out, Found{Value: d.Host, Label: d.ID, Hint: fmt.Sprintf("%s:%d", d.Engine, d.Port), Extra: strconv.Itoa(d.Port)})
	}
	return byLabel(out), err
}

// ParseEKSClusters reads EKSClustersCommand.
func ParseEKSClusters(b []byte) ([]Found, error) {
	r, err := decodeJSON[struct{ Clusters []string }](b, "aws eks list-clusters")
	var out []Found
	for _, c := range r.Clusters {
		out = append(out, Found{Value: c})
	}
	return byLabel(out), err
}

// ParseGCEInstances reads GCEInstancesCommand; Extra is the zone.
func ParseGCEInstances(b []byte) ([]Found, error) {
	r, err := decodeJSON[[]struct {
		Name, Zone        string
		NetworkInterfaces []struct{ NetworkIP string }
	}](b, "gcloud compute instances list")
	var out []Found
	for _, v := range r {
		hint := v.Zone
		if len(v.NetworkInterfaces) > 0 {
			hint += " " + v.NetworkInterfaces[0].NetworkIP
		}
		out = append(out, Found{Value: v.Name, Hint: hint, Extra: v.Zone})
	}
	return byLabel(out), err
}

// ParseCloudSQL reads CloudSQLInstancesCommand; Extra is the database
// version (POSTGRES_16, MYSQL_8_0, …).
func ParseCloudSQL(b []byte) ([]Found, error) {
	r, err := decodeJSON[[]struct{ ConnectionName, DatabaseVersion string }](b, "gcloud sql instances list")
	var out []Found
	for _, i := range r {
		out = append(out, Found{Value: i.ConnectionName, Hint: i.DatabaseVersion, Extra: i.DatabaseVersion})
	}
	return byLabel(out), err
}

// ParseGKEClusters reads GKEClustersCommand; Extra is the location.
func ParseGKEClusters(b []byte) ([]Found, error) {
	r, err := decodeJSON[[]struct{ Name, Location string }](b, "gcloud container clusters list")
	var out []Found
	for _, c := range r {
		out = append(out, Found{Value: c.Name, Hint: c.Location, Extra: c.Location})
	}
	return byLabel(out), err
}
