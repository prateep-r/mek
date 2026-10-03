package guard

import (
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

func TestClassifyKubectl(t *testing.T) {
	cases := map[string]Class{
		"":                                  Read,
		"get pods":                          Read,
		"-n kube-system get pods":           Read,
		"--context x -o yaml get deploy":    Read,
		"--namespace=prod get pods":         Read,
		"describe pod p":                    Read,
		"logs -f p -c app":                  Read,
		"top nodes":                         Read,
		"config use-context x":              Read,
		"auth can-i create pods":            Read,
		"auth whoami":                       Read,
		"auth reconcile -f r.yaml":          Write,
		"rollout status deploy/x":           Read,
		"rollout history deploy/x":          Read,
		"rollout restart deploy/x":          Write,
		"rollout undo deploy/x":             Destructive,
		"rollout":                           Write,
		"apply -f x.yaml":                   Write,
		"apply -f x.yaml --dry-run=server":  Read,
		"create secret generic s --dry-run": Read,
		"apply -f x.yaml --dry-run=none":    Write,
		"scale deploy/x --replicas 3":       Write,
		"cordon node-1":                     Write,
		"delete pod p":                      Destructive,
		"delete pod p --dry-run=client":     Read,
		"drain node-1":                      Destructive,
		"replace -f x.yaml":                 Write,
		"replace --force -f x.yaml":         Destructive,
		"exec -it p -- sh":                  Shell,
		"attach p":                          Shell,
		"debug node/n -it --image busybox":  Shell,
		"port-forward svc/db 5432":          Tunnel,
		"proxy":                             Tunnel,
		"delete --help":                     Read,
		"exec -h":                           Read,
		"frobnicate x":                      Write,
	}
	for in, want := range cases {
		if got := ClassifyKubectl(strings.Fields(in)); got != want {
			t.Errorf("kubectl %q = %s, want %s", in, got, want)
		}
	}
}

func TestDecideSessions(t *testing.T) {
	plain := &config.Context{}
	prot := &config.Context{Protected: true}
	ro := &config.Context{ReadOnly: true}
	cases := []struct {
		ctx  *config.Context
		c    Class
		want Decision
	}{
		{plain, Shell, Allow},
		{plain, Tunnel, Allow},
		{prot, Shell, Confirm},
		{prot, Tunnel, Confirm},
		{ro, Shell, Block},
		{ro, Tunnel, Allow},
		{&config.Context{Protected: true, ReadOnly: true}, Tunnel, Confirm},
	}
	for _, c := range cases {
		if got := Decide(c.ctx, c.c); got != c.want {
			t.Errorf("Decide(%+v,%s)=%d want %d", c.ctx, c.c, got, c.want)
		}
	}
	if Shell.String() != "shell" || Tunnel.String() != "tunnel" {
		t.Errorf("String: %s %s", Shell, Tunnel)
	}
}

func TestClassifySessionCommands(t *testing.T) {
	cases := []struct {
		classify func([]string) Class
		args     string
		want     Class
	}{
		{ClassifyAWS, "ssm start-session --target i-1", Shell},
		{ClassifyAWS, "ssm start-session --target i-1 --document-name AWS-StartPortForwardingSession", Tunnel},
		{ClassifyAWS, "ssm start-session --target i-1 --document-name=AWS-StartPortForwardingSessionToRemoteHost", Tunnel},
		{ClassifyAWS, "ssm start-session --target i-1 --document-name AWS-StartInteractiveCommand", Shell},
		{ClassifyAWS, "ssm send-command --document-name AWS-RunShellScript", Write},
		{ClassifyGCloud, "compute ssh vm-1 --zone z --tunnel-through-iap", Shell},
		{ClassifyGCloud, "beta compute ssh vm-1", Shell},
		{ClassifyGCloud, "compute start-iap-tunnel vm-1 22 --local-host-port=localhost:2222", Tunnel},
		{ClassifyGCloud, "compute scp a vm-1:b", Write},
	}
	for _, c := range cases {
		if got := c.classify(strings.Fields(c.args)); got != c.want {
			t.Errorf("%q = %s, want %s", c.args, got, c.want)
		}
	}
	if FlagValue([]string{"--document-name"}, "--document-name") != "" {
		t.Error("flag without a value")
	}
}
