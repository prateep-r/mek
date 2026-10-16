//go:build contract

package contract

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prateep-r/mek/internal/catalog"
)

// closedPort is an address nothing listens on: a CLI sent there parses its
// arguments, then fails to connect.
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// argvs is every command the Resources tab can run for some services'
// resources ("ec2", or one resource: "compute/instances"), with a made-up
// resource.
func argvs(t *testing.T, cloud string, want map[string]bool) [][]string {
	t.Helper()
	c, err := catalog.Load(cloud)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := catalog.For(cloud)
	// An id long enough for any id's minimum length (the CLI checks those).
	row := catalog.Row{ID: "arn:aws:mek:us-east-1:111122223333:contract/0123456789abcdef0123456789", Where: "us-central1-a"}
	seen := map[string]bool{}
	var out [][]string
	for i := range c.Services {
		s := &c.Services[i]
		for j := range s.Resources {
			r := &s.Resources[j]
			if !want[s.Name] && !want[s.Name+"/"+r.Name] {
				continue
			}
			seen[s.Name], seen[s.Name+"/"+r.Name] = true, true
			out = append(out, src.List(s, r, ""))
			if d := src.Describe(s, r, row); d != nil {
				out = append(out, d)
			}
			for _, a := range r.Actions {
				out = append(out, src.Act(s, r, a, row))
			}
		}
	}
	for s := range want {
		if !seen[s] {
			t.Errorf("%s has no %s", cloud, s)
		}
	}
	return out
}

// parses runs each argv with cli and reports the ones whose arguments it
// rejected — an unknown option, a required one missing — not those it
// sent and failed to reach the cloud with, nor a made-up id it disliked.
func parses(t *testing.T, cli string, env []string, all [][]string) {
	t.Helper()
	var wg sync.WaitGroup
	jobs := make(chan []string)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for argv := range jobs {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				cmd := exec.CommandContext(ctx, cli, argv[1:]...)
				cmd.Env = env
				out, _ := cmd.CombinedOutput()
				if ctx.Err() != nil {
					t.Errorf("%s hangs: %s", cli, strings.Join(argv, " "))
				}
				cancel()
				if o := string(out); strings.Contains(o, "usage:") || strings.Contains(o, "Unknown options") ||
					strings.Contains(o, "arguments are required") {
					t.Errorf("%s rejects: %s\n%s", cli, strings.Join(argv, " "), out)
				}
			}
		}()
	}
	for _, argv := range all {
		jobs <- argv
	}
	close(jobs)
	wg.Wait()
}

// The catalog's commands for the popular AWS resources, as the real aws
// parses them: every option exists and every required one is given.
func TestCatalogParsesWithRealAWS(t *testing.T) {
	aws := realCLI(t, "aws")
	all := argvs(t, "aws", map[string]bool{"ec2/instances": true, "ec2/volumes": true, "ec2/snapshots": true, "ec2/images": true,
		"ec2/security-groups": true, "ec2/vpcs": true, "rds": true, "s3api": true, "lambda": true, "eks": true, "ecs": true,
		"dynamodb": true, "iam/roles": true, "iam/users": true, "sqs": true, "sns": true, "secretsmanager": true, "cloudformation": true})
	parses(t, aws, []string{"HOME=" + t.TempDir(), "AWS_ACCESS_KEY_ID=x", "AWS_SECRET_ACCESS_KEY=x", "AWS_REGION=us-east-1",
		"AWS_MAX_ATTEMPTS=1", "AWS_ENDPOINT_URL=http://" + closedPort(t), "AWS_CONFIG_FILE=/dev/null"}, all)
	t.Logf("%d aws commands parsed", len(all))
}

// A list, a describe and an action through mek with the real aws CLI: the
// output parses into rows, and the commands reach the API as asked.
func TestCatalogWithRealAWS(t *testing.T) {
	aws := realCLI(t, "aws")
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		calls = append(calls, r.Form.Get("Action")+" "+r.Form.Get("InstanceId.1"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/xml")
		switch r.Form.Get("Action") {
		case "DescribeInstances":
			w.Write([]byte(`<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><requestId>1</requestId>
<reservationSet><item><reservationId>r-1</reservationId><instancesSet>
<item><instanceId>i-0aaa</instanceId><instanceState><code>16</code><name>running</name></instanceState><tagSet><item><key>Name</key><value>web</value></item></tagSet></item>
<item><instanceId>i-0bbb</instanceId><instanceState><code>80</code><name>stopped</name></instanceState></item>
</instancesSet></item></reservationSet></DescribeInstancesResponse>`))
		case "StopInstances":
			w.Write([]byte(`<StopInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><requestId>1</requestId><instancesSet>
<item><instanceId>i-0aaa</instanceId><currentState><code>64</code><name>stopping</name></currentState><previousState><code>16</code><name>running</name></previousState></item>
</instancesSet></StopInstancesResponse>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u := newUser(t, "contexts:\n  dev: {provider: aws, aws_profile: fake, region: us-east-1}\n", []string{aws},
		"AWS_ENDPOINT_URL_EC2="+srv.URL)
	os.MkdirAll(filepath.Join(u.home, ".aws"), 0o700)
	os.WriteFile(filepath.Join(u.home, ".aws", "credentials"),
		[]byte("[fake]\naws_access_key_id = AKIAFAKEFAKEFAKEFAKE\naws_secret_access_key = fake\n"), 0o600)

	c, _ := catalog.Load("aws")
	src, _ := catalog.For("aws")
	var svc *catalog.Service
	var res *catalog.Resource
	for i := range c.Services {
		for j := range c.Services[i].Resources {
			if c.Services[i].Name == "ec2" && c.Services[i].Resources[j].Name == "instances" {
				svc, res = &c.Services[i], &c.Services[i].Resources[j]
			}
		}
	}
	out, code := u.run(append([]string{"-c", "dev"}, src.List(svc, res, "")...)...)
	if code != 0 {
		t.Fatalf("list (exit %d):\n%s", code, out)
	}
	rows, next, err := src.Rows(res, []byte(out))
	if err != nil || next != "" || len(rows) != 2 || rows[0].ID != "i-0aaa" || rows[0].Name != "web" || rows[0].State != "running" || rows[1].State != "stopped" {
		t.Fatalf("rows %+v next %q err %v:\n%s", rows, next, err, out)
	}
	if out, code = u.run(append([]string{"-c", "dev"}, src.Describe(svc, res, rows[0])...)...); code != 0 {
		t.Fatalf("describe (exit %d):\n%s", code, out)
	}
	var stop catalog.Action
	for _, a := range res.Actions {
		if a.Command == "stop-instances" {
			stop = a
		}
	}
	if out, code = u.run(append([]string{"-c", "dev"}, src.Act(svc, res, stop, rows[0])...)...); code != 0 || !strings.Contains(out, "stopping") {
		t.Fatalf("stop (exit %d):\n%s", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := "DescribeInstances ,DescribeInstances i-0aaa,StopInstances i-0aaa"; strings.Join(calls, ",") != want {
		t.Errorf("the API saw %q, want %q", calls, want)
	}
}

// The same with the real gcloud: Compute Engine instances listed, described
// and stopped, each where the list said it is.
func TestCatalogWithRealGcloud(t *testing.T) {
	gcloud := realCLI(t, "gcloud")
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		const vm = `{"name":"vm-1","status":"RUNNING","zone":"https://compute.googleapis.com/compute/v1/projects/acme/zones/asia-southeast1-b"}`
		const done = `{"name":"op-1","status":"DONE","zone":"https://compute.googleapis.com/compute/v1/projects/acme/zones/asia-southeast1-b",` +
			`"targetLink":"https://compute.googleapis.com/compute/v1/projects/acme/zones/asia-southeast1-b/instances/vm-1",` +
			`"selfLink":"https://compute.googleapis.com/compute/v1/projects/acme/zones/asia-southeast1-b/operations/op-1"}`
		switch r.URL.Path {
		case "/compute/v1/projects/acme/aggregated/instances":
			w.Write([]byte(`{"items":{"zones/asia-southeast1-b":{"instances":[` + vm + `]}}}`))
		case "/compute/v1/projects/acme/zones/asia-southeast1-b/instances/vm-1":
			w.Write([]byte(vm))
		case "/compute/v1/projects/acme/zones/asia-southeast1-b/instances/vm-1/stop",
			"/compute/v1/projects/acme/zones/asia-southeast1-b/operations/op-1",
			"/compute/v1/projects/acme/zones/asia-southeast1-b/operations/op-1/wait":
			w.Write([]byte(done))
		default:
			t.Logf("fake compute: unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u := newUser(t, "contexts:\n  g: {provider: gcp, project: acme}\n", []string{gcloud}, append(gcloudPython(t),
		"CLOUDSDK_AUTH_ACCESS_TOKEN=ya29.fake", "CLOUDSDK_API_ENDPOINT_OVERRIDES_COMPUTE="+srv.URL+"/compute/v1/")...)

	c, _ := catalog.Load("gcp")
	src, _ := catalog.For("gcp")
	var svc *catalog.Service
	var res *catalog.Resource
	for i := range c.Services {
		for j := range c.Services[i].Resources {
			if c.Services[i].Name == "compute" && c.Services[i].Resources[j].Name == "instances" {
				svc, res = &c.Services[i], &c.Services[i].Resources[j]
			}
		}
	}
	out, code := u.run(append([]string{"-c", "g"}, src.List(svc, res, "")...)...)
	if code != 0 {
		t.Fatalf("list (exit %d):\n%s", code, out)
	}
	rows, _, err := src.Rows(res, []byte(out))
	if err != nil || len(rows) != 1 || rows[0].ID != "vm-1" || rows[0].State != "RUNNING" || rows[0].Where != "asia-southeast1-b" {
		t.Fatalf("rows %+v err %v:\n%s", rows, err, out)
	}
	if out, code = u.run(append([]string{"-c", "g"}, src.Describe(svc, res, rows[0])...)...); code != 0 || !strings.Contains(out, `"vm-1"`) {
		t.Fatalf("describe (exit %d):\n%s", code, out)
	}
	var stop catalog.Action
	for _, a := range res.Actions {
		if a.Command == "instances stop" {
			stop = a
		}
	}
	if out, code = u.run(append([]string{"-c", "g"}, src.Act(svc, res, stop, rows[0])...)...); code != 0 {
		t.Fatalf("stop (exit %d):\n%s", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(calls, "POST /compute/v1/projects/acme/zones/asia-southeast1-b/instances/vm-1/stop") {
		t.Errorf("the API saw %q", calls)
	}
}
