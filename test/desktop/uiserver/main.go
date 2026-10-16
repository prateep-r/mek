// Command uiserver serves mek-desktop's built frontend to a browser, with
// the Wails runtime replaced by one that calls internal/desktop's real
// Service over HTTP — so the desktop e2e tests (Playwright,
// cmd/mek-desktop/frontend/e2e) drive the window's own code and the
// service behind it, without a native webview.
//
// It makes its own world in a temporary directory: a MEK_HOME with
// contexts, a background tunnel and audit entries, and a stub mek that
// records each command and answers the lookups. GET /e2e/log is what the
// stub ran; POST /e2e/reset forgets it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/prateep-r/mek/internal/audit"
	"github.com/prateep-r/mek/internal/desktop"
	"github.com/prateep-r/mek/internal/view"
)

const config = `contexts:
  dev:
    provider: aws
    aws_profile: dev
    region: ap-southeast-1
    targets: {bastion: {instance: "tag:Name=bastion"}}
    clusters: {main: {name: dev-eks}}
    tunnels: {db: {via: bastion, host: orders-db.internal, port: 5432}}
  prod:
    provider: aws
    aws_profile: prod
    region: ap-southeast-1
    protected: true
    tunnels: {db: {via: "tag:Name=bastion", host: prod-db.internal, port: 5432, local_port: 25432}}
  audit:
    provider: aws
    aws_profile: audit
    readonly: true
`

// stub is the mek the service runs: it logs its arguments and answers.
const stub = `#!/bin/sh
printf '%s\n' "$*" >> "$MEK_E2E_LOG"
case "$*" in
  *"ec2 describe-instances --output json"*)
    echo '{"Reservations":[{"Instances":[{"InstanceId":"i-0aaa","State":{"Name":"running"},"Tags":[{"Key":"Name","Value":"web"}]},{"InstanceId":"i-0bbb","State":{"Name":"stopped"}}]}]}' ;;
  *"describe-instances --instance-ids"*)
    echo '{"Reservations":[{"Instances":[{"InstanceId":"i-0aaa","InstanceType":"t3.micro"}]}]}' ;;
  "use "*) echo "✓ now using ${2}" ;;
  *"stop-instances"*) echo '{"StoppingInstances":[]}' ;;
  *) echo "done" ;;
esac
`

func main() {
	dist := flag.String("dist", "dist", "the frontend's build")
	addr := flag.String("addr", "127.0.0.1:18767", "where to listen")
	flag.Parse()

	dir, err := os.MkdirTemp("", "mek-desktop-e2e-")
	if err != nil {
		log.Fatal(err)
	}
	home, logFile, mek := filepath.Join(dir, "home"), filepath.Join(dir, "mek.log"), filepath.Join(dir, "mek")
	must(os.MkdirAll(home, 0o700))
	must(os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600))
	must(os.WriteFile(filepath.Join(home, "state.yaml"), []byte("current: dev\n"), 0o600))
	must(os.WriteFile(mek, []byte(stub), 0o755))
	must(os.WriteFile(logFile, nil, 0o600))
	os.Setenv("MEK_HOME", home)
	os.Setenv("MEK_E2E_LOG", logFile)
	for _, e := range []audit.Entry{
		{Time: time.Now().Add(-time.Minute), Context: "dev", Decision: "allowed", Class: "read", Command: []string{"aws", "s3", "ls"}},
		{Time: time.Now(), Context: "audit", Decision: "blocked", Class: "destructive", ExitCode: -1, Command: []string{"aws", "s3", "rb", "s3://old"}},
	} {
		must(audit.Write(e))
	}

	svc := desktop.New(view.Live{}, mek)
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(*dist)))
	mux.HandleFunc("/wails/runtime.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, runtime)
	})
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) { call(w, r, svc) })
	mux.HandleFunc("/e2e/log", func(w http.ResponseWriter, _ *http.Request) {
		b, _ := os.ReadFile(logFile)
		w.Write(b)
	})
	mux.HandleFunc("/e2e/reset", func(w http.ResponseWriter, _ *http.Request) { os.WriteFile(logFile, nil, 0o600) })
	log.Printf("mek-desktop e2e on http://%s (world in %s)", *addr, dir)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// runtime stands in for Wails's: Call.ByName posts to /call.
const runtime = `export const Call = {
  ByName: async (name, ...args) => {
    const r = await fetch("/call", { method: "POST", body: JSON.stringify({ name, args }) });
    const j = await r.json();
    if (j.error) throw new Error(j.error);
    return j.result;
  },
};
`

// call runs a Service method by name ("…desktop.Service.Overview"), its
// arguments decoded into the method's own parameter types, as Wails does.
func call(w http.ResponseWriter, r *http.Request, svc *desktop.Service) {
	var req struct {
		Name string
		Args []json.RawMessage
	}
	reply := func(result any, err error) {
		out := map[string]any{"result": result}
		if err != nil {
			out["error"] = err.Error()
		}
		json.NewEncoder(w).Encode(out)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		reply(nil, err)
		return
	}
	m := reflect.ValueOf(svc).MethodByName(req.Name[strings.LastIndex(req.Name, ".")+1:])
	if !m.IsValid() || m.Type().NumIn() != len(req.Args) {
		reply(nil, fmt.Errorf("no method %s with %d arguments", req.Name, len(req.Args)))
		return
	}
	in := make([]reflect.Value, len(req.Args))
	for i, raw := range req.Args {
		v := reflect.New(m.Type().In(i))
		if err := json.Unmarshal(raw, v.Interface()); err != nil {
			reply(nil, fmt.Errorf("argument %d: %w", i, err))
			return
		}
		in[i] = v.Elem()
	}
	var result any
	var err error
	for _, v := range m.Call(in) {
		if e, ok := v.Interface().(error); ok || v.Type().String() == "error" {
			err = e
			continue
		}
		result = v.Interface()
	}
	reply(result, err)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
