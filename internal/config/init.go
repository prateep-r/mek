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

// globalTemplate starts a ~/.mek/config.yaml that mek ctx add creates:
// the example's contexts would be in the way there.
const globalTemplate = `# mek configuration — https://github.com/prateep-r/mek
# Each context is one cloud target. Switch with ` + "`mek use <name>`" + `.
# Reference: mek help config
contexts:
`

// Template is what a missing config file starts as when mek ctx add
// creates it: a project's file as mek init writes it, else a short header.
func Template(path string) []byte {
	switch filepath.Base(path) {
	case TeamFile:
		return projectTemplate
	case LocalFile:
		return localTemplate
	}
	return []byte(globalTemplate)
}

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
	ignore, err = IgnoreLocal(dir)
	return file, ignore, err
}

// IgnoreLocal lists mek.local.yaml in the .gitignore of the git repository
// dir is in, unless it is there already; ignore is the file it changed.
func IgnoreLocal(dir string) (ignore string, err error) {
	root := gitRoot(dir)
	if root == "" {
		return "", nil
	}
	ignore = filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(ignore)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if l := strings.TrimSpace(line); l == LocalFile || l == "/"+LocalFile || l == "**/"+LocalFile {
			return "", nil // ignored already
		}
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	b = append(b, []byte("# personal mek contexts (mek init --local)\n"+LocalFile+"\n")...)
	return ignore, os.WriteFile(ignore, b, 0o644)
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
