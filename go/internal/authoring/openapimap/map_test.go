package openapimap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const authoritySHA256 = "e4e25a12a871c895364bce16fe05a8bcd94debd1eddc53de9fc75ca82bc8ce3c"

func TestFrozenV1Reports(t *testing.T) {
	t.Parallel()
	bytes, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "node-tests", "fixtures", "python-openapi-resource-map-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bytes)
	if got := hex.EncodeToString(sum[:]); got != authoritySHA256 {
		t.Fatalf("authority SHA = %s", got)
	}
	fixture, err := canonjson.Decode(bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"node_live_differential", "retained_unittest"} {
		for _, raw := range anyObjects(object(fixture)[group].(Object)["report_cases"]) {
			raw := raw
			t.Run(group+"/"+str(raw["name"]), func(t *testing.T) {
				input := object(raw["input"])
				openAPI := recordedValue(input["openapi"])
				document, err := documentFor(t, openAPI)
				if err != nil {
					t.Fatal(err)
				}
				var provider *string
				if text, ok := input["provider_source"].(string); ok {
					provider = &text
				}
				registry := recordedValue(input["registry_data"])
				if input["registry_data"] == nil {
					registry = defaultRegistry(t)
					if len(registry) == 0 {
						t.Fatal("default registry is empty")
					}
				}
				registryPtr := &registry
				apiPrefix := str(input["api_prefix"])
				report, err := Build(context.Background(), Options{SchemaData: recordedValue(input["schema"]), Document: document, ProviderSource: provider, ResourcePrefix: str(input["resource_prefix"]), APIPrefix: &apiPrefix, RegistryData: registryPtr})
				if err != nil {
					t.Fatal(err)
				}
				got, err := report.Render()
				if err != nil {
					t.Fatal(err)
				}
				expected := raw["python_report"]
				if expected == nil {
					expected = raw["report"]
				}
				want, err := canonjson.Render(expected)
				if err != nil {
					t.Fatal(err)
				}
				want += "\n"
				if string(got) != want {
					t.Fatalf("frozen report differs at %s", firstDifference(string(want), string(got)))
				}
			})
		}
	}
}

func TestHelperAndBoundaryVectors(t *testing.T) {
	t.Parallel()
	if got := RoundPythonRatio4(1, 32); got != 0.0312 {
		t.Fatalf("1/32 = %v", got)
	}
	if got := RoundPythonRatio4(1, 160); got != 0.0063 {
		t.Fatalf("1/160 = %v", got)
	}
	if got := RoundPythonRatio4(0, 0); got != 0 {
		t.Fatalf("0/0 = %v", got)
	}
	if got := RoundPythonRatio4(-3, 32); got != -0.0938 {
		t.Fatalf("-3/32 = %v", got)
	}
	if got := RoundPythonRatio4(-1, 32); got != -0.0312 {
		t.Fatalf("-1/32 = %v", got)
	}
	if got := plural("address"); got != "addresses" {
		t.Fatalf("plural address = %s", got)
	}
	if got := canonicalParts("/a/{id}/b/"); len(got) != 3 || got[1] != "{}" {
		t.Fatalf("canonical parts = %#v", got)
	}
	variants := fetchVariants("/api/v1/zcc/devices/{id}", "zcc", "/api/v1/")
	if got := len(variants); got != 3 {
		t.Fatalf("variants = %#v", variants)
	}
	if _, err := providerFromSchema(Object{"provider_schemas": Object{"a/x": Object{}, "b/x": Object{}}}, nil); err == nil {
		t.Fatal("ambiguous provider accepted")
	}
	view := openapiadapter.LegacyMap{Paths: []openapiadapter.LegacyPath{{Template: "/things/{id}"}, {Template: "/things/{id}/"}, {Template: "/things//{id}"}, {Template: "/things/{id}//"}, {Template: "/things/{id}/extra"}}}
	if got := detailPaths(view, "/things"); len(got) != 2 || got[0] != "/things/{id}" || got[1] != "/things/{id}/" {
		t.Fatalf("detail paths = %#v", got)
	}
}

func TestOptionsAPIPrefixAndRegistryOptionalRenderedVectors(t *testing.T) {
	t.Parallel()
	document, err := documentFor(t, Object{"openapi": "3.0.3", "paths": Object{"/things": Object{"get": Object{}}}})
	if err != nil {
		t.Fatal(err)
	}
	schema := Object{"resource_schemas": Object{"example_thing": Object{"block": Object{"attributes": Object{"name": Object{"required": true, "type": "string"}}}}}}
	omitted, err := Build(context.Background(), Options{SchemaData: schema, Document: document, ResourcePrefix: "example"})
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	explicit, err := Build(context.Background(), Options{SchemaData: schema, Document: document, ResourcePrefix: "example", APIPrefix: &empty})
	if err != nil {
		t.Fatal(err)
	}
	omittedBytes, err := omitted.Render()
	if err != nil {
		t.Fatal(err)
	}
	explicitBytes, err := explicit.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(omittedBytes), `"api_prefix": "/api/"`) || !strings.Contains(string(explicitBytes), `"api_prefix": ""`) {
		t.Fatalf("prefix render omitted=%s explicit=%s", omittedBytes, explicitBytes)
	}

	view := openapiadapter.LegacyMap{Paths: []openapiadapter.LegacyPath{{Template: "/things", Methods: []string{"get"}}}}
	registry := Object{
		"absent_product":         Object{"fetch": Object{"path": "/things"}},
		"false_product":          Object{"product": false, "fetch": Object{"path": "/things"}},
		"nested_pagination":      Object{"product": "", "pagination": "top", "fetch": Object{"path": "/things", "pagination": "nested"}},
		"top_level_pagination":   Object{"product": "", "pagination": "top", "fetch": Object{"path": "/things"}},
		"falsy_pagination":       Object{"product": "", "fetch": Object{"path": "/things", "pagination": false}},
		"absent_reason":          Object{"product": "", "status": "graphql_source", "read": Object{}},
		"falsy_status_optionals": Object{"product": "", "status": false, "read": Object{"path": "/things", "operation_id": "", "path_kind": 0}},
	}
	fetchBytes, err := canonjson.Render(renderableJSON(registryCoverage(view, "", "", registry, "fetch")))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fetchBytes, `"absent_product"`) || strings.Contains(fetchBytes, `"false_product"`) || !strings.Contains(fetchBytes, `"pagination": ""`) || !strings.Contains(fetchBytes, `"pagination": "nested"`) || !strings.Contains(fetchBytes, `"pagination": false`) {
		t.Fatalf("fetch optional parity bytes: %s", fetchBytes)
	}
	read := registryCoverage(view, "", "", registry, "read")
	readBytes, err := canonjson.Render(renderableJSON(read))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readBytes, `"reason": "graphql_source"`) || strings.Contains(readBytes, `"operation_id"`) || strings.Contains(readBytes, `"path_kind"`) {
		t.Fatalf("read optional parity bytes: %s", readBytes)
	}
}

func documentFor(t *testing.T, value Object) (openapiadapter.Document, error) {
	t.Helper()
	rendered, err := canonjson.Render(value)
	if err != nil {
		return openapiadapter.Document{}, err
	}
	bytes := []byte(rendered)
	sum := sha256.Sum256(bytes)
	return openapiadapter.ParseForMetadata(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{{Path: "root.json", Bytes: bytes, SHA256: hex.EncodeToString(sum[:])}}})
}

func recordedValue(value any) Object {
	object := object(value)
	if nested, ok := object["json"].(map[string]any); ok {
		return nested
	}
	return object
}

func anyObjects(value any) []Object { return objects(value) }

func defaultRegistry(t *testing.T) Object {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "packs")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	out := Object{}
	for _, entry := range entries {
		bytes, err := os.ReadFile(filepath.Join(root, entry.Name(), "registry.json"))
		if err != nil {
			continue
		}
		value, err := canonjson.Decode(bytes)
		if err != nil {
			t.Fatal(err)
		}
		for key, item := range object(value) {
			out[key] = item
		}
	}
	return out
}

func firstDifference(left, right string) string {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] != right[i] {
			return left[testMax(0, i-80):min(len(left), i+80)] + " != " + right[testMax(0, i-80):min(len(right), i+80)]
		}
	}
	return "different lengths"
}

func testMax(left, right int) int {
	if left > right {
		return left
	}
	return right
}
