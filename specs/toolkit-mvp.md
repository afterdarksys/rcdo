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

## Commands

- `git-diff-walker`: navigate and search unified diffs.
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
