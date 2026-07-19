package openapiadapter

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

func TestAnalyzeClosedCapabilityRejectsVirtualSchemeReferences(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets")})
	for _, test := range []struct {
		name string
		ref  string
	}{
		{name: "virtual scheme", ref: "infrawright-openapi://closed/other.json#/ok"},
		{name: "virtual scheme userinfo", ref: "infrawright-openapi://user@closed/other.json#/ok"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets":{"get":{"responses":{"200":{"$ref":` + quoteJSON(test.ref) + `}}}}}}`)
			other := []byte(`{"ok":{"description":"ok"}}`)
			status := sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", root), captured("other.json", other)}}
			assertDegradedCorroborated(t, source, status, test.name)
		})
	}
}

func TestAnalyzeIgnoredExtensionRefDoesNotExpandClosure(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets")})
	root := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets":{"get":{"x-ignored":{"$ref":"https://example.invalid/ignored.json#/value"},"responses":{"200":{"description":"ok"}}}}}}`)
	result, err := Analyze(context.Background(), available(root), source)
	if err != nil {
		t.Fatalf("Analyze(ignored extension reference) error = %v, want nil", err)
	}
	diagnostics, err := result.Diagnostics(context.Background(), source)
	if err != nil {
		t.Fatalf("Result.Diagnostics(ignored extension reference) error = %v, want nil", err)
	}
	if got, want := diagnostics.DocumentState, contracts.OpenAPIUsable; got != want {
		t.Errorf("Analyze(ignored extension reference).DocumentState = %q, want %q", got, want)
	}
	if got, want := diagnostics.Comparisons["widget"].State, contracts.ComparisonCorroborated; got != want {
		t.Errorf("Analyze(ignored extension reference).Comparisons[widget].State = %q, want %q", got, want)
	}
	if _, ok := result.Document(); !ok {
		t.Error("Result.Document(ignored extension reference) present = false, want true")
	}
}

func TestAnalyzeAllowsSpaceBearingCapturedKeysWithoutPercentAliases(t *testing.T) {
	t.Parallel()
	source := observedReport(t, map[string]contracts.HTTPEndpointEvidence{"widget": observedEndpoint("GET", "/widgets")})
	root := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets":{"get":{"responses":{"200":{"description":"ok"}}}}}}`)
	result, err := Analyze(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("open api.json", root)}}, source)
	if err != nil {
		t.Fatalf("Analyze(space-bearing root key) error = %v, want nil", err)
	}
	diagnostics, err := result.Diagnostics(context.Background(), source)
	if err != nil {
		t.Fatalf("Result.Diagnostics(space-bearing root key) error = %v, want nil", err)
	}
	if got, want := diagnostics.DocumentState, contracts.OpenAPIUsable; got != want {
		t.Errorf("Analyze(space-bearing root key).DocumentState = %q, want %q", got, want)
	}

	root = []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets":{"$ref":"item space.json"}}}`)
	item := []byte(`{"get":{"responses":{"200":{"description":"ok"}}}}`)
	result, err = Analyze(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", root), captured("item space.json", item)}}, source)
	if err != nil {
		t.Fatalf("Analyze(space-bearing local reference key) error = %v, want nil", err)
	}
	diagnostics, err = result.Diagnostics(context.Background(), source)
	if err != nil {
		t.Fatalf("Result.Diagnostics(space-bearing local reference key) error = %v, want nil", err)
	}
	if got, want := diagnostics.DocumentState, contracts.OpenAPIUsable; got != want {
		t.Errorf("Analyze(space-bearing local reference key).DocumentState = %q, want %q", got, want)
	}

	percent := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets":{"$ref":"item%20space.json"}}}`)
	assertUnavailableLocalRef(t, source, sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", percent), captured("item space.json", item)}}, "percent-encoded space alias")

	encodedFragment := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets":{"$ref":"#/%70aths/~1widgets"}}}`)
	assertUnavailableLocalRef(t, source, sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", encodedFragment)}}, "percent-encoded fragment alias")

	emptyQuery := []byte(`{"openapi":"3.0.3","info":{"title":"widgets","version":"1"},"paths":{"/widgets":{"$ref":"item.json?"}}}`)
	assertUnavailableLocalRef(t, source, sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", emptyQuery), captured("item.json", item)}}, "empty-query path alias")
}

func TestAnalyzeYAMLOverflowValidationLeavesAuthoritativeBytesUntouched(t *testing.T) {
	t.Parallel()
	input := []byte("openapi: 3.0.3\ninfo:\n  title: numbers\n  version: '1'\npaths: {}\ncomponents:\n  schemas:\n    Limits:\n      type: number\n      maximum: 1e400\n      minimum: -1e400\n      multipleOf: 1234567890123456789012345678901234567890\n")
	wantInput := append([]byte(nil), input...)
	result, err := Analyze(context.Background(), available(input), emptySourceReport())
	if err != nil {
		t.Fatalf("Analyze(YAML overflow constraints) error = %v, want nil", err)
	}
	if got, want := input, wantInput; !reflect.DeepEqual(got, want) {
		t.Errorf("Analyze(YAML overflow constraints) caller bytes = %q, want unchanged %q", got, want)
	}
	diagnostics, err := result.Diagnostics(context.Background(), emptySourceReport())
	if err != nil {
		t.Fatalf("Result.Diagnostics(YAML overflow constraints) error = %v, want nil", err)
	}
	if got, want := diagnostics.DocumentState, contracts.OpenAPIUsable; got != want {
		t.Errorf("Analyze(YAML overflow constraints).DocumentState = %q, want %q", got, want)
	}
}

func TestParseDocumentYAMLRetainsNumericLexemesAndStrings(t *testing.T) {
	t.Parallel()
	value, err := parseDocument([]byte("maximum: 1e400\nminimum: -1e400\nhuge: 1234567890123456789012345678901234567890\nquoted: \"1e400\"\nexplicit: !!str 1e400\n"))
	if err != nil {
		t.Fatalf("parseDocument(YAML numeric lexemes) error = %v, want nil", err)
	}
	graph, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("parseDocument(YAML numeric lexemes) type = %T, want map[string]any", value)
	}
	for _, test := range []struct {
		key  string
		want string
	}{
		{key: "maximum", want: "1e400"},
		{key: "minimum", want: "-1e400"},
		{key: "huge", want: "1234567890123456789012345678901234567890"},
	} {
		got, ok := graph[test.key].(json.Number)
		if !ok || string(got) != test.want {
			t.Errorf("parseDocument(YAML numeric lexemes)[%q] = %#v (%T), want json.Number(%q)", test.key, graph[test.key], graph[test.key], test.want)
		}
	}
	for _, key := range []string{"quoted", "explicit"} {
		if got, want := graph[key], "1e400"; got != want {
			t.Errorf("parseDocument(YAML string lexeme)[%q] = %#v, want %q", key, got, want)
		}
	}
}
