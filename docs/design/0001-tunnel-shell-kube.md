# 0001 — `mek tunnel`, `mek shell`, `mek kube`

Status: **accepted** — `mek kube` / `mek kubectl` shipped in v0.6.0; `mek shell` and foreground
`mek tunnel` are done for v0.7.0; background tunnels follow in v0.8.0 (which also lists
foreground tunnels in `mek tunnel ls`).

## Problem

Getting *into* cloud resources is where people reach for long-lived keys and hand-edited
config: a port-forward to a private database, a shell on an instance, a kubeconfig for a
cluster. Each cloud has its own incantation (SSM documents, IAP tunnels, Bastion, kubelogin)
and each writes credentials or kubeconfig into shared files in `$HOME`. mek already solves
"which account, which credentials" per context; these commands extend that to access paths,
with the same guarantees: official CLIs only, no stored keys, isolated per context, guarded
and audited.

## Goals

- One verb per access path, the same on every cloud mek supports:
  `mek tunnel` (local port → private host), `mek shell` (interactive session on a host),
  `mek kube` (kubeconfig for a cluster, usable by kubectl / K9s / FreeLens).
- Credentials always come from the context (generated AWS profile, isolated gcloud/az dirs).
- Nothing written to the user's `~/.kube/config`, `~/.ssh`, `~/.aws` or `~/.azure` unless
  they opt in (`mek kube --merge`).
- Protected/readonly contexts behave predictably; every session is in the audit log.

## Non-goals

- Re-implementing tunnels or shells. mek orchestrates the official tools
  (`aws ssm start-session` + session-manager-plugin, `gcloud compute start-iap-tunnel`,
  `gcloud compute ssh --tunnel-through-iap`, `cloud-sql-proxy`, `aws eks get-token`,
  `gke-gcloud-auth-plugin`).
- Discovering every resource type. Targets are named in config or given explicitly.
- Caching tokens: every kubectl request gets a fresh one (no stored credentials).

## User experience

```bash
mek -c prod kube                  # writes <MEK_HOME>/kube/prod.yaml
mek -c prod kubectl get pods      # kubectl through the guard and audit log
eval "$(mek use --shell prod)"    # sets KUBECONFIG too: kubectl / k9s typed directly
mek -c prod kube --merge [--use]  # opt-in: add prod/* entries to ~/.kube/config
mek -c prod shell bastion         # v0.7.0: named target, instance id or tag:Key=Value
mek -c prod tunnel db             # v0.7.0: named tunnel from config → localhost:15432
mek -c prod tunnel db -b          # v0.8.0: in the background; mek tunnel ls / stop / logs
```

Config (optional keys per context):

```yaml
contexts:
  prod:
    provider: aws
    # ...existing fields...
    clusters:                                   # v0.6.0
      main: {name: prod-eks, namespace: app}    # aws: region defaults to the context's
    targets:                                    # v0.7.0, for shell and as tunnel hops
      bastion: {instance: "tag:Name=bastion"}   # or i-0abc123
    tunnels:
      db: {via: bastion, host: mydb.cluster-xyz.ap-southeast-1.rds.amazonaws.com, port: 5432, local_port: 15432}
  gcp-prod:
    provider: gcp
    clusters:
      apps: {name: apps-1, location: asia-southeast1}   # zone or region
```

Shared rules: names follow the context-name rules and must not be a subcommand (`token`,
`ls`, `stop`, `logs`); no value may start with `-` or contain control characters (they become
CLI arguments). Each cloud adds its own (`provider.Cloud.Validate`).

## How it maps to each cloud

| | AWS | GCP | Azure (later) | Huawei Cloud (later) |
|---|---|---|---|---|
| kube | `aws eks describe-cluster` → kubeconfig; token `aws eks get-token` | `gcloud container clusters describe` → kubeconfig; token `gke-gcloud-auth-plugin` | `az aks` + kubelogin | CCE (static certificate — undecided) |
| shell | `aws ssm start-session --target <id>` | `gcloud compute ssh <vm> --tunnel-through-iap` | `az network bastion ssh` | ❓ |
| tunnel | SSM `AWS-StartPortForwardingSession[ToRemoteHost]` | IAP → VM port; IAP + SSH `-L` to another host; `cloud-sql-proxy` | `az network bastion tunnel` | ❓ |
| needs | session-manager-plugin | gke-gcloud-auth-plugin, cloud-sql-proxy | kubelogin | — |

Target resolution: an instance id is used as is; `tag:Key=Value` (AWS) / VM name (GCP) is
resolved with a read-only describe call and must match exactly one running instance.

## Design decisions

1. **Kubeconfig credentials go through mek.** mek renders the kubeconfig itself from the
   describe call (one path for every cloud, golden-file tested) instead of editing the output
   of `update-kubeconfig`/`get-credentials`. The exec plugin runs
   `mek --context <ctx> kube token --name <cluster> --location <region|zone>`, which runs the
   cloud's token command in the context environment. The context is always explicit, so a
   kubeconfig never follows `mek use`, and K9s/FreeLens started from the Dock work. The
   kubeconfig embeds mek's path (the stable one on `PATH` when it is the running binary, e.g.
   Homebrew's symlink) and `PATH` at generation time, so GUI apps find the cloud CLIs.
   `kube token` is not audited (kubectl calls it constantly); the describe calls of
   `mek kube` are audited as reads.
2. **One kubeconfig file per context** at `<MEK_HOME>/kube/<ctx>.yaml`, written atomically
   with mode 0600. Kube context names are `<ctx>/<cluster>`, unique across mek contexts.
   The context's env gets `KUBECONFIG` when it has clusters (or a file from an ad-hoc
   `--name`), so `mek exec`, `mek env`, `mek use --shell` and `mek kubectl` all see it.
   Switching to a context without clusters unsets `KUBECONFIG` only when it points into
   `<MEK_HOME>/kube/` — never a value the user set.
3. **`~/.kube/config` is opt-in.** `mek kube --merge` adds the entries with the official
   `kubectl config set-cluster/set-credentials/set-context` commands (kubectl merges; mek
   never parses or writes that file), into the first `$KUBECONFIG` file that isn't mek's or
   `~/.kube/config`, after a one-time `.mek-backup`. It changes current-context only with
   `--use`. `--unmerge` deletes the `<ctx>/*` entries.
4. **Guard classes** `shell` and `tunnel`:

   | | plain | protected | readonly |
   |---|---|---|---|
   | shell (`mek shell`, IAP+SSH tunnels, `kubectl exec/attach/debug`) | run | confirm y/N | blocked |
   | tunnel (`mek tunnel`, `kubectl port-forward/proxy`) | run | confirm y/N | run |

   A tunnel only moves bytes; what runs over it is governed by the target's own permissions.
   `mek kube` is a read. `kubectl` gets its own classifier (`--dry-run` = read,
   `delete/drain/replace --force/rollout undo` = destructive, unknown verbs = write).
5. **Audit.** Sessions get a `start` entry (after the guard, so blocked sessions have none)
   and an `end` entry with duration and exit code, sharing a session id.
6. **Lifecycle.** `mek tunnel` stays in the foreground and forwards Ctrl-C. `--background`
   (v0.8.0) runs it under a supervisor; liveness is a `flock` held by the supervisor (not a
   pid, which can be reused), and `ls`/`stop` cover every context. Tunnels bind to loopback.
7. **Doctor** reports plugins as required only when the config uses them (kubectl and
   gke-gcloud-auth-plugin when a context has `clusters`).

## Design patterns (GoF)

| Pattern | Where |
|---|---|
| Abstract Factory (extended) | `provider.Cloud` gains capability interfaces (`KubeProvider`, later `Sessioner`/`Tunneler`) and `Plugins` |
| Template Method | `kube.Describe`/`Render`/`Write`: fixed kubeconfig skeleton, cloud steps from `KubeProvider` |
| Strategy | `guard.ClassifyKubectl`; `provider.TunnelMethod`: `ssmPort`, `ssmRemoteHost`, `iapPort`, `iapSSH` (a shell), `cloudSQL` |
| Factory Method | `Tunneler.TunnelMethod(config.Tunnel)`: the kind follows from the fields set; config validation calls it too, so each cloud's rules live in one place |
| Chain of Responsibility | `provider/resolve.go`: configured name → instance id → tag lookup (AWS) / VM-name lookup (GCP) |
| Builder | `provider/argv.go`: `command(...).opt(...).env(...).needs(...)` assembles session commands |
| Decorator (new) | `audit.SessionStart` after the guard: `audit → guard → sessionStart → exec` |
| Command + Builder | `kube.MergeCommands`/`UnmergeCommands` build `kubectl config` argv lists |
| Command / Decorator (extended) | `runner.Invocation.Stdout` lets lookups run through `audit → guard → exec` |
| State, Observer, Facade | tunnel states, supervisor events, `internal/tunnel` API (v0.8.0) |

## Spikes (M0)

- `aws eks get-token` works offline with static (fake) keys — it only presigns an STS URL.
- `gke-gcloud-auth-plugin` honours `CLOUDSDK_CONFIG` and works offline with
  `CLOUDSDK_AUTH_ACCESS_TOKEN`.
- Floci's EKS needs real subnets and the Docker socket (k3s), so kube is tested against a
  fake TLS EKS/GKE + Kubernetes API in the contract layer instead.
- `gcloud compute ssh --ssh-key-file <f>` creates the key at `<f>`, but always tells ssh
  `-o UserKnownHostsFile=$HOME/.ssh/google_compute_known_hosts` — ssh options are
  first-wins, so a later `--ssh-flag` can't override it. mek therefore runs it with
  `HOME=<MEK_HOME>/ssh/<ctx>` (gcloud itself stays on the context's `CLOUDSDK_CONFIG`).
  A real run also adds the key to the project's metadata: confirmed as a shell, not a read.
- session-manager-plugin listens on `localhost:<port>` (or a Unix socket), for both its
  basic and multiplexed port forwarding — loopback only, as tunnels require.

## Testing

- Unit (100%): validation tables, `ClassifyKubectl`, `Decide` matrix, describe parsing,
  golden kubeconfig, merge/unmerge argv, `MekPath`, CLI commands with a fake runner.
- Integration (real binary, stub CLIs): describe/token argv and env, `kube token` unaudited,
  `mek kubectl` guard and `--from-literal` masking, `eval "$(mek use --shell …)"` in a real sh.
- Contract (real CLIs, offline): `mek kube` + kubectl against a fake TLS EKS API with real
  `aws eks get-token`, and a fake GKE API with real gcloud and gke-gcloud-auth-plugin; merge and
  unmerge with real kubectl keeping the user's entries.
- E2E: the installed binary writes a kubeconfig that runs itself.
- Sessions (v0.7.0): SSM/IAP data channels can't be emulated, so `shell`/`tunnel` stop at
  integration (stub CLIs) and contract (request bodies).

## Open questions

1. Huawei CCE: a static client certificate is a long-lived credential — accept it with a
   short duration, or leave Huawei out of `mek kube`?
2. GKE private/DNS-based endpoints.
