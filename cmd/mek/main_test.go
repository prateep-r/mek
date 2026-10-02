package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	t.Setenv("MEK_CONTEXT", "dev")
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("contexts:\n  dev: {provider: gcp, project: p}\n"), 0o600)

	var stderr bytes.Buffer
	if code := run([]string{"ctx", "--short"}, &stderr); code != 0 {
		t.Errorf("success: exit %d (%s)", code, stderr.String())
	}
	// The wrapped command's exit code becomes mek's, with no extra message.
	stderr.Reset()
	if code := run([]string{"exec", "--", "sh", "-c", "exit 3"}, &stderr); code != 3 || stderr.Len() != 0 {
		t.Errorf("child exit: code %d, stderr %q", code, stderr.String())
	}
	// mek's own errors exit 1 with a message.
	stderr.Reset()
	if code := run([]string{"use", "nope"}, &stderr); code != 1 || !strings.Contains(stderr.String(), "mek: unknown context") {
		t.Errorf("mek error: code %d, stderr %q", code, stderr.String())
	}
}
