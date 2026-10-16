package catalog

import (
	"bytes"
	"cmp"
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
	if err := decodeJSON(out, &v); err != nil {
		return nil, "", err
	}
	next := ""
	if r.Paged { // else the CLI got every page itself, whatever it says
		next, _ = v["NextToken"].(string)
	}
	var rows []Row
	for _, item := range items(v, r.Items) {
		raw, _ := json.Marshal(item) // decoded from JSON: always encodes
		row := rowOf(r, item, raw)
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
	list, raws, err := decodeList(out)
	if err != nil {
		return nil, "", err
	}
	var rows []Row
	for i, item := range list {
		row := rowOf(r, item, raws[i])
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

// gcpCommand names the resource, and where it is — or that it is global —
// unless its id is a full path that says so.
func gcpCommand(s *Service, r *Resource, a Action, row Row) []string {
	argv := append(append([]string{"gcloud", s.Name}, strings.Fields(a.Command)...), row.ID)
	switch {
	case r.Location == nil || strings.Contains(row.ID, "/"):
	case row.Where != "":
		argv = append(argv, r.Location.Flag+"="+row.Where)
	case r.Location.Global: // a global address, not a region's
		argv = append(argv, "--global")
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
	list, raws, err := decodeList(out)
	if err != nil {
		return nil, "", err
	}
	var rows []Row
	for i, item := range list {
		row := rowOf(r, item, raws[i])
		row.Name = cmp.Or(row.Name, str(item, "displayName")) // Entra ID objects (az ad)
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

// decodeJSON reads a CLI's JSON, numbers as written (an id of 20 digits
// stays exact).
func decodeJSON(out []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(out))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("reading the list: %w", err)
	}
	return nil
}

// decodeList reads a JSON list: each item, and its JSON as printed.
func decodeList(out []byte) ([]any, []json.RawMessage, error) {
	var raws []json.RawMessage
	if err := decodeJSON(out, &raws); err != nil {
		return nil, nil, err
	}
	list := make([]any, len(raws))
	for i, raw := range raws {
		decodeJSON(raw, &list[i]) // a part of valid JSON: valid
	}
	return list, raws, nil
}

// rowOf is the id, name and state an item has (when its fields are
// strings) — or, for a list of strings, the item is the id. raw is the
// item's JSON.
func rowOf(r *Resource, item any, raw json.RawMessage) Row {
	if s, ok := item.(string); ok {
		return Row{ID: s, Raw: raw}
	}
	row := Row{ID: str(item, r.ID), Raw: raw}
	if r.Label != "tag:Name" {
		row.Name = str(item, r.Label)
	}
	row.State = str(item, r.State)
	if r.State == "" { // gcloud's resources
		row.State = cmp.Or(str(item, "status"), str(item, "state"))
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
