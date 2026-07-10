import copy
import glob
import io
import json
import os
import unittest
from contextlib import redirect_stderr
from contextlib import redirect_stdout
from unittest import mock

from engine import transform_adopt_parity as parity


FIXTURE_DIR = os.path.join("tests", "fixtures", "parity")


def _fixture_paths():
    return sorted(glob.glob(os.path.join(FIXTURE_DIR, "*.json")))


def _fixture_named(name):
    path = os.path.join(FIXTURE_DIR, name + ".json")
    return parity.load_fixture(path)


class TransformAdoptParityTest(unittest.TestCase):
    def test_committed_fixtures_are_fully_classified(self):
        report = parity.build_report([
            parity.load_fixture(path) for path in _fixture_paths()
        ])
        self.assertEqual(report["kind"], parity.REPORT_KIND)
        self.assertEqual(report["report_version"], 1)
        self.assertEqual(report["result"], "evidence_gates")
        self.assertEqual(report["summary"], {
            "fixtures": 4,
            "equal": 1,
            "classified_differences": 0,
            "evidence_gate_fixtures": 3,
            "review_required": 0,
            "differences": 4,
            "classified": 4,
            "unclassified": 0,
            "evidence_gates": 4,
            "accepted": 0,
            "stale_expectations": 0,
            "unacknowledged_drops": 0,
            "unaccounted_byte_differences": 0,
        })
        by_name = dict((item["name"], item) for item in report["fixtures"])
        self.assertEqual(
            by_name["zcc_failopen_policy_inversion"]["result"], "equal"
        )
        self.assertTrue(
            by_name["zcc_failopen_policy_inversion"]["outputs"]["byte_equal"]
        )
        zcc_fixture = _fixture_named("zcc_failopen_policy_inversion")
        zcc_raw = zcc_fixture["raw_items"][0]
        for field in (
                "active",
                "enableWebSecOnProxyUnreachable",
                "enableWebSecOnTunnelFailure"):
            self.assertIs(type(zcc_raw[field]), str)
        self.assertEqual(
            zcc_fixture["provenance"]["dependency_sources"][0]["version"],
            "3.8.37",
        )
        expected_paths = {
            "zcc_failopen_policy_inversion": [],
            "zia_dlp_engines_predefined_name": [
                "/items/predefined_engine/name",
            ],
            "zia_url_filtering_rules_zero_quota": [
                "/items/no_quota_rule/size_quota",
                "/items/no_quota_rule/time_quota",
            ],
            "zpa_application_segment_microtenant": [
                "/items/example_segment/microtenant_id",
            ],
        }
        self.assertEqual(
            dict(
                (name, [entry["path"] for entry in item["differences"]])
                for name, item in by_name.items()
            ),
            expected_paths,
        )

    def test_unclassified_difference_requires_review(self):
        fixture = copy.deepcopy(_fixture_named(
            "zia_dlp_engines_predefined_name"
        ))
        fixture["expected_differences"] = []
        result = parity.compare_fixture(fixture)
        self.assertEqual(result["result"], "review_required")
        self.assertEqual(result["summary"]["unclassified"], 1)
        self.assertEqual(result["summary"]["classified"], 0)
        self.assertEqual(
            result["differences"][0]["status"], "unclassified"
        )

    def test_classification_is_bound_to_both_values(self):
        fixture = copy.deepcopy(_fixture_named(
            "zia_dlp_engines_predefined_name"
        ))
        fixture["expected_differences"][0]["adopt"]["value"] = (
            "Different provider value"
        )
        result = parity.compare_fixture(fixture)
        self.assertEqual(result["result"], "review_required")
        self.assertEqual(result["summary"]["unclassified"], 1)
        self.assertEqual(result["summary"]["stale_expectations"], 1)

    def test_extra_provider_state_is_rejected(self):
        fixture = copy.deepcopy(_fixture_named(
            "zcc_failopen_policy_inversion"
        ))
        fixture["provider_state"]["unreferenced"] = {
            "values": {},
            "sensitive_values": {},
        }
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "unreferenced import id"):
            parity.compare_fixture(fixture)

    def test_missing_provider_state_is_rejected(self):
        fixture = copy.deepcopy(_fixture_named(
            "zcc_failopen_policy_inversion"
        ))
        fixture["provider_state"] = {
            "other-policy": fixture["provider_state"]["policy-001"]
        }
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "missing import id policy-001"):
            parity.compare_fixture(fixture)

    def test_fixture_rejects_unknown_keys_and_unsanitized_state(self):
        fixture = copy.deepcopy(_fixture_named(
            "zcc_failopen_policy_inversion"
        ))
        fixture["unexpected"] = True
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "unknown key unexpected"):
            parity.validate_fixture(fixture)

        fixture.pop("unexpected")
        fixture["provenance"]["sanitized"] = False
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "sanitized must be true"):
            parity.validate_fixture(fixture)

    def test_fixture_provenance_must_match_active_pack_pin(self):
        fixture = copy.deepcopy(_fixture_named(
            "zcc_failopen_policy_inversion"
        ))
        fixture["provenance"]["provider_version"] = "different-version"
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "does not match active zcc pack pin"):
            parity.validate_fixture(fixture)

    def test_fixture_provider_source_must_use_exact_pinned_ref(self):
        fixture = copy.deepcopy(_fixture_named(
            "zia_dlp_engines_predefined_name"
        ))
        fixture["provenance"]["sources"][0] = (
            "https://github.com/zscaler/terraform-provider-zia/blob/main/"
            "source.go#L1"
        )
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "GitHub blob ref pinned"):
            parity.validate_fixture(fixture)

    def test_fixture_provider_source_requires_a_file_path(self):
        fixture = copy.deepcopy(_fixture_named(
            "zia_dlp_engines_predefined_name"
        ))
        fixture["provenance"]["sources"][0] = (
            "https://github.com/zscaler/terraform-provider-zia/blob/v4.7.26"
        )
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "GitHub blob ref pinned"):
            parity.validate_fixture(fixture)

    def test_fixture_provenance_local_sources_must_exist(self):
        fixture = copy.deepcopy(_fixture_named(
            "zcc_failopen_policy_inversion"
        ))
        fixture["provenance"]["local_sources"][0] = "missing/source.json"
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "does not exist"):
            parity.validate_fixture(fixture)

    def test_classification_evidence_must_be_declared_by_provenance(self):
        fixture = copy.deepcopy(_fixture_named(
            "zia_dlp_engines_predefined_name"
        ))
        fixture["expected_differences"][0]["evidence"].append(
            "https://example.invalid/unpinned"
        )
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "not declared by fixture provenance"):
            parity.validate_fixture(fixture)

    def test_fixture_version_rejects_boolean(self):
        fixture = copy.deepcopy(_fixture_named(
            "zcc_failopen_policy_inversion"
        ))
        fixture["fixture_version"] = True
        with self.assertRaisesRegex(
                parity.ParityFixtureError, "unsupported fixture_version"):
            parity.validate_fixture(fixture)

    def test_json_diff_distinguishes_boolean_and_number(self):
        differences = parity._json_differences(
            {"items": {"one": {"enabled": False}}},
            {"items": {"one": {"enabled": 0}}},
        )
        self.assertEqual(len(differences), 1)
        self.assertEqual(
            differences[0]["path"], "/items/one/enabled"
        )

    def test_signed_zero_is_a_canonical_difference(self):
        fixture = copy.deepcopy(_fixture_named(
            "zcc_failopen_policy_inversion"
        ))
        fixture["raw_items"][0][
            "captivePortalWebSecDisableMinutes"
        ] = -0.0
        fixture["provider_state"]["policy-001"]["values"][
            "captive_portal_web_sec_disable_minutes"
        ] = 0.0
        result = parity.compare_fixture(fixture)
        self.assertEqual(result["result"], "review_required")
        self.assertFalse(result["outputs"]["byte_equal"])
        self.assertEqual(result["summary"]["unclassified"], 1)
        self.assertEqual(
            result["differences"][0]["path"],
            "/items/policy_001/captive_portal_web_sec_disable_minutes",
        )

    def test_unaccounted_byte_difference_requires_review(self):
        fixture = _fixture_named("zia_dlp_engines_predefined_name")
        with mock.patch.object(parity, "_json_differences", return_value=[]):
            result = parity.compare_fixture(fixture)
        self.assertEqual(result["result"], "review_required")
        self.assertTrue(
            result["outputs"]["unaccounted_byte_difference"]
        )
        self.assertEqual(
            result["summary"]["unaccounted_byte_differences"], 1
        )

    def test_partial_comparator_miss_requires_review(self):
        fixture = copy.deepcopy(_fixture_named(
            "zia_dlp_engines_predefined_name"
        ))
        fixture["expected_differences"][0]["disposition"] = "accepted"
        fixture["provider_state"]["101"]["values"]["description"] = (
            "Different provider description"
        )
        complete = parity.compare_fixture(fixture)
        known = next(
            entry for entry in complete["differences"]
            if entry["path"] == "/items/predefined_engine/name"
        )
        reported_only = [{
            "path": known["path"],
            "transform": known["transform"],
            "adopt": known["adopt"],
        }]
        with mock.patch.object(
                parity, "_json_differences", return_value=reported_only):
            result = parity.compare_fixture(fixture)
        self.assertEqual(result["result"], "review_required")
        self.assertEqual(result["summary"]["accepted"], 1)
        self.assertEqual(result["summary"]["unclassified"], 0)
        self.assertEqual(
            result["summary"]["unaccounted_byte_differences"], 1
        )

    def test_reported_differences_reconstruct_list_changes(self):
        transform_payload = {
            "items": {"one": {"values": [1, 2, 3]}},
        }
        adopt_payload = {
            "items": {"one": {"values": [1, 4]}},
        }
        differences = parity._json_differences(
            transform_payload, adopt_payload
        )
        self.assertEqual(
            parity._apply_reported_differences(
                transform_payload, differences
            ),
            adopt_payload,
        )

        extended = {
            "items": {"one": {"values": [1, 2, 3, 4]}},
        }
        differences = parity._json_differences(adopt_payload, extended)
        self.assertEqual(
            parity._apply_reported_differences(
                adopt_payload, differences
            ),
            extended,
        )

    def test_json_pointer_escapes_object_keys(self):
        differences = parity._json_differences(
            {"items": {"a/b~c": 1}},
            {"items": {"a/b~c": 2}},
        )
        self.assertEqual(differences[0]["path"], "/items/a~1b~0c")

    def test_cli_returns_one_for_open_evidence_gates(self):
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            code = parity.main(_fixture_paths())
        self.assertEqual(code, 1)
        self.assertEqual(stderr.getvalue(), "")
        report = json.loads(stdout.getvalue())
        self.assertEqual(report["summary"]["unclassified"], 0)
        self.assertEqual(report["result"], "evidence_gates")

    def test_accepted_difference_does_not_leave_an_evidence_gate(self):
        fixture = copy.deepcopy(_fixture_named(
            "zia_dlp_engines_predefined_name"
        ))
        fixture["expected_differences"][0]["disposition"] = "accepted"
        result = parity.compare_fixture(fixture)
        self.assertEqual(result["result"], "classified_differences")
        self.assertEqual(result["summary"]["accepted"], 1)
        self.assertEqual(result["summary"]["evidence_gates"], 0)

    def test_cli_returns_two_for_invalid_fixture(self):
        stdout = io.StringIO()
        stderr = io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            code = parity.main(["does-not-exist.json"])
        self.assertEqual(code, 2)
        self.assertEqual(stdout.getvalue(), "")
        self.assertIn("does-not-exist.json", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
