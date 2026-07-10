import json
import io
import os
import shutil
import sys
import tempfile
import unittest

from engine import ops
from engine import packs
from engine import registry
from engine import transform


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


def _root_tuple(path, resource_type="sample_resource",
                label=None, tenant="tenant"):
    return (tenant, label or resource_type, path, [resource_type])


def _write_test_root_modules(path, member_types):
    os.makedirs(path, exist_ok=True)
    sections = []
    for resource_type in member_types:
        module_path = os.path.join(path, "test-modules", resource_type)
        os.makedirs(module_path, exist_ok=True)
        with open(os.path.join(module_path, "main.tf"), "w",
                  encoding="utf-8") as f:
            f.write("# %s test module\n" % resource_type)
        sections.append(
            'module "%s" {\n'
            '  source = "./test-modules/%s"\n'
            '  items = var.%s_items\n'
            '}\n'
            % (resource_type, resource_type, resource_type)
        )
    with open(os.path.join(path, "main.tf"), "w", encoding="utf-8") as f:
        f.write("\n".join(sections))


def _write_fresh_plan(path, member_types=None):
    member_types = member_types or ["sample_resource"]
    _write_test_root_modules(path, member_types)
    with open(os.path.join(path, "tfplan"), "w", encoding="utf-8") as f:
        f.write("fake")
    ops._write_plan_fingerprint(path, [], member_types)


class OpsReferenceOrderTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="ops-reference-order-")
        self.pack_root = os.path.join(self.tmp, "packs")
        self.saved_packs = os.environ.get("INFRAWRIGHT_PACKS")

    def tearDown(self):
        if self.saved_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.saved_packs
        packs.reset()
        registry.reload_registry()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _configure_pack(self, references):
        os.environ["INFRAWRIGHT_PACKS"] = self.pack_root
        _write_json(os.path.join(self.pack_root, "sample", "pack.json"), {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "references": references,
        })
        _write_json(os.path.join(self.pack_root, "sample", "registry.json"), {
            "sample_a_referrer": {"generate": True, "product": "sample"},
            "sample_aa_unrelated": {"generate": True, "product": "sample"},
            "sample_b_referent": {"generate": True, "product": "sample"},
            "sample_cycle_a": {"generate": True, "product": "sample"},
            "sample_cycle_b": {"generate": True, "product": "sample"},
        })
        packs.reset()
        registry.reload_registry()

    def _capture_resources(self, argv):
        old_stdout = sys.stdout
        old_stderr = sys.stderr
        stdout = io.StringIO()
        stderr = io.StringIO()
        try:
            sys.stdout = stdout
            sys.stderr = stderr
            code = ops.main(["resources"] + argv)
        finally:
            sys.stdout = old_stdout
            sys.stderr = old_stderr
        return code, stdout.getvalue(), stderr.getvalue()

    def test_reference_order_emits_referent_before_referrer(self):
        self._configure_pack({
            "sample_a_referrer": {
                "referent_id": {
                    "referent": "sample_b_referent",
                    "name_field": "name",
                },
            },
        })

        self.assertEqual(ops.reference_order([
            "sample_a_referrer",
            "sample_aa_unrelated",
            "sample_b_referent",
        ]), [
            "sample_aa_unrelated",
            "sample_b_referent",
            "sample_a_referrer",
        ])

    def test_resources_cli_default_order_is_unchanged(self):
        self._configure_pack({
            "sample_a_referrer": {
                "referent_id": {
                    "referent": "sample_b_referent",
                    "name_field": "name",
                },
            },
        })

        code, stdout, stderr = self._capture_resources([])

        self.assertEqual(code, 0)
        self.assertEqual(
            stdout.splitlines(),
            [
                "sample_a_referrer",
                "sample_aa_unrelated",
                "sample_b_referent",
                "sample_cycle_a",
                "sample_cycle_b",
            ],
        )
        self.assertEqual(stderr, "")

    def test_resources_cli_reference_order(self):
        self._configure_pack({
            "sample_a_referrer": {
                "referent_id": {
                    "referent": "sample_b_referent",
                    "name_field": "name",
                },
            },
        })

        code, stdout, stderr = self._capture_resources([
            "--order=references",
            "sample_a_referrer",
            "sample_b_referent",
        ])

        self.assertEqual(code, 0)
        self.assertEqual(
            stdout.splitlines(),
            ["sample_b_referent", "sample_a_referrer"],
        )
        self.assertEqual(stderr, "")

    def test_reference_cycle_falls_back_with_one_note(self):
        self._configure_pack({
            "sample_cycle_a": {
                "other_id": {
                    "referent": "sample_cycle_b",
                    "name_field": "name",
                },
            },
            "sample_cycle_b": {
                "other_id": {
                    "referent": "sample_cycle_a",
                    "name_field": "name",
                },
            },
        })
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            sys.stderr = stderr
            order = ops.reference_order(["sample_cycle_b", "sample_cycle_a"])
        finally:
            sys.stderr = old_stderr

        self.assertEqual(order, ["sample_cycle_a", "sample_cycle_b"])
        self.assertEqual(
            stderr.getvalue(),
            "NOTE: reference order cycle detected among sample_cycle_a, "
            "sample_cycle_b; breaking alphabetically\n",
        )


class OpsReferenceOrderTransformIntegrationTest(unittest.TestCase):
    def setUp(self):
        self.cwd = os.getcwd()
        self.tmp = tempfile.mkdtemp(prefix="ops-reference-transform-")
        os.chdir(self.tmp)
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")
        self.tenant = "tenant"
        dep = os.path.join(self.tmp, "deployment.json")
        _write_json(dep, {
            "overlay": self.tmp,
            "roots": {
                "zpa": {
                    "groups": {
                        "zpa_custom": [
                            "zpa_application_segment",
                            "zpa_segment_group",
                        ],
                    },
                    "bind_references": True,
                },
            },
        })
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep

    def tearDown(self):
        os.chdir(self.cwd)
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_input(self, name, data):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f)
        return path

    def test_topo_order_transform_derives_binding_on_first_run(self):
        inputs = {
            "zpa_segment_group": self._write_input("zpa_segment_group.json", [{
                "id": "sg-1",
                "name": "Segment One",
                "enabled": True,
            }]),
            "zpa_application_segment": self._write_input(
                "zpa_application_segment.json",
                [{
                    "id": "app-1",
                    "name": "App One",
                    "domainNames": ["app.example.com"],
                    "segmentGroupId": "sg-1",
                }],
            ),
        }

        ordered = ops.reference_order([
            "zpa_application_segment",
            "zpa_segment_group",
        ])
        self.assertEqual(ordered, [
            "zpa_segment_group",
            "zpa_application_segment",
        ])
        for resource_type in ordered:
            self.assertEqual(
                transform.main([resource_type, inputs[resource_type], self.tenant]),
                0,
            )

        generated_path = os.path.join(
            self.tmp,
            "config",
            self.tenant,
            "zpa_application_segment.generated.expressions.json",
        )
        with open(generated_path, encoding="utf-8") as f:
            generated = json.load(f)
        expression = (
            generated["resources"]["zpa_application_segment.app_one"]
            ["segment_group_id"]["expression"]
        )
        self.assertEqual(
            expression,
            'module.zpa_segment_group.items["segment_one"].id',
        )

    def test_access_rule_does_not_claim_inspection_profile_dependency(self):
        access_rule_refs = packs.references().get(
            "zpa_policy_access_rule",
            {},
        )
        self.assertNotIn("zpn_inspection_profile_id", access_rule_refs)


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

    def test_grouped_root_discovery_and_member_selection_note(self):
        grouped_path = self._env_root("envs", "tenant", "zpa_custom")
        self._write_deployment({
            "roots": {
                "zpa": {
                    "groups": {
                        "zpa_custom": [
                            "zpa_segment_group",
                            "zpa_server_group",
                        ],
                    },
                },
            },
        })
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            sys.stderr = stderr
            self.assertEqual(
                ops.selected_env_roots("tenant", ["zpa_segment_group"]),
                [(
                    "tenant",
                    "zpa_custom",
                    grouped_path,
                    ["zpa_segment_group", "zpa_server_group"],
                )],
            )
        finally:
            sys.stderr = old_stderr
        self.assertEqual(
            stderr.getvalue(),
            "NOTE: selecting zpa_segment_group selects whole root zpa_custom; "
            "also operating on zpa_server_group\n",
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

    def test_grouped_stage_imports_copies_each_member_file_to_shared_root(self):
        dep = os.path.join(self.tmp, "deployment.json")
        _write_json(dep, {
            "roots": {
                "zpa": {
                    "groups": {
                        "zpa_custom": [
                            "zpa_segment_group",
                            "zpa_server_group",
                        ],
                    },
                },
            },
        })
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep
        os.makedirs(os.path.join("imports", "tenant"), exist_ok=True)
        os.makedirs(os.path.join("envs", "tenant", "zpa_custom"), exist_ok=True)
        sources = [
            os.path.join("imports", "tenant", "zpa_segment_group_imports.tf"),
            os.path.join("imports", "tenant", "zpa_server_group_moves.tf"),
        ]
        for source in sources:
            with open(source, "w", encoding="utf-8") as f:
                f.write("# staged\n")
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            sys.stderr = stderr
            code = ops.cmd_stage_imports({
                "tenant": "tenant",
                "selectors": ["zpa_segment_group"],
                "state_aware": False,
                "backend_config": None,
            })
        finally:
            sys.stderr = old_stderr
        self.assertEqual(code, 0)
        self.assertTrue(os.path.exists(os.path.join(
            "envs", "tenant", "zpa_custom", "zpa_segment_group_imports.tf"
        )))
        self.assertTrue(os.path.exists(os.path.join(
            "envs", "tenant", "zpa_custom", "zpa_server_group_moves.tf"
        )))
        self.assertIn(
            "NOTE: selecting zpa_segment_group selects whole root zpa_custom; "
            "also operating on zpa_server_group\n",
            stderr.getvalue(),
        )


class OpsGroupedRootCommandTest(unittest.TestCase):
    def setUp(self):
        self.cwd = os.getcwd()
        self.tmp = tempfile.mkdtemp(prefix="ops-grouped-")
        os.chdir(self.tmp)
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")
        dep = os.path.join(self.tmp, "deployment.json")
        _write_json(dep, {
            "roots": {
                "zpa": {
                    "groups": {
                        "zpa_custom": [
                            "zpa_segment_group",
                            "zpa_server_group",
                        ],
                    },
                },
            },
        })
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep

    def tearDown(self):
        os.chdir(self.cwd)
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_group_root(self, with_plan=False):
        root = os.path.join("envs", "tenant", "zpa_custom")
        members = ["zpa_segment_group", "zpa_server_group"]
        _write_test_root_modules(root, members)
        if with_plan:
            with open(os.path.join(root, "tfplan"), "w", encoding="utf-8") as f:
                f.write("fake")
            ops._write_plan_fingerprint(root, [], members)
        return root

    def _write_member_configs(self):
        os.makedirs(os.path.join("config", "tenant"), exist_ok=True)
        paths = []
        for resource_type in ("zpa_segment_group", "zpa_server_group"):
            path = os.path.join(
                "config", "tenant", resource_type + ".auto.tfvars.json")
            with open(path, "w", encoding="utf-8") as f:
                json.dump({"%s_items" % resource_type: {}}, f)
            paths.append(path)
        return paths

    def test_plan_fails_loud_on_partial_member_configs(self):
        self._write_group_root()
        os.makedirs(os.path.join("config", "tenant"), exist_ok=True)
        present = os.path.join(
            "config", "tenant", "zpa_segment_group.auto.tfvars.json")
        with open(present, "w", encoding="utf-8") as f:
            json.dump({"zpa_segment_group_items": {}}, f)
        old_check = ops._check_call
        old_backend = ops._check_backend
        old_stderr = sys.stderr
        calls = []
        try:
            ops._check_call = lambda args, stdout=None: calls.append(args) or 0
            ops._check_backend = lambda env_dir, label, backend_config: None
            sys.stderr = io.StringIO()
            with self.assertRaises(RuntimeError) as ctx:
                ops.cmd_plan({
                    "tenant": "tenant",
                    "selectors": ["zpa_segment_group"],
                    "imports_only": False,
                    "backend_config": None,
                    "save": False,
                })
        finally:
            ops._check_call = old_check
            ops._check_backend = old_backend
            sys.stderr = old_stderr
        message = str(ctx.exception)
        self.assertIn("missing member config(s)", message)
        self.assertIn("zpa_server_group.auto.tfvars.json", message)
        self.assertEqual(calls, [])

    def test_plan_builds_one_root_argv_with_each_member_var_file(self):
        root = self._write_group_root()
        config_paths = self._write_member_configs()
        old_check = ops._check_call
        old_backend = ops._check_backend
        old_stderr = sys.stderr
        calls = []
        stderr = io.StringIO()
        try:
            ops._check_call = lambda args, stdout=None: calls.append(args) or 0
            ops._check_backend = lambda env_dir, label, backend_config: None
            sys.stderr = stderr
            self.assertEqual(ops.cmd_plan({
                "tenant": "tenant",
                "selectors": ["zpa_segment_group"],
                "imports_only": False,
                "backend_config": None,
                "save": True,
            }), 0)
        finally:
            ops._check_call = old_check
            ops._check_backend = old_backend
            sys.stderr = old_stderr
        self.assertEqual(calls[0], [
            ops.terraform(), "-chdir=" + root, "init", "-input=false",
        ])
        self.assertEqual(calls[1], [
            ops.terraform(), "-chdir=" + root, "plan", "-input=false",
            "-var-file=" + os.path.abspath(config_paths[0]),
            "-var-file=" + os.path.abspath(config_paths[1]),
            "-out=tfplan",
        ])
        self.assertIn(
            "NOTE: selecting zpa_segment_group selects whole root zpa_custom; "
            "also operating on zpa_server_group\n",
            stderr.getvalue(),
        )

    def test_assert_clean_checks_grouped_root_plan_once(self):
        root = self._write_group_root(with_plan=True)
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            ops._show_plan_json = lambda env_dir: {
                "format_version": "1.0",
                "resource_changes": [],
            }
            sys.stderr = stderr
            self.assertEqual(ops.cmd_assert_clean({
                "tenant": "tenant",
                "selectors": ["zpa_segment_group"],
            }), 0)
        finally:
            ops._show_plan_json = old_show
            sys.stderr = old_stderr
        self.assertTrue(os.path.exists(os.path.join(root, "tfplan")))
        self.assertIn("all 1 saved plan(s) clean", stderr.getvalue())

    def test_assert_adoptable_collects_guidance_for_all_root_members(self):
        root = self._write_group_root(with_plan=True)
        old_show = ops._show_plan_json
        old_guidance = ops._guidance_annotations
        calls = []
        try:
            ops._show_plan_json = lambda env_dir: {
                "format_version": "1.0",
                "resource_changes": [{
                    "address": "module.zpa_segment_group.x.this[\"one\"]",
                    "type": "zpa_segment_group",
                    "change": {
                        "actions": ["update"],
                        "before": {"name": "old"},
                        "after": {"name": "new"},
                    },
                }],
            }
            ops._guidance_annotations = lambda plan, resource_type: (
                calls.append(resource_type) or []
            )
            with self.assertRaises(RuntimeError):
                ops.cmd_assert_adoptable({
                    "tenant": "tenant",
                    "selectors": [],
                    "policy": None,
                })
        finally:
            ops._show_plan_json = old_show
            ops._guidance_annotations = old_guidance
        self.assertTrue(os.path.exists(os.path.join(root, "tfplan")))
        self.assertEqual(calls, ["zpa_segment_group", "zpa_server_group"])


class OpsPlanFingerprintTest(unittest.TestCase):
    def setUp(self):
        self.cwd = os.getcwd()
        self.tmp = tempfile.mkdtemp(prefix="ops-plan-fingerprint-")
        os.chdir(self.tmp)
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")
        self.tenant = "tenant"
        self.members = ["zpa_segment_group", "zpa_server_group"]
        self._write_deployment(self.members)
        self._write_root_and_configs(self.members)

    def tearDown(self):
        os.chdir(self.cwd)
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_deployment(self, members, module_dir="modules"):
        dep = os.path.join(self.tmp, "deployment.json")
        _write_json(dep, {
            "module_dir": module_dir,
            "roots": {
                "zpa": {
                    "groups": {
                        "zpa_custom": members,
                    },
                },
            },
        })
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep

    def _root(self):
        return os.path.join("envs", self.tenant, "zpa_custom")

    def _write_root_and_configs(self, members):
        root = self._root()
        os.makedirs(root, exist_ok=True)
        with open(os.path.join(root, "main.tf"), "w", encoding="utf-8") as f:
            f.write("# root\n")
            for resource_type in members:
                source = os.path.relpath(
                    os.path.join("modules", resource_type), root)
                f.write(
                    '\nmodule "%s" {\n'
                    '  source = "%s"\n'
                    '  items = var.%s_items\n'
                    '}\n'
                    % (resource_type, source, resource_type)
                )
        os.makedirs(os.path.join("config", self.tenant), exist_ok=True)
        for resource_type in members:
            module_path = os.path.join("modules", resource_type)
            os.makedirs(module_path, exist_ok=True)
            with open(os.path.join(module_path, "main.tf"), "w",
                      encoding="utf-8") as f:
                f.write("# %s module\n" % resource_type)
            with open(
                os.path.join(
                    "config",
                    self.tenant,
                    resource_type + ".auto.tfvars.json",
                ),
                "w",
                encoding="utf-8",
            ) as f:
                json.dump({"%s_items" % resource_type: {}}, f)

    def _save_plan(self, backend_config=None, plan_hook=None):
        old_check = ops._check_call
        old_backend = ops._check_backend
        calls = []

        def fake_check(args, stdout=None):
            calls.append(args)
            if "-out=tfplan" in args:
                with open(os.path.join(self._root(), "tfplan"), "w",
                          encoding="utf-8") as f:
                    f.write("fake")
                if plan_hook:
                    plan_hook()
            return 0

        try:
            ops._check_call = fake_check
            ops._check_backend = lambda env_dir, label, backend_config: None
            self.assertEqual(ops.cmd_plan({
                "tenant": self.tenant,
                "selectors": ["zpa_segment_group"],
                "imports_only": False,
                "backend_config": backend_config,
                "save": True,
            }), 0)
        finally:
            ops._check_call = old_check
            ops._check_backend = old_backend
        return calls

    def _apply_saved_plan(self, backend_config=None, init_hook=None,
                          calls=None):
        old_check = ops._check_call
        old_backend = ops._check_backend
        old_show = ops._show_plan_json
        old_branch = ops._current_branch
        if calls is None:
            calls = []

        def fake_check(args, stdout=None):
            calls.append(args)
            if "init" in args and init_hook:
                init_hook()
            return 0

        try:
            ops._check_call = fake_check
            ops._check_backend = lambda env_dir, label, backend_config: None
            ops._show_plan_json = lambda env_dir: {
                "format_version": "1.0",
                "resource_changes": [],
            }
            ops._current_branch = lambda: "main"
            result = ops.cmd_apply({
                "tenant": self.tenant,
                "selectors": ["zpa_segment_group"],
                "backend_config": backend_config,
                "policy": None,
                "allow_destroy": False,
                "allow_non_main": False,
                "allow_plan_changes": False,
                "main_branch": "main",
            })
            return result, calls
        finally:
            ops._check_call = old_check
            ops._check_backend = old_backend
            ops._show_plan_json = old_show
            ops._current_branch = old_branch

    def test_plan_save_writes_fingerprint_and_apply_proceeds(self):
        self._save_plan()
        source_path = os.path.join(self._root(), ops.PLAN_FINGERPRINT)
        self.assertTrue(os.path.exists(source_path))
        with open(source_path, encoding="utf-8") as f:
            data = json.load(f)
        self.assertEqual(data["version"], 2)
        self.assertEqual(len(data["sha256"]), 64)

        result, calls = self._apply_saved_plan()

        self.assertEqual(result, 0)
        self.assertTrue(any("apply" in call for call in calls))
        self.assertFalse(os.path.exists(os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(source_path))

    def test_membership_change_makes_saved_plan_stale(self):
        self._save_plan()
        self._write_deployment(["zpa_segment_group"])

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan()

        self.assertEqual(
            str(ctx.exception),
            ops.STALE_PLAN_MESSAGE % self._root(),
        )

    def test_main_tf_edit_makes_saved_plan_stale(self):
        self._save_plan()
        with open(os.path.join(self._root(), "main.tf"), "a",
                  encoding="utf-8") as f:
            f.write("# changed\n")

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan()

        self.assertEqual(
            str(ctx.exception),
            ops.STALE_PLAN_MESSAGE % self._root(),
        )

    def test_dependency_lock_edit_makes_saved_plan_stale(self):
        lock_path = os.path.join(self._root(), ".terraform.lock.hcl")
        with open(lock_path, "w", encoding="utf-8") as f:
            f.write("# provider selections\n")
        self._save_plan()
        with open(lock_path, "a", encoding="utf-8") as f:
            f.write("# changed\n")

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan()

        self.assertEqual(
            str(ctx.exception),
            ops.STALE_PLAN_MESSAGE % self._root(),
        )

    def test_root_auto_tfvars_edit_makes_saved_plan_stale(self):
        tfvars_path = os.path.join(self._root(), "local.auto.tfvars")
        with open(tfvars_path, "w", encoding="utf-8") as f:
            f.write('value = "before"\n')
        self._save_plan()
        with open(tfvars_path, "w", encoding="utf-8") as f:
            f.write('value = "after"\n')

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan()

        self.assertEqual(
            str(ctx.exception),
            ops.STALE_PLAN_MESSAGE % self._root(),
        )

    def test_module_source_edit_makes_saved_plan_stale(self):
        self._save_plan()
        module_path = os.path.join(
            "modules", "zpa_segment_group", "main.tf")
        with open(module_path, "a", encoding="utf-8") as f:
            f.write("# changed\n")

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan()

        self.assertEqual(
            str(ctx.exception),
            ops.STALE_PLAN_MESSAGE % self._root(),
        )

    def test_missing_member_module_source_fails_loudly(self):
        main_path = os.path.join(self._root(), "main.tf")
        with open(main_path, encoding="utf-8") as f:
            text = f.read()
        text = text.replace(
            '  source = "../../../modules/zpa_server_group"\n', "")
        with open(main_path, "w", encoding="utf-8") as f:
            f.write(text)

        with self.assertRaisesRegex(
                RuntimeError,
                "module zpa_server_group is outside the generated-root "
                "contract"):
            self._save_plan()

        self.assertFalse(os.path.exists(
            os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), ops.PLAN_FINGERPRINT)))

    def test_nonlocal_member_module_source_fails_loudly(self):
        main_path = os.path.join(self._root(), "main.tf")
        with open(main_path, encoding="utf-8") as f:
            text = f.read()
        text = text.replace(
            "../../../modules/zpa_server_group",
            "example/zpa_server_group/provider",
        )
        with open(main_path, "w", encoding="utf-8") as f:
            f.write(text)

        with self.assertRaisesRegex(
                RuntimeError, "zpa_server_group module source .* is not local"):
            self._save_plan()

        self.assertFalse(os.path.exists(
            os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), ops.PLAN_FINGERPRINT)))

    def test_commented_local_source_cannot_shadow_nonlocal_source(self):
        main_path = os.path.join(self._root(), "main.tf")
        with open(main_path, encoding="utf-8") as f:
            text = f.read()
        text = text.replace(
            '  source = "../../../modules/zpa_server_group"\n',
            '  source = "example/zpa_server_group/provider"\n'
            '  /* ignored text must not replace the effective source:\n'
            '  source = "../../../modules/zpa_server_group"\n'
            '  }\n'
            '  */\n',
        )
        with open(main_path, "w", encoding="utf-8") as f:
            f.write(text)

        with self.assertRaisesRegex(
                RuntimeError, "zpa_server_group module source .* is not local"):
            self._save_plan()

        self.assertFalse(os.path.exists(
            os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), ops.PLAN_FINGERPRINT)))

    def test_module_source_template_escape_fails_loudly(self):
        decoded_path = os.path.join("modules", "${zpa_server_group}")
        os.makedirs(decoded_path, exist_ok=True)
        with open(os.path.join(decoded_path, "main.tf"), "w",
                  encoding="utf-8") as f:
            f.write("# Terraform's decoded source tree\n")

        main_path = os.path.join(self._root(), "main.tf")
        with open(main_path, encoding="utf-8") as f:
            text = f.read()
        text = text.replace(
            "../../../modules/zpa_server_group",
            "../../../modules/$${zpa_server_group}",
        )
        with open(main_path, "w", encoding="utf-8") as f:
            f.write(text)

        with self.assertRaisesRegex(
                RuntimeError, "source uses HCL template syntax"):
            self._save_plan()

        self.assertFalse(os.path.exists(
            os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), ops.PLAN_FINGERPRINT)))

    def test_effective_root_module_source_survives_deployment_change(self):
        self._save_plan()
        shutil.copytree("modules", "modules_b")
        self._write_deployment(self.members, module_dir="modules_b")
        with open(os.path.join(
                "modules", "zpa_segment_group", "main.tf"), "a",
                encoding="utf-8") as f:
            f.write("# changed in effective source\n")

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan()

        self.assertEqual(
            str(ctx.exception),
            ops.STALE_PLAN_MESSAGE % self._root(),
        )

    def test_plan_input_change_during_plan_discards_saved_artifacts(self):
        module_path = os.path.join(
            "modules", "zpa_segment_group", "main.tf")

        def change_module():
            with open(module_path, "a", encoding="utf-8") as f:
                f.write("# changed during plan\n")

        with self.assertRaisesRegex(
                RuntimeError, "plan inputs changed while the plan was running"):
            self._save_plan(plan_hook=change_module)

        self.assertFalse(os.path.exists(
            os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), ops.PLAN_FINGERPRINT)))

    def test_failed_replan_discards_previous_saved_artifacts(self):
        self._save_plan()
        old_check = ops._check_call
        old_backend = ops._check_backend

        def fake_check(args, stdout=None):
            if "init" in args:
                return 0
            raise RuntimeError("plan failed")

        try:
            ops._check_call = fake_check
            ops._check_backend = lambda env_dir, label, backend_config: None
            with self.assertRaisesRegex(RuntimeError, "plan failed"):
                ops.cmd_plan({
                    "tenant": self.tenant,
                    "selectors": ["zpa_segment_group"],
                    "imports_only": False,
                    "backend_config": None,
                    "save": True,
                })
        finally:
            ops._check_call = old_check
            ops._check_backend = old_backend

        self.assertFalse(os.path.exists(
            os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), ops.PLAN_FINGERPRINT)))

    def test_failed_init_discards_previous_saved_artifacts(self):
        self._save_plan()
        old_check = ops._check_call
        old_backend = ops._check_backend

        def fake_check(args, stdout=None):
            raise RuntimeError("init failed")

        try:
            ops._check_call = fake_check
            ops._check_backend = lambda env_dir, label, backend_config: None
            with self.assertRaisesRegex(RuntimeError, "init failed"):
                ops.cmd_plan({
                    "tenant": self.tenant,
                    "selectors": ["zpa_segment_group"],
                    "imports_only": False,
                    "backend_config": None,
                    "save": True,
                })
        finally:
            ops._check_call = old_check
            ops._check_backend = old_backend

        self.assertFalse(os.path.exists(
            os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), ops.PLAN_FINGERPRINT)))

    def test_backend_change_during_init_discards_saved_artifacts(self):
        backend_config = os.path.join(self.tmp, "backend.hcl")
        with open(backend_config, "w", encoding="utf-8") as f:
            f.write('bucket = "before"\n')
        self._save_plan(backend_config=backend_config)
        old_check = ops._check_call
        old_backend = ops._check_backend
        calls = []

        def fake_check(args, stdout=None):
            calls.append(args)
            if "init" in args:
                with open(backend_config, "w", encoding="utf-8") as f:
                    f.write('bucket = "after"\n')
            return 0

        try:
            ops._check_call = fake_check
            ops._check_backend = lambda env_dir, label, backend_config: None
            with self.assertRaisesRegex(
                    RuntimeError, "init inputs changed while init was running"):
                ops.cmd_plan({
                    "tenant": self.tenant,
                    "selectors": ["zpa_segment_group"],
                    "imports_only": False,
                    "backend_config": backend_config,
                    "save": True,
                })
        finally:
            ops._check_call = old_check
            ops._check_backend = old_backend

        self.assertFalse(any("plan" in call for call in calls))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), ops.PLAN_FINGERPRINT)))

    def test_backend_config_edit_makes_saved_plan_stale(self):
        backend_config = os.path.join(self.tmp, "backend.hcl")
        with open(backend_config, "w", encoding="utf-8") as f:
            f.write('bucket = "before"\n')
        self._save_plan(backend_config=backend_config)
        with open(backend_config, "w", encoding="utf-8") as f:
            f.write('bucket = "after"\n')

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan(backend_config=backend_config)

        self.assertEqual(
            str(ctx.exception),
            ops.STALE_PLAN_MESSAGE % self._root(),
        )

    def test_unchanged_backend_config_allows_saved_plan_apply(self):
        backend_config = os.path.join(self.tmp, "backend.hcl")
        with open(backend_config, "w", encoding="utf-8") as f:
            f.write('bucket = "same"\n')
        self._save_plan(backend_config=backend_config)

        result, calls = self._apply_saved_plan(
            backend_config=backend_config)

        self.assertEqual(result, 0)
        self.assertTrue(any("apply" in call for call in calls))

    def test_backend_config_must_be_reused_for_saved_plan(self):
        backend_config = os.path.join(self.tmp, "backend.hcl")
        with open(backend_config, "w", encoding="utf-8") as f:
            f.write('bucket = "same"\n')
        self._save_plan(backend_config=backend_config)

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan()

        self.assertEqual(
            str(ctx.exception),
            ops.STALE_PLAN_MESSAGE % self._root(),
        )

    def test_apply_rechecks_fingerprint_after_init(self):
        self._save_plan()
        calls = []

        def create_lock_file():
            with open(os.path.join(self._root(), ".terraform.lock.hcl"), "w",
                      encoding="utf-8") as f:
                f.write("# changed by init\n")

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan(init_hook=create_lock_file, calls=calls)

        self.assertEqual(
            str(ctx.exception),
            ops.STALE_PLAN_MESSAGE % self._root(),
        )
        self.assertFalse(any("apply" in call for call in calls))

    def test_missing_fingerprint_makes_saved_plan_stale_with_migration_note(self):
        self._save_plan()
        os.remove(os.path.join(self._root(), ops.PLAN_FINGERPRINT))

        with self.assertRaises(RuntimeError) as ctx:
            self._apply_saved_plan()

        self.assertEqual(
            str(ctx.exception),
            "%s (%s)" % (
                ops.STALE_PLAN_MESSAGE % self._root(),
                ops.MISSING_PLAN_FINGERPRINT_DETAIL,
            ),
        )

    def test_clean_plans_removes_plan_and_fingerprint(self):
        self._save_plan()

        self.assertEqual(ops.cmd_clean_plans({
            "tenant": self.tenant,
            "selectors": ["zpa_segment_group"],
        }), 0)

        self.assertFalse(os.path.exists(os.path.join(self._root(), "tfplan")))
        self.assertFalse(os.path.exists(
            os.path.join(self._root(), ops.PLAN_FINGERPRINT)
        ))


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
        _write_fresh_plan(tmp)
        old_roots = ops.selected_env_roots
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
            ops.selected_env_roots = lambda tenant, selectors, require_plan=False: [
                _root_tuple(tmp)
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
            ops.selected_env_roots = old_roots
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
        old_roots = ops.selected_env_roots
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            _write_fresh_plan(tmp)
            ops.selected_env_roots = lambda tenant, selectors, require_plan=False: [
                _root_tuple(tmp)
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
            ops.selected_env_roots = old_roots
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
        old_roots = ops.selected_env_roots
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            _write_fresh_plan(tmp)
            ops.selected_env_roots = lambda tenant, selectors, require_plan=False: [
                _root_tuple(tmp)
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
            ops.selected_env_roots = old_roots
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
        old_roots = ops.selected_env_roots
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            _write_fresh_plan(tmp)
            ops.selected_env_roots = lambda tenant, selectors, require_plan=False: [
                _root_tuple(tmp)
            ]
            ops._show_plan_json = lambda env_dir: plan_data
            sys.stderr = stderr
            return tmp, old_packs, old_roots, old_show, old_stderr, stderr
        except Exception:
            shutil.rmtree(tmp, ignore_errors=True)
            raise

    def _teardown(self, tmp, old_packs, old_roots, old_show, old_stderr):
        if old_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = old_packs
        packs.reset()
        ops.selected_env_roots = old_roots
        ops._show_plan_json = old_show
        sys.stderr = old_stderr
        shutil.rmtree(tmp, ignore_errors=True)

    def _run_blocked(self, pack_data, plan_data):
        tmp, old_packs, old_roots, old_show, old_stderr, stderr = self._setup_test(
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
            self._teardown(tmp, old_packs, old_roots, old_show, old_stderr)

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
        tmp, old_packs, old_roots, old_show, old_stderr, stderr = self._setup_test(
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
            self._teardown(tmp, old_packs, old_roots, old_show, old_stderr)

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
        old_roots = ops.selected_env_roots
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            _write_fresh_plan(tmp)
            ops.selected_env_roots = lambda tenant, selectors, require_plan=False: [
                _root_tuple(tmp)
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
            ops.selected_env_roots = old_roots
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
        old_roots = ops.selected_env_roots
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            _write_fresh_plan(tmp)
            ops.selected_env_roots = lambda tenant, selectors, require_plan=False: [
                _root_tuple(tmp)
            ]
            ops._show_plan_json = lambda env_dir: plan_data
            sys.stderr = stderr
            return tmp, old_packs, old_roots, old_show, old_stderr, stderr
        except Exception:
            shutil.rmtree(tmp, ignore_errors=True)
            raise

    def _teardown(self, tmp, old_packs, old_roots, old_show, old_stderr):
        if old_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = old_packs
        packs.reset()
        ops.selected_env_roots = old_roots
        ops._show_plan_json = old_show
        sys.stderr = old_stderr
        shutil.rmtree(tmp, ignore_errors=True)

    def _run_blocked(self, pack_data, plan_data):
        tmp, old_packs, old_roots, old_show, old_stderr, stderr = self._setup_test(
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
            self._teardown(tmp, old_packs, old_roots, old_show, old_stderr)

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
        old_roots = ops.selected_env_roots
        old_show = ops._show_plan_json
        old_guidance = ops._guidance_annotations
        old_stderr = sys.stderr
        stderr = io.StringIO()
        calls = []
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            _write_fresh_plan(tmp)
            ops.selected_env_roots = lambda tenant, selectors, require_plan=False: [
                _root_tuple(tmp)
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
            ops.selected_env_roots = old_roots
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
        old_roots = ops.selected_env_roots
        old_show = ops._show_plan_json
        old_stderr = sys.stderr
        stderr = io.StringIO()
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            _write_fresh_plan(tmp)
            ops.selected_env_roots = lambda tenant, selectors, require_plan=False: [
                _root_tuple(tmp)
            ]
            ops._show_plan_json = lambda env_dir: plan_data
            sys.stderr = stderr
            return tmp, old_packs, old_roots, old_show, old_stderr, stderr
        except Exception:
            shutil.rmtree(tmp, ignore_errors=True)
            raise

    def _teardown(self, tmp, old_packs, old_roots, old_show, old_stderr):
        if old_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = old_packs
        packs.reset()
        ops.selected_env_roots = old_roots
        ops._show_plan_json = old_show
        sys.stderr = old_stderr
        shutil.rmtree(tmp, ignore_errors=True)

    def _run_blocked(self, pack_data, plan_data):
        tmp, old_packs, old_roots, old_show, old_stderr, stderr = self._setup_test(
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
            self._teardown(tmp, old_packs, old_roots, old_show, old_stderr)

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
        old_roots = ops.selected_env_roots
        old_show = ops._show_plan_json
        old_guidance = ops._guidance_annotations
        old_stderr = sys.stderr
        stderr = io.StringIO()
        calls = []
        try:
            os.environ["INFRAWRIGHT_PACKS"] = pack_root
            packs.reset()
            _write_fresh_plan(tmp)
            ops.selected_env_roots = lambda tenant, selectors, require_plan=False: [
                _root_tuple(tmp)
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
            ops.selected_env_roots = old_roots
            ops._show_plan_json = old_show
            ops._guidance_annotations = old_guidance
            sys.stderr = old_stderr
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    unittest.main()
