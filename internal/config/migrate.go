package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/prateep-r/mek/internal/proc"
)

// oldDir is set while the old directory can't move yet (tunnels run from it).
var oldDir string

// Test seams.
var (
	rename  = os.Rename
	symlink = os.Symlink
	goos    = runtime.GOOS
)

// oldDirs are where mek kept everything before ~/.mek.
func oldDirs(home string) []string {
	dirs := []string{filepath.Join(home, ".config", "mek")}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.Join(x, "mek") != dirs[0] {
		dirs = append([]string{filepath.Join(x, "mek")}, dirs...)
	}
	return dirs
}

// Migrate moves the old ~/.config/mek (or $XDG_CONFIG_HOME/mek) to ~/.mek
// the first time a new mek runs, leaving a symlink at the old path on Unix so
// paths that point there (KUBECONFIG in a shell profile) keep working. It
// tells w what it did. With $MEK_HOME set, it does nothing.
func Migrate(w io.Writer) error {
	home, err := os.UserHomeDir()
	if os.Getenv("MEK_HOME") != "" || err != nil {
		return nil
	}
	dst := defaultDir()
	_, err = os.Lstat(dst)
	for _, src := range oldDirs(home) {
		fi, lerr := os.Lstat(src)
		switch {
		case lerr != nil || !fi.IsDir(): // nothing there, or our symlink
		case err == nil:
			fmt.Fprintf(w, "⚠ mek uses %s; %s is left over and ignored — remove it once you've checked it\n", dst, src)
		case tunnelsRunning(src):
			oldDir = src
			fmt.Fprintf(w, "⚠ mek moves to %s, but tunnels are running from %s — stop them (mek tunnel stop --all) and it moves by itself\n", dst, src)
			return nil
		default:
			return move(w, src, dst)
		}
	}
	return nil
}

func move(w io.Writer, src, dst string) error {
	if err := rename(src, dst); err != nil {
		return fmt.Errorf("can't move %s to %s (%w) — move it yourself, then run mek again", src, dst, err)
	}
	fmt.Fprintf(w, "✓ mek moved %s to %s\n", src, dst)
	if goos == "windows" {
		fmt.Fprintf(w, "  paths that pointed into %s (KUBECONFIG, K9s) now need %s\n", src, dst)
		return nil
	}
	if err := symlink(dst, src); err != nil {
		fmt.Fprintf(w, "  couldn't leave a link at %s (%v): update paths that pointed there\n", src, err)
	}
	return nil
}

// tunnelsRunning reports whether a tunnel's owner still holds its lock in dir.
func tunnelsRunning(dir string) bool {
	locks, _ := filepath.Glob(filepath.Join(dir, "tunnels", "*.lock")) // the pattern is valid
	for _, p := range locks {
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			continue
		}
		err = proc.TryLock(f)
		f.Close()
		if errors.Is(err, proc.ErrLocked) {
			return true
		}
	}
	return false
}
