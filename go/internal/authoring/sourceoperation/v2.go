package sourceoperation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceanalysis"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

const (
	sourceRegistryName     = "source-registry.json"
	sourceDiagnosticsName  = "source-diagnostics.json"
	summaryName            = "summary.json"
	summaryMarkdownName    = "summary.md"
	inputProvenanceName    = "input-provenance.json"
	openAPIDiagnosticsName = "openapi-diagnostics.json"
)

var requiredArtifactNames = []string{
	sourceRegistryName,
	sourceDiagnosticsName,
	summaryName,
	summaryMarkdownName,
	inputProvenanceName,
	openAPIDiagnosticsName,
}

// Artifact is one detached named byte stream in the v2 complete-set bundle.
// Name is deliberately constrained to the fixed artifact vocabulary.
type Artifact struct {
	Name  string
	Bytes []byte
}

// Bundle is a fully compiled and self-validating v2 source-operation artifact
// set. It contains no local filesystem paths.
type Bundle struct {
	artifacts []Artifact
}

// Artifacts returns a detached copy in deterministic bundle order.
func (b Bundle) Artifacts() []Artifact {
	result := make([]Artifact, len(b.artifacts))
	for i, artifact := range b.artifacts {
		result[i] = Artifact{Name: artifact.Name, Bytes: append([]byte(nil), artifact.Bytes...)}
	}
	return result
}

// input holds canonical bytes produced by the source-first evidence pipeline.
// It is private so arbitrary canonical bytes cannot be made to look qualified.
type input struct {
	SourceRegistry  []byte
	InputProvenance []byte
}

// CompileQualified is the production A1-to-A2 integration seam. It takes the
// sealed A1 evidence and sourcebind-qualified input so callers cannot replace
// the registry or provenance with freshly rendered lookalikes. It analyzes the
// captured optional OpenAPI status only through the sealed adapter capability.
func CompileQualified(ctx context.Context, evidence sourceanalysis.QualifiedEvidence, inputs sourcebind.QualifiedInputs) (Bundle, error) {
	if err := ctx.Err(); err != nil {
		return Bundle{}, fmt.Errorf("compile source-operation bundle: %w", err)
	}
	registry, err := evidence.CanonicalBytes()
	if err != nil {
		return Bundle{}, fmt.Errorf("read qualified source evidence: %w", err)
	}
	report, err := contracts.DecodeSourceEvidenceReport(registry)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode qualified source evidence: %w", err)
	}
	snapshot, err := inputs.Snapshot()
	if err != nil {
		return Bundle{}, fmt.Errorf("snapshot qualified source inputs: %w", err)
	}
	openAPI, err := openapiadapter.Analyze(ctx, snapshot.OpenAPI, report)
	if err != nil {
		return Bundle{}, fmt.Errorf("analyze qualified OpenAPI status: %w", err)
	}
	return compile(ctx, input{SourceRegistry: registry, InputProvenance: snapshot.InputProvenanceBytes}, contracts.SourceTrustVerified, &openAPI)
}

// CompileUnverified compiles a diagnostic-only v2 bundle from the separate A1
// unverified capability. It cannot be passed where QualifiedEvidence is
// required, and this function never upgrades its trust or legacy projection.
func CompileUnverified(ctx context.Context, evidence sourceanalysis.UnverifiedEvidence, inputs sourcebind.UnverifiedInputs) (Bundle, error) {
	if err := ctx.Err(); err != nil {
		return Bundle{}, fmt.Errorf("compile source-operation bundle: %w", err)
	}
	registry, err := evidence.CanonicalBytes()
	if err != nil {
		return Bundle{}, fmt.Errorf("read unverified source evidence: %w", err)
	}
	return compile(ctx, input{
		SourceRegistry:  registry,
		InputProvenance: append([]byte(nil), inputs.InputProvenanceBytes...),
	}, contracts.SourceTrustUnverified, nil)
}

// compile is deliberately package-private: arbitrary canonical bytes cannot
// mint a verified-looking bundle. Production callers use CompileQualified or
// CompileUnverified, which require distinct sealed evidence capabilities.
func compile(ctx context.Context, input input, trust contracts.SourceTrust, openAPI *openapiadapter.Result) (Bundle, error) {
	if err := ctx.Err(); err != nil {
		return Bundle{}, fmt.Errorf("compile source-operation bundle: %w", err)
	}
	report, err := contracts.DecodeSourceEvidenceReport(input.SourceRegistry)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode source registry: %w", err)
	}
	if report.SourceTrust != trust {
		return Bundle{}, fmt.Errorf("source-operation v2 source trust = %q, want %q", report.SourceTrust, trust)
	}
	provenance, err := contracts.DecodeInputProvenance(input.InputProvenance)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode input provenance: %w", err)
	}
	if provenance.SourceTrust != trust {
		return Bundle{}, fmt.Errorf("source-operation v2 input provenance trust = %q, want %q", provenance.SourceTrust, trust)
	}
	if err := contracts.ValidateSourceEvidenceReportAgainstInput(report, provenance); err != nil {
		return Bundle{}, fmt.Errorf("bind source registry to input provenance: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Bundle{}, fmt.Errorf("compile source-operation bundle: %w", err)
	}

	registry := append([]byte(nil), input.SourceRegistry...)
	inputBytes := append([]byte(nil), input.InputProvenance...)
	diagnostics, err := renderSourceDiagnostics(report, registry)
	if err != nil {
		return Bundle{}, err
	}
	summary, err := renderSummary(report, registry)
	if err != nil {
		return Bundle{}, err
	}
	markdown := renderSummaryMarkdown(report, registry)
	openAPIDiagnostics, err := renderOpenAPIDiagnostics(report, openAPI)
	if err != nil {
		return Bundle{}, err
	}
	bundle := Bundle{artifacts: []Artifact{
		{Name: sourceRegistryName, Bytes: registry},
		{Name: sourceDiagnosticsName, Bytes: diagnostics},
		{Name: summaryName, Bytes: summary},
		{Name: summaryMarkdownName, Bytes: markdown},
		{Name: inputProvenanceName, Bytes: inputBytes},
		{Name: openAPIDiagnosticsName, Bytes: openAPIDiagnostics},
	}}
	if err := validateBundle(bundle); err != nil {
		return Bundle{}, fmt.Errorf("validate compiled source-operation bundle: %w", err)
	}
	return bundle, nil
}

// renderOpenAPIDiagnostics accepts only an optional sealed adapter capability.
// It never accepts caller-supplied diagnostic bytes, and it validates detached
// adapter output against the exact decoded source report before bundling it.
func renderOpenAPIDiagnostics(report contracts.SourceEvidenceReport, result *openapiadapter.Result) ([]byte, error) {
	if result == nil {
		return renderAbsentOpenAPIDiagnostics(report)
	}
	bytes, err := result.CanonicalBytes()
	if err != nil {
		return nil, fmt.Errorf("read sealed OpenAPI diagnostics: %w", err)
	}
	if _, err := contracts.DecodeOpenAPIDiagnosticsReport(bytes, report); err != nil {
		return nil, fmt.Errorf("bind sealed OpenAPI diagnostics to source report: %w", err)
	}
	return bytes, nil
}

func renderAbsentOpenAPIDiagnostics(report contracts.SourceEvidenceReport) ([]byte, error) {
	registry, err := contracts.RenderSourceEvidenceReport(report)
	if err != nil {
		return nil, fmt.Errorf("render source report for OpenAPI binding: %w", err)
	}
	comparisons := make(map[string]contracts.OpenAPIComparisonRow, len(report.Resources))
	for resource := range report.Resources {
		comparisons[resource] = contracts.OpenAPIComparisonRow{State: contracts.ComparisonNotAttempted, Operations: []contracts.OpenAPIOperationCandidate{}}
	}
	diagnostics := contracts.OpenAPIDiagnosticsReport{
		Kind: "infrawright.openapi_diagnostics", SchemaVersion: 1,
		SourceTrust: report.SourceTrust, SourceManifestSHA256: cloneOptionalString(report.SourceManifestSHA256),
		SourceReportSHA256: sha256Hex([]byte(registry)), DocumentState: contracts.OpenAPIAbsent,
		Comparisons: comparisons,
		Summary: contracts.OpenAPIComparisonSummary{
			ComparisonEligibleTotal: report.Summary.ClassificationCounts.ObservedHTTP,
			ComparisonCounts:        contracts.OpenAPIComparisonCounts{NotAttempted: report.Summary.SelectedTotal},
		},
	}
	rendered, err := contracts.RenderOpenAPIDiagnosticsReport(diagnostics, report)
	if err != nil {
		return nil, fmt.Errorf("render absent OpenAPI diagnostics: %w", err)
	}
	return []byte(rendered), nil
}

func validateBundle(bundle Bundle) error {
	if len(bundle.artifacts) != len(requiredArtifactNames) {
		return fmt.Errorf("expected %d artifacts, got %d", len(requiredArtifactNames), len(bundle.artifacts))
	}
	byName := make(map[string][]byte, len(bundle.artifacts))
	for i, artifact := range bundle.artifacts {
		if artifact.Name != requiredArtifactNames[i] || len(artifact.Bytes) == 0 {
			return fmt.Errorf("invalid artifact at index %d", i)
		}
		if _, duplicate := byName[artifact.Name]; duplicate {
			return fmt.Errorf("duplicate artifact %q", artifact.Name)
		}
		byName[artifact.Name] = artifact.Bytes
	}
	report, err := contracts.DecodeSourceEvidenceReport(byName[sourceRegistryName])
	if err != nil {
		return fmt.Errorf("source registry: %w", err)
	}
	provenance, err := contracts.DecodeInputProvenance(byName[inputProvenanceName])
	if err != nil {
		return fmt.Errorf("input provenance: %w", err)
	}
	if err := contracts.ValidateSourceEvidenceReportAgainstInput(report, provenance); err != nil {
		return fmt.Errorf("source/input binding: %w", err)
	}
	if err := decodeSourceDiagnostics(byName[sourceDiagnosticsName], report, byName[sourceRegistryName]); err != nil {
		return err
	}
	if err := decodeSummary(byName[summaryName], report, byName[sourceRegistryName]); err != nil {
		return err
	}
	if !bytes.Equal(byName[summaryMarkdownName], renderSummaryMarkdown(report, byName[sourceRegistryName])) {
		return fmt.Errorf("summary Markdown does not derive from the validated source report")
	}
	if _, err := contracts.DecodeOpenAPIDiagnosticsReport(byName[openAPIDiagnosticsName], report); err != nil {
		return fmt.Errorf("OpenAPI diagnostics: %w", err)
	}
	return nil
}

func decodeSourceDiagnostics(data []byte, report contracts.SourceEvidenceReport, registry []byte) error {
	var diagnostics SourceDiagnostics
	if err := decodeCanonical(data, &diagnostics); err != nil {
		return fmt.Errorf("source diagnostics: %w", err)
	}
	return validateSourceDiagnostics(diagnostics, report, registry)
}

func decodeSummary(data []byte, report contracts.SourceEvidenceReport, registry []byte) error {
	var summary Summary
	if err := decodeCanonical(data, &summary); err != nil {
		return fmt.Errorf("summary: %w", err)
	}
	return validateSummary(summary, report, registry)
}

func decodeCanonical(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("must contain one JSON value")
	}
	rendered, err := marshalCanonical(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, rendered) {
		return fmt.Errorf("must use canonical source-operation bytes")
	}
	return nil
}

func marshalCanonical(value any) ([]byte, error) {
	return json.Marshal(value)
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
