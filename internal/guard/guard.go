// Package guard classifies cloud CLI commands as read / write / destructive
// and decides what a context's safety settings allow.
//
// Classification is a heuristic based on operation names. It is a seatbelt
// against mistakes, NOT a security boundary — real enforcement belongs in
// IAM / SCP / org policies.
package guard

import (
	"strings"

	"github.com/prateep-r/mek/internal/config"
)

type Class int

const (
	Read Class = iota
	Write
	Destructive
	Unknown // cannot be classified (e.g. arbitrary `mek exec` commands)
)

func (c Class) String() string {
	return [...]string{"read", "write", "destructive", "unknown"}[c]
}

type Decision int

const (
	Allow        Decision = iota
	Confirm               // ask y/N
	ConfirmTyped          // require typing the context name
	Block
)

// Decide applies a context's safety settings to a command class.
func Decide(ctx *config.Context, c Class) Decision {
	if c == Read {
		return Allow
	}
	if ctx.ReadOnly {
		return Block
	}
	if !ctx.Protected {
		return Allow
	}
	if c == Destructive {
		return ConfirmTyped
	}
	return Confirm
}

// ---------- AWS ----------

// Global aws options that take a value (so the value isn't mistaken for a positional).
var awsValueFlags = map[string]bool{
	"--region": true, "--profile": true, "--output": true, "--endpoint-url": true,
	"--query": true, "--cli-read-timeout": true, "--cli-connect-timeout": true,
	"--color": true, "--ca-bundle": true, "--cli-binary-format": true,
}

var (
	awsReadPrefixes = []string{
		"describe", "list", "get", "head", "search", "lookup", "query", "scan",
		"select", "batch-get", "filter", "preview", "estimate", "validate",
		"simulate", "test", "check", "detect", "view", "tail", "wait", "help",
	}
	awsDestructivePrefixes = []string{
		"delete", "terminate", "remove", "purge", "deregister", "revoke", "detach",
		"disassociate", "cancel", "stop", "reset", "reboot", "disable", "abort",
		"release", "deactivate", "unassign", "failover", "batch-delete", "drop",
	}
	// Services that only touch local state.
	awsLocalServices = map[string]bool{"configure": true, "sso": true, "help": true, "history": true}
)

func positionals(args []string, valueFlags map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			if valueFlags[a] && !strings.Contains(a, "=") {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if s == p || strings.HasPrefix(s, p+"-") {
			return true
		}
	}
	return false
}

// ClassifyAWS classifies `aws <service> <operation> ...` arguments.
func ClassifyAWS(args []string) Class {
	pos := positionals(args, awsValueFlags)
	if len(pos) == 0 || awsLocalServices[pos[0]] {
		return Read
	}
	if len(pos) < 2 {
		return Read // e.g. `aws ec2` prints usage
	}
	svc, op := pos[0], pos[1]
	if svc == "s3" { // high-level s3 commands
		switch op {
		case "ls", "presign":
			return Read
		case "rm", "rb":
			return Destructive
		case "sync":
			if hasFlag(args, "--delete") {
				return Destructive
			}
		}
		return Write
	}
	if hasFlag(args, "--dry-run") {
		return Read
	}
	switch {
	case hasPrefix(op, awsReadPrefixes):
		return Read
	case hasPrefix(op, awsDestructivePrefixes):
		return Destructive
	}
	return Write
}

// ---------- gcloud ----------

var gcloudValueFlags = map[string]bool{
	"--project": true, "--account": true, "--configuration": true, "--format": true,
	"--filter": true, "--region": true, "--zone": true, "--verbosity": true,
	"--impersonate-service-account": true, "--billing-project": true, "--limit": true,
	"--sort-by": true, "--page-size": true, "--flags-file": true, "--trace-token": true,
}

var (
	gcloudLocalGroups = map[string]bool{
		"config": true, "auth": true, "help": true, "info": true, "version": true,
		"components": true, "topic": true, "init": true, "cheat-sheet": true, "feedback": true,
	}
	gcloudReadVerbs        = []string{"list", "describe", "get", "read", "tail", "print", "show", "search", "lookup", "help", "test", "validate", "check", "wait"}
	gcloudDestructiveVerbs = []string{"delete", "remove", "destroy", "purge", "stop", "reset", "abandon", "cancel", "disable", "revoke", "detach", "suspend", "failover"}
	gcloudWriteVerbs       = []string{"create", "update", "set", "add", "deploy", "apply", "patch", "start", "resume", "resize", "import", "export", "enable", "attach", "ssh", "scp", "restart", "restore", "rollback", "promote", "move", "copy", "upload", "submit", "execute", "replace", "undelete", "reboot", "scale", "migrate"}
)

// ClassifyGCloud classifies `gcloud <group>... <verb> ...` arguments.
func ClassifyGCloud(args []string) Class {
	pos := positionals(args, gcloudValueFlags)
	if len(pos) == 0 {
		return Read
	}
	start := 0
	if pos[0] == "alpha" || pos[0] == "beta" {
		start = 1
	}
	if len(pos) <= start || gcloudLocalGroups[pos[start]] {
		return Read
	}
	return classifyVerbs(pos[start+1:], gcloudReadVerbs, gcloudWriteVerbs, gcloudDestructiveVerbs)
}

// ---------- az (Azure) ----------

var azValueFlags = map[string]bool{
	"--subscription": true, "--resource-group": true, "-g": true, "--output": true, "-o": true,
	"--query": true, "--name": true, "-n": true, "--location": true, "-l": true,
}

var (
	azLocalGroups = map[string]bool{
		"login": true, "logout": true, "account": true, "config": true, "configure": true,
		"extension": true, "version": true, "upgrade": true, "interactive": true, "find": true,
		"init": true, "feedback": true, "cloud": true, "self-test": true, "bicep": true,
	}
	azReadVerbs        = []string{"list", "show", "get", "exists", "check", "wait", "query", "tail", "download", "browse"}
	azDestructiveVerbs = []string{"delete", "remove", "purge", "deallocate", "stop", "reset", "revoke", "detach", "disable", "cancel", "abort", "failover", "regenerate"}
	azWriteVerbs       = []string{"create", "update", "set", "add", "start", "restart", "deploy", "apply", "import", "enable", "attach", "assign", "scale", "resize", "upgrade", "invoke", "run", "swap", "move", "copy", "upload", "patch", "restore", "approve", "grant", "register", "rotate"}
)

// ClassifyAzure classifies `az <group>... <command> ...` arguments.
func ClassifyAzure(args []string) Class {
	pos := positionals(args, azValueFlags)
	if len(pos) == 0 || azLocalGroups[pos[0]] || hasFlag(args, "--help") || hasFlag(args, "-h") {
		return Read
	}
	return classifyVerbs(pos[1:], azReadVerbs, azWriteVerbs, azDestructiveVerbs)
}

// classifyVerbs lets the first known verb after the top-level group decide,
// for CLIs shaped like `<cli> <group> [<subgroup>...] <verb>`.
func classifyVerbs(rest []string, read, write, destructive []string) Class {
	for _, p := range rest {
		switch {
		case hasPrefix(p, destructive):
			return Destructive
		case hasPrefix(p, read):
			return Read
		case hasPrefix(p, write):
			return Write
		}
	}
	if len(rest) == 0 {
		return Read // bare group prints usage
	}
	return Write // unknown verb: be conservative
}

// ---------- hcloud (Huawei Cloud KooCLI) ----------

var hcloudValueFlags = map[string]bool{
	"--cli-profile": true, "--cli-region": true, "--cli-output": true, "--cli-query": true,
	"--cli-jsonInput": true, "--cli-mode": true, "--cli-endpoint": true,
}

var (
	hcloudLocalCommands = map[string]bool{
		"configure": true, "version": true, "update": true, "meta": true,
		"log": true, "auto-complete": true, "help": true,
	}
	// First word of an API operation name, e.g. "List" in ListServersDetails.
	hcloudReadOps        = map[string]bool{"List": true, "Show": true, "Get": true, "Check": true, "Count": true, "Query": true, "Search": true, "Describe": true, "Validate": true, "Preview": true, "Estimate": true}
	hcloudDestructiveOps = map[string]bool{"Delete": true, "Remove": true, "Stop": true, "Reboot": true, "Reset": true, "Detach": true, "Disassociate": true, "Unbind": true, "Revoke": true, "Cancel": true, "Disable": true, "Terminate": true, "Release": true, "Purge": true, "Uninstall": true, "Abort": true, "Deregister": true, "Unregister": true, "Unsubscribe": true, "Shutdown": true}
	// Prefixes that come before the verb (OpenStack-compatible APIs, batch variants).
	hcloudOpPrefixes = map[string]bool{"Batch": true, "Nova": true, "Keystone": true, "Neutron": true, "Cinder": true}
)

// IsHcloudAPICommand reports whether hcloud args call a cloud API (as opposed
// to one of KooCLI's own commands like configure or version, or bare help).
func IsHcloudAPICommand(args []string) bool {
	pos := positionals(args, hcloudValueFlags)
	return len(pos) > 0 && !hcloudLocalCommands[pos[0]]
}

// ClassifyHuawei classifies `hcloud <Service> <Operation> ...` arguments and
// the bundled obsutil (`hcloud obs <command> ...`).
func ClassifyHuawei(args []string) Class {
	pos := positionals(args, hcloudValueFlags)
	if len(pos) < 2 || hcloudLocalCommands[pos[0]] ||
		hasFlag(args, "--dryrun") || hasFlag(args, "--help") || hasFlag(args, "--skeleton") {
		return Read
	}
	if pos[0] == "obs" {
		switch pos[1] {
		case "ls", "stat":
			return Read
		case "rm", "rb":
			return Destructive
		}
		return Write
	}
	words := pascalWords(pos[1])
	for len(words) > 1 && hcloudOpPrefixes[words[0]] {
		words = words[1:]
	}
	switch {
	case len(words) == 0:
		return Write
	case hcloudReadOps[words[0]]:
		return Read
	case hcloudDestructiveOps[words[0]]:
		return Destructive
	}
	return Write
}

// pascalWords splits "BatchDeleteServers" into [Batch Delete Servers].
func pascalWords(s string) []string {
	var words []string
	start := 0
	for i := 1; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			words = append(words, s[start:i])
			start = i
		}
	}
	if s != "" {
		words = append(words, s[start:])
	}
	return words
}
