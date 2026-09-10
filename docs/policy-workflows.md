# Scriptable policy workflows

## Starter packs

`examples/rego/starter` contains five composable rules: required Owner/Environment
tags, public exposure, approved regions, deletion/replacement/forgetting, and
production approval observations. Load `base.rego` with one or more rules:

```sh
rcdo rego-check --rego examples/rego/starter/base.rego \
  --rego examples/rego/starter/tags.rego \
  --rego examples/rego/starter/public.rego \
  --input examples/rego/starter/pass.json
```

Each rule has a `<rule>-fail.json` example; `pass.json` passes all five. These are
editable organization policy examples. The region allowlist is us-east-1/us-west-2;
production is the exact environment name `production`.

Input is a normalized observation with this shape:

```json
{"environment":"development","approved":false,"resources":[
  {"address":"example.bucket","tags":{"Owner":"platform","Environment":"development"},
   "region":"us-east-1","public":false,"actions":["create"]}
]}
```

Resource addresses must be unique. All shown fields are required. Missing or
malformed facts yield incomplete status. `public` and `approved` are supplied
observations; these packs do not discover effective exposure or verify approval
authenticity. Keep supporting evidence in your review. Empty resource lists are
valid and mean the supplied scope contains no resources. Tags/regions/public
checks apply to every listed resource, including deletions.

These normalized packs do not accept raw provider responses or raw Terraform
plans. For direct saved-plan deletion review use `examples/rego/terraform.rego`.
The [Rego reference](rego.md) documents execution limits and exit statuses.

## Policy fixture tests

`rego-test` evaluates JSON cases through the same restricted evaluator as
`rego-check`. It does not execute arbitrary test scripts or Rego `test_*` rules.

```sh
rcdo rego-test --suite examples/rego/starter/suite.json \
  --rego examples/rego/starter/base.rego \
  --rego examples/rego/starter/tags.rego \
  --rego examples/rego/starter/public.rego \
  --rego examples/rego/starter/regions.rego \
  --rego examples/rego/starter/deletion.rego \
  --rego examples/rego/starter/production.rego --format json
```

A suite is one JSON file with `schema_version: "1"` and 1–100 uniquely named cases:

```json
{"schema_version":"1","cases":[
  {"name":"approved change","input":{"ok":true},
   "expect":{"status":"clean","finding_ids":[]}}
]}
```

The example case above needs a policy that returns `allow: input.ok`. Expectations
use `clean`, `review`, or `blocked`. Optional `finding_ids` asserts the exact set
(order independent), including the `rego.` prefix and automatic `rego-denied`
finding. Omit it to assert only status. Denial that matches the fixture is a passing
test. Incomplete evaluations always make the suite incomplete; they cannot be
accepted as passing expectations.

Output is a standard RCDO report: all passing tests exit 0, mismatches exit 20,
incomplete evaluations exit 30, and invalid suite/arguments exit 2. Each case
records input/module hashes. Module files are snapshotted once for the whole suite.
`--timeout` is a total evaluation budget (default 30s, range 100ms–1m); remaining
cases are explicitly marked incomplete on exhaustion. Suites are limited to 16 MiB.
Normal audit logging captures the invocation and aggregate report. Safe defaults
for `rego-test` are format, width, environment, query, and timeout.

## Compare policy versions

```sh
rcdo rego-diff --before old.rego --after new.rego \
  --suite cases.json --format json
```

Repeat `--before` and `--after` to compose each version from multiple modules.
Both versions use the same `--query`, frozen suite inputs, and individually frozen
module sets. Suite syntax is shared with `rego-test`; expectations are optional
and do not affect comparison. Existing expectations must still be well formed.

Each changed case reports before/after `allow` and report status, with added,
removed, or changed finding details. Finding order alone is not a change.
Unchanged behavior exits 0 even when both policies deny: this command evaluates
policy differences, not deployment permission. Newly denied or newly blocking
cases exit 20. Other changes, including removed restrictions, exit 10 for review.
Undefined decisions, errors, or incomplete checks on either side exit 30 and are
never interpreted as allowed or unchanged. Invalid arguments/suites exit 2.

`--timeout` covers all before/after evaluations (default 30s, maximum 1m).
Reports retain case and module hashes. Safe configuration defaults match
`rego-test`; normal audit logging applies. This is a comparison over supplied
examples, not a proof that two policies are equivalent for all possible inputs.
Finding details can contain policy-supplied values; avoid returning secrets.

## Combined IaC and Rego review

```sh
rcdo policy-review --input plan.json \
  --rego examples/rego/terraform.rego --format json
```

Export saved-plan JSON with `tofu show -json saved.tfplan` or
`terraform show -json saved.tfplan`. `policy-review` reads JSON from a file or stdin
and never runs an apply, refresh, or provider operation. Both checks see the same
input bytes: existing IaC deletion/replacement, unknown values, drift, security,
and plan-completeness checks, followed by your Rego decision. Policies must accept
raw plan JSON; the normalized starter packs require a separate normalization step
and cannot be directly substituted here.

Optional `--limits limits.json` uses the existing `tofu-check` limits schema:

```json
{"max_deletes":0,"max_replacements":0,"critical_resources":["aws_s3_bucket.archive"]}
```

The combined standard RCDO report works with `report-read` and existing consumers.
`completed_checks` contains a `Check iac: status=...` and a
`Check rego: status=...` summary, with individual findings/incomplete counts.
Findings use `iac/` and `rego/` ID prefixes; incomplete explanations identify their
origin. Input, module, and optional limits hashes bind the report to the reviewed
artifacts. Neither check suppresses the other; no suppression flag is accepted.

Exit codes are 0 clean, 10 review, 20 blocked, 30 incomplete (takes precedence,
while retaining known blockers), and 2 invalid arguments/JSON/files. A structurally
invalid plan is an incomplete IaC check; Rego still evaluates valid JSON. Missing
OPA does not remove the IaC findings. A clean report describes these checks on this
artifact, not effective authorization or permission to deploy.

`--timeout` sets the evaluation budget after local input/module loading. Built-in
IaC review runs synchronously; OPA receives the remaining budget, or is marked
incomplete if no budget remains. It is not a hard interrupt for Go's built-in
analysis. Safe defaults are format, width, environment, query, and timeout.
Configured auditing records one invocation and the combined output, including
individual check summaries, subject to the configured redaction and output cap.

## Repeatable local verification

After `make build`, run `python3 scripts/policy-workflows-practice.py`. With OPA on
PATH, it checks passing/denied starter inputs, all six fixture cases, changed
policy decisions, and combined findings using the built executable. It uses a
temporary config and no cloud services; it does not establish workplace usability.
