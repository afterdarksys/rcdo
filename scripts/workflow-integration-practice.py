#!/usr/bin/env python3
"""Test recorder/exporter/collector plumbing; --native uses constant-only local state."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="dist/rcdo")
    parser.add_argument("--native", action="store_true")
    parser.add_argument("--engine", choices=["terraform", "tofu"], default="terraform")
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[1]
    binary = str(Path(args.binary).resolve())
    root = Path(tempfile.mkdtemp(prefix="rcdo-integration-"))

    def write(name, value):
        path = root / name
        with path.open("x") as stream:
            os.chmod(path, 0o600)
            stream.write(value if isinstance(value, str) else json.dumps(value))
        return path

    def run(command, expected=0, env=None):
        result = subprocess.run(command, cwd=repo, env=env, capture_output=True, text=True, timeout=180)
        if result.returncode != expected:
            raise RuntimeError(f"{command[0]}: expected {expected}, got {result.returncode}\n{result.stdout}\n{result.stderr}")
        return result.stdout

    if args.native:
        identity = dict(change_id="LOCAL-RECORDER-PRACTICE", commit="a" * 40, environment="local-practice",
                        engine=args.engine, backend="local-practice", workspace="default", account="local-only", region="local-only")
        write("identity.json", identity)
        write("main.tf", 'output "web_host" { value = "127.0.0.1" }\noutput "db" { value = "local-endpoint" }\n')
        write("playbook.yml", '- hosts: web\n  gather_facts: false\n  tasks:\n    - name: Write local configuration\n      ansible.builtin.copy:\n        content: "{{ db_host }}"\n        dest: ' + json.dumps(str(root / "configured.txt")) + '\n        mode: "0600"\n')
        helper = write("helper.py", '''import json, pathlib, sys
root = pathlib.Path(__file__).resolve().parent
mode = sys.argv[1]
if mode == "identity":
    print((root / "identity.json").read_text())
elif mode == "inventory":
    outputs = json.load(sys.stdin)
    print(json.dumps({"all": {"children": {"web": {"hosts": {"blue": {
        "ansible_host": outputs["web_host"]["value"], "ansible_connection": "local",
        "db_host": outputs["db"]["value"]}}}}}}))
elif mode == "health":
    result = "pass" if (root / "configured.txt").read_text() == "local-endpoint" else "fail"
    print(json.dumps([{"host": "blue", "name": "configuration-content", "outcome": result}]))
''')
        config = dict(nonproduction=True, identity=identity,
            origin=dict(provider="local", project="practice", run_id="1", attempt=1),
            project_root=str(root), terraform_dir=str(root), playbook=str(root / "playbook.yml"),
            identity_command=[sys.executable, str(helper), "identity"],
            producer_command=[args.engine, "apply", "-input=false", "-auto-approve", "-no-color"],
            inventory_command=[sys.executable, str(helper), "inventory"],
            verify_command=[sys.executable, str(helper), "health"], hosts=["blue"], variable_sources_complete=True,
            mappings=[dict(id="address", output="web_host", pointer="", host="blue", variable="ansible_host", type="string"),
                      dict(id="database", output="db", pointer="", host="blue", variable="db_host", type="string")],
            required_checks=[dict(host="blue", name="configuration-content")])
        config_path = write("record.json", config)
        run([args.engine, "-chdir=" + str(root), "init", "-input=false", "-no-color"])
        run([sys.executable, str(repo / "integrations/workflow/record.py"), "--config", str(config_path),
             "--output-dir", str(root / "recorded"), "--binary", binary, "--execute"])
        manifest = root / "recorded/workflow.json"
        print("native recorder: PASS")
    else:
        practice = run([sys.executable, str(repo / "scripts/workflow-practice.py"), "--binary", binary])
        source = next(line.split(": ", 1)[1] for line in practice.splitlines() if line.startswith("Evidence directory:"))
        manifest = Path(source) / "workflow.json"
    run([sys.executable, str(repo / "integrations/workflow/export.py"), "--manifest", str(manifest),
         "--output-dir", str(root / "export"), "--provider", "local", "--project", "practice", "--run", "1"])
    collect = [binary, "workflow-collect", "--profile", str(root / "export/profile.json"),
               "--output-dir", str(root / "collected"), "--format", "json"]
    report = run(collect, expected=10)
    if "local-endpoint" in report or "synthetic-private-endpoint" in report:
        raise RuntimeError("collector leaked source value")
    print("portable verified collection: PASS")
    profile = json.loads((root / "export/profile.json").read_text())
    profile.pop("bundle")
    profile["adapter"] = str(repo / "integrations/workflow/adapter.py")
    adapter_profile = write("adapter-profile.json", profile)
    environment = dict(os.environ, RCDO_WORKFLOW_EXPORT=str(root / "export/export.json"))
    run([binary, "workflow-collect", "--profile", str(adapter_profile), "--native",
         "--output-dir", str(root / "adapter-collected")], expected=10, env=environment)
    print("export adapter protocol: PASS")
    run(collect, expected=2)
    print("overwrite refused: PASS")
    run([binary, "pilot", "start", "--suite", "workflow", "--state", str(root / "pilot.json"),
         "--operator", "Automated fixture", "--setup", "No assistive technology acceptance performed"], expected=30)
    print("operator acceptance remains pending: PASS")
    print("Private evidence: " + str(root))


if __name__ == "__main__":
    main()
