"""Freshness and closure tests for the private ZPA transform cohort."""
import contextlib
import hashlib
import io
import json
import os
import tempfile
import unittest

from engine import transform_catalog
from tools import zpa_transform_cohort_catalog as cohort_catalog


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOG_PATH = os.path.join(
    ROOT, "catalogs", "zpa-transform-cohort-catalog.v1.json"
)


class ZpaTransformCohortCatalogTest(unittest.TestCase):
    def test_committed_catalog_is_fresh(self):
        with open(CATALOG_PATH, encoding="utf-8") as f:
            committed = f.read()
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


if __name__ == "__main__":
    unittest.main()
