package config

import (
	"fmt"
	"strings"
	"unicode"
)

// Cluster is a Kubernetes cluster reachable from a context (mek kube).
type Cluster struct {
	Name      string `yaml:"name"`                // the cloud's cluster name
	Region    string `yaml:"region,omitempty"`    // aws: defaults to the context's region
	Location  string `yaml:"location,omitempty"`  // gcp: zone or region
	Namespace string `yaml:"namespace,omitempty"` // default namespace in the kubeconfig
}

// Names that would clash with subcommands (`mek kube token`).
var reservedClusterNames = map[string]bool{"token": true}

// validateAccess checks the access sections every cloud shares; each cloud
// adds its own rules (provider.Cloud.Validate).
func (c *Context) validateAccess() error {
	for alias, cl := range c.Clusters {
		if err := validateAlias("clusters", alias, reservedClusterNames); err != nil {
			return err
		}
		if cl == nil || cl.Name == "" {
			return fmt.Errorf("clusters.%s needs a name", alias)
		}
		for _, f := range []struct{ key, val string }{
			{"name", cl.Name}, {"region", cl.Region}, {"location", cl.Location}, {"namespace", cl.Namespace},
		} {
			if err := argValue(f.val); err != nil {
				return fmt.Errorf("clusters.%s.%s %w", alias, f.key, err)
			}
		}
	}
	return nil
}

func validateAlias(section, alias string, reserved map[string]bool) error {
	if !validName.MatchString(alias) {
		return fmt.Errorf("%s: name %q may only contain letters, digits, '.', '_' and '-'", section, alias)
	}
	if reserved[alias] {
		return fmt.Errorf("%s: %q is reserved (it is a mek subcommand)", section, alias)
	}
	return nil
}

// argValue rejects values that would be unsafe as a CLI argument: control
// characters, and a leading "-" that the CLI would read as a flag.
func argValue(v string) error {
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("must not start with '-'")
	}
	if strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return fmt.Errorf("must not contain control characters")
	}
	return nil
}
