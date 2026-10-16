// Package catalog is what mek ui's Resources tab can show: for each cloud,
// its services, their kinds of resource, how to list them and what can be
// done to one. It is generated from the clouds' own API models
// (tools/catalog) and embedded; every command it yields is run through mek,
// so the guard and the audit log apply.
package catalog

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sync"
)

// Catalog is one cloud's services.
type Catalog struct {
	Cloud    string    `json:"cloud"`
	Source   string    `json:"source"` // the CLI version the models came from
	Services []Service `json:"services"`
}

// Service is a CLI command group with resources: "ec2", "compute".
type Service struct {
	Name      string     `json:"name"`
	Resources []Resource `json:"resources"`
}

// Resource is a kind of resource and how to reach it. Commands are the
// words after the service: "describe-instances", "instances list".
type Resource struct {
	Name     string    `json:"name"`               // "instances"
	List     string    `json:"list"`               // the command that lists them
	ListArgs []string  `json:"listArgs,omitempty"` // what the list always takes: "--owners self"
	Items    string    `json:"items,omitempty"`    // aws: where the items are: "Reservations[].Instances[]"
	ID       string    `json:"id,omitempty"`       // the id's path in an item ("": the item is its id)
	Label    string    `json:"label,omitempty"`    // the name's path, or "tag:Name"
	State    string    `json:"state,omitempty"`    // the state's path: "State.Name"
	Paged    bool      `json:"paged,omitempty"`    // aws: the list takes --max-items / --starting-token
	Location *Location `json:"location,omitempty"` // gcp: the flag an action needs for where the item is
	Describe *Action   `json:"describe,omitempty"`
	Actions  []Action  `json:"actions,omitempty"`
}

// Action is a command that takes one resource: "stop-instances
// --instance-ids <id>", or for gcloud "instances stop <name>".
type Action struct {
	Command string `json:"command"`
	Param   string `json:"param,omitempty"` // "--instance-ids"; "": a positional name
}

// Location is how a gcloud action says where its resource is: --zone with
// the item's zone (a URL or a name; the last part is used).
type Location struct {
	Flag  string `json:"flag"`  // "--zone"
	Field string `json:"field"` // "zone"; a path: metadata.labels."cloud.googleapis.com/location"
}

//go:embed *.json.gz
var embedded embed.FS

var (
	files  fs.ReadFileFS = embedded // test seam
	mu     sync.Mutex
	loaded = map[string]*Catalog{}
)

// Load is a cloud's catalog, read once: only the Resources tab needs it, so
// nothing else pays for decoding it.
func Load(cloud string) (*Catalog, error) {
	mu.Lock()
	defer mu.Unlock()
	if c := loaded[cloud]; c != nil {
		return c, nil
	}
	b, err := files.ReadFile(cloud + ".json.gz")
	if err != nil {
		return nil, fmt.Errorf("no resource catalog for %s", cloud)
	}
	c, err := decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s catalog: %w", cloud, err)
	}
	loaded[cloud] = c
	return c, nil
}

func decode(b []byte) (*Catalog, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
