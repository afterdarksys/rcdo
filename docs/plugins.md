# Writing and configuring RCDO plugins

RCDO supports explicitly registered executable plugins in any language. Plugins
run as separate processes and return the standard RCDO report. No recompilation
of RCDO is needed. Existing configurations have plugin support disabled.

## Configuration switches

Run `rcdo config init` if you do not already have a configuration. Register the
[working Python example](../examples/plugins/line-count.py) using its absolute
path; make it executable with `chmod +x` after copying it.

```sh
rcdo config set --key plugins.entries.line-count \
  --value '{"enabled":false,"path":"/absolute/path/to/line-count.py","description":"Count input lines"}'
rcdo config set --key plugins.entries.line-count.enabled --value true
rcdo config set --key plugins.enabled --value true
rcdo plugin list
printf 'first line\nsecond line\n' | rcdo plugin run --name line-count
```

The corresponding YAML is:

```yaml
plugins:
  enabled: true
  entries:
    line-count:
      enabled: true
      path: /absolute/path/to/line-count.py
      description: Count input lines
      timeout_seconds: 30
      max_output_bytes: 1048576
```

Both switches must be true. The global switch takes precedence over every
individual switch. Disable one plugin or all plugins:

```sh
rcdo config set --key plugins.entries.line-count.enabled --value false
rcdo config set --key plugins.enabled --value false
```

Changes apply on the next invocation; disabling does not cancel an already
running process. `plugin list` works while disabled and never executes plugins.
`plugin list --format json` returns configured entries and the global switch.
Text output also identifies each plugin's effective enabled state.

Use `--config-file PATH` or `RCDO_CONFIG` to select a configuration. Plugin
paths must be absolute; no PATH discovery, automatic loading or downloads occur.
Names contain lowercase letters, digits and hyphens and start with a letter.
Plugins cannot replace built-in commands: invoke them through `plugin run`.

## Executable protocol, version 1

RCDO invokes exactly:

```text
/configured/executable rcdo-plugin-v1
```

Standard input is one UTF-8 JSON request:

```json
{
  "schema_version": "1",
  "plugin": "line-count",
  "args": [],
  "input": "first line\nsecond line\n"
}
```

`input` comes from `--input FILE` or stdin and is limited to 1 MiB of UTF-8 text.
Use an empty pipe or `/dev/null` for plugins that need no input. Arguments after
`--` become the `args` array and are not interpreted by a shell or as RCDO flags:

```sh
rcdo plugin run --name my-check --input evidence.json --format json -- --scope staging
```

On successful protocol handling, exit zero and write exactly one report to
stdout. Status values are lowercase: `clean`, `review`, `blocked`, `incomplete`.

```json
{
  "schema_version": "1",
  "status": "clean",
  "findings": [],
  "completed_checks": ["Describe the check actually completed"],
  "incomplete_checks": []
}
```

At least one completed or incomplete check is required. Findings follow the
existing `finding.Finding` JSON contract and determine report severity. Missing
evidence belongs in `incomplete_checks`; do not return clean for an unsupported
operation. RCDO validates the report and its declared status, prefixes finding
IDs with the plugin name, discards plugin-supplied provenance, and renders text,
JSON, GitHub annotations or SARIF. Plugin reports therefore do not impersonate
built-in checkers or supply trusted provenance to `review-change`.

Nonzero process exits, timeouts, excess output and malformed/inconsistent reports
produce an incomplete review (exit 30). Report status determines RCDO's normal
exit codes: 0 clean, 10 review, 20 blocked, 30 incomplete. Disabled/unregistered
plugins and invalid configuration return 2 without executing the plugin.

Timeout defaults to 30 seconds, maximum 300. Standard output defaults to 1 MiB,
maximum 16 MiB; stderr is capped at 64 KiB. Zero configured limits select defaults.
Diagnostics are withheld from reports. The runner caps retained output, terminates
the direct process on timeout, and bounds waiting for inherited pipes. A timeout
does not establish whether an external operation completed or stop every descendant.

Plugins are trusted local programs, not sandboxed code. They inherit the current
working directory, environment and OS permissions, and may access files or the
network. Register only executables you intend to run. Report validation checks
structure and consistency, not whether a plugin's claims are true. Plugin authors
must withhold sensitive values from reports. RCDO's existing optional audit log
records the invocation and rendered result; the request input is not logged.

## Adapting shell2see

Use the example's request/response structure in a wrapper around a selected
shell2see command. Translate its actual observations into findings or completed
checks, and report unavailable tools or unsupported operations as incomplete.
Register the wrapper under `plugins.entries.shell2see`. Registration does not
automatically enable or alter the existing shell2see toolkit. Interactive terminal
applications need a wrapper that can produce a finite JSON report; raw terminal
output is not the plugin protocol.
