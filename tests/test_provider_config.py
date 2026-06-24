import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest

from engine import packs
from engine import provider_config


def _update(resource_type, before, after):
    return {
        "address": "module.%s.%s.this[\"item\"]" % (resource_type, resource_type),
        "type": resource_type,
        "change": {"actions": ["update"], "before": before, "after": after},
    }


_DELETE = object()


class ProviderConfigDiagnosticsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="provider-config-")
        self.prev_packs = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        self._write_pack()
        packs.reset()

    def tearDown(self):
        if self.prev_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev_packs
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_json(self, path, data):
        full = os.path.join(self.tmp, path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w", encoding="utf-8") as f:
            json.dump(data, f)
        return full

    def _write_pack(self, requirements=None):
        if requirements is None:
            requirements = [{
                "id": "sample_disable_attribution_label",
                "setting": "add_sample_attribution_label",
                "value": False,
                "reason": "Provider adds a management label by default.",
                "resource_types": ["sample_topic"],
                "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
            }]
        self._write_json("sample/pack.json", {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "provider_config": {"requirements": requirements},
        })
        packs.reset()

    def _run(self, argv):
        stdout = io.StringIO()
        stderr = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            with contextlib.redirect_stderr(stderr):
                code = provider_config.main(argv)
        return code, stdout.getvalue(), stderr.getvalue()

    def _base_requirement(self, **overrides):
        req = {
            "id": "google_disable_attribution_label",
            "provider": "google",
            "setting": "add_terraform_attribution_label",
            "value": False,
            "reason": "Google provider adds attribution labels by default.",
            "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
        }
        for key, value in overrides.items():
            if value is _DELETE:
                req.pop(key, None)
            else:
                req[key] = value
        return req

    def _renderable_remediation(self, **overrides):
        remediation = {
            "kind": "provider_argument",
            "mode": "renderable_default",
            "evidence": "docs/provider-labs/gcp-pr38.md",
            "safety": {
                "non_sensitive": True,
                "not_tenant_specific": True,
                "not_destructive": True,
            },
        }
        for key, value in overrides.items():
            if value is _DELETE:
                remediation.pop(key, None)
            else:
                remediation[key] = value
        return remediation

    def _assert_invalid_requirement(self, req, text):
        with self.assertRaises(ValueError) as ctx:
            provider_config.validate_requirements([req])
        err = str(ctx.exception)
        self.assertIn("provider_config requirement 0", err)
        self.assertIn(text, err)

    def test_pack_requirement_matches_plan_drift(self):
        plan = {
            "resource_changes": [
                _update(
                    "sample_topic",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                )
            ]
        }

        report = provider_config.build_report(provider="sample", plan=plan)

        self.assertEqual(report["summary"]["provider_config_matches"], 1)
        self.assertEqual(report["summary"]["unmatched_plan_changes"], 0)
        change = report["plan_changes"][0]
        self.assertEqual(change["status"], "provider_config_requirement")
        self.assertEqual(change["setting"], "add_sample_attribution_label")
        self.assertEqual(change["value"], False)

    def test_same_path_without_metadata_is_not_inferred(self):
        self._write_pack(requirements=[])
        plan = {
            "resource_changes": [
                _update(
                    "sample_topic",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                )
            ]
        }

        report = provider_config.build_report(provider="sample", plan=plan)

        self.assertEqual(report["summary"]["provider_config_matches"], 0)
        self.assertEqual(report["summary"]["unmatched_plan_changes"], 1)
        self.assertEqual(
            report["plan_changes"][0]["status"],
            "unmatched_plan_change",
        )

    def test_requirement_resource_scope_is_respected(self):
        plan = {
            "resource_changes": [
                _update(
                    "sample_other",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                )
            ]
        }

        report = provider_config.build_report(provider="sample", plan=plan)

        self.assertEqual(report["summary"]["provider_config_matches"], 0)
        self.assertEqual(report["summary"]["unmatched_plan_changes"], 1)

    def test_resource_type_filter_limits_plan_changes(self):
        plan = {
            "resource_changes": [
                _update(
                    "sample_topic",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                ),
                _update("sample_topic", {"name": "old"}, {"name": "new"}),
                _update("sample_other", {"name": "old"}, {"name": "new"}),
            ]
        }

        report = provider_config.build_report(
            resource_type="sample_topic",
            plan=plan,
        )

        self.assertEqual(report["provider"], "sample")
        self.assertEqual(report["summary"]["plan_changes"], 2)
        self.assertEqual(report["summary"]["provider_config_matches"], 1)
        self.assertEqual(report["summary"]["unmatched_plan_changes"], 1)

    def test_unchanged_sensitive_marker_is_not_plan_change(self):
        plan = {
            "resource_changes": [
                {
                    "address": "module.sample_topic.sample_topic.this[\"item\"]",
                    "type": "sample_topic",
                    "change": {
                        "actions": ["update"],
                        "before": {"name": "same", "secret": "redacted"},
                        "after": {"name": "same", "secret": "redacted"},
                        "before_sensitive": {"secret": True},
                        "after_sensitive": {"secret": True},
                    },
                }
            ]
        }

        report = provider_config.build_report(provider="sample", plan=plan)

        self.assertEqual(report["summary"]["plan_changes"], 0)
        self.assertEqual(report["plan_changes"], [])

    def test_requirement_without_remediation_remains_valid(self):
        reqs = provider_config.validate_requirements([
            self._base_requirement(),
        ])

        self.assertEqual(len(reqs), 1)
        self.assertEqual(reqs[0]["setting"], "add_terraform_attribution_label")

    def test_valid_renderable_default_remediation_is_accepted(self):
        reqs = provider_config.validate_requirements([
            self._base_requirement(
                remediation=self._renderable_remediation(),
            ),
        ])

        self.assertEqual(len(reqs), 1)

    def test_valid_required_external_remediation_accepts_advisory_string_value(self):
        reqs = provider_config.validate_requirements([
            self._base_requirement(
                value="use-consumer-provider-config",
                remediation={
                    "kind": "provider_argument",
                    "mode": "required_external",
                },
            ),
        ])

        self.assertEqual(reqs[0]["value"], "use-consumer-provider-config")

    def test_valid_required_external_remediation_accepts_missing_value(self):
        reqs = provider_config.validate_requirements([
            self._base_requirement(
                value=_DELETE,
                remediation={
                    "kind": "provider_argument",
                    "mode": "required_external",
                },
            ),
        ])

        self.assertIsNone(reqs[0]["value"])

    def test_valid_diagnostic_only_remediation_is_accepted(self):
        reqs = provider_config.validate_requirements([
            self._base_requirement(
                value="diagnostic context only",
                remediation={
                    "kind": "provider_argument",
                    "mode": "diagnostic_only",
                },
            ),
        ])

        self.assertEqual(reqs[0]["value"], "diagnostic context only")

    def test_remediation_metadata_validation_failures(self):
        bad_safety = dict(self._renderable_remediation()["safety"])
        bad_safety.pop("non_sensitive")
        cases = [
            (
                "remediation_not_object",
                self._base_requirement(remediation=True),
                "remediation must be an object",
            ),
            (
                "missing_kind",
                self._base_requirement(
                    remediation=self._renderable_remediation(kind=_DELETE),
                ),
                "remediation.kind is required",
            ),
            (
                "unknown_kind",
                self._base_requirement(
                    remediation=self._renderable_remediation(kind="other"),
                ),
                "unknown remediation kind other",
            ),
            (
                "missing_mode",
                self._base_requirement(
                    remediation=self._renderable_remediation(mode=_DELETE),
                ),
                "remediation.mode is required",
            ),
            (
                "unknown_mode",
                self._base_requirement(
                    remediation=self._renderable_remediation(mode="auto"),
                ),
                "unknown remediation mode auto",
            ),
            (
                "unknown_remediation_key",
                self._base_requirement(
                    remediation=self._renderable_remediation(extra=True),
                ),
                "unknown remediation key extra",
            ),
            (
                "missing_evidence",
                self._base_requirement(
                    remediation=self._renderable_remediation(evidence=_DELETE),
                ),
                "remediation.evidence is required",
            ),
            (
                "missing_safety_object",
                self._base_requirement(
                    remediation=self._renderable_remediation(safety=_DELETE),
                ),
                "remediation.safety must be an object",
            ),
            (
                "missing_safety_boolean",
                self._base_requirement(
                    remediation=self._renderable_remediation(safety=bad_safety),
                ),
                "remediation.safety.non_sensitive is required",
            ),
            (
                "safety_false",
                self._base_requirement(
                    remediation=self._renderable_remediation(safety={
                        "non_sensitive": False,
                        "not_tenant_specific": True,
                        "not_destructive": True,
                    }),
                ),
                "remediation.safety.non_sensitive must be true",
            ),
            (
                "safety_non_boolean",
                self._base_requirement(
                    remediation=self._renderable_remediation(safety={
                        "non_sensitive": "true",
                        "not_tenant_specific": True,
                        "not_destructive": True,
                    }),
                ),
                "remediation.safety.non_sensitive must be boolean true",
            ),
            (
                "missing_diagnostic_value",
                self._base_requirement(value=_DELETE),
                "missing value",
            ),
            (
                "string_renderable_value",
                self._base_requirement(
                    value="false",
                    remediation=self._renderable_remediation(),
                ),
                "renderable_default value must be a JSON boolean or number",
            ),
            (
                "null_renderable_value",
                self._base_requirement(
                    value=None,
                    remediation=self._renderable_remediation(),
                ),
                "renderable_default value must be a JSON boolean or number",
            ),
            (
                "array_renderable_value",
                self._base_requirement(
                    value=[False],
                    remediation=self._renderable_remediation(),
                ),
                "renderable_default value must be a JSON boolean or number",
            ),
            (
                "object_renderable_value",
                self._base_requirement(
                    value={"enabled": False},
                    remediation=self._renderable_remediation(),
                ),
                "renderable_default value must be a JSON boolean or number",
            ),
            (
                "renderable_with_resource_types",
                self._base_requirement(
                    resource_types=["google_pubsub_topic"],
                    remediation=self._renderable_remediation(),
                ),
                "renderable_default must not include resource_types",
            ),
            (
                "renderable_with_resource_prefixes",
                self._base_requirement(
                    resource_prefixes=["google_"],
                    remediation=self._renderable_remediation(),
                ),
                "renderable_default must not include resource_prefixes",
            ),
            (
                "missing_plan_paths",
                self._base_requirement(
                    plan_paths=_DELETE,
                    remediation=self._renderable_remediation(),
                ),
                "plan_paths must be a non-empty list",
            ),
            (
                "empty_plan_paths",
                self._base_requirement(
                    plan_paths=[],
                    remediation=self._renderable_remediation(),
                ),
                "plan_paths must be a non-empty list",
            ),
            (
                "missing_reason",
                self._base_requirement(
                    reason=_DELETE,
                    remediation=self._renderable_remediation(),
                ),
                "missing reason",
            ),
            (
                "empty_reason",
                self._base_requirement(
                    reason="",
                    remediation=self._renderable_remediation(),
                ),
                "missing reason",
            ),
        ]

        for name, req, text in cases:
            with self.subTest(name=name):
                self._assert_invalid_requirement(req, text)

    def test_each_renderable_safety_boolean_is_required(self):
        for key in (
                "non_sensitive",
                "not_tenant_specific",
                "not_destructive"):
            with self.subTest(key=key):
                safety = dict(self._renderable_remediation()["safety"])
                safety.pop(key)
                self._assert_invalid_requirement(
                    self._base_requirement(
                        remediation=self._renderable_remediation(safety=safety),
                    ),
                    "remediation.safety.%s is required" % key,
                )

    def test_duplicate_provider_setting_requirements_are_rejected(self):
        first = self._base_requirement()
        second = self._base_requirement(id="google_disable_attribution_label_2")

        with self.assertRaises(ValueError) as ctx:
            provider_config.validate_requirements([first, second])

        err = str(ctx.exception)
        self.assertIn("provider_config requirement 1", err)
        self.assertIn("google.add_terraform_attribution_label", err)
        self.assertIn("duplicate metadata", err)

    def test_duplicate_provider_setting_conflicting_value_is_rejected(self):
        first = self._base_requirement()
        second = self._base_requirement(
            id="google_disable_attribution_label_2",
            value=True,
        )

        with self.assertRaises(ValueError) as ctx:
            provider_config.validate_requirements([first, second])

        self.assertIn("conflicting values", str(ctx.exception))

    def test_duplicate_provider_setting_conflicting_mode_is_rejected(self):
        first = self._base_requirement()
        second = self._base_requirement(
            id="google_disable_attribution_label_2",
            remediation=self._renderable_remediation(),
        )

        with self.assertRaises(ValueError) as ctx:
            provider_config.validate_requirements([first, second])

        self.assertIn("conflicting remediation modes", str(ctx.exception))

    def test_bad_metadata_fails_loudly(self):
        self._write_pack(requirements=[{
            "id": "sample_bad",
            "setting": "missing_plan_paths",
            "value": False,
            "reason": "bad fixture",
        }])

        with self.assertRaises(ValueError) as ctx:
            provider_config.build_report(provider="sample", plan={})

        self.assertIn(
            "sample_bad plan_paths must be a non-empty list",
            str(ctx.exception),
        )

    def test_cli_reads_pack_metadata_and_plan(self):
        plan_path = self._write_json("plan.json", {
            "resource_changes": [
                _update(
                    "sample_topic",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                )
            ]
        })

        code, out, err = self._run([
            "--provider", "sample",
            "--plan", plan_path,
        ])

        self.assertEqual(code, 0, err)
        report = json.loads(out)
        self.assertEqual(report["summary"]["provider_config_matches"], 1)
        self.assertEqual(
            report["requirements"][0]["setting"],
            "add_sample_attribution_label",
        )


if __name__ == "__main__":
    unittest.main()
