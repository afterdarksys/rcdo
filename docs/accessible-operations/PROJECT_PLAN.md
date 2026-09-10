# Accessible Senior DevOps Operations

Owner: Ryan Coleman. Repositories: ads-missing-utils and rcdo.

## Purpose

Enable blind and low-vision engineers to independently inspect, review, operate,
and explain infrastructure using structured evidence, keyboard navigation, speech,
and magnification. Reduce context reconstruction and repetitive output during
interruptions. Make professional work reproducible and reviewable by the team.
Tooling can remove operational barriers; it cannot guarantee that workplace
discrimination ends. Success is measured by completed engineering tasks, not vision.

## Architecture and delivery rules

Missing Utils owns bounded read-only collection, normalization, timestamps,
provenance, explicit collection gaps, and stable JSON schemas. RCDO owns accessible
navigation, expectation checking, review continuity, findings, and handoffs.
Keep both independently useful. No shell evaluation or mandatory AI dependency.
Human labels, observed facts, inferences, and authorization must remain distinct.
Every cloud adapter must document pagination, identity, region, permissions,
credential expiry limitations, timeouts, schema versions, and redaction boundaries.
Preview any future write action and preserve the platform's actual approval rules.

## Existing foundation

Implemented before this project: versioned findings and accessible renderers;
config explanation/diff/edit; Git and plan review; required-component manifests;
jsonprobe readiness adapter; evidence hashes; schema-2 review sessions preserving
blockers and rejecting changed report/artifact bytes, expired sessions, and changed
bound Git state. This project extends that foundation rather than replacing it.

## Release A: independently demonstrable operations

In progress. Deliver context verification for AWS/AliCloud, review navigation and
notes, bounded Docker event summaries, and deterministic evidence-linked handoffs.
Ship fixture-based acceptance tests and a manager demonstration. This release does
not claim full cloud inventory, continuous monitoring, effective IAM evaluation,
or production acceptance on a workplace's accounts.

## Workstream 1: verified work context (CTX)

- Missing Utils: collect AWS STS and AliCloud STS identity using explicit profile
  and region. Bound execution and output; discard credential-bearing diagnostics.
- RCDO: named JSON/YAML expectation files for service/environment, account, region,
  profile and optional principal. Validate freshness and compare collected identity.
- Next: Docker endpoint identity, Terraform/OpenTofu workspace/backend, Ansible
  inventory, Spacelift stack/run; cross-tool comparison and context-change notices.
- Next: credential lifetime where actually observable; otherwise report unknown.
- Acceptance: wrong account blocks; missing/expired evidence is incomplete;
  profile name alone never proves identity; region is labeled as selected for the
  collector, not inferred from STS. No global cloud configuration is changed.

## Workstream 2: interruption recovery (NAV)

- Extend review sessions with resume, repeat, back, bookmarks, finding-linked notes,
  an explicit next action, and multiple independent session files.
- Preserve position and notes when inputs become stale. Allow historical reading
  with unmistakable stale labeling; do not allow new acknowledgements.
- Next: task/runbook steps across a whole incident, named workspace registry, and
  since-checkpoint comparisons across fresh versions of the evidence.
- Acceptance: a second process invocation resumes the same item; moving through
  findings never acknowledges them; a changed source does not erase notes or turn
  a blocker into a pass; long paths and IDs wrap at the selected reading width.

## Workstream 3: quiet incident events (EVT)

- Missing Utils: normalize Docker JSON events into bounded groups by resource,
  action, and exit evidence, retaining counts and first/last timestamps.
- Support offline artifacts and explicit bounded collection from a named Docker
  context. Missing/malformed/truncated collection remains visible.
- RCDO: rank meaningful events, filter by resource, cap displayed groups, expose
  counts and gaps, and avoid terminal redraws. No diagnosis of OOM from exit 137.
- Next: continuous append-only stream, deduplication windows, reconnect/gap
  markers, pause announcements with bounded buffering, health/Spacelift adapters.
- Acceptance: repeated identical events become one count; different resources
  remain distinct; interruption/truncation cannot be a clean observation; no raw
  Docker labels, environment variables, or log payloads in normalized output.

## Workstream 4: resource relationships (REL)

Planned after Release A.
- Collect paginated AWS EC2/ELB/RDS/VPC/security-group/IAM and AliCloud
  ECS/load-balancer/RDS/VPC/VSwitch/security-group/RAM relationships.
- Version nodes and edges with source, account, region, time, and completeness.
- RCDO: parents, dependents, explain, changes and stable resource bookmarks.
- Separate observed links, configuration references, and inferred relationships.
- Acceptance: navigate load balancer to target to instance to security group;
  cycles terminate; pagination loss and denied regions are explicitly incomplete;
  deleted and renamed resources retain useful comparison identity.

## Workstream 5: infrastructure change consequences (IAC)

Implemented RCDO extension: replacement explanations, unknown/sensitive handling,
plan comparisons, impact limits, configuration references, native validation and
context expectations. See [workflow and limitations](../spacelift-iac.md).
Provider-specific interpretation and authenticated acquisition remain integration work.
- Explain replacement paths, lifecycle order, unknown values, moved resources,
  backend/workspace/provider changes, and config-derived dependents.
- Compare IAM/RAM principals, actions, resources and conditions; never claim
  effective authorization without evaluating the relevant policy layers.
- Add security-rule broadening/narrowing with IPv4/IPv6-aware comparison.
- Bind source revision, saved-plan hash, variables, lockfile, policy and collector
  identities through acquisition; current session hashes only bind after review.
- Acceptance: database replacement reason navigable; unknown-after values cannot
  become unchanged; sensitive markers survive all transforms; module references
  are explicitly configuration-derived and not asserted as live dependencies.

## Workstream 6: Ansible execution companion (ANS)

Planned after Release A.
- Inventory adapter explains resolved hosts, groups, limits, become and serial.
- Opt-in callback emits versioned task/host events and groups repeated failures.
- Distinguish completed, unsupported check mode, skipped, failed and unreachable.
- Preserve no_log and Vault boundaries; show variable provenance without values.
- Acceptance: unreachable hosts are not counted as successful; unsupported modules
  are not validated by silence; no_log fixtures emit no task result secrets;
  host-level drilldown and rollout batch remain available without full output.

## Workstream 7: actual Spacelift run tracking (SPC)

Implemented RCDO extension: explicit normalized snapshots, actual-run core
collection, policy/approval and dependency/drift checks, supplied plan binding,
run comparisons, bounded polling and command generation. Full live operational
collection and account-specific acceptance remain integration work.
- Read-only adapter captures stack/run IDs, run type, revision, phase, policy
  outcomes and outstanding requirements, with time and source provenance.
- Bind the reviewed plan to the actual run; show supersession by a newer run.
- Keep RCDO reading acknowledgements separate from Spacelift approvals.
- Acceptance: proposed/tracked runs clearly distinguished; superseded run cannot
  represent the latest deployment; failed collection cannot hide policy results;
  no local acknowledgement is translated into platform approval.

## Workstream 8: evidence-linked handoff (HND)

- Generate a deterministic plain-text handoff from a review session: change,
  current/stale status, supplied impact/owner, notes, next action, unresolved
  findings, incomplete checks, and report/artifact fingerprints.
- Distinguish user statements from verified evidence. Do not infer actions taken
  from recommended remediations. Do not auto-send messages.
- Next: incident event references, actual executed-change records, audience-specific
  summaries, optional evidence-constrained AI wording, signed provenance.
- Acceptance: every finding is traceable; acknowledged blockers remain unresolved;
  stale input is visible in the exported handoff; output is deterministic for
  unchanged state and includes no fabricated remediation or approval.

## Acceptance and evidence

1. Unit tests: missing/mismatched/stale context; both cloud response shapes;
   timeouts/output caps; malformed events; navigation persistence; stale history;
   width/control-character checks; deterministic handoff and unaltered blockers.
2. Integration: built binaries execute a reproducible credential-free demo with
   good context, wrong account, noisy events, interrupted review and a handoff.
3. Repository checks: go test/go vet; RCDO race suite; metadata generation check;
   build new commands and aliases. Test live adapters with mocked command runners.
4. Workplace pilot: Ryan chooses actual screen reader/terminal/magnifier. Measure
   time to identify target, recover position, locate blocker, explain evidence,
   and hand off. Record obstacles, not personal vision or disability metrics.
5. Controlled non-production pilot: verify real cloud identities and Docker
   context against independently known values; compare output to provider source.
   Only after this validation describe that adapter as workplace-accepted.

## Manager demonstration

Use a deterministic local scenario first; no production changes or credentials.
Identify a wrong account, inspect grouped container failures, resume an interrupted
review, show why a blocker remains, and produce a concise handoff with evidence.
Explain what each check establishes and what remains unknown. Invite the team to
use the same acceptance cases; do not frame the demo as proving a blind person
must outperform colleagues. See DEMO.md for reproducible commands and scoring.

## Sequencing and remaining delivery

Release A: CTX core + NAV core + EVT bounded core + HND core + demo.
Release B: REL inventory + IAC consequences + cross-tool context acquisition.
Release C: ANS structured execution + SPC live run tracking + continuous EVT.
Release D: actual assistive-technology pilot, packaging, workplace fixtures,
provenance verification, documentation and independent maintainability.

Each release must update STATUS.md with executable evidence, implemented scope,
remaining work, and environmental validation gaps. No calendar estimates until
actual workplace adapters and acceptance setup are known.

The expanded [shared roadmap](../../ROADMAP.md) includes all nine additional workstreams and delivery order.
