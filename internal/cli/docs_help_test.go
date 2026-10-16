package cli

import (
	"os"
	"strings"
	"testing"
)

// The README shows mek help as it is, and mek alone shows the same.
func TestREADMEShowsMekHelp(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(b), "```console\n$ mek help\n")
	if !ok {
		t.Fatal("README has no mek help example")
	}
	shown, _, _ := strings.Cut(rest, "```")
	h := newHarness(t, "")
	if got := h.mustRun("help"); got != shown {
		t.Errorf("README's mek help is out of date; it prints:\n%s", got)
	}
	root := newRoot(&app{exec: h.exec})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{}) // not nil: cobra would read the test binary's own arguments
	if err := root.Execute(); err != nil || out.String() != shown {
		t.Errorf("mek alone differs from mek help (%v):\n%s", err, out.String())
	}
}
