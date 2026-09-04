# Accessible DevOps Toolkit MVP

## Audience

Blind and low-vision infrastructure operators using screen readers, large print, or high magnification, especially in teams without established accessible operational practices.

## Safety contract

1. Review operations are offline-first and accept explicit files or standard input.
2. Missing evidence produces `INCOMPLETE`, never `CLEAN`.
3. Every finding states severity, target, action, environment, reason, evidence, confidence, and remediation.
4. Text output does not require color, tables, cursor positioning, or mouse interaction.
5. Common review output is available as deterministic JSON.
6. Exit codes distinguish clean, review, blocked, incomplete, and invalid-input outcomes.
7. File mutation requires `--write`; GitHub mutation requires `--execute`; previews are the default.
8. Secrets matching common assignment forms are redacted from scanner evidence.
9. Help is linear and labeled without tab-based columns.
10. Static heuristics identify risk but never claim to prove a deployment safe.
11. Structured configuration can be read as complete paths without depending on indentation or delimiter matching.
12. Values at likely secret-bearing paths are redacted unless disclosure is explicitly requested.
13. User configuration follows CLI-over-command-over-global precedence.
14. AI keys never appear in command arguments or status output; stored keys require mode `0600`.
15. Primary, backup, and tertiary AI routing is explicit and locally inspectable without a network request.
16. Remote AI assistance requires `--ai` on every invocation; configuration cannot enable it implicitly.
17. AI input is bounded and redacted before transmission, and AI-proposed fixes never write files.

## Commands

- `rcdo diff-walk`: navigate and search unified diffs.
- `config-explain`: linearize JSON, YAML, TOML, and HCL into labeled paths.
- `config-diff`: compare structured documents by path with redacted before-and-after values.
- `review`: orchestrate a repository change review from Git evidence.
- `repo-policy-check`: enforce owned critical paths, companion files, and content rules.
- `ops-policy-check`: detect dangerous cloud, IAM, network, storage, and workload settings.
- `config-set` and `config-remove`: preview and atomically apply path-based configuration changes.
- `review-session`: resume findings and record explicit, timestamped acknowledgements.
- `error-explain`: narrate common operational failures with redacted evidence and next actions.
- `config`: manage user defaults, provider order, and protected API-key storage.
- `ai-assist`: explicitly request model-backed explanation, debugging, or a proposed fix for configuration or a repository snapshot.
- `git-danger-check`: detect dangerous operational commands.
- `git-isimportant-check`: identify changes to configured critical paths.
- `git-update-json`: preview or atomically apply deep JSON updates.
- `gha-tool`: check, normalize, fix safe formatting, and apply templates to GitHub Actions workflows.
- `tofu-check`: review OpenTofu plan JSON.
- `spacelift-check`: verify normalized Spacelift run snapshots.
- `ansible-check`: detect common dangerous Ansible patterns.
- `pr-manager`: inspect, preview, create, and update pull requests.
- `deploy-review`: aggregate component finding reports.
- `cloud-context-check`: prevent wrong-cloud, account, or region changes.
- `runbook-check`: require rollback, validation, ownership, scope, and stop conditions.
- `review-brief`: produce narrow, low-density review output.
- `a11y-output-check`: test command output accessibility.
- `evidence-pack`: hash reviewed artifacts for audit handoff.
