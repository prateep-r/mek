package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

// helpTopic is a page of `mek help <topic>`, listed under "Additional help
// topics" (a command with no Run).
func helpTopic(name, short, long string) *cobra.Command {
	return &cobra.Command{Use: name, Short: short, Long: long}
}

const configHelp = `Where mek reads contexts from, and what each file is for.

FILES — all three have the same shape: a "contexts:" map
  ./mek.local.yaml     yours, for this project; gitignored        mek init --local
  ./mek.yaml           the project's, for the team; committed     mek init --project
  ~/.mek/config.yaml   yours, for every project                   mek init

  Inside a project the files are read top first. A context comes whole from the
  first file that has it: fields are never mixed across files. To change one
  field of the team's context (your role, say), copy the whole context into
  mek.local.yaml and change it there.

  protected and readonly can't be switched off from above. If any lower file
  sets them on a context of the same name, it stays guarded; a file above can
  always add them. So a repository's mek.yaml can't remove a guard you set.
  (See: mek help guard)

THE PROJECT
  The project is the nearest directory, from where you are up, that holds
  mek.yaml or mek.local.yaml. The search stops at a repository's root (a .git)
  and never looks at your home directory or above it.

    --project DIR    use DIR's files, from anywhere (mek's kubeconfigs do this,
                     since kubectl runs "mek kube token" in any directory)
    --no-local       ignore the project's files; MEK_NO_LOCAL=1 does the same

  Outside the project its contexts don't exist. A context chosen there with
  "mek use" says so elsewhere; "mek use -s" (this shell only) suits project work.

WHERE A CONTEXT COMES FROM
  mek ctx and mek ctx ls show a project's context with its file, and every
  command on one says it:  ● orders-dev (./mek.yaml) aws 111122223333 / Developer
  When mek.yaml replaces a context of your own, mek warns each time it's used.
  mek doctor lists every file read, what each adds and replaces, and flags a
  committed mek.local.yaml. The audit log records the file ("config").

FOR TEAMS
  In mek.yaml, use IAM Identity Center fields (sso_start_url, sso_region,
  account_id, role): they work for everyone. aws_profile names a profile in one
  person's ~/.aws/config, so keep such contexts in mek.local.yaml or
  ~/.mek/config.yaml.

  A project's context keeps its gcloud login, SSH key, kubeconfig and AWS
  profile apart from a same-named context elsewhere — they are filed by name and
  account — so two projects' "dev" never mix, while the same "dev" in two clones
  shares one login.

EXAMPLE
  # mek.yaml — committed
  contexts:
    orders-dev:
      provider: aws
      sso_start_url: https://acme.awsapps.com/start
      sso_region: ap-southeast-1
      account_id: "111122223333"
      role: Developer
      tunnels: {db: {via: "tag:Name=orders-bastion", host: orders-db.internal, port: 5432}}
    orders-prod:
      provider: aws
      sso_start_url: https://acme.awsapps.com/start
      sso_region: ap-southeast-1
      account_id: "444455556666"
      role: Developer
      protected: true

  # mek.local.yaml — yours: the team's orders-dev with your role
  contexts:
    orders-dev:
      provider: aws
      sso_start_url: https://acme.awsapps.com/start
      sso_region: ap-southeast-1
      account_id: "111122223333"
      role: Admin
      tunnels: {db: {via: "tag:Name=orders-bastion", host: orders-db.internal, port: 5432}}

WHERE MEK KEEPS THINGS
  ~/.mek (%USERPROFILE%\.mek on Windows), or $MEK_HOME: config.yaml, the current
  context, the audit log, kubeconfigs, gcloud logins, SSH keys and tunnels. mek
  before v0.16 used ~/.config/mek: the first run of a newer one moves it there,
  leaving a link at the old path on macOS and Linux.`

// guardHelp is the guard's page; its table comes from guard.Decide itself.
func guardHelp() string {
	examples := map[guard.Class]string{
		guard.Read:        "describe-*, list-*, get-*, s3 ls, kubectl get/logs, --dry-run",
		guard.Write:       "create-*, update-*, s3 cp, deploy, kubectl apply/scale, unknown verbs",
		guard.Destructive: "delete-*, terminate-*, stop-*, s3 rm, sync --delete, kubectl delete/drain",
		guard.Unknown:     "anything through mek exec",
		guard.Shell:       "mek shell, aws ssm start-session, gcloud compute ssh, kubectl exec",
		guard.Tunnel:      "mek tunnel, port forwarding, kubectl port-forward/proxy",
	}
	word := map[guard.Decision]string{guard.Allow: "run", guard.Confirm: "ask y/N",
		guard.ConfirmTyped: "type the context name", guard.Block: "blocked"}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  CLASS\tPLAIN\tPROTECTED\tREADONLY\tEXAMPLES")
	for c := guard.Read; c <= guard.Tunnel; c++ {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", c, word[guard.Decide(&config.Context{}, c)],
			word[guard.Decide(&config.Context{Protected: true}, c)], word[guard.Decide(&config.Context{ReadOnly: true}, c)], examples[c])
	}
	w.Flush()
	return `protected and readonly are mek's own settings — not the cloud's. AWS and GCP never
see them: mek checks each command on your machine before handing it to aws,
gcloud or kubectl. They guard against mistakes — a delete typed in the prod
terminal you took for dev, a script run with the wrong context — not against
someone who means harm.

  protected: true   writes ask y/N; destructive commands need the context's name typed
  readonly: true    writes, destructive commands and shells are blocked

HOW COMMANDS ARE JUDGED
  mek classifies a command from its operation name, then the context decides:

` + b.String() + `
ANSWERING AHEAD
  -y, --yes                answers yes to writes (and shells, tunnels) on protected contexts
  --confirm <context>      confirms a destructive command (the name must match)
  Without a terminal to ask on (CI, a pipe), a command that needs an answer
  doesn't run.

THE AUDIT LOG
  Every decision is recorded in ~/.mek/audit.jsonl: allowed, confirmed, blocked
  or declined, with the command (secret-looking values masked).

WHAT THE GUARD DOESN'T COVER
  The cloud's own permissions decide what is possible: IAM roles, permission
  sets, SCPs, GCP IAM. A role that can delete prod can delete it without mek.
  Running aws / gcloud / kubectl yourself, or after "mek env" / "mek use -s",
  skips the guard; "mek exec" does not (its commands are "unknown").
  For an account that must stay read-only, use a read-only role in the cloud
  and readonly: true in mek: mek then says so before anything is sent.

IN A PROJECT
  A project's mek.yaml or mek.local.yaml can't switch protected or readonly off
  for a context you guard in ~/.mek/config.yaml (see: mek help config).`
}
