// Package fsutil holds small file helpers shared by mek's packages.
package fsutil

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// Test seams; rename retries on Windows (rename_windows.go).
var (
	createTemp = os.CreateTemp
	rename     = os.Rename
)

// WriteFileAtomic replaces path with data so readers never see a partial
// file, even when several mek processes write it at once: each writer uses
// its own temp file in the same directory and renames it into place.
// It does nothing when path already holds exactly data, which keeps hot
// paths (every `mek aws ...` regenerates the AWS config) free of disk writes.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := createTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Chmod(perm), tmp.Close()); err != nil {
		return err
	}
	return rename(tmp.Name(), path)
}
