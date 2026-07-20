package contracts

import (
	"encoding/json"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// SourceProvenanceValue converts a valid manifest into the deterministic value
// tree required by docs/go-authoring-port-roadmap.md §3.2.1.
func SourceProvenanceValue(provenance SourceProvenance) (canonjson.Value, error) {
	if err := ValidateSourceProvenance(provenance); err != nil {
		return nil, err
	}
	return typedValue(provenance, sourceProvenanceContract)
}

// RenderSourceProvenance renders a valid verified manifest deterministically
// under docs/go-authoring-port-roadmap.md §3.2.1.
func RenderSourceProvenance(provenance SourceProvenance) (string, error) {
	value, err := SourceProvenanceValue(provenance)
	if err != nil {
		return "", err
	}
	return canonjson.Render(value)
}

// InputProvenanceValue converts the verified/unverified input union into the
// deterministic value tree from docs/go-authoring-port-roadmap.md §3.5.
func InputProvenanceValue(provenance InputProvenance) (canonjson.Value, error) {
	if err := ValidateInputProvenance(provenance); err != nil {
		return nil, err
	}
	return typedValue(provenance, inputProvenanceContract)
}

// RenderInputProvenance renders input-provenance.json deterministically under
// docs/go-authoring-port-roadmap.md §3.5.
func RenderInputProvenance(provenance InputProvenance) (string, error) {
	value, err := InputProvenanceValue(provenance)
	if err != nil {
		return "", err
	}
	return canonjson.Render(value)
}

// SourceEvidenceReportValue converts a valid source report into the isolated
// deterministic value tree from docs/go-authoring-port-roadmap.md §3.3.
func SourceEvidenceReportValue(report SourceEvidenceReport) (canonjson.Value, error) {
	if err := ValidateSourceEvidenceReport(report); err != nil {
		return nil, err
	}
	return typedValue(report, sourceReportContract)
}

// RenderSourceEvidenceReport renders source evidence without OpenAPI fields so
// optional-adapter changes cannot alter its bytes under docs/go-authoring-port-roadmap.md §3.6.
func RenderSourceEvidenceReport(report SourceEvidenceReport) (string, error) {
	value, err := SourceEvidenceReportValue(report)
	if err != nil {
		return "", err
	}
	return canonjson.Render(value)
}

// OpenAPIDiagnosticsReportValue converts a valid isolated comparison report to
// the deterministic value tree from docs/go-authoring-port-roadmap.md §3.6.
func OpenAPIDiagnosticsReportValue(diagnostics OpenAPIDiagnosticsReport, source SourceEvidenceReport) (canonjson.Value, error) {
	if err := ValidateOpenAPIDiagnosticsReport(diagnostics, source); err != nil {
		return nil, err
	}
	return typedValue(diagnostics, openAPIDiagnosticsContract)
}

// RenderOpenAPIDiagnosticsReport renders the isolated comparison artifact
// deterministically under docs/go-authoring-port-roadmap.md §3.6.
func RenderOpenAPIDiagnosticsReport(diagnostics OpenAPIDiagnosticsReport, source SourceEvidenceReport) (string, error) {
	value, err := OpenAPIDiagnosticsReportValue(diagnostics, source)
	if err != nil {
		return "", err
	}
	return canonjson.Render(value)
}

func typedValue(value any, contract string) (canonjson.Value, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, contractError(ErrorInvalidStructure, contract, "$", "typed value is not JSON encodable")
	}
	decoded, err := canonjson.Decode(data)
	if err != nil {
		return nil, contractError(ErrorInvalidStructure, contract, "$", "typed value cannot be converted to canonical JSON")
	}
	return decoded, nil
}
