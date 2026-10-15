// Package kube writes per-context kubeconfig files whose credentials always
// come from mek (`mek --context <ctx> kube token ...`), so kubectl, K9s and
// FreeLens use the context's cloud login and never a stored key.
package kube

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/fsutil"
	"github.com/prateep-r/mek/internal/provider"
)

// Dir holds mek's kubeconfig files.
func Dir() string { return filepath.Join(config.Dir(), "kube") }

// Path is the kubeconfig file of one context, by its Key.
func Path(key string) string { return filepath.Join(Dir(), key+".yaml") }

// For is who a kubeconfig is for: the context's name (kube contexts are
// <name>/<cluster>), its Key (the file) and the mek flags that bring the
// context back when kubectl runs `mek kube token` from any directory.
type For struct {
	Name, Key string
	Flags     []string
}

// ForContext is For a context; project is the directory of the project
// config it came from ("" for ~/.mek/config.yaml).
func ForContext(ctx *config.Context, project string) For {
	f := For{Name: ctx.Name, Key: ctx.Key(), Flags: []string{"--context", ctx.Name}}
	if ctx.InProject {
		f.Flags = append([]string{"--project", project}, f.Flags...)
	}
	return f
}

// Env returns the KUBECONFIG a context's commands get: its mek kubeconfig,
// when the context has clusters or the file was generated before.
func Env(ctx *config.Context) (string, bool) {
	p := Path(ctx.Key())
	if len(ctx.Clusters) > 0 {
		return p, true
	}
	_, err := os.Stat(p)
	return p, err == nil
}

// Owned reports whether a KUBECONFIG value is mek's alone (every path in
// mek's directory), so mek may replace or unset it; a list the user built
// with mek's file in it stays theirs.
func Owned(kubeconfig string) bool {
	paths := filepath.SplitList(kubeconfig)
	for _, p := range paths {
		if !owned(p) {
			return false
		}
	}
	return len(paths) > 0
}

func owned(p string) bool { return strings.HasPrefix(p, Dir()+string(filepath.Separator)) }

// Test seams.
var (
	executable = os.Executable
	lookPath   = exec.LookPath
)

// MekPath is the mek the kubeconfig's exec plugin runs: the one on PATH when
// it is the running binary (a stable Homebrew symlink beats the versioned
// Caskroom path), else the running binary itself.
func MekPath() (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if p, err := lookPath("mek"); err == nil {
		a, aerr := os.Stat(p)
		b, berr := os.Stat(exe)
		if aerr == nil && berr == nil && os.SameFile(a, b) {
			return p, nil
		}
	}
	return exe, nil
}

// Entry is one cluster in a context's kubeconfig.
type Entry struct {
	Alias   string
	Cluster config.Cluster
	Info    provider.KubeCluster
}

// Name is the cluster's name in kubeconfig files: unique across contexts.
func (e Entry) Name(ctx string) string { return ctx + "/" + e.Alias }

// tokenArgs are the exec plugin arguments: the cluster's real identity, so
// ad-hoc clusters work and renaming an alias doesn't break a file.
func (e Entry) tokenArgs(f For) []string {
	return append(slices.Clone(f.Flags), "kube", "token", "--name", e.Cluster.Name, "--location", e.Info.Location)
}

// Describe looks up every cluster (sorted by alias) through the provider.
func Describe(kp provider.KubeProvider, clusters map[string]*config.Cluster, q provider.Query) ([]Entry, error) {
	var entries []Entry
	for _, alias := range slices.Sorted(maps.Keys(clusters)) {
		info, err := kp.DescribeCluster(*clusters[alias], q)
		if err != nil {
			return nil, fmt.Errorf("cluster %s: %w", alias, err)
		}
		entries = append(entries, Entry{Alias: alias, Cluster: *clusters[alias], Info: info})
	}
	return entries, nil
}

// Write renders entries into the context's kubeconfig — the fixed skeleton of
// the Template Method; Describe's provider supplied the cloud-specific parts.
// current selects current-context (default: the first entry).
func Write(f For, entries []Entry, current, mek, pathEnv string) (string, error) {
	b, err := Render(f, entries, current, mek, pathEnv)
	if err != nil {
		return "", err
	}
	p := Path(f.Key)
	return p, fsutil.WriteFileAtomic(p, b, 0o600)
}

// Render produces the kubeconfig YAML.
func Render(f For, entries []Entry, current, mek, pathEnv string) ([]byte, error) {
	ctx := f.Name
	if len(entries) == 0 {
		return nil, fmt.Errorf("no clusters to write")
	}
	cfg := kubeconfig{APIVersion: "v1", Kind: "Config", CurrentContext: entries[0].Name(ctx), Preferences: map[string]any{}}
	for _, e := range entries {
		name := e.Name(ctx)
		if e.Alias == current {
			cfg.CurrentContext = name
		}
		cfg.Clusters = append(cfg.Clusters, namedCluster{name, clusterInfo{e.Info.Server, e.Info.CAData}})
		cfg.Users = append(cfg.Users, namedUser{name, userInfo{execConfig{
			APIVersion: "client.authentication.k8s.io/v1beta1", Command: mek, Args: e.tokenArgs(f),
			Env: []execEnv{{"PATH", pathEnv}}, InteractiveMode: "Never",
			InstallHint: "mek is needed for these credentials: https://github.com/prateep-r/mek",
		}}})
		cfg.Contexts = append(cfg.Contexts, namedContext{name, contextInfo{name, name, e.Cluster.Namespace}})
	}
	var buf bytes.Buffer
	buf.WriteString("# Generated by mek — `mek -c " + ctx + " kube` rewrites it.\n")
	out, _ := yaml.Marshal(cfg) // plain structs of strings always marshal
	buf.Write(out)
	return buf.Bytes(), nil
}

type kubeconfig struct {
	APIVersion     string         `yaml:"apiVersion"`
	Kind           string         `yaml:"kind"`
	Clusters       []namedCluster `yaml:"clusters"`
	Users          []namedUser    `yaml:"users"`
	Contexts       []namedContext `yaml:"contexts"`
	CurrentContext string         `yaml:"current-context"`
	Preferences    map[string]any `yaml:"preferences"`
}
type namedCluster struct {
	Name    string      `yaml:"name"`
	Cluster clusterInfo `yaml:"cluster"`
}
type clusterInfo struct {
	Server string `yaml:"server"`
	CAData string `yaml:"certificate-authority-data,omitempty"`
}
type namedUser struct {
	Name string   `yaml:"name"`
	User userInfo `yaml:"user"`
}
type userInfo struct {
	Exec execConfig `yaml:"exec"`
}
type execConfig struct {
	APIVersion         string    `yaml:"apiVersion"`
	Command            string    `yaml:"command"`
	Args               []string  `yaml:"args"`
	Env                []execEnv `yaml:"env"`
	InteractiveMode    string    `yaml:"interactiveMode"`
	ProvideClusterInfo bool      `yaml:"provideClusterInfo"`
	InstallHint        string    `yaml:"installHint"`
}
type execEnv struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}
type namedContext struct {
	Name    string      `yaml:"name"`
	Context contextInfo `yaml:"context"`
}
type contextInfo struct {
	Cluster   string `yaml:"cluster"`
	User      string `yaml:"user"`
	Namespace string `yaml:"namespace,omitempty"`
}
