package reconcile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const authoritySHA256 = "fff36234703a253bf903b97c2396a8d2d65a7b50b82407eff752eeb86c521004"

type frozenAuthority struct {
	Kind                 string                 `json:"kind"`
	Version              int                    `json:"version"`
	NodeLiveDifferential frozenLiveDifferential `json:"node_live_differential"`
	RetainedUnitTest     frozenRetainedUnitTest `json:"retained_unittest"`
}

type frozenLiveDifferential struct {
	ReportCases []frozenNodeReportCase `json:"report_cases"`
}

type frozenRetainedUnitTest struct {
	HelperCases []frozenHelperCase `json:"helper_cases"`
	ReportCases []frozenReportCase `json:"report_cases"`
}

type frozenNodeReportCase struct {
	Name         string `json:"name"`
	Input        Object `json:"input"`
	PythonReport Object `json:"python_report"`
}

type frozenReportCase struct {
	Name   string `json:"name"`
	Input  Object `json:"input"`
	Report Object `json:"report"`
}

type frozenHelperCase struct {
	Name   string `json:"name"`
	Input  Object `json:"input"`
	Output any    `json:"output"`
}

func loadAuthority(t *testing.T) frozenAuthority {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "node-tests", "fixtures", "python-reconcile-schema-api-v1.json")
	authorityBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("loadAuthority(%q): %v", path, err)
	}
	gotSHA := sha256.Sum256(authorityBytes)
	if got, want := hex.EncodeToString(gotSHA[:]), authoritySHA256; got != want {
		t.Fatalf("loadAuthority(%q) SHA-256 = %q, want %q", path, got, want)
	}
	decoder := json.NewDecoder(bytes.NewReader(authorityBytes))
	decoder.UseNumber()
	var authority frozenAuthority
	if err := decoder.Decode(&authority); err != nil {
		t.Fatalf("loadAuthority(%q) decode: %v", path, err)
	}
	if got, want := authority.Kind, "infrawright.python-reconcile-schema-api-authority"; got != want {
		t.Fatalf("loadAuthority(%q).kind = %q, want %q", path, got, want)
	}
	if got, want := authority.Version, 1; got != want {
		t.Fatalf("loadAuthority(%q).version = %d, want %d", path, got, want)
	}
	return authority
}

func reconcileFrozenInput(t *testing.T, input Object) Object {
	t.Helper()
	items, itemsOK := input["items"].([]any)
	if !itemsOK {
		t.Fatalf("ReconcileItems(%#v) items = %#v, want []any", input, input["items"])
	}
	schema, schemaOK := input["schema"].(map[string]any)
	if !schemaOK {
		t.Fatalf("ReconcileItems(%#v) schema = %#v, want object", input, input["schema"])
	}
	resourceType, resourceTypeOK := input["resource_type"].(string)
	if !resourceTypeOK {
		t.Fatalf("ReconcileItems(%#v) resource_type = %#v, want string", input, input["resource_type"])
	}
	options := ReconcileOptions{Items: items, ResourceSchema: schema, ResourceType: resourceType}
	if override, ok := input["override"].(map[string]any); ok {
		options.Override = override
	}
	if apiMetadata, ok := input["api_metadata"].(map[string]any); ok {
		options.APIMetadata = metadataFromObject(t, apiMetadata)
	}
	if apiMetadata, ok := input["metadata"].(map[string]any); ok {
		options.APIMetadata = metadataFromObject(t, apiMetadata)
	}
	report, err := ReconcileItems(options)
	if err != nil {
		t.Fatalf("ReconcileItems(%#v) error = %v, want nil", input, err)
	}
	return report.AsMap()
}

func metadataFromObject(t *testing.T, value Object) APIMetadata {
	t.Helper()
	metadata := APIMetadata{}
	for path, field := range value {
		object, ok := field.(map[string]any)
		if !ok {
			t.Fatalf("metadataFromObject(%#v) field %q = %#v, want object", value, path, field)
		}
		metadata[path] = object
	}
	return metadata
}

func requireJSONEqual(t *testing.T, function string, input, got, want any) {
	t.Helper()
	if !canonjson.JSONEqual(got, want) {
		t.Errorf("%s(%#v) = %#v, want %#v", function, input, got, want)
	}
}

func TestFrozenAuthorityReplaysNodeLiveDifferentialReports(t *testing.T) {
	authority := loadAuthority(t)
	if got, want := len(authority.NodeLiveDifferential.ReportCases), 2; got != want {
		t.Fatalf("node_live_differential.report_cases = %d, want %d", got, want)
	}
	for _, frozen := range authority.NodeLiveDifferential.ReportCases {
		t.Run(frozen.Name, func(t *testing.T) {
			requireJSONEqual(t, "ReconcileItems", frozen.Input, reconcileFrozenInput(t, frozen.Input), frozen.PythonReport)
		})
	}
}

func TestFrozenAuthorityReplaysRetainedUnitReports(t *testing.T) {
	authority := loadAuthority(t)
	if got, want := len(authority.RetainedUnitTest.ReportCases), 7; got != want {
		t.Fatalf("retained_unittest.report_cases = %d, want %d", got, want)
	}
	for _, frozen := range authority.RetainedUnitTest.ReportCases {
		t.Run(frozen.Name, func(t *testing.T) {
			requireJSONEqual(t, "ReconcileItems", frozen.Input, reconcileFrozenInput(t, frozen.Input), frozen.Report)
		})
	}
}

func TestFrozenAuthorityReplaysNonOpenAPIHelpers(t *testing.T) {
	authority := loadAuthority(t)
	var replayed int
	for _, frozen := range authority.RetainedUnitTest.HelperCases {
		switch frozen.Name {
		case "api_metadata_from_options:test_api_options_metadata_splits_read_only_and_provider_gaps#1":
			value := frozen.Input["value"]
			source, _ := frozen.Input["source"].(string)
			got, err := APIMetadataFromOptions(value, source)
			if err != nil {
				t.Fatalf("APIMetadataFromOptions(%#v) error = %v, want nil", frozen.Input, err)
			}
			requireJSONEqual(t, "APIMetadataFromOptions", frozen.Input, metadataAsObject(got), frozen.Output)
			replayed++
		case "load_resource_schema:test_cli_fails_on_unknown_when_requested#1", "load_resource_schema:test_loads_raw_terraform_provider_schema_shape#1":
			schema, schemaOK := frozen.Input["schema"].(map[string]any)
			if !schemaOK {
				t.Fatalf("ResourceSchemaFromData(%#v) schema = %#v, want object", frozen.Input, frozen.Input["schema"])
			}
			jsonValue, jsonOK := schema["json"].(map[string]any)
			if !jsonOK {
				t.Fatalf("ResourceSchemaFromData(%#v) schema.json = %#v, want object", frozen.Input, schema["json"])
			}
			resourceType, _ := frozen.Input["resource_type"].(string)
			var providerSource *string
			if source, present := frozen.Input["provider_source"].(string); present {
				providerSource = &source
			}
			got, err := ResourceSchemaFromData(jsonValue, resourceType, providerSource)
			if err != nil {
				t.Fatalf("ResourceSchemaFromData(%#v) error = %v, want nil", frozen.Input, err)
			}
			requireJSONEqual(t, "ResourceSchemaFromData", frozen.Input, got, frozen.Output)
			replayed++
		}
	}
	if got, want := replayed, 3; got != want {
		t.Errorf("non-OpenAPI helper replay count = %d, want %d", got, want)
	}
}

func metadataAsObject(value APIMetadata) Object {
	result := Object{}
	for path, field := range value {
		result[path] = field
	}
	return result
}

func TestReconcileItemsRejectsMalformedItemsWithoutPanic(t *testing.T) {
	_, err := ReconcileItems(ReconcileOptions{ResourceType: "example", ResourceSchema: Object{"block": Object{}}, Items: []any{"not-object"}})
	if err == nil {
		t.Errorf("ReconcileItems(malformed item) error = nil, want non-nil")
	}
}

func TestReconcileItemsClassifiesUncoercibleNumberShapeMismatch(t *testing.T) {
	report, err := ReconcileItems(ReconcileOptions{
		ResourceType: "example",
		ResourceSchema: Object{"block": Object{"attributes": Object{
			"number": Object{"optional": true, "type": "number"},
		}}},
		Items: []any{Object{"number": Object{"uncoercible": "object"}}},
	})
	if err != nil {
		t.Fatalf("ReconcileItems(uncoercible number object) error = %v, want nil", err)
	}
	entries := report.Paths(BucketShapeMismatch)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("ReconciliationReport.Paths(shape_mismatch) = %#v, want one expected_number entry", entries)
	}
	entry := entries[0]
	if got, want := entry["path"], "number"; got != want {
		t.Errorf("ReconciliationReport.Paths(shape_mismatch) path = %#v, want %#v", got, want)
	}
	if got, want := entry["reasons"].(Object)["expected_number"], float64(1); got != want {
		t.Errorf("ReconciliationReport.Paths(shape_mismatch) reasons = %#v, want expected_number count %#v", entry["reasons"], want)
	}
	if got := report.Paths(BucketKept); len(got) != 0 {
		t.Errorf("ReconciliationReport.Paths(kept) = %#v, want no uncoercible number object", got)
	}
}

func TestReconcileItemsRecordsPresentNullRename(t *testing.T) {
	report, err := ReconcileItems(ReconcileOptions{
		ResourceType: "example",
		ResourceSchema: Object{"block": Object{"attributes": Object{
			"new_name": Object{"optional": true, "type": "string"},
		}}},
		Override: Object{"renames": Object{"old_name": "new_name"}},
		Items:    []any{Object{"oldName": nil}},
	})
	if err != nil {
		t.Fatalf("ReconcileItems(present-null rename) error = %v, want nil", err)
	}
	entries := report.Paths(BucketRenamed)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("ReconciliationReport.Paths(renamed) = %#v, want one null rename entry", entries)
	}
	entry := entries[0]
	if got, want := entry["path"], "old_name"; got != want {
		t.Errorf("ReconciliationReport.Paths(renamed) path = %#v, want %#v", got, want)
	}
	if got, want := entry["count"], float64(1); got != want {
		t.Errorf("ReconciliationReport.Paths(renamed) count = %#v, want %#v", got, want)
	}
	if got, want := entry["types"].(Object)["null"], float64(1); got != want {
		t.Errorf("ReconciliationReport.Paths(renamed) types = %#v, want null count %#v", entry["types"], want)
	}
}

func TestReconcileItemsSkippedNullNameUsesIDType(t *testing.T) {
	report, err := ReconcileItems(ReconcileOptions{
		ResourceType:   "example",
		ResourceSchema: Object{"block": Object{"attributes": Object{}}},
		Override:       Object{"skip_if_lte": []any{Object{"order": float64(0)}}},
		Items:          []any{Object{"name": nil, "id": json.Number("7"), "order": float64(0)}},
	})
	if err != nil {
		t.Fatalf("ReconcileItems(skipped null name) error = %v, want nil", err)
	}
	entries := report.Paths(BucketSkipped)
	if got, want := len(entries), 1; got != want {
		t.Fatalf("ReconciliationReport.Paths(skipped) = %#v, want one skipped entry", entries)
	}
	if got, want := entries[0]["types"].(Object)["int"], float64(1); got != want {
		t.Errorf("ReconciliationReport.Paths(skipped) types = %#v, want id int count %#v", entries[0]["types"], want)
	}
	if _, nullType := entries[0]["types"].(Object)["null"]; nullType {
		t.Errorf("ReconciliationReport.Paths(skipped) types = %#v, want no null name type", entries[0]["types"])
	}
}

func TestProviderSchemaFromTerraformDumpRejectsAmbiguousResource(t *testing.T) {
	data := Object{"provider_schemas": Object{
		"registry.example/one": Object{"resource_schemas": Object{"example": Object{}}},
		"registry.example/two": Object{"resource_schemas": Object{"example": Object{}}},
	}}
	_, err := ProviderSchemaFromTerraformDump(data, "example", nil)
	if err == nil {
		t.Errorf("ProviderSchemaFromTerraformDump(%#v, %q, nil) error = nil, want ambiguous-provider error", data, "example")
	}
}

func TestProviderSchemaSelectionPreservesProviderSourcePresence(t *testing.T) {
	data := Object{"provider_schemas": Object{
		"registry.example/acme": Object{"resource_schemas": Object{"example": Object{"block": Object{}}}},
	}}
	if _, err := ProviderSchemaFromTerraformDump(data, "example", nil); err != nil {
		t.Errorf("ProviderSchemaFromTerraformDump(%#v, %q, nil) error = %v, want nil", data, "example", err)
	}
	empty := ""
	if _, err := ProviderSchemaFromTerraformDump(data, "example", &empty); err == nil {
		t.Errorf("ProviderSchemaFromTerraformDump(%#v, %q, %q) error = nil, want explicit-empty-source error", data, "example", empty)
	}
	exact := "registry.example/acme"
	if _, err := ProviderSchemaFromTerraformDump(data, "example", &exact); err != nil {
		t.Errorf("ProviderSchemaFromTerraformDump(%#v, %q, %q) error = %v, want nil", data, "example", exact, err)
	}
	suffix := "acme"
	if _, err := ProviderSchemaFromTerraformDump(data, "example", &suffix); err != nil {
		t.Errorf("ProviderSchemaFromTerraformDump(%#v, %q, %q) error = %v, want nil", data, "example", suffix, err)
	}
	if _, err := ResourceSchemaFromData(data, "example", nil); err != nil {
		t.Errorf("ResourceSchemaFromData(%#v, %q, nil) error = %v, want nil", data, "example", err)
	}
	if _, err := ResourceSchemaFromData(data, "example", &empty); err == nil {
		t.Errorf("ResourceSchemaFromData(%#v, %q, %q) error = nil, want explicit-empty-source error", data, "example", empty)
	}
}

func TestReconcileItemsRejectsNonFiniteAuthoringNumbers(t *testing.T) {
	for _, number := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := ReconcileItems(ReconcileOptions{
			ResourceType:   "example",
			ResourceSchema: Object{"block": Object{"attributes": Object{}}},
			Items:          []any{Object{"nested": []any{Object{"number": number}}}},
		})
		if err == nil {
			t.Errorf("ReconcileItems(nested non-finite %v) error = nil, want non-nil", number)
		}
	}
}

func TestAPIMetadataFromOptionsRejectsNestedNonFiniteAuthoringNumbers(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "PATCH"} {
		for _, number := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			options := Object{"actions": Object{method: Object{
				"field": Object{"nested": []any{Object{"number": number}}},
			}}}
			got, err := APIMetadataFromOptions(options, "<options>")
			if err == nil {
				t.Errorf("APIMetadataFromOptions(%s nested non-finite %v) error = nil, want non-nil", method, number)
			}
			if got != nil {
				t.Errorf("APIMetadataFromOptions(%s nested non-finite %v) = %#v, want nil result", method, number, got)
			}
		}
	}
}

func TestAPIMetadataFromOptionsRejectsProcessedMetadataSnakeKeyCollisions(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "PATCH"} {
		options := Object{"actions": Object{method: Object{
			"field": Object{"camelCase": "one", "camel_case": Object{"different": "shape"}},
		}}}
		got, err := APIMetadataFromOptions(options, "<options>")
		if err == nil {
			t.Errorf("APIMetadataFromOptions(%s processed collision) error = nil, want non-nil", method)
		}
		if got != nil {
			t.Errorf("APIMetadataFromOptions(%s processed collision) = %#v, want nil result", method, got)
		}
	}
}

func TestAPIMetadataFromOptionsIgnoresUnprocessedBoundaryValues(t *testing.T) {
	getOnly := Object{"actions": Object{"GET": Object{
		"field": Object{"number": math.NaN(), "camelCase": "one", "camel_case": Object{"different": "shape"}},
	}}}
	gotMetadata, err := APIMetadataFromOptions(getOnly, "<options>")
	if err != nil {
		t.Errorf("APIMetadataFromOptions(GET unprocessed boundary values) error = %v, want nil", err)
	}
	if got, want := len(gotMetadata), 0; got != want {
		t.Errorf("APIMetadataFromOptions(GET unprocessed boundary values) = %#v, want empty metadata", gotMetadata)
	}
	unknownTopLevel := Object{"unknown": Object{
		"number": math.Inf(1), "camelCase": "one", "camel_case": Object{"different": "shape"},
	}}
	gotMetadata, err = APIMetadataFromOptions(unknownTopLevel, "<options>")
	if err != nil {
		t.Errorf("APIMetadataFromOptions(unknown top-level boundary values) error = %v, want nil", err)
	}
	if got, want := len(gotMetadata), 0; got != want {
		t.Errorf("APIMetadataFromOptions(unknown top-level boundary values) = %#v, want empty metadata", gotMetadata)
	}
	nonObjectPostMember := Object{"actions": Object{"POST": Object{"field": math.Inf(-1)}}}
	gotMetadata, err = APIMetadataFromOptions(nonObjectPostMember, "<options>")
	if err != nil {
		t.Errorf("APIMetadataFromOptions(non-object POST member) error = %v, want nil", err)
	}
	if got, want := len(gotMetadata), 0; got != want {
		t.Errorf("APIMetadataFromOptions(non-object POST member) = %#v, want empty metadata", gotMetadata)
	}
}

func TestReconcileItemsRejectsSnakeKeyCollisionsAtEveryDepth(t *testing.T) {
	assertCollision := func(t *testing.T, input Object) {
		t.Helper()
		report, err := ReconcileItems(ReconcileOptions{
			ResourceType:   "example",
			ResourceSchema: Object{"block": Object{"attributes": Object{}}},
			Items:          []any{input},
		})
		if err == nil {
			t.Errorf("ReconcileItems(%#v) error = nil, want snake-key-collision error", input)
		}
		if report != nil {
			t.Errorf("ReconcileItems(%#v) report = %#v, want nil after snake-key-collision error", input, report)
		}
	}
	topForward := Object{}
	topForward["camelCase"] = "scalar"
	topForward["camel_case"] = Object{"different": "shape"}
	assertCollision(t, topForward)
	topReversed := Object{}
	topReversed["camel_case"] = Object{"different": "shape"}
	topReversed["camelCase"] = "scalar"
	assertCollision(t, topReversed)
	nestedForward := Object{"outer": Object{}}
	nestedForward["outer"].(Object)["camelCase"] = "scalar"
	nestedForward["outer"].(Object)["camel_case"] = Object{"different": "shape"}
	assertCollision(t, nestedForward)
	nestedReversed := Object{"outer": Object{}}
	nestedReversed["outer"].(Object)["camel_case"] = Object{"different": "shape"}
	nestedReversed["outer"].(Object)["camelCase"] = "scalar"
	assertCollision(t, nestedReversed)
}

func TestReportCodePointOrderingAndDefensiveCopies(t *testing.T) {
	report, err := ReconcileItems(ReconcileOptions{
		ResourceType:   "unicode",
		ResourceSchema: Object{"block": Object{"attributes": Object{}}},
		Items:          []any{Object{"\U00010000": float64(1), "\ue000": float64(1), "a": float64(1)}},
	})
	if err != nil {
		t.Fatalf("ReconcileItems(unicode item) error = %v, want nil", err)
	}
	first := report.AsMap()
	unknown := first["paths"].(Object)["unknown"].([]any)
	if got, want := unknown[0].(Object)["path"], "a"; got != want {
		t.Errorf("ReconciliationReport.AsMap(unicode item) first path = %#v, want %#v", got, want)
	}
	if got, want := unknown[1].(Object)["path"], "\ue000"; got != want {
		t.Errorf("ReconciliationReport.AsMap(unicode item) second path = %#v, want %#v", got, want)
	}
	if got, want := unknown[2].(Object)["path"], "\U00010000"; got != want {
		t.Errorf("ReconciliationReport.AsMap(unicode item) third path = %#v, want %#v", got, want)
	}
	unknown[0].(Object)["path"] = "mutated"
	unknown[0].(Object)["reasons"].(Object)["mutated"] = float64(99)
	second := report.AsMap()
	got := second["paths"].(Object)["unknown"].([]any)[0].(Object)
	if gotPath, wantPath := got["path"], "a"; gotPath != wantPath {
		t.Errorf("ReconciliationReport.AsMap(defensive copy) path = %#v, want %#v", gotPath, wantPath)
	}
	if _, mutated := got["reasons"].(Object)["mutated"]; mutated {
		t.Errorf("ReconciliationReport.AsMap(defensive copy) reasons = %#v, want no mutated key", got["reasons"])
	}
}
