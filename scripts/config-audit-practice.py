#!/usr/bin/env python3
"""Exercise config defaults and audit records using only temporary local files."""
import concurrent.futures
import gzip
import json
import pathlib
import subprocess
import tempfile

binary = pathlib.Path(__file__).resolve().parents[1] / "dist" / "rcdo"
directory = pathlib.Path(tempfile.mkdtemp(prefix="rcdo-config-audit-"))
config = directory / "config.yaml"


def run(*args, expected=0):
    result = subprocess.run([str(binary), *args], text=True, capture_output=True)
    assert result.returncode == expected, (args, result.returncode, result.stderr)
    return result


run("config", "init", "--file", str(config))
for key, value in {
    "commands.log-read.format": "json",
    "commands.log-read.context": "0",
    "commands.log-read.state": str(directory / "reading.json"),
    "audit.enabled": "true",
}.items():
    run("config", "set", "--file", str(config), "--key", key, "--value", value)

source = directory / "app.log.gz"
source.write_bytes(gzip.compress(b"first\nsecond token=secret-example\n"))
result = run("log-read", "start", "--config-file", str(config), "--input", str(source))
assert json.loads(result.stdout)["cursor"] == 1
result = run("log-read", "next", "--config-file", str(config))
assert json.loads(result.stdout)["cursor"] == 2
run("log-read", "--config-file", str(config), "--invalid-flag", expected=2)
with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
    results = list(pool.map(lambda _: run("log-read", "--config-file", str(config),
                                        "--input", str(source)), range(6)))
    assert all(json.loads(result.stdout)["total_events"] == 2 for result in results)

audit = directory / "audit.jsonl"
contents = audit.read_text()
assert "secret-example" not in contents
records = [json.loads(line) for line in contents.splitlines()]
assert len(records) == 18, len(records)
for run_id in {record["run_id"] for record in records}:
    pair = [record for record in records if record["run_id"] == run_id]
    assert [record["event"] for record in pair] == ["start", "finish"]
    finished = pair[1]
    assert finished["user"] and finished["uid"] and finished["host"]
    assert finished["at"].endswith("Z")
    assert finished["exit_code"] in (0, 2)
    assert finished["stdout_bytes"] or finished["stderr_bytes"]
assert audit.stat().st_mode & 0o777 == 0o600
print(f"Passed: configured compressed-log navigation and 9 audited invocations, including 6 concurrent processes. Artifacts: {directory}")
