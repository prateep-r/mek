package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

var (
	_ Sessioner = (*Azure)(nil)
	_ Tunneler  = (*Azure)(nil)

	azureVMID   = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Compute/virtualMachines/[^/]+$`)
	azureVMName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
)

// userHome finds the user's ~/.azure (a test seam).
var userHome = os.UserHomeDir

// extensionDir is where az keeps extensions for mek's contexts: the user's
// own (AZURE_EXTENSION_DIR or ~/.azure/cliextensions), so an extension the
// user added once works in every context — extensions are code, not
// credentials, unlike the per-context AZURE_CONFIG_DIR.
func extensionDir() (string, error) {
	if d := os.Getenv("AZURE_EXTENSION_DIR"); d != "" {
		return d, nil
	}
	home, err := userHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".azure", "cliextensions"), nil
}

// needExtensions fails, with the command to fix it, when az extensions are
// missing. mek never installs code itself.
func needExtensions(names ...string) error {
	dir, err := extensionDir()
	if err != nil {
		return err
	}
	var missing []string
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("az extension %s is needed — install it: az extension add --name %s",
			strings.Join(missing, " and "), strings.Join(missing, " --name "))
	}
	return nil
}

func (z *Azure) ResolveTarget(spec string, o TargetOptions, q Query) (Instance, error) {
	if o.Zone != "" {
		return Instance{}, errors.New("--zone is for gcp; azure VMs take --resource-group")
	}
	return chain(unresolved{"use a name under targets:, a VM resource id, or a VM name"},
		configured(z.ctx.Targets), azureLiteralID, z.vmLookup(q),
	).resolve(spec, o)
}

func azureLiteralID(spec string, o TargetOptions, next resolver) (Instance, error) {
	if !azureVMID.MatchString(spec) {
		return next.resolve(spec, o)
	}
	return Instance{ID: spec, User: o.User}, nil
}

// vmLookup turns a VM name into its resource id: in its resource group,
// or the one VM of that name in the subscription.
func (z *Azure) vmLookup(q Query) link {
	return func(spec string, o TargetOptions, next resolver) (Instance, error) {
		if !azureVMName.MatchString(spec) {
			return next.resolve(spec, o)
		}
		argv := []string{"az", "vm", "list", "--query", fmt.Sprintf("[?name=='%s'].id", spec), "--output", "json"}
		if o.ResourceGroup != "" {
			argv = append(argv, "--resource-group", o.ResourceGroup)
		}
		out, err := q(argv)
		if err != nil {
			return Instance{}, err
		}
		var ids []string
		if err := json.Unmarshal(out, &ids); err != nil {
			return Instance{}, fmt.Errorf("vm list: %w", err)
		}
		if len(ids) != 1 {
			return Instance{}, fmt.Errorf("VM %s: %d found (%s) — pass --resource-group", spec, len(ids), strings.Join(ids, ", "))
		}
		return Instance{ID: ids[0], User: o.User}, nil
	}
}

// bastion is the Bastion an instance goes through: its own, else the context's.
func (z *Azure) bastion(in Instance) (*config.Bastion, error) {
	if in.Bastion != nil {
		return in.Bastion, nil
	}
	if z.ctx.Bastion != nil {
		return z.ctx.Bastion, nil
	}
	return nil, fmt.Errorf("azure shells and tunnels go through Azure Bastion: set bastion: {name, resource_group} on context %s", z.ctx.Name)
}

func bastionCommand(verb string, b *config.Bastion) *cmdBuilder {
	return command("az", "network", "bastion", verb).opt("--name", b.Name).opt("--resource-group", b.ResourceGroup)
}

// sshKey is the context's key for `auth: ssh-key` targets.
func (z *Azure) sshKey() string { return filepath.Join(z.dir, "ssh", z.ctx.Name, "id_ed25519") }

// ShellCommand opens a shell through Azure Bastion, with an Entra ID login
// (no key) or the context's SSH key. Bastion's ssh keeps no known hosts.
func (z *Azure) ShellCommand(in Instance) (Command, error) {
	b, err := z.bastion(in)
	if err != nil {
		return Command{}, err
	}
	c := bastionCommand("ssh", b).opt("--target-resource-id", in.ID)
	if in.Auth == config.AuthSSHKey {
		if err := needExtensions("bastion"); err != nil {
			return Command{}, err
		}
		key := z.sshKey()
		if _, err := os.Stat(key); err != nil {
			return Command{}, fmt.Errorf("auth ssh-key uses %s — create it with: ssh-keygen -t ed25519 -f %s, then add %s.pub to the VM", key, key, key)
		}
		return c.opt("--auth-type", "ssh-key").opt("--username", in.User).opt("--ssh-key", key).build(), nil
	}
	if err := needExtensions("bastion", "ssh"); err != nil {
		return Command{}, err
	}
	return c.opt("--auth-type", "AAD").build(), nil
}

func (z *Azure) TunnelMethod(t config.Tunnel) (TunnelMethod, error) {
	switch {
	case t.CloudSQL != "":
		return nil, errors.New("cloudsql tunnels are gcp only")
	case t.Via != "" && t.Host != "":
		return nil, errors.New("azure Bastion reaches another host by itself: drop via, keep host (an IP)")
	case t.Host != "":
		if net.ParseIP(t.Host) == nil {
			return nil, fmt.Errorf("host %q: Bastion connects to an IP address, not a name", t.Host)
		}
		if z.ctx.Bastion == nil {
			return nil, errors.New("tunnels to a host need the context's bastion: {name, resource_group}")
		}
		return bastionIP{bastion: z.ctx.Bastion, host: t.Host, port: t.Port}, nil
	}
	return bastionPort{hop{via: t.Via, port: t.Port}, z.bastion}, nil
}

// bastionPort forwards to a port on a VM through Azure Bastion.
type bastionPort struct {
	hop
	bastion func(Instance) (*config.Bastion, error)
}

func (m bastionPort) Command(in Instance, local int) (Command, error) {
	b, err := m.bastion(in)
	if err != nil {
		return Command{}, err
	}
	if err := needExtensions("bastion"); err != nil {
		return Command{}, err
	}
	return bastionCommand("tunnel", b).opt("--target-resource-id", in.ID).
		opt("--resource-port", m.portArg()).opt("--port", strconv.Itoa(local)).build(), nil
}

// bastionIP has Bastion connect to an IP in its network (IP-based
// connection): no VM in between, so it stays a tunnel.
type bastionIP struct {
	bastion *config.Bastion
	host    string
	port    int
}

func (m bastionIP) Via() string        { return "" }
func (m bastionIP) Class() guard.Class { return guard.Tunnel }
func (m bastionIP) Remote() string {
	return m.host + ":" + strconv.Itoa(m.port) + " via Bastion " + m.bastion.Name
}

func (m bastionIP) Command(_ Instance, local int) (Command, error) {
	if err := needExtensions("bastion"); err != nil {
		return Command{}, err
	}
	return bastionCommand("tunnel", m.bastion).opt("--target-ip-address", m.host).
		opt("--resource-port", strconv.Itoa(m.port)).opt("--port", strconv.Itoa(local)).build(), nil
}
