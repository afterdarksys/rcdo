# Local Rego policy checks

`rego-check` evaluates user-supplied Rego modules with the local OPA CLI and produces
RCDO reports, including accessible text, JSON, SARIF, and GitHub annotations.
Install OPA 1.x separately and make `opa` available on PATH. Rego v1 syntax is used.
No OPA server or cloud account is required.

```sh
rcdo rego-check --rego examples/rego/terraform.rego \
  --input examples/rego/plan.json --format text

# Export a saved Terraform/OpenTofu plan, then check it.
tofu show -json saved.tfplan > plan.json
rcdo rego-check --rego examples/rego/terraform.rego --input plan.json --format json

# Multiple modules can share package rules.
rcdo rego-check --rego rules.rego --rego helpers.rego \
  --query data.team.decision --input config.json
```

Input must be one JSON document (stdin by default). Any JSON configuration,
normalized RCDO report, AWS response, or Spacelift export can be checked; policies
must understand that input's schema. This does not automatically reproduce
Spacelift's policy input or decision semantics. Convert other formats to JSON first.

The query defaults to `data.rcdo.decision` and must be a simple dotted data
reference. It must return this object:

```rego
package rcdo
import rego.v1

decision := {
  "allow": input.environment != "production",
  "findings": [],
  "incomplete": [],
}
```

`allow` is required and boolean. Optional `findings` is an array of objects with
required `id`, `severity`, `title`, `resource`, `reason`, and `remediation` fields.
IDs must be unique; severity is `info`, `warning`, `high`, or `critical`. Optional
`incomplete` contains nonempty explanations of missing checks. Unknown fields and
invalid findings are rejected. Policies should validate their expected input and
report missing data explicitly; Rego rules can otherwise produce empty results
when input fields are absent.

An explicit `allow: false` always adds a blocking finding. Findings still affect
status when `allow` is true. Suppression policies are intentionally not supported
for this command. Exit codes follow RCDO conventions:

- `0`: allowed, no findings or missing checks.
- `10`: informational or warning findings.
- `20`: denied or high/critical findings.
- `30`: missing OPA, undefined/malformed decision, policy compilation/evaluation
  failure, timeout, or incomplete checks (takes precedence over findings).
- `2`: invalid arguments/input or local file errors.

## Execution limits and evidence

RCDO snapshots the input and explicitly named modules into a private temporary
directory, then removes it after evaluation. Reports record SHA-256 hashes of
the input and each module in argument order. Input and combined modules are each
limited to 16 MiB, with at most 32 module files. Directories and bundles are not
loaded. OPA stdout is capped at 16 MiB and stderr at 64 KiB. `--timeout` defaults
to 5s and accepts 100ms through 1m, covering capabilities discovery and evaluation.
This is a wall-clock/output limit, not an OS memory sandbox.

Capabilities permit an explicit allowlist of pure arithmetic, collection,
comparison, string, JSON, regex, glob, semver, and CIDR operations. The exact list
is `regoBuiltins` in `toolkit/rego.go`. Network calls (`http.send`, DNS), runtime
inspection (`opa.runtime`), print/trace, random values, and clock reads are not
available. Remote schema access is disabled. The subprocess receives a minimal
environment without inherited cloud credentials. Use a trusted OPA executable;
these restrictions are policy capabilities, not isolation of a malicious binary.

OPA diagnostics are deliberately summarized because errors may expose policy
literals or input values. For detailed syntax diagnostics on trusted policies,
run `opa check --strict your-policy.rego` locally. Normal RCDO audit configuration
also covers this command. A policy can put input values in findings, so policy
authors should avoid returning secrets; report output is not automatically secret-free.

Safe defaults can be configured under `commands.rego-check`: `format`, `width`,
`environment`, `timeout`, and `query`. Module paths remain explicit CLI arguments.

See [OPA's policy language](https://www.openpolicyagent.org/docs/policy-language)
and [CLI reference](https://www.openpolicyagent.org/docs/cli) for Rego authoring.
