# Accessible engineering roadmap

Shared roadmap for ads-missing-utils and RCDO. The copy in ads-missing-utils is
authoritative; RCDO carries the same roadmap so either checkout is understandable.
This roadmap includes the original eight operations workstreams and all nine
additional recommendations. Existing detailed requirements remain in
[the operations project plan](docs/accessible-operations/PROJECT_PLAN.md).

## Objective and evidence standard

Make Linux, cloud, IaC and automation work independently navigable and verifiable
by blind and low-vision engineers. Demonstrate real task completion with artifacts
that any teammate can inspect. Accessibility is a product requirement, not a claim
that an engineer must compensate for workplace prejudice. Tooling cannot guarantee
that employers stop discrimination.

Done means documented behavior, meaningful failure tests, runnable artifacts and
explicit limitations. Local automated acceptance and real workplace/assistive-
technology acceptance are separate. Do not label an entire track complete when
only its core is implemented. Do not create nonfunctional command scaffolds.

## Architecture

Missing Utils: bounded collection, normalization, process evidence, comparisons,
portable machine-readable primitives and stable schemas.
RCDO: nonvisual navigation, previews, interpretation, expectations, review state,
runbook progression and handoffs. Both support ordinary files and pipes.
No mandatory AI or daemon. User-requested execution must be explicit; no automatic
retry of uncertain operations. Process exit is distinct from verified outcome.

## Original workstreams — current status

- CTX — Native acquisition now covers AWS, explicit Docker context/daemon,
  initialized IaC workspace/backend, Ansible inventory identity, and pinned
  Spacelift run/commit/approval observations. Remaining: broader backend/provider
  adapters, effective playbook execution scope and observed credential lifetimes.
- NAV — Finding, incident, runbook, state and task registry navigation are delivered,
  along with evidence comparisons, HCL references and identity-based list bookmarks.
  Remaining: cross-file/module references and nested-array identity traversal.
- EVT — Finite event review and live local-file announcements are delivered, with
  bounded queues, pause/resume and explicit source gaps. Direct remote stream
  reconnect and provider-specific event normalization remain open.
- REL — AWS EC2 attachment collection and evidence-labelled resource navigation
  are delivered. Broader service relationships and AliCloud collection remain open.
- IAC — Replacement, unknown/sensitive values, configuration dependencies and
  IAM/RAM/network scope comparisons are delivered. Effective permission simulation
  and broader native rule normalization remain open.
- ANS — Callback receipts, bounded live announcements, inventory fingerprints and
  host/task outcome review are delivered. Play-level resolved limits, effective
  remote user and broader Ansible-version compatibility remain open.
- SPC — Run/commit binding, approval-needed and most-recent observations are
  acquired through pinned read-only queries. Complete policy decision histories
  and authenticated account compatibility testing remain open.
- HND — Evidence-linked handoffs, local execution receipt review and configurable
  command audit logging are delivered. Durable remote receipts and signed
  provenance remain open; no automatic handoff sending is performed.

## Added workstreams and acceptance criteria

### EXE — Execution receipts and uncertain outcomes

Priority 1. Missing Utils records explicit argv execution and structured pipelines;
RCDO reviews each stage's process result, timeout, launch failure and missing final
receipt. Include host, directory, timestamps, executable and argument fingerprint.
Do not persist argument values or raw command output in the receipt by default.
Preserve an in-progress record if the recorder cannot finalize. No automatic retry.

Acceptance: an early failed stage cannot be hidden by a successful final stage;
preview launches nothing; output still flows to its normal streams; missing CLI,
timeout and incomplete receipt are distinguishable; process success does not
establish deployment success. Remote durable operation IDs/reconnection are later
scope, and a local ssh exit cannot certify remote side effects.

### CFG — Structural configuration navigation

Priority 1. Extend existing config explanation with persistent parent/child/sibling
navigation, search, exact paths/source locations, and named bookmarks. Do not
require indentation counting. Support JSON, YAML, TOML and HCL where existing
parsers can establish structure; reject ambiguous duplicate paths.

Acceptance: resuming in another invocation preserves the path; formatting-only
changes can retain a path but must announce the changed source version; removed
paths require explicit relocation; redacted values remain redacted; quoted keys
and array indices work. HCL reference navigation and identity-aware array bookmarks
are further scope rather than silently inferred.

### EDT — Edits bound to the reviewed version

Priority 1. Preview emits an exact-source fingerprint. Guarded writes require the
reviewed fingerprint and can assert an expected existing value. Refuse changed
bytes and recheck before replacement. Validate resulting syntax. Preserve default
preview behavior. Keep provider-schema validation distinct from syntax checking.

Acceptance: changing the file after preview prevents overwrite; expected-value
mismatch leaves bytes untouched; valid guarded edits work for each supported
syntax; no preview secrets; meaningful list identity selection follows as a
separate extension. Document the limit of optimistic checks against noncooperating
concurrent writers and do not claim filesystem-wide transactional isolation.

### FLE — Fleet comparison

Priority 2. Missing Utils accepts an explicit host manifest and bounded observations
of versions, service configuration, ports, certificates, storage and security
controls. RCDO summarizes exceptions and provides per-host evidence navigation.
Ansible inventory is an optional source, not required infrastructure.

Acceptance: nineteen matching hosts and one exception produce one primary
exception; unreachable/missing hosts remain separate from matches; baseline age,
platform differences, collection limits and sensitive inventory fields are visible.

### LOG — Investigation-oriented log navigation

Priority 2. Bounded indexed artifact reader with search context, next occurrence,
request/correlation ID, time-range filtering and evidence bookmarks. Keep exact
source references and counts of omitted lines. Never make deduplication destructive.

Acceptance: repeated errors collapse without losing access to original lines;
resume retains location; rotated/changed source is detected; timezone and possible
clock skew remain explicit; multiline events and very long lines have limits.

### OUT — Terminal compatibility layer

Priority 2. Shared behavior for plain, compact speech and compact braille-oriented
layouts; stable numbering, punctuation choices, escaped control sequences, no
cursor rewrites, separately accessible exact identifiers and status notifications.
Reuse the existing width-aware renderers rather than inventing incompatible flags.

Acceptance: no untrusted ANSI/OSC/control content reaches interactive rendering;
JSON preserves exact values; samples work with actual assistive technology;
compact output retains severity, uncertainty, target and next action.

### DOC — Operational accessibility doctor

Priority 2. Report installed adapter versions, expected CLIs, pager/color/prompt
settings, evidence-storage access and sample-output checks. All remediation is
previewed. Include an operator-driven speech/magnification check, not an automated
accessibility certification. Avoid reading or printing credential contents.

Acceptance: missing CLI, unexpected pager and unwritable evidence directory are
reported individually; absence of an optional tool is not a blanket failure;
checks are reproducible and distinguish observed settings from assumptions.

### RUN — Executable runbook progression

Priority 3. Versioned steps with required observations, expected results, next
steps and stop conditions. Begin with read-only checks and separately labeled
operator actions. Bind progress to input versions and independent outcome checks.
Later execution requires explicit authorization and no unsafe automatic retry.

Acceptance: unreachable required hosts prevent advancing a dependent verification;
step read, action attempted, action completed and outcome verified remain distinct;
resumption and handoff show unresolved stop conditions and original evidence.

### DEM — Repeatable engineering demonstration suite

Continuous. Scenario fixtures for wrong account, service misconfiguration,
incomplete Ansible rollout, hidden pipeline failure, IaC replacement, interrupted
remote operation and stale configuration editing. Each scenario includes expected
observations, a controlled resolution path and verification artifacts.

Acceptance: demos run without employer credentials or production changes; tests
assert behavior, not just printed wording; manager can inspect the same evidence;
actual usability results are recorded rather than invented. Measure completion
and navigation obstacles, not visual ability.

## Delivery order and remaining work

1. Current operations foundation: CTX/NAV/EVT/HND core — implemented, pilot pending.
2. Implemented core milestone: EXE/CFG/EDT plus DEM regression scenarios;
   remote receipts, reference traversal and identity-aware list selection remain open.
3. Investigation milestone: FLE/LOG/OUT/DOC and NAV evidence-version comparisons.
4. Infrastructure milestone: REL/IAC/ANS/SPC and remaining cross-tool CTX acquisition.
5. Operational continuity: RUN/continuous EVT, remote receipts and advanced HND.
6. Workplace pilot: actual screen reader/terminal/braille/magnification, nonproduction
   accounts, provider/CLI compatibility, packaging and reproducibility.

Milestones are dependency-ordered, not calendar promises. Every implementation
turn updates [delivery status](docs/accessible-operations/STATUS.md) with executable
checks and remaining scope. The roadmap is not complete until all acceptance
criteria, including the operator's workplace pilot, are met.

## RCDO delivery batch — September 2026

This checkout tracks the following RCDO features independently of upstream
collector work. Each feature is delivered in its own tested commit, with schemas,
commands and limitations in [feature workflows](docs/roadmap-features.md).
Offline artifact workflows do not imply complete live collection or workplace
assistive-technology acceptance. Those remain explicit follow-up work.

- [x] LOG: bounded log investigation, search, grouping and saved navigation.
- [x] CTX: one cross-tool context summary with required observations and expectations.
- [x] REL: evidence-labelled dependency navigation and missing-coverage detection.
- [x] NAV: resumable incident workspace with hypotheses, evidence and next actions.
- [x] RUN: runbook progression with separate attempts, completion and verification.
- [x] FLE: manifest-based fleet comparison leading with exceptions and unknown hosts.
- [x] OUT: plain/speech/braille report layouts and exact identifier spelling.
- [x] DOC/DEM: accessibility doctor and credential-free operational practice scenarios.

Already delivered in this checkout: document converters and Markdown viewer;
IaC replacement/sensitivity/comparison checks; normalized Spacelift policy,
dependency/drift review and core run collection. Earlier planned IAC/SPC entries
refer to remaining cross-tool acquisition and live integration, not absence of
these RCDO features.

## Live operations priority batch

One commit per section. Native adapters are tested with controlled CLI responses;
workplace credentials and assistive-technology acceptance remain separate.

- [x] COLLECT: AWS context, EC2 fleet inventory and attachment graph acquisition.
- [x] CHANGES: compare evidence snapshots without treating disappearance as recovery.
- [x] KUBE: investigate pods, deployments and correlated events.
- [x] COMMAND: explain generated commands, required inputs and shell quoting.

The subsequent operator and continuation batches deliver state navigation,
permission-scope explanations, network checks, task registry and local-file
monitoring. A local browser UI and direct remote stream monitoring remain open.

## Operator workflow batch

Each section has its own implementation commit and documented acceptance limits.

- [x] ANS-READ: opt-in Ansible receipts and host/task rollout reading.
- [x] STATE-NAV: redacted Terraform/OpenTofu state navigation and bookmarks.
- [x] NETWORK: bounded DNS, TCP, TLS and HTTP investigation.
- [x] TASKS: explicit registry and resume for incidents, reviews and runbooks.

## Six-section continuation

One commit per section; usage and limits are in [next-six.md](docs/next-six.md).

- [x] Audit filters, output inspection, completed-run gzip rotation and explicit retention pruning.
- [x] Local log/event/Ansible monitoring with pause/resume, bounded queues and source gaps.
- [x] IAM/RAM and normalized network-scope explanations, integrated with plan policy review.
- [x] Native Docker, IaC, Ansible and Spacelift context acquisition and provenance.
- [x] Current-file HCL references and identity-based array bookmarks.
- [x] Operator acceptance sessions, repeatable local scenarios and local native checks.
- [ ] Actual screen-reader/braille/magnification task observations.
- [ ] Authenticated nonproduction Docker/cloud/Spacelift compatibility pilot.

Configuration defaults for the newer commands and opt-in command/output auditing
are delivered. No automated test result substitutes for the two open pilot items.

## Scriptable policies

- [x] Local Rego evaluation through OPA, structured decisions and CI exit codes,
  restricted builtins, input/module hashes, and a Terraform/OpenTofu plan example.
  See [Rego usage](docs/rego.md).

## Policy workflow batch

- [x] Five composable Rego starter safeguards with passing/failing fixtures.
- [x] Fixture-based policy testing.
- [x] Before/after policy comparison on identical inputs.
- [x] Combined built-in IaC and Rego review with individual check evidence.

## Terraform-to-Ansible workflow and review integrity

- [x] Fix six audited false-clean/false-ready cases in report aggregation,
  truncated scanning, Ansible YAML inspection, change provenance, PR readiness
  and partial Ansible decomposition.
- [x] Trace explicit Terraform/OpenTofu outputs to Ansible host variables and
  identify extra-vars overrides, type mismatches and changed targets.
- [x] Check source bindings, producer identity, freshness, invocation inputs,
  callback scope and required post-run outcome evidence.
- [x] Support static inventory and saved native inventory snapshots, with
  dependency-aware invalidation of aggregated reports and review sessions.
- [x] Validate synthetic cases and actual localhost handoffs with Terraform,
  OpenTofu and Ansible.
- [ ] Complete workplace adapters, remaining producer provenance, expanded
  consumer tracing and operator acceptance described in [TODO.md](TODO.md).

Usage and evidence limits: [workflow guide](docs/terraform-ansible-workflow.md).
