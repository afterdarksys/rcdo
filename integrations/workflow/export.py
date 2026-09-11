#!/usr/bin/env python3
"""Package existing, hash-bound workflow evidence; never invent run receipts.

The output directory must be new. Upload its bundle/ directory as one GitHub
artifact, or transport bundle.zip to a Spacelift/custom evidence store.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import zipfile

LIMIT = 16 * 1024 * 1024


def digest(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return (json.dumps(value, indent=2, sort_keys=True) + "\n").encode()


def read(path):
    with Path(path).open("rb") as stream:
        data = stream.read(LIMIT + 1)
    if len(data) > LIMIT:
        raise ValueError("evidence file exceeds 16 MiB")
    return data


def write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with path.open("xb") as stream:
        os.chmod(path, 0o600)
        stream.write(data)


def export(manifest_path, output, origin, stage):
    manifest_path, output = Path(manifest_path).resolve(), Path(output).absolute()
    manifest_raw = read(manifest_path)
    manifest = json.loads(manifest_raw)
    if manifest.get("schema_version") != "1" or "identity" not in manifest:
        raise ValueError("invalid workflow manifest")
    if manifest.get("origin", origin) != origin:
        raise ValueError("existing manifest origin differs")
    files, sources = {}, {manifest_path: digest(manifest_raw)}

    def get(binding):
        path = (manifest_path.parent / binding["path"]).resolve()
        data = read(path)
        if digest(data) != binding["sha256"]:
            raise ValueError("source artifact changed")
        sources[path] = binding["sha256"]
        return data

    def put(name, data):
        if name in files and files[name] != data:
            raise ValueError("conflicting archive paths")
        files[name] = data
        return {"path": name, "sha256": digest(data)}

    for key in ("outputs", "producer", "inventory", "playbook", "extra_vars", "events"):
        if key in manifest:
            suffix = Path(manifest[key]["path"]).suffix
            manifest[key] = put(key + suffix, get(manifest[key]))
    if "source_graph" in manifest:
        graph = json.loads(get(manifest["source_graph"]))
        if graph["root"]["sha256"] != manifest["playbook"]["sha256"]:
            raise ValueError("source graph belongs to another playbook")
        get(graph["root"])
        # Keep consumer display paths, but rebind physical artifacts for portability.
        graph["root"] = manifest["playbook"]
        graph["files"] = [put("sources/" + str(i) + Path(a["path"]).suffix, get(a))
                          for i, a in enumerate(graph["files"])]
        manifest["source_graph"] = put("source-graph.json", encoded(graph))
    old_execution = None
    if "execution" in manifest:
        old_execution = manifest["execution"]["sha256"]
        execution = json.loads(get(manifest["execution"]))
        if execution.get("resolved_inputs"):
            execution["resolved_inputs"] = put("resolved.json", get(execution["resolved_inputs"]))
        manifest["execution"] = put("execution.json", encoded(execution))
    if "verification" in manifest:
        health = json.loads(get(manifest["verification"]))
        if old_execution is None or health.get("execution_sha256") != old_execution:
            raise ValueError("verification belongs to another invocation")
        health["execution_sha256"] = manifest["execution"]["sha256"]
        manifest["verification"] = put("verification.json", encoded(health))
    manifest["origin"] = origin
    put("workflow.json", encoded(manifest))
    if len(files) > 256 or sum(map(len, files.values())) > 32 * 1024 * 1024:
        raise ValueError("bundle exceeds collector limits")
    for path, sha in sources.items():
        if digest(read(path)) != sha:
            raise ValueError("source changed during export")
    output.mkdir(mode=0o700)  # no overwrite, including partial earlier exports
    bundle = output / "bundle"
    bundle.mkdir(mode=0o700)
    for name, data in files.items():
        write(bundle / name, data)
    archive_path = output / "bundle.zip"
    with archive_path.open("xb") as stream:
        os.chmod(archive_path, 0o600)
        with zipfile.ZipFile(stream, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for name in sorted(files):
                archive.writestr(name, files[name])
    data = read(archive_path)
    profile = dict(schema_version="1", identity=manifest["identity"], origin=origin,
                   stage=stage, bundle=dict(path="bundle.zip", sha256=digest(data)))
    write(output / "profile.json", encoded(profile))
    envelope = dict(schema_version="1", identity=manifest["identity"], origin=origin,
                    archive_base64=base64.b64encode(data).decode(), sha256=digest(data))
    write(output / "export.json", encoded(envelope))
    return profile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--provider", choices=["local", "github", "spacelift", "custom"], required=True)
    parser.add_argument("--project", required=True)
    parser.add_argument("--run", required=True)
    parser.add_argument("--attempt", type=int, default=1)
    parser.add_argument("--stage", choices=["inputs", "execution", "verified"], default="verified")
    args = parser.parse_args()
    if args.attempt < 1:
        parser.error("attempt must be positive")
    origin = dict(provider=args.provider, project=args.project, run_id=args.run, attempt=args.attempt)
    try:
        export(args.manifest, args.output_dir, origin, args.stage)
    except (OSError, ValueError, KeyError, TypeError):
        raise SystemExit("Export incomplete: invalid, changed or unavailable evidence; no success receipt was created")
    print("Evidence exported to " + str(Path(args.output_dir).absolute()))


if __name__ == "__main__":
    main()
