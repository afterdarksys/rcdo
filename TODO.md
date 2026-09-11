# RCDO next steps

Updated September 11, 2026 after workplace workflow plumbing and implementation.

The six audited false-clean/false-ready defects are fixed. `workflow-check` now
checks explicit output-to-input mappings, extra-vars overrides, source identity,
artifact freshness, invocation inputs, callback results, and required outcome
checks. Full tests, race tests, vet, the build, and native localhost workflows
with both Terraform and OpenTofu passed. See the
[workflow guide](docs/terraform-ansible-workflow.md) for contracts and practice.

## Next implementation and acceptance work

- [x] Add an explicit nonproduction runner that records producer state lineage/
  serial, output-to-inventory conversion, resolved inputs, callback events and
  independent outcome observations. Add a portable exporter and runnable local
  integration practice with Terraform and OpenTofu.
- [x] Add the eight-task workflow operator acceptance suite with persistent,
  hash-bound observations. Automated practice leaves operator acceptance pending.
- [x] Fix top-level help for incident, runbook and state navigation, and remove
  tab alignment from receipt-review help; include every alias in the help audit.
- [x] Add GitHub run/artifact acquisition and Spacelift run validation around
  pinned exports, plus the custom-kit executable protocol and reference adapter.
- [x] Trace literal task/includes, local roles, variable aliases and templates;
  bind source graphs into workflow reviews and exports. Dynamic/runtime sources
  retain explicit gaps.
- [x] Extend common report provenance to named primary and secondary inputs;
  bind native validation/Kubernetes sources and reject unsupported provenance
  claims. Preserve legacy unbound reports as incomplete.

- [ ] Run the workflow with the operator's screen reader, braille display or
  magnification setup. Record whether they can trace an output to its consumer,
  identify an override, understand a stop condition, and resume after interruption.
- [ ] Validate an authenticated nonproduction workplace workflow. Bind the actual
  backend/state version, account, region, commit, inventory, invocation and health
  checks; retain redacted compatibility fixtures and the tool versions used.
- [ ] Connect the employer's authenticated identity, inventory, health and export
  commands to the supplied recorder/adapter contracts; retain compatibility
  fixtures from a real nonproduction run. No private deployment-kit API is assumed.
- [ ] Extend supported runtime transformations and variable precedence using
  concrete workplace fixtures. Unresolved Jinja, role metadata, shell transforms
  and ambiguous precedence remain incomplete.
- [ ] Evaluate authenticated or signed remote evidence for deployment approval.
  Current hashes and caller-supplied receipts establish local consistency, not
  authenticated execution or independent authority to deploy.

For each extension, preserve value redaction, exact host identity, freshness,
required coverage, and invalidation of saved reviews after source changes.
See [workplace implementation and acceptance](docs/workplace-workflow.md).
