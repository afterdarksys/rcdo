# Live operations extensions

## COLLECT: AWS evidence acquisition

```sh
rcdo collect --kind context --region us-east-1 --expect-account 111111111111 --output context.json
rcdo collect --kind fleet --region us-east-1 --expect-account 111111111111 --manifest fleet-manifest.json --output fleet.json
rcdo collect --kind relations --region us-east-1 --expect-account 111111111111 --output graph.json
```

The installed AWS CLI performs read-only requests using its credential chain or
`--profile`. STS identity must match the expected account before EC2 requests.
Inventory collection rechecks account/principal afterward; a changed or unavailable
identity discards inventory. This is an optimistic check, not credential pinning
across a transaction. Raw errors and unrelated EC2 response fields are not saved.

Context output feeds `context-summary` with context name `aws`. Fleet manifests
use the existing fleet schema and platform `aws-ec2`; observations are replaced.
Available baseline fields: `instance_type`, `image_id`, `state`, `vpc_id`, `subnet_id`.
This is control-plane inventory, not guest service health or host reachability.
Absent required hosts return incomplete. Relations output feeds `resource-walk`;
it contains observed instance attachments to VPC/subnet/security-group references,
not an exhaustive dependency graph or independently verified target resources.

[EC2 CLI pagination](https://docs.aws.amazon.com/cli/latest/reference/ec2/describe-instances.html)
uses `--max-items 100`, `--page-size 100` and opaque CLI `--starting-token` values.
Default maximum 10 CLI pages, configurable 1..100; maximum 10000 instances and
16 MiB per decoded response/final artifact. Failed pages, duplicate IDs, repeated
tokens and truncation preserve incomplete coverage (exit 30). Successful normalized
artifacts return 0; collection does not imply that their contents satisfy a baseline.
Times and source descriptions are recorded. Existing output files are never replaced.
No AWS calls run as part of automated tests; fixture runners check argv and failures.
