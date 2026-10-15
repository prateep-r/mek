package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/ui"
)

// saveEdit validates the whole config as it would be after the edit — every
// layer, every cloud's rules — and only then writes the file. A new
// mek.local.yaml goes into the repository's .gitignore.
func saveEdit(e *config.Editor) error {
	cfg, err := config.Preview(e.Path, e.Bytes())
	if err == nil {
		err = provider.Validate(cfg)
	}
	if err != nil {
		return fmt.Errorf("not saved — %s would be invalid: %w", config.Show(e.Path), err)
	}
	isNew := e.IsNew()
	if err := e.Save(); err != nil {
		return err
	}
	if isNew && filepath.Base(e.Path) == config.LocalFile {
		ignore, err := config.IgnoreLocal(filepath.Dir(e.Path))
		if err != nil {
			return err
		}
		if ignore != "" {
			ui.Info("%s added %s to %s", ui.Green("✓"), config.LocalFile, config.Show(ignore))
		}
	}
	return nil
}

// editConfig opens a config file (a missing one starts from template),
// changes it and saves it through saveEdit.
func editConfig(path string, template []byte, change func(*config.Editor) error) error {
	perm := os.FileMode(0o600)
	if filepath.Base(path) == config.TeamFile || filepath.Base(path) == config.LocalFile {
		perm = 0o644 // a project's files are for a repository
	}
	e, err := config.Open(path, template, perm)
	if err == nil {
		err = change(e)
	}
	if err != nil {
		return err
	}
	return saveEdit(e)
}

func (a *app) newCtxRmCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm <context>",
		Short: "Remove a context from the file it is defined in",
		Long: `Remove a context from the file it comes from — ~/.mek/config.yaml, or the
project's mek.yaml or mek.local.yaml — leaving the rest of that file, comments
included, as it was. It asks first; --yes doesn't (for scripts).

When the context replaced one of the same name in a lower file, that one is
used from then on, and mek says which.

  mek ctx rm old-sandbox
  mek ctx rm orders-dev          # your copy in mek.local.yaml: the team's is used again
  mek ctx rm staging --yes`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeContexts,
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := provider.Load()
			if err != nil {
				return err
			}
			ctx, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			if !yes {
				ok, err := confirm(fmt.Sprintf("Remove context %s from %s?", ctx.Name, config.Show(ctx.Source)))
				if errors.Is(err, ui.ErrNoTTY) {
					return errors.New("no terminal to ask on — pass --yes to remove it")
				}
				if err != nil || !ok {
					return err
				}
			}
			if err := editConfig(ctx.Source, nil, func(e *config.Editor) error { return e.Delete(ctx.Name) }); err != nil {
				return err
			}
			ui.Info("%s removed %s from %s", ui.Green("✓"), ctx.Name, config.Show(ctx.Source))
			if ctx.Shadowed != "" {
				ui.Info("  %s from %s is used again", ctx.Name, config.Show(ctx.Shadowed))
			} else if config.Current() == ctx.Name {
				ui.Info("  it was the current context: mek use <context>")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "don't ask")
	return cmd
}
