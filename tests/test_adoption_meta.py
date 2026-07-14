import json
import os
import shutil
import tempfile
import unittest

from engine import packs
from engine import registry
from engine import transform
from engine.adoption_meta import (
    adoption_entry,
    classify_raw_items,
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
            "pin": "1.2.3",
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

    def test_skip_identity_item_supports_numeric_lte_matchers(self):
        self._registry({"sample_resource": {"generate": True, "product": "sample"}})
        self._override("sample_resource", {
            "skip_if_lte": [{"order": 0}],
        })
        meta = adoption_entry("sample_resource")

        self.assertTrue(skip_identity_item(
            identity_item({"id": "1", "name": "Zero", "order": 0},
                          "sample_resource"),
            meta,
        ))
        self.assertTrue(skip_identity_item(
            identity_item({"id": "2", "name": "String Zero", "order": "0"},
                          "sample_resource"),
            meta,
        ))
        self.assertFalse(skip_identity_item(
            identity_item({"id": "3", "name": "Managed", "order": 1},
                          "sample_resource"),
            meta,
        ))
        self.assertFalse(skip_identity_item(
            identity_item({"id": "4", "name": "Bool", "order": False},
                          "sample_resource"),
            meta,
        ))

    def test_raw_system_and_strict_unsupported_classification_precede_identity(self):
        rule = {
            "evidence": ["https://example.invalid/provider-source"],
            "match": {"action": "ISOLATE"},
            "provider": {"source": "example/sample", "version": "1.2.3"},
            "reason": "provider cannot round-trip this object",
        }
        self._registry({
            "sample_resource": {
                "adopt": {
                    "identity_fields": {"import_id": "details.missing"},
                    "key_field": "missing_name",
                    "skip_if": [{"system": True}],
                    "unsupported_if": [rule],
                },
                "generate": True,
                "product": "sample",
            },
        })
        classified = classify_raw_items([
            {"action": "ISOLATE", "system": True},
            {"action": "ISOLATE", "system": False},
            {"action": "BLOCK", "system": False},
        ], "sample_resource")
        self.assertEqual(len(classified["skipped"]), 1)
        self.assertEqual(len(classified["unsupported"]), 1)
        self.assertEqual(len(classified["eligible"]), 1)

        self._registry({
            "sample_resource": {
                "adopt": {
                    "unsupported_if": [dict(rule, match={"marker": 1})],
                },
                "generate": True,
                "product": "sample",
            },
        })
        strict = classify_raw_items([
            {"id": "bool", "name": "Boolean", "marker": True},
            {"id": "number", "name": "Number", "marker": 1},
        ], "sample_resource")
        self.assertEqual([item["id"] for item in strict["eligible"]], ["bool"])
        self.assertEqual(
            [item["item"]["id"] for item in strict["unsupported"]],
            ["number"],
        )

    def test_strict_scalar_matchers_keep_transform_and_adopt_aligned(self):
        cases = [
            (
                "true does not equal one",
                {"system": True},
                [
                    {"id": "true", "system": True},
                    {"id": "one", "system": 1},
                ],
                ["true"],
            ),
            (
                "one does not equal true",
                {"system": 1},
                [
                    {"id": "one", "system": 1},
                    {"id": "true", "system": True},
                ],
                ["one"],
            ),
            (
                "false does not equal zero",
                {"system": False},
                [
                    {"id": "false", "system": False},
                    {"id": "zero", "system": 0},
                ],
                ["false"],
            ),
            (
                "zero does not equal false",
                {"system": 0},
                [
                    {"id": "zero", "system": 0},
                    {"id": "false", "system": False},
                ],
                ["zero"],
            ),
            (
                "explicit null does not equal absence",
                {"system": None},
                [
                    {"id": "null", "system": None},
                    {"id": "absent"},
                ],
                ["null"],
            ),
        ]
        rule = {
            "evidence": ["https://example.invalid/provider-source"],
            "provider": {"source": "example/sample", "version": "1.2.3"},
            "reason": "provider cannot round-trip this object",
        }

        for name, matcher, items, expected_ids in cases:
            with self.subTest(name=name):
                transform_ids = [
                    item["id"] for item in items
                    if transform.skip_item_match_reason(
                        transform.snake_keys(item), {"skip_if": [matcher]}
                    ) == "skip_if"
                ]
                self._registry({
                    "sample_resource": {
                        "adopt": {"skip_if": [matcher]},
                        "generate": True,
                        "product": "sample",
                    },
                })
                skipped_ids = [
                    entry["item"]["id"]
                    for entry in classify_raw_items(items, "sample_resource")["skipped"]
                ]
                self._registry({
                    "sample_resource": {
                        "adopt": {
                            "unsupported_if": [dict(rule, match=matcher)],
                        },
                        "generate": True,
                        "product": "sample",
                    },
                })
                unsupported_ids = [
                    entry["item"]["id"]
                    for entry in classify_raw_items(
                        items, "sample_resource"
                    )["unsupported"]
                ]

                self.assertEqual(transform_ids, expected_ids)
                self.assertEqual(skipped_ids, expected_ids)
                self.assertEqual(unsupported_ids, expected_ids)

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

    def test_constant_key_derives_literal_key_without_identity_field(self):
        self._registry({
            "sample_singleton": {
                "generate": True,
                "product": "sample",
                "adopt": {
                    "constant_key": "settings",
                    "import_id": "settings",
                },
            }
        })
        meta = adoption_entry("sample_singleton")
        item = identity_item({"enabled": True}, "sample_singleton")

        self.assertEqual(derive_key_from_identity(item, meta), "settings")
        self.assertEqual(
            derive_import_id_from_identity(
                item, meta, "sample_singleton", "settings"
            ),
            "settings",
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

    def test_identity_field_alias_derives_import_id_without_renaming_source(self):
        self._registry({
            "sample_resource": {
                "generate": True,
                "product": "sample",
                "adopt": {
                    "key_field": "name",
                    "identity_fields": {
                        "raw_id": "uuid",
                        "import_id": "uuid",
                    },
                },
            }
        })
        meta = adoption_entry("sample_resource")
        item = identity_item(
            {"name": "D1 Database", "uuid": "db-123"},
            "sample_resource",
        )

        self.assertEqual(meta["import_id"], "{import_id}")
        self.assertEqual(item["uuid"], "db-123")
        self.assertEqual(item["raw_id"], "db-123")
        self.assertEqual(item["import_id"], "db-123")
        self.assertEqual(derive_key_from_identity(item, meta), "d1_database")
        self.assertEqual(
            derive_import_id_from_identity(
                item, meta, "sample_resource", "d1_database"
            ),
            "db-123",
        )

    def test_missing_identity_field_alias_fails_loudly(self):
        self._registry({
            "sample_resource": {
                "generate": True,
                "product": "sample",
                "adopt": {
                    "key_field": "name",
                    "identity_fields": {"import_id": "uuid"},
                },
            }
        })

        with self.assertRaises(KeyError) as ctx:
            identity_item({"name": "D1 Database"}, "sample_resource")

        msg = str(ctx.exception)
        self.assertIn("sample_resource", msg)
        self.assertIn("import_id", msg)
        self.assertIn("uuid", msg)

    def test_identity_field_alias_does_not_override_explicit_import_id(self):
        self._registry({
            "sample_resource": {
                "generate": True,
                "product": "sample",
                "adopt": {
                    "key_field": "name",
                    "identity_fields": {"import_id": "uuid"},
                    "import_id": "{legacy_id}",
                },
            }
        })
        meta = adoption_entry("sample_resource")
        item = identity_item(
            {
                "name": "D1 Database",
                "uuid": "db-123",
                "legacyId": "legacy-999",
            },
            "sample_resource",
        )

        self.assertEqual(meta["import_id"], "{legacy_id}")
        self.assertEqual(item["import_id"], "db-123")
        self.assertEqual(
            derive_import_id_from_identity(
                item, meta, "sample_resource", "d1_database"
            ),
            "legacy-999",
        )


if __name__ == "__main__":
    unittest.main()
