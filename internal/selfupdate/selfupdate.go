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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/prateep-r/mek/internal/fsutil"
)

var client = &http.Client{Timeout: 60 * time.Second}

// Test seams.
var (
	baseURL    = "https://github.com" // GitHub's web host, where releases live
	executable = os.Executable
)

// ErrHomebrew means the binary is managed by Homebrew and must be upgraded there.
var ErrHomebrew = errors.New("mek is installed via Homebrew — run: brew upgrade mek")

// Latest returns the tag of the latest release (e.g. "v0.2.0") by following
// GitHub's /releases/latest redirect, avoiding the rate-limited REST API.
func Latest(repo string) (string, error) {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(baseURL + "/" + repo + "/releases/latest")
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

// assetName matches the archive name_template in .goreleaser.yaml.
func assetName() string { return fmt.Sprintf("mek_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH) }

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-(.+))?$`)

// aheadOfRelease matches what `git describe` appends to a tag for a local
// build: commits since the tag (-3-gabc1234) and/or -dirty.
var aheadOfRelease = regexp.MustCompile(`^(\d+-g[0-9a-f]+)?(-?dirty)?$`)

// Newer reports whether release latest should replace version current.
// Unparsable current versions ("dev") always update; a local build of a tag
// or ahead of it (v0.3.0-2-gabc, v0.3.0-dirty) is not downgraded; a
// pre-release (v0.3.0-rc1) updates to the final v0.3.0.
func Newer(current, latest string) bool {
	c, l := semver.FindStringSubmatch(current), semver.FindStringSubmatch(latest)
	if c == nil {
		return true
	}
	if l == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		cn, _ := strconv.Atoi(c[i])
		ln, _ := strconv.Atoi(l[i])
		if ln != cn {
			return ln > cn
		}
	}
	// Same X.Y.Z: only a pre-release of it is older.
	return c[4] != "" && !aheadOfRelease.MatchString(c[4]) && l[4] == ""
}

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
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if IsHomebrew(exe) {
		return exe, ErrHomebrew
	}

	base := fmt.Sprintf("%s/%s/releases/download/%s/", baseURL, repo, tag)
	asset := assetName()
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

	if err := fsutil.WriteFileAtomic(exe, bin, 0o755); err != nil {
		return exe, fmt.Errorf("cannot replace %s: %w", exe, err)
	}
	return exe, nil
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
