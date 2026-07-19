package sourceoperation

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const legacyV1AuthoritySHA256 = "5f94567238aabfc6522b07863b764719ceef7708bc8f55b8e12db13f88bf299e"

func legacyV1Authority(t *testing.T) LegacyV1Artifact {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "node-tests", "fixtures", "python-source-evidence-eval-v1.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frozen authority: %v", err)
	}
	if actual := sha256.Sum256(bytes); fmtSHA256(actual) != legacyV1AuthoritySHA256 {
		t.Fatalf("frozen authority SHA-256 = %s, want %s", fmtSHA256(actual), legacyV1AuthoritySHA256)
	}
	value, err := canonjson.Decode(bytes)
	if err != nil {
		t.Fatalf("decode frozen authority: %v", err)
	}
	return legacyV1Object(value)
}

func fmtSHA256(sum [32]byte) string {
	const hexdigits = "0123456789abcdef"
	output := make([]byte, 64)
	for index, byteValue := range sum {
		output[index*2] = hexdigits[byteValue>>4]
		output[index*2+1] = hexdigits[byteValue&0x0f]
	}
	return string(output)
}

func legacyV1FrozenCase(t *testing.T, name string) LegacyV1Artifact {
	t.Helper()
	authority := legacyV1Authority(t)
	cases := legacyV1Object(authority["cases"])
	result, ok := cases[name]
	if !ok {
		t.Fatalf("frozen authority has no %q case", name)
	}
	return legacyV1Object(result)
}

func legacyV1DecodedArtifactBytes(t *testing.T, bytes []byte) LegacyV1Artifact {
	t.Helper()
	decoded, err := canonjson.Decode(bytes)
	if err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	return legacyV1Object(decoded)
}

func legacyV1ExactJSON(t *testing.T, actual, expected any) {
	t.Helper()
	actualBytes, err := canonjson.Render(actual)
	if err != nil {
		t.Fatalf("render actual: %v", err)
	}
	expectedBytes, err := canonjson.Render(expected)
	if err != nil {
		t.Fatalf("render expected: %v", err)
	}
	if actualBytes != expectedBytes {
		t.Fatalf("JSON mismatch:\nactual:\n%s\nexpected:\n%s", actualBytes, expectedBytes)
	}
}

func legacyV1DecodedArtifact(t *testing.T, value any) LegacyV1Artifact {
	t.Helper()
	bytes, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test artifact: %v", err)
	}
	decoded, err := canonjson.Decode(bytes)
	if err != nil {
		t.Fatalf("decode test artifact: %v", err)
	}
	return legacyV1Object(decoded)
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

func TestLegacyV1ClassificationsShortcomingsAndMarkdownMatchFrozenAuthority(t *testing.T) {
	candidate, comparison := legacyV1ClassificationInputs()
	candidate = legacyV1DecodedArtifact(t, candidate)
	comparison = legacyV1DecodedArtifact(t, comparison)
	evaluation := EvaluateLegacyV1SourceEvidence(legacyV1DecodedArtifact(t, candidate), legacyV1DecodedArtifact(t, comparison))
	expected := legacyV1FrozenCase(t, "classifications_shortcomings_markdown")
	legacyV1ExactJSON(t, evaluation, expected["evaluation"])
	if actual, want := RenderLegacyV1SourceEvidenceMarkdown(evaluation), expected["markdown"].(string); actual != want {
		t.Fatalf("Markdown mismatch:\nactual:\n%s\nexpected:\n%s", actual, want)
	}
}

func TestLegacyV1AuthorityCaseSetAndEmbeddedCLIEvaluatorOutputs(t *testing.T) {
	authority := legacyV1Authority(t)
	cases := legacyV1Object(authority["cases"])
	wantCases := []string{
		"authoring_cli_artifact_set",
		"classifications_shortcomings_markdown",
		"explicit_null_metrics",
		"markdown_change_cap",
	}
	if len(cases) != len(wantCases) {
		t.Fatalf("frozen authority case count = %d, want %d; add an explicit replay before accepting a new case", len(cases), len(wantCases))
	}
	for _, name := range wantCases {
		if _, ok := cases[name]; !ok {
			t.Fatalf("frozen authority is missing required case %q", name)
		}
	}

	fixture := legacyV1Object(cases["authoring_cli_artifact_set"])
	artifacts := legacyV1Object(fixture["artifacts"])
	candidate := legacyV1DecodedArtifactBytes(t, []byte(artifacts["ast-report.json"].(string)))
	comparison := legacyV1DecodedArtifactBytes(t, []byte(artifacts["source-facts-compare.json"].(string)))
	expected := legacyV1DecodedArtifactBytes(t, []byte(artifacts["source-evidence-eval.json"].(string)))
	delete(expected, "artifacts") // CLI routing owns paths; this package owns evaluator content.

	evaluation := EvaluateLegacyV1SourceEvidence(candidate, comparison)
	legacyV1ExactJSON(t, evaluation, expected)
	if actual, want := RenderLegacyV1SourceEvidenceMarkdown(evaluation), artifacts["source-evidence-eval.md"].(string); actual != want {
		t.Fatalf("embedded CLI Markdown mismatch:\nactual:\n%s\nexpected:\n%s", actual, want)
	}
}

func TestLegacyV1ExplicitNullMetricsMatchFrozenAuthority(t *testing.T) {
	comparison := LegacyV1Artifact{"changes": []any{}, "summary": LegacyV1Artifact{"resources": nil, "unchanged": nil, "control": LegacyV1Artifact{"resources": nil, "mapped": nil}, "candidate": LegacyV1Artifact{"resources": nil, "mapped": nil}}}
	candidate := LegacyV1Artifact{"diagnostics": []any{}, "registry": LegacyV1Artifact{"missing": LegacyV1Artifact{"status": "unmapped", "reason": "resource_file_not_found", "source": LegacyV1Artifact{"candidate_count": nil, "client_call_count": nil, "package_call_count": nil, "raw_rest_call_count": nil}}}, "summary": LegacyV1Artifact{"resources": nil, "mapped": nil}}
	evaluation := EvaluateLegacyV1SourceEvidence(legacyV1DecodedArtifact(t, candidate), legacyV1DecodedArtifact(t, comparison))
	expected := legacyV1FrozenCase(t, "explicit_null_metrics")
	legacyV1ExactJSON(t, evaluation, expected["evaluation"])
	if actual, want := RenderLegacyV1SourceEvidenceMarkdown(evaluation), expected["markdown"].(string); actual != want {
		t.Fatalf("Markdown mismatch:\nactual:\n%s\nexpected:\n%s", actual, want)
	}
}

func TestLegacyV1MarkdownChangeCapMatchesFrozenAuthority(t *testing.T) {
	changes := make([]any, 0, LegacyV1MaxMarkdownChangeRows+6)
	for index := 0; index < LegacyV1MaxMarkdownChangeRows+5; index++ {
		name := "example_" + string(rune('0'+index/100)) + string(rune('0'+(index/10)%10)) + string(rune('0'+index%10))
		changes = append(changes, LegacyV1Artifact{"resource": name, "before": LegacyV1Artifact{"status": "mapped", "read_path": "/old", "files": []any{"old.go", "extra.go"}}, "after": LegacyV1Artifact{"status": "mapped", "read_path": "/old", "files": []any{"old.go"}}})
	}
	changes = append(changes, LegacyV1Artifact{"resource": "example_regression", "before": LegacyV1Artifact{"status": "mapped", "read_path": "/old", "files": []any{"old.go"}}, "after": LegacyV1Artifact{"status": "unmapped", "files": []any{"old.go"}}})
	empty := LegacyV1Artifact{"registry": LegacyV1Artifact{}, "diagnostics": []any{}, "summary": LegacyV1Artifact{}}
	evaluation := EvaluateLegacyV1SourceEvidence(legacyV1DecodedArtifact(t, empty), legacyV1DecodedArtifact(t, LegacyV1Artifact{"changes": changes, "summary": LegacyV1Artifact{"resources": len(changes), "unchanged": 0, "control": LegacyV1Artifact{}, "candidate": LegacyV1Artifact{}}}))
	expected := legacyV1FrozenCase(t, "markdown_change_cap")
	legacyV1ExactJSON(t, evaluation, expected["evaluation"])
	if actual, want := RenderLegacyV1SourceEvidenceMarkdown(evaluation), expected["markdown"].(string); actual != want {
		t.Fatalf("Markdown mismatch:\nactual:\n%s\nexpected:\n%s", actual, want)
	}
}

func TestLegacyV1FailOnRegressionAfterArtifacts(t *testing.T) {
	if !LegacyV1FailOnRegressionAfterArtifacts(LegacyV1Artifact{"summary": LegacyV1Artifact{"regressions": 1}}) {
		t.Fatal("positive regression count must request legacy exit status 1 after publication")
	}
	if LegacyV1FailOnRegressionAfterArtifacts(LegacyV1Artifact{"summary": LegacyV1Artifact{"regressions": nil}}) {
		t.Fatal("explicit null regression count must not request a failure")
	}
}
