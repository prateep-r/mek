//go:build contract

package contract

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// KooCLI takes the --cli-profile and --cli-region mek adds to its API
// commands, and mek reads KooCLI's real config to log in. KooCLI always
// writes the real user's ~/.hcloud, ignoring $HOME, so this runs only where
// MEK_CONTRACT_HCLOUD=1 says the machine is a throwaway one (CI, mek's
// test container).
func TestKooCLIAcceptsMekFlags(t *testing.T) {
	if os.Getenv("MEK_CONTRACT_HCLOUD") == "" {
		t.Skip("set MEK_CONTRACT_HCLOUD=1 on a throwaway machine: KooCLI writes the real ~/.hcloud")
	}
	hcloud := realCLI(t, "hcloud")
	// KooCLI setup a user does once: accept the privacy statement, create a profile.
	for _, args := range [][]string{
		{"configure", "set", "--cli-agree-privacy-statement=true"},
		{"configure", "set", "--cli-profile=mek-keys", "--cli-mode=AKSK", "--cli-region=ap-southeast-2", "--cli-access-key=AKFAKE", "--cli-secret-key=SKFAKE"},
	} {
		if out, err := exec.Command(hcloud, args...).CombinedOutput(); err != nil {
			t.Fatalf("hcloud %v: %v\n%s", args, err, out)
		}
	}
	u := newUser(t, `contexts:
  hw:      {provider: huawei, hcloud_profile: mek-keys, region: ap-southeast-2}
  hw-gone: {provider: huawei, hcloud_profile: mek-no-such-profile}
`, []string{hcloud})

	// KooCLI honours the --cli-profile mek appends after the operation...
	u.want("Profile mek-no-such-profile does not exist", "-c", "hw-gone", "hcloud", "ECS", "ListServersDetails")
	// ...and rejects it on its own commands, which is why mek leaves those alone.
	if out, _ := u.run("-c", "hw", "hcloud", "version"); strings.Contains(out, "Invalid parameter") || !strings.Contains(out, "KooCLI version") {
		t.Errorf("mek must not add flags to `hcloud version`:\n%s", out)
	}
	u.want(`"name": "mek-keys"`, "-c", "hw", "hcloud", "configure", "show", "--cli-profile=mek-keys")
	// mek reads KooCLI's real config to decide how to log in: keys have no step.
	u.want("hw has no login step", "login", "hw")
	u.want(`KooCLI profile "mek-no-such-profile" not found`, "login", "hw-gone")
}
