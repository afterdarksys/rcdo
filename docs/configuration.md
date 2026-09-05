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

Decomposition aliases inherit settings under `commands.decompose`. For example:

```sh
rcdo config set --key commands.decompose.region --value us-east-1
rcdo config set --key commands.decompose.profile --value work-readwrite
```

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
