#!/usr/bin/env python3
"""Minimal RCDO executable plugin. Copy this file to start writing a plugin."""
import json
import sys


def main():
    if sys.argv[1:] != ["rcdo-plugin-v1"]:
        raise SystemExit("Invoke through rcdo plugin run")
    request = json.load(sys.stdin)
    if request.get("schema_version") != "1" or not isinstance(request.get("input"), str):
        raise SystemExit("Unsupported request")
    gaps = []
    completed = []
    if request.get("args"):
        gaps.append("Line-count plugin accepts no extra arguments")
    else:
        completed.append(f"Counted {len(request['input'].splitlines())} lines; input contents withheld")
    json.dump({"schema_version": "1", "status": "incomplete" if gaps else "clean",
               "findings": [], "completed_checks": completed, "incomplete_checks": gaps}, sys.stdout)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
