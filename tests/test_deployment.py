import json
import os
import unittest

from engine import deployment


class DeploymentResolverTest(unittest.TestCase):
    def setUp(self):
        self._cwd = os.getcwd()
        self._tmp = self.id().replace(".", "_") + ".tmpdir"
        os.makedirs(self._tmp, exist_ok=True)
        os.chdir(self._tmp)
        # This suite exercises REAL deployment.json reading, so opt out of the
        # hermetic INFRAWRIGHT_DEPLOYMENT pin set in tests/__init__.py and let
        # _load() resolve deployment.json from this tmp cwd.
        self._saved_dep = os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)

    def tearDown(self):
        os.chdir(self._cwd)
        if self._saved_dep is not None:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self._saved_dep
        import shutil
        shutil.rmtree(os.path.join(self._cwd, self._tmp), ignore_errors=True)

    def _write(self, obj_or_text):
        with open("deployment.json", "w", encoding="utf-8") as f:
            f.write(obj_or_text if isinstance(obj_or_text, str) else json.dumps(obj_or_text))

    def test_absent_resolves_root(self):
        self.assertEqual(deployment.overlay(), ".")
        self.assertEqual(deployment.tenant_root("anything"), ".")
        self.assertEqual(deployment.config_dir("acme"), os.path.join("config", "acme"))

    def test_empty_file_resolves_root(self):
        self._write("   \n")
        self.assertEqual(deployment.overlay(), ".")

    def test_dot_overlay_resolves_root(self):
        self._write({"overlay": "."})
        self.assertEqual(deployment.tenant_root("acme"), ".")

    def test_overlay_set_real_tenant_under_overlay(self):
        self._write({"overlay": "_local"})
        self.assertEqual(deployment.overlay(), "_local")
        self.assertEqual(deployment.tenant_root("acme"), "_local")
        self.assertEqual(deployment.config_dir("acme"), os.path.join("_local", "config", "acme"))
        self.assertEqual(deployment.imports_dir("acme"), os.path.join("_local", "imports", "acme"))
        self.assertEqual(deployment.envs_dir("acme"), os.path.join("_local", "envs", "acme"))

    def test_demo_always_root_even_with_overlay(self):
        self._write({"overlay": "_local"})
        self.assertEqual(deployment.tenant_root("demo"), ".")
        self.assertEqual(deployment.config_dir("demo"), os.path.join("config", "demo"))

    def test_pulls_always_root(self):
        self._write({"overlay": "_local"})
        self.assertEqual(deployment.pulls_dir("acme"), os.path.join("pulls", "acme"))

    def test_comment_keys_ignored(self):
        self._write({"$note": "hi", "overlay": "_local"})
        self.assertEqual(deployment.overlay(), "_local")

    def test_malformed_raises(self):
        self._write("{ not json")
        with self.assertRaises(ValueError):
            deployment.overlay()

    def test_prefix_is_repo_relative_string(self):
        self._write({"overlay": "_local"})
        self.assertEqual(deployment.config_prefix("acme"), os.path.join("_local", "config", "acme"))

    def test_env_override_beats_cwd_file(self):
        # The INFRAWRIGHT_DEPLOYMENT override must WIN over a deployment.json
        # present in the cwd — the invariant the whole fix exists for, and the
        # only test that proves env-beats-cwd (not just empty-file => root). A
        # revert of _deployment_path() must fail HERE, not pass silently.
        self._write({"overlay": "_cwd"})  # a conflicting cwd deployment.json
        with open("env_deploy.json", "w", encoding="utf-8") as f:
            f.write(json.dumps({"overlay": "_env"}))
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = os.path.abspath("env_deploy.json")
        self.assertEqual(deployment.overlay(), "_env")

    # --- single output layout ---------------------------------------------
    def test_provider_argument_does_not_create_path_segment(self):
        self.assertEqual(
            deployment.config_dir("demo", "zia"),
            os.path.join("config", "demo"),
        )
        self.assertEqual(
            deployment.imports_dir("demo", "zia"),
            os.path.join("imports", "demo"),
        )

    def test_provider_argument_ignored_for_envs(self):
        self.assertEqual(
            deployment.envs_dir("demo", "zpa"),
            os.path.join("envs", "demo"),
        )

    def test_no_provider_returns_tenant_level_dir(self):
        self.assertEqual(deployment.config_dir("demo"), os.path.join("config", "demo"))

    def test_overlay_prefixes_flat_layout_for_real_tenant(self):
        self._write({"overlay": "acme"})
        self.assertEqual(
            deployment.config_dir("zs3", "zia"),
            os.path.join("acme", "config", "zs3"),
        )


if __name__ == "__main__":
    unittest.main()
