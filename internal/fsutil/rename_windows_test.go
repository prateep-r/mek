//go:build windows

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetryRename(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "from"), filepath.Join(dir, "to")
	os.WriteFile(to, []byte("old"), 0o600)
	open := func() *os.File { f, _ := os.Open(to); return f } // no FILE_SHARE_DELETE: blocks replacing it

	os.WriteFile(from, []byte("new"), 0o600)
	f := open()
	go func() { time.Sleep(100 * time.Millisecond); f.Close() }()
	if err := retryRename(from, to); err != nil {
		t.Fatalf("rename once the reader let go: %v", err)
	}
	if b, _ := os.ReadFile(to); string(b) != "new" {
		t.Errorf("content %q", b)
	}

	old := renameFor
	renameFor = 50 * time.Millisecond
	t.Cleanup(func() { renameFor = old })
	os.WriteFile(from, []byte("newer"), 0o600)
	f = open()
	defer f.Close()
	if err := retryRename(from, to); err == nil || !transient(err) {
		t.Errorf("still open: %v", err)
	}
	if err := retryRename(filepath.Join(dir, "missing"), to); err == nil || transient(err) {
		t.Errorf("missing source: %v", err)
	}
}
