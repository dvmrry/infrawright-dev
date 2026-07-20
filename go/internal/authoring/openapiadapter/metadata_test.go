package openapiadapter

import (
	"context"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

func TestMetadataSchemaAndMediaGuards(t *testing.T) {
	t.Parallel()
	root := []byte(`{"openapi":"3.0.3","paths":{"/things":{"get":{"responses":{"200":{"content":{"application/json":"not-an-object","text/json":{"schema":{"type":"string"}}}}}},"post":{"requestBody":"not-an-object","parameters":["not-an-object",{"in":"body","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}}}}`)
	document, err := ParseForMetadata(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", root)}})
	if err != nil {
		t.Fatalf("ParseForMetadata(guards) error = %v, want nil", err)
	}
	got, err := document.Metadata(context.Background(), MetadataOptions{ReadOperations: []OperationReference{"GET:/things"}, WriteOperations: []OperationReference{"POST:/things"}})
	if err != nil {
		t.Fatalf("Document.Metadata(guards) error = %v, want nil", err)
	}
	if got[""] != nil || got["[]"] != nil {
		t.Errorf("Document.Metadata(guards) = %#v, want no synthetic scalar field", got)
	}
}

func TestMetadataCompatibilityFencesAndSchemaEdges(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"3.2.0", "2.1.0", "not-openapi"} {
		_, err := ParseForMetadata(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{captured("root.json", []byte(`{"openapi":"`+version+`","paths":{}}`))}})
		if err == nil {
			t.Errorf("ParseForMetadata(version %q) error = nil, want non-nil", version)
		}
	}
	root := []byte(`{
  "openapi": "3.0.3",
  "paths": {
    "/things": {
      "get": {"responses": {"200": {"content": {"application/json": {"description": "not-a-media-object"}, "text/json": {"schema": {"type": "string"}}}}}},
      "post": {"requestBody": {"content": {"application/json": {"schema": null}}}, "responses": {"200": {"description": "ok"}}}
    }
  }
}`)
	document, err := ParseForMetadata(context.Background(), sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{
		captured("root.json", root),
		captured("unreferenced-invalid.json", []byte(`{"not":"a complete OpenAPI document"`)),
	}})
	if err != nil {
		t.Fatalf("ParseForMetadata(unreferenced auxiliary file) error = %v, want nil", err)
	}
	metadata, err := document.Metadata(context.Background(), MetadataOptions{ReadOperations: []OperationReference{"GET:/things"}, WriteOperations: []OperationReference{"POST:/things"}})
	if err != nil {
		t.Fatalf("Document.Metadata(application/json no fallback and nil schema) error = %v, want nil", err)
	}
	if got, want := metadata, map[string]map[string]any{}; !reflect.DeepEqual(map[string]map[string]any(got), want) {
		t.Errorf("Document.Metadata(application/json no fallback and nil schema) = %#v, want %#v", got, want)
	}
}

func TestMetadataRejectsNonLocalReferenceAliases(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"#", "root.json#/components/schemas/Thing"} {
		root := []byte(`{"openapi":"3.0.3","paths":{"/things":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"` + ref + `"}}}}}}}},"components":{"schemas":{"Thing":{"type":"object"}}}}`)
		document, err := ParseForMetadata(context.Background(), available(root))
		if err != nil {
			t.Fatalf("ParseForMetadata(metadata ref %q) error = %v, want nil", ref, err)
		}
		if _, err := document.Metadata(context.Background(), MetadataOptions{ReadOperations: []OperationReference{"GET:/things"}}); err == nil {
			t.Errorf("Document.Metadata(metadata ref %q) error = nil, want fragment-local boundary error", ref)
		}
	}
}

func TestMetadataAdditionalPropertiesAndReadWriteFlagsAreDetached(t *testing.T) {
	t.Parallel()
	root := []byte(`{"openapi":"3.0.3","paths":{"/things":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"readOnly":{"type":"string","readOnly":true},"writeOnly":{"type":"string","writeOnly":true},"responseOnly":{"type":"string"},"objectTrue":{"additionalProperties":true},"objectFalse":{"additionalProperties":false}}}}}}}},"post":{"requestBody":{"content":{"application/json":{"schema":{"type":"object","required":["writeOnly"],"properties":{"writeOnly":{"type":"string","writeOnly":true}}}}}},"responses":{"200":{"description":"ok"}}}}}}`)
	document, err := ParseForMetadata(context.Background(), available(root))
	if err != nil {
		t.Fatalf("ParseForMetadata(read/write schema flags) error = %v, want nil", err)
	}
	got, err := document.Metadata(context.Background(), MetadataOptions{ReadOperations: []OperationReference{"GET:/things"}, WriteOperations: []OperationReference{"POST:/things"}})
	if err != nil {
		t.Fatalf("Document.Metadata(read/write schema flags) error = %v, want nil", err)
	}
	if got["object_true"]["schema_types"] == nil || got["object_false"]["schema_types"] != nil {
		t.Errorf("Document.Metadata(additionalProperties true/false) = %#v, want object only for true", got)
	}
	if got["read_only"]["read_only"] != true || got["read_only"]["response_only"] != nil || got["write_only"]["write_only"] != true || got["write_only"]["required"] != true || got["response_only"]["response_only"] != true {
		t.Errorf("Document.Metadata(read/write flags) = %#v, want read_only/write_only/required/response_only semantics", got)
	}
	got["read_only"]["readable"] = false
	again, err := document.Metadata(context.Background(), MetadataOptions{ReadOperations: []OperationReference{"GET:/things"}, WriteOperations: []OperationReference{"POST:/things"}})
	if err != nil {
		t.Fatalf("Document.Metadata(detached repeat) error = %v, want nil", err)
	}
	if got, want := again["read_only"]["readable"], true; got != want {
		t.Errorf("Document.Metadata(detached repeat)[read_only].readable = %v, want %v", got, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := document.Metadata(ctx, MetadataOptions{}); err == nil {
		t.Error("Document.Metadata(cancelled context) error = nil, want non-nil")
	}
}

func TestMergeSchemaRejectsScalarAndAdditionalPropertiesFalse(t *testing.T) {
	t.Parallel()
	document := Document{root: "root.json", raw: map[string][]byte{"root.json": []byte(`{}`)}, files: map[string]any{"root.json": map[string]any{}}}
	if _, err := mergeSchema(document, "scalar", map[string]bool{}, 0); err == nil {
		t.Error("mergeSchema(scalar) error = nil, want non-nil")
	}
	if got := schemaKind(map[string]any{"additionalProperties": false}); got != "" {
		t.Errorf("schemaKind(additionalProperties:false) = %q, want empty", got)
	}
}

func TestFlattenRootArrayDoesNotEmitSyntheticField(t *testing.T) {
	t.Parallel()
	document := Document{root: "root.json", raw: map[string][]byte{"root.json": []byte(`{}`)}, files: map[string]any{"root.json": map[string]any{}}}
	fields := map[string]map[string]any{}
	if err := flatten(context.Background(), document, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, fields, "read", "", 0); err != nil {
		t.Fatalf("flatten(root array) error = %v", err)
	}
	if len(fields) != 0 {
		t.Errorf("flatten(root array) = %#v, want no fields", fields)
	}
}
