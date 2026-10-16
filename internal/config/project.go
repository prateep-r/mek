package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// The project's files: the team's (committed) and yours (gitignored).
const (
	TeamFile  = "mek.yaml"
	LocalFile = "mek.local.yaml"
)

// Project options, set from mek's --project and --no-local flags.
var (
	ProjectFlag string // use this directory as the project
	NoProject   bool   // read no project files ($MEK_NO_LOCAL=1 too)
)

// Sources are the layers Load reads, top first, and the project directory
// they came from ("" for none).
func Sources() (Layers, string) {
	global := File{Path: Path()}
	if NoProject || os.Getenv("MEK_NO_LOCAL") == "1" {
		return Layers{global}, ""
	}
	dir := ProjectFlag
	if dir == "" {
		cwd, _ := os.Getwd()
		dir = FindProject(cwd)
	}
	if dir == "" {
		return Layers{global}, ""
	}
	dir, _ = filepath.Abs(dir)
	project := Layers{File{filepath.Join(dir, LocalFile), true}, File{filepath.Join(dir, TeamFile), true}}
	return Layers{project, global}, dir
}

// FindProject is the nearest directory from start up that has a mek.yaml or
// mek.local.yaml. It stops at a repository's root (a .git) and never looks
// at your home directory or above it.
func FindProject(start string) string {
	home, _ := os.UserHomeDir()
	for dir := start; dir != "" && dir != home; {
		if exists(filepath.Join(dir, TeamFile)) || exists(filepath.Join(dir, LocalFile)) {
			return dir
		}
		parent := filepath.Dir(dir)
		if exists(filepath.Join(dir, ".git")) || parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Key names what mek keeps for a context (its gcloud login, SSH key,
// kubeconfig, AWS profile). A context from ~/.mek/config.yaml keeps its
// name; one from a project adds a hash of who it is (cloud, portal, account,
// role, profile, project), so two projects' "dev" on different accounts
// never share a login, while the same "dev" in two clones does.
func (c *Context) Key() string {
	if !c.InProject {
		return c.Name
	}
	fields := []string{c.Provider, c.SSOStartURL, c.SSORegion, c.AccountID, c.Role, c.AWSProfile, c.Project, c.Account}
	if c.Provider == ProviderAzure {
		fields = append(fields, c.TenantID, c.SubscriptionID)
	}
	id := strings.Join(fields, "\x00")
	sum := sha256.Sum256([]byte(id))
	return c.Name + "-" + hex.EncodeToString(sum[:4])
}

// Show is a path as messages show it: relative to the working directory
// when it is close — ./mek.yaml, or ../../mek.yaml from inside a project —
// else, and always for mek's own directory, with ~ for the home directory.
func Show(path string) string {
	if cwd, err := os.Getwd(); err == nil && !strings.HasPrefix(path, Dir()+string(filepath.Separator)) { // mek's own: ~/.mek/…
		if rel, err := filepath.Rel(cwd, path); err == nil {
			up := strings.Count(rel+string(filepath.Separator), ".."+string(filepath.Separator))
			switch {
			case up == 0:
				return "." + string(filepath.Separator) + rel
			case up <= 3:
				return rel
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}
