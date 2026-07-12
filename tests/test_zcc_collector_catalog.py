"""Freshness and closure tests for the private ZCC collector catalog."""
import hashlib
import json
import os
import tempfile
import unittest
from unittest import mock

from tools import zcc_collector_catalog as collector_catalog


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOG_PATH = os.path.join(
    ROOT, "catalogs", "zcc-collector-catalog.v1.json"
)


class ZccCollectorCatalogTest(unittest.TestCase):
    def _committed_text(self):
        with open(CATALOG_PATH, encoding="utf-8") as f:
            return f.read()

    def _mutated_registry(self, mutate):
        original = collector_catalog._read_json("packs/zcc/registry.json")
        mutate(original)
        original_read = collector_catalog._read_json

        def read_json(relative_path):
            if relative_path == "packs/zcc/registry.json":
                return original
            return original_read(relative_path)

        return read_json

    def test_committed_catalog_is_fresh_and_source_bound(self):
        committed = self._committed_text()
        self.assertEqual(committed, collector_catalog.render_catalog())
        self.assertEqual(
            hashlib.sha256(committed.encode("utf-8")).hexdigest(),
            "e2e169b5a83dbc240de7b218914332d5f7f3241417e63a8d1663430a2a81f90b",
        )
        catalog = collector_catalog.build_catalog()
        self.assertEqual(
            [resource["type"] for resource in catalog["resources"]],
            list(collector_catalog.COHORT_RESOURCES),
        )
        self.assertEqual(catalog["provider"], {
            "source": "zscaler/zcc",
            "version": "0.1.0-beta.1",
        })
        self.assertRegex(catalog["sources_sha256"], r"^[0-9a-f]{64}$")

    def test_cli_check_rejects_stale_bytes(self):
        with tempfile.NamedTemporaryFile(mode="w+", encoding="utf-8") as f:
            f.write("{}\n")
            f.flush()
            self.assertEqual(collector_catalog.main(["--check", f.name]), 1)

    def test_path_pagination_envelope_and_cohort_drift_fail_closed(self):
        mutations = [
            lambda value: value["zcc_device_cleanup"]["fetch"].update({
                "path": "zcc/papi/public/v1/other",
            }),
            lambda value: value["zcc_failopen_policy"]["fetch"].update({
                "pagination": "single",
            }),
            lambda value: value["zcc_trusted_network"]["fetch"].update({
                "envelope": "items",
            }),
            lambda value: value["zcc_notification_template"].update({
                "fetch": {
                    "pagination": "single",
                    "path": "zcc/papi/public/v1/future",
                },
            }),
        ]
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                with mock.patch.object(
                        collector_catalog, "_read_json",
                        side_effect=self._mutated_registry(mutate)):
                    with self.assertRaisesRegex(ValueError, "drift|cohort"):
                        collector_catalog.build_catalog()

    def test_unknown_registry_metadata_is_not_silently_dropped(self):
        def mutate(value):
            value["zcc_failopen_policy"]["fetch"]["query"] = {
                "secretFutureControl": "must-not-be-ignored",
            }

        with mock.patch.object(
                collector_catalog, "_read_json",
                side_effect=self._mutated_registry(mutate)):
            with self.assertRaisesRegex(
                    ValueError, "fetch metadata has unsupported key query"):
                collector_catalog.build_catalog()

    def test_pack_provider_pin_and_shared_dependency_are_frozen(self):
        original_read = collector_catalog._read_json
        for key, value in [
                ("pin", "99.0.0"),
                ("requires_shared", []),
                ("provider_sources", {"zcc": "other/zcc"})]:
            manifest = original_read("packs/zcc/pack.json")
            manifest[key] = value

            def read_json(relative_path, candidate=manifest):
                if relative_path == "packs/zcc/pack.json":
                    return candidate
                return original_read(relative_path)

            with self.subTest(key=key):
                with mock.patch.object(
                        collector_catalog, "_read_json",
                        side_effect=read_json):
                    with self.assertRaisesRegex(
                            ValueError, "provider metadata drifted"):
                        collector_catalog.build_catalog()

    def test_repository_authority_ignores_ambient_pack_root(self):
        with tempfile.TemporaryDirectory() as alternate:
            pack_dir = os.path.join(alternate, "zcc")
            os.makedirs(pack_dir)
            with open(
                    os.path.join(pack_dir, "registry.json"), "w",
                    encoding="utf-8") as f:
                json.dump({
                    "zcc_device_cleanup": {
                        "fetch": {
                            "pagination": "single",
                            "path": "private/poisoned/path",
                        },
                        "generate": True,
                        "product": "zcc",
                    },
                }, f)
            with open(
                    os.path.join(pack_dir, "pack.json"), "w",
                    encoding="utf-8") as f:
                json.dump({"pin": "99.99.99"}, f)
            with mock.patch.dict(
                    os.environ, {"INFRAWRIGHT_PACKS": alternate}):
                self.assertEqual(
                    collector_catalog.render_catalog(),
                    self._committed_text(),
                )


if __name__ == "__main__":
    unittest.main()
