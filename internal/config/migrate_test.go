package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/proc"
)

// migrateHome is a fake home with mek's old directory holding a config.
func migrateHome(t *testing.T) (home, old string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MEK_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Cleanup(func() { oldDir = "" })
	old = filepath.Join(home, ".config", "mek")
	os.MkdirAll(filepath.Join(old, "tunnels"), 0o700)
	os.WriteFile(filepath.Join(old, "config.yaml"), []byte("contexts: {}\n"), 0o600)
	return home, old
}

func TestMigrate(t *testing.T) {
	home, old := migrateHome(t)
	var out bytes.Buffer
	if err := Migrate(&out); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(home, ".mek")
	if b, _ := os.ReadFile(filepath.Join(dst, "config.yaml")); string(b) != "contexts: {}\n" || Dir() != dst {
		t.Errorf("not moved: %q, Dir %s", b, Dir())
	}
	if !strings.Contains(out.String(), "✓ mek moved "+old+" to "+dst) {
		t.Errorf("message: %q", out.String())
	}
	if goos != "windows" { // the old path still leads to the files
		if b, _ := os.ReadFile(filepath.Join(old, "config.yaml")); len(b) == 0 {
			t.Error("no link at the old path")
		}
	}
	out.Reset()
	if err := Migrate(&out); err != nil || out.Len() != 0 { // moved already: quiet
		t.Errorf("second run: %v %q", err, out.String())
	}
}

func TestMigrateNothingToDo(t *testing.T) {
	home, old := migrateHome(t)
	os.RemoveAll(filepath.Dir(old))
	var out bytes.Buffer
	if err := Migrate(&out); err != nil || out.Len() != 0 { // a new user
		t.Errorf("fresh: %v %q", err, out.String())
	}
	t.Setenv("MEK_HOME", filepath.Join(home, "custom"))
	os.MkdirAll(old, 0o700)
	if err := Migrate(&out); err != nil || out.Len() != 0 { // MEK_HOME: never moved
		t.Errorf("MEK_HOME: %v %q", err, out.String())
	}
	t.Setenv("MEK_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if err := Migrate(&out); err != nil { // no home at all
		t.Error(err)
	}
}

func TestMigrateFromXDG(t *testing.T) {
	home, _ := migrateHome(t)
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	os.MkdirAll(filepath.Join(xdg, "mek"), 0o700)
	os.WriteFile(filepath.Join(xdg, "mek", "state.yaml"), []byte("current: dev\n"), 0o600)
	var out bytes.Buffer
	if err := Migrate(&out); err != nil || !strings.Contains(out.String(), filepath.Join(xdg, "mek")) {
		t.Fatalf("%v %q", err, out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".mek", "state.yaml")); string(b) != "current: dev\n" {
		t.Errorf("XDG dir not moved: %q", b)
	}
	// ~/.config/mek is still there and ~/.mek exists now: left over.
	out.Reset()
	Migrate(&out)
	if !strings.Contains(out.String(), "is left over and ignored") {
		t.Errorf("left over: %q", out.String())
	}
}

func TestMigrateWaitsForTunnels(t *testing.T) {
	home, old := migrateHome(t)
	lock := filepath.Join(old, "tunnels", "zz-running.lock")             // after the others: they are skipped first
	os.WriteFile(filepath.Join(old, "tunnels", "free.lock"), nil, 0o600) // a finished tunnel's
	os.Mkdir(filepath.Join(old, "tunnels", "aa-odd.lock"), 0o700)        // can't be opened
	f, _ := os.Create(lock)
	defer f.Close()
	if err := proc.TryLock(f); err != nil { // a running tunnel's owner
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Migrate(&out); err != nil || !strings.Contains(out.String(), "tunnels are running") {
		t.Fatalf("%v %q", err, out.String())
	}
	if Dir() != old {
		t.Errorf("Dir %s, want the old one for now", Dir())
	}
	if _, err := os.Stat(filepath.Join(home, ".mek")); err == nil {
		t.Error("moved under a running tunnel")
	}
}

func TestMigrateFailures(t *testing.T) {
	_, old := migrateHome(t)
	oldRename, oldLink, oldGOOS := rename, symlink, goos
	t.Cleanup(func() { rename, symlink, goos = oldRename, oldLink, oldGOOS })

	rename = func(string, string) error { return errors.New("cross-device link") }
	if err := Migrate(&bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "move it yourself") {
		t.Errorf("rename: %v", err)
	}
	rename = oldRename

	symlink = func(string, string) error { return errors.New("not permitted") }
	var out bytes.Buffer
	if err := Migrate(&out); err != nil || !strings.Contains(out.String(), "couldn't leave a link") {
		t.Errorf("symlink: %v %q", err, out.String())
	}

	home, _ := migrateHome(t) // Windows: no link, a note instead
	_ = old
	goos = "windows"
	out.Reset()
	if err := Migrate(&out); err != nil || !strings.Contains(out.String(), "now need "+filepath.Join(home, ".mek")) {
		t.Errorf("windows: %v %q", err, out.String())
	}
}
