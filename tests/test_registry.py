"""Tests for engine.registry."""
import json
import os
import shutil
import tempfile
import unittest

from engine import packs
from engine.adoption_status import disposition_for, known_hold_paths
from engine.collectors.rest import pagination_styles
from engine.headroom_report import classify_resource, provider_resources
from engine.registry import (
    check_duplicate_resource_types,
    derive_entry,
    derived_types,
    fetch_entry,
    generated_types,
    load_registry,
    reload_registry,
    validate_registry,
)


EXPECTED_PROVIDER_HEADROOM = {
    "zpa_private_cloud": "zpa",
}


def _registry_pack_product_tokens():
    # Independent expectation source: pack.json manifests of registry-bearing
    # packs, never the registry under test. Assumes provider_prefixes values
    # equal the pack's registry 'product' tokens — true for zia/zpa/zcc; a
    # future pack where provider != product must extend this derivation.
    declared = set()
    root = packs.packs_root()
    if not os.path.isdir(root):
        return declared
    for name in sorted(os.listdir(root)):
        manifest_path = os.path.join(root, name, "pack.json")
        registry_path = os.path.join(root, name, "registry.json")
        if not os.path.isfile(manifest_path) or not os.path.isfile(registry_path):
            continue
        with open(manifest_path, encoding="utf-8") as f:
            manifest = json.load(f)
        declared.update(manifest.get("provider_prefixes", {}).values())
    return declared


class CheckDuplicateResourceTypesTest(unittest.TestCase):
    def test_none_data_entries_are_skipped(self):
        check_duplicate_resource_types([
            ("a.json", None),
            ("b.json", {"sample_x": {"product": "sample"}}),
        ])

    def test_duplicate_across_files_names_first_owner(self):
        with self.assertRaises(ValueError) as ctx:
            check_duplicate_resource_types([
                ("a.json", {"sample_x": {"product": "sample"}}),
                ("b.json", {"sample_x": {"product": "sample"}}),
            ])
        self.assertIn(
            "b.json: duplicate resource type 'sample_x' "
            "already loaded from a.json",
            str(ctx.exception),
        )

    def test_first_duplicate_in_insertion_order_is_reported(self):
        with self.assertRaises(ValueError) as ctx:
            check_duplicate_resource_types([
                ("a.json", {
                    "sample_m": {"product": "sample"},
                    "sample_z": {"product": "sample"},
                }),
                ("b.json", {
                    "sample_z": {"product": "sample"},
                    "sample_m": {"product": "sample"},
                }),
            ])
        self.assertIn("'sample_z'", str(ctx.exception))


class RegistryTest(unittest.TestCase):
    def test_generated_types_sorted(self):
        resources = provider_resources()
        headroom = dict(
            (rt, resources[rt])
            for rt in EXPECTED_PROVIDER_HEADROOM
            if rt in resources
        )
        self.assertEqual(headroom, EXPECTED_PROVIDER_HEADROOM)
        self.assertEqual(
            generated_types(),
            sorted(
                rt for rt in resources
                if rt not in EXPECTED_PROVIDER_HEADROOM
            ),
        )
        registry = load_registry()
        for rt, product in sorted(EXPECTED_PROVIDER_HEADROOM.items()):
            self.assertEqual(
                classify_resource(rt, product, registry, {"dispositions": {}})[0],
                "module-ready",
            )

    def test_derived_resource_has_no_fetch(self):
        # a derived resource is generated from another's pull, never fetched
        self.assertEqual(derived_types(), ["zpa_policy_access_rule_reorder"])
        d = derive_entry("zpa_policy_access_rule_reorder")
        self.assertEqual(d["from"], "zpa_policy_access_rule")
        with self.assertRaises(KeyError):
            fetch_entry("zpa_policy_access_rule_reorder")
        self.assertIsNone(derive_entry("zpa_policy_access_rule"))

    def test_fetch_entry_shape(self):
        e = fetch_entry("zpa_segment_group")
        self.assertEqual(e["product"], "zpa")
        self.assertEqual(e["path"], "segmentGroup")
        self.assertEqual(e["pagination"], "zpa")

        singleton = fetch_entry("zia_sandbox_behavioral_analysis")
        self.assertEqual(singleton["product"], "zia")
        self.assertEqual(
            singleton["path"], "behavioralAnalysisAdvancedSettings"
        )
        self.assertEqual(singleton["pagination"], "single")
        self.assertNotIn("optional_http_statuses", singleton)

        # The provider merges /security and /security/advanced. Until the
        # collector has an object-merge primitive, a single fetch would be
        # lossy and must remain unavailable.
        with self.assertRaises(KeyError):
            fetch_entry("zia_security_settings")

    def test_zia_http_header_fetch_entries_are_flat_unpaginated_lists(self):
        expected = {
            "zia_http_header_action_profile": "httpHeaderActionProfile",
            "zia_http_header_profile": "httpHeaderProfile",
        }
        registry = load_registry()
        for resource_type, path in sorted(expected.items()):
            with self.subTest(resource_type=resource_type):
                entry = fetch_entry(resource_type)
                self.assertEqual(entry["product"], "zia")
                self.assertEqual(entry["path"], path)
                self.assertEqual(entry["pagination"], "single")
                self.assertTrue(registry[resource_type]["generate"])
                self.assertEqual(
                    classify_resource(
                        resource_type,
                        "zia",
                        registry,
                        {"dispositions": {}},
                    )[0],
                    "managed-fetch",
                )

    def test_fetch_entry_unknown_raises(self):
        with self.assertRaises(KeyError):
            fetch_entry("zpa_nope")

    def test_every_entry_has_product(self):
        declared = _registry_pack_product_tokens()
        for rt, e in load_registry().items():
            self.assertIn(e["product"], declared, rt)

    def test_generators_and_fetch_consume_registry(self):
        import engine.collectors.rest as fetch
        for rt in generated_types():
            self.assertIn(rt, load_registry())
        self.assertEqual(
            sorted(fetch.products_in_manifest()),
            sorted(_registry_pack_product_tokens()),
        )

    def test_reload_registry(self):
        reg = reload_registry()
        self.assertEqual(reg, load_registry())
        self.assertIn("zpa_segment_group", reg)


class PackRegistryValidationTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="pack-registry-validation-")
        self.prev = os.environ.get("INFRAWRIGHT_PACKS")
        packs.reset()
        reload_registry()

    def tearDown(self):
        if self.prev is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev
        packs.reset()
        reload_registry()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _pack_metadata(self):
        return {
            "pin": "1.0.0",
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "vendor": "sample",
        }

    def _registry_metadata(self, resource_type="sample_resource"):
        return {
            resource_type: {
                "generate": True,
                "product": "sample",
                "fetch": {
                    "pagination": "single",
                    "path": "sample/path",
                },
            },
        }

    def _write_pack(self, name, pack=None, registry=None):
        root = os.path.join(self.tmp, name)
        os.makedirs(root)
        with open(os.path.join(root, "pack.json"), "w", encoding="utf-8") as f:
            json.dump(pack or self._pack_metadata(), f)
        if registry is not None:
            with open(os.path.join(root, "registry.json"), "w", encoding="utf-8") as f:
                json.dump(registry, f)

    def _activate_tmp_packs(self):
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        packs.reset()
        reload_registry()

    def test_current_committed_pack_metadata_validates(self):
        for manifest in packs._manifests():
            self.assertIn("_name", manifest)

    def test_current_committed_registries_validate(self):
        for path in packs.registry_paths():
            with open(path, encoding="utf-8") as f:
                validate_registry(json.load(f), path=path)

    def test_current_committed_pagination_values_are_supported(self):
        seen = set()
        for path in packs.registry_paths():
            with open(path, encoding="utf-8") as f:
                data = json.load(f)
            validate_registry(data, path=path)
            for entry in data.values():
                if "fetch" in entry:
                    seen.add(entry["fetch"]["pagination"])
        self.assertTrue(seen.issubset(pagination_styles()))

    def test_zia_singleton_adopt_constants_are_declared(self):
        expected = {
            "zia_advanced_settings": "advanced_settings",
            "zia_advanced_threat_settings": "advanced_threat_settings",
            "zia_atp_malicious_urls": "all_urls",
            "zia_atp_malware_inspection": "inspection",
            "zia_atp_malware_policy": "policy",
            "zia_atp_malware_protocols": "protocol",
            "zia_atp_malware_settings": "malware_settings",
            "zia_atp_security_exceptions": "bypass_url",
            "zia_auth_settings_urls": "all_urls",
            "zia_browser_control_policy": "browser_settings",
            "zia_end_user_notification": "enduser_notification",
            "zia_ftp_control_policy": "ftp_control",
            "zia_mobile_malware_protection_policy": "mobile_settings",
            "zia_sandbox_behavioral_analysis": "sandbox_settings",
            "zia_security_settings": "all_urls",
            "zia_url_filtering_and_cloud_app_settings": "app_setting",
        }
        registry = load_registry()
        for resource_type, constant in sorted(expected.items()):
            adopt = registry[resource_type]["adopt"]
            self.assertEqual(adopt["constant_key"], constant)
            self.assertEqual(adopt["import_id"], constant)

    def test_resolved_zia_name_holds_are_removed_precisely(self):
        self.assertEqual(
            known_hold_paths("zia_advanced_settings"),
            [
                "domain_fronting_bypass_apps",
                "domain_fronting_exempted_url_categories",
            ],
        )
        self.assertEqual(
            known_hold_paths("zia_advanced_threat_settings"),
            [
                "alert_for_unknown_or_suspicious_c2_traffic",
                "wpad_blocked",
            ],
        )
        self.assertEqual(known_hold_paths("zia_end_user_notification"), [])

    def test_sandbox_entitlement_diagnostic_is_explicit_and_fail_closed(self):
        disposition = disposition_for("zia_sandbox_behavioral_analysis")
        self.assertEqual(disposition["status"], "entitlement-gated")
        self.assertIn("Custom File Hash feature", disposition["reason"])
        self.assertIn("fail-closed", disposition["reason"])

    def test_all_supported_pagination_values_validate(self):
        for pagination in sorted(pagination_styles()):
            data = self._registry_metadata()
            data["sample_resource"]["fetch"]["pagination"] = pagination
            validate_registry(data, path="packs/sample/registry.json")

    def test_bad_fetch_pagination_value_fails(self):
        data = self._registry_metadata()
        data["sample_resource"]["fetch"]["pagination"] = "ziaa"
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        msg = str(ctx.exception)
        self.assertIn("packs/sample/registry.json.sample_resource.fetch.pagination", msg)
        self.assertIn("'ziaa'", msg)
        self.assertIn("allowed values:", msg)
        for value in sorted(pagination_styles()):
            self.assertIn(value, msg)

    def test_derive_policy_type_remains_open_data_value(self):
        data = {
            "sample_reorder": {
                "generate": True,
                "product": "sample",
                "derive": {
                    "from": "sample_resource",
                    "policy_type": "CUSTOM_POLICY",
                },
            },
        }
        validate_registry(data, path="packs/sample/registry.json")

    def test_adopt_constant_key_validates_as_string(self):
        data = self._registry_metadata()
        data["sample_resource"]["adopt"] = {
            "constant_key": "settings",
            "import_id": "settings",
        }
        validate_registry(data, path="packs/sample/registry.json")

        data["sample_resource"]["adopt"]["constant_key"] = ""
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        self.assertIn("sample_resource.adopt.constant_key", str(ctx.exception))

    def test_adopt_constant_key_rejects_explicit_key_field(self):
        data = self._registry_metadata()
        data["sample_resource"]["adopt"] = {
            "constant_key": "settings",
            "key_field": "name",
            "import_id": "settings",
        }
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        self.assertIn("cannot set both constant_key and key_field", str(ctx.exception))

    def test_adopt_constant_key_requires_explicit_import_id(self):
        data = self._registry_metadata()
        data["sample_resource"]["adopt"] = {
            "constant_key": "settings",
        }
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        self.assertIn("constant_key requires import_id", str(ctx.exception))

    def test_adopt_skip_if_lte_validates_as_list(self):
        data = self._registry_metadata()
        data["sample_resource"]["adopt"] = {
            "skip_if_lte": [{"order": 0}],
        }
        validate_registry(data, path="packs/sample/registry.json")

        data["sample_resource"]["adopt"]["skip_if_lte"] = {"order": 0}
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        self.assertIn("sample_resource.adopt.skip_if_lte", str(ctx.exception))

        data["sample_resource"]["adopt"]["skip_if_lte"] = [{"order": "0"}]
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        self.assertIn("finite JSON number", str(ctx.exception))

    def test_unknown_key_in_pack_json_fails(self):
        data = self._pack_metadata()
        data["rename"] = {}
        with self.assertRaises(ValueError) as ctx:
            packs.validate_pack_metadata(data, path="packs/sample/pack.json")
        self.assertIn("unknown key rename", str(ctx.exception))

    def test_missing_required_key_in_pack_json_fails(self):
        data = self._pack_metadata()
        data["lookup_sources"] = {"sample_resource": {}}
        with self.assertRaises(ValueError) as ctx:
            packs.validate_pack_metadata(data, path="packs/sample/pack.json")
        self.assertIn("missing required key name_field", str(ctx.exception))

    def test_wrong_type_in_pack_json_fails(self):
        data = self._pack_metadata()
        data["provider_prefixes"] = []
        with self.assertRaises(ValueError) as ctx:
            packs.validate_pack_metadata(data, path="packs/sample/pack.json")
        self.assertIn("provider_prefixes must be an object", str(ctx.exception))

    def test_unknown_per_resource_key_in_registry_fails(self):
        data = self._registry_metadata()
        data["sample_resource"]["rename"] = {}
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        self.assertIn("unknown key rename", str(ctx.exception))

    def test_slug_group_must_be_boolean(self):
        data = self._registry_metadata()
        data["sample_resource"]["slug_group"] = False
        validate_registry(data, path="packs/sample/registry.json")

        data["sample_resource"]["slug_group"] = "false"
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        self.assertIn("sample_resource.slug_group", str(ctx.exception))

    def test_missing_required_per_resource_key_in_registry_fails(self):
        data = self._registry_metadata()
        del data["sample_resource"]["product"]
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        self.assertIn("missing required key product", str(ctx.exception))

    def test_wrong_type_in_registry_fails(self):
        data = self._registry_metadata()
        data["sample_resource"]["fetch"]["optional_http_statuses"] = ["403"]
        with self.assertRaises(ValueError) as ctx:
            validate_registry(data, path="packs/sample/registry.json")
        self.assertIn("optional_http_statuses[0] must be an integer", str(ctx.exception))

    def test_duplicate_resource_type_across_registries_fails(self):
        self._write_pack("one", registry=self._registry_metadata("sample_resource"))
        self._write_pack("two", registry=self._registry_metadata("sample_resource"))
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        packs.reset()
        with self.assertRaises(ValueError) as ctx:
            reload_registry()
        self.assertIn("duplicate resource type 'sample_resource'", str(ctx.exception))

    def test_existing_registry_lookups_still_work_with_valid_pack(self):
        self._write_pack("one", registry=self._registry_metadata("sample_resource"))
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        packs.reset()
        reload_registry()
        self.assertEqual(generated_types(), ["sample_resource"])
        entry = fetch_entry("sample_resource")
        self.assertEqual(entry["product"], "sample")
        self.assertEqual(entry["path"], "sample/path")
