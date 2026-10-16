package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Row is one listed resource.
type Row struct {
	ID, Name, State string
	Where           string          // gcp: its zone, region or location; azure: its location
	Group           string          // azure: its resource group
	Raw             json.RawMessage // the item as the CLI printed it
}

// Source is how one cloud's CLI lists, describes and acts on resources — a
// GoF Strategy, so the Resources tab works the same for every cloud. Argv is
// the CLI's own arguments ("aws", "ec2", …), run through mek.
type Source interface {
	// List lists a resource; next is the page to fetch ("" for the first).
	List(s *Service, r *Resource, next string) []string
	// Rows reads what List printed, and the next page ("" when none).
	Rows(r *Resource, out []byte) ([]Row, string, error)
	// Describe shows one resource; nil when the row is all there is.
	Describe(s *Service, r *Resource, row Row) []string
	// Act runs an action on one resource.
	Act(s *Service, r *Resource, a Action, row Row) []string
}

// For is the Source of a cloud.
func For(cloud string) (Source, error) {
	switch cloud {
	case "aws":
		return aws{}, nil
	case "gcp":
		return gcp{}, nil
	case "azure":
		return azure{}, nil
	}
	return nil, fmt.Errorf("no resource catalog for %s", cloud)
}

// PageSize is how many items a page of an AWS list holds.
const PageSize = 100

// GCPLimit is how many items a gcloud list shows (gcloud fetches every
// page itself).
const GCPLimit = 500

type aws struct{}

func (aws) List(s *Service, r *Resource, next string) []string {
	argv := append(append([]string{"aws", s.Name}, strings.Fields(r.List)...), r.ListArgs...)
	argv = append(argv, "--output", "json")
	if r.Paged {
		argv = append(argv, "--max-items", fmt.Sprint(PageSize))
		if next != "" {
			argv = append(argv, "--starting-token", next)
		}
	}
	return argv
}

func (aws) Rows(r *Resource, out []byte) ([]Row, string, error) {
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, "", fmt.Errorf("reading the list: %w", err)
	}
	next, _ := v["NextToken"].(string)
	var rows []Row
	for _, item := range items(v, r.Items) {
		row := rowOf(r, item)
		if r.Label == "tag:Name" {
			row.Name = tag(item, "Name")
		}
		rows = append(rows, row)
	}
	return rows, next, nil
}

func (aws) Describe(s *Service, r *Resource, row Row) []string {
	if r.Describe == nil {
		return nil
	}
	return append(awsCommand(s, *r.Describe, row), "--output", "json")
}

func (aws) Act(s *Service, r *Resource, a Action, row Row) []string {
	return append(awsCommand(s, a, row), a.Flags...)
}

func awsCommand(s *Service, a Action, row Row) []string {
	return append(append([]string{"aws", s.Name}, strings.Fields(a.Command)...), a.Param, row.ID)
}

type gcp struct{}

func (gcp) List(s *Service, r *Resource, _ string) []string {
	argv := append(append([]string{"gcloud", s.Name}, strings.Fields(r.List)...), r.ListArgs...)
	return append(argv, "--format=json", fmt.Sprintf("--limit=%d", GCPLimit))
}

func (gcp) Rows(r *Resource, out []byte) ([]Row, string, error) {
	var list []any
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, "", fmt.Errorf("reading the list: %w", err)
	}
	var rows []Row
	for _, item := range list {
		row := rowOf(r, item)
		if row.Name == "" && strings.Contains(row.ID, "/") {
			row.Name = last(row.ID) // projects/p/locations/l/functions/f
		}
		if r.Location != nil {
			row.Where = last(str(item, r.Location.Field))
		}
		rows = append(rows, row)
	}
	return rows, "", nil
}

func (gcp) Describe(s *Service, r *Resource, row Row) []string {
	if r.Describe == nil {
		return nil
	}
	return append(gcpCommand(s, r, *r.Describe, row), "--format=json")
}

func (gcp) Act(s *Service, r *Resource, a Action, row Row) []string {
	return append(append(gcpCommand(s, r, a, row), a.Flags...), "--quiet") // mek ui has asked already
}

// gcpCommand names the resource, and where it is unless its id is a full
// path that says so.
func gcpCommand(s *Service, r *Resource, a Action, row Row) []string {
	argv := append(append([]string{"gcloud", s.Name}, strings.Fields(a.Command)...), row.ID)
	if r.Location != nil && row.Where != "" && !strings.Contains(row.ID, "/") {
		argv = append(argv, r.Location.Flag+"="+row.Where)
	}
	return argv
}

// azure lists ARM resources: each one's id is its resource id, which every
// command about it takes as --ids.
type azure struct{}

func (azure) List(s *Service, r *Resource, _ string) []string {
	argv := append(append([]string{"az", s.Name}, strings.Fields(r.List)...), r.ListArgs...)
	return append(argv, "--output", "json")
}

func (azure) Rows(r *Resource, out []byte) ([]Row, string, error) {
	var list []any
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, "", fmt.Errorf("reading the list: %w", err)
	}
	var rows []Row
	for _, item := range list {
		row := rowOf(r, item)
		row.Where, row.Group = str(item, "location"), str(item, "resourceGroup")
		rows = append(rows, row)
	}
	return rows, "", nil
}

func (azure) Describe(s *Service, r *Resource, row Row) []string {
	if r.Describe == nil {
		return nil
	}
	return append(azureCommand(s, *r.Describe, row), "--output", "json")
}

func (azure) Act(s *Service, r *Resource, a Action, row Row) []string {
	return append(azureCommand(s, a, row), a.Flags...)
}

func azureCommand(s *Service, a Action, row Row) []string {
	argv := append([]string{"az", s.Name}, strings.Fields(a.Command)...)
	if a.Group {
		argv = append(argv, "--resource-group", row.Group)
	}
	if a.ByName {
		return append(argv, a.Param, row.Name)
	}
	return append(argv, a.Param, row.ID)
}

// rowOf is the id, name and state an item has (when its fields are
// strings) — or, for a list of strings, the item is the id.
func rowOf(r *Resource, item any) Row {
	raw, _ := json.Marshal(item) // decoded from JSON: always encodes
	if s, ok := item.(string); ok {
		return Row{ID: s, Raw: raw}
	}
	row := Row{ID: str(item, r.ID), Raw: raw}
	if r.Label != "tag:Name" {
		row.Name = str(item, r.Label)
	}
	row.State = str(item, r.State)
	if r.State == "" { // gcloud's resources
		row.State = cmpStr(str(item, "status"), str(item, "state"))
	}
	return row
}

// items follows a path of lists: "Reservations[].Instances[]".
func items(v any, path string) []any {
	cur := []any{v}
	for _, part := range strings.Split(path, ".") {
		var next []any
		for _, c := range cur {
			if l, ok := get(c, strings.TrimSuffix(part, "[]")).([]any); ok {
				next = append(next, l...)
			}
		}
		cur = next
	}
	return cur
}

// str is the string at a path of fields ("State.Name", or with a quoted
// key: metadata.labels."cloud.googleapis.com/location"); "" when there is
// none.
func str(v any, path string) string {
	if path == "" {
		return ""
	}
	for _, k := range split(path) {
		v = get(v, k)
	}
	s, _ := v.(string)
	return s
}

func get(v any, k string) any {
	m, _ := v.(map[string]any)
	return m[k]
}

// split cuts a path at the dots outside quotes.
func split(path string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, c := range path {
		switch {
		case c == '"':
			quoted = !quoted
		case c == '.' && !quoted:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(c)
		}
	}
	return append(out, b.String())
}

// tag is an AWS item's tag value.
func tag(item any, key string) string {
	tags, _ := get(item, "Tags").([]any)
	for _, t := range tags {
		if str(t, "Key") == key {
			return str(t, "Value")
		}
	}
	return ""
}

func last(s string) string { return s[strings.LastIndex(s, "/")+1:] }

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
