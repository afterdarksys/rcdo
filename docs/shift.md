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

Record a real assistive-technology pass with `rcdo pilot` tasks `shift-brief`,
`pipeline-stage-failure`, `change-ticket`, and `image-or-pdf`. Automated tests
do not establish screen-reader, braille, or magnification acceptance.
