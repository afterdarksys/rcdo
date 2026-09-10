#!/usr/bin/env python3
"""Synthetic, local RCDO practice. Runs no cloud commands or remediation."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
binary = repo / 'dist' / 'rcdo'
if not binary.is_file():
    raise SystemExit('Build first with: make build')
root = Path(tempfile.mkdtemp(prefix='rcdo-practice-'))
os.chmod(root, 0o700)
print(f'Synthetic practice artifacts: {root}', flush=True)
now = datetime.datetime.now(datetime.timezone.utc).isoformat().replace('+00:00', 'Z')
results = []


def write(name, value):
    path = root / name
    content = value if isinstance(value, str) else json.dumps(value, indent=2) + '\n'
    path.write_text(content)
    path.chmod(0o600)
    return str(path)


def run(label, expected, *args):
    result = subprocess.run([str(binary), *map(str, args)], capture_output=True, text=True, timeout=20)
    write(f'{len(results)+1:02d}-{label}.txt', result.stdout + result.stderr)
    results.append({'scenario': label, 'args': list(map(str, args)), 'expected_exit': expected,
                    'actual_exit': result.returncode})
    write('results.json', results)
    if result.returncode != expected:
        raise AssertionError(f'{label}: expected {expected}, got {result.returncode}\n{result.stdout}\n{result.stderr}')
    print(f'PASS {label}: exit {expected}', flush=True)
    return result.stdout


# Wrong account, then a controlled synthetic correction.
expected = write('expected.json', {'schema_version': '1', 'name': 'practice', 'contexts': {
    'cloud': {'kind': 'aws', 'values': {'account': '111111111111', 'region': 'us-east-1'}}}})
context = {'schema_version': '1', 'complete': True, 'contexts': {'cloud': {
    'kind': 'aws', 'values': {'account': '222222222222', 'region': 'us-east-1'},
    'source': 'synthetic fixture', 'outcome': 'pass', 'collected_at': now}}}
wrong = write('wrong-context.json', context)
run('wrong-account', 20, 'context-summary', '--expect', expected, '--input', wrong)
context['contexts']['cloud']['values']['account'] = '111111111111'
run('corrected-account', 0, 'context-summary', '--expect', expected, '--input', write('correct-context.json', context))

# Repeated failure, interruption, and rotated evidence.
logs = write('app.jsonl', '\n'.join(json.dumps({'timestamp': now, 'level': 'error',
    'resource': 'api', 'request_id': 'request-1', 'message': 'database unavailable'}) for _ in range(3)) + '\n')
text = run('grouped-logs', 0, 'log-read', '--input', logs, '--syntax', 'jsonl')
assert 'Count 3' in text
incident = root / 'incident.json'
run('incident-start', 0, 'incident', 'start', '--state', incident, '--title', 'Synthetic API incident')
run('incident-hypothesis', 0, 'incident', 'hypothesis', '--state', incident, '--text', 'Database unavailable')
run('incident-evidence', 0, 'incident', 'evidence', '--state', incident, '--name', 'logs', '--input', logs)
run('incident-next-action', 0, 'incident', 'next-action', '--state', incident, '--text', 'Inspect database health evidence')
assert 'Inspect database health evidence' in run('incident-resume', 0, 'incident', 'resume', '--state', incident)
write('app.jsonl', json.dumps({'timestamp': now, 'message': 'database healthy'}) + '\n')
run('incident-stale-evidence', 30, 'incident', 'handoff', '--state', incident)
run('incident-refresh-evidence', 0, 'incident', 'evidence', '--state', incident, '--name', 'logs', '--input', logs)

# Evidence-labelled relationship with an explicitly missing region.
graph = {'schema_version': '1', 'complete': True, 'collected_at': now, 'source': 'synthetic fixture',
         'scopes': [{'account': 'demo', 'region': 'us-east-1', 'complete': True, 'outcome': 'pass'}],
         'nodes': [{'id': name, 'type': kind, 'account': 'demo', 'region': 'us-east-1'}
                   for name, kind in [('api', 'service'), ('database', 'database')]],
         'edges': [{'from': 'api', 'to': 'database', 'kind': 'configuration', 'source': 'synthetic manifest'}]}
graph_path = write('graph.json', graph)
run('dependency', 10, 'resource-walk', '--input', graph_path, '--resource', 'api')
run('dependency-missing-region', 30, 'resource-walk', '--input', graph_path, '--resource', 'api', '--require-scope', 'demo/us-west-2')

# Incomplete rollout and service misconfiguration, then all required observations.
fleet = {'schema_version': '1', 'complete': True,
         'hosts': [{'id': h, 'platform': 'linux', 'baseline': 'web'} for h in ['web-1', 'web-2']],
         'baselines': {'web': {'platform': 'linux', 'source': 'synthetic baseline', 'collected_at': now,
                              'values': {'release': '2', 'service_running': True}}},
         'observations': [{'host': 'web-1', 'platform': 'linux', 'outcome': 'pass', 'source': 'synthetic probe',
                           'collected_at': now, 'values': {'release': '1', 'service_running': False}},
                          {'host': 'web-2', 'outcome': 'unreachable'}]}
report = run('partial-rollout', 30, 'fleet-check', '--input', write('partial-fleet.json', fleet), '--format', 'json')
report_path = write('fleet-report.json', report)
for layout in ['plain', 'speech', 'braille']:
    run(f'report-{layout}', 30, 'report-read', '--input', report_path, '--layout', layout)
for host in fleet['observations']:
    host.update(platform='linux', outcome='pass', source='synthetic probe', collected_at=now,
                values={'release': '2', 'service_running': True})
run('resolved-rollout', 0, 'fleet-check', '--input', write('resolved-fleet.json', fleet))

# Attempted/completed is distinct from independently supplied health evidence.
book = {'schema_version': '1', 'title': 'Synthetic recovery', 'targets': {'environment': 'practice'},
        'steps': [{'id': 'health', 'title': 'Check API health', 'instruction': 'Inspect the probe artifact',
                   'expected': 'API passes', 'stop_conditions': ['API unreachable'], 'checks': ['api']},
                  {'id': 'traffic', 'title': 'Check traffic', 'instruction': 'Inspect synthetic traffic',
                   'expected': 'Traffic normal', 'stop_conditions': [], 'checks': ['traffic']}]}
book_path = write('runbook.json', book)
progress = root / 'progress.json'
run('runbook-start', 0, 'runbook', 'start', '--input', book_path, '--state', progress)
for mode in ['read', 'attempt', 'complete']:
    run(f'runbook-{mode}', 0, 'runbook', mode, '--state', progress)
run('completion-not-verification', 2, 'runbook', 'next', '--state', progress)
verification = {'schema_version': '1', 'runbook_sha256': hashlib.sha256(Path(book_path).read_bytes()).hexdigest(),
                'step_id': 'health', 'targets': book['targets'], 'source': 'synthetic probe',
                'collected_at': now, 'complete': True, 'checks': [{'id': 'api', 'status': 'fail', 'evidence': 'probe-1'}]}
run('failed-health', 20, 'runbook', 'verify', '--state', progress, '--input', write('failed-check.json', verification))
verification['checks'][0].update(status='pass', evidence='probe-2')
passed = write('passed-check.json', verification)
run('verified-health', 0, 'runbook', 'verify', '--state', progress, '--input', passed)
run('advance-verified-step', 0, 'runbook', 'next', '--state', progress)
write('passed-check.json', 'changed\n')
run('verification-changed', 30, 'runbook', 'handoff', '--state', progress)

# Existing plan review: replacement and stale plan binding.
plan = {'format_version': '1.0', 'resource_changes': [{'address': 'aws_db_instance.demo', 'type': 'aws_db_instance',
        'change': {'actions': ['delete', 'create'], 'before': {'engine_version': '15'}, 'after': {'engine_version': '16'}}}]}
plan_path = write('plan.json', plan)
plan_report = write('plan-report.json', run('database-replacement', 20, 'plan-explain', '--input', plan_path, '--format', 'json'))
session = root / 'plan-session.json'
run('plan-session', 0, 'review-session', 'start', '--report', plan_report, '--session', session,
    '--change-id', 'PRACTICE', '--commit', 'a' * 40, '--artifact', plan_path)
write('plan.json', {'format_version': '1.0', 'resource_changes': []})
run('stale-plan', 30, 'review-session', 'resume', '--session', session)

write('README.md', '# Synthetic practice results\n\nAll artifacts are synthetic; no real recovery is asserted.\n'
      'See results.json and numbered transcripts for expected/actual exits.\n'
      'Repeat commands to practice navigation. Regenerate for fresh timestamps.\n'
      'Device usability is untested; use the operator pilot worksheet in the repository.\n')
print(f'Completed {len(results)} checks. Evidence retained at {root}')
