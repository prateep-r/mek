package cli

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/kube"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/wizard"
)

// answering runs the wizard's forms on keys instead of a terminal, one key
// list per form; screens gets each form's last screen.
func answering(t *testing.T, forms ...[]string) (screens *[]string) {
	t.Helper()
	oldRun, oldTerm := runForm, isTerminal
	t.Cleanup(func() { runForm, isTerminal = oldRun, oldTerm })
	isTerminal = func() bool { return true }
	screens = &[]string{}
	runForm = func(f *wizard.Form) (wizard.Answers, error) {
		if len(forms) == 0 {
			t.Fatal("a form nobody answers")
		}
		keys := forms[0]
		forms = forms[1:]
		a, err := wizard.Play(f, keys...)
		*screens = append(*screens, ansi.Strip(f.View().Content))
		return a, err
	}
	return screens
}

func TestCtxAddNeedsATerminal(t *testing.T) {
	h := newHarness(t, testConfig)
	for _, verb := range []string{"add", "edit"} {
		_, err := h.run("ctx", verb)
		wantErr(t, err, "ctx "+verb+" asks questions and needs a terminal")
	}
}

func TestCtxAddSSO(t *testing.T) {
	h := newHarness(t, "# my contexts\n"+testConfig)
	screens := answering(t, []string{
		"type:uat", "enter", "ctrl+u", // taken
		"type:dev", "enter",
		"enter", // AWS
		"enter", // IAM Identity Center
		"enter", // the portal uat uses: its region is known
		"enter", // log in: still no token, so the account is typed
		"type:12", "enter", "ctrl+u", "type:222233334444", "enter",
		"type:Admin", "enter",
		"type:southeast-1", "enter", // filtered to ap-southeast-1
		"down", "enter", // protected
	})
	h.run("ctx", "add") // stops at the review: nothing written
	s := (*screens)[0]
	for _, want := range []string{"✓ Name: dev", "✓ Access portal: https://o.awsapps.com/start", "✓ Region: ap-southeast-1",
		"✓ Guard: protected", "in " + config.Show(config.Path()) + ":", "  sso_region: ap-southeast-1", `  account_id: "222233334444"`, "[Save]"} {
		if !strings.Contains(s, want) {
			t.Errorf("review lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(readFile(t, config.Path()), "dev:") {
		t.Fatal("written before Save")
	}
	if got := strings.Join(h.exec.argv(), "\n"); !strings.Contains(got, "aws sso login --sso-session mek-o") {
		t.Errorf("login: %s", got)
	}
}

func TestCtxAddSaves(t *testing.T) {
	h := newHarness(t, "# my contexts\n"+testConfig)
	answering(t, []string{"enter", "enter", "enter", "enter", "down", "enter", "type:222233334444", "enter", "type:Admin", "enter", "enter", "enter", "enter"})
	out := stderr(t, func() { h.mustRun("ctx", "add", "dev") })
	for _, want := range []string{"✓ added dev to " + config.Show(config.Path()), "next: mek login dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	got := readFile(t, config.Path())
	if !strings.HasPrefix(got, "# my contexts\n") || !strings.Contains(got, "  dev:\n    provider: aws\n    sso_start_url: https://o.awsapps.com/start\n    sso_region: ap-southeast-1\n") {
		t.Errorf("file:\n%s", got)
	}
	if out := h.mustRun("ctx", "ls"); !strings.Contains(out, "dev") {
		t.Errorf("ls: %s", out)
	}
}

func TestCtxAddFirstContext(t *testing.T) {
	h := newHarness(t, "") // no config yet
	answering(t, []string{"type:me", "enter", "down", "enter", "enter", "down", "enter", "type:my-proj", "enter", "type:asia-southeast1", "enter", "enter", "enter"})
	h.mustRun("context", "add")
	got := readFile(t, config.Path())
	if !strings.Contains(got, "# Reference: mek help config\ncontexts:\n  me:\n    provider: gcp\n    region: asia-southeast1\n    project: my-proj\n") {
		t.Errorf("new file:\n%s", got)
	}
}

func TestCtxAddProfileAndAccounts(t *testing.T) {
	h := newHarness(t, testConfig)
	home, _ := os.UserHomeDir()
	os.MkdirAll(filepath.Join(home, ".aws"), 0o700)
	os.WriteFile(filepath.Join(home, ".aws", "config"), []byte("[default]\nregion = x\n[profile team-a]\n[sso-session s]\n[ profile  team-b ]\nnot a section\n"), 0o600)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(config.Dir(), "aws", "config")) // mek's own, from mek env: not this one
	screens := answering(t,
		[]string{"type:p", "enter", "enter", "down", "enter", "down", "enter", "enter", "enter"},                                                      // team-a
		[]string{"type:g", "enter", "down", "enter", "down", "enter", "down", "enter", "type:x", "enter", "enter", "type:readonly", "enter", "enter"}, // me@example.com
	)
	h.run("ctx", "add") // stops at the review
	s := (*screens)[0]
	for _, want := range []string{"✓ Profile: team-a", "aws_profile: team-a"} {
		if !strings.Contains(s, want) {
			t.Errorf("profile review lacks %q:\n%s", want, s)
		}
	}
	h.mustRun("ctx", "add")
	got := readFile(t, config.Path())
	if strings.Contains(got, "  p:") || !strings.Contains(got, "  g:\n    provider: gcp\n    readonly: true\n    project: x\n    account: me@example.com\n") {
		t.Errorf("file:\n%s", got)
	}
	if got := awsProfiles(); len(got) != 3 || got[2].Value != "team-b" {
		t.Errorf("profiles: %v", got)
	}
}

func TestCtxAddInProject(t *testing.T) {
	h, repo := projectHarness(t)
	local, team := filepath.Join(repo, config.LocalFile), filepath.Join(repo, config.TeamFile)

	// prod is in mek.yaml and ~/.mek/config.yaml: only mek.local.yaml is free, so it isn't asked.
	answering(t, []string{"type:prod", "enter", "down", "enter", "enter", "down", "enter", "type:mine", "enter", "enter", "enter", "enter"})
	out := stderr(t, func() { h.mustRun("ctx", "add") })
	if !strings.Contains(out, "added prod to ./mek.local.yaml") || !strings.Contains(out, "in this project it replaces prod from ./mek.yaml") {
		t.Errorf("output: %s", out)
	}
	if !strings.Contains(readFile(t, local), "  prod:\n") {
		t.Errorf("local:\n%s", readFile(t, local))
	}

	// A new name: all three files, with what each means.
	screens := answering(t, []string{"type:extra", "enter"})
	h.run("ctx", "add")
	for _, want := range []string{"▸ " + "./mek.local.yaml" + "  yours, this project only (not committed)", "./mek.yaml" + "  the team's — commit it", config.Show(config.Path())} {
		if !strings.Contains((*screens)[0], want) {
			t.Errorf("files lack %q:\n%s", want, (*screens)[0])
		}
	}

	// gcp is in mek.local.yaml and ~/.mek/config.yaml already: mek.yaml it is.
	answering(t, []string{"type:gcp", "enter", "down", "enter", "enter", "down", "enter", "type:shared", "enter", "enter", "enter", "enter"})
	h.mustRun("ctx", "add")
	if got := readFile(t, team); !strings.Contains(got, "  gcp:\n    provider: gcp\n    project: shared\n") {
		t.Errorf("team:\n%s", got)
	}

	// A new mek.local.yaml goes into .gitignore.
	os.Remove(local)
	answering(t, []string{"type:solo", "enter", "enter", "down", "enter", "enter", "down", "enter", "type:s", "enter", "enter", "enter", "enter"})
	if out := stderr(t, func() { h.mustRun("ctx", "add") }); !strings.Contains(out, "added mek.local.yaml to") {
		t.Errorf("gitignore: %s", out)
	}
	if !strings.HasPrefix(readFile(t, local), "# mek.local.yaml") {
		t.Errorf("template:\n%s", readFile(t, local))
	}
}

func TestCtxEdit(t *testing.T) {
	h := newHarness(t, "# keep me\n"+testConfig)
	answering(t, []string{
		"enter", "enter", "enter", "enter", // name, cloud, sign-in, portal
		"enter", "down", "enter", "enter", // its region (only uat uses the portal: asked, filled in), skip the login, account
		"ctrl+u", "type:Admin", "enter",
		"enter", "down", "enter", "enter", // region, protected, Save
	})
	out := stderr(t, func() { h.mustRun("ctx", "edit", "uat") })
	if !strings.Contains(out, "✓ saved uat in "+config.Show(config.Path())) {
		t.Errorf("output: %s", out)
	}
	got := readFile(t, config.Path())
	if !strings.HasPrefix(got, "# keep me\n") || !strings.Contains(got, "role: Admin") || !strings.Contains(got, "protected: true") ||
		!strings.Contains(got, "prod: {provider: aws, aws_profile: prod-admin, protected: true}") {
		t.Errorf("file:\n%s", got)
	}

	// Renamed: the current context follows. Picked from the list.
	h.mustRun("use", "gcp")
	answering(t, []string{"enter"}, []string{"ctrl+u", "type:gcp2", "enter", "enter", "enter", "down", "enter", "enter", "enter", "enter", "enter"})
	out = stderr(t, func() { h.mustRun("ctx", "edit") })
	if !strings.Contains(out, "renamed gcp to gcp2") || !strings.Contains(out, "the current context is now gcp2") || config.Current() != "gcp2" {
		t.Errorf("rename: %s", out)
	}
	if got := readFile(t, config.Path()); strings.Contains(got, "  gcp:") || !strings.Contains(got, "account: me@example.com") {
		t.Errorf("renamed file:\n%s", got)
	}
	answering(t, []string{"ctrl+u", "type:gcp3", "enter", "enter", "enter", "down", "enter", "enter", "enter", "enter", "enter"})
	if out := stderr(t, func() { h.mustRun("ctx", "edit", "ro") }); strings.Contains(out, "current context") {
		t.Errorf("not the current one: %s", out)
	}

	answering(t, []string{"ctrl+c"})
	_, err := h.run("ctx", "edit")
	wantErr(t, err, "canceled — nothing was changed")
	_, err = h.run("ctx", "edit", "nope")
	wantErr(t, err, "unknown context")
}

func TestCtxEditTeamContext(t *testing.T) {
	h, repo := projectHarness(t)
	screens := answering(t, []string{"enter"})
	h.run("ctx", "edit", "orders-dev")
	for _, want := range []string{"./mek.yaml" + "  the team's — commit the change", "./mek.local.yaml" + "  your own copy — the team's stays as it is"} {
		if !strings.Contains((*screens)[0], want) {
			t.Errorf("choices lack %q:\n%s", want, (*screens)[0])
		}
	}
	answering(t, []string{"enter", "down", "enter", "enter", "enter", "down", "enter", "enter", "enter", "enter", "enter"})
	out := stderr(t, func() { h.mustRun("ctx", "edit", "orders-dev") })
	if !strings.Contains(out, "copied orders-dev to ./mek.local.yaml") {
		t.Errorf("output: %s", out)
	}
	if got := readFile(t, filepath.Join(repo, config.LocalFile)); !strings.Contains(got, "  orders-dev:\n    provider: gcp\n    project: orders-dev\n") {
		t.Errorf("local:\n%s", got)
	}
	if got := readFile(t, filepath.Join(repo, config.TeamFile)); !strings.Contains(got, "orders-dev: {provider: gcp, project: orders-dev}") {
		t.Errorf("team changed:\n%s", got)
	}

	// mek.local.yaml's own: no choice of file.
	screens = answering(t, []string{"enter", "enter", "enter", "down", "enter", "enter", "enter", "enter"})
	h.run("ctx", "edit", "gcp")
	if strings.Contains((*screens)[0], "Save it in") || !strings.Contains((*screens)[0], "in ./mek.local.yaml:") {
		t.Errorf("own file:\n%s", (*screens)[0])
	}
}

func TestCtxEditInvalid(t *testing.T) {
	h := newHarness(t, `contexts:
  k: {provider: gcp, project: p, use_metadata: true, clusters: {main: {name: m, location: us-central1}}}
`)
	// To AWS: its clusters take a region, not a location — shown, and not saved.
	screens := answering(t, []string{"enter", "up", "enter", "enter", "enter", "type:https://n.awsapps.com/start", "enter",
		"type:us-east-1", "enter", "down", "enter", "type:111122223333", "enter", "type:R", "enter", "enter", "enter"})
	_, err := h.run("ctx", "edit", "k")
	wantErr(t, err, "canceled")
	if s := (*screens)[0]; !strings.Contains(s, "would be invalid") || !strings.Contains(s, "aws clusters use region, not location") {
		t.Errorf("review:\n%s", s)
	}

	// The file turned into something unreadable while the wizard asked.
	screens = answering(t, []string{"enter", "enter", "enter", "down", "enter", "enter", "enter", "enter"})
	play := runForm
	runForm = func(f *wizard.Form) (wizard.Answers, error) {
		os.Remove(config.Path())
		os.Mkdir(config.Path(), 0o700)
		return play(f)
	}
	_, err = h.run("ctx", "edit", "k")
	wantErr(t, err, "canceled")
	if s := (*screens)[0]; !strings.Contains(s, "[Back]") {
		t.Errorf("review of an unreadable file:\n%s", s)
	}
}

func TestCtxWizardErrors(t *testing.T) {
	h := newHarness(t, "contexts: {}\n")
	answering(t)
	_, err := h.run("ctx", "edit")
	wantErr(t, err, "no contexts yet — add one: mek ctx add")

	h = newHarness(t, "contexts: [\n")
	_, err = h.run("ctx", "add")
	wantErr(t, err, "parse")

	// Saved after the form, but the file went away under it.
	h = newHarness(t, testConfig)
	answering(t, []string{"type:x", "enter", "down", "enter", "enter", "down", "enter", "type:p", "enter", "enter", "enter", "enter"})
	play := runForm
	runForm = func(f *wizard.Form) (wizard.Answers, error) {
		a, err := play(f)
		os.Remove(config.Path())
		os.Mkdir(config.Path(), 0o700)
		return a, err
	}
	_, err = h.run("ctx", "add")
	if err == nil {
		t.Error("saved into a directory")
	}

	if flowFor("oracle") != nil {
		t.Error("a flow for a cloud mek doesn't have")
	}
	if ownContexts(t.TempDir()) != nil {
		t.Error("contexts from a directory")
	}
}

func TestCtxRenameCurrentUnsaved(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("needs a directory this user can't write")
	}
	h, _ := projectHarness(t)
	h.mustRun("use", "gcp")
	answering(t, []string{"ctrl+u", "type:mine", "enter", "enter", "enter", "down", "enter", "enter", "enter", "enter", "enter"})
	os.Chmod(config.Dir(), 0o500)
	t.Cleanup(func() { os.Chmod(config.Dir(), 0o700) })
	_, err := h.run("ctx", "edit", "gcp")
	if err == nil {
		t.Error("the current context couldn't be saved, and nobody heard")
	}
}

func TestCtxWizardDetails(t *testing.T) {
	h := newHarness(t, `contexts:
  a: {provider: aws, sso_start_url: "https://o.awsapps.com/start", region: ap-southeast-1, account_id: "111122223333", role: Dev}
  b: {provider: aws, sso_start_url: "https://o.awsapps.com/start", region: ap-southeast-1, account_id: "444455556666", role: Dev}
  prod: {provider: aws, aws_profile: prod-admin, protected: true}
`)
	screens := answering(t, []string{
		"type:bad name", "enter", "ctrl+u", "type:c", "enter", "enter", "enter",
		"down", "enter", "type:http://x", "enter", "esc", "up", "enter", // typed: refused; back to the list
		"down", "enter", // no login
		"type:999988887777", "enter", "enter", "type:R", "enter", "enter", "enter",
	})
	h.run("ctx", "add")
	for _, want := range []string{"✓ Access portal: https://o.awsapps.com/start", "sso_region: ap-southeast-1", "✓ Role: R"} {
		if !strings.Contains((*screens)[0], want) {
			t.Errorf("review lacks %q:\n%s", want, (*screens)[0])
		}
	}
	for _, keys := range [][]string{
		{"type:bad name", "enter"}, {"type:c", "enter", "enter", "enter", "down", "enter", "type:http://x", "enter"},
		{"type:c", "enter", "enter", "enter", "enter", "down", "enter", "type:999988887777", "enter", "enter"},
		{"type:c", "enter", "enter", "enter"},
	} {
		screens := answering(t, keys)
		h.run("ctx", "add")
		if s := (*screens)[0]; !strings.Contains(s, "name may only contain") && !strings.Contains(s, "starts with https://") &&
			!strings.Contains(s, "a role, please") && !strings.Contains(s, "used by a, b") {
			t.Errorf("no reason shown:\n%s", s)
		}
	}

	// A profile context, protected: both kept.
	answering(t, []string{"enter", "enter", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("ctx", "edit", "prod")
	if got := readFile(t, config.Path()); !strings.Contains(got, "  prod:\n    provider: aws\n    protected: true\n    aws_profile: prod-admin\n") {
		t.Errorf("prod:\n%s", got)
	}

	h = newHarness(t, "contexts: [\n")
	answering(t)
	_, err := h.run("ctx", "edit")
	wantErr(t, err, "parse")
}

func TestCtxEditPickInProject(t *testing.T) {
	h, _ := projectHarness(t)
	screens := answering(t, []string{"type:orders"}, nil)
	h.run("ctx", "edit")
	if s := (*screens)[0]; !strings.Contains(s, "· "+"./mek.yaml") {
		t.Errorf("pick:\n%s", s)
	}
}

// ssoLogin caches a portal token where the AWS CLI would.
func ssoLogin(t *testing.T, session, token string) {
	t.Helper()
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".aws", "sso", "cache")
	os.MkdirAll(dir, 0o700)
	sum := sha1.Sum([]byte(session))
	body := `{"accessToken":"` + token + `","expiresAt":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`
	os.WriteFile(filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), []byte(body), 0o600)
}

func gcloudLogin(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "credentials.db"), nil, 0o600)
}

func TestCtxAddDiscoversSSO(t *testing.T) {
	h := newHarness(t, testConfig)
	ssoLogin(t, "mek-o", "s3cr3t-token")
	var tokenFile string
	h.exec.respond = func(argv []string) (string, int) {
		cmd := strings.Join(argv, " ")
		if i := slices.Index(argv, "--cli-input-json"); i > 0 {
			tokenFile = strings.TrimPrefix(argv[i+1], "file://")
			if b, _ := os.ReadFile(tokenFile); !strings.Contains(string(b), "s3cr3t-token") {
				t.Errorf("token file: %s", b)
			}
		}
		switch {
		case strings.HasPrefix(cmd, "aws sso list-accounts"):
			return `{"accountList":[{"accountId":"444455556666","accountName":"prod"},{"accountId":"111122223333","accountName":"dev"}]}`, 0
		case strings.HasPrefix(cmd, "aws sso list-account-roles"):
			return `{"roleList":[{"roleName":"ReadOnly"},{"roleName":"Admin"}]}`, 0
		}
		return "", 1
	}
	screens := answering(t, []string{"type:new", "enter", "enter", "enter", "enter", // logged in: no login step
		"down", "enter", // prod
		"enter", // Admin
		"enter", "enter"})
	h.run("ctx", "add")
	s := (*screens)[0]
	for _, want := range []string{"✓ Account: prod", "✓ Role: Admin", `account_id: "444455556666"`, "role: Admin"} {
		if !strings.Contains(s, want) {
			t.Errorf("review lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Log in to the portal") {
		t.Errorf("asked to log in with a token:\n%s", s)
	}
	argv := strings.Join(h.exec.argv(), "\n")
	for _, want := range []string{
		"aws sso list-accounts --cli-input-json file://" + filepath.Join(config.Dir(), "sso-"),
		"--region ap-southeast-1 --output json",
		"aws sso list-account-roles --cli-input-json file://", "--account-id 444455556666",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv lacks %q:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, "s3cr3t") {
		t.Errorf("the token in argv:\n%s", argv)
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Errorf("token file kept: %v", err)
	}
	inv := h.exec.invs[0]
	if env := strings.Join(inv.Env, "\n"); !strings.Contains(env, "AWS_CONFIG_FILE=") || strings.Contains(env, "AWS_PROFILE=mek-") {
		t.Errorf("env: %s", env)
	}
	if aws := readFile(t, filepath.Join(config.Dir(), "aws", "config")); !strings.Contains(aws, "[sso-session mek-o]") {
		t.Errorf("generated config:\n%s", aws)
	}
	if audit := readFile(t, filepath.Join(config.Dir(), "audit.jsonl")); !strings.Contains(audit, `"list-accounts","--cli-input-json","****"`) ||
		!strings.Contains(audit, `"context":"new"`) || strings.Contains(audit, "s3cr3t") {
		t.Errorf("audit:\n%s", audit)
	}
}

func TestCtxAddDiscoveryFails(t *testing.T) {
	h := newHarness(t, testConfig)
	ssoLogin(t, "mek-o", "tok")
	h.exec.code, h.exec.stderr = 255, "\nAn error occurred (UnauthorizedException) when calling the ListAccounts operation: Session token not found or invalid\n"
	screens := answering(t, []string{"type:new", "enter", "enter", "enter", "enter"})
	h.run("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "couldn't list them: aws sso list-accounts: exit status 255: An error occurred (UnauthorizedException)") ||
		strings.Contains(s, "mek -c new login") {
		t.Errorf("failure:\n%s", s)
	}
	h.exec.stderr = ""
	screens = answering(t, []string{"type:new", "enter", "enter", "enter", "enter"})
	h.run("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "exit status 255 — type the value") {
		t.Errorf("silent failure:\n%s", s)
	}

	// mek's generated AWS config can't be written: said, at the login and the lookup.
	os.RemoveAll(filepath.Join(config.Dir(), "aws"))
	os.WriteFile(filepath.Join(config.Dir(), "aws"), nil, 0o600)
	screens = answering(t, []string{"type:new", "enter", "enter", "enter", "enter"})
	h.run("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "couldn't list them") {
		t.Errorf("no generated config:\n%s", s)
	}
	h2 := newHarness(t, testConfig)
	os.WriteFile(filepath.Join(config.Dir(), "aws"), nil, 0o600)
	screens = answering(t, []string{"type:new", "enter", "enter", "enter", "enter", "enter"})
	h2.run("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "[Run it]") || len(h2.exec.invs) != 0 {
		t.Errorf("login without a config:\n%s", s)
	}
}

func TestCtxAddDiscoversProjects(t *testing.T) {
	h := newHarness(t, testConfig)
	gcloudLogin(t, filepath.Join(config.Dir(), "gcloud", "gcp")) // gcp's login is me@example.com's
	h.exec.respond = func(argv []string) (string, int) {
		return `[{"projectId":"acme-dev","name":"Acme Dev"},{"projectId":"acme-prod","name":"acme-prod"}]`, 0
	}
	screens := answering(t, []string{"type:new", "enter", "down", "enter", "down", "enter", // me@example.com: logged in
		"down", "enter", "enter", "enter"})
	h.run("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "✓ Project: acme-prod") || strings.Contains(s, "Log in to Google") {
		t.Errorf("review:\n%s", s)
	}
	inv := h.exec.invs[0]
	if got := strings.Join(inv.Argv, " "); got != "gcloud projects list --format=json(projectId,name)" {
		t.Errorf("argv: %s", got)
	}
	env := strings.Join(inv.Env, "\n")
	if !strings.Contains(env, "CLOUDSDK_CONFIG="+filepath.Join(config.Dir(), "gcloud", "gcp")) || !strings.Contains(env, "CLOUDSDK_CORE_ACCOUNT=me@example.com") ||
		strings.Contains(env, "CLOUDSDK_CORE_PROJECT=") {
		t.Errorf("env: %s", env)
	}
}

func TestCtxAddGCPLogin(t *testing.T) {
	h := newHarness(t, testConfig)
	wizardDir := filepath.Join(config.Dir(), "gcloud", ".wizard")
	h.exec.respond = func(argv []string) (string, int) {
		if argv[1] == "auth" { // gcloud auth login: logged in, in .wizard
			gcloudLogin(t, wizardDir)
		}
		return `[{"projectId":"solo-1"}]`, 0
	}
	answering(t, []string{"type:solo", "enter", "down", "enter", "enter", // no account
		"enter",                             // log in
		"enter", "enter", "enter", "enter"}) // solo-1, no region, normal, Save
	h.mustRun("ctx", "add")
	if got := h.exec.argv()[0]; got != "gcloud auth login" {
		t.Errorf("login: %s", got)
	}
	if !strings.Contains(readFile(t, config.Path()), "project: solo-1") {
		t.Error("not saved")
	}
	if _, err := os.Stat(filepath.Join(config.Dir(), "gcloud", "solo", "credentials.db")); err != nil {
		t.Errorf("the login didn't move to the new context: %v", err)
	}
	if _, err := os.Stat(wizardDir); !os.IsNotExist(err) {
		t.Errorf(".wizard left: %v", err)
	}

	// Renamed: its login goes with it. Unchanged: stays.
	answering(t, []string{"ctrl+u", "type:solo2", "enter", "enter", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("ctx", "edit", "solo")
	if _, err := os.Stat(filepath.Join(config.Dir(), "gcloud", "solo2", "credentials.db")); err != nil {
		t.Errorf("the login didn't follow the rename: %v", err)
	}
	answering(t, []string{"enter", "enter", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("ctx", "edit", "solo2")
	if _, err := os.Stat(filepath.Join(config.Dir(), "gcloud", "solo2", "credentials.db")); err != nil {
		t.Errorf("unchanged, and the login moved: %v", err)
	}

	// Another context's login (same account) is used, and stays its own.
	gcloudLogin(t, filepath.Join(config.Dir(), "gcloud", "gcp"))
	answering(t, []string{"type:more", "enter", "down", "enter", "down", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("ctx", "add")
	if _, err := os.Stat(filepath.Join(config.Dir(), "gcloud", "gcp", "credentials.db")); err != nil {
		t.Errorf("gcp's login moved: %v", err)
	}
}

func TestCtxEditCopyKeepsTeamLogin(t *testing.T) {
	h, _ := projectHarness(t)
	cfg, _ := config.Load()
	team := provider.GCloudDir(cfg.Contexts["orders-dev"])
	gcloudLogin(t, team)
	answering(t, []string{"enter", "down", "enter", "enter", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("ctx", "edit", "orders-dev")
	if _, err := os.Stat(filepath.Join(team, "credentials.db")); err != nil {
		t.Errorf("the team context's login moved to the copy: %v", err)
	}
}

func TestCtxAddGCPLoginAsked(t *testing.T) {
	h := newHarness(t, testConfig)
	h.exec.code = 1
	screens := answering(t, []string{"type:n", "enter", "down", "enter", "down", "enter"}, // me@example.com, not logged in
		[]string{"type:n", "enter", "down", "enter", "enter", "down", "enter"})
	h.run("ctx", "add")
	if s := (*screens)[0]; !strings.Contains(s, "runs: gcloud auth login me@example.com  (a browser opens)") {
		t.Errorf("login step:\n%s", s)
	}
	h.run("ctx", "add")
	if s := (*screens)[1]; !strings.Contains(s, "couldn't list them: gcloud projects list: exit status 1 — type the value") {
		t.Errorf("failed lookup:\n%s", s)
	}
}

func TestCtxRenameKeepsComments(t *testing.T) {
	h := newHarness(t, "contexts:\n  # my sandbox\n  sb:\n    provider: gcp\n    project: p\n  # other\n  o: {provider: gcp, project: q}\n")
	gcloudLogin(t, filepath.Join(config.Dir(), "gcloud", "sb"))
	os.MkdirAll(filepath.Join(config.Dir(), "ssh", "sb"), 0o700)
	os.WriteFile(filepath.Join(config.Dir(), "ssh", "sb", "google_compute_engine"), nil, 0o600)
	os.MkdirAll(kube.Dir(), 0o700)
	os.WriteFile(kube.Path("sb"), nil, 0o600)
	answering(t, []string{"ctrl+u", "type:sandbox", "enter", "enter", "enter", "enter", "enter", "enter", "enter"})
	out := stderr(t, func() { h.mustRun("ctx", "edit", "sb") })
	if got := readFile(t, config.Path()); !strings.HasPrefix(got, "contexts:\n  # my sandbox\n  sandbox:\n") || !strings.Contains(got, "  # other\n  o: {") {
		t.Errorf("rename:\n%s", got)
	}
	// Its login and SSH keys follow it; its kubeconfig named sb, so it goes.
	for _, p := range []string{filepath.Join(config.Dir(), "gcloud", "sandbox"), filepath.Join(config.Dir(), "ssh", "sandbox", "google_compute_engine")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("not moved: %v", err)
		}
	}
	if _, err := os.Stat(kube.Path("sb")); err == nil || !strings.Contains(out, "mek -c sandbox kube writes it again") {
		t.Errorf("old kubeconfig kept: %s", out)
	}

	// A login or keys that can't move are said, not lost silently.
	os.MkdirAll(filepath.Join(config.Dir(), "gcloud", "o2", "taken"), 0o700)
	os.MkdirAll(filepath.Join(config.Dir(), "ssh", "o2", "taken"), 0o700)
	answering(t, []string{"ctrl+u", "type:o2", "enter", "enter", "enter", "enter", "enter", "enter", "enter"})
	out = stderr(t, func() { h.mustRun("ctx", "edit", "sandbox") })
	if !strings.Contains(out, "the login stayed behind") || !strings.Contains(out, "its SSH keys stayed in") {
		t.Errorf("no warning:\n%s", out)
	}
}

// Editing a GCP context to another account: its own login directory, and a
// login offered for the new account.
func TestCtxEditGCPAccount(t *testing.T) {
	h := newHarness(t, testConfig)
	own := filepath.Join(config.Dir(), "gcloud", "gcp")
	gcloudLogin(t, own) // logged in as me@example.com
	screens := answering(t, []string{"enter", "enter", "down", "enter", "type:new@example.com", "enter"})
	h.run("ctx", "edit", "gcp")
	if s := (*screens)[0]; !strings.Contains(s, "? Log in to Google") || !strings.Contains(s, "gcloud auth login new@example.com") {
		t.Errorf("no login for the new account:\n%s", s)
	}
	h.exec.respond = func([]string) (string, int) { return `[{"projectId":"acme-dev"}]`, 0 }
	answering(t, []string{"enter", "enter", "down", "enter", "type:new@example.com", "enter", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("ctx", "edit", "gcp")
	for _, inv := range h.exec.invs {
		if !slices.Contains(inv.Env, "CLOUDSDK_CONFIG="+own) {
			t.Errorf("%v ran outside the context's own login: %v", inv.Argv, inv.Env)
		}
	}
	if !strings.Contains(readFile(t, config.Path()), "account: new@example.com") {
		t.Error("not saved")
	}
}

// A login made in the wizard and not saved doesn't stay behind.
func TestCtxAddCanceledLeavesNoLogin(t *testing.T) {
	h := newHarness(t, testConfig)
	wizardDir := filepath.Join(config.Dir(), "gcloud", ".wizard")
	h.exec.respond = func(argv []string) (string, int) {
		gcloudLogin(t, wizardDir)
		return "[]", 0
	}
	answering(t, []string{"type:n", "enter", "down", "enter", "enter", "enter", "ctrl+c"})
	h.run("ctx", "add")
	if _, err := os.Stat(wizardDir); !os.IsNotExist(err) {
		t.Errorf(".wizard left after a cancel: %v", err)
	}
}

func TestCheckBastion(t *testing.T) {
	for v, ok := range map[string]bool{"rg/b": true, "": false, "b": false, "/b": false, "rg/": false, "rg/b/x": false} {
		if err := checkBastion(false)(v, nil); (err == nil) != ok {
			t.Errorf("checkBastion(%q) = %v", v, err)
		}
	}
	if checkBastion(true)("", nil) != nil || checkBastion(true)("b", nil) == nil {
		t.Error("optional")
	}
}
