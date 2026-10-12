package cli

import (
	"errors"
	"testing"

	"github.com/prateep-r/mek/internal/tui"
)

func TestUI(t *testing.T) {
	oldTerm, oldRun, oldExe := isTerminal, runUI, executable
	t.Cleanup(func() { isTerminal, runUI, executable = oldTerm, oldRun, oldExe })
	var got []string
	runUI = func(src tui.Source, mek, selected string) error {
		got = []string{mek, selected}
		return nil
	}
	executable = func() (string, error) { return "/usr/local/bin/mek", nil }

	isTerminal = func() bool { return false }
	h := newHarness(t, "")
	_, err := h.run("ui")
	wantErr(t, err, "mek ui needs a terminal")

	isTerminal = func() bool { return true }
	_, err = h.run("ui")
	wantErr(t, err, "no config")

	h = newHarness(t, "contexts:\n  dev: {provider: aws, aws_profile: dev}\n")
	_, err = h.run("-c", "nope", "ui")
	wantErr(t, err, `unknown context "nope"`)
	h.mustRun("-c", "dev", "ui")
	if got[0] != "/usr/local/bin/mek" || got[1] != "dev" {
		t.Errorf("ran the UI with %v", got)
	}
	h.mustRun("ui")
	if got[1] != "" {
		t.Errorf("without -c: %v", got)
	}

	executable = func() (string, error) { return "", errors.New("no executable") }
	_, err = h.run("ui")
	wantErr(t, err, "no executable")

	// The real check: a test's stdin is no terminal.
	if oldTerm() {
		t.Skip("stdin is a terminal")
	}
}
