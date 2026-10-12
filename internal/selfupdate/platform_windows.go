//go:build windows

package selfupdate

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/prateep-r/mek/internal/fsutil"
)

// The release archive and the binary in it (see .goreleaser.yaml).
const (
	archiveExt = ".zip"
	binaryName = "mek.exe"
)

var writeBinary = fsutil.WriteFileAtomic // test seam

// A previous update's old binary goes once mek runs again.
func init() { removeOld() }

func removeOld() {
	if exe, err := executable(); err == nil {
		os.Remove(exe + ".old")
	}
}

// extract finds name in a .zip archive.
func extract(archive []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), name) && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 200<<20))
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

// replace: Windows won't overwrite a running .exe but lets it be renamed,
// so the old one moves aside (removed on the next run) and the new one
// takes its name; on failure the old one comes back.
func replace(exe string, bin []byte) error {
	old := exe + ".old"
	os.Remove(old) // a leftover from an update this mek didn't outlive
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := writeBinary(exe, bin, 0o755); err != nil {
		os.Rename(old, exe)
		return err
	}
	return nil
}
