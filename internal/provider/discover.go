package provider

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
)

// Discovery is a command the config wizard runs to offer a cloud's values
// instead of asking you to type them. Every one only reads; each goes
// through mek's guard and audit log like any other command.
type Discovery struct {
	Cloud, What, Permission string
	Argv                    []string // as mek help wizard shows it
}

// Found is one value a discovery found.
type Found struct{ Value, Label, Hint string }

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

// Discoveries are every discovery command, for mek help wizard.
func Discoveries() []Discovery {
	return []Discovery{
		{"aws", "accounts", "your IAM Identity Center login (no IAM permission)", ListAccountsCommand("<token file>", "<sso region>")},
		{"aws", "roles in an account", "your IAM Identity Center login (no IAM permission)", ListAccountRolesCommand("<token file>", "<sso region>", "<account>")},
		{"gcp", "projects", "resourcemanager.projects.get on each (any role on it)", ListProjectsCommand()},
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
		out = append(out, Found{Value: a.AccountID, Label: a.AccountName, Hint: a.AccountID})
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
