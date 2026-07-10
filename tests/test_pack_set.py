"""Tests for exact pack-set and example-requirement contracts."""
import io
import json
import os
import tempfile
import unittest
from contextlib import redirect_stderr
from contextlib import redirect_stdout

from engine import pack_set


class PackSetTest(unittest.TestCase):
    def _write(self, root, name, data):
        path = os.path.join(root, name)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f)
        return path

    def _profile(self, packs=None, shared=None, kind=None):
        return {
            "kind": kind or pack_set.PACK_SET_KIND,
            "version": 1,
            "packs": list(packs or []),
            "shared": list(shared or []),
        }

    def _pack(self, root, name):
        path = os.path.join(root, name)
        os.makedirs(path)
        self._write(path, "pack.json", {})

    def _shared(self, root, name):
        os.makedirs(os.path.join(root, "_shared", name))

    def test_full_profile_matches_committed_pack_root(self):
        result = pack_set.validate_active_pack_set("packsets/full.json")
        self.assertEqual(result["active"]["packs"], [
            "aws", "cloudflare", "google", "netbox",
            "zcc", "zia", "zpa", "ztc",
        ])
        self.assertEqual(result["active"]["shared"], ["zscaler"])

    def test_exact_profile_rejects_missing_and_undeclared_components(self):
        with tempfile.TemporaryDirectory() as root:
            self._pack(root, "one")
            self._pack(root, "extra")
            self._shared(root, "unexpected")
            profile = self._write(
                root, "profile.json", self._profile(
                    packs=["missing", "one"], shared=["required"]
                )
            )
            with self.assertRaisesRegex(
                    pack_set.PackSetError,
                    "missing packs: missing; undeclared packs: extra; "
                    "missing shared: required; undeclared shared: unexpected"):
                pack_set.validate_active_pack_set(profile, root=root)

    def test_exact_profile_rejects_manifestless_runtime_pack_data(self):
        with tempfile.TemporaryDirectory() as root:
            ghost = os.path.join(root, "ghost")
            os.makedirs(ghost)
            self._write(ghost, "registry.json", {
                "ghost_resource": {"product": "ghost"},
            })
            self._write(ghost, "adoption_status.json", {
                "dispositions": {
                    "ghost_resource": {
                        "status": "manual-only",
                        "reason": "stale partial pack",
                    },
                },
            })
            profile = self._write(
                root, "profile.json", self._profile()
            )

            with self.assertRaisesRegex(
                    pack_set.PackSetError, "undeclared packs: ghost"):
                pack_set.validate_active_pack_set(profile, root=root)

    def test_requirements_are_subset_not_exact(self):
        with tempfile.TemporaryDirectory() as root:
            self._pack(root, "one")
            self._pack(root, "two")
            self._shared(root, "common")
            requirements = self._write(
                root, "requirements.json", self._profile(
                    packs=["one"], shared=["common"],
                    kind=pack_set.REQUIREMENTS_KIND,
                )
            )
            result = pack_set.check_requirements(requirements, root=root)
            self.assertTrue(result["available"])
            self.assertEqual(result["missing"], {"packs": [], "shared": []})

    def test_requirements_cli_uses_distinct_unavailable_status(self):
        with tempfile.TemporaryDirectory() as root:
            catalog = self._write(
                root, "catalog.json", self._profile(packs=["one"])
            )
            requirements = self._write(
                root, "requirements.json", self._profile(
                    packs=["one"], kind=pack_set.REQUIREMENTS_KIND,
                )
            )
            stdout = io.StringIO()
            stderr = io.StringIO()
            with redirect_stdout(stdout), redirect_stderr(stderr):
                code = pack_set.main([
                    "--root", root, "--catalog", catalog,
                    "--requirements", requirements,
                ])
            self.assertEqual(code, 3)
            self.assertIn("packs=one", stdout.getvalue())
            self.assertEqual(stderr.getvalue(), "")

    def test_unknown_requirement_is_error_not_optional_skip(self):
        with tempfile.TemporaryDirectory() as root:
            catalog = self._write(
                root, "catalog.json", self._profile(packs=["known"])
            )
            requirements = self._write(
                root, "requirements.json", self._profile(
                    packs=["typo"], kind=pack_set.REQUIREMENTS_KIND,
                )
            )
            with self.assertRaisesRegex(
                    pack_set.PackSetError, "unknown packs: typo"):
                pack_set.check_requirements(
                    requirements, root=root, catalog_path=catalog
                )

    def test_manifest_rejects_boolean_version_unsorted_and_traversal(self):
        cases = [
            (self._profile(), "version", True, "version must be 1"),
            (self._profile(packs=["two", "one"]), None, None,
             "packs must be sorted"),
            (self._profile(packs=["../one"]), None, None,
             "must be a lowercase pack name"),
        ]
        for data, key, value, message in cases:
            if key is not None:
                data[key] = value
            with self.subTest(message=message):
                with self.assertRaisesRegex(pack_set.PackSetError, message):
                    pack_set.validate_document(
                        data, "profile.json", pack_set.PACK_SET_KIND
                    )


if __name__ == "__main__":
    unittest.main()
