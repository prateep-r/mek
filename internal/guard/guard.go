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
	Shell   // interactive session on a host (ssm start-session, gcloud compute ssh, kubectl exec)
	Tunnel  // port forwarding (kubectl port-forward); only moves bytes
)

func (c Class) String() string {
	return [...]string{"read", "write", "destructive", "unknown", "shell", "tunnel"}[c]
}

type Decision int

const (
	Allow        Decision = iota
	Confirm               // ask y/N
	ConfirmTyped          // require typing the context name
	Block
)

// Decide applies a context's safety settings to a command class.
//
// Sessions: a shell is blocked on readonly contexts and confirmed on protected
// ones; a tunnel is allowed on readonly contexts (what runs over it is up to
// the remote service's own permissions) and confirmed on protected ones.
func Decide(ctx *config.Context, c Class) Decision {
	if c == Read {
		return Allow
	}
	if c == Tunnel {
		if ctx.Protected {
			return Confirm
		}
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
	return Confirm // write, unknown, shell
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

// HasFlag reports whether args contain flag, as `--flag` or `--flag=value`.
func HasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// FlagValue returns the value of `--flag value` or `--flag=value` ("" if absent).
func FlagValue(args []string, flag string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
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
		if HasFlag(args, "--dryrun") {
			return Read
		}
		switch op {
		case "ls", "presign":
			return Read
		case "rm", "rb":
			return Destructive
		case "sync":
			if HasFlag(args, "--delete") {
				return Destructive
			}
		}
		return Write
	}
	if HasFlag(args, "--dry-run") {
		return Read
	}
	if svc == "ssm" && op == "start-session" { // the session itself, not an API change
		if strings.Contains(FlagValue(args, "--document-name"), "PortForwarding") {
			return Tunnel
		}
		return Shell
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
	// ls/cat/du/hash and rm are `gcloud storage` verbs; access reads a secret version.
	gcloudReadVerbs        = []string{"list", "describe", "get", "read", "tail", "print", "show", "search", "lookup", "help", "test", "validate", "check", "wait", "ls", "cat", "du", "hash", "access"}
	gcloudDestructiveVerbs = []string{"delete", "remove", "destroy", "purge", "stop", "reset", "abandon", "cancel", "disable", "revoke", "detach", "suspend", "failover", "rm"}
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
	if pos[start] == "storage" && HasFlag(args, "--delete-unmatched-destination-objects") {
		return Destructive // storage rsync that deletes extra destination objects
	}
	if rest := pos[start:]; len(rest) >= 2 && rest[0] == "compute" {
		switch rest[1] {
		case "ssh":
			return Shell
		case "start-iap-tunnel":
			return Tunnel
		}
	}
	return classifyVerbs(pos[start+1:], gcloudReadVerbs, gcloudWriteVerbs, gcloudDestructiveVerbs)
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
