// The window's calls into mek: internal/desktop's Service, bound by Wails.
// Each returns a promise; a Go error rejects it.
const service = "github.com/prateep-r/mek/internal/desktop.Service";

let runtime;
async function call(method, ...args) {
  runtime ??= await import(/* @vite-ignore */ "/wails/runtime.js");
  return runtime.Call.ByName(`${service}.${method}`, ...args);
}

export const api = {
  overview: () => call("Overview"),
  audit: (n) => call("Audit", n),
  tunnelLog: (id) => call("TunnelLog", id),
  run: (plan, answer) => call("Run", plan, answer),
  use: (name) => call("UseContext", name),
  startTunnel: (ctx, name) => call("StartTunnel", ctx, name),
  stopTunnel: (id) => call("StopTunnel", id),
  kubeconfig: (ctx, alias) => call("WriteKubeconfig", ctx, alias),
  removeContext: (name) => call("RemoveContext", name),
  login: (ctx) => call("Login", ctx),
  shell: (ctx, target) => call("Shell", ctx, target),
  k9s: (ctx, alias) => call("K9s", ctx, alias),
  addContext: () => call("AddContext"),
  editContext: (name) => call("EditContext", name),
  addEntry: (ctx, kind) => call("AddEntry", ctx, kind),
  resources: (ctx) => call("Resources", ctx),
  list: (ctx, svc, kind, next) => call("List", ctx, svc, kind, next),
  describe: (ctx, svc, kind, row) => call("Describe", ctx, svc, kind, row),
  act: (ctx, svc, kind, action, row) => call("Act", ctx, svc, kind, action, row),
};
