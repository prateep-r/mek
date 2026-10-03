package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/prateep-r/mek/internal/config"
)

var (
	_ Sessioner = (*GCP)(nil)

	gcpVMName = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

func (g *GCP) ResolveTarget(spec string, o TargetOptions, q Query) (Instance, error) {
	if o.ResourceGroup != "" {
		return Instance{}, errors.New("--resource-group is for azure; gcp VMs take --zone")
	}
	return chain(unresolved{"use a name under targets: or a VM name"},
		configured(g.ctx.Targets), g.vmLookup(q),
	).resolve(spec, o)
}

// vmLookup takes a VM name; without a zone it finds the one running VM of
// that name in the project.
func (g *GCP) vmLookup(q Query) link {
	return func(spec string, o TargetOptions, next resolver) (Instance, error) {
		if !gcpVMName.MatchString(spec) {
			return next.resolve(spec, o)
		}
		in := Instance{ID: spec, Zone: o.Zone, User: o.User}
		if in.Zone != "" {
			return in, nil
		}
		out, err := q([]string{"gcloud", "compute", "instances", "list",
			"--filter", fmt.Sprintf("name=(%q)", spec), "--format", "json(name,zone,status)"})
		if err != nil {
			return Instance{}, err
		}
		var vms []struct{ Name, Zone, Status string }
		if err := json.Unmarshal(out, &vms); err != nil {
			return Instance{}, fmt.Errorf("compute instances list: %w", err)
		}
		var zones []string
		for _, vm := range vms {
			if vm.Name == spec && vm.Status == "RUNNING" {
				zones = append(zones, path.Base(vm.Zone)) // zone comes as a URL
			}
		}
		if len(zones) != 1 {
			return Instance{}, fmt.Errorf("VM %s: %d running in the project (%s) — pass --zone", spec, len(zones), strings.Join(zones, ", "))
		}
		in.Zone = zones[0]
		return in, nil
	}
}

// sshDir is the context's own SSH home: gcloud's key and known_hosts live
// there instead of ~/.ssh.
func (g *GCP) sshDir() string { return filepath.Join(g.dir, "ssh", g.ctx.Name) }

// ShellCommand runs gcloud compute ssh through IAP. HOME points at the
// context's SSH dir, because gcloud always tells ssh to keep known hosts in
// ~/.ssh/google_compute_known_hosts (CLOUDSDK_CONFIG keeps gcloud itself on
// the context's config).
func (g *GCP) ShellCommand(in Instance) (Command, error) {
	b, err := g.sshBuilder(in)
	if err != nil {
		return Command{}, err
	}
	return b.build(), nil
}

// sshBuilder starts the gcloud compute ssh command shared by shells and
// SSH-hop tunnels.
func (g *GCP) sshBuilder(in Instance) (*cmdBuilder, error) {
	dir := g.sshDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	host := in.ID
	if in.User != "" {
		host = in.User + "@" + in.ID
	}
	return command("gcloud", "compute", "ssh", host).opt("--zone", in.Zone).args("--tunnel-through-iap").
		opt("--ssh-key-file", filepath.Join(dir, "google_compute_engine")).env("HOME", dir), nil
}

var _ Tunneler = (*GCP)(nil)

func (g *GCP) TunnelMethod(t config.Tunnel) (TunnelMethod, error) {
	h := hop{via: t.Via, port: t.Port}
	switch {
	case t.CloudSQL != "":
		return cloudSQL{conn: t.CloudSQL, privateIP: t.PrivateIP, ctx: g.ctx.Name,
			adc: filepath.Join(g.configDir(), "application_default_credentials.json")}, nil
	case t.Via == "":
		return nil, errors.New("needs via: the VM to go through")
	case t.Host != "":
		return iapSSH{h, t.Host, g.sshBuilder}, nil
	}
	return iapPort{h}, nil
}
