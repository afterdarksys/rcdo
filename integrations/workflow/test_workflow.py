import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile

from export import digest, encoded, export, write
from record import record


class ExportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.origin = dict(provider="local", project="practice", run_id="1", attempt=1)

    def artifact(self, name, data):
        write(self.root / name, data)
        return dict(path=name, sha256=digest(data))

    def fixture(self):
        manifest = dict(schema_version="1", identity={"commit": "a" * 40})
        for key in ("outputs", "producer", "inventory", "playbook"):
            manifest[key] = self.artifact(key + ".json", encoded({key: True}))
        resolved = self.artifact("nested-input.json", b"{}")
        execution = self.artifact("invocation.json", encoded({"resolved_inputs": resolved}))
        manifest["execution"] = execution
        manifest["verification"] = self.artifact("health.json", encoded({"execution_sha256": execution["sha256"]}))
        write(self.root / "workflow.json", encoded(manifest))
        return manifest

    def test_rebinds_nested_inputs_and_health(self):
        self.fixture()
        export(self.root / "workflow.json", self.root / "export", self.origin, "verified")
        with zipfile.ZipFile(self.root / "export/bundle.zip") as bundle:
            manifest = json.loads(bundle.read("workflow.json"))
            execution_raw = bundle.read(manifest["execution"]["path"])
            execution = json.loads(execution_raw)
            self.assertEqual(digest(bundle.read(execution["resolved_inputs"]["path"])), execution["resolved_inputs"]["sha256"])
            health = json.loads(bundle.read(manifest["verification"]["path"]))
            self.assertEqual(health["execution_sha256"], digest(execution_raw))
        with self.assertRaises(FileExistsError):
            export(self.root / "workflow.json", self.root / "export", self.origin, "verified")

    def test_changed_source_never_exports(self):
        self.fixture()
        (self.root / "outputs.json").write_text("changed")
        with self.assertRaises(ValueError):
            export(self.root / "workflow.json", self.root / "export", self.origin, "verified")
        self.assertFalse((self.root / "export").exists())

    def test_another_origin_never_exports(self):
        manifest = self.fixture()
        manifest["origin"] = dict(self.origin, run_id="different")
        (self.root / "workflow.json").write_bytes(encoded(manifest))
        with self.assertRaises(ValueError):
            export(self.root / "workflow.json", self.root / "export", self.origin, "verified")

    def test_placeholder_config_never_executes(self):
        with patch("record.subprocess.run") as execute:
            with self.assertRaises(ValueError):
                record(Path(__file__).with_name("record.example.json"), self.root / "recorded", "rcdo")
            execute.assert_not_called()


if __name__ == "__main__":
    unittest.main()
