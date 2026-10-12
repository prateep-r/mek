//go:build e2e && windows

package e2e

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/prateep-r/mek/test/testkit"
)

var zipAsset = "mek_windows_" + runtime.GOARCH + ".zip" // .goreleaser.yaml name_template + format_overrides

// zipArchive packs mek.exe and README.md like GoReleaser's Windows archive.
func zipArchive(t *testing.T) []byte {
	t.Helper()
	bin, err := os.ReadFile(mek)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string][]byte{"README.md": []byte("# mek\n"), "mek.exe": bin} {
		w, _ := zw.Create(name)
		w.Write(body)
	}
	zw.Close()
	return buf.Bytes()
}

// installPS1 runs the repository's install.ps1 in Windows PowerShell (what
// every Windows has), with a console: PowerShell needs one.
func installPS1(t *testing.T, url, dir string) (string, error) {
	t.Helper()
	root, err := testkit.ModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(root, "install.ps1"))
	cmd.Env = append(os.Environ(), "MEK_DOWNLOAD_URL="+url, "MEK_INSTALL_DIR="+dir, "MEK_NO_MODIFY_PATH=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallPS1(t *testing.T) {
	good := zipArchive(t)
	url := serve(t, map[string][]byte{zipAsset: good, "checksums.txt": checksums(map[string][]byte{zipAsset: good})})
	dir := filepath.Join(t.TempDir(), "mek bin") // a space in the path
	out, err := installPS1(t, url, dir)
	if err != nil || !strings.Contains(out, releaseVersion) {
		t.Fatalf("install: %v\n%s", err, out)
	}
	exe := filepath.Join(dir, "mek.exe")
	if b, _ := exec.Command(exe, "version").Output(); !strings.Contains(string(b), releaseVersion) {
		t.Errorf("installed mek: %s", b)
	}
	if out, err := installPS1(t, url, dir); err != nil || !strings.Contains(out, "installed") { // over an existing one
		t.Errorf("reinstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Error("the old binary was left behind")
	}

	for name, files := range map[string]map[string][]byte{
		"checksum mismatch": {zipAsset: append(bytes.Clone(good), 0), "checksums.txt": checksums(map[string][]byte{zipAsset: good})},
		"is not listed":     {zipAsset: good, "checksums.txt": checksums(map[string][]byte{"mek_other.zip": good})},
	} {
		dir := t.TempDir()
		out, err := installPS1(t, serve(t, files), dir)
		if err == nil || !strings.Contains(out, name) {
			t.Errorf("%s: %v\n%s", name, err, out)
		}
		if _, err := os.Stat(filepath.Join(dir, "mek.exe")); err == nil {
			t.Errorf("%s: installed anyway", name)
		}
	}
}
