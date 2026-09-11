#!/usr/bin/env python3
"""Exercise output-to-input review locally and retain private evidence.

Default: synthetic fixtures, no infrastructure or Ansible commands.
--native: constant-only Terraform local state and Ansible on localhost, writing
only inside a newly created temporary directory. No cloud resources are used.
"""
import argparse
import copy
import datetime
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import tempfile


def stamp(offset=0):
    return (datetime.datetime.now(datetime.timezone.utc)
            + datetime.timedelta(seconds=offset)).isoformat().replace("+00:00", "Z")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="dist/rcdo")
    parser.add_argument("--native", action="store_true")
    parser.add_argument("--engine", choices=["terraform", "tofu"], default="terraform")
    args = parser.parse_args()
    binary = str(pathlib.Path(args.binary).resolve())
    repo = pathlib.Path(__file__).resolve().parents[1]
    root = pathlib.Path(tempfile.mkdtemp(prefix="rcdo-workflow-"))
    results = []

    def write(name, value):
        data = value.encode() if isinstance(value, str) else json.dumps(value, indent=2).encode()
        path = root / name
        with path.open("wb") as target:
            os.chmod(path, 0o600)
            target.write(data)
        return {"path": name, "sha256": hashlib.sha256(data).hexdigest()}

    def bound(name):
        return {"path": name, "sha256": hashlib.sha256((root / name).read_bytes()).hexdigest()}

    identity = dict(change_id="LOCAL-PRACTICE", commit="a" * 40, environment="local-practice",
                    engine=args.engine, backend="local-practice-state", workspace="default",
                    account="local-only", region="local-only")
    inventory = """all:
  children:
    web:
      hosts:
        blue:
          ansible_host: 127.0.0.1
          ansible_connection: local
          db_host: synthetic-private-endpoint
"""
    target = str(root / "configured.txt")
    playbook = """- hosts: web
  gather_facts: false
  tasks:
    - name: Write synthetic configuration inside practice directory
      ansible.builtin.copy:
        content: "{{ db_host }}"
        dest: DESTINATION
        mode: '0600'
""".replace("DESTINATION", json.dumps(target))
    inv = write("inventory.yml", inventory)
    play = write("playbook.yml", playbook)
    outputs = write("outputs.json", {
        "web_host": {"sensitive": False, "type": "string", "value": "127.0.0.1"},
        "database_endpoint": {"sensitive": True, "type": "string", "value": "synthetic-private-endpoint"},
    })
    resolved_data = {"_meta": {"hostvars": {"blue": {"ansible_host": "127.0.0.1",
                     "ansible_connection": "local", "db_host": "synthetic-private-endpoint"}}},
                     "all": {"children": ["web"]}, "web": {"hosts": ["blue"]}}
    resolved = write("resolved.json", resolved_data)
    applied, collected = stamp(-180), stamp(-120)
    started, finished = stamp(-60), stamp(-30)
    lineage, serial, run_id = "synthetic-local-lineage", 1, "synthetic-run-1"
    events = [dict(schema_version="1", event="start", run_id=run_id, sequence=0,
                   at=started, check_mode=False),
              dict(schema_version="1", event="result", run_id=run_id, sequence=1,
                   at=stamp(-45), check_mode=False, host="blue", task_id="copy-1",
                   task="Write synthetic configuration", outcome="ok"),
              dict(schema_version="1", event="finish", run_id=run_id, sequence=2, at=finished)]

    if args.native:
        for program in (args.engine, "ansible-inventory", "ansible-playbook"):
            if not shutil.which(program):
                raise SystemExit(f"Required native executable missing: {program}")
        native_env = {key: value for key, value in os.environ.items()
                      if not key.startswith(("ANSIBLE_", "TF_", "RCDO_ANSIBLE_"))}
        write("ansible.cfg", "[defaults]\nretry_files_enabled = False\ninterpreter_python = auto_silent\n")
        native_env.update(ANSIBLE_CONFIG=str(root / "ansible.cfg"),
                          ANSIBLE_LOCAL_TEMP=str(root / "ansible-local"),
                          ANSIBLE_REMOTE_TEMP=str(root / "ansible-remote"),
                          ANSIBLE_CALLBACK_PLUGINS=str(repo / "integrations/ansible/callback_plugins"),
                          ANSIBLE_CALLBACKS_ENABLED="rcdo", RCDO_ANSIBLE_EVENTS=str(root / "events.jsonl"))

        def native(command, label):
            p = subprocess.run(command, cwd=root, env=native_env, capture_output=True, timeout=120)
            write(label + ".stdout", p.stdout.decode(errors="replace"))
            write(label + ".stderr", p.stderr.decode(errors="replace"))
            if p.returncode:
                raise SystemExit(f"Native {label} failed; inspect private artifacts in {root}")
            return p.stdout.decode()

        write("main.tf", '''output "web_host" {
  value = "127.0.0.1"
}
output "database_endpoint" {
  value = "synthetic-private-endpoint"
  sensitive = true
}
''')
        native([args.engine, "init", "-input=false", "-no-color"], "init")
        native([args.engine, "apply", "-input=false", "-auto-approve", "-no-color"], "local-apply")
        applied = stamp()
        outputs = write("outputs.json", native([args.engine, "output", "-json"], "output"))
        collected = stamp()
        exported = json.loads((root / "outputs.json").read_text())
        inv = write("inventory.yml", {"all": {"children": {"web": {"hosts": {"blue": {
            "ansible_host": exported["web_host"]["value"], "ansible_connection": "local",
            "db_host": exported["database_endpoint"]["value"],
        }}}}}})
        state = json.loads((root / "terraform.tfstate").read_text())
        lineage, serial = state["lineage"], state["serial"]
        resolved = write("resolved.json", native(["ansible-inventory", "-i", str(root / "inventory.yml"), "--list"], "inventory"))
        started = stamp()
        native(["ansible-playbook", "-i", str(root / "inventory.yml"), "--limit", "blue",
                str(root / "playbook.yml")], "ansible")
        finished = stamp()
        events = [json.loads(line) for line in (root / "events.jsonl").read_text().splitlines()]
        run_id = events[0]["run_id"]
        if pathlib.Path(target).read_text() != "synthetic-private-endpoint":
            raise SystemExit("Independent localhost configuration verification failed")
    else:
        write("events.jsonl", "".join(json.dumps(event) + "\n" for event in events))

    producer = write("producer.json", dict(schema_version="1", identity=identity, outcome="succeeded",
        applied_at=applied, collected_at=collected, state_lineage=lineage, state_serial=serial,
        outputs_sha256=outputs["sha256"]))
    execution_data = dict(schema_version="1", identity=identity, run_id=run_id, started_at=started,
        finished_at=finished, outcome="succeeded", check_mode=False, outputs_sha256=outputs["sha256"],
        inventory_sha256=inv["sha256"], extra_vars_sha256="", playbook_sha256=play["sha256"],
        hosts=["blue"], variable_sources_complete=True, resolved_inputs=resolved)
    execution = write("execution.json", execution_data)
    health = write("health.json", dict(schema_version="1", identity=identity, run_id=run_id,
        execution_sha256=execution["sha256"], collected_at=stamp(), hosts=["blue"],
        checks=[dict(host="blue", name="configuration-content", outcome="pass")]))
    manifest = dict(schema_version="1", identity=identity, outputs=outputs, producer=producer,
        inventory=inv, playbook=play, hosts=["blue"], execution=execution, events=bound("events.jsonl"),
        verification=health, required_checks=[dict(host="blue", name="configuration-content")], mappings=[
            dict(id="blue-address", output="web_host", pointer="", host="blue", variable="ansible_host", type="string"),
            dict(id="database", output="database_endpoint", pointer="", host="blue", variable="db_host", type="string")])
    write("workflow.json", manifest)

    def check(label, value, stage, expected):
        write(label + ".manifest.json", value)
        p = subprocess.run([binary, "workflow-check", "--manifest", str(root / (label + ".manifest.json")),
                            "--stage", stage, "--width", "60"], capture_output=True, text=True, timeout=30)
        write(label + ".txt", p.stdout)
        write(label + ".stderr", p.stderr)
        passed = p.returncode == expected and "synthetic-private-endpoint" not in p.stdout
        results.append(dict(case=label, expected=expected, actual=p.returncode, passed=passed))
        print(f"{label}: expected {expected}; got {p.returncode}; {'PASS' if passed else 'FAIL'}")

    check("inputs", manifest, "inputs", 0)
    check("execution", manifest, "execution", 10)
    check("verified", manifest, "verified", 10)
    altered = copy.deepcopy(manifest)
    altered["extra_vars"] = write("wrong-extra.json", {"db_host": "wrong-endpoint"})
    check("override-blocked", altered, "inputs", 20)
    altered = copy.deepcopy(manifest)
    altered.pop("execution")
    check("missing-execution", altered, "execution", 30)
    altered = copy.deepcopy(manifest)
    altered["outputs"]["sha256"] = "0" * 64
    check("changed-artifact", altered, "inputs", 30)
    altered = copy.deepcopy(manifest)
    altered["inventory_format"] = "resolved"
    altered["inventory"] = resolved
    check("resolved-inventory", altered, "inputs", 0)
    write("results.json", results)
    print(f"Evidence directory: {root}")
    print("Mode: " + ("native local-only execution" if args.native else "synthetic fixtures; no execution proof"))
    raise SystemExit(0 if all(r["passed"] for r in results) else 1)


if __name__ == "__main__":
    main()
