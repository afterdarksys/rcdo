#!/usr/bin/env python3
"""Credential-free practice for mappings and multi-cloud deployment comparison."""
import argparse
import copy
import datetime
import json
import pathlib
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="dist/rcdo")
    args = parser.parse_args()
    binary = str(pathlib.Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix="rcdo-service-practice-") as temporary:
        root = pathlib.Path(temporary)
        config = root / "config.json"

        def run(command, flags=(), bundle=None, expected=0):
            result = subprocess.run(
                [binary, command, "--config-file", str(config), *flags],
                input=json.dumps(bundle) if bundle is not None else "",
                text=True, capture_output=True, timeout=30,
            )
            if result.returncode != expected:
                raise RuntimeError(f"{command}: expected {expected}, got {result.returncode}\n"
                                   f"{result.stdout}\n{result.stderr}")
            return result.stdout

        run("config", ["init"])
        catalog = json.loads(run("service-map", ["--format", "json"]))
        family = next(f for f in catalog["families"] if f["id"] == "object-storage")
        stamp = datetime.datetime.now(datetime.timezone.utc).isoformat()
        bundle = {"schema_version": "1", "deployments": []}
        for service in family["services"]:
            bundle["deployments"].append({
                "name": service["cloud"], "cloud": service["cloud"],
                "account": "synthetic-account", "location": "synthetic-location",
                "source": "credential-free synthetic practice", "collected_at": stamp,
                "complete": True,
                "resources": [{"key": "uploads", "service": service["id"],
                               "id": "synthetic-resource",
                               "properties": {"public_access": False, "versioning": True,
                                              "customer_managed_key": True}}],
            })
        report = run("service-compare", bundle=bundle)
        assert report.count("Pair ") == 6, report
        print("Four clouds, six matching pairs: PASS")
        report = run("service-compare", ["--deployments", "aws,gcp"], bundle)
        assert report.count("Pair ") == 1, report
        print("Selected cloud pair: PASS")
        modified = copy.deepcopy(bundle)
        modified["deployments"][0]["resources"][0]["properties"]["versioning"] = False
        run("service-compare", bundle=modified, expected=10)
        print("Configuration difference requires review: PASS")
        modified["deployments"][0]["complete"] = False
        run("service-compare", bundle=modified, expected=30)
        print("Incomplete coverage cannot match: PASS")
        modified = copy.deepcopy(bundle)
        aws = next(d for d in modified["deployments"] if d["cloud"] == "aws")
        aws["resources"][0].update(service="ebs", properties={"size_gib": 100, "encrypted": True})
        report = run("service-compare", bundle=modified, expected=10)
        assert "different service families" in report, report
        print("Object and block storage remain distinct: PASS")
        maps = root / "maps.json"
        run("service-map", ["--output", str(maps)])
        run("service-compare", ["--maps", str(maps)], bundle)
        run("service-map", ["--output", str(maps)], expected=2)
        print("Editable catalog round trip and overwrite refusal: PASS")


if __name__ == "__main__":
    main()
