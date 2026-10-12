//go:build windows

package selfupdate

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractZip(t *testing.T) {
	if _, err := extract([]byte("not a zip"), "mek.exe"); err == nil {
		t.Error("not a zip")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	zw.Create("dir/")
	zw.Close()
	if _, err := extract(buf.Bytes(), "mek.exe"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("no binary: %v", err)
	}
	buf.Reset()
	zw = zip.NewWriter(&buf)
	w, _ := zw.CreateRaw(&zip.FileHeader{Name: "mek.exe", Method: 99}) // a compression zip can't read
	w.Write([]byte("x"))
	zw.Close()
	if _, err := extract(buf.Bytes(), "mek.exe"); !errors.Is(err, zip.ErrAlgorithm) {
		t.Errorf("unknown method: %v", err)
	}
	if bin, err := extract(makeArchive([]byte("NEW")), "MEK.EXE"); err != nil || string(bin) != "NEW" { // case doesn't matter
		t.Errorf("extract: %q %v", bin, err)
	}
}

// A running mek.exe can't be overwritten: it moves aside, and comes back
// when the new one can't be written.
func TestReplaceRunning(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mek.exe")
	if err := replace(exe, []byte("NEW")); err == nil {
		t.Error("nothing to replace")
	}
	os.WriteFile(exe, []byte("OLD"), 0o755)
	os.WriteFile(exe+".old", []byte("STALE"), 0o755)
	if err := replace(exe, []byte("NEW")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEW" {
		t.Errorf("binary %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "OLD" {
		t.Errorf("old one: %q", b)
	}

	old := writeBinary
	t.Cleanup(func() { writeBinary = old })
	writeBinary = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if err := replace(exe, []byte("NEWER")); err == nil {
		t.Error("write failure")
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEW" {
		t.Errorf("not restored: %q", b)
	}

	oldExe := executable
	t.Cleanup(func() { executable = oldExe })
	executable = func() (string, error) { return exe, nil }
	removeOld()
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Error("the old binary is still there")
	}
}
