// Package selfupdate replaces the running mek binary with the latest GitHub
// release, verifying its SHA-256 against the release's checksums.txt.
package selfupdate

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 60 * time.Second}

// ErrHomebrew means the binary is managed by Homebrew and must be upgraded there.
var ErrHomebrew = errors.New("mek is installed via Homebrew — run: brew upgrade mek")

// Latest returns the tag of the latest release (e.g. "v0.2.0") by following
// GitHub's /releases/latest redirect, avoiding the rate-limited REST API.
func Latest(repo string) (string, error) {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get("https://github.com/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return "", fmt.Errorf("no published release found for %s (HTTP %d)", repo, resp.StatusCode)
	}
	return loc[i+len("/tag/"):], nil
}

// AssetName matches the archive name_template in .goreleaser.yaml.
func AssetName() string { return fmt.Sprintf("mek_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH) }

// IsHomebrew reports whether path lives inside a Homebrew prefix.
func IsHomebrew(path string) bool {
	for _, m := range []string{"/Caskroom/", "/Cellar/", "/homebrew/", "/linuxbrew/"} {
		if strings.Contains(path, m) {
			return true
		}
	}
	return false
}

// Update installs release tag over the running executable.
func Update(repo, tag string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if IsHomebrew(exe) {
		return exe, ErrHomebrew
	}

	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/", repo, tag)
	asset := AssetName()
	archive, err := fetch(base + asset)
	if err != nil {
		return exe, err
	}
	sums, err := fetch(base + "checksums.txt")
	if err != nil {
		return exe, err
	}
	if err := verify(archive, sums, asset); err != nil {
		return exe, err
	}
	bin, err := extract(archive, "mek")
	if err != nil {
		return exe, err
	}

	tmp, err := os.CreateTemp(filepath.Dir(exe), ".mek-update-*")
	if err != nil {
		return exe, fmt.Errorf("cannot write to %s: %w", filepath.Dir(exe), err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return exe, err
	}
	if err := tmp.Close(); err != nil {
		return exe, err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return exe, err
	}
	return exe, os.Rename(tmp.Name(), exe)
}

func fetch(url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func verify(data, sums []byte, name string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if f[0] != got {
				return fmt.Errorf("checksum mismatch for %s", name)
			}
			return nil
		}
	}
	return fmt.Errorf("%s not listed in checksums.txt", name)
}

func extract(tgz []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == name && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}
