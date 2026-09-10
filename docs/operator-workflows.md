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

## Terraform/OpenTofu state navigation

```sh
# Export state yourself; show JSON can contain secrets. Protect this source file.
rcdo state-walk start --input state-show.json --state navigation.json
rcdo state-walk goto --state navigation.json --address 'module.api.aws_instance.web["blue"]'
rcdo state-walk child --state navigation.json --index 1
rcdo state-walk bookmark --state navigation.json --name attributes
rcdo state-walk goto --state navigation.json --name attributes
```

Accepts [state show JSON format 1](https://opentofu.org/docs/internals/json-format/),
not raw tfstate or plan JSON. No infrastructure CLI is invoked. `parent`, `child`,
`next`, `previous`, `find --query` and `goto --id|--address|--name` navigate exact
identities. Root ID is `module:`, resources use `resource:ADDRESS`, and attributes
append `#` followed by escaped JSON-pointer segments. Search excludes values.

Sensitivity masks and sensitive field names redact values in both text and JSON.
Missing/invalid resource sensitivity metadata withholds all its attributes and
returns incomplete. Missing output sensitivity also withholds its value. This
cannot detect every unmarked secret. Persisted navigation stores only paths,
source hash, cursor and bookmarks. Any source byte change blocks further navigation
with 30; start a new file to review the new version. Limits: 16 MiB source, 50000
nodes, depth 64, 1000 bookmarks, and 4096-byte scalar display limit. Output is
bounded with `--limit`; snapshot values do not establish current service health.

## Network investigation

```sh
rcdo network-check --url https://service.example/health --timeout 10s --expect-status 200
```

This command contacts the explicit endpoint. It resolves DNS, attempts at most eight
addresses, connects TCP, verifies TLS chain/hostname for HTTPS, then issues HTTP HEAD
on that same socket. Default total deadline is 10 seconds (maximum 60). A failure
stops dependent layers, which remain visibly unattempted. Earlier failed addresses
are not hidden by a later successful connection; other backends are not certified.

No redirects or environment proxies are followed. URLs with userinfo, query strings,
fragments or control characters are rejected. No authorization, cookies, custom
headers or request bodies are sent. Response headers/bodies and raw diagnostic text
are withheld; response headers are limited to 64 KiB. A HEAD request can still reach
server handlers; this is an explicit diagnostic, not an offline parser.

TLS uses the platform trust store and hostname verification; there is no insecure
bypass. `--min-valid-for` defaults to 24h for a leaf-certificate expiry warning.
HTTP status defaults to expected 200 and is configurable. HTTP success does not
prove application dependencies work. Tests use local HTTP/TLS servers, controlled
DNS failure and a silent-server deadline check. The implementation uses Go's
[HTTP transport](https://pkg.go.dev/net/http) and [TLS verification](https://pkg.go.dev/crypto/tls).

## Task switching

```sh
rcdo tasks add --registry tasks.json --name api --kind incident --input incident.json
rcdo tasks add --registry tasks.json --name deploy --kind review --input review-session.json
rcdo tasks list --registry tasks.json
rcdo tasks resume --registry tasks.json --name api
rcdo tasks remove --registry tasks.json --name api
```

Supported kinds: incident, review, runbook and state (state-walk navigation).
The registry stores absolute workflow paths and a logical identity hash. It accepts
normal progress edits but detects replacement with a different incident, review or
source binding. This is change detection, not authenticated provenance. Listing and
resuming recheck the workflow/evidence, preserve full risk status, show the saved
position and next action, and never advance or acknowledge the underlying workflow.
Incident resume also identifies the selected timeline entry, even when recent
history is displayed. Registry operations never execute workflow instructions.

Names are explicit; there is no recursive filesystem search or global task index.
A missing registry lists as empty. Add refuses duplicate names; remove deletes only
the registry entry. Missing/replaced/invalid workflow files remain incomplete.
Reviews can perform their existing local Git freshness checks. Registry updates use
optimistic guarded atomic replacement; concurrent reads may need repeating. Maximum
100 tasks, 16 MiB per state file, 128 KiB resume details. JSON output includes status,
position, next action and individual gaps. An add can return 20/30 because the newly
registered workflow has risk/gaps; registration itself can still have succeeded.

## Practice

After `make build`, run `python3 scripts/operator-workflows-practice.py`. It creates
synthetic Ansible receipts, a sensitive state fixture and all four task kinds,
then checks navigation, redaction, missing evidence and task continuity. Network
checks contact only a temporary localhost HTTP server. Numbered transcripts and
expected/actual exits remain in a private temporary directory. No cloud credentials,
remote host access or infrastructure changes are involved.
