# Input Formats

All review commands are offline-first. They read standard input by default or a file supplied with `--input`. They do not fetch cloud or repository data themselves, so a missing credential or unavailable service cannot silently reduce review coverage.

## Structured configuration explanation

`config-explain` accepts JSON, YAML, TOML, and native HCL. It detects syntax by
file extension first, then uses conservative content hints for standard input.
Pass `--syntax json`, `--syntax yaml`, `--syntax toml`, or `--syntax hcl` when
input is ambiguous.

Paths begin at `$`, use dots for ordinary keys, brackets for array indexes, and
quoted brackets for keys that contain punctuation. For example:

```text
$.services.api.ports[0]
$["team.example.com/owner"]
$.resource.aws_instance.web.instance_type
```

`--path services.api` accepts a shorthand root path. Secret-like paths such as
`password`, `token`, `api_key`, `private_key`, and `credentials` are redacted by
default in both text and JSON output. `--show-secrets` is intentionally explicit.

## Structured configuration comparison

`config-diff` requires `--before FILE` and `--after FILE`. One, but not both, may
be `-` for standard input. By default, a successful comparison exits 0 whether
or not changes exist. Pass `--check` to return the toolkit's review exit code 10
when changes are present.

```sh
rcdo config-diff \
  --before config/main.before.tf \
  --after config/main.tf \
  --path resource.aws_instance \
  --check
```

Use `--syntax` when both inputs share an extensionless format. For mixed or
ambiguous formats, use `--before-syntax` and `--after-syntax`. Object key order,
comments, and presentation whitespace do not create changes. Arrays are
compared by zero-based index. HCL expressions are whitespace-normalized but are
not evaluated, so two different expressions that produce the same runtime value
are still reported as changed.

Comparison uses the original values internally, then redacts the rendered
before-and-after values. A password or token rotation is therefore reported even
when both displayed values are `[REDACTED]`.

## Pull requests

Generate an inspection snapshot with GitHub CLI:

```sh
gh pr view 42 --json number,title,headRefOid,mergeable,reviewDecision,statusCheckRollup \
  | pr-manager inspect --expect-commit COMMIT_SHA
```

`pr-manager create` and `pr-manager update` print a preview. They call GitHub CLI only when `--execute` is explicitly supplied.

## OpenTofu

Generate plan JSON from a saved plan, not from speculative source parsing:

```sh
tofu plan -out=review.tfplan
tofu show -json review.tfplan | tofu-check --environment production
```

## Spacelift

`spacelift-check` accepts a JSON run snapshot and recursively looks for these equivalent fields:

- Stack: `stack_id`, `stackId`, or `stack`
- Commit: `commit_sha`, `commitSha`, `commit`, or `head_sha`
- State: `state` or `status`
- Run: `run_id`, `runId`, or `id`

Always pass `--expect-stack` and `--expect-commit` in deployment automation.

## Cloud identity

Normalize the active identity into JSON containing `cloud`, an account field, and `region`:

```json
{
  "cloud": "aws",
  "account_id": "111111111111",
  "region": "us-east-1"
}
```

AWS `get-caller-identity` output may use `Account`. Alicloud snapshots may use `AccountId`. The command searches nested objects for supported names.

## Combined deployment review

Create component reports with `--format json`, then combine them. Name every
report used in a required coverage check:

```sh
rcdo deploy-review \
  --report pull-request=pr.json \
  --report opentofu=tofu.json \
  --report spacelift=spacelift.json \
  --report ansible=ansible.json \
  --require pull-request \
  --require opentofu \
  --require spacelift \
  --require ansible \
  --require cloud-context
```

Unreadable, invalid, or absent required component reports make the combined
result `INCOMPLETE`. A bare `--report FILE` remains supported and derives the
component name from the filename, but explicit `COMPONENT=FILE` naming is safer.

## Change review manifest

`review-change` provides a repeatable coverage contract for the full toolchain:

```json
{
  "schema_version": "1",
  "change_id": "PR-42",
  "commit": "0123456789abcdef",
  "environment": "production",
  "required_components": [
    "pull-request",
    "github-actions",
    "opentofu",
    "spacelift",
    "ansible",
    "cloud-context",
    "runbook",
    "deployment-kit"
  ],
  "reports": {
    "pull-request": "reports/pr.json",
    "github-actions": "reports/gha.json",
    "opentofu": "reports/tofu.json",
    "spacelift": "reports/spacelift.json",
    "ansible": "reports/ansible.json",
    "cloud-context": "reports/cloud.json",
    "runbook": "reports/runbook.json",
    "deployment-kit": "reports/deployment-kit.json"
  }
}
```

Report paths are resolved relative to the manifest. Component names are
case-insensitive, and underscores normalize to hyphens. Findings whose
environment differs from the manifest also make the combined review
`INCOMPLETE`.
