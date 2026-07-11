"""Contract tests for the Python-authored ZCC transform catalog."""
import json
import os
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from engine import packs
from engine import transform_catalog


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOG_PATH = os.path.join(
    ROOT, "catalogs", "zcc-transform-catalog.v1.json"
)
SCHEMA_PATH = os.path.join(
    ROOT, "docs", "schemas", "transform-catalog.schema.json"
)


class TransformCatalogTest(unittest.TestCase):
    def _catalog(self):
        with open(CATALOG_PATH, encoding="utf-8") as f:
            return json.load(f)

    def test_committed_catalog_matches_exact_rendered_bytes(self):
        with open(CATALOG_PATH, encoding="utf-8") as f:
            committed = f.read()
        self.assertEqual(
            committed, transform_catalog.render_catalog("zcc")
        )
        self.assertTrue(committed.endswith("\n"))

    def test_catalog_is_the_closed_five_resource_slice(self):
        catalog = self._catalog()
        self.assertEqual(catalog["kind"], "infrawright.transform_catalog")
        self.assertEqual(catalog["schema_version"], 1)
        self.assertEqual(catalog["product"], "zcc")
        self.assertEqual(
            [resource["type"] for resource in catalog["resources"]],
            list(transform_catalog.ZCC_FETCH_RESOURCES),
        )
        self.assertEqual(
            catalog["source_files"],
            [
                "zcc/overrides/zcc_device_cleanup.json",
                "zcc/overrides/zcc_failopen_policy.json",
                "zcc/overrides/zcc_forwarding_profile.json",
                "zcc/overrides/zcc_trusted_network.json",
                "zcc/overrides/zcc_web_privacy.json",
                "zcc/pack.json",
                "zcc/registry.json",
                "zcc/schemas/provider/zcc.json",
            ],
        )

    def test_transform_semantics_are_explicit(self):
        resources = dict(
            (resource["type"], resource)
            for resource in self._catalog()["resources"]
        )
        self.assertEqual(resources["zcc_device_cleanup"]["key_fields"], ["id"])
        failopen = resources["zcc_failopen_policy"]
        self.assertEqual(failopen["html_unescape_passes"], 2)
        self.assertEqual(
            failopen["invert_bool"],
            [
                "active",
                "enable_captive_portal_detection",
                "enable_fail_open",
                "enable_web_sec_on_proxy_unreachable",
                "enable_web_sec_on_tunnel_failure",
            ],
        )
        forwarding = resources["zcc_forwarding_profile"]
        self.assertEqual(forwarding["html_unescape_passes"], 0)
        self.assertEqual(forwarding["key_fields"], ["name"])
        actions = forwarding["projection"]["blocks"][
            "forwarding_profile_actions"
        ]
        self.assertEqual(actions["cardinality"], "many")
        self.assertEqual(
            actions["projection"]["blocks"]["system_proxy_data"][
                "cardinality"
            ],
            "single",
        )
        trusted = resources["zcc_trusted_network"]
        self.assertEqual(trusted["renames"]["network_name"], "name")
        self.assertIn("dns_server_ips", trusted["split_csv"])
        self.assertEqual(trusted["lookup_source"], {"name_field": "name"})
        forwarding_references = forwarding["references"]
        self.assertEqual(
            forwarding_references,
            {
                "trusted_network_ids": {
                    "name_field": "network_name",
                    "referent": "zcc_trusted_network",
                },
                "trusted_network_ids_selected": {
                    "name_field": "network_name",
                    "referent": "zcc_trusted_network",
                },
            },
        )
        for resource in resources.values():
            self.assertEqual(
                resource["import_id"],
                {
                    "segments": [{"field": "id"}],
                    "template": "{id}",
                },
            )
            self.assertEqual(
                resource["projection"]["silently_ignored_attributes"],
                ["id"],
            )
            if resource["type"] != "zcc_trusted_network":
                self.assertIsNone(resource["lookup_source"])
            if resource["type"] != "zcc_forwarding_profile":
                self.assertEqual(resource["references"], {})

    def test_python_html_unescape_tables_are_complete_and_stable(self):
        compatibility = self._catalog()["python_compatibility"][
            "html_unescape"
        ]
        entities = compatibility["entities"]
        invalid_references = compatibility["invalid_references"]
        invalid_codepoints = compatibility["invalid_codepoints"]
        self.assertEqual(len(entities), 2231)
        self.assertEqual(len(invalid_references), 34)
        self.assertEqual(len(invalid_codepoints), 126)
        self.assertEqual(entities["AMP"], "&")
        self.assertEqual(entities["AMP;"], "&")
        self.assertEqual(entities["NotEqualTilde;"], "\u2242\u0338")
        self.assertEqual(invalid_references["0"], "\ufffd")
        self.assertEqual(invalid_references["13"], "\r")
        self.assertEqual(invalid_references["128"], "\u20ac")
        for codepoint in (1, 11, 127, 64976, 1114111):
            self.assertIn(codepoint, invalid_codepoints)

    def test_schema_is_closed_and_names_the_contract(self):
        with open(SCHEMA_PATH, encoding="utf-8") as f:
            schema = json.load(f)
        self.assertFalse(schema["additionalProperties"])
        self.assertEqual(
            schema["properties"]["kind"]["const"],
            "infrawright.transform_catalog",
        )
        self.assertEqual(schema["properties"]["product"]["const"], "zcc")
        self.assertEqual(schema["properties"]["resources"]["minItems"], 5)
        self.assertEqual(schema["properties"]["resources"]["maxItems"], 5)

    def test_cli_out_and_check_are_exact_byte_gates(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = os.path.join(tmp, "catalog.json")
            generated = subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "engine.transform_catalog",
                    "--product",
                    "zcc",
                    "--out",
                    output,
                ],
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
                    "zcc",
                    "--check",
                    output,
                ],
                cwd=ROOT,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                universal_newlines=True,
            )
            self.assertEqual(checked.returncode, 0, checked.stderr)
            with open(output, "a", encoding="utf-8") as f:
                f.write(" ")
            stale = subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "engine.transform_catalog",
                    "--product",
                    "zcc",
                    "--check",
                    output,
                ],
                cwd=ROOT,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                universal_newlines=True,
            )
            self.assertEqual(stale.returncode, 1)
            self.assertIn("transform catalog is stale", stale.stderr)

    def test_new_fetch_resource_fails_closed(self):
        resources = list(transform_catalog.ZCC_FETCH_RESOURCES)
        resources.append("zcc_new_fetch_resource")
        with self.assertRaisesRegex(
                ValueError, "unsupported: zcc_new_fetch_resource"):
            transform_catalog._validate_fetch_resources(resources)

    def test_unsupported_transform_override_key_fails_closed(self):
        with self.assertRaisesRegex(
                ValueError, "unsupported transform override key 'defaults'"):
            transform_catalog._supported_override(
                "zcc_device_cleanup", {"defaults": {"active": False}}
            )

    def test_duplicate_invert_bool_fails_instead_of_cancelling_semantics(self):
        override = {
            "invert_bool": ["active", "active"],
            "key_field": "id",
        }
        with mock.patch.object(
                transform_catalog, "load_override", return_value=override):
            with self.assertRaisesRegex(
                    ValueError,
                    "zcc_failopen_policy.invert_bool duplicates 'active'"):
                transform_catalog._resource(
                    "zcc_failopen_policy",
                    set(transform_catalog.ZCC_FETCH_RESOURCES),
                    {},
                    {},
                )

    def test_string_list_override_values_must_be_non_empty_strings(self):
        with self.assertRaisesRegex(
                ValueError, r"zcc_trusted_network.split_csv\[1\]"):
            transform_catalog._supported_override(
                "zcc_trusted_network", {"split_csv": ["ssid", 3]}
            )

    def test_composite_key_field_order_is_preserved(self):
        self.assertEqual(
            transform_catalog._supported_override(
                "zcc_device_cleanup",
                {"key_field": ["second", "first"]},
            ),
            ["second", "first"],
        )

    def test_import_id_segments_are_deterministic_and_merge_literals(self):
        compiled = transform_catalog._compile_import_id(
            "zcc_device_cleanup",
            {"import_id": "prefix-{{x}}-{id}-suffix"},
            {"id"},
        )
        self.assertEqual(
            compiled,
            {
                "segments": [
                    {"literal": "prefix-{x}-"},
                    {"field": "id"},
                    {"literal": "-suffix"},
                ],
                "template": "prefix-{{x}}-{id}-suffix",
            },
        )

    def test_import_id_template_rejects_unfrozen_formatter_features(self):
        cases = [
            "{}",
            "{0}",
            "{id!r}",
            "{id:>3}",
            "{id.value}",
            "{id[0]}",
            "{camelCase}",
            "{missing}",
            "{id",
        ]
        for template in cases:
            with self.subTest(template=template):
                with self.assertRaisesRegex(ValueError, "import_id template"):
                    transform_catalog._compile_import_id(
                        "zcc_device_cleanup",
                        {"import_id": template},
                        {"id"},
                    )

    def test_import_id_fields_follow_normalized_original_names(self):
        block = {
            "attributes": {
                "id": {"type": "string", "computed": True},
                "old_name": {"type": "string", "optional": True},
            }
        }
        available = transform_catalog._normalized_original_fields(
            block, {"renames": {"old_name": "new_name"}}
        )
        self.assertEqual(available, {"id", "new_name"})
        with self.assertRaisesRegex(ValueError, "unavailable"):
            transform_catalog._compile_import_id(
                "zcc_device_cleanup",
                {"import_id": "{old_name}"},
                available,
            )

    def test_reference_metadata_fails_closed_on_invalid_topology(self):
        projection = {
            "attributes": {"trusted_network_ids": ["list", "number"]},
            "blocks": {},
            "silently_ignored_attributes": ["id"],
        }
        valid_reference = {
            "referent": "zcc_trusted_network",
            "name_field": "network_name",
        }
        with self.assertRaisesRegex(ValueError, "absent from its projection"):
            transform_catalog._resource_references(
                "zcc_forwarding_profile",
                {"zcc_forwarding_profile": {"missing": valid_reference}},
                {"zcc_trusted_network": {"name_field": "name"}},
                projection,
                set(transform_catalog.ZCC_FETCH_RESOURCES),
            )
        with self.assertRaisesRegex(ValueError, "unsupported referent"):
            transform_catalog._resource_references(
                "zcc_forwarding_profile",
                {"zcc_forwarding_profile": {
                    "trusted_network_ids": {
                        "referent": "zcc_future_resource",
                        "name_field": "network_name",
                    },
                }},
                {},
                projection,
                set(transform_catalog.ZCC_FETCH_RESOURCES),
            )
        with self.assertRaisesRegex(ValueError, "without lookup_source"):
            transform_catalog._resource_references(
                "zcc_forwarding_profile",
                {"zcc_forwarding_profile": {
                    "trusted_network_ids": valid_reference,
                }},
                {},
                projection,
                set(transform_catalog.ZCC_FETCH_RESOURCES),
            )

    def test_unsupported_projection_encoding_fails_closed(self):
        with self.assertRaisesRegex(ValueError, "unsupported type encoding"):
            transform_catalog._catalog_encoding(
                ["map", "string"], "zcc_device_cleanup.sample"
            )

    def test_set_projection_encoding_fails_until_sorting_is_supported(self):
        with self.assertRaisesRegex(ValueError, "unsupported type encoding"):
            transform_catalog._catalog_encoding(
                ["set", "string"], "zcc_device_cleanup.sample"
            )

    def test_unknown_product_fails_closed(self):
        with self.assertRaisesRegex(ValueError, "unsupported.*product"):
            transform_catalog.transform_catalog("zia")

    def test_unrelated_pack_cannot_enable_catalog_html_unescape(self):
        previous = os.environ.get("INFRAWRIGHT_PACKS")
        with tempfile.TemporaryDirectory() as tmp:
            owner = os.path.join(tmp, "owner")
            unrelated = os.path.join(tmp, "unrelated")
            os.makedirs(owner)
            os.makedirs(unrelated)
            with open(
                    os.path.join(owner, "pack.json"),
                    "w", encoding="utf-8") as f:
                json.dump({
                    "provider_prefixes": {"zcc_": "zcc"},
                }, f)
            with open(
                    os.path.join(unrelated, "pack.json"),
                    "w", encoding="utf-8") as f:
                json.dump({
                    "provider_prefixes": {"other_": "other"},
                    "unescape_products": ["zcc_"],
                    "lookup_sources": {
                        "zcc_device_cleanup": {"name_field": "id"},
                    },
                    "references": {
                        "zcc_failopen_policy": {
                            "active": {
                                "name_field": "id",
                                "referent": "zcc_device_cleanup",
                            },
                        },
                    },
                }, f)
            os.environ["INFRAWRIGHT_PACKS"] = tmp
            packs.reset()
            try:
                self.assertEqual(packs.unescape_products(), ("zcc_",))
                self.assertEqual(
                    packs.unescape_products_for_provider("zcc"), ()
                )
                self.assertIn("zcc_device_cleanup", packs.lookup_sources())
                self.assertEqual(
                    packs.lookup_sources_for_provider("zcc"), {}
                )
                self.assertIn("zcc_failopen_policy", packs.references())
                self.assertEqual(
                    packs.references_for_provider("zcc"), {}
                )
                self.assertEqual(
                    transform_catalog._html_unescape_passes(
                        "zcc_device_cleanup", {}
                    ),
                    0,
                )
            finally:
                if previous is None:
                    os.environ.pop("INFRAWRIGHT_PACKS", None)
                else:
                    os.environ["INFRAWRIGHT_PACKS"] = previous
                packs.reset()


if __name__ == "__main__":
    unittest.main()
