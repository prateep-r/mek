package main

import (
	"encoding/json"
	"testing"

	"github.com/prateep-r/mek/internal/catalog"
)

func TestSingular(t *testing.T) {
	for in, want := range map[string]string{
		"describe-instances": "instance", "list-policies": "policy", "describe-addresses": "address",
		"list-branches": "branch", "describe-db-instances": "dbinstance", "list-functions": "function",
	} {
		if got := singular(in); got != want {
			t.Errorf("singular(%s) = %s, want %s", in, got, want)
		}
	}
}

// A slice of EC2 and DynamoDB, the shapes that need following down a level
// and a list of names.
const ec2Model = `{
 "operations": {
  "DescribeInstances": {"output": {"shape": "DescribeInstancesResult"}},
  "DescribeAccountAttributes": {"output": {"shape": "Attrs"}}
 },
 "shapes": {
  "DescribeInstancesResult": {"type": "structure", "members": {"Reservations": {"shape": "ReservationList"}, "NextToken": {"shape": "String"}}},
  "ReservationList": {"type": "list", "member": {"shape": "Reservation"}},
  "Reservation": {"type": "structure", "members": {"Groups": {"shape": "GroupList"}, "Instances": {"shape": "InstanceList"}, "OwnerId": {"shape": "String"}}},
  "GroupList": {"type": "list", "member": {"shape": "String"}},
  "InstanceList": {"type": "list", "member": {"shape": "Instance"}},
  "Instance": {"type": "structure", "members": {"InstanceId": {"shape": "String"}, "ImageId": {"shape": "String"}, "State": {"shape": "InstanceState"}, "Tags": {"shape": "TagList"}}},
  "InstanceState": {"type": "structure", "members": {"Name": {"shape": "String"}}},
  "TagList": {"type": "list", "member": {"shape": "Tag"}},
  "Tag": {"type": "structure", "members": {"Key": {"shape": "String"}, "Value": {"shape": "String"}}},
  "Attrs": {"type": "structure", "members": {"Count": {"shape": "Integer"}}},
  "String": {"type": "string"},
  "Integer": {"type": "integer"}
 }
}`

func TestAWSResources(t *testing.T) {
	var model serviceModel
	if err := json.Unmarshal([]byte(ec2Model), &model); err != nil {
		t.Fatal(err)
	}
	cmds := map[string][]param{
		"describe-instances":          {{"instance-ids", false}, {"filters", false}},
		"describe-account-attributes": {},
		"stop-instances":              {{"instance-ids", true}, {"force", false}},
		"start-instances":             {{"instance-ids", true}},
		"modify-instance-attribute":   {{"instance-id", true}},                 // changes need more than the id
		"create-image":                {{"instance-id", true}, {"name", true}}, // two required
		"describe-images":             {{"image-ids", false}},
	}
	got := resources(cmds, model, map[string]map[string]any{"DescribeInstances": {}})
	want := []catalog.Resource{{
		Name: "instances", List: "describe-instances", Items: "Reservations[].Instances[]",
		ID: "InstanceId", Label: "tag:Name", State: "State.Name", Paged: true,
		Describe: &catalog.Action{Command: "describe-instances", Param: "--instance-ids"},
		Actions:  []catalog.Action{{Command: "start-instances", Param: "--instance-ids"}, {Command: "stop-instances", Param: "--instance-ids"}},
	}}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("resources:\n got %s\nwant %s", g, w)
	}
}

func TestAWSListOfNames(t *testing.T) {
	var model serviceModel
	json.Unmarshal([]byte(`{"operations": {"ListTables": {"output": {"shape": "Out"}}},
	 "shapes": {"Out": {"type": "structure", "members": {"TableNames": {"shape": "Names"}}},
	  "Names": {"type": "list", "member": {"shape": "String"}}, "String": {"type": "string"}}}`), &model)
	cmds := map[string][]param{
		"list-tables":    {},
		"describe-table": {{"table-name", true}},
		"delete-table":   {{"table-name", true}},
		"update-table":   {{"table-name", true}},
	}
	got := resources(cmds, model, nil)
	if len(got) != 1 || got[0].Items != "TableNames[]" || got[0].ID != "" ||
		got[0].Describe == nil || got[0].Describe.Command != "describe-table" ||
		len(got[0].Actions) != 1 || got[0].Actions[0].Command != "delete-table" {
		t.Errorf("tables: %+v", got)
	}
}

const gcpFixture = `{
 "SERIALIZED_FLAG_LIST": [{"is_required": false, "is_global": false}, {"is_required": true, "is_global": false}, {"is_required": false, "is_global": true}],
 "commands": {
  "compute": {"is_group": true, "commands": {
   "instances": {"is_group": true, "commands": {
    "list": {"flags": {"--zones": 0}, "positionals": [{"name": "names", "nargs": "*", "is_required": false}]},
    "describe": {"flags": {"--zone": 0}, "positionals": [{"name": "name", "nargs": "1", "is_required": true}]},
    "stop": {"flags": {"--zone": 0}, "positionals": [{"name": "names", "nargs": "+", "is_required": true}]},
    "set-machine-type": {"flags": {"--machine-type": 1}, "positionals": [{"name": "name", "nargs": "1", "is_required": true}]},
    "create": {"flags": {}, "positionals": [{"name": "names", "nargs": "+", "is_required": true}]}
   }},
   "networks": {"is_group": true, "commands": {
    "list": {"flags": {"--project": 2}},
    "delete": {"positionals": [{"name": "name", "nargs": "1", "is_required": true}],
     "constraints": {"arguments": [{"arguments": [0], "is_required": true}]}}
   }},
   "disks": {"is_group": true, "commands": {"list": {"flags": {"--region": 1}}}}
  }},
  "projects": {"is_group": true, "commands": {
   "list": {},
   "delete": {"positionals": [{"name": "id", "nargs": "1", "is_required": true}],
    "constraints": {"arguments": [{"arguments": [-1, 0], "is_required": true}]}}
  }},
  "info": {"is_group": false}
 }
}`

func TestGCPCatalog(t *testing.T) {
	var tree gcpTree
	if err := json.Unmarshal([]byte(gcpFixture), &tree); err != nil {
		t.Fatal(err)
	}
	got := tree.catalog("1.0")
	g, _ := json.Marshal(got)
	want := `{"cloud":"gcp","source":"gcloud/1.0","services":[` +
		`{"name":"compute","resources":[` +
		`{"name":"instances","list":"instances list","id":"name","location":{"flag":"--zone","field":"zone"},"describe":{"command":"instances describe"},"actions":[{"command":"instances stop"}]},` +
		`{"name":"networks","list":"networks list","id":"name"}]},` +
		`{"name":"projects","resources":[{"name":"projects","list":"list","id":"name","actions":[{"command":"delete"}]}]}]}`
	if string(g) != want {
		t.Errorf("gcp catalog:\n got %s\nwant %s", g, want)
	}
}

func TestTune(t *testing.T) {
	c := catalog.Catalog{Cloud: "aws", Services: []catalog.Service{{Name: "ec2", Resources: []catalog.Resource{{Name: "images"}, {Name: "vpcs"}}}}}
	tune(&c)
	if r := c.Services[0].Resources; len(r[0].ListArgs) != 2 || r[1].ListArgs != nil {
		t.Errorf("tuned: %+v", r)
	}
}

func TestAzureCatalog(t *testing.T) {
	cmds := map[string][]azArg{
		"vm list":         {{Dest: "resource_group_name", ID: true}},
		"vm show":         {{Dest: "resource_group_name", Required: true, ID: true}, {Dest: "vm_name", Required: true, ID: true}},
		"vm delete":       {{Dest: "resource_group_name", Required: true, ID: true}, {Dest: "vm_name", Required: true, ID: true}, {Dest: "yes"}},
		"vm resize":       {{Dest: "resource_group_name", Required: true, ID: true}, {Dest: "vm_name", Required: true, ID: true}, {Dest: "size", Required: true, Option: "--size"}},
		"aks list":        {},
		"aks stop":        {{Dest: "resource_group_name", Required: true, Option: "--resource-group"}, {Dest: "name", Required: true, Option: "--name"}},
		"acr list":        {},
		"acr delete":      {{Dest: "registry_name", Required: true, Option: "--name"}, {Dest: "yes"}},
		"account list":    {},                            // nothing takes one of its items
		"disk list":       {{Dest: "x", Required: true}}, // needs an argument: not a resource
		"lonely":          {},
		"group list":      {},
		"group show":      {{Dest: "resource_group_name", Required: true, ID: true}},
		"group lock list": {},
	}
	got, _ := json.Marshal(azureCatalog(cmds, "2.0"))
	want := `{"cloud":"azure","source":"azure-cli/2.0","services":[` +
		`{"name":"acr","resources":[{"name":"acr","list":"list","id":"id","label":"name","state":"provisioningState","actions":[{"command":"delete","param":"--name","flags":["--yes"],"byName":true}]}]},` +
		`{"name":"aks","resources":[{"name":"aks","list":"list","id":"id","label":"name","state":"provisioningState","actions":[{"command":"stop","param":"--name","byName":true,"group":true}]}]},` +
		`{"name":"group","resources":[{"name":"group","list":"list","id":"id","label":"name","state":"provisioningState","describe":{"command":"show","param":"--ids"}}]},` +
		`{"name":"vm","resources":[{"name":"vm","list":"list","id":"id","label":"name","state":"provisioningState","describe":{"command":"show","param":"--ids"},"actions":[{"command":"delete","param":"--ids","flags":["--yes"]}]}]}]}`
	if string(got) != want {
		t.Errorf("azure catalog:\n got %s\nwant %s", got, want)
	}
}
