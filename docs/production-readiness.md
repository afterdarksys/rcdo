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
- Semantic explanation and comparison fixtures for JSON, YAML, TOML, and HCL.
- Preview-only and atomic-write tests for JSON, YAML, TOML, and HCL mutation.
- Unified read-only Git review, repository-policy enforcement, and resumable review sessions.
- Default redaction for configuration diffs, mutation previews, and narrated errors.
- Configuration precedence, credential redaction, mode-`0600` enforcement, and three-slot AI routing.
- Explicit, non-configurable AI activation; bounded/redacted inputs; mocked OpenAI, Anthropic, and OpenRouter request/response handling.
- Read-only HCL and Ansible decomposition with dependency ordering, single-step inspection, and fail-closed unresolved reporting.

## Review evidence and session validation

- jsonprobe adapter fixtures cover required checks, missing and stale observations,
  contradictory outcomes, failures, duplicate names, and raw diagnostic isolation.
- Schema-2 review sessions retain complete coverage, bind source report bytes and
  optional artifact hashes, and optionally recheck a clean Git repository and HEAD.
- Reading completion preserves blocked and incomplete report status. Sessions expire
  and refuse acknowledgement when their bound inputs change.
- These are local integrity checks, not signed provenance or deployment authorization.

## Requires validation in each workplace

- GitHub authentication, repository rules, required checks, and merge queue behavior.
- Spacelift account schema, login profile, run states, policy naming, and approval rules.
- OpenTofu provider-specific sensitive fields and company-specific blast-radius policies.
- AWS partitions, profiles, assumed roles, account aliases, and approved regions.
- Alicloud profiles, RAM roles, account layout, and approved regions.
- Ansible inventories, collections, Vault setup, custom modules, and check-mode limitations.
- Actionlint installation and organization-specific runner labels.
- CI licensing and permissions for SARIF upload on private repositories.
- Repository policy owners, critical-path patterns, companion-file requirements, and approved exceptions.
- Built-in operational-policy matches against organization-specific AWS, Alicloud, Kubernetes, and IAM conventions.
- Review-session storage location, retention, ticket linkage, and acknowledgement language.
- Whether plaintext mode-`0600` API-key storage is allowed or environment/secret-manager resolution is required.
- Approved AI models, provider endpoints, billing limits, retention terms, failover data sharing, and the effectiveness of redaction against representative repositories.
- Generated AWS and AliCloud commands against the exact CLI versions, regions, resource types, naming rules, and organizational controls used at work.

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
