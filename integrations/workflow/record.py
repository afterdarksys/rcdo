#!/usr/bin/env python3
"""Observe an explicitly authorized nonproduction workflow and create receipts.

Commands are JSON argv arrays, never shell strings. This executes the configured
producer and Ansible only with --execute. Inspect the config before running it.
"""
import argparse
import datetime
import json
import os
import re
from pathlib import Path
import subprocess
import sys

from export import digest, encoded, read, write


def stamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")


def record(config_path, output, binary):
    config_path = Path(config_path).resolve()
    cfg = json.loads(read(config_path))
    base = config_path.parent
    identity = cfg["identity"]
    required = {"change_id", "commit", "environment", "engine", "backend", "workspace", "account", "region"}
    if set(identity) != required or not all(isinstance(v, str) and v.strip() and not any(ord(c) < 32 for c in v) for v in identity.values()):
        raise ValueError("invalid workflow identity")
    if not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", identity["commit"]):
        raise ValueError("full deployed commit required")
    for key in ("identity_command", "producer_command", "inventory_command", "verify_command"):
        if not isinstance(cfg[key], list) or not cfg[key] or not all(isinstance(v, str) and v for v in cfg[key]):
            raise ValueError("commands must be argv arrays")
    if not isinstance(cfg.get("mappings"), list) or not cfg["mappings"] or not isinstance(cfg.get("required_checks"), list) or not cfg["required_checks"]:
        raise ValueError("mappings and required checks are required")
    origin = cfg["origin"]
    if origin.get("provider") not in ("local", "github", "spacelift", "custom") or not origin.get("project") or not origin.get("run_id") or not isinstance(origin.get("attempt"), int) or origin["attempt"] < 1:
        raise ValueError("invalid run origin")
    if identity["engine"] not in ("terraform", "tofu"):
        raise ValueError("unsupported engine")
    if cfg.get("nonproduction") is not True:
        raise ValueError("requires explicit nonproduction scope")
    root = Path(output).absolute()
    root.mkdir(mode=0o700)
    project = (base / cfg["project_root"]).resolve()
    terraform = (base / cfg["terraform_dir"]).resolve()
    playbook = (base / cfg["playbook"]).resolve()
    playbook.relative_to(project)
    timeout = cfg.get("timeout_seconds", 1200)
    if not isinstance(timeout, int) or not 1 <= timeout <= 3600:
        raise ValueError("invalid timeout")
    environment = {k: v for k, v in os.environ.items()
                   if not k.startswith(("ANSIBLE_", "RCDO_ANSIBLE_"))}
    callback = Path(__file__).resolve().parents[1] / "ansible/callback_plugins"
    write(root / "ansible.cfg", b"[defaults]\nretry_files_enabled = False\n")
    environment.update(ANSIBLE_CONFIG=str(root / "ansible.cfg"),
                       ANSIBLE_LOCAL_TEMP=str(root / "ansible-local"),
                       ANSIBLE_CALLBACK_PLUGINS=str(callback), ANSIBLE_CALLBACKS_ENABLED="rcdo",
                       RCDO_ANSIBLE_EVENTS=str(root / "events.jsonl"))

    def run(argv, label, cwd=project, data=None, allowed=(0,)):
        if not isinstance(argv, list) or not argv or not all(isinstance(v, str) and v for v in argv):
            raise ValueError("command must be a nonempty argv array")
        stdout, stderr = root / (label + ".stdout"), root / (label + ".stderr")
        with stdout.open("xb") as out, stderr.open("xb") as err:
            os.chmod(stdout, 0o600)
            os.chmod(stderr, 0o600)
            result = subprocess.run(argv, cwd=cwd, env=environment, input=data,
                                    stdout=out, stderr=err, timeout=timeout)
        if result.returncode not in allowed:
            raise ValueError("command failed: " + label)
        return read(stdout)

    def artifact(name, data):
        write(root / name, data)
        return dict(path=name, sha256=digest(data))

    def context(label):
        if json.loads(run(cfg["identity_command"], label)) != identity:
            raise ValueError("observed identity differs from configured identity")
        workspace = run([identity["engine"], "workspace", "show"], label + "-workspace", terraform).decode().strip()
        if workspace != identity["workspace"]:
            raise ValueError("workspace mismatch")

    context("identity-before")
    run(cfg["producer_command"], "producer", terraform)
    applied = stamp()
    state_before = json.loads(run([identity["engine"], "state", "pull"], "state-before", terraform))
    outputs_raw = run([identity["engine"], "output", "-json"], "outputs", terraform)
    collected = stamp()
    state_after = json.loads(run([identity["engine"], "state", "pull"], "state-after", terraform))
    if state_before != state_after or not state_after.get("lineage") or "serial" not in state_after:
        raise ValueError("state changed during output capture or lacks version identity")
    state_outputs = state_after.get("outputs", {})
    normalized_outputs = {name: dict(value, sensitive=value.get("sensitive", False))
                          for name, value in state_outputs.items()}
    if encoded(json.loads(outputs_raw)) != encoded(normalized_outputs):
        raise ValueError("exported outputs differ from captured state")
    outputs = artifact("outputs.json", outputs_raw)
    inventory = artifact("inventory.yml", run(cfg["inventory_command"], "inventory", data=outputs_raw))
    play = artifact("playbook.yml", read(playbook))
    extra = artifact("extra.json", read(base / cfg["extra_vars"])) if cfg.get("extra_vars") else None
    graph_path = root / "source-graph.json"
    run([binary, "workflow-trace", "--input", str(playbook), "--root", str(project),
         "--graph-out", str(graph_path), "--format", "json"], "source-trace", allowed=(0, 10, 20, 30))
    graph = json.loads(read(graph_path))
    ansible_args = ["-i", str(root / "inventory.yml")]
    if extra:
        ansible_args += ["--extra-vars", "@" + str(root / "extra.json")]
    resolved = artifact("resolved.json", run(["ansible-inventory", *ansible_args, "--list"], "resolved"))
    hosts = cfg["hosts"]
    if not hosts or not all(isinstance(h, str) and h and all(c.isalnum() or c in "_.-" for c in h) for h in hosts):
        raise ValueError("hosts must be literal inventory names")
    started = stamp()
    run(["ansible-playbook", *ansible_args, "--limit", ",".join(hosts), str(playbook)], "ansible")
    finished = stamp()
    for source in graph["files"]:
        if digest(read(source["path"])) != source["sha256"]:
            raise ValueError("playbook source changed during execution")
    for binding in (inventory, play, extra, resolved):
        if binding and digest(read(root / binding["path"])) != binding["sha256"]:
            raise ValueError("invocation input changed")
    context("identity-after")
    if json.loads(run([identity["engine"], "state", "pull"], "state-final", terraform)) != state_after:
        raise ValueError("state changed during Ansible execution")
    events_raw = read(root / "events.jsonl")
    events = [json.loads(line) for line in events_raw.splitlines()]
    if not events or events[0].get("event") != "start" or events[-1].get("event") != "finish":
        raise ValueError("callback recording incomplete")
    run_id = events[0]["run_id"]
    producer = artifact("producer.json", encoded(dict(schema_version="1", identity=identity,
        outcome="succeeded", applied_at=applied, collected_at=collected,
        state_lineage=state_after["lineage"], state_serial=state_after["serial"], outputs_sha256=outputs["sha256"])))
    execution = artifact("execution.json", encoded(dict(schema_version="1", identity=identity,
        run_id=run_id, started_at=started, finished_at=finished, outcome="succeeded", check_mode=False,
        outputs_sha256=outputs["sha256"], inventory_sha256=inventory["sha256"],
        extra_vars_sha256=extra["sha256"] if extra else "", playbook_sha256=play["sha256"], hosts=hosts,
        variable_sources_complete=cfg.get("variable_sources_complete") is True, resolved_inputs=resolved)))
    manifest = dict(schema_version="1", identity=identity, origin=cfg["origin"], outputs=outputs,
        producer=producer, inventory=inventory, playbook=play, hosts=hosts, mappings=cfg["mappings"],
        execution=execution, events=dict(path="events.jsonl", sha256=digest(events_raw)),
        source_graph=dict(path="source-graph.json", sha256=digest(read(graph_path))),
        required_checks=cfg["required_checks"])
    if extra:
        manifest["extra_vars"] = extra
    # The independent verifier returns [{host, name, outcome}], not a success label.
    checks = json.loads(run(cfg["verify_command"], "verification"))
    if not isinstance(checks, list) or not checks:
        raise ValueError("verification must return observed checks")
    manifest["verification"] = artifact("verification.json", encoded(dict(schema_version="1",
        identity=identity, run_id=run_id, execution_sha256=execution["sha256"], collected_at=stamp(),
        hosts=hosts, checks=checks)))
    artifact("workflow.json", encoded(manifest))
    run([binary, "workflow-check", "--manifest", str(root / "workflow.json"), "--stage", "verified",
         "--format", "json"], "review", allowed=(0, 10))
    return root


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--binary", default="dist/rcdo")
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    if not args.execute:
        parser.error("inspect the config, then use --execute to authorize its commands")
    try:
        root = record(args.config, args.output_dir, str(Path(args.binary).resolve()))
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        raise SystemExit("Recording stopped; inspect private artifacts in " + str(Path(args.output_dir).absolute()))
    print("Evidence recorded in " + str(root) + "; read review.stdout for readiness and gaps")


if __name__ == "__main__":
    main()
