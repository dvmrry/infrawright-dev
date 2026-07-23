package sourceoperation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func decodedLegacyV1Artifact(t *testing.T, value any) LegacyV1Artifact {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T) error: %v", value, err)
	}
	decoded, err := canonjson.Decode(data)
	if err != nil {
		t.Fatalf("canonjson.Decode(%T) error: %v", value, err)
	}
	return legacyV1Object(decoded)
}

func legacyV1EvaluationSHA256(t *testing.T, evaluation LegacyV1Artifact) string {
	t.Helper()
	rendered, err := canonjson.Render(evaluation)
	if err != nil {
		t.Fatalf("canonjson.Render(evaluation) error: %v", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rendered)))
}

func legacyV1ClassificationInputs() (LegacyV1Artifact, LegacyV1Artifact) {
	changes := []any{
		LegacyV1Artifact{"resource": "mapped_unmapped", "before": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "files": []any{"a.go"}}, "after": LegacyV1Artifact{"status": "unmapped", "files": []any{"a.go"}}},
		LegacyV1Artifact{"resource": "mapped_path", "before": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "files": []any{"a.go"}}, "after": LegacyV1Artifact{"status": "mapped", "read_path": "/b", "files": []any{"a.go"}}},
		LegacyV1Artifact{"resource": "files_zero", "before": LegacyV1Artifact{"status": "unmapped", "files": []any{"a.go"}}, "after": LegacyV1Artifact{"status": "unmapped", "files": []any{}}},
		LegacyV1Artifact{"resource": "files_narrow", "before": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "files": []any{"a.go", "b.go"}}, "after": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "files": []any{"a.go"}}},
		LegacyV1Artifact{"resource": "files_changed", "before": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "files": []any{"a.go"}}, "after": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "files": []any{"b.go"}}},
		LegacyV1Artifact{"resource": "new_mapping", "before": LegacyV1Artifact{"status": "unmapped", "files": []any{"a.go"}}, "after": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "files": []any{"a.go"}}},
		LegacyV1Artifact{"resource": "ambiguous", "before": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "files": []any{"a.go"}}, "after": LegacyV1Artifact{"status": "ambiguous_source_operation", "read_path": "/a", "files": []any{"a.go"}}},
		LegacyV1Artifact{"resource": "list", "before": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "list_path": "/list-a", "files": []any{}}, "after": LegacyV1Artifact{"status": "mapped", "read_path": "/a", "list_path": "/list-b", "files": []any{}}},
		LegacyV1Artifact{"resource": "read", "before": LegacyV1Artifact{"status": "unmapped", "read_path": "/a", "files": []any{}}, "after": LegacyV1Artifact{"status": "unmapped", "read_path": "/b", "files": []any{}}},
		LegacyV1Artifact{"resource": "status", "before": LegacyV1Artifact{"status": "graphql_source", "files": []any{}}, "after": LegacyV1Artifact{"status": "unmapped", "files": []any{}}},
		LegacyV1Artifact{"resource": "diagnostic", "before": LegacyV1Artifact{"status": "unmapped", "candidate_count": 0, "files": []any{}}, "after": LegacyV1Artifact{"status": "unmapped", "candidate_count": 1, "files": []any{}}},
	}
	candidate := LegacyV1Artifact{
		"diagnostics": []any{LegacyV1Artifact{"resource": "no_reason", "hits": []any{LegacyV1Artifact{"client_symbol": "Widgets.Get", "operation_id": "GetWidget", "method": "GET", "path": "/widgets/{id}", "read_score": 50}}}},
		"registry": LegacyV1Artifact{
			"ambiguous": LegacyV1Artifact{"status": "ambiguous_source_operation", "reason": "ambiguous_source_operation", "source": LegacyV1Artifact{"files": []any{"ambiguous.go"}, "candidate_count": 2}, "candidates": []any{LegacyV1Artifact{"client_symbol": "One.Get", "method": "GET", "operation_id": "GetOne", "path": "/one/{id}", "path_kind": "detail", "source_role": "read", "read_score": 50, "list_score": 10}}},
			"graphql":   LegacyV1Artifact{"status": "graphql_source", "reason": "graphql_source", "source": LegacyV1Artifact{"files": []any{"graphql.go"}}},
			"missing":   LegacyV1Artifact{"status": "unmapped", "reason": "resource_file_not_found", "source": LegacyV1Artifact{}},
			"no_match":  LegacyV1Artifact{"status": "unmapped", "reason": "no_source_operation_match", "source": LegacyV1Artifact{"files": []any{"calls.go"}, "client_call_count": 1, "client_calls": []any{"Widgets.Get"}}},
			"no_calls":  LegacyV1Artifact{"status": "unmapped", "reason": "no_source_operation_match", "source": LegacyV1Artifact{"files": []any{"empty.go"}}},
			"no_reason": LegacyV1Artifact{"status": "unmapped", "reason": nil, "source": LegacyV1Artifact{"files": []any{"unknown.go"}, "candidate_count": 1}},
			"read_only": LegacyV1Artifact{"status": "mapped", "reason": nil, "source": LegacyV1Artifact{"files": []any{"read.go"}}, "read": LegacyV1Artifact{"path": "/read/{id}", "operation_id": "GetRead"}},
		},
		"summary": LegacyV1Artifact{"resources": 7, "mapped": 1, "ambiguous": 1, "graphql_source": 1, "unmapped": 4, "resources_with_source_files": 6},
	}
	comparison := LegacyV1Artifact{"changes": changes, "summary": LegacyV1Artifact{"resources": len(changes), "unchanged": 2, "control": LegacyV1Artifact{"resources": 11, "mapped": 5}, "candidate": candidate["summary"]}}
	return candidate, comparison
}

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

func TestEvaluateLegacyV1SourceEvidenceMatchesFixedOutput(t *testing.T) {
	candidate, comparison := legacyV1ClassificationInputs()
	evaluation := EvaluateLegacyV1SourceEvidence(
		decodedLegacyV1Artifact(t, candidate),
		decodedLegacyV1Artifact(t, comparison),
	)
	if got, want := legacyV1EvaluationSHA256(t, evaluation), "e8853c371b08896e4239848d458f47c6c7c72fc95fa62a1448e45cf5929d90cc"; got != want {
		t.Errorf("SHA256(EvaluateLegacyV1SourceEvidence()) = %q, want fixed %q", got, want)
	}
	markdown := RenderLegacyV1SourceEvidenceMarkdown(evaluation)
	if got, want := fmt.Sprintf("%x", sha256.Sum256([]byte(markdown))), "ea249cfe71f908cb912f19679df4f2d2872720084d789b6b2e1b71da3402c593"; got != want {
		t.Errorf("SHA256(RenderLegacyV1SourceEvidenceMarkdown(evaluation)) = %q, want fixed %q", got, want)
	}
	for _, want := range []string{
		"| \x60mapped_unmapped\x60 | \x60regression\x60 | \x60mapped_to_unmapped\x60 |",
		"| \x60ambiguous_source_operation\x60 | \x60review\x60 | \x601\x60 | \x60ambiguous\x60 |",
		"| \x60calls_without_openapi_match\x60 | \x60gap\x60 | \x601\x60 | \x60no_match\x60 |",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("RenderLegacyV1SourceEvidenceMarkdown(evaluation) lacks fixed row %q", want)
		}
	}
}

func TestLegacyV1MarkdownChangeCapMatchesFixedOutput(t *testing.T) {
	changes := make([]any, 0, LegacyV1MaxMarkdownChangeRows+6)
	for index := 0; index < LegacyV1MaxMarkdownChangeRows+5; index++ {
		name := fmt.Sprintf("example_%03d", index)
		changes = append(changes, LegacyV1Artifact{
			"resource": name,
			"before":   LegacyV1Artifact{"status": "mapped", "read_path": "/old", "files": []any{"old.go", "extra.go"}},
			"after":    LegacyV1Artifact{"status": "mapped", "read_path": "/old", "files": []any{"old.go"}},
		})
	}
	changes = append(changes, LegacyV1Artifact{
		"resource": "example_regression",
		"before":   LegacyV1Artifact{"status": "mapped", "read_path": "/old", "files": []any{"old.go"}},
		"after":    LegacyV1Artifact{"status": "unmapped", "files": []any{"old.go"}},
	})
	empty := LegacyV1Artifact{"registry": LegacyV1Artifact{}, "diagnostics": []any{}, "summary": LegacyV1Artifact{}}
	comparison := LegacyV1Artifact{
		"changes": changes,
		"summary": LegacyV1Artifact{"resources": len(changes), "unchanged": 0, "control": LegacyV1Artifact{}, "candidate": LegacyV1Artifact{}},
	}
	evaluation := EvaluateLegacyV1SourceEvidence(decodedLegacyV1Artifact(t, empty), decodedLegacyV1Artifact(t, comparison))
	if got, want := legacyV1EvaluationSHA256(t, evaluation), "e6c182656a2816ab0f7c44ca12f0e3179d8c971c3ef10173b5df6e3c78e1162b"; got != want {
		t.Errorf("SHA256(change-cap evaluation) = %q, want fixed %q", got, want)
	}
	markdown := RenderLegacyV1SourceEvidenceMarkdown(evaluation)
	if got, want := fmt.Sprintf("%x", sha256.Sum256([]byte(markdown))), "fa716de451aef6a5065f3f7ff19b69a094cdc7db7cd3910ebf5bdc4f4038c860"; got != want {
		t.Errorf("SHA256(change-cap Markdown) = %q, want fixed %q", got, want)
	}
	if !strings.Contains(markdown, "Showing `100` of `106` changes; full detail is in JSON.") ||
		!strings.Contains(markdown, "| `example_098` |") || strings.Contains(markdown, "| `example_099` |") {
		t.Errorf("change-cap Markdown did not retain the fixed first-100 boundary:\n%s", markdown)
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
