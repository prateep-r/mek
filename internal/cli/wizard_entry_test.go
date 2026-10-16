package cli

import (
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/ui"
	"github.com/prateep-r/mek/internal/wizard"
)

const entryConfig = `contexts:
  aws: {provider: aws, aws_profile: p, region: ap-southeast-1, targets: {bastion: {instance: "tag:Name=bastion"}},
        tunnels: {db: {via: bastion, host: old.db, port: 5432}}, clusters: {main: {name: main-eks}}}
  noreg: {provider: aws, aws_profile: p}
  g: {provider: gcp, project: p}
  uat: {provider: aws, sso_start_url: "https://o.awsapps.com/start", sso_region: ap-southeast-1, account_id: "111122223333", role: Dev}
`

// cloud answers the discovery commands like the clouds would.
func cloud(argv []string) (string, int) {
	switch cmd := strings.Join(argv, " "); {
	case strings.HasPrefix(cmd, "aws ec2 describe-instances"):
		return `[{"id":"i-0aaa1111bbbb2222c","name":"web 1","ip":"10.0.0.5"},{"id":"i-0ddd3333eeee4444f","name":null,"ip":"10.0.0.6"}]`, 0
	case strings.HasPrefix(cmd, "aws rds describe-db-instances"):
		return `[{"id":"orders","host":"orders.x.rds.amazonaws.com","port":6543,"engine":"postgres"}]`, 0
	case strings.HasPrefix(cmd, "aws rds describe-db-clusters"):
		return `[{"id":"aurora","host":"aurora.cluster-x.rds.amazonaws.com","port":3306,"engine":"aurora-mysql"}]`, 0
	case strings.HasPrefix(cmd, "aws eks list-clusters"):
		return `{"clusters":["main-eks","data-eks"]}`, 0
	case strings.HasPrefix(cmd, "gcloud compute instances list"):
		return `[{"name":"vm-1","zone":"asia-southeast1-a","networkInterfaces":[{"networkIP":"10.1.0.2"}]}]`, 0
	case strings.HasPrefix(cmd, "gcloud sql instances list"):
		return `[{"connectionName":"p:asia-southeast1:orders","databaseVersion":"POSTGRES_16"}]`, 0
	case strings.HasPrefix(cmd, "gcloud container clusters list"):
		return `[{"name":"gke-1","location":"asia-southeast1"}]`, 0
	}
	return "", 1
}

func entryHarness(t *testing.T) *harness {
	h := newHarness(t, entryConfig)
	h.exec.respond = cloud
	return h
}

// freePort is a local port nothing listens on (as far as anyone knows).
func freePort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func TestTargetWizard(t *testing.T) {
	h := entryHarness(t)
	answering(t, []string{"down", "enter", "enter", "enter", "enter"}) // web 1, by its Name tag, web-1
	out := stderr(t, func() { h.mustRun("-c", "aws", "ctx", "target", "add") })
	if !strings.Contains(out, "✓ added target web-1 to aws in") || !strings.Contains(out, "next: mek -c aws shell web-1") {
		t.Errorf("output: %s", out)
	}
	answering(t, []string{"enter", "enter", "type:raw", "enter", "enter"}) // the unnamed one: by id; no name to suggest
	h.mustRun("-c", "aws", "ctx", "target", "add")
	got := readFile(t, config.Path())
	if !strings.Contains(got, "web-1:\n        instance: tag:Name=web 1\n") || !strings.Contains(got, "raw:\n        instance: i-0ddd3333eeee4444f\n") {
		t.Errorf("file:\n%s", got)
	}

	screens := answering(t, []string{"enter", "enter", "type:me", "enter", "enter"}) // vm-1, its zone, user me, name vm-1
	h.run("-c", "g", "ctx", "target", "add")
	if s := (*screens)[0]; !strings.Contains(s, "✓ Zone: asia-southeast1-a") || !strings.Contains(s, "vm-1:\n        instance: vm-1\n        zone: asia-southeast1-a\n        user: me") {
		t.Errorf("gcp review:\n%s", s)
	}
	screens = answering(t, []string{"down", "enter", "type:Bad_VM", "enter", "enter", "enter", "enter"}) // not a VM name: shown, not saved
	h.run("-c", "g", "ctx", "target", "add")
	if s := (*screens)[0]; !strings.Contains(s, "would be invalid") || !strings.Contains(s, "is not a VM name") {
		t.Errorf("invalid:\n%s", s)
	}
}

func TestClusterWizard(t *testing.T) {
	h := entryHarness(t)
	answering(t, []string{"enter", "type:etl", "enter", "enter", "enter"}) // data-eks (the context's region: not asked)
	h.mustRun("-c", "aws", "ctx", "cluster", "add")
	if got := readFile(t, config.Path()); !strings.Contains(got, "data-eks:\n        name: data-eks\n        namespace: etl\n") {
		t.Errorf("file:\n%s", got)
	}
	if got := strings.Join(h.exec.argv(), "\n"); !strings.Contains(got, "aws eks list-clusters --output json") {
		t.Errorf("argv: %s", got)
	}

	// No region on the context: asked, and needed.
	screens := answering(t, []string{"type:zzz", "enter", "enter", "type:us-east-1", "enter", "enter", "enter", "enter"})
	h.run("-c", "noreg", "ctx", "cluster", "add")
	if s := (*screens)[0]; !strings.Contains(s, "region: us-east-1") || !strings.Contains(s, "✓ EKS cluster: data-eks") {
		t.Errorf("noreg:\n%s", s)
	}
	if got := strings.Join(h.exec.argv(), "\n"); !strings.Contains(got, "aws eks list-clusters --region us-east-1 --output json") {
		t.Errorf("argv: %s", got)
	}
	screens = answering(t, []string{"type:zzz", "enter", "enter"})
	h.run("-c", "noreg", "ctx", "cluster", "add")
	if s := (*screens)[0]; !strings.Contains(s, "a region, please: the context has none") {
		t.Errorf("no region:\n%s", s)
	}

	// Edit, same name: saved in place.
	answering(t, []string{"enter", "type:apps", "enter", "enter", "enter"})
	out := stderr(t, func() { h.mustRun("-c", "aws", "ctx", "cluster", "edit", "main") })
	if !strings.Contains(out, "saved cluster main to aws") || !strings.Contains(readFile(t, config.Path()), "main:\n        name: main-eks\n        namespace: apps\n") {
		t.Errorf("edit: %s\n%s", out, readFile(t, config.Path()))
	}

	answering(t, []string{"enter", "enter", "down", "enter", "enter", "enter", "enter"}) // gke-1, its location, private
	h.mustRun("-c", "g", "ctx", "cluster", "add")
	if got := readFile(t, config.Path()); !strings.Contains(got, "gke-1:\n        name: gke-1\n        location: asia-southeast1\n        endpoint: private\n") {
		t.Errorf("gke:\n%s", got)
	}
}

func TestTunnelWizard(t *testing.T) {
	h := entryHarness(t)
	answering(t, []string{"enter", "enter", "down", "enter", "enter", "enter", "enter", "enter"}) // host via bastion, orders, its port, +10000
	h.mustRun("-c", "aws", "ctx", "tunnel", "add")
	got := readFile(t, config.Path())
	if !strings.Contains(got, "orders:\n        via: bastion\n        host: orders.x.rds.amazonaws.com\n        port: 6543\n") || strings.Contains(got, "local_port: 16543") {
		t.Errorf("file:\n%s", got)
	}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	used, free := strconv.Itoa(busy.Addr().(*net.TCPAddr).Port), freePort(t)
	screens := answering(t, []string{"down", "enter", "down", "enter", "type:8080", "enter", // a port on the unnamed instance
		"ctrl+u", "type:" + used, "enter", "ctrl+u", "type:" + free, "enter", "type:web", "enter"})
	h.run("-c", "aws", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "via: i-0ddd3333eeee4444f") || !strings.Contains(s, "local_port: "+free) {
		t.Errorf("port tunnel:\n%s", s)
	}
	screens = answering(t, []string{"down", "enter", "down", "enter", "type:8080", "enter", "ctrl+u", "type:" + used, "enter"})
	h.run("-c", "aws", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "localhost:"+used+" is in use — pick another") {
		t.Errorf("busy port:\n%s", s)
	}
	screens = answering(t, []string{"down", "enter", "down", "down", "enter", "type:70000", "enter"})
	h.run("-c", "aws", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "via: tag:Name=web 1") && !strings.Contains(s, "a port is a number from 1 to 65535") {
		t.Errorf("bad port:\n%s", s)
	}

	// Edit: the tunnel's own local port is fine even when in use (by itself).
	answering(t, []string{"enter", "enter", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("-c", "aws", "ctx", "tunnel", "edit", "db")
	if got := readFile(t, config.Path()); !strings.Contains(got, "db:\n        via: bastion\n        host: old.db\n        port: 5432\n") {
		t.Errorf("edit:\n%s", got)
	}

	screens = answering(t, []string{"down", "down", "enter", "enter", "down", "enter", "ctrl+u", "type:" + free, "enter", "enter"}) // Cloud SQL, private IP
	h.run("-c", "g", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "orders:\n        local_port: "+free+"\n        cloudsql: p:asia-southeast1:orders\n        private_ip: true") {
		t.Errorf("cloud sql:\n%s", s)
	}
	screens = answering(t, []string{"enter", "enter", "type:10.0.0.9", "enter", "type:6379", "enter", "enter", "type:redis", "enter"}) // a host via vm-1
	h.run("-c", "g", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "redis:\n        via: vm-1\n        host: 10.0.0.9\n        port: 6379") {
		t.Errorf("gcp host:\n%s", s)
	}
}

func TestTunnelWizardLookupsFail(t *testing.T) {
	h := newHarness(t, entryConfig)
	h.exec.code, h.exec.stderr = 254, "\nAn error occurred (UnauthorizedOperation) when calling the DescribeInstances operation\n"
	screens := answering(t, []string{"enter"})
	h.run("-c", "aws", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "▸ bastion  target · tag:Name=bastion") { // its targets, still
		t.Errorf("targets only:\n%s", s)
	}
	screens = answering(t, []string{"enter", "enter"})
	h.run("-c", "aws", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "couldn't list them: aws rds describe-db-instances: exit status 254: An error occurred (UnauthorizedOperation)") ||
		!strings.Contains(s, "(logged in? try: mek -c aws login)") {
		t.Errorf("databases:\n%s", s)
	}
	screens = answering(t, []string{"enter"})
	h.run("-c", "g", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "couldn't list them: gcloud compute instances: exit status 254") {
		t.Errorf("no targets, no VMs:\n%s", s)
	}
}

func TestEntryCommands(t *testing.T) {
	h := entryHarness(t)
	old := confirm
	t.Cleanup(func() { confirm = old })

	_, err := h.run("ctx", "tunnel", "add")
	wantErr(t, err, "ctx tunnel add asks questions and needs a terminal")
	_, err = h.run("ctx", "tunnel", "edit")
	wantErr(t, err, "needs a terminal")
	_, err = h.run("ctx", "tunnel", "rm", "db")
	wantErr(t, err, "ctx tunnel rm without -c asks questions")
	_, err = h.run("ctx", "tunnel", "rm", "db", "--yes")
	wantErr(t, err, "which context? mek -c <context> ctx tunnel rm db --yes")

	out := stderr(t, func() { h.mustRun("-c", "aws", "context", "tunnel", "rm", "db", "--yes") })
	if !strings.Contains(out, "removed tunnel db from aws") || strings.Contains(readFile(t, config.Path()), "old.db") {
		t.Errorf("rm: %s", out)
	}
	_, err = h.run("-c", "aws", "ctx", "tunnel", "rm", "db", "--yes")
	wantErr(t, err, "aws has no tunnel db (have: )")

	// Picked: the context, then confirmed.
	answers := []bool{false, true}
	confirm = func(q string) (bool, error) {
		if q != "Remove cluster main from aws ("+config.Show(config.Path())+")?" {
			t.Errorf("question: %s", q)
		}
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	answering(t, []string{"enter"}, []string{"enter"}) // aws: first in the list
	h.mustRun("ctx", "cluster", "rm", "main")
	if !strings.Contains(readFile(t, config.Path()), "main-eks") {
		t.Error("removed though declined")
	}
	h.mustRun("-c", "aws", "ctx", "cluster", "rm", "main")
	if strings.Contains(readFile(t, config.Path()), "main-eks") {
		t.Error("not removed")
	}
	confirm = func(string) (bool, error) { return false, ui.ErrNoTTY }
	_, err = h.run("-c", "aws", "ctx", "target", "rm", "bastion")
	wantErr(t, err, "no terminal to ask on — pass --yes")

	// Removing a target a tunnel still uses: refused by validation.
	h = newHarness(t, entryConfig)
	_, err = h.run("-c", "aws", "ctx", "target", "rm", "bastion", "--yes")
	wantErr(t, err, "not saved")

	answering(t, []string{"enter"})
	_, err = h.run("-c", "g", "ctx", "tunnel", "edit")
	wantErr(t, err, "g has no tunnels yet — add one: mek -c g ctx tunnel add")
	_, err = h.run("-c", "aws", "ctx", "tunnel", "edit", "nope")
	wantErr(t, err, "aws has no tunnel nope (have: db)")
	_, err = h.run("-c", "nope", "ctx", "tunnel", "add")
	wantErr(t, err, "unknown context")

	// Edit, picked: the tunnel list says where each goes.
	h = newHarness(t, `contexts:
  g: {provider: gcp, project: p, tunnels: {sql: {cloudsql: "p:r:i", local_port: 15432}, r: {via: vm, host: 10.0.0.9, port: 6379}, w: {via: vm, port: 80, local_port: 8080}}}
`)
	screens := answering(t, []string{}, nil)
	h.run("-c", "g", "ctx", "tunnel", "edit")
	for _, want := range []string{"r  localhost:16379 → 10.0.0.9:6379 via vm", "sql  localhost:15432 → p:r:i", "w  localhost:8080 → vm:80"} {
		if !strings.Contains((*screens)[0], want) {
			t.Errorf("pick lacks %q:\n%s", want, (*screens)[0])
		}
	}
	answering(t, []string{"ctrl+c"})
	_, err = h.run("-c", "g", "ctx", "tunnel", "edit")
	wantErr(t, err, "canceled")
	answering(t, []string{"ctrl+c"})
	_, err = h.run("ctx", "target", "add")
	wantErr(t, err, "canceled")

	h = newHarness(t, "contexts: {}\n")
	answering(t)
	_, err = h.run("ctx", "target", "add")
	wantErr(t, err, "no contexts yet")
	h = newHarness(t, "contexts: [\n")
	_, err = h.run("ctx", "target", "add")
	wantErr(t, err, "parse")
}

func TestEntryWizardFiles(t *testing.T) {
	// The context's environment can't be prepared (mek's AWS config): said.
	h := entryHarness(t)
	os.WriteFile(config.Dir()+"/aws", nil, 0o600)
	answering(t)
	_, err := h.run("-c", "uat", "ctx", "target", "add")
	if err == nil {
		t.Error("no environment, and no error")
	}

	// The file went away under the form: at the review, and at the save.
	h = entryHarness(t)
	screens := answering(t, []string{"enter", "enter", "enter", "type:x", "enter"})
	play := runForm
	runForm = func(f *wizard.Form) (wizard.Answers, error) {
		os.Remove(config.Path())
		os.Mkdir(config.Path(), 0o700)
		return play(f)
	}
	h.run("-c", "g", "ctx", "target", "add")
	if s := (*screens)[0]; !strings.Contains(s, "[Back]") {
		t.Errorf("review:\n%s", s)
	}
	h = entryHarness(t)
	answering(t, []string{"enter", "enter", "enter", "enter", "enter"})
	play = runForm
	runForm = func(f *wizard.Form) (wizard.Answers, error) {
		a, err := play(f)
		os.Remove(config.Path())
		os.Mkdir(config.Path(), 0o700)
		return a, err
	}
	if _, err := h.run("-c", "g", "ctx", "target", "add"); err == nil {
		t.Error("saved into a directory")
	}

	if alias("tunnels", "ls") != "" || alias("clusters", "ls") != "ls" || alias("clusters", "token") != "" || alias("targets", "Web Server #1") != "web-server-1" {
		t.Errorf("alias: %q %q %q", alias("tunnels", "ls"), alias("clusters", "ls"), alias("targets", "Web Server #1"))
	}
}

func TestEntryWizardEdits(t *testing.T) {
	h := entryHarness(t)
	screens := answering(t, []string{}, nil)
	h.run("-c", "aws", "ctx", "target", "edit")
	if !strings.Contains((*screens)[0], "bastion  tag:Name=bastion") {
		t.Errorf("pick:\n%s", (*screens)[0])
	}
	answering(t, []string{"enter"}, []string{"enter", "enter", "enter"}) // pick bastion; its tag, typed; its name
	if out := stderr(t, func() { h.mustRun("-c", "aws", "ctx", "target", "edit") }); !strings.Contains(out, "saved target bastion") {
		t.Errorf("edit: %s", out)
	}
	screens = answering(t, []string{}, nil)
	h.run("-c", "aws", "ctx", "cluster", "edit")
	if !strings.Contains((*screens)[0], "main  main-eks") {
		t.Errorf("clusters:\n%s", (*screens)[0])
	}
	for _, noun := range []string{"target", "cluster"} {
		answering(t)
		_, err := h.run("-c", "aws", "ctx", noun, "edit", "nope")
		wantErr(t, err, "aws has no "+noun+" nope")
	}
	for _, args := range [][]string{{"edit", "x"}, {"rm", "x", "--yes"}} {
		answering(t)
		_, err := h.run(append([]string{"-c", "nope", "ctx", "tunnel"}, args...)...)
		wantErr(t, err, "unknown context")
	}

	// A name given; one that's taken; a port with no +10000; a local port that isn't one.
	screens = answering(t, []string{"enter", "ctrl+u", "type:bastion", "enter"})
	h.run("-c", "aws", "ctx", "target", "add", "jump")
	if s := (*screens)[0]; !strings.Contains(s, "aws has a target called bastion — change it with: mek -c aws ctx target edit bastion") {
		t.Errorf("taken:\n%s", s)
	}
	screens = answering(t, []string{"enter"})
	h.run("-c", "aws", "ctx", "target", "add", "jump")
	if s := (*screens)[0]; !strings.Contains(s, "jump▏") {
		t.Errorf("name given:\n%s", s)
	}
	screens = answering(t, []string{"down", "enter", "enter", "type:60000", "enter", "enter", "type:abc", "enter"})
	h.run("-c", "aws", "ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "a port is a number from 1 to 65535") || !strings.Contains(s, "abc▏") {
		t.Errorf("local port:\n%s", s)
	}

	// A Cloud SQL tunnel over its private IP: all kept.
	h = newHarness(t, "contexts:\n  g: {provider: gcp, project: p, tunnels: {sql: {cloudsql: \"p:r:i\", private_ip: true, local_port: 15432}}}\n")
	h.exec.respond = cloud
	answering(t, []string{"enter", "enter", "enter", "enter", "enter", "enter"})
	h.mustRun("-c", "g", "ctx", "tunnel", "edit", "sql")
	if got := readFile(t, config.Path()); !strings.Contains(got, "sql:\n        local_port: 15432\n        cloudsql: p:r:i\n        private_ip: true\n") {
		t.Errorf("cloud sql edit:\n%s", got)
	}
}

// The review's fixes: entries edited in place, comments kept; $MEK_CONTEXT
// honoured; lookups that can't read the keyboard.
func TestEntryInPlace(t *testing.T) {
	cfg := `contexts:
  dev:
    provider: aws # the team's
    aws_profile: p
    region: ap-southeast-1
    targets:
      bastion: {instance: "tag:Name=bastion"}   # the jump host
    tunnels:
      # the database
      db: {via: bastion, host: old.db, port: 5432}
`
	h := newHarness(t, cfg)
	h.exec.respond = cloud
	answering(t, []string{"down", "enter", "down", "enter", "type:8080", "enter", "enter", "type:web", "enter", "enter"})
	h.mustRun("-c", "dev", "ctx", "tunnel", "add")
	got := readFile(t, config.Path())
	for _, keep := range []string{"provider: aws # the team's", "bastion: {instance: \"tag:Name=bastion\"}   # the jump host",
		"# the database\n      db: {via: bastion, host: old.db, port: 5432}", "      web:\n        via: i-0ddd3333eeee4444f\n        port: 8080\n"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}
	// Renamed: the old entry goes, the new one is added; the rest stays.
	answering(t, []string{"enter", "enter", "enter", "enter", "enter", "ctrl+u", "type:pg", "enter", "enter"})
	h.mustRun("-c", "dev", "ctx", "tunnel", "edit", "db")
	got = readFile(t, config.Path())
	if strings.Contains(got, "  db:") || !strings.Contains(got, "      pg:\n        via: bastion\n        host: old.db\n") || !strings.Contains(got, "# the jump host") {
		t.Errorf("renamed:\n%s", got)
	}
	// Removed in place.
	h.mustRun("-c", "dev", "ctx", "tunnel", "rm", "web", "--yes")
	if got := readFile(t, config.Path()); strings.Contains(got, "web:") || !strings.Contains(got, "# the jump host") {
		t.Errorf("rm:\n%s", got)
	}

	// The file turned into something that isn't YAML under the form.
	for _, args := range [][]string{{"edit", "pg"}, {"add"}} {
		keys := []string{"enter", "enter", "enter", "enter", "enter", "ctrl+u", "type:pg2", "enter", "enter"}
		if args[0] == "add" {
			keys = []string{"down", "enter", "enter", "type:81", "enter", "enter", "type:w", "enter", "enter"}
		}
		answering(t, keys)
		play := runForm
		runForm = func(f *wizard.Form) (wizard.Answers, error) {
			a, err := play(f)
			os.WriteFile(config.Path(), []byte("contexts: [\n"), 0o600)
			return a, err
		}
		if _, err := h.run(append([]string{"-c", "dev", "ctx", "tunnel"}, args...)...); err == nil || !strings.Contains(err.Error(), "can't edit") {
			t.Errorf("%v on a broken file: %v", args, err)
		}
		os.WriteFile(config.Path(), []byte(cfg), 0o600)
	}
}

func TestMekContextHonoured(t *testing.T) {
	h := entryHarness(t)
	h.mustRun("use", "noreg")
	t.Setenv("MEK_CONTEXT", "g")
	screens := answering(t, []string{})
	h.run("ctx", "tunnel", "add")
	if s := (*screens)[0]; !strings.Contains(s, "▸ g ") {
		t.Errorf("the picker starts on this shell's context:\n%s", s)
	}
	h2 := newHarness(t, entryConfig)
	t.Setenv("MEK_CONTEXT", "aws")
	if out := stderr(t, func() { h2.mustRun("ctx", "tunnel", "rm", "db", "--yes") }); !strings.Contains(out, "removed tunnel db from aws") {
		t.Errorf("rm --yes in this shell's context: %s", out)
	}
	// A renamed context this shell uses: said.
	h3 := newHarness(t, testConfig)
	t.Setenv("MEK_CONTEXT", "gcp")
	answering(t, []string{"ctrl+u", "type:gcp2", "enter", "enter", "enter", "down", "enter", "enter", "enter", "enter", "enter"})
	if out := stderr(t, func() { h3.mustRun("ctx", "edit", "gcp") }); !strings.Contains(out, "this shell's MEK_CONTEXT is still gcp: mek use -s gcp2") {
		t.Errorf("rename note: %s", out)
	}
}

func TestLookupsCantReadTheKeyboard(t *testing.T) {
	h := entryHarness(t)
	answering(t, []string{"enter"})
	h.run("-c", "g", "ctx", "target", "add")
	inv := h.exec.invs[0]
	if inv.Stdin == nil || !slices.Contains(inv.Env, "CLOUDSDK_CORE_DISABLE_PROMPTS=1") || !inv.Quiet {
		t.Errorf("lookup: stdin %v, quiet %v, env %v", inv.Stdin, inv.Quiet, inv.Env)
	}
}
