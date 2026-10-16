package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWriteFileAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config")
	if err := WriteFileAtomic(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", fi.Mode().Perm())
	}

	// Same content: the file must not be rewritten.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); !fi.ModTime().Equal(past) {
		t.Error("unchanged content was rewritten")
	}

	if err := WriteFileAtomic(path, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "b" {
		t.Errorf("content = %q, want b", b)
	}
}

func TestWriteFileAtomicConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- WriteFileAtomic(path, []byte(fmt.Sprintf("writer %02d", i)), 0o600)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(path); len(b) != len("writer 00") {
		t.Errorf("partial or mixed content: %q", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestWriteFileAtomicErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o600)
	if err := WriteFileAtomic(filepath.Join(file, "x", "config"), []byte("a"), 0o600); err == nil {
		t.Error("MkdirAll under a file must fail")
	}

	locked := filepath.Join(dir, "locked")
	os.Mkdir(locked, 0o500) // can't create the temp file here
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if err := WriteFileAtomic(filepath.Join(locked, "config"), []byte("a"), 0o600); err == nil {
		t.Error("CreateTemp in a read-only dir must fail")
	}

	target := filepath.Join(dir, "target")
	os.MkdirAll(filepath.Join(target, "child"), 0o700) // rename over a non-empty dir fails
	if err := WriteFileAtomic(target, []byte("a"), 0o600); err == nil {
		t.Error("rename over a directory must fail")
	}

	// A temp file that can't be written (opened read-only).
	old := createTemp
	t.Cleanup(func() { createTemp = old })
	createTemp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		f.Close()
		return os.Open(f.Name())
	}
	if err := WriteFileAtomic(filepath.Join(dir, "ro"), []byte("a"), 0o600); err == nil {
		t.Error("write to a read-only temp file must fail")
	}
}
