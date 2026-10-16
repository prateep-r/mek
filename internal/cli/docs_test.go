package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/provider"
)

// The README keeps up with the wizard: every command under mek ctx has an
// example, and every lookup's permission and command is listed.
func TestREADMECoversTheWizard(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	ctx, _, err := newRoot(&app{}).Find([]string{"ctx"})
	if err != nil {
		t.Fatal(err)
	}
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		for _, sub := range c.Commands() {
			p := path + " " + sub.Name()
			if sub.Runnable() && !strings.Contains(readme, p) { // "ctx tunnel add", as in "mek -c dev ctx tunnel add"
				t.Errorf("README has no example of mek %s", p)
			}
			walk(sub, p)
		}
	}
	walk(ctx, "ctx")
	for _, d := range provider.Discoveries() {
		perm, _, _ := strings.Cut(d.Permission, " ")
		if !strings.Contains(readme, perm) || !strings.Contains(readme, "`"+strings.Join(d.Argv[:3], " ")) {
			t.Errorf("README's lookup table lacks %s / %v", perm, d.Argv[:3])
		}
	}
	for _, want := range []string{"mek context", "mek help wizard", "~/.aws/sso/cache", "`a` add · `e` edit · `d` remove",
		"## Editing config with the wizard", "--yes"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q", want)
		}
	}
}
