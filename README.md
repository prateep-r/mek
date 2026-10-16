<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/mek-logo-dark.svg">
    <img src="assets/logo/mek-logo-light.svg" alt="mek" width="300">
  </picture>
</h1>

**mek** (เมฆ — Thai for *cloud*) keeps all your cloud accounts in one place, logs you
in, switches between them and runs the official CLIs with the right credentials —
for AWS and GCP. Think *K9s/Lens, but for cloud accounts*.

```bash
mek ctx add                    # add an account by answering questions (or edit the YAML)
mek login dev                  # SSO once, covers every account behind the same portal
mek use dev                    # switch context
mek aws s3 ls                  # any aws command, in that context
mek -c gcp-dev gcloud compute instances list
mek -c prod kubectl get pods   # EKS/GKE, with no key on disk
mek -c prod tunnel db -b       # localhost:15432 → a private database
mek exec -- terraform plan     # any tool, same credentials
```

- **Every CLI feature, day one** — `mek aws …` / `mek gcloud …` pass everything through to the real CLI.
- **No long-lived keys** — AWS uses IAM Identity Center (SSO) through the AWS CLI's own token cache; GCP uses an isolated gcloud config per context; kubeconfigs fetch a fresh token on every request.
- **Your files stay untouched** — mek writes its own AWS config instead of editing `~/.aws/config`, keeps gcloud off `~/.config/gcloud`, and never writes `~/.kube/config` or `~/.ssh`.
- **Prod guard** — `protected` contexts confirm writes and require typing the context name for destructive commands; `readonly` contexts block them.
- **Audit log** — every command is recorded with secrets masked; shells and tunnels get an entry when they start and one when they end.

## Contents

1. [Install](#1-install)
2. [Getting started](#2-getting-started)
3. [Configuration reference](#3-configuration-reference)
4. [Project config: mek.yaml and mek.local.yaml](#4-project-config-mekyaml-and-meklocalyaml)
5. [Editing config with the wizard](#5-editing-config-with-the-wizard)
6. [Everyday use](#6-everyday-use)
7. [Kubernetes (EKS, GKE)](#7-kubernetes-eks-gke)
8. [Shell on an instance](#8-shell-on-an-instance)
9. [Tunnels](#9-tunnels)
10. [Command reference](#10-command-reference)
11. [How the guard works](#11-how-the-guard-works)
12. [Troubleshooting](#12-troubleshooting)
13. [Limitations](#13-limitations)
14. [Roadmap](#14-roadmap)
15. [Development](#development)

## 1. Install

**macOS (Homebrew)**

```bash
brew install prateep-r/tap/mek
```

**macOS / Linux (script)** — verifies the SHA-256 checksum, installs to `~/.local/bin`, no sudo:

```bash
curl -fsSL https://raw.githubusercontent.com/prateep-r/mek/main/install.sh | sh
# prefer to read it first:
curl -fsSLO https://raw.githubusercontent.com/prateep-r/mek/main/install.sh && less install.sh && sh install.sh
```

**Windows (PowerShell)** — verifies the SHA-256 checksum, installs to
`%LOCALAPPDATA%\Programs\mek` and adds it to your user `PATH`, no admin:

```powershell
irm https://raw.githubusercontent.com/prateep-r/mek/main/install.ps1 | iex
# options, as for install.sh: $env:MEK_VERSION, $env:MEK_INSTALL_DIR, $env:MEK_DOWNLOAD_URL,
# and $env:MEK_NO_MODIFY_PATH = '1' to leave PATH alone
```

**Go**

```bash
go install github.com/prateep-r/mek/cmd/mek@latest
```

**From source**

```bash
git clone https://github.com/prateep-r/mek && cd mek
make install          # builds with version info, copies to ~/.local/bin (make uninstall removes it)
```

Check it works:

```bash
mek version
mek doctor            # config + the CLIs and plugins your contexts need, with install hints
mek help              # every command; mek help <command> for one in full
```

**Shell completion** (contexts, clusters, targets and tunnels complete too):

```bash
# zsh
mek completion zsh > "${fpath[1]}/_mek"
# bash
mek completion bash > ~/.local/share/bash-completion/completions/mek
# fish
mek completion fish > ~/.config/fish/completions/mek.fish
```

**Shell integration** — one line in your shell's startup file lets `mek use -s <context>`
switch just that terminal (otherwise it needs `eval "$(…)"`, since a program can't change its
shell's variables):

```bash
eval "$(mek shell-init zsh)"      # ~/.zshrc
eval "$(mek shell-init bash)"     # ~/.bashrc
mek shell-init fish | source      # ~/.config/fish/config.fish
```

```powershell
mek shell-init powershell | Out-String | Invoke-Expression   # in $PROFILE (Windows PowerShell or pwsh)
```

Without it, `mek env --syntax powershell | Out-String | Invoke-Expression` exports a context
into the session, like `eval "$(mek env)"` does for POSIX shells.

**Update**: `brew upgrade mek` (Homebrew), or `mek self-update` (script / manual installs, and
Windows; `mek self-update --check` only looks). mek tells Homebrew users to use brew.

### Requirements

mek drives the official tools, so install the ones your contexts use (`mek doctor` lists
exactly which, with `brew` commands on macOS and `winget` commands on Windows):

| For | Needs |
|---|---|
| AWS contexts | [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) |
| GCP contexts | [Google Cloud CLI](https://cloud.google.com/sdk/docs/install) |
| `mek kubectl`, kubeconfigs | [kubectl](https://kubernetes.io/docs/tasks/tools/) |
| GKE clusters | [gke-gcloud-auth-plugin](https://cloud.google.com/kubernetes-engine/docs/how-to/cluster-access-for-kubectl#install_plugin) |
| `mek shell` / `mek tunnel` on AWS | [session-manager-plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html) |
| Cloud SQL tunnels | [cloud-sql-proxy](https://cloud.google.com/sql/docs/postgres/connect-auth-proxy#install) |
| GCP shells, GCP tunnels to another host | `ssh` and `ssh-keygen` ([OpenSSH](https://www.openssh.com); built into macOS, `apt install openssh-client` on Debian/Ubuntu; not on Windows, where gcloud brings PuTTY) |

AWS CLI v1 doesn't work: it can't do IAM Identity Center (`sso-session`) logins, and
`mek doctor` says so.

### What the cloud side needs

mek adds no agent, server or open port of its own; shells, tunnels and clusters need what the
official tools need. Ask whoever runs the account for:

| Feature | On the cloud side | For your role / user |
|---|---|---|
| AWS `mek shell`, `mek tunnel` | the SSM Agent running on the instance, an instance profile with `AmazonSSMManagedInstanceCore`, and a way out to the SSM endpoints (internet or VPC endpoints) | `ssm:StartSession` (plus `ec2:DescribeInstances` for `tag:` targets) |
| GCP `mek shell`, tunnels with `via:` | a firewall rule allowing `35.235.240.0/20` (IAP) to port 22, or to the forwarded port for tunnels without `host:` | IAP-secured Tunnel User, and OS Login or permission to add SSH keys (shells and `host:` tunnels) |
| Cloud SQL tunnels | the Cloud SQL Admin API enabled; with `private_ip: true`, a network path from your machine to the instance (VPN, peering) | Cloud SQL Client |
| EKS (`mek kube`, `mek kubectl`) | an access entry (or `aws-auth` mapping) for your role, and an endpoint you can reach | `eks:DescribeCluster` |
| GKE (`mek kube`, `mek kubectl`) | Kubernetes RBAC for your user, and an endpoint you can reach | Kubernetes Engine Cluster Viewer |
| The wizard's lists (`mek ctx add`, `mek ctx tunnel add`, …) | nothing | each optional — without one, you type the value: `ec2:DescribeInstances`, `rds:DescribeDBInstances`, `rds:DescribeDBClusters`, `eks:ListClusters`; `compute.instances.list`, `cloudsql.instances.list`, `container.clusters.list` ([details](#what-the-wizard-looks-up)) |

## 2. Getting started

A ten-minute walk from nothing to working in four accounts: AWS `dev`, `prod` and `audit`
behind one IAM Identity Center portal, and a GCP project.

### Step 1 — create the config file

```console
$ mek init
✓ created /Users/you/.mek/config.yaml
edit it (or add contexts with: mek ctx add), then run: mek ctx ls && mek login <context>
```

The file starts as a commented example. `mek init --force` writes it again. Joining a team
whose repository has a `mek.yaml`? You may need no file of your own: see
[Project config](#4-project-config-mekyaml-and-meklocalyaml).

### Step 2 — describe your accounts

Each *context* is one cloud target. Replace the example with yours:

```yaml
# ~/.mek/config.yaml
contexts:
  dev:
    provider: aws
    sso_start_url: https://acme.awsapps.com/start   # your IAM Identity Center portal
    sso_region: ap-southeast-1                      # the region Identity Center lives in
    account_id: "111122223333"                      # quote it: 12 digits
    role: Developer                                 # permission set name
    region: ap-southeast-1                          # default region for commands

  prod:
    provider: aws
    sso_start_url: https://acme.awsapps.com/start   # same portal → same login as dev
    sso_region: ap-southeast-1
    account_id: "444455556666"
    role: Admin
    region: ap-southeast-1
    protected: true      # asks before writes; destructive commands need the name typed

  audit:
    provider: aws
    sso_start_url: https://acme.awsapps.com/start
    sso_region: ap-southeast-1
    account_id: "777788889999"
    role: ReadOnly
    readonly: true       # blocks writes and destructive commands entirely

  gcp-dev:
    provider: gcp
    project: acme-dev
    region: asia-southeast1
    account: you@acme.com   # optional: preselects the account at login
```

Already have an AWS profile you like? `provider: aws` with `aws_profile: my-profile`
uses it as it is (see [Configuration reference](#3-configuration-reference)).

**Rather answer questions than write YAML?** `mek ctx add` asks for a context — listing your
IAM Identity Center accounts and roles, or your GCP projects — checks it and writes the file
(creating it if need be): see [Editing config with the wizard](#5-editing-config-with-the-wizard).

### Step 3 — check

```console
$ mek ctx ls
  audit                aws 777788889999 / ReadOnly / -               [readonly]
  dev                  aws 111122223333 / Developer / ap-southeast-1
  gcp-dev              gcp acme-dev / asia-southeast1
  prod                 aws 444455556666 / Admin / ap-southeast-1     [protected]

$ mek doctor
```

`mek doctor` checks the config and that `aws` and `gcloud` (and any plugin a context
needs — `session-manager-plugin`, `gke-gcloud-auth-plugin`, `cloud-sql-proxy`, `ssh`) are
installed, with the install command for anything missing. It also flags AWS CLI v1.

### Step 4 — log in

```console
$ mek login dev
→ logging in to dev aws 111122223333 / Developer / ap-southeast-1
  (the browser opens the IAM Identity Center page; approve it)
✓ logged in — identity:
  … aws sts get-caller-identity …

$ mek login gcp-dev            # gcloud auth login, into gcp-dev's own gcloud config
$ mek login gcp-dev --adc      # …plus application-default credentials for SDKs/terraform
```

`dev`, `prod` and `audit` share one portal, so the one login covers all three (until the
SSO session expires — the portal's session length, 8 hours by default).

### Step 5 — choose a context

```console
$ mek use dev
✓ switched to dev aws 111122223333 / Developer / ap-southeast-1

$ mek ctx
dev  aws 111122223333 / Developer / ap-southeast-1
```

| How | Scope |
|---|---|
| `mek use dev` | saved: every terminal that hasn't picked its own |
| `mek use -s prod` | this terminal only (sets `$MEK_CONTEXT`) — with [shell integration](#1-install); without it, `eval "$(mek use -s prod)"` |
| `mek -c audit …` | this one command |

The most specific wins: `-c` over `$MEK_CONTEXT` over the saved context.

### Step 6 — work

```console
$ mek aws s3 ls                                    # dev, the current context
$ mek -c gcp-dev gcloud compute instances list
$ mek -c audit aws ec2 describe-instances          # reads are fine on readonly
$ mek -c audit aws s3 rb s3://old-bucket
● audit [readonly] aws 777788889999 / ReadOnly / -
mek: audit is readonly — blocked destructive command: aws s3 rb s3://old-bucket

$ mek exec -- terraform plan                       # any tool with dev's credentials
$ eval "$(mek env gcp-dev)"                        # or export gcp-dev into this shell
```

That's the core. The rest of this page is reference and recipes.

## 3. Configuration reference

### Where things live

Everything mek keeps is under `~/.mek` (`%USERPROFILE%\.mek` on Windows); `$MEK_HOME`
overrides it:

| Path | What | Who writes it |
|---|---|---|
| `config.yaml` | your contexts, on every project | you (`mek init` creates it) |
| `state.yaml` | the saved current context | `mek use` |
| `aws/config` | AWS profiles `mek-<key>` for SSO contexts (`aws/config-<hash>` inside a project) | mek, on every run — don't edit |
| `gcloud/<key>/` | each GCP context's own gcloud config and login | gcloud |
| `kube/<key>.yaml` | each context's kubeconfig | `mek kube` |
| `ssh/<key>/` | GCP shells' SSH key and known hosts | gcloud |
| `tunnels/` | background tunnels' records, locks and logs | `mek tunnel` |
| `audit.jsonl` | the audit log (rotated at 10 MiB, 3 old files kept) | mek |

`<key>` is the context's name for contexts in `config.yaml`; a project's context adds a short
hash of its account (`dev-1a2b3c4d`), so two projects' `dev` never share a login. AWS SSO
tokens stay in the AWS CLI's own cache (`~/.aws/sso/cache`); mek stores no keys.

**Moving from `~/.config/mek`.** mek before v0.16 kept everything in `~/.config/mek` (or
`$XDG_CONFIG_HOME/mek`). The first run of a newer mek moves it to `~/.mek` in one rename —
logins, keys and the audit log come along — and leaves a link at the old path on macOS and
Linux, so `KUBECONFIG` lines pointing there keep working (on Windows, update them). While
tunnels still run from the old directory it waits; stop them (`mek tunnel stop --all`) and it
moves on the next run. `XDG_CONFIG_HOME` no longer applies.

### Specification

`config.yaml` is YAML — and so are a project's `mek.yaml` and `mek.local.yaml`, with exactly
the same keys. Every key mek reads is in this one table; paths under a context are
relative to `contexts.<name>`; a key that isn't in it is an error. "—" means the key doesn't
apply to that cloud: under `clusters`, `targets` and `tunnels` setting it is an error; other
keys are ignored. Rules
marked *arg* mean no leading `-` and no control characters, because the value becomes a CLI
argument.

| Key | Type | Required | Default | AWS | GCP | Rules | Effect |
|---|---|---|---|---|---|---|---|
| `contexts` | map: *name* → context | no | no contexts | ✓ | ✓ | top-level key | every cloud target, by the name you type (`mek use <name>`, `-c <name>`) |
| `contexts.<name>` | map | — | — | ✓ | ✓ | name: `^[A-Za-z0-9][A-Za-z0-9._-]*$`; can't be empty | one context. The name becomes a directory and an AWS profile name (`mek-<name>`) |
| **Every context** | | | | | | | |
| `provider` | string | **yes** | — | `aws` | `gcp` | one of the two | which cloud and CLI the context drives |
| `region` | string | no | none | ✓ | ✓ | no line breaks | AWS: `AWS_REGION` / `AWS_DEFAULT_REGION`, the profile's region, the default for `sso_region` and EKS clusters. GCP: `CLOUDSDK_COMPUTE_REGION` |
| `protected` | bool | no | `false` | ✓ | ✓ | | writes, shells, tunnels and `mek exec` ask y/N; destructive commands need the context name typed |
| `readonly` | bool | no | `false` | ✓ | ✓ | | blocks writes, destructive commands, shells and `mek exec`; reads and tunnels run (with `protected` too, tunnels ask) |
| **AWS — IAM Identity Center (SSO)** | | | | | | | |
| `sso_start_url` | string (URL) | **yes** (SSO) | — | ✓ | — | no line breaks | the portal. Contexts with the same URL share one `[sso-session]`: one `mek login` covers them all |
| `sso_region` | string | **yes** (SSO), unless `region` is set | `region` | ✓ | — | equal across contexts sharing an `sso_start_url` | the region IAM Identity Center lives in |
| `account_id` | string | **yes** (SSO) | — | ✓ | — | exactly 12 digits; quote it | the account |
| `role` | string | **yes** (SSO) | — | ✓ | — | no line breaks | the permission set |
| **AWS — existing profile** | | | | | | | |
| `aws_profile` | string | **yes** (instead of the SSO keys) | — | ✓ | — | no line breaks | use this profile from your own `~/.aws/config` untouched (SSO, keys, `credential_process`, assume-role…); the SSO keys are then ignored |
| **GCP** | | | | | | | |
| `project` | string | **yes** | — | — | ✓ | no line breaks | the project ID: `CLOUDSDK_CORE_PROJECT`, `GOOGLE_CLOUD_PROJECT` |
| `account` | string (e-mail) | no | gcloud's active account | — | ✓ | no line breaks | `gcloud auth login <account>`, `CLOUDSDK_CORE_ACCOUNT` |
| `use_metadata` | bool | no | `false` | — | ✓ | — | on a GCP VM: let gcloud act as the VM's service account when the context has no login. Off, mek sets `CLOUDSDK_CORE_CHECK_GCE_METADATA=false`, so a context that isn't logged in fails instead of quietly using the machine's identity |
| **Kubernetes clusters** (`mek kube`, `mek kubectl`) | | | | | | | |
| `clusters` | map: *alias* → cluster | no | none | ✓ | ✓ | alias: name rule, not `token` | the kube context is `<name>/<alias>`; `mek kube <alias>` makes it current |
| `clusters.<alias>.name` | string | **yes** | — | EKS name | GKE name | *arg* | the cluster in the cloud (must be `ACTIVE` / `RUNNING`) |
| `clusters.<alias>.region` | string | AWS: yes, unless the context has `region` | the context's `region` | ✓ | — | *arg* | the EKS cluster's region |
| `clusters.<alias>.location` | string | GCP: **yes** | — | — | ✓ | *arg* | the GKE cluster's zone or region |
| `clusters.<alias>.endpoint` | `private` / `dns` | no | the cluster's endpoint | — | ✓ | one of the two | which GKE endpoint the kubeconfig uses: `private` = its internal IP (from inside the VPC or over VPN), `dns` = its DNS-based endpoint (from anywhere IAM allows, if it allows external traffic) — see [private clusters](#private-clusters) |
| `clusters.<alias>.namespace` | string | no | `default` | ✓ | ✓ | *arg* | the kube context's default namespace |
| **Hosts** (`mek shell`, tunnel hops) | | | | | | | |
| `targets` | map: *alias* → target | no | none | ✓ | ✓ | alias: name rule, not `ls` `list` `stop` `logs` | hosts by short name: `mek shell <alias>`, `via: <alias>` |
| `targets.<alias>.instance` | string | **yes** | — | `i-…` / `mi-…` (8–17 hex digits), or `tag:Key=Value` | a VM name: `^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$` | *arg* | the host; a tag must match exactly one running instance |
| `targets.<alias>.zone` | string | no | looked up (the one running VM of that name) | — | ✓ | *arg* | the VM's zone |
| `targets.<alias>.user` | string | no | gcloud's (your local user) | — | ✓ | *arg* | the SSH user (SSM picks the OS user on AWS) |
| **Tunnels** (`mek tunnel`) | | | | | | | |
| `tunnels` | map: *alias* → tunnel | no | none | ✓ | ✓ | alias: name rule, not `ls` `list` `stop` `logs` | named port forwards: `mek tunnel <alias>` |
| `tunnels.<alias>.via` | string | **yes**, except Cloud SQL | — | a target alias, instance id or `tag:Key=Value` | a target alias or VM name | *arg* | the host the tunnel goes through. `via` + `port`: a port on that host (AWS SSM port forwarding; GCP `start-iap-tunnel`) |
| `tunnels.<alias>.host` | string | no | — | ✓ | ✓ | *arg* | another host reached through `via` (an RDS endpoint, an internal IP): AWS SSM to a remote host; GCP `ssh -L` — which counts as a **shell** |
| `tunnels.<alias>.port` | int | **yes** with `via` | — | ✓ | ✓ | 1–65535 | the remote port |
| `tunnels.<alias>.local_port` | int | **yes** for Cloud SQL | `port` + 10000 | ✓ | ✓ | 1–65535; set it when `port` > 55535 | the port on `127.0.0.1`; `mek tunnel --local` overrides it |
| `tunnels.<alias>.cloudsql` | string | no | — | — | ✓ | `PROJECT:REGION:INSTANCE`; no `via`/`host`/`port` with it | a Cloud SQL instance through the Cloud SQL Auth Proxy (needs `mek login --adc`) |
| `tunnels.<alias>.private_ip` | bool | no | `false` | — | ✓ | only with `cloudsql` | reach Cloud SQL over its private IP |

Block style and flow style (`dev: {provider: aws, …}`) are both fine. Everything is checked
when mek loads the file, before any command runs; the first problem is reported with its
context and key, e.g. `context "prod": tunnels.db: port 0 is not a port (1-65535)`.

Keys mek doesn't know are errors, so a typo can't slip by — `protcted: true` stops mek with
`unknown field "protcted"` and the line it is on, instead of leaving `prod` unprotected.

What commands get: AWS SSO contexts become profiles `mek-<name>` in mek's own
`~/.mek/aws/config` (rewritten on every run — never edit it), passed as `AWS_PROFILE`
and `AWS_CONFIG_FILE`. Each GCP context has its own gcloud directory (`CLOUDSDK_CONFIG`), plus
`GOOGLE_APPLICATION_CREDENTIALS` once `mek login --adc` created them. Credentials from your
shell that would override a context (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
`AWS_SESSION_TOKEN`, `GOOGLE_APPLICATION_CREDENTIALS`, …) are removed.

### A complete example

Two contexts using every section:

```yaml
contexts:
  prod:
    provider: aws
    sso_start_url: https://acme.awsapps.com/start
    sso_region: ap-southeast-1
    account_id: "444455556666"
    role: Admin
    region: ap-southeast-1
    protected: true
    clusters:
      main: {name: prod-eks, namespace: app}             # region defaults to the context's
      data: {name: data-eks, region: us-east-1}
    targets:
      bastion: {instance: "tag:Name=bastion"}            # or an instance id: i-0abc1234def567890
    tunnels:
      db:    {via: bastion, host: prod-db.cluster-xyz.ap-southeast-1.rds.amazonaws.com, port: 5432}
      redis: {via: bastion, host: prod-redis.abc.cache.amazonaws.com, port: 6379, local_port: 16379}

  gcp-prod:
    provider: gcp
    project: acme-prod
    region: asia-southeast1
    clusters:
      apps: {name: apps-1, location: asia-southeast1}    # GKE: zone or region (endpoint: private | dns for private clusters)
    targets:
      web: {instance: web-1, zone: asia-southeast1-b, user: ops}   # zone is looked up if omitted
    tunnels:
      app: {via: web, port: 8080}                        # a port on the VM itself
      pg:  {via: web, host: 10.10.0.5, port: 5432}       # another host, through the VM
      sql: {cloudsql: "acme-prod:asia-southeast1:main", local_port: 15432}
```

> Never commit real account IDs or SSO URLs to a public repository. Share team configs from a private repo.

### Environment variables

| Variable | Effect |
|---|---|
| `MEK_HOME` | where mek keeps everything (default `~/.mek`) |
| `MEK_NO_LOCAL` | `1`: ignore project config files, like `--no-local` |
| `MEK_CONTEXT` | this shell's context, over the saved one (set by `mek use -s`) |
| `NO_COLOR` | plain output |

What mek gives the commands it runs: AWS gets `AWS_PROFILE`, `AWS_CONFIG_FILE` (SSO) and
`AWS_REGION`; GCP gets `CLOUDSDK_CONFIG`, `CLOUDSDK_CORE_PROJECT` and friends, plus
`GOOGLE_APPLICATION_CREDENTIALS` once `mek login --adc` created them; contexts with clusters
get `KUBECONFIG`. Credentials from your shell that would override the context
(`AWS_ACCESS_KEY_ID`, `GOOGLE_APPLICATION_CREDENTIALS`, …) are removed.

## 4. Project config: mek.yaml and mek.local.yaml

A repository can carry its own contexts, so a team shares them and nobody has to ask which
account or role a project uses. Three files, the same shape (a `contexts:` map, keys as in
the [specification](#specification)):

| File | Whose | In git | Create it |
|---|---|---|---|
| `./mek.local.yaml` | yours, for this project | **no** — gitignored | `mek init --local` |
| `./mek.yaml` | the project's, for the team | yes — commit it | `mek init --project` |
| `~/.mek/config.yaml` | yours, for every project | — | `mek init` |

Inside a project they are read top first. **A context comes whole from the first file that
has it** — fields are never mixed across files. Outside the project, its contexts don't exist.

**protected and readonly can't be switched off from above.** If a lower file sets them on a
context of the same name, it stays guarded; a file above can always add them:

```yaml
# ~/.mek/config.yaml — yours
contexts:
  prod: {provider: aws, aws_profile: prod, protected: true}
  dev:  {provider: aws, aws_profile: dev}

# ./mek.yaml — in the repository
contexts:
  prod: {provider: aws, aws_profile: prod}                    # no protected…
  dev:  {provider: aws, aws_profile: dev, protected: true}    # …and an extra guard
```

In that project `prod` is the repository's — and still protected, because you protect yours —
and `dev` is protected too. A context whose name exists in no lower file is exactly what its
file says. ([What protected and readonly do](#11-how-the-guard-works).)

### The project: where mek looks

The project is the nearest directory, from where you are up, that holds `mek.yaml` or
`mek.local.yaml` (either is enough). The search stops at a repository's root (a `.git`) and
never looks at your home directory or above it.

| Flag | Does |
|---|---|
| `--project DIR` | use `DIR`'s files, from anywhere — mek's kubeconfigs do this, since kubectl runs `mek kube token` in any directory |
| `--no-local` (or `MEK_NO_LOCAL=1`) | ignore the project's files: only `~/.mek/config.yaml` |

### For the team: set up mek.yaml

```console
$ cd ~/work/orders
$ mek init --project
✓ created ./mek.yaml
add the project's contexts (mek ctx add helps), then commit it; teammates see them with: mek ctx ls
```

```yaml
# mek.yaml — committed
contexts:
  orders-dev:
    provider: aws
    sso_start_url: https://acme.awsapps.com/start    # IAM Identity Center: works for everyone
    sso_region: ap-southeast-1
    account_id: "111122223333"
    role: Developer
    region: ap-southeast-1
    tunnels:
      db: {via: "tag:Name=orders-bastion", host: orders-db.internal, port: 5432}
    clusters:
      orders: {name: orders-eks}
  orders-prod:
    provider: aws
    sso_start_url: https://acme.awsapps.com/start
    sso_region: ap-southeast-1
    account_id: "444455556666"
    role: Developer
    region: ap-southeast-1
    protected: true
```

Use IAM Identity Center fields (`sso_start_url`, `account_id`, `role`) in `mek.yaml`;
`aws_profile` names a profile in one person's `~/.aws/config`, so contexts with it belong in
`mek.local.yaml` or `~/.mek/config.yaml`. Review changes to `mek.yaml` like code: they decide
which account a teammate's commands reach.

### For a teammate: start using it

```console
$ git clone …/orders && cd orders
$ mek ctx ls
  orders-dev    aws 111122223333 / Developer / ap-southeast-1  (./mek.yaml)
  orders-prod   aws 444455556666 / Developer / ap-southeast-1  [protected] (./mek.yaml)
$ mek login orders-dev
$ mek -c orders-dev tunnel db -b
● orders-dev (./mek.yaml) aws 111122223333 / Developer / ap-southeast-1
✓ tunnel db is running in the background: localhost:15432 → orders-db.internal:5432 via …
$ mek -c orders-dev kube && mek -c orders-dev kubectl get pods
```

No `~/.mek/config.yaml` is needed for that. Every command on a project's context names the
file it came from, as above.

### Yours only: mek.local.yaml

```console
$ mek init --local
✓ created ./mek.local.yaml
✓ added mek.local.yaml to ./.gitignore
```

`mek init --local` adds the file to the repository's `.gitignore`, and `mek doctor` flags it
if it is committed anyway. Things it is for:

1. **The team's context with your role** — `mek ctx edit orders-dev`, then *your own copy* in
   `mek.local.yaml`, does it for you ([how](#change-a-context)). By hand: copy the whole
   context and change what is yours (a context comes whole from one file, so copying only
   `role:` would drop the rest):

   ```yaml
   contexts:
     orders-dev:
       provider: aws
       sso_start_url: https://acme.awsapps.com/start
       sso_region: ap-southeast-1
       account_id: "111122223333"
       role: Admin                         # yours
       region: ap-southeast-1
       tunnels:
         db: {via: "tag:Name=orders-bastion", host: orders-db.internal, port: 5432}
   ```

2. **A context of your own for this project**, from your `~/.aws/config`:

   ```yaml
   contexts:
     orders-sandbox: {provider: aws, aws_profile: my-orders-sandbox}
   ```

3. **A tunnel or target only you use** — again in a full copy of the context:
   `tunnels: {db: …, metrics: {via: …, host: metrics.internal, port: 9090}}`.

4. **More caution on your machine** — `protected: true` on a copy of the team's context.

### Seeing where a context comes from

```console
$ mek ctx ls                      # the file after each project context
$ mek -c orders-dev ctx           # orders-dev (./mek.local.yaml)  aws 111122223333 / Admin
$ mek doctor                      # every file read, highest first, and what each replaces
Config
  ✓ ./mek.local.yaml — 1 contexts, replacing orders-dev from below
  ✓ ./mek.yaml — 2 contexts
  ✓ ~/.mek/config.yaml — 4 contexts
  current: dev
```

When the team's `mek.yaml` replaces a context of your own, mek warns every time it is used:
`⚠ prod from ./mek.yaml replaces your prod in ~/.mek/config.yaml`. (A context of yours
replaced by your own `mek.local.yaml` isn't warned about.) The audit log records the file
(`config`) for every command on a project's context.

### Questions

- **Why can't I put just `tunnels:` in mek.local.yaml?** A context comes whole from one file;
  copy the context, then add to it.
- **I left the project and the context is gone.** A project's contexts exist inside it only.
  A context chosen there with `mek use` makes mek say so elsewhere; for project work,
  `mek use -s orders-dev` (this shell only) suits better.
- **Why does prod still ask, when mek.yaml doesn't say protected?** Your own `prod` is
  protected, and a project can't switch that off.
- **Where are a project context's logins?** In `~/.mek` like any other, filed by name and
  account (`gcloud/orders-dev-1a2b3c4d`, profile `mek-orders-dev-1a2b3c4d`): two projects'
  `dev` never share one, and the same `dev` in two clones does.
- **Can a project's kubeconfig work from K9s started anywhere?** Yes: it runs
  `mek --project <the repository> --context … kube token`.

## 5. Editing config with the wizard

Rather not write YAML? `mek ctx add` asks a question at a time, offers what it can find —
your IAM Identity Center accounts and roles, your GCP projects, instances, databases,
clusters — checks the result, and writes it into the right file without touching the rest.

| Command | Does |
|---|---|
| `mek ctx add [name]` | add a context |
| `mek ctx edit [name]` | change one, today's answers filled in (no name: pick it from a list) |
| `mek ctx rm <name> [--yes]` | remove one (asks first; `--yes` for scripts) |
| `mek ctx target add\|edit\|rm [name]` | a context's hosts for `mek shell` |
| `mek ctx cluster add\|edit\|rm [name]` | its Kubernetes clusters for `mek kube` |
| `mek ctx tunnel add\|edit\|rm [name]` | its port forwards for `mek tunnel` |

`mek context` is the same command as `mek ctx`: `mek context add`, `mek context ls`. For a
target, cluster or tunnel, `-c <context>` picks the context; without it, a list does.
`mek help wizard` is this section in the terminal, with the commands each lookup runs.

### The keys

| Key | Does |
|---|---|
| `↑` `↓` | choose |
| type | filter a long list (`esc` clears the filter) |
| *type it yourself…* | the last row of every list: a value that isn't listed |
| `enter` | OK |
| `esc` | back a step — the answers so far are kept |
| `ctrl-c` | cancel: nothing is written |

Answers stay on the screen above the question (`✓ Account: acme-prod`). The last step shows
what will be written, and where: **Save**, **Back** or **Cancel** (`←` `→`).

### Add an AWS context (IAM Identity Center)

```console
$ mek ctx add
Add a context
✓ Name: acme-prod
✓ Cloud: AWS
✓ Sign in with: IAM Identity Center (SSO)
✓ Access portal: https://acme.awsapps.com/start
? Account
  the accounts your login can reach
  ▸ acme-audit  777788889999
    acme-dev  111122223333
    acme-prod  444455556666
    type it yourself…
  enter ok · esc back · ctrl-c cancel
```

1. **Name** — what you'll type after `mek use` and `-c`; a name that's taken says where
   (`acme-dev is already in ~/.mek/config.yaml — change it with: mek ctx edit acme-dev`).
2. **Cloud** — AWS or GCP.
3. **Sign in with** — IAM Identity Center, or a profile from `~/.aws/config` (next example).
4. **Access portal** — portals your other contexts use are listed; for a new one, *type it
   yourself…* and give its URL. A listed portal's region is known; a new one's is asked
   (**Portal region**).
5. **Log in to the portal** — only when you aren't logged in yet:

   ```console
   ? Log in to the portal
     to list the portal's accounts and roles — or skip, and type them
     runs: aws sso login --sso-session mek-acme  (a browser opens)
     [Run it]  Skip
   ```

   *Run it* puts the form away while the browser login runs, then brings it back. *Skip*
   goes on without it: you type the account id and role.
6. **Account** — the accounts your login can reach, by name (above).
7. **Role** — your roles (permission sets) in that account:

   ```console
   ? Role
     your roles (permission sets) in that account
     ▸ Admin
       Developer
       ReadOnly
       type it yourself…
   ```

8. **Region** — where commands go; known regions are listed (type to filter: `southeast`),
   or *none* for the AWS CLI's default.
9. **Guard** — *normal*, *protected* (changes ask first; deletes need the context's name
   typed) or *readonly* (changes are blocked). See [How the guard works](#11-how-the-guard-works).
10. **Save?**

```console
? Save?
    in ~/.mek/config.yaml:
    acme-prod:
      provider: aws
      protected: true
      region: ap-southeast-1
      sso_start_url: https://acme.awsapps.com/start
      sso_region: ap-southeast-1
      account_id: "444455556666"
      role: Developer
  [Save]  Back   Cancel
```

```console
✓ added acme-prod to ~/.mek/config.yaml
  next: mek login acme-prod (or mek use acme-prod)
$ mek -c acme-prod aws sts get-caller-identity
```

`mek ctx add acme-prod` fills the name in. With no config yet, the first `mek ctx add`
creates `~/.mek/config.yaml`.

### Add an AWS context (a profile you already have)

```console
$ mek ctx add sandbox
Add a context
✓ Name: sandbox
✓ Cloud: AWS
✓ Sign in with: a profile from ~/.aws/config
? Profile
  the profiles in ~/.aws/config, used as they are
  ▸ default
    sandbox
    personal
    type it yourself…
```

The profiles are read from `~/.aws/config` (or `$AWS_CONFIG_FILE`) — names only. Then the
region and the guard, and:

```yaml
sandbox:
  provider: aws
  region: us-east-1
  aws_profile: personal
```

### Add a GCP context

```console
$ mek ctx add gcp-data
Add a context
✓ Name: gcp-data
✓ Cloud: GCP
✓ Google account: you@acme.com
? Project
  the projects your account can see
  ▸ acme-data  Acme Data
    acme-dev  Acme Dev
    acme-prod  Acme Prod
    type it yourself…
```

- **Google account** — accounts your other contexts use are listed, and that context's login
  is used to list projects. *none* leaves it to `mek login` to ask.
- Not logged in as that account (or *none*)? **Log in to Google** runs `gcloud auth login`
  with the form put away. That login becomes the new context's when you save: no second
  `mek login`.
- **Project** — the projects you can see, by id with their name.
- **Region** — optional (*none*: gcloud's default).

```yaml
gcp-data:
  provider: gcp
  region: asia-southeast1
  project: acme-data
  account: you@acme.com
```

### Add a context inside a project

Inside a project there are three files to choose from. One that has the name already isn't
offered, and the hint says what a choice replaces:

```console
$ cd ~/work/orders && mek ctx add orders-sandbox
Add a context
✓ Name: orders-sandbox
? Save it in
  ▸ ./mek.local.yaml  yours, this project only (not committed)
    ./mek.yaml  the team's — commit it
    ~/.mek/config.yaml  yours, on every project
```

- `./mek.local.yaml` — only you, only here. A new one is added to the repository's `.gitignore`.
- `./mek.yaml` — the whole team gets it once you commit it. Use IAM Identity Center here, not
  a profile: a profile is in one person's `~/.aws/config`.
- `~/.mek/config.yaml` — yours, everywhere.

A new file starts as `mek init` would write it (with its comments), then gets the context.

### Change a context

`mek ctx edit` asks the same questions with today's answers filled in — press `enter` to keep
one. Without a name it lists the contexts to pick from. Clusters, targets and tunnels are kept
as they are; renaming a context renames it in place (and the current context follows).

```console
$ mek ctx edit acme-prod        # e.g. another role, or protected on
$ mek ctx edit                  # pick one
```

**A team context:** for a context from the project's `mek.yaml`, the first question is where
the change goes:

```console
$ mek ctx edit orders-dev
Edit orders-dev
✓ Name: orders-dev
? Save it in
    ./mek.yaml  the team's — commit the change
  ▸ ./mek.local.yaml  your own copy — the team's stays as it is
```

*Your own copy* is the usual case — the team's account with your role:

```console
✓ Role: Admin
? Save?
    in ./mek.local.yaml (a new file):
    orders-dev:
      provider: aws
      region: ap-southeast-1
      sso_start_url: https://acme.awsapps.com/start
      sso_region: ap-southeast-1
      account_id: "111122223333"
      role: Admin
      targets:
        bastion:
          instance: tag:Name=orders-bastion
  [Save]  Back   Cancel

✓ added mek.local.yaml to ./.gitignore
✓ copied orders-dev to ./mek.local.yaml — yours is used in this project; the team's stays as it is
```

The copy is whole (a context comes whole from one file), so its targets and tunnels came too.
Your guards stay on whatever the copy says ([Project config](#4-project-config-mekyaml-and-meklocalyaml)).

### Remove a context

```console
$ mek ctx rm old-sandbox
Remove context old-sandbox from ~/.mek/config.yaml? [y/N]: y
✓ removed old-sandbox from ~/.mek/config.yaml

$ mek ctx rm orders-dev          # your copy in mek.local.yaml…
✓ removed orders-dev from ./mek.local.yaml
  orders-dev from ./mek.yaml is used again

$ mek ctx rm staging --yes       # in a script: no question
```

Removing the current context says so (`it was the current context: mek use <context>`).

### Add a tunnel

```console
$ mek -c orders-dev ctx tunnel add
Add a tunnel to orders-dev
? Forward to
  ▸ a host, through an instance  e.g. a database: RDS, a private IP, Redis
    a port on an instance  a service on the instance itself
```

**A database through a bastion** — the instance is one of the context's targets or a running
instance (by its Name tag), and RDS and Aurora endpoints are listed with their port:

```console
✓ Forward to: a host, through an instance
? Through
  one of the context's targets, or a running instance
  ▸ bastion  target · tag:Name=orders-bastion
    i-0ddd3333eeee4444f  10.0.0.6
    web 1  i-0aaa1111bbbb2222c 10.0.0.5
    type it yourself…

✓ Through: bastion
? Host
  RDS and Aurora endpoints — or type a host
  ▸ aurora  aurora-mysql:3306
    orders  postgres:5432
    type it yourself…
```

Then **Port** (the database's, filled in), **Local port** (the port + 10000, filled in; one
something here already listens on isn't taken) and **Name** (the database's, filled in):

```console
? Save?
    in ./mek.yaml, context orders-dev:
    tunnels:
      orders:
        via: bastion
        host: orders.x.rds.amazonaws.com
        port: 5432
  [Save]  Back   Cancel

✓ added tunnel orders to orders-dev in ./mek.yaml
  next: mek -c orders-dev tunnel orders
```

**A port on an instance** — *Through* the instance, then the port (say 8080): the instance's
own service on `localhost:18080`.

**Cloud SQL** (GCP) — the instances are listed by connection name; then the public or the
private IP, and a local port (15432 for PostgreSQL, 13306 for MySQL, 11433 for SQL Server):

```yaml
tunnels:
  orders:
    local_port: 15432
    cloudsql: acme-dev:asia-southeast1:orders
    private_ip: true
```

On GCP, *a host, through an instance* lists the running VMs; type the host as the VM reaches it.

### Add a target

```console
$ mek -c orders-dev ctx target add
Add a target to orders-dev
✓ Instance: web 1
? Refer to it by
  ▸ tag:Name=web 1  its Name tag: still right when the instance is replaced
    i-0aaa1111bbbb2222c  its id: this instance only
```

An instance without a Name tag goes in by its id. On GCP the VMs are listed with their zone,
which is filled in, then the SSH user (empty: gcloud's):

```yaml
targets:
  vm-1:
    instance: vm-1
    zone: asia-southeast1-a
    user: me
```

Then: `mek -c orders-dev shell web-1`.

### Add a cluster

```console
$ mek -c orders-dev ctx cluster add
Add a cluster to orders-dev
? EKS cluster
  ▸ data-eks
    main-eks
    type it yourself…
```

- **EKS** — the clusters in the context's region; a context without a region is asked for one
  first.
- **GKE** — the clusters in every location; the location is filled in, then how to reach it:

  ```console
  ? Reach it at
      its endpoint  the cluster's own (public, if it has one)
    ▸ private  its internal IP — from inside the VPC
      dns  its DNS-based endpoint — from anywhere IAM allows
  ```

- **Namespace** — kubectl's default for it (empty: `default`).
- **Name** — the cluster's, filled in.

Then: `mek -c orders-dev kube main-eks` ([Kubernetes](#7-kubernetes-eks-gke)).

### Change or remove a tunnel, target or cluster

```console
$ mek -c orders-dev ctx tunnel edit orders     # e.g. another local port
$ mek ctx tunnel edit                          # pick the context, then the tunnel
$ mek -c orders-dev ctx target edit web-1      # e.g. by its id instead of its tag
$ mek -c orders-dev ctx cluster edit main-eks  # e.g. another namespace
$ mek -c orders-dev ctx tunnel rm orders       # asks first
$ mek -c orders-dev ctx target rm web-1        # asks first
$ mek -c orders-dev ctx cluster rm old --yes   # in a script
```

The pick lists say what each is (`orders  localhost:15432 → orders.x.rds.amazonaws.com:5432 via bastion`).
A target a tunnel still uses can't be removed: the check before saving says why.

### In mek ui

| Tab | Key | Runs |
|---|---|---|
| 1 Contexts | `a` | `mek ctx add` |
| | `e` | `mek ctx edit <the chosen context>` |
| | `d` | `mek ctx rm <the chosen context>` (asks first, in the UI) |
| 2 Tunnels | `a` | `mek -c <the chosen context> ctx tunnel add` |

The wizard takes the terminal and the UI comes back when it ends, showing the change.

### What the wizard looks up

Every list comes from the cloud through mek, like any command: read-only, through the guard,
in the [audit log](#the-audit-log), under the context being made. Each needs a permission —
without it, the step says why and you type the value:

| Cloud | Lists | Needs | Runs |
|---|---|---|---|
| AWS | accounts | your IAM Identity Center login (no IAM permission) | `aws sso list-accounts` |
| AWS | roles in an account | your IAM Identity Center login | `aws sso list-account-roles` |
| AWS | instances (targets, tunnels) | `ec2:DescribeInstances` | `aws ec2 describe-instances` (running) |
| AWS | databases (tunnels) | `rds:DescribeDBInstances` | `aws rds describe-db-instances` |
| AWS | Aurora clusters (tunnels) | `rds:DescribeDBClusters` | `aws rds describe-db-clusters` |
| AWS | EKS clusters | `eks:ListClusters` | `aws eks list-clusters` |
| GCP | projects | `resourcemanager.projects.get` on each (any role on it) | `gcloud projects list` |
| GCP | VMs (targets, tunnels) | `compute.instances.list` | `gcloud compute instances list` (running) |
| GCP | Cloud SQL instances (tunnels) | `cloudsql.instances.list` | `gcloud sql instances list` |
| GCP | GKE clusters | `container.clusters.list` | `gcloud container clusters list` |

`mek help wizard` prints the full commands.

**The portal token.** Listing accounts and roles takes your IAM Identity Center access token.
mek reads it from the AWS CLI's own cache (`~/.aws/sso/cache`, where `aws sso login` puts it),
writes it to a file only you can read in `~/.mek` for `--cli-input-json`, and removes the file
as soon as the command ends. It is never in argv (which `ps` shows to every user), never in the
audit log (`cli-input-json` is masked) and never kept by mek.

**When a list can't be had** — not logged in, no permission, no CLI — the step says why, with
the CLI's own message, and takes a typed value:

```console
? Account
  couldn't list them: aws sso list-accounts: exit status 255: An error occurred (UnauthorizedException) … — type the value
  ▏
```

### How it saves

- **Only what you changed is rewritten.** The context (or the one context a target, cluster or
  tunnel belongs to) is written in place; the rest of the file — comments, order, other
  contexts, blank lines — stays as it was. Comments *inside* that context aren't kept; the
  ones above it are.
- **Nothing invalid is saved.** Before writing, the whole config is checked as it would be —
  every file, every cloud's rules (an account id of 12 digits, one region per portal, a
  tunnel's target that exists…). If it wouldn't load, the last step says why and only *Back*
  and *Cancel* are offered.
- **Atomically**, with the file's permissions: `0600` for `~/.mek/config.yaml`, `0644` for a
  project's files.
- A new `mek.local.yaml` is added to `.gitignore`.

### In scripts and CI

`add` and `edit` need a terminal; without one they say so. In CI, write the file (see the
[specification](#specification)), or remove with `--yes`:

```bash
mek ctx rm staging --yes
mek -c dev ctx tunnel rm db --yes     # a target, cluster or tunnel: -c too
```

## 6. Everyday use

### The terminal UI

`mek ui` puts contexts, tunnels, shells, clusters and the audit log on one screen:

```console
$ mek ui                 # or: mek -c prod ui, to start on prod
mek   1 Contexts   2 Tunnels   3 Access   4 Audit
     CONTEXT  ACCOUNT
▸ *  dev      aws 111122223333 / Developer / ap-southeast-1
     gcp-dev  gcp acme-dev / asia-southeast1                  readonly
     prod     aws 444455556666 / Admin / ap-southeast-1        protected

↑↓ choose · enter use (saved current) · l log in · a add · e edit · d remove · 1-4/tab switch · ? keys · q quit
```

| Tab | Shows | Keys |
|---|---|---|
| 1 Contexts | every context; `*` is the saved current one | `enter` make it current · `l` log in · `a` add · `e` edit · `d` remove ([the wizard](#in-mek-ui)) |
| 2 Tunnels | every tunnel (state, `↻` reconnects, port, age), and named tunnels that aren't running | `enter` start a named one in the background, or read a running one's log · `x` stop · `l` log (follows new lines) · `a` add a tunnel to the chosen context |
| 3 Access | the chosen context's targets and clusters | `enter` a shell on a target, or write a cluster's kubeconfig · `k` K9s on a cluster |
| 4 Audit | the last 500 entries, newest first | `/` search · `b` only blocked / declined · `c` only the chosen context · `esc` clear |

The context chosen on tab 1 is the one tabs 3 and 4 use. Everything the UI does runs mek
itself — `mek use`, `mek -c dev tunnel db -b`, `mek -c dev shell bastion` — so the guard and
the audit log work exactly as when typed: a protected context asks first (in the UI, or in the
terminal for shells), a readonly one still blocks shells. Shells, logins and K9s take over the
terminal and return to the UI when they end; when one fails, it waits for Enter so the error
can be read. Tunnels and the audit log refresh every second.

### Several accounts at once

Runs on different contexts never mix credentials, so use as many at once as you like:

```bash
# one terminal per account
mek use -s prod      # terminal 1
mek use -s dev       # terminal 2

# one terminal, any account per command
mek -c prod aws s3 ls
mek -c dev  aws s3 ls

# at the same time, in a script
mek -c prod aws s3 ls > prod.txt &
mek -c dev  aws s3 ls > dev.txt  &
wait

# tunnels into several accounts (different local ports)
mek -c prod tunnel db -b
mek -c dev  tunnel db -b --local 25432
mek tunnel ls
```

Kubernetes clusters from several accounts in one kubectl/K9s:
[section 5](#7-kubernetes-eks-gke).

### Protected and readonly contexts

```console
$ mek -c prod aws ecs update-service --cluster c --service s --desired-count 3
⚠ write command on prod:
  aws ecs update-service --cluster c --service s --desired-count 3
Continue? [y/N]: y

$ mek -c prod aws ec2 terminate-instances --instance-ids i-0abc
⚠ DESTRUCTIVE command on prod:
  aws ec2 terminate-instances --instance-ids i-0abc
Type prod to continue: prod
```

In scripts and CI there is no terminal to ask, so mek refuses unless you pass the answer:
`--yes` for writes, `--confirm prod` for destructive commands (it must match the context).

```bash
mek -c prod --yes aws ecs update-service …
mek -c prod --confirm prod aws ec2 terminate-instances --instance-ids i-0abc
```

`mek exec` can't tell what an arbitrary command does, so on a protected context it always
asks, and a readonly context refuses it. See [section 10](#11-how-the-guard-works) (or
`mek help guard`) for how commands are classified, and what the guard doesn't cover.

### The audit log

One JSON line per command — including ones the guard blocked — in `~/.mek/audit.jsonl`:

```json
{"time":"2026-10-10T13:29:30+07:00","user":"you","context":"prod","provider":"aws","command":["aws","ec2","terminate-instances","--instance-ids","i-1"],"class":"destructive","decision":"confirmed","exit_code":0,"duration_ms":8}
```

Secret-looking flag values (`--password`, `--secret-string`, `--from-literal`, `--token`,
…) are written as `****`. Shells and tunnels add `"event":"start"` / `"end"` entries that
share a `"session"` id.

```bash
tail -f ~/.mek/audit.jsonl | jq -c '{time, context, decision, command: (.command | join(" "))}'
jq -c 'select(.context == "prod" and .class != "read")' ~/.mek/audit.jsonl   # every change on prod
jq -c 'select(.decision == "blocked")' ~/.mek/audit.jsonl                    # what the guard stopped
```

### Using the native CLIs alongside mek

mek doesn't replace `aws` or `gcloud`; it runs them with the context's settings.

- Run `aws` / `gcloud` yourself, without mek, and nothing changes: mek never edits
  `~/.aws/config` or `~/.config/gcloud`.
- **AWS SSO contexts**: mek rewrites `~/.mek/aws/config` on every run, so
  `mek aws configure set …` doesn't stick there — change the account, role or region in
  `config.yaml`. **`aws_profile` contexts** use your own `~/.aws/config`; configure them with
  `aws configure` as usual.
- **GCP contexts** each have their own gcloud config, which keeps what you set:
  `mek -c gcp-dev gcloud config set compute/zone asia-southeast1-b`. The project, account and
  region come from `config.yaml` and win over gcloud's own settings.
- To type plain `aws …` / `gcloud …` / `kubectl …` in a context, export it into the shell:
  `mek use -s prod` or `eval "$(mek env prod)"`. Commands typed that way skip
  mek's guard and audit log.

### Terraform, SDKs and other tools

```bash
mek -c dev exec -- terraform plan           # AWS provider picks up AWS_PROFILE + AWS_CONFIG_FILE
mek login gcp-dev --adc                     # once: application-default credentials for gcp-dev
mek -c gcp-dev exec -- terraform apply      # google provider picks up the ADC and the project
mek -c dev exec -- ./deploy.sh              # scripts, SDK programs, anything
mek -c dev exec -- k9s                      # K9s with dev's clusters
```

On a GCP VM, client libraries and Terraform fall back to the VM's service account when
there are no ADC; mek turns that fallback off only for gcloud (see `use_metadata`).

### Logging in without a browser (CI)

Flags after `--` go to the CLI's own login, which still lands in the context's isolated
config:

```bash
mek login dev    -- --no-browser                                  # aws sso login, device code only
mek login ci-gcp -- --cred-file="$GOOGLE_APPLICATION_CREDENTIALS"  # gcloud auth login with a key file
```

mek removes `GOOGLE_APPLICATION_CREDENTIALS` and similar variables from the commands it runs
so they can't override a context; passing the file to `--cred-file` as above logs the
context in with it instead.

### The context in your prompt

```bash
# zsh (~/.zshrc)
setopt PROMPT_SUBST
PROMPT='%F{cyan}$(mek ctx --short 2>/dev/null)%f %~ %# '
# bash (~/.bashrc)
PS1='\[\e[36m\]$(mek ctx --short 2>/dev/null)\[\e[0m\] \w \$ '
```

```powershell
# PowerShell ($PROFILE)
function prompt { "($(mek ctx --short 2>$null)) PS $($PWD.Path)> " }
```

`mek ctx --short` reads only the config, so it is fast enough to run on every prompt.

## 7. Kubernetes (EKS, GKE)

List the clusters under the context (see [the complete example](#a-complete-example)), then write the
context's kubeconfig:

```console
$ mek -c prod kube
/Users/you/.mek/kube/prod.yaml
✓ kubeconfig for prod: data, main
```

Use it in whichever way suits you:

```bash
mek -c prod kubectl get pods                 # kubectl through the guard and audit log
mek -c prod kubectl -n app logs deploy/api

mek use -s prod                              # this shell: plain kubectl / k9s see prod
kubectl get pods
k9s

mek -c prod exec -- k9s                      # just for one command
KUBECONFIG=~/.mek/kube/prod.yaml k9s  # or point any tool at the file
```

| Command | What you get |
|---|---|
| `mek -c prod kube` | every cluster under `clusters:`; the first is current |
| `mek -c prod kube main` | …with `main` as the current one |
| `mek -c prod kube --name other-eks [--region us-east-1]` | a cluster that isn't in the config (GCP: `--location`, `--endpoint`) |

Kube contexts are named `<context>/<cluster>` (`prod/main`), so they never clash across
accounts. To see clusters from several accounts — and your own `~/.kube/config` — in one
kubectl or K9s, list the files:

```bash
export KUBECONFIG=~/.kube/config:~/.mek/kube/prod.yaml:~/.mek/kube/gcp-prod.yaml
kubectl --context prod/main get nodes
kubectl --context gcp-prod/apps get nodes
```

How it stays keyless: the kubeconfig holds no credentials. Each request runs
`mek --context prod kube token …`, which gets a short-lived token from `aws eks get-token` or
`gke-gcloud-auth-plugin` with that context's login — so the file is safe to keep, works in
K9s or FreeLens started from the Dock, and can never use another context's credentials.
`~/.kube/config` is never written.

For the guard, `kubectl get/describe/logs` are reads, `apply/scale/…` writes,
`delete/drain` destructive, `exec/attach/debug` a **shell** and `port-forward/proxy` a
**tunnel**; `--dry-run` is a read.

### Private clusters

**EKS** has one API hostname: with a private endpoint it resolves to the private address
inside the VPC (or over VPN / Direct Connect), so nothing changes in mek — be on that network.

**GKE** has separate endpoints; pick one per cluster with `endpoint:`, as
`gcloud container clusters get-credentials --internal-ip` / `--dns-endpoint` would:

```yaml
clusters:
  apps: {name: apps-1, location: asia-southeast1}                  # the cluster's default endpoint
  core: {name: core-1, location: asia-southeast1, endpoint: private}  # internal IP: from the VPC or over VPN
  edge: {name: edge-1, location: asia-southeast1, endpoint: dns}      # DNS-based: no VPN, IAM decides
```

The DNS-based endpoint (`gke-….gke.goog`) needs "allow external traffic" turned on for the
cluster; its certificate is publicly trusted, so the kubeconfig carries no CA for it. A
cluster with its IP endpoints turned off uses it automatically.

### Example: reach an nginx pod in EKS

Say `prod` (a protected AWS context) runs nginx in an EKS cluster, namespace `web`, with a
`nginx` Deployment and Service and no public load balancer. Give the context the cluster:

```yaml
contexts:
  prod:
    provider: aws
    # …sso_start_url, account_id, role, region…
    protected: true
    clusters:
      main: {name: prod-eks, namespace: web}   # web becomes the default namespace
```

```bash
mek login prod          # if the SSO session expired
mek -c prod kube        # writes ~/.mek/kube/prod.yaml (again after changing clusters:)
```

**1. Find the pods** — reads, so no question even on a protected context:

```console
$ mek -c prod kubectl get pods -l app=nginx
NAME                     READY   STATUS    RESTARTS   AGE
nginx-7c5ddbdf54-abcde   1/1     Running   0          3d
nginx-7c5ddbdf54-fghij   1/1     Running   0          3d

$ mek -c prod kubectl describe pod nginx-7c5ddbdf54-abcde
$ mek -c prod kubectl get svc nginx
```

**2. Read its logs:**

```bash
mek -c prod kubectl logs deploy/nginx --tail 50 -f        # one pod of the Deployment, following
mek -c prod kubectl logs -l app=nginx --prefix --tail 20  # every pod, labelled
```

**3. Open a shell inside the pod** — `exec` counts as a shell, so a protected context asks:

```console
$ mek -c prod kubectl exec -it deploy/nginx -- sh
● prod [protected] aws 444455556666 / Admin / ap-southeast-1
⚠ shell command on prod:
  kubectl exec -it deploy/nginx -- sh
Continue? [y/N]: y
/ # nginx -T | head -20          # the effective config
/ # wget -qO- localhost          # or curl, if the image has it
/ # exit
```

**4. Open nginx from your machine** — `port-forward` counts as a tunnel (asks on protected,
allowed on readonly). It goes through the cluster's API server, so the pod needs no public
address:

```console
$ mek -c prod kubectl port-forward svc/nginx 8080:80
● prod [protected] aws 444455556666 / Admin / ap-southeast-1
⚠ tunnel command on prod:
  kubectl port-forward svc/nginx 8080:80
Continue? [y/N]: y
Forwarding from 127.0.0.1:8080 -> 80
```

Then, in another terminal, `curl http://localhost:8080` or open it in a browser; Ctrl-C closes
the forward. The audit log gets its start and end. (It runs in the foreground; `mek tunnel`
is for hosts in a VPC, not pods.)

**5. Change things** — writes ask, destructive commands want the context's name:

```console
$ mek -c prod kubectl rollout restart deploy/nginx      # write: Continue? [y/N]
$ mek -c prod kubectl scale deploy/nginx --replicas 3   # write
$ mek -c prod kubectl delete pod nginx-7c5ddbdf54-abcde # destructive: Type prod to continue
```

In a script there is no terminal to ask: pass `--yes` for writes, shells and port-forwards,
and `--confirm prod` for destructive commands —
`mek -c prod --yes kubectl rollout restart deploy/nginx`. On a `readonly` context, steps 1, 2
and 4 work and 3 and 5 are blocked.

**Without the `mek` prefix** — export the context into the shell, then use kubectl or K9s as
usual (these skip mek's guard and audit log):

```bash
mek use -s prod
kubectl get pods -l app=nginx
kubectl port-forward svc/nginx 8080:80
k9s -n web
```

If the cluster's API endpoint is private-only, your machine needs a network path to it (VPN,
or a tunnel through a host in the VPC) — kubectl talks to the API server directly.

## 8. Shell on an instance

```bash
mek -c prod shell bastion                 # a name under the context's targets:
mek -c prod shell i-0abc1234def567890     # aws: an instance id
mek -c prod shell tag:Name=bastion        # aws: the one running instance with that tag
mek -c gcp-prod shell web                 # gcp: a target…
mek -c gcp-prod shell web-2 --zone asia-southeast1-b --user ops   # …or any VM (zone looked up if omitted)
```

AWS uses `aws ssm start-session` (needs session-manager-plugin and the SSM agent on the
instance); GCP uses `gcloud compute ssh --tunnel-through-iap`. No public IP, bastion key or
open port is needed. GCP keeps the context's SSH key and known hosts under
`~/.mek/ssh/<key>`, not `~/.ssh`.

Protected contexts ask first; readonly contexts block shells. The same applies to
`mek aws ssm start-session` and `mek gcloud compute ssh`.

## 9. Tunnels

A tunnel forwards a port on `127.0.0.1` to a host in a private network — a database, a
cache, an internal web UI — through the cloud's own session service.

```bash
mek -c prod tunnel db                     # named: localhost:15432 → the RDS endpoint, until Ctrl-C
psql -h localhost -p 15432 -U app appdb   # in another terminal

mek -c prod tunnel --via bastion --to prod-db.cluster-xyz.ap-southeast-1.rds.amazonaws.com:5432 --local 5432
mek -c prod tunnel --via i-0abc1234def567890 --to :8080      # a port on the instance itself
mek -c gcp-prod tunnel sql                                    # Cloud SQL (after: mek login gcp-prod --adc)
mek -c gcp-prod tunnel --cloudsql acme-prod:asia-southeast1:main --private-ip --local 15432
```

| Kind (config fields) | AWS | GCP |
|---|---|---|
| a port on the instance: `via` + `port` | SSM port forwarding | `gcloud compute start-iap-tunnel` |
| another host: `via` + `host` + `port` | SSM port forwarding to a remote host | `gcloud compute ssh` with `-L` — counts as a **shell** |
| Cloud SQL: `cloudsql` + `local_port` | — | Cloud SQL Auth Proxy (needs `mek login --adc`) |

The local port is `local_port`, or the remote port + 10000; mek checks it is free before
starting. A tunnel only moves bytes, so readonly contexts allow it; protected contexts ask.

**In the background** — the tunnel outlives the terminal that started it:

```console
$ mek -c prod tunnel db -b
✓ tunnel db is running in the background: localhost:15432 → prod-db…:5432 via bastion → i-0abc…
  mek tunnel ls · mek tunnel logs db · mek tunnel stop db

$ mek tunnel ls
ID        CONTEXT  NAME   LOCAL  STATE                 AGE  TARGET
16f87092  prod     db     15432  running ↻2            3h   localhost:15432 → prod-db…:5432 via bastion → i-0abc…
3b1c9e04  dev      -      18080  running (foreground)  5m   localhost:18080 → port 8080 on the instance via i-0def…

$ mek tunnel logs db -f          # the tunnel's output, as it happens
$ mek tunnel stop db             # or an id (prefix), several, or --all
$ mek -c prod tunnel stop --all  # every tunnel of one context
```

`-b` returns once the local port accepts connections (`--wait 30s` by default) and shows the
log if it doesn't. `mek tunnel ls` covers every context — foreground tunnels too — and shows
`exited (code)` when the tunnel ended, or `dead` if its supervisor was killed. Two tunnels
can't use the same local port.

**Reconnecting** — when a background tunnel's session ends on its own (SSM closes idle
sessions after 20 minutes; Wi-Fi drops; the laptop sleeps), mek starts it again, keeping the
same local port: connections open at that moment break, new ones work. `ls` shows
`reconnecting` while it waits and `↻N` for how many times it reconnected; the log has each
attempt. When it keeps failing straight away, mek waits longer each time (1 s, 2 s, 4 s… up
to 30 s) and gives up after 5 minutes of that — usually an expired login: `mek login`, then
start the tunnel again. A tunnel whose first attempt fails isn't retried (`-b` shows why).
`--no-restart` turns reconnecting off; foreground tunnels never reconnect.

## 10. Command reference

### Getting help

Every command explains itself, with examples:

```bash
mek help                    # every command, grouped, with a one-line summary
mek help tunnel             # one command in full: what it does, examples, flags
mek help tunnel stop        # a subcommand
mek tunnel --help           # the same, as a flag (or -h)
mek help config             # where contexts come from: ~/.mek/config.yaml, mek.yaml, mek.local.yaml
mek help guard              # what protected and readonly do, command by command
mek help wizard             # mek ctx add / edit: the keys, what it looks up, how it saves
```

`mek` on its own shows the same as `mek help`:

```console
$ mek help
mek (เมฆ, "cloud") manages cloud contexts — an AWS account+role or a GCP
project — and runs the official CLIs (aws, gcloud) with the right
credentials, plus a safety guard and audit log for protected contexts.

Contexts come from ~/.mek/config.yaml and, inside a project, its mek.yaml
(the team's) and mek.local.yaml (yours): mek help config.
Add one by answering questions: mek ctx add (mek help wizard).
What protected and readonly do: mek help guard.

Usage:
  mek [command]

Contexts:
  ctx         Show the current context (use `mek ctx ls` to list all)
  init        Create ~/.mek/config.yaml (or a project's mek.yaml / mek.local.yaml) from a commented example
  login       Log in to a context (AWS IAM Identity Center / gcloud)
  ui          Browse and use contexts, tunnels, shells, clusters and the audit log in one screen
  use         Switch the current context (every shell), or only this shell with -s

Run commands:
  aws         Run any AWS CLI command in the current context
  env         Print shell exports for a context: eval "$(mek env)"
  exec        Run any command (terraform, kubectl, scripts...) with the context's credentials
  gcloud      Run any Google Cloud CLI command in the current context
  kubectl     Run kubectl on the current context's clusters (see `mek kube`)

Access (shells, tunnels, Kubernetes):
  kube        Write a kubeconfig for the context's clusters (kubectl, K9s, FreeLens)
  shell       Open a shell on an instance (AWS SSM, GCP IAP + SSH)
  tunnel      Forward a local port to a private host (AWS SSM, GCP IAP, Cloud SQL)

Setup:
  completion  Generate the autocompletion script for the specified shell
  doctor      Check config and required CLIs, with install hints
  help        Help about any command
  self-update Update mek to the latest release (installs via install.sh / manual download)
  shell-init  Set up mek use -s to switch this shell: eval "$(mek shell-init zsh)"
  version     Print mek's version

Flags:
      --confirm string   confirm destructive commands non-interactively by passing the context name
  -c, --context string   context for this command (overrides the current context and $MEK_CONTEXT)
  -h, --help             help for mek
      --no-local         ignore project config files (mek.yaml, mek.local.yaml); also MEK_NO_LOCAL=1
      --project string   read the project config (mek.yaml, mek.local.yaml) from this directory instead of looking up from here
  -y, --yes              answer yes to write confirmations on protected contexts

Additional help topics:
  mek config      Where contexts come from: ~/.mek/config.yaml, a project's mek.yaml and mek.local.yaml
  mek guard       What protected and readonly do, command by command
  mek wizard      mek ctx add / edit: the keys, what it looks up in the cloud, how it saves

Use "mek [command] --help" for more information about a command.
```

After `aws`, `gcloud` or `kubectl`, help belongs to that CLI — `mek aws s3 help`,
`mek gcloud compute --help` and `mek kubectl get --help` show the real CLI's help (in the
current context). For mek's own description of those commands, use `mek help aws`.

### Commands

| Command | What it does |
|---|---|
| **Contexts** | |
| `mek init [--force]` | create `~/.mek/config.yaml` from a commented example |
| `mek init --project` / `--local` | create the project's `./mek.yaml` (for the team) / `./mek.local.yaml` (yours; added to `.gitignore`) |
| `mek ui` | contexts, tunnels, shells, clusters and the audit log in one terminal screen |
| `mek ctx ls` | list contexts (`*` = current; a project's with its file) |
| `mek ctx [--short]` | show the current context (`--short`: just the name, for prompts); `mek context` is the same command |
| `mek ctx add [name]` / `mek ctx edit [name]` | add or change a context, a question at a time ([the wizard](#5-editing-config-with-the-wizard)) |
| `mek ctx rm <ctx> [--yes]` | remove a context from the file it is in |
| `mek ctx target\|cluster\|tunnel add\|edit\|rm [name]` | a context's targets, clusters or tunnels, by wizard (`-c` picks the context) |
| `mek use <ctx>` | switch the saved context (every terminal without its own) |
| `mek use -s <ctx>` | switch only this terminal (and `KUBECONFIG`, when the context has clusters); needs `mek shell-init`, else `eval "$(mek use -s <ctx>)"` |
| `mek login [ctx] [--adc] [-- <flags>]` | `aws sso login` / `gcloud auth login` (+ application-default with `--adc`) |
| **Run commands** | |
| `mek aws …` / `mek gcloud …` | the real CLI in the context, through the guard and audit log |
| `mek kubectl …` | kubectl on the context's clusters, through the guard and audit log |
| `mek exec -- <cmd> [args…]` | any command with the context's credentials |
| `eval "$(mek env [ctx])"` | export a context into this shell (bypasses the guard and audit); fish: `mek env --syntax fish \| source` |
| **Access (shells, tunnels, Kubernetes)** | |
| `mek shell <target> [--zone Z] [--user U]` | a shell on an instance (SSM / IAP) |
| `mek tunnel [name] [--via T] [--to host:port] [--cloudsql C] [--private-ip] [--local N] [-b [--no-restart] [--wait D]]` | forward a local port |
| `mek tunnel ls` / `stop <name\|id>… \| --all` / `logs <name\|id> [-f]` | list, stop or read tunnels |
| `mek kube [cluster] [--name N --region R \| --location L]` | write the context's kubeconfig |
| **Setup** | |
| `mek shell-init zsh\|bash\|fish` | print the shell integration that makes `mek use -s` work by itself |
| `mek doctor` | check the config and tools, with install hints |
| `mek help [command]` / `mek <command> --help` | how a command works, with examples (`-h` for short) |
| `mek version` | print the version |
| `mek self-update [--check]` | update a script-installed binary |
| `mek completion zsh\|bash\|fish` | shell completion |

### Global flags

Global flags go **before** the CLI name: `mek -c prod --yes aws ecs update-service …`

| Flag | Meaning |
|---|---|
| `-c, --context <ctx>` | use a context for this command |
| `-y, --yes` | accept write confirmations (for scripts) |
| `--confirm <ctx>` | accept destructive confirmations non-interactively; must name the context |
| `--project <dir>` | read the project config (`mek.yaml`, `mek.local.yaml`) from `dir`, from anywhere |
| `--no-local` | ignore project config files; `MEK_NO_LOCAL=1` does the same |

## 11. How the guard works

**`protected` and `readonly` are mek's own settings, not the cloud's.** AWS and GCP never see
them: mek checks each command on your machine before handing it to `aws`, `gcloud` or
`kubectl`. They are there for mistakes — a delete typed in the prod terminal you took for
dev, a script run with the wrong context — not for someone who means harm.

| Setting | Effect |
|---|---|
| `protected: true` | writes, shells and tunnels ask y/N; destructive commands need the context's name typed |
| `readonly: true` | writes, destructive commands and shells are blocked; reads and tunnels run |

Commands are classified from their operation name, then the context decides (`mek help guard`
prints this table from the code itself):

| Class | Examples | plain | `protected` | `readonly` |
|---|---|---|---|---|
| read | `describe-*`, `list-*`, `get-*` (aws), `list` / `describe` (gcloud), `s3 ls`, `kubectl get`/`logs`, `--dry-run` | run | run | run |
| write | `create-*`, `update-*`, `create` / `deploy`, `s3 cp`, `kubectl apply`/`scale`, unknown verbs | run | confirm y/N | blocked |
| destructive | `delete-*`, `terminate-*`, `stop-*`, `delete` / `stop` (gcloud), `s3 rm`, `sync --delete`, `kubectl delete`/`drain` | run | type context name | blocked |
| unknown | anything via `mek exec` | run | confirm y/N | blocked |
| shell | `mek shell`, `aws ssm start-session`, `gcloud compute ssh`, `kubectl exec`/`attach`/`debug` | run | confirm y/N | blocked |
| tunnel | `mek tunnel`, SSM port forwarding, `gcloud compute start-iap-tunnel`, `kubectl port-forward`/`proxy` | run | confirm y/N | run |

- **run** — runs at once. **confirm y/N** — asks first. **type context name** — you type the
  context's name to go on. **blocked** — refused, nothing is sent.
- **Answering ahead:** `-y` / `--yes` answers yes to writes (and shells, tunnels);
  `--confirm <context>` confirms a destructive command — the name must match.
- **No terminal to ask on** (CI, a pipe, a cron job): a command that needs an answer doesn't run.
- **The audit log** records every decision — `allowed`, `confirmed`, `blocked`, `declined` —
  with the command, secret-looking values masked ([The audit log](#the-audit-log)).
- **What skips the guard:** running `aws` / `gcloud` / `kubectl` yourself, or after
  `mek env` / `mek use -s` exported a context into your shell. `mek exec` doesn't skip it: its
  commands count as *unknown*.
- **In a project:** a repository's `mek.yaml` or your `mek.local.yaml` can't switch
  `protected` or `readonly` off for a context you guard in `~/.mek/config.yaml`; they can add
  them ([Project config](#4-project-config-mekyaml-and-meklocalyaml)).

**This is a seatbelt, not a security boundary.** It prevents mistakes; real enforcement
belongs in IAM roles, SCPs and org policies. For an account that must stay read-only, give it a
read-only role in the cloud *and* `readonly: true` in mek: mek then says so before anything is
sent, instead of the cloud refusing afterwards.

## 12. Troubleshooting

| You see | Do |
|---|---|
| `no config found — run mek init first` | `mek init`, then edit `~/.mek/config.yaml` (or work in a project with a `mek.yaml`) |
| `context "x" isn't defined here — it may be a project's (mek.yaml)` | it was chosen inside a project: go back there, `mek use <context>`, or `-c <context>` |
| `⚠ prod from ./mek.yaml replaces your prod in ~/.mek/config.yaml` | the repository defines a context with your context's name; rename one, or check the project's is the one you mean (your guard stays on) |
| `✗ ./mek.local.yaml is committed` (doctor) | `git rm --cached mek.local.yaml`, and list it in `.gitignore` |
| `can't move ~/.config/mek to ~/.mek` | move the directory yourself (`mv ~/.config/mek ~/.mek`), then run mek again |
| `no context selected — run mek use <context> or pass --context` | `mek use <context>`, or pass `-c <context>` |
| `unknown context "x"` | check the name with `mek ctx ls` |
| `unknown field "…"` (with the file and line) | a misspelt key in `config.yaml`, `mek.yaml` or `mek.local.yaml` — compare with the [specification](#specification) |
| `unknown provider "azure"` | mek supports AWS and GCP; Azure and Huawei Cloud are paused (use v0.9.0 for them) |
| an SSO / token expired error from aws, or `… (logged in? try: mek -c dev login)` | `mek login <context>` |
| `confirmation required but no terminal is available` | add `--yes` (writes) or `--confirm <context>` (destructive) |
| `session-manager-plugin is needed for this` | install it (the message has the command), then retry |
| `localhost:15432 is in use` / `already forwarded by mek tunnel db` | `--local <port>`, or `mek tunnel stop db` |
| `cloud-sql-proxy needs application-default credentials` | `mek login <context> --adc` |
| `aws not found in PATH` | install the CLI; `mek doctor` shows how |
| `AWS CLI v1 can't do IAM Identity Center logins` | install AWS CLI v2 |
| kubectl: `getting credentials: exec: … mek …` | the kubeconfig remembers where mek was; run `mek -c <context> kube` again after moving mek |
| the wizard: `couldn't list them: … (UnauthorizedException) …` | the portal login expired: *Run it* on the wizard's login step, or `mek login <a context on that portal>`; or type the value |
| the wizard: `couldn't list them: … AccessDenied` / `UnauthorizedOperation` / `PERMISSION_DENIED` | your role lacks the permission in [What the wizard looks up](#what-the-wizard-looks-up): type the value, or ask for it |
| `mek ctx add asks questions and needs a terminal` | run it in a terminal; in CI or a script, edit the file |
| `not saved — ./mek.yaml would be invalid: …` | what the change would break: go Back and fix that answer (or fix the file) |
| anything else | `mek doctor`, and the audit log for what ran |

## 13. Limitations

- Needs the official CLIs installed; passthrough speed equals the CLI's speed.
- mek can't grant more than your IAM role/permissions allow.
- AWS login supports IAM Identity Center (SSO) and existing profiles; SAML-only IdPs without Identity Center are not built in (use `aws_profile` with your existing tooling).
- mek supports AWS and GCP. Azure and Huawei Cloud were in v0.4.0–v0.9.0 and are paused; that code is kept on the `keep/azure-huawei` branch.
- Every kubectl request through mek's kubeconfig fetches a fresh token (about 0.5–1 s); mek never caches tokens.
- The kubeconfig records mek's path and `PATH` when it is written; run `mek kube` again after moving mek or the cloud CLIs.
- `mek shell` on GCP runs ssh with the context's own home directory, so your `~/.ssh/config` doesn't apply to it.
- A foreground tunnel ends when the cloud closes its session (SSM's idle timeout is 20 minutes by default); use `-b`, which reconnects. A reconnect breaks the connections open at that moment.
- If a tunnel's supervisor is killed with `kill -9`, `ls` shows it as dead; on macOS the tunnel's own process can outlive it (`stop` warns when the port is still taken).
- The wizard rewrites the context it changes: comments inside that context aren't kept (the
  rest of the file's are). Its region lists are built into mek; type a region it doesn't list.
- The Homebrew cask is macOS-only; use `install.sh` or `go install` on Linux, `install.ps1` on Windows.
- Windows support is new in v0.16 and tested on CI (unit, integration, contract with the real
  `aws`, `gcloud.cmd` and `kubectl`, `install.ps1`); GCP shells through gcloud's PuTTY haven't
  been tried on a real Windows machine yet — please report what you find.
- Windows: stopping a tunnel ends it at once (Windows has no SIGTERM to let it clean up), and a
  killed supervisor takes its tunnel with it.
- Windows: `gcloud` is a `.cmd`, so an argument for it with `%` or `"` is refused (cmd.exe would
  change it); gcloud takes single quotes in filters.
- Windows: `mek shell` and `host:` tunnels on GCP use the PuTTY that comes with gcloud; the key
  stays in the context's own directory, but PuTTY keeps host keys in the registry
  (`HKCU\Software\SimonTatham\PuTTY\SshHostKeys`), shared by every context.

## 14. Roadmap

1. ✅ Contexts, SSO login, passthrough, exec/env, guard, audit, doctor, self-update
2. ✅ Access paths ([design](docs/design/0001-tunnel-shell-kube.md)): `mek kube` / `mek kubectl` (EKS, GKE), `mek shell` (SSM, IAP), `mek tunnel` (SSM, IAP, Cloud SQL), background tunnels
3. ✅ Terminal UI (`mek ui`): contexts, tunnels, shells, clusters, audit log
4. ✅ Config wizard (`mek ctx add` / `edit` / `rm`, and a context's targets, clusters and tunnels), with lists from the cloud
5. A resource catalog in the terminal UI, generated from AWS/GCP API models
6. Desktop GUI (Wails)
7. Azure and Huawei Cloud again, later (see `keep/azure-huawei`)

## Development

How the code fits together (the GoF patterns are named where they shape it):

- **One file per cloud** (`internal/provider/aws.go`, `gcp.go`). Each defines a `Cloud`:
  an Abstract Factory for that cloud's Provider (an Adapter from a mek context to the
  official CLI), its command classifier (a Strategy from `internal/guard`), its config
  rules and the plugins it may need. A Provider also covers the access paths:
  `KubeProvider` (`mek kube`), `Sessioner` (`mek shell`) and `Tunneler` (`mek tunnel`;
  its `TunnelMethod` is a Factory Method for one Strategy per kind of tunnel). Adding a
  cloud = one file plus an entry in `clouds` (`cloud.go`).
- **Every guarded command is a `runner.Invocation`** (a Command) passed through
  Decorators: `audit → guard → sessionStart → terminal`, so blocked commands are
  audited too and sessions get start and end entries. The terminal is `exec`, a
  foreground tunnel's registration around it, or a background tunnel's supervisor;
  tests swap it for a fake.
- **`internal/kube`** renders kubeconfigs (a Template Method; the cloud's describe call
  fills the steps). **`internal/tunnel`** is a Facade over tunnel records, flock-based
  liveness, port claims and the supervisor, with a State per tunnel deciding what
  listing and stopping it means.
- **`internal/proc`** is what differs per OS — the implementation side of a Bridge: file
  locks, process groups, detaching, stop requests and running `.cmd` programs are flock and
  signals in `proc_unix.go`, LockFileEx, Job Objects, named events and cmd.exe quoting in
  `proc_windows.go`; `internal/tunnel` and `internal/audit` call one API.
- **`internal/tui`** is `mek ui` (Bubble Tea): each tab and overlay is a State handling its
  own keys, and every key that acts is an `action` (a Command) holding mek's own arguments,
  run in the terminal or in the background — the UI never talks to a cloud itself.
  Design notes: [docs/design/0001-tunnel-shell-kube.md](docs/design/0001-tunnel-shell-kube.md).

```bash
make             # list all targets
make check       # gofmt check + go vet + unit tests (race) — run before pushing
make test-all    # check + integration + e2e + contract + emulator (the SSO login)
make cover       # unit + integration coverage; fails below 100%
make run ARGS="doctor"
make install     # build with version info and copy to ~/.local/bin
make vuln        # govulncheck
make snapshot    # goreleaser build of all archives into dist/ (no publish)
```

Tests come in five layers, all run by CI (integration and e2e on Linux and macOS):

| Layer | Where | What it covers |
|---|---|---|
| Unit | `*_test.go` next to the code | every package, with fakes for processes, prompts, HTTP and the filesystem |
| Integration (`-tags integration`) | `test/integration` | the real binary against recording stub CLIs: env per cloud, leaked credentials removed, guard + audit, exit codes, signal forwarding, concurrent runs, login flows, kube/shell/tunnel commands, and background tunnels with a stub that really listens (start → ls → stop, reconnecting, a `kill -9`ed supervisor, ten simultaneous starts) |
| E2E (`-tags e2e`) | `test/e2e` | release archives served over HTTP, installed by the real `install.sh` (including tampered / unlisted archives being refused), a new user's first session on both clouds, the confirmation prompts (including shells and tunnels) answered on a real pseudo-terminal, a background tunnel outliving the terminal that started it, and `mek ui` driven by keys (switch, start and stop a tunnel, open a shell, quit with the terminal restored) |
| Contract (`-tags contract`) | `test/contract` | mek with the **real** `aws`, `gcloud`, `gke-gcloud-auth-plugin` and `kubectl`, offline: each CLI reads the config, directories, kubeconfigs and session commands mek hands it, against local fake APIs where one is needed. A missing CLI is skipped; `MEK_CONTRACT_REQUIRE=1` makes it fail. `sh test/contract/install-clis.sh` installs the CLIs on Debian/Ubuntu |
| Emulator (`-tags emulator`) | `test/emulator` | the **full AWS IAM Identity Center login** with the real `aws` against the [Floci](https://floci.io) emulator in docker — `mek login` → `aws sso login` (device code) → approval → role credentials through the profile mek generates — then a real call, a readonly block and a confirmed delete with them. It starts its own container on a random local port (never an emulator you already run); a small test proxy adds the `x-amzn-ErrorType` header Floci 2.1.0 leaves out of pending-token responses |

`make cover` merges unit coverage with coverage from the instrumented binary the
integration tests run, so `main()` and real process paths count too. Tests that
check permission errors need a non-root user.

On Windows (CI's `windows-latest` job) the unit, integration and contract tests run too —
with the real `aws`, `gcloud.cmd`, `gke-gcloud-auth-plugin` and `kubectl`, and background
tunnels through Job Objects — plus `install.ps1` in Windows PowerShell; every statement in
`*_windows.go` must be covered (`go run ./test/testkit/cmd/covercheck -suffix _windows.go
cover.out`), since the 100% gate on Linux and macOS never compiles them. Tests that need a
POSIX shell or signals skip there.

### Everything in Docker

`test/docker/` is mek's own test environment, so you don't install the cloud CLIs
or start the emulator by hand: an image with Go and the real `aws`, `gcloud` (with
`gke-gcloud-auth-plugin`) and `kubectl`, plus the Floci AWS emulator.

```bash
make docker-test                          # every layer + coverage, nothing skipped
make docker-test TARGETS="test-contract"  # just some make targets
make docker-up                            # only the emulator, for local runs
make docker-down                          # remove mek's containers, network, cache volume
make docker-clean                         # ...and the mek-test image (a few GB)
```

It is isolated from other projects on the same machine: compose project
`mek-test` with its own network and volume, everything labelled
`io.github.prateep-r.mek=test`, and an emulator port that isn't Floci's default —
`127.0.0.1:14566` — so an emulator another project runs on 4566 is never touched.
With `make docker-up` running, point the emulator test at it:

```bash
MEK_FLOCI_AWS_URL=http://127.0.0.1:14566 make test-emulator
```

Releases: push a tag `vX.Y.Z` — GitHub Actions runs GoReleaser, publishes the
release archives and updates the Homebrew tap (needs the `HOMEBREW_TAP_TOKEN`
secret: a fine-grained PAT with *Contents: read/write* on `homebrew-tap` only).
