# Accessible operations: the next feature batch

Each feature below has its own implementation commit and regression tests.
Read-only artifact workflows work offline; live collector completeness and actual
screen-reader, braille and magnification acceptance are separate milestones.
All schemas use explicit source identity, timestamps and coverage where applicable.

## LOG: investigate saved logs

```
rcdo log-read --input app.log --query error
rcdo log-read --input events.jsonl --syntax jsonl --request req-42
rcdo log-read --input events.jsonl --syntax jsonl --since 2026-09-10T12:00:00Z
rcdo log-read start --input app.log --query error --state log-reading.json
rcdo log-read next --state log-reading.json --context 2
rcdo log-read bookmark --state log-reading.json --name database
rcdo log-read goto --state log-reading.json --name database
```

Text mode treats each physical line as an event. JSONL accepts one object per line:
`{"timestamp":"2026-09-10T12:00:00Z","level":"error","resource":"api","request_id":"req-42","message":"Connection failed"}`.
Only message is required; escaped newlines retain multiline event content.
Unknown fields/duplicate keys are rejected; normalize provider logs to this schema.
Time bounds require timezone-bearing RFC3339, are inclusive, and report unfilterable
events as incomplete (30). No cross-host clock synchronization is inferred.

Summary groups identical original messages/level/resource/request combinations,
ignoring timestamps. First/last source lines and counts remain explicit; JSON also
lists all occurrence lines. Group display limits announce omissions. Raw artifacts
are never rewritten. Common secrets and terminal controls are filtered in output;
this is not guaranteed detection of arbitrary secrets. Search operates on displayed
message text. Reading persists filters, exact source bytes, cursor and bookmarks.
Changed/rotated evidence returns 30 without changing state. Goto accepts a matched
`--line` or `--name`; show/next/previous include up to 20 adjacent events. Input is
bounded to 8 MiB, 100000 lines and 64 KiB per line. No live tail or heuristic
stack-trace joining occurs. All commands support `--format json` and `--width`.

## CTX: cross-tool context summary

`rcdo context-summary --input contexts.json --expect expected.json [--before old.json]`
compares all required contexts in one report. The expectation is
`{"schema_version":"1","name":"production","contexts":{"cloud":{"kind":"aws","values":{"account":"123","region":"us-east-1"}},"kube":{"kind":"kubernetes","values":{"cluster":"prod","namespace":"api"}}}}`.
The input is `{"schema_version":"1","complete":true,"contexts":{"cloud":{"kind":"aws","values":{"account":"123","region":"us-east-1"},"source":"sts adapter","outcome":"pass","collected_at":"2026-09-10T12:00:00Z"}}}`
with every required context supplied. Omit unavailable observations; do not invent
pass results. Missing contexts/fields, failed collectors or stale evidence are
incomplete. Kind/value mismatches and expired observed credentials block. Optional
`expires_at` records observable credential expiry; absence does not infer lifetime.

Kinds: aws/alicloud (account, region, profile, principal); kubernetes (cluster,
namespace, server, user); terraform/tofu (workspace, backend, backend_key,
engine_version); docker (endpoint, daemon_id); spacelift (account, stack, run,
commit); ansible (inventory_sha256, limit, user). Expectations determine required
fields; declare every field material to the operation. No arbitrary secret fields
are accepted. `--before` reports required-context identity changes; it is historical
comparison, not fresh identity evidence. Default age is 5m. This command compares
normalized artifacts; it does not acquire Kubernetes/Docker identity or modify
shell profiles. Existing context/Spacelift/IaC adapters remain separate collectors.

## REL: resource dependency navigation

`rcdo resource-walk --input graph.json --resource lb --depth 2`
reads dependencies; `--direction dependents` reverses edges. Each result has a
number, exact resource ID, type, account, region, depth, source and confidence.
Navigate further by supplying that ID as `--resource` against the same artifact.
The finding JSON can also use existing review-session bookmarks.

Schema: `{"schema_version":"1","complete":true,"collected_at":"2026-09-10T12:00:00Z","source":"collector","scopes":[{"account":"123","region":"us-east-1","complete":true,"outcome":"pass"}],"nodes":[{"id":"lb","type":"load-balancer","account":"123","region":"us-east-1"},{"id":"vm","type":"instance","account":"123","region":"us-east-1"}],"edges":[{"from":"lb","to":"vm","kind":"observed","source":"cloud API"}]}`.

An edge points from dependent to dependency. Kinds are observed, configuration,
or inferred; they are never relabelled as equivalent evidence. `--require-scope
123/us-east-1` is repeatable. Missing nodes/scopes, denied or partial pagination,
and stale graph evidence are incomplete. Producers set complete only after all
pages succeed; RCDO cannot independently attest an unsigned artifact. Cycles are
reported and traversal terminates. Depth is 1..10; input <=16 MiB, nodes <=10000,
edges <=50000. Default evidence age is 15m. This implements graph review/navigation,
not live paginated AWS/AliCloud collection or automatic relationship discovery.

## NAV: resumable incident workspace

```
rcdo incident start --state incident.json --title 'Database outage'
rcdo incident hypothesis --state incident.json --text 'Connection limit reached'
rcdo incident evidence --state incident.json --name logs --input database.log
rcdo incident action --state incident.json --status attempted --text 'Requested connection count'
rcdo incident next-action --state incident.json --text 'Compare connection limit'
rcdo incident resume --state incident.json
rcdo incident resolve --state incident.json --id 1 --status rejected
rcdo incident handoff --state incident.json
```

Notes, hypotheses (open/supported/rejected), next actions and attempted/completed
operator actions persist in a timestamped timeline. They are operator statements,
not execution receipts or proof of recovery. `next`/`previous` move a timeline
cursor. `show`/`resume`/`handoff` show recent entries, hypotheses, current evidence
and next action; `--limit` expands history and JSON exports all entries. No message
is sent. Named workspace files allow independent incidents; no global registry yet.

Evidence stores exact file bindings; replacing a label records a new version in
the timeline rather than deleting the prior attachment record. Changed/unavailable
current evidence stays visible and returns 30. Operators may still record notes
while evidence is stale; attaching refreshed evidence replaces its current binding.
Past versions are historical references and may no longer exist. Start refuses
existing state; updates use optimistic guarded atomic replacement. Common secret
patterns are redacted in operator text, not guaranteed arbitrary-secret detection.
Timeline limit is 10000 entries; text statements are bounded single-line labels.
