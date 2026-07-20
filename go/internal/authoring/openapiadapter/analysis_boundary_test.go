package openapiadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

func TestAnalyzeUsableJSONAndYAMLBindsObservedSourceReport(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{
		"widget": observedEndpoint("GET", "/widgets/{id}"),
	})
	jsonDocument := `{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"operationId":"getWidget","responses":{"200":{"description":"ok"}}}}}}`
	yamlDocument := "openapi: 3.0.3\ninfo:\n  title: widgets\n  version: '1'\npaths:\n  /widgets/{id}:\n    parameters:\n      - name: id\n        in: path\n        required: true\n        schema:\n          type: string\n    get:\n      operationId: getWidget\n      responses:\n        '200':\n          description: ok\n"
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "json", data: []byte(jsonDocument)},
		{name: "yaml", data: []byte(yamlDocument)},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := Analyze(context.Background(), available(test.data), source)
			if err != nil {
				t.Fatalf("Analyze(%s usable document) error = %v, want nil", test.name, err)
			}
			diagnostics, err := result.Diagnostics(context.Background(), source)
			if err != nil {
				t.Fatalf("Result.Diagnostics(%s usable document) error = %v, want nil", test.name, err)
			}
			if got, want := diagnostics.DocumentState, contracts.OpenAPIUsable; got != want {
				t.Errorf("Analyze(%s usable document).DocumentState = %q, want %q", test.name, got, want)
			}
			rendered, err := contracts.RenderSourceEvidenceReport(source)
			if err != nil {
				t.Fatalf("RenderSourceEvidenceReport(observed source) error = %v, want nil", err)
			}
			if got, want := diagnostics.SourceReportSHA256, testSHA256([]byte(rendered)); got != want {
				t.Errorf("Analyze(%s usable document).SourceReportSHA256 = %q, want exact source-report SHA %q", test.name, got, want)
			}
			row := diagnostics.Comparisons["widget"]
			if got, want := row.State, contracts.ComparisonCorroborated; got != want {
				t.Errorf("Analyze(%s usable document).Comparisons[widget].State = %q, want %q", test.name, got, want)
			}
			if got, want := row.Operations, []contracts.OpenAPIOperationCandidate{{Method: "GET", PathTemplate: "/widgets/{id}", OperationID: stringPointer("getWidget")}}; !reflect.DeepEqual(got, want) {
				t.Errorf("Analyze(%s usable document).Comparisons[widget].Operations = %#v, want %#v", test.name, got, want)
			}
			document, ok := result.Document()
			if !ok {
				t.Fatal("Result.Document(usable document) present = false, want true")
			}
			if got, want := document.Operations(), []Operation{{Method: "GET", PathTemplate: "/widgets/{id}", OperationID: stringPointer("getWidget")}}; !reflect.DeepEqual(got, want) {
				t.Errorf("Result.Document(%s usable document).Operations() = %#v, want %#v", test.name, got, want)
			}
		})
	}
}

func TestAnalyzeClosedRequiredRefsAndUnrelatedFailureIsolation(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets/{id}")})
	root := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"$ref":"item.yaml"}}}`)
	item := []byte("parameters:\n  - name: id\n    in: path\n    required: true\n    schema:\n      type: string\nget:\n  operationId: getWidget\n  responses:\n    '200':\n      $ref: response.json#/ok\n")
	response := []byte(`{"ok":{"description":"ok","content":{"application/json":{"schema":{"$ref":"schema.yaml#/Thing"}}}}}`)
	schema := []byte("Thing:\n  type: object\n  properties:\n    id:\n      type: string\n")
	result, err := Analyze(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", root), captured("item.yaml", item), captured("response.json", response), captured("schema.yaml", schema)}}, source)
	if err != nil {
		t.Fatalf("Analyze(mixed local JSON/YAML refs) error = %v, want nil", err)
	}
	diagnostics, err := result.Diagnostics(context.Background(), source)
	if err != nil {
		t.Fatalf("Result.Diagnostics(mixed local JSON/YAML refs) error = %v, want nil", err)
	}
	if got, want := diagnostics.DocumentState, contracts.OpenAPIUsable; got != want {
		t.Errorf("Analyze(mixed local JSON/YAML refs).DocumentState = %q, want %q", got, want)
	}

	degraded := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok"}}}},"/broken":{"get":"not-an-operation"}}}`)
	result, err = Analyze(context.Background(), available(degraded), source)
	if err != nil {
		t.Fatalf("Analyze(unrelated invalid operation) error = %v, want nil", err)
	}
	diagnostics, err = result.Diagnostics(context.Background(), source)
	if err != nil {
		t.Fatalf("Result.Diagnostics(unrelated invalid operation) error = %v, want nil", err)
	}
	if got, want := diagnostics.DocumentState, contracts.OpenAPIDegraded; got != want {
		t.Errorf("Analyze(unrelated invalid operation).DocumentState = %q, want %q", got, want)
	}
	if got, want := diagnostics.ReasonCode, openAPIReason(contracts.OpenAPIReasonDegradedOperation); !reflect.DeepEqual(got, want) {
		t.Errorf("Analyze(unrelated invalid operation).ReasonCode = %v, want %v", got, want)
	}
	if got, want := diagnostics.Comparisons["widget"].State, contracts.ComparisonCorroborated; got != want {
		t.Errorf("Analyze(unrelated invalid operation).Comparisons[widget].State = %q, want %q", got, want)
	}
	if _, ok := result.Document(); ok {
		t.Error("Result.Document(unrelated invalid operation) present = true, want false")
	}
}

func TestAnalyzeRequiredReferenceBoundaryRejectsUnsafeClasses(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets/{id}")})
	for _, test := range []struct {
		name string
		ref  string
	}{
		{name: "scheme", ref: "https://example.invalid/schema.json#/x"},
		{name: "authority", ref: "//example.invalid/schema.json#/x"},
		{name: "userinfo", ref: "https://user@example.invalid/schema.json#/x"},
		{name: "absolute", ref: "/schema.json#/x"},
		{name: "backslash", ref: "dir\\schema.json#/x"},
		{name: "nul", ref: "schema\x00.json#/x"},
		{name: "query", ref: "schema.json?query=1#/x"},
		{name: "percent", ref: "schema%2Ejson#/x"},
		{name: "dot", ref: "./schema.json#/x"},
		{name: "dotdot", ref: "../schema.json#/x"},
		{name: "double_slash", ref: "dir//schema.json#/x"},
		{name: "unlisted", ref: "schema.json#/x"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"$ref":` + quoteJSON(test.ref) + `}}}`)
			assertUnavailableLocalRef(t, source, available(input), test.name+" required path-item reference")
		})
	}
}

func TestAnalyzeRejectsDuplicateSyntaxAndRequiredReferenceShapes(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets/{id}")})
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "json duplicate key", data: []byte(`{"openapi":"3.0.3","openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{}}`)},
		{name: "yaml duplicate key", data: []byte("openapi: 3.0.3\nopenapi: 3.0.3\ninfo:\n  title: widgets\n  version: '1'\npaths: {}\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := Analyze(context.Background(), available(test.data), source)
			if err != nil {
				t.Fatalf("Analyze(%s) error = %v, want nil", test.name, err)
			}
			diagnostics, err := result.Diagnostics(context.Background(), source)
			if err != nil {
				t.Fatalf("Result.Diagnostics(%s) error = %v, want nil", test.name, err)
			}
			if got, want := diagnostics.DocumentState, contracts.OpenAPIUnavailable; got != want {
				t.Errorf("Analyze(%s).DocumentState = %q, want %q", test.name, got, want)
			}
			if got, want := diagnostics.ReasonCode, openAPIReason(contracts.OpenAPIReasonMalformed); !reflect.DeepEqual(got, want) {
				t.Errorf("Analyze(%s).ReasonCode = %v, want %v", test.name, got, want)
			}
		})
	}

	for _, test := range []struct {
		name string
		root string
	}{
		{name: "non-string path-item ref", root: `{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"$ref":7}}}`},
		{name: "operation ref", root: `{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"get":{"$ref":"operation.json#/get"}}}}`},
		{name: "path item ref sibling is ignored", root: `{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"$ref":"unlisted.json#/item","get":{"responses":{"200":{"description":"ignored sibling"}}}}}}`},
		{name: "invalid pointer escape", root: `{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"$ref":"#/components/pathItems/bad~2token"}},"components":{"pathItems":{}}}`},
		{name: "array pointer non-numeric", root: `{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"$ref":"#/components/pathItems/a"}},"components":{"pathItems":[]}}`},
		{name: "array pointer out-of-range", root: `{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"$ref":"#/components/pathItems/1"}},"components":{"pathItems":[{}]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertUnavailableLocalRef(t, source, available([]byte(test.root)), test.name)
		})
	}
}

func TestAnalyzeCaptureRejectsDuplicateIdentityHashMismatchAndUnsafePath(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets/{id}")})
	root := captured("root.json", []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{}}`))
	badHash := root
	badHash.SHA256 = strings.Repeat("0", 64)
	unsafe := root
	unsafe.Path = "../root.json"
	for _, test := range []struct {
		name  string
		files []sourcebind.CapturedFile
	}{
		{name: "duplicate root identity", files: []sourcebind.CapturedFile{root, root}},
		{name: "hash mismatch", files: []sourcebind.CapturedFile{badHash}},
		{name: "unsafe captured path", files: []sourcebind.CapturedFile{unsafe}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertUnavailableLocalRef(t, source, sourcebind.OpenAPIStatus{Available: true, Files: test.files}, test.name)
		})
	}
}

func TestAnalyzeRequiredReferenceCycleAndDepthLimit(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets/{id}")})
	cycle := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"$ref":"#/components/pathItems/loop"}},"components":{"pathItems":{"loop":{"$ref":"#/components/pathItems/loop"}}}}`)
	assertUnavailableLocalRef(t, source, available(cycle), "required path-item reference cycle")

	var components strings.Builder
	for i := 0; i <= maxRefDepth; i++ {
		if i != 0 {
			components.WriteByte(',')
		}
		if i == maxRefDepth {
			components.WriteString(`"r` + strconv.Itoa(i) + `":{"get":{"responses":{"200":{"description":"end"}}}}`)
			continue
		}
		components.WriteString(`"r` + strconv.Itoa(i) + `":{"$ref":"#/components/pathItems/r` + strconv.Itoa(i+1) + `"}`)
	}
	deep := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"$ref":"#/components/pathItems/r0"}},"components":{"pathItems":{` + components.String() + `}}}`)
	assertUnavailableLocalRef(t, source, available(deep), "required path-item reference depth limit")
}

func TestAnalyzeNestedInvalidRefsAreDegradedAfterOperationIdentity(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets/{id}")})
	for _, test := range []struct {
		name string
		bad  string
	}{
		{name: "response ref", bad: `"responses":{"200":{"$ref":"https://example.invalid/response.json#/ok"}}`},
		{name: "schema ref", bad: `"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"https://example.invalid/schema.json#/Thing"}}}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{` + test.bad + `}}}}`)
			assertDegradedCorroborated(t, source, available(input), test.name+" nested invalid ref")
		})
	}
}

func TestAnalyzeNestedEmptyQueryReferenceIsDegraded(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets/{id}")})
	root := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"$ref":"other.json?"}}}}}}`)
	other := []byte(`{"description":"ok"}`)
	status := sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", root), captured("other.json", other)}}
	assertDegradedCorroborated(t, source, status, "nested empty-query reference")
}

func TestAnalyzeCaptureAndResultBoundariesAreDetached(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets/{id}")})
	bytes := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	status := available(bytes)
	result, err := Analyze(context.Background(), status, source)
	if err != nil {
		t.Fatalf("Analyze(boundary document) error = %v, want nil", err)
	}
	status.Files[0].Bytes[0] = 'x'
	first, err := result.CanonicalBytes()
	if err != nil {
		t.Fatalf("Result.CanonicalBytes(before mutation) error = %v, want nil", err)
	}
	first[0] = 'x'
	second, err := result.CanonicalBytes()
	if err != nil {
		t.Fatalf("Result.CanonicalBytes(after returned-byte mutation) error = %v, want nil", err)
	}
	if first[0] == second[0] {
		t.Errorf("Result.CanonicalBytes(returned byte mutation) got leading byte %q, want fresh detached bytes", second[0])
	}
	document, ok := result.Document()
	if !ok {
		t.Fatal("Result.Document(boundary document) present = false, want true")
	}
	operations := document.Operations()
	operations[0].Method = "POST"
	again, ok := result.Document()
	if !ok || again.Operations()[0].Method != "GET" {
		t.Errorf("Result.Document().Operations() after caller mutation = %#v, want GET operation", again.Operations())
	}
	for _, unavailable := range []sourcebind.OpenAPIStatus{{}, {Available: true, Files: []sourcebind.CapturedFile{captured("root.json", []byte(`{"openapi":"9.0.0","info":{"title":"x","version":"1"},"paths":{}}`))}}} {
		failed, err := Analyze(context.Background(), unavailable, source)
		if err != nil {
			t.Fatalf("Analyze(unavailable boundary state) error = %v, want nil", err)
		}
		if _, ok := failed.Document(); ok {
			t.Error("Result.Document(non-usable document) present = true, want false")
		}
	}
}

func TestAnalyzeCancellationAndFreshLoaderParallelism(t *testing.T) {
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets/{id}")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Analyze(ctx, available([]byte(`{"openapi":"3.0.3","info":{"title":"x","version":"1"},"paths":{}}`)), source); err == nil {
		t.Error("Analyze(cancelled context) error = nil, want non-nil")
	}
	result, err := Analyze(context.Background(), available([]byte(`{"openapi":"3.0.3","info":{"title":"x","version":"1"},"paths":{"/widgets/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok"}}}}}}`)), source)
	if err != nil {
		t.Fatalf("Analyze(parallel seed) error = %v, want nil", err)
	}
	if _, err := result.Diagnostics(ctx, source); err == nil {
		t.Error("Result.Diagnostics(cancelled context) error = nil, want non-nil")
	}
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := Analyze(context.Background(), available([]byte(`{"openapi":"3.0.3","info":{"title":"x","version":"1"},"paths":{"/widgets/{id}":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"get":{"responses":{"200":{"description":"ok"}}}}}}`)), source)
			if err != nil {
				t.Errorf("Analyze(fresh loader parallel invocation) error = %v, want nil", err)
			}
		}()
	}
	group.Wait()
}

func assertUnavailableLocalRef(t *testing.T, source contracts.SourceEvidenceReport, status sourcebind.OpenAPIStatus, name string) {
	t.Helper()
	result, err := Analyze(context.Background(), status, source)
	if err != nil {
		t.Fatalf("Analyze(%s) error = %v, want nil", name, err)
	}
	diagnostics, err := result.Diagnostics(context.Background(), source)
	if err != nil {
		t.Fatalf("Result.Diagnostics(%s) error = %v, want nil", name, err)
	}
	if got, want := diagnostics.DocumentState, contracts.OpenAPIUnavailable; got != want {
		t.Errorf("Analyze(%s).DocumentState = %q, want %q", name, got, want)
	}
	if got, want := diagnostics.ReasonCode, openAPIReason(contracts.OpenAPIReasonLocalRefUnresolved); !reflect.DeepEqual(got, want) {
		t.Errorf("Analyze(%s).ReasonCode = %v, want %v", name, got, want)
	}
	if _, ok := result.Document(); ok {
		t.Errorf("Result.Document(%s) present = true, want false", name)
	}
}

func assertDegradedCorroborated(t *testing.T, source contracts.SourceEvidenceReport, status sourcebind.OpenAPIStatus, name string) {
	t.Helper()
	result, err := Analyze(context.Background(), status, source)
	if err != nil {
		t.Fatalf("Analyze(%s) error = %v, want nil", name, err)
	}
	diagnostics, err := result.Diagnostics(context.Background(), source)
	if err != nil {
		t.Fatalf("Result.Diagnostics(%s) error = %v, want nil", name, err)
	}
	if got, want := diagnostics.DocumentState, contracts.OpenAPIDegraded; got != want {
		t.Errorf("Analyze(%s).DocumentState = %q, want %q", name, got, want)
	}
	if got, want := diagnostics.ReasonCode, openAPIReason(contracts.OpenAPIReasonDegradedOperation); !reflect.DeepEqual(got, want) {
		t.Errorf("Analyze(%s).ReasonCode = %v, want %v", name, got, want)
	}
	if got, want := diagnostics.Comparisons["widget"].State, contracts.ComparisonCorroborated; got != want {
		t.Errorf("Analyze(%s).Comparisons[widget].State = %q, want %q", name, got, want)
	}
	if _, ok := result.Document(); ok {
		t.Errorf("Result.Document(%s) present = true, want false", name)
	}
}

func available(data []byte) sourcebind.OpenAPIStatus {
	return sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", data)}}
}

func observedReport(t *testing.T, endpoints map[string]contracts.HTTPEndpointEvidence) contracts.SourceEvidenceReport {
	t.Helper()
	resources := make(map[string]contracts.SourceEvidenceRow, len(endpoints))
	for resource, endpoint := range endpoints {
		readName := "read" + strings.ReplaceAll(resource, "_", "")
		function := readName
		read := contracts.SourceSymbol{PackagePath: "example.test/provider", Symbol: readName, Location: contracts.SourceLocation{Origin: contracts.SourceLocationProvider, Path: "provider/resource.go", Function: &function, Line: 1, Column: 1}}
		location := read.Location
		registration := contracts.SourceSymbol{PackagePath: "example.test/provider", Symbol: "resource" + resource, Location: contracts.SourceLocation{Origin: contracts.SourceLocationProvider, Path: "provider/resource.go", Line: 1, Column: 1}}
		endpoint.Origin = contracts.EndpointOriginProvider
		endpoint.Location = location
		resources[resource] = contracts.SourceEvidenceRow{Classification: contracts.SourceObservedHTTP, ProviderRegistration: &registration, ReadCallback: &read, Chains: []contracts.SourceEvidenceChain{{Steps: []contracts.SourceCallStep{{Kind: contracts.CallRawHTTP, Symbol: "client.NewRequest", Caller: read, Location: location}}, Endpoint: &endpoint}}}
	}
	count := len(resources)
	report := contracts.SourceEvidenceReport{Kind: "infrawright.source_evidence_report", SchemaVersion: 1, SourceTrust: contracts.SourceTrustUnverified, InputProvenanceSHA256: strings.Repeat("0", 64), Resources: resources, Summary: contracts.SourceSummary{SelectedTotal: count, ApplicableTotal: count, SourceCallObservedTotal: count, EndpointObservedTotal: count, ClassificationCounts: contracts.SourceClassificationCounts{ObservedHTTP: count}, EndpointCoverage: contracts.ExactCoverage{State: contracts.CoverageRatio, Numerator: count, Denominator: count}}}
	if err := contracts.ValidateSourceEvidenceReport(report); err != nil {
		t.Fatalf("observedReport(%#v) validation error = %v, want nil", endpoints, err)
	}
	return report
}

func observedEndpoint(method, path string) contracts.HTTPEndpointEvidence {
	return contracts.HTTPEndpointEvidence{Method: method, PathTemplate: path}
}

func stringPointer(value string) *string { return &value }

func testSHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func quoteJSON(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\x00", "\\u0000")
	return "\"" + replacer.Replace(value) + "\""
}
