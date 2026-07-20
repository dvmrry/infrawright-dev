package sourceoperation

import (
	"fmt"
	"sort"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

// SourceDiagnostics is the explicit v1 derived diagnostics contract. Its
// resource rows and summary are exact projections of the bound source report;
// validation rejects any changed classification, reason, chain count, or count.
type SourceDiagnostics struct {
	Kind                 string                     `json:"kind"`
	SchemaVersion        int                        `json:"schema_version"`
	SourceTrust          contracts.SourceTrust      `json:"source_trust"`
	SourceManifestSHA256 *string                    `json:"source_manifest_sha256"`
	SourceReportSHA256   string                     `json:"source_report_sha256"`
	Resources            []SourceDiagnosticResource `json:"resources"`
	Summary              contracts.SourceSummary    `json:"summary"`
}

// SourceDiagnosticResource is a sorted, source-report-bound diagnostic row.
type SourceDiagnosticResource struct {
	Resource       string                         `json:"resource"`
	Classification contracts.SourceClassification `json:"classification"`
	ReasonCode     *contracts.SourceReasonCode    `json:"reason_code"`
	ChainCount     int                            `json:"chain_count"`
}

func renderSourceDiagnostics(report contracts.SourceEvidenceReport, registry []byte) ([]byte, error) {
	resources := make([]SourceDiagnosticResource, 0, len(report.Resources))
	for _, resource := range sortedResourceKeys(report.Resources) {
		row := report.Resources[resource]
		resources = append(resources, SourceDiagnosticResource{
			Resource: resource, Classification: row.Classification, ReasonCode: row.ReasonCode, ChainCount: len(row.Chains),
		})
	}
	value := SourceDiagnostics{
		Kind: "infrawright.source_diagnostics", SchemaVersion: 1,
		SourceTrust: report.SourceTrust, SourceManifestSHA256: cloneOptionalString(report.SourceManifestSHA256),
		SourceReportSHA256: sha256Hex(registry), Resources: resources, Summary: report.Summary,
	}
	if err := validateSourceDiagnostics(value, report, registry); err != nil {
		return nil, fmt.Errorf("validate source diagnostics: %w", err)
	}
	return marshalCanonical(value)
}

func validateSourceDiagnostics(value SourceDiagnostics, report contracts.SourceEvidenceReport, registry []byte) error {
	if value.Kind != "infrawright.source_diagnostics" || value.SchemaVersion != 1 {
		return fmt.Errorf("must identify source-diagnostics-v1")
	}
	if value.SourceTrust != report.SourceTrust || !sameOptionalString(value.SourceManifestSHA256, report.SourceManifestSHA256) || value.SourceReportSHA256 != sha256Hex(registry) {
		return fmt.Errorf("must bind exact source report identity")
	}
	if value.Summary != report.Summary || len(value.Resources) != len(report.Resources) {
		return fmt.Errorf("must preserve exact source summary and resource count")
	}
	keys := sortedResourceKeys(report.Resources)
	for i, resource := range keys {
		row := value.Resources[i]
		source := report.Resources[resource]
		if row.Resource != resource || row.Classification != source.Classification || !sameReason(row.ReasonCode, source.ReasonCode) || row.ChainCount != len(source.Chains) {
			return fmt.Errorf("resource %q must be an exact source diagnostics projection", resource)
		}
	}
	return nil
}

func sortedResourceKeys(resources map[string]contracts.SourceEvidenceRow) []string {
	keys := make([]string, 0, len(resources))
	for resource := range resources {
		keys = append(keys, resource)
	}
	sort.Strings(keys)
	return keys
}

func sameReason(left, right *contracts.SourceReasonCode) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
