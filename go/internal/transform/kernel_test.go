package transform

// kernel_test.go ports the kernel-level vectors from
// node-tests/generic-transform-core.test.ts: the full TransformLoadedItems
// pipeline (override order, schema projection, coercion, HTML
// escape/unescape, skip_if/skip_if_lte, null-stub recognition) and
// DeriveReorderItems.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func strPtr(s string) *string { return &s }

// repoRoot walks up from this test file's directory until it finds a
// directory containing both "catalogs" and "packs", matching
// go/internal/metadata/gate_test.go's own helper of the same name/contract
// (unexported there, so not reusable across packages).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		_, catalogsErr := os.Stat(filepath.Join(dir, "catalogs"))
		_, packsErr := os.Stat(filepath.Join(dir, "packs"))
		if catalogsErr == nil && packsErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up to filesystem root from %s without finding a directory containing both catalogs/ and packs/", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

func sampleRuleResource(override metadata.JsonObject) metadata.LoadedResourceMetadata {
	return metadata.LoadedResourceMetadata{
		Type:     "sample_rule",
		Product:  "sample",
		Provider: "sample",
		Pack:     strPtr("sample"),
		Registry: metadata.JsonObject{"generate": true, "product": "sample"},
		Override: override,
	}
}

// normalizeJSON round-trips value through encoding/json, the Go analogue
// of the Node tests' own JSON.parse(JSON.stringify(...)) normalization --
// used wherever a test only cares about a value's plain-JSON shape, not
// which of this package's internal representations (json.Number vs a Go
// string/bool/nil) produced it. It must NOT be used to compare a value
// that carries a numeric leaf outside JSON's safe-integer round-trip
// range (see TestLoadedMetadataDrivesOverrideOrderAndProjection's direct
// json.Number assertion on the wide originals.id).
func normalizeJSON(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("normalizeJSON marshal: %v", err)
	}
	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("normalizeJSON unmarshal: %v", err)
	}
	return result
}

func assertJSONEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	gotNorm := normalizeJSON(t, got)
	wantNorm := normalizeJSON(t, want)
	if !reflect.DeepEqual(gotNorm, wantNorm) {
		t.Fatalf("%s:\n got  %#v\n want %#v", label, gotNorm, wantNorm)
	}
}

func TestLoadedMetadataDrivesOverrideOrderAndProjection(t *testing.T) {
	schema := metadata.JsonObject{
		"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{
				"custom":    metadata.JsonObject{"optional": true, "type": "string"},
				"defaulted": metadata.JsonObject{"optional": true, "type": []any{"list", "string"}},
				"drop_zero": metadata.JsonObject{"optional": true, "type": "number"},
				"id":        metadata.JsonObject{"computed": true, "optional": true, "type": "number"},
				"inverted":  metadata.JsonObject{"optional": true, "type": "bool"},
				"name":      metadata.JsonObject{"required": true, "type": "string"},
				"policy":    metadata.JsonObject{"optional": true, "type": "bool"},
				"quota":     metadata.JsonObject{"optional": true, "type": "number"},
				"reference": metadata.JsonObject{"optional": true, "type": "string"},
				"tags":      metadata.JsonObject{"optional": true, "type": []any{"list", "string"}},
				"urls":      metadata.JsonObject{"optional": true, "type": []any{"list", "string"}},
			},
			"block_types": metadata.JsonObject{
				"conditions": metadata.JsonObject{
					"nesting_mode": "list",
					"block": metadata.JsonObject{
						"attributes": metadata.JsonObject{
							"id":   metadata.JsonObject{"optional": true, "type": "string"},
							"name": metadata.JsonObject{"optional": true, "type": "string"},
						},
					},
				},
			},
		},
	}

	override := metadata.JsonObject{
		"acknowledged_drops": []any{"unknown"},
		"defaults":           metadata.JsonObject{"defaulted": []any{"ANY"}},
		"divide":             metadata.JsonObject{"quota": json.Number("1024")},
		"drop_if_default":    metadata.JsonObject{"drop_zero": json.Number("0")},
		"drops":              []any{"discard", "conditions.name"},
		"html_escape_fields": []any{"custom"},
		"invert_bool":        []any{"inverted"},
		"key_field":          "name",
		"references":         metadata.JsonObject{"reference": "unused"},
		"renames":            metadata.JsonObject{"display_name": "name"},
		"skip_if":            []any{metadata.JsonObject{"predefined": true}},
		"sort_lists":         []any{"urls"},
		"split_csv":          []any{"tags"},
		"strip_prefix":       metadata.JsonObject{"tags": "COUNTRY_"},
		"value_map":          metadata.JsonObject{"policy": metadata.JsonObject{"NONE": false}},
	}

	rawItems := []any{
		metadata.JsonObject{"displayName": "ignored", "predefined": true},
		metadata.JsonObject{
			"conditions":  []any{metadata.JsonObject{"id": "1", "name": "computed display"}},
			"custom":      "R&amp;D &amp;quot;x&amp;quot;",
			"discard":     "gone",
			"displayName": "R&amp;amp;D",
			"dropZero":    "0",
			"id":          json.Number("9007199254740997"),
			"inverted":    json.Number("0"),
			"policy":      "NONE",
			"quota":       "2049",
			"reference":   metadata.JsonObject{"id": json.Number("9007199254740999"), "name": "ref"},
			"tags":        "COUNTRY_US, COUNTRY_CA",
			"unknown":     "acknowledged",
			"urls":        []any{"z", "a"},
		},
	}

	htmlUnescape := func(value string) string {
		value = strings.ReplaceAll(value, "&amp;", "&")
		value = strings.ReplaceAll(value, "&quot;", "\"")
		return value
	}

	var skipped []string
	result, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource:     sampleRuleResource(override),
		Schema:       schema,
		RawItems:     rawItems,
		HTMLUnescape: htmlUnescape,
		UnescapeHTML: true,
		OnSkip: func(_ any, reason string) {
			skipped = append(skipped, reason)
		},
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems: %v", err)
	}

	if !reflect.DeepEqual(skipped, []string{"skip_if"}) {
		t.Fatalf("skipped = %v, want [skip_if]", skipped)
	}
	if len(result.Drops) != 0 {
		t.Fatalf("drops = %v, want none", result.Drops)
	}

	assertJSONEqual(t, "items", result.Items, map[string]any{
		"r_amp_amp_d": map[string]any{
			"conditions": []any{map[string]any{"id": "1"}},
			"custom":     "R&amp;D &#34;x&#34;",
			"defaulted":  []any{"ANY"},
			"inverted":   true,
			"name":       "R&amp;amp;D",
			"policy":     false,
			"quota":      2,
			"reference":  "9007199254740999",
			"tags":       []any{"US", "CA"},
			"urls":       []any{"a", "z"},
		},
	})

	original, ok := result.Originals["r_amp_amp_d"]
	if !ok {
		t.Fatalf("originals missing r_amp_amp_d; got %v", result.Originals)
	}
	id, ok := original["id"].(json.Number)
	if !ok || string(id) != "9007199254740997" {
		t.Fatalf("originals.r_amp_amp_d.id = %#v, want json.Number(9007199254740997)", original["id"])
	}
}

func TestCommittedZIAOverridesDropRawEmptyStringSentinels(t *testing.T) {
	root := repoRoot(t)
	profilePath := filepath.Join(root, "packsets", "full.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
		CatalogPath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}

	fixtures := []struct {
		resourceType  string
		raw           metadata.JsonObject
		retained      metadata.JsonObject
		field         string
		retainedValue string
	}{
		{
			resourceType:  "zia_firewall_filtering_network_service",
			raw:           metadata.JsonObject{"id": "1", "name": "Example", "tag": ""},
			retained:      metadata.JsonObject{"id": "1", "name": "Example", "tag": "managed"},
			field:         "tag",
			retainedValue: "managed",
		},
		{
			resourceType:  "zia_browser_control_policy",
			raw:           metadata.JsonObject{"id": "browser_settings", "pluginCheckFrequency": ""},
			retained:      metadata.JsonObject{"id": "browser_settings", "pluginCheckFrequency": "weekly"},
			field:         "plugin_check_frequency",
			retainedValue: "weekly",
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.resourceType, func(t *testing.T) {
			resource, ok := loaded.Resources[fixture.resourceType]
			if !ok {
				t.Fatalf("resource %s not found", fixture.resourceType)
			}
			schema, err := loaded.LoadResourceSchema(fixture.resourceType)
			if err != nil {
				t.Fatalf("LoadResourceSchema: %v", err)
			}
			dropped, err := TransformLoadedItems(TransformLoadedItemsOptions{Resource: resource, Schema: schema, RawItems: []any{fixture.raw}})
			if err != nil {
				t.Fatalf("TransformLoadedItems(raw): %v", err)
			}
			retained, err := TransformLoadedItems(TransformLoadedItemsOptions{Resource: resource, Schema: schema, RawItems: []any{fixture.retained}})
			if err != nil {
				t.Fatalf("TransformLoadedItems(retained): %v", err)
			}
			droppedItem := firstItem(t, dropped)
			retainedItem := firstItem(t, retained)
			if _, hasField := droppedItem[fixture.field]; hasField {
				t.Fatalf("%s: dropped item still has field %s = %#v", fixture.resourceType, fixture.field, droppedItem[fixture.field])
			}
			if got := retainedItem[fixture.field]; got != fixture.retainedValue {
				t.Fatalf("%s: retained item field %s = %#v, want %q", fixture.resourceType, fixture.field, got, fixture.retainedValue)
			}
		})
	}
}

func firstItem(t *testing.T, result PullTransformResult) TransformRecord {
	t.Helper()
	for _, item := range result.Items {
		return item
	}
	t.Fatalf("no items in result")
	return nil
}

func TestCommittedZIAOverridesOmitLiveProvenEmptyEnumsAndRetainRealValues(t *testing.T) {
	root := repoRoot(t)
	profilePath := filepath.Join(root, "packsets", "full.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
		CatalogPath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}

	cases := []struct {
		resourceType     string
		empty            metadata.JsonObject
		nullable         metadata.JsonObject
		retained         metadata.JsonObject
		emptyExpected    map[string]any
		nullExpected     map[string]any
		retainedExpected map[string]any
	}{
		{
			resourceType: "zia_dlp_dictionaries",
			empty: metadata.JsonObject{
				"id": "1", "name": "Dictionary",
				"confidenceLevelForPredefinedDict": "", "confidenceThreshold": "",
			},
			nullable: metadata.JsonObject{
				"id": "1", "name": "Dictionary",
				"confidenceLevelForPredefinedDict": nil, "confidenceThreshold": nil,
			},
			retained: metadata.JsonObject{
				"id": "1", "name": "Dictionary",
				"confidenceLevelForPredefinedDict": "HIGH", "confidenceThreshold": "MEDIUM",
			},
			emptyExpected: map[string]any{"name": "Dictionary"},
			nullExpected: map[string]any{
				"confidence_level_for_predefined_dict": nil,
				"confidence_threshold":                 nil,
				"name":                                 "Dictionary",
			},
			retainedExpected: map[string]any{
				"confidence_level_for_predefined_dict": "HIGH",
				"confidence_threshold":                 "MEDIUM",
				"name":                                 "Dictionary",
			},
		},
		{
			resourceType: "zia_http_header_profile",
			empty: metadata.JsonObject{
				"id": "2", "name": "Header",
				"httpHeaderProfileCriteria": []any{metadata.JsonObject{"header": "USERAGENT", "operator": "", "userAgent": ""}},
			},
			nullable: metadata.JsonObject{
				"id": "2", "name": "Header",
				"httpHeaderProfileCriteria": []any{metadata.JsonObject{"header": "USERAGENT", "operator": nil, "userAgent": nil}},
			},
			retained: metadata.JsonObject{
				"id": "2", "name": "Header",
				"httpHeaderProfileCriteria": []any{metadata.JsonObject{
					"header": "USERAGENT", "operator": "UAVERSIONEQ", "userAgent": "FIREFOX",
				}},
			},
			emptyExpected: map[string]any{
				"http_header_profile_criteria": []any{map[string]any{"header": "USERAGENT"}},
				"name":                         "Header",
			},
			nullExpected: map[string]any{
				"http_header_profile_criteria": []any{map[string]any{"header": "USERAGENT", "operator": nil, "user_agent": nil}},
				"name":                         "Header",
			},
			retainedExpected: map[string]any{
				"http_header_profile_criteria": []any{map[string]any{
					"header": "USERAGENT", "operator": "UAVERSIONEQ", "user_agent": "FIREFOX",
				}},
				"name": "Header",
			},
		},
		{
			resourceType: "zia_location_management",
			empty: metadata.JsonObject{
				"id": "3", "name": "Location",
				"displayTimeUnit": "", "subLocScope": "", "surrogateRefreshTimeUnit": "",
			},
			nullable: metadata.JsonObject{
				"id": "3", "name": "Location",
				"displayTimeUnit": nil, "subLocScope": nil, "surrogateRefreshTimeUnit": nil,
			},
			retained: metadata.JsonObject{
				"id": "3", "name": "Location",
				"displayTimeUnit": "MINUTE", "subLocScope": "SUB_LOCATION", "surrogateRefreshTimeUnit": "HOUR",
			},
			emptyExpected: map[string]any{"name": "Location"},
			nullExpected: map[string]any{
				"display_time_unit":           nil,
				"name":                        "Location",
				"sub_loc_scope":               nil,
				"surrogate_refresh_time_unit": nil,
			},
			retainedExpected: map[string]any{
				"display_time_unit":           "MINUTE",
				"name":                        "Location",
				"sub_loc_scope":               "SUB_LOCATION",
				"surrogate_refresh_time_unit": "HOUR",
			},
		},
		{
			resourceType: "zia_ssl_inspection_rules",
			empty: metadata.JsonObject{
				"id": "4", "name": "SSL",
				"action": []any{metadata.JsonObject{
					"type":                   "DO_NOT_DECRYPT",
					"doNotDecryptSubActions": []any{metadata.JsonObject{"bypassOtherPolicies": true, "minTlsVersion": ""}},
				}},
			},
			nullable: metadata.JsonObject{
				"id": "4", "name": "SSL",
				"action": []any{metadata.JsonObject{
					"type":                   "DO_NOT_DECRYPT",
					"doNotDecryptSubActions": []any{metadata.JsonObject{"bypassOtherPolicies": true, "minTlsVersion": nil}},
				}},
			},
			retained: metadata.JsonObject{
				"id": "4", "name": "SSL",
				"action": []any{metadata.JsonObject{
					"type":                   "DO_NOT_DECRYPT",
					"doNotDecryptSubActions": []any{metadata.JsonObject{"bypassOtherPolicies": true, "minTlsVersion": "TLSV1_2"}},
				}},
			},
			emptyExpected: map[string]any{
				"action": []any{map[string]any{
					"do_not_decrypt_sub_actions": []any{map[string]any{"bypass_other_policies": true}},
					"type":                       "DO_NOT_DECRYPT",
				}},
				"name": "SSL",
			},
			nullExpected: map[string]any{
				"action": []any{map[string]any{
					"do_not_decrypt_sub_actions": []any{map[string]any{
						"bypass_other_policies": true,
						"min_tls_version":       nil,
					}},
					"type": "DO_NOT_DECRYPT",
				}},
				"name": "SSL",
			},
			retainedExpected: map[string]any{
				"action": []any{map[string]any{
					"do_not_decrypt_sub_actions": []any{map[string]any{
						"bypass_other_policies": true,
						"min_tls_version":       "TLSV1_2",
					}},
					"type": "DO_NOT_DECRYPT",
				}},
				"name": "SSL",
			},
		},
	}

	for _, fixture := range cases {
		t.Run(fixture.resourceType, func(t *testing.T) {
			resource, ok := loaded.Resources[fixture.resourceType]
			if !ok {
				t.Fatalf("resource %s not found", fixture.resourceType)
			}
			schema, err := loaded.LoadResourceSchema(fixture.resourceType)
			if err != nil {
				t.Fatalf("LoadResourceSchema: %v", err)
			}
			empty, err := TransformLoadedItems(TransformLoadedItemsOptions{Resource: resource, Schema: schema, RawItems: []any{fixture.empty}})
			if err != nil {
				t.Fatalf("TransformLoadedItems(empty): %v", err)
			}
			nullable, err := TransformLoadedItems(TransformLoadedItemsOptions{Resource: resource, Schema: schema, RawItems: []any{fixture.nullable}})
			if err != nil {
				t.Fatalf("TransformLoadedItems(nullable): %v", err)
			}
			retained, err := TransformLoadedItems(TransformLoadedItemsOptions{Resource: resource, Schema: schema, RawItems: []any{fixture.retained}})
			if err != nil {
				t.Fatalf("TransformLoadedItems(retained): %v", err)
			}
			assertJSONEqual(t, fixture.resourceType+" empty", firstItem(t, empty), fixture.emptyExpected)
			assertJSONEqual(t, fixture.resourceType+" null", firstItem(t, nullable), fixture.nullExpected)
			assertJSONEqual(t, fixture.resourceType+" retained", firstItem(t, retained), fixture.retainedExpected)
		})
	}
}

func TestGenericSchemaShapingMergesConfiguredBlocksAndRecordsConflicts(t *testing.T) {
	schema := metadata.JsonObject{
		"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{"name": metadata.JsonObject{"required": true, "type": "string"}},
			"block_types": metadata.JsonObject{
				"groups": metadata.JsonObject{
					"nesting_mode": "set",
					"block": metadata.JsonObject{
						"attributes": metadata.JsonObject{
							"ids":  metadata.JsonObject{"optional": true, "type": []any{"set", "string"}},
							"mode": metadata.JsonObject{"optional": true, "type": "string"},
						},
					},
				},
			},
		},
	}
	result, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: sampleRuleResource(metadata.JsonObject{"merge_blocks": []any{"groups"}}),
		Schema:   schema,
		RawItems: []any{metadata.JsonObject{
			"groups": []any{
				metadata.JsonObject{"ids": "b", "mode": "first"},
				metadata.JsonObject{"ids": []any{"a"}, "mode": "second"},
			},
			"name": "Example",
		}},
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems: %v", err)
	}
	item, ok := result.Items["example"]
	if !ok {
		t.Fatalf("items missing 'example'; got %v", result.Items)
	}
	assertJSONEqual(t, "example item", item, map[string]any{
		"groups": []any{map[string]any{"ids": []any{"a", "b"}, "mode": "first"}},
		"name":   "Example",
	})
	if want := []string{"groups[].mode (conflicting values across merged elements; kept first)"}; !reflect.DeepEqual(result.Drops, want) {
		t.Fatalf("drops = %v, want %v", result.Drops, want)
	}
}

func TestDerivedReorderRequiresCompleteRulesAndSortsNumericOrders(t *testing.T) {
	result, err := DeriveReorderItems([]any{
		metadata.JsonObject{"id": "b", "ruleOrder": "10"},
		metadata.JsonObject{"id": "a", "ruleOrder": "2"},
	}, metadata.JsonObject{"from": "sample_rule", "policy_type": "ACCESS_POLICY"})
	if err != nil {
		t.Fatalf("DeriveReorderItems: %v", err)
	}
	assertJSONEqual(t, "reorder result", result, map[string]any{
		"ACCESS_POLICY": map[string]any{
			"policy_type": "ACCESS_POLICY",
			"rules": []any{
				map[string]any{"id": "a", "order": "2"},
				map[string]any{"id": "b", "order": "10"},
			},
		},
	})

	_, err = DeriveReorderItems(
		[]any{metadata.JsonObject{"id": "missing-order"}},
		metadata.JsonObject{"from": "sample_rule", "policy_type": "ACCESS_POLICY"},
	)
	if err == nil || !strings.Contains(err.Error(), "refusing to emit a partial reorder") {
		t.Fatalf("DeriveReorderItems error = %v, want a 'refusing to emit a partial reorder' error", err)
	}
}

func TestSkipIfLtePreservesWideIntegersAndPythonDecimalStringParsing(t *testing.T) {
	schema := metadata.JsonObject{
		"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{
				"name":  metadata.JsonObject{"required": true, "type": "string"},
				"order": metadata.JsonObject{"optional": true, "type": "number"},
			},
		},
	}
	result, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: sampleRuleResource(metadata.JsonObject{
			"skip_if_lte": []any{metadata.JsonObject{"order": json.Number("9007199254740992")}},
		}),
		Schema: schema,
		RawItems: []any{
			metadata.JsonObject{"name": "equal", "order": json.Number("9007199254740992")},
			metadata.JsonObject{"name": "above", "order": json.Number("9007199254740993")},
			metadata.JsonObject{"name": "hex", "order": "0x10"},
			metadata.JsonObject{"name": "decimal string", "order": "1_6"},
		},
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems: %v", err)
	}
	assertItemKeySet(t, result, []string{"above", "hex"})

	floats, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: sampleRuleResource(metadata.JsonObject{
			"skip_if_lte": []any{metadata.JsonObject{"order": json.Number("1.5")}},
		}),
		Schema: schema,
		RawItems: []any{
			metadata.JsonObject{"name": "integer", "order": json.Number("1")},
			metadata.JsonObject{"name": "fraction", "order": "1.25"},
			metadata.JsonObject{"name": "greater", "order": json.Number("2")},
		},
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems(floats): %v", err)
	}
	assertItemKeySet(t, floats, []string{"greater"})
}

// assertItemKeySet checks result.Items' key SET (not order): the Node
// test this ports (node-tests/generic-transform-core.test.ts's
// "skip_if_lte preserves wide integers...") asserts Object.keys(result.items)
// as an ordered array, relying on V8's Map/object insertion-order
// preservation. PullTransformResult.Items is a plain Go map by design (see
// its own doc comment: no real downstream consumer of the Node source's
// PullTransformResult.items ever relies on that order -- transform-artifacts.ts
// always re-sorts via sortedStrings(Object.keys(...)) first), so this port
// checks the key set instead of key order. Flagged per this port's
// semantic-uncertainty reporting convention.
func assertItemKeySet(t *testing.T, result PullTransformResult, want []string) {
	t.Helper()
	if len(result.Items) != len(want) {
		t.Fatalf("items = %v, want keys %v", result.Items, want)
	}
	for _, key := range want {
		if _, ok := result.Items[key]; !ok {
			t.Fatalf("items missing key %q; got %v", key, result.Items)
		}
	}
}

func TestNullStubsRecognizeComputedSchemaMembersWithoutEmittingThem(t *testing.T) {
	childBlock := metadata.JsonObject{
		"attributes": metadata.JsonObject{
			"computed_name": metadata.JsonObject{"computed": true, "type": "string"},
			"id":            metadata.JsonObject{"computed": true, "type": "number"},
			"setting":       metadata.JsonObject{"optional": true, "type": "string"},
		},
		"block_types": metadata.JsonObject{
			"computed_details": metadata.JsonObject{
				"nesting_mode": "list",
				"block": metadata.JsonObject{
					"attributes": metadata.JsonObject{"code": metadata.JsonObject{"computed": true, "type": "string"}},
				},
			},
		},
	}
	schema := metadata.JsonObject{
		"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{"name": metadata.JsonObject{"required": true, "type": "string"}},
			"block_types": metadata.JsonObject{
				"many_child":   metadata.JsonObject{"block": childBlock, "nesting_mode": "list"},
				"single_child": metadata.JsonObject{"block": childBlock, "nesting_mode": "single"},
			},
		},
	}
	stub := metadata.JsonObject{
		"computedDetails": []any{},
		"computedName":    "",
		"id":              json.Number("0"),
	}
	result, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: sampleRuleResource(metadata.JsonObject{}),
		Schema:   schema,
		RawItems: []any{metadata.JsonObject{"manyChild": []any{stub}, "name": "Example", "singleChild": stub}},
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems: %v", err)
	}
	assertJSONEqual(t, "items", result.Items, map[string]any{
		"example": map[string]any{"many_child": []any{}, "name": "Example"},
	})
	if len(result.Drops) != 0 {
		t.Fatalf("drops = %v, want none", result.Drops)
	}
}

func TestSchemaStringCoercionAcceptsSafeNativeNumbersProducedInternally(t *testing.T) {
	schema := metadata.JsonObject{
		"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{
				"name":  metadata.JsonObject{"required": true, "type": "string"},
				"quota": metadata.JsonObject{"optional": true, "type": "string"},
			},
		},
	}
	result, err := TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: sampleRuleResource(metadata.JsonObject{"divide": metadata.JsonObject{"quota": json.Number("1024")}}),
		Schema:   schema,
		RawItems: []any{metadata.JsonObject{"name": "Example", "quota": json.Number("2048")}},
	})
	if err != nil {
		t.Fatalf("TransformLoadedItems: %v", err)
	}
	item, ok := result.Items["example"]
	if !ok {
		t.Fatalf("items missing 'example'; got %v", result.Items)
	}
	// quota's schema type is "string" (not "number"): the divide override
	// runs first and produces a json.Number, which the schema's "string"
	// encoding then coerces down to a plain string -- matching the Node
	// test's own `assert.equal(result.items.example?.quota, "2")`.
	if got := item["quota"]; got != "2" {
		t.Fatalf("quota = %#v, want \"2\"", got)
	}

	_, err = TransformLoadedItems(TransformLoadedItemsOptions{
		Resource: sampleRuleResource(metadata.JsonObject{}),
		Schema:   schema,
		RawItems: []any{metadata.JsonObject{"name": "Raw native", "quota": 2.0}},
	})
	if err == nil || !strings.Contains(err.Error(), "raw transform numeric tokens must be LosslessNumber") {
		t.Fatalf("error = %v, want a 'raw transform numeric tokens must be LosslessNumber' error", err)
	}
}
