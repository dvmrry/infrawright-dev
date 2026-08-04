package metadata

// referent_id_field_test.go covers the pack-grammar surface for
// references.<referrer>.<field>.referent_id_field: structural validation
// (identifier shape, non-empty, not the literal default "id") and the
// data-referent-target refusal, which needs a loaded registry.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePackManifestAcceptsAlternateReferentIdField(t *testing.T) {
	manifest := referenceManifest(JsonObject{
		"sample_a": JsonObject{
			"b_id": JsonObject{"name_field": "name", "referent": "sample_b", "referent_id_field": "val"},
		},
	})
	if _, err := ValidatePackManifest(manifest, "manifest.json"); err != nil {
		t.Fatalf("ValidatePackManifest: expected alternate referent_id_field to be accepted, got %v", err)
	}
}

func TestValidatePackManifestRejectsNonStringReferentIdField(t *testing.T) {
	manifest := referenceManifest(JsonObject{
		"sample_a": JsonObject{
			"b_id": JsonObject{"name_field": "name", "referent": "sample_b", "referent_id_field": 5.0},
		},
	})
	_, err := ValidatePackManifest(manifest, "manifest.json")
	if err == nil {
		t.Fatal("ValidatePackManifest: expected non-string referent_id_field to be refused, got nil")
	}
	want := "manifest.json.references.sample_a.b_id.referent_id_field must be a non-empty string"
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestValidatePackManifestRejectsEmptyReferentIdField(t *testing.T) {
	manifest := referenceManifest(JsonObject{
		"sample_a": JsonObject{
			"b_id": JsonObject{"name_field": "name", "referent": "sample_b", "referent_id_field": ""},
		},
	})
	_, err := ValidatePackManifest(manifest, "manifest.json")
	if err == nil {
		t.Fatal("ValidatePackManifest: expected empty referent_id_field to be refused, got nil")
	}
	want := "manifest.json.references.sample_a.b_id.referent_id_field must be a non-empty string"
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestValidatePackManifestRejectsNonIdentifierReferentIdField(t *testing.T) {
	manifest := referenceManifest(JsonObject{
		"sample_a": JsonObject{
			"b_id": JsonObject{"name_field": "name", "referent": "sample_b", "referent_id_field": "not-an-identifier"},
		},
	})
	_, err := ValidatePackManifest(manifest, "manifest.json")
	if err == nil {
		t.Fatal("ValidatePackManifest: expected non-identifier referent_id_field to be refused, got nil")
	}
	want := "manifest.json.references.sample_a.b_id.referent_id_field must be a valid identifier"
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestValidatePackManifestRejectsLiteralIdReferentIdField(t *testing.T) {
	manifest := referenceManifest(JsonObject{
		"sample_a": JsonObject{
			"b_id": JsonObject{"name_field": "name", "referent": "sample_b", "referent_id_field": "id"},
		},
	})
	_, err := ValidatePackManifest(manifest, "manifest.json")
	if err == nil {
		t.Fatal("ValidatePackManifest: expected literal \"id\" referent_id_field to be refused, got nil")
	}
	want := "manifest.json.references.sample_a.b_id.referent_id_field must not be \"id\"; omit it to use the default id space"
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestLoadPackRootRejectsReferentIdFieldOnDataReferentTarget(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "sample", "pack.json"), JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": JsonObject{"sample_": "sample"},
		"provider_sources":  JsonObject{"sample": "example/sample"},
		"lookup_sources": JsonObject{
			"sample_data": JsonObject{"name_field": "name"},
		},
		"references": JsonObject{
			"sample_source": JsonObject{
				"target_id": JsonObject{"name_field": "name", "referent": "sample_data", "referent_id_field": "val"},
			},
		},
	})
	writeJSONFile(t, filepath.Join(directory, "sample", "registry.json"), JsonObject{
		"sample_data": JsonObject{
			"data_referent": true,
			"fetch":         JsonObject{"pagination": "single", "path": "items"},
			"product":       "sample",
		},
		"sample_source": JsonObject{"product": "sample"},
	})
	writeJSONFile(t, filepath.Join(directory, "sample", "schemas", "provider", "sample.json"), JsonObject{
		"resource_schemas": JsonObject{
			"sample_source": JsonObject{},
		},
		"data_source_schemas": JsonObject{
			"sample_data": JsonObject{},
		},
	})

	_, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory})
	if err == nil {
		t.Fatal("LoadPackRoot: expected data-referent-target refusal, got nil")
	}
	want := "references.sample_source.target_id.referent_id_field: \"sample_data\" is a data referent and cannot declare an alternate id space"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("LoadPackRoot error = %q, want it to contain %q", err.Error(), want)
	}
}
