# Shift brief

`rcdo shift` reads finding reports you already produced and speaks one change
window. It does not call a cloud. It does not prove a deployment is safe.

```sh
rcdo context --input snapshot.json --expect work.yaml --format json > context.json
rcdo shift --report context.json --report plan-report.json --layout speech --change-id CHG-42
rcdo shift --report context.json --report plan-report.json --format ticket --change-id CHG-42 --width 72
rcdo shift --report context.json --format json
```

Layouts are `plain`, `speech`, and `braille`. Braille defaults to 40 columns.
`--format ticket` is labeled text for a Jira or change comment. `--format json`
is schema `rcdo/shift/v1` and `deployment_proven` is always false.

Identity comes from a completed check written by `rcdo context`:

```text
identity label: cloud azure; subscription SUBSCRIPTION-GUID; principal NAME
```

`rcdo context` accepts cloud `aws`, `alicloud`, `azure`, and `gcp` in a
`missing-utils/contextsnap/v1` snapshot. The account field is an AWS or
AliCloud account, an Azure subscription GUID, or a Google Cloud project ID.
The region label does not prove where resources run. A supplied snapshot is
not a live identity check.

Live Azure metadata, still not an access-token request:

```sh
rcdo context-acquire --kind azure --native --name work \
  --subscription SUBSCRIPTION-GUID --expect-tenant TENANT-GUID > azure.json
```

The command runs `az account show` twice. CLI diagnostics are withheld. A
mismatch or a changed second read is incomplete. Compare the bundle with
`rcdo context-summary` before treating it as the identity in a shift brief.

`rcdo receipt-review` counts failed and unknown stages. When a stage failed,
it says the earlier failure remains. It always says that a later local success
does not make the deployment successful.

`rcdo permission-diff` adds a plain sentence when an added Allow names every
action, every resource, `iam:PassRole`, or `sts:AssumeRole`. That sentence is
a reading of the document. It is not an effective-access decision.

`rcdo see` runs `imgsee info` for images and `pdfsee` for PDFs. Pass `--text`
for a bounded PDF extract of 40 non-empty lines. Text inside images is not
read. A missing tool, terminal controls, or a truncated extract is incomplete.
Use `markdown-view` and `to-markdown` for text documents.

`rcdo ansible-scope --playbook site.yml --inventory inventory.json` expands a
literal host or group from `ansible-inventory --list` JSON. It ignores host
vars. A templated limit, a role, or a task-level user stays incomplete. The
effective remote user beyond the play text is always unproven.

`rcdo permission-check --document policy.json --action s3:GetObject --resource arn:aws:s3:::bucket/key`
matches one action and one resource in one document. An explicit Deny wins.
A condition or a complement on a matching statement is incomplete. An Allow
here is not effective access.

`rcdo spacelift-check` speaks each supplied policy history event in time order.
An earlier denial remains a blocker when the current decision passed. A snapshot
without history says that history was not supplied; it does not invent events.

`rcdo prebuild --plan plan.json --playbook site.yml` reads a saved
`terraform show -json` or `tofu show -json` plan and a playbook. It prints
create, update, delete, replace, and task lines for both. Nothing is applied.
An Ansible `state: absent` is a delete. A template or copy without a literal
absent state is `ensure`: the file was not compared with the host, so create
and update are not distinguished. Roles are not expanded.

```sh
rcdo prebuild --plan plan.json --playbook site.yml --color=never
rcdo prebuild --plan plan.json --playbook site.yml --color=always --color-flag vm=cyan --color-flag action-delete=bright-red
```

`--color=auto` adds color on a terminal unless `NO_COLOR` is set or `TERM` is
`dumb`. `--color=always` colors even a pipe. `--color=never` is plain text.
Classes are `vm`, `container`, `route`, `filter`, `lb`, `logs`, `fs`, `dir`,
`proc`, `devops`, and `conn`. Action colors use `action-create`,
`action-update`, `action-delete`, `action-replace`, `action-ensure`, and
`action-task`. The word and the `[class]` label are always present. Colors are
named, not raw terminal codes. `--color_flag` is the same option as
`--color-flag`.

Record a real assistive-technology pass with `rcdo pilot` tasks `shift-brief`,
`pipeline-stage-failure`, `change-ticket`, and `image-or-pdf`. Automated tests
do not establish screen-reader, braille, or magnification acceptance.
