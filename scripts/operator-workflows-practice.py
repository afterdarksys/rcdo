#!/usr/bin/env python3
"""Synthetic RCDO operator practice; network requests reach localhost only."""
import datetime
import http.server
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading

repo = Path(__file__).resolve().parents[1]
binary = repo / 'dist' / 'rcdo'
if not binary.is_file():
    raise SystemExit('Run make build first')
root = Path(tempfile.mkdtemp(prefix='rcdo-operator-practice-'))
root.chmod(0o700)
now = datetime.datetime.now(datetime.timezone.utc).isoformat().replace('+00:00', 'Z')
results = []

def write(name, value):
    path = root / name
    path.write_text(value if isinstance(value, str) else json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)
    return path

def run(label, expected, *args):
    p = subprocess.run([str(binary), *map(str, args)], capture_output=True, text=True, timeout=20)
    write(f'{len(results)+1:02d}-{label}.txt', p.stdout + p.stderr)
    results.append({'scenario':label, 'expected_exit':expected, 'actual_exit':p.returncode, 'args':list(map(str,args))})
    write('results.json', results)
    if p.returncode != expected:
        raise AssertionError(f'{label}: expected {expected}, got {p.returncode}\n{p.stdout}\n{p.stderr}')
    print(f'PASS {label}: {expected}', flush=True)
    return p.stdout

records = [dict(event='start', check_mode=True),
           dict(event='result', host='web', task_id='t1', task='Synthetic prediction', outcome='changed', check_mode=True),
           dict(event='result', host='web', task_id='t2', task='hidden title', outcome='failed', check_mode=False, no_log=True, ignored=True),
           dict(event='finish')]
for i, record in enumerate(records):
    record.update(schema_version='1', run_id='synthetic-run', sequence=i, at=now)
callback = write('events.jsonl', '\n'.join(json.dumps(r) for r in records) + '\n')
report = run('ansible-outcomes', 20, 'ansible-watch', '--input', callback, '--require-host', 'web', '--format', 'json')
assert 'hidden title' not in report
report_path = write('report.json', report)
run('ansible-missing-host', 30, 'ansible-watch', '--input', callback, '--require-host', 'missing')
partial = write('partial.jsonl', '\n'.join(json.dumps(r) for r in records[:-1]) + '\n')
run('ansible-missing-finish', 30, 'ansible-watch', '--input', partial, '--require-host', 'web')

source = write('state-show.json', {'format_version':'1.0','values':{'root_module':{'resources':[
    {'address':'aws_instance.demo','values':{'name':'demo','password':'secret-value','revision':9007199254740993},'sensitive_values':{}}]}}})
nav = root / 'navigation.json'
run('state-start', 0, 'state-walk', 'start', '--input', source, '--state', nav)
text = run('state-redaction', 0, 'state-walk', 'goto', '--state', nav, '--id', 'resource:aws_instance.demo#/password', '--format', 'json')
assert 'secret-value' not in text and 'REDACTED' in text
run('state-bookmark', 0, 'state-walk', 'bookmark', '--state', nav, '--name', 'credential-field')
run('state-parent', 0, 'state-walk', 'parent', '--state', nav)
run('state-return', 0, 'state-walk', 'goto', '--state', nav, '--name', 'credential-field')

incident = root / 'incident.json'
run('incident-start', 0, 'incident', 'start', '--state', incident, '--title', 'Synthetic rollout incident')
run('incident-next-action', 0, 'incident', 'next-action', '--state', incident, '--text', 'Inspect failed host task')
review = root / 'review.json'
run('review-start', 0, 'review-session', 'start', '--session', review, '--report', report_path, '--change-id', 'PRACTICE', '--commit', 'a'*40)
book = write('book.json', {'schema_version':'1','title':'Synthetic recovery','targets':{'environment':'practice'},'steps':[
    {'id':'health','title':'Read probe','instruction':'Inspect health evidence','expected':'Healthy','checks':['health'],'stop_conditions':[]}]})
progress = root / 'progress.json'
run('runbook-start', 0, 'runbook', 'start', '--input', book, '--state', progress)
registry = root / 'tasks.json'
for name, kind, path, expected in [('incident','incident',incident,0),('state','state',nav,0),('book','runbook',progress,0),('review','review',review,20)]:
    run('register-'+name, expected, 'tasks', 'add', '--registry', registry, '--name', name, '--kind', kind, '--input', path)
before = incident.read_bytes()
run('resume-incident', 0, 'tasks', 'resume', '--registry', registry, '--name', 'incident')
assert incident.read_bytes() == before
run('list-preserves-review-risk', 20, 'tasks', 'list', '--registry', registry)
source.write_text(source.read_text() + '\n')
run('state-source-changed', 30, 'state-walk', 'show', '--state', nav)
run('registry-retains-stale-state', 30, 'tasks', 'list', '--registry', registry)

class Handler(http.server.BaseHTTPRequestHandler):
    def do_HEAD(self):
        self.send_response(204)
        self.end_headers()
    def log_message(self, *args):
        pass

server = http.server.HTTPServer(('127.0.0.1', 0), Handler)
thread = threading.Thread(target=server.serve_forever, daemon=True)
thread.start()
try:
    target = f'http://127.0.0.1:{server.server_port}/health'
    run('network-local-health', 0, 'network-check', '--url', target, '--expect-status', '204')
    run('network-status-mismatch', 20, 'network-check', '--url', target, '--expect-status', '200')
finally:
    server.shutdown()
    server.server_close()
    thread.join()
print(f'{len(results)} checks passed. Synthetic evidence retained at {root}')
