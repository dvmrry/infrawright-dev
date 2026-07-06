"""Tests for shared manifest structural validators."""
import unittest

from engine import manifest_checks


class ManifestChecksTest(unittest.TestCase):
    def test_reject_unknown_keys_names_first_sorted_offender(self):
        with self.assertRaises(ValueError) as ctx:
            manifest_checks.reject_unknown_keys(
                {"z": 1, "a": 2}, set(["known"]), "pack.json"
            )
        self.assertEqual(str(ctx.exception), "pack.json: unknown key a")

    def test_reject_unknown_keys_accepts_allowed_keys(self):
        self.assertIsNone(
            manifest_checks.reject_unknown_keys(
                {"known": 1}, set(["known"]), "pack.json"
            )
        )

    def test_require_keys_names_first_sorted_missing_key(self):
        with self.assertRaises(ValueError) as ctx:
            manifest_checks.require_keys({}, set(["z", "a"]), "registry.json")
        self.assertEqual(str(ctx.exception), "registry.json: missing required key a")

    def test_require_keys_accepts_present_keys(self):
        self.assertIsNone(
            manifest_checks.require_keys(
                {"a": 1, "z": 2}, set(["z", "a"]), "registry.json"
            )
        )

    def test_require_non_empty_string_rejects_non_string(self):
        with self.assertRaises(ValueError) as ctx:
            manifest_checks.require_non_empty_string(None, "registry.json.type")
        self.assertEqual(
            str(ctx.exception),
            "registry.json.type must be a non-empty string",
        )

    def test_require_non_empty_string_rejects_empty_string(self):
        with self.assertRaises(ValueError) as ctx:
            manifest_checks.require_non_empty_string("", "registry.json.type")
        self.assertEqual(
            str(ctx.exception),
            "registry.json.type must be a non-empty string",
        )

    def test_require_non_empty_string_accepts_non_empty_string(self):
        self.assertIsNone(
            manifest_checks.require_non_empty_string("value", "registry.json.type")
        )

    def test_validate_string_map_rejects_non_dict(self):
        with self.assertRaises(ValueError) as ctx:
            manifest_checks.validate_string_map([], "pack.json.provider_prefixes")
        self.assertEqual(
            str(ctx.exception),
            "pack.json.provider_prefixes must be an object",
        )

    def test_validate_string_map_rejects_empty_key(self):
        with self.assertRaises(ValueError) as ctx:
            manifest_checks.validate_string_map({"": "zia"}, "pack.json.provider_prefixes")
        self.assertEqual(
            str(ctx.exception),
            "pack.json.provider_prefixes keys must be non-empty strings",
        )

    def test_validate_string_map_rejects_non_string_value(self):
        with self.assertRaises(ValueError) as ctx:
            manifest_checks.validate_string_map(
                {"zia_": None}, "pack.json.provider_prefixes"
            )
        self.assertEqual(
            str(ctx.exception),
            "pack.json.provider_prefixes.zia_ must be a non-empty string",
        )

    def test_validate_string_map_accepts_string_map(self):
        self.assertIsNone(
            manifest_checks.validate_string_map(
                {"zia_": "zia"}, "pack.json.provider_prefixes"
            )
        )


if __name__ == "__main__":
    unittest.main()
