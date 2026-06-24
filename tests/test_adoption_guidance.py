import io
import unittest

from engine import adoption_guidance


class AdoptionGuidanceTest(unittest.TestCase):
    def _provider_config_annotation(self, **overrides):
        values = {
            "source": "resource_changes",
            "address": "sample_resource.this",
            "matched_plan_path": "terraform_labels.goog-terraform-provisioned",
            "provider": "sample",
            "resource_type": "sample_resource",
            "setting": "add_sample_attribution_label",
            "expected_value": False,
            "mode": "required_external",
            "reason": "Sample provider adds attribution labels.",
            "evidence": "docs/provider-labs/sample.md",
        }
        values.update(overrides)
        return adoption_guidance.provider_config_annotation(**values)

    def _absent_default_annotation(self, **overrides):
        values = {
            "source": "resource_changes",
            "address": "sample_resource.this",
            "matched_plan_path": "name_prefix",
            "provider": "sample",
            "resource_type": "sample_resource",
            "rule": "sample_empty_name_prefix",
            "kind": "provider_absent_placeholder",
            "action": "manual_review_required",
            "observed_value": "",
            "reason": (
                "Sample provider imported empty name_prefix alongside concrete "
                "name; manual review required."
            ),
            "evidence": "docs/provider-labs/sample.md",
        }
        values.update(overrides)
        return adoption_guidance.absent_default_annotation(**values)

    def test_safe_collect_guidance_returns_empty_on_exception(self):
        def boom():
            raise RuntimeError("hidden failure")

        self.assertEqual(adoption_guidance.safe_collect_guidance(boom), [])

    def test_sort_annotations_is_deterministic(self):
        annotations = [
            adoption_guidance.absent_default_annotation(
                source="resource_changes",
                address="sample_resource.this",
                matched_plan_path="z_path",
                provider="sample",
                resource_type="sample_resource",
                rule="z_rule",
                kind="provider_absent_placeholder",
                action="manual_review_required",
                observed_value="",
                reason="z",
                evidence="docs/z.md",
            ),
            adoption_guidance.provider_config_annotation(
                source="resource_changes",
                address="sample_resource.this",
                matched_plan_path="a_path",
                provider="sample",
                resource_type="sample_resource",
                setting="a_setting",
                expected_value=False,
                mode="required_external",
                reason="a",
                evidence="docs/a.md",
            ),
        ]

        ordered = adoption_guidance.sort_annotations(annotations)
        self.assertEqual(
            [a["lane"] for a in ordered],
            ["provider_config", "absent_default"],
        )

    def test_empty_annotations_render_no_sections(self):
        out = io.StringIO()
        adoption_guidance.print_guidance_sections([], out.write)
        self.assertEqual(out.getvalue(), "")

    def test_provider_config_guidance_section_golden(self):
        out = io.StringIO()
        adoption_guidance.print_guidance_sections(
            [self._provider_config_annotation()],
            out.write,
        )
        self.assertEqual(out.getvalue(), (
            "  Provider configuration guidance:\n"
            "    - provider: sample\n"
            "      setting: add_sample_attribution_label\n"
            "      expected value: false\n"
            "      mode: required_external\n"
            "      matched plan path: "
            "terraform_labels.goog-terraform-provisioned\n"
            "      reason: Sample provider adds attribution labels.\n"
            "      evidence: docs/provider-labs/sample.md\n"
            "      status: informational only; plan remains blocked\n"
        ))

    def test_absent_default_guidance_section_golden(self):
        out = io.StringIO()
        adoption_guidance.print_guidance_sections(
            [self._absent_default_annotation()],
            out.write,
        )
        self.assertEqual(out.getvalue(), (
            "  Absent/default guidance:\n"
            "    - rule: sample_empty_name_prefix\n"
            "      provider: sample\n"
            "      resource type: sample_resource\n"
            "      kind: provider_absent_placeholder\n"
            "      action: manual_review_required\n"
            "      observed value: \"\"\n"
            "      matched plan path: name_prefix\n"
            "      reason: Sample provider imported empty name_prefix alongside "
            "concrete name; manual review required.\n"
            "      evidence: docs/provider-labs/sample.md\n"
            "      status: informational only; plan remains blocked\n"
        ))

    def test_combined_guidance_sections_golden_ordering(self):
        out = io.StringIO()
        adoption_guidance.print_guidance_sections([
            self._absent_default_annotation(
                rule="z_empty_name_prefix",
                matched_plan_path="z_name_prefix",
                reason="Z absent/default reason.",
            ),
            self._provider_config_annotation(
                setting="z_provider_setting",
                matched_plan_path="z_provider_path",
                reason="Z provider-config reason.",
            ),
            self._absent_default_annotation(
                rule="a_empty_name_prefix",
                matched_plan_path="a_name_prefix",
                reason="A absent/default reason.",
            ),
            self._provider_config_annotation(
                setting="a_provider_setting",
                matched_plan_path="a_provider_path",
                reason="A provider-config reason.",
            ),
        ], out.write)
        self.assertEqual(out.getvalue(), (
            "  Provider configuration guidance:\n"
            "    - provider: sample\n"
            "      setting: a_provider_setting\n"
            "      expected value: false\n"
            "      mode: required_external\n"
            "      matched plan path: a_provider_path\n"
            "      reason: A provider-config reason.\n"
            "      evidence: docs/provider-labs/sample.md\n"
            "      status: informational only; plan remains blocked\n"
            "    - provider: sample\n"
            "      setting: z_provider_setting\n"
            "      expected value: false\n"
            "      mode: required_external\n"
            "      matched plan path: z_provider_path\n"
            "      reason: Z provider-config reason.\n"
            "      evidence: docs/provider-labs/sample.md\n"
            "      status: informational only; plan remains blocked\n"
            "  Absent/default guidance:\n"
            "    - rule: a_empty_name_prefix\n"
            "      provider: sample\n"
            "      resource type: sample_resource\n"
            "      kind: provider_absent_placeholder\n"
            "      action: manual_review_required\n"
            "      observed value: \"\"\n"
            "      matched plan path: a_name_prefix\n"
            "      reason: A absent/default reason.\n"
            "      evidence: docs/provider-labs/sample.md\n"
            "      status: informational only; plan remains blocked\n"
            "    - rule: z_empty_name_prefix\n"
            "      provider: sample\n"
            "      resource type: sample_resource\n"
            "      kind: provider_absent_placeholder\n"
            "      action: manual_review_required\n"
            "      observed value: \"\"\n"
            "      matched plan path: z_name_prefix\n"
            "      reason: Z absent/default reason.\n"
            "      evidence: docs/provider-labs/sample.md\n"
            "      status: informational only; plan remains blocked\n"
        ))

    def test_annotations_for_finding_path_matches_normalized_key(self):
        annotation = adoption_guidance.absent_default_annotation(
            source="resource_changes",
            address="sample_resource.this",
            matched_plan_path="rules[].name_prefix",
            provider="sample",
            resource_type="sample_resource",
            rule="sample_rule",
            kind="provider_absent_placeholder",
            action="manual_review_required",
            observed_value="",
            reason="sample",
            evidence="docs/sample.md",
        )
        finding = {
            "source": "resource_changes",
            "address": "sample_resource.this",
        }

        self.assertEqual(
            adoption_guidance.annotations_for_finding_path(
                [annotation], finding, ("rules", 0, "name_prefix")
            ),
            [annotation],
        )


if __name__ == "__main__":
    unittest.main()
