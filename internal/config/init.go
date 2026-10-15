package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed example.yaml
var exampleConfig []byte

// The templates for a project's files.
var (
	//go:embed project.yaml
	projectTemplate []byte
	//go:embed local.yaml
	localTemplate []byte
)

// Example returns the commented example config.
func Example() []byte { return exampleConfig }

// Init writes the example config if no config exists yet.
func Init(force bool) (string, error) {
	p := Path()
	if _, err := os.Stat(p); err == nil && !force {
		return p, fmt.Errorf("%s already exists (use --force to overwrite)", p)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return p, err
	}
	return p, os.WriteFile(p, exampleConfig, 0o600)
}

// InitProject writes a project's mek.yaml (local false) or mek.local.yaml
// (local true) in dir. Inside a git repository, mek.local.yaml goes into the
// repository's .gitignore; ignore is that file when it changed.
func InitProject(dir string, local, force bool) (file, ignore string, err error) {
	name, body := TeamFile, projectTemplate
	if local {
		name, body = LocalFile, localTemplate
	}
	file = filepath.Join(dir, name)
	if _, err := os.Stat(file); err == nil && !force {
		return file, "", fmt.Errorf("%s already exists (use --force to overwrite)", Show(file))
	}
	if err := os.WriteFile(file, body, 0o644); err != nil {
		return file, "", err
	}
	if !local {
		return file, "", nil
	}
	root := gitRoot(dir)
	if root == "" {
		return file, "", nil
	}
	ignore = filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(ignore)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return file, "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if l := strings.TrimSpace(line); l == LocalFile || l == "/"+LocalFile || l == "**/"+LocalFile {
			return file, "", nil // ignored already
		}
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	b = append(b, []byte("# personal mek contexts (mek init --local)\n"+LocalFile+"\n")...)
	return file, ignore, os.WriteFile(ignore, b, 0o644)
}

// gitRoot is the git repository dir is in ("" for none).
func gitRoot(dir string) string {
	for {
		if exists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
