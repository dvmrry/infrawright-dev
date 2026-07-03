import json
import os
import shutil
import tempfile
import unittest

from engine import artifacts
from engine import packs
from engine import registry


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)


class ArtifactsSelectorTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="artifacts-packs-")
        self.saved_packs = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        os.makedirs(os.path.join(self.tmp, "sample"), exist_ok=True)
        _write_json(os.path.join(self.tmp, "sample", "pack.json"), {
            "provider_prefixes": {"unused_": "sample"},
            "provider_sources": {"sample": "example/sample"},
        })
        _write_json(os.path.join(self.tmp, "sample", "registry.json"), {
            "resource_without_provider_prefix": {
                "generate": True,
                "product": "sample",
            },
            "sample_data_only": {
                "product": "sample",
            },
        })
        packs.reset()
        registry.reload_registry()

    def tearDown(self):
        if self.saved_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.saved_packs
        packs.reset()
        registry.reload_registry()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_product_selector_uses_registry_product_not_prefix(self):
        self.assertEqual(
            artifacts.expand_resources(["sample"]),
            ["resource_without_provider_prefix"],
        )

    def test_non_generated_exact_resource_is_rejected(self):
        with self.assertRaises(ValueError):
            artifacts.expand_resources(["sample_data_only"])

    def test_validate_resource_type_requires_exact_generated_type(self):
        artifacts.validate_resource_type("resource_without_provider_prefix")
        with self.assertRaises(ValueError):
            artifacts.validate_resource_type("../resource_without_provider_prefix")


class ArtifactsPathTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="artifacts-deployment-")
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")

    def tearDown(self):
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_overlay_keeps_flat_resource_type_paths(self):
        dep = os.path.join(self.tmp, "deployment.json")
        with open(dep, "w", encoding="utf-8") as f:
            f.write(json.dumps({"overlay": "acme"}))
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep
        self.assertEqual(
            artifacts.config_file("tenant", "sample_resource"),
            os.path.join("acme", "config", "tenant",
                         "sample_resource.auto.tfvars.json"),
        )
        self.assertEqual(
            artifacts.env_root("tenant", "sample_resource"),
            os.path.join("acme", "envs", "tenant", "sample_resource"),
        )

    def test_artifacts_does_not_import_ops(self):
        import engine.artifacts
        with open(engine.artifacts.__file__.rstrip("c"), encoding="utf-8") as f:
            source = f.read()
        self.assertNotIn("import ops", source)
        self.assertNotIn("from engine import ops", source)


if __name__ == "__main__":
    unittest.main()
