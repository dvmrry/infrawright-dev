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
from engine.drift_policy import DriftPolicy
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


FAKE_PROVIDER_SCHEMA = {
    "block": {
        "attributes": {
            "id": {"type": "string", "computed": True},
            "name": {"type": "string", "required": True},
            "size_quota": {"type": "number", "optional": True},
            "enabled": {"type": "bool", "optional": True},
            "description": {"type": "string", "optional": True},
        },
        "block_types": {
            "cbi_profile": {
                "nesting_mode": "list",
                "block": {
                    "attributes": {
                        "id": {"type": "string", "optional": True},
                        "name": {"type": "string", "optional": True},
                        "profile_seq": {"type": "number", "optional": True},
                        "url": {"type": "string", "optional": True},
                    }
                },
            },
            "ports": {
                "nesting_mode": "list",
                "block": {
                    "attributes": {
                        "start": {"type": "number", "optional": True},
                        "end": {"type": "number", "optional": True},
                    }
                },
            },
        },
    }
}


class ImportOracleTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="import-oracle-")
        self.prev_packs = os.environ.get("INFRAWRIGHT_PACKS")
        self.prev_tf = os.environ.get("TF")
        self.prev_schema_load_resource = (
            import_oracle.schema_paths.load_resource
        )
        self.prev_fill_load_resource = (
            import_oracle.projection_fill.load_resource
        )
        import_oracle.schema_paths.load_resource = (
            lambda resource_type: FAKE_PROVIDER_SCHEMA
        )
        import_oracle.projection_fill.load_resource = (
            lambda resource_type: FAKE_PROVIDER_SCHEMA
        )
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

                def generated_config_path(args):
                    for index, arg in enumerate(args):
                        if arg.startswith("-generate-config-out="):
                            return arg[len("-generate-config-out="):]
                        if arg == "-generate-config-out" and index + 1 < len(args):
                            return args[index + 1]
                    return None

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

                def tf_text():
                    out = []
                    for name in sorted(os.listdir(os.getcwd())):
                        if not name.endswith(".tf") or name == "imports.tf":
                            continue
                        with open(os.path.join(os.getcwd(), name), encoding="utf-8") as f:
                            out.append(f.read())
                    return "\\n".join(out)

                def has_rejected_generated_sentinel():
                    text = tf_text()
                    return (
                        re.search(r'^\\s*size_quota\\s*=\\s*0\\s*$',
                                  text, re.M)
                        or re.search(r'^\\s*end\\s*=\\s*0\\s*$',
                                     text, re.M)
                    )

                def has_cbi_profile():
                    return re.search(r'^\\s*cbi_profile\\s*\\{\\s*$',
                                     tf_text(), re.M)

                def main():
                    args = sys.argv[1:]
                    log_command(args)
                    if args[0] == "init":
                        return 0
                    if args[0] == "import":
                        sys.stderr.write("unexpected terraform import command\\n")
                        return 99
                    if args[0] == "plan":
                        with open(os.path.join(os.getcwd(), "imports.tf"), encoding="utf-8") as f:
                            imports_tf = f.read()
                        declared = set(
                            "%s.%s" % (m.group(1), m.group(2))
                            for m in re.finditer(r'resource\\s+"([^"]+)"\\s+"([^"]+)"', tf_text())
                        )
                        imports = parse_import_blocks(imports_tf)
                        if (
                                os.environ.get("FAKE_TF_REJECT_GENERATED_SENTINEL") == "1"
                                and not generated_config_path(args)
                                and has_rejected_generated_sentinel()):
                            sys.stderr.write("generated config rejected sentinel\\n")
                            return 45
                        if (
                                os.environ.get("FAKE_TF_REJECT_MISSING_CBI_PROFILE") == "1"
                                and not generated_config_path(args)
                                and not has_cbi_profile()):
                            sys.stderr.write("generated config missing cbi_profile\\n")
                            return 46
                        undeclared = sorted(
                            address for address in imports if address not in declared
                        )
                        generated_path = generated_config_path(args)
                        if undeclared:
                            if not generated_path:
                                sys.stderr.write(
                                    "undeclared import address %s\\n" % undeclared[0]
                                )
                                return 42
                            if os.path.exists(generated_path):
                                sys.stderr.write(
                                    "generated config target already exists\\n"
                                )
                                return 43
                            if os.environ.get(
                                    "FAKE_TF_SKIP_GENERATED_CONFIG") != "1":
                                with open(generated_path, "w",
                                          encoding="utf-8") as f:
                                    for address in undeclared:
                                        rtype, name = address.split(".", 1)
                                        f.write(
                                            'resource "%s" "%s" {\\n  id = %s\\n'
                                            % (rtype, name,
                                               json.dumps(imports[address]))
                                        )
                                        if os.environ.get("FAKE_TF_GENERATED_SENTINEL") == "1":
                                            f.write("  size_quota = 0\\n")
                                            f.write("  ports {\\n    start = 443\\n    end = 0\\n  }\\n")
                                        if os.environ.get("FAKE_TF_GENERATED_COMPLEX") == "1":
                                            f.write("  size_quota = [0]\\n")
                                        f.write("}\\n\\n")
                            if os.environ.get("FAKE_TF_FAIL_GENERATED_CONFIG") == "1":
                                sys.stderr.write("generated config validation failed\\n")
                                return 44
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
        os.environ.pop("FAKE_TF_GENERATED_SENTINEL", None)
        os.environ.pop("FAKE_TF_GENERATED_COMPLEX", None)
        os.environ.pop("FAKE_TF_SKIP_GENERATED_CONFIG", None)
        os.environ.pop("FAKE_TF_FAIL_GENERATED_CONFIG", None)
        os.environ.pop("FAKE_TF_REJECT_GENERATED_SENTINEL", None)
        os.environ.pop("FAKE_TF_REJECT_MISSING_CBI_PROFILE", None)
        os.environ.pop("FAKE_TF_COMMAND_LOG", None)
        os.environ.pop("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS", None)
        os.environ.pop("INFRAWRIGHT_KEEP_ORACLE", None)
        import_oracle.schema_paths.load_resource = self.prev_schema_load_resource
        import_oracle.projection_fill.load_resource = self.prev_fill_load_resource
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_render_root_uses_pack_source_pin_and_provider_block_only(self):
        root = render_root("sample_resource", {"prod_app": "123"})
        self.assertIn('source = "example/sample"', root)
        self.assertIn('version = "1.2.3"', root)
        self.assertIn('provider "sample"', root)
        self.assertNotIn('resource "sample_resource" "iw_', root)

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
        generate_config_args = [
            arg for arg in commands[1] if arg.startswith("-generate-config-out=")
        ]
        self.assertEqual(len(generate_config_args), 1)
        generated_config = generate_config_args[0].split("=", 1)[1]
        self.assertEqual(os.path.basename(generated_config), "generated.tf")
        self.assertEqual(sum(1 for cmd in commands if cmd[0] == "plan"), 1)
        self.assertEqual(sum(1 for cmd in commands if cmd[0] == "apply"), 1)
        self.assertFalse([cmd for cmd in commands if cmd[0] == "import"])

    def test_generated_config_policy_rescues_provider_validation_failure(self):
        os.environ["FAKE_TF_GENERATED_SENTINEL"] = "1"
        os.environ["FAKE_TF_FAIL_GENERATED_CONFIG"] = "1"
        os.environ["FAKE_TF_REJECT_GENERATED_SENTINEL"] = "1"
        policy = self._policy({
            "projection_omit_if": [
                {
                    "path": "size_quota",
                    "values": [0],
                    "reason": "provider rejects unset sentinel",
                    "approved_by": "unit",
                },
                {
                    "path": "ports[*].end",
                    "values": [0],
                    "reason": "provider rejects unset sentinel",
                    "approved_by": "unit",
                },
            ],
        })

        out = import_state(
            "sample_resource", {"prod_app": "123"}, policy=policy)

        self.assertEqual(out["prod_app"]["values"]["id"], "123")
        self.assertEqual(policy.stale_entries(
            modes=("projection_omit_if",)), [])
        commands = self._fake_commands()
        self.assertEqual([cmd[0] for cmd in commands], [
            "init", "plan", "plan", "show", "apply", "show",
        ])
        self.assertTrue(any(
            arg.startswith("-generate-config-out=") for arg in commands[1]))
        self.assertFalse(any(
            arg.startswith("-generate-config-out=") for arg in commands[2]))

    def test_generated_config_policy_fails_closed_when_file_is_missing(self):
        os.environ["FAKE_TF_SKIP_GENERATED_CONFIG"] = "1"
        policy = self._policy({
            "projection_omit_if": [
                {
                    "path": "size_quota",
                    "values": [0],
                    "reason": "provider rejects unset sentinel",
                    "approved_by": "unit",
                },
            ],
        })

        with self.assertRaisesRegex(
                OracleError, "generated import config is missing"):
            import_state(
                "sample_resource", {"prod_app": "123"}, policy=policy)

        self.assertEqual([cmd[0] for cmd in self._fake_commands()], [
            "init", "plan",
        ])

    def test_missing_generated_config_without_policy_is_ignored(self):
        missing = os.path.join(self.tmp, "does-not-exist.tf")

        self.assertEqual(import_oracle._apply_generated_config_policy(
            "sample_resource",
            {"sample_resource.iw_prod_app": "prod_app"},
            missing,
            None,
        ), 0)

    def test_failed_plan_without_generated_config_reports_plan_failure(self):
        os.environ["FAKE_TF_SKIP_GENERATED_CONFIG"] = "1"
        os.environ["FAKE_TF_FAIL_GENERATED_CONFIG"] = "1"
        policy = self._policy({
            "projection_omit_if": [
                {
                    "path": "size_quota",
                    "values": [0],
                    "reason": "provider rejects unset sentinel",
                    "approved_by": "unit",
                },
            ],
        })

        with self.assertRaisesRegex(OracleError, "failed with exit 44"):
            import_state(
                "sample_resource", {"prod_app": "123"}, policy=policy)

        self.assertEqual([cmd[0] for cmd in self._fake_commands()], [
            "init", "plan",
        ])

    def test_generated_config_policy_debug_artifacts_show_removed_lines(self):
        os.environ["FAKE_TF_GENERATED_SENTINEL"] = "1"
        os.environ["FAKE_TF_FAIL_GENERATED_CONFIG"] = "1"
        os.environ["FAKE_TF_REJECT_GENERATED_SENTINEL"] = "1"
        policy = self._policy({
            "projection_omit_if": [
                {
                    "path": "size_quota",
                    "values": [0],
                    "reason": "provider rejects unset sentinel",
                    "approved_by": "unit",
                },
                {
                    "path": "ports[*].end",
                    "values": [0],
                    "reason": "provider rejects unset sentinel",
                    "approved_by": "unit",
                },
            ],
        })
        kept = None
        stderr = io.StringIO()
        try:
            with contextlib.redirect_stderr(stderr):
                out = import_state(
                    "sample_resource",
                    {"prod_app": "123"},
                    keep_workdir=True,
                    policy=policy,
                )
            self.assertEqual(out["prod_app"]["values"]["id"], "123")
            match = re.search(r"workdir ([^;]+);", stderr.getvalue())
            self.assertIsNotNone(match, stderr.getvalue())
            kept = match.group(1)

            before_path = os.path.join(kept, "generated.tf.before-policy")
            after_path = os.path.join(kept, "generated.tf")
            diff_path = os.path.join(kept, "generated.tf.policy.diff")
            with open(before_path, encoding="utf-8") as f:
                before = f.read()
            with open(after_path, encoding="utf-8") as f:
                after = f.read()
            with open(diff_path, encoding="utf-8") as f:
                diff = f.read()

            self.assertIn("  size_quota = 0\n", before)
            self.assertIn("    end = 0\n", before)
            self.assertNotIn("  size_quota = 0\n", after)
            self.assertNotIn("    end = 0\n", after)
            self.assertIn("--- generated.tf.before-policy", diff)
            self.assertIn("+++ generated.tf", diff)
            self.assertIn("-  size_quota = 0\n", diff)
            self.assertIn("-    end = 0\n", diff)
        finally:
            if kept:
                shutil.rmtree(kept, ignore_errors=True)

    def test_generated_config_policy_rescues_missing_raw_fill_block(self):
        os.environ["FAKE_TF_FAIL_GENERATED_CONFIG"] = "1"
        os.environ["FAKE_TF_REJECT_MISSING_CBI_PROFILE"] = "1"
        policy = self._policy({
            "projection_fill": [
                {
                    "path": "cbi_profile",
                    "source": "cbiProfile",
                    "reason": "provider read omits isolate profile",
                    "approved_by": "unit",
                },
            ],
        })

        out = import_state(
            "sample_resource",
            {"prod_app": "123"},
            policy=policy,
            raw_items={
                "prod_app": {
                    "cbiProfile": {
                        "id": "cbi-1",
                        "name": "Isolation",
                        "profileSeq": "7",
                        "url": "https://example.invalid",
                    },
                },
            },
        )

        self.assertEqual(out["prod_app"]["values"]["id"], "123")
        self.assertEqual(policy.stale_entries(modes=("projection_fill",)), [])
        commands = self._fake_commands()
        self.assertEqual([cmd[0] for cmd in commands], [
            "init", "plan", "plan", "show", "apply", "show",
        ])

    def test_generated_config_fill_update_plan_rejected_before_apply(self):
        os.environ["FAKE_TF_FAIL_GENERATED_CONFIG"] = "1"
        os.environ["FAKE_TF_REJECT_MISSING_CBI_PROFILE"] = "1"
        os.environ["FAKE_TF_PLAN_CHANGES"] = "1"
        policy = self._policy({
            "projection_fill": [
                {
                    "path": "cbi_profile",
                    "source": "cbiProfile",
                    "reason": "provider read omits isolate profile",
                    "approved_by": "unit",
                },
            ],
        })

        with self.assertRaises(OracleError) as ctx:
            import_state(
                "sample_resource",
                {"prod_app": "123"},
                policy=policy,
                raw_items={
                    "prod_app": {
                        "cbiProfile": {
                            "id": "cbi-1",
                            "name": "Isolation",
                            "profileSeq": "7",
                            "url": "https://example.invalid",
                        },
                    },
                },
            )

        msg = str(ctx.exception)
        self.assertIn(
            "sample_resource oracle import plan was not import-only", msg)
        self.assertIn("actions=['update']", msg)
        commands = self._fake_commands()
        self.assertEqual([cmd[0] for cmd in commands], [
            "init", "plan", "plan", "show",
        ])
        self.assertFalse([cmd for cmd in commands if cmd[0] == "apply"])

    def test_generated_config_policy_debug_artifacts_show_filled_block(self):
        os.environ["FAKE_TF_FAIL_GENERATED_CONFIG"] = "1"
        os.environ["FAKE_TF_REJECT_MISSING_CBI_PROFILE"] = "1"
        policy = self._policy({
            "projection_fill": [
                {
                    "path": "cbi_profile",
                    "source": "cbiProfile",
                    "reason": "provider read omits isolate profile",
                    "approved_by": "unit",
                },
            ],
        })
        kept = None
        stderr = io.StringIO()
        try:
            with contextlib.redirect_stderr(stderr):
                import_state(
                    "sample_resource",
                    {"prod_app": "123"},
                    keep_workdir=True,
                    policy=policy,
                    raw_items={
                        "prod_app": {
                            "cbiProfile": {
                                "id": "cbi-1",
                                "name": "Isolation",
                                "profileSeq": "7",
                                "url": "https://example.invalid",
                            },
                        },
                    },
                )
            match = re.search(r"workdir ([^;]+);", stderr.getvalue())
            self.assertIsNotNone(match, stderr.getvalue())
            self.assertIn("raw API pull values", stderr.getvalue())
            kept = match.group(1)

            before_path = os.path.join(kept, "generated.tf.before-policy")
            after_path = os.path.join(kept, "generated.tf")
            diff_path = os.path.join(kept, "generated.tf.policy.diff")
            self.assertFalse(before_path.endswith(".tf"))
            self.assertFalse(diff_path.endswith(".tf"))
            with open(before_path, encoding="utf-8") as f:
                before = f.read()
            with open(after_path, encoding="utf-8") as f:
                after = f.read()
            with open(diff_path, encoding="utf-8") as f:
                diff = f.read()

            self.assertNotIn("cbi_profile {", before)
            self.assertIn("  cbi_profile {\n", after)
            self.assertIn('    id = "cbi-1"\n', after)
            self.assertIn('    name = "Isolation"\n', after)
            self.assertIn("    profile_seq = 7\n", after)
            self.assertIn('    url = "https://example.invalid"\n', after)
            self.assertIn("+  cbi_profile {\n", diff)
            self.assertIn("+    profile_seq = 7\n", diff)
        finally:
            if kept:
                shutil.rmtree(kept, ignore_errors=True)

    def test_projection_fill_policy_requires_raw_items(self):
        policy = self._policy({
            "projection_fill": [
                {
                    "path": "cbi_profile",
                    "source": "cbiProfile",
                    "reason": "test",
                    "approved_by": "unit",
                },
            ],
        })

        with self.assertRaisesRegex(OracleError, "requires raw_items"):
            import_state("sample_resource", {"prod_app": "123"}, policy=policy)

    def test_generated_config_policy_does_not_fill_when_source_missing(self):
        os.environ["FAKE_TF_FAIL_GENERATED_CONFIG"] = "1"
        policy = self._policy({
            "projection_fill": [
                {
                    "path": "cbi_profile",
                    "source": "cbiProfile",
                    "reason": "test",
                    "approved_by": "unit",
                },
            ],
        })

        with self.assertRaisesRegex(OracleError, "failed with exit 44"):
            import_state(
                "sample_resource",
                {"prod_app": "123"},
                policy=policy,
                raw_items={"prod_app": {"otherRaw": {}}},
            )

        self.assertEqual(
            policy.stale_entries(modes=("projection_fill",)),
            [("sample_resource", "projection_fill", "cbi_profile")],
        )

    def test_generated_config_policy_fill_skips_existing_target(self):
        policy = self._policy({
            "projection_fill": [
                {
                    "path": "cbi_profile",
                    "source": "cbiProfile",
                    "reason": "test",
                    "approved_by": "unit",
                },
            ],
        })
        lines = [
            'resource "sample_resource" "iw_prod_app" {\n',
            '  cbi_profile = []\n',
            "}\n",
        ]
        filled, count = import_oracle._fill_generated_config_lines(
            "sample_resource",
            {"sample_resource.iw_prod_app": "prod_app"},
            {"prod_app": {"cbiProfile": {"id": "cbi-1"}}},
            lines,
            import_oracle._generated_config_fill_entries(
                "sample_resource", policy),
            policy,
        )

        self.assertEqual(count, 0)
        self.assertEqual(filled, lines)

    def test_render_hcl_value_escapes_object_keys(self):
        rendered = import_oracle._render_hcl_value({
            "bad-key": "${var.value}",
            'quote"key': "literal",
        }, 2)

        self.assertIn('    "bad-key" = "$${var.value}"\n', rendered)
        self.assertIn('    "quote\\"key" = "literal"\n', rendered)
        self.assertNotIn("bad-key =", rendered)

    def test_generated_config_policy_fill_rejects_duplicate_resource_block(self):
        policy = self._policy({
            "projection_fill": [
                {
                    "path": "cbi_profile",
                    "source": "cbiProfile",
                    "reason": "test",
                    "approved_by": "unit",
                },
            ],
        })
        lines = [
            'resource "sample_resource" "iw_prod_app" {\n',
            "}\n",
            'resource "sample_resource" "iw_prod_app" {\n',
            "}\n",
        ]

        with self.assertRaisesRegex(OracleError, "duplicate resource block"):
            import_oracle._fill_generated_config_lines(
                "sample_resource",
                {"sample_resource.iw_prod_app": "prod_app"},
                {"prod_app": {"cbiProfile": {"id": "cbi-1"}}},
                lines,
                import_oracle._generated_config_fill_entries(
                    "sample_resource", policy),
                policy,
            )

    def test_generated_config_policy_rejects_required_omit_before_plan(self):
        policy = self._policy({
            "projection_omit": [
                {"path": "name", "reason": "test", "approved_by": "unit"}
            ],
        })

        with self.assertRaisesRegex(
                OracleError,
                "cannot projection_omit required path name"):
            import_state("sample_resource", {"prod_app": "123"}, policy=policy)

        self.assertEqual([cmd[0] for cmd in self._fake_commands()], ["init"])

    def test_generated_config_policy_non_scalar_reraises_original_failure(self):
        os.environ["FAKE_TF_GENERATED_COMPLEX"] = "1"
        os.environ["FAKE_TF_FAIL_GENERATED_CONFIG"] = "1"
        policy = self._policy({
            "projection_omit_if": [
                {
                    "path": "size_quota",
                    "values": [0],
                    "reason": "test",
                    "approved_by": "unit",
                }
            ],
        })

        with self.assertRaisesRegex(OracleError, "failed with exit 44"):
            import_state("sample_resource", {"prod_app": "123"}, policy=policy)

        self.assertEqual([cmd[0] for cmd in self._fake_commands()], [
            "init", "plan",
        ])

    def test_generated_config_policy_skips_exact_indexes_and_reraises(self):
        os.environ["FAKE_TF_GENERATED_SENTINEL"] = "1"
        os.environ["FAKE_TF_FAIL_GENERATED_CONFIG"] = "1"
        policy = self._policy({
            "projection_omit_if": [
                {
                    "path": "ports[0].end",
                    "values": [0],
                    "reason": "test",
                    "approved_by": "unit",
                }
            ],
        })

        with self.assertRaisesRegex(OracleError, "failed with exit 44"):
            import_state("sample_resource", {"prod_app": "123"}, policy=policy)

        self.assertEqual([cmd[0] for cmd in self._fake_commands()], [
            "init", "plan",
        ])

    def test_generated_config_policy_uses_strict_json_equality(self):
        policy = self._policy({
            "projection_omit_if": [
                {
                    "path": "enabled",
                    "values": [0],
                    "reason": "test",
                    "approved_by": "unit",
                },
                {
                    "path": "size_quota",
                    "values": [0],
                    "reason": "test",
                    "approved_by": "unit",
                },
            ],
        })
        lines = [
            'resource "sample_resource" "iw_prod_app" {\n',
            "  enabled = false\n",
            "  size_quota = 0.0\n",
            "}\n",
        ]
        entries = import_oracle._generated_config_policy_entries(
            "sample_resource", policy)

        filtered, removed = import_oracle._filter_generated_config_lines(
            "sample_resource",
            {"sample_resource.iw_prod_app"},
            lines,
            entries,
            policy,
        )

        self.assertEqual(removed, 1)
        text = "".join(filtered)
        self.assertIn("enabled = false", text)
        self.assertNotIn("size_quota", text)
        self.assertEqual(policy.stale_entries(
            modes=("projection_omit_if",)),
            [("sample_resource", "projection_omit_if", "enabled")])

    def test_generated_config_policy_removes_nested_scalar_leaf_only(self):
        policy = self._policy({
            "projection_omit_if": [
                {
                    "path": "ports[*].end",
                    "values": [0],
                    "reason": "test",
                    "approved_by": "unit",
                },
            ],
        })
        lines = [
            'resource "sample_resource" "iw_prod_app" {\n',
            "  ports {\n",
            "    start = 443\n",
            "    end = 0\n",
            "  }\n",
            "}\n",
        ]
        entries = import_oracle._generated_config_policy_entries(
            "sample_resource", policy)

        filtered, removed = import_oracle._filter_generated_config_lines(
            "sample_resource",
            {"sample_resource.iw_prod_app"},
            lines,
            entries,
            policy,
        )

        self.assertEqual(removed, 1)
        text = "".join(filtered)
        self.assertIn("ports {\n", text)
        self.assertIn("start = 443", text)
        self.assertNotIn("end = 0", text)

    def test_generated_config_policy_rejects_unexpected_resource_block(self):
        policy = self._policy({
            "projection_omit": [
                {
                    "path": "size_quota",
                    "reason": "test",
                    "approved_by": "unit",
                },
            ],
        })
        entries = import_oracle._generated_config_policy_entries(
            "sample_resource", policy)

        with self.assertRaisesRegex(
                OracleError,
                "unexpected resource block other_resource.iw_bad"):
            import_oracle._filter_generated_config_lines(
                "sample_resource",
                {"sample_resource.iw_prod_app"},
                ['resource "other_resource" "iw_bad" {\n', "}\n"],
                entries,
                policy,
            )

    def test_generated_config_policy_leaves_unsafe_hcl_values_untouched(self):
        policy = self._policy({
            "projection_omit": [
                {
                    "path": "description",
                    "reason": "test",
                    "approved_by": "unit",
                },
                {
                    "path": "size_quota",
                    "reason": "test",
                    "approved_by": "unit",
                },
            ],
        })
        lines = [
            'provider "sample" {}\n',
            "terraform {\n",
            "}\n",
            "import {\n",
            "}\n",
            'resource "sample_resource" "iw_prod_app" {\n',
            '  description = "brace } in string"\n',
            "  size_quota = [0]\n",
            "  description = <<EOT\n",
            "}\n",
            "EOT\n",
            "  description = {\n",
            '    value = "drop"\n',
            "  }\n",
            "}\n",
        ]
        entries = import_oracle._generated_config_policy_entries(
            "sample_resource", policy)

        filtered, removed = import_oracle._filter_generated_config_lines(
            "sample_resource",
            {"sample_resource.iw_prod_app"},
            lines,
            entries,
            policy,
        )

        self.assertEqual(removed, 1)
        text = "".join(filtered)
        self.assertIn('provider "sample" {}', text)
        self.assertIn("terraform {\n}\n", text)
        self.assertIn("import {\n}\n", text)
        self.assertNotIn('description = "brace } in string"', text)
        self.assertIn("size_quota = [0]", text)
        self.assertIn("description = <<EOT", text)
        self.assertIn("description = {", text)

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
            self.assertIn("generated configuration", msg)
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

    def _policy(self, resource_policy):
        return DriftPolicy({
            "version": 1,
            "resource_types": {"sample_resource": resource_policy},
        })

    def _fake_commands(self):
        with open(self.command_log, encoding="utf-8") as f:
            return json.load(f)


if __name__ == "__main__":
    unittest.main()
