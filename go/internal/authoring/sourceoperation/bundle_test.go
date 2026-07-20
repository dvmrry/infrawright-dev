package sourceoperation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceanalysis"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

func TestCompileVerifiedFixtureProducesExactCoreInputs(t *testing.T) {
	input := fixtureInput(t)
	bundle, err := compile(context.Background(), input, contracts.SourceTrustVerified, nil)
	if err != nil {
		t.Fatalf("compile(verified fixture) error = %v, want nil", err)
	}
	artifacts := bundle.Artifacts()
	if len(artifacts) != 6 {
		t.Fatalf("compile(verified fixture) artifacts = %d, want 6", len(artifacts))
	}
	byName := artifactMap(t, artifacts)
	if !bytes.Equal(byName[sourceRegistryName], input.SourceRegistry) {
		t.Error("source-registry.json does not retain exact A1 source report bytes")
	}
	if !bytes.Equal(byName[inputProvenanceName], input.InputProvenance) {
		t.Error("input-provenance.json does not retain exact sourcebind bytes")
	}
	report, err := contracts.DecodeSourceEvidenceReport(byName[sourceRegistryName])
	if err != nil {
		t.Fatalf("DecodeSourceEvidenceReport(bundle source registry) error = %v, want nil", err)
	}
	if _, err := contracts.DecodeOpenAPIDiagnosticsReport(byName[openAPIDiagnosticsName], report); err != nil {
		t.Fatalf("DecodeOpenAPIDiagnosticsReport(absent) error = %v, want nil", err)
	}
	if err := decodeSourceDiagnostics(byName[sourceDiagnosticsName], report, byName[sourceRegistryName]); err != nil {
		t.Fatalf("decodeSourceDiagnostics(bundle) error = %v, want nil", err)
	}
	if err := decodeSummary(byName[summaryName], report, byName[sourceRegistryName]); err != nil {
		t.Fatalf("decodeSummary(bundle) error = %v, want nil", err)
	}
	for _, name := range []string{sourceDiagnosticsName, summaryName, summaryMarkdownName} {
		want := mustRead(t, filepath.Join("testdata", "source-first-v2", name))
		// JSON golden source files end in a repository-text newline; v2 JSON
		// contracts deliberately do not. Markdown's terminal newline is data.
		if name != summaryMarkdownName {
			want = bytes.TrimSuffix(want, []byte("\n"))
		}
		if !bytes.Equal(byName[name], want) {
			t.Errorf("compile(verified fixture) %s differs from the reviewed v2 expectation", name)
		}
	}
}

func TestCompileIsDeterministicAndPortable(t *testing.T) {
	input := fixtureInput(t)
	first, err := compile(context.Background(), input, contracts.SourceTrustVerified, nil)
	if err != nil {
		t.Fatalf("first compile() error = %v, want nil", err)
	}
	second, err := compile(context.Background(), input, contracts.SourceTrustVerified, nil)
	if err != nil {
		t.Fatalf("second compile() error = %v, want nil", err)
	}
	firstArtifacts, secondArtifacts := first.Artifacts(), second.Artifacts()
	if len(firstArtifacts) != len(secondArtifacts) {
		t.Fatalf("compile() artifact count changed: got %d, want %d", len(secondArtifacts), len(firstArtifacts))
	}
	for i := range firstArtifacts {
		if firstArtifacts[i].Name != secondArtifacts[i].Name || !bytes.Equal(firstArtifacts[i].Bytes, secondArtifacts[i].Bytes) {
			t.Errorf("compile() artifact %d changed between identical inputs", i)
		}
	}
	firstArtifacts[0].Bytes[0] ^= 1
	if bytes.Equal(firstArtifacts[0].Bytes, first.Artifacts()[0].Bytes) {
		t.Error("Bundle.Artifacts() returned mutable internal artifact bytes")
	}
	absolute := t.TempDir()
	for _, artifact := range firstArtifacts {
		if bytes.Contains(artifact.Bytes, []byte(absolute)) || bytes.Contains(artifact.Bytes, []byte(repositoryRoot(t))) {
			t.Errorf("%s contains an absolute local path", artifact.Name)
		}
	}
}

func TestCompileRejectsUnverifiedAndBrokenBindings(t *testing.T) {
	fixture := fixtureInput(t)
	report, err := contracts.DecodeSourceEvidenceReport(fixture.SourceRegistry)
	if err != nil {
		t.Fatal(err)
	}
	report.SourceTrust = contracts.SourceTrustUnverified
	report.SourceManifestSHA256 = nil
	for resource, row := range report.Resources {
		row.LegacyMapped = false
		report.Resources[resource] = row
	}
	if _, err := compile(context.Background(), input{SourceRegistry: mustRenderSource(t, report), InputProvenance: fixture.InputProvenance}, contracts.SourceTrustVerified, nil); err == nil {
		t.Error("compile(unverified source report) error = nil, want rejection")
	}
	badProvenance := append([]byte(nil), fixture.InputProvenance...)
	badProvenance[len(badProvenance)-1] ^= 1
	if _, err := compile(context.Background(), input{SourceRegistry: fixture.SourceRegistry, InputProvenance: badProvenance}, contracts.SourceTrustVerified, nil); err == nil {
		t.Error("compile(noncanonical provenance) error = nil, want rejection")
	}
}

func TestCompileUnverifiedProducesDiagnosticOnlyBundle(t *testing.T) {
	inputs := unverifiedInputs(t)
	evidence, err := sourceanalysis.AnalyzeUnverified(context.Background(), inputs)
	if err != nil {
		t.Fatalf("AnalyzeUnverified() error = %v, want nil", err)
	}
	bundle, err := CompileUnverified(context.Background(), evidence, inputs)
	if err != nil {
		t.Fatalf("CompileUnverified() error = %v, want nil", err)
	}
	artifacts := artifactMap(t, bundle.Artifacts())
	if len(artifacts) != len(requiredArtifactNames) || !bytes.Equal(artifacts[inputProvenanceName], inputs.InputProvenanceBytes) {
		t.Fatal("CompileUnverified() did not preserve the complete diagnostic bundle and exact provenance bytes")
	}
	report, err := contracts.DecodeSourceEvidenceReport(artifacts[sourceRegistryName])
	if err != nil {
		t.Fatal(err)
	}
	if report.SourceTrust != contracts.SourceTrustUnverified {
		t.Errorf("CompileUnverified() trust = %q, want unverified", report.SourceTrust)
	}
	for resource, row := range report.Resources {
		if row.LegacyMapped {
			t.Errorf("CompileUnverified() %s legacy_mapped = true, want false", resource)
		}
	}
	openAPI, err := contracts.DecodeOpenAPIDiagnosticsReport(artifacts[openAPIDiagnosticsName], report)
	if err != nil || openAPI.SourceTrust != contracts.SourceTrustUnverified {
		t.Fatalf("CompileUnverified() absent diagnostics = (%#v, %v), want unverified diagnostics", openAPI, err)
	}
	tampered := inputs
	tampered.InputProvenanceBytes = append([]byte(nil), inputs.InputProvenanceBytes...)
	tampered.InputProvenanceBytes[0] ^= 1
	if _, err := CompileUnverified(context.Background(), evidence, tampered); err == nil {
		t.Error("CompileUnverified(tampered provenance bytes) error = nil, want binding rejection")
	}
}

func TestDerivedArtifactsCannotAlterSourceClassificationOrCounts(t *testing.T) {
	bundle := fixtureBundle(t)
	mutated := Bundle{artifacts: bundle.Artifacts()}
	for i := range mutated.artifacts {
		if mutated.artifacts[i].Name != sourceDiagnosticsName {
			continue
		}
		var diagnostics SourceDiagnostics
		if err := json.Unmarshal(mutated.artifacts[i].Bytes, &diagnostics); err != nil {
			t.Fatal(err)
		}
		diagnostics.Resources[0].Classification = contracts.SourceUnresolved
		bytes, err := json.Marshal(diagnostics)
		if err != nil {
			t.Fatal(err)
		}
		mutated.artifacts[i].Bytes = bytes
		break
	}
	if err := validateBundle(mutated); err == nil {
		t.Error("validateBundle(mutated source diagnostics) error = nil, want source-projection rejection")
	}
}

func fixtureBundle(t *testing.T) Bundle {
	t.Helper()
	bundle, err := compile(context.Background(), fixtureInput(t), contracts.SourceTrustVerified, nil)
	if err != nil {
		t.Fatalf("compile(fixture input) error = %v, want nil", err)
	}
	return bundle
}

func fixtureInput(t *testing.T) input {
	t.Helper()
	root := filepath.Join(repositoryRoot(t), "tests", "fixtures", "authoring", "source-first-v2", "expected")
	return input{
		SourceRegistry:  mustRead(t, filepath.Join(root, "source-evidence-report-v1.json")),
		InputProvenance: mustRead(t, filepath.Join(root, "input-provenance.json")),
	}
}

func unverifiedInputs(t *testing.T) sourcebind.UnverifiedInputs {
	t.Helper()
	root := t.TempDir()
	providerRoot := filepath.Join(root, "provider")
	if err := os.Mkdir(providerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerRoot, "provider.go"), []byte("package provider\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "schema.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	inputs, err := sourcebind.LoadUnverified(context.Background(), sourcebind.UnverifiedRoots{
		ProviderRoot: providerRoot, ProviderModulePath: "example.invalid/provider", ProviderFiles: []string{"provider.go"},
		SchemaRoot: root, TerraformSchema: "schema.json",
		SDKRoots: map[string]string{}, SDKFiles: map[string][]string{}, SDKVersions: map[string]string{},
		Selection: contracts.SelectionBinding{ResourceTypes: []string{"example_widget"}, Filters: []contracts.SelectionFilterBinding{}},
	})
	if err != nil {
		t.Fatalf("LoadUnverified() error = %v, want nil", err)
	}
	return inputs
}

func artifactMap(t *testing.T, artifacts []Artifact) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte, len(artifacts))
	for _, artifact := range artifacts {
		if _, duplicate := result[artifact.Name]; duplicate {
			t.Fatalf("duplicate artifact %q", artifact.Name)
		}
		result[artifact.Name] = artifact.Bytes
	}
	return result
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", name, err)
	}
	return data
}

func mustRenderSource(t *testing.T, report contracts.SourceEvidenceReport) []byte {
	t.Helper()
	rendered, err := contracts.RenderSourceEvidenceReport(report)
	if err != nil {
		t.Fatalf("RenderSourceEvidenceReport() error = %v", err)
	}
	return []byte(rendered)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) = false")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}
