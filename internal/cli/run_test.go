package cli

import (
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
	"github.com/prateep-r/mek/internal/provider"
)

func TestTakeGlobalFlags(t *testing.T) {
	cases := []struct {
		in, rest, ctx, confirm string
		yes                    bool
	}{
		{"s3 ls", "s3 ls", "", "", false},
		{"-c uat s3 ls", "s3 ls", "uat", "", false},
		{"--context=uat -y ec2 describe-instances", "ec2 describe-instances", "uat", "", true},
		{"--confirm prod -c prod ec2 terminate-instances --yes", "ec2 terminate-instances --yes", "prod", "prod", false},
		{"--region x s3 ls", "--region x s3 ls", "", "", false}, // aws flags are left alone
	}
	for _, c := range cases {
		a := &app{}
		rest, err := a.takeGlobalFlags(strings.Fields(c.in))
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if strings.Join(rest, " ") != c.rest || a.opts.context != c.ctx || a.opts.confirm != c.confirm || a.opts.yes != c.yes {
			t.Errorf("%q -> rest=%q opts=%+v", c.in, rest, a.opts)
		}
	}
	if _, err := (&app{}).takeGlobalFlags([]string{"-c"}); err == nil {
		t.Error("expected error for -c without value")
	}
}

// Clouds may lack a capability: commands that need it say which.
func TestCapability(t *testing.T) {
	l := &loaded{ctx: &config.Context{Provider: "bare"}, prov: describer{}}
	if _, err := capability[provider.KubeProvider](l, "mek kube"); err == nil || err.Error() != "mek kube doesn't support bare yet" {
		t.Errorf("missing capability: %v", err)
	}
	l.prov = &provider.GCP{}
	if _, err := capability[provider.Tunneler](l, "mek tunnel"); err != nil {
		t.Errorf("gcp tunnels: %v", err)
	}
}

// bareCloud has no optional capability; tunnelOnly tunnels via instances it
// can't resolve (no Sessioner).
type bareCloud struct{ describer }
type tunnelOnly struct{ describer }

func (tunnelOnly) TunnelMethod(config.Tunnel) (provider.TunnelMethod, error) { return viaMethod{}, nil }

type viaMethod struct{}

func (viaMethod) Via() string { return "vm-1" }
func (viaMethod) Command(provider.Instance, int) (provider.Command, error) {
	return provider.Command{}, nil
}
func (viaMethod) Class() guard.Class { return guard.Tunnel }
func (viaMethod) Remote() string     { return "x" }

func TestCommandsNeedCapabilities(t *testing.T) {
	for name, p := range map[string]provider.Provider{"bare": bareCloud{}, "tunnelonly": tunnelOnly{}} {
		t.Cleanup(provider.Register(provider.Cloud{Name: name, CLI: name, Title: name, Classify: guard.ClassifyAWS,
			Validate: func(*config.Context) error { return nil },
			New:      func(*config.Config, *config.Context, string) provider.Provider { return p }}))
	}
	h := newHarness(t, "contexts:\n  b: {provider: bare}\n  t: {provider: tunnelonly}\n")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", "b", "kube", "--name", "x"}, "mek kube doesn't support bare yet"},
		{[]string{"--context", "b", "kube", "token", "--name", "x", "--location", "y"}, "mek kube doesn't support bare yet"},
		{[]string{"-c", "b", "shell", "x"}, "mek shell doesn't support bare yet"},
		{[]string{"-c", "b", "tunnel", "--via", "x", "--to", ":1"}, "mek tunnel doesn't support bare yet"},
		{[]string{"-c", "t", "tunnel", "--via", "vm-1", "--to", ":1"}, "mek tunnel --via doesn't support tunnelonly yet"},
	} {
		_, err := h.run(c.args...)
		wantErr(t, err, c.want)
	}
}
