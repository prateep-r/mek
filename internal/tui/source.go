package tui

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/tunnel"
)

// Context is a context as the UI lists it.
type Context struct {
	Name, Summary       string
	Cloud               string // its provider: "aws", "gcp"
	Protected, ReadOnly bool
	Targets             []Target
	Clusters            []Cluster
	Tunnels             []Tunnel
}

// Target is a host for `mek shell`.
type Target struct{ Name, Instance string }

// Cluster is a Kubernetes cluster for `mek kube`.
type Cluster struct{ Alias, Name string }

// Tunnel is a named tunnel from the config.
type Tunnel struct {
	Name, Remote string
	Local        int
}

// Source is what the UI shows. Live reads mek's own files; tests use a fake.
type Source interface {
	Contexts() (list []Context, current string, err error)
	Tunnels() []tunnel.Info
	Audit(n int) ([]audit.Entry, error)
	Log(id string) string
}

// Live is the Source of a real mek: its config, tunnels and audit log.
type Live struct{}

// loaded is the last contexts Live read, and the files they came from as
// they were then: the UI asks every second, and they rarely change.
var loaded struct {
	stamp    string
	contexts []Context
}

func (Live) Contexts() ([]Context, string, error) {
	stamp := configStamp()
	if stamp == loaded.stamp {
		return loaded.contexts, config.Current(), nil
	}
	cfg, err := config.Load()
	if err != nil {
		loaded.stamp = ""
		return nil, "", err
	}
	var out []Context
	for _, name := range cfg.Names() {
		c := cfg.Contexts[name]
		out = append(out, contextOf(c, provider.For(cfg, c).Describe()))
	}
	loaded.stamp, loaded.contexts = stamp, out
	return out, config.Current(), nil
}

// configStamp is which config files there are and when each last changed.
func configStamp() string {
	src, project := config.Sources()
	var b strings.Builder
	b.WriteString(project)
	for _, p := range src.Paths() {
		if fi, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "\n%s %d %d", p, fi.Size(), fi.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "\n%s -", p)
		}
	}
	return b.String()
}

func (Live) Tunnels() []tunnel.Info             { return tunnel.List("") }
func (Live) Audit(n int) ([]audit.Entry, error) { return audit.Tail(n) }

func (Live) Log(id string) string { return tunnel.LogTail(id, 256<<10) } // gone: nothing to show

func contextOf(c *config.Context, summary string) Context {
	out := Context{Name: c.Name, Summary: summary, Cloud: c.Provider, Protected: c.Protected, ReadOnly: c.ReadOnly}
	for _, n := range slices.Sorted(maps.Keys(c.Targets)) {
		out.Targets = append(out.Targets, Target{n, c.Targets[n].Instance})
	}
	for _, n := range slices.Sorted(maps.Keys(c.Clusters)) {
		out.Clusters = append(out.Clusters, Cluster{n, c.Clusters[n].Name})
	}
	for _, n := range slices.Sorted(maps.Keys(c.Tunnels)) {
		t := c.Tunnels[n]
		out.Tunnels = append(out.Tunnels, Tunnel{n, remote(t), t.Local()})
	}
	return out
}

// remote describes where a tunnel goes, like `mek tunnel` does.
func remote(t *config.Tunnel) string {
	switch {
	case t.CloudSQL != "":
		return "Cloud SQL " + t.CloudSQL
	case t.Host != "" && t.Via == "":
		return fmt.Sprintf("%s:%d via Bastion", t.Host, t.Port)
	case t.Host != "":
		return fmt.Sprintf("%s:%d via %s", t.Host, t.Port, t.Via)
	}
	return fmt.Sprintf("port %d on %s", t.Port, t.Via)
}
