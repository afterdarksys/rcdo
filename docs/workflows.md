# Work Workflows

These examples are designed to be copied into local review scripts or CI. Review commands never deploy infrastructure.

## Install without administrator access

```sh
make install PREFIX="$HOME/.local"
```

Ensure `$HOME/.local/bin` is on `PATH`.

## Pull-request review

```sh
rcdo pr-manager inspect \
  --collect 42 \
  --repo OWNER/REPOSITORY \
  --expect-commit COMMIT_SHA \
  --environment production
```

Collection uses the installed GitHub CLI. Authentication or API failures produce exit code 30 (`INCOMPLETE`).

## OpenTofu review

```sh
tofu plan -out=review.tfplan
rcdo tofu-check \
  --plan review.tfplan \
  --environment production \
  --format json > tofu-review.json
```

The saved plan is converted using `tofu show -json`. The checker accepts compatible minor format additions and focuses on destructive actions, stateful resources, IAM changes, and public-access indicators.

## Spacelift review

```sh
rcdo spacelift-check \
  --stack production-network \
  --run RUN_ID \
  --expect-stack production-network \
  --expect-commit COMMIT_SHA \
  --environment production
```

This uses a fixed read-only `spacectl api` query for the actual run. Basic live collection lacks policy, approval and other operational evidence and therefore returns INCOMPLETE. Supply normalized snapshots for full review. See [expanded workflows](spacelift-iac.md) for schemas, binding, comparisons, watch and command generation.

## Cloud identity gate

```sh
rcdo cloud-context-check \
  --collect \
  --expect-cloud aws \
  --expect-account 111111111111 \
  --expect-region us-east-1 \
  --actual-region us-east-1 \
  --environment production
```

For Alicloud, use `--expect-cloud alicloud`. The collector invokes only STS caller-identity operations. Pass the active region explicitly because caller-identity responses do not contain it.

## GitHub Actions and Ansible

```sh
rcdo gha-tool check --input .github/workflows/deploy.yml --native
rcdo ansible-check --input playbooks/deploy.yml --native
```

`gha-tool --native` uses `actionlint` when installed. `ansible-check --native` uses `ansible-playbook --syntax-check`. A missing checker is reported as incomplete coverage.

## Policy exceptions

```sh
rcdo gha-tool check \
  --input .github/workflows/internal.yml \
  --policy examples/policy.json
```

Suppressions require an ID prefix, owner, reason, and future expiry date. Expired exceptions do not suppress findings. Suppression use is included in completed-check evidence.

## CI output

```sh
rcdo git-danger-check --input deploy.sh --format github
rcdo git-danger-check --input deploy.sh --format sarif > danger.sarif
```

GitHub format emits escaped workflow annotations. SARIF output uses version 2.1.0. GitHub Code Scanning availability depends on repository and organization licensing.

## End-to-end coverage gate

Generate each component report with `--format json`, place the paths in a
versioned change manifest, and run:

```sh
rcdo review-change --manifest review-change.json --format text
```

The command exits 30 when any required report is absent, unreadable, invalid,
or already contains incomplete checks. Keep `deployment-kit` in
`required_components` even before an adapter exists: its missing report is an
intentional fail-closed signal, not a reason to silently omit that coverage.

## Daily repository review

Review committed branch changes:

```sh
rcdo review \
  --base origin/main \
  --head HEAD \
  --environment production \
  --repo-policy .rcdo/policy.yaml
```

Omit `--head` to include staged, unstaged, and untracked working-tree files.
Collection invokes only read-only Git commands. Missing evidence makes the
result incomplete.

Save JSON output and start a resumable review:

```sh
rcdo review --base origin/main --head HEAD --format json > review.json
rcdo review-session start \
  --report review.json \
  --change-id PR-42 \
  --commit COMMIT_SHA
rcdo review-session next
```

## Safe configuration mutation

Preview first, then repeat the exact command with `--write --expect-sha256 REVIEWED_SHA256` using the source hash printed by the preview:

```sh
rcdo config-set --input environments/production.yaml --path api.replicas --value 4
rcdo config-set --input environments/production.yaml --path api.replicas --value 4 --write --expect-sha256 REVIEWED_SHA256
```

JSON values support booleans, numbers, null, arrays, and objects. Use `--string`
for literal text. YAML and TOML documents are re-encoded; HCL is rewritten with
its syntax-aware formatter. Use `config-diff` to review the semantic result and
Git to inspect presentation changes.

Expanded roadmap: [ROADMAP.md](../ROADMAP.md); guarded writes require the SHA-256 from the preview.
