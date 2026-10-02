// Package audit appends one JSON line per command mek runs, to
// ~/.config/mek/audit.jsonl. Secret-looking flag values are masked.
package audit

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/runner"
)

type Entry struct {
	Time       time.Time `json:"time"`
	User       string    `json:"user"`
	Context    string    `json:"context"`
	Provider   string    `json:"provider"`
	Command    []string  `json:"command"`
	Class      string    `json:"class"`
	Decision   string    `json:"decision"` // allowed | confirmed | blocked | declined
	ExitCode   int       `json:"exit_code"`
	DurationMS int64     `json:"duration_ms"`
}

func Path() string { return filepath.Join(config.Dir(), "audit.jsonl") }

// Recorder is a runner.Decorator that writes one entry per invocation after
// the rest of the chain ran — including commands the guard blocked. A failed
// write is reported to onError but never fails the user's command.
func Recorder(onError func(error)) runner.Decorator {
	return func(next runner.Runner) runner.Runner {
		return runner.Func(func(inv *runner.Invocation) error {
			start := time.Now()
			err := next.Run(inv)
			if werr := Write(Entry{
				Time: start, Context: inv.Context.Name, Provider: inv.Context.Provider,
				Command: inv.Argv, Class: inv.Class.String(), Decision: inv.Decision,
				ExitCode: inv.ExitCode, DurationMS: inv.Duration.Milliseconds(),
			}); werr != nil && onError != nil {
				onError(werr)
			}
			return err
		})
	}
}

// username prefers the environment: on macOS the first user.Current() call
// in a process goes through Directory Services and costs ~1.5–2.5 ms, which
// is most of an audit write.
func username() string {
	for _, k := range []string{"USER", "LOGNAME"} {
		if u := os.Getenv(k); u != "" {
			return u
		}
	}
	u, err := currentUser()
	if err != nil {
		return ""
	}
	return u.Username
}

var currentUser = user.Current // test seam

// Write appends an entry. Errors are returned but callers may ignore them:
// auditing must never break the user's command.
func Write(e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.User = username()
	e.Command = Mask(e.Command)
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(Path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e) // one line; an Entry always marshals
}

var secretFlag = regexp.MustCompile(`(?i)(password|passwd|secret|token|private-key|key-material|credential|plaintext|auth-key|api-key|account-key|connection-string|cli-input-json|cli-input-yaml)`)

// Short flags that carry a secret in one CLI only (in others they mean
// something else): `az login -p <password>`, `hcloud obs config -k <sk> -t <token>`.
var shortSecretFlags = map[string]map[string]bool{
	"az":     {"-p": true},
	"hcloud": {"-k": true, "-t": true},
}

// Mask hides values of secret-looking flags in both `--flag value` and
// `--flag=value` forms. args[0] is the program name.
func Mask(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	var short map[string]bool
	if len(args) > 0 {
		short = shortSecretFlags[filepath.Base(args[0])]
	}
	for i := 0; i < len(out); i++ {
		a := out[i]
		name, _, _ := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "-") || !(secretFlag.MatchString(a) || short[name]) {
			continue
		}
		if k, _, ok := strings.Cut(a, "="); ok {
			out[i] = k + "=****"
			continue
		}
		if i+1 < len(out) && !strings.HasPrefix(out[i+1], "-") {
			out[i+1] = "****"
			i++
		}
	}
	return out
}
