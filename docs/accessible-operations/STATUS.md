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

## Remaining scope

CTX: Docker daemon identity, IaC workspace/backend, Ansible inventory and Spacelift
run binding; cross-tool acquisition provenance; credential lifetime observations.
NAV: full runbook steps, workspace registry and comparisons across evidence versions.
EVT: continuous stream, bounded announcement pause, reconnection and gap markers.
REL: paginated AWS/AliCloud relationship collection and navigable resource graph.
IAC: replacement causes, policy/rule semantic changes and configuration dependencies.
ANS: structured callback, resolved inventory/rollout scope and no_log validation.
SPC: live run navigation, supersession and actual platform policy/approval state.
HND: executed-change records, event references, signed provenance and optional AI wording.

These remain planned work under PROJECT_PLAN.md, not production capabilities.

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
