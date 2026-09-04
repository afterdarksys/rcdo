# Input Formats

All review commands are offline-first. They read standard input by default or a file supplied with `--input`. They do not fetch cloud or repository data themselves, so a missing credential or unavailable service cannot silently reduce review coverage.

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
deploy-review \
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
