"""Freshness and closure tests for the private ZPA transform cohort."""
import contextlib
import hashlib
import io
import json
import os
import shutil
import tempfile
import unittest
from unittest import mock

from engine import packs
from engine import registry
from engine import tfschema
from engine import transform_catalog
from tools import zpa_transform_cohort_catalog as cohort_catalog


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOG_PATH = os.path.join(
    ROOT, "catalogs", "zpa-transform-cohort-catalog.v1.json"
)


class ZpaTransformCohortCatalogTest(unittest.TestCase):
    def _committed_text(self):
        with open(CATALOG_PATH, encoding="utf-8") as f:
            return f.read()

    def _copy_zpa_pack_root(self, destination):
        shutil.copytree(
            os.path.join(ROOT, "packs", "zpa"),
            os.path.join(destination, "zpa"),
        )

    def _rewrite_json(self, path, mutate):
        with open(path, encoding="utf-8") as f:
            value = json.load(f)
        mutate(value)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(value, f, indent=2, sort_keys=True)
            f.write("\n")

    def _reset_loader_state(self):
        packs.reset()
        registry.reload_registry()
        tfschema._cache.clear()

    def test_committed_catalog_is_fresh(self):
        committed = self._committed_text()
        self.assertEqual(committed, cohort_catalog.render_catalog())
        self.assertEqual(
            hashlib.sha256(committed.encode("utf-8")).hexdigest(),
            "eab7f5ce8f3e508629cd6a3cebd344332f57647442741717762e7373e2ae5694",
        )

    def test_catalog_is_exactly_the_reviewed_two_resource_cohort(self):
        catalog = cohort_catalog.build_catalog()
        self.assertEqual(catalog["kind"], cohort_catalog.CATALOG_KIND)
        self.assertEqual(catalog["product"], "zpa")
        self.assertEqual(
            [resource["type"] for resource in catalog["resources"]],
            [
                "zpa_pra_console_controller",
                "zpa_pra_portal_controller",
            ],
        )
        self.assertEqual(
            catalog["provider"],
            {
                "evidence_commit": (
                    "dcf12469a9a8f648be0691c74e9816fc94ec7ddc"
                ),
                "source": "zscaler/zpa",
                "version": "4.4.6",
            },
        )
        self.assertEqual(
            catalog["python_compatibility_source"],
            "catalogs/zcc-transform-catalog.v1.json",
        )
        self.assertEqual(
            catalog["sources_sha256"],
            "e1dbc94cd82cfb824e88cfa2db3cc7398787369557d16dc23b660a1c2302a149",
        )

    def test_evidence_decorator_preserves_exact_generic_resources(self):
        decorated = cohort_catalog.build_catalog()["resources"]
        stripped = []
        for resource in decorated:
            core = dict(resource)
            self.assertIsNotNone(core.pop("provider_evidence"))
            stripped.append(core)
        generic = transform_catalog.transform_resource_cohort(
            "zpa", list(cohort_catalog.COHORT_RESOURCES)
        )
        self.assertEqual(stripped, generic["resources"])

    def test_no_resource_override_can_appear_without_review(self):
        catalog = cohort_catalog.build_catalog()
        self.assertEqual(
            catalog["absent_override_files"],
            [
                "packs/zpa/overrides/zpa_pra_console_controller.json",
                "packs/zpa/overrides/zpa_pra_portal_controller.json",
            ],
        )
        for relative_path in catalog["absent_override_files"]:
            self.assertFalse(os.path.exists(os.path.join(ROOT, relative_path)))

    def test_projection_keeps_only_current_kernel_encodings(self):
        catalog = cohort_catalog.build_catalog()
        encodings = []
        ignored = {}

        def visit(projection):
            encodings.extend(projection["attributes"].values())
            for block in projection["blocks"].values():
                self.assertIn(block["cardinality"], ("single", "many"))
                visit(block["projection"])

        for resource in catalog["resources"]:
            projection = resource["projection"]
            visit(projection)
            ignored[resource["type"]] = projection[
                "silently_ignored_attributes"
            ]
        self.assertIn(["set", "string"], encodings)
        self.assertNotIn(["map", "string"], encodings)
        console = catalog["resources"][0]["projection"]["blocks"]
        self.assertEqual(sorted(console), ["pra_application", "pra_portals"])
        self.assertEqual(
            ignored,
            {
                "zpa_pra_console_controller": ["id"],
                "zpa_pra_portal_controller": ["id"],
            },
        )

    def test_check_mode_rejects_catalog_drift(self):
        catalog = cohort_catalog.build_catalog()
        catalog["sources_sha256"] = "0" * 64
        with tempfile.NamedTemporaryFile(
                mode="w", encoding="utf-8", suffix=".json") as f:
            json.dump(catalog, f, indent=2, sort_keys=True)
            f.write("\n")
            f.flush()
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                self.assertEqual(cohort_catalog.main(["--check", f.name]), 1)
            self.assertIn("catalog is stale", stderr.getvalue())

    def test_repository_authority_ignores_alternate_selected_override(self):
        with tempfile.TemporaryDirectory() as alternate:
            self._copy_zpa_pack_root(alternate)
            override = os.path.join(
                alternate,
                "zpa",
                "overrides",
                "zpa_pra_console_controller.json",
            )
            with open(override, "w", encoding="utf-8") as f:
                json.dump({"key_field": "id"}, f)
                f.write("\n")
            try:
                with mock.patch.dict(
                        os.environ, {"INFRAWRIGHT_PACKS": alternate}):
                    self._reset_loader_state()
                    poisoned = transform_catalog.transform_resource_cohort(
                        "zpa", list(cohort_catalog.COHORT_RESOURCES)
                    )
                    self.assertEqual(
                        poisoned["resources"][0]["key_fields"], ["id"]
                    )
                    self.assertEqual(
                        cohort_catalog.render_catalog(),
                        self._committed_text(),
                    )
            finally:
                self._reset_loader_state()

    def test_repository_authority_ignores_alternate_schema_registry_and_pin(
            self):
        with tempfile.TemporaryDirectory() as alternate:
            self._copy_zpa_pack_root(alternate)
            pack_dir = os.path.join(alternate, "zpa")
            self._rewrite_json(
                os.path.join(pack_dir, "pack.json"),
                lambda value: value.update({"pin": "99.99.99"}),
            )

            def mutate_registry(value):
                value["zpa_pra_console_controller"]["fetch"]["path"] = (
                    "poisonedConsole"
                )

            self._rewrite_json(
                os.path.join(pack_dir, "registry.json"), mutate_registry
            )

            def mutate_schema(value):
                value["resource_schemas"][
                    "zpa_pra_console_controller"
                ]["block"]["attributes"]["name"]["type"] = "bool"

            self._rewrite_json(
                os.path.join(pack_dir, "schemas", "provider", "zpa.json"),
                mutate_schema,
            )
            try:
                with mock.patch.dict(
                        os.environ, {"INFRAWRIGHT_PACKS": alternate}):
                    self._reset_loader_state()
                    poisoned = transform_catalog.transform_resource_cohort(
                        "zpa", list(cohort_catalog.COHORT_RESOURCES)
                    )
                    self.assertEqual(
                        poisoned["resources"][0]["projection"][
                            "attributes"
                        ]["name"],
                        "bool",
                    )
                    self.assertEqual(packs.provider_pins()["zpa"], "99.99.99")
                    self.assertEqual(
                        cohort_catalog.render_catalog(),
                        self._committed_text(),
                    )
            finally:
                self._reset_loader_state()

    def test_fresh_compiler_ignores_poisoned_parent_caches(self):
        with tempfile.TemporaryDirectory() as alternate:
            self._copy_zpa_pack_root(alternate)
            pack_dir = os.path.join(alternate, "zpa")

            def mutate_registry(value):
                value["zpa_pra_console_controller"]["fetch"]["path"] = (
                    "cachedPoison"
                )

            self._rewrite_json(
                os.path.join(pack_dir, "registry.json"), mutate_registry
            )

            def mutate_schema(value):
                value["resource_schemas"][
                    "zpa_pra_console_controller"
                ]["block"]["attributes"]["name"]["type"] = "bool"

            self._rewrite_json(
                os.path.join(pack_dir, "schemas", "provider", "zpa.json"),
                mutate_schema,
            )
            try:
                with mock.patch.dict(
                        os.environ, {"INFRAWRIGHT_PACKS": alternate}):
                    packs.reset()
                    registry.reload_registry()
                    tfschema._cache.clear()
                    self.assertEqual(
                        registry.load_registry()[
                            "zpa_pra_console_controller"
                        ]["fetch"]["path"],
                        "cachedPoison",
                    )
                    self.assertEqual(
                        tfschema.load_resource(
                            "zpa_pra_console_controller"
                        )["block"]["attributes"]["name"]["type"],
                        "bool",
                    )
                # The alternate environment is gone, but the parent process
                # still carries both poisoned caches at this point.
                self.assertEqual(
                    cohort_catalog.render_catalog(),
                    self._committed_text(),
                )
            finally:
                self._reset_loader_state()

    def test_unknown_fetch_metadata_fails_instead_of_being_dropped(self):
        original_read = cohort_catalog._read_json
        poisoned = original_read("packs/zpa/registry.json")
        poisoned["zpa_pra_console_controller"]["fetch"]["query"] = {
            "future": "surface"
        }

        def read_json(relative_path):
            if relative_path == "packs/zpa/registry.json":
                return poisoned
            return original_read(relative_path)

        with mock.patch.object(
                cohort_catalog, "_read_json", side_effect=read_json):
            with self.assertRaisesRegex(
                    ValueError, "fetch metadata has unsupported key query"):
                cohort_catalog.build_catalog()

    def test_duplicate_provider_evidence_rows_fail_closed(self):
        original_read = cohort_catalog._read_json
        poisoned = original_read("docs/evidence/zpa-provider-v4.4.6.json")
        selected = next(
            row for row in poisoned["resources"]
            if row["resource_type"] == "zpa_pra_console_controller"
        )
        poisoned["resources"].append(dict(selected))

        def read_json(relative_path):
            if relative_path == "docs/evidence/zpa-provider-v4.4.6.json":
                return poisoned
            return original_read(relative_path)

        with mock.patch.object(
                cohort_catalog, "_read_json", side_effect=read_json):
            with self.assertRaisesRegex(
                    ValueError, "provider evidence duplicates"):
                cohort_catalog.build_catalog()


if __name__ == "__main__":
    unittest.main()
