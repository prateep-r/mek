package provider

import (
	"fmt"

	"github.com/prateep-r/mek/internal/config"
)

// resolver turns a target spec into an instance. Resolvers form a GoF Chain
// of Responsibility: each link handles the specs it recognizes and hands the
// rest on, and every cloud wires its own chain (configured names, instance
// ids, tag or VM-name lookups).
type resolver interface {
	resolve(spec string, o TargetOptions) (Instance, error)
}

// link is one step of a chain; next is the rest of it.
type link func(spec string, o TargetOptions, next resolver) (Instance, error)

type chained struct {
	step link
	next resolver
}

func (c chained) resolve(spec string, o TargetOptions) (Instance, error) {
	return c.step(spec, o, c.next)
}

// unresolved ends a chain: nothing recognized the spec.
type unresolved struct{ hint string }

func (u unresolved) resolve(spec string, _ TargetOptions) (Instance, error) {
	return Instance{}, fmt.Errorf("unknown target %q — %s", spec, u.hint)
}

// chain links steps in order, ending with end.
func chain(end resolver, steps ...link) resolver {
	r := end
	for i := len(steps) - 1; i >= 0; i-- {
		r = chained{steps[i], r}
	}
	return r
}

// configured resolves a name under the context's targets: its instance goes
// down the rest of the chain with the config's zone and user, which flags
// override.
func configured(targets map[string]*config.Target) link {
	return func(spec string, o TargetOptions, next resolver) (Instance, error) {
		t, ok := targets[spec]
		if !ok {
			return next.resolve(spec, o)
		}
		if o.Zone == "" {
			o.Zone = t.Zone
		}
		if o.User == "" {
			o.User = t.User
		}
		in, err := next.resolve(t.Instance, o)
		in.Alias = spec
		return in, err
	}
}
