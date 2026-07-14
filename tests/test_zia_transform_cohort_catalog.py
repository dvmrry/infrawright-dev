"""Contract tests for the private three-resource ZIA transform cohort."""
import json
import os
import subprocess
import sys
import tempfile
import unittest

from engine import transform_catalog


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOG_PATH = os.path.join(
    ROOT, "catalogs", "zia-transform-cohort.v1.json"
)
ZIA_RESOURCES = (
    "zia_admin_roles",
    "zia_traffic_forwarding_static_ip",
    "zia_url_categories",
)


class ZiaTransformCohortCatalogTest(unittest.TestCase):
    def _catalog(self):
        with open(CATALOG_PATH, encoding="utf-8") as f:
            return json.load(f)

    def test_committed_catalog_matches_exact_rendered_bytes(self):
        with open(CATALOG_PATH, encoding="utf-8") as f:
            committed = f.read()
        self.assertEqual(
            committed,
            transform_catalog.render_catalog(
                "zia", resources=list(ZIA_RESOURCES)
            ),
        )
        self.assertTrue(committed.endswith("\n"))

    def test_catalog_is_the_closed_source_bound_cohort(self):
        catalog = self._catalog()
        self.assertEqual(
            catalog["kind"],
            "infrawright.transform_resource_cohort",
        )
        self.assertEqual(catalog["schema_version"], 1)
        self.assertEqual(catalog["product"], "zia")
        self.assertEqual(
            [resource["type"] for resource in catalog["resources"]],
            list(ZIA_RESOURCES),
        )
        self.assertEqual(
            catalog["source_files"],
            [
                "zia/overrides/zia_admin_roles.json",
                "zia/overrides/zia_traffic_forwarding_static_ip.json",
                "zia/overrides/zia_url_categories.json",
                "zia/pack.json",
                "zia/registry.json",
                "zia/schemas/provider/zia.json",
            ],
        )
        self.assertRegex(catalog["sources_sha256"], r"^[0-9a-f]{64}$")

    def test_catalog_captures_only_reachable_cohort_semantics(self):
        resources = dict(
            (resource["type"], resource)
            for resource in self._catalog()["resources"]
        )
        self.assertEqual(
            resources["zia_traffic_forwarding_static_ip"]["key_fields"],
            ["ip_address"],
        )
        self.assertEqual(
            resources["zia_url_categories"]["sort_lists"],
            ["urls"],
        )
        self.assertNotIn("sort_lists", resources["zia_admin_roles"])
        self.assertEqual(
            resources["zia_admin_roles"]["skip_if"],
            [{"is_non_editable": True}],
        )
        self.assertEqual(
            resources["zia_url_categories"]["lookup_source"],
            {"name_field": "configured_name"},
        )
        for resource in resources.values():
            self.assertEqual(resource["html_unescape_passes"], 0)
            self.assertEqual(
                resource["projection"]["silently_ignored_attributes"],
                ["id"],
            )
            self.assertEqual(resource["references"], {})

        # Python reports non-id computed fields as drops; the cohort must not
        # silently classify them with the universal top-level id.
        self.assertNotIn(
            "role_id",
            resources["zia_admin_roles"]["projection"]["attributes"],
        )
        self.assertNotIn(
            "static_ip_id",
            resources["zia_traffic_forwarding_static_ip"]["projection"][
                "attributes"
            ],
        )
        self.assertNotIn(
            "category_id",
            resources["zia_url_categories"]["projection"]["attributes"],
        )

    def test_default_zcc_generation_remains_byte_identical(self):
        path = os.path.join(
            ROOT, "catalogs", "zcc-transform-catalog.v1.json"
        )
        with open(path, encoding="utf-8") as f:
            committed = f.read()
        self.assertEqual(committed, transform_catalog.render_catalog("zcc"))

    def test_cli_resource_selection_is_an_exact_byte_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = os.path.join(tmp, "catalog.json")
            resource_args = []
            for resource_type in ZIA_RESOURCES:
                resource_args.extend(["--resource", resource_type])
            generated = subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "engine.transform_catalog",
                    "--product",
                    "zia",
                ] + resource_args + ["--out", output],
                cwd=ROOT,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                universal_newlines=True,
            )
            self.assertEqual(generated.returncode, 0, generated.stderr)
            with open(output, encoding="utf-8") as f:
                actual = f.read()
            with open(CATALOG_PATH, encoding="utf-8") as f:
                expected = f.read()
            self.assertEqual(actual, expected)

            checked = subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "engine.transform_catalog",
                    "--product",
                    "zia",
                ] + resource_args + ["--check", output],
                cwd=ROOT,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                universal_newlines=True,
            )
            self.assertEqual(checked.returncode, 0, checked.stderr)

    def test_selection_rejects_duplicates_and_wrong_product(self):
        with self.assertRaisesRegex(ValueError, "must be unique"):
            transform_catalog.transform_resource_cohort(
                "zia", ["zia_admin_roles", "zia_admin_roles"]
            )
        with self.assertRaisesRegex(ValueError, "belongs to product"):
            transform_catalog.transform_resource_cohort(
                "zia", ["zcc_device_cleanup"]
            )

    def test_sort_list_metadata_is_validated(self):
        with self.assertRaisesRegex(ValueError, "duplicates 'urls'"):
            transform_catalog._supported_override(
                "zia_url_categories",
                {"sort_lists": ["urls", "urls"]},
                additional_keys={"sort_lists"},
            )
        with self.assertRaisesRegex(
                ValueError, "unsupported transform override key 'sample'"):
            transform_catalog._supported_override(
                "zia_traffic_forwarding_static_ip",
                {"key_field": "ip_address", "sample": {"ignored": True}},
            )

    def test_skip_if_metadata_is_validated_for_private_cohorts(self):
        for invalid in ({}, {"is_non_editable": []}):
            with self.assertRaisesRegex(
                    ValueError, "must not be empty|value must be a scalar"):
                transform_catalog._supported_override(
                    "zia_admin_roles",
                    {"skip_if": [invalid]},
                    additional_keys={"skip_if"},
                )

    def test_default_projection_gate_remains_strictly_zcc_closed(self):
        block = {
            "attributes": {
                "id": {"type": "string", "computed": True},
                "other_id": {"type": "string", "computed": True},
                "name": {"type": "string", "optional": True},
            }
        }
        with self.assertRaisesRegex(
                ValueError, "silently ignore only.*top-level id"):
            transform_catalog._projection(
                block, "synthetic", resource_top=True
            )
        projection = transform_catalog._projection(
            block,
            "synthetic",
            resource_top=True,
            allow_reported_top_computed=True,
        )
        self.assertEqual(projection["silently_ignored_attributes"], ["id"])
        self.assertNotIn("other_id", projection["attributes"])


if __name__ == "__main__":
    unittest.main()
