#!/bin/sh
# Credential-free, synthetic demonstration. Never executes generated commands.
set -eu
RCDO_REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
RCDO_BIN="$RCDO_REPO/dist/rcdo"
RCDO_DEMO=$(mktemp -d "${TMPDIR:-/tmp}/rcdo-iac-demo.XXXXXX")
if [ ! -x "$RCDO_BIN" ]; then
  echo "Build first with: make build" >&2
  exit 2
fi
python3 - "$RCDO_DEMO" <<'PY'
import datetime, hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
def write(name, value):
    data = (json.dumps(value, indent=2) + '\n').encode()
    (root / name).write_bytes(data)
    return data
now = datetime.datetime.now(datetime.timezone.utc).isoformat().replace('+00:00', 'Z')
plan = {'format_version': '1.0', 'resource_changes': [
    {'address':'aws_db_instance.main','type':'aws_db_instance',
     'action_reason':'replace_because_cannot_update',
     'change':{'actions':['delete','create'], 'replace_paths':[['engine_version']],
               'before':{'engine_version':'15'}, 'after':{'engine_version':'16'},
               'after_unknown':{'endpoint':True}}}]}
plan_bytes = write('plan.json', plan)
write('old-plan.json', {'format_version':'1.0', 'resource_changes':[]})
s = {'schema_version':'1', 'account':'demo', 'stack_id':'production-network',
     'run_id':'run-123', 'commit_sha':'a'*40, 'run_type':'TRACKED',
     'state':'UNCONFIRMED', 'collected_at':now,
     'source':'synthetic demonstration, not live evidence', 'latest_run_id':'run-123',
     'policies':[{'id':'protect-data','type':'PLAN','decision':'deny'}],
     'approval':{'satisfied':False,'outstanding':['Database owner approval']},
     'dependencies':[], 'downstream':['application'],
     'drift':{'enabled':True,'last_success':now,'detected':False,'reconcile':False},
     'plan':{'json_sha256':hashlib.sha256(plan_bytes).hexdigest(),
             'run_id':'run-123','commit_sha':'a'*40,'source':'synthetic fixture generation'}}
write('run.json', s)
write('runs.json', {'schema_version':'1','complete':True,'runs':[s]})
PY
expect_status() {
  RCDO_EXPECTED=$1
  shift
  RCDO_STATUS=0
  "$@" || RCDO_STATUS=$?
  if [ "$RCDO_STATUS" -ne "$RCDO_EXPECTED" ]; then
    echo "Unexpected exit: $RCDO_STATUS; expected $RCDO_EXPECTED" >&2
    exit 1
  fi
}
expect_status 20 "$RCDO_BIN" plan-explain --input "$RCDO_DEMO/plan.json" --format json > "$RCDO_DEMO/review.json"
expect_status 0 "$RCDO_BIN" review-session start --report "$RCDO_DEMO/review.json" --session "$RCDO_DEMO/session.json" --change-id DEMO --commit aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa --artifact "$RCDO_DEMO/plan.json"
expect_status 20 "$RCDO_BIN" review-session resume --session "$RCDO_DEMO/session.json" --width 72
expect_status 20 "$RCDO_BIN" spacelift-check --input "$RCDO_DEMO/run.json" --plan-json "$RCDO_DEMO/plan.json" --width 72
expect_status 20 "$RCDO_BIN" plan-diff --before "$RCDO_DEMO/old-plan.json" --after "$RCDO_DEMO/plan.json" --width 72
expect_status 10 "$RCDO_BIN" spacelift-runs --input "$RCDO_DEMO/runs.json" --width 72
expect_status 0 "$RCDO_BIN" command-gen --to spacelift --input "$RCDO_REPO/examples/spacelift-iac/stack-command.yaml" --action logs
expect_status 0 "$RCDO_BIN" command-gen --to aws --input "$RCDO_REPO/examples/spacelift-iac/main.tf" --region us-east-1
expect_status 0 "$RCDO_BIN" command-gen --to terraform --input "$RCDO_REPO/examples/spacelift-iac/main.tf" --action plan
echo "Synthetic demo complete. Artifacts: $RCDO_DEMO"
