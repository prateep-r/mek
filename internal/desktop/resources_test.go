package desktop

import (
	"errors"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/catalog"
	"github.com/prateep-r/mek/internal/view"
)

var testCatalogs = map[string]*catalog.Catalog{
	"aws": {Cloud: "aws", Source: "aws-cli/2.0", Services: []catalog.Service{
		{Name: "ec2", Resources: []catalog.Resource{
			{Name: "instances", List: "describe-instances", Items: "Reservations[].Instances[]", ID: "InstanceId", State: "State.Name", Paged: true,
				Describe: &catalog.Action{Command: "describe-instances", Param: "--instance-ids"},
				Actions:  []catalog.Action{{Command: "stop-instances", Param: "--instance-ids"}, {Command: "terminate-instances", Param: "--instance-ids"}}},
			{Name: "regions", List: "describe-regions", Items: "Regions[]", ID: "RegionName"},
		}},
	}},
	"gcp": {Cloud: "gcp", Services: []catalog.Service{
		{Name: "compute", Resources: []catalog.Resource{{Name: "instances", List: "instances list", ID: "name", Actions: []catalog.Action{{Command: "instances stop"}}}}},
	}},
	"azure": {Cloud: "azure", Services: []catalog.Service{
		{Name: "vm", Resources: []catalog.Resource{{Name: "vm", List: "list", ID: "id", Actions: []catalog.Action{{Command: "delete", Param: "--ids", Flags: []string{"--yes"}}}}}},
	}},
	"odd": {Cloud: "odd"},
}

func fakeCatalogs(t *testing.T) {
	old := loadCatalog
	t.Cleanup(func() { loadCatalog = old })
	loadCatalog = func(cloud string) (*catalog.Catalog, error) {
		if c := testCatalogs[cloud]; c != nil {
			return c, nil
		}
		return nil, errors.New("no resource catalog for " + cloud)
	}
}

func TestResources(t *testing.T) {
	fakeCatalogs(t)
	r := fakeRun(t)
	src := sample()
	src.contexts = append(src.contexts, view.Context{Name: "hw", Cloud: "huawei"}, view.Context{Name: "odd", Cloud: "odd"})
	s := New(src, "/bin/mek")

	cat, err := s.Resources("dev")
	if err != nil || cat.Source != "aws-cli/2.0" || len(cat.Kinds) != 2 || strings.Join(cat.Kinds[0].Actions, " ") != "stop-instances terminate-instances" {
		t.Fatalf("catalog: %+v %v", cat, err)
	}
	for ctx, want := range map[string]string{"nope": `no context "nope"`, "hw": "no resource catalog for huawei"} {
		if _, err := s.Resources(ctx); err == nil || err.Error() != want {
			t.Errorf("Resources(%s): %v", ctx, err)
		}
	}

	r.out = `{"Reservations":[{"Instances":[{"InstanceId":"i-1","State":{"Name":"running"}}]}],"NextToken":"t2"}`
	page, err := s.List("dev", "ec2", "instances", "")
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ID != "i-1" || page.Next != "t2" || r.last() != "-c dev aws ec2 describe-instances --output json --max-items 100" {
		t.Fatalf("list: %+v %v %s", page, err, r.last())
	}
	s.List("dev", "ec2", "instances", "t2")
	if !strings.HasSuffix(r.last(), "--starting-token t2") {
		t.Errorf("next page: %s", r.last())
	}
	row := page.Rows[0]

	r.out = `{"Reservations":[{"Instances":[{"InstanceId":"i-1"}]}]}`
	if text, err := s.Describe("dev", "ec2", "instances", row); err != nil || !strings.Contains(text, `"InstanceId": "i-1"`) || r.last() != "-c dev aws ec2 describe-instances --instance-ids i-1 --output json" {
		t.Errorf("describe: %q %v", text, err)
	}
	r.out = "not json"
	if text, _ := s.Describe("dev", "ec2", "instances", row); text != "not json" {
		t.Errorf("describe, not JSON: %q", text)
	}
	region := catalog.Row{ID: "ap-southeast-1", Raw: []byte(`{"RegionName":"ap-southeast-1"}`)}
	if text, _ := s.Describe("dev", "ec2", "regions", region); !strings.Contains(text, `"RegionName": "ap-southeast-1"`) {
		t.Errorf("no describe command: %q", text)
	}

	// Actions: asked on any context; typed on a protected one; mek blocks readonly.
	p, err := s.Act("dev", "ec2", "instances", "stop-instances", row)
	if err != nil || p.Ask != "yes" || p.Text != "destructive: aws ec2 stop-instances --instance-ids i-1?" || p.Label != "stop-instances i-1 (dev)" {
		t.Errorf("act: %+v %v", p, err)
	}
	p, _ = s.Act("prod", "ec2", "instances", "terminate-instances", catalog.Row{ID: "i-2", Name: "web"})
	if p.Ask != "typed" || p.Typed != "prod" || p.Label != "terminate-instances web (prod)" {
		t.Errorf("protected: %+v", p)
	}
	p, _ = s.Act("ro", "compute", "instances", "stop", catalog.Row{ID: "vm"})
	if p.Ask != "" || strings.Join(p.Args, " ") != "-c ro gcloud compute instances stop vm --quiet" {
		t.Errorf("readonly: %+v", p)
	}
	p, _ = s.Act("az", "vm", "vm", "delete", catalog.Row{ID: "/subscriptions/s/x"})
	if p.Ask != "yes" || strings.Join(p.Args, " ") != "-c az az vm delete --ids /subscriptions/s/x --yes" {
		t.Errorf("azure: %+v", p)
	}
	if _, err := s.Act("dev", "ec2", "instances", "reboot-instances", row); err == nil {
		t.Error("an action the kind hasn't")
	}

	for _, c := range []struct{ ctx, svc, name, want string }{
		{"nope", "ec2", "instances", `no context "nope"`},
		{"hw", "ec2", "instances", "no resource catalog for huawei"},
		{"odd", "x", "y", "no resource catalog for odd"},
		{"dev", "ec2", "volumes", "aws has no ec2 volumes"},
		{"dev", "s3", "buckets", "aws has no s3 buckets"},
	} {
		if _, err := s.List(c.ctx, c.svc, c.name, ""); err == nil || err.Error() != c.want {
			t.Errorf("List(%s %s %s) = %v", c.ctx, c.svc, c.name, err)
		}
		if _, err := s.Describe(c.ctx, c.svc, c.name, row); err == nil {
			t.Errorf("Describe(%s)", c.ctx)
		}
		if _, err := s.Act(c.ctx, c.svc, c.name, "x", row); err == nil {
			t.Errorf("Act(%s)", c.ctx)
		}
	}

	r.err = errors.New("ExpiredToken")
	if _, err := s.List("dev", "ec2", "instances", ""); err == nil {
		t.Error("a failed list")
	}
	if _, err := s.Describe("dev", "ec2", "instances", row); err == nil {
		t.Error("a failed describe")
	}
}
