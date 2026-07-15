"""Tests for the engine vendor-boundary audit."""
import json
import os
import subprocess
import sys
import tempfile
import unittest


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _write(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)


class AuditVendorBoundaryCliTest(unittest.TestCase):
    def _run(self, args=None):
        return subprocess.run(
            [sys.executable, "-m", "engine.audit_vendor_boundary"] + list(args or []),
            cwd=ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True,
        )

    def test_current_repo_passes_with_allowlist(self):
        proc = self._run()
        self.assertEqual(proc.returncode, 0, proc.stderr + proc.stdout)
        self.assertIn("allowed matches:", proc.stdout)
        self.assertIn("violations: 0", proc.stdout)

    def test_unallowlisted_vendor_token_fails(self):
        with tempfile.TemporaryDirectory() as td:
            _write(os.path.join(td, "engine", "new_edge.py"), "VALUE = 'aws_default_tags'\n")
            allowlist = os.path.join(td, "allow.json")
            _write_json(allowlist, {"allow": []})
            proc = self._run(["--root", td, "--allowlist", allowlist])
        self.assertEqual(proc.returncode, 1, proc.stdout)
        self.assertIn("violations: 1", proc.stdout)
        self.assertIn("engine/new_edge.py:1: aws", proc.stdout)

    def test_allowlisted_vendor_token_passes(self):
        with tempfile.TemporaryDirectory() as td:
            _write(os.path.join(td, "engine", "new_edge.py"), "VALUE = 'aws_default_tags'\n")
            allowlist = os.path.join(td, "allow.json")
            _write_json(allowlist, {
                "allow": [{
                    "path": "engine/new_edge.py",
                    "token": "aws",
                    "pattern": "aws_default_tags",
                    "reason": "test allowlist entry",
                }],
            })
            proc = self._run(["--root", td, "--allowlist", allowlist])
        self.assertEqual(proc.returncode, 0, proc.stderr + proc.stdout)
        self.assertIn("allowed matches: 1", proc.stdout)
        self.assertIn("violations: 0", proc.stdout)

    def test_transform_catalog_allowlist_does_not_mask_new_zcc_occurrence(self):
        source_path = os.path.join(ROOT, "engine", "transform_catalog.py")
        with open(source_path, encoding="utf-8") as f:
            source = f.read()
        with tempfile.TemporaryDirectory() as td:
            _write(
                os.path.join(td, "engine", "transform_catalog.py"),
                source + "\nzcc_future_backdoor = True\n",
            )
            proc = self._run([
                "--root", td,
                "--allowlist", os.path.join(
                    ROOT, "engine", "vendor_boundary_allowlist.json"
                ),
            ])
        self.assertEqual(proc.returncode, 1, proc.stderr + proc.stdout)
        self.assertIn("violations: 1", proc.stdout)
        self.assertIn("zcc_future_backdoor", proc.stdout)

    def test_malformed_allowlist_fails(self):
        with tempfile.TemporaryDirectory() as td:
            _write(os.path.join(td, "engine", "new_edge.py"), "VALUE = 'aws_default_tags'\n")
            allowlist = os.path.join(td, "allow.json")
            _write_json(allowlist, {"allow": [{"path": "engine/new_edge.py"}]})
            proc = self._run(["--root", td, "--allowlist", allowlist])
        self.assertEqual(proc.returncode, 2)
        self.assertIn("must be a non-empty string", proc.stderr)

    def test_vendor_token_boundary_avoids_ordinary_words(self):
        with tempfile.TemporaryDirectory() as td:
            _write(os.path.join(td, "engine", "new_edge.py"), "VALUE = 'awesome'\n")
            allowlist = os.path.join(td, "allow.json")
            _write_json(allowlist, {"allow": []})
            proc = self._run(["--root", td, "--allowlist", allowlist])
        self.assertEqual(proc.returncode, 0, proc.stderr + proc.stdout)
        self.assertIn("violations: 0", proc.stdout)

    def test_make_target_invokes_audit_command(self):
        proc = subprocess.run(
            ["make", "-C", ROOT, "-n", "audit-vendor-boundary"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn(
            "node dist/infrawright-cli.mjs audit-vendor-boundary",
            proc.stdout,
        )
        self.assertNotIn("python", proc.stdout.lower())


if __name__ == "__main__":
    unittest.main()
