package sourceoperation

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const legacyEvalCompatibilitySHA256 = "bd1df7836c7c3c2e51c191ec278f2df22c75def90717601419a5852310637de1"

func loadLegacyEvalCompatibility(t *testing.T) LegacyV1Artifact {
	t.Helper()
	fixturePath := filepath.Join("testdata", "legacy_eval_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != legacyEvalCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, legacyEvalCompatibilitySHA256)
	}
	decoded, err := canonjson.Decode(fixtureBytes)
	if err != nil {
		t.Fatalf("canonjson.Decode(%q) error: %v", fixturePath, err)
	}
	fixture := legacyV1Object(decoded)
	if legacyV1Number(fixture["schema_version"]) != 1 {
		t.Fatalf("%s schema_version = %v, want 1", fixturePath, fixture["schema_version"])
	}
	cases := legacyV1Object(fixture["cases"])
	if len(cases) != 2 {
		t.Fatalf("%s cases = %d, want 2", fixturePath, len(cases))
	}
	return cases
}

func TestLegacyV1ExplicitNullMetricsCompatibility(t *testing.T) {
	cases := loadLegacyEvalCompatibility(t)
	comparison := LegacyV1Artifact{"changes": []any{}, "summary": LegacyV1Artifact{"resources": nil, "unchanged": nil, "control": LegacyV1Artifact{"resources": nil, "mapped": nil}, "candidate": LegacyV1Artifact{"resources": nil, "mapped": nil}}}
	candidate := LegacyV1Artifact{"diagnostics": []any{}, "registry": LegacyV1Artifact{"missing": LegacyV1Artifact{"status": "unmapped", "reason": "resource_file_not_found", "source": LegacyV1Artifact{"candidate_count": nil, "client_call_count": nil, "package_call_count": nil, "raw_rest_call_count": nil}}}, "summary": LegacyV1Artifact{"resources": nil, "mapped": nil}}
	evaluation := EvaluateLegacyV1SourceEvidence(decodedLegacyV1Artifact(t, candidate), decodedLegacyV1Artifact(t, comparison))
	expected := legacyV1Object(cases["explicit_null_metrics"])
	legacyEvalCompatibilityJSONEqual(t, evaluation, expected["evaluation"])
	if got, want := RenderLegacyV1SourceEvidenceMarkdown(evaluation), expected["markdown"].(string); got != want {
		t.Errorf("explicit-null Markdown = %q, want fixed %q", got, want)
	}
}

func TestLegacyV1EmbeddedCLIArtifactCompatibility(t *testing.T) {
	cases := loadLegacyEvalCompatibility(t)
	expected := legacyV1Object(cases["authoring_cli_artifact_set"])
	artifacts := legacyV1Object(expected["artifacts"])
	candidate := legacyEvalCompatibilityDecode(t, artifacts["ast-report.json"].(string))
	comparison := legacyEvalCompatibilityDecode(t, artifacts["source-facts-compare.json"].(string))
	evaluation := EvaluateLegacyV1SourceEvidence(candidate, comparison)
	legacyEvalCompatibilityJSONEqual(t, evaluation, expected["stdout_without_artifacts"])

	artifactEvaluation := legacyEvalCompatibilityDecode(t, artifacts["source-evidence-eval.json"].(string))
	delete(artifactEvaluation, "artifacts")
	legacyEvalCompatibilityJSONEqual(t, evaluation, artifactEvaluation)
	if got, want := RenderLegacyV1SourceEvidenceMarkdown(evaluation), artifacts["source-evidence-eval.md"].(string); got != want {
		t.Errorf("embedded CLI Markdown = %q, want fixed %q", got, want)
	}
}

func legacyEvalCompatibilityDecode(t *testing.T, text string) LegacyV1Artifact {
	t.Helper()
	decoded, err := canonjson.Decode([]byte(text))
	if err != nil {
		t.Fatalf("canonjson.Decode(compatibility artifact) error: %v", err)
	}
	return legacyV1Object(decoded)
}

func legacyEvalCompatibilityJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	gotJSON, err := canonjson.Render(got)
	if err != nil {
		t.Fatalf("canonjson.Render(got) error: %v", err)
	}
	wantJSON, err := canonjson.Render(want)
	if err != nil {
		t.Fatalf("canonjson.Render(want) error: %v", err)
	}
	if gotJSON != wantJSON {
		t.Errorf("compatibility JSON = %s, want %s", gotJSON, wantJSON)
	}
}
