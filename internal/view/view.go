// Package view is what a user interface shows of mek: the contexts with
// their targets, clusters and tunnels, the tunnels running, the audit log.
// mek ui (internal/tui) and mek-desktop read it the same way.
package view

import (
	"fmt"
	"maps"
	"os"
	"slices"

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

// Source is what a UI shows. Live reads mek's own files; tests use a fake.
type Source interface {
	Contexts() (list []Context, current string, err error)
	Tunnels() []tunnel.Info
	Audit(n int) ([]audit.Entry, error)
	Log(id string) string
}

// Live is the Source of a real mek: its config, tunnels and audit log.
type Live struct{}

func (Live) Contexts() ([]Context, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", err
	}
	var out []Context
	for _, name := range cfg.Names() {
		c := cfg.Contexts[name]
		out = append(out, contextOf(c, provider.For(cfg, c).Describe()))
	}
	return out, config.Current(), nil
}

func (Live) Tunnels() []tunnel.Info             { return tunnel.List("") }
func (Live) Audit(n int) ([]audit.Entry, error) { return audit.Tail(n) }

func (Live) Log(id string) string {
	b, _ := os.ReadFile(tunnel.LogPath(id)) // gone: nothing to show
	return string(b)
}

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
