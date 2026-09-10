#!/usr/bin/env python3
"""Local acceptance checks; never reports synthetic results as operator passes."""
import gzip
import json
import os
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[1]
binary = root / "dist" / "rcdo"
work = pathlib.Path(tempfile.mkdtemp(prefix="rcdo-next-six-"))
config = work / "config.yaml"
checks = []
environment = os.environ.copy()
for key in list(environment):
    if key.startswith(("TF_", "TOFU_", "ANSIBLE_")):
        environment.pop(key)
ansible_config = work / "ansible.cfg"
ansible_config.write_text("[defaults]\nretry_files_enabled = False\n")
environment["ANSIBLE_CONFIG"] = str(ansible_config)


def run(command, *args, expected=0):
    result = subprocess.run([str(binary), command, *args, "--config-file", str(config)],
                            capture_output=True, text=True, env=environment)
    assert result.returncode == expected, (command, args, result.returncode, result.stderr)
    checks.append({"command": command, "exit": result.returncode})
    return result.stdout


def fixture(name, value):
    path = work / name
    path.write_text(value if isinstance(value, str) else json.dumps(value))
    return str(path)


run("config", "init")
run("config", "set", "--key", "audit.enabled", "--value", "true")
source = work / "log.gz"
source.write_bytes(gzip.compress(b"one\ntwo\n"))
run("log-read", "--input", str(source))
audit = json.loads(run("audit", "read", "--format", "json"))
assert audit["matched"] == 1
run("audit", "rotate", "--input", str(work / "audit.jsonl"), "--output", str(work / "archive.gz"), "--apply")
assert gzip.decompress((work / "archive.gz").read_bytes())

live = fixture("live.log", "one\n")
state = str(work / "monitor.json")
run("monitor", "start", "--input", live, "--state", state)
run("monitor", "pause", "--state", state)
with open(live, "a") as stream:
    stream.write("two\n")
paused = json.loads(run("monitor", "poll", "--state", state, "--format", "jsonl"))
assert paused["paused"] and paused["queued"] == 1
resumed = json.loads(run("monitor", "resume", "--state", state, "--format", "jsonl"))
assert "two" in resumed["messages"][0]

before = fixture("before.json", {"Statement": [{"Effect": "Deny", "Action": "s3:DeleteObject", "Resource": "*"}]})
after = fixture("after.json", {"Statement": []})
text = run("permission-diff", "--before", before, "--after", after, expected=20)
assert "Deny clause removed" in text

document = fixture("list.json", {"servers": [{"id": "one", "port": 80}, {"id": "two", "port": 443}]})
navigation = str(work / "navigation.json")
run("config-walk", "start", "--state", navigation, "--input", document)
run("config-walk", "goto", "--state", navigation, "--path", "$.servers[1].port")
run("config-walk", "bookmark", "--state", navigation, "--name", "https", "--identity-key", "id")
fixture("list.json", {"servers": [{"id": "two", "port": 443}, {"id": "one", "port": 80}]})
position = json.loads(run("config-walk", "goto", "--state", navigation, "--name", "https", "--format", "json"))
assert position["cursor"] == "$.servers[0].port"

pilot = str(work / "pilot.json")
run("pilot", "start", "--state", pilot, "--operator", "automated fixture runner",
    "--setup", "Synthetic run; device usability not observed", expected=30)
assert "pending" in run("pilot", "show", "--state", pilot, expected=30)

native = {}
if shutil.which("ansible-inventory"):
    inventory = fixture("inventory.ini", "[local]\nlocalhost ansible_connection=local\n")
    output = work / "inventory-context.json"
    run("context-acquire", "--native", "--kind", "ansible", "--inventory", inventory,
        "--name", "local", "--output", str(output))
    assert json.loads(output.read_text())["contexts"]["local"]["values"]["host_count"] == "1"
    native["ansible_inventory"] = "passed: localhost inventory only; no playbook or SSH"
else:
    native["ansible_inventory"] = "not run: CLI unavailable"

if shutil.which("tofu"):
    module = work / "module"
    module.mkdir()
    (module / "main.tf").write_text('terraform {\n  backend "local" {\n    path = "state.tfstate"\n  }\n}\n')
    initialized = subprocess.run(["tofu", f"-chdir={module}", "init", "-input=false"], capture_output=True, text=True, env=environment)
    assert initialized.returncode == 0, initialized.stderr
    output = work / "iac-context.json"
    run("context-acquire", "--native", "--kind", "tofu", "--directory", str(module),
        "--name", "local", "--output", str(output))
    values = json.loads(output.read_text())["contexts"]["local"]["values"]
    assert values["workspace"] == "default" and values["backend"] == "local"
    native["tofu_backend"] = "passed: empty temporary module with local backend"
else:
    native["tofu_backend"] = "not run: CLI unavailable"

results = {"automated_checks": checks, "local_native": native,
           "assistive_technology": "not run: requires operator observations",
           "authenticated_cloud": "not run: nonproduction targets not selected"}
(work / "results.json").write_text(json.dumps(results, indent=2) + "\n")
print(f"Passed {len(checks)} local checks. Native results: {native}. Artifacts: {work}")
