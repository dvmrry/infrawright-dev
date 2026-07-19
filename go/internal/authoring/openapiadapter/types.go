package openapiadapter

import (
	"context"
	"fmt"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/reconcile"
)

// Operation is a detached, normalized operation inventory entry.
type Operation struct {
	Method       string
	PathTemplate string
	OperationID  *string
}

// OperationReference selects an exact operation for retained metadata.
// Its canonical spelling is METHOD:/literal/path.
type OperationReference string

// MetadataOptions controls explicit response and request operation extraction.
type MetadataOptions struct {
	ReadOperations  []OperationReference
	WriteOperations []OperationReference
}

// Document is an immutable, parsed in-memory OpenAPI document. It deliberately
// exposes neither a pathname nor the mutable raw parsing graph.
type Document struct {
	root         string
	raw          map[string][]byte
	files        map[string]any
	operations   []Operation
	metadataOnly bool
}

// Operations returns a detached deterministic operation inventory.
func (d Document) Operations() []Operation {
	result := make([]Operation, len(d.operations))
	for i, operation := range d.operations {
		result[i] = operation
		if operation.OperationID != nil {
			value := *operation.OperationID
			result[i].OperationID = &value
		}
	}
	return result
}

// Metadata extracts retained read/write field metadata. It accepts context so
// cancellation is observable before potentially recursive schema expansion.
func (d Document) Metadata(ctx context.Context, options MetadataOptions) (reconcile.APIMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("metadata extraction cancelled: %w", err)
	}
	d.metadataOnly = true
	return metadata(ctx, d, options)
}

// Result is a sealed adapter analysis. Its fields are private so callers cannot
// mint a diagnostic artifact without the source report validation performed by
// Analyze.
type Result struct {
	canonical []byte
	document  *Document
}

// CanonicalBytes returns a detached canonical diagnostics artifact.
func (r Result) CanonicalBytes() ([]byte, error) {
	if len(r.canonical) == 0 {
		return nil, fmt.Errorf("openapi adapter result must come from Analyze")
	}
	return append([]byte(nil), r.canonical...), nil
}

// Diagnostics returns a detached, strictly decoded report.
func (r Result) Diagnostics(ctx context.Context, source contracts.SourceEvidenceReport) (contracts.OpenAPIDiagnosticsReport, error) {
	if err := ctx.Err(); err != nil {
		return contracts.OpenAPIDiagnosticsReport{}, fmt.Errorf("openapi diagnostics cancelled: %w", err)
	}
	if len(r.canonical) == 0 {
		return contracts.OpenAPIDiagnosticsReport{}, fmt.Errorf("openapi adapter result must come from Analyze")
	}
	return contracts.DecodeOpenAPIDiagnosticsReport(append([]byte(nil), r.canonical...), source)
}

// Document returns a detached immutable document only when the selected input
// was fully usable. Degraded, absent, and unavailable inputs have no document.
func (r Result) Document() (Document, bool) {
	if r.document == nil {
		return Document{}, false
	}
	return cloneDocument(*r.document), true
}
