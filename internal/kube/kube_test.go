package kube

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fakeKube answers DescribeCluster from a table (Template Method's hooks).
type fakeKube map[string]provider.KubeCluster

func (f fakeKube) DescribeCluster(c config.Cluster, _ provider.Query) (provider.KubeCluster, error) {
	k, ok := f[c.Name]
	if !ok {
		return k, errors.New("not found")
	}
	return k, nil
}
func (fakeKube) TokenCommand(string, string) []string { return nil }

func entries(t *testing.T) []Entry {
	t.Helper()
	kp := fakeKube{
		"prod-eks": {Server: "https://A.eks.amazonaws.com", CAData: "Q0EtMQ==", Location: "ap-southeast-1"},
		"data-eks": {Server: "https://B.eks.amazonaws.com", CAData: "Q0EtMg==", Location: "us-east-1"},
	}
	es, err := Describe(kp, map[string]*config.Cluster{
		"main": {Name: "prod-eks", Namespace: "app"},
		"data": {Name: "data-eks", Region: "us-east-1"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestDescribe(t *testing.T) {
	es := entries(t)
	if len(es) != 2 || es[0].Alias != "data" || es[1].Alias != "main" { // sorted by alias
		t.Fatalf("entries: %+v", es)
	}
	if es[1].Name("prod") != "prod/main" {
		t.Errorf("name: %s", es[1].Name("prod"))
	}
	_, err := Describe(fakeKube{}, map[string]*config.Cluster{"x": {Name: "gone"}}, nil)
	if err == nil || err.Error() != "cluster x: not found" {
		t.Errorf("err: %v", err)
	}
}

func TestRenderGolden(t *testing.T) {
	got, err := Render("prod", entries(t), "main", "/opt/homebrew/bin/mek", "/opt/homebrew/bin:/usr/bin")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "prod.yaml")
	if *update {
		os.WriteFile(golden, got, 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("render differs from %s (go test ./internal/kube -update):\n%s", golden, got)
	}
	// Without a current cluster, the first one is current.
	got, _ = Render("prod", entries(t), "", "mek", "")
	if !strings.Contains(string(got), "current-context: prod/data") {
		t.Errorf("default current:\n%s", got)
	}
	if _, err := Render("prod", nil, "", "mek", ""); err == nil {
		t.Error("no entries must fail")
	}
}

func TestWriteAndEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	ctx := &config.Context{Name: "prod"}
	if p, ok := Env(ctx); ok || p != filepath.Join(home, "kube", "prod.yaml") {
		t.Errorf("Env without file or clusters: %s %v", p, ok)
	}
	ctx.Clusters = map[string]*config.Cluster{"main": {}}
	if _, ok := Env(ctx); !ok {
		t.Error("Env with clusters")
	}
	p, err := Write("prod", entries(t), "", "mek", "")
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("written: %v %v", fi, err)
	}
	ctx.Clusters = nil // ad-hoc clusters only: the file still counts
	if got, ok := Env(ctx); !ok || got != p {
		t.Errorf("Env with file: %s %v", got, ok)
	}
	if _, err := Write("prod", nil, "", "mek", ""); err == nil {
		t.Error("Write must report Render errors")
	}
}

func TestOwned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEK_HOME", home)
	mine := filepath.Join(home, "kube", "prod.yaml")
	sep := string(filepath.ListSeparator)
	for v, want := range map[string]bool{
		"":                             false,
		mine:                           true,
		mine + sep + mine:              true,
		mine + sep + "/u/.kube/config": false,
		filepath.Join(home, "kube"):    false,
		"/u/.kube/config":              false,
	} {
		if Owned(v) != want {
			t.Errorf("Owned(%q) = %v", v, !want)
		}
	}
}

func TestMekPath(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "mek-real")
	link := filepath.Join(dir, "mek")
	other := filepath.Join(dir, "other")
	os.WriteFile(real, nil, 0o755)
	os.WriteFile(other, nil, 0o755)
	os.Symlink(real, link)
	t.Cleanup(func() { executable, lookPath = os.Executable, exec.LookPath })

	executable = func() (string, error) { return real, nil }
	lookPath = func(string) (string, error) { return link, nil }
	if p, _ := MekPath(); p != link {
		t.Errorf("same binary on PATH: %s", p)
	}
	lookPath = func(string) (string, error) { return other, nil }
	if p, _ := MekPath(); p != real {
		t.Errorf("different mek on PATH: %s", p)
	}
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if p, _ := MekPath(); p != real {
		t.Errorf("no mek on PATH: %s", p)
	}
	executable = func() (string, error) { return "", errors.New("boom") }
	if _, err := MekPath(); err == nil {
		t.Error("executable error")
	}
}
