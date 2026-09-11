# Google Cloud

RCDO supports Google Cloud context checks, read-only Compute Engine collection,
and create-command previews. Install `gcloud` and authenticate it separately.
Replace the example project, account and zone with your intended environment.
RCDO never logs in, switches configurations, or executes generated create commands.

## Check the active context

```sh
rcdo cloud-context-check --expect-cloud gcp --collect \
  --expect-project rcdo-test --expect-account operator@example.com \
  --expect-region us-central1 --expect-zone us-central1-a
```

Project and account expectations are required. Optional region and zone
expectations compare the configured compute defaults. `--configuration NAME`
selects a named gcloud configuration. Configuration, active credential account,
and the accessible project's ID, number and lifecycle are checked. Credential
impersonation, credential overrides and custom API endpoints are unsupported.
This checks gcloud identity; it does not attest Application Default Credentials
or independently authenticate saved evidence.

Offline checks accept a flat JSON object with `cloud`, `project`, `account`, and
optional `region` and `zone` string fields. Use `--input FILE` without `--collect`.

## Collect evidence

```sh
rcdo collect --cloud gcp --kind context \
  --project rcdo-test --expect-account operator@example.com \
  --output gcp-context.json

rcdo collect --cloud gcp --kind relations \
  --project rcdo-test --expect-account operator@example.com \
  --zone us-central1-a --output gcp-relations.json
```

Context bundles work with `context-summary`. Named acquisition and merging into
existing workflow context bundles also work:

```sh
rcdo context-acquire --kind gcp --native --name workplace-gcp \
  --project rcdo-test --expect-account operator@example.com \
  --output workplace-context.json
```

Fleet and relationship collection require one explicit zone. Collection checks
identity before and after inventory; a failed recheck discards the inventory.
`--max-instances` defaults to 1000 and permits up to 10000. The collector requests
one extra instance to detect truncation. Invalid, missing, duplicate, out-of-scope
or over-limit inventory produces incomplete evidence and exit 30.

Relationships describe instances and their network/subnet attachment references.
They do not independently inventory the referenced networks or subnets. IDs use
canonical `projects/PROJECT/...` paths. In the shared graph schema, `account`
contains the project ID and `region` contains the selected observation zone,
including for network/subnet references. Shared VPC and legacy network
attachments are currently unsupported and make collection incomplete.
VM metadata, SSH keys and IP addresses are not exported.

Fleet collection uses the existing version 1 fleet manifest with
`--kind fleet --manifest FILE` and the same selectors. Host `platform` must be
`gcp-compute`, and host IDs must be
`projects/PROJECT/zones/ZONE/instances/NAME`. Baselines can compare `instance_id`,
`machine_type` (canonical resource path), `state`, `project` and `zone`.
Missing required hosts make the bundle incomplete. Feed the output to
`fleet-check` for baseline comparison. Output files must be new; they are created
with private permissions. Standard report exit codes still apply, including
10 for informational relationship findings, 20 for critical mismatches, and
30 for incomplete checks.

## Preview create commands

```sh
rcdo command-gen --to gcp --input examples/gcp/network.json
rcdo command-gen --to gcp --input examples/gcp/network.json \
  --shell powershell --format json
```

Requests are JSON or YAML objects of literal string fields. All requests require
`project`, `account`, `resource` and `name`; `configuration` is optional.

| Resource | Additional fields |
| --- | --- |
| `network` | Optional `subnet_mode`: `custom` (default) or `auto` |
| `subnet` | `network`, `region`, IPv4 CIDR `range` |
| `bucket` | `location`: region or `US`, `EU`, `ASIA` |
| `service-account` | Optional `display_name` |

Unknown fields are rejected. Every preview pins the project and account and
reports `executed: false`. Bucket previews enable uniform bucket-level access
and public access prevention. Service-account previews create no keys or role
grants. Provider naming, availability and organization policies still apply.

Google Cloud HCL decomposition, IAM policy inventory, Cloud SQL, Cloud Run, GKE,
Shared VPC and cross-project collection are not implemented in this release.
The existing Terraform-to-Ansible workflow checks remain provider-neutral;
Google Cloud support does not bypass their evidence requirements.

## Validation and CLI contracts

Credential-free Go tests exercise fixture-based collection through the context,
fleet and relationship readers, incorrect identity, identity changes, malformed
inventory, collection limits, redaction and both command-preview shells.
Authenticated nonproduction Google Cloud acceptance remains pending.

The native commands follow Google's references for
[configuration](https://docs.cloud.google.com/sdk/gcloud/reference/config/list),
[active credentials](https://docs.cloud.google.com/sdk/gcloud/reference/auth/list),
[project identity](https://docs.cloud.google.com/sdk/gcloud/reference/projects/describe),
[instance listing](https://docs.cloud.google.com/sdk/gcloud/reference/compute/instances/list),
[networks](https://docs.cloud.google.com/sdk/gcloud/reference/compute/networks/create),
[subnets](https://docs.cloud.google.com/sdk/gcloud/reference/compute/networks/subnets/create),
[buckets](https://docs.cloud.google.com/sdk/gcloud/reference/storage/buckets/create),
and [service accounts](https://docs.cloud.google.com/sdk/gcloud/reference/iam/service-accounts/create).
