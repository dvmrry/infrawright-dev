package metadata

import (
	"path/filepath"
	"strings"
	"testing"
)

func valueRewriteEntry(path, from, to, why string) JsonObject {
	return JsonObject{"path": path, "from": from, "to": to, "why": why}
}

func TestValueRewriteOverrideValidation(t *testing.T) {
	valid := JsonObject{
		"value_rewrite": []any{
			valueRewriteEntry("name", "SHORT_FORM_VALUE", "LONG_FORM_VALUE", "The provider accepts only the long write spelling."),
			valueRewriteEntry("name", "OTHER_SHORT_FORM", "OTHER_LONG_FORM", "A second enumerated provider spelling has the same asymmetry."),
		},
	}
	if _, err := ValidateOverride(valid, "override.json"); err != nil {
		t.Fatalf("ValidateOverride(valid value_rewrite) error = %v, want nil", err)
	}

	tests := []struct {
		name     string
		override JsonObject
		want     string
	}{
		{
			name:     "missing why",
			override: JsonObject{"value_rewrite": []any{JsonObject{"path": "name", "from": "SHORT", "to": "LONG"}}},
			want:     ".why must be a non-empty string",
		},
		{
			name:     "empty why",
			override: JsonObject{"value_rewrite": []any{valueRewriteEntry("name", "SHORT", "LONG", "")}},
			want:     ".why must be a non-empty string",
		},
		{
			name:     "from equals to",
			override: JsonObject{"value_rewrite": []any{valueRewriteEntry("name", "SAME", "SAME", "The provider has a distinct write spelling.")}},
			want:     "from and to must differ",
		},
		{
			name: "duplicate from for path",
			override: JsonObject{"value_rewrite": []any{
				valueRewriteEntry("name", "SHORT", "LONG", "First enumerated spelling."),
				valueRewriteEntry("name", "SHORT", "OTHER_LONG", "Duplicate source spelling."),
			}},
			want: "contains duplicate from value",
		},
		{
			name:     "empty path",
			override: JsonObject{"value_rewrite": []any{valueRewriteEntry("", "SHORT", "LONG", "The provider has a distinct write spelling.")}},
			want:     ".path must be a non-empty string",
		},
		{
			name:     "empty from",
			override: JsonObject{"value_rewrite": []any{valueRewriteEntry("name", "", "LONG", "The provider has a distinct write spelling.")}},
			want:     ".from must be a non-empty string",
		},
		{
			name:     "empty to",
			override: JsonObject{"value_rewrite": []any{valueRewriteEntry("name", "SHORT", "", "The provider has a distinct write spelling.")}},
			want:     ".to must be a non-empty string",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateOverride(test.override, "override.json")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateOverride(%s) error = %v, want text %q", test.name, err, test.want)
			}
		})
	}
}

func valueRewritePackRoot(t *testing.T, override JsonObject, enum []string) (LoadedPackRoot, error) {
	t.Helper()
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "sample", "pack.json"), JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": JsonObject{"sample_": "sample"},
		"provider_sources":  JsonObject{"sample": "example/sample"},
	})
	writeJSONFile(t, filepath.Join(directory, "sample", "registry.json"), JsonObject{
		"sample_resource": JsonObject{"product": "sample"},
	})
	choice := JsonObject{"optional": true, "type": "string"}
	if enum != nil {
		choice["enum"] = enum
	}
	writeJSONFile(t, filepath.Join(directory, "sample", "schemas", "provider", "sample.json"), JsonObject{
		"resource_schemas": JsonObject{
			"sample_resource": JsonObject{"block": JsonObject{"attributes": JsonObject{
				"choice":        choice,
				"computed_only": JsonObject{"computed": true, "type": "string"},
				"enabled":       JsonObject{"optional": true, "type": "bool"},
				"name":          JsonObject{"required": true, "type": "string"},
				"number_value":  JsonObject{"optional": true, "type": "number"},
				"repeated": JsonObject{"optional": true, "nested_type": JsonObject{
					"nesting_mode": "list",
					"attributes":   JsonObject{"child": JsonObject{"optional": true, "type": "string"}},
				}},
			}}},
		},
	})
	writeJSONFile(t, filepath.Join(directory, "sample", "overrides", "sample_resource.json"), override)
	profile := filepath.Join(directory, "profile.json")
	writeJSONFile(t, profile, JsonObject{
		"kind": PackSetKind, "version": 1, "packs": []string{"sample"}, "shared": []string{},
	})
	return LoadPackRoot(LoadPackRootOptions{PacksRoot: directory, ProfilePath: &profile})
}

func TestValueRewritePackValidationBindsWritablePathAndEnum(t *testing.T) {
	valid, err := valueRewritePackRoot(t, JsonObject{"value_rewrite": []any{
		valueRewriteEntry("name", "SHORT", "LONG", "The provider rejects the short read spelling on write."),
	}}, nil)
	if err != nil {
		t.Fatalf("LoadPackRoot(valid value_rewrite) error = %v, want nil", err)
	}
	entries, ok := valid.Resources["sample_resource"].Override["value_rewrite"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("loaded value_rewrite = %#v, want one validated entry", valid.Resources["sample_resource"].Override["value_rewrite"])
	}

	tests := []struct {
		name     string
		override JsonObject
		enum     []string
		want     string
	}{
		{
			name: "non-writable path",
			override: JsonObject{"value_rewrite": []any{
				valueRewriteEntry("computed_only", "SHORT", "LONG", "The provider rejects the short read spelling on write."),
			}},
			want: "not a writable input attribute",
		},
		{
			name: "repeated nested path",
			override: JsonObject{"value_rewrite": []any{
				valueRewriteEntry("repeated.child", "SHORT", "LONG", "A repeated nested value has no stable v1 exact path."),
			}},
			want: "not a writable input attribute",
		},
		{
			name: "out of enum",
			override: JsonObject{"value_rewrite": []any{
				valueRewriteEntry("choice", "SHORT", "NOT_ENUMERATED", "The provider rejects the short read spelling on write."),
			}},
			enum: []string{"LONG"},
			want: "not in the provider schema enum",
		},
		{
			name: "boolean terminal attribute",
			override: JsonObject{"value_rewrite": []any{
				valueRewriteEntry("enabled", "false", "true", "The provider uses a string spelling for this read value."),
			}},
			want: "must target a string attribute",
		},
		{
			name: "number terminal attribute",
			override: JsonObject{"value_rewrite": []any{
				valueRewriteEntry("number_value", "0", "1", "The provider uses a string spelling for this read value."),
			}},
			want: "must target a string attribute",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := valueRewritePackRoot(t, test.override, test.enum)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadPackRoot(%s) error = %v, want text %q", test.name, err, test.want)
			}
		})
	}
}

func validateValueRewritePackOverridesForTest(metadataValue PackMetadata, registry LoadedRegistry, overrides LoadedOverrides) (err error) {
	defer recoverMetadataError(&err)
	validateValueRewritePackOverrides(metadataValue, registry, overrides)
	return nil
}

func TestValueRewritePackValidationRejectsTransformDelegatedTypes(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(JsonObject)
	}{
		{
			name: "derive",
			mutate: func(entry JsonObject) {
				entry["derive"] = JsonObject{"from": "source_resource"}
			},
		},
		{
			name: "data referent",
			mutate: func(entry JsonObject) {
				entry["data_referent"] = true
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := valueRewritePackRoot(t, JsonObject{"value_rewrite": []any{
				valueRewriteEntry("name", "SHORT", "LONG", "The provider normalizes the successful write to LONG."),
			}}, nil)
			if err != nil {
				t.Fatalf("LoadPackRoot(initial root) error = %v", err)
			}
			entry := root.Registry.Entries["sample_resource"]
			test.mutate(entry)
			err = validateValueRewritePackOverridesForTest(root.Packs, root.Registry, root.Overrides)
			if err == nil || !strings.Contains(err.Error(), "primitive only applies to generated adopt types") {
				t.Fatalf("validateValueRewritePackOverrides(%s) error = %v, want generated-adopt refusal", test.name, err)
			}
		})
	}
}

func TestValueRewritePackValidationRejectsDropDefaultCollision(t *testing.T) {
	_, err := valueRewritePackRoot(t, JsonObject{
		"drop_if_default": JsonObject{"choice": "LONG"},
		"value_rewrite": []any{
			valueRewriteEntry("choice", "SHORT", "LONG", "The provider normalizes the successful write to LONG."),
		},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "drop_if_default") {
		t.Fatalf("LoadPackRoot(drop-default value_rewrite collision) error = %v, want drop_if_default refusal", err)
	}
}

func TestValueRewriteManifestPolicyUsesPackSchemaValidation(t *testing.T) {
	root, err := valueRewritePackRoot(t, JsonObject{}, nil)
	if err != nil {
		t.Fatalf("LoadPackRoot(initial value_rewrite root) error = %v", err)
	}
	writeJSONFile(t, root.Packs.Manifests[0].Path, JsonObject{
		"pin":               "1.0.0",
		"provider_prefixes": JsonObject{"sample_": "sample"},
		"provider_sources":  JsonObject{"sample": "example/sample"},
		"drift_policy": JsonObject{
			"version": 1,
			"resource_types": JsonObject{
				"sample_resource": JsonObject{"value_rewrite": []any{
					valueRewriteEntry("computed_only", "SHORT", "LONG", "The provider rejects the short read spelling on write."),
				}},
			},
		},
	})
	_, err = LoadPackRoot(LoadPackRootOptions{PacksRoot: root.Packs.Root})
	if err == nil || !strings.Contains(err.Error(), "not a writable input attribute") {
		t.Fatalf("LoadPackRoot(manifest value_rewrite) error = %v, want schema writability refusal", err)
	}
}
