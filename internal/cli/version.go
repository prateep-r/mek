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

func newSelfUpdateCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update mek to the latest release (installs via install.sh / manual download)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			latest, err := selfupdate.Latest(version.Repo)
			if err != nil {
				return err
			}
			if latest == version.Version || "v"+version.Version == latest {
				ui.Info("%s mek %s is the latest version", ui.Green("✓"), version.Version)
				return nil
			}
			ui.Info("current: %s  latest: %s", version.Version, ui.Bold(latest))
			if check {
				return nil
			}
			exe, err := selfupdate.Update(version.Repo, latest)
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
