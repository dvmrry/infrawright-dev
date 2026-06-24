import io
import unittest

from engine import adoption_guidance


class AdoptionGuidanceTest(unittest.TestCase):
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
