package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/prateep-r/mek/internal/config"
)

var (
	_ Sessioner = (*AWS)(nil)

	awsInstanceID = regexp.MustCompile(`^m?i-[0-9a-f]{8,17}$`) // EC2 and SSM managed instances
)

// awsTargetSpec checks a configured instance: an id or tag:Key=Value.
func awsTargetSpec(s string) error {
	if awsInstanceID.MatchString(s) {
		return nil
	}
	if _, _, err := parseTag(s); err != nil {
		return err
	}
	return nil
}

func parseTag(s string) (key, value string, err error) {
	kv, ok := strings.CutPrefix(s, "tag:")
	if !ok {
		return "", "", fmt.Errorf("%q is not an instance id (i-…) or tag:Key=Value", s)
	}
	key, value, _ = strings.Cut(kv, "=")
	if key == "" || value == "" {
		return "", "", fmt.Errorf("%q: tag targets look like tag:Key=Value", s)
	}
	return key, value, nil
}

func (a *AWS) ResolveTarget(spec string, o TargetOptions, q Query) (Instance, error) {
	if o.Zone != "" || o.User != "" {
		return Instance{}, errors.New("--zone and --user are for gcp; SSM sessions need neither")
	}
	alias, spec, _ := fromConfig(a.ctx.Targets, spec, o)
	var in Instance
	var err error
	switch {
	case awsInstanceID.MatchString(spec):
		in = Instance{ID: spec}
	case strings.HasPrefix(spec, "tag:"):
		in, err = a.tagLookup(spec, q)
	default:
		return Instance{}, fmt.Errorf("unknown target %q — use a name under targets:, an instance id (i-…) or tag:Key=Value", spec)
	}
	in.Alias = alias
	return in, err
}

// tagLookup finds the one running instance with a tag. The filter goes as
// JSON, so commas or spaces in a tag value can't split it.
func (a *AWS) tagLookup(spec string, q Query) (Instance, error) {
	key, value, err := parseTag(spec)
	if err != nil {
		return Instance{}, err
	}
	filters, _ := json.Marshal([]map[string]any{ // plain strings always marshal
		{"Name": "tag:" + key, "Values": []string{value}},
		{"Name": "instance-state-name", "Values": []string{"running"}},
	})
	out, err := q([]string{"aws", "ec2", "describe-instances", "--filters", string(filters),
		"--query", "Reservations[].Instances[].InstanceId", "--output", "json"})
	if err != nil {
		return Instance{}, err
	}
	var ids []string
	if err := json.Unmarshal(out, &ids); err != nil {
		return Instance{}, fmt.Errorf("ec2 describe-instances: %w", err)
	}
	if len(ids) != 1 {
		return Instance{}, fmt.Errorf("%s matches %d running instances, want exactly 1 (%s)", spec, len(ids), strings.Join(ids, ", "))
	}
	return Instance{ID: ids[0]}, nil
}

func (a *AWS) ShellCommand(in Instance) (Command, error) {
	return command("aws", "ssm", "start-session").opt("--target", in.ID).needs("session-manager-plugin").build(), nil
}

var _ Tunneler = (*AWS)(nil)

func (a *AWS) TunnelMethod(t config.Tunnel) (TunnelMethod, error) {
	h := hop{via: t.Via, port: t.Port}
	switch {
	case t.CloudSQL != "":
		return nil, errors.New("cloudsql tunnels are gcp only")
	case t.Via == "":
		return nil, errors.New("needs via: the instance to go through")
	case t.Host != "":
		return ssmRemoteHost{h, t.Host}, nil
	}
	return ssmPort{h}, nil
}
