package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/ui"
)

type tool struct {
	bin, purpose string
	versionArgs  []string
	needed       func(*config.Config) bool // required by this config; nil = optional
	brew, url    string
}

// tools are every cloud's CLI and plugins (from the provider registry) plus
// optional extras.
func tools() []tool {
	var ts []tool
	for _, c := range provider.Clouds() {
		ts = append(ts, tool{c.CLI, c.Title, c.Tool.VersionArgs, anyContext(func(x *config.Context) bool { return x.Provider == c.Name }), c.Tool.Brew, c.Tool.URL})
	}
	for _, c := range provider.Clouds() {
		for _, p := range c.Plugins {
			uses := func(x *config.Context) bool { return x.Provider == c.Name && p.Needed(x) }
			ts = append(ts, tool{p.Bin, p.Purpose, p.Tool.VersionArgs, anyContext(uses), p.Tool.Brew, p.Tool.URL})
		}
	}
	return append(ts, extraTools...)
}

// anyContext lifts a per-context test to "some context in the config needs it".
func anyContext(f func(*config.Context) bool) func(*config.Config) bool {
	return func(cfg *config.Config) bool {
		return slices.ContainsFunc(slices.Collect(maps.Values(cfg.Contexts)), f)
	}
}

var extraTools = []tool{
	{"kubectl", "Kubernetes CLI (mek kubectl, mek kube --merge)", []string{"version", "--client"},
		anyContext(func(x *config.Context) bool { return len(x.Clusters) > 0 }),
		"brew install kubectl", "https://kubernetes.io/docs/tasks/tools/"},
	{"session-manager-plugin", "AWS SSM tunnels/shell (upcoming `mek tunnel`/`mek shell`)", []string{"--version"}, nil,
		"brew install --cask session-manager-plugin", "https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html"},
	{"k9s", "Kubernetes TUI", []string{"version", "--short"}, nil,
		"brew install k9s", "https://k9scli.io/topics/install/"},
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check config and required CLIs, with install hints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, p := cmd.OutOrStdout(), ui.Out()
			problems := 0

			// Version probes are slow (gcloud alone takes ~1s), so start them all now
			// and print the results in order once the config section is done.
			ts := tools()
			probes := probeTools(ts)

			fmt.Fprintln(out, p.Bold("Config"))
			cfg, err := provider.Load()
			switch {
			case errors.Is(err, config.ErrNoConfig):
				fmt.Fprintf(out, "  %s no config at %s — run `mek init`\n", p.Red("✗"), config.Path())
				problems++
			case err != nil:
				fmt.Fprintf(out, "  %s %v\n", p.Red("✗"), err)
				problems++
			default:
				cur := config.Current()
				if cur == "" {
					cur = p.Dim("(none — run `mek use <context>`)")
				}
				fmt.Fprintf(out, "  %s %s — %d contexts, current: %s\n", p.Green("✓"), config.Path(), len(cfg.Contexts), cur)
			}

			fmt.Fprintln(out, p.Bold("\nTools"))
			for i, t := range ts {
				pr := <-probes[i]
				if pr.path == "" {
					mark, label := p.Dim("–"), "optional"
					if cfg != nil && t.needed != nil && t.needed(cfg) {
						mark, label = p.Red("✗"), "required"
						problems++
					}
					fmt.Fprintf(out, "  %s %-24s not found (%s: %s)\n      install: %s\n", mark, t.bin, label, t.purpose, hint(p, t))
					continue
				}
				fmt.Fprintf(out, "  %s %-24s %s %s\n", p.Green("✓"), t.bin, pr.version, p.Dim(pr.path))
			}

			if problems > 0 {
				return fmt.Errorf("%d problem(s) found", problems)
			}
			fmt.Fprintf(out, "\n%s all good\n", p.Green("✓"))
			return nil
		},
	}
}

type probe struct{ path, version string } // path "" = not installed

// probeTools looks up every tool and its version concurrently; receive from
// the i-th channel to get ts[i]'s result, so callers can print in order.
func probeTools(ts []tool) []chan probe {
	res := make([]chan probe, len(ts))
	for i, t := range ts {
		ch := make(chan probe, 1) // buffered: never blocks, even if unread
		res[i] = ch
		go func() {
			path, err := exec.LookPath(t.bin)
			if err != nil {
				ch <- probe{}
				return
			}
			ch <- probe{path: path, version: firstLine(path, t.versionArgs)}
		}()
	}
	return res
}

var goos = runtime.GOOS // test seam: install hints differ on macOS

func hint(p ui.Palette, t tool) string {
	if goos == "darwin" && t.brew != "" {
		return t.brew + "  " + p.Dim("("+t.url+")")
	}
	return t.url
}

func firstLine(path string, args []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil && len(b) == 0 {
		return "(version unknown)"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if r := []rune(line); len(r) > 60 { // cut on a character, not mid-UTF-8
		line = string(r[:60]) + "…"
	}
	return line
}
