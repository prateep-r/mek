# 0001 — `mek tunnel`, `mek shell`, `mek kube`

Status: **proposed** — decisions marked ❓ need the maintainer's call before implementation.

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
- Nothing written to the user's `~/.kube/config`, `~/.ssh`, `~/.aws` or `~/.azure`.
- Protected/readonly contexts behave predictably; every session is in the audit log.

## Non-goals

- Re-implementing tunnels or shells. mek orchestrates the official tools
  (`aws ssm start-session` + session-manager-plugin, `gcloud compute start-iap-tunnel`,
  `az network bastion`, `aws eks get-token`, `gke-gcloud-auth-plugin`, `kubelogin`).
- Discovering every resource type. Targets are named in config or given explicitly.

## User experience

```bash
mek -c prod tunnel db             # named tunnel from config → localhost:15432
mek -c prod tunnel --via i-0abc --to mydb.xyz.rds.amazonaws.com:5432 --local 15432
mek -c prod shell bastion         # named target, or an instance id / Name tag
mek -c prod kube                  # writes ~/.config/mek/kube/prod.yaml, prints how to use it
mek -c prod exec -- k9s           # KUBECONFIG is set for the context automatically
eval "$(mek -c prod env)"         # ...as it is for anything run from the shell
```

Config (new, optional keys per context):

```yaml
contexts:
  prod:
    provider: aws
    # ...existing fields...
    targets:                          # for shell and as tunnel hops
      bastion: {instance: "tag:Name=bastion"}     # or i-0abc123
    tunnels:
      db: {via: bastion, host: mydb.cluster-xyz.ap-southeast-1.rds.amazonaws.com, port: 5432, local_port: 15432}
    clusters:
      main: {name: prod-eks}          # gcp: {name, location}; azure: {name, resource_group}
```

## How it maps to each cloud

| | AWS (phase 1) | GCP (phase 2) | Azure (phase 3) | Huawei Cloud |
|---|---|---|---|---|
| tunnel | `aws ssm start-session --document-name AWS-StartPortForwardingSessionToRemoteHost --parameters host=…,portNumber=…,localPortNumber=…` | `gcloud compute start-iap-tunnel <vm> <port> --local-host-port=localhost:<n>` | `az network bastion tunnel` (Standard SKU) | ❓ no CLI equivalent found yet |
| shell | `aws ssm start-session --target <id>` | `gcloud compute ssh <vm> --tunnel-through-iap` | `az network bastion ssh` / `az ssh vm` | ❓ |
| kube | `aws eks update-kubeconfig --kubeconfig <file> --alias <ctx>` | `gcloud container clusters get-credentials` (with `KUBECONFIG=<file>`) | `az aks get-credentials --file <file>` + `kubelogin convert-kubeconfig -l azurecli` | `hcloud CCE CreateKubernetesClusterCert` (static cert — ❓ acceptable?) |
| needs | session-manager-plugin | gke-gcloud-auth-plugin (kube) | kubelogin (kube) | — |

Target resolution: an instance id is used as is; `tag:Name=…` (AWS) / VM name (GCP, Azure)
is resolved once with a read-only describe call and must match exactly one running instance.

## Design decisions

1. **Kubeconfig credentials go through mek.** The generated kubeconfig's exec plugin calls
   `mek -c <ctx> kube token <cluster>` (an internal command), which runs the cloud's token
   command with the context environment. K9s/FreeLens started from the Dock then work without
   `mek exec`, and a context switch can never send one context's token to another cluster.
   The alternative — embedding `AWS_PROFILE`/`AWS_CONFIG_FILE`/`CLOUDSDK_CONFIG` in the exec
   env — leaks mek's internals into files other tools read and breaks if they move.
   `kube token` is not audited (kubectl calls it constantly); `mek kube` itself is.
2. **One kubeconfig file per context** at `~/.config/mek/kube/<ctx>.yaml`, written with
   `fsutil.WriteFileAtomic`. The context's env gets `KUBECONFIG` pointing at it, so
   `mek exec`, `mek env` and passthrough commands all see it. `~/.kube/config` is never touched.
3. **Guard classes.** `tunnel` and `shell` are a new class, `session`:
   ❓ protected → confirm y/N (shell) and allow (tunnel)? readonly → block shell, allow tunnel?
   The proposal: on protected contexts both ask y/N; on readonly contexts `shell` is blocked and
   `tunnel` is allowed (a tunnel only moves bytes; what runs over it is governed by the
   database's own permissions). `kube` writing a kubeconfig is a read.
4. **Audit.** One entry when a session starts (target, ports) and one when it ends with its
   duration and exit code — sessions can last hours, so start/end is more useful than a single
   line at the end.
5. **Lifecycle.** `mek tunnel` stays in the foreground and forwards Ctrl-C; no daemon, no
   background tunnels to forget (❓ a `--background` flag later, if asked for). Local ports
   default to the remote port + 10000 and fail clearly when taken.
6. **Doctor.** `mek doctor` already lists session-manager-plugin; it will add
   gke-gcloud-auth-plugin and kubelogin, required only when a context uses `clusters`.

## Testing

- Unit: target resolution, command construction per cloud, guard decisions, kubeconfig
  rendering (golden files), `kube token` env.
- Integration (stub CLIs): exact `ssm start-session` / IAP / bastion arguments, Ctrl-C and
  SIGTERM forwarding to the session, audit start/end entries.
- Contract (real CLIs, offline): `aws eks update-kubeconfig --dry-run` against a
  `describe-cluster` served by the emulator; plugin `--version` checks.
- Emulator: Floci serves EKS (k3s) and EC2 describe calls — enough for `mek kube` end to end
  with kubectl; SSM sessions need a websocket data channel Floci does not emulate, so
  `tunnel`/`shell` stop at the integration layer.

## Phases

1. **AWS**: tunnel, shell (SSM), kube (EKS). Ships as one minor release.
2. **GCP**: IAP tunnel/ssh, GKE.
3. **Azure**: Bastion tunnel/ssh, AKS with kubelogin.
4. **Huawei Cloud**: kube via CCE certificates once ❓ below is decided; tunnel/shell if a
   supported path exists.

## Open questions

1. Guard policy for `session` on protected and readonly contexts (decision 3).
2. Phase 1 scope: AWS only, or AWS + GCP together?
3. Named targets/tunnels in config.yaml (proposed), or flags only?
4. Huawei CCE: a static client certificate is a long-lived credential — accept it with a
   short `--duration`, or leave Huawei out of `mek kube`?
5. Background tunnels (`--background` + `mek tunnel ls/stop`) — wanted, or never?
