package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Cluster is a Kubernetes cluster reachable from a context (mek kube).
type Cluster struct {
	Name          string `yaml:"name"`                     // the cloud's cluster name
	Region        string `yaml:"region,omitempty"`         // aws: defaults to the context's region
	Location      string `yaml:"location,omitempty"`       // gcp: zone or region
	ResourceGroup string `yaml:"resource_group,omitempty"` // azure
	Namespace     string `yaml:"namespace,omitempty"`      // default namespace in the kubeconfig
}

// Bastion is an Azure Bastion host (Standard SKU or above, native client
// support on).
type Bastion struct {
	Name          string `yaml:"name"`
	ResourceGroup string `yaml:"resource_group"`
}

// Target auth values (azure).
const (
	AuthAAD    = "aad"     // Microsoft Entra ID login: no key at all (the default)
	AuthSSHKey = "ssh-key" // the context's key in <MEK_HOME>/ssh/<context>
)

// Target is a host reachable from a context (mek shell, tunnel hops).
type Target struct {
	Instance      string   `yaml:"instance"`                 // aws: i-… or tag:Key=Value; gcp, azure: VM name (azure: or resource id)
	Zone          string   `yaml:"zone,omitempty"`           // gcp: looked up when empty
	ResourceGroup string   `yaml:"resource_group,omitempty"` // azure: looked up when empty
	User          string   `yaml:"user,omitempty"`           // gcp, azure: SSH user
	Auth          string   `yaml:"auth,omitempty"`           // azure: aad (default) or ssh-key
	Bastion       *Bastion `yaml:"bastion,omitempty"`        // azure: instead of the context's
}

// Tunnel is a local port forwarded to a private host (mek tunnel). Which
// way it goes follows from the fields set: cloudsql → Cloud SQL Auth Proxy;
// via alone → a port on that instance; via and host → another host,
// reached through the instance; host alone → (azure) Bastion connects to it.
type Tunnel struct {
	Via       string `yaml:"via,omitempty"`        // a target name or instance spec
	Host      string `yaml:"host,omitempty"`       // remote host: through via, or (azure) from Bastion directly
	Port      int    `yaml:"port,omitempty"`       // remote port
	LocalPort int    `yaml:"local_port,omitempty"` // default: port + 10000
	CloudSQL  string `yaml:"cloudsql,omitempty"`   // gcp: PROJECT:REGION:INSTANCE
	PrivateIP bool   `yaml:"private_ip,omitempty"` // gcp: Cloud SQL over its private IP
}

// Local is the local port: local_port, or the remote port + 10000.
func (t *Tunnel) Local() int {
	if t.LocalPort != 0 {
		return t.LocalPort
	}
	return t.Port + 10000
}

// Validate checks a tunnel's shape; each cloud adds which kinds it supports.
func (t *Tunnel) Validate() error {
	if err := argValues("", field{"via", t.Via}, field{"host", t.Host}, field{"cloudsql", t.CloudSQL}); err != nil {
		return err
	}
	if t.LocalPort < 0 || t.LocalPort > 65535 {
		return fmt.Errorf("local_port %d is not a port (1-65535)", t.LocalPort)
	}
	if t.CloudSQL != "" {
		if t.Via != "" || t.Host != "" || t.Port != 0 {
			return errors.New("cloudsql tunnels take only local_port and private_ip")
		}
		if t.LocalPort == 0 {
			return errors.New("cloudsql tunnels need local_port")
		}
		return nil
	}
	if t.PrivateIP {
		return errors.New("private_ip is only for cloudsql tunnels")
	}
	if t.Via == "" && t.Host == "" {
		return errors.New("needs via (a target), host or cloudsql")
	}
	if t.Port < 1 || t.Port > 65535 {
		return fmt.Errorf("port %d is not a port (1-65535)", t.Port)
	}
	if t.Local() > 65535 {
		return fmt.Errorf("port %d + 10000 is not a port: set local_port", t.Port)
	}
	return nil
}

func (b *Bastion) validate(key string) error {
	if b == nil {
		return nil
	}
	if b.Name == "" || b.ResourceGroup == "" {
		return fmt.Errorf("%s needs name and resource_group", key)
	}
	return argValues(key, field{"name", b.Name}, field{"resource_group", b.ResourceGroup})
}

// Names that would clash with subcommands (`mek kube token`, `mek tunnel ls`).
var (
	reservedClusterNames = map[string]bool{"token": true}
	reservedTargetNames  = map[string]bool{"ls": true, "list": true, "stop": true, "logs": true}
	reservedTunnelNames  = reservedTargetNames // `mek tunnel ls`
)

type field struct{ key, val string }

// argValues checks every field of an entry with argValue; errors name the
// field as prefix.key (or key alone).
func argValues(prefix string, fs ...field) error {
	for _, f := range fs {
		if err := argValue(f.val); err != nil {
			key := f.key
			if prefix != "" {
				key = prefix + "." + key
			}
			return fmt.Errorf("%s %w", key, err)
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
			field{"location", cl.Location}, field{"resource_group", cl.ResourceGroup}, field{"namespace", cl.Namespace}); err != nil {
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
		if err := argValues("targets."+alias, field{"instance", t.Instance}, field{"zone", t.Zone},
			field{"resource_group", t.ResourceGroup}, field{"user", t.User}); err != nil {
			return err
		}
		switch t.Auth {
		case "", AuthAAD, AuthSSHKey:
		default:
			return fmt.Errorf("targets.%s.auth must be %s or %s", alias, AuthAAD, AuthSSHKey)
		}
		if t.Auth == AuthSSHKey && t.User == "" {
			return fmt.Errorf("targets.%s: auth ssh-key needs a user", alias)
		}
		if err := t.Bastion.validate("targets." + alias + ".bastion"); err != nil {
			return err
		}
	}
	if err := c.Bastion.validate("bastion"); err != nil {
		return err
	}
	for alias, t := range c.Tunnels {
		if err := validateAlias("tunnels", alias, reservedTunnelNames); err != nil {
			return err
		}
		if t == nil {
			return fmt.Errorf("tunnels.%s is empty", alias)
		}
		if err := t.Validate(); err != nil {
			return fmt.Errorf("tunnels.%s: %w", alias, err)
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
