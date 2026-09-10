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
