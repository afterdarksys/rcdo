# Production Readiness

This document deliberately separates executable evidence from integration assumptions.

## Verified in this repository

- Race-enabled unit and command tests.
- `go vet` across all packages.
- Reproducible builds through `make build`.
- Staged installation through `make install`.
- Linear, tab-free, width-checked help for every binary.
- Unified diff parsing, finding validation, status and exit-code behavior.
- Text, JSON, GitHub annotation, and SARIF rendering.
- Default-preview behavior for file and GitHub mutations.
- Owner-attributed suppressions with required future expiry dates.
- Mocked command-shape tests for read-only external collectors.

## Requires validation in each workplace

- GitHub authentication, repository rules, required checks, and merge queue behavior.
- Spacelift account schema, login profile, run states, policy naming, and approval rules.
- OpenTofu provider-specific sensitive fields and company-specific blast-radius policies.
- AWS partitions, profiles, assumed roles, account aliases, and approved regions.
- Alicloud profiles, RAM roles, account layout, and approved regions.
- Ansible inventories, collections, Vault setup, custom modules, and check-mode limitations.
- Actionlint installation and organization-specific runner labels.
- CI licensing and permissions for SARIF upload on private repositories.

## Blocked integration

The custom deployment kit adapter cannot be implemented or verified until its command help, version output, non-mutating inspection commands, input/output schemas, and redacted example artifacts are available.

Until that adapter exists, represent deployment-kit checks as a finding JSON report passed to `deploy-review`. If the report cannot be produced, add an incomplete check; never omit the component silently.

## Minimum workplace acceptance test

1. Run every collector with a known safe non-production change.
2. Confirm account, region, stack, commit, and environment labels against the authoritative UI.
3. Run a fixture containing one known failure for every blocking policy.
4. Confirm exit codes are preserved by shell pipelines and CI wrappers.
5. Confirm secrets are absent from terminal output, logs, artifacts, and SARIF.
6. Test with the actual screen reader, magnification, terminal, font size, and shell used by the operator.
7. Have an infrastructure owner and the blind or low-vision operator approve the output vocabulary and review order.
8. Record tool versions and artifact hashes with `evidence-pack`.
