package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/prateep-r/mek/internal/catalog"
)

// gcloudTree prints gcloud's command tree (GA commands, not hidden) as JSON,
// walked with gcloud's own loader; commands that fail to load are skipped.
const gcloudTree = `
import sys
sys.path[:0] = [sys.argv[1] + '/lib', sys.argv[1] + '/lib/third_party']
from googlecloudsdk import gcloud_main
from googlecloudsdk.calliope import cli_tree
tree = cli_tree.CliTreeGenerator(gcloud_main.CreateCLI([]), ignore_load_errors=True).Walk(hidden=False)
cli_tree._DumpToFile(tree, sys.stdout)
`

// gcpNode is a group or command in gcloud's tree; flags index into the
// tree's flag list.
type gcpNode struct {
	Commands    map[string]*gcpNode `json:"commands"`
	Flags       map[string]int      `json:"flags"`
	Positionals []struct {
		Name       string `json:"name"`
		Nargs      string `json:"nargs"`
		IsRequired bool   `json:"is_required"`
	} `json:"positionals"`
	Constraints json.RawMessage `json:"constraints"`
	IsGroup     bool            `json:"is_group"`
}

type gcpTree struct {
	gcpNode
	FlagList []struct {
		IsRequired bool `json:"is_required"`
		IsGlobal   bool `json:"is_global"`
	} `json:"SERIALIZED_FLAG_LIST"`
}

func loadGCP() (*gcpTree, string, error) {
	info, err := exec.Command("gcloud", "info", "--format=value(installation.sdk_root,basic.python_location,basic.version)").Output()
	if err != nil {
		return nil, "", fmt.Errorf("gcloud info: %w", err)
	}
	f := strings.Fields(string(info))
	if len(f) != 3 {
		return nil, "", fmt.Errorf("gcloud info: %q", info)
	}
	root, python, version := f[0], f[1], f[2]
	cmd := exec.Command(python, "-I", "-c", gcloudTree, root)
	cmd.Env = append(cmd.Environ(), "CLOUDSDK_CORE_DISABLE_PROMPTS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("walk gcloud's tree with %s: %w: %s", filepath.Base(python), err, stderr.String())
	}
	var t gcpTree
	if err := json.Unmarshal(out, &t); err != nil {
		return nil, "", err
	}
	return &t, version, nil
}

// gcpCatalog is every group with a list command that needs nothing: the
// service is the top group (compute), the resource the rest (instances).
func (t *gcpTree) catalog(version string) catalog.Catalog {
	c := catalog.Catalog{Cloud: "gcp", Source: "gcloud/" + version}
	for _, svc := range sortedKeys(t.Commands) {
		n := t.Commands[svc]
		if !n.IsGroup {
			continue
		}
		s := catalog.Service{Name: svc}
		t.walk(n, nil, &s)
		if len(s.Resources) > 0 {
			c.Services = append(c.Services, s)
		}
	}
	return c
}

func (t *gcpTree) walk(n *gcpNode, path []string, s *catalog.Service) {
	if l := n.Commands["list"]; l != nil && !l.IsGroup && t.free(l) {
		r := t.resource(n, path)
		if r.Name == "" {
			r.Name = s.Name // gcloud projects list
		}
		s.Resources = append(s.Resources, r)
	}
	for _, k := range sortedKeys(n.Commands) {
		if sub := n.Commands[k]; sub.IsGroup {
			t.walk(sub, append(slices.Clone(path), k), s)
		}
	}
}

func (t *gcpTree) resource(n *gcpNode, path []string) catalog.Resource {
	prefix := strings.Join(append(slices.Clone(path), ""), " ")
	r := catalog.Resource{Name: strings.Join(path, " "), List: prefix + "list", ID: "name"}
	for _, verb := range sortedKeys(n.Commands) {
		c := n.Commands[verb]
		if c.IsGroup || !t.takesOne(c) {
			continue
		}
		a := catalog.Action{Command: prefix + verb}
		switch {
		case verb == "describe":
			r.Describe = &a
		case acts[verb]:
			r.Actions = append(r.Actions, a)
		default:
			continue
		}
		if r.Location == nil {
			for _, f := range []string{"location", "zone", "region"} {
				if i, ok := c.Flags["--"+f]; ok && !t.FlagList[i].IsGlobal {
					r.Location = &catalog.Location{Flag: "--" + f, Field: f}
					break
				}
			}
		}
	}
	return r
}

// free is whether a command runs with no arguments.
func (t *gcpTree) free(c *gcpNode) bool {
	for _, p := range c.Positionals {
		if p.IsRequired {
			return false
		}
	}
	return t.flagsFree(c)
}

// takesOne is whether a command needs just one resource's name.
func (t *gcpTree) takesOne(c *gcpNode) bool {
	var req int
	for _, p := range c.Positionals {
		if p.IsRequired {
			req++
		}
	}
	return req == 1 && len(c.Positionals) == 1 && (c.Positionals[0].Nargs == "1" || c.Positionals[0].Nargs == "+") && t.flagsFree(c)
}

// flagsFree is whether a command needs no flag: none is required, and no
// required group of arguments is made only of flags.
func (t *gcpTree) flagsFree(c *gcpNode) bool {
	for _, i := range c.Flags {
		if f := t.FlagList[i]; f.IsRequired && !f.IsGlobal {
			return false
		}
	}
	var group gcpGroup
	if json.Unmarshal(c.Constraints, &group) != nil {
		return true
	}
	return !group.needsFlag(true)
}

// gcpGroup is a group of arguments in a command's constraints: flag indexes,
// -1 for the positional, or nested groups.
type gcpGroup struct {
	Arguments  []json.RawMessage `json:"arguments"`
	IsRequired bool              `json:"is_required"`
}

// needsFlag is whether a required group under g can only be met with flags.
func (g gcpGroup) needsFlag(top bool) bool {
	positional := false
	for _, a := range g.Arguments {
		var sub gcpGroup
		if json.Unmarshal(a, &sub) == nil {
			if sub.needsFlag(false) {
				return true
			}
			continue
		}
		var i int
		if json.Unmarshal(a, &i) == nil && i == -1 {
			positional = true
		}
	}
	return !top && g.IsRequired && !positional
}
