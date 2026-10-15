package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mek loads its config on every command — shell prompts (mek ctx --short)
// and kubectl (mek kube token) run it often. Not a CI gate: runner timings
// vary; run with go test -bench . ./internal/config/
func BenchmarkLoadLayers(b *testing.B) {
	home := b.TempDir()
	b.Setenv("HOME", home)
	b.Setenv("USERPROFILE", home)
	b.Setenv("MEK_HOME", filepath.Join(home, ".mek"))
	os.MkdirAll(filepath.Join(home, ".mek"), 0o700)
	os.WriteFile(filepath.Join(home, ".mek", "config.yaml"), []byte(globalYAML), 0o600)
	repo := filepath.Join(home, "work", "orders")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o700)
	os.WriteFile(filepath.Join(repo, TeamFile), []byte(teamYAML), 0o600)
	os.WriteFile(filepath.Join(repo, LocalFile), []byte(localYAML), 0o600)
	b.Chdir(repo)
	for b.Loop() {
		if _, err := Load(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFindProject(b *testing.B) {
	root := b.TempDir()
	os.WriteFile(filepath.Join(root, TeamFile), nil, 0o600)
	deep := filepath.Join(append([]string{root}, strings.Split(strings.Repeat("d/", 20), "/")...)...)
	os.MkdirAll(deep, 0o700)
	for b.Loop() {
		if FindProject(deep) != root {
			b.Fatal("not found")
		}
	}
}
