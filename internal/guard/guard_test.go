package guard

import (
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

func TestClassifyAWS(t *testing.T) {
	cases := map[string]Class{
		"":                        Read,
		"sts get-caller-identity": Read,
		"--region ap-southeast-1 ec2 describe-instances":       Read,
		"ec2 describe-instances --filters Name=x,Values=y":     Read,
		"rds list-tags-for-resource --resource-name x":         Read,
		"logs tail /aws/lambda/x --follow":                     Read,
		"s3 ls s3://bucket":                                    Read,
		"s3 cp a s3://b/a":                                     Write,
		"s3 sync . s3://b":                                     Write,
		"s3 sync . s3://b --delete":                            Destructive,
		"s3 rm s3://b/a":                                       Destructive,
		"s3api delete-object --bucket b --key k":               Destructive,
		"ec2 terminate-instances --instance-ids i-1":           Destructive,
		"ec2 terminate-instances --instance-ids i-1 --dry-run": Read,
		"ec2 stop-instances --instance-ids i-1":                Destructive,
		"rds failover-db-cluster --db-cluster-identifier c":    Destructive,
		"ecs update-service --cluster c --service s":           Write,
		"ec2 run-instances --image-id ami-1":                   Write,
		"iam create-user --user-name u":                        Write,
		"kms decrypt --ciphertext-blob x":                      Write, // unknown verb -> conservative
		"configure list":                                       Read,
		"sso login":                                            Read,
		"ec2":                                                  Read,
		"ec2 describe-instances help":                          Read,
	}
	for in, want := range cases {
		if got := ClassifyAWS(strings.Fields(in)); got != want {
			t.Errorf("aws %q = %s, want %s", in, got, want)
		}
	}
}

func TestClassifyGCloud(t *testing.T) {
	cases := map[string]Class{
		"compute instances list":                   Read,
		"compute instances describe vm-1 --zone a": Read,
		"--project p compute instances list":       Read,
		"projects get-iam-policy p":                Read,
		"logging read severity>=ERROR --limit 10":  Read,
		"run services list":                        Read,
		"run deploy svc --image x":                 Write,
		"compute instances create vm-1":            Write,
		"compute instances delete vm-1 --zone a":   Destructive,
		"compute instances stop vm-1":              Destructive,
		"sql instances patch db --tier x":          Write,
		"beta container clusters delete c":         Destructive,
		"container clusters get-credentials c":     Read,
		"config set project p":                     Read, // local only
		"auth login":                               Read,
		"compute ssh vm-1":                         Write,
		"compute":                                  Read,
		"foo bar baz":                              Write,
	}
	for in, want := range cases {
		if got := ClassifyGCloud(strings.Fields(in)); got != want {
			t.Errorf("gcloud %q = %s, want %s", in, got, want)
		}
	}
}

func TestDecide(t *testing.T) {
	plain := &config.Context{}
	prot := &config.Context{Protected: true}
	ro := &config.Context{Protected: true, ReadOnly: true}
	cases := []struct {
		ctx  *config.Context
		c    Class
		want Decision
	}{
		{plain, Destructive, Allow},
		{prot, Read, Allow},
		{prot, Write, Confirm},
		{prot, Unknown, Confirm},
		{prot, Destructive, ConfirmTyped},
		{ro, Read, Allow},
		{ro, Write, Block},
		{ro, Unknown, Block},
	}
	for _, c := range cases {
		if got := Decide(c.ctx, c.c); got != c.want {
			t.Errorf("Decide(%+v,%s)=%d want %d", c.ctx, c.c, got, c.want)
		}
	}
}
