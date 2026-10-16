package catalog

import (
	"reflect"
	"strings"
	"testing"
)

func TestFor(t *testing.T) {
	for _, cloud := range []string{"aws", "gcp"} {
		if _, err := For(cloud); err != nil {
			t.Errorf("For(%s): %v", cloud, err)
		}
	}
	if _, err := For("azure"); err == nil {
		t.Error("For(azure) has a source")
	}
}

func words(argv []string) string { return strings.Join(argv, " ") }

var (
	ec2       = &Service{Name: "ec2"}
	instances = &Resource{
		Name: "instances", List: "describe-instances", Items: "Reservations[].Instances[]",
		ID: "InstanceId", Label: "tag:Name", State: "State.Name", Paged: true,
		Describe: &Action{Command: "describe-instances", Param: "--instance-ids"},
	}
	stop = Action{Command: "stop-instances", Param: "--instance-ids"}
)

func TestAWS(t *testing.T) {
	src, _ := For("aws")
	if got := words(src.List(ec2, instances, "")); got != "aws ec2 describe-instances --output json --max-items 100" {
		t.Errorf("first page: %s", got)
	}
	if got := words(src.List(ec2, instances, "tok")); got != "aws ec2 describe-instances --output json --max-items 100 --starting-token tok" {
		t.Errorf("next page: %s", got)
	}
	images := &Resource{List: "describe-images", ListArgs: []string{"--owners", "self"}}
	if got := words(src.List(ec2, images, "")); got != "aws ec2 describe-images --owners self --output json" {
		t.Errorf("unpaged with args: %s", got)
	}

	rows, next, err := src.Rows(instances, []byte(`{"Reservations":[
		{"Instances":[{"InstanceId":"i-1","State":{"Name":"running"},"Tags":[{"Key":"env","Value":"x"},{"Key":"Name","Value":"web"}]}]},
		{"Instances":[{"InstanceId":"i-2","State":{"Name":"stopped"}}]},
		{"Instances":"not a list"}],
		"NextToken":"tok"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{{ID: "i-1", Name: "web", State: "running"}, {ID: "i-2", State: "stopped"}}
	if len(rows) != 2 || next != "tok" {
		t.Fatalf("rows %+v next %q", rows, next)
	}
	for i := range want {
		if rows[i].ID != want[i].ID || rows[i].Name != want[i].Name || rows[i].State != want[i].State || len(rows[i].Raw) == 0 {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if got := words(src.Describe(ec2, instances, rows[0])); got != "aws ec2 describe-instances --instance-ids i-1 --output json" {
		t.Errorf("describe: %s", got)
	}
	if got := words(src.Act(ec2, instances, stop, rows[0])); got != "aws ec2 stop-instances --instance-ids i-1" {
		t.Errorf("act: %s", got)
	}

	tables := &Resource{List: "list-tables", Items: "TableNames[]", Label: "TableName"}
	rows, next, _ = src.Rows(tables, []byte(`{"TableNames":["a","b"]}`))
	if len(rows) != 2 || rows[1].ID != "b" || rows[1].Name != "" || string(rows[1].Raw) != `"b"` || next != "" {
		t.Errorf("a list of names: %+v %q", rows, next)
	}
	if src.Describe(&Service{Name: "dynamodb"}, tables, rows[0]) != nil {
		t.Error("no describe command, yet a describe")
	}

	functions := &Resource{Items: "Functions[]", ID: "FunctionName", Label: "FunctionName", State: "Status"}
	rows, _, _ = src.Rows(functions, []byte(`{"Functions":[{"FunctionName":"f","Status":"Active"}]}`))
	if rows[0].Name != "f" || rows[0].State != "Active" {
		t.Errorf("label and state: %+v", rows[0])
	}

	if _, _, err := src.Rows(instances, []byte(`[`)); err == nil {
		t.Error("bad JSON read")
	}
}

func TestGCP(t *testing.T) {
	src, _ := For("gcp")
	compute := &Service{Name: "compute"}
	vms := &Resource{Name: "instances", List: "instances list", ID: "name",
		Location: &Location{Flag: "--zone", Field: "zone"}, Describe: &Action{Command: "instances describe"}}
	if got := words(src.List(compute, vms, "ignored")); got != "gcloud compute instances list --format=json --limit=500" {
		t.Errorf("list: %s", got)
	}
	rows, next, err := src.Rows(vms, []byte(`[
		{"name":"vm-1","status":"RUNNING","zone":"https://www.googleapis.com/compute/v1/projects/p/zones/asia-southeast1-a"},
		{"name":"projects/p/locations/l/things/t","state":"ACTIVE"}]`))
	if err != nil || next != "" || len(rows) != 2 {
		t.Fatalf("rows %+v next %q err %v", rows, next, err)
	}
	if rows[0].ID != "vm-1" || rows[0].Name != "" || rows[0].State != "RUNNING" || rows[0].Where != "asia-southeast1-a" {
		t.Errorf("zonal row: %+v", rows[0])
	}
	if rows[1].Name != "t" || rows[1].State != "ACTIVE" {
		t.Errorf("full path row: %+v", rows[1])
	}
	if got := words(src.Describe(compute, vms, rows[0])); got != "gcloud compute instances describe vm-1 --zone=asia-southeast1-a --format=json" {
		t.Errorf("describe: %s", got)
	}
	stop := Action{Command: "instances stop"}
	if got := words(src.Act(compute, vms, stop, rows[0])); got != "gcloud compute instances stop vm-1 --zone=asia-southeast1-a --quiet" {
		t.Errorf("act: %s", got)
	}
	if got := words(src.Act(compute, vms, stop, rows[1])); got != "gcloud compute instances stop projects/p/locations/l/things/t --quiet" {
		t.Errorf("a full path says where already: %s", got)
	}

	run := &Resource{ID: "metadata.name", Location: &Location{Flag: "--region", Field: `metadata.labels."cloud.googleapis.com/location"`}}
	rows, _, _ = src.Rows(run, []byte(`[{"metadata":{"name":"svc","labels":{"cloud.googleapis.com/location":"asia-southeast1"}}}]`))
	if rows[0].ID != "svc" || rows[0].Where != "asia-southeast1" {
		t.Errorf("quoted path: %+v", rows[0])
	}
	if src.Describe(&Service{Name: "run"}, run, rows[0]) != nil {
		t.Error("no describe command, yet a describe")
	}
	if _, _, err := src.Rows(vms, []byte(`{}`)); err == nil {
		t.Error("an object read as a list")
	}
}

func TestPaths(t *testing.T) {
	if got := split(`a."b.c".d`); !reflect.DeepEqual(got, []string{"a", "b.c", "d"}) {
		t.Errorf("split: %q", got)
	}
	if str(map[string]any{"a": 1.0}, "a") != "" || str(nil, "a") != "" {
		t.Error("str of a number or nothing")
	}
}
