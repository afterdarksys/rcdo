# RCDO next steps

Updated September 11, 2026 after the Terraform-to-Ansible workflow work.

The six audited false-clean/false-ready defects are fixed. `workflow-check` now
checks explicit output-to-input mappings, extra-vars overrides, source identity,
artifact freshness, invocation inputs, callback results, and required outcome
checks. Full tests, race tests, vet, the build, and native localhost workflows
with both Terraform and OpenTofu passed. See the
[workflow guide](docs/terraform-ansible-workflow.md) for contracts and practice.

## Next implementation and acceptance work

- [ ] Run the workflow with the operator's screen reader, braille display or
  magnification setup. Record whether they can trace an output to its consumer,
  identify an override, understand a stop condition, and resume after interruption.
- [ ] Validate an authenticated nonproduction workplace workflow. Bind the actual
  backend/state version, account, region, commit, inventory, invocation and health
  checks; retain redacted compatibility fixtures and the tool versions used.
- [ ] Add workplace acquisition adapters for GitHub Actions, Spacelift and the
  custom deployment kit. Collect producer/invocation receipts from observed runs;
  keep missing evidence incomplete rather than inferring success from labels.
- [ ] Extend report provenance to the remaining producers. Their unbound legacy
  reports remain incomplete in `review-change` until migrated to the documented
  provenance/source contract.
- [ ] Extend consumer tracing beyond host variables to template/task references
  and additional runtime variable sources. Add explicit adapters and regressions
  for supported transformations; retain incomplete results for unresolved Jinja,
  shell transformations, roles, includes and ambiguous precedence.
- [ ] Evaluate authenticated or signed remote evidence for deployment approval.
  Current hashes and caller-supplied receipts establish local consistency, not
  authenticated execution or independent authority to deploy.

For each extension, preserve value redaction, exact host identity, freshness,
required coverage, and invalidation of saved reviews after source changes.
