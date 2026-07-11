"""Contract tests for the Python-authored ZCC adoption catalog."""
import json
import os
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from engine import adoption_catalog


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOG_PATH = os.path.join(
    ROOT, "catalogs", "zcc-adoption-catalog.v1.json"
)
SCHEMA_PATH = os.path.join(
    ROOT, "docs", "schemas", "zcc-adoption-catalog.schema.json"
)


class AdoptionCatalogTest(unittest.TestCase):
    def _catalog(self):
        with open(CATALOG_PATH, encoding="utf-8") as f:
            return json.load(f)

    def _resources(self):
        return dict(
            (resource["type"], resource)
            for resource in self._catalog()["resources"]
        )

    def test_committed_catalog_matches_exact_rendered_bytes(self):
        with open(CATALOG_PATH, encoding="utf-8") as f:
            committed = f.read()
        self.assertEqual(
            committed, adoption_catalog.render_catalog("zcc")
        )
        self.assertTrue(committed.endswith("\n"))

    def test_unrelated_global_lookup_metadata_cannot_change_zcc_catalog(self):
        expected = adoption_catalog.render_catalog("zcc")
        unrelated_global_lookups = {
            "zcc_device_cleanup": {"name_field": "unrelated_name"},
            "zcc_trusted_network": {"name_field": "poisoned_name"},
        }
        with mock.patch.object(
                adoption_catalog.packs,
                "lookup_sources",
                return_value=unrelated_global_lookups):
            actual = adoption_catalog.render_catalog("zcc")
        self.assertEqual(actual, expected)

    def test_lookup_source_entry_must_be_an_object(self):
        with self.assertRaisesRegex(
                ValueError,
                "zcc_trusted_network lookup source must contain an object"):
            adoption_catalog._resource(
                "zcc_trusted_network",
                {"zcc_trusted_network": "not-an-object"},
            )

    def test_lookup_name_field_must_be_a_projected_attribute(self):
        with self.assertRaisesRegex(
                ValueError,
                "name_field 'id' is not a projected attribute"):
            adoption_catalog._resource(
                "zcc_trusted_network",
                {"zcc_trusted_network": {"name_field": "id"}},
            )

    def test_catalog_is_the_closed_five_resource_slice(self):
        catalog = self._catalog()
        self.assertEqual(catalog["kind"], "infrawright.adoption_catalog")
        self.assertEqual(catalog["schema_version"], 1)
        self.assertEqual(catalog["product"], "zcc")
        self.assertEqual(
            catalog["provider"],
            {
                "name": "zcc",
                "source": "zscaler/zcc",
                "version": "0.1.0-beta.1",
            },
        )
        self.assertEqual(
            [resource["type"] for resource in catalog["resources"]],
            list(adoption_catalog.ZCC_ADOPTION_RESOURCES),
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

    def test_identity_contract_matches_current_adoption_metadata(self):
        resources = self._resources()
        expected_keys = {
            "zcc_device_cleanup": ["id"],
            "zcc_failopen_policy": ["id"],
            "zcc_forwarding_profile": ["name"],
            "zcc_trusted_network": ["name"],
            "zcc_web_privacy": ["id"],
        }
        for resource_type, resource in resources.items():
            identity = resource["identity"]
            self.assertIsNone(identity["constant_key"])
            self.assertEqual(identity["key_fields"], expected_keys[resource_type])
            self.assertEqual(identity["identity_fields"], {})
            self.assertEqual(identity["skip_if"], [])
            self.assertEqual(identity["skip_if_lte"], [])
            self.assertEqual(
                identity["import_id"],
                {
                    "segments": [{"field": "id"}],
                    "template": "{id}",
                },
            )
        self.assertEqual(
            resources["zcc_trusted_network"]["identity"][
                "identity_renames"
            ],
            {
                "dns_servers": "dns_server_ips",
                "hostnames": "hostname",
                "network_name": "name",
                "ssids": "ssid",
                "trusted_dhcp_servers": "trusted_dhcp_servers_ips",
                "trusted_gateways": "trusted_gateway_ips",
                "trusted_subnets": "trusted_subnet_ips",
            },
        )
        for resource_type, resource in resources.items():
            if resource_type != "zcc_trusted_network":
                self.assertEqual(
                    resource["identity"]["identity_renames"], {}
                )

        self.assertEqual(
            resources["zcc_trusted_network"]["lookup_source"],
            {"name_field": "name"},
        )
        for resource_type, resource in resources.items():
            if resource_type != "zcc_trusted_network":
                self.assertIsNone(resource["lookup_source"])

    def test_projection_classification_and_types_are_recursive(self):
        resources = self._resources()
        forwarding = resources["zcc_forwarding_profile"]["projection"]
        self.assertEqual(
            forwarding["attributes"]["name"],
            {
                "encoding": "string",
                "provider_sensitive": False,
                "status": "required",
            },
        )
        self.assertEqual(
            forwarding["attributes"]["trusted_network_ids"]["encoding"],
            ["list", "number"],
        )
        actions = forwarding["blocks"]["forwarding_profile_actions"]
        self.assertEqual(actions["status"], "optional")
        self.assertEqual(actions["nesting_mode"], "list")
        self.assertEqual(actions["cardinality"], "many")
        self.assertEqual(
            actions["projection"]["attributes"]["action_type"]["status"],
            "optional",
        )
        system_proxy = actions["projection"]["blocks"]["system_proxy_data"]
        self.assertEqual(system_proxy["status"], "optional")
        self.assertEqual(system_proxy["nesting_mode"], "single")
        self.assertEqual(system_proxy["cardinality"], "single")
        self.assertEqual(
            system_proxy["projection"]["attributes"]["pac_url"]["encoding"],
            "string",
        )

        trusted = resources["zcc_trusted_network"]["projection"]
        self.assertEqual(trusted["attributes"]["active"]["status"], "required")
        self.assertEqual(
            trusted["attributes"]["condition_type"]["status"], "required"
        )
        self.assertEqual(
            trusted["attributes"]["dns_server_ips"]["encoding"],
            ["list", "string"],
        )

    def test_projection_explicitly_classifies_all_computed_and_sensitive_paths(self):
        def walk(projection):
            for attribute in projection["attributes"].values():
                self.assertIn(attribute["status"], ("required", "optional"))
                self.assertIs(attribute["provider_sensitive"], False)
            for block in projection["blocks"].values():
                self.assertIn(block["status"], ("required", "optional"))
                walk(block["projection"])

        for resource in self._catalog()["resources"]:
            projection = resource["projection"]
            self.assertEqual(projection["computed_only_attributes"], ["id"])
            self.assertEqual(projection["computed_only_blocks"], [])
            walk(projection)

    def test_schema_is_closed_and_names_the_contract(self):
        with open(SCHEMA_PATH, encoding="utf-8") as f:
            schema = json.load(f)
        self.assertFalse(schema["additionalProperties"])
        self.assertEqual(
            schema["properties"]["kind"]["const"],
            "infrawright.adoption_catalog",
        )
        self.assertEqual(schema["properties"]["product"]["const"], "zcc")
        self.assertEqual(schema["properties"]["resources"]["minItems"], 5)
        self.assertEqual(schema["properties"]["resources"]["maxItems"], 5)
        self.assertFalse(schema["$defs"]["projection"]["additionalProperties"])
        self.assertFalse(schema["$defs"]["identity"]["additionalProperties"])

    def test_cli_out_and_check_are_exact_byte_gates(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = os.path.join(tmp, "catalog.json")
            generated = subprocess.run(
                [
                    sys.executable,
                    "-m",
                    "engine.adoption_catalog",
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
                    "engine.adoption_catalog",
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
                    "engine.adoption_catalog",
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
            self.assertIn("adoption catalog is stale", stale.stderr)

    def test_new_fetch_resource_fails_closed(self):
        resources = list(adoption_catalog.ZCC_ADOPTION_RESOURCES)
        resources.append("zcc_new_fetch_resource")
        with self.assertRaisesRegex(
                ValueError, "unsupported: zcc_new_fetch_resource"):
            adoption_catalog._validate_fetch_resources(resources)

    def test_unknown_product_fails_closed(self):
        with self.assertRaisesRegex(ValueError, "unsupported.*product"):
            adoption_catalog.adoption_catalog("zia")

    def test_identity_paths_are_canonical_and_rename_chains_fail_closed(self):
        self.assertEqual(
            adoption_catalog._field_path("outer.DisplayName", "sample"),
            "outer.display_name",
        )
        with self.assertRaisesRegex(ValueError, "order-dependent chain"):
            adoption_catalog._identity_renames(
                "zcc_sample", {"rawName": "name", "name": "display_name"}
            )
        with self.assertRaisesRegex(ValueError, "target collision"):
            adoption_catalog._identity_renames(
                "zcc_sample", {"rawName": "name", "otherName": "name"}
            )

    def test_import_id_rejects_unfrozen_formatter_features(self):
        cases = ["{}", "{0}", "{id!r}", "{id:>3}", "{id.value}", "{id[0]}"]
        for template in cases:
            with self.subTest(template=template):
                with self.assertRaisesRegex(ValueError, "import_id template"):
                    adoption_catalog._compile_import_id(
                        "zcc_sample", template, {"id"}
                    )

    def test_unsupported_projection_encoding_fails_closed(self):
        for encoding in (["map", "string"], ["set", "string"], "dynamic"):
            with self.subTest(encoding=encoding):
                with self.assertRaisesRegex(ValueError, "unsupported.*encoding"):
                    adoption_catalog._catalog_encoding(encoding, "sample")


if __name__ == "__main__":
    unittest.main()
