import json
import os
import shutil
import stat
import tempfile
import textwrap
import unittest

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


if __name__ == "__main__":
    unittest.main()
