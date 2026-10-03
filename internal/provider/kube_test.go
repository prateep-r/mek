package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/prateep-r/mek/internal/config"
)

// fakeQuery records the command and answers with out/err.
func fakeQuery(out string, err error, got *[]string) Query {
	return func(argv []string) ([]byte, error) {
		*got = argv
		return []byte(out), err
	}
}

func TestValidateClusters(t *testing.T) {
	aws := "contexts:\n  a:\n    provider: aws\n    aws_profile: p\n"
	gcp := "contexts:\n  a:\n    provider: gcp\n    project: p\n"
	cases := []struct{ in, want string }{
		{aws + "    clusters:\n      m: {name: x}\n", "needs a region"},
		{aws + "    region: r\n    clusters:\n      m: {name: x, location: z}\n", "use region, not location"},
		{gcp + "    clusters:\n      m: {name: x}\n", "needs a location"},
		{gcp + "    clusters:\n      m: {name: x, location: z, region: r}\n", "not region"},
		{"contexts:\n  a:\n    provider: huawei\n    hcloud_profile: p\n    clusters:\n      m: {name: x}\n", "not supported on huawei"},
	}
	for _, c := range cases {
		if err := parse(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("parse(%q) err=%v, want containing %q", c.in, err, c.want)
		}
	}
	for _, ok := range []string{
		aws + "    region: r\n    clusters:\n      m: {name: x}\n",
		aws + "    clusters:\n      m: {name: x, region: r}\n",
		gcp + "    clusters:\n      m: {name: x, location: asia-southeast1-a}\n",
	} {
		if err := parse(ok); err != nil {
			t.Errorf("parse(%q) should be valid: %v", ok, err)
		}
	}
}

func TestCheckContext(t *testing.T) {
	cfg, err := config.Parse([]byte("contexts:\n  a:\n    provider: gcp\n    project: p\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := *cfg.Contexts["a"]
	c.Clusters = map[string]*config.Cluster{"x": {Name: "x", Location: "z"}}
	if err := CheckContext(cfg, &c); err != nil {
		t.Errorf("valid: %v", err)
	}
	c.Clusters["x"].Location = ""
	if err := CheckContext(cfg, &c); err == nil || !strings.Contains(err.Error(), "needs a location") {
		t.Errorf("cloud rule: %v", err)
	}
	c.Clusters["x"].Name = "-x"
	if err := CheckContext(cfg, &c); err == nil || !strings.Contains(err.Error(), "must not start") {
		t.Errorf("config rule: %v", err)
	}
}

func TestEKS(t *testing.T) {
	a := &AWS{ctx: &config.Context{Name: "a", Provider: config.ProviderAWS, Region: "ap-southeast-1"}}
	var argv []string
	q := fakeQuery(`{"endpoint":"https://E.eks.amazonaws.com","ca":"Q0E=","status":"ACTIVE"}`, nil, &argv)
	got, err := a.DescribeCluster(config.Cluster{Name: "prod"}, q)
	if err != nil || got != (KubeCluster{Server: "https://E.eks.amazonaws.com", CAData: "Q0E=", Location: "ap-southeast-1"}) {
		t.Fatalf("describe: %+v %v", got, err)
	}
	if s := strings.Join(argv, " "); !strings.HasPrefix(s, "aws eks describe-cluster --name prod --region ap-southeast-1 --query") {
		t.Errorf("argv: %s", s)
	}
	if got, _ := a.DescribeCluster(config.Cluster{Name: "x", Region: "us-east-1"}, q); got.Location != "us-east-1" {
		t.Errorf("cluster region: %+v", got)
	}
	for out, want := range map[string]string{
		`{"status":"CREATING","endpoint":"https://e"}`: "is CREATING, not ACTIVE",
		`{}`:       "is -, not ACTIVE",
		`not json`: "eks describe-cluster prod",
	} {
		if _, err := a.DescribeCluster(config.Cluster{Name: "prod"}, fakeQuery(out, nil, &argv)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", out, err)
		}
	}
	boom := errors.New("boom")
	if _, err := a.DescribeCluster(config.Cluster{Name: "prod"}, fakeQuery("", boom, &argv)); !errors.Is(err, boom) {
		t.Errorf("query error: %v", err)
	}
	if s := strings.Join(a.TokenCommand("prod", "r"), " "); s != "aws eks get-token --cluster-name prod --region r --output json" {
		t.Errorf("token: %s", s)
	}
}

func TestGKE(t *testing.T) {
	g := &GCP{ctx: &config.Context{Name: "g", Provider: config.ProviderGCP, Project: "p"}}
	var argv []string
	c := config.Cluster{Name: "gke-1", Location: "asia-southeast1"}
	q := fakeQuery(`{"endpoint":"34.1.2.3","masterAuth":{"clusterCaCertificate":"Q0E="},"status":"RUNNING"}`, nil, &argv)
	got, err := g.DescribeCluster(c, q)
	if err != nil || got != (KubeCluster{Server: "https://34.1.2.3", CAData: "Q0E=", Location: "asia-southeast1"}) {
		t.Fatalf("describe: %+v %v", got, err)
	}
	if s := strings.Join(argv, " "); s != "gcloud container clusters describe gke-1 --location asia-southeast1 --format json(endpoint,masterAuth.clusterCaCertificate,status)" {
		t.Errorf("argv: %s", s)
	}
	for out, want := range map[string]string{
		`{"status":"PROVISIONING"}`: "is PROVISIONING, not RUNNING",
		`[`:                         "gke describe gke-1",
	} {
		if _, err := g.DescribeCluster(c, fakeQuery(out, nil, &argv)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", out, err)
		}
	}
	boom := errors.New("boom")
	if _, err := g.DescribeCluster(c, fakeQuery("", boom, &argv)); !errors.Is(err, boom) {
		t.Errorf("query error: %v", err)
	}
	if s := strings.Join(g.TokenCommand("gke-1", "z"), " "); s != "gke-gcloud-auth-plugin" {
		t.Errorf("token: %s", s)
	}
	ctx := &config.Context{}
	if gcpCloud.Plugins[0].Needed(ctx) {
		t.Error("plugin needed without clusters")
	}
	ctx.Clusters = map[string]*config.Cluster{"m": {}}
	if !gcpCloud.Plugins[0].Needed(ctx) {
		t.Error("plugin not needed with clusters")
	}
}
