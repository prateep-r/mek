package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/ui"
)

type tool struct {
	bin, purpose string
	versionArgs  []string
	provider     string // required when a context of this provider exists; "" = optional
	brew, url    string
}

var tools = []tool{
	{"aws", "AWS CLI v2", []string{"--version"}, config.ProviderAWS,
		"brew install awscli", "https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"},
	{"session-manager-plugin", "AWS SSM tunnels/shell (upcoming `mek tunnel`/`mek shell`)", []string{"--version"}, "",
		"brew install --cask session-manager-plugin", "https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html"},
	{"gcloud", "Google Cloud CLI", []string{"--version"}, config.ProviderGCP,
		"", "https://cloud.google.com/sdk/docs/install"},
	{"kubectl", "Kubernetes CLI", []string{"version", "--client"}, "",
		"brew install kubectl", "https://kubernetes.io/docs/tasks/tools/"},
	{"k9s", "Kubernetes TUI", []string{"version", "--short"}, "",
		"brew install k9s", "https://k9scli.io/topics/install/"},
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check config and required CLIs, with install hints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			problems := 0

			fmt.Fprintln(out, ui.Bold("Config"))
			needed := map[string]bool{}
			cfg, err := config.Load()
			switch {
			case errors.Is(err, config.ErrNoConfig):
				fmt.Fprintf(out, "  %s no config at %s — run `mek init`\n", ui.Red("✗"), config.Path())
				problems++
			case err != nil:
				fmt.Fprintf(out, "  %s %v\n", ui.Red("✗"), err)
				problems++
			default:
				for _, c := range cfg.Contexts {
					needed[c.Provider] = true
				}
				cur := config.Current()
				if cur == "" {
					cur = ui.Dim("(none — run `mek use <context>`)")
				}
				fmt.Fprintf(out, "  %s %s — %d contexts, current: %s\n", ui.Green("✓"), config.Path(), len(cfg.Contexts), cur)
			}

			fmt.Fprintln(out, ui.Bold("\nTools"))
			for _, t := range tools {
				required := t.provider != "" && needed[t.provider]
				path, err := exec.LookPath(t.bin)
				if err != nil {
					mark, label := ui.Dim("–"), "optional"
					if required {
						mark, label = ui.Red("✗"), "required"
						problems++
					}
					fmt.Fprintf(out, "  %s %-24s not found (%s: %s)\n      install: %s\n", mark, t.bin, label, t.purpose, hint(t))
					continue
				}
				fmt.Fprintf(out, "  %s %-24s %s %s\n", ui.Green("✓"), t.bin, firstLine(path, t.versionArgs), ui.Dim(path))
			}

			if problems > 0 {
				return fmt.Errorf("%d problem(s) found", problems)
			}
			fmt.Fprintf(out, "\n%s all good\n", ui.Green("✓"))
			return nil
		},
	}
}

func hint(t tool) string {
	if runtime.GOOS == "darwin" && t.brew != "" {
		return t.brew + "  " + ui.Dim("("+t.url+")")
	}
	return t.url
}

func firstLine(path string, args []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil && len(b) == 0 {
		return ui.Dim("(version unknown)")
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if len(line) > 60 {
		line = line[:60] + "…"
	}
	return line
}
