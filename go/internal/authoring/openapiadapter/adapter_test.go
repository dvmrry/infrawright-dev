package openapiadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

func TestParseForMetadataUsesOnlyRequestedClosure(t *testing.T) {
	t.Parallel()
	root := []byte(`{"openapi":"3.0.3","paths":{"/things":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"displayName":{"type":"string"}}}}}}}}}}}`)
	status := sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", root)}}
	document, err := ParseForMetadata(context.Background(), status)
	if err != nil {
		t.Fatalf("ParseForMetadata(partial OpenAPI document) error = %v, want nil", err)
	}
	metadata, err := document.Metadata(context.Background(), MetadataOptions{ReadOperations: []OperationReference{"GET:/things"}})
	if err != nil {
		t.Fatalf("Document.Metadata(GET:/things) error = %v, want nil", err)
	}
	if got := metadata["display_name"]["readable"]; got != true {
		t.Errorf("Document.Metadata(GET:/things)[display_name].readable = %v, want true", got)
	}
}

func TestPointerRejectsOverflowAndInvalidEscape(t *testing.T) {
	t.Parallel()
	if _, err := pointer([]any{"one"}, "/999999999999999999999999999999999999999999999999999"); err == nil {
		t.Error("pointer(overflow index) error = nil, want non-nil")
	}
	if _, err := pointer(map[string]any{}, "/bad~2token"); err == nil {
		t.Error("pointer(invalid escape) error = nil, want non-nil")
	}
}

func TestValidRootRequiresSupportedVersionAndInfo(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]any{
		"unsupported version": map[string]any{"openapi": "3.9.0", "info": map[string]any{"title": "x", "version": "1"}, "paths": map[string]any{}},
		"missing title":       map[string]any{"openapi": "3.0.3", "info": map[string]any{"version": "1"}, "paths": map[string]any{}},
	} {
		if validRoot(value) {
			t.Errorf("validRoot(%s) = true, want false", name)
		}
	}
}

func TestRequiredPathItemClosureRejectsBrokenPathItem(t *testing.T) {
	t.Parallel()
	root := map[string]any{"paths": map[string]any{"/things/{id}": map[string]any{"$ref": "broken.json#/item"}}}
	document := Document{root: "root.json", raw: map[string][]byte{"root.json": []byte(`{}`), "broken.json": []byte(`{"response":`)}, files: map[string]any{"root.json": root}}
	item := root["paths"].(map[string]any)["/things/{id}"]
	if _, _, err := resolvePathItem(document, "root.json", item, map[string]bool{}, 0); err == nil {
		t.Error("resolvePathItem(broken required path item) error = nil, want non-nil")
	}
}

func TestAnalyzeZeroRowFailureIsolation(t *testing.T) {
	t.Parallel()
	source := emptySourceReport()
	malformed := []byte(`{"openapi":`)
	cases := []struct {
		name   string
		status sourcebind.OpenAPIStatus
		want   contracts.OpenAPIDocumentState
		reason *contracts.OpenAPIReasonCode
	}{
		{name: "absent", status: sourcebind.OpenAPIStatus{}, want: contracts.OpenAPIAbsent},
		{name: "unreadable", status: sourcebind.OpenAPIStatus{Err: os.ErrNotExist}, want: contracts.OpenAPIUnavailable, reason: openAPIReason(contracts.OpenAPIReasonUnreadable)},
		{name: "malformed", status: sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", malformed)}}, want: contracts.OpenAPIUnavailable, reason: openAPIReason(contracts.OpenAPIReasonMalformed)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := Analyze(context.Background(), test.status, source)
			if err != nil {
				t.Fatalf("Analyze(%s) error = %v, want nil", test.name, err)
			}
			diagnostics, err := result.Diagnostics(context.Background(), source)
			if err != nil {
				t.Fatalf("Result.Diagnostics(%s) error = %v, want nil", test.name, err)
			}
			if diagnostics.DocumentState != test.want || !reflect.DeepEqual(diagnostics.ReasonCode, test.reason) {
				t.Errorf("Analyze(%s) state/reason = %q/%v, want %q/%v", test.name, diagnostics.DocumentState, diagnostics.ReasonCode, test.want, test.reason)
			}
		})
	}
}

func TestSwagger2MetadataCompatibility(t *testing.T) {
	t.Parallel()
	input := []byte(`{"swagger":"2.0","paths":{"/widgets":{"post":{"parameters":[{"in":"body","name":"body","schema":{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}}],"responses":{"200":{"description":"ok"}}}}}}`)
	document, err := ParseForMetadata(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", input)}})
	if err != nil {
		t.Fatalf("ParseForMetadata(Swagger 2) error = %v, want nil", err)
	}
	got, err := document.Metadata(context.Background(), MetadataOptions{WriteOperations: []OperationReference{"POST:/widgets"}})
	if err != nil {
		t.Fatalf("Document.Metadata(Swagger 2) error = %v, want nil", err)
	}
	if got["name"]["writable"] != true || got["name"]["required"] != true {
		t.Errorf("Document.Metadata(Swagger 2)[name] = %#v, want writable and required", got["name"])
	}
}

func TestAnalyzeSanitizesOverflowOnlyForKinValidation(t *testing.T) {
	t.Parallel()
	input := []byte(`{"openapi":"3.0.3","info":{"title":"test","version":"1"},"paths":{},"components":{"schemas":{"Thing":{"type":"number","maximum":1e400}}}}`)
	result, err := Analyze(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", input)}}, emptySourceReport())
	if err != nil {
		t.Fatalf("Analyze(overflow constraint) error = %v, want nil", err)
	}
	diagnostics, err := result.Diagnostics(context.Background(), emptySourceReport())
	if err != nil {
		t.Fatalf("Result.Diagnostics(overflow constraint) error = %v, want nil", err)
	}
	if diagnostics.DocumentState != contracts.OpenAPIUsable {
		t.Errorf("Analyze(overflow constraint).DocumentState = %q, want usable", diagnostics.DocumentState)
	}
}

func TestRequiredInventoryDeduplicatesSharedEndpointAndCarriesPathItemBase(t *testing.T) {
	t.Parallel()
	root := map[string]any{"paths": map[string]any{"/things/{thing}": map[string]any{"$ref": "item.yaml"}}}
	document := Document{root: "root.json", raw: map[string][]byte{
		"root.json":      []byte(`{}`),
		"item.yaml":      []byte("get:\n  operationId: getThing\n  responses:\n    '200':\n      $ref: responses.yaml#/ok\n"),
		"responses.yaml": []byte(`{"ok":{"description":"ok","content":{"application/json":{"schema":{"$ref":"thing.json#/Thing"}}}}}`),
		"thing.json":     []byte(`{"Thing":{"type":"object"}}`),
	}, files: map[string]any{"root.json": root}}
	endpoint := &contracts.HTTPEndpointEvidence{Method: "GET", PathTemplate: "/things/{id}"}
	source := contracts.SourceEvidenceReport{Resources: map[string]contracts.SourceEvidenceRow{"one": {Classification: contracts.SourceObservedHTTP, Chains: []contracts.SourceEvidenceChain{{Endpoint: endpoint}}}, "two": {Classification: contracts.SourceObservedHTTP, Chains: []contracts.SourceEvidenceChain{{Endpoint: endpoint}}}}}
	operations, err := inventoryRequired(context.Background(), document, source)
	if err != nil {
		t.Fatalf("inventoryRequired(mixed local refs) error = %v, want nil", err)
	}
	if len(operations) != 1 || operations[0].PathTemplate != "/things/{thing}" {
		t.Errorf("inventoryRequired(shared endpoint) = %#v, want one literal candidate", operations)
	}
}

func TestMetadataRejectsUnsupportedExternalFiles(t *testing.T) {
	t.Parallel()
	status := sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", []byte(`{"openapi":"3.0.3","paths":{"/things":{"get":{"responses":{"200":{"$ref":"other.json#/response"}}}}}}`)), captured("other.json", []byte(`{"response":{"description":"ok"}}`))}}
	document, err := ParseForMetadata(context.Background(), status)
	if err != nil {
		t.Fatalf("ParseForMetadata(unreferenced extra) error = %v, want nil", err)
	}
	if _, err := document.Metadata(context.Background(), MetadataOptions{ReadOperations: []OperationReference{"GET:/things"}}); err == nil {
		t.Error("Document.Metadata(external ref) error = nil, want non-nil")
	}
}

func TestDocumentOperationsAreDetachedAndContextCancellationIsObserved(t *testing.T) {
	t.Parallel()
	id := "getThing"
	document := Document{operations: []Operation{{Method: "GET", PathTemplate: "/things", OperationID: &id}}}
	first := document.Operations()
	*first[0].OperationID = "mutated"
	first[0].Method = "POST"
	second := document.Operations()
	if second[0].Method != "GET" || *second[0].OperationID != "getThing" {
		t.Errorf("Document.Operations defensive copy = %#v", second)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseForMetadata(ctx, sourcebind.OpenAPIStatus{}); err == nil {
		t.Error("ParseForMetadata(cancelled) error = nil, want non-nil")
	}
}

func TestAnalyzeInvalidRootAndUnrelatedDegraded(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]struct {
		input []byte
		want  contracts.OpenAPIDocumentState
	}{
		"invalid root": {[]byte(`{"openapi":"3.9.0","info":{"title":"x","version":"1"},"paths":{}}`), contracts.OpenAPIUnavailable},
		"degraded":     {[]byte(`{"openapi":"3.0.3","info":{"title":"x","version":"1"},"paths":{"/bad":7}}`), contracts.OpenAPIDegraded},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := Analyze(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", input.input)}}, emptySourceReport())
			if err != nil {
				t.Fatalf("Analyze(%s) error = %v", name, err)
			}
			diagnostics, err := result.Diagnostics(context.Background(), emptySourceReport())
			if err != nil {
				t.Fatalf("Diagnostics(%s) error = %v", name, err)
			}
			if diagnostics.DocumentState != input.want {
				t.Errorf("Analyze(%s).state = %q, want %q", name, diagnostics.DocumentState, input.want)
			}
		})
	}
}

func emptySourceReport() contracts.SourceEvidenceReport {
	return contracts.SourceEvidenceReport{Kind: "infrawright.source_evidence_report", SchemaVersion: 1, SourceTrust: contracts.SourceTrustUnverified, InputProvenanceSHA256: "0000000000000000000000000000000000000000000000000000000000000000", Resources: map[string]contracts.SourceEvidenceRow{}, Summary: contracts.SourceSummary{ClassificationCounts: contracts.SourceClassificationCounts{}, EndpointCoverage: contracts.ExactCoverage{State: contracts.CoverageNotApplicable}}}
}
func openAPIReason(value contracts.OpenAPIReasonCode) *contracts.OpenAPIReasonCode { return &value }

func captured(path string, bytes []byte) sourcebind.CapturedFile {
	digest := sha256.Sum256(bytes)
	return sourcebind.CapturedFile{Path: path, Bytes: append([]byte(nil), bytes...), SHA256: hex.EncodeToString(digest[:])}
}
