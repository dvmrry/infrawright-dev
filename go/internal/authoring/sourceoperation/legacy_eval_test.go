package sourceoperation

import "testing"

func TestClassifyLegacyV1SourceEvidenceChange(t *testing.T) {
	tests := []struct {
		name   string
		change LegacyV1Artifact
		class  string
		reason string
	}{
		{
			name: "mapped_to_unmapped",
			change: LegacyV1Artifact{
				"before": LegacyV1Artifact{"status": "mapped", "read_path": "/old", "files": []any{"resource.go"}},
				"after":  LegacyV1Artifact{"status": "unmapped", "files": []any{"resource.go"}},
			},
			class: "regression", reason: "mapped_to_unmapped",
		},
		{
			name: "source_files_narrowed",
			change: LegacyV1Artifact{
				"before": LegacyV1Artifact{"status": "mapped", "read_path": "/same", "files": []any{"a.go", "b.go"}},
				"after":  LegacyV1Artifact{"status": "mapped", "read_path": "/same", "files": []any{"a.go"}},
			},
			class: "acceptable", reason: "source_files_narrowed",
		},
		{
			name: "new_mapping",
			change: LegacyV1Artifact{
				"before": LegacyV1Artifact{"status": "unmapped", "files": []any{"resource.go"}},
				"after":  LegacyV1Artifact{"status": "mapped", "read_path": "/new", "files": []any{"resource.go"}},
			},
			class: "review", reason: "new_mapping",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyLegacyV1SourceEvidenceChange(test.change)
			if got["classification"] != test.class || got["reason"] != test.reason {
				t.Errorf("ClassifyLegacyV1SourceEvidenceChange(%#v) = %#v, want classification=%q reason=%q", test.change, got, test.class, test.reason)
			}
		})
	}
}

func TestEvaluateLegacyV1SourceEvidenceReportsRegressions(t *testing.T) {
	candidate := LegacyV1Artifact{
		"diagnostics": []any{},
		"registry": LegacyV1Artifact{
			"sample": LegacyV1Artifact{"status": "unmapped", "reason": "resource_file_not_found", "source": LegacyV1Artifact{}},
		},
		"summary": LegacyV1Artifact{"resources": 1, "unmapped": 1},
	}
	comparison := LegacyV1Artifact{
		"changes": []any{LegacyV1Artifact{
			"resource": "sample",
			"before":   LegacyV1Artifact{"status": "mapped", "read_path": "/sample/{id}", "files": []any{"resource.go"}},
			"after":    LegacyV1Artifact{"status": "unmapped", "files": []any{"resource.go"}},
		}},
		"summary": LegacyV1Artifact{"resources": 1, "unchanged": 0, "control": LegacyV1Artifact{"mapped": 1}, "candidate": candidate["summary"]},
	}
	evaluation := EvaluateLegacyV1SourceEvidence(candidate, comparison)
	summary := legacyV1Object(evaluation["summary"])
	if got := legacyV1Number(summary["regressions"]); got != 1 {
		t.Errorf("EvaluateLegacyV1SourceEvidence() regressions = %v, want 1", got)
	}
	if !LegacyV1FailOnRegressionAfterArtifacts(evaluation) {
		t.Error("LegacyV1FailOnRegressionAfterArtifacts(evaluation) = false, want true")
	}
	markdown := RenderLegacyV1SourceEvidenceMarkdown(evaluation)
	if markdown == "" {
		t.Error("RenderLegacyV1SourceEvidenceMarkdown(evaluation) is empty, want report")
	}
}

func TestLegacyV1FailOnRegressionAfterArtifacts(t *testing.T) {
	if !LegacyV1FailOnRegressionAfterArtifacts(LegacyV1Artifact{"summary": LegacyV1Artifact{"regressions": 1}}) {
		t.Error("LegacyV1FailOnRegressionAfterArtifacts(positive regression count) = false, want true")
	}
	if LegacyV1FailOnRegressionAfterArtifacts(LegacyV1Artifact{"summary": LegacyV1Artifact{"regressions": nil}}) {
		t.Error("LegacyV1FailOnRegressionAfterArtifacts(null regression count) = true, want false")
	}
}
