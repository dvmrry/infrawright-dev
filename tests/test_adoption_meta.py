import json
import os
import shutil
import tempfile
import unittest

from engine import packs
from engine import registry
from engine.adoption_meta import (
    adoption_entry,
    derive_import_id_from_identity,
    derive_key_from_identity,
    identity_item,
    skip_identity_item,
)


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)


class AdoptionMetaTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="adoption-meta-")
        self.prev = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        os.makedirs(os.path.join(self.tmp, "sample", "overrides"), exist_ok=True)
        _write_json(os.path.join(self.tmp, "sample", "pack.json"), {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
        })
        packs.reset()
        registry.reload_registry()

    def tearDown(self):
        if self.prev is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev
        packs.reset()
        registry.reload_registry()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _registry(self, data):
        _write_json(os.path.join(self.tmp, "sample", "registry.json"), data)
        registry.reload_registry()

    def _override(self, resource_type, data):
        _write_json(
            os.path.join(self.tmp, "sample", "overrides", resource_type + ".json"),
            data,
        )

    def test_falls_back_to_override_identity_fields(self):
        self._registry({"sample_resource": {"generate": True, "product": "sample"}})
        self._override("sample_resource", {
            "key_field": "display_name",
            "import_id": "{object_id}",
            "renames": {"objectId": "object_id"},
            "skip_if": [{"system": True}],
            "drops": ["ignored_by_adoption"],
        })
        meta = adoption_entry("sample_resource")
        self.assertEqual(meta["key_field"], "display_name")
        self.assertEqual(meta["import_id"], "{object_id}")
        item = identity_item(
            {"displayName": "Prod App", "objectId": "123", "system": True},
            "sample_resource",
        )
        self.assertEqual(item["object_id"], "123")
        self.assertTrue(skip_identity_item(item, meta))

    def test_registry_adopt_overrides_legacy_override(self):
        self._registry({
            "sample_resource": {
                "generate": True,
                "product": "sample",
                "adopt": {
                    "key_field": "displayName",
                    "import_id": "{id}",
                    "identity_renames": {"objectId": "id"},
                },
            }
        })
        self._override("sample_resource", {
            "key_field": "legacy_name",
            "import_id": "{legacy_id}",
            "renames": {"legacyId": "legacy_id"},
        })
        meta = adoption_entry("sample_resource")
        item = identity_item(
            {"displayName": "Prod App", "objectId": "123"},
            "sample_resource",
        )
        self.assertEqual(derive_key_from_identity(item, meta), "prod_app")
        self.assertEqual(
            derive_import_id_from_identity(item, meta, "sample_resource", "prod_app"),
            "123",
        )

    def test_missing_key_and_import_fields_fail_loudly(self):
        self._registry({
            "sample_resource": {
                "generate": True,
                "product": "sample",
                "adopt": {"key_field": "name", "import_id": "{id}"},
            }
        })
        meta = adoption_entry("sample_resource")
        with self.assertRaises(KeyError):
            derive_key_from_identity({"id": "123"}, meta)
        with self.assertRaises(ValueError):
            derive_import_id_from_identity(
                {"name": "Prod"}, meta, "sample_resource", "prod"
            )

    def test_dotted_key_field_uses_snaked_path_segments(self):
        self._registry({
            "sample_resource": {
                "generate": True,
                "product": "sample",
                "adopt": {"key_field": "tags.DisplayName"},
            }
        })
        meta = adoption_entry("sample_resource")
        item = identity_item(
            {"tags": {"DisplayName": "Prod App"}, "id": "123"},
            "sample_resource",
        )
        self.assertEqual(derive_key_from_identity(item, meta), "prod_app")


if __name__ == "__main__":
    unittest.main()
