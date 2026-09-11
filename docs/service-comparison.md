# Like-service mappings and deployment comparison

Use `service-map` to build a catalog of services with comparable purposes.
Use `service-compare` to compare two through sixteen deployment snapshots under
that catalog. Four deployments produce six pair comparisons. The commands run
offline and use RCDO's existing text and JSON report formats.

## Built-in mappings

| Family | AWS | Azure | Google Cloud | Oracle Cloud |
| --- | --- | --- | --- | --- |
| Object storage | S3 | Blob Storage | Cloud Storage | Object Storage |
| Block storage | EBS | Managed Disks | Persistent Disk or Hyperdisk | Block Volumes |

Azure **block blobs** are objects within Blob Storage. Azure **Managed Disks**
are VM block storage. Mapping EBS to S3 would compare different storage models.
These groupings follow the provider descriptions:
[AWS S3](https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html),
[AWS EBS](https://docs.aws.amazon.com/ebs/),
[Azure Blob Storage](https://learn.microsoft.com/en-us/azure/storage/blobs/storage-blobs-introduction),
[Azure Managed Disks](https://learn.microsoft.com/en-us/azure/virtual-machines/managed-disks-overview),
[Google storage options](https://docs.cloud.google.com/architecture/storage-advisor),
and [Oracle storage](https://www.oracle.com/cloud/storage/).

Catalog membership describes comparable purpose. It does not establish API,
performance, security, durability, pricing or migration equivalence. It also does
not add native collectors for Azure or Oracle. This release compares normalized
snapshots supplied by you or an exporter; it does not ingest raw Terraform state,
provider CLI output, or automatically query cloud accounts.

## Build your mappings

```sh
rcdo service-map
rcdo service-map --output team-services.json
rcdo service-map --input team-services.json
```

The second command creates a private JSON file and refuses to overwrite an
existing file. Edit it to add your own families and services, then validate it
with the third command. `--format json` writes the validated catalog to stdout.
A custom catalog replaces the built-in catalog; export the built-ins first if
you want to extend them.

Each family requires a unique ID, description, at least two service entries, and
one or more field contracts. For example, a team-defined queue mapping could be:

```json
{
  "schema_version": "1",
  "families": [{
    "id": "work-queue",
    "description": "Our worker queue comparison contract",
    "services": [
      {"cloud": "cloud-a", "id": "queue-a", "name": "Queue A"},
      {"cloud": "cloud-b", "id": "queue-b", "name": "Queue B"}
    ],
    "fields": {
      "retention_seconds": {
        "type": "number",
        "unit": "seconds",
        "description": "Configured retention duration for newly accepted messages"
      },
      "delivery_mode": {
        "type": "string",
        "description": "Team-normalized delivery guarantee label"
      }
    }
  }]
}
```

Supported types are `boolean`, `string` and `number`. Numbers require an explicit
unit, including `count` or `ratio` for dimensionless quantities. **Every field in
a family's contract is required for comparison.** Definitions must state the
same meaning for every mapped service. A cloud/service pair can belong to only
one family; ambiguous mappings are rejected. IDs use lowercase letters, digits,
hyphens and underscores and start with a letter.

Built-in object-storage fields are `public_access`, `versioning`, and
`customer_managed_key`. Built-in block-storage fields are `size_gib` and
`encrypted`. Run `service-map` to read each field's exact definition. Extend these
contracts with the controls your deployment needs; the initial fields are not a
complete deployment assessment.

## Describe the deployments

A version 1 bundle contains a `deployments` array. Each entry has this shape:

```json
{
  "name": "cloud-a",
  "cloud": "aws",
  "account": "example-account",
  "location": "us-east-1",
  "source": "redacted-aws-observation.json",
  "collected_at": "2026-09-11T12:00:00Z",
  "complete": true,
  "resources": [{
    "key": "uploads",
    "service": "s3",
    "id": "example-uploads-bucket",
    "properties": {
      "public_access": false,
      "versioning": true,
      "customer_managed_key": true
    }
  }]
}
```

`name` identifies the deployment in reports and selectors. `cloud` and `service`
select a catalog entry. `account`, `location` and `id` record source identity;
they need not match across deployments and their values are withheld in reports.
`source` is an observation label, not an automatically fetched or authenticated
file. Use the actual observation time for `collected_at`.

The resource `key` is its logical role. Give the corresponding Azure container,
Google bucket and Oracle bucket the same `uploads` key, even when their provider
IDs differ. Pairing is explicit; RCDO does not guess based on resource names.
Multiple instances of the same service require separate logical keys.

The producer must normalize properties to the catalog's types, units and
definitions. For example, convert bytes to GiB before supplying `size_gib`.
RCDO does not infer units, translate provider enums, convert currency, or deduce
effective public access from one isolated policy flag. Omit a field or use null
when its meaning cannot be established; that produces an incomplete comparison.
Never substitute zero or false for unknown evidence.

Set `complete` to true only for an explicitly covered scope of logical roles.
An empty `resources` array is valid evidence of absence in that declared scope;
an omitted array is invalid. Missing or false `complete` prevents absence from
being treated as established. A comparison of entirely empty deployments remains
incomplete.

## Compare two, three or four clouds

```sh
rcdo service-compare --input deployments.json
rcdo service-compare --input deployments.json --deployments cloud-a,cloud-b
rcdo service-compare --input deployments.json \
  --deployments cloud-a,cloud-b,cloud-c,cloud-d --values --format json
rcdo service-compare --input deployments.json --maps team-services.json
```

By default every pair is compared. A selector compares only the named deployments
and rejects missing or duplicate names. Multiple deployments in the same cloud
are also supported. Pair names and role/field names are included in findings,
with one pair summary at a time in the text report for linear reading.

- Matching: both resources have the same family and every mapped field matches.
- Different: a mapped field differs, or the role uses different service families.
- Missing: the logical role is absent from one fresh, complete deployment scope.
- Unknown: freshness, coverage, service mapping or field evidence is insufficient.

Unknown services, null/missing fields and properties outside the contract make
the report incomplete. RCDO does not silently drop extra properties. An invalid
resource's fields are not compared until its complete contract is available.
Differences elsewhere can still appear alongside incomplete findings.
Numeric comparison preserves decimal precision: `100`, `100.0`, and `1e2` match;
adjacent large integers remain distinguishable. Strings compare exactly.

Property values are withheld by default. `--values` reveals only differing
properties, so use it deliberately when sharing a report. Matching values and
provider resource IDs are not printed. Labels and field names are report content;
do not put secrets in them.

Exit codes are 0 for matching covered fields, 10 for differences or missing
resources requiring review, 30 for incomplete evidence, and 2 for invalid input.
The default freshness limit is 15 minutes; `--max-age` changes that explicitly.
A matching result establishes consistency under the selected contract, not that
either deployment is safe or correctly configured.

Both source and catalog hashes appear in the report. Supply `--change-id`,
`--commit` and `--environment` to attach standard RCDO provenance; named source
and custom mapping files are hash-bound and subsequent changes invalidate that
saved evidence. Hashes establish local consistency, not authenticated collection.

Configurable comparison defaults are `format`, `width`, `environment`, `max-age`
and `maps` under `commands.service-compare`. Deployment selection and value
disclosure remain explicit CLI choices. `service-map` supports configured
`format` and `width`.

## Credential-free practice

```sh
make build
python3 scripts/service-compare-practice.py --binary dist/rcdo
```

Practice creates four explicitly synthetic observations with current fixture
timestamps. It checks all six pairs, a two-cloud selection, a difference, missing
coverage, and object-versus-block incompatibility. Do not refresh timestamps on
real historical snapshots to make stale evidence pass.

Limits: 16 deployments, 256 resources per deployment, 32 properties per resource,
64 families, 32 services and 32 fields per family; 16 MiB for deployment JSON and
1 MiB for catalogs. Comparisons exceeding 100000 possible field checks must be
split into smaller bundles. JSON schemas reject duplicate and unknown fields.
