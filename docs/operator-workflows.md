# Operator workflow extensions

## Ansible rollout reading

Enable the optional callback for a playbook you choose to run:

```sh
export ANSIBLE_CALLBACK_PLUGINS=/path/to/rcdo/integrations/ansible/callback_plugins
export ANSIBLE_CALLBACKS_ENABLED=rcdo
export RCDO_ANSIBLE_EVENTS=/path/to/new-run.jsonl
# Run your chosen ansible-playbook command separately.
rcdo ansible-watch --input /path/to/new-run.jsonl --require-host web-1 --require-host web-2
```

The callback follows Ansible's [callback plugin interface](https://docs.ansible.com/projects/ansible-core/2.16/dev_guide/developing_plugins.html).
It exclusively creates a 0600 JSONL file, records a UUID start, sequential task
results and a finish. Recording failure suppresses finish. Result bodies, variables,
module arguments and loop items are never recorded; no_log also withholds task names.
Host names and task UUIDs remain necessary scope references. Task-level check-mode
overrides are retained. Callback events are operator-supplied evidence, not signed
execution attestation. Ansible may report callback errors without stopping a playbook.

The reader requires explicit expected host names, supports `--expect-run`, and
reviews a finite saved artifact (not a continuous stream). Missing host results,
unreachable hosts, a missing finish or stale last observation return incomplete.
Failed tasks remain high risk even when ignored. Check-mode changes are predictions;
skipped tasks do not establish module check-mode support. Counts represent task
results, not unique hosts. Default freshness is 24h; max 16 MiB, 100000 records,
64 KiB per line. `--format json` feeds existing `report-read` and `review-session`
for finding navigation/bookmarks. RCDO never launches Ansible itself.

Run callback contract tests with `python3 integrations/ansible/test_callback.py`.
Actual Ansible-version and workplace-device acceptance remain separate.
