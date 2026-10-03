<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/mek-logo-dark.svg">
    <img src="assets/logo/mek-logo-light.svg" alt="mek" width="300">
  </picture>
</h1>

**mek** (เมฆ — Thai for *cloud*) logs you in, switches between cloud accounts
and runs the official CLIs with the right credentials — for AWS, GCP, Azure and
Huawei Cloud. Think *K9s/Lens, but for cloud accounts*.

```bash
mek login baas-uat          # SSO once, covers every account behind the same portal
mek use baas-uat            # switch context
mek aws s3 ls               # any aws command, in that context
mek gcloud compute instances list
mek az vm list
mek hcloud ECS ListServersDetails
mek exec -- terraform plan  # any tool, same credentials
```

- **Every CLI feature, day one** — `mek aws …` / `mek gcloud …` / `mek az …` / `mek hcloud …` pass everything through to the real CLI.
- **No long-lived keys** — AWS and Huawei Cloud use IAM Identity Center (SSO) through their CLIs' own token caches; GCP and Azure use an isolated CLI config per context.
- **Your files stay untouched** — mek writes its own AWS config (`~/.config/mek/aws/config`) instead of editing `~/.aws/config`, and never edits KooCLI profiles.
- **Prod guard** — `protected` contexts confirm writes and require typing the context name for destructive commands; `readonly` contexts block them.
- **Audit log** — every command is recorded in `~/.config/mek/audit.jsonl` with secrets masked (rotated at 10 MiB, 3 old files kept).

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

Then:

```bash
mek doctor   # checks aws / gcloud / session-manager-plugin / kubectl / k9s
mek init     # creates ~/.config/mek/config.yaml from a commented example
```

Update with `brew upgrade mek` (Homebrew) or `mek self-update` (script / manual installs).

### Requirements

| For | Needs |
|---|---|
| AWS contexts | [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) |
| GCP contexts | [Google Cloud CLI](https://cloud.google.com/sdk/docs/install) |
| Azure contexts | [Azure CLI](https://learn.microsoft.com/cli/azure/install-azure-cli) |
| Huawei Cloud contexts | [KooCLI (`hcloud`)](https://support.huaweicloud.com/intl/en-us/qs-hcli/hcli_02_003.html) |
| Upcoming `mek tunnel` / `mek shell` | [session-manager-plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html) |

## Configure

`~/.config/mek/config.yaml`:

```yaml
contexts:
  baas-uat:
    provider: aws
    sso_start_url: https://your-org.awsapps.com/start
    sso_region: ap-southeast-1
    account_id: "111122223333"
    role: PlatformEngineer
    region: ap-southeast-1

  baas-prod:
    provider: aws
    sso_start_url: https://your-org.awsapps.com/start   # same portal → same login
    sso_region: ap-southeast-1
    account_id: "444455556666"
    role: ReadOnlyAccess
    region: ap-southeast-1
    protected: true      # confirm writes, type the name for destructive commands
    readonly: true       # or block writes entirely

  legacy:
    provider: aws
    aws_profile: my-existing-profile   # reuse a profile from ~/.aws/config

  gcp-sandbox:
    provider: gcp
    project: my-sandbox
    region: asia-southeast1

  azure-dev:
    provider: azure
    tenant_id: your-org.onmicrosoft.com              # Entra tenant ID or domain
    subscription_id: 00000000-0000-0000-0000-000000000000
    region: southeastasia                            # default location

  huawei-prod:
    provider: huawei
    hcloud_profile: my-sso-profile    # a KooCLI profile (see below)
    region: ap-southeast-2
    protected: true
```

**Huawei Cloud:** KooCLI keeps its profiles in `~/.hcloud/config.json`, and
`hcloud configure set` also changes your current profile, so mek never edits it.
Create an SSO profile once, then point a context at it:

```bash
hcloud configure set --cli-profile=my-sso-profile --cli-mode=SSO --cli-region=ap-southeast-2 \
  --cli-sso-start-url=https://your-portal-url --cli-sso-region=ap-southeast-1 \
  --cli-sso-account-name=your-account --cli-sso-permission-set-name=ReadOnly
```

mek adds `--cli-profile` / `--cli-region` to each `mek hcloud …` command (KooCLI has
no environment variable for them) and sets `HW_PROFILE` / `HW_REGION_NAME` for terraform.
Like KooCLI, mek finds `~/.hcloud` through your account's home directory, not `$HOME`;
set `MEK_HCLOUD_CONFIG` to read KooCLI's profiles from somewhere else.

Rules checked on load: context names use letters, digits, `.`, `_` and `-`;
`account_id` is the 12-digit AWS account ID (quote it); SSO contexts need
`sso_region` (or `region`), and contexts sharing an `sso_start_url` must agree on it;
Azure `subscription_id` is a GUID.

> Never commit real account IDs or SSO URLs to a public repository. Share team configs from a private repo.

## Usage

| Command | What it does |
|---|---|
| `mek init` | create the config file |
| `mek ctx ls` | list contexts (`*` = current) |
| `mek use <ctx>` | switch the current context (saved, shared by every terminal) |
| `eval "$(mek use --shell <ctx>)"` | switch only this terminal |
| `mek ctx` / `mek ctx --short` | show the current context (`--short` for shell prompts) |
| `mek login [ctx] [--adc] [-- <flags>]` | `aws sso login` / `gcloud auth login` (+ application-default with `--adc`) / `az login` / `hcloud configure sso`; flags after `--` go to that login command |
| `mek aws …` / `mek gcloud …` / `mek az …` / `mek hcloud …` | run the CLI in the context, through the guard and audit log |
| `mek exec -- <cmd>` | run any command with the context's credentials |
| `eval "$(mek env [ctx])"` | export the context into your shell (bypasses guard/audit) |
| `mek doctor` | check config and tools, with install hints |
| `mek self-update [--check]` | update a script-installed binary |
| `mek completion zsh\|bash\|fish` | shell completion |

Global flags go **before** the CLI name: `mek -c baas-prod --yes aws ecs update-service …`

- `-c, --context <ctx>` — use a context for one command (also `$MEK_CONTEXT`)
- `-y, --yes` — accept write confirmations (for scripts)
- `--confirm <ctx>` — accept destructive confirmations non-interactively

### Several contexts at once

`mek use` saves one current context for all your terminals. To work on
different contexts side by side, pin a terminal with `eval "$(mek use --shell prod)"`
(it sets `$MEK_CONTEXT`, which wins over the saved context), or pass `-c` per command.
Runs on different contexts at the same time never mix credentials.

### Logging in without a browser (CI)

Flags after `--` go to the CLI's own login, which still lands in the context's
isolated config:

```bash
mek login dev    -- --no-browser                                  # aws sso login
mek login ci-gcp -- --cred-file="$GOOGLE_APPLICATION_CREDENTIALS"  # gcloud auth login
mek login ci-az  -- --service-principal -u "$APP_ID" -p "$SECRET"   # az login
```

mek removes `GOOGLE_APPLICATION_CREDENTIALS` and similar variables from the
commands it runs so they can't override the context; passing the file to
`--cred-file` as above logs the context in with it instead.

### Show the context in your prompt

```bash
# zsh
setopt PROMPT_SUBST
PROMPT='%F{cyan}$(mek ctx --short 2>/dev/null)%f %~ %# '
```

## How the guard works

Commands are classified from their operation name:

| Class | Examples | `protected` | `readonly` |
|---|---|---|---|
| read | `describe-*`, `list-*`, `get-*`, `show`, `List*`/`Show*` (hcloud), `s3 ls`, `--dry-run` | run | run |
| write | `create-*`, `update-*`, `Create*`/`Update*`, `s3 cp`, unknown verbs | confirm y/N | blocked |
| destructive | `delete-*`, `terminate-*`, `stop-*`, `deallocate`, `Delete*`/`BatchStop*`, `s3 rm`, `sync --delete` | type context name | blocked |
| unknown | anything via `mek exec` | confirm y/N | blocked |

**This is a seatbelt, not a security boundary.** It prevents mistakes; real
enforcement belongs in IAM roles, SCPs and org policies.

## Limitations

- Needs the official CLIs installed; passthrough speed equals the CLI's speed.
- mek can't grant more than your IAM role/permissions allow.
- AWS login supports IAM Identity Center (SSO) and existing profiles; SAML-only IdPs without Identity Center are not built in (use `aws_profile` with your existing tooling).
- Azure contexts each have their own `az login` (isolated config dirs), so several subscriptions in one tenant mean one login per context.
- Huawei Cloud contexts need a KooCLI profile you create yourself; `mek login` only logs in SSO profiles (AK/SK profiles are used as-is).
- KooCLI exits with status 0 even when a command fails, so `mek hcloud …` (and its audit entry) reports success then; check its output.
- The Homebrew cask is macOS-only; use `install.sh` or `go install` on Linux.
- Windows is not supported yet.

## Roadmap

1. ✅ Contexts, SSO login, passthrough, exec/env, guard, audit, doctor, self-update
2. `mek tunnel` (RDS/MSK via SSM), `mek shell`, `mek kube` (EKS kubeconfig with exec credentials for kubectl / K9s / FreeLens)
3. TUI with a resource catalog generated from AWS/GCP/Azure/Huawei API models
4. More GCP / Azure / Huawei features (Cloud SQL proxy, IAP, GKE, AKS, CCE)
5. Desktop GUI (Wails)

## Development

How the code fits together:

- **One file per cloud** (`internal/provider/<cloud>.go`). Each defines a `Cloud`: an
  Abstract Factory for that cloud's Provider (an Adapter from a mek context to the
  official CLI), its command classifier (a Strategy from `internal/guard`) and its
  config rules. Adding a cloud = one new file plus an entry in `clouds` (`cloud.go`).
- **Every guarded command is a `runner.Invocation`** (a Command) passed through
  Decorators: `audit → guard → exec`, so blocked commands are audited too, and
  tests swap `exec` for a fake.

```bash
make             # list all targets
make check       # gofmt check + go vet + unit tests (race) — run before pushing
make test-all    # check + integration + e2e + contract + emulator
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
| Integration (`-tags integration`) | `test/integration` | the real binary against recording stub CLIs: env per cloud, leaked credentials removed, guard + audit, exit codes, signal forwarding, concurrent runs, login flows |
| E2E (`-tags e2e`) | `test/e2e` | release archives served over HTTP, installed by the real `install.sh` (including tampered / unlisted archives being refused), a new user's first session on all four clouds, and the confirmation prompts answered on a real pseudo-terminal |
| Contract (`-tags contract`) | `test/contract` | mek with the **real** `aws`, `gcloud`, `az` and `hcloud`, offline: each CLI reads the config, directories and flags mek hands it. A missing CLI is skipped; `MEK_CONTRACT_REQUIRE=1` makes it fail. The Huawei test also needs `MEK_CONTRACT_HCLOUD=1`, since KooCLI writes your real `~/.hcloud` — run it only on a throwaway machine (CI does). `sh test/contract/install-clis.sh` installs the CLIs on Debian/Ubuntu |
| Emulator (`-tags emulator`) | `test/emulator` | the real CLIs making **real API calls** against [Floci](https://floci.io) emulators in docker — AWS, GCP (Cloud Storage) and Azure (Storage): a command the guard blocks never reaches the API, a confirmed one really changes state, API errors come back with the CLI's exit code. Each test starts its own container on a random local port (never an emulator you already run). Huawei Cloud has no emulator. AWS also runs the **full IAM Identity Center login** — `mek login` → `aws sso login` (device code) → approval → role credentials through the profile mek generates — with a small test proxy adding the `x-amzn-ErrorType` header Floci 2.1.0 leaves out of pending-token responses |

`make cover` merges unit coverage with coverage from the instrumented binary the
integration tests run, so `main()` and real process paths count too. Tests that
check permission errors need a non-root user.

### Everything in Docker

`test/docker/` is mek's own test environment, so you don't install four cloud CLIs
or start emulators by hand: an image with Go and the real `aws`, `gcloud`, `az` and
`hcloud`, plus Floci emulators for AWS, GCP and Azure.

```bash
make docker-test                          # every layer + coverage, nothing skipped
make docker-test TARGETS="test-contract"  # just some make targets
make docker-up                            # only the emulators, for local runs
make docker-down                          # remove mek's containers, network, cache volume
make docker-clean                         # ...and the mek-test image (~3.5 GB)
```

It is isolated from other projects on the same machine: compose project
`mek-test` with its own network and volume, everything labelled
`io.github.prateep-r.mek=test`, and emulator ports that aren't Floci's defaults —
`127.0.0.1:14566` (AWS), `14588` (GCP), `14577` (Azure) — so an emulator another
project runs on 4566 is never touched. With `make docker-up` running, point the
emulator tests at it:

```bash
MEK_FLOCI_AWS_URL=http://127.0.0.1:14566 MEK_FLOCI_GCP_URL=http://127.0.0.1:14588 \
MEK_FLOCI_AZ_URL=http://127.0.0.1:14577 make test-emulator
```

Releases: push a tag `vX.Y.Z` — GitHub Actions runs GoReleaser, publishes the
release archives and updates the Homebrew tap (needs the `HOMEBREW_TAP_TOKEN`
secret: a fine-grained PAT with *Contents: read/write* on `homebrew-tap` only).
