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

// Target is a host reachable from a context (mek shell, tunnel hops).
type Target struct {
	Instance string `yaml:"instance"`       // aws: i-… or tag:Key=Value; gcp: VM name
	Zone     string `yaml:"zone,omitempty"` // gcp: looked up when empty
	User     string `yaml:"user,omitempty"` // gcp: SSH user (default: gcloud's)
}

// Names that would clash with subcommands (`mek kube token`, `mek tunnel ls`).
var (
	reservedClusterNames = map[string]bool{"token": true}
	reservedTargetNames  = map[string]bool{"ls": true, "list": true, "stop": true, "logs": true}
)

type field struct{ key, val string }

// argValues checks every field of an entry with argValue.
func argValues(prefix string, fs ...field) error {
	for _, f := range fs {
		if err := argValue(f.val); err != nil {
			return fmt.Errorf("%s.%s %w", prefix, f.key, err)
		}
	}
	return nil
}

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
		if err := argValues("clusters."+alias, field{"name", cl.Name}, field{"region", cl.Region},
			field{"location", cl.Location}, field{"namespace", cl.Namespace}); err != nil {
			return err
		}
	}
	for alias, t := range c.Targets {
		if err := validateAlias("targets", alias, reservedTargetNames); err != nil {
			return err
		}
		if t == nil || t.Instance == "" {
			return fmt.Errorf("targets.%s needs an instance", alias)
		}
		if err := argValues("targets."+alias, field{"instance", t.Instance}, field{"zone", t.Zone}, field{"user", t.User}); err != nil {
			return err
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
