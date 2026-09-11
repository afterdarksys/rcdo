# Workplace workflow plumbing

RCDO can record an authorized Terraform/OpenTofu-to-Ansible run, package its
evidence, acquire it from GitHub Actions or an explicit Spacelift/custom export,
trace static consumers, and retain operator acceptance observations. The actual
employer identity adapter, authentication and assistive-technology observations
must come from the workplace. Local practice does not certify them.

## 1. Configure and record the first nonproduction run

Copy [record.example.json](../integrations/workflow/record.example.json) to a
private workplace configuration. Replace every placeholder. Paths are relative
to the config file. Commands are argv arrays, executed without a shell.

The four workplace commands have explicit contracts:

| Command | Input | Required output or behavior |
| --- | --- | --- |
| `identity_command` | Existing authenticated context | JSON exactly matching the identity object, obtained from the checkout, backend and cloud context |
| `producer_command` | Initialized Terraform directory and previously approved plan | Execute the authorized producer; exit nonzero on failure |
| `inventory_command` | Exact `terraform output -json` on standard input | Static Ansible inventory as JSON or YAML |
| `verify_command` | Workplace configuration/context | JSON list of `{host,name,outcome}` observations; outcome `pass` or `fail` |

The identity command must observe context, not echo the config. The recorder
checks it before and after execution, independently checks `workspace show`,
and compares `state pull` before/after output collection. It binds state lineage
and serial, source hashes, resolved inventory, callback events and health checks.
Terraform state and command diagnostics stay in the private recording directory;
the exporter includes only referenced evidence.

```sh
python3 integrations/workflow/record.py --config /private/work/record.json \
  --output-dir /private/work/run-42 --binary dist/rcdo --execute
rcdo workflow-check --manifest /private/work/run-42/workflow.json \
  --stage verified --width 60
```

`--execute` authorizes the configured producer and Ansible commands. Collection
commands themselves never apply infrastructure. The recorder uses a private
Ansible config and the RCDO callback. It does not support arbitrary additional
Ansible CLI flags. Set `variable_sources_complete` only after accounting for
inventory plugins, adjacent group/host vars, roles, lookups and other variable
sources. It defaults to false; unresolved sources remain incomplete. Static
tracing cannot establish Ansible's complete runtime precedence.

An unsuccessful command stops recording. Incomplete or blocked final reviews
fail the recorder even though their evidence is retained. Rerun into a new
directory. No successful receipt is synthesized for a failed command.

## 2. Record accessibility acceptance

```sh
rcdo pilot start --suite workflow --state workflow-pilot.json \
  --operator Ryan --setup 'Actual OS, terminal, screen reader/braille versions'
rcdo pilot show --state workflow-pilot.json --width 60
```

Start returns exit 30 while observations are pending. Perform these tasks using
the actual assistive technology and record the outcome, obstacles, assistance
and evidence. Use text reports at the operator's preferred width and saved
`review-session` bookmarks to test interruption/resumption.

| Task ID | Operator acceptance criterion |
| --- | --- |
| `confirm-nonproduction-identity` | Identify account, region, backend, workspace and deployed commit |
| `trace-output-consumer` | Locate a mapping and its task/template consumer without reading secret values |
| `identify-override` | Find the extra-var override and explain the resulting mismatch |
| `detect-stale-output` | Identify stale evidence and the step needed to recollect it |
| `detect-wrong-host` | Identify the unexpected/missing host and stop execution |
| `explain-stop-condition` | Distinguish review, blocked and incomplete outcomes |
| `resume-interrupted-review` | Resume the same saved report/bookmark with unresolved findings preserved |
| `verify-run-outcome` | Locate independent required checks and explain any missing coverage |

```sh
rcdo pilot record --state workflow-pilot.json --task trace-output-consumer \
  --outcome pass --notes 'Actual observed result and assistance needed' \
  --evidence operator-observation.txt
```

Use `fail` or `blocked` when appropriate. Evidence is hash-bound; edits invalidate
the observation. Automation never records an operator pass. Existing pilot
history and latest observations survive resumption.

## 3. Export and collect evidence

```sh
python3 integrations/workflow/export.py --manifest evidence/workflow.json \
  --output-dir exported-run-42 --provider local --project staging --run 42
rcdo workflow-collect --profile exported-run-42/profile.json \
  --output-dir collected-run-42 --format json > collection-report.json
```

Export creates `bundle/`, `bundle.zip`, `profile.json` and `export.json`. It checks
existing hashes, copies only referenced files, and rewrites nested artifact
paths and dependent receipt hashes for portability. This does not create remote
attestation. Keep the bundle private: Terraform output values and inventory may
contain secrets even though RCDO reports withhold them.

The collector validates origin, identity, artifact hashes, archive paths and
size limits before creating a new private output directory. It saves
`review.json` and runs the ordinary checker at the profile's `inputs`,
`execution` or `verified` stage. Exit codes remain 0 clean, 10 review, 20 blocked,
30 incomplete, 2 invalid usage. Collection does not turn a warning into approval.

### GitHub Actions

Use [github-steps.yml](../integrations/workflow/github-steps.yml) after the
authorized job has created its manifest. Upload `bundle/`, not the containing
ZIP inside another ZIP. The steps use the artifact action's documented
[directory upload and artifact ID support](https://github.com/actions/upload-artifact).

For collection, retain the exported profile identity and origin, remove `bundle`,
and set `artifact_id` to the uploader's artifact ID. Use provider `github`,
project `owner/repository`, numeric run ID and the exact positive attempt.

```sh
rcdo workflow-collect --native --profile github-profile.json \
  --output-dir github-run-42 --width 60
```

The installed authenticated `gh` CLI needs Actions read access. The collector
checks the completed successful run's commit/attempt before and after download,
and the artifact's run, commit, expiration and SHA-256 digest. These fields follow
the [GitHub artifacts API](https://docs.github.com/en/rest/actions/artifacts).
Missing metadata stays incomplete. Collection must occur after the producing
run completes; calling it inside that same running job cannot satisfy this check.

### Spacelift

Export in your existing run hook using provider `spacelift`, stack ID as project,
and the exact run ID. Transport `bundle.zip` using the workplace's artifact
store. Set the profile's `endpoint` to the expected Spacelift API endpoint and
`bundle` to the pinned local ZIP, or `adapter` to an absolute export executable.

`workflow-collect --native` uses the existing authenticated `spacectl` context
acquirer to verify endpoint, stack/run identity, deployed commit and `FINISHED`
state before and after acquiring the export. It does not assume Spacelift has a
generic artifact download endpoint. Authentication follows the installed
[spacectl CLI](https://docs.spacelift.io/concepts/spacectl).

### Custom deployment kit

Set provider `custom` and `adapter` to an absolute executable path. The contract:

```text
/absolute/kit-export rcdo-export --project PROJECT --run RUN_ID
```

Return one JSON object: `schema_version: "1"`, the exact `identity` and `origin`,
`archive_base64` containing the ZIP, and its hexadecimal `sha256`. Nonzero exit,
changed identity, malformed JSON and changed bytes are incomplete. Standard
error is withheld from the report. No shell interpolation occurs.

[adapter.py](../integrations/workflow/adapter.py) is a working reference for an
export already retrieved by the kit: set `RCDO_WORKFLOW_EXPORT` to its private
`export.json`, then use the script's absolute path as `adapter`. Replace retrieval
with the kit's actual authenticated store API. This reference does not claim to
implement an unknown employer's private API.

Limits: individual files 16 MiB, extracted bundle 32 MiB, 256 ZIP entries;
the reference exporter/local bound-file reader limits the ZIP itself to 16 MiB.
Adapter standard output is capped at 32 MiB and collector commands at one minute.

## 4. Trace consumers into source

```sh
rcdo workflow-trace --root ./ansible --input ./ansible/site.yml \
  --variable db_host --graph-out consumer-graph.json --width 60
```

The graph follows literal task/playbook includes, local `roles/NAME` task,
defaults/vars/handler files, literal vars files and template sources. Direct
variable aliases are shown as candidate source chains. Reports name files,
lines and tasks while withholding expression values. Scope/precedence and
conditional execution are not inferred from a static relationship.

Add `source_graph: {path,sha256}` to the workflow manifest to require consumer
coverage for each mapped application variable and bind all traced files.
`ansible_host` is a connection input and does not require a template reference.
The recorder adds this graph automatically.

Dynamic includes, role metadata dependencies, unresolved expressions/filters,
Jinja control flow, runtime conditions and lookups remain explicit gaps. Sources
outside the project root, inclusion cycles, and traversal limits also produce
incomplete results. The graph never grants runtime execution coverage by itself.

## 5. Bind other report producers

Common report commands now accept paired `--change-id` and `--commit` flags.
Named `--input`, comparison inputs and workflow manifests bind primary evidence;
named policies, expectations and related input files are captured as dependencies
and rechecked at emission. A policy alone cannot stand in for primary evidence.

```sh
rcdo iac-validate --input validate.json --environment staging \
  --change-id CHANGE-42 --commit FULL_DEPLOYED_COMMIT --format json
```

Native IaC validation and Kubernetes collection also bind their observed source.
Use `kube-explain --save-snapshot` to retain the exact normalized source for later
aggregation. Other live-only commands must retain their collector's source or
be reviewed from a named snapshot. Requested provenance with no captured source
remains incomplete. Legacy unbound reports are not silently upgraded.

## Local validation

```sh
make build
python3 scripts/workflow-integration-practice.py
python3 scripts/workflow-integration-practice.py --native --engine terraform
python3 scripts/workflow-integration-practice.py --native --engine tofu
```

Native practice executes only constant-output local state and a localhost file
write. It tests the actual recorder, output-to-inventory adapter, callback,
independent check, export rebinding and verified collection. The pilot remains
pending. Remote workplace credentials and operator acceptance are separate runs.
