import json
import io
import os
import shutil
import sys
import tempfile
import unittest

from engine import deployment
from engine import ops
from engine import packs
from engine import registry


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)


class OpsSelectorTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="ops-packs-")
        self.saved_packs = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        os.makedirs(os.path.join(self.tmp, "sample"), exist_ok=True)
        _write_json(os.path.join(self.tmp, "sample", "pack.json"), {
            "provider_prefixes": {"unused_": "sample"},
            "provider_sources": {"sample": "example/sample"},
        })
        _write_json(os.path.join(self.tmp, "sample", "registry.json"), {
            "resource_without_provider_prefix": {
                "generate": True,
                "product": "sample",
            },
            "sample_data_only": {
                "product": "sample",
            },
        })
        packs.reset()
        registry.reload_registry()

    def tearDown(self):
        if self.saved_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.saved_packs
        packs.reset()
        registry.reload_registry()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_product_selector_uses_registry_product_not_prefix(self):
        self.assertEqual(
            ops.expand_resources(["sample"]),
            ["resource_without_provider_prefix"],
        )

    def test_non_generated_exact_resource_is_rejected(self):
        with self.assertRaises(ValueError):
            ops.expand_resources(["sample_data_only"])


class OpsPathTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="ops-deployment-")
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")

    def tearDown(self):
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_overlay_keeps_flat_resource_type_paths(self):
        dep = os.path.join(self.tmp, "deployment.json")
        with open(dep, "w", encoding="utf-8") as f:
            f.write(json.dumps({"overlay": "acme"}))
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep
        self.assertEqual(
            ops.config_file("tenant", "sample_resource"),
            os.path.join("acme", "config", "tenant",
                         "sample_resource.auto.tfvars.json"),
        )
        self.assertEqual(
            ops.env_root("tenant", "sample_resource"),
            os.path.join("acme", "envs", "tenant", "sample_resource"),
        )


class OpsStageImportsTest(unittest.TestCase):
    def setUp(self):
        self.cwd = os.getcwd()
        self.tmp = tempfile.mkdtemp(prefix="ops-stage-")
        os.chdir(self.tmp)
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = os.devnull

    def tearDown(self):
        os.chdir(self.cwd)
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_stage_imports_copies_flat_resource_type_file(self):
        os.makedirs(os.path.join("imports", "tenant"), exist_ok=True)
        os.makedirs(os.path.join("envs", "tenant", "zia_rule_labels"), exist_ok=True)
        source = os.path.join("imports", "tenant", "zia_rule_labels_imports.tf")
        with open(source, "w", encoding="utf-8") as f:
            f.write("import {\n  to = x.y\n  id = \"1\"\n}\n")
        code = ops.cmd_stage_imports({
            "tenant": "tenant",
            "selectors": ["zia_rule_labels"],
            "state_aware": False,
            "backend_config": None,
        })
        self.assertEqual(code, 0)
        staged = os.path.join(
            "envs", "tenant", "zia_rule_labels", "zia_rule_labels_imports.tf"
        )
        self.assertTrue(os.path.exists(staged))

    def test_stage_imports_mentions_transform_or_adopt_when_sources_missing(self):
        with self.assertRaises(RuntimeError) as ctx:
            ops.cmd_stage_imports({
                "tenant": "tenant",
                "selectors": ["zia_rule_labels"],
                "state_aware": False,
                "backend_config": None,
            })
        self.assertIn("run make transform or make adopt first", str(ctx.exception))


class OpsPlanSafetyTest(unittest.TestCase):
    def test_non_import_change_count_ignores_noops_and_import_creates(self):
        plan = {
            "resource_changes": [
                {"change": {"actions": ["no-op"]}},
                {"change": {"actions": ["create"], "importing": {"id": "1"}}},
                {"change": {"actions": ["update"]}},
            ],
            "resource_drift": [
                {"change": {"actions": ["update"]}},
            ],
        }
        self.assertEqual(ops._non_import_change_count(plan), 2)

    def test_destroy_count_includes_resource_drift(self):
        plan = {
            "resource_changes": [{"change": {"actions": ["delete", "create"]}}],
            "resource_drift": [{"change": {"actions": ["delete"]}}],
        }
        self.assertEqual(ops._destroy_count(plan), 2)

    def test_apply_refuses_non_import_changes_by_default(self):
        tmp = tempfile.mkdtemp(prefix="ops-apply-")
        old_pairs = ops.selected_env_pairs
        old_check_backend = ops._check_backend
        old_check_call = ops._check_call
        old_show = ops._show_plan_json
        old_branch = ops._current_branch
        try:
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._check_backend = lambda env_dir, resource_type, backend_config: None
            ops._check_call = lambda args, stdout=None: 0
            ops._current_branch = lambda: "main"
            ops._show_plan_json = lambda env_dir: {
                "format_version": "1.0",
                "resource_changes": [{"change": {"actions": ["update"]}}],
            }
            with self.assertRaises(RuntimeError):
                ops.cmd_apply({
                    "tenant": "tenant",
                    "selectors": [],
                    "backend_config": None,
                    "allow_destroy": False,
                    "allow_non_main": False,
                    "allow_plan_changes": False,
                    "main_branch": "main",
                })
        finally:
            ops.selected_env_pairs = old_pairs
            ops._check_backend = old_check_backend
            ops._check_call = old_check_call
            ops._show_plan_json = old_show
            ops._current_branch = old_branch
            shutil.rmtree(tmp, ignore_errors=True)

    def test_apply_allows_import_only_without_plan_change_override(self):
        tmp = tempfile.mkdtemp(prefix="ops-apply-")
        os.makedirs(tmp, exist_ok=True)
        with open(os.path.join(tmp, "tfplan"), "w", encoding="utf-8") as f:
            f.write("fake")
        old_pairs = ops.selected_env_pairs
        old_check_backend = ops._check_backend
        old_check_call = ops._check_call
        old_show = ops._show_plan_json
        old_branch = ops._current_branch
        try:
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._check_backend = lambda env_dir, resource_type, backend_config: None
            ops._check_call = lambda args, stdout=None: 0
            ops._current_branch = lambda: "main"
            ops._show_plan_json = lambda env_dir: {
                "format_version": "1.0",
                "resource_changes": [{
                    "change": {
                        "actions": ["create"],
                        "importing": {"id": "123"},
                    }
                }],
            }
            self.assertEqual(ops.cmd_apply({
                "tenant": "tenant",
                "selectors": [],
                "backend_config": None,
                "allow_destroy": False,
                "allow_non_main": False,
                "allow_plan_changes": False,
                "main_branch": "main",
            }), 0)
            self.assertFalse(os.path.exists(os.path.join(tmp, "tfplan")))
        finally:
            ops.selected_env_pairs = old_pairs
            ops._check_backend = old_check_backend
            ops._check_call = old_check_call
            ops._show_plan_json = old_show
            ops._current_branch = old_branch
            shutil.rmtree(tmp, ignore_errors=True)

    def test_assert_adoptable_warns_on_stale_policy(self):
        tmp = tempfile.mkdtemp(prefix="ops-adoptable-")
        policy_path = os.path.join(tmp, "policy.json")
        with open(policy_path, "w", encoding="utf-8") as f:
            json.dump({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "plan_tolerate": [
                            {
                                "path": "status",
                                "reason": "test",
                                "approved_by": "unit",
                            }
                        ]
                    }
                },
            }, f)
        old_pairs = ops.selected_env_pairs
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._show_plan_json = lambda env_dir: {
                "format_version": "1.0",
                "resource_changes": [],
            }
            sys.stderr = stderr
            self.assertEqual(ops.cmd_assert_adoptable({
                "tenant": "tenant",
                "selectors": [],
                "policy": policy_path,
            }), 0)
            self.assertIn(
                "STALE DRIFT POLICY: sample_resource plan_tolerate status",
                stderr.getvalue(),
            )
        finally:
            ops.selected_env_pairs = old_pairs
            ops._show_plan_json = old_show
            sys.stderr = old_stderr
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    unittest.main()
