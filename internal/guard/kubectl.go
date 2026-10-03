package guard

import "strings"

// kubectl flags that take a value, so the value isn't mistaken for a verb.
var kubectlValueFlags = map[string]bool{
	"-n": true, "--namespace": true, "--context": true, "--kubeconfig": true, "--cluster": true,
	"--user": true, "-o": true, "--output": true, "-l": true, "--selector": true, "-f": true,
	"--filename": true, "-c": true, "--container": true, "--field-selector": true, "--sort-by": true,
	"-s": true, "--server": true, "--token": true, "--as": true, "--as-group": true,
	"--request-timeout": true, "--cache-dir": true, "--certificate-authority": true,
	"--client-certificate": true, "--client-key": true, "--tls-server-name": true, "-v": true,
}

var (
	kubectlRead = map[string]bool{
		"get": true, "describe": true, "logs": true, "top": true, "explain": true, "version": true,
		"api-resources": true, "api-versions": true, "cluster-info": true, "diff": true, "events": true,
		"wait": true, "completion": true, "kustomize": true, "plugin": true, "options": true, "help": true,
		"config": true, // edits the local kubeconfig only, like `aws configure`
	}
	kubectlDestructive = map[string]bool{"delete": true, "drain": true}
	kubectlShell       = map[string]bool{"exec": true, "attach": true, "debug": true}
	kubectlTunnel      = map[string]bool{"port-forward": true, "proxy": true}
)

// ClassifyKubectl classifies `kubectl <verb> ...` arguments.
func ClassifyKubectl(args []string) Class {
	pos := positionals(args, kubectlValueFlags)
	if len(pos) == 0 || HasFlag(args, "--help") || HasFlag(args, "-h") || kubectlDryRun(args) {
		return Read
	}
	verb := pos[0]
	switch {
	case kubectlRead[verb]:
		return Read
	case kubectlDestructive[verb]:
		return Destructive
	case kubectlShell[verb]:
		return Shell
	case kubectlTunnel[verb]:
		return Tunnel
	case verb == "replace" && HasFlag(args, "--force"):
		return Destructive // deletes and re-creates
	case verb == "auth" && len(pos) > 1 && (pos[1] == "can-i" || pos[1] == "whoami"):
		return Read
	case verb == "rollout" && len(pos) > 1:
		switch pos[1] {
		case "status", "history":
			return Read
		case "undo":
			return Destructive
		}
	}
	return Write // apply, create, patch, edit, scale, label, rollout restart, unknown verbs...
}

// kubectlDryRun reports --dry-run=client|server (but not --dry-run=none).
func kubectlDryRun(args []string) bool {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--dry-run="); ok {
			return v != "none"
		}
		if a == "--dry-run" {
			return true // old boolean form means client
		}
	}
	return false
}
