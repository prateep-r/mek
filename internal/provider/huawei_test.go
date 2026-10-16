package provider

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

// hcloudConfig points KooCLI's config at a file with body ("" : none).
func hcloudConfig(t *testing.T, body string) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.json")
	if body != "" {
		os.WriteFile(cfg, []byte(body), 0o600)
	}
	old := HcloudConfigPath
	HcloudConfigPath = func() string { return cfg }
	t.Cleanup(func() { HcloudConfigPath = old })
	return cfg
}

func TestHuawei(t *testing.T) {
	h := &Huawei{ctx: &config.Context{Name: "hw", Provider: config.ProviderHuawei, HcloudProfile: "sso-prod", Region: "ap-southeast-2"}}

	env, _ := h.Prepare()
	if env.Set["HW_PROFILE"] != "sso-prod" || env.Set["HW_REGION_NAME"] != "ap-southeast-2" || !strings.Contains(strings.Join(env.Unset, " "), "HW_SECRET_KEY") {
		t.Errorf("env: %+v", env)
	}
	rewrite := map[string]string{
		"ECS ListServersDetails":                         "ECS ListServersDetails --cli-profile=sso-prod --cli-region=ap-southeast-2",
		"ECS ListServersDetails --cli-region=cn-north-4": "ECS ListServersDetails --cli-region=cn-north-4 --cli-profile=sso-prod",
		"--cli-profile other ECS ListServersDetails":     "--cli-profile other ECS ListServersDetails --cli-region=ap-southeast-2",
		"configure list":                                 "configure list", // KooCLI's own commands are left alone
		"version":                                        "version",
		"--help":                                         "--help",
	}
	for in, want := range rewrite {
		if got := strings.Join(h.RewriteArgs(strings.Fields(in)), " "); got != want {
			t.Errorf("RewriteArgs(%q) = %q, want %q", in, got, want)
		}
	}

	// Login follows the profile's mode in KooCLI's config (read, never written).
	cfg := hcloudConfig(t, "")
	if err := h.CheckLogin(); err == nil || !strings.Contains(err.Error(), "no KooCLI config at "+cfg) {
		t.Errorf("missing config: %v", err)
	}
	os.WriteFile(cfg, []byte(`{"current":"x","profiles":[{"name":"sso-prod","mode":"SSO","region":"ap-southeast-2"},{"name":"keys","mode":"AKSK"}]}`), 0o600)
	if err := h.CheckLogin(); err != nil || fmt.Sprint(h.LoginCommands(false)) != "[[hcloud configure sso --cli-profile=sso-prod]]" {
		t.Errorf("sso login: %v %v", h.LoginCommands(false), err)
	}
	h.ctx.HcloudProfile = "keys"
	if err := h.CheckLogin(); err != nil || h.LoginCommands(false) != nil {
		t.Errorf("keys: nothing to run, got %v %v", h.LoginCommands(false), err)
	}
	h.ctx.HcloudProfile = "nope"
	if err := h.CheckLogin(); err == nil || !strings.Contains(err.Error(), `KooCLI profile "nope" not found`) {
		t.Errorf("unknown profile: %v", err)
	}
	profiles, err := HcloudProfiles()
	if err != nil || len(profiles) != 2 || profiles[0] != (HcloudProfile{"sso-prod", "SSO", "ap-southeast-2"}) {
		t.Errorf("profiles: %+v %v", profiles, err)
	}

	if got := strings.Join(h.WhoAmICommand(), " "); got != "hcloud configure show --cli-profile=nope" {
		t.Errorf("whoami: %s", got)
	}
	h.ctx.Region = ""
	if env, _ := h.Prepare(); env.Set["HW_REGION_NAME"] != "" || !strings.Contains(strings.Join(env.Unset, " "), "HW_REGION_NAME") {
		t.Errorf("no region: %+v", env)
	}
	if got := h.Describe(); got != "huawei profile nope / -" {
		t.Errorf("describe: %s", got)
	}

	// No clusters, shells or tunnels.
	if _, err := h.DescribeCluster(config.Cluster{}, nil); !errors.Is(err, errHuaweiAccess) {
		t.Errorf("kube: %v", err)
	}
	if _, err := h.ResolveTarget("x", TargetOptions{}, nil); !errors.Is(err, errHuaweiAccess) {
		t.Errorf("shell: %v", err)
	}
	if _, err := h.ShellCommand(Instance{}); !errors.Is(err, errHuaweiAccess) {
		t.Errorf("shell command: %v", err)
	}
	if _, err := h.TunnelMethod(config.Tunnel{}); !errors.Is(err, errHuaweiAccess) {
		t.Errorf("tunnel: %v", err)
	}
	if h.TokenCommand("", "") != nil {
		t.Error("a token command")
	}
}

func TestHuaweiConfigErrors(t *testing.T) {
	h := &Huawei{ctx: &config.Context{Name: "hw", Provider: config.ProviderHuawei, HcloudProfile: "p"}}
	dir := t.TempDir()
	old := HcloudConfigPath
	t.Cleanup(func() { HcloudConfigPath = old })

	HcloudConfigPath = func() string { return dir } // a directory: read fails
	if err := h.CheckLogin(); err == nil || strings.Contains(err.Error(), "no KooCLI config") {
		t.Errorf("read error: %v", err)
	}
	bad := filepath.Join(dir, "config.json")
	os.WriteFile(bad, []byte("{not json"), 0o600)
	HcloudConfigPath = func() string { return bad }
	if err := h.CheckLogin(); err == nil || !strings.Contains(err.Error(), "read") {
		t.Errorf("bad json: %v", err)
	}
	// Default: the account's home from the user database, like KooCLI —
	// not $HOME; $HOME only if the lookup fails.
	oldLookup := lookupUser
	t.Cleanup(func() { lookupUser = oldLookup })
	t.Setenv("MEK_HCLOUD_CONFIG", "")
	t.Setenv("HOME", "/env-home")
	lookupUser = func() (*user.User, error) { return &user.User{HomeDir: "/passwd-home"}, nil }
	if got := old(); got != filepath.Join("/passwd-home", ".hcloud", "config.json") {
		t.Errorf("default path: %s", got)
	}
	lookupUser = func() (*user.User, error) { return nil, errors.New("no entry") }
	if got := old(); got != filepath.Join("/env-home", ".hcloud", "config.json") {
		t.Errorf("fallback path: %s", got)
	}
	t.Setenv("MEK_HCLOUD_CONFIG", "/custom/config.json")
	if got := old(); got != "/custom/config.json" {
		t.Errorf("MEK_HCLOUD_CONFIG: %s", got)
	}
}

func TestValidateHuawei(t *testing.T) {
	head := "contexts:\n  h:\n    provider: huawei\n"
	for in, want := range map[string]string{
		head: "needs hcloud_profile",
		head + "    hcloud_profile: p\n    project: x\n":                                                                      "project is for gcp contexts",
		head + "    hcloud_profile: p\n    targets: {vm: {instance: x}}\n":                                                    "clusters and targets are not for huawei contexts",
		head + "    hcloud_profile: p\n    tunnels: {db: {host: 10.0.0.1, port: 1}}\n":                                        "huawei contexts have no clusters, shells or tunnels",
		head + "    hcloud_profile: p\n    tenant_id: t\n":                                                                    "tenant_id is for azure contexts",
		"contexts:\n  a:\n    provider: aws\n    aws_profile: p\n    hcloud_profile: x\n":                                     "hcloud_profile is for huawei contexts",
		"contexts:\n  a:\n    provider: aws\n    aws_profile: p\n    project: x\n":                                            "project is for gcp contexts", // never quietly ignored
		"contexts:\n  a:\n    provider: gcp\n    project: p\n    role: Admin\n":                                               "role is for aws contexts",
		"contexts:\n  a:\n    provider: gcp\n    project: p\n    bastion: {name: b, resource_group: g}\n":                     "bastion is for azure contexts",
		"contexts:\n  a:\n    provider: azure\n    tenant_id: t\n    subscription_id: " + subID + "\n    hcloud_profile: x\n": "hcloud_profile is for huawei contexts",
	} {
		if err := parse(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parse(%q) = %v, want %q", in, err, want)
		}
	}
	if err := parse(head + "    hcloud_profile: p\n    region: ap-southeast-2\n"); err != nil {
		t.Errorf("valid: %v", err)
	}
}
