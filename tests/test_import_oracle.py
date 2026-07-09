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
from engine.import_oracle import (
    OracleError,
    import_state,
    render_import_blocks,
    render_root,
)


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
        self.command_log = os.path.join(self.tmp, "fake-commands.json")
        self.fake_tf = os.path.join(self.tmp, "fake_tf.py")
        with open(self.fake_tf, "w", encoding="utf-8") as f:
            f.write(textwrap.dedent("""\
                #!/usr/bin/env python3
                import json
                import os
                import re
                import sys

                def log_command(args):
                    path = os.environ.get("FAKE_TF_COMMAND_LOG")
                    if not path:
                        return
                    data = []
                    if os.path.exists(path):
                        with open(path, encoding="utf-8") as f:
                            data = json.load(f)
                    data.append(args)
                    with open(path, "w", encoding="utf-8") as f:
                        json.dump(data, f)

                def parse_import_blocks(text):
                    blocks = re.findall(r'import\\s*\\{(.*?)\\n\\}', text, re.S)
                    imports = {}
                    for block in blocks:
                        to_match = re.search(r'^\\s*to\\s*=\\s*([^\\n]+)\\s*$',
                                             block, re.M)
                        id_match = re.search(r'^\\s*id\\s*=\\s*"(.*)"\\s*$',
                                             block, re.M)
                        if not to_match or not id_match:
                            continue
                        import_id = bytes(
                            id_match.group(1), "utf-8").decode("unicode_escape")
                        imports[to_match.group(1).strip()] = import_id
                    return imports

                def plan_path(args):
                    for index, arg in enumerate(args):
                        if arg.startswith("-out="):
                            return arg[len("-out="):]
                        if arg == "-out" and index + 1 < len(args):
                            return args[index + 1]
                    return os.path.join(os.getcwd(), "tfplan")

                def plan_json(imports, actions=None, drift=False):
                    changes = []
                    for address, import_id in sorted(imports.items()):
                        rtype, name = address.split(".", 1)
                        changes.append({
                            "address": address,
                            "mode": "managed",
                            "type": rtype,
                            "name": name,
                            "change": {
                                "actions": actions or ["no-op"],
                                "importing": {"id": import_id},
                            },
                        })
                    out = {
                        "format_version": "1.0",
                        "resource_changes": changes,
                    }
                    if drift:
                        out["resource_drift"] = [{
                            "address": sorted(imports)[0],
                            "change": {"actions": ["update"]},
                        }]
                    return out

                def imports_from_plan(path):
                    with open(path, encoding="utf-8") as f:
                        plan = json.load(f)
                    return {
                        change["address"]: change["change"]["importing"]["id"]
                        for change in plan.get("resource_changes", [])
                        if change.get("change", {}).get("importing")
                    }

                def main():
                    args = sys.argv[1:]
                    log_command(args)
                    if args[0] == "init":
                        return 0
                    if args[0] == "import":
                        sys.stderr.write("unexpected terraform import command\\n")
                        return 99
                    if args[0] == "plan":
                        with open(os.path.join(os.getcwd(), "main.tf"), encoding="utf-8") as f:
                            main_tf = f.read()
                        with open(os.path.join(os.getcwd(), "imports.tf"), encoding="utf-8") as f:
                            imports_tf = f.read()
                        declared = set(
                            "%s.%s" % (m.group(1), m.group(2))
                            for m in re.finditer(r'resource\\s+"([^"]+)"\\s+"([^"]+)"', main_tf)
                        )
                        imports = parse_import_blocks(imports_tf)
                        for address in imports:
                            if address not in declared:
                                sys.stderr.write("undeclared import address %s\\n" % address)
                                return 42
                        if os.environ.get("FAKE_TF_FAIL_IMPORT") == "1":
                            import_id = sorted(imports.values())[0]
                            print(("stdout import id=%s " % import_id) + ("x" * 1600))
                            sys.stderr.write(
                                ("stderr import id=%s " % import_id) + ("y" * 1600)
                            )
                            return 17
                        if os.environ.get("FAKE_TF_SPOOF_PLAN_STDOUT") == "1":
                            print(
                                "description = \\"Plan: %d to import, 0 to add, 0 to change, 0 to destroy.\\""
                                % len(imports)
                            )
                        if os.environ.get("FAKE_TF_PLAN_CHANGES") == "1":
                            print(
                                "Plan: %d to import, 0 to add, 1 to change, 0 to destroy."
                                % len(imports)
                            )
                            data = plan_json(imports, actions=["update"])
                        elif os.environ.get("FAKE_TF_PLAN_DRIFT") == "1":
                            print(
                                "Plan: %d to import, 0 to add, 0 to change, 0 to destroy."
                                % len(imports)
                            )
                            data = plan_json(imports, drift=True)
                        else:
                            print(
                                "Plan: %d to import, 0 to add, 0 to change, 0 to destroy."
                                % len(imports)
                            )
                            data = plan_json(imports)
                        with open(plan_path(args), "w", encoding="utf-8") as f:
                            json.dump(data, f)
                        return 0
                    if args[0] == "apply":
                        imports = imports_from_plan(args[-1])
                        with open(os.path.join(os.getcwd(), "fake-imports.json"), "w", encoding="utf-8") as f:
                            json.dump(imports, f)
                        with open(os.path.join(os.getcwd(), "terraform.tfstate"), "w", encoding="utf-8") as f:
                            f.write("{}")
                        return 0
                    if args[0] == "show":
                        if len(args) >= 3 and args[1] == "-json" and args[2].endswith("tfplan"):
                            with open(args[2], encoding="utf-8") as f:
                                sys.stdout.write(f.read())
                            return 0
                        if os.environ.get("FAKE_TF_BAD_SHOW_JSON_SECRET") == "1":
                            print('{"secret": "PLAINTEXT", bad')
                            return 0
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
        os.environ["FAKE_TF_COMMAND_LOG"] = self.command_log

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
        os.environ.pop("FAKE_TF_PLAN_CHANGES", None)
        os.environ.pop("FAKE_TF_PLAN_DRIFT", None)
        os.environ.pop("FAKE_TF_SPOOF_PLAN_STDOUT", None)
        os.environ.pop("FAKE_TF_BAD_SHOW_JSON", None)
        os.environ.pop("FAKE_TF_BAD_SHOW_JSON_SECRET", None)
        os.environ.pop("FAKE_TF_COMMAND_LOG", None)
        os.environ.pop("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS", None)
        os.environ.pop("INFRAWRIGHT_KEEP_ORACLE", None)
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_render_root_uses_pack_source_pin_and_provider_block(self):
        root = render_root("sample_resource", {"prod_app": "123"})
        self.assertIn('source = "example/sample"', root)
        self.assertIn('version = "1.2.3"', root)
        self.assertIn('provider "sample"', root)
        self.assertIn('resource "sample_resource" "iw_', root)

    def test_render_import_blocks_targets_scratch_addresses_and_escapes_ids(self):
        blocks = render_import_blocks(
            "sample_resource",
            {"prod_app": 'tenant"}\n${secret}'},
        )
        self.assertIn("import {\n", blocks)
        self.assertIn("  to = sample_resource.iw_", blocks)
        self.assertIn('  id = "tenant\\"}\\n$${secret}"', blocks)
        self.assertNotIn('module.sample_resource', blocks)
        self.assertNotIn('\nresource "', blocks)

    def test_import_state_uses_fake_terraform_and_returns_values(self):
        out = import_state("sample_resource", {"a": "123", "b": "456"})
        self.assertEqual(set(out), {"a", "b"})
        self.assertEqual(out["a"]["values"]["id"], "123")
        self.assertTrue(out["a"]["address"].startswith("sample_resource.iw_"))

        commands = self._fake_commands()
        self.assertEqual([cmd[0] for cmd in commands], [
            "init", "plan", "show", "apply", "show",
        ])
        self.assertEqual(sum(1 for cmd in commands if cmd[0] == "plan"), 1)
        self.assertEqual(sum(1 for cmd in commands if cmd[0] == "apply"), 1)
        self.assertFalse([cmd for cmd in commands if cmd[0] == "import"])

    def test_spoofed_plan_stdout_does_not_authorize_apply(self):
        os.environ["FAKE_TF_PLAN_CHANGES"] = "1"
        os.environ["FAKE_TF_SPOOF_PLAN_STDOUT"] = "1"

        with self.assertRaises(OracleError) as ctx:
            import_state("sample_resource", {"prod_app": "123"})

        msg = str(ctx.exception)
        self.assertIn(
            "sample_resource oracle import plan was not import-only", msg)
        self.assertIn("actions=['update']", msg)
        commands = self._fake_commands()
        self.assertEqual([cmd[0] for cmd in commands], ["init", "plan", "show"])
        self.assertFalse([cmd for cmd in commands if cmd[0] == "apply"])

    def test_plan_resource_drift_is_rejected_before_apply(self):
        os.environ["FAKE_TF_PLAN_DRIFT"] = "1"

        with self.assertRaises(OracleError) as ctx:
            import_state("sample_resource", {"prod_app": "123"})

        msg = str(ctx.exception)
        self.assertIn("sample_resource oracle import plan reported resource drift", msg)
        commands = self._fake_commands()
        self.assertEqual([cmd[0] for cmd in commands], ["init", "plan", "show"])
        self.assertFalse([cmd for cmd in commands if cmd[0] == "apply"])

    def test_empty_import_set_returns_empty_without_terraform(self):
        os.environ["TF"] = os.path.join(self.tmp, "missing-fake-tf")
        self.assertEqual(import_state("sample_resource", {}), {})

    def test_duplicate_import_ids_fail_before_terraform(self):
        with self.assertRaises(OracleError) as ctx:
            import_state(
                "sample_resource",
                {"a": "SECRET-IMPORT-ID", "b": "SECRET-IMPORT-ID"},
            )
        msg = str(ctx.exception)
        self.assertIn("sample_resource duplicate import_id", msg)
        self.assertIn("'a'", msg)
        self.assertIn("'b'", msg)
        self.assertNotIn("SECRET-IMPORT-ID", msg)

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
        self.assertNotIn("{not valid json", msg)

    def test_invalid_show_json_does_not_include_secret_stdout(self):
        os.environ["FAKE_TF_BAD_SHOW_JSON_SECRET"] = "1"

        with self.assertRaises(OracleError) as ctx:
            import_state("sample_resource", {"prod_app": "123"})

        msg = str(ctx.exception)
        self.assertIn("sample_resource terraform show -json returned invalid JSON", msg)
        self.assertNotIn("PLAINTEXT", msg)

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

    def test_keep_oracle_zero_and_false_do_not_keep_workdir(self):
        for value in ("0", "false"):
            os.environ["INFRAWRIGHT_KEEP_ORACLE"] = value
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                out = import_state("sample_resource", {"prod_app": "123"})
            self.assertEqual(out["prod_app"]["values"]["id"], "123")
            self.assertNotIn("WARNING: kept oracle workdir", stderr.getvalue())

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

    def _fake_commands(self):
        with open(self.command_log, encoding="utf-8") as f:
            return json.load(f)


if __name__ == "__main__":
    unittest.main()
