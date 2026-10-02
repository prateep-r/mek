package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// release serves a fake GitHub: /releases/latest redirects to tag, and the
// download URLs serve assets (name -> body; missing names 404).
func release(t *testing.T, tag string, assets map[string][]byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/o/mek/releases/latest" && tag != "":
			http.Redirect(w, r, "/o/mek/releases/tag/"+tag, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/o/mek/releases/download/"+tag+"/"):
			body, ok := assets[filepath.Base(r.URL.Path)]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = old })
}

// installed fakes the running executable at a path inside dir.
func installed(t *testing.T, dir string) string {
	t.Helper()
	exe := filepath.Join(dir, "mek")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	old := executable
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = old })
	return exe
}

func goodAssets(bin string) map[string][]byte {
	tgz := makeTGZ(nil, "mek", []byte(bin))
	sum := sha256.Sum256(tgz)
	return map[string][]byte{
		assetName():     tgz,
		"checksums.txt": []byte(hex.EncodeToString(sum[:]) + "  " + assetName() + "\n"),
	}
}

func TestLatest(t *testing.T) {
	release(t, "v1.2.3", nil)
	if tag, err := Latest("o/mek"); err != nil || tag != "v1.2.3" {
		t.Errorf("Latest = %q, %v", tag, err)
	}
	release(t, "", nil) // no release: 404, no redirect
	if _, err := Latest("o/mek"); err == nil || !strings.Contains(err.Error(), "no published release") {
		t.Errorf("no release: %v", err)
	}
	baseURL = "http://127.0.0.1:1" // nothing listening
	if _, err := Latest("o/mek"); err == nil {
		t.Error("unreachable server must fail")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.2.0", "v0.3.0", true},
		{"0.2.0", "v0.3.0", true},
		{"v0.3.0", "v0.3.0", false},
		{"v0.4.0", "v0.3.0", false},            // never downgrade
		{"v0.3.1", "v0.3.0", false},            // patch ahead
		{"v1.0.0", "v0.9.9", false},            // major ahead
		{"v0.3.0-2-gabc1234", "v0.3.0", false}, // local build ahead of the tag
		{"v0.3.0-dirty", "v0.3.0", false},
		{"v0.3.0-2-gabc1234-dirty", "v0.3.0", false},
		{"v0.3.0-rc1", "v0.3.0", true}, // pre-release -> final
		{"v0.3.0-rc1", "v0.3.0-rc2", false},
		{"dev", "v0.3.0", true}, // unknown build: allow
		{"v0.3.0", "nightly", false},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestUpdate(t *testing.T) {
	dir := t.TempDir()
	exe := installed(t, dir)
	exe, _ = filepath.EvalSymlinks(exe) // macOS: /var -> /private/var
	release(t, "v9.0.0", goodAssets("NEW"))
	got, err := Update("o/mek", "v9.0.0")
	if err != nil || got != exe {
		t.Fatalf("Update = %s, %v", got, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEW" {
		t.Errorf("binary = %q", b)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Errorf("perm = %v", fi.Mode().Perm())
	}

	// Symlinked install (e.g. ~/.local/bin/mek -> ~/tools/mek): the target is replaced.
	link := filepath.Join(t.TempDir(), "mek")
	os.Symlink(exe, link)
	executable = func() (string, error) { return link, nil }
	if got, err := Update("o/mek", "v9.0.0"); err != nil || got != exe {
		t.Errorf("symlink: %s %v", got, err)
	}
}

func TestUpdateRefuses(t *testing.T) {
	good := goodAssets("NEW")
	cases := []struct {
		name   string
		assets map[string][]byte
		want   string
	}{
		{"archive missing", map[string][]byte{"checksums.txt": good["checksums.txt"]}, "404"},
		{"checksums missing", map[string][]byte{assetName(): good[assetName()]}, "404"},
		{"tampered archive", map[string][]byte{assetName(): append(good[assetName()], 0), "checksums.txt": good["checksums.txt"]}, "mismatch"},
		{"not a gzip", func() map[string][]byte {
			body := []byte("this is definitely not gzip data")
			sum := sha256.Sum256(body)
			return map[string][]byte{assetName(): body, "checksums.txt": []byte(hex.EncodeToString(sum[:]) + "  " + assetName())}
		}(), "gzip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exe := installed(t, t.TempDir())
			release(t, "v9.0.0", c.assets)
			if _, err := Update("o/mek", "v9.0.0"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
			if b, _ := os.ReadFile(exe); string(b) != "OLD" {
				t.Error("the installed binary must be left alone")
			}
		})
	}
}

func TestUpdateEnvironmentErrors(t *testing.T) {
	old := executable
	t.Cleanup(func() { executable = old })

	executable = func() (string, error) { return "", errors.New("no exe") }
	if _, err := Update("o/mek", "v1"); err == nil {
		t.Error("executable error must be returned")
	}
	executable = func() (string, error) { return "/opt/homebrew/Caskroom/mek/1/mek", nil }
	if _, err := Update("o/mek", "v1"); !errors.Is(err, ErrHomebrew) {
		t.Errorf("homebrew: %v", err)
	}

	release(t, "v9.0.0", goodAssets("NEW"))
	locked := t.TempDir()
	exe := installed(t, locked)
	os.Chmod(locked, 0o500) // can't place the new binary next to the old one
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if _, err := Update("o/mek", "v9.0.0"); err == nil || !strings.Contains(err.Error(), "cannot replace") {
		t.Errorf("read-only dir: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Error("binary changed despite the error")
	}

	baseURL = "http://127.0.0.1:1"
	installed(t, t.TempDir())
	if _, err := Update("o/mek", "v9.0.0"); err == nil {
		t.Error("unreachable server must fail")
	}
}

func TestExtractCorruptTar(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte("this is not a tar header, just junk that is long enough to read"))
	gz.Close()
	if _, err := extract(buf.Bytes(), "mek"); err == nil {
		t.Error("corrupt tar must fail")
	}
	// a tar without the binary
	var tbuf bytes.Buffer
	gz = gzip.NewWriter(&tbuf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 0, Typeflag: tar.TypeReg})
	tw.Close()
	gz.Close()
	if _, err := extract(tbuf.Bytes(), "mek"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing binary: %v", err)
	}
}
