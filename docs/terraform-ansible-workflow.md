# Terraform outputs to Ansible inputs

`rcdo workflow-check` explains and verifies explicit output-to-input mappings.
It reads saved artifacts and never launches a deployment. It supports Terraform
and OpenTofu root output JSON, static JSON/YAML inventory, saved
`ansible-inventory --list` snapshots, and JSON extra-vars.

```sh
rcdo workflow-check --manifest workflow.json --width 60
rcdo workflow-check --manifest workflow.json --stage execution
rcdo workflow-check --manifest workflow.json --stage verified --format json
```

Each matched mapping names the output, JSON pointer, host, variable, effective
variable source, type, and sensitivity. All values are withheld, including
values not marked sensitive. Extra-vars take precedence over inventory and are
identified as overrides. Wrong values or types block; unavailable or stale
evidence remains incomplete.

## Try the complete workflow

```sh
make build
python3 scripts/workflow-practice.py
```

This retains a private temporary directory containing complete example manifests,
artifacts, transcripts, and `results.json`. It checks matching inputs, overrides,
missing execution evidence, changed artifacts, and resolved inventory.

For actual local execution with installed Terraform and Ansible:

```sh
python3 scripts/workflow-practice.py --native
python3 scripts/workflow-practice.py --native --engine tofu
```

Native practice creates constant-only local state with no cloud resources,
extracts its outputs, generates inventory from those actual values, runs an
Ansible copy task on localhost, and independently checks the resulting file.
It writes inside a newly created temporary directory. The callback supplies the
actual run UUID and task results. Ansible may need permission to start its local
RPC process in a sandbox. Native practice requires no API credentials.

The example change/commit/account labels identify a local exercise; they do not
assert that a production commit or account was authenticated.

## Manifest contract

The manifest has `schema_version: "1"` and these fields:

- `identity`: `change_id`, full `commit`, `environment`, `engine` (`terraform` or
  `tofu`), `backend`, `workspace`, `account`, and `region`. All are required.
- `outputs`, `producer`, `inventory`, `playbook`: artifact bindings, each with
  `path` and the SHA-256 of the exact file bytes. Paths resolve relative to the
  manifest. An absent or changed artifact never silently passes.
- `inventory_format`: `static` (default) or `resolved`. Resolved means a saved
  native inventory snapshot, including dynamic-plugin results. No plugin runs.
- `extra_vars`: optional artifact binding for a JSON object.
- `hosts`: the exact intended host aliases/limit, with no duplicates.
- `mappings`: nonempty array of entries like the following. Every selected host
  needs an explicit mapping for `ansible_host`.
- `execution`, `events`, `verification`: additional artifact bindings used by
  the later stages.
- `required_checks`: named `{ "host": "blue", "name": "http-health" }`
  requirements for the verified stage, at least one per selected host.

```json
{
  "id": "blue-address",
  "output": "web_hosts",
  "pointer": "/blue/private_ip",
  "host": "blue",
  "variable": "ansible_host",
  "type": "string"
}
```

`pointer` is a JSON pointer within the output's `value`; empty selects the whole
value. Escape `/` as `~1` and `~` as `~0`. Types are `string`, `number`, `boolean`,
`array`, and `object`. Numeric comparison preserves precision. Terraform's own
declared output type is validated as well. Missing sensitivity metadata, null
selected values, unresolved templates, and missing paths remain incomplete.

## Evidence stages

**Inputs** checks source hashes, producer identity/freshness, inventory membership,
play host selection, variable precedence within the supported model, and every
mapping. It does not claim Ansible ran. The producer receipt requires:

```text
schema_version: 1
identity: the complete identity object from the manifest
outcome: succeeded
applied_at: RFC3339 timestamp
collected_at: RFC3339 timestamp after applied_at
state_lineage: observed state lineage
state_serial: observed nonnegative state serial
outputs_sha256: exact acquired output artifact digest
```

**Execution** additionally requires an invocation receipt and callback JSONL.
The receipt contains `schema_version`, matching `identity`, `run_id`, `started_at`,
`finished_at`, `outcome: "succeeded"`, `check_mode: false`, `hosts`, and
`outputs_sha256`, `inventory_sha256`, `playbook_sha256`, `extra_vars_sha256`
(empty when absent). `resolved_inputs` binds a saved native inventory snapshot
of the invocation's effective mapped inputs. `variable_sources_complete: true`
declares the adapter accounted for all variable sources. Missing declarations,
changed resolved variables, wrong run IDs, out-of-window events, check mode,
skipped-only hosts, and missing final callbacks cannot establish completion.

Do not set `variable_sources_complete` when configuration, environment variables,
additional `-e` arguments, vars plugins, or runtime sources remain unaccounted for.
A normal inventory snapshot alone does not prove task-time variable values.

**Verified** additionally requires `schema_version`, matching `identity`, `run_id`,
`execution_sha256`, `collected_at` after execution finished, `hosts`, and a
`checks` array of `{ "name": "http-health", "host": "blue", "outcome": "pass" }`.
Every required named check must pass. Failed checks retain blocking findings;
missing checks remain incomplete. This evaluates supplied independent evidence;
it does not perform live health probes.

Default maximum evidence age is 24 hours, adjustable with a positive `--max-age`.
Exit codes are 0 for clean inputs, 10 for review findings (including informational
callback results), 20 for blockers, 30 for incomplete evidence, and 2 for invalid
arguments/manifests. Do not interpret only exit 20 as a stop condition.

## Supported resolution and explicit limits

Static inventory supports one `all` root, nested children, inherited group vars,
host vars, and extra-vars. Conflicting memberships and group priority require a
resolved snapshot. Native snapshots must include `_meta.hostvars` and a valid
group graph; the native optional profile metadata and implicit empty `ungrouped`
group are supported.

The tool does not evaluate arbitrary Jinja/shell transformations, resolve external
roles/includes, inspect external templates, or reconstruct runtime variable
precedence. Plays with additional variable sources, delegation, dynamic host
selection, or unsupported execution controls remain incomplete. Mappings identify
host-variable consumers, not arbitrary template expressions or every task use.

The receipts are caller-supplied local evidence, not signatures or authenticated
remote execution attestation. A workplace collector must acquire and bind the
right backend/state version, invocation, and independent outcome checks. No
GitHub Actions or Spacelift write/execute adapter is added by this command.

Source and dependent artifact hashes are retained in report provenance. A
changed dependent artifact invalidates subsequent aggregation/session reading.
Use `report-read` or `review-session` on JSON output for speech/braille reading
and bookmarks. Actual assistive-technology acceptance still requires the operator.

## Change review provenance

`ansible-check`, `tofu-check`, `pr-manager inspect`, and `cloud-context-check`
accept paired `--change-id` and `--commit` flags. Reports retain environment,
tool, source digest, collection time, and source bindings even with zero findings.
`workflow-check` derives these from its manifest.

```sh
rcdo ansible-check --input playbook.yml --environment staging \
  --change-id CHANGE-42 --commit FULL_GIT_COMMIT --format json > ansible-report.json
```

`review-change` now requires matching provenance and a `sources` entry for each
component; old unbound reports remain incomplete. For example:

```json
{
  "schema_version": "1",
  "change_id": "CHANGE-42",
  "commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "environment": "staging",
  "required_components": ["ansible"],
  "reports": {"ansible": "ansible-report.json"},
  "sources": {"ansible": "playbook.yml"},
  "tools": {"ansible": "ansible-check"}
}
```

Known component names have tool defaults; custom component names should specify
`tools`. The collection timestamp describes when RCDO reviewed the artifact,
not when a remote resource last changed. Supplied commit labels do not prove
source checkout identity. Keep the existing repository/run-specific checks.

`deploy-review` requires valid versioned reports with explicit coverage and a
consistent status. It aggregates coverage; use `review-change` when change
identity must be verified. Neither command treats `{}` as completed evidence.

## Validation recorded September 11, 2026

- Full Go test suite and race-enabled suite passed.
- `go vet ./...`, `make build`, and the Python callback contract test passed.
- The six original audit reproductions now return blocking/incomplete results;
  no unsafe success reproduced.
- Seven synthetic workflow practice cases passed.
- Seven practice cases passed using installed Terraform with real localhost
  Ansible execution and independent file verification.
- The same seven cases passed using installed OpenTofu with real localhost
  Ansible execution and independent file verification.
- Focused regressions cover YAML presentation, secret redaction, truncated scans,
  omitted play sections, PR readiness, clean-report provenance, exact numbers,
  wrong/missing outputs, conflicting overrides, resolved inventory, invocation
  inputs, required health checks, and dependency changes after saving a session.

The native tests use temporary local state and localhost only. Authenticated
workplace adapters and actual screen-reader/braille acceptance are not claimed.
