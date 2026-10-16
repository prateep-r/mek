package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

const huaweiConfig = `contexts:
  hw:  {provider: huawei, hcloud_profile: sso-prod, region: ap-southeast-2}
  hwk: {provider: huawei, hcloud_profile: keys}
  hwx: {provider: huawei, hcloud_profile: nope, readonly: true}
`

// kooCLI writes KooCLI's config with an SSO and a key profile.
func kooCLI(t *testing.T) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(cfg, []byte(`{"profiles":[{"name":"sso-prod","mode":"SSO","region":"ap-southeast-2"},{"name":"keys","mode":"AKSK"}]}`), 0o600)
	t.Setenv("MEK_HCLOUD_CONFIG", cfg)
	return cfg
}

func TestHuaweiLogin(t *testing.T) {
	h := newHarness(t, huaweiConfig)
	kooCLI(t)
	h.mustRun("login", "hw")
	if got := h.exec.argv(); !slices.Equal(got, []string{"hcloud configure sso --cli-profile=sso-prod", "hcloud configure show --cli-profile=sso-prod"}) {
		t.Errorf("sso login ran %q", got)
	}
	h.exec.invs = nil
	out := stderr(t, func() { h.mustRun("login", "hwk", "--", "--ignored") })
	if got := h.exec.argv(); !slices.Equal(got, []string{"hcloud configure show --cli-profile=keys"}) || !strings.Contains(out, "hwk has no login step") {
		t.Errorf("keys: ran %q, said %s", got, out)
	}
	h.exec.invs = nil
	_, err := h.run("login", "hwx")
	wantErr(t, err, `KooCLI profile "nope" not found`)
	if len(h.exec.invs) != 0 {
		t.Errorf("ran %q", h.exec.argv())
	}
}

func TestHuaweiPassthrough(t *testing.T) {
	h := newHarness(t, huaweiConfig)
	h.mustRun("-c", "hw", "hcloud", "ECS", "ListServersDetails")
	inv := h.exec.invs[0]
	if got := strings.Join(inv.Argv, " "); got != "hcloud ECS ListServersDetails --cli-profile=sso-prod --cli-region=ap-southeast-2" ||
		!slices.Contains(inv.Env, "HW_PROFILE=sso-prod") {
		t.Errorf("hcloud: %s %v", got, inv.Env)
	}
	_, err := h.run("-c", "hwx", "hcloud", "ECS", "DeleteServers", "--servers.1.id=x")
	wantErr(t, err, "hwx is readonly — blocked destructive command: hcloud ECS DeleteServers --servers.1.id=x")

	for _, args := range [][]string{{"-c", "hw", "shell", "vm"}, {"-c", "hw", "tunnel", "--to", "10.0.0.1:22"}, {"-c", "hw", "kube"}} {
		if _, err := h.run(args...); err == nil {
			t.Errorf("%v worked on huawei", args)
		}
	}
	answering(t) // a terminal, and no form
	_, err = h.run("-c", "hw", "ctx", "tunnel", "add")
	wantErr(t, err, "hw is a huawei context: those have no tunnels")
}

func TestCtxAddHuawei(t *testing.T) {
	h := newHarness(t, testConfig)
	kooCLI(t)
	screens := answering(t, []string{"type:hw", "enter", "down", "down", "down", "enter", // Huawei Cloud
		"down", "enter", // keys
		"type:ap-southeast-3", "enter", "enter", "enter"})
	h.mustRun("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "✓ KooCLI profile: keys") {
		t.Errorf("answers:\n%s", s)
	}
	if got := readFile(t, config.Path()); !strings.Contains(got, "hw:\n    provider: huawei\n    region: ap-southeast-3\n    hcloud_profile: keys\n") {
		t.Errorf("file:\n%s", got)
	}
	answering(t, []string{"enter", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("ctx", "edit", "hw")

	// No KooCLI config: the step says so, and takes a typed name.
	t.Setenv("MEK_HCLOUD_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	screens = answering(t, []string{"type:hw2", "enter", "down", "down", "down", "enter"})
	h.run("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "couldn't list them: no KooCLI config at") {
		t.Errorf("no config:\n%s", s)
	}
}
