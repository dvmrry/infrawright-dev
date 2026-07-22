package providerprobe

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readLegacyOpenAPICompatibilityFixture(t *testing.T, name, wantSHA256 string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", name, err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != wantSHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", name, got, wantSHA256)
	}
	return data
}

func TestLegacyOpenAPIValidatorFrozenAssets(t *testing.T) {
	want := map[string]string{
		"v2.0/schema.json": "b36871c8016292c5e66dd3b203e69aeff98bfef97e0b3c67c1909036095586a5",
		"v3.0/schema.json": "d03136244e74914d37003908554bf184c4496c6a8fe03fb3910c810561a86bed",
		"v3.1/schema.json": "eb5c4544fa2560f8dbd25da98014b7efc07d1ab1e6d7320afec559ee0df2a1fc",
	}
	for name, expected := range want {
		if got := legacyOpenAPISchemaHashes()[name]; got != expected {
			t.Errorf("legacyOpenAPISchemaHashes()[%q] = %q, want %q", name, got, expected)
		}
	}
}

// TestLegacyOpenAPIValidatorCompatibilityCorpus replays the independently
// captured SwaggerParser 12.1.0 acceptance and failure-phase matrix. The
// external runtime is retired; this fixed corpus remains as Go regression
// evidence for the compatibility behavior that the product still exposes.
// The capture harness SHA-256 was
// e4bd570706967632bed7daac56875963099bcc266e2dd377d21704c24fca24eb;
// its captured acceptance/phase result SHA-256 was
// b8b174628d6b6004d557f0b77a7aa655de0475f8b296eb01b672449ffdcf6a2f.
func TestLegacyOpenAPIValidatorCompatibilityCorpus(t *testing.T) {
	type compatibilityCase struct {
		Name          string          `json:"name"`
		Expected      bool            `json:"expected"`
		Document      json.RawMessage `json:"document"`
		ExpectedPhase string          `json:"expectedPhase"`
	}
	data := readLegacyOpenAPICompatibilityFixture(
		t,
		"legacy_openapi_compatibility_cases.json",
		"fc9e0f6fbaa804af62738b514260f18e4fcd04d98fdfbe0f24a5af67d6080f0c",
	)
	var cases []compatibilityCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("json.Unmarshal(legacy OpenAPI compatibility corpus) error: %v", err)
	}
	if got, want := len(cases), 90; got != want {
		t.Fatalf("legacy OpenAPI compatibility cases = %d, want %d", got, want)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			document := legacyValidationDocument(t, string(test.Document))
			document = legacyRestoreCompatibilityLossless(document).(map[string]any)
			err := validateLegacyOpenAPI(document)
			if got := err == nil; got != test.Expected {
				t.Fatalf("validateLegacyOpenAPI(%s) accepted = %t, want %t (error = %v)", test.Name, got, test.Expected, err)
			}
			if !test.Expected {
				if got := legacyValidationPhase(err); got != test.ExpectedPhase {
					t.Fatalf("validateLegacyOpenAPI(%s) phase = %q, want %q (error = %v)", test.Name, got, test.ExpectedPhase, err)
				}
			}
		})
	}
}

// TestLegacySwagger2SpecCompatibilityCorpus replays spec-validation branches
// that the JSON schema rejects before the full validation pipeline reaches
// them.
func TestLegacySwagger2SpecCompatibilityCorpus(t *testing.T) {
	type compatibilityCase struct {
		Name     string          `json:"name"`
		Accepted bool            `json:"accepted"`
		Document json.RawMessage `json:"document"`
	}
	data := readLegacyOpenAPICompatibilityFixture(
		t,
		"legacy_swagger2_spec_compatibility_cases.json",
		"bf6daae6d0de5315740d6e97b655ee70e5ef3395296d38ace7999e005355d459",
	)
	var cases []compatibilityCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("json.Unmarshal(legacy Swagger 2 compatibility corpus) error: %v", err)
	}
	if got, want := len(cases), 18; got != want {
		t.Fatalf("legacy Swagger 2 compatibility cases = %d, want %d", got, want)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			err := validateLegacySwagger2Spec(legacyValidationDocument(t, string(test.Document)))
			if got := err == nil; got != test.Accepted {
				t.Fatalf("validateLegacySwagger2Spec(%s) accepted = %t, want %t (error = %v)", test.Name, got, test.Accepted, err)
			}
		})
	}
}

func TestLegacySwagger2SpecDistinguishesUndefinedTypeFromNullType(t *testing.T) {
	api := legacyValidationDocument(t, `{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{}}`)
	if err := validateLegacySwagger2Schema(map[string]any{}, "body", legacySchemaTypes[:], true); err != nil {
		t.Fatalf("validateLegacySwagger2Schema(undefined body type) error = %v, want nil", err)
	}
	for name, schema := range map[string]map[string]any{
		"query null":    {"type": nil},
		"response null": {"type": nil},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLegacySwagger2Schema(schema, name, legacyPrimitiveTypes[:], false); err == nil {
				t.Fatalf("validateLegacySwagger2Schema(%s) error = nil, want null-type rejection", name)
			}
		})
	}
	if err := validateLegacySwagger2Spec(api); err != nil {
		t.Fatalf("validateLegacySwagger2Spec(base) error = %v", err)
	}
}

func legacyRestoreCompatibilityLossless(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if marker, marked := typed["isLosslessNumber"].(bool); marked && marker && len(typed) == 2 {
			if token, ok := typed["value"].(string); ok {
				return json.Number(token)
			}
		}
		copy := make(map[string]any, len(typed))
		for key, child := range typed {
			copy[key] = legacyRestoreCompatibilityLossless(child)
		}
		return copy
	case []any:
		copy := make([]any, len(typed))
		for index, child := range typed {
			copy[index] = legacyRestoreCompatibilityLossless(child)
		}
		return copy
	default:
		return value
	}
}

func legacyValidationPhase(err error) string {
	if err == nil {
		return "accept"
	}
	message := err.Error()
	for _, phase := range []string{"version", "deref", "schema", "spec"} {
		if strings.HasPrefix(message, "legacy OpenAPI validation "+phase+":") {
			return phase
		}
	}
	return "unknown"
}

func TestLegacyOpenAPIValidatorAcceptanceCases(t *testing.T) {
	validSwagger := `{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"operationId":"read","responses":{"200":{"description":"ok"}}}}}}`
	validOpenAPI30 := `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{}}`
	validOpenAPI31Webhook := `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"webhooks":{"notice":{"post":{"responses":{"200":{"description":"ok"}}}}}}`
	validOpenAPI31BooleanSchema := `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Foo"}}}}}}}},"components":{"schemas":{"Foo":true}}}`
	validLocalRefs := `{"swagger":"2.0","info":{"title":"x","version":"1"},"x-parameters":[{"name":"id","in":"query","type":"string"}],"parameters":{"q":{"name":"q","in":"query","type":"string"}},"definitions":{"A/B~C":{"type":"string"},"Cycle":{"$ref":"#/definitions/Cycle"}},"paths":{"/x":{"get":{"parameters":[{"$ref":"#/x-parameters/0"},{"$ref":"#/parameters/q","description":"sibling"}],"responses":{"200":{"description":"ok","schema":{"$ref":"#/definitions/A~1B~0C"}},"201":{"$ref":"other.json#/response"}}}}}}`
	for name, source := range map[string]string{
		"swagger 2":               validSwagger,
		"OpenAPI 3.0":             validOpenAPI30,
		"OpenAPI 3.1 webhook":     validOpenAPI31Webhook,
		"OpenAPI 3.1 boolean ref": validOpenAPI31BooleanSchema,
		"local refs and siblings": validLocalRefs,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLegacyOpenAPI(legacyValidationDocument(t, source)); err != nil {
				t.Fatalf("validateLegacyOpenAPI(%s) error = %v, want nil", name, err)
			}
		})
	}
}

// TestLegacyOpenAPIValidatorJSONSchemaRefParserPointerCompatibility covers
// Pointer.parse/resolve behavior in json-schema-ref-parser 14.0.1, as reached
// by SwaggerParser 12.1.0. The upstream resolver URI-decodes each token after
// RFC 6901 tilde decoding and retains a longest-joined raw-slash fallback.
func TestLegacyOpenAPIValidatorJSONSchemaRefParserPointerCompatibility(t *testing.T) {
	for name, source := range map[string]string{
		"URI-decoded space":        `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/a%20b"}}}}}}}},"components":{"schemas":{"a b":{"type":"string"}}}}`,
		"URI-decoded slash":        `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/a%2Fb"}}}}}}}},"components":{"schemas":{"a/b":{"type":"string"}}}}`,
		"raw slash fallback":       `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/a/b"}}}}}}}},"components":{"schemas":{"a/b":{"type":"string"}}}}`,
		"malformed escape literal": `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/a%2Gb"}}}}}}}},"components":{"schemas":{"a%2Gb":{"type":"string"}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLegacyOpenAPI(legacyValidationDocument(t, source)); err != nil {
				t.Fatalf("validateLegacyOpenAPI(%s) error = %v, want nil", name, err)
			}
		})
	}

	root := map[string]any{
		"a/b": "joined",
		"a":   map[string]any{"b": "nested"},
	}
	if got, err := resolveLegacyJSONPointer(root, "#/a/b"); err != nil || got != "nested" {
		t.Fatalf("resolveLegacyJSONPointer(exact prefix) = %v, %v; want nested, nil", got, err)
	}
	delete(root, "a")
	if got, err := resolveLegacyJSONPointer(root, "#/a/b"); err != nil || got != "joined" {
		t.Fatalf("resolveLegacyJSONPointer(joined fallback) = %v, %v; want joined, nil", got, err)
	}
}

func TestLegacyOpenAPIValidatorJSONSchemaRefParserReferenceRecognitionAndIndirection(t *testing.T) {
	for _, ref := range []string{"#nope", "##/foo", "#%2Ffoo"} {
		t.Run("retained "+ref, func(t *testing.T) {
			document := map[string]any{
				"openapi": "3.0.4",
				"info":    map[string]any{"title": "x", "version": "1"},
				"paths":   map[string]any{},
				"x-out":   map[string]any{"$ref": ref},
			}
			if err := validateLegacyOpenAPI(document); err != nil {
				t.Fatalf("validateLegacyOpenAPI(non-pointer hash %q) error = %v, want nil", ref, err)
			}
		})
	}

	document := map[string]any{
		"openapi": "3.0.4",
		"info":    map[string]any{"title": "x", "version": "1"},
		"paths":   map[string]any{},
		"x-out":   map[string]any{"$ref": "#/components/schemas/A/type"},
		"components": map[string]any{
			"schemas": map[string]any{
				"A": map[string]any{"$ref": "#/components/schemas/B"},
				"B": map[string]any{"type": "object"},
			},
		},
	}
	if err := validateLegacyOpenAPI(document); err != nil {
		t.Fatalf("validateLegacyOpenAPI(pointer through intermediate ref) error = %v, want nil", err)
	}
}

// TestLegacyOpenAPIValidatorJSONSchemaRefParserExtendedReferenceCompatibility
// covers $Ref.dereference's JavaScript object boundary. Primitive targets
// replace an extended reference and discard its siblings; arrays are objects
// and merge their enumerable numeric keys into a plain object.
func TestLegacyOpenAPIValidatorJSONSchemaRefParserExtendedReferenceCompatibility(t *testing.T) {
	for name, source := range map[string]string{
		"false target discards sibling":       `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/T","description":"ignored"}}}}}}}},"components":{"schemas":{"T":false}}}`,
		"true target discards sibling":        `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/T","description":"ignored"}}}}}}}},"components":{"schemas":{"T":true}}}`,
		"false target does not crawl sibling": `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/T","x-discarded":{"$ref":"#/missing"}}}}}}}}},"components":{"schemas":{"T":false}}}`,
		"true target does not crawl sibling":  `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/T","x-discarded":{"$ref":"#/missing"}}}}}}}}},"components":{"schemas":{"T":true}}}`,
		"empty array target merges":           `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/x-target","type":"string"}}}}}}}},"x-target":[]}`,
		"nonempty array target merges":        `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/x-target","type":"string"}}}}}}}},"x-target":[{"ignored":true}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLegacyOpenAPI(legacyValidationDocument(t, source)); err != nil {
				t.Fatalf("validateLegacyOpenAPI(%s) error = %v, want nil", name, err)
			}
		})
	}

	arrayWithoutSiblings := `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/x-target"}}}}}}}},"x-target":[]}`
	if err := validateLegacyOpenAPI(legacyValidationDocument(t, arrayWithoutSiblings)); legacyValidationPhase(err) != "schema" {
		t.Fatalf("validateLegacyOpenAPI(array ref without siblings) error = %v, want schema phase", err)
	}

	for name, target := range map[string]any{
		"null":   nil,
		"string": "target",
		"number": float64(1),
	} {
		t.Run(name+" target discards siblings", func(t *testing.T) {
			root := map[string]any{
				"target": target,
				"value":  map[string]any{"$ref": "#/target", "type": "string"},
			}
			resolved, err := dereferenceLegacyOpenAPI(root)
			if err != nil {
				t.Fatalf("dereferenceLegacyOpenAPI(%s target) error = %v", name, err)
			}
			if got := resolved["value"]; !reflect.DeepEqual(got, target) {
				t.Fatalf("dereferenceLegacyOpenAPI(%s target) value = %#v, want %#v", name, got, target)
			}
		})
	}
}

// TestLegacySwagger2RequiredAllOfCycleCompatibility safely reproduces the
// pinned spec.js acceptance boundary. Its unguarded collectProperties recurses
// only when required is present, so circular allOf rejects in that branch but
// remains accepted otherwise.
func TestLegacySwagger2RequiredAllOfCycleCompatibility(t *testing.T) {
	for name, source := range map[string]string{
		"self required":   `{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{},"definitions":{"A":{"type":"object","properties":{"x":{"type":"string"}},"required":["x"],"allOf":[{"$ref":"#/definitions/A"}]}}}`,
		"mutual required": `{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{},"definitions":{"A":{"type":"object","properties":{"x":{"type":"string"}},"required":["x"],"allOf":[{"$ref":"#/definitions/B"}]},"B":{"type":"object","allOf":[{"$ref":"#/definitions/A"}]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLegacyOpenAPI(legacyValidationDocument(t, source)); legacyValidationPhase(err) != "spec" {
				t.Fatalf("validateLegacyOpenAPI(%s) error = %v, want spec phase", name, err)
			}
		})
	}

	for name, source := range map[string]string{
		"self without required":   `{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{},"definitions":{"A":{"type":"object","allOf":[{"$ref":"#/definitions/A"}]}}}`,
		"mutual without required": `{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{},"definitions":{"A":{"type":"object","allOf":[{"$ref":"#/definitions/B"}]},"B":{"type":"object","allOf":[{"$ref":"#/definitions/A"}]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLegacyOpenAPI(legacyValidationDocument(t, source)); err != nil {
				t.Fatalf("validateLegacyOpenAPI(%s) error = %v, want nil", name, err)
			}
		})
	}
}

// TestLegacyOpenAPIValidatorCachesPlainReferenceTargets protects the ordinary
// high-fanout case handled by ref-parser's dereference cache. The source graph
// is modest, but cloning the shared component once per operation would exceed
// the defensive work bound and reject an excessively deep document.
func TestLegacyOpenAPIValidatorCachesPlainReferenceTargets(t *testing.T) {
	properties := make(map[string]any, 100)
	for index := 0; index < 100; index++ {
		properties[fmt.Sprintf("field_%03d", index)] = map[string]any{"type": "string"}
	}
	paths := make(map[string]any, 500)
	for index := 0; index < 500; index++ {
		paths[fmt.Sprintf("/item/%03d", index)] = map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/Shared"},
							},
						},
					},
				},
			},
		}
	}
	document := map[string]any{
		"openapi": "3.0.4",
		"info":    map[string]any{"title": "x", "version": "1"},
		"paths":   paths,
		"components": map[string]any{
			"schemas": map[string]any{
				"Shared": map[string]any{"type": "object", "properties": properties},
			},
		},
	}
	if err := validateLegacyOpenAPI(document); err != nil {
		t.Fatalf("validateLegacyOpenAPI(high-fanout shared component) error = %v, want nil", err)
	}
}

func TestLegacyOpenAPIValidatorRejectsInvalidExtendedSiblingDeterministically(t *testing.T) {
	for name, paths := range map[string]string{
		"plain reference first":    `"/a":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/T"}}}}}}},"/b":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/T","type":17}}}}}}}`,
		"extended reference first": `"/a":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/T","type":17}}}}}}},"/b":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/T"}}}}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			source := `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{` + paths + `},"components":{"schemas":{"T":{"type":"string"}}}}`
			if err := validateLegacyOpenAPI(legacyValidationDocument(t, source)); legacyValidationPhase(err) != "schema" {
				t.Fatalf("validateLegacyOpenAPI(%s) error = %v, want deterministic schema rejection", name, err)
			}
		})
	}
}

func TestLegacyOpenAPIValidatorRejectsJavaScriptNativePropertyPointerExtensions(t *testing.T) {
	for name, source := range map[string]string{
		"string index":  `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{},"x-string":"abc","x-out":{"$ref":"#/x-string/0"}}`,
		"string length": `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{},"x-string":"abc","x-out":{"$ref":"#/x-string/length"}}`,
		"array length":  `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{},"x-array":["a"],"x-out":{"$ref":"#/x-array/length"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLegacyOpenAPI(legacyValidationDocument(t, source)); legacyValidationPhase(err) != "deref" {
				t.Fatalf("validateLegacyOpenAPI(%s) error = %v, want strict JSON Pointer deref rejection", name, err)
			}
		})
	}
}

func TestLegacyOpenAPIValidatorRetainsDefensiveDepthCeiling(t *testing.T) {
	document := map[string]any{
		"openapi": "3.0.4",
		"info":    map[string]any{"title": "x", "version": "1"},
		"paths":   map[string]any{},
	}
	current := map[string]any{}
	document["x-deep"] = current
	for depth := 0; depth < legacyValidationMaxDepth+2; depth++ {
		next := map[string]any{}
		current["next"] = next
		current = next
	}
	if err := validateLegacyOpenAPI(document); legacyValidationPhase(err) != "deref" {
		t.Fatalf("validateLegacyOpenAPI(over-depth extension) error = %v, want defensive deref rejection", err)
	}
}

func TestLegacyOpenAPIValidatorRejectionCases(t *testing.T) {
	base := `{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	cases := []struct {
		name  string
		data  string
		phase string
	}{
		{name: "adjacent Swagger", data: strings.Replace(base, `"2.0"`, `"2.1"`, 1), phase: "version"},
		{name: "numeric Swagger", data: strings.Replace(base, `"2.0"`, `2`, 1), phase: "version"},
		{name: "numeric API version", data: strings.Replace(base, `"version":"1"`, `"version":1`, 1), phase: "version"},
		{name: "missing local reference", data: strings.Replace(base, `"responses"`, `"parameters":[{"$ref":"#/missing"}],"responses"`, 1), phase: "deref"},

		{name: "duplicate operation ID", data: `{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"operationId":"same","responses":{"200":{"description":"ok"}}}},"/y":{"get":{"operationId":"same","responses":{"200":{"description":"ok"}}}}}}`, phase: "spec"},
		{name: "duplicate operation parameter", data: strings.Replace(base, `"responses"`, `"parameters":[{"name":"q","in":"query","type":"string"},{"name":"q","in":"query","type":"string"}],"responses"`, 1), phase: "schema"},
		{name: "missing path parameter", data: strings.Replace(base, `"/x"`, `"/x/{id}"`, 1), phase: "spec"},
		{name: "body and form", data: strings.Replace(base, `"responses"`, `"parameters":[{"name":"body","in":"body","schema":{"type":"string"}},{"name":"form","in":"formData","type":"string"}],"responses"`, 1), phase: "spec"},
		{name: "required missing allOf property", data: strings.Replace(base, `"paths"`, `"definitions":{"x":{"type":"object","required":["missing"],"allOf":[{"type":"object","properties":{"present":{"type":"string"}}}]}},"paths"`, 1), phase: "spec"},
		{name: "dereferenced extension target is invalid", data: `{"openapi":"3.0.3","info":{"title":"x","version":"1"},"x-parameter":{"in":"query","type":"string"},"paths":{"/x":{"get":{"parameters":[{"$ref":"#/x-parameter"}],"responses":{"200":{"description":"ok"}}}}}}`, phase: "schema"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := validateLegacyOpenAPI(legacyValidationDocument(t, test.data))
			if err == nil || !strings.Contains(err.Error(), "legacy OpenAPI validation "+test.phase+":") {
				t.Fatalf("validateLegacyOpenAPI(%s) error = %v, want %s phase", test.name, err, test.phase)
			}
		})
	}
}

func TestLegacyOpenAPIValidatorVersionRoutingAndYAMLEquivalence(t *testing.T) {
	for name, data := range map[string]struct{ data, phase string }{
		"3.0 adjacent":                     {`{"openapi":"3.0.5","info":{"title":"x","version":"1"},"paths":{}}`, "version"},
		"3.1 adjacent":                     {`{"openapi":"3.1.3","info":{"title":"x","version":"1"},"paths":{}}`, "version"},
		"numeric OpenAPI":                  {`{"openapi":3.1,"info":{"title":"x","version":"1"},"paths":{}}`, "version"},
		"3.1 no paths or webhooks":         {`{"openapi":"3.1.2","info":{"title":"x","version":"1"}}`, "version"},
		"3.1 null webhooks reaches schema": {`{"openapi":"3.1.2","info":{"title":"x","version":"1"},"webhooks":null}`, "schema"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateLegacyOpenAPI(legacyValidationDocument(t, data.data))
			if err == nil || !strings.Contains(err.Error(), "legacy OpenAPI validation "+data.phase+":") {
				t.Fatalf("validateLegacyOpenAPI(%s) error = %v, want %s phase", name, err, data.phase)
			}
		})
	}
	jsonDocument := legacyValidationDocument(t, `{"openapi":"3.0.3","info":{"title":"x","version":"1"},"paths":{}}`)
	yamlDocument, err := decodeLegacyOpenAPI([]byte("openapi: 3.0.3\ninfo:\n  title: x\n  version: '1'\npaths: {}\n"), true)
	if err != nil {
		t.Fatalf("decodeLegacyOpenAPI(YAML) error = %v", err)
	}
	if err := validateLegacyOpenAPI(jsonDocument); err != nil {
		t.Fatalf("validateLegacyOpenAPI(JSON) error = %v", err)
	}
	if err := validateLegacyOpenAPI(yamlDocument); err != nil {
		t.Fatalf("validateLegacyOpenAPI(YAML) error = %v", err)
	}
}

func TestLegacyOpenAPISchemaFormatKeywordDoesNotDeleteFormatProperty(t *testing.T) {
	invalid := `{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{},"components":{"schemas":{"Example":{"type":"string","format":17}}}}`
	err := validateLegacyOpenAPI(legacyValidationDocument(t, invalid))
	if err == nil || !strings.HasPrefix(err.Error(), "legacy OpenAPI validation schema:") {
		t.Fatalf("validateLegacyOpenAPI(non-string schema format) error = %v, want schema rejection", err)
	}
	valid := `{"openapi":"3.0.4","info":{"title":"x","version":"1","contact":{"email":"not an email"}},"servers":[{"url":"not a URI %%%"}],"paths":{}}`
	if err := validateLegacyOpenAPI(legacyValidationDocument(t, valid)); err != nil {
		t.Fatalf("validateLegacyOpenAPI(invalid format values) error = %v, want formats disabled", err)
	}
}

func TestLegacyOpenAPIValidatorRejectsBeforeArtifactConstruction(t *testing.T) {
	root := t.TempDir()
	writeLegacyFixture(t, root)
	if err := os.WriteFile(filepath.Join(root, "openapi.json"), []byte(`{"openapi":"3.0.5","info":{"title":"x","version":"1"},"paths":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	recipe, err := loadRecipe(filepath.Join(root, "recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runLegacy(context.Background(), recipe, RunOptions{WorkDirectory: filepath.Join(root, "work")})
	if err == nil || !strings.Contains(err.Error(), "legacy OpenAPI validation version:") {
		t.Fatalf("runLegacy(invalid OpenAPI) error = %v, want version validation failure", err)
	}
	if artifacts := result.Artifacts(); len(artifacts) != 0 {
		t.Fatalf("runLegacy(invalid OpenAPI) artifacts = %#v, want none", artifacts)
	}
	if _, statErr := os.Stat(filepath.Join(root, "work", "artifacts")); !os.IsNotExist(statErr) {
		t.Fatalf("runLegacy(invalid OpenAPI) created artifacts directory: %v", statErr)
	}
}

func TestLegacyOpenAPIValidationGraphBoundsAndNumericConversion(t *testing.T) {
	value := map[string]any{"negative": json.Number("-0"), "positive": json.Number("1e999999"), "negative-large": json.Number("-1e999999")}
	graph, err := legacyValidationGraph(value)
	if err != nil {
		t.Fatalf("legacyValidationGraph(number edge cases) error = %v", err)
	}
	converted := graph.(map[string]any)
	if got := math.Signbit(converted["negative"].(float64)); !got {
		t.Error("legacyValidationGraph(-0) lost signed zero")
	}
	if got := converted["positive"].(float64); got != math.MaxFloat64 {
		t.Errorf("legacyValidationGraph(positive infinity) = %v, want %v", got, math.MaxFloat64)
	}
	if got := converted["negative-large"].(float64); got != -math.MaxFloat64 {
		t.Errorf("legacyValidationGraph(negative infinity) = %v, want %v", got, -math.MaxFloat64)
	}
	shared := map[string]any{"n": json.Number("1")}
	sharedGraph, err := legacyValidationGraph(map[string]any{"a": shared, "b": shared})
	if err != nil {
		t.Fatalf("legacyValidationGraph(shared graph) error = %v", err)
	}
	sharedObject := sharedGraph.(map[string]any)
	if reflect.ValueOf(sharedObject["a"]).Pointer() != reflect.ValueOf(sharedObject["b"]).Pointer() {
		t.Error("legacyValidationGraph(shared object) did not preserve shared identity")
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	graph, err = legacyValidationGraph(cycle)
	if err != nil {
		t.Fatalf("legacyValidationGraph(direct cycle) error = %v, want detached graph", err)
	}
	if err := rejectLegacyNativeCycle(graph); err == nil {
		t.Fatal("rejectLegacyNativeCycle(direct cycle) error = nil, want bounded rejection")
	}
	cycle["openapi"] = "3.0.5"
	if err := validateLegacyOpenAPI(cycle); legacyValidationPhase(err) != "version" {
		t.Fatalf("validateLegacyOpenAPI(invalid version plus native cycle) error = %v, want version phase before dereference", err)
	}
}

func legacyValidationDocument(t *testing.T, source string) map[string]any {
	t.Helper()
	document, err := decodeLegacyJSON([]byte(source))
	if err != nil {
		t.Fatalf("decodeLegacyJSON(%s) error = %v", source, err)
	}
	return document
}
