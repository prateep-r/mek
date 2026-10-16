package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/catalog"
)

// testCatalogs are small catalogs, one per cloud.
var testCatalogs = map[string]*catalog.Catalog{
	"aws": {Cloud: "aws", Source: "aws-cli/2.0.0", Services: []catalog.Service{
		{Name: "dynamodb", Resources: []catalog.Resource{{Name: "global-tables"}, {Name: "tables", List: "list-tables", Items: "TableNames[]"}}},
		{Name: "ec2", Resources: []catalog.Resource{
			{Name: "instances", List: "describe-instances", Items: "Reservations[].Instances[]", ID: "InstanceId",
				Label: "tag:Name", State: "State.Name", Paged: true,
				Describe: &catalog.Action{Command: "describe-instances", Param: "--instance-ids"},
				Actions: []catalog.Action{{Command: "stop-instances", Param: "--instance-ids"},
					{Command: "terminate-instances", Param: "--instance-ids"}}},
			{Name: "regions", List: "describe-regions", Items: "Regions[]", ID: "RegionName"},
		}},
	}},
	"gcp": {Cloud: "gcp", Source: "gcloud/1.0", Services: []catalog.Service{
		{Name: "compute", Resources: []catalog.Resource{
			{Name: "instances", List: "instances list", ID: "name", Location: &catalog.Location{Flag: "--zone", Field: "zone"},
				Actions: []catalog.Action{{Command: "instances stop"}}},
		}},
	}},
	"azure": {Cloud: "azure", Source: "azure-cli/2.0", Services: []catalog.Service{
		{Name: "vm", Resources: []catalog.Resource{
			{Name: "vm", List: "list", ID: "id", Label: "name", State: "provisioningState",
				Actions: []catalog.Action{{Command: "delete", Param: "--ids", Flags: []string{"--yes"}}}},
		}},
	}},
	"odd": {Cloud: "odd"}, // a catalog with no Source for it
}

// fetcher answers the tab's lookups by their arguments.
type fetcher struct {
	answers map[string]string
	fail    map[string]error
	asked   []string
}

func (f *fetcher) fetch(_ string, args []string) ([]byte, error) {
	q := strings.Join(args, " ")
	f.asked = append(f.asked, q)
	if err := f.fail[q]; err != nil {
		return nil, err
	}
	return []byte(f.answers[q]), nil
}

const (
	page1 = `{"Reservations":[{"Instances":[
		{"InstanceId":"i-1","State":{"Name":"running"},"Tags":[{"Key":"Name","Value":"web"}]},
		{"InstanceId":"i-2","State":{"Name":"stopped"}}]}],"NextToken":"t2"}`
	page2 = `{"Reservations":[{"Instances":[{"InstanceId":"i-3","State":{"Name":"pending"}}]}]}`
)

func resourcesHarness(t *testing.T, selected string) (*harness, *fetcher) {
	t.Helper()
	f := &fetcher{answers: map[string]string{
		"-c dev aws ec2 describe-instances --output json --max-items 100":                     page1,
		"-c dev aws ec2 describe-instances --output json --max-items 100 --starting-token t2": page2,
		"-c dev aws ec2 describe-instances --instance-ids i-1 --output json":                  `{"Reservations":[{"Instances":[{"InstanceId":"i-1"}]}]}`,
		"-c prod aws ec2 describe-instances --output json --max-items 100":                    page2,
		"-c gcp gcloud compute instances list --format=json --limit=500":                      `[{"name":"vm","status":"RUNNING","zone":"zones/asia-southeast1-a"}]`,
		"-c dev aws ec2 describe-regions --output json":                                       `{"Regions":[{"RegionName":"ap-southeast-1"}]}`,
		"-c dev aws dynamodb list-tables --output json":                                       `{"TableNames":["orders"]}`,
		"-c az az vm list --output json":                                                      `[{"id":"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm-1","name":"vm-1","provisioningState":"Succeeded","location":"southeastasia"}]`,
	}, fail: map[string]error{}}
	oldFetch, oldLoad := fetch, loadCatalog
	t.Cleanup(func() { fetch, loadCatalog = oldFetch, oldLoad })
	fetch = f.fetch
	loadCatalog = func(cloud string) (*catalog.Catalog, error) {
		if c := testCatalogs[cloud]; c != nil {
			return c, nil
		}
		return nil, errors.New("no resource catalog for " + cloud)
	}
	h := newHarness(t, sample(), selected)
	h.press("5")
	return h, f
}

func TestResourcesBrowse(t *testing.T) {
	h, f := resourcesHarness(t, "dev")
	h.want("5 Resources", "dev  aws  (aws-cli/2.0.0)", "SERVICE", "dynamodb", "ec2", "enter open · / filter")

	h.press("/", "e", "c", "x")
	h.want(`"ecx"`, "(none)", "/ ecx▏")
	h.press("backspace", "enter")
	h.want(`"ec"`, "▸ ec2")
	h.press("esc") // back from the top stays at the top, and clears the filter
	h.want("dynamodb")
	h.press("/", "z", "esc")
	h.want("dynamodb", "ec2")

	h.press("enter", "/", "t", "a", "b", "l", "e", "s", "enter")
	h.want("▸ tables", "  global-tables") // the exact match first
	h.press("esc", "down", "enter")
	h.want("aws › ec2", "KIND", "instances  stop-instances terminate-instances", "regions")
	h.press("right") // enter, too
	if f.asked[len(f.asked)-1] != "-c dev aws ec2 describe-instances --output json --max-items 100" {
		t.Errorf("listed with %q", f.asked)
	}
	h.want("aws › ec2 › instances", "ID", "NAME", "STATE", "i-1", "web", "running", "i-2", "stopped",
		"more below", "enter details · a actions")
	if strings.Contains(h.screen(), "WHERE") {
		t.Error("a WHERE column for AWS")
	}

	h.press("down") // the last row: the next page
	h.want("i-3", "pending")
	if strings.Contains(h.screen(), "more below") {
		t.Error("more below, with no next page")
	}
	h.press("/", "3", "enter")
	h.want("▸ i-3")
	h.press("/", "esc", "home", "enter")
	h.want("aws › ec2 › instances › i-1", `"InstanceId": "i-1"`, "↑↓ scroll")
	for _, k := range []string{"down", "j", "pgdown", "end", "G", "up", "k", "pgup", "home", "g"} {
		h.press(k)
	}
	h.want(`"Reservations"`)
	h.press("backspace")
	h.want("aws › ec2 › instances", "i-2")
	h.press("r")
	if n := len(f.asked); f.asked[n-1] != "-c dev aws ec2 describe-instances --output json --max-items 100" {
		t.Errorf("reloaded with %q", f.asked[n-1])
	}
	h.press("left", "left")
	h.press("r") // nothing to reload at the top
	h.want("SERVICE")

	// Regions have no describe: the row is the detail. Their names are
	// their ids.
	h.press("down", "enter", "down", "enter")
	h.want("ap-southeast-1")
	h.press("enter")
	h.want(`"RegionName": "ap-southeast-1"`)
	h.press("a")
	h.want("ec2 regions has no actions")
	h.press("esc", "esc", "esc", "home", "enter", "down", "enter")
	h.want("aws › dynamodb › tables", "orders")
}

func TestResourcesFetching(t *testing.T) {
	h, f := resourcesHarness(t, "dev")
	f.fail["-c dev aws ec2 describe-instances --output json --max-items 100"] = errors.New("ExpiredToken: the SSO session has expired")
	h.press("down", "enter", "enter")
	h.want("✗ ExpiredToken: the SSO session has expired")
	h.press("enter", "a") // nothing to open or act on
	h.press("esc")
	delete(f.fail, "-c dev aws ec2 describe-instances --output json --max-items 100")

	// A fetch from before going back is dropped.
	tab := h.m.tabs[4].(*resourcesTab)
	_, cmd := h.m.Update(key("enter"))
	h.want("listing ec2 instances…")
	if body := tab.body(120, 10); body != "" {
		t.Errorf("rows while listing: %q", body)
	}
	h.press("esc")
	h.drain(cmd)
	if tab.rows != nil || tab.loading != "" {
		t.Errorf("a dropped fetch landed: %+v", tab.rows)
	}
	if tab.receive(doneMsg{}) {
		t.Error("received a message that isn't a fetch's")
	}

	h.press("enter")
	f.answers["-c dev aws ec2 describe-instances --instance-ids i-1 --output json"] = "not json"
	h.press("enter")
	h.want("not json")
	h.press("esc")
	f.fail["-c dev aws ec2 describe-instances --instance-ids i-1 --output json"] = errors.New("AccessDenied")
	h.press("enter")
	h.want("✗ AccessDenied")
	h.press("esc", "down", "down") // the next page, while it is fetched: no second fetch
	if tab.loading != "" {
		t.Errorf("still loading: %s", tab.loading)
	}

	f.answers["-c dev aws ec2 describe-instances --output json --max-items 100"] = "{"
	h.press("r")
	h.want("✗ reading the list")
	f.answers["-c dev aws ec2 describe-instances --output json --max-items 100"] = page1
	h.press("r") // after a failed list too
	h.want("i-1", "more below")

	// A next page that fails keeps the rows, and ↓ tries again.
	f.fail["-c dev aws ec2 describe-instances --output json --max-items 100 --starting-token t2"] = errors.New("Throttling")
	h.press("down")
	h.want("i-1", "i-2", "✗ the next page: Throttling", "tries again")
	delete(f.fail, "-c dev aws ec2 describe-instances --output json --max-items 100 --starting-token t2")
	h.press("up", "down")
	h.want("i-3")

	// An exact id or name comes first, before rows that only contain it.
	tab.rows = []catalog.Row{{ID: "i-10", Name: "web-1"}, {ID: "i-1", Name: "web"}}
	h.press("/", "w", "e", "b", "enter")
	h.want("▸ i-1 ")
}

// gcloud lists at most GCPLimit: the tab says so.
func TestResourcesGCPLimit(t *testing.T) {
	h, f := resourcesHarness(t, "gcp")
	f.answers["-c gcp gcloud compute instances list --format=json --limit=500"] = "[" + strings.TrimSuffix(strings.Repeat(`{"name":"vm"},`, catalog.GCPLimit), ",") + "]"
	h.press("enter", "enter")
	h.want("the first 500 only")
}

func TestResourcesActions(t *testing.T) {
	h, _ := resourcesHarness(t, "dev")
	h.press("down", "enter", "enter", "a")
	h.want("actions on i-1 (dev)", "stop-instances", "aws ec2 stop-instances --instance-ids i-1", "enter run")
	h.press("x", "esc")
	if h.m.overlay != nil {
		t.Error("actions stayed open")
	}
	h.press("a", "enter")
	h.want("destructive: aws ec2 stop-instances --instance-ids i-1? [y/N]")
	h.press("n")
	h.want("cancelled: stop-instances i-1 (dev)")
	h.out = "{}"
	h.press("a", "down", "enter", "y")
	h.wantRun(false, "--yes", "-c", "dev", "aws", "ec2", "terminate-instances", "--instance-ids", "i-1")
	h.want("✓ terminate-instances i-1 (dev)", "i-1") // and the list again
	if strings.Contains(h.screen(), "{}") {
		t.Error("the cloud's JSON on the status line")
	}

	h.press("enter", "a", "enter", "y") // from the detail view too
	h.wantRun(false, "--yes", "-c", "dev", "aws", "ec2", "stop-instances", "--instance-ids", "i-1")

	// The context removed while the menu is open: nothing runs.
	h.press("a")
	n := len(h.runs)
	h.src.contexts = h.src.contexts[1:]
	h.m.reload()
	h.press("enter")
	if len(h.runs) != n || h.m.overlay != nil {
		t.Errorf("acted on a removed context: %v", h.runs[n:])
	}
	h.src.contexts = sample().contexts
	h.m.reload()

	// No rows: nothing to act on.
	h.press("esc")
	h.m.tabs[4].(*resourcesTab).rows = nil
	h.press("a")
	if h.m.overlay != nil {
		t.Error("actions with no rows")
	}
}

func TestResourcesGuard(t *testing.T) {
	// A destructive command on a protected context: its name is typed, and
	// passed as --confirm.
	h, _ := resourcesHarness(t, "prod")
	h.press("down", "enter", "enter", "a", "enter")
	h.want("prod is protected — destructive: aws ec2 stop-instances --instance-ids i-3", "Type prod to run it: ▏")
	h.press("p", "r", "x", "backspace", "enter")
	h.want("cancelled: stop-instances i-3 (prod) (the name didn't match)")
	h.press("a", "enter", "esc")
	h.want("cancelled: stop-instances i-3 (prod)")
	if len(h.runs) != 0 {
		t.Fatalf("ran %v", h.runs)
	}
	h.press("a", "enter", "p", "r", "o", "d", "enter")
	h.wantRun(false, "--confirm", "prod", "-c", "prod", "aws", "ec2", "stop-instances", "--instance-ids", "i-3")

	// Readonly: mek blocks it, so the audit log has the attempt; no question.
	g, _ := resourcesHarness(t, "gcp")
	g.want("gcp readonly  gcp  (gcloud/1.0)")
	g.press("enter", "enter")
	g.want("WHERE", "vm", "RUNNING", "asia-southeast1-a")
	g.fail, g.out = errors.New("exit status 1"), "mek: gcp is readonly — blocked destructive command"
	g.press("a", "enter")
	g.wantRun(false, "-c", "gcp", "gcloud", "compute", "instances", "stop", "vm", "--zone=asia-southeast1-a", "--quiet")
	g.want("✗ instances stop vm (gcp): mek: gcp is readonly — blocked")
}

func TestResourcesContexts(t *testing.T) {
	h, _ := resourcesHarness(t, "dev")
	h.press("down", "enter")
	h.m.selected = "prod" // chosen on tab 1: start over
	h.want("prod protected  aws  (aws-cli/2.0.0)", "SERVICE")

	src := sample()
	src.contexts = append(src.contexts, Context{Name: "az", Cloud: "huawei"}, Context{Name: "odd", Cloud: "odd"})
	odd := newHarness(t, src, "az")
	odd.press("5", "enter")
	odd.want("✗ no resource catalog for huawei")
	odd.m.selected = "odd"
	odd.want("✗ no resource catalog for odd")

	none := newHarness(t, &fakeSource{}, "")
	none.press("5", "enter")
	none.want("no contexts")
}

func TestResourcesHelp(t *testing.T) {
	tab := &resourcesTab{}
	for level, want := range map[int]string{levelServices: "enter open", levelKinds: "enter open",
		levelItems: "enter details", levelDetail: "↑↓ scroll"} {
		tab.level = level
		if !strings.HasPrefix(tab.help(), want) {
			t.Errorf("level %d: %s", level, tab.help())
		}
	}
}

func TestFetch(t *testing.T) {
	out, err := fetch("/bin/sh", []string{"-c", `echo "$MEK_SHOW_CMD $CLOUDSDK_CORE_DISABLE_PROMPTS"`})
	if err != nil || string(out) != "0 1\n" {
		t.Errorf("fetch: %q %v", out, err)
	}
	_, err = fetch("/bin/sh", []string{"-c", "echo 'ERROR: (gcloud) bad' >&2; echo 'hint' >&2; exit 1"})
	if err == nil || err.Error() != "ERROR: (gcloud) bad" {
		t.Errorf("first error line: %v", err)
	}
	_, err = fetch("/bin/sh", []string{"-c", "echo first >&2; echo 'An occurred' >&2; exit 1"})
	if err == nil || err.Error() != "An occurred" {
		t.Errorf("last line: %v", err)
	}
	_, err = fetch("/bin/sh", []string{"-c", "exit 3"})
	if err == nil || err.Error() != "exit status 3" {
		t.Errorf("silent: %v", err)
	}
	if _, err = fetch("/nonexistent/mek", nil); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("not found: %v", err)
	}
}

func TestResourcesAzure(t *testing.T) {
	h, _ := resourcesHarness(t, "dev")
	h.src.contexts = append(h.src.contexts, Context{Name: "az", Cloud: "azure", Protected: true})
	h.m.reload()
	h.m.selected = "az"
	h.press("enter", "enter")
	h.want("azure › vm › vm", "WHERE", "vm-1", "Succeeded", "southeastasia")
	h.press("a", "enter")
	h.want("az is protected — destructive: az vm delete --ids /subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm-1 --yes")
}
