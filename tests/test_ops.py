import json
import io
import os
import shutil
import sys
import tempfile
import unittest

from engine import ops
from engine import packs


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)


def _plan(records, drift=None):
    return {
        "format_version": "1.2",
        "resource_changes": records,
        "resource_drift": drift or [],
    }


def _rc(actions, importing=False, before=None, after=None):
    change = {"actions": actions, "before": before, "after": after}
    if importing:
        change["importing"] = {"id": "x"}
    return {"address": "m.x", "type": "t_x", "change": change}


class OpsEnvDiscoveryTest(unittest.TestCase):
    RESOURCE = "zia_rule_labels"

    def setUp(self):
        self.cwd = os.getcwd()
        self.tmp = tempfile.mkdtemp(prefix="ops-env-discovery-")
        os.chdir(self.tmp)
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")

    def tearDown(self):
        os.chdir(self.cwd)
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_deployment(self, data):
        path = os.path.join(self.tmp, "deployment.json")
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f)
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = path
        return path

    def _write_deployment_text(self, text):
        path = os.path.join(self.tmp, "deployment.json")
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = path
        return path

    def _env_root(self, *parts):
        path = os.path.join(*parts)
        os.makedirs(path, exist_ok=True)
        return path

    def test_no_tenant_discovery_uses_only_active_overlay_envs(self):
        root_path = self._env_root("envs", "rootTenant", self.RESOURCE)
        overlay_path = self._env_root("demo", "envs", "demoTenant", self.RESOURCE)
        self._write_deployment({"overlay": "demo"})

        self.assertEqual(
            ops.discover_env_pairs(),
            [("demoTenant", self.RESOURCE, overlay_path)],
        )
        self.assertNotIn(
            ("rootTenant", self.RESOURCE, root_path),
            ops.discover_env_pairs(),
        )

    def test_no_tenant_discovery_uses_root_when_no_overlay(self):
        root_path = self._env_root("envs", "rootTenant", self.RESOURCE)
        self._env_root("demo", "envs", "demoTenant", self.RESOURCE)
        self._write_deployment({})

        self.assertEqual(
            ops.selected_env_pairs(None, []),
            [("rootTenant", self.RESOURCE, root_path)],
        )

    def test_no_tenant_discovery_uses_root_for_dot_overlay(self):
        root_path = self._env_root("envs", "rootTenant", self.RESOURCE)
        self._env_root("demo", "envs", "demoTenant", self.RESOURCE)
        self._write_deployment({"overlay": "."})

        self.assertEqual(
            ops.discover_env_pairs(),
            [("rootTenant", self.RESOURCE, root_path)],
        )

    def test_explicit_tenant_resolves_under_active_overlay(self):
        self._env_root("envs", "demoTenant", self.RESOURCE)
        overlay_path = self._env_root("demo", "envs", "demoTenant", self.RESOURCE)
        self._write_deployment({"overlay": "demo"})

        self.assertEqual(
            ops.selected_env_pairs("demoTenant", []),
            [("demoTenant", self.RESOURCE, overlay_path)],
        )

    def test_malformed_deployment_does_not_fall_back_to_root_envs(self):
        self._env_root("envs", "rootTenant", self.RESOURCE)
        self._write_deployment_text("{ not json")

        with self.assertRaises(ValueError):
            ops.discover_env_pairs()


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


class NonImportChangeCountTest(unittest.TestCase):
    def test_noop_and_import_only_are_zero(self):
        plan = _plan([_rc(["no-op"]), _rc(["create"], importing=True)])
        self.assertEqual(ops._non_import_change_count(plan), 0)

    def test_update_delete_replace_create_read_each_count_one(self):
        plan = _plan([
            _rc(["update"], before={"a": 1}, after={"a": 2}),
            _rc(["delete"]),
            _rc(["create", "delete"]),
            _rc(["create"]),
            _rc(["read"]),
        ])
        self.assertEqual(ops._non_import_change_count(plan), 5)

    def test_drift_records_count(self):
        plan = _plan([], drift=[_rc(["update"], before={"a": 1}, after={"a": 2})])
        self.assertEqual(ops._non_import_change_count(plan), 1)

    def test_importing_update_counts(self):
        plan = _plan([_rc(["update"], importing=True,
                          before={"a": 1}, after={"a": 2})])
        self.assertEqual(ops._non_import_change_count(plan), 1)


class OpsPlanSafetyTest(unittest.TestCase):
    def _run_apply_fixture(self, plan, opts_extra=None):
        tmp = tempfile.mkdtemp(prefix="ops-apply-")
        os.makedirs(tmp, exist_ok=True)
        with open(os.path.join(tmp, "tfplan"), "w", encoding="utf-8") as f:
            f.write("fake")
        old_pairs = ops.selected_env_pairs
        old_check_backend = ops._check_backend
        old_check_call = ops._check_call
        old_show = ops._show_plan_json
        old_branch = ops._current_branch
        old_stderr = sys.stderr
        stderr = io.StringIO()
        calls = []
        opts = {
            "tenant": "tenant",
            "selectors": [],
            "backend_config": None,
            "policy": None,
            "allow_destroy": False,
            "allow_non_main": False,
            "allow_plan_changes": False,
            "main_branch": "main",
        }
        if opts_extra:
            opts.update(opts_extra)
        try:
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._check_backend = lambda env_dir, resource_type, backend_config: None
            ops._check_call = lambda args, stdout=None: calls.append(args) or 0
            ops._current_branch = lambda: "main"
            ops._show_plan_json = lambda env_dir: plan
            sys.stderr = stderr
            error = None
            result = None
            try:
                result = ops.cmd_apply(opts)
            except Exception as exc:  # noqa: BLE001 - test helper preserves errors.
                error = exc
            return {
                "result": result,
                "error": error,
                "stderr": stderr.getvalue(),
                "calls": calls,
                "tfplan_exists": os.path.exists(os.path.join(tmp, "tfplan")),
            }
        finally:
            ops.selected_env_pairs = old_pairs
            ops._check_backend = old_check_backend
            ops._check_call = old_check_call
            ops._show_plan_json = old_show
            ops._current_branch = old_branch
            sys.stderr = old_stderr
            shutil.rmtree(tmp, ignore_errors=True)

    def _update_plan(self, before=None, after=None):
        return {
            "format_version": "1.0",
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["update"],
                    "before": before if before is not None else {"status": "old"},
                    "after": after if after is not None else {"status": "new"},
                },
            }],
        }

    def _delete_plan(self):
        return {
            "format_version": "1.0",
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["delete"],
                    "before": {"status": "old"},
                    "after": None,
                },
            }],
        }

    def _write_policy(self, entries):
        tmp = tempfile.mkdtemp(prefix="ops-policy-")
        path = os.path.join(tmp, "policy.json")
        with open(path, "w", encoding="utf-8") as f:
            json.dump({
                "version": 1,
                "resource_types": {
                    "sample_resource": {"plan_tolerate": entries},
                },
            }, f)
        self.addCleanup(lambda: shutil.rmtree(tmp, ignore_errors=True))
        return path

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
        result = self._run_apply_fixture(self._update_plan())
        self.assertIsInstance(result["error"], RuntimeError)
        self.assertIn("blocked by untolerated changes", str(result["error"]))
        self.assertTrue(result["tfplan_exists"])

    def test_apply_allows_import_only_without_plan_change_override(self):
        result = self._run_apply_fixture({
            "format_version": "1.0",
            "resource_changes": [{
                "change": {
                    "actions": ["create"],
                    "importing": {"id": "123"},
                }
            }],
        })
        self.assertIsNone(result["error"])
        self.assertEqual(result["result"], 0)
        self.assertFalse(result["tfplan_exists"])

    def test_apply_allows_policy_tolerated_update_without_plan_change_override(self):
        policy = self._write_policy([{
            "path": "status",
            "reason": "unit",
            "approved_by": "unit",
        }])
        result = self._run_apply_fixture(
            self._update_plan(),
            {"policy": policy},
        )
        self.assertIsNone(result["error"])
        self.assertEqual(result["result"], 0)
        self.assertIn("TOLERATED: tenant/sample_resource", result["stderr"])
        self.assertFalse(result["tfplan_exists"])

    def test_apply_blocks_update_not_tolerated_by_policy(self):
        policy = self._write_policy([{
            "path": "other_status",
            "reason": "unit",
            "approved_by": "unit",
        }])
        result = self._run_apply_fixture(
            self._update_plan(),
            {"policy": policy},
        )
        self.assertIsInstance(result["error"], RuntimeError)
        self.assertIn("blocked by untolerated changes", str(result["error"]))
        self.assertTrue(result["tfplan_exists"])

    def test_apply_malformed_policy_fails_closed_before_apply(self):
        tmp = tempfile.mkdtemp(prefix="ops-bad-policy-")
        path = os.path.join(tmp, "policy.json")
        with open(path, "w", encoding="utf-8") as f:
            f.write("{")
        self.addCleanup(lambda: shutil.rmtree(tmp, ignore_errors=True))
        result = self._run_apply_fixture(
            self._update_plan(),
            {"policy": path},
        )
        self.assertIsNotNone(result["error"])
        self.assertEqual(result["calls"], [])
        self.assertTrue(result["tfplan_exists"])

    def test_apply_allow_plan_changes_is_loud_legacy_override_for_update(self):
        result = self._run_apply_fixture(
            self._update_plan(),
            {"allow_plan_changes": True},
        )
        self.assertIsNone(result["error"])
        self.assertEqual(result["result"], 0)
        self.assertIn("broad legacy override", result["stderr"])
        self.assertIn("applying BLOCKED tenant/sample_resource", result["stderr"])
        self.assertFalse(result["tfplan_exists"])

    def test_apply_allow_plan_changes_does_not_bypass_destroy_guard(self):
        result = self._run_apply_fixture(
            self._delete_plan(),
            {"allow_plan_changes": True},
        )
        self.assertIsInstance(result["error"], RuntimeError)
        self.assertIn("saved plan destroys", str(result["error"]))
        self.assertTrue(result["tfplan_exists"])

    def test_assert_adoptable_warns_on_stale_policy(self):
        tmp = tempfile.mkdtemp(prefix="ops-adoptable-")
        policy_path = os.path.join(tmp, "policy.json")
        with open(policy_path, "w", encoding="utf-8") as f:
            json.dump({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "projection_omit": [
                            {
                                "path": "description",
                                "reason": "test",
                                "approved_by": "unit",
                            }
                        ],
                        "plan_tolerate": [
                            {
                                "path": "status",
                                "reason": "test",
                                "approved_by": "unit",
                            }
                        ]
                    },
                    "other_resource": {
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
            self.assertNotIn("projection_omit", stderr.getvalue())
            self.assertNotIn("other_resource", stderr.getvalue())
        finally:
            ops.selected_env_pairs = old_pairs
            ops._show_plan_json = old_show
            sys.stderr = old_stderr
            shutil.rmtree(tmp, ignore_errors=True)

    def test_assert_adoptable_guides_provider_config_but_stays_blocked(self):
        tmp = tempfile.mkdtemp(prefix="ops-provider-config-")
        pack_root = os.path.join(tmp, "packs")
        _write_json(os.path.join(pack_root, "sample", "pack.json"), {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "provider_config": {
                "requirements": [{
                    "id": "sample_disable_attribution_label",
                    "setting": "add_sample_attribution_label",
                    "value": False,
                    "reason": "Sample provider adds attribution labels.",
                    "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
                    "remediation": {
                        "kind": "provider_argument",
                        "mode": "required_external",
                        "evidence": "docs/provider-labs/sample.md",
                    },
                }]
            },
        })
        old_packs = os.environ.get("INFRAWRIGHT_PACKS")
        old_pairs = ops.selected_env_pairs
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._show_plan_json = lambda env_dir: {
                "format_version": "1.0",
                "resource_changes": [{
                    "address": "sample_resource.this",
                    "type": "sample_resource",
                    "change": {
                        "actions": ["update"],
                        "before": {"terraform_labels": {}},
                        "after": {
                            "terraform_labels": {
                                "goog-terraform-provisioned": "true",
                            }
                        },
                    },
                }],
            }
            sys.stderr = stderr

            with self.assertRaises(RuntimeError) as ctx:
                ops.cmd_assert_adoptable({
                    "tenant": "tenant",
                    "selectors": [],
                    "policy": None,
                })

            self.assertIn("1 saved plan(s) blocked", str(ctx.exception))
            out = stderr.getvalue()
            self.assertIn("BLOCKED: tenant/sample_resource", out)
            self.assertIn("Provider configuration guidance:", out)
            self.assertIn("provider: sample", out)
            self.assertIn("setting: add_sample_attribution_label", out)
            self.assertIn("expected value: false", out)
            self.assertIn("mode: required_external", out)
            self.assertIn(
                "matched plan path: terraform_labels.goog-terraform-provisioned", out
            )
            self.assertIn("reason: Sample provider adds attribution labels.", out)
            self.assertIn("evidence: docs/provider-labs/sample.md", out)
            self.assertIn("status: informational only; plan remains blocked", out)
            self.assertNotIn("adoptable with consumer-tolerated drift", out)
            self.assertNotIn("all 1 saved plan(s) clean", out)
        finally:
            if old_packs is None:
                os.environ.pop("INFRAWRIGHT_PACKS", None)
            else:
                os.environ["INFRAWRIGHT_PACKS"] = old_packs
            packs.reset()
            ops.selected_env_pairs = old_pairs
            ops._show_plan_json = old_show
            sys.stderr = old_stderr
            shutil.rmtree(tmp, ignore_errors=True)


class OpsAssertAdoptableProviderConfigGuidanceTest(unittest.TestCase):
    """Tests for provider-config guidance annotations in assert-adoptable output.

    These tests verify that the annotation is additive, fail-closed, and never
    changes plan status or renders/mutates provider configuration.
    """

    def _setup_test(self, pack_data, plan_data):
        tmp = tempfile.mkdtemp(prefix="ops-provider-config-")
        pack_root = os.path.join(tmp, "packs")
        _write_json(os.path.join(pack_root, "sample", "pack.json"), pack_data)
        old_packs = os.environ.get("INFRAWRIGHT_PACKS")
        old_pairs = ops.selected_env_pairs
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._show_plan_json = lambda env_dir: plan_data
            sys.stderr = stderr
            return tmp, old_packs, old_pairs, old_show, old_stderr, stderr
        except Exception:
            shutil.rmtree(tmp, ignore_errors=True)
            raise

    def _teardown(self, tmp, old_packs, old_pairs, old_show, old_stderr):
        if old_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = old_packs
        packs.reset()
        ops.selected_env_pairs = old_pairs
        ops._show_plan_json = old_show
        sys.stderr = old_stderr
        shutil.rmtree(tmp, ignore_errors=True)

    def _run_blocked(self, pack_data, plan_data):
        tmp, old_packs, old_pairs, old_show, old_stderr, stderr = self._setup_test(
            pack_data, plan_data
        )
        try:
            with self.assertRaises(RuntimeError) as ctx:
                ops.cmd_assert_adoptable({
                    "tenant": "tenant",
                    "selectors": [],
                    "policy": None,
                })
            return str(ctx.exception), stderr.getvalue()
        finally:
            self._teardown(tmp, old_packs, old_pairs, old_show, old_stderr)

    def _base_pack(self, requirement):
        return {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "provider_config": {"requirements": [requirement]},
        }

    def _base_plan(self, before, after, resource_type="sample_resource"):
        return {
            "format_version": "1.0",
            "resource_changes": [{
                "address": "%s.this" % resource_type,
                "type": resource_type,
                "change": {
                    "actions": ["update"],
                    "before": before,
                    "after": after,
                },
            }],
        }

    def test_required_external_annotation_contains_all_fields(self):
        requirement = {
            "id": "sample_disable_attribution_label",
            "setting": "add_sample_attribution_label",
            "value": False,
            "reason": "Sample provider adds attribution labels.",
            "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
            "remediation": {
                "kind": "provider_argument",
                "mode": "required_external",
                "evidence": "docs/provider-labs/sample.md",
            },
        }
        plan = self._base_plan(
            {"terraform_labels": {}},
            {"terraform_labels": {"goog-terraform-provisioned": "true"}},
        )
        exc, out = self._run_blocked(self._base_pack(requirement), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("Provider configuration guidance:", out)
        self.assertIn("provider: sample", out)
        self.assertIn("setting: add_sample_attribution_label", out)
        self.assertIn("expected value: false", out)
        self.assertIn("mode: required_external", out)
        self.assertIn(
            "matched plan path: terraform_labels.goog-terraform-provisioned", out
        )
        self.assertIn("reason: Sample provider adds attribution labels.", out)
        self.assertIn("evidence: docs/provider-labs/sample.md", out)
        self.assertIn("status: informational only; plan remains blocked", out)

    def test_renderable_default_annotation_is_guidance_only(self):
        requirement = {
            "id": "sample_disable_attribution_label",
            "setting": "add_sample_attribution_label",
            "value": False,
            "reason": "Sample provider adds attribution labels.",
            "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
            "remediation": {
                "kind": "provider_argument",
                "mode": "renderable_default",
                "evidence": "docs/provider-labs/sample.md",
                "safety": {
                    "non_sensitive": True,
                    "not_tenant_specific": True,
                    "not_destructive": True,
                },
            },
        }
        plan = self._base_plan(
            {"terraform_labels": {}},
            {"terraform_labels": {"goog-terraform-provisioned": "true"}},
        )
        exc, out = self._run_blocked(self._base_pack(requirement), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("mode: renderable_default", out)
        self.assertIn("status: informational only; plan remains blocked", out)
        self.assertNotIn("rendered provider", out)
        self.assertNotIn("provider_config {", out)

    def test_no_matching_metadata_leaves_output_unchanged(self):
        requirement = {
            "id": "sample_disable_attribution_label",
            "setting": "add_sample_attribution_label",
            "value": False,
            "reason": "Sample provider adds attribution labels.",
            "plan_paths": ["other_path"],
        }
        plan = self._base_plan(
            {"terraform_labels": {}},
            {"terraform_labels": {"goog-terraform-provisioned": "true"}},
        )
        exc, out = self._run_blocked(self._base_pack(requirement), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Provider configuration guidance:", out)
        self.assertIn("terraform_labels.goog-terraform-provisioned", out)

    def test_non_matching_plan_path_does_not_annotate(self):
        requirement = {
            "id": "sample_disable_attribution_label",
            "setting": "add_sample_attribution_label",
            "value": False,
            "reason": "Sample provider adds attribution labels.",
            "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
        }
        plan = self._base_plan(
            {"other_field": {}},
            {"other_field": {"changed": "true"}},
        )
        exc, out = self._run_blocked(self._base_pack(requirement), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Provider configuration guidance:", out)

    def test_wrong_provider_does_not_annotate(self):
        requirement = {
            "id": "other_disable_attribution_label",
            "provider": "other",
            "setting": "add_other_attribution_label",
            "value": False,
            "reason": "Other provider adds attribution labels.",
            "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
        }
        plan = self._base_plan(
            {"terraform_labels": {}},
            {"terraform_labels": {"goog-terraform-provisioned": "true"}},
        )
        # pack metadata has no provider_prefixes for other; provider is taken from
        # requirement but must still match the resource provider.
        pack = self._base_pack(requirement)
        pack["provider_prefixes"] = {"sample_": "sample", "other_": "other"}
        exc, out = self._run_blocked(pack, plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Provider configuration guidance:", out)

    def test_non_blocked_paths_do_not_annotate(self):
        # A clean plan produces no guidance and no exception.
        requirement = {
            "id": "sample_disable_attribution_label",
            "setting": "add_sample_attribution_label",
            "value": False,
            "reason": "Sample provider adds attribution labels.",
            "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
        }
        plan = {
            "format_version": "1.0",
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["no-op"],
                    "before": {"terraform_labels": {}},
                    "after": {"terraform_labels": {}},
                },
            }],
        }
        tmp, old_packs, old_pairs, old_show, old_stderr, stderr = self._setup_test(
            self._base_pack(requirement), plan
        )
        try:
            code = ops.cmd_assert_adoptable({
                "tenant": "tenant",
                "selectors": [],
                "policy": None,
            })
            self.assertEqual(code, 0)
            self.assertNotIn("Provider configuration guidance:", stderr.getvalue())
        finally:
            self._teardown(tmp, old_packs, old_pairs, old_show, old_stderr)

    def test_metadata_failure_does_not_annotate(self):
        requirement = {
            "id": "sample_disable_attribution_label",
            "setting": "add_sample_attribution_label",
            "reason": "Sample provider adds attribution labels.",
            # Missing plan_paths makes the metadata invalid.
        }
        plan = self._base_plan(
            {"terraform_labels": {}},
            {"terraform_labels": {"goog-terraform-provisioned": "true"}},
        )
        exc, out = self._run_blocked(self._base_pack(requirement), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        # The provider-config guidance section is omitted because metadata loading
        # fails. Existing blocked output is preserved.
        self.assertNotIn("Provider configuration guidance:", out)
        self.assertIn("terraform_labels.goog-terraform-provisioned", out)

    def test_deterministic_ordering_with_multiple_matches(self):
        requirement = {
            "id": "sample_settings",
            "setting": "add_sample_attribution_label",
            "value": False,
            "reason": "Sample provider adds attribution labels.",
            "plan_paths": [
                "terraform_labels.goog-terraform-provisioned",
                "labels.goog-terraform-provisioned",
            ],
            "remediation": {
                "kind": "provider_argument",
                "mode": "required_external",
                "evidence": "docs/provider-labs/sample.md",
            },
        }
        plan = {
            "format_version": "1.0",
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["update"],
                    "before": {
                        "terraform_labels": {},
                        "labels": {},
                    },
                    "after": {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        },
                        "labels": {
                            "goog-terraform-provisioned": "true",
                        },
                    },
                },
            }],
        }
        exc, out = self._run_blocked(self._base_pack(requirement), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("Provider configuration guidance:", out)
        # Annotations are sorted by provider, setting, matched_plan_path.
        first = out.find("matched plan path: labels.goog-terraform-provisioned")
        second = out.find(
            "matched plan path: terraform_labels.goog-terraform-provisioned"
        )
        self.assertNotEqual(first, -1)
        self.assertNotEqual(second, -1)
        self.assertLess(first, second)


    def test_diagnostic_only_does_not_annotate(self):
        requirement = {
            "id": "sample_disable_attribution_label",
            "setting": "add_sample_attribution_label",
            "value": False,
            "reason": "Sample provider adds attribution labels.",
            "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
            # No remediation -> diagnostic_only
        }
        plan = self._base_plan(
            {"terraform_labels": {}},
            {"terraform_labels": {"goog-terraform-provisioned": "true"}},
        )
        exc, out = self._run_blocked(self._base_pack(requirement), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Provider configuration guidance:", out)
        self.assertIn("terraform_labels.goog-terraform-provisioned", out)

    def test_tolerated_drift_does_not_annotate(self):
        requirement = {
            "id": "sample_disable_attribution_label",
            "setting": "add_sample_attribution_label",
            "value": False,
            "reason": "Sample provider adds attribution labels.",
            "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
            "remediation": {
                "kind": "provider_argument",
                "mode": "required_external",
                "evidence": "docs/provider-labs/sample.md",
            },
        }
        # Tolerated path is a no-op update that is fully covered by a policy
        # plan_tolerate entry.
        plan = {
            "format_version": "1.0",
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["update"],
                    "before": {"terraform_labels": {}},
                    "after": {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                },
            }],
        }
        tmp = tempfile.mkdtemp(prefix="ops-provider-config-tolerated-")
        policy_path = os.path.join(tmp, "policy.json")
        _write_json(policy_path, {
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "plan_tolerate": [{
                        "path": 'terraform_labels["goog-terraform-provisioned"]',
                        "reason": "test tolerance",
                        "approved_by": "unit",
                    }]
                }
            }
        })
        pack_root = os.path.join(tmp, "packs")
        _write_json(os.path.join(pack_root, "sample", "pack.json"), self._base_pack(requirement))
        old_packs = os.environ.get("INFRAWRIGHT_PACKS")
        old_pairs = ops.selected_env_pairs
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._show_plan_json = lambda env_dir: plan
            sys.stderr = stderr
            code = ops.cmd_assert_adoptable({
                "tenant": "tenant",
                "selectors": [],
                "policy": policy_path,
            })
            self.assertEqual(code, 0)
            out = stderr.getvalue()
            self.assertIn("adoptable with consumer-tolerated drift", out)
            self.assertNotIn("Provider configuration guidance:", out)
        finally:
            if old_packs is None:
                os.environ.pop("INFRAWRIGHT_PACKS", None)
            else:
                os.environ["INFRAWRIGHT_PACKS"] = old_packs
            packs.reset()
            ops.selected_env_pairs = old_pairs
            ops._show_plan_json = old_show
            sys.stderr = old_stderr
            shutil.rmtree(tmp, ignore_errors=True)

    def test_provider_config_and_absent_default_guidance_can_coexist(self):
        pack = {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "provider_config": {
                "requirements": [{
                    "id": "sample_disable_attribution_label",
                    "setting": "add_sample_attribution_label",
                    "value": False,
                    "reason": "Sample provider adds attribution labels.",
                    "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
                    "remediation": {
                        "kind": "provider_argument",
                        "mode": "required_external",
                        "evidence": "docs/provider-labs/sample.md",
                    },
                }]
            },
            "absent_defaults": {
                "rules": [{
                    "id": "sample_empty_name_prefix",
                    "provider": "sample",
                    "resource_type": "sample_resource",
                    "path": "name_prefix",
                    "kind": "provider_absent_placeholder",
                    "observed_value": "",
                    "action": "manual_review_required",
                    "evidence": "docs/provider-labs/sample.md",
                    "reason": "Sample provider imported empty name_prefix.",
                }]
            },
        }
        plan = self._base_plan(
            {
                "name": "thing",
                "name_prefix": "",
                "terraform_labels": {},
            },
            {
                "name": "thing",
                "terraform_labels": {
                    "goog-terraform-provisioned": "true",
                },
            },
        )
        exc, out = self._run_blocked(pack, plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("Provider configuration guidance:", out)
        self.assertIn("setting: add_sample_attribution_label", out)
        self.assertIn("Absent/default guidance:", out)
        self.assertIn("rule: sample_empty_name_prefix", out)
        self.assertIn("status: informational only; plan remains blocked", out)
        self.assertNotIn("adoptable with consumer-tolerated drift", out)
        self.assertNotIn("all 1 saved plan(s) clean", out)


class OpsAssertAdoptableDynamicSchemaGuidanceTest(unittest.TestCase):
    """Tests for dynamic-schema guidance annotations in blocked output."""

    def _setup_test(self, pack_data, plan_data):
        tmp = tempfile.mkdtemp(prefix="ops-dynamic-schema-")
        pack_root = os.path.join(tmp, "packs")
        _write_json(os.path.join(pack_root, "sample", "pack.json"), pack_data)
        old_packs = os.environ.get("INFRAWRIGHT_PACKS")
        old_pairs = ops.selected_env_pairs
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._show_plan_json = lambda env_dir: plan_data
            sys.stderr = stderr
            return tmp, old_packs, old_pairs, old_show, old_stderr, stderr
        except Exception:
            shutil.rmtree(tmp, ignore_errors=True)
            raise

    def _teardown(self, tmp, old_packs, old_pairs, old_show, old_stderr):
        if old_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = old_packs
        packs.reset()
        ops.selected_env_pairs = old_pairs
        ops._show_plan_json = old_show
        sys.stderr = old_stderr
        shutil.rmtree(tmp, ignore_errors=True)

    def _run_blocked(self, pack_data, plan_data):
        tmp, old_packs, old_pairs, old_show, old_stderr, stderr = self._setup_test(
            pack_data, plan_data
        )
        try:
            with self.assertRaises(RuntimeError) as ctx:
                ops.cmd_assert_adoptable({
                    "tenant": "tenant",
                    "selectors": [],
                    "policy": None,
                })
            return str(ctx.exception), stderr.getvalue()
        finally:
            self._teardown(tmp, old_packs, old_pairs, old_show, old_stderr)

    def _base_pack(self, rule):
        return {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "dynamic_schema": {"rules": [rule]},
        }

    def _base_rule(self, **overrides):
        rule = {
            "id": "sample_dynamic_data_flags",
            "provider": "sample",
            "provider_version_constraint": "1.2.3",
            "resource_type": "sample_resource",
            "path": "data.flags",
            "kind": "provider_observed_projection_unsafe",
            "ownership": "unknown",
            "action": "manual_review_required",
            "evidence": "docs/provider-labs/sample.md",
            "reason": "Sample provider exposes a dynamic data.flags path.",
        }
        rule.update(overrides)
        return rule

    def _base_plan(self, before, after):
        return {
            "format_version": "1.0",
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["update"],
                    "before": before,
                    "after": after,
                },
            }],
        }

    def test_manual_review_annotation_contains_all_fields(self):
        plan = self._base_plan(
            {"data": {}},
            {"data": {"flags": "provider-added"}},
        )
        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("BLOCKED: tenant/sample_resource", out)
        self.assertIn("Dynamic-schema guidance:", out)
        self.assertIn("rule: sample_dynamic_data_flags", out)
        self.assertIn("provider: sample", out)
        self.assertIn("resource type: sample_resource", out)
        self.assertIn("kind: provider_observed_projection_unsafe", out)
        self.assertIn("ownership: unknown", out)
        self.assertIn("action: manual_review_required", out)
        self.assertIn("provider version constraint: 1.2.3", out)
        self.assertIn("matched plan path: data.flags", out)
        self.assertIn("reason: Sample provider exposes a dynamic data.flags path.", out)
        self.assertIn("evidence: docs/provider-labs/sample.md", out)
        self.assertIn("status: informational only; plan remains blocked", out)
        self.assertNotIn("adoptable with consumer-tolerated drift", out)
        self.assertNotIn("all 1 saved plan(s) clean", out)

    def test_non_matching_plan_path_does_not_annotate(self):
        plan = self._base_plan({"other": ""}, {"other": "value"})
        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("other", out)
        self.assertNotIn("Dynamic-schema guidance:", out)

    def test_wrong_provider_does_not_annotate(self):
        rule = self._base_rule(
            id="other_dynamic_data_flags",
            provider="other",
            resource_type="other_resource",
        )
        pack = self._base_pack(rule)
        pack["provider_prefixes"] = {"sample_": "sample", "other_": "other"}
        plan = self._base_plan({"data": {}}, {"data": {"flags": "x"}})
        exc, out = self._run_blocked(pack, plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Dynamic-schema guidance:", out)

    def test_wrong_resource_type_does_not_annotate(self):
        rule = self._base_rule(resource_type="sample_other")
        plan = self._base_plan({"data": {}}, {"data": {"flags": "x"}})
        exc, out = self._run_blocked(self._base_pack(rule), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Dynamic-schema guidance:", out)

    def test_resource_prefix_scope_can_annotate(self):
        rule = self._base_rule(resource_type=None, resource_prefix="sample_")
        del rule["resource_type"]
        plan = self._base_plan({"data": {}}, {"data": {"flags": "x"}})
        exc, out = self._run_blocked(self._base_pack(rule), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("Dynamic-schema guidance:", out)
        self.assertIn("rule: sample_dynamic_data_flags", out)

    def test_diagnostic_only_does_not_annotate(self):
        rule = self._base_rule(action="diagnostic_only")
        plan = self._base_plan({"data": {}}, {"data": {"flags": "x"}})
        exc, out = self._run_blocked(self._base_pack(rule), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Dynamic-schema guidance:", out)
        self.assertIn("data.flags", out)

    def test_reserved_action_fails_closed_without_annotation(self):
        rule = self._base_rule(action="preserve_observed_scalar")
        plan = self._base_plan({"data": {}}, {"data": {"flags": "x"}})
        exc, out = self._run_blocked(self._base_pack(rule), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("data.flags", out)
        self.assertNotIn("Dynamic-schema guidance:", out)
        self.assertNotIn("preserve_observed_scalar", out)

    def test_helper_failure_preserves_blocked_output(self):
        plan = self._base_plan({"data": {}}, {"data": {"flags": "x"}})
        old_impl = ops._dynamic_schema_guidance
        try:
            ops._dynamic_schema_guidance = lambda _plan, _resource_type: (
                (_ for _ in ()).throw(RuntimeError("boom"))
            )
            exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        finally:
            ops._dynamic_schema_guidance = old_impl
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("data.flags", out)
        self.assertNotIn("Dynamic-schema guidance:", out)
        self.assertNotIn("boom", out)

    def test_after_unknown_path_can_annotate(self):
        plan = self._base_plan(
            {"data": {"flags": "known"}},
            {"data": {"flags": "known"}},
        )
        plan["resource_changes"][0]["change"]["after_unknown"] = {
            "data": {"flags": True},
        }
        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("Dynamic-schema guidance:", out)
        self.assertIn("matched plan path: data.flags", out)

    def test_sensitivity_only_path_does_not_annotate(self):
        plan = self._base_plan(
            {"data": {"flags": "same"}, "other": "old"},
            {"data": {"flags": "same"}, "other": "new"},
        )
        plan["resource_changes"][0]["change"]["before_sensitive"] = {
            "data": {"flags": True},
        }
        plan["resource_changes"][0]["change"]["after_sensitive"] = {
            "data": {"flags": True},
        }
        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("other", out)
        self.assertNotIn("data.flags", out)
        self.assertNotIn("Dynamic-schema guidance:", out)

    def test_tolerated_drift_does_not_collect_guidance(self):
        plan = self._base_plan({"data": {}}, {"data": {"flags": "x"}})
        tmp = tempfile.mkdtemp(prefix="ops-dynamic-schema-tolerated-")
        policy_path = os.path.join(tmp, "policy.json")
        _write_json(policy_path, {
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "plan_tolerate": [{
                        "path": "data.flags",
                        "reason": "test tolerance",
                        "approved_by": "unit",
                    }]
                }
            }
        })
        pack_root = os.path.join(tmp, "packs")
        _write_json(
            os.path.join(pack_root, "sample", "pack.json"),
            self._base_pack(self._base_rule()),
        )
        old_packs = os.environ.get("INFRAWRIGHT_PACKS")
        old_pairs = ops.selected_env_pairs
        old_show = ops._show_plan_json
        old_guidance = ops._guidance_annotations
        old_stderr = sys.stderr
        stderr = io.StringIO()
        calls = []
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._show_plan_json = lambda env_dir: plan
            ops._guidance_annotations = lambda _plan, _resource_type: (
                calls.append(True) or []
            )
            sys.stderr = stderr
            code = ops.cmd_assert_adoptable({
                "tenant": "tenant",
                "selectors": [],
                "policy": policy_path,
            })
            self.assertEqual(code, 0)
            out = stderr.getvalue()
            self.assertIn("adoptable with consumer-tolerated drift", out)
            self.assertNotIn("Dynamic-schema guidance:", out)
            self.assertEqual(calls, [])
        finally:
            if old_packs is None:
                os.environ.pop("INFRAWRIGHT_PACKS", None)
            else:
                os.environ["INFRAWRIGHT_PACKS"] = old_packs
            packs.reset()
            ops.selected_env_pairs = old_pairs
            ops._show_plan_json = old_show
            ops._guidance_annotations = old_guidance
            sys.stderr = old_stderr
            shutil.rmtree(tmp, ignore_errors=True)

    def test_committed_cloudflare_metadata_can_surface_guidance(self):
        old_packs = os.environ.get("INFRAWRIGHT_PACKS")
        try:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
            packs.reset()
            plan = {
                "format_version": "1.0",
                "resource_changes": [{
                    "address": "cloudflare_dns_record.this",
                    "type": "cloudflare_dns_record",
                    "change": {
                        "actions": ["update"],
                        "before": {"data": {}},
                        "after": {"data": {"flags": ["aa"]}},
                    },
                }],
            }
            annotations = ops._dynamic_schema_guidance(
                plan,
                "cloudflare_dns_record",
            )
        finally:
            if old_packs is None:
                os.environ.pop("INFRAWRIGHT_PACKS", None)
            else:
                os.environ["INFRAWRIGHT_PACKS"] = old_packs
            packs.reset()
        self.assertEqual(len(annotations), 1)
        self.assertEqual(
            annotations[0]["rule"],
            "cloudflare_dns_record_data_flags_dynamic",
        )
        self.assertEqual(annotations[0]["matched_plan_path"], "data.flags")


class OpsAssertAdoptableAbsentDefaultGuidanceTest(unittest.TestCase):
    """Tests for absent/default guidance annotations in blocked output."""

    def _setup_test(self, pack_data, plan_data):
        tmp = tempfile.mkdtemp(prefix="ops-absent-default-")
        pack_root = os.path.join(tmp, "packs")
        _write_json(os.path.join(pack_root, "sample", "pack.json"), pack_data)
        old_packs = os.environ.get("INFRAWRIGHT_PACKS")
        old_pairs = ops.selected_env_pairs
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._show_plan_json = lambda env_dir: plan_data
            sys.stderr = stderr
            return tmp, old_packs, old_pairs, old_show, old_stderr, stderr
        except Exception:
            shutil.rmtree(tmp, ignore_errors=True)
            raise

    def _teardown(self, tmp, old_packs, old_pairs, old_show, old_stderr):
        if old_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = old_packs
        packs.reset()
        ops.selected_env_pairs = old_pairs
        ops._show_plan_json = old_show
        sys.stderr = old_stderr
        shutil.rmtree(tmp, ignore_errors=True)

    def _run_blocked(self, pack_data, plan_data):
        tmp, old_packs, old_pairs, old_show, old_stderr, stderr = self._setup_test(
            pack_data, plan_data
        )
        try:
            with self.assertRaises(RuntimeError) as ctx:
                ops.cmd_assert_adoptable({
                    "tenant": "tenant",
                    "selectors": [],
                    "policy": None,
                })
            return str(ctx.exception), stderr.getvalue()
        finally:
            self._teardown(tmp, old_packs, old_pairs, old_show, old_stderr)

    def _base_pack(self, rule):
        return {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "absent_defaults": {"rules": [rule]},
        }

    def _base_rule(self, **overrides):
        rule = {
            "id": "sample_empty_name_prefix",
            "provider": "sample",
            "resource_type": "sample_resource",
            "path": "name_prefix",
            "kind": "provider_absent_placeholder",
            "observed_value": "",
            "action": "manual_review_required",
            "evidence": "docs/provider-labs/sample.md",
            "reason": (
                "Sample provider imported empty name_prefix alongside concrete "
                "name; manual review required."
            ),
        }
        rule.update(overrides)
        return rule

    def _base_plan(self, before, after):
        return {
            "format_version": "1.0",
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["update"],
                    "before": before,
                    "after": after,
                },
            }],
        }

    def test_manual_review_annotation_contains_all_fields(self):
        plan = self._base_plan(
            {"name": "thing", "name_prefix": ""},
            {"name": "thing"},
        )
        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("BLOCKED: tenant/sample_resource", out)
        self.assertIn("Absent/default guidance:", out)
        self.assertIn("rule: sample_empty_name_prefix", out)
        self.assertIn("provider: sample", out)
        self.assertIn("resource type: sample_resource", out)
        self.assertIn("kind: provider_absent_placeholder", out)
        self.assertIn("action: manual_review_required", out)
        self.assertIn('observed value: ""', out)
        self.assertIn("matched plan path: name_prefix", out)
        self.assertIn(
            "reason: Sample provider imported empty name_prefix", out
        )
        self.assertIn("evidence: docs/provider-labs/sample.md", out)
        self.assertIn("status: informational only; plan remains blocked", out)
        self.assertNotIn("adoptable with consumer-tolerated drift", out)
        self.assertNotIn("all 1 saved plan(s) clean", out)

    def test_sensitivity_only_path_does_not_annotate(self):
        plan = self._base_plan(
            {"name": "thing", "name_prefix": "", "other": "old"},
            {"name": "thing", "name_prefix": "", "other": "new"},
        )
        plan["resource_changes"][0]["change"]["before_sensitive"] = {
            "name_prefix": True,
        }
        plan["resource_changes"][0]["change"]["after_sensitive"] = {
            "name_prefix": True,
        }

        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("other", out)
        self.assertNotIn("name_prefix", out)
        self.assertNotIn("Absent/default guidance:", out)

    def test_unknown_after_path_still_annotates(self):
        plan = self._base_plan(
            {"name": "thing", "name_prefix": ""},
            {"name": "thing", "name_prefix": ""},
        )
        plan["resource_changes"][0]["change"]["after_unknown"] = {
            "name_prefix": True,
        }

        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("Absent/default guidance:", out)
        self.assertIn("matched plan path: name_prefix", out)

    def test_diff_path_still_annotates_when_also_sensitive(self):
        plan = self._base_plan(
            {"name": "thing", "name_prefix": ""},
            {"name": "thing"},
        )
        plan["resource_changes"][0]["change"]["before_sensitive"] = {
            "name_prefix": True,
        }
        plan["resource_changes"][0]["change"]["after_sensitive"] = {
            "name_prefix": True,
        }

        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("Absent/default guidance:", out)
        self.assertIn("matched plan path: name_prefix", out)

    def test_observed_value_must_match_before_value(self):
        plan = self._base_plan(
            {"name": "thing", "name_prefix": "not-empty"},
            {"name": "thing"},
        )
        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("name_prefix", out)
        self.assertNotIn("Absent/default guidance:", out)

    def test_missing_observed_value_does_not_annotate(self):
        plan = self._base_plan(
            {"name": "thing"},
            {"name": "thing", "name_prefix": "generated"},
        )
        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("name_prefix", out)
        self.assertNotIn("Absent/default guidance:", out)

    def test_guidance_helper_failure_preserves_blocked_output(self):
        plan = self._base_plan(
            {"name": "thing", "name_prefix": ""},
            {"name": "thing"},
        )
        old_impl = ops._absent_default_guidance
        try:
            ops._absent_default_guidance = lambda _plan, _resource_type: (
                (_ for _ in ()).throw(RuntimeError("boom"))
            )
            exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        finally:
            ops._absent_default_guidance = old_impl
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertIn("name_prefix", out)
        self.assertNotIn("Absent/default guidance:", out)
        self.assertNotIn("boom", out)

    def test_non_matching_plan_path_does_not_annotate(self):
        plan = self._base_plan({"other": ""}, {"other": "value"})
        exc, out = self._run_blocked(self._base_pack(self._base_rule()), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Absent/default guidance:", out)
        self.assertIn("other", out)

    def test_diagnostic_only_rule_does_not_annotate(self):
        rule = self._base_rule(action="diagnostic_only")
        plan = self._base_plan(
            {"name": "thing", "name_prefix": ""},
            {"name": "thing"},
        )
        exc, out = self._run_blocked(self._base_pack(rule), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Absent/default guidance:", out)
        self.assertIn("name_prefix", out)

    def test_metadata_failure_does_not_annotate(self):
        rule = self._base_rule()
        del rule["evidence"]
        plan = self._base_plan(
            {"name": "thing", "name_prefix": ""},
            {"name": "thing"},
        )
        exc, out = self._run_blocked(self._base_pack(rule), plan)
        self.assertIn("1 saved plan(s) blocked", exc)
        self.assertNotIn("Absent/default guidance:", out)
        self.assertIn("name_prefix", out)

    def test_tolerated_drift_does_not_annotate(self):
        plan = self._base_plan(
            {"name": "thing", "name_prefix": ""},
            {"name": "thing"},
        )
        tmp = tempfile.mkdtemp(prefix="ops-absent-default-tolerated-")
        policy_path = os.path.join(tmp, "policy.json")
        _write_json(policy_path, {
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "plan_tolerate": [{
                        "path": "name_prefix",
                        "reason": "test tolerance",
                        "approved_by": "unit",
                    }]
                }
            }
        })
        pack_root = os.path.join(tmp, "packs")
        _write_json(
            os.path.join(pack_root, "sample", "pack.json"),
            self._base_pack(self._base_rule()),
        )
        old_packs = os.environ.get("INFRAWRIGHT_PACKS")
        old_pairs = ops.selected_env_pairs
        old_show = ops._show_plan_json
        old_guidance = ops._guidance_annotations
        old_stderr = sys.stderr
        stderr = io.StringIO()
        calls = []
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            ops.selected_env_pairs = lambda tenant, selectors, require_plan=False: [
                ("tenant", "sample_resource", tmp)
            ]
            ops._show_plan_json = lambda env_dir: plan
            ops._guidance_annotations = lambda _plan, _resource_type: (
                calls.append(True) or []
            )
            sys.stderr = stderr
            code = ops.cmd_assert_adoptable({
                "tenant": "tenant",
                "selectors": [],
                "policy": policy_path,
            })
            self.assertEqual(code, 0)
            out = stderr.getvalue()
            self.assertIn("adoptable with consumer-tolerated drift", out)
            self.assertNotIn("Absent/default guidance:", out)
            self.assertEqual(calls, [])
        finally:
            if old_packs is None:
                os.environ.pop("INFRAWRIGHT_PACKS", None)
            else:
                os.environ["INFRAWRIGHT_PACKS"] = old_packs
            packs.reset()
            ops.selected_env_pairs = old_pairs
            ops._show_plan_json = old_show
            ops._guidance_annotations = old_guidance
            sys.stderr = old_stderr
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    unittest.main()
