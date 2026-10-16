// Package audit appends one JSON line per command mek runs, to
// ~/.mek/audit.jsonl. Secret-looking flag values are masked.
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/proc"
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
	// Sessions get a "start" entry when they begin and an "end" entry with
	// the exit code and duration; both carry the same session id.
	Event   string `json:"event,omitempty"`
	Session string `json:"session,omitempty"`
	Target  string `json:"target,omitempty"`
	// Config is the project file a context came from (mek.yaml or
	// mek.local.yaml); empty for ~/.mek/config.yaml.
	Config string `json:"config,omitempty"`
}

// entry is the audit record of an invocation.
func entry(inv *runner.Invocation, start time.Time) Entry {
	return Entry{
		Time: start, Context: inv.Context.Name, Provider: inv.Context.Provider,
		Command: inv.Argv, Class: inv.Class.String(), Decision: inv.Decision,
		ExitCode: inv.ExitCode, DurationMS: inv.Duration.Milliseconds(),
		Session: inv.Session, Target: inv.Target, Config: projectFile(inv.Context),
	}
}

func projectFile(c *config.Context) string {
	if c.InProject {
		return c.Source
	}
	return ""
}

// started reports whether the guard let the command run.
func started(inv *runner.Invocation) bool {
	return inv.Decision == "allowed" || inv.Decision == "confirmed"
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
			if inv.Detached {
				return err // its supervisor audits the end
			}
			e := entry(inv, start)
			if inv.Session != "" && started(inv) {
				e.Event = "end"
			}
			if werr := Write(e); werr != nil && onError != nil {
				onError(werr)
			}
			return err
		})
	}
}

// SessionStart is a runner.Decorator that writes a session's "start" entry.
// It goes after the guard, so a blocked or declined session has none; a
// long session is then in the log while it runs, not only once it ends.
func SessionStart(onError func(error)) runner.Decorator {
	return func(next runner.Runner) runner.Runner {
		return runner.Func(func(inv *runner.Invocation) error {
			if inv.Session != "" {
				e := entry(inv, time.Now())
				e.Event, e.ExitCode = "start", 0
				if werr := Write(e); werr != nil && onError != nil {
					onError(werr)
				}
			}
			return next.Run(inv)
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

// Rotation: once audit.jsonl reaches maxSize it becomes audit.jsonl.1, older
// files shift to .2 … .keep, and the oldest is dropped.
var (
	maxSize  int64 = 10 << 20
	keep           = 3
	lockFile       = proc.Lock // test seam
)

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
	rerr := rotate() // a failed rotation must not lose this entry
	f, err := os.OpenFile(Path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.Join(rerr, err)
	}
	defer f.Close()
	return errors.Join(rerr, json.NewEncoder(f).Encode(e)) // one line; an Entry always marshals
}

// rotate moves a full log aside. Concurrent mek processes take a file lock
// and re-check the size, so only one of them rotates.
func rotate() error {
	if fi, err := os.Stat(Path()); err != nil || fi.Size() < maxSize {
		return nil
	}
	lock, err := os.OpenFile(Path()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close() // also releases the lock
	if err := lockFile(lock); err != nil {
		return err
	}
	if fi, err := os.Stat(Path()); err != nil || fi.Size() < maxSize {
		return nil // another mek rotated while we waited
	}
	for i := keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", Path(), i), fmt.Sprintf("%s.%d", Path(), i+1)) // gaps are fine
	}
	return os.Rename(Path(), Path()+".1")
}

var (
	secretFlag = regexp.MustCompile(`(?i)(password|passwd|secret|token|private-key|key-material|credential|plaintext|auth-key|api-key|account-key|connection-string|from-literal|value|cli-input-json|cli-input-yaml)`)
	// A KEY=VALUE argument whose key looks like this (kubectl set env
	// deploy/x DB_PASSWORD=…) has its value masked.
	secretKey = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|credential|private.?key|api.?key|access.?key)`)
	// user:password@ in a URL (a database URL in an argument).
	urlPassword = regexp.MustCompile(`(://[^/@:\s]+):[^/@\s]+@`)
)

// Flags whose value is KEY=VALUE pairs — environments, parameters, app
// settings — where any value may be a secret: the keys stay, the values go.
var pairFlags = map[string]bool{
	"--set-env-vars": true, "--update-env-vars": true, "--environment": true, "--env": true, "-e": true,
	"--parameter-overrides": true, "--parameters": true, "--settings": true, "--set": true,
}

// maskPairs masks the values of a comma-separated KEY=VALUE list.
func maskPairs(v string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		if k, _, ok := strings.Cut(p, "="); ok {
			parts[i] = k + "=****"
		}
	}
	return strings.Join(parts, ",")
}

// Mask hides values of secret-looking flags in both `--flag value` and
// `--flag=value` forms, the values of KEY=VALUE pairs that may be secrets,
// and passwords in URLs. args[0] is the program name.
func Mask(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i < len(out); i++ {
		a := out[i]
		k, v, hasValue := strings.Cut(a, "=")
		switch {
		case !strings.HasPrefix(a, "-"):
			if hasValue && secretKey.MatchString(k) {
				out[i] = k + "=****"
			}
			continue
		case pairFlags[k]:
			if hasValue {
				out[i] = k + "=" + maskPairs(v)
				continue
			}
			for i+1 < len(out) && !strings.HasPrefix(out[i+1], "-") && strings.Contains(out[i+1], "=") {
				i++ // --parameter-overrides A=1 B=2
				out[i] = maskPairs(out[i])
			}
			continue
		case !secretFlag.MatchString(a):
			continue
		case hasValue:
			out[i] = k + "=****"
			continue
		}
		// The next argument is the value unless it is clearly another long
		// flag; "-abc" may be a password, so it is masked too (over-masking a
		// short flag in the log is the safe mistake).
		if i+1 < len(out) && !strings.HasPrefix(out[i+1], "--") {
			out[i+1] = "****"
			i++
		}
	}
	for i := range out {
		out[i] = urlPassword.ReplaceAllString(out[i], "$1:****@")
	}
	return out
}
