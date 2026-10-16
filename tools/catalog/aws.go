package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/prateep-r/mek/internal/catalog"
)

// awsModels is what the AWS CLI ships: its command index (exact command
// and parameter names, required flags) and botocore's service models.
type awsModels struct {
	index, data string                        // ac.index; botocore/data
	commands    map[string]map[string][]param // service → command → its params
}

type param struct {
	name     string
	required bool
}

func loadAWS(awscli string) (*awsModels, error) {
	m := &awsModels{index: filepath.Join(awscli, "data", "ac.index"), data: filepath.Join(awscli, "botocore", "data"),
		commands: map[string]map[string][]param{}}
	out, err := exec.Command("sqlite3", "-separator", "\t", m.index,
		"select c.parent, c.command, coalesce(p.argname,''), coalesce(p.required,0) from command_table c "+
			"left join param_table p on p.command = c.command and p.parent = c.parent where c.parent like 'aws.%'").Output()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", m.index, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || strings.Count(f[0], ".") != 1 {
			continue // aws.<service> only, not deeper customisations
		}
		svc := strings.TrimPrefix(f[0], "aws.")
		if m.commands[svc] == nil {
			m.commands[svc] = map[string][]param{}
		}
		ps := m.commands[svc][f[1]]
		if f[2] != "" {
			ps = append(ps, param{f[2], f[3] == "1"})
		}
		m.commands[svc][f[1]] = ps
	}
	return m, nil
}

// norm makes an API name and a CLI name comparable: DescribeDBInstances and
// describe-db-instances are both "describedbinstances".
func norm(s string) string { return strings.ToLower(strings.ReplaceAll(s, "-", "")) }

type shape struct {
	Type     string                            `json:"type"`
	Members  map[string]struct{ Shape string } `json:"members"`
	Member   struct{ Shape string }            `json:"member"`
	Required []string                          `json:"required"`
}

type serviceModel struct {
	Operations map[string]struct {
		Input, Output struct{ Shape string }
	} `json:"operations"`
	Shapes map[string]shape `json:"shapes"`
}

// latest is a service's newest model directory.
func latest(dir string) string {
	vs, _ := filepath.Glob(filepath.Join(dir, "*", "service-2.json"))
	sort.Strings(vs)
	if len(vs) == 0 {
		return ""
	}
	return filepath.Dir(vs[len(vs)-1])
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// cliService is the CLI name of a botocore service directory.
func cliService(dir string) string {
	if dir == "s3" {
		return "s3api" // `aws s3` is the high-level commands
	}
	return dir
}

func (m *awsModels) catalog(version string) catalog.Catalog {
	c := catalog.Catalog{Cloud: "aws", Source: "aws-cli/" + version}
	dirs, _ := os.ReadDir(m.data)
	for _, d := range dirs {
		svc := cliService(d.Name())
		cmds := m.commands[svc]
		ver := latest(filepath.Join(m.data, d.Name()))
		if cmds == nil || ver == "" {
			continue
		}
		var model serviceModel
		if !readJSON(filepath.Join(ver, "service-2.json"), &model) {
			continue
		}
		var pg struct{ Pagination map[string]map[string]any }
		readJSON(filepath.Join(ver, "paginators-1.json"), &pg)
		s := catalog.Service{Name: svc, Resources: resources(cmds, model, pg.Pagination)}
		if len(s.Resources) > 0 {
			c.Services = append(c.Services, s)
		}
	}
	return c
}

// cliName is the CLI command for an API operation ("" when it has none).
func cliName(cmds map[string][]param, op string) string {
	for name := range cmds {
		if norm(name) == norm(op) {
			return name
		}
	}
	return ""
}

// resources are the service's List*/Describe* commands that need no
// parameters and whose output holds the resource's list.
func resources(cmds map[string][]param, model serviceModel, pages map[string]map[string]any) []catalog.Resource {
	var out []catalog.Resource
	for _, op := range sortedKeys(model.Operations) {
		list := cliName(cmds, op)
		if list == "" || hasRequired(cmds[list]) || !(strings.HasPrefix(op, "List") || strings.HasPrefix(op, "Describe")) {
			continue
		}
		res := singular(list)
		items, item := findItems(model, model.Shapes[model.Operations[op].Output.Shape], res)
		if items == "" {
			continue
		}
		r := catalog.Resource{
			Name:  strings.TrimPrefix(strings.TrimPrefix(list, "list-"), "describe-"),
			List:  list,
			Items: items,
			Paged: pages[op] != nil,
		}
		if item != nil {
			r.Label, r.State = nameAndState(model, *item, res)
		}
		if !pickID(model, cmds, item, &r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findItems is the path to the resource's list in an output shape and the
// shape of one item (nil for a list of strings): the list named after the
// resource or the only list, followed down a level when its items hold the
// resource's list (Reservations[].Instances[]).
func findItems(model serviceModel, out shape, res string) (string, *shape) {
	name := pickList(model, out, res, true)
	if name == "" {
		return "", nil
	}
	elem := model.Shapes[model.Shapes[out.Members[name].Shape].Member.Shape]
	switch elem.Type {
	case "string":
		return name + "[]", nil
	case "structure":
		if inner := pickList(model, elem, res, false); inner != "" {
			in := model.Shapes[model.Shapes[elem.Members[inner].Shape].Member.Shape]
			if in.Type == "structure" {
				return name + "[]." + inner + "[]", &in
			}
		}
		return name + "[]", &elem
	}
	return "", nil
}

// pickList is the member list named after the resource (Instances for
// "instance"), or, when only is set, the shape's only list.
func pickList(model serviceModel, s shape, res string, only bool) string {
	var lists []string
	for _, k := range sortedKeys(s.Members) {
		if model.Shapes[s.Members[k].Shape].Type != "list" {
			continue
		}
		if singular(k) == res {
			return k
		}
		lists = append(lists, k)
	}
	if only && len(lists) == 1 {
		return lists[0]
	}
	return ""
}

// pickID sets the item's identifying member and the actions that take it:
// of its string members, the one the most commands take (GroupId for
// security groups, UserName for IAM users), else the one named after the
// resource (InstanceId, DBInstanceIdentifier), then Name, Id, an ARN. False
// when nothing identifies an item.
func pickID(model serviceModel, cmds map[string][]param, item *shape, r *catalog.Resource) bool {
	if item == nil { // a list of strings: each is its own id
		r.Describe, r.Actions = actions(cmds, *r)
		return true
	}
	res := singular(r.List)
	var ids []string
	for _, want := range []string{res + "id", res + "identifier", res + "name", "id", "name", res + "arn", "arn"} {
		for _, k := range sortedKeys(item.Members) {
			if norm(k) == want && model.Shapes[item.Members[k].Shape].Type == "string" {
				ids = append(ids, k)
			}
		}
	}
	for _, k := range sortedKeys(item.Members) {
		if n := norm(k); (strings.HasSuffix(n, "id") || strings.HasSuffix(n, "name") || strings.HasSuffix(n, "arn")) &&
			model.Shapes[item.Members[k].Shape].Type == "string" && !slices.Contains(ids, k) {
			ids = append(ids, k)
		}
	}
	best := -1
	for _, id := range ids {
		c := *r
		c.ID = id
		c.Describe, c.Actions = actions(cmds, c)
		score := len(c.Actions)
		if c.Describe != nil {
			score++
		}
		if score == 0 && !named(id, res) {
			continue
		}
		if score > best {
			best, *r = score, c
		}
	}
	if r.Label == r.ID {
		r.Label = ""
	}
	return r.ID != ""
}

func hasRequired(ps []param) bool {
	for _, p := range ps {
		if p.required {
			return true
		}
	}
	return false
}

// singular is a list command's resource, singular: describe-db-instances →
// "dbinstance", list-functions → "function".
func singular(list string) string {
	n := norm(strings.TrimPrefix(strings.TrimPrefix(list, "list-"), "describe-"))
	switch {
	case strings.HasSuffix(n, "ies"):
		return strings.TrimSuffix(n, "ies") + "y"
	case strings.HasSuffix(n, "sses"), strings.HasSuffix(n, "ches"), strings.HasSuffix(n, "shes"):
		return strings.TrimSuffix(n, "es")
	}
	return strings.TrimSuffix(n, "s")
}

// named is whether a member is one idField would choose without actions.
func named(k, res string) bool {
	switch norm(k) {
	case res + "id", res + "identifier", res + "name", "id", "name", res + "arn", "arn":
		return true
	}
	return false
}

// nameAndState are expressions for an item's name and state, when it has
// them: a Name tag or name member; a state or status (or its .Name).
func nameAndState(model serviceModel, item shape, res string) (name, state string) {
	if t, ok := item.Members["Tags"]; ok {
		if el := model.Shapes[model.Shapes[t.Shape].Member.Shape]; el.Members["Key"].Shape != "" && el.Members["Value"].Shape != "" {
			name = "tag:Name"
		}
	}
	for _, k := range sortedKeys(item.Members) {
		n, s := norm(k), model.Shapes[item.Members[k].Shape]
		switch {
		case name == "" && (n == "name" || n == res+"name") && s.Type == "string":
			name = k
		case state == "" && (n == "state" || n == "status" || n == res+"state" || n == res+"status"):
			if s.Type == "string" {
				state = k
			} else if _, ok := s.Members["Name"]; ok {
				state = k + ".Name"
			}
		}
	}
	return name, state
}

// actions are the commands about the resource (stop-instances,
// delete-table) whose only required parameter is its id: --instance-ids for
// InstanceId, --table-name or --name for a table's name. A describe-/get-
// one is the detail view; the list command itself is when it takes the id.
func actions(cmds map[string][]param, r catalog.Resource) (*catalog.Action, []catalog.Action) {
	res := singular(r.List)
	id := norm(r.ID)
	want := map[string]bool{id: true, id + "s": true}
	switch id {
	case "": // a list of strings: names, ids, URLs or ARNs of the resource
		for _, w := range []string{res, res + "name", res + "id", res + "url", res + "arn", "name", "id"} {
			want[w] = true
		}
	case "name", "id", res + "name", res + "identifier":
		want[res], want[res+"s"] = true, true // --bucket, --cluster
		if id == "name" || id == "id" {
			want[res+id], want[res+id+"s"] = true, true // --image-id takes an Id, not a Name
		}
	}
	var describe *catalog.Action
	var actions []catalog.Action
	for _, cmd := range sortedKeys(cmds) {
		verb, obj, _ := strings.Cut(cmd, "-")
		if o := norm(obj); o != res && singular("list-"+o) != res {
			continue
		}
		// The one parameter it requires, or with none required, the one
		// that names the resource (delete-security-group --group-id).
		var req, named []param
		for _, p := range cmds[cmd] {
			if p.required {
				req = append(req, p)
			}
			if want[norm(p.name)] {
				named = append(named, p)
			}
		}
		if len(req) == 0 && len(named) == 1 {
			req = named
		}
		if len(req) != 1 || !want[norm(req[0].name)] {
			continue
		}
		a := catalog.Action{Command: cmd, Param: "--" + req[0].name}
		switch {
		case verb == "describe" || verb == "get":
			if describe == nil {
				describe = &a
			}
		case acts[verb]:
			actions = append(actions, a)
		}
	}
	for _, p := range cmds[r.List] {
		if want[norm(p.name)] && norm(p.name) != "name" && id != "" {
			describe = &catalog.Action{Command: r.List, Param: "--" + p.name}
		}
	}
	return describe, actions
}
