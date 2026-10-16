<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/mek-logo-dark.svg">
    <img src="assets/logo/mek-logo-light.svg" alt="mek" width="300">
  </picture>
</h1>

**mek** (เมฆ — Thai for *cloud*) keeps all your cloud accounts in one place, logs you
in, switches between them and runs the official CLIs with the right credentials.
Think *K9s/Lens, but for cloud accounts*.

**Supported clouds: AWS, Google Cloud (GCP), Microsoft Azure and Huawei Cloud** — on macOS and Linux.

| Cloud | Signing in | Shells and tunnels | Kubernetes | Databases |
|---|---|---|---|---|
| **AWS** | IAM Identity Center (SSO), or any AWS CLI profile | SSM Session Manager | EKS | RDS and Aurora, through a tunnel |
| **GCP** | your Google account through gcloud (a key file in CI) | IAP, with SSH | GKE | Cloud SQL, through the Auth Proxy |
| **Azure** | your Microsoft Entra ID account through az | Azure Bastion | AKS | any private IP, through Bastion |
| **Huawei Cloud** | a KooCLI profile you made (SSO, or keys) | — | — | — |

Huawei Cloud contexts run `hcloud` (KooCLI) with the guard and the audit log; they have no
shells, tunnels, clusters or Resources tab ([why](#limitations)). Other clouds aren't supported.

```bash
mek ctx add                    # add an account by answering questions (or edit the YAML)
mek login dev                  # SSO once, covers every account behind the same portal
mek use dev                    # switch context
mek aws s3 ls                  # any aws command, in that context
mek -c gcp-dev gcloud compute instances list
mek -c az-dev az vm list
mek -c prod kubectl get pods   # EKS/GKE/AKS, with no key on disk
mek -c prod tunnel db -b       # localhost:15432 → a private database
mek exec -- terraform plan     # any tool, same credentials
```

- **Every CLI feature, day one** — `mek aws …` / `mek gcloud …` / `mek az …` / `mek hcloud …` pass everything through to the real CLI.
- **No long-lived keys** — AWS uses IAM Identity Center (SSO) through the AWS CLI's own token cache; GCP and Azure use an isolated gcloud or az config per context; kubeconfigs fetch a fresh token on every request.
- **Your files stay untouched** — mek writes its own AWS config instead of editing `~/.aws/config`, keeps gcloud off `~/.config/gcloud` and az off `~/.azure`, and never writes `~/.kube/config` or `~/.ssh`.
- **Prod guard** — `protected` contexts confirm writes and require typing the context name for destructive commands; `readonly` contexts block them.
- **Audit log** — every command is recorded with secrets masked; shells and tunnels get an entry when they start and one when they end.

## Contents

- [Install](#install)
- [Getting started](#getting-started)
- [Everyday use](#everyday-use)
- [Project config: mek.yaml and mek.local.yaml](#project-config-mekyaml-and-meklocalyaml)
- [Editing config with the wizard](#editing-config-with-the-wizard)
- [Kubernetes (EKS, GKE, AKS)](#kubernetes-eks-gke-aks)
- [Shell on an instance](#shell-on-an-instance)
- [Tunnels](#tunnels)
- [Configuration reference](#configuration-reference)
- [How the guard works](#how-the-guard-works)
- [Command reference](#command-reference)
- [What the cloud side needs](#what-the-cloud-side-needs)
- [Troubleshooting](#troubleshooting)
- [Limitations](#limitations)
- [Development](#development)

## Install

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

### Requirements

mek drives the official tools, so install the ones your contexts use (`mek doctor` lists them
all, marks the ones your contexts need as required, and gives install commands — `brew` on
macOS where there is one):

| For | Needs |
|---|---|
| AWS contexts | [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) |
| GCP contexts | [Google Cloud CLI](https://cloud.google.com/sdk/docs/install) |
| Azure contexts | [Azure CLI](https://learn.microsoft.com/cli/azure/install-azure-cli) |
| Huawei Cloud contexts | [KooCLI](https://support.huaweicloud.com/intl/en-us/qs-hcli/hcli_02_003.html) (`hcloud`), with a profile you made: `hcloud configure init` |
| `mek kubectl`, kubeconfigs | [kubectl](https://kubernetes.io/docs/tasks/tools/) |
| GKE clusters | [gke-gcloud-auth-plugin](https://cloud.google.com/kubernetes-engine/docs/how-to/cluster-access-for-kubectl#install_plugin) |
| AKS clusters | [kubelogin](https://azure.github.io/kubelogin/install.html) |
| `mek shell` / `mek tunnel` on AWS | [session-manager-plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html) |
| Cloud SQL tunnels | [cloud-sql-proxy](https://cloud.google.com/sql/docs/postgres/connect-auth-proxy#install) |
| GCP shells, GCP tunnels to another host | `ssh` and `ssh-keygen` ([OpenSSH](https://www.openssh.com); built into macOS, `apt install openssh-client` on Debian/Ubuntu) |
| Azure shells and tunnels | az's `bastion` extension, and `ssh` for Microsoft Entra ID logins: `az extension add --name bastion --name ssh` (mek never installs them; `mek shell` says when one is missing) |

AWS CLI v1 doesn't work: it can't do IAM Identity Center (`sso-session`) logins, and
`mek doctor` says so.

### Optional: completion, shell integration, updates

**Shell completion** (contexts, clusters, targets and tunnels complete too):

```bash
# zsh
mek completion zsh > "${fpath[1]}/_mek"
# bash
mek completion bash > ~/.local/share/bash-completion/completions/mek
```

**Shell integration** — one line in your shell's startup file lets `mek use -s <context>`
switch just that terminal (otherwise it needs `eval "$(…)"`, since a program can't change its
shell's variables):

```bash
eval "$(mek shell-init zsh)"      # ~/.zshrc
eval "$(mek shell-init bash)"     # ~/.bashrc
```

**Update**: `brew upgrade mek` (Homebrew), or `mek self-update` (script / manual installs;
`mek self-update --check` only looks). mek tells Homebrew users to use brew.

## Getting started

A ten-minute walk from nothing to working in five accounts: AWS `dev`, `prod` and `audit`
behind one IAM Identity Center portal, a GCP project and an Azure subscription.

### Step 1 — add your accounts

`mek ctx add` asks a question at a time and writes `~/.mek/config.yaml` for you. For an AWS
account behind IAM Identity Center it lists the accounts and roles you can pick (logging you
in first if need be); for GCP, your projects; for Azure, your tenants and subscriptions:

```console
$ mek ctx add dev
  … the portal, the account and role from lists, the region, the guard …
✓ added dev to ~/.mek/config.yaml
  next: mek login dev (or mek use dev)
```

Then the others: `mek ctx add prod` (Guard: *protected*), `mek ctx add audit` (Guard:
*readonly*), `mek ctx add gcp-dev` (Cloud: GCP), `mek ctx add az-dev` (Cloud: Azure). For
`prod` and `audit` the wizard lists the portal you already used, so the one login covers them. [Editing config with the wizard](#editing-config-with-the-wizard) shows every
question.

Joining a team whose repository has a `mek.yaml`? You may need no context of your own: see
[Project config](#project-config-mekyaml-and-meklocalyaml).

<details>
<summary>Or write the file yourself</summary>

`mek init` creates `~/.mek/config.yaml` from a commented example (`--force` writes it again).
Each *context* is one cloud target; the five above look like this:

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

  az-dev:
    provider: azure
    tenant_id: acme.onmicrosoft.com                          # Microsoft Entra tenant: id or domain
    subscription_id: 00000000-1111-2222-3333-444444444444    # a GUID
    region: southeastasia                                    # optional: where new resources go
```

Already have an AWS profile you like? `provider: aws` with `aws_profile: my-profile` uses it
as it is. Every key is in the [specification](#specification).

</details>

### Step 2 — check

```console
$ mek ctx ls
  audit                aws 777788889999 / ReadOnly / -               [readonly]
  az-dev               azure 00000000-1111-2222-3333-444444444444 / southeastasia
  dev                  aws 111122223333 / Developer / ap-southeast-1
  gcp-dev              gcp acme-dev / asia-southeast1
  prod                 aws 444455556666 / Admin / ap-southeast-1     [protected]

$ mek doctor
```

`mek doctor` checks the config and lists every CLI and plugin mek can use — `aws`, `gcloud`,
`az`, `hcloud`, `session-manager-plugin`, `gke-gcloud-auth-plugin`, `cloud-sql-proxy`, `ssh`, `ssh-keygen`, `kubelogin`,
`kubectl`, `k9s` — marking the ones your contexts need as required, with the install command for anything
missing. It also flags AWS CLI v1.

### Step 3 — log in

```console
$ mek login dev
→ logging in to dev aws 111122223333 / Developer / ap-southeast-1
  (the browser opens the IAM Identity Center page; approve it)
✓ logged in — identity:
  … aws sts get-caller-identity …

$ mek login gcp-dev            # gcloud auth login, into gcp-dev's own gcloud config
$ mek login gcp-dev --adc      # …plus application-default credentials for SDKs/terraform
$ mek login az-dev             # az login --tenant …, then az account set, in az-dev's own az config
```

`dev`, `prod` and `audit` share one portal, so the one login covers all three (until the
SSO session expires — the portal's session length, 8 hours by default).

### Step 4 — choose a context

```console
$ mek use dev
✓ switched to dev aws 111122223333 / Developer / ap-southeast-1

$ mek ctx
dev  aws 111122223333 / Developer / ap-southeast-1
```

| How | Scope |
|---|---|
| `mek use dev` | saved: every terminal that hasn't picked its own |
| `mek use -s prod` | this terminal only (sets `$MEK_CONTEXT`) — with [shell integration](#optional-completion-shell-integration-updates); without it, `eval "$(mek use -s prod)"` |
| `mek -c audit …` | this one command |

The most specific wins: `-c` over `$MEK_CONTEXT` over the saved context.

### Step 5 — work

```console
$ mek aws s3 ls                                    # dev, the current context
$ mek -c gcp-dev gcloud compute instances list
$ mek -c az-dev az vm list -o table
$ mek -c audit aws ec2 describe-instances          # reads are fine on readonly
$ mek -c audit aws s3 rb s3://old-bucket
● audit [readonly] aws 777788889999 / ReadOnly / -
mek: audit is readonly — blocked destructive command: aws s3 rb s3://old-bucket

$ mek exec -- terraform plan                       # any tool with dev's credentials
$ eval "$(mek env gcp-dev)"                        # or export gcp-dev into this shell
```

That's the core. The rest of this page is reference and recipes.

## Everyday use

### The terminal UI

`mek ui` puts contexts, tunnels, shells, clusters, the audit log and the cloud's resources on
one screen:

```console
$ mek ui                 # or: mek -c prod ui, to start on prod
mek   1 Contexts   2 Tunnels   3 Access   4 Audit   5 Resources
     CONTEXT  ACCOUNT
     audit    aws 777788889999 / ReadOnly / -                readonly
     az-dev   azure 00000000-1111-2222-3333-444444444444 / southeastasia
▸ *  dev      aws 111122223333 / Developer / ap-southeast-1
     gcp-dev  gcp acme-dev / asia-southeast1
     prod     aws 444455556666 / Admin / ap-southeast-1      protected

↑↓ choose · enter use (saved current) · l log in · a add · e edit · d remove · 1-5/tab switch · ? keys · q quit
```

| Tab | Shows | Keys |
|---|---|---|
| 1 Contexts | every context; `*` is the saved current one | `enter` make it current · `l` log in · `a` add · `e` edit · `d` remove ([the wizard](#in-mek-ui)) |
| 2 Tunnels | every tunnel (state, `↻` reconnects, port, age), and named tunnels that aren't running | `enter` start a named one in the background, or read a running one's log · `x` stop · `l` log (follows new lines) · `a` add a tunnel to the chosen context |
| 3 Access | the chosen context's targets and clusters | `enter` a shell on a target, or write a cluster's kubeconfig · `k` K9s on a cluster |
| 4 Audit | the last 500 entries, newest first | `/` search · `b` only blocked / declined · `c` only the chosen context · `esc` clear |
| 5 Resources | the chosen context's cloud: its services, their kinds of resource, the resources, one resource's details ([below](#the-resources-tab)) | `enter` open · `esc` back · `/` filter · `a` actions · `r` list again |

- **The chosen context:** the one on tab 1 is the one tabs 3, 4 and 5 use.
- **Everything runs mek itself** — `mek use`, `mek -c dev tunnel db -b`, `mek -c dev shell
  bastion` — so the guard and the audit log work as when typed: a protected context asks first
  (in the UI, or in the terminal for shells), a readonly one still blocks shells.
- **Shells, logins, K9s and the wizard** take over the terminal and return to the UI when they
  end; when one fails, it waits for Enter so the error can be read.
- **Tunnels and the audit log** refresh every second.

#### The Resources tab

Every service of the context's cloud, and every kind of resource in it that a list
command shows with no arguments — EC2 instances, RDS databases, Lambda functions, S3
buckets, Compute Engine VMs, Cloud SQL instances, Cloud Run services, Azure VMs, AKS
clusters, web apps and the rest:

```console
mek   1 Contexts   2 Tunnels   3 Access   4 Audit   5 Resources
  dev  aws › ec2 › instances

  ID                   NAME     STATE
▸ i-0aaa1111bbbb2222c  bastion  running
  i-0ddd3333eeee4444f  batch    stopped

enter details · a actions · / filter · r reload · esc back · 1-5/tab switch · ? keys · q quit
```

- **Lists, details and actions are mek runs** of the cloud's CLI — `mek -c dev aws ec2
  describe-instances`, `mek -c dev gcloud compute instances stop vm-1 --zone=…`, `mek -c az-dev az vm
  stop --ids …` — so they use
  the context's login, and the guard and the audit log apply as when typed.
- **Actions** (`a`) are the commands that take nothing but the resource: start, stop,
  reboot, delete and the like. mek ui asks before each one, on any context; on a protected
  context a destructive one needs the context's name typed. A readonly context blocks them.
  A command that needs more than the resource isn't offered: type it with `mek aws` / `mek gcloud` / `mek az`.
- **Details** are the resource's describe output as JSON, or its list entry when there is no
  describe command.
- **Long lists:** AWS lists come 100 at a time; moving past the last row fetches the next
  hundred. gcloud lists show the first 500; az lists everything at once.
- **Where the list comes from:** the services and their commands are generated from the AWS
  CLI's, gcloud's and az's own command models (`make catalog`, see
  [Development](#development)), and built into mek. The tab names the CLI version they came
  from.

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
[Kubernetes](#kubernetes-eks-gke-aks).

### Protected and readonly contexts

```console
$ mek -c prod aws ecs update-service --cluster c --service s --desired-count 3
● prod [protected] aws 444455556666 / Admin / ap-southeast-1
⚠ write command on prod:
  aws ecs update-service --cluster c --service s --desired-count 3
Continue? [y/N]: y

$ mek -c prod aws ec2 terminate-instances --instance-ids i-0abc
● prod [protected] aws 444455556666 / Admin / ap-southeast-1
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

[How the guard works](#how-the-guard-works) (or `mek help guard`) has the rules, command by
command — `mek exec` included — and what the guard doesn't cover.

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

mek doesn't replace `aws`, `gcloud` or `az`; it runs them with the context's settings.

- Run `aws` / `gcloud` / `az` yourself, without mek, and nothing changes: mek never edits
  `~/.aws/config`, `~/.config/gcloud` or `~/.azure`.
- **AWS SSO contexts**: mek rewrites its own AWS config (`~/.mek/aws/config`, or `config-<hash>`
  for a project's context) on every run, so
  `mek aws configure set …` doesn't stick there — change the account, role or region in
  `config.yaml`. **`aws_profile` contexts** use your own `~/.aws/config`; configure them with
  `aws configure` as usual.
- **GCP contexts** each have their own gcloud config, which keeps what you set:
  `mek -c gcp-dev gcloud config set compute/zone asia-southeast1-b`. The project, account and
  region come from `config.yaml` and win over gcloud's own settings.
- **Azure contexts** each have their own az config (`AZURE_CONFIG_DIR`), which keeps what you
  set (`mek -c az-dev az config set defaults.group=rg-app`). az's extensions are your own,
  shared by every context: `az extension add` once, without mek. The tenant, subscription and
  region come from `config.yaml`.
- **Huawei Cloud contexts** name one of your KooCLI profiles; mek adds `--cli-profile` and
  `--cli-region` to each API command (`mek -c hw hcloud ECS ListServersDetails` runs
  `hcloud ECS ListServersDetails --cli-profile=sso-prod --cli-region=ap-southeast-3`), since
  KooCLI has no environment variables for them. KooCLI's own commands (`configure`,
  `version`) are passed as they are. mek never writes `~/.hcloud`.
- To type plain `aws …` / `gcloud …` / `az …` / `kubectl …` in a context, export it into the shell:
  `eval "$(mek env prod)"`. Commands typed that way skip mek's guard and audit log.
  (`mek use -s prod` switches mek's own commands in this shell — and plain `kubectl` / K9s,
  through `KUBECONFIG` — but not plain `aws`, `gcloud` or `az`.)
- To see what mek runs for each command — the exports and the command — turn on
  [show-cmd](#seeing-the-native-command).

### Seeing the native command

Want to see what mek runs — to learn the CLIs, check what a context does, or copy it into a
script? Turn on **show-cmd**: before each command, mek prints it as you would type it yourself,
with the variables it sets or removes:

```console
$ mek -c prod aws s3 ls
● prod [protected] aws 444455556666 / Admin / ap-southeast-1
↳ runs:
    unset AWS_ACCESS_KEY_ID
    export AWS_CONFIG_FILE='/Users/you/.mek/aws/config'
    export AWS_DEFAULT_REGION='ap-southeast-1'
    export AWS_PROFILE='mek-prod'
    export AWS_REGION='ap-southeast-1'
    aws s3 ls
    # typed yourself, it skips mek's guard and audit log
2026-10-01 12:00:00 my-bucket
```

```console
$ mek -c gcp-dev gcloud compute instances list
↳ runs:
    export CLOUDSDK_COMPUTE_REGION='asia-southeast1'
    export CLOUDSDK_CONFIG='/Users/you/.mek/gcloud/gcp-dev'
    export CLOUDSDK_CORE_ACCOUNT='you@acme.com'
    export CLOUDSDK_CORE_CHECK_GCE_METADATA='false'
    export CLOUDSDK_CORE_PROJECT='acme-dev'
    export GOOGLE_CLOUD_PROJECT='acme-dev'
    gcloud compute instances list
    # typed yourself, it skips mek's guard and audit log
```

```console
$ mek -c az-dev az vm list
↳ runs:
    export ARM_SUBSCRIPTION_ID='00000000-1111-2222-3333-444444444444'
    export ARM_TENANT_ID='acme.onmicrosoft.com'
    export AZURE_CONFIG_DIR='/Users/you/.mek/azure/az-dev'
    export AZURE_CORE_LOGIN_EXPERIENCE_V2='off'
    export AZURE_DEFAULTS_LOCATION='southeastasia'
    export AZURE_EXTENSION_DIR='/Users/you/.azure/cliextensions'
    export AZURE_SUBSCRIPTION_ID='00000000-1111-2222-3333-444444444444'
    export AZURE_TENANT_ID='acme.onmicrosoft.com'
    az vm list
    # typed yourself, it skips mek's guard and audit log
```

- **Only what differs from this shell** is listed: after `eval "$(mek env prod)"` it is just
  `aws s3 ls`. An `unset` line appears only for a variable
  this shell has (like a leftover access key, which would win over the profile).
- **Every command mek runs** is shown: `kubectl` with its `KUBECONFIG`, `aws ssm start-session` for
  `mek shell`, the `aws ec2 describe-instances` that finds a `tag:` target first, `aws sso login`
  for `mek login`. A command the guard blocks or you decline isn't (it doesn't run).
- **It goes to stderr**, so `mek aws … | jq` gets only the command's output.
- **Never shown:** in `mek kube token` (kubectl reads its output on every request), in `mek ui`'s
  background actions (their output is the status line), and in the wizard's lookups.

Turn it on or off — the first that says wins:

| How | Example | For |
|---|---|---|
| a flag, before the CLI name | `mek --show-cmd -c prod aws s3 ls`, `--no-show-cmd` | this command |
| an environment variable | `MEK_SHOW_CMD=1` / `MEK_SHOW_CMD=0` | this shell, or a script |
| a setting | `mek show-cmd on` / `off` — writes `settings: {show_cmd: true}` in `~/.mek/config.yaml` (comments kept) | every terminal |
| none of them | | off |

`mek show-cmd` on its own says whether it is on and what decided it
(`show-cmd is off — MEK_SHOW_CMD=0 in this shell`).

### Terraform, SDKs and other tools

```bash
mek -c dev exec -- terraform plan           # AWS provider picks up AWS_PROFILE + AWS_CONFIG_FILE
mek login gcp-dev --adc                     # once: application-default credentials for gcp-dev
mek -c gcp-dev exec -- terraform apply      # google provider picks up the ADC and the project
mek -c az-dev exec -- terraform plan        # azurerm uses az-dev's az login (ARM_TENANT_ID, ARM_SUBSCRIPTION_ID)
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

For Azure, `mek login ci-az -- --service-principal -u "$AZURE_CLIENT_ID" --certificate /path/cert.pem`
signs a service principal in to the context's own az config (or `--identity` for a managed
identity on an Azure VM).

mek removes `GOOGLE_APPLICATION_CREDENTIALS`, `AZURE_CLIENT_SECRET` and similar variables from the commands it runs
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

`mek ctx --short` reads only the config, so it is fast enough to run on every prompt.

## Project config: mek.yaml and mek.local.yaml

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
file says. ([What protected and readonly do](#how-the-guard-works).)

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

Commit `mek.yaml` only to a private repository: it names your accounts and your portal.

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

Use IAM Identity Center fields (`sso_start_url`, `sso_region`, `account_id`, `role`) in `mek.yaml`;
`aws_profile` names a profile in one person's `~/.aws/config`, so contexts with it belong in
`mek.local.yaml` or `~/.mek/config.yaml`. Review changes to `mek.yaml` like code: they decide
which account a teammate's commands reach.

### For a teammate: start using it

```console
$ git clone …/orders && cd orders
$ mek ctx ls
  orders-dev           aws 111122223333 / Developer / ap-southeast-1  (./mek.yaml)
  orders-prod          aws 444455556666 / Developer / ap-southeast-1 [protected] (./mek.yaml)
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
copy a context from mek.yaml and change what is yours (mek ctx edit <context> does it); check with: mek ctx ls
```

`mek init --local` adds the file to the repository's `.gitignore`, and `mek doctor` flags it
if it is committed anyway. Things it is for:

1. **The team's context with your role** — `mek ctx edit orders-dev`, then *your own copy* in
   `mek.local.yaml`, does it for you ([how](#change-a-context)). By hand: copy the whole
   context (only `role:` would drop the rest) and change what is yours:

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
$ mek -c orders-dev ctx           # orders-dev (./mek.local.yaml)  aws 111122223333 / Admin / ap-southeast-1
$ mek doctor                      # every file read, highest first, and what each replaces
Config
  ✓ ./mek.local.yaml — 1 context, replacing orders-dev from below
  ✓ ./mek.yaml — 1 context
  ✓ ~/.mek/config.yaml — 4 contexts
  current: dev
```

When the team's `mek.yaml` replaces a context of your own, mek warns every time a command runs in it:
`⚠ prod from ./mek.yaml replaces your prod in ~/.mek/config.yaml`. (A context of yours
replaced by your own `mek.local.yaml` isn't warned about.) The audit log records the file
(`config`) for every command on a project's context.

### Questions

- **Why can't I put just `tunnels:` in mek.local.yaml?** A context comes whole from one file
  (see above): copy the context, then add to it.
- **I left the project and the context is gone.** A project's contexts exist inside it only.
  A context chosen there with `mek use` makes mek say so elsewhere; for project work,
  `mek use -s orders-dev` (this shell only) suits better.
- **Why does prod still ask, when mek.yaml doesn't say protected?** Your own `prod` is
  protected, and a project can't switch that off.
- **Where are a project context's logins?** In `~/.mek` like any other, filed by name and a
  hash of who it is — portal, account, role, profile, project — (`gcloud/orders-dev-1a2b3c4d`,
  profile `mek-orders-dev-1a2b3c4d`): two projects' `dev` never share one, the same `dev` in
  two clones does, and your `mek.local.yaml` copy with another role gets its own.
- **Can a project's kubeconfig work from K9s started anywhere?** Yes: it runs
  `mek --project <the repository> --context … kube token`.

## Editing config with the wizard

Rather not write YAML? `mek ctx add` asks a question at a time, offers what it can find —
your IAM Identity Center accounts and roles, your GCP projects, your Azure tenants and
subscriptions, instances, databases, clusters — checks the result, and writes it into the right file without touching the rest.

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
| *type it yourself…* | the last row of a list that takes any value (accounts, roles, regions, projects, instances…): a value that isn't listed |
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
2. **Cloud** — AWS, GCP, Azure or Huawei Cloud.
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
   typed) or *readonly* (changes are blocked). See [How the guard works](#how-the-guard-works).
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

<details>
<summary>Show the example</summary>

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

</details>

### Add a GCP context

<details>
<summary>Show the example</summary>

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

</details>

### Add an Azure context

<details>
<summary>Show the example</summary>

```console
$ mek ctx add az-prod
Add a context
✓ Name: az-prod
✓ Cloud: Azure
✓ Log in to Azure
✓ Tenant: Acme
? Subscription
  the tenant's subscriptions you can see
  ▸ Acme Dev  00000000-1111-2222-3333-444444444444
    Acme Prod  55555555-6666-7777-8888-999999999999
    type it yourself…
```

- Not logged in yet? **Log in to Azure** runs `az login` with the form put away. That login
  becomes the new context's when you save: no second `mek login`. Another Azure context's login
  is used to list instead, when there is one.
- **Tenant** — the Microsoft Entra tenants your login reaches, by name.
- **Subscription** — the tenant's, by name (one that isn't enabled says so).
- **Bastion** — the subscription's Azure Bastion hosts, which shells and tunnels go through;
  *none* leaves that for later.
- **Region** — optional: where new resources go (*none*: az's default).

```yaml
az-prod:
  provider: azure
  tenant_id: 9f8e7d6c-0000-1111-2222-333344445555
  subscription_id: 55555555-6666-7777-8888-999999999999
  bastion:
    name: bas-hub
    resource_group: rg-network
```

</details>

### Add a Huawei Cloud context

<details>
<summary>Show the example</summary>

mek uses a KooCLI profile you made with `hcloud configure init` (or `hcloud configure set`);
it reads `~/.hcloud/config.json` and never writes it:

```console
$ mek ctx add hw-prod
Add a context
✓ Name: hw-prod
✓ Cloud: Huawei Cloud
? KooCLI profile
  from ~/.hcloud/config.json — make one with: hcloud configure init
  ▸ default  AKSK ap-southeast-2
    sso-prod  SSO ap-southeast-3
    type it yourself…
```

Then the region (*none*: the profile's own) and the guard:

```yaml
hw-prod:
  provider: huawei
  region: ap-southeast-3
  hcloud_profile: sso-prod
```

</details>

### Add a context inside a project

<details>
<summary>Show the example</summary>

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

- `./mek.local.yaml` — only you, only here. Inside a git repository, a new one is added to its
  `.gitignore`.
- `./mek.yaml` — the whole team gets it once you commit it. Use IAM Identity Center here, not
  a profile: a profile is in one person's `~/.aws/config`.
- `~/.mek/config.yaml` — yours, everywhere.

A new `mek.yaml` or `mek.local.yaml` starts as `mek init --project` / `--local` would write it
(with its comments); a new `~/.mek/config.yaml` starts with a short header. Then it gets the
context.

</details>

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
  ▸ ./mek.yaml  the team's — commit the change
    ./mek.local.yaml  your own copy — the team's stays as it is
```

It starts on the team's file. *Your own copy* (`↓`, then `enter`) is the usual case — the
team's account with your role:

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
      tunnels:
        db:
          via: tag:Name=orders-bastion
          host: orders-db.internal
          port: 5432
      clusters:
        orders:
          name: orders-eks
  [Save]  Back   Cancel

✓ added mek.local.yaml to ./.gitignore
✓ copied orders-dev to ./mek.local.yaml — yours is used in this project; the team's stays as it is
```

The copy is the whole context, so its tunnels and clusters came too.
It can add `protected` / `readonly`, but not drop one the team's `mek.yaml` sets
([Project config](#project-config-mekyaml-and-meklocalyaml)).

### Remove a context

<details>
<summary>Show the example</summary>

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

</details>

### Add a tunnel

```console
$ mek -c orders-dev ctx tunnel add
Add a tunnel to orders-dev
? Forward to
  ▸ a host, through an instance  e.g. a database: RDS, a private IP, Redis
    a port on an instance  a service on the instance itself
```

On a GCP context there is a third choice, *Cloud SQL* (below). On Azure, the first is *an IP,
through Bastion*: Bastion connects to it itself, with no VM in between.

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

**An IP through Bastion** (Azure) — type the private IP (Bastion connects to IPs, not names),
then the port; nothing in between:

```yaml
tunnels:
  db:
    host: 10.3.0.5
    port: 5432
```

*A port on a VM* lists the running VMs; the tunnel goes through the context's Bastion.

### Add a target

<details>
<summary>Show the example</summary>

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

On Azure the running VMs are listed with their resource group, which is filled in; then how
to log in — your Microsoft Entra ID account (no key; the VM needs the Entra ID login
extension) or the context's SSH key (and a user) — and, on a context with no Bastion, the
target's own:

```yaml
targets:
  jump:
    instance: vm-jump
    resource_group: rg-app
```

Then: `mek -c orders-dev shell web-1`.

</details>

### Add a cluster

<details>
<summary>Show the example</summary>

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
- **AKS** — the subscription's clusters; the resource group is filled in.
- **GKE** — the clusters in every location; the location is filled in, then how to reach it:

  ```console
  ? Reach it at
      its endpoint  the cluster's own (public, if it has one)
    ▸ private  its internal IP — from inside the VPC
      dns  its DNS-based endpoint — from anywhere IAM allows
  ```

- **Namespace** — kubectl's default for it (empty: `default`).
- **Name** — the cluster's, filled in.

Then: `mek -c orders-dev kube main-eks` ([Kubernetes](#kubernetes-eks-gke-aks)).

</details>

### Change or remove a tunnel, target or cluster

<details>
<summary>Show the example</summary>

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

</details>

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
| Azure | tenants | your Microsoft Entra login (no Azure role) | `az account tenant list` |
| Azure | subscriptions | any role on each subscription | `az account list` |
| Azure | Bastion hosts | `Microsoft.Network/bastionHosts/read` (Reader) | `az network bastion list` |
| Azure | VMs (targets, tunnels) | `Microsoft.Compute/virtualMachines/read` (Reader) | `az vm list` (running) |
| Azure | AKS clusters | `Microsoft.ContainerService/managedClusters/read` (Reader) | `az aks list` |

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
  the accounts your login can reach
  couldn't list them: aws sso list-accounts: exit status 255: An error occurred (UnauthorizedException) … — type the value
  ▏
  esc: back to the list
```

### How it saves

- **Only what you changed is rewritten:**
  - a target, cluster or tunnel is written in place; the rest of its context, comments
    included, stays as it was (a context written on one line, `{…}`, is rewritten as a block);
  - `mek ctx add` / `edit` write the context in place: the comments above it stay, the ones
    inside it don't;
  - the rest of the file — comments, order, other contexts, blank lines — stays as it was, and a
    symlinked file stays a symlink.
- **Nothing invalid is saved.** Before writing, the whole config is checked as it would be —
  every file, every cloud's rules (an account id of 12 digits, one region per portal, a
  tunnel's target that exists…). If it wouldn't load, the last step says why, starts on
  *Back*, and *Save* does nothing.
- **Atomically**, keeping an existing file's permissions; a new one gets `0600`
  (`~/.mek/config.yaml`) or `0644` (a project's files).
- Inside a git repository, a new `mek.local.yaml` is added to `.gitignore`.

### In scripts and CI

`add` and `edit` need a terminal; without one they say so. In CI, write the file (see the
[specification](#specification)), or remove with `--yes`:

```bash
mek ctx rm staging --yes
mek -c dev ctx tunnel rm db --yes     # a target, cluster or tunnel: -c too
```

## Kubernetes (EKS, GKE, AKS)

List the clusters under the context (see [the complete example](#a-complete-example)), then write the
context's kubeconfig:

```console
$ mek -c prod kube
/Users/you/.mek/kube/prod.yaml
✓ kubeconfig for prod: data, main
  mek -c prod kubectl get pods
  mek use -s prod   # then kubectl, k9s, ... in this shell
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
| `mek -c prod kube` | every cluster under `clusters:`; the first by name (alphabetical) is current |
| `mek -c prod kube main` | …with `main` as the current one |
| `mek -c prod kube --name other-eks [--region us-east-1]` | a cluster that isn't in the config (GCP: `--location`, `--endpoint`; Azure: `--resource-group`) |

Kube contexts are named `<context>/<cluster>` (`prod/main`), so they never clash across
accounts. To see clusters from several accounts — and your own `~/.kube/config` — in one
kubectl or K9s, list the files:

```bash
export KUBECONFIG=~/.kube/config:~/.mek/kube/prod.yaml:~/.mek/kube/gcp-prod.yaml
kubectl --context prod/main get nodes
kubectl --context gcp-prod/apps get nodes
```

How it stays keyless: the kubeconfig holds no credentials. Each request runs
`mek --context prod kube token …`, which gets a short-lived token from `aws eks get-token`,
`gke-gcloud-auth-plugin` or `kubelogin get-token --login azurecli` with that context's login — so the file is safe to keep, works in
K9s or FreeLens started from the Dock, and can never use another context's credentials.
`~/.kube/config` is never written.

For the guard, `kubectl get/describe/logs` are reads, `apply/scale/…` writes,
`delete/drain` (and `apply --prune`) destructive, `exec/attach/debug/proxy` a **shell**
(`proxy` serves the whole API) and `port-forward` a **tunnel**; `--dry-run` is a read.

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

**AKS** — a private cluster's API resolves inside its virtual network (or over VPN / peering);
be on that network. mek supports clusters with Microsoft Entra ID integration: a cluster with
only local accounts hands out long-lived certificates, so `mek kube` refuses it and says so.

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
● prod [protected] aws 444455556666 / Admin / ap-southeast-1
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
Forwarding from [::1]:8080 -> 80
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

## Shell on an instance

```bash
mek -c prod shell bastion                 # a name under the context's targets:
mek -c prod shell i-0abc1234def567890     # aws: an instance id
mek -c prod shell tag:Name=bastion        # aws: the one running instance with that tag
mek -c gcp-prod shell web                 # gcp: a target…
mek -c gcp-prod shell web-2 --zone asia-southeast1-b --user ops   # …or any VM (zone looked up if omitted)
mek -c az-prod shell jump                 # azure: a target…
mek -c az-prod shell vm-api --resource-group rg-app   # …or any VM, by name (resource group looked up if omitted) or resource id
```

AWS uses `aws ssm start-session` (needs session-manager-plugin and the SSM agent on the
instance); GCP uses `gcloud compute ssh --tunnel-through-iap`; Azure uses
`az network bastion ssh` through the context's Bastion (Standard SKU or above, native client
support on). No public IP or open port is needed. GCP keeps the context's SSH key and known
hosts under `~/.mek/ssh/<key>`, not `~/.ssh`.

On Azure the login is your Microsoft Entra ID account by default — no key at all; the VM needs
the Microsoft Entra ID login extension and you a *Virtual Machine User Login* role. A target
with `auth: ssh-key` and a `user` uses the context's key, `~/.mek/ssh/<key>/id_ed25519`
(mek says how to create it if it's missing).

Protected contexts ask first; readonly contexts block shells. The same applies to
`mek aws ssm start-session`, `mek gcloud compute ssh` and `mek az network bastion ssh`.

## Tunnels

A tunnel forwards a port on `127.0.0.1` to a host in a private network — a database, a
cache, an internal web UI — through the cloud's own session service.

```bash
mek -c prod tunnel db                     # named: localhost:15432 → the RDS endpoint, until Ctrl-C
psql -h localhost -p 15432 -U app appdb   # in another terminal

mek -c prod tunnel --via bastion --to prod-db.cluster-xyz.ap-southeast-1.rds.amazonaws.com:5432 --local 5432
mek -c prod tunnel --via i-0abc1234def567890 --to :8080      # a port on the instance itself
mek -c gcp-prod tunnel sql                                    # Cloud SQL (after: mek login gcp-prod --adc)
mek -c gcp-prod tunnel --cloudsql acme-prod:asia-southeast1:main --private-ip --local 15432
mek -c az-prod tunnel db                                      # Azure: through Bastion
mek -c az-prod tunnel --to 10.3.0.5:5432                      # Bastion connects to the IP itself
```

| Kind (config fields) | AWS | GCP | Azure |
|---|---|---|---|
| a port on the instance: `via` + `port` | SSM port forwarding | `gcloud compute start-iap-tunnel` | `az network bastion tunnel` to the VM |
| another host: `via` + `host` + `port` | SSM port forwarding to a remote host | `gcloud compute ssh` with `-L` — counts as a **shell** | — (use `host` alone) |
| an IP from Bastion: `host` (an IP) + `port` | — | — | `az network bastion tunnel` to the IP (IP-based connection) |
| Cloud SQL: `cloudsql` + `local_port` | — | Cloud SQL Auth Proxy (needs `mek login --adc`) | — |

The local port is `local_port`, or the remote port + 10000; mek checks it is free before
starting. A tunnel only moves bytes, so readonly contexts allow it (except a GCP tunnel to another
host, which is an ssh session: a shell); protected contexts ask.

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

## Configuration reference

### Where things live

Everything mek keeps is under `~/.mek`; `$MEK_HOME`
overrides it:

| Path | What | Who writes it |
|---|---|---|
| `config.yaml` | your contexts, on every project, and your settings | you, `mek init`, the wizard (`mek ctx …`) and `mek show-cmd on/off` |
| `state.yaml` | the saved current context | `mek use` |
| `aws/config` | AWS profiles `mek-<key>` for SSO contexts (`aws/config-<hash>` inside a project) | mek, on every run — don't edit |
| `gcloud/<key>/` | each GCP context's own gcloud config and login | gcloud |
| `azure/<key>/` | each Azure context's own az config and login (`AZURE_CONFIG_DIR`) | az |
| `kube/<key>.yaml` | each context's kubeconfig | `mek kube` |
| `ssh/<key>/` | GCP shells' SSH key and known hosts; an Azure context's key for `auth: ssh-key` targets | gcloud; you (Azure) |
| `tunnels/` | background tunnels' records, locks and logs | `mek tunnel` |
| `audit.jsonl` | the audit log (rotated at 10 MiB, 3 old files kept) | mek |

`<key>` is the context's name for contexts in `config.yaml`; a project's context adds a short
hash of its account (`dev-1a2b3c4d`), so two projects' `dev` never share a login. AWS SSO
tokens stay in the AWS CLI's own cache (`~/.aws/sso/cache`); mek stores no keys.

### Specification

`config.yaml` is YAML — and so are a project's `mek.yaml` and `mek.local.yaml`, with the same
keys except `settings:`, which only `~/.mek/config.yaml` may have. Every key mek reads is in
this one table; paths under a context are relative to `contexts.<name>`; a key that isn't in
it is an error. "—" means the key doesn't apply to that cloud: under `clusters`, `targets` and
`tunnels`, for `use_metadata`, for the Azure and Huawei keys, and on an Azure or Huawei
context, setting it is an error; an AWS key on a GCP context (or the other way round) is
ignored. Rules marked
*arg* mean no leading `-` and no control characters, because the value becomes a CLI argument.

| Key | Type | Required | Default | AWS | GCP | Azure | Huawei | Rules | Effect |
|---|---|---|---|---|---|---|---|---|---|
| `contexts` | map: *name* → context | no | no contexts | ✓ | ✓ | ✓ | ✓ | top-level key | every cloud target, by the name you type (`mek use <name>`, `-c <name>`) |
| `contexts.<name>` | map | — | — | ✓ | ✓ | ✓ | ✓ | name: `^[A-Za-z0-9][A-Za-z0-9._-]*$`; can't be empty | one context. The name becomes a directory and an AWS profile name (`mek-<name>`) |
| **Every context** | | | | | | | | | |
| `provider` | string | **yes** | — | `aws` | `gcp` | `azure` | `huawei` | one of the four | which cloud and CLI the context drives |
| `region` | string | no | none | ✓ | ✓ | ✓ | ✓ | no line breaks | AWS: `AWS_REGION` / `AWS_DEFAULT_REGION`, the profile's region, the default for `sso_region` and EKS clusters. GCP: `CLOUDSDK_COMPUTE_REGION`. Azure: `AZURE_DEFAULTS_LOCATION`, where new resources go. Huawei: `--cli-region` on each API command, `HW_REGION_NAME` |
| `protected` | bool | no | `false` | ✓ | ✓ | ✓ | ✓ | | changes ask first ([how the guard works](#how-the-guard-works)) |
| `readonly` | bool | no | `false` | ✓ | ✓ | ✓ | ✓ | | changes are blocked; with `protected` too, tunnels ask ([how the guard works](#how-the-guard-works)) |
| **AWS — IAM Identity Center (SSO)** | | | | | | | | | |
| `sso_start_url` | string (URL) | **yes** (SSO) | — | ✓ | — | — | — | no line breaks | the portal. Contexts with the same URL share one `[sso-session]`: one `mek login` covers them all |
| `sso_region` | string | **yes** (SSO), unless `region` is set | `region` | ✓ | — | — | — | equal across contexts sharing an `sso_start_url` | the region IAM Identity Center lives in |
| `account_id` | string | **yes** (SSO) | — | ✓ | — | — | — | exactly 12 digits; quote it | the account |
| `role` | string | **yes** (SSO) | — | ✓ | — | — | — | no line breaks | the permission set |
| **AWS — existing profile** | | | | | | | | | |
| `aws_profile` | string | **yes** (instead of the SSO keys) | — | ✓ | — | — | — | no line breaks | use this profile from your own `~/.aws/config` untouched (SSO, keys, `credential_process`, assume-role…); the SSO keys are then ignored |
| **GCP** | | | | | | | | | |
| `project` | string | **yes** | — | — | ✓ | — | — | no line breaks | the project ID: `CLOUDSDK_CORE_PROJECT`, `GOOGLE_CLOUD_PROJECT` |
| `account` | string (e-mail) | no | gcloud's active account | — | ✓ | — | — | no line breaks | `gcloud auth login <account>`, `CLOUDSDK_CORE_ACCOUNT` |
| `use_metadata` | bool | no | `false` | — | ✓ | — | — | AWS, Azure, Huawei: an error | on a GCP VM: let gcloud act as the VM's service account when the context has no login. Off, mek sets `CLOUDSDK_CORE_CHECK_GCE_METADATA=false`, so a context that isn't logged in fails instead of quietly using the machine's identity |
| **Azure** | | | | | | | | | |
| `tenant_id` | string | **yes** | — | — | — | ✓ | — | no line breaks | the Microsoft Entra tenant, by id or domain: `az login --tenant`, `AZURE_TENANT_ID`, `ARM_TENANT_ID` |
| `subscription_id` | string (GUID) | **yes** | — | — | — | ✓ | — | a GUID | `az account set`, `AZURE_SUBSCRIPTION_ID`, `ARM_SUBSCRIPTION_ID` |
| `bastion` | map | no (yes for shells and tunnels) | none | — | — | ✓ | — | `name` and `resource_group`, both *arg* | the Azure Bastion (Standard SKU or above, native client support on) that shells and tunnels go through |
| **Huawei Cloud** | | | | | | | | | |
| `hcloud_profile` | string | **yes** | — | — | — | — | ✓ | no line breaks (`mek login` checks it is in `~/.hcloud/config.json`) | the KooCLI profile (SSO, keys or an agency) — mek adds `--cli-profile` to each API command and sets `HW_PROFILE`; `mek login` runs `hcloud configure sso` for an SSO profile. `clusters`, `targets` and `tunnels` are errors on a Huawei context |
| **Kubernetes clusters** (`mek kube`, `mek kubectl`) | | | | | | | | | |
| `clusters` | map: *alias* → cluster | no | none | ✓ | ✓ | ✓ | — | alias: name rule, not `token` | the kube context is `<name>/<alias>`; `mek kube <alias>` makes it current |
| `clusters.<alias>.name` | string | **yes** | — | EKS name | GKE name | AKS name | — | *arg* | the cluster in the cloud (must be `ACTIVE` / `RUNNING`) |
| `clusters.<alias>.region` | string | AWS: yes, unless the context has `region` | the context's `region` | ✓ | — | — | — | *arg* | the EKS cluster's region |
| `clusters.<alias>.location` | string | GCP: **yes** | — | — | ✓ | — | — | *arg* | the GKE cluster's zone or region |
| `clusters.<alias>.resource_group` | string | Azure: **yes** | — | — | — | ✓ | — | *arg* | the AKS cluster's resource group |
| `clusters.<alias>.endpoint` | `private` / `dns` | no | the cluster's endpoint | — | ✓ | — | — | one of the two | which GKE endpoint the kubeconfig uses: `private` = its internal IP (from inside the VPC or over VPN), `dns` = its DNS-based endpoint (from anywhere IAM allows, if it allows external traffic) — see [private clusters](#private-clusters) |
| `clusters.<alias>.namespace` | string | no | `default` | ✓ | ✓ | ✓ | — | *arg* | the kube context's default namespace |
| **Hosts** (`mek shell`, tunnel hops) | | | | | | | | | |
| `targets` | map: *alias* → target | no | none | ✓ | ✓ | ✓ | — | alias: name rule, not `ls` `list` `stop` `logs` | hosts by short name: `mek shell <alias>`, `via: <alias>` |
| `targets.<alias>.instance` | string | **yes** | — | `i-…` / `mi-…` (8–17 hex digits), or `tag:Key=Value` | a VM name: `^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$` | a VM name, or its resource id | — | *arg* | the host; a tag must match exactly one running instance |
| `targets.<alias>.zone` | string | no | looked up (the one running VM of that name) | — | ✓ | — | — | *arg* | the VM's zone |
| `targets.<alias>.user` | string | no | gcloud's (your local user); Azure: your Entra ID login | — | ✓ | ✓ | — | *arg* | the SSH user (SSM picks the OS user on AWS; on Azure, for `auth: ssh-key`) |
| `targets.<alias>.resource_group` | string | no | looked up (the one VM of that name in the subscription) | — | — | ✓ | — | *arg* | the VM's resource group |
| `targets.<alias>.auth` | `aad` / `ssh-key` | no | `aad` | — | — | ✓ | — | `ssh-key` needs `user` | how the shell logs in: your Microsoft Entra ID account (no key; the VM needs the Entra ID login extension), or the context's key `~/.mek/ssh/<key>/id_ed25519` |
| `targets.<alias>.bastion` | map | no | the context's `bastion` | — | — | ✓ | — | like `bastion` | this VM's own Bastion |
| **Tunnels** (`mek tunnel`) | | | | | | | | | |
| `tunnels` | map: *alias* → tunnel | no | none | ✓ | ✓ | ✓ | — | alias: name rule, not `ls` `list` `stop` `logs` | named port forwards: `mek tunnel <alias>` |
| `tunnels.<alias>.via` | string | **yes**, except Cloud SQL and Azure's `host` | — | a target alias, instance id or `tag:Key=Value` | a target alias or VM name | a target alias, VM name or resource id | — | *arg* | the host the tunnel goes through. `via` + `port`: a port on that host (AWS SSM port forwarding; GCP `start-iap-tunnel`) |
| `tunnels.<alias>.host` | string | no | — | ✓ | ✓ | an IP, without `via` | — | *arg*; Azure: an IP | another host reached through `via` (an RDS endpoint, an internal IP): AWS SSM to a remote host; GCP `ssh -L` — which counts as a **shell**. On Azure, without `via`: an IP the context's Bastion connects to itself |
| `tunnels.<alias>.port` | int | **yes** with `via` or `host` | — | ✓ | ✓ | ✓ | — | 1–65535 | the remote port |
| `tunnels.<alias>.local_port` | int | **yes** for Cloud SQL | `port` + 10000 | ✓ | ✓ | ✓ | — | 1–65535; set it when `port` > 55535 | the port on `127.0.0.1`; `mek tunnel --local` overrides it |
| `tunnels.<alias>.cloudsql` | string | no | — | — | ✓ | — | — | *arg*; no `via`/`host`/`port` with it | a Cloud SQL instance, by its connection name `PROJECT:REGION:INSTANCE`, through the Cloud SQL Auth Proxy (needs `mek login --adc`) |
| `tunnels.<alias>.private_ip` | bool | no | `false` | — | ✓ | — | — | only with `cloudsql` | reach Cloud SQL over its private IP |
| **Settings** (`~/.mek/config.yaml` only) | | | | | | | | | |
| `settings` | map | no | none | ✓ | ✓ | ✓ | ✓ | top-level key; in a project's file, an error | how mek behaves for you |
| `settings.show_cmd` | bool | no | `false` | ✓ | ✓ | ✓ | ✓ | | print each command mek runs as typed yourself ([show-cmd](#seeing-the-native-command)); `mek show-cmd on` / `off` sets it |

Block style and flow style (`dev: {provider: aws, …}`) are both fine. Everything is checked
when mek loads the file, before any command runs; the first problem is reported with its
context and key, e.g. `context "prod": tunnels.db: port 0 is not a port (1-65535)`.

Keys mek doesn't know are errors, so a typo can't slip by — `protcted: true` stops mek with
`unknown field "protcted"` and the line it is on, instead of leaving `prod` unprotected.

### A complete example

Three contexts using every section:

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

  az-prod:
    provider: azure
    tenant_id: acme.onmicrosoft.com
    subscription_id: 55555555-6666-7777-8888-999999999999
    region: southeastasia
    protected: true
    bastion: {name: bas-hub, resource_group: rg-network}   # shells and tunnels go through it
    clusters:
      aks: {name: aks-prod, resource_group: rg-aks}
    targets:
      jump: {instance: vm-jump, resource_group: rg-app}      # your Entra ID login: no key
      ops:  {instance: vm-ops, auth: ssh-key, user: azureuser}
    tunnels:
      pg:  {host: 10.20.0.4, port: 5432}                     # Bastion connects to the IP itself
      web: {via: jump, port: 8080}                           # a port on the VM
```


### Environment variables

| Variable | Effect |
|---|---|
| `MEK_HOME` | where mek keeps everything (default `~/.mek`) |
| `MEK_NO_LOCAL` | `1`: ignore project config files, like `--no-local` |
| `MEK_CONTEXT` | this shell's context, over the saved one (set by `mek use -s`) |
| `MEK_SHOW_CMD` | `1` / `0`: print each command mek runs as typed yourself, or don't ([show-cmd](#seeing-the-native-command)) |
| `NO_COLOR` | plain output |

What mek gives the commands it runs: AWS gets `AWS_PROFILE`, `AWS_CONFIG_FILE` (SSO),
`AWS_REGION` and `AWS_DEFAULT_REGION`; GCP gets `CLOUDSDK_CONFIG`, `CLOUDSDK_CORE_PROJECT` and
friends, plus `GOOGLE_APPLICATION_CREDENTIALS` once `mek login --adc` created them; Azure gets
`AZURE_CONFIG_DIR`, `AZURE_EXTENSION_DIR`, `AZURE_TENANT_ID` / `AZURE_SUBSCRIPTION_ID` and
their `ARM_` twins for terraform, and `AZURE_DEFAULTS_LOCATION`; Huawei Cloud gets `HW_PROFILE`
and `HW_REGION_NAME` (and `--cli-profile` / `--cli-region` on each API command); contexts
with clusters (or a kubeconfig from `mek kube --name`) get `KUBECONFIG`. `--show-cmd` prints
exactly what a command gets. Credentials from your shell that would override the context
(`AWS_ACCESS_KEY_ID`, `GOOGLE_APPLICATION_CREDENTIALS`, `AZURE_CLIENT_SECRET`, `HW_SECRET_KEY`, …) are removed.

## How the guard works

**`protected` and `readonly` are mek's own settings, not the cloud's.** The clouds never see
them: mek checks each command on your machine before handing it to `aws`, `gcloud`, `az`,
`hcloud` or `kubectl`. They are there for mistakes — a delete typed in the prod terminal you took for
dev, a script run with the wrong context — not for someone who means harm.

| Setting | Effect |
|---|---|
| `protected: true` | writes, shells, tunnels and `mek exec` ask y/N; destructive commands need the context's name typed |
| `readonly: true` | writes, destructive commands, shells and `mek exec` are blocked; reads and tunnels run |

Commands are classified from their operation name, then the context decides (`mek help guard`
prints the same rules, from the code itself):

| Class | Examples | plain | `protected` | `readonly` |
|---|---|---|---|---|
| read | `describe-*`, `list-*`, `get-*` (aws), `list` / `describe` (gcloud), `list` / `show` (az), `List*` / `Show*` (hcloud), `s3 ls`, `kubectl get`/`logs`, `--dry-run` | run | run | run |
| write | `create-*`, `update-*`, `create` / `deploy`, `s3 cp`, `kubectl apply`/`scale`, unknown verbs | run | ask y/N | blocked |
| destructive | `delete-*`, `terminate-*`, `stop-*`, `delete` / `stop` (gcloud), `delete` / `stop` / `deallocate` (az), `Delete*` / `Stop*` (hcloud), `s3 rm` / `mv`, `sync --delete`, `kubectl delete`/`drain` | run | type the context name | blocked |
| unknown | anything via `mek exec` | run | ask y/N | blocked |
| shell | `mek shell`, `aws ssm start-session`, `gcloud compute ssh` / `sql connect`, `az network bastion ssh`, `kubectl exec`/`attach`/`debug`/`proxy` | run | ask y/N | blocked |
| tunnel | `mek tunnel`, SSM port forwarding, `gcloud compute start-iap-tunnel`, `az network bastion tunnel`, `kubectl port-forward` | run | ask y/N | run |

- **run** — runs at once. **ask y/N** — asks first. **type the context name** — you type the
  context's name to go on. **blocked** — refused, nothing is sent.
- **Answering ahead:** `-y` / `--yes` answers yes to writes, shells, tunnels and `mek exec`;
  `--confirm <context>` answers every question on that context, destructive commands included —
  the name must match.
- **No terminal to ask on** (CI, a cron job — no controlling terminal): a command that needs an
  answer doesn't run. A pipe is fine: mek asks on the terminal itself.
- **The audit log** records every decision — `allowed`, `confirmed`, `blocked`, `declined` —
  with the command, secret-looking values masked ([The audit log](#the-audit-log)).
- **What skips the guard:** running `aws` / `gcloud` / `az` / `kubectl` yourself, or after
  `mek env` / `mek use -s` exported a context into your shell. `mek exec` doesn't skip it: its
  commands count as *unknown*.
- **In a project:** a repository's `mek.yaml` or your `mek.local.yaml` can't switch
  `protected` or `readonly` off for a context you guard in `~/.mek/config.yaml`; they can add
  them ([Project config](#project-config-mekyaml-and-meklocalyaml)).

**This is a seatbelt, not a security boundary.** It prevents mistakes; real enforcement
belongs in IAM roles, SCPs, org policies and Azure RBAC. For an account that must stay read-only, give it a
read-only role in the cloud *and* `readonly: true` in mek: mek then says so before anything is
sent, instead of the cloud refusing afterwards.

## Command reference

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

`mek` on its own shows the same as `mek help`.

<details>
<summary>What mek help shows</summary>

```console
$ mek help
mek (เมฆ, "cloud") manages cloud contexts — an AWS account+role, a GCP
project, an Azure subscription or a Huawei Cloud profile — and runs the
official CLIs (aws, gcloud, az, hcloud) with the right credentials, plus a
safety guard and audit log for protected contexts.

Contexts come from ~/.mek/config.yaml and, inside a project, its mek.yaml
(the team's) and mek.local.yaml (yours): mek help config.
Add one by answering questions: mek ctx add (mek help wizard).
What protected and readonly do: mek help guard.

Usage:
  mek [command]

Contexts:
  ctx         Show the current context (use `mek ctx ls` to list all)
  init        Create ~/.mek/config.yaml (or a project's mek.yaml / mek.local.yaml) from a commented example
  login       Log in to a context (AWS IAM Identity Center / gcloud / az / hcloud)
  ui          Browse and use contexts, tunnels, shells, clusters, cloud resources and the audit log in one screen
  use         Switch the current context (every shell), or only this shell with -s

Run commands:
  aws         Run any AWS CLI command in the current context
  az          Run any Azure CLI command in the current context
  env         Print shell exports for a context: eval "$(mek env)"
  exec        Run any command (terraform, kubectl, scripts...) with the context's credentials
  gcloud      Run any Google Cloud CLI command in the current context
  hcloud      Run any Huawei Cloud KooCLI command in the current context
  kubectl     Run kubectl on the current context's clusters (see `mek kube`)

Access (shells, tunnels, Kubernetes):
  kube        Write a kubeconfig for the context's clusters (kubectl, K9s, FreeLens)
  shell       Open a shell on an instance (AWS SSM, GCP IAP + SSH, Azure Bastion)
  tunnel      Forward a local port to a private host (AWS SSM, GCP IAP, Cloud SQL, Azure Bastion)

Setup:
  completion  Print the shell completion script for zsh or bash
  doctor      Check config and required CLIs, with install hints
  help        Help about any command
  self-update Update mek to the latest release (installs via install.sh / manual download)
  shell-init  Set up mek use -s to switch this shell: eval "$(mek shell-init zsh)"
  show-cmd    Print each command mek runs as you would type it yourself (on, off, or say which)
  version     Print mek's version

Flags:
      --confirm string   answer every confirmation on that context, destructive ones included, by passing its name
  -c, --context string   context for this command (overrides the current context and $MEK_CONTEXT)
  -h, --help             help for mek
      --no-local         ignore project config files (mek.yaml, mek.local.yaml); also MEK_NO_LOCAL=1
      --no-show-cmd      don't print it, whatever MEK_SHOW_CMD or settings.show_cmd say
      --project string   read the project config (mek.yaml, mek.local.yaml) from this directory instead of looking up from here
      --show-cmd         print each command mek runs as you would type it yourself (see mek show-cmd)
  -y, --yes              answer yes to write confirmations on protected contexts

Additional help topics:
  mek config      Where contexts come from: ~/.mek/config.yaml, a project's mek.yaml and mek.local.yaml
  mek guard       What protected and readonly do, command by command
  mek wizard      mek ctx add / edit: the keys, what it looks up in the cloud, how it saves

Use "mek [command] --help" for more information about a command.
```

</details>

After `aws`, `gcloud`, `az`, `hcloud` or `kubectl`, help belongs to that CLI — `mek aws s3 help`,
`mek gcloud compute --help` and `mek kubectl get --help` show the real CLI's help (in the
current context). For mek's own description of those commands, use `mek help aws`.

### Commands

| Command | What it does |
|---|---|
| **Contexts** | |
| `mek init [--force]` | create `~/.mek/config.yaml` from a commented example |
| `mek init --project` / `--local` | create the project's `./mek.yaml` (for the team) / `./mek.local.yaml` (yours; added to `.gitignore`) |
| `mek ui` | contexts, tunnels, shells, clusters, cloud resources and the audit log in one terminal screen |
| `mek ctx ls` | list contexts (`*` = current; a project's with its file) |
| `mek ctx [--short]` | show the current context (`--short`: just the name, for prompts); `mek context` is the same command |
| `mek ctx add\|edit\|rm`, `mek ctx target\|cluster\|tunnel add\|edit\|rm` | the config wizard: contexts, and a context's targets, clusters and tunnels ([commands and how](#editing-config-with-the-wizard)) |
| `mek use <ctx>` | switch the saved context (every terminal without its own) |
| `mek use -s <ctx>` | switch only this terminal (and `KUBECONFIG`, when the context has clusters); needs `mek shell-init`, else `eval "$(mek use -s <ctx>)"` |
| `mek login [ctx] [--adc] [-- <flags>]` | `aws sso login` / `gcloud auth login` (+ application-default with `--adc`) / `az login` + `az account set` / `hcloud configure sso` (SSO profiles) |
| **Run commands** | |
| `mek aws …` / `mek gcloud …` / `mek az …` / `mek hcloud …` | the real CLI in the context, through the guard and audit log |
| `mek kubectl …` | kubectl on the context's clusters, through the guard and audit log |
| `mek exec -- <cmd> [args…]` | any command with the context's credentials |
| `eval "$(mek env [ctx])"` | export a context into this shell (bypasses the guard and audit) |
| **Access (shells, tunnels, Kubernetes)** | |
| `mek shell <target> [--zone Z] [--user U] [--resource-group G]` | a shell on an instance (SSM / IAP / Azure Bastion) |
| `mek tunnel <name> [--local N]` | forward a local port: a tunnel under the context's `tunnels:` |
| `mek tunnel --via T --to host:port [--zone Z] [--user U] [--local N]` / `--cloudsql C [--private-ip] --local N` | the same, without a config entry (GCP: `--zone`, `--user` for the hop) |
| `mek tunnel … -b [--no-restart] [--wait D]` | in the background: outlives the terminal, reconnects |
| `mek tunnel ls` / `stop <name\|id>… \| --all` / `logs <name\|id> [-f]` | list, stop or read tunnels |
| `mek kube [cluster] [--name N (--region R \| --location L [--endpoint private\|dns] \| --resource-group G)]` | write the context's kubeconfig |
| **Setup** | |
| `mek shell-init zsh\|bash` | print the shell integration that makes `mek use -s` work by itself |
| `mek show-cmd [on\|off]` | print each command mek runs as you would type it yourself: on, off, or say which ([how](#seeing-the-native-command)) |
| `mek doctor` | check the config and tools, with install hints |
| `mek help [command]` / `mek <command> --help` | how a command works, with examples (`-h` for short) |
| `mek version` | print the version |
| `mek self-update [--check]` | update a script-installed binary |
| `mek completion zsh\|bash` | shell completion |

### Global flags

Global flags go **before** the CLI name: `mek -c prod --yes aws ecs update-service …`

| Flag | Meaning |
|---|---|
| `-c, --context <ctx>` | use a context for this command |
| `-y, --yes` | answer yes to y/N questions — writes, shells, tunnels, `mek exec`, and `mek ctx … rm` (for scripts) |
| `--confirm <ctx>` | answer every confirmation on that context, destructive ones included; must name the context |
| `--project <dir>` | read the project config (`mek.yaml`, `mek.local.yaml`) from `dir`, from anywhere |
| `--no-local` | ignore project config files; `MEK_NO_LOCAL=1` does the same |
| `--show-cmd` / `--no-show-cmd` | print (or don't) each command mek runs as typed yourself, whatever `MEK_SHOW_CMD` or the setting say |

## What the cloud side needs

mek adds no agent, server or open port of its own; shells, tunnels and clusters need what the
official tools need. Ask whoever runs the account for:

| Feature | On the cloud side | For your role / user |
|---|---|---|
| AWS `mek shell`, `mek tunnel` | the SSM Agent running on the instance, an instance profile with `AmazonSSMManagedInstanceCore`, and a way out to the SSM endpoints (internet or VPC endpoints) | `ssm:StartSession` (plus `ec2:DescribeInstances` for `tag:` targets) |
| GCP `mek shell`, tunnels with `via:` | a firewall rule allowing `35.235.240.0/20` (IAP) to port 22, or to the forwarded port for tunnels without `host:` | IAP-secured Tunnel User, and OS Login or permission to add SSH keys (shells and `host:` tunnels) |
| Cloud SQL tunnels | the Cloud SQL Admin API enabled; with `private_ip: true`, a network path from your machine to the instance (VPN, peering) | Cloud SQL Client |
| EKS (`mek kube`, `mek kubectl`) | an access entry (or `aws-auth` mapping) for your role, and an endpoint you can reach | `eks:DescribeCluster` |
| GKE (`mek kube`, `mek kubectl`) | Kubernetes RBAC for your user, and an endpoint you can reach | Kubernetes Engine Cluster Viewer |
| Azure `mek shell`, `mek tunnel` | an Azure Bastion of the Standard SKU or above with native client support on; for Entra ID shells, the VM's Microsoft Entra ID login extension | Reader on the Bastion and the VM; Virtual Machine User Login (or Administrator Login) for Entra ID shells |
| AKS (`mek kube`, `mek kubectl`) | Microsoft Entra ID integration on the cluster, and an endpoint you can reach | Azure Kubernetes Service Cluster User, and Kubernetes or Azure RBAC for your user |
| The wizard's lists (`mek ctx add`, `mek ctx tunnel add`, …) | nothing | each optional — without one, you type the value: `ec2:DescribeInstances`, `rds:DescribeDBInstances`, `rds:DescribeDBClusters`, `eks:ListClusters`; `resourcemanager.projects.get` (any role on a project), `compute.instances.list`, `cloudsql.instances.list`, `container.clusters.list`; Reader for Azure's Bastion hosts, VMs and AKS clusters. AWS accounts and roles need only your IAM Identity Center login; Azure tenants and subscriptions, only your Entra ID login ([details](#what-the-wizard-looks-up)) |

## Troubleshooting

| You see | Do |
|---|---|
| `no config found — run mek init first` | `mek init`, then edit `~/.mek/config.yaml` (or work in a project with a `mek.yaml`) |
| `context "x" isn't defined here — it may be a project's (mek.yaml)` | it was chosen inside a project: go back there, `mek use <context>`, or `-c <context>` |
| `⚠ prod from ./mek.yaml replaces your prod in ~/.mek/config.yaml` | the repository defines a context with your context's name; rename one, or check the project's is the one you mean (your guard stays on) |
| `✗ ./mek.local.yaml is committed` (doctor) | `git rm --cached mek.local.yaml`, and list it in `.gitignore` |
| `no context selected — run mek use <context> or pass --context` | `mek use <context>`, or pass `-c <context>` |
| `unknown context "x"` | check the name with `mek ctx ls` |
| `unknown field "…"` (with the file and line) | a misspelt key in `config.yaml`, `mek.yaml` or `mek.local.yaml` — compare with the [specification](#specification) |
| `unknown provider "…"` | mek supports AWS, GCP, Azure and Huawei Cloud |
| `KooCLI profile "x" not found` / `no KooCLI config at …` | make the profile with `hcloud configure init --cli-profile=x`, or fix `hcloud_profile` |
| `az extension bastion and ssh is needed` | `az extension add --name bastion --name ssh`, once, without mek (extensions are shared by every context) |
| `aks cluster … uses local accounts` | mek needs a cluster with Microsoft Entra ID integration (local accounts are long-lived certificates) |
| `VM vm-1: 2 found` | pass `--resource-group`, or set `resource_group` on the target |
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
| `mek: ctx add asks questions and needs a terminal` | run it in a terminal; in CI or a script, edit the file |
| `not saved — ./mek.yaml would be invalid: …` | what the change would break: go Back and fix that answer (or fix the file) |
| anything else | `mek doctor`, and the audit log for what ran |

## Limitations

- Needs the official CLIs installed; passthrough speed equals the CLI's speed.
- mek can't grant more than your IAM role/permissions allow.
- AWS login supports IAM Identity Center (SSO) and existing profiles; SAML-only IdPs without Identity Center are not built in (use `aws_profile` with your existing tooling).
- mek supports AWS, GCP, Azure and Huawei Cloud.
- Huawei Cloud contexts run `hcloud` only: no `mek kube` (CCE hands out long-lived client
  certificates, and mek keeps no keys), no `mek shell` or `mek tunnel` (there is no session
  service like SSM, IAP or Bastion to go through), and no Resources tab (KooCLI fetches its API
  definitions online, so there is no model to build a catalog from). Their credentials are the
  KooCLI profile's: keys stay keys.
- Azure shells and tunnels go through Azure Bastion only (not Just-in-Time access or a public IP); AKS clusters need Microsoft Entra ID integration.
- Every kubectl request through mek's kubeconfig fetches a fresh token (about 0.5–1 s); mek never caches tokens.
- The kubeconfig records mek's path and `PATH` when it is written; run `mek kube` again after moving mek or the cloud CLIs.
- `mek shell` on GCP runs ssh with the context's own home directory, so your `~/.ssh/config` doesn't apply to it.
- A foreground tunnel ends when the cloud closes its session (SSM's idle timeout is 20 minutes by default); use `-b`, which reconnects. A reconnect breaks the connections open at that moment.
- If a tunnel's supervisor is killed with `kill -9`, `ls` shows it as dead; on macOS the tunnel's own process can outlive it (`stop` warns when the port is still taken).
- `mek ctx edit` rewrites the context it changes: comments inside it aren't kept (a target,
  cluster or tunnel change keeps them). The wizard's region lists are built into mek; type a
  region it doesn't list.
- The Homebrew cask is macOS-only; use `install.sh` or `go install` on Linux.
- macOS and Linux only; zsh and bash for `mek shell-init` and completion.

## Development

How the code fits together (the GoF patterns are named where they shape it):

- **Per cloud** (`internal/provider/aws*.go`, `gcp*.go`, `azure*.go`, `huawei.go`). Each defines a `Cloud`:
  an Abstract Factory for that cloud's Provider (an Adapter from a mek context to the
  official CLI), its command classifier (a Strategy from `internal/guard`), its config
  rules and the plugins it may need. A Provider also covers the access paths:
  `KubeProvider` (`mek kube`), `Sessioner` (`mek shell`) and `Tunneler` (`mek tunnel`;
  its `TunnelMethod` is a Factory Method for one Strategy per kind of tunnel). Adding a
  cloud = its provider files, an entry in `clouds` (`cloud.go`), its wizard questions and
  lookups (`internal/cli/wizard_<cloud>.go`, `internal/provider/discover.go`).
- **Every guarded command is a `runner.Invocation`** (a Command) passed through
  Decorators: `audit → guard → sessionStart → showCmd → terminal`, so blocked commands are
  audited too, sessions get start and end entries, and show-cmd prints only what runs. The terminal is `exec`, a
  foreground tunnel's registration around it, or a background tunnel's supervisor;
  tests swap it for a fake.
- **`internal/kube`** renders kubeconfigs (a Template Method; the cloud's describe call
  fills the steps). **`internal/tunnel`** is a Facade over tunnel records, flock-based
  liveness, port claims and the supervisor, with a State per tunnel deciding what
  listing and stopping it means.
- **`internal/proc`** is the process and file-lock plumbing — file locks, process groups,
  detaching and stop requests, with flock and signals — so `internal/tunnel` and
  `internal/audit` need almost none of their own.
- **The wizard:** `internal/wizard` is the question-at-a-time forms (Bubble Tea; a Builder for
  the form, a State per step); each cloud's questions are a `providerFlow` and each entry kind
  (target, cluster, tunnel) an `entryKind` (Strategies, `internal/cli/wizard*.go`); and
  `config.Editor` (`internal/config/edit.go`, a Facade) changes one context or entry in a file
  and leaves the rest of it as it was.
- **`internal/tui`** is `mek ui` (Bubble Tea): each tab and overlay is a State handling its
  own keys, and every key that acts is an `action` (a Command) holding mek's own arguments,
  run in the terminal or in the background — the UI never talks to a cloud itself.
- **The resource catalog:** `tools/catalog` (`make catalog`, or `make catalog-docker` with the
  CLIs in mek's test image) reads the installed AWS CLI's command index and botocore models,
  gcloud's command tree and az's command loader, and writes
  `internal/catalog/{aws,gcp,azure}.json.gz` — for each service, the resources a list command shows
  with no arguments, the field that identifies one, and the commands that take only that
  (`tools/catalog/tuned.go` fixes the few the models get wrong). `internal/catalog` embeds
  them, decodes one only when the Resources tab opens, and turns a resource into each
  cloud's argv (a `Source` per cloud, a Strategy).
  Design notes: [docs/design/0001-tunnel-shell-kube.md](docs/design/0001-tunnel-shell-kube.md).

```bash
make             # list all targets
make check       # gofmt check + go vet + unit tests (race) — run before pushing
make test-all    # check + integration + e2e + contract + emulator (the SSO login and the wizard)
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
| Integration (`-tags integration`) | `test/integration` | the real binary against recording stub CLIs: env per cloud, leaked credentials removed, guard + audit, exit codes, signal forwarding, concurrent runs, login flows, kube/shell/tunnel commands, project config, `mek shell-init` in real bash and zsh, show-cmd on stderr only, and background tunnels with a stub that really listens (start → ls → stop, reconnecting, a `kill -9`ed supervisor, ten simultaneous starts) |
| E2E (`-tags e2e`) | `test/e2e` | release archives served over HTTP, installed by the real `install.sh` (including tampered / unlisted archives being refused), a new user's first session on AWS and GCP, the confirmation prompts (including shells and tunnels) answered on a real pseudo-terminal, a background tunnel outliving the terminal that started it, `mek ui` driven by keys (switch, start and stop a tunnel, open a shell, quit with the terminal restored), and the config wizard by arrow keys (accounts and roles from a stub portal, a team context copied into `mek.local.yaml`, `mek ui` opening it) |
| Contract (`-tags contract`) | `test/contract` | mek with the **real** `aws`, `gcloud`, `gke-gcloud-auth-plugin`, `az` (with its bastion and ssh extensions) and `kubectl`, offline: every Azure Bastion and AKS command mek builds parsed by the real `az`; each CLI reads the config, directories, kubeconfigs and session commands mek hands it, against local fake APIs where one is needed; the wizard's lookups (`--query` / `--format` expressions and the portal token in a file), show-cmd's printed commands run in `sh`, and the resource catalog's commands (every one for the popular AWS resources parsed by the real `aws`; an EC2 and a Compute Engine instance listed, described and stopped). A missing CLI is skipped; `MEK_CONTRACT_REQUIRE=1` makes it fail. `sh test/contract/install-clis.sh` installs the CLIs on Debian/Ubuntu |
| Emulator (`-tags emulator`) | `test/emulator` | the **full AWS IAM Identity Center login** with the real `aws` against the [Floci](https://floci.io) emulator in docker — `mek login` → `aws sso login` (device code) → approval → role credentials through the profile mek generates — then a real call, a readonly block and a confirmed delete with them; and **the config wizard** on a real terminal — `mek ctx add` listing that login's accounts and roles, `mek ctx tunnel/target/cluster add` listing the account's EC2 instances, RDS databases and EKS clusters — with show-cmd's output run by hand and the portal token kept out of the audit log; and **the Resources tab** of `mek ui` listing an EC2 instance, stopping and terminating it, and blocked on a readonly context. It starts its own container on a random local port (never an emulator you already run); a small test proxy adds the `x-amzn-ErrorType` header Floci 2.1.0 leaves out of pending-token responses. A missing docker or CLI is skipped; `MEK_EMULATOR_REQUIRE=1` makes it fail |

`make cover` merges unit coverage with coverage from the instrumented binary the
integration tests run, so `main()` and real process paths count too. Tests that
check permission errors need a non-root user.

### Everything in Docker

`test/docker/` is mek's own test environment, so you don't install the cloud CLIs
or start the emulator by hand: an image with Go and the real `aws`, `gcloud` (with
`gke-gcloud-auth-plugin`), `az` (with its bastion and ssh extensions), `hcloud` and `kubectl`,
plus the Floci AWS emulator.

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
