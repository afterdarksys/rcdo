# Accessible operations demonstration

This demonstration uses local synthetic fixtures. It does not contact AWS,
AliCloud, Docker, Spacelift, or AI providers. It never applies infrastructure.
A clean fixture result is not production certification.

## Build and run

From ads-missing-utils:

```sh
make dist/contextsnap dist/eventwhy
make -C ../rcdo build
python3 scripts/demo-accessible-operations.py
```

Use `--output /path/to/new-directory` to choose a retained evidence directory.
Otherwise the script prints its retained temporary directory. Alternative binaries
can be supplied with `--rcdo` and `--bin-dir`.

## Demonstration narrative

1. Read the AWS context. Account, principal, selected region, profile and source
   are explicit. Repeat for AliCloud. Show a wrong-account expectation blocking.
2. Four Docker events become two observations. Exit 137 is not labeled as OOM;
   a separate explicit Docker oom event is preserved as its own observation.
3. Start a review with an owner, impact statement and next action. Move to a
   finding, bookmark it and record an observation. Resume in a separate invocation.
4. Acknowledge that findings were read. The handoff still lists unresolved blockers.
5. Change the source bytes. Resume still shows the place and note but returns
   INCOMPLETE. Historical reading does not authorize new acknowledgement.
6. Open handoff.txt, stale-handoff.txt, acceptance-results.json and evidence.json.
   These are the concrete deliverables a teammate can independently inspect.

Suggested introduction: “These tools make the target, evidence, missing checks,
and handoff explicit. This is a workflow any engineer on the team can reproduce.”

## Live caller identity (explicit opt-in)

After configuring the normal provider CLI, collect an observation:

```sh
./dist/contextsnap --cloud aws --profile work --region us-east-1 --collect > context.json
# Or select your AliCloud profile and region:
./dist/contextsnap --cloud alicloud --profile work --region cn-hangzhou --collect > context.json
```

Use one command for the appropriate cloud. Neither command changes the global
profile. Keep collector exit 3 (partial) distinct from exit 0. Raw credentials
and upstream diagnostics are not retained. The CLI can use credentials normally
available through its profile; successful execution is not an authorization check
for subsequent cloud operations. Region is selected for collection, not inferred
from STS. CLI executables and the local evidence files remain trust dependencies.

Create a named expectation file, for example work-aws.yaml:

```yaml
schema_version: "1"
name: payments-staging
environment: staging
cloud: aws
profile: work
region: us-east-1
account: "YOUR_ACCOUNT_ID"
# principal: optional exact expected ARN
```

```sh
rcdo context --input context.json --expect work-aws.yaml --width 72
```

RCDO returns 0 for matching fresh evidence, 20 for mismatches, 30 for stale or
incomplete evidence, and 2 for invalid arguments/expectations. Default freshness
is five minutes. Supplied snapshots require their original observation timestamp
and remain labeled as supplied; normalization does not create a live verification.
The expectation file names a persistent context but does not switch your shell or
bind later AWS/AliCloud commands. Docker/workspace/Ansible/Spacelift context
acquisition remains later project work.

## Bounded Docker evidence

```sh
./dist/eventwhy --collect --context work --window 5m > events.json
rcdo watch --input events.json --environment staging --width 72
```

The collector uses a read-only finite historical query, not a continuous event
subscription. Docker retains limited history, so live queries report partial
coverage (exit 3). Preserve the artifact and let RCDO report INCOMPLETE (exit 30).
Do not interpret that status as a failed parser or suppress the history gap.
Offline input accepts Docker NDJSON; it is marked supplied evidence. A successful
parse only describes that artifact, not current health or complete event history.

Use `--resource EXACT_ID` to narrow the review. `--max-groups` limits displayed
findings, defaults to 20, and reports omitted matching groups as incomplete.
Groups are ranked by observed event severity. Source attributes beyond resource
ID, action, time and numeric exit code are discarded. Context labels are not
Docker daemon identity verification.

## Reading navigation and handoff

The existing `next` selects the first unacknowledged finding. New `forward` and
`back` move the reading cursor without acknowledging. `resume` and `repeat` show
the persisted cursor; `bookmark --name NAME` and `goto --name NAME` manage named
positions. `note --note TEXT` attaches a timestamped operator note to the current
finding (or `--id ID`). `action --note TEXT` records the next action.

```sh
rcdo review-session resume --session incident.json --width 72
rcdo review-session forward --session incident.json --width 72
rcdo review-session bookmark --session incident.json --name networking
rcdo review-session note --session incident.json --note "Check target registration."
rcdo handoff --session incident.json --width 72 > handoff.txt
```

Navigation and handoff return the underlying review status, including 20 for
blockers. Bookmark/note/action return 0 when saved. Stale evidence returns 30;
historical navigation and handoff remain readable but do not change saved state.
Notes/bookmarks/actions and acknowledgements refuse stale evidence. Multiple
session files keep different investigations separate. Files are single-operator
records; concurrent modification and signed approvals are not supported.

Schema-2 files remain readable. Cursor/notes/bookmarks are optional new fields.
Keep notes free of secrets; common assignment patterns are redacted, but arbitrary
prose cannot be guaranteed secret-free. Handoffs label operator statements and do
not infer remediation execution from suggestions or acknowledgement.

## Actual assistive-technology acceptance

Automated text checks verify width and basic formatting. They do not establish
screen-reader usability. Use the actual terminal, screen reader and magnifier to
record, without inventing results:

- Can the operator identify cloud, account and intended environment?
- Can the operator locate a blocker and the source evidence?
- Can they resume after an interruption without rereading the entire report?
- Can they distinguish historical evidence from current checks?
- Can another engineer follow the handoff and identify the next action?

Record completion, navigation obstacles and time relative to the existing
workflow. These are product usability measures, not measures of visual ability.

## Primary interface references

- [AWS caller identity](https://docs.aws.amazon.com/cli/latest/reference/sts/get-caller-identity.html)
- [AliCloud STS role identity](https://www.alibabacloud.com/help/en/ram/user-guide/assume-a-ram-role)
- [Docker event output and limited history](https://docs.docker.com/reference/cli/docker/system/events/)

## Execution and configuration milestone

See [the shared roadmap](../../ROADMAP.md) for all 17 workstreams. This milestone
adds `runreceipt` (Missing Utils), `receipt-review` and `config-walk` (RCDO), and
reviewed-source guards to `config-set` / `config-remove`.

```sh
runreceipt --label "version check" --execute --receipt version.json -- terraform version
rcdo receipt-review --input version.json
rcdo config-walk start --input settings.yaml --state navigation.json
rcdo config-walk child --state navigation.json
rcdo config-walk bookmark --name service --state navigation.json
rcdo config-walk show --state navigation.json
rcdo config-set --input settings.yaml --path service.port --value 8080
# Copy the Source SHA-256 from the preview into REVIEWED_SHA256 below.
rcdo config-set --input settings.yaml --path service.port --value 8080 --write --expect-sha256 REVIEWED_SHA256 --expect-value 80
```

Writes now require `--expect-sha256`. `--expect-value` accepts an existing scalar;
HCL expressions are not evaluated. Syntax validation is not provider validation.
The hash is checked again immediately before replacement, but this is optimistic
concurrency protection, not isolation from another writer racing the rename.
JSON/YAML/TOML rewrites may normalize formatting and remove comments.

Navigation supports parent, child, next, previous, find, goto and bookmark.
Use `--format json` for exact paths; text is wrapped and secret-aware. Bookmarks
identify paths, not list item identities. Removed paths produce incomplete status
and require explicit `goto`. Navigation state contains paths, not source values.

Receipts omit command arguments and output, but retain executable, label, host,
directory and argument fingerprints. Fingerprints do not encrypt guessable secrets.
Use private storage. Command output itself passes through unchanged. Timeouts stop
direct local children; descendant and remote work may remain. Never infer that a
retry is safe from a missing final receipt. No retry is automatic.

From the Missing Utils checkout, after building both tools:
`python3 scripts/demo-roadmap.py` runs offline acceptance scenarios without cloud
credentials or production actions. Assistive-technology pilot acceptance remains
open; automated tests do not certify screen-reader usability.
