package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

// TunnelMethod is one way to forward a local port — a GoF Strategy. The
// ways differ in their command, the plugins they need and their guard class
// (an SSH hop logs in to the instance, so it is a shell).
type TunnelMethod interface {
	// Via is the instance spec the tunnel goes through ("" for none).
	Via() string
	// Command forwards localhost:local, through in when Via is set.
	Command(in Instance, local int) (Command, error)
	Class() guard.Class
	// Remote names the far end for messages, e.g. "mydb:5432".
	Remote() string
}

// Tunneler is implemented by providers that `mek tunnel` supports. Its
// TunnelMethod is a Factory Method: the kind of tunnel follows from which
// fields are set, and is decided in one place.
type Tunneler interface {
	TunnelMethod(t config.Tunnel) (TunnelMethod, error)
}

// hop is the part every instance-based method shares.
type hop struct {
	via  string
	port int
}

func (h hop) Via() string        { return h.via }
func (h hop) Class() guard.Class { return guard.Tunnel }
func (h hop) Remote() string     { return "port " + strconv.Itoa(h.port) + " on the instance" }
func (h hop) portArg() string    { return strconv.Itoa(h.port) }
func ssmParams(p map[string]string) string {
	m := map[string][]string{}
	for k, v := range p {
		m[k] = []string{v}
	}
	b, _ := json.Marshal(m) // strings always marshal
	return string(b)
}

// ssmPort forwards to a port on the instance itself.
type ssmPort struct{ hop }

func (m ssmPort) Command(in Instance, local int) (Command, error) {
	return command("aws", "ssm", "start-session").opt("--target", in.ID).
		opt("--document-name", "AWS-StartPortForwardingSession").
		opt("--parameters", ssmParams(map[string]string{"portNumber": m.portArg(), "localPortNumber": strconv.Itoa(local)})).
		needs("session-manager-plugin").build(), nil
}

// ssmRemoteHost forwards to another host (an RDS endpoint, say) through the instance.
type ssmRemoteHost struct {
	hop
	host string
}

func (m ssmRemoteHost) Remote() string { return m.host + ":" + m.portArg() }

func (m ssmRemoteHost) Command(in Instance, local int) (Command, error) {
	return command("aws", "ssm", "start-session").opt("--target", in.ID).
		opt("--document-name", "AWS-StartPortForwardingSessionToRemoteHost").
		opt("--parameters", ssmParams(map[string]string{"host": m.host, "portNumber": m.portArg(), "localPortNumber": strconv.Itoa(local)})).
		needs("session-manager-plugin").build(), nil
}

// iapPort forwards to a port on the VM through IAP TCP forwarding.
type iapPort struct{ hop }

func (m iapPort) Command(in Instance, local int) (Command, error) {
	return command("gcloud", "compute", "start-iap-tunnel", in.ID, m.portArg(),
		"--local-host-port=localhost:"+strconv.Itoa(local)).opt("--zone", in.Zone).build(), nil
}

// iapSSH reaches another host with `ssh -L` on the VM, through IAP. It logs
// in to the VM, so the guard treats it as a shell.
type iapSSH struct {
	hop
	host string
	ssh  func(Instance) (*cmdBuilder, error) // GCP.sshBuilder
}

func (m iapSSH) Class() guard.Class { return guard.Shell }
func (m iapSSH) Remote() string     { return m.host + ":" + m.portArg() }

func (m iapSSH) Command(in Instance, local int) (Command, error) {
	b, err := m.ssh(in)
	if err != nil {
		return Command{}, err
	}
	return b.args("--", "-N", "-L", fmt.Sprintf("127.0.0.1:%d:%s:%d", local, m.host, m.port)).build(), nil
}

// cloudSQL runs the Cloud SQL Auth Proxy, which authenticates with the
// context's application-default credentials (`mek login --adc`).
type cloudSQL struct {
	conn      string
	privateIP bool
	adc       string // the context's ADC file
	ctx       string
}

func (m cloudSQL) Via() string        { return "" }
func (m cloudSQL) Class() guard.Class { return guard.Tunnel }
func (m cloudSQL) Remote() string     { return "Cloud SQL " + m.conn }

func (m cloudSQL) Command(_ Instance, local int) (Command, error) {
	if _, err := os.Stat(m.adc); err != nil {
		return Command{}, fmt.Errorf("cloud-sql-proxy needs application-default credentials — run: mek login %s --adc", m.ctx)
	}
	b := command("cloud-sql-proxy", m.conn, "--port", strconv.Itoa(local), "--address", "127.0.0.1")
	if m.privateIP {
		b.args("--private-ip")
	}
	return b.needs("cloud-sql-proxy").build(), nil
}
