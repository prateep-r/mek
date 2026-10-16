package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/prateep-r/mek/internal/catalog"
)

// azTree prints az's commands (not preview or experimental, not
// deprecated) as JSON, read from az's own command loader: for each, its
// required arguments, and whether each can come from a resource id
// (--ids).
const azTree = `
import json, sys, os
os.environ['AZURE_CORE_COLLECT_TELEMETRY'] = 'no'
from azure.cli.core import get_default_cli
cli = get_default_cli()
cli.invocation = cli.invocation_cls(cli_ctx=cli, parser_cls=cli.parser_cls,
    commands_loader_cls=cli.commands_loader_cls, help_cls=cli.help_cls)
loader = cli.invocation.commands_loader
loader.load_command_table(None)
out = {}
for name in sorted(loader.command_table):
    cmd = loader.command_table[name]
    if getattr(cmd, 'preview_info', None) or getattr(cmd, 'experimental_info', None) or getattr(cmd, 'deprecate_info', None):
        continue
    try:
        # Argument settings apply to the command being run: this one.
        cli.invocation.data['command_string'] = name
        loader.command_name = name
        loader.load_arguments(name)
    except Exception:
        continue
    args = []
    for dest, arg in cmd.arguments.items():
        s = arg.type.settings
        if s.get('help') == '==SUPPRESS==':
            continue
        opts = [o for o in (s.get('options_list') or []) if isinstance(o, str) and o.startswith('--')]
        args.append({'dest': dest, 'required': bool(s.get('required')), 'id': bool(s.get('id_part')),
                     'option': opts[0] if opts else ''})
    out[name] = args
json.dump(out, sys.stdout)
`

type azArg struct {
	Dest     string `json:"dest"`
	Required bool   `json:"required"`
	ID       bool   `json:"id"`
	Option   string `json:"option"` // its long flag: "--name"
}

var pythonLocation = regexp.MustCompile(`Python location '([^']+)'`)

func loadAzure() (map[string][]azArg, string, error) {
	out, err := exec.Command("az", "version", "--output", "json").Output()
	if err != nil {
		return nil, "", fmt.Errorf("az version: %w", err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, "", err
	}
	version, _ := v["azure-cli"].(string)
	long, _ := exec.Command("az", "--version").Output()
	m := pythonLocation.FindSubmatch(long)
	if m == nil {
		return nil, "", fmt.Errorf("az --version names no Python:\n%s", long)
	}
	cmd := exec.Command(string(m[1]), "-c", azTree)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	tree, err := cmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("walk az's commands: %w: %s", err, stderr.String())
	}
	var cmds map[string][]azArg
	if err := json.Unmarshal(tree, &cmds); err != nil {
		return nil, "", err
	}
	return cmds, version, nil
}

// azureCatalog is every group with a list command that needs nothing: the
// service is the top group (vm), the resource the rest ("" for the group
// itself, named after the service). Items are ARM resources: their id is
// their resource id, which every action takes as --ids.
func azureCatalog(cmds map[string][]azArg, version string) catalog.Catalog {
	c := catalog.Catalog{Cloud: "azure", Source: "azure-cli/" + version}
	groups := map[string]bool{}
	for name := range cmds {
		if g, verb, ok := cut(name); ok && verb == "list" && azFree(cmds[name]) {
			groups[g] = true
		}
	}
	byService := map[string]*catalog.Service{}
	for _, g := range sortedKeys(groups) {
		svc, rest, _ := strings.Cut(g, " ")
		r := catalog.Resource{Name: rest, List: strings.TrimPrefix(rest+" list", " "), ID: "id", Label: "name", State: "provisioningState"}
		if r.Name == "" {
			r.Name = svc
		}
		prefix := strings.TrimPrefix(rest+" ", " ")
		for _, name := range sortedKeys(cmds) {
			grp, verb, ok := cut(name)
			if !ok || grp != g {
				continue
			}
			a := catalog.Action{Command: prefix + verb, Param: "--ids"}
			if !azByID(cmds[name]) {
				if a.Param, a.Group = azByName(cmds[name]); a.Param == "" {
					continue
				}
				a.ByName = a.Param != "--id" // az ad … --id: the item's id, not its name
			}
			if slices.ContainsFunc(cmds[name], func(x azArg) bool { return x.Dest == "yes" }) {
				a.Flags = []string{"--yes"} // az's own "are you sure?": mek ui has asked
			}
			switch {
			case verb == "show":
				r.Describe = &a
			case acts[verb]:
				r.Actions = append(r.Actions, a)
			}
		}
		if r.Describe == nil && len(r.Actions) == 0 {
			continue // its items aren't ARM resources that take --ids
		}
		s := byService[svc]
		if s == nil {
			s = &catalog.Service{Name: svc}
			byService[svc] = s
		}
		s.Resources = append(s.Resources, r)
	}
	for _, name := range sortedKeys(byService) {
		c.Services = append(c.Services, *byService[name])
	}
	return c
}

// cut splits "vm disk list" into its group and verb.
func cut(name string) (group, verb string, ok bool) {
	i := strings.LastIndex(name, " ")
	if i < 0 {
		return "", "", false
	}
	return name[:i], name[i+1:], true
}

// azByName is the flag that names the resource when a command requires
// just its name, and maybe its resource group ("" when it requires more).
func azByName(args []azArg) (flag string, group bool) {
	for _, a := range args {
		switch {
		case !a.Required:
		case a.Dest == "resource_group_name":
			group = true
		case flag == "" && a.Option != "":
			flag = a.Option
		default:
			return "", false
		}
	}
	return flag, group
}

// azFree is whether a command runs with no arguments.
func azFree(args []azArg) bool {
	return !slices.ContainsFunc(args, func(a azArg) bool { return a.Required })
}

// azByID is whether a resource id (--ids) gives a command everything it
// requires: each required argument is a part of the id.
func azByID(args []azArg) bool {
	ids := false
	for _, a := range args {
		if a.Required && !a.ID {
			return false
		}
		ids = ids || a.ID
	}
	return ids
}
