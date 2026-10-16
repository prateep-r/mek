package main

import "github.com/prateep-r/mek/internal/catalog"

// acts are the verbs that become action keys: what changes one resource's
// life and needs nothing but its name.
var acts = map[string]bool{
	"delete": true, "terminate": true, "deregister": true, "undelete": true, "restore": true,
	"start": true, "stop": true, "reboot": true, "restart": true, "reset": true,
	"resume": true, "pause": true, "suspend": true, "enable": true, "disable": true,
	"cancel": true, "retry": true, "failover": true, "rollback": true, "rotate": true,
}

// tuned fixes popular resources that the models alone get wrong, keyed
// "<cloud>/<service>/<resource>".
var tuned = map[string]func(*catalog.Resource){
	// Unfiltered, these list every public image and snapshot.
	"aws/ec2/images":    listArgs("--owners", "self"),
	"aws/ec2/snapshots": listArgs("--owner-ids", "self"),
	// Cloud Run lists Knative objects.
	"gcp/run/services": func(r *catalog.Resource) {
		r.ID = "metadata.name"
		r.Location = &catalog.Location{Flag: "--region", Field: `metadata.labels."cloud.googleapis.com/location"`}
	},
	"gcp/projects/projects": func(r *catalog.Resource) { r.ID = "projectId" },
	// --secret-id takes the name too.
	"aws/secretsmanager/secrets": func(r *catalog.Resource) {
		r.Describe = &catalog.Action{Command: "describe-secret", Param: "--secret-id"}
		r.Actions = []catalog.Action{
			{Command: "delete-secret", Param: "--secret-id"},
			{Command: "restore-secret", Param: "--secret-id"},
			{Command: "rotate-secret", Param: "--secret-id"},
		}
	},
}

func listArgs(args ...string) func(*catalog.Resource) {
	return func(r *catalog.Resource) { r.ListArgs = args }
}

// tune applies tuned to a catalog's resources.
func tune(c *catalog.Catalog) {
	for i := range c.Services {
		s := &c.Services[i]
		for j := range s.Resources {
			if f := tuned[c.Cloud+"/"+s.Name+"/"+s.Resources[j].Name]; f != nil {
				f(&s.Resources[j])
			}
		}
	}
}
