#!/usr/bin/env python3
"""Exercise policy workflows using local OPA, without cloud credentials."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
BINARY = ROOT / "dist" / "rcdo"
PACK = ROOT / "examples" / "rego" / "starter"


def main():
    with tempfile.TemporaryDirectory(prefix="rcdo-policy-practice-") as folder:
        work = Path(folder)
        # Disable user defaults/auditing by selecting an explicit minimal config.
        env = os.environ.copy()
        env["RCDO_CONFIG"] = str(work / "config.yaml")
        # Use the application's own config initializer instead of duplicating its schema.
        subprocess.run([str(BINARY), "config", "init", "--file", env["RCDO_CONFIG"]],
                       cwd=ROOT, env=env, check=True, capture_output=True)
        before = work / "before.rego"
        after = work / "after.rego"
        before.write_text('package rcdo\ndecision := {"allow": true}\n')
        after.write_text('package rcdo\ndecision := {"allow": false}\n')
        modules = []
        for name in ("base", "tags", "public", "regions", "deletion", "production"):
            modules += ["--rego", str(PACK / (name + ".rego"))]
        scenarios = [
            ("starter-pass", ["rego-check", *modules, "--input", str(PACK / "pass.json")], 0),
            ("starter-deny", ["rego-check", *modules, "--input", str(PACK / "public-fail.json")], 20),
            ("fixture-suite", ["rego-test", *modules, "--suite", str(PACK / "suite.json")], 0),
            ("policy-difference", ["rego-diff", "--before", str(before), "--after", str(after),
                                   "--suite", str(PACK / "suite.json")], 20),
            ("combined-review", ["policy-review", "--rego", "examples/rego/terraform.rego",
                                  "--input", "examples/rego/plan.json"], 20),
        ]
        for name, args, expected in scenarios:
            result = subprocess.run([str(BINARY), *args, "--format", "json"],
                                    cwd=ROOT, env=env, capture_output=True, text=True, timeout=65)
            if result.returncode != expected:
                raise RuntimeError(f"{name}: expected {expected}, got {result.returncode}: {result.stderr} {result.stdout}")
            report = json.loads(result.stdout)
            if report["schema_version"] != "1" or report["incomplete_checks"]:
                raise RuntimeError(f"{name}: unexpected incomplete report")
            if name == "combined-review":
                ids = [f["id"] for f in report["findings"]]
                if not any(i.startswith("iac/") for i in ids) or not any(i.startswith("rego/") for i in ids):
                    raise RuntimeError("combined review lost a check's findings")
            print(f"PASS {name}: exit {result.returncode}, status {report['status']}")


if __name__ == "__main__":
    main()
