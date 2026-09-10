# Spacelift and Terraform/OpenTofu workflows

These commands are offline-first, produce linear text or finding JSON, and never
approve or apply infrastructure. `command-gen` prints command examples only.
`iac-validate --native` invokes the selected engine; saved-plan conversion and
Spacelift collection are also explicit. Collectors time out after one minute and
retain at most 32 MiB stdout / 64 KiB stderr; exceeding a limit fails collection.

Review exit codes: 0 clean, 10 review, 20 blocked, 30 incomplete, 2 invalid input.
An informational finding also returns 10. INCOMPLETE takes precedence over BLOCKED
but blocking findings remain in the report. Filtering a plan's detail retains
blockers outside the filter and whole-plan completeness checks.

## Plan validation and explanation

```sh
rcdo tofu-check --engine terraform --plan review.tfplan --environment production
rcdo tofu-check --engine tofu --plan review.tfplan --limits limits.json
rcdo plan-explain --input plan.json --resource module.database --width 72
rcdo plan-explain --input plan.json --format json > plan-review.json
rcdo plan-diff --before reviewed-plan.json --after new-plan.json
```

`--engine` selects the binary for saved-plan conversion; JSON input is inspected
without launching either engine. `terraform_version` is retained as producer
version evidence, but cannot identify which engine produced the JSON: OpenTofu
uses the same field. Unknown major formats and missing resource-change evidence
remain incomplete. Unknown additive JSON fields are ignored for minor-version
compatibility. Unsupported action sequences are incomplete, not clean.

Plan checks cover:

- Replacement action order, reason and `replace_paths`; destructive impact limits.
- Unknown values, partial/deferred plans, failed and unknown check results.
- Moves, imports, state-removal actions, drift and output changes.
- New versus existing public exposure, protection disabling and network changes.
- IPv4/IPv6 CIDR broadening/narrowing when comparable prefixes are supplied.
- IAM/RAM policy statement additions/removals, preserving Effect, Principal,
  Action, Resource and Condition together. Added Deny clauses are not described
  as permission grants. This is structural policy review, not effective access
  evaluation across IAM/RAM policy layers.

```json
{
  "max_deletes": 2,
  "max_replacements": 0,
  "critical_resources": ["module.data.aws_db_instance.main"]
}
```

Deletion totals include replacements. Explicit critical addresses supplement the
existing stateful-resource heuristic. Limits apply to the entire plan.

Plan detail distinguishes absent, null, unknown, and changed sensitive values.
Both before/after sensitivity masks are honored before rendering, including
comparisons between plans. Output values and import IDs are withheld. Unmarked
secrets cannot be identified reliably; preserve native sensitivity metadata and
protect the original plan files. JSON exports from either engine can contain
plaintext secrets.

`plan-diff` compares by resource address plus deposed-object key, independent of
resource list ordering. It also compares outputs, replacement metadata and unknown
markers. The new plan is checked for hazards. Previous findings absent from the
new plan are labelled as absent, not independently resolved in infrastructure.
The prior plan's incomplete coverage is retained.

Use the existing review sessions for persistent navigation, bookmarks and notes:

```sh
rcdo review-session start --report plan-review.json --session session.json \
  --change-id PR-42 --commit FULL_COMMIT_SHA --artifact plan.json
rcdo review-session resume --session session.json
rcdo review-session bookmark --session session.json --name database
rcdo review-session note --session session.json --note "Verify restore procedure."
```

Informational resource details are navigable findings. Original report/artifact
fingerprints are checked by the session. An acknowledgement records reading only.

## Configuration, validation and context

```sh
rcdo iac-config-check --input main.tf
rcdo iac-validate --engine tofu --native --directory infrastructure
rcdo iac-validate --input validate.json
rcdo config-diff --before old.lock.hcl --after .terraform.lock.hcl --syntax hcl
rcdo iac-context --input context.json --expect expected-context.json
```

`iac-config-check` reads one HCL file, explains engine/provider constraints,
flags missing provider source/version and unpinned remote modules, and reports
lifecycle attributes and configuration-derived references. It does not resolve
modules, evaluate variables, or prove version compatibility. A missing constraint
in one file is not proof it is absent from the module. Use native validation in an
already initialized directory to check provider schemas and module consistency.

Native diagnostics preserve severity and source location. Diagnostic messages and
snippets are withheld because providers may embed unmarked secret values in them.
A nonzero validation process with valid error diagnostics is a blocked review;
malformed output, contradictory counts, and unexplained process failures are
incomplete. No automatic `init`, downloads, backend migration, plan or apply occur.

Context observation schema:

```json
{
  "schema_version": "1",
  "collected_at": "2026-09-10T12:00:00Z",
  "source": "organization read-only context adapter",
  "values": {
    "engine": "tofu",
    "engine_version": "1.10.0",
    "workspace": "production",
    "backend": "s3",
    "backend_key": "production/network.tfstate",
    "account": "111111111111",
    "region": "us-east-1"
  }
}
```

The expectation file is a subset of `values`, with exact string matches. At least
one expectation is required. Missing evidence and old/future timestamps are
incomplete. Default `--max-age` is 15m. Supply freshly acquired observations;
RCDO compares supplied evidence and does not switch workspace/backend or infer
cloud account identity from an environment label.

## Spacelift evidence and checks

```sh
rcdo spacelift-check --input run.json \
  --expect-account example --expect-stack production-network \
  --expect-run RUN_ID --expect-commit FULL_COMMIT_SHA --expect-type TRACKED \
  --expect-config expected-stack.json --plan-json plan.json

rcdo spacelift-check --stack production-network --run RUN_ID
rcdo spacelift-watch --stack production-network --run RUN_ID --samples 6 --interval 5s
rcdo spacelift-runs --input runs.json --type TRACKED --state FAILED
rcdo spacelift-diff --before previous-run.json --after current-run.json
```

The live collector uses `spacectl api` with a fixed read-only GraphQL query for the
specified `stack(id).run(id)`, including run ID, state, type and commit hash. It
requires a spacectl release providing the `api` command. `stack show --run` is not
used: the upstream command accepts that flag but its implementation queries stack
configuration, not the requested run. GraphQL errors, missing/null objects,
credential failures and unsupported schemas remain incomplete.

**Live coverage limitation:** the basic run query does not collect account,
latest-run identity, policy results, approvals, dependency pagination, drift or
plan artifact provenance. Live checks therefore return INCOMPLETE until an
organization adapter supplies a normalized snapshot containing those sections.
The account's GraphQL schema and permissions still require live integration
validation. No organization credentials were used in local tests.

`spacelift-watch` polls one explicit run, reports observed phase changes and stops
at a recognized terminal state or its sample limit. Each failed observation is a
continuity gap. Samples are bounded to 1..100, interval 1s..1m, total scheduled wait
at most 10m. Polling can miss intermediate transitions; terminal process results
do not establish application health. Text emits progress observations; JSON emits
one final finding report. Full policy coverage is still required for a clean check.

Normalized run snapshot schema (all times below are examples; refresh them):

```json
{
  "schema_version": "1",
  "account": "example",
  "stack_id": "production-network",
  "run_id": "run-123",
  "commit_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "run_type": "TRACKED",
  "state": "FINISHED",
  "collected_at": "2026-09-10T12:00:00Z",
  "source": "organization adapter version 1",
  "latest_run_id": "run-123",
  "policies": [{"id": "network-policy", "type": "PLAN", "decision": "allow"}],
  "approval": {"satisfied": true, "outstanding": []},
  "dependencies": [],
  "downstream": [],
  "drift": {
    "enabled": true,
    "last_success": "2026-09-10T12:00:00Z",
    "detected": false,
    "reconcile": false
  },
  "config": {
    "branch": "main",
    "project_root": "infrastructure/network",
    "engine": "tofu",
    "engine_version": "1.10.0",
    "worker_pool": "private-workers",
    "autodeploy": false,
    "policies": ["network-policy"],
    "contexts": ["production"],
    "workspace": "production",
    "backend": "spacelift"
  },
  "plan": {
    "json_sha256": "SHA256_OF_EXACT_DOWNLOADED_PLAN_JSON_BYTES",
    "run_id": "run-123",
    "commit_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "source": "artifact acquisition for run-123"
  }
}
```

Empty `policies`, `dependencies`, and `downstream` arrays assert that collection
completed and found none. **An adapter must omit these fields on failed or partial
collection; it must never substitute an empty array.** Null/absent sections mean
unknown, not successful. The snapshot's source identifies its supplier; this is
not cryptographic attestation. Keep snapshots within your trusted evidence path.

Dependencies have `stack_id`, `run_id`, `state`, `collected_at`, and `depends_on`
(an array of stack IDs). Missing referenced nodes and stale observations are
incomplete; cycles and upstream runs not FINISHED block. Downstream entries are
stack IDs; reported edges are supplied observations, not inferred live topology.

Expected stack configuration is a JSON subset of `config`. Supported keys are
branch, project_root, engine, engine_version, worker_pool, autodeploy, policies,
contexts, workspace, backend, repository and runner_image. Values match exactly;
array order is significant. Missing fields are incomplete; mismatches block.

Exact commit comparison replaces the previous prefix matching. Commit expectations
require a full 40- or 64-digit hexadecimal SHA; shortened observed SHAs are incomplete. Run state is
separate from stack state. Pending phases require review; unknown phases are
incomplete. A finished PROPOSED run establishes a preview, not an apply. Policy
IDs/types and decisions are required; outstanding platform approvals block.
`latest_run_id` must represent the latest run under the adapter's documented
selection scope; differing IDs block using this snapshot as the latest deployment.

`--plan-json` also reviews the plan for hazards, then hashes exact JSON bytes and compares the supplied acquisition hash,
run and commit. Reformatting the JSON changes its hash. RCDO does not manufacture
an acquisition binding merely because two files were passed together. Matching
supplied evidence does not prove its authenticity or that the binary applied plan
was identical; signed artifacts/binary acquisition remain integration work.

A run list uses `{"schema_version":"1","complete":true,"runs":[SNAPSHOT,...]}`.
At most 1000 observations are accepted. `complete:false`, missing arrays, or stale
selected observations are incomplete. The list is an inventory utility, not a
replacement for `spacelift-check`. `spacelift-diff` compares the two snapshots and
fully reviews the new one; historical timestamps in the old snapshot remain
usable for comparison. Cross-account/stack comparisons are explicitly flagged.

## Generate commands from configuration

```sh
rcdo command-gen --to aws --input main.tf --region us-east-1
rcdo command-gen --to alicloud --input alicloud.tf --region cn-hangzhou
rcdo command-gen --to aws --from ansible --input playbook.yml
rcdo command-gen --to terraform --input main.tf --action plan
rcdo command-gen --to tofu --input main.tf --action validate
rcdo command-gen --to spacelift --input stack-command.yaml --action logs
```

Spacelift command configuration accepts JSON, YAML or literal top-level HCL:

```yaml
stack_id: production-network
run_id: run-123
commit_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
```

Spacelift actions: `show` (default), `logs`, `changes`, `preview`, `deploy`.
`logs`/`changes` require a run ID; `preview`/`deploy` require a commit. Commands
creating remote runs are explicitly labelled as having side effects. Stack
configuration and policies control whether an eventual run applies automatically.
No approval/confirm/apply action is performed by RCDO.

Terraform/OpenTofu actions: `validate` (default), `fmt-check`, `plan`. The source
must parse as HCL. `--directory` overrides the source directory and is required
for stdin. Generated commands operate on the whole module, not just the input
file. Plan uses `-input=false -out=review.tfplan` and is labelled as having side
effects because it writes a plan and may acquire locks/read remote providers.

AWS/AliCloud generation reuses `decompose` and its documented support matrix,
dependency ordering, request examples, verification and rollback commands. Only
`create` recipes are currently supported through this entry point. Unsupported
resources, unresolved expressions and sensitive inputs remain INCOMPLETE.
See [IaC decomposition](iac-decomposition.md) for exact resource coverage.

Spacelift stack creation from arbitrary `spacelift_stack` resources is not a
supported recipe: resource display names do not establish platform IDs, and
Spacelift creation requires its API/provider rather than an invented CLI command.
Use explicit existing stack IDs in the command configuration. The generator uses
deterministic recipes without AI; `ai-assist` remains separately opt-in.

Spacelift/IaC JSON recipes include exact argv, POSIX-shell quoted command text,
source hash, `executed:false`, and side-effect notes. AWS/AliCloud retain the
existing decomposition schema. RCDO never evaluates embedded shell substitutions.

## Validation sources

- [OpenTofu plan JSON](https://opentofu.org/docs/internals/json-format/)
- [Terraform plan JSON](https://developer.hashicorp.com/terraform/internals/json-format)
- [Terraform validation JSON](https://developer.hashicorp.com/terraform/cli/commands/validate)
- [Spacelift run lifecycle](https://docs.spacelift.io/concepts/run)
- [Spacelift approval policies](https://docs.spacelift.io/concepts/policy/approval-policy)
- [Spacelift CLI API implementation](https://github.com/spacelift-io/spacectl/blob/main/internal/cmd/api/api.go)
- [Spacelift stack commands](https://github.com/spacelift-io/spacectl/blob/main/internal/cmd/stack/stack.go)

## Local acceptance

Run `make build` then `scripts/spacelift-iac-demo.sh` for a credential-free end-to-end
walkthrough with dated synthetic snapshots, saved-plan binding, persistent session
navigation, plan comparison and generated commands. The script checks expected
exit codes and leaves artifacts in the printed temporary directory.

Validation performed locally: `go test ./...`, `go vet ./...`, build and synthetic
demo; real `validate`, saved-plan JSON and binary conversion with Terraform 1.15.8
and OpenTofu 1.12.6 using only the built-in `terraform_data` resource. Installed
spacectl 1.25.0 exposes the required `api` flags. No live Spacelift account or
provider-backed cloud plan was used; no infrastructure was applied.
