# IaC Decomposition

RCDO can decompose HCL/OpenTofu and Ansible YAML into an ordered manual runbook
for the AWS CLI or AliCloud CLI. This is an inspection and learning tool: it
never invokes either cloud CLI.

## Commands

Use a short source-to-target alias:

```sh
rcdo hcl2aws --input main.tf
rcdo hcl2ali --input alicloud.tf --region cn-hangzhou --profile work
rcdo ansible2aws --input playbook.yml
rcdo ansible2ali --input playbook.yml --region cn-hangzhou
```

Or use the generic form:

```sh
rcdo decompose --from hcl --to aws --input main.tf
rcdo decompose --from ansible --to alicloud --input playbook.yml --region cn-hangzhou
```

Use `--format json` for automation. Use `--step NUMBER` to inspect one operation
while retaining its original plan number and dependencies.

```sh
rcdo hcl2aws --input main.tf --step 1
rcdo hcl2aws --input main.tf --step 2
```

## Step contents

Every mapped operation includes:

- The source resource or Ansible task.
- Dependencies reordered before the operation.
- A wrapped CLI command example.
- A JSON request example using provider API parameter names.
- The response field and shell variable to capture.
- A read-only verification command.
- A rollback command.
- Notes about source attributes that were not mapped.

The plan always prints `Execution: NOT RUN`.

## Beta support matrix

| Source | AWS target | AliCloud target |
|---|---|---|
| HCL | `aws_vpc`, `aws_subnet`, `aws_security_group`, `aws_instance`, `aws_s3_bucket`, `aws_iam_role` | `alicloud_vpc`, `alicloud_vswitch`, `alicloud_security_group`, `alicloud_instance`, `alicloud_ram_role` |
| Ansible | `ec2_vpc_net`, `ec2_vpc_subnet`, `ec2_group`, `ec2_instance`, `s3_bucket`, `iam_role` from their common collections | AliCloud module names corresponding to the supported HCL resource names |

Collection-qualified Ansible module names are accepted. The final component of
the module name selects the recipe.

Use `--region` and `--profile` to place explicit CLI context on create,
verification, and rollback examples. AliCloud VPC and ECS recipes require
`--region`; omitting it makes the plan incomplete. AWS S3 bucket creation is
also incomplete without a region because regions outside `us-east-1` require a
location constraint. AliCloud OSS is excluded from this beta because the legacy
`aliyun oss` surface is deprecated in favor of ossutil and workplace command
standards vary.

## Fail-closed behavior

RCDO returns exit code `30` and status `INCOMPLETE` when the source needs runtime
or provider context that static parsing cannot establish. Examples include:

- Terraform/OpenTofu variables, locals, data sources, modules, `count`, and
  `for_each` expressions.
- Nested resource blocks such as inline security-group rules.
- Composite interpolations rather than direct resource ID references.
- Ansible Jinja expressions, loops, conditionals, delegation, and deletion
  tasks.
- Unsupported resources or modules.
- Missing required API parameters or redacted sensitive values.

Direct references such as `aws_vpc.main.id` become a captured variable such as
`$RCDO_AWS_VPC_MAIN_ID`. RCDO does not query state, inventories, credentials,
accounts, regions, or live resources to fill placeholders.

## Workplace use

Before running any generated command manually:

1. Confirm the authenticated identity, account, subscription, and region using
   the organization's approved procedure.
2. Compare the request example with current provider documentation.
3. Replace every placeholder and resolve every incomplete item.
4. Run the read-only verification command after the create operation.
5. Review the rollback command and dependency impact before proceeding.
6. Record the reviewed source and output with `evidence-pack` when required.

Generated commands are educational starting points, not proof of semantic
equivalence with Terraform, OpenTofu, or Ansible.
