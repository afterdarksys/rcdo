# Toolkit Configuration and AI Credentials

The toolkit loads optional user defaults from the operating system's user
configuration directory. Find the exact paths on the current machine with:

```sh
rcdo config path
```

Create the initial configuration with mode `0600`:

```sh
rcdo config init
```

The generated YAML contains runtime defaults, AI routing, and provider sections:

```yaml
version: "1"
credentials_file: credentials.json
audit:
  enabled: false
  file: audit.jsonl
  output: redacted
  max_output_bytes: 65536
defaults:
  format: text
  environment: unknown
  width: 100
commands:
  ai-assist:
    action: explain
    target: config
    domain: auto
    max-input-bytes: 262144
    timeout: 60
  decompose:
    from: auto
    to: aws
    format: text
    width: 100
  review:
    base: HEAD
    repo-policy: .rcdo/policy.yaml
  config-explain:
    syntax: auto
    values: true
  config-diff:
    syntax: auto
    values: true
ai:
  primary: openai
  backup: anthropic
  tertiary: openrouter
providers:
  openai:
    api_key_env: OPENAI_API_KEY
  anthropic:
    api_key_env: ANTHROPIC_API_KEY
  openrouter:
    api_key_env: OPENROUTER_API_KEY
    base_url: https://openrouter.ai/api/v1
```

## Precedence

Optional executable plugins are controlled by `plugins.enabled` and
`plugins.entries.NAME.enabled`. Both default to false; both must be true to run
a plugin. See [plugin configuration and authoring](plugins.md).

Options resolve in this order, from strongest to weakest:

1. An explicit command-line option.
2. The matching entry under `commands`.
3. The matching entry under `defaults`.
4. The command's built-in default.

Use a different configuration for one invocation:

```sh
rcdo config-explain --config-file team-config.yaml --input service.yaml
```

Or select one for the current shell:

```sh
export RCDO_CONFIG=/approved/path/rcdo.yaml
```

`GIT_TOOLS_CONFIG` and the former `git-tools` user configuration directory are
read as migration fallbacks when no RCDO configuration is present.

Examples of changing defaults safely:

```sh
rcdo config set --key defaults.environment --value production
rcdo config set --key commands.config-explain.width --value 60
rcdo config set --key commands.review.format --value json
```

Explicit options still win:

```sh
rcdo config-explain --input service.yaml --width 100
```

The newer operator commands also load defaults, including compressed-log reading,
Markdown conversion/viewing, context/resource/fleet reviews, incident/runbook
navigation, report reading, doctor, collect, changes, Kubernetes/Ansible reviews,
state navigation, network checks, tasks, and config-walk. Subcommands retain their
position before flags. For example:

```sh
rcdo config set --key commands.log-read.context --value 3
rcdo config set --key commands.log-read.format --value json
rcdo config set --key commands.report-read.layout --value braille
rcdo config set --key commands.to-markdown.table-mode --value records
rcdo config set --key commands.network-check.timeout --value 15s
rcdo config set --key commands.tasks.registry --value /path/to/tasks.json
```

Only supported defaults are accepted. Network targets, expected account identity,
native execution switches, evidence inputs/outputs, and workflow actions remain
explicit command-line choices. Saved readers retain their bound source and filters.

Decomposition aliases inherit settings under `commands.decompose`. For example:

```sh
rcdo config set --key commands.decompose.region --value us-east-1
rcdo config set --key commands.decompose.profile --value work-readwrite
```

## Audit logging

Enable local audit logging through the same configuration:

```sh
rcdo config set --key audit.file --value audit.jsonl
rcdo config set --key audit.output --value redacted
rcdo config set --key audit.max_output_bytes --value 65536
rcdo config set --key audit.enabled --value true
rcdo config path
```

A relative audit path resolves beside the configuration file. `config path` shows
the destination and whether logging is enabled. Older configs without an `audit`
section keep logging disabled. Enabling it takes effect on the next invocation;
an invocation that disables it still finishes its record in the previous log.

Each invocation writes two JSONL events with a shared `run_id`: `start` before
execution and `finish` afterward. Fields include the command, redacted supplied
arguments, effective arguments after defaults (on completion), config path,
OS username and UID, effective UID, hostname, PID, working directory, UTC RFC3339
timestamps, duration, command exit code, and separate stdout/stderr captures.
Identity describes the local OS account; shared service accounts do not identify
the individual human behind them. Nested native commands are represented through
the rcdo invocation and its output, not separate shell-process audit events.

`output: redacted` captures at most `max_output_bytes` per stream (default 65536;
maximum 1048576). Byte counts and truncation flags disclose omitted output. A
truncated final line is withheld. Output is redacted after capture so secrets
split across writes are still handled. Common secret assignments, bearer tokens,
AWS access keys and private-key material are filtered; sensitive argument values
and free-form `--value`, `--text`, and `--question` values are withheld. Redaction
cannot identify every arbitrary secret. Use `output: none` for metadata and byte
counts without retaining stdout/stderr. Stdin and environment contents are never
captured directly. Output written only to an artifact file is not copied into the
audit; the arguments retain the destination path. Terminal/pipeline output and
ordinary command exit codes remain unchanged when audit writes succeed.

Audit files are created with mode `0600`; symlinks, nonregular files and files
readable by other accounts are rejected. A short directory lock coordinates
concurrent appenders and each event is flushed to disk. If the start record
cannot be saved, the command does not execute (exit 2). If the finish record
cannot be saved, rcdo reports the original command status and returns 2; completed
actions are not rolled back. A start without a finish can mean interruption,
crash or logging failure and must not be interpreted as success.

Audit logging is a local operational record, not a tamper-proof central audit
service: the OS account can edit its files or select another configuration.
An unreadable/invalid configuration prevents execution but cannot provide a
usable audit destination. No automatic retention or rotation is performed; archive
or rotate the JSONL file when rcdo invocations are idle. If a process dies while
holding the short append lock, remove `<audit-file>.lock` only after confirming
no writer is active. Config changes and help/version invocations are logged when
they select an audit-enabled configuration.

For a local integration check, run `make build` followed by
`python3 scripts/config-audit-practice.py`. It uses a temporary config and gzip
fixture, exercises configured navigation and a command failure, and checks audit
record pairing from concurrent CLI processes without contacting cloud services.

## AI provider order

Configure the complete order atomically so no provider temporarily occupies two
slots:

```sh
rcdo config ai-order \
  --primary openai \
  --backup anthropic \
  --tertiary openrouter
```

The provider configuration may also specify `model` and `base_url`. The toolkit
selects the first slot with an available credential:

```sh
rcdo config ai-status
rcdo config ai-resolve
```

These commands never print an API key. `ai-resolve` performs no network request;
it verifies local routing and credential availability for AI-backed commands.

Each provider used by `ai-assist` must have a model. `base_url` is optional for
OpenAI and Anthropic and defaults to the provider's public API. OpenRouter's
generated configuration already includes its base URL.

```sh
rcdo config set --key providers.openai.model --value YOUR_APPROVED_MODEL
rcdo config set --key providers.anthropic.model --value YOUR_APPROVED_MODEL
rcdo config set --key providers.openrouter.model --value PROVIDER/MODEL
```

## Explicit AI assistance

AI requests are always opt-in. The `--ai` flag is required on every invocation
and is intentionally not a configurable default. A configuration file therefore
cannot silently enable network transmission to a model provider.

Use AI assistance on a structured configuration, an Ansible file, HCL, or a
bounded repository snapshot:

```sh
rcdo ai-assist --ai --action explain --target config --domain aws --input infrastructure.yaml
rcdo ai-assist --ai --action debug --target ansible --domain ansible --input playbook.yml
rcdo ai-assist --ai --action fix --target hcl --domain opentofu --input main.tf
rcdo ai-assist --ai --action debug --target repo --domain github-actions --repo .
```

Supported domains are `auto`, `aws`, `alicloud`, `terraform`, `opentofu`,
`docker`, `github-actions`, and `ansible`. The optional `--question` flag can
describe a specific symptom or error.

RCDO redacts common credential assignments, bearer tokens, AWS access-key IDs,
and private-key blocks before transmission. Repository mode excludes common
secret files and directories such as `.git`, `.terraform`, `node_modules`, and
`vendor`, then applies the configured byte limit. Redaction is defense in depth,
not a guarantee that arbitrary source contains no sensitive business data;
review provider and workplace data-handling policy before using `--ai`.
If a provider call fails, the same redacted context may be sent to the next
available provider in the configured backup order.

The `fix` action returns a proposed unified diff and validation/rollback steps.
It does not modify files automatically.

## Credential storage

Environment variables take precedence over stored credentials. To copy a key
from an environment variable into the credential store:

```sh
rcdo config credential-set \
  --provider openai \
  --from-env OPENAI_API_KEY
```

To avoid placing a key in shell history or a process argument, pipe it through
standard input:

```sh
secret-manager read openai/api-key |
  rcdo config credential-set --provider openai --stdin
```

Supported provider names are `openai`, `anthropic`, and `openrouter`.

```sh
rcdo config credential-remove --provider openai
```

The credential store is separate from the YAML configuration and must have mode
`0600`. The toolkit refuses to read it when group or other permission bits are
present. Its parent directory is created with mode `0700`.

The file is protected by operating-system file permissions but is not encrypted
at rest. Prefer environment variables or an approved secret manager when local
plaintext credential storage does not meet workplace policy. Never commit the
configuration directory or credential store to Git.
