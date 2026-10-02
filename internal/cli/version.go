package cli

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/selfupdate"
	"github.com/prateep-r/mek/internal/ui"
	"github.com/prateep-r/mek/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print mek's version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "mek %s (commit %s, built %s, %s %s/%s)\n",
				version.Version, version.Commit, version.Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		},
	}
}

// Self-update steps, swapped out in tests.
var (
	latestRelease = selfupdate.Latest
	applyUpdate   = selfupdate.Update
)

func newSelfUpdateCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update mek to the latest release (installs via install.sh / manual download)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			latest, err := latestRelease(version.Repo)
			if err != nil {
				return err
			}
			if !selfupdate.Newer(version.Version, latest) {
				ui.Info("%s mek %s is up to date (latest release: %s)", ui.Green("✓"), version.Version, latest)
				return nil
			}
			ui.Info("current: %s  latest: %s", version.Version, ui.Bold(latest))
			if check {
				return nil
			}
			exe, err := applyUpdate(version.Repo, latest)
			if errors.Is(err, selfupdate.ErrHomebrew) {
				ui.Info("%s %v", ui.Yellow("→"), err)
				return nil
			}
			if err != nil {
				return err
			}
			ui.Info("%s updated %s to %s", ui.Green("✓"), exe, latest)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only check whether an update is available")
	return cmd
}
