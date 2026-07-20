package sourceoperation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceanalysis"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

const (
	usableOpenAPI   = `{"openapi":"3.0.3","info":{"title":"sourcefirst","version":"1"},"paths":{"/v1/direct/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok"}}}},"/v1/catalog/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok"}}}}}}`
	degradedOpenAPI = `{"openapi":"3.0.3","info":{"title":"sourcefirst","version":"1"},"paths":{"/v1/direct/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok"}}}},"/v1/catalog/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok"}}}},"/broken":{"get":"not-an-operation"}}}`
)

func TestCompileSealedOpenAPIResultPreservesCoreArtifacts(t *testing.T) {
	input := fixtureInput(t)
	report := mustDecodeSourceReport(t, input.SourceRegistry)
	baseline, err := compile(context.Background(), input, contracts.SourceTrustVerified, nil)
	if err != nil {
		t.Fatalf("compile(absent OpenAPI) error = %v, want nil", err)
	}

	cases := []struct {
		name   string
		status sourcebind.OpenAPIStatus
		want   contracts.OpenAPIDocumentState
	}{
		{name: "unreadable", status: sourcebind.OpenAPIStatus{Err: errors.New("unreadable")}, want: contracts.OpenAPIUnavailable},
		{name: "malformed", status: availableOpenAPI([]byte(`{"openapi":`)), want: contracts.OpenAPIUnavailable},
		{name: "invalid root", status: availableOpenAPI([]byte(`{"openapi":"9.0.0","info":{"title":"x","version":"1"},"paths":{}}`)), want: contracts.OpenAPIUnavailable},
		{name: "degraded", status: availableOpenAPI([]byte(degradedOpenAPI)), want: contracts.OpenAPIDegraded},
		{name: "usable", status: availableOpenAPI([]byte(usableOpenAPI)), want: contracts.OpenAPIUsable},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := openapiadapter.Analyze(context.Background(), test.status, report)
			if err != nil {
				t.Fatalf("Analyze(%s) error = %v, want nil", test.name, err)
			}
			bundle, err := compile(context.Background(), input, contracts.SourceTrustVerified, &result)
			if err != nil {
				t.Fatalf("compile(%s sealed result) error = %v, want nil", test.name, err)
			}
			assertOnlyOpenAPIDiagnosticsChanged(t, baseline, bundle)
			diagnostics := mustBundleOpenAPIDiagnostics(t, bundle, report)
			if got := diagnostics.DocumentState; got != test.want {
				t.Errorf("compile(%s).DocumentState = %q, want %q", test.name, got, test.want)
			}
			if got, want := comparisonCountTotal(diagnostics.Summary.ComparisonCounts), report.Summary.SelectedTotal; got != want {
				t.Errorf("compile(%s) comparison partition total = %d, want %d", test.name, got, want)
			}
			if test.want == contracts.OpenAPIUsable || test.want == contracts.OpenAPIDegraded {
				if got, want := diagnostics.Summary.ComparisonEligibleTotal, report.Summary.ClassificationCounts.ObservedHTTP; got != want {
					t.Errorf("compile(%s) comparison eligible total = %d, want %d", test.name, got, want)
				}
				return
			}
			if got, want := diagnostics.Summary.ComparisonCounts.NotAttempted, report.Summary.SelectedTotal; got != want {
				t.Errorf("compile(%s) not attempted total = %d, want %d", test.name, got, want)
			}
		})
	}
}

func TestCompileQualifiedUsesCapturedOpenAPIStatus(t *testing.T) {
	evidence, inputs := qualifiedInputsWithOpenAPI(t, []byte(usableOpenAPI))
	snapshot, err := inputs.Snapshot()
	if err != nil {
		t.Fatalf("QualifiedInputs.Snapshot() error = %v, want nil", err)
	}
	if !snapshot.OpenAPI.Available || len(snapshot.OpenAPI.Files) != 2 {
		t.Fatalf("QualifiedInputs.Snapshot().OpenAPI = %#v, want captured document and local reference", snapshot.OpenAPI)
	}
	registry, err := evidence.CanonicalBytes()
	if err != nil {
		t.Fatalf("QualifiedEvidence.CanonicalBytes() error = %v, want nil", err)
	}
	baseline, err := compile(context.Background(), input{SourceRegistry: registry, InputProvenance: snapshot.InputProvenanceBytes}, contracts.SourceTrustVerified, nil)
	if err != nil {
		t.Fatalf("compile(qualified source-only baseline) error = %v, want nil", err)
	}
	bundle, err := CompileQualified(context.Background(), evidence, inputs)
	if err != nil {
		t.Fatalf("CompileQualified(captured usable OpenAPI) error = %v, want nil", err)
	}
	assertOnlyOpenAPIDiagnosticsChanged(t, baseline, bundle)
	report := mustDecodeSourceReport(t, registry)
	if got := mustBundleOpenAPIDiagnostics(t, bundle, report).DocumentState; got != contracts.OpenAPIUsable {
		t.Errorf("CompileQualified(captured usable OpenAPI).DocumentState = %q, want usable", got)
	}
}

func TestCompileQualifiedWithoutOpenAPIRetainsAbsentDiagnostics(t *testing.T) {
	evidence, inputs := qualifiedInputsWithOpenAPI(t, nil)
	bundle, err := CompileQualified(context.Background(), evidence, inputs)
	if err != nil {
		t.Fatalf("CompileQualified(no captured OpenAPI) error = %v, want nil", err)
	}
	registry, err := evidence.CanonicalBytes()
	if err != nil {
		t.Fatalf("QualifiedEvidence.CanonicalBytes() error = %v, want nil", err)
	}
	report := mustDecodeSourceReport(t, registry)
	if got := mustBundleOpenAPIDiagnostics(t, bundle, report).DocumentState; got != contracts.OpenAPIAbsent {
		t.Errorf("CompileQualified(no captured OpenAPI).DocumentState = %q, want absent", got)
	}
}

func TestSealedOpenAPIResultRejectsMismatchedSourceAndMutation(t *testing.T) {
	input := fixtureInput(t)
	report := mustDecodeSourceReport(t, input.SourceRegistry)
	exact, err := openapiadapter.Analyze(context.Background(), availableOpenAPI([]byte(usableOpenAPI)), report)
	if err != nil {
		t.Fatalf("Analyze(exact report) error = %v, want nil", err)
	}
	returned, err := exact.CanonicalBytes()
	if err != nil {
		t.Fatalf("Result.CanonicalBytes() error = %v, want nil", err)
	}
	returned[0] ^= 1
	bundle, err := compile(context.Background(), input, contracts.SourceTrustVerified, &exact)
	if err != nil {
		t.Fatalf("compile(mutated detached result bytes) error = %v, want nil", err)
	}
	if got := mustBundleOpenAPIDiagnostics(t, bundle, report).DocumentState; got != contracts.OpenAPIUsable {
		t.Errorf("compile(mutated detached result bytes).DocumentState = %q, want usable", got)
	}

	for _, test := range []struct {
		name   string
		report contracts.SourceEvidenceReport
	}{
		{name: "changed report digest", report: changedReportDigest(report)},
		{name: "changed manifest digest", report: changedManifestDigest(report)},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := openapiadapter.Analyze(context.Background(), sourcebind.OpenAPIStatus{}, test.report)
			if err != nil {
				t.Fatalf("Analyze(%s) error = %v, want nil", test.name, err)
			}
			if _, err := compile(context.Background(), input, contracts.SourceTrustVerified, &result); err == nil {
				t.Errorf("compile(%s) error = nil, want source-binding rejection", test.name)
			}
		})
	}

	unverified := unverifiedInputs(t)
	unverifiedEvidence, err := sourceanalysis.AnalyzeUnverified(context.Background(), unverified)
	if err != nil {
		t.Fatalf("AnalyzeUnverified() error = %v, want nil", err)
	}
	unverifiedReport, err := unverifiedEvidence.Snapshot()
	if err != nil {
		t.Fatalf("UnverifiedEvidence.Snapshot() error = %v, want nil", err)
	}
	trustMismatch, err := openapiadapter.Analyze(context.Background(), sourcebind.OpenAPIStatus{}, unverifiedReport)
	if err != nil {
		t.Fatalf("Analyze(unverified report) error = %v, want nil", err)
	}
	if _, err := compile(context.Background(), input, contracts.SourceTrustVerified, &trustMismatch); err == nil {
		t.Error("compile(unverified sealed result) error = nil, want trust rejection")
	}

	var zero openapiadapter.Result
	if _, err := compile(context.Background(), input, contracts.SourceTrustVerified, &zero); err == nil {
		t.Error("compile(zero sealed result) error = nil, want rejection")
	}
}

func TestOpenAPIAccountingHandlesZeroSelectedRows(t *testing.T) {
	report := mustDecodeSourceReport(t, fixtureInput(t).SourceRegistry)
	report.Resources = map[string]contracts.SourceEvidenceRow{}
	report.Summary = contracts.SourceSummary{
		ClassificationCounts: contracts.SourceClassificationCounts{},
		EndpointCoverage:     contracts.ExactCoverage{State: contracts.CoverageNotApplicable},
	}
	result, err := openapiadapter.Analyze(context.Background(), availableOpenAPI([]byte(usableOpenAPI)), report)
	if err != nil {
		t.Fatalf("Analyze(zero selected rows) error = %v, want nil", err)
	}
	data, err := renderOpenAPIDiagnostics(report, &result)
	if err != nil {
		t.Fatalf("renderOpenAPIDiagnostics(zero selected rows) error = %v, want nil", err)
	}
	diagnostics, err := contracts.DecodeOpenAPIDiagnosticsReport(data, report)
	if err != nil {
		t.Fatalf("DecodeOpenAPIDiagnosticsReport(zero selected rows) error = %v, want nil", err)
	}
	if got := comparisonCountTotal(diagnostics.Summary.ComparisonCounts); got != 0 || diagnostics.Summary.ComparisonEligibleTotal != 0 {
		t.Errorf("zero-row OpenAPI accounting = %#v, want all comparison totals zero", diagnostics.Summary)
	}
}

func TestCompileQualifiedAndPrivateCompileHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CompileQualified(ctx, sourceanalysis.QualifiedEvidence{}, sourcebind.QualifiedInputs{}); !errors.Is(err, context.Canceled) {
		t.Errorf("CompileQualified(cancelled before snapshot) error = %v, want wrapped context cancellation", err)
	}
	if _, err := openapiadapter.Analyze(ctx, sourcebind.OpenAPIStatus{}, mustDecodeSourceReport(t, fixtureInput(t).SourceRegistry)); !errors.Is(err, context.Canceled) {
		t.Errorf("Analyze(cancelled before adapter analysis) error = %v, want wrapped context cancellation", err)
	}
	if _, err := compile(ctx, fixtureInput(t), contracts.SourceTrustVerified, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("compile(cancelled before bundle validation) error = %v, want wrapped context cancellation", err)
	}
}

func assertOnlyOpenAPIDiagnosticsChanged(t *testing.T, baseline, got Bundle) {
	t.Helper()
	baselineArtifacts, gotArtifacts := baseline.Artifacts(), got.Artifacts()
	if len(gotArtifacts) != len(requiredArtifactNames) {
		t.Fatalf("Bundle.Artifacts() count = %d, want %d", len(gotArtifacts), len(requiredArtifactNames))
	}
	for i, name := range requiredArtifactNames {
		if gotArtifacts[i].Name != name {
			t.Errorf("Bundle.Artifacts()[%d].Name = %q, want %q", i, gotArtifacts[i].Name, name)
		}
		if i < len(baselineArtifacts) && i < len(gotArtifacts) && i < len(requiredArtifactNames)-1 && !bytes.Equal(baselineArtifacts[i].Bytes, gotArtifacts[i].Bytes) {
			t.Errorf("Bundle.Artifacts()[%d] %s changed, want source-only byte identity", i, name)
		}
	}
}

func mustBundleOpenAPIDiagnostics(t *testing.T, bundle Bundle, report contracts.SourceEvidenceReport) contracts.OpenAPIDiagnosticsReport {
	t.Helper()
	artifacts := artifactMap(t, bundle.Artifacts())
	diagnostics, err := contracts.DecodeOpenAPIDiagnosticsReport(artifacts[openAPIDiagnosticsName], report)
	if err != nil {
		t.Fatalf("DecodeOpenAPIDiagnosticsReport(bundle) error = %v, want nil", err)
	}
	return diagnostics
}

func comparisonCountTotal(counts contracts.OpenAPIComparisonCounts) int {
	return counts.NotAttempted + counts.NotComparable + counts.Corroborated + counts.MissingPath + counts.Ambiguous + counts.Conflict
}

func availableOpenAPI(data []byte) sourcebind.OpenAPIStatus {
	digest := sha256.Sum256(data)
	return sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{{Path: "openapi.json", Bytes: append([]byte(nil), data...), SHA256: hex.EncodeToString(digest[:])}}}
}

func mustDecodeSourceReport(t *testing.T, data []byte) contracts.SourceEvidenceReport {
	t.Helper()
	report, err := contracts.DecodeSourceEvidenceReport(data)
	if err != nil {
		t.Fatalf("DecodeSourceEvidenceReport() error = %v, want nil", err)
	}
	return report
}

func changedReportDigest(report contracts.SourceEvidenceReport) contracts.SourceEvidenceReport {
	report.InputProvenanceSHA256 = strings.Repeat("0", 64)
	return report
}

func changedManifestDigest(report contracts.SourceEvidenceReport) contracts.SourceEvidenceReport {
	changed := strings.Repeat("0", 64)
	report.SourceManifestSHA256 = &changed
	return report
}

func qualifiedInputsWithOpenAPI(t *testing.T, document []byte) (sourceanalysis.QualifiedEvidence, sourcebind.QualifiedInputs) {
	t.Helper()
	fixtureRoot := filepath.Join(repositoryRoot(t), "tests", "fixtures", "authoring", "source-first-v2")
	workingRoot := t.TempDir()
	checkedRoot := filepath.Join(workingRoot, "checked")
	providerRoot := filepath.Join(workingRoot, "provider")
	sdkRoot := filepath.Join(workingRoot, "sdk")
	copyFixtureTree(t, fixtureRoot, checkedRoot)
	copyFixtureTree(t, filepath.Join(fixtureRoot, "provider"), providerRoot)
	copyFixtureTree(t, filepath.Join(fixtureRoot, "sdk"), sdkRoot)

	manifestPath := filepath.Join(checkedRoot, "source-provenance-v1.json")
	manifestBytes := mustRead(t, manifestPath)
	manifest, err := contracts.DecodeSourceProvenance(manifestBytes)
	if err != nil {
		t.Fatalf("DecodeSourceProvenance() error = %v, want nil", err)
	}
	if document != nil {
		openAPIPath := filepath.Join(checkedRoot, "openapi.json")
		if err := os.WriteFile(openAPIPath, document, 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", openAPIPath, err)
		}
		localReference := []byte(`{"components":{}}`)
		localReferencePath := filepath.Join(checkedRoot, "openapi-local.json")
		if err := os.WriteFile(localReferencePath, localReference, 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", localReferencePath, err)
		}
		digest := sha256.Sum256(document)
		localReferenceDigest := sha256.Sum256(localReference)
		manifest.OpenAPI = &contracts.OpenAPIInputBinding{
			Document:  contracts.FileBinding{Path: "openapi.json", SHA256: hex.EncodeToString(digest[:])},
			LocalRefs: []contracts.FileBinding{{Path: "openapi-local.json", SHA256: hex.EncodeToString(localReferenceDigest[:])}},
		}
		rendered, err := contracts.RenderSourceProvenance(manifest)
		if err != nil {
			t.Fatalf("RenderSourceProvenance(with OpenAPI) error = %v, want nil", err)
		}
		if err := os.WriteFile(manifestPath, []byte(rendered), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", manifestPath, err)
		}
	}
	commitFixtureProvider(t, providerRoot)
	loaded, err := sourcebind.LoadVerified(context.Background(), sourcebind.LocalRoots{
		ManifestPath: manifestPath, ProviderRoot: providerRoot,
		SDKRoots: map[string]string{"example.invalid/sourcefirst-sdk": sdkRoot}, SchemaRoot: checkedRoot, OpenAPIRoot: checkedRoot,
	})
	if err != nil {
		t.Fatalf("LoadVerified(captured OpenAPI fixture) error = %v, want nil", err)
	}
	inputs, err := sourcebind.RequireQualification(loaded)
	if err != nil {
		t.Fatalf("RequireQualification() error = %v, want nil", err)
	}
	snapshot, err := inputs.Snapshot()
	if err != nil {
		t.Fatalf("QualifiedInputs.Snapshot() error = %v, want nil", err)
	}
	if _, err := contracts.RenderInputProvenance(snapshot.InputProvenance); err != nil {
		t.Fatalf("RenderInputProvenance(captured OpenAPI fixture) error = %v, want nil", err)
	}
	evidence, err := sourceanalysis.Analyze(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Analyze(qualified captured fixture) error = %v, want nil", err)
	}
	return evidence, inputs
}

func copyFixtureTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	}); err != nil {
		t.Fatalf("copyFixtureTree(%q, %q) error = %v", source, destination, err)
	}
}

func commitFixtureProvider(t *testing.T, directory string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Infrawright Fixture", "-c", "user.email=fixtures@infrawright.invalid", "commit", "-qm", "source-first fixture provider"},
	} {
		command := exec.Command("git", args...)
		command.Dir = directory
		command.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_COUNT=0",
			"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false",
			"GIT_AUTHOR_NAME=Infrawright Fixture", "GIT_AUTHOR_EMAIL=fixtures@infrawright.invalid", "GIT_AUTHOR_DATE=2000-01-01T00:00:00 +0000",
			"GIT_COMMITTER_NAME=Infrawright Fixture", "GIT_COMMITTER_EMAIL=fixtures@infrawright.invalid", "GIT_COMMITTER_DATE=2000-01-01T00:00:00 +0000",
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v error = %v, output = %s", args, err, output)
		}
	}
}
