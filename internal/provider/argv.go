package provider

import "maps"

// cmdBuilder assembles a Command step by step (a GoF Builder): optional
// parts are skipped when empty, so callers never splice argv by hand.
type cmdBuilder struct{ c Command }

func command(argv ...string) *cmdBuilder { return &cmdBuilder{Command{Argv: argv}} }

func (b *cmdBuilder) args(a ...string) *cmdBuilder {
	b.c.Argv = append(b.c.Argv, a...)
	return b
}

// opt appends `flag value`, unless value is empty.
func (b *cmdBuilder) opt(flag, value string) *cmdBuilder {
	if value != "" {
		b.c.Argv = append(b.c.Argv, flag, value)
	}
	return b
}

func (b *cmdBuilder) env(k, v string) *cmdBuilder {
	if b.c.Env == nil {
		b.c.Env = map[string]string{}
	}
	b.c.Env[k] = v
	return b
}

func (b *cmdBuilder) needs(bin string) *cmdBuilder {
	b.c.Requires = append(b.c.Requires, bin)
	return b
}

func (b *cmdBuilder) build() Command {
	c := b.c
	c.Argv = append([]string(nil), c.Argv...)
	c.Env = maps.Clone(c.Env)
	c.Requires = append([]string(nil), c.Requires...)
	return c
}
