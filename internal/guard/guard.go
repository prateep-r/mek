// Package guard classifies cloud CLI commands as read / write / destructive
// and decides what a context's safety settings allow.
//
// Classification is a heuristic based on operation names. It is a seatbelt
// against mistakes, NOT a security boundary — real enforcement belongs in
// IAM / SCP / org policies.
package guard

import (
	"slices"
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
	// Read-looking prefixes that act: test-invoke-method runs the backend.
	awsNotRead             = []string{"test-invoke"}
	awsDestructivePrefixes = []string{
		"delete", "terminate", "remove", "purge", "deregister", "revoke", "detach",
		"disassociate", "cancel", "stop", "reset", "reboot", "disable", "abort",
		"release", "deactivate", "unassign", "failover", "batch-delete", "drop",
		"schedule-key-deletion",
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

// beforeDashDash is args up to a bare "--": after it, the arguments are a
// command's own (kubectl exec … -- df -h).
func beforeDashDash(args []string) []string {
	for i, a := range args {
		if a == "--" {
			return args[:i]
		}
	}
	return args
}

// lastBool is the last of --flag / --no-flag in args (the CLI's own rule:
// the last one wins); false when neither is there.
func lastBool(args []string, flag string) bool {
	on := false
	for _, a := range args {
		switch a {
		case "--" + flag:
			on = true
		case "--no-" + flag:
			on = false
		}
	}
	return on
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

// FlagValue returns the value of `--flag value` or `--flag=value` ("" if
// absent); given twice, the last, as the CLIs take it.
func FlagValue(args []string, flag string) string {
	v := ""
	for i, a := range args {
		if after, ok := strings.CutPrefix(a, flag+"="); ok {
			v = after
		} else if a == flag && i+1 < len(args) {
			v = args[i+1]
		}
	}
	return v
}

// hasPrefixOf is hasPrefix with its arguments the other way round, for
// slices.Contains's shape.
func hasPrefixOf(prefixes []string, s string) bool { return hasPrefix(s, prefixes) }

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
		case "rm", "rb", "mv": // mv deletes the source
			return Destructive
		case "sync":
			if HasFlag(args, "--delete") {
				return Destructive
			}
		}
		return Write
	}
	if lastBool(args, "dry-run") {
		return Read
	}
	if svc == "ssm" && op == "start-session" { // the session itself, not an API change
		if strings.HasPrefix(FlagValue(args, "--document-name"), "AWS-StartPortForwardingSession") {
			return Tunnel
		}
		return Shell // a custom document may do anything a shell can
	}
	switch {
	case hasPrefix(op, awsNotRead):
		return Write
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
	// ls/cat/du/hash, rm and mv are `gcloud storage` verbs; access reads a
	// secret version; seek rewinds a subscription (acks are lost). Verbs
	// that only look unknown are listed too, so that a name after them
	// (scheduler jobs run list-users) is not taken for the verb.
	gcloudReadVerbs        = []string{"list", "describe", "get", "read", "tail", "print", "show", "search", "lookup", "help", "test-iam-permissions", "validate", "check", "wait", "ls", "cat", "du", "hash", "access"}
	gcloudDestructiveVerbs = []string{"delete", "remove", "destroy", "purge", "stop", "reset", "abandon", "cancel", "disable", "revoke", "detach", "suspend", "failover", "rm", "mv", "seek"}
	gcloudWriteVerbs       = []string{"create", "update", "set", "add", "deploy", "apply", "patch", "start", "resume", "resize", "import", "export", "enable", "attach", "scp", "restart", "restore", "rollback", "promote", "move", "copy", "upload", "submit", "execute", "replace", "undelete", "reboot", "scale", "migrate", "pull", "publish", "ack", "modify-ack-deadline", "run"}
	// Verbs that open a session: compute ssh, cloud-shell ssh, sql connect
	// (a database shell), compute connect-to-serial-port (a console).
	gcloudShellVerbs = map[string]bool{"ssh": true, "connect": true, "connect-to-serial-port": true}
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
	if rest := pos[start:]; len(rest) >= 2 && rest[0] == "compute" && rest[1] == "start-iap-tunnel" {
		return Tunnel
	}
	for _, p := range pos[start+1:] {
		if gcloudShellVerbs[p] {
			return Shell
		}
		if hasPrefix(p, gcloudReadVerbs) || hasPrefix(p, gcloudWriteVerbs) || hasPrefix(p, gcloudDestructiveVerbs) {
			break // the verb comes first: what follows it are names
		}
	}
	return classifyVerbs(pos[start+1:], gcloudReadVerbs, gcloudWriteVerbs, gcloudDestructiveVerbs)
}

// ---------- az (Azure) ----------

// az's global flags that take a value, for flags put before the command.
var azValueFlags = map[string]bool{
	"--subscription": true, "--output": true, "-o": true, "--query": true,
}

var (
	// Groups that only touch az's own files: its login, config and extensions.
	// (account and bicep are not: account set changes the subscription the
	// context's commands act on, account lock and bicep publish the cloud.)
	azLocalGroups = map[string]bool{
		"login": true, "logout": true, "config": true, "configure": true,
		"extension": true, "version": true, "upgrade": true, "interactive": true, "find": true,
		"init": true, "feedback": true, "cloud": true, "self-test": true,
	}
	azReadVerbs        = []string{"list", "show", "get", "exists", "check", "wait", "query", "tail", "download", "browse"}
	azDestructiveVerbs = []string{"delete", "remove", "purge", "deallocate", "stop", "reset", "revoke", "detach", "disable", "cancel", "abort", "failover", "regenerate", "reimage", "unregister", "flush", "generalize"}
	azWriteVerbs       = []string{"create", "update", "set", "add", "start", "restart", "deploy", "apply", "import", "enable", "attach", "assign", "scale", "resize", "upgrade", "invoke", "run", "swap", "move", "copy", "upload", "patch", "restore", "approve", "grant", "register", "rotate"}
)

// ClassifyAzure classifies `az <group>... <command> ...` arguments. az takes
// no positional arguments: the words before the first flag are the command,
// and its last word is the verb (monitor log-analytics query-pack delete).
func ClassifyAzure(args []string) Class {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") { // az -o table vm list
		if azValueFlags[args[0]] && len(args) > 1 {
			args = args[1:]
		}
		args = args[1:]
	}
	path := args
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			path = args[:i]
			break
		}
	}
	// -h right after the command is help; later, it may be a flag's value.
	help := len(path) < len(args) && (args[len(path)] == "-h" || args[len(path)] == "--help")
	if len(path) < 2 || help || azLocalGroups[path[0]] {
		return Read // a bare group prints its commands
	}
	verb := path[len(path)-1]
	switch {
	case path[0] == "network" && path[1] == "bastion" && (verb == "ssh" || verb == "rdp"):
		return Shell
	case path[0] == "network" && path[1] == "bastion" && verb == "tunnel":
		return Tunnel
	case path[0] == "bicep" && verb != "publish", path[0] == "account" && verb == "clear":
		return Read // builds, formats, the CLI's own install; az's token cache
	}
	return classifyVerbs([]string{verb}, azReadVerbs, azWriteVerbs, azDestructiveVerbs)
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
	hcloudDestructiveOps = map[string]bool{"Delete": true, "Remove": true, "Stop": true, "Reboot": true, "Reset": true, "Detach": true, "Disassociate": true, "Unbind": true, "Revoke": true, "Cancel": true, "Disable": true, "Terminate": true, "Release": true, "Purge": true, "Uninstall": true, "Abort": true, "Deregister": true, "Unregister": true, "Unsubscribe": true, "Shutdown": true, "Reinstall": true}
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
		HasFlag(args, "--dryrun") || HasFlag(args, "--help") || HasFlag(args, "--skeleton") {
		return Read
	}
	if pos[0] == "obs" {
		switch pos[1] {
		case "ls", "stat":
			return Read
		case "rm", "rb", "abort": // abort drops the parts uploaded so far
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

// classifyVerbs lets the first known verb after the top-level group decide,
// for CLIs shaped like `<cli> <group> [<subgroup>...] <verb>`: first a word
// that is a verb (os-login ssh-keys remove: remove), then one that starts
// like one (get-iam-policy).
func classifyVerbs(rest []string, read, write, destructive []string) Class {
	for _, match := range []func([]string, string) bool{slices.Contains[[]string], hasPrefixOf} {
		for _, p := range rest {
			switch {
			case match(destructive, p):
				return Destructive
			case match(read, p):
				return Read
			case match(write, p):
				return Write
			}
		}
	}
	if len(rest) == 0 {
		return Read // bare group prints usage
	}
	return Write // unknown verb: be conservative
}
