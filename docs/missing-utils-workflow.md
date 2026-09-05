# Accessible readiness and change review

Missing Utils collects observations. RCDO turns them into accessible findings,
required-component reviews, and resumable reading sessions. This workflow is
local and advisory: it does not execute deployments or certify their safety.

## Reproduce the demonstration

From the sibling Missing Utils checkout, build the probe and RCDO:

```sh
cd ../ads-missing-utils
make dist/jsonprobe
make -C ../rcdo build
python3 scripts/demo-rcdo.py
```

Pass `--rcdo /path/to/rcdo` or `--jsonprobe /path/to/jsonprobe` to use other
binaries. Pass `--output /path/to/new-directory` to retain evidence at a chosen
location. The script also retains its default temporary evidence directory and
prints its location. It requires Python 3 for orchestration; the toolkits remain
Go binaries.

The demo uses a local file probe and synthetic OpenTofu plan fixtures. It checks:

1. Missing required readiness check: INCOMPLETE, exit 30.
2. No-resource-change fixture plus a passing probe: CLEAN, exit 0.
3. Database deletion fixture: BLOCKED, exit 20.
4. Reading every finding: still BLOCKED, exit 20.
5. Changing the bound plan: stale session, INCOMPLETE, exit 30.

It checks the generated plain-text output at 72 columns and saves an evidence
manifest. No cloud authentication, AI provider, or deployment command is used.
`fixture-only` is a demo commit label, not a verified repository revision.

## Use actual readiness evidence

Create a specification with stable, unique names:

```json
{"checks":[{"name":"api-health","type":"http","url":"https://service.example/health","status":200,"timeout":"5s"}]}
```

Save aggregate output even when a probe fails. Handle producer and consumer exit
codes separately; an empty or truncated pipe must never become a clean review.
For Bash:

```sh
probe_status=0
./dist/jsonprobe --input probes.json > readiness-source.json || probe_status=$?
case "$probe_status" in
  0|1) ;; # successful collection or a reported failed probe
  *) printf 'Collection failed: %s\n' "$probe_status" >&2; exit "$probe_status" ;;
esac

../rcdo/dist/rcdo jsonprobe-check \
  --input readiness-source.json --require api-health \
  --environment staging --max-age 15m --format json > readiness-review.json
```

`jsonprobe-check` returns 0 for passing required checks, 20 for failures, 30 for
missing, inconsistent, unsupported, or stale evidence, and 2 for invalid CLI
arguments. Repeated `--require` flags declare required names. It accepts the
aggregate `jsonprobe --format json` envelope, not NDJSON records. Older aggregate
results lacking collection time or version need to be recollected.

The timestamp records when collection began, so long-running probes consume the
freshness window. Producer version and collection time are observational metadata,
not signed attestations. A timestamp more than one minute in the future is
incomplete. Failure diagnostics stay in the source artifact because upstream
errors may contain URLs or credentials; the accessible report includes its hash
and names each check. Protect source artifacts using your normal access controls.

## Combine and resume

Add `readiness` to the required components and report paths of an RCDO
`review-change` manifest alongside `opentofu`, cloud context, and other required
workplace checks. Missing required components remain INCOMPLETE.

Create a session from the resulting versioned report:

```sh
rcdo review-session start \
  --report /path/to/reports/review.json \
  --session /path/to/reports/session.json \
  --change-id PR-42 --commit HEAD --repo /path/to/clean/repository \
  --artifact /path/to/reports/plan.json \
  --artifact /path/to/policy.yaml --max-age 8h
rcdo review-session next --session /path/to/reports/session.json --width 72
rcdo review-session acknowledge --session /path/to/reports/session.json \
  --id FINDING-ID --note "Read; follow-up assigned to the service owner."
```

With `--repo`, the revision must resolve to current HEAD and the repository must
be clean. Store the session outside that repository. Every status, next, and ack
checks report bytes, bound artifact bytes, session expiry, and (when bound) HEAD
and working-tree cleanliness. Bind ignored inputs such as generated plans and
variable files explicitly with `--artifact`; Git cleanliness does not cover them.
Without `--repo`, the commit is clearly presented as a supplied label.

Start and acknowledgement return 0 when the operation succeeds. Status and next
return the underlying report status (0/10/20/30), including after reading is
complete. Stale sessions return 30 and refuse acknowledgements. Acknowledgement
never clears a finding or incomplete check. Start refuses to overwrite a session;
use a new path after regenerating evidence. Legacy schema-1 sessions must be
recreated because they did not preserve full report coverage.

## What is and is not verified

The readiness adapter validates required names, check statuses, supported schemas,
and collection age. It does not authenticate the collector, establish cloud
account identity, verify that a named check targets the intended endpoint, or
prove that the report was generated from the supplied commit or plan. Session
fingerprints detect changes after binding; they do not establish those upstream
relationships or observe live drift. Session expiry is measured from session
creation and does not refresh collector evidence. Recollect and re-review before
an actual deployment. The local session is a single-operator record, not a signed
approval database; do not share it for concurrent acknowledgements.

For workplace acceptance, run the demo with your actual terminal, screen reader,
and magnification settings. Confirm that you can locate blockers, distinguish
incomplete evidence, resume reading, and identify the next action. Automated
line checks do not replace that usability check.
