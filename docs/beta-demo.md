# RCDO Beta Demo

This demo exercises only local parsing and rendering. It does not call AWS,
AliCloud, Terraform, OpenTofu, Ansible, or an AI provider.

## Build and identify the beta

```sh
make verify
make build
./dist/rcdo --version
```

Expected version:

```text
rcdo 1.3.0-beta.1
```

## Show the complete dependency-ordered plan

```sh
./dist/hcl2aws --input examples/decompose-aws.tf
```

Point out:

- The VPC appears before the subnet even if source ordering changes.
- Resource references become captured `RCDO_*` variables.
- Every create example has verification and rollback commands.
- The sample AMI placeholder makes the result `INCOMPLETE` rather than falsely
  ready.
- The header always says `Execution: NOT RUN`.

## Step through it

```sh
./dist/hcl2aws --input examples/decompose-aws.tf --step 1
./dist/hcl2aws --input examples/decompose-aws.tf --step 2
./dist/hcl2aws --input examples/decompose-aws.tf --step 3
```

## Show Ansible decomposition

```sh
./dist/ansible2aws --input examples/decompose-ansible.yml
```

## Show machine-readable output

```sh
./dist/hcl2aws --input examples/decompose-aws.tf --format json
```

## Show fail-closed behavior

```sh
printf '%s\n' \
  'resource "aws_vpc" "demo" {' \
  '  cidr_block = var.vpc_cidr' \
  '}' |
  ./dist/hcl2aws
```

The result identifies the runtime expression as unresolved and exits with code
`30`.

## Optional workplace-safe AliCloud demonstration

Use parsing only; do not copy generated commands into a shell connected to a
real account during the initial demonstration.

```sh
./dist/hcl2ali \
  --input path/to/reviewed-alicloud.tf \
  --region cn-hangzhou \
  --profile approved-demo-profile
```

Before any later manual cloud exercise, follow the workplace validation steps
in `docs/iac-decomposition.md`.
