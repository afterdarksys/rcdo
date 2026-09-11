#!/usr/bin/env python3
"""Reference adapter for an export already fetched by a workplace deployment kit.

Set RCDO_WORKFLOW_EXPORT to the private export.json. For remote stores, replace
this adapter with the kit's authenticated, run-specific retrieval implementation.
"""
import argparse
import json
import os
from pathlib import Path
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=["rcdo-export"])
    parser.add_argument("--project", required=True)
    parser.add_argument("--run", required=True)
    args = parser.parse_args()
    try:
        with Path(os.environ["RCDO_WORKFLOW_EXPORT"]).open("rb") as stream:
            data = stream.read((32 << 20) + 1)
        if len(data) > 32 << 20:
            raise ValueError("oversized export")
        envelope = json.loads(data)
        if envelope["origin"]["project"] != args.project or envelope["origin"]["run_id"] != args.run:
            raise ValueError("run mismatch")
    except (KeyError, ValueError, OSError, TypeError):
        raise SystemExit("Export unavailable or run identity differs")
    sys.stdout.buffer.write(data)


if __name__ == "__main__":
    main()
