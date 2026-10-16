package catalog

import (
	"bytes"
	"compress/gzip"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func find(t *testing.T, c *Catalog, svc, res string) (*Service, *Resource) {
	t.Helper()
	for i := range c.Services {
		s := &c.Services[i]
		if s.Name != svc {
			continue
		}
		for j := range s.Resources {
			if s.Resources[j].Name == res {
				return s, &s.Resources[j]
			}
		}
	}
	t.Fatalf("%s %s not in the %s catalog", svc, res, c.Cloud)
	return nil, nil
}

func TestLoadEmbedded(t *testing.T) {
	aws, err := Load("aws")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := Load("aws"); again != aws {
		t.Error("Load decoded the catalog twice")
	}
	if aws.Cloud != "aws" || !strings.HasPrefix(aws.Source, "aws-cli/") {
		t.Errorf("aws catalog: %q from %q", aws.Cloud, aws.Source)
	}
	_, ec2 := find(t, aws, "ec2", "instances")
	if ec2.Items != "Reservations[].Instances[]" || ec2.ID != "InstanceId" || ec2.Label != "tag:Name" || ec2.State != "State.Name" {
		t.Errorf("ec2 instances: %+v", ec2)
	}
	var verbs []string
	for _, a := range ec2.Actions {
		verbs = append(verbs, a.Command)
	}
	for _, v := range []string{"start-instances", "stop-instances", "reboot-instances", "terminate-instances"} {
		if !slices.Contains(verbs, v) {
			t.Errorf("ec2 instances has no %s: %v", v, verbs)
		}
	}
	_, rds := find(t, aws, "rds", "db-instances")
	if rds.ID != "DBInstanceIdentifier" || rds.Describe == nil {
		t.Errorf("rds db-instances: %+v", rds)
	}

	gcp, err := Load("gcp")
	if err != nil {
		t.Fatal(err)
	}
	_, vm := find(t, gcp, "compute", "instances")
	if vm.List != "instances list" || vm.Location == nil || vm.Location.Flag != "--zone" || vm.Describe == nil {
		t.Errorf("compute instances: %+v", vm)
	}
}

func gz(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(s))
	w.Close()
	return b.Bytes()
}

func TestLoadErrors(t *testing.T) {
	old := files
	t.Cleanup(func() { files = old })
	files = fstest.MapFS{
		"notgz.json.gz":    {Data: []byte("plain text, not gzip")},
		"cut.json.gz":      {Data: gz(`{"cloud":"x"}`)[:12]},
		"notjson.json.gz":  {Data: gz(`nope`)},
		"working.json.gz":  {Data: gz(`{"cloud":"working","services":[{"name":"s"}]}`)},
		"elsewhere.txt.gz": {Data: nil},
	}
	for cloud, want := range map[string]string{
		"azure":   "no resource catalog for azure",
		"notgz":   "notgz catalog: gzip: invalid header",
		"cut":     "cut catalog: unexpected EOF",
		"notjson": "notjson catalog: invalid character",
	} {
		if _, err := Load(cloud); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("Load(%s) = %v, want %s", cloud, err, want)
		}
	}
	if c, err := Load("working"); err != nil || c.Services[0].Name != "s" {
		t.Errorf("Load(working) = %+v, %v", c, err)
	}
}
