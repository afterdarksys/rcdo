# Accessible operations delivery status

Release A implementation: available locally, with automated validation and a
credential-free end-to-end demonstration. Workplace acceptance is pending.
The complete multi-release project is not finished.

## Available now

CTX core: Missing Utils contextsnap collects AWS/AliCloud STS identity through
bounded read-only CLI calls, or normalizes explicitly dated supplied input. RCDO
context compares named JSON/YAML expectations, freshness, account, cloud, selected
region, profile and optional principal. It never changes shell profiles.

NAV core: review-session resume/repeat/back/forward, named bookmarks/goto,
finding-linked notes, next-action statements, independent session files and
historical reading after staleness. Navigation does not acknowledge findings.

EVT bounded core: Missing Utils eventwhy groups finite Docker event artifacts or
bounded historical CLI queries. RCDO watch ranks groups, filters resources, caps
display and preserves missing coverage. Continuous monitoring is not implemented.

HND core: deterministic handoff includes original findings, recorded evidence and
checks, operator statements, unresolved blockers, coverage gaps and source hashes.
Reading completion does not establish remediation or deployment approval.

## Executable validation

- Missing Utils: full command/internal/integration suite; collector tests exercise
  AWS/AliCloud response shapes, discarded secrets/diagnostics, subprocess timeout,
  output limits, malformed/oversized events and repeated-event grouping.
- RCDO: full race-enabled suite, plus affected-package regression after handoff
  evidence additions. Tests cover wrong account, stale context, persisted position,
  notes/bookmarks, historical reading, deterministic handoffs, unchanged blockers,
  event display limits and 40-column wrapping.
- Static analysis: Go vet for both repositories.
- Capability matrix: generator validates all 48 Missing Utils command directories.
- Built-binary demo: both cloud fixtures, wrong accounts, grouped Docker events,
  interrupted review, unresolved acknowledged findings, changed evidence, and
  72-column output checks. Run scripts/demo-accessible-operations.py to reproduce
  the per-step acceptance-results.json and evidence manifest.

## Not yet verified in a workplace

No live employer AWS/AliCloud credentials, Docker daemon, Spacelift account or
Ansible inventory was used during implementation. Mocked collector tests establish
command construction and failure behavior, not account integration success.

Actual screen-reader, braille-display and magnification usability is unmeasured.
Use the checklist in DEMO.md with the operator's real setup. Neither output width
checks nor a sighted review can certify nonvisual task usability.

## Remaining scope — current

The older milestone entries below are delivery history. Current remaining scope:

- Direct remote event streams and provider reconnect handling beyond local-file monitoring.
- Broader cloud relationships, backend adapters and effective permission simulation.
- Resolved Ansible play limits/effective user and expanded version compatibility.
- Complete Spacelift policy decision history and authenticated account acceptance.
- Cross-file HCL references and nested-array identity bookmarks.
- Durable remote receipts, signed provenance and full terminal compatibility.
- Actual assistive-technology task observations and nonproduction cloud pilot.

See the six-section continuation at the end of this file for latest delivery.

## Roadmap milestone: execution and configuration

Implemented core: per-stage local receipts and RCDO review; persistent structural
configuration navigation; fingerprint-required writes and scalar expectations.
Acceptance fixtures cover early pipeline failure, timeouts, missing executables,
intent persistence failure, stale edits, missing navigation paths and ambiguous
JSON/YAML. See DEMO.md for the cross-tool scenario.

Remaining: remote durable receipts, reference traversal, identity-aware list
selection, full terminal compatibility and real assistive-technology pilot.
The full roadmap remains in progress; no workplace acceptance is claimed.

Validation: full Go test suites and go vet passed in both projects; the offline
cross-tool demo passed. Missing Utils receipt and RCDO toolkit race tests passed.

## Document conversion

Implemented: `to-markdown` converts DOCX/PDF with local Pandoc/Poppler and CSV/XLSX
with built-in readers. Supports linear records, bounded input, no-overwrite output
and explicit missing/OCR/formula-cache limitations. Tests, vet, build and native
DOCX/PDF conversion smoke checks passed. See ../document-conversion.md.
Actual assistive-technology usability remains unverified.

## Markdown reading

Implemented: `markdown-view` reads structured Markdown with section navigation,
search, labelled table rows, persistent bookmarks and exact-source freshness.
Full tests, vet, build and converter-to-viewer smoke checks passed. See
../markdown-viewer.md. Assistive-technology acceptance is still pending.

## log-read

Implemented bounded text/JSONL log grouping, source-line search, request/time filters, context and fingerprint-bound reading/bookmarks. Source text remains intact; missing timestamps during time filtering are incomplete.
Native gzip/bzip2 detection supports files, stdin, concatenated streams, and saved navigation. Decoding enforces an 8 MiB limit and rejects corrupt or incomplete streams before results or state are written.
Validation: focused regression tests; see ../roadmap-features.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## context-summary

Implemented required cross-tool context comparison, kind/value mismatches, freshness, credential expiry and before/after identity changes over normalized observations.
Validation: focused regression tests; see ../roadmap-features.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## resource-walk

Implemented numbered dependency/dependent traversal with explicit observed/configuration/inferred edges, scope coverage, stale evidence, missing nodes and cycle handling.
Validation: focused regression tests; see ../roadmap-features.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## incident

Implemented independent resumable incident workspaces with hypotheses, action records, next steps, bounded timelines and versioned evidence attachments. Stale current evidence remains visible with exit 30.
Validation: focused regression tests; see ../roadmap-features.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## runbook

Implemented ordered runbook reading, attempted/completed operator records and fresh runbook/step/target-bound verification before advancing. Failed or missing checks preserve stop conditions; instructions are never executed.
Validation: focused regression tests; see ../roadmap-features.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## fleet-check

Added manifest-based fleet comparison with explicit matching, differing, missing, unreachable, partial and stale evidence states. Platform-specific baselines and freshness prevent unsupported clean claims.
Validation: focused regression tests; see ../roadmap-features.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## report-read

Added a shared reader for versioned RCDO reports with plain, speech-oriented and 40-column braille-oriented layouts, exact identifier spelling, stable-ID selection and persistent full-report risk/coverage.
Validation: focused regression tests; see ../roadmap-features.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## doctor

Added local accessibility diagnostics and synthetic operational practice scenarios. Optional CLIs, environment settings, storage checks and operator reading samples report their individual scope; no automated accessibility certification is claimed.
Validation: focused regression tests; see ../roadmap-features.md for scope.
Live integration and assistive-technology acceptance are not asserted.

### September 2026 batch validation

- `go test ./...`: passed across all packages.
- `go vet ./...`: passed.
- `make build`: passed, including command aliases.
- `python3 scripts/accessible-practice.py`: 29 expected-exit checks passed;
  synthetic fixtures, numbered transcripts and results.json retained locally.
- Focused tests cover evidence changes, incomplete coverage, wrong identity,
  large integer comparison, terminal control removal and step verification gates.
- Workplace assistive-technology pilot: not run. Live cross-tool collector
  integration and authenticated outcome attestation remain follow-up work.

## collect

Added explicit AWS identity and paginated EC2 inventory/attachment acquisition feeding context, fleet and resource readers. Expected account gating and identity rechecks prevent silent cross-account collection; failed pages preserve incomplete coverage.
Validation: focused regression tests; see ../live-operations.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## changes

Added report and fleet snapshot comparisons with source fingerprints, persistent risk and explicit uncertainty. Finding disappearance is not recovery, missing hosts remain unknown and changed baselines cannot imply improvement.
Validation: focused regression tests; see ../live-operations.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## kube-explain

Added explicit-context Kubernetes collection and replay with pod/container readiness, restart reasons, deployment convergence/deadlines and UID-correlated warning events. Missing sections and stale evidence remain incomplete; saved snapshots omit environment and message bodies.
Validation: focused regression tests; see ../live-operations.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## Command builder explanations

Added `command-gen --explain` and `--shell posix|powershell`: argument metadata,
effect classification, required-input visibility and literal shell quoting.
Enhanced cloud generation withholds runnable commands for unresolved inputs.
Validation includes real POSIX and available PowerShell argument round trips;
provider execution remains unperformed and compatibility is not implied.
See ../live-operations.md for supported shells and scope.

### Live operations priority batch validation

- `go test ./...`: passed, including real POSIX and installed PowerShell literal
  argument round trips. Shell tests execute only an argument-echo helper.
- `go vet ./...` and `make build`: passed.
- `python3 scripts/live-operations-practice.py`: 14 expected-exit checks passed
  using synthetic AWS/kubectl adapters and the built CLI.
- No live cloud credentials, cluster access or provider mutations were used.
- Workplace provider compatibility and assistive-technology acceptance remain open.

## ansible-watch

Added bounded Ansible callback receipt reading with required host coverage, per-task check mode, no_log handling and distinct failed/unreachable/skipped/changed results. An opt-in callback records no result bodies or module arguments.
Validation: focused regression tests; see ../operator-workflows.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## state-walk

Added exact-address state show JSON navigation with parent/child/sibling traversal, search and saved bookmarks. Sensitivity masks and sensitive field names redact values; missing masks withhold resource values and changed source bytes block navigation updates.
Validation: focused regression tests; see ../operator-workflows.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## network-check

Added direct bounded DNS/TCP/TLS/HTTP HEAD investigation. Probes reuse one selected socket, verify certificates, avoid redirects/proxies/credentials, and report failed and unattempted layers separately.
Validation: focused regression tests; see ../operator-workflows.md for scope.
Live integration and assistive-technology acceptance are not asserted.

## tasks

Added an explicit named registry for incidents, reviews, runbooks and state navigation. Listing/resuming preserves cursor and next action, checks underlying evidence, detects replaced workflow identity and never advances or acknowledges work.
Validation: focused regression tests; see ../operator-workflows.md for scope.
Live integration and assistive-technology acceptance are not asserted.

### Operator workflow batch validation

- `go test ./...`, `go vet ./...` and `make build`: passed.
- Callback contract test: passed without Ansible or remote hosts.
- Installed Ansible localhost-only check-mode play: five callback records;
  task-level execution override, no_log and ignored failure verified. Its artifact
  was read by the built CLI with blocked status, preserving the ignored failure.
- `python3 scripts/operator-workflows-practice.py`: 22 expected-exit checks passed,
  including a localhost-only HTTP endpoint and all four registry workflow kinds.
- Network tests cover untrusted TLS, DNS failure, redirects and a silent-server
  deadline. State tests cover redaction, large integers and changed-source refusal.
- No cloud or remote infrastructure changes were performed. Broader provider,
  Ansible-version and assistive-technology workplace acceptance remain open.

## Configuration, audit and six-section continuation

Delivered configuration defaults for newer operator commands and opt-in JSONL
command auditing (identity, UTC timestamps, exit status, redacted bounded output).
The continuation adds audit investigation/rotation/retention, local-file live
announcements, IAM/RAM/network scope explanations, native context acquisition,
HCL reference and identity bookmark navigation, and operator acceptance sessions.
See [the continuation guide](../next-six.md) for executable commands and limits.

Operator sessions require actual setup/observation notes and bound evidence;
unrun tasks remain pending and changed evidence becomes stale. Automated fixtures
never certify device usability. Final validation:

- `go test ./...`: passed, including localhost network regressions.
- Focused race checks for all six sections: passed.
- `go vet ./...` and `make build`: passed.
- `python3 scripts/next-six-practice.py`: 18 local checks passed, including actual
  installed Ansible localhost-inventory resolution and an empty OpenTofu module
  initialized with a temporary local backend. The script isolates Ansible and
  OpenTofu environment overrides and records results in its artifact directory.
- No remote hosts, cloud resources, platform approvals or assistive-technology
  settings were changed. Actual AT and authenticated cloud/Spacelift/Docker pilot
  results remain pending; no operator pass was generated.

## Policy workflow batch

Five Rego starter safeguards and passing/failing normalized input examples are
available. Missing facts are incomplete; supplied approval/public flags are not
independent verification. Automated OPA tests exercise each pack. Usage and limits:
[policy workflows](../policy-workflows.md).

`rego-test` now runs bounded fixture suites with status/exact-ID assertions and
input/module hashes. Passing expected denials are distinct from incomplete
execution. Automated checks cover mismatches, malformed suites, missing decisions,
and the combined six-case starter suite.
