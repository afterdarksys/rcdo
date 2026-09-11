# RCDO capability and workflow review — September 11, 2026

Reviewed commit: `19ab7df270861bc8a22c0d24c0cb3e8b78e6719d`.

**Implementation update:** the working tree now fixes R1–R6 and adds
`workflow-check`. The original findings below describe the reviewed commit.
Replay now reproduces zero unsafe successes. See
[the implemented workflow and validation](../terraform-ansible-workflow.md).

RCDO has a substantial accessible inspection foundation. Cross-tool execution
lineage is still missing, and six reproduced defects undermine clean/ready
results. This is a focused source and synthetic CLI review, not an exhaustive
security audit or authenticated workplace acceptance test. Implementation files
were not changed during this review.

## Confirmed findings

All six cases below reproduced against a fresh build. The reproduction script
uses only synthetic artifacts; it does not launch Terraform, Ansible, or cloud
commands. Each case returned exit 0 where uncertainty or hazards should remain.

### R1 — High: invalid component evidence satisfies required coverage

Location: `toolkit/operations.go`, `runDeployReview` and `appendReport`.

A file containing `{}` passed as `--report terraform=FILE --require terraform`
returns CLEAN. `appendReport` accepts arbitrary objects and ignores schema and
declared status; the caller marks the component present. A required review can
therefore disappear without making the result incomplete. The stricter
`appendVersionedReport` exists but is not used here.

Fix: validate the complete report contract before marking a component present;
make missing/contradictory evidence incomplete. Preserve legacy formats only
through an explicit adapter that cannot claim coverage it lacks.

### R2 — High: oversized lines silently truncate safety scanning

Location: `toolkit/rules.go`, `scanRules`.

A comment line exceeding the scanner's 4 MiB token limit followed by
`- hosts: all` returns CLEAN. The loop terminates on scanner failure but never
checks `scanner.Err()`. This shared scanner also serves other safety rules.

Fix: report scanning failures as incomplete and retain prior findings. Bound
input reading before allocating the entire source; `readInput` currently reads
all bytes before scanner limits apply.

### R3 — High: equivalent Ansible YAML evades existing hazard rules

Location: `toolkit/rules.go`, `ansibleRules`.

The control play with `hosts: all` and `ignore_errors: true` is BLOCKED with two
findings. Quoting the host as `hosts: "all"` and adding a comment after
`ignore_errors: true # continue` makes it CLEAN. In both versions, a task written
as `- shell: echo hello` also escapes the shell rule because the regex does not
allow a sequence marker.

Fix: inspect parsed YAML nodes and normalized scalar values, retaining source
locations. Cover short and fully qualified module names, sequence syntax,
comments, flow mappings, blocks, includes, and aliases. Unresolved semantics
should be explicitly incomplete rather than silently absent.

### R4 — High: clean reports have no verifiable change/environment binding

Location: `finding/finding.go`, `Report`; `toolkit/operations.go`,
`reviewChangeManifest` and `runReviewChange`.

A clean report produced with `ansible-check --environment staging` is accepted
by a production manifest with an arbitrary different commit. The environment
comparison only visits findings. A clean report has no finding environment and
the report contract has no structured commit, source digest, or acquisition
timestamp to verify. The manifest labels a change; it does not establish that
the component evidence belongs to that change.

Fix: introduce report-level provenance and compare it even for zero findings.
Include source binding, change/commit, environment, tool identity, acquisition
time, and explicit coverage. Legacy unbound evidence must remain visibly
unverified. Hashes establish local consistency, not authenticated execution.

### R5 — High: pending PR checks and unknown mergeability return CLEAN

Location: `toolkit/platform.go`, `runPRManager`.

A PR with `reviewDecision: APPROVED`, `mergeable: UNKNOWN`, and an IN_PROGRESS
check with a null conclusion returns CLEAN. The implementation looks for failure
strings and missing keys but does not establish that checks finished successfully
or that mergeability is known.

Fix: validate check structures, classify pending/unknown results explicitly, and
support required-check expectations. Absence of a failure is insufficient evidence
of completion.

### R6 — High: Ansible decomposition omits play sections without warning

Location: `toolkit/decompose.go`, `parseAnsibleResources`.

A play containing `pre_tasks`, `roles`, a supported VPC task, and `post_tasks`
returns `ready`, one generated step, and no unresolved items. Only `play["tasks"]`
is traversed. Prerequisites and verification can disappear from the manual
runbook. Generated commands are not executed, but the readiness claim is wrong.

Fix: detect every execution-bearing section, preserve ordering when supported,
and mark unsupported roles/includes/handlers/control flow incomplete. Never call
a partial translation ready.

## What is already useful

- Accessible structured reading: semantic configuration explanation/diff, saved
  navigation, Markdown/document readers, log reading, severity-ordered reports,
  speech/braille layouts, bookmarks and resumable review sessions.
- Infrastructure review: Terraform/OpenTofu plan actions, replacement order,
  unknown/sensitive markers, drift, output-change warnings, state navigation,
  policy rules, and Spacelift run/plan evidence.
- Operations: explicit context acquisition/comparison, AWS inventory collection,
  Kubernetes explanations, network diagnostics, incidents, runbooks, receipts,
  and fleet comparisons.
- Ansible: heuristic source review, optional native syntax checks, callback
  artifact reading with run/host expectations and no-log handling, inventory
  hostname fingerprints, and a limited manual cloud-command translator.
- Review controls: previewed edits with source hashes, combined reports, evidence
  manifests, stale session detection, and acknowledgement that preserves risk.

These are implemented capabilities, not proof that every edge case is correct.
Real screen-reader, braille-device, magnification, and authenticated workplace
acceptance remains pending in the project's status documents.

## Terraform outputs becoming Ansible inputs

The intended workflow needs distinct evidence at each boundary:

1. Review the intended plan, commit, backend/workspace, account, and region.
2. Record the actual apply outcome and acquire outputs from the corresponding
   state version. A planned value may still be unknown; a previous state export
   may be stale even when the file was copied recently.
3. Map each required output to inventory host/group variables or an extra-vars
   artifact, preserving types, sensitivity, and stable resource identities.
4. Resolve Ansible inventory and variable precedence for the exact invocation,
   including host limits, tags, configuration, and variable sources.
5. Bind callback results to that invocation and target mapping; then perform
   independent service verification before declaring the workflow complete.

For example, `output.web_hosts["blue"].private_ip` should be traceable to
`inventory.web.hosts.blue.ansible_host`; `output.database_endpoint` should be
traceable to `group_vars.web.db_host`, including any overriding extra variable.
The reader should explain producer, transformation, consumer, selected hosts,
overrides, freshness, and the next unresolved requirement one item at a time.

Terraform's `output` command reads root outputs from state; `-json` exposes
sensitive values, so exporting them must not imply they are safe for narration
or logs. See [HashiCorp's output contract](https://developer.hashicorp.com/terraform/cli/commands/output).
Ansible extra variables have highest variable precedence, so correct inventory
alone cannot establish the effective values. See
[Ansible precedence rules](https://docs.ansible.com/projects/ansible/latest/reference_appendices/general_precedence.html).

Current implementation boundaries:

- `reviewIAC` emits “Output changes may affect consumers” but has no consumer map.
- `acquireInventory` hashes inventory file bytes and sorted hostnames; it withholds
  host variables. It cannot distinguish the same host alias pointing at a new IP
  through a dynamic source. Its evidence explicitly excludes limits/reachability.
- The callback stores run UUID, mode, host alias, task identity, and outcome. It
  does not bind a run to output bytes, inventory addresses, extra-vars, or commit.
- `resource-walk` navigates supplied relationship graphs; it does not infer a
  Terraform-output-to-Ansible-variable graph from these artifacts.
- HCL reference navigation and limited decomposition help inspect individual
  files but do not evaluate modules, Jinja, or a complete deployment pipeline.
- `review-change` combines component evidence; it does not prove ordering,
  transformation correctness, or the identity of the data actually consumed.

## Recommended implementation order

First, fix R1–R6 with focused regressions so CLEAN and ready regain their intended
meaning. R4 requires a versioned evidence contract, not merely another finding
check.

Next, implement an offline workflow manifest and reader that bind supplied
artifacts and explicit output-to-input mappings. Start with root output JSON,
static JSON/YAML inventory, and JSON extra-vars. Reject missing outputs, null or
wrong types, duplicate identities, unknown values, stale bindings, changed
targets, and unaccounted overrides. Keep secret values out of reports and avoid
publishing hashes of individual low-entropy secrets.

Then add adapters for dynamic inventory, GitHub Actions/Spacelift artifact
handoffs, invocation receipts, and callback linkage. Distinguish configuration
evidence, observed inputs, process completion, and verified remote outcomes.
Arbitrary shell/Jinja transformations should stay unresolved until an explicit
adapter can explain and verify them.

Finally, exercise the whole path with a blind operator: identify an output's
consumer, explain why a host is selected, locate an override, recover a reading
position after interruption, and understand the exact stop condition. Measure
task success and navigation effort, not just line widths or absence of ANSI.

## Validation and replay

Passed: `go test ./...`, `go vet ./...`, fresh CLI build, and
`python3 integrations/ansible/test_callback.py` (one test). Existing Go tests
reported cached successes. Six new synthetic unsafe-success cases reproduced;
the existing passing suite does not cover these sufficiently.

Build a binary named rcdo, then run:

```sh
go build -o /tmp/rcdo ./cmd/rcdo
python3 docs/reviews/2026-09-11-reproduce.py /tmp/rcdo
```

The script exits 1 when it reproduces any unsafe success. It is an audit replay,
not a replacement for permanent behavioral regression tests; parser errors or
other nonzero outcomes need inspection before claiming a fix.
