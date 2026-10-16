package config

import (
	"errors"
	"io/fs"
	"os"
)

// Source is where contexts come from — a GoF Strategy: a YAML file today,
// maybe an organisation's shared config later. Layers is a Source made of
// Sources — a GoF Composite — so a project's two files act as one layer,
// and a new layer is one more Source in the list.
type Source interface {
	Read() (*Config, error)
}

// File is a config file. A missing file is no contexts, and no entry in
// Config.Files.
type File struct {
	Path      string
	InProject bool // a project's mek.yaml or mek.local.yaml
}

func (f File) Read() (*Config, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Contexts: map[string]*Context{}}, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := decode(b, f.Path, f.InProject)
	if err != nil {
		return nil, err
	}
	c.Files = []string{f.Path}
	return c, nil
}

// Layers merges its Sources, the first winning. A context comes whole from
// the highest layer that has it — fields are never mixed across files —
// with one exception: protected and readonly from any lower layer stay on,
// so a repository's mek.yaml can't switch off a guard you set.
type Layers []Source

func (l Layers) Read() (*Config, error) {
	out := &Config{Contexts: map[string]*Context{}}
	for _, s := range l {
		c, err := s.Read()
		if err != nil {
			return nil, err
		}
		out.Files = append(out.Files, c.Files...)
		if c.Settings != nil { // only ~/.mek/config.yaml may have them
			out.Settings = c.Settings
		}
		for name, ctx := range c.Contexts {
			won, ok := out.Contexts[name]
			if !ok {
				out.Contexts[name] = ctx
				continue
			}
			won.Protected = won.Protected || ctx.Protected
			won.ReadOnly = won.ReadOnly || ctx.ReadOnly
			if won.Shadowed == "" {
				won.Shadowed = ctx.Source
			}
		}
	}
	return out, nil
}
