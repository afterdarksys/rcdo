#!/usr/bin/env python3
"""Exercise plugin registration, execution, and both disable switches locally."""
import json
from pathlib import Path
import subprocess
import tempfile


def main():
    repo = Path(__file__).resolve().parents[1]
    binary = str(repo / "dist/rcdo")
    with tempfile.TemporaryDirectory(prefix="rcdo-plugin-") as directory:
        config = str(Path(directory) / "config.yaml")

        def run(args, expected=0, data=""):
            result = subprocess.run([binary, *args, "--config-file", config], input=data,
                                    capture_output=True, text=True, timeout=15)
            if result.returncode != expected:
                raise RuntimeError(f"Expected {expected}; got {result.returncode}: {result.stderr} {result.stdout}")
            return result.stdout

        run(["config", "init"])
        entry = dict(enabled=False, path=str(repo / "examples/plugins/line-count.py"))
        run(["config", "set", "--key", "plugins.entries.line-count", "--value", json.dumps(entry)])
        run(["plugin", "run", "--name", "line-count"], expected=2)
        run(["config", "set", "--key", "plugins.enabled", "--value", "true"])
        run(["plugin", "run", "--name", "line-count"], expected=2)
        run(["config", "set", "--key", "plugins.entries.line-count.enabled", "--value", "true"])
        report = json.loads(run(["plugin", "run", "--name", "line-count", "--format", "json"], data="one\ntwo\n"))
        if report["status"] != "clean" or "Counted 2 lines" not in " ".join(report["completed_checks"]):
            raise RuntimeError("Example did not process input")
        run(["config", "set", "--key", "plugins.enabled", "--value", "false"])
        run(["plugin", "run", "--name", "line-count"], expected=2)
        listing = json.loads(run(["plugin", "list", "--format", "json"]))
        if listing["enabled"] or not listing["entries"]["line-count"]["enabled"]:
            raise RuntimeError("Global disable changed individual configuration")
    print("Plugin registration, example execution, and both disable switches: PASS")


if __name__ == "__main__":
    main()
