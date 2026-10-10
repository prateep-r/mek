package provider

import "github.com/prateep-r/mek/internal/config"

// fromConfig turns a name under the context's targets into its instance
// spec, with the config's zone and user unless flags gave them; anything
// else (an instance id, tag or VM name) passes through with no alias.
func fromConfig(targets map[string]*config.Target, spec string, o TargetOptions) (alias, instance string, _ TargetOptions) {
	t, ok := targets[spec]
	if !ok {
		return "", spec, o
	}
	if o.Zone == "" {
		o.Zone = t.Zone
	}
	if o.User == "" {
		o.User = t.User
	}
	return spec, t.Instance, o
}
