"""Tests for engine.check_pack."""
import json
import os
import subprocess
import sys
import tempfile
import unittest


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)


def _write_pack(root, name, pack=None, registry=None):
    pack = pack or {
        "provider_prefixes": {"sample_": "sample"},
        "provider_sources": {"sample": "example/sample"},
    }
    d = os.path.join(root, name)
    os.makedirs(d)
    _write_json(os.path.join(d, "pack.json"), pack)
    if registry is not None:
        _write_json(os.path.join(d, "registry.json"), registry)


def _registry(resource_type="sample_resource"):
    return {
        resource_type: {
            "product": "sample",
            "generate": True,
            "fetch": {
                "pagination": "single",
                "path": "sample/path",
            },
        },
    }


class CheckPackCliTest(unittest.TestCase):
    def _run(self, args=None, packs_root=None):
        env = os.environ.copy()
        if packs_root is not None:
            env["INFRAWRIGHT_PACKS"] = packs_root
        return subprocess.run(
            [sys.executable, "-m", "engine.check_pack"] + list(args or []),
            cwd=ROOT,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True,
        )

    def test_current_committed_packs_validate(self):
        proc = self._run()
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("validated packs:", proc.stdout)
        self.assertEqual(proc.stderr, "")

    def test_current_single_pack_validates(self):
        proc = self._run(["--pack", "zia"])
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout, "validated packs: zia\n")

    def test_pack_equals_argument_validates_single_pack(self):
        proc = self._run(["PACK=zia"])
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout, "validated packs: zia\n")

    def test_unknown_pack_name_fails(self):
        proc = self._run(["--pack", "not-a-pack"])
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("unknown pack 'not-a-pack'", proc.stderr)

    def test_invalid_pack_metadata_fails(self):
        with tempfile.TemporaryDirectory() as td:
            _write_pack(td, "bad", pack={"rename": {}})
            proc = self._run(packs_root=td)
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("unknown key rename", proc.stderr)

    def test_reserved_shared_root_is_not_an_authoring_pack(self):
        with tempfile.TemporaryDirectory() as td:
            _write_json(os.path.join(td, "_shared", "pack.json"), {
                "provider_sources": {"ghost": "example/ghost"},
            })
            proc = self._run(packs_root=td)

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(proc.stdout, "validated packs: none\n")

    def test_invalid_registry_metadata_fails(self):
        with tempfile.TemporaryDirectory() as td:
            data = _registry()
            data["sample_resource"]["fetch"]["optional_http_statuses"] = ["403"]
            _write_pack(td, "bad", registry=data)
            proc = self._run(packs_root=td)
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("optional_http_statuses[0] must be an integer", proc.stderr)

    def test_invalid_registry_pagination_value_fails(self):
        with tempfile.TemporaryDirectory() as td:
            data = _registry()
            data["sample_resource"]["fetch"]["pagination"] = "ziaa"
            _write_pack(td, "bad", registry=data)
            proc = self._run(packs_root=td)
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("fetch.pagination unsupported value 'ziaa'", proc.stderr)
        self.assertIn("allowed values:", proc.stderr)
        self.assertIn("single", proc.stderr)
        self.assertIn("zia", proc.stderr)
        self.assertIn("zpa", proc.stderr)

    def test_invalid_override_metadata_fails(self):
        with tempfile.TemporaryDirectory() as td:
            _write_pack(td, "bad", registry=_registry())
            _write_json(
                os.path.join(td, "bad", "overrides", "sample_resource.json"),
                {"rename": {"old": "new"}},
            )
            proc = self._run(packs_root=td)
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("unknown override key rename", proc.stderr)
        self.assertIn("sample_resource.json", proc.stderr)

    def test_duplicate_registry_resource_type_fails_all_pack_validation(self):
        with tempfile.TemporaryDirectory() as td:
            _write_pack(td, "one", registry=_registry("sample_resource"))
            _write_pack(td, "two", registry=_registry("sample_resource"))
            proc = self._run(packs_root=td)
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("duplicate resource type 'sample_resource'", proc.stderr)

    def test_make_target_invokes_check_pack_command(self):
        proc = subprocess.run(
            ["make", "-C", ROOT, "-n", "PACK=zia", "check-pack"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("python3 -m engine.check_pack --pack \"zia\"", proc.stdout)


if __name__ == "__main__":
    unittest.main()
