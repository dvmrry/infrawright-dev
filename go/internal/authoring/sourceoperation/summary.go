package sourceoperation

import (
	"fmt"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

// Summary is the explicit v1 concise source summary contract. SourceSummary is
// copied only after validation and must equal the source report summary exactly.
type Summary struct {
	Kind                 string                         `json:"kind"`
	SchemaVersion        int                            `json:"schema_version"`
	SourceTrust          contracts.SourceTrust          `json:"source_trust"`
	SourceManifestSHA256 *string                        `json:"source_manifest_sha256"`
	SourceReportSHA256   string                         `json:"source_report_sha256"`
	OpenAPIDocumentState contracts.OpenAPIDocumentState `json:"openapi_document_state"`
	SourceSummary        contracts.SourceSummary        `json:"source_summary"`
}

func renderSummary(report contracts.SourceEvidenceReport, registry []byte) ([]byte, error) {
	value := Summary{
		Kind: "infrawright.source_operation_summary", SchemaVersion: 1,
		SourceTrust: report.SourceTrust, SourceManifestSHA256: cloneOptionalString(report.SourceManifestSHA256),
		SourceReportSHA256: sha256Hex(registry), OpenAPIDocumentState: contracts.OpenAPIAbsent,
		SourceSummary: report.Summary,
	}
	if err := validateSummary(value, report, registry); err != nil {
		return nil, fmt.Errorf("validate summary: %w", err)
	}
	return marshalCanonical(value)
}

func renderSummaryMarkdown(report contracts.SourceEvidenceReport, registry []byte) []byte {
	summary := report.Summary
	counts := summary.ClassificationCounts
	coverage := "not_applicable"
	if summary.EndpointCoverage.State == contracts.CoverageRatio {
		coverage = fmt.Sprintf("%d/%d", summary.EndpointCoverage.Numerator, summary.EndpointCoverage.Denominator)
	}
	text := strings.Join([]string{
		"# Source operation summary", "",
		"source_report_sha256: " + sha256Hex(registry),
		"source_trust: " + string(report.SourceTrust),
		"openapi_document_state: absent",
		"selected_total: " + fmt.Sprint(summary.SelectedTotal),
		"applicable_total: " + fmt.Sprint(summary.ApplicableTotal),
		"source_call_observed_total: " + fmt.Sprint(summary.SourceCallObservedTotal),
		"endpoint_observed_total: " + fmt.Sprint(summary.EndpointObservedTotal),
		"endpoint_coverage: " + coverage,
		"observed_http: " + fmt.Sprint(counts.ObservedHTTP),
		"observed_sdk_call: " + fmt.Sprint(counts.ObservedSDKCall),
		"ambiguous: " + fmt.Sprint(counts.Ambiguous),
		"dynamic: " + fmt.Sprint(counts.Dynamic),
		"unresolved: " + fmt.Sprint(counts.Unresolved),
		"no_source: " + fmt.Sprint(counts.NoSource),
		"not_applicable: " + fmt.Sprint(counts.NotApplicable), "",
	}, "\n")
	return []byte(text)
}

func validateSummary(value Summary, report contracts.SourceEvidenceReport, registry []byte) error {
	if value.Kind != "infrawright.source_operation_summary" || value.SchemaVersion != 1 || value.OpenAPIDocumentState != contracts.OpenAPIAbsent {
		return fmt.Errorf("must identify absent-openapi summary-v1")
	}
	if value.SourceTrust != report.SourceTrust || !sameOptionalString(value.SourceManifestSHA256, report.SourceManifestSHA256) || value.SourceReportSHA256 != sha256Hex(registry) {
		return fmt.Errorf("must bind exact source report identity")
	}
	if value.SourceSummary != report.Summary {
		return fmt.Errorf("must preserve the exact source summary")
	}
	return nil
}
