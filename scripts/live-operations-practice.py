#!/usr/bin/env python3
"""Exercise RCDO with synthetic CLI adapters; no cloud requests are made."""
import datetime
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

repo = Path(__file__).resolve().parents[1]
binary = repo / 'dist' / 'rcdo'
if not binary.is_file():
    raise SystemExit('Run make build first')
root = Path(tempfile.mkdtemp(prefix='rcdo-live-practice-'))
root.chmod(0o700)
now = datetime.datetime.now(datetime.timezone.utc).isoformat().replace('+00:00', 'Z')
results = []

def write(name, value):
    path = root / name
    path.write_text(value if isinstance(value, str) else json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)
    return path

# These adapters exist only inside the child processes' PATH for this practice.
adapter = '#!' + sys.executable + '''
import json, pathlib, sys
name=pathlib.Path(sys.argv[0]).name
args=sys.argv[1:]
if name=='aws':
    if args[:2]==['sts','get-caller-identity']:
        print(json.dumps({'Account':'111111111111','Arn':'arn:aws:iam::111111111111:user/practice'}))
    elif args[:2]==['ec2','describe-instances']:
        print(json.dumps({'Reservations':[{'Instances':[{'InstanceId':'i-demo','InstanceType':'t3.micro','State':{'Name':'running'},'SubnetId':'subnet-demo','VpcId':'vpc-demo','ImageId':'ami-demo','SecurityGroups':[]}]}]}))
    else: sys.exit(2)
elif name=='kubectl':
    kind=args[args.index('get')+1]
    items=[]
    if kind=='pods':
        items=[{'metadata':{'name':'api','namespace':'test','uid':'p1'},'spec':{'containers':[{'name':'app'}]},'status':{'phase':'Running','conditions':[{'type':'Ready','status':'False'}],'containerStatuses':[{'name':'app','ready':False,'restartCount':3,'state':{'waiting':{'reason':'CrashLoopBackOff'}}}]}}]
    print(json.dumps({'kind':'List','apiVersion':'v1','items':items}))
else: sys.exit(2)
'''
for name in ['aws', 'kubectl']:
    path = write(name, adapter)
    path.chmod(0o700)
env = dict(os.environ)
env['PATH'] = str(root) + os.pathsep + env.get('PATH', '')

def run(label, expected, *args):
    p = subprocess.run([str(binary), *map(str, args)], env=env, capture_output=True, text=True, timeout=30)
    write(f'{len(results)+1:02d}-{label}.txt', p.stdout + p.stderr)
    results.append({'scenario': label, 'expected_exit': expected, 'actual_exit': p.returncode,
                    'args': list(map(str, args))})
    write('results.json', results)
    if p.returncode != expected:
        raise AssertionError(f'{label}: expected {expected}, got {p.returncode}\n{p.stdout}\n{p.stderr}')
    print(f'PASS {label}: {expected}', flush=True)
    return p.stdout

context = root / 'context.json'
run('collect-context', 0, 'collect', '--region', 'us-east-1', '--expect-account', '111111111111', '--output', context)
expected = write('expected.json', {'schema_version':'1', 'name':'practice', 'contexts':{
    'aws': {'kind':'aws', 'values': {'account':'111111111111', 'region':'us-east-1'}}}})
run('review-context', 0, 'context-summary', '--input', context, '--expect', expected)
run('wrong-account-stops-collection', 30, 'collect', '--kind', 'relations', '--region', 'us-east-1', '--expect-account', '222222222222')
manifest = {'schema_version':'1','complete':True,'hosts':[{'id':'i-demo','platform':'aws-ec2','baseline':'b'}],
            'baselines':{'b':{'platform':'aws-ec2','source':'synthetic baseline','collected_at':now,'values':{'state':'running'}}},'observations':[]}
manifest_path = write('manifest.json', manifest)
fleet = root / 'fleet.json'
run('collect-fleet', 0, 'collect', '--kind', 'fleet', '--region', 'us-east-1', '--expect-account', '111111111111', '--manifest', manifest_path, '--output', fleet)
run('review-fleet', 0, 'fleet-check', '--input', fleet)
run('compare-missing-to-observed', 10, 'changes', '--kind', 'fleet', '--before', manifest_path, '--after', fleet)
graph = root / 'graph.json'
run('collect-graph', 0, 'collect', '--kind', 'relations', '--region', 'us-east-1', '--expect-account', '111111111111', '--output', graph)
run('walk-graph', 10, 'resource-walk', '--input', graph, '--resource', 'i-demo')
snapshot = root / 'kube.json'
report = run('kube-investigation', 20, 'kube-explain', '--native', '--context', 'practice', '--namespace', 'test', '--save-snapshot', snapshot, '--format', 'json')
run('kube-replay', 20, 'kube-explain', '--input', snapshot, '--context', 'practice', '--namespace', 'test')
report_path = write('report.json', report)
run('report-change-retains-risk', 20, 'changes', '--before', report_path, '--after', report_path)
source = write('main.tf', 'resource "aws_vpc" "demo" { cidr_block = "10.0.0.0/16" }\n')
for shell in ['posix', 'powershell']:
    run('command-' + shell, 0, 'command-gen', '--to', 'aws', '--input', source, '--region', 'us-east-1', '--explain', '--shell', shell)
source = write('missing.tf', 'resource "aws_subnet" "demo" { cidr_block = "10.0.0.0/24" }\n')
run('command-missing-parent', 30, 'command-gen', '--to', 'aws', '--input', source, '--region', 'us-east-1', '--explain')
print(f'{len(results)} checks passed. Synthetic evidence retained at {root}')
