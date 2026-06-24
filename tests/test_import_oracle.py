import contextlib
import io
import json
import os
import re
import shutil
import stat
import tempfile
import textwrap
import unittest
from unittest import mock

from engine import packs
from engine import import_oracle
from engine.import_oracle import OracleError, import_state, render_root


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)


class ImportOracleTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="import-oracle-")
        self.prev_packs = os.environ.get("INFRAWRIGHT_PACKS")
        self.prev_tf = os.environ.get("TF")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        os.makedirs(os.path.join(self.tmp, "sample"), exist_ok=True)
        _write_json(os.path.join(self.tmp, "sample", "pack.json"), {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "pin": "1.2.3",
        })
        packs.reset()
        self.fake_tf = os.path.join(self.tmp, "fake_tf.py")
        with open(self.fake_tf, "w", encoding="utf-8") as f:
            f.write(textwrap.dedent("""\
                #!/usr/bin/env python3
                import json
                import os
                import re
                import sys

                def main():
                    args = sys.argv[1:]
                    if args[0] == "init":
                        return 0
                    if args[0] == "import":
                        address = args[-2]
                        import_id = args[-1]
                        if os.environ.get("FAKE_TF_FAIL_IMPORT") == "1":
                            print(("stdout import id=%s " % import_id) + ("x" * 1600))
                            sys.stderr.write(
                                ("stderr import id=%s " % import_id) + ("y" * 1600)
                            )
                            return 17
                        with open(os.path.join(os.getcwd(), "main.tf"), encoding="utf-8") as f:
                            main_tf = f.read()
                        declared = set(
                            "%s.%s" % (m.group(1), m.group(2))
                            for m in re.finditer(r'resource\\s+"([^"]+)"\\s+"([^"]+)"', main_tf)
                        )
                        if address not in declared:
                            sys.stderr.write("undeclared import address %s\\n" % address)
                            return 42
                        path = os.path.join(os.getcwd(), "fake-imports.json")
                        data = {}
                        if os.path.exists(path):
                            with open(path, encoding="utf-8") as f:
                                data = json.load(f)
                        data[address] = import_id
                        with open(path, "w", encoding="utf-8") as f:
                            json.dump(data, f)
                        with open(os.path.join(os.getcwd(), "terraform.tfstate"), "w", encoding="utf-8") as f:
                            f.write("{}")
                        return 0
                    if args[0] == "show":
                        if os.environ.get("FAKE_TF_BAD_SHOW_JSON") == "1":
                            print("{not valid json")
                            return 0
                        with open(os.path.join(os.getcwd(), "fake-imports.json"), encoding="utf-8") as f:
                            imports = json.load(f)
                        resources = []
                        for address, import_id in sorted(imports.items()):
                            rtype, name = address.split(".", 1)
                            resources.append({
                                "address": address,
                                "mode": "managed",
                                "type": rtype,
                                "name": name,
                                "values": {
                                    "id": import_id,
                                    "name": "imported-" + import_id,
                                    "computed_only": "ignored"
                                },
                                "sensitive_values": {}
                            })
                        print(json.dumps({
                            "format_version": "1.0",
                            "values": {"root_module": {"resources": resources}}
                        }))
                        return 0
                    return 1

                if __name__ == "__main__":
                    raise SystemExit(main())
            """))
        os.chmod(self.fake_tf, os.stat(self.fake_tf).st_mode | stat.S_IXUSR)
        os.environ["TF"] = self.fake_tf

    def tearDown(self):
        if self.prev_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev_packs
        if self.prev_tf is None:
            os.environ.pop("TF", None)
        else:
            os.environ["TF"] = self.prev_tf
        os.environ.pop("FAKE_TF_FAIL_IMPORT", None)
        os.environ.pop("FAKE_TF_BAD_SHOW_JSON", None)
        os.environ.pop("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS", None)
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_render_root_uses_pack_source_pin_and_provider_block(self):
        root = render_root("sample_resource", {"prod_app": "123"})
        self.assertIn('source = "example/sample"', root)
        self.assertIn('version = "1.2.3"', root)
        self.assertIn('provider "sample"', root)
        self.assertIn('resource "sample_resource" "iw_', root)

    def test_import_state_uses_fake_terraform_and_returns_values(self):
        out = import_state("sample_resource", {"a": "123", "b": "456"})
        self.assertEqual(set(out), {"a", "b"})
        self.assertEqual(out["a"]["values"]["id"], "123")
        self.assertTrue(out["a"]["address"].startswith("sample_resource.iw_"))

    def test_empty_import_set_returns_empty_without_terraform(self):
        os.environ["TF"] = os.path.join(self.tmp, "missing-fake-tf")
        self.assertEqual(import_state("sample_resource", {}), {})

    def test_duplicate_import_ids_fail_before_terraform(self):
        with self.assertRaises(OracleError):
            import_state("sample_resource", {"a": "same", "b": "same"})

    def test_duplicate_instance_names_fail_before_terraform(self):
        original = import_oracle._instance_name
        import_oracle._instance_name = lambda key: "iw_collision"
        os.environ["TF"] = os.path.join(self.tmp, "missing-fake-tf")
        try:
            with self.assertRaises(OracleError) as ctx:
                import_state("sample_resource", {"a": "123", "b": "456"})
        finally:
            import_oracle._instance_name = original
        msg = str(ctx.exception)
        self.assertIn("sample_resource oracle instance name collision", msg)
        self.assertIn("'a'", msg)
        self.assertIn("'b'", msg)
        self.assertIn("iw_collision", msg)

    def test_subprocess_failure_redacts_import_id_and_truncates_output(self):
        secret_import_id = "tenant-url-token-secret-import-id"
        os.environ["FAKE_TF_FAIL_IMPORT"] = "1"

        with self.assertRaises(OracleError) as ctx:
            import_state("sample_resource", {"prod_app": secret_import_id})

        msg = str(ctx.exception)
        self.assertIn("<redacted-import-id>", msg)
        self.assertNotIn(secret_import_id, msg)
        self.assertIn("[truncated", msg)
        self.assertIn("failed with exit 17", msg)

    def test_subprocess_timeout_raises_oracle_error(self):
        secret_import_id = "tenant-url-token-secret-import-id"

        def timeout(*_args, **_kwargs):
            raise import_oracle.subprocess.TimeoutExpired(
                ["terraform", "import"],
                12,
                output=("stdout import id=%s" % secret_import_id).encode(),
                stderr=("stderr import id=%s" % secret_import_id).encode(),
            )

        with mock.patch.object(import_oracle.subprocess, "run", timeout):
            with self.assertRaises(OracleError) as ctx:
                import_oracle._run(
                    ["terraform", "import", "addr", secret_import_id],
                    cwd=self.tmp,
                    env=os.environ.copy(),
                )

        msg = str(ctx.exception)
        self.assertIn("timed out after 12 seconds", msg)
        self.assertIn("<redacted-import-id>", msg)
        self.assertNotIn(secret_import_id, msg)

    def test_invalid_show_json_is_wrapped_as_oracle_error(self):
        os.environ["FAKE_TF_BAD_SHOW_JSON"] = "1"

        with self.assertRaises(OracleError) as ctx:
            import_state("sample_resource", {"prod_app": "123"})

        msg = str(ctx.exception)
        self.assertIn("sample_resource terraform show -json returned invalid JSON", msg)
        self.assertIn("{not valid json", msg)

    def test_cleanup_failure_does_not_mask_primary_error(self):
        secret_import_id = "tenant-url-token-secret-import-id"
        os.environ["FAKE_TF_FAIL_IMPORT"] = "1"
        oracle_temp = os.path.join(self.tmp, "oracle-temp")
        os.makedirs(oracle_temp)
        stderr = io.StringIO()

        with mock.patch.object(import_oracle.tempfile, "mkdtemp", return_value=oracle_temp):
            with mock.patch.object(import_oracle.shutil, "rmtree", side_effect=OSError("busy")):
                with contextlib.redirect_stderr(stderr):
                    with self.assertRaises(OracleError) as ctx:
                        import_state("sample_resource", {"prod_app": secret_import_id})

        msg = str(ctx.exception)
        self.assertIn("failed with exit 17", msg)
        self.assertNotIn("failed to remove oracle workdir", msg)
        warning = stderr.getvalue()
        self.assertIn("WARNING: failed to remove oracle workdir", warning)
        self.assertIn("busy", warning)

    def test_cleanup_failure_after_success_is_oracle_error(self):
        oracle_temp = os.path.join(self.tmp, "oracle-temp")
        os.makedirs(oracle_temp)

        with mock.patch.object(import_oracle.tempfile, "mkdtemp", return_value=oracle_temp):
            with mock.patch.object(import_oracle.shutil, "rmtree", side_effect=OSError("busy")):
                with self.assertRaises(OracleError) as ctx:
                    import_state("sample_resource", {"prod_app": "123"})

        msg = str(ctx.exception)
        self.assertIn("failed to remove oracle workdir", msg)
        self.assertIn("busy", msg)

    def test_keep_workdir_warns_about_unencrypted_provider_state(self):
        kept = None
        stderr = io.StringIO()
        try:
            with contextlib.redirect_stderr(stderr):
                out = import_state(
                    "sample_resource",
                    {"prod_app": "123"},
                    keep_workdir=True,
                )
            self.assertEqual(out["prod_app"]["values"]["id"], "123")
            msg = stderr.getvalue()
            self.assertIn("WARNING: kept oracle workdir", msg)
            self.assertIn("unencrypted provider state", msg)
            self.assertIn("import IDs", msg)
            match = re.search(r"workdir ([^;]+);", msg)
            self.assertIsNotNone(match, msg)
            kept = match.group(1)
            self.assertTrue(os.path.isdir(kept))
            self.assertTrue(os.path.exists(os.path.join(kept, "terraform.tfstate")))
        finally:
            if kept:
                shutil.rmtree(kept, ignore_errors=True)

    def test_backend_blocks_are_rejected_before_terraform_init(self):
        self._assert_oracle_override_rejected(textwrap.dedent("""\
            provider "sample" {}

            terraform {
              backend "local" {}
            }
        """))

    def test_backend_block_with_multiple_spaces_is_rejected(self):
        self._assert_oracle_override_rejected(textwrap.dedent("""\
            provider "sample" {}

            terraform {
              backend  "s3" {}
            }
        """))

    def test_backend_block_with_tab_is_rejected(self):
        self._assert_oracle_override_rejected(
            'provider "sample" {}\nterraform {\n  backend\t"s3" {}\n}\n')

    def test_cloud_block_is_rejected_before_terraform_init(self):
        self._assert_oracle_override_rejected(textwrap.dedent("""\
            provider "sample" {}

            terraform {
              cloud {}
            }
        """))

    def test_provider_only_oracle_override_is_allowed(self):
        import_oracle._assert_local_scratch_root(textwrap.dedent("""\
            provider "sample" {
              # credentials via environment
            }
        """))

    def _assert_oracle_override_rejected(self, text):
        os.makedirs(os.path.join(self.tmp, "sample", "oracle"), exist_ok=True)
        with open(
                os.path.join(self.tmp, "sample", "oracle", "sample.tf"),
                "w",
                encoding="utf-8") as f:
            f.write(text)
        os.environ["TF"] = os.path.join(self.tmp, "missing-fake-tf")

        with self.assertRaises(OracleError) as ctx:
            import_state("sample_resource", {"prod_app": "123"})

        msg = str(ctx.exception)
        self.assertIn("oracle scratch root must not declare", msg)
        self.assertIn("ephemeral and local", msg)


if __name__ == "__main__":
    unittest.main()
