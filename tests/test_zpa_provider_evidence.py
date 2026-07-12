import copy
import importlib.util
import json
import os
import unittest


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TOOL_PATH = os.path.join(ROOT, "tools", "zpa_provider_evidence.py")
MATRIX_PATH = os.path.join(
    ROOT, "docs", "evidence", "zpa-provider-v4.4.6.json")

SPEC = importlib.util.spec_from_file_location(
    "zpa_provider_evidence_tool", TOOL_PATH)
evidence = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(evidence)


def _matrix():
    with open(MATRIX_PATH, encoding="utf-8") as f:
        return json.load(f)


class ZpaProviderEvidenceTest(unittest.TestCase):
    def test_committed_matrix_matches_local_pack_and_schema(self):
        report = evidence.validate_local(_matrix())
        self.assertEqual(report["summary"], {
            "fetch_backed_resources": 16,
            "generated_config_runtime_gates": 16,
            "numeric_or_alternate_importers": 14,
            "passthrough_importers": 2,
            "resources_with_sensitive_inputs": 1,
            "schema_id_not_source_populated": 3,
        })

    def test_load_bearing_identity_exceptions_are_explicit(self):
        resources = dict(
            (item["resource_type"], item) for item in _matrix()["resources"])
        for resource_type in (
            "zpa_ba_certificate",
            "zpa_emergency_access_user",
            "zpa_inspection_profile",
        ):
            self.assertEqual(
                resources[resource_type]["read_identity"]["schema_id_attribute"],
                "not_source_populated",
            )
        self.assertEqual(
            resources["zpa_application_segment"]["read_identity"],
            {
                "schema_id_attribute": "read_response_id",
                "terraform_instance_id": (
                    "current_id_lookup_with_response_schema_id"
                ),
            },
        )
        self.assertIn(
            "importer_writes_undeclared_profile_id",
            resources["zpa_inspection_profile"]["exceptions"],
        )

    def test_sensitive_credential_boundary_is_explicit(self):
        resources = dict(
            (item["resource_type"], item) for item in _matrix()["resources"])
        sensitive = dict(
            (resource_type, item["state_shape"]["sensitive_input_paths"])
            for resource_type, item in resources.items()
            if item["state_shape"]["sensitive_input_paths"]
        )
        self.assertEqual(sensitive, {
            "zpa_pra_credential_controller": [
                "passphrase", "password", "private_key",
            ],
        })

    def test_generated_config_cannot_be_marked_qualified(self):
        report = copy.deepcopy(_matrix())
        report["resources"][0]["generated_config"]["qualification"] = (
            "qualified"
        )
        with self.assertRaisesRegex(
                evidence.EvidenceError, "overclaims generated-config"):
            evidence.validate_local(report)

    def test_registry_and_schema_drift_fail_closed(self):
        report = copy.deepcopy(_matrix())
        report["resources"][0]["fetch"]["path"] = "different"
        with self.assertRaisesRegex(evidence.EvidenceError, "fetch metadata"):
            evidence.validate_local(report)

        report = copy.deepcopy(_matrix())
        report["resources"][0]["state_shape"]["counts"][
            "input_attributes"
        ] += 1
        with self.assertRaisesRegex(evidence.EvidenceError, "state-shape"):
            evidence.validate_local(report)

    def test_every_claim_has_a_pinned_source_range(self):
        report = evidence.validate_local(_matrix())
        hashes = []
        for resource in report["resources"]:
            source = resource["source_evidence"]
            anchors = [source["importer"], source["read_identity"]]
            anchors.extend(source["exceptions"].values())
            for anchor in anchors:
                self.assertTrue(anchor["url"].startswith(
                    evidence.PROVIDER_REPOSITORY
                    + "/blob/" + evidence.PROVIDER_REF + "/"
                ))
                hashes.append(anchor["sha256"])
        self.assertGreater(len(hashes), 32)

    @unittest.skipUnless(
        os.environ.get("ZPA_PROVIDER_SOURCE"),
        "set ZPA_PROVIDER_SOURCE to audit the external pinned checkout",
    )
    def test_external_pinned_provider_source(self):
        report = evidence.validate_local(_matrix())
        evidence.validate_provider_source(
            report, os.environ["ZPA_PROVIDER_SOURCE"])


if __name__ == "__main__":
    unittest.main()
