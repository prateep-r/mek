# mek

**mek** (เมฆ — Thai for *cloud*) logs you in, switches between cloud accounts
and runs the official CLIs with the right credentials — for AWS, GCP and more
clouds later. Think *K9s/Lens, but for cloud accounts*.

```bash
mek login baas-uat          # SSO once, covers every account behind the same portal
mek use baas-uat            # switch context
mek aws s3 ls               # any aws command, in that context
mek gcloud compute instances list
mek exec -- terraform plan  # any tool, same credentials
```

- **Every CLI feature, day one** — `mek aws …` / `mek gcloud …` pass everything through to the real CLI.
- **No long-lived keys** — AWS uses IAM Identity Center (SSO) via the AWS CLI's own token cache; GCP uses an isolated gcloud config per context.
- **Your files stay untouched** — mek writes its own AWS config (`~/.config/mek/aws/config`) instead of editing `~/.aws/config`.
- **Prod guard** — `protected` contexts confirm writes and require typing the context name for destructive commands; `readonly` contexts block them.
- **Audit log** — every command is recorded in `~/.config/mek/audit.jsonl` with secrets masked.

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
```

> Never commit real account IDs or SSO URLs to a public repository. Share team configs from a private repo.

## Usage

| Command | What it does |
|---|---|
| `mek init` | create the config file |
| `mek ctx ls` | list contexts (`*` = current) |
| `mek use <ctx>` | switch the current context |
| `mek ctx` / `mek ctx --short` | show the current context (`--short` for shell prompts) |
| `mek login [ctx] [--adc]` | `aws sso login` / `gcloud auth login` (+ application-default with `--adc`) |
| `mek aws …` / `mek gcloud …` | run the CLI in the context, through the guard and audit log |
| `mek exec -- <cmd>` | run any command with the context's credentials |
| `eval "$(mek env [ctx])"` | export the context into your shell (bypasses guard/audit) |
| `mek doctor` | check config and tools, with install hints |
| `mek self-update [--check]` | update a script-installed binary |
| `mek completion zsh\|bash\|fish` | shell completion |

Global flags go **before** the CLI name: `mek -c baas-prod --yes aws ecs update-service …`

- `-c, --context <ctx>` — use a context for one command (also `$MEK_CONTEXT`)
- `-y, --yes` — accept write confirmations (for scripts)
- `--confirm <ctx>` — accept destructive confirmations non-interactively

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
| read | `describe-*`, `list-*`, `get-*`, `s3 ls`, `--dry-run` | run | run |
| write | `create-*`, `update-*`, `s3 cp`, unknown verbs | confirm y/N | blocked |
| destructive | `delete-*`, `terminate-*`, `stop-*`, `s3 rm`, `sync --delete` | type context name | blocked |
| unknown | anything via `mek exec` | confirm y/N | blocked |

**This is a seatbelt, not a security boundary.** It prevents mistakes; real
enforcement belongs in IAM roles, SCPs and org policies.

## Limitations

- Needs the official CLIs installed; passthrough speed equals the CLI's speed.
- mek can't grant more than your IAM role/permissions allow.
- AWS login supports IAM Identity Center (SSO) and existing profiles; SAML-only IdPs without Identity Center are not built in (use `aws_profile` with your existing tooling).
- The Homebrew cask is macOS-only; use `install.sh` or `go install` on Linux.
- Windows is not supported yet.

## Roadmap

1. ✅ Contexts, SSO login, passthrough, exec/env, guard, audit, doctor, self-update
2. `mek tunnel` (RDS/MSK via SSM), `mek shell`, `mek kube` (EKS kubeconfig with exec credentials for kubectl / K9s / FreeLens)
3. TUI with a resource catalog generated from AWS/GCP API models
4. More GCP features (Cloud SQL proxy, IAP, GKE)
5. Desktop GUI (Wails)

## Development

```bash
make             # list all targets
make check       # gofmt check + go vet + tests (race) — run before pushing
make run ARGS="doctor"
make install     # build with version info and copy to ~/.local/bin
make vuln        # govulncheck
make snapshot    # goreleaser build of all archives into dist/ (no publish)
```

Releases: push a tag `vX.Y.Z` — GitHub Actions runs GoReleaser, publishes the
release archives and updates the Homebrew tap (needs the `HOMEBREW_TAP_TOKEN`
secret: a fine-grained PAT with *Contents: read/write* on `homebrew-tap` only).
