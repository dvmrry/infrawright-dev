package adopt

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func adoptionTestResource(adoptData, override metadata.JsonObject) metadata.LoadedResourceMetadata {
	registry := metadata.JsonObject{"generate": true, "product": "sample"}
	if adoptData != nil {
		registry["adopt"] = adoptData
	}
	return metadata.LoadedResourceMetadata{
		Type: "sample_resource", Product: "sample", Provider: "sample", Registry: registry, Override: override,
	}
}

func mustLosslessAdoptionItems(t *testing.T, text string) []any {
	t.Helper()
	value, err := canonjson.ParseDataJSONLosslessly(text)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly: %v", err)
	}
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("fixture type = %T, want []any", value)
	}
	return items
}

func TestAdoptionIdentitySourceVectors(t *testing.T) {
	resource := adoptionTestResource(metadata.JsonObject{
		"import_id": "{{tenant}}:{type}:{id}", "key_field": []any{"type", "name"},
	}, nil)
	result, err := DeriveAdoptionIdentities(
		mustLosslessAdoptionItems(t, `[{"id":9007199254740993,"name":"Rule One","type":"ACCESS"}]`),
		resource,
	)
	if err != nil {
		t.Fatalf("DeriveAdoptionIdentities: %v", err)
	}
	if got, want := result.Identities[0].Key, "access_rule_one"; got != want {
		t.Errorf("identity key = %q, want %q", got, want)
	}
	if got, want := result.Identities[0].ImportID, "{tenant}:ACCESS:9007199254740993"; got != want {
		t.Errorf("identity import ID = %q, want %q", got, want)
	}

	meta := AdoptionMetadata{IdentityFields: map[string]string{}, IdentityRenames: map[string]string{}, ImportID: "{id}", KeyFields: []string{"name"}}
	if got, err := DeriveAdoptionKey(map[string]any{"id": "fallback-7", "name": "東京"}, meta); err != nil || got != "id_fallback_7" {
		t.Fatalf("non-ASCII fallback = %q, %v; want id_fallback_7", got, err)
	}
}

func TestAdoptionClassificationSkipThenUnsupportedWithWideNumbers(t *testing.T) {
	rule := metadata.JsonObject{
		"evidence": []any{"https://example.invalid/provider-source"},
		"match":    metadata.JsonObject{"action": "ISOLATE"},
		"provider": metadata.JsonObject{"source": "example/sample", "version": "1.2.3"},
		"reason":   "provider cannot round-trip this object",
	}
	resource := adoptionTestResource(metadata.JsonObject{
		"key_field": "name", "skip_if": []any{metadata.JsonObject{"system": true}},
		"skip_if_lte":    []any{metadata.JsonObject{"order": json.Number("9007199254740992")}},
		"unsupported_if": []any{rule},
	}, nil)
	items := mustLosslessAdoptionItems(t, `[
		{"id":"system","name":"System","system":true,"action":"ISOLATE","order":99},
		{"id":"unsupported","name":"Unsupported","system":false,"action":"ISOLATE","order":9007199254740993},
		{"id":"low","name":"Low","system":false,"action":"BLOCK","order":9007199254740992},
		{"id":"high","name":"High","system":false,"action":"BLOCK","order":9007199254740993}
	]`)
	classified, err := ClassifyAdoptionRawItems(items, resource)
	if err != nil {
		t.Fatalf("ClassifyAdoptionRawItems: %v", err)
	}
	if got, want := []int{len(classified.Skipped), len(classified.Unsupported), len(classified.Eligible)}, []int{2, 1, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("classification counts = %v, want %v", got, want)
	}
	if got := classified.Eligible[0]["id"]; got != "high" {
		t.Errorf("eligible ID = %v, want high", got)
	}
}

func TestAdoptionIdentityValidationPrecedesStateLoader(t *testing.T) {
	policy, err := metadata.NewDriftPolicy(nil, "adoption metadata test")
	if err != nil {
		t.Fatalf("metadata.NewDriftPolicy: %v", err)
	}
	for name, override := range map[string]metadata.JsonObject{
		"import": {"import_id": json.Number("7")},
		"key":    {"key_field": nil},
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			_, err := AdoptResourceItems(policy, []any{map[string]any{"id": "UNINTENDED", "name": "Wrong Default"}}, adoptionTestResource(nil, override), metadata.LoadedPackRoot{}, func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
				called = true
				return map[string]OracleStateObject{}, nil
			}, nil)
			if err == nil || !strings.Contains(err.Error(), "adopt.") {
				t.Fatalf("AdoptResourceItems error = %v, want malformed adoption metadata", err)
			}
			if called {
				t.Fatal("state loader called after malformed identity metadata")
			}
		})
	}
}

func TestAdoptionMetadataRegistryPrecedenceAndNormalizedAliasCollisions(t *testing.T) {
	resource := adoptionTestResource(metadata.JsonObject{
		"identity_fields":  metadata.JsonObject{"ImportAlias": "details.ImportValue"},
		"identity_renames": metadata.JsonObject{"RegistryOld": "RegistryNew"},
		"import_id":        "registry:{import_alias}",
		"key_field":        []any{"registry_new", "name"},
		"skip_if":          []any{metadata.JsonObject{"system": true}},
		"skip_if_lte":      []any{metadata.JsonObject{"order": json.Number("2")}},
	}, metadata.JsonObject{
		"identity_fields": metadata.JsonObject{"legacy": "id"}, "renames": metadata.JsonObject{"legacy_old": "legacy_new"},
		"import_id": "legacy:{id}", "key_field": "legacy_name",
	})
	resolved, err := AdoptionMetadataFor(resource)
	if err != nil {
		t.Fatalf("AdoptionMetadataFor precedence: %v", err)
	}
	if got, want := resolved.ImportID, "registry:{import_alias}"; got != want {
		t.Errorf("ImportID = %q, want %q", got, want)
	}
	if got, want := resolved.KeyFields, []string{"registry_new", "name"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeyFields = %v, want %v", got, want)
	}
	if got := resolved.IdentityFields["import_alias"]; got != "details.ImportValue" {
		t.Errorf("identity field = %q, want registry value", got)
	}
	if got := resolved.IdentityRenames["registry_old"]; got != "RegistryNew" {
		t.Errorf("identity rename = %q, want registry value", got)
	}

	for name, adoptData := range map[string]metadata.JsonObject{
		"fields":  {"identity_fields": metadata.JsonObject{"ImportId": "one", "import_id": "two"}},
		"renames": {"identity_renames": metadata.JsonObject{"OldName": "one", "old_name": "two"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := AdoptionMetadataFor(adoptionTestResource(adoptData, nil))
			if err == nil || !strings.Contains(err.Error(), "normalized alias collision") {
				t.Fatalf("AdoptionMetadataFor collision error = %v", err)
			}
		})
	}
}

func TestAdoptionRawSnakeCollisionsFailBeforeStateLoading(t *testing.T) {
	policy, err := metadata.NewDriftPolicy(nil, "snake collision test")
	if err != nil {
		t.Fatalf("metadata.NewDriftPolicy: %v", err)
	}
	topLevel := []string{
		`[{"id":"1","name":"safe","displayName":"first","display_name":"second"}]`,
		`[{"id":"1","name":"safe","display_name":"second","displayName":"first"}]`,
	}
	for index, source := range topLevel {
		called := false
		_, err := AdoptResourceItems(policy, mustLosslessAdoptionItems(t, source), adoptionTestResource(nil, nil), metadata.LoadedPackRoot{}, func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
			called = true
			return nil, nil
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "snake_case key collision") {
			t.Fatalf("variant %d collision error = %v", index, err)
		}
		if called {
			t.Fatalf("variant %d invoked state loader", index)
		}
	}

	nestedResource := adoptionTestResource(metadata.JsonObject{
		"identity_fields": metadata.JsonObject{"import_id": "details.external_id"},
	}, nil)
	called := false
	_, err = AdoptResourceItems(policy, mustLosslessAdoptionItems(t, `[{"id":"1","name":"safe","details":{"externalId":"one","external_id":"two"}}]`), nestedResource, metadata.LoadedPackRoot{}, func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
		called = true
		return nil, nil
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "$raw.details") {
		t.Fatalf("nested collision error = %v", err)
	}
	if called {
		t.Fatal("nested collision invoked state loader")
	}
}

func TestAdoptionDuplicateKeysAndImportIDsFailBeforeStateLoading(t *testing.T) {
	policy, err := metadata.NewDriftPolicy(nil, "duplicate identity test")
	if err != nil {
		t.Fatalf("metadata.NewDriftPolicy: %v", err)
	}
	tests := []struct {
		name  string
		items string
		want  string
	}{
		{name: "key", items: `[{"id":"one","name":"Same"},{"id":"two","name":"Same"}]`, want: "duplicate derived key"},
		{name: "import", items: `[{"id":"same","name":"One"},{"id":"same","name":"Two"}]`, want: "duplicate import_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			_, err := AdoptResourceItems(policy, mustLosslessAdoptionItems(t, test.items), adoptionTestResource(nil, nil), metadata.LoadedPackRoot{}, func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
				called = true
				return nil, nil
			}, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AdoptResourceItems error = %v, want %q", err, test.want)
			}
			if called {
				t.Fatal("duplicate identity invoked state loader")
			}
		})
	}
}

func TestAdoptionStrictScalarMatchersSeparateBoolNumberNullAndAbsence(t *testing.T) {
	tests := []struct {
		name       string
		matcher    any
		items      string
		matchedIDs []string
	}{
		{name: "true", matcher: true, items: `[{"id":"true","marker":true},{"id":"one","marker":1}]`, matchedIDs: []string{"true"}},
		{name: "one", matcher: json.Number("1"), items: `[{"id":"one","marker":1},{"id":"true","marker":true}]`, matchedIDs: []string{"one"}},
		{name: "false", matcher: false, items: `[{"id":"false","marker":false},{"id":"zero","marker":0}]`, matchedIDs: []string{"false"}},
		{name: "null", matcher: nil, items: `[{"id":"null","marker":null},{"id":"absent"}]`, matchedIDs: []string{"null"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := metadata.JsonObject{
				"evidence": []any{"fixture"}, "match": metadata.JsonObject{"marker": test.matcher},
				"provider": metadata.JsonObject{"source": "example/test", "version": "1.0.0"}, "reason": "test",
			}
			classified, err := ClassifyAdoptionRawItems(mustLosslessAdoptionItems(t, test.items), adoptionTestResource(metadata.JsonObject{"unsupported_if": []any{rule}}, nil))
			if err != nil {
				t.Fatalf("ClassifyAdoptionRawItems: %v", err)
			}
			ids := make([]string, len(classified.Unsupported))
			for index, item := range classified.Unsupported {
				ids[index], _ = item.Item["id"].(string)
			}
			if !reflect.DeepEqual(ids, test.matchedIDs) {
				t.Fatalf("unsupported IDs = %v, want %v", ids, test.matchedIDs)
			}
		})
	}
}

func TestAdoptionUnsupportedDiagnosticUsesIDWhenNameIsNull(t *testing.T) {
	if got, want := adoptionItemLabel(map[string]any{"name": nil, "id": "stable-id"}), `"stable-id"`; got != want {
		t.Fatalf("adoptionItemLabel = %s, want %s", got, want)
	}
}

func TestCommittedRegistryAdoptionMetadataAndClassificationFixture(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	profile := filepath.Join(repositoryRoot, "packs", "full.packset.json")
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: filepath.Join(repositoryRoot, "packs"), ProfilePath: &profile, CatalogPath: &profile,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	explicit := 0
	for _, resource := range root.Resources {
		if _, present := resource.Registry["adopt"]; !present {
			continue
		}
		explicit++
		if _, err := AdoptionMetadataFor(resource); err != nil {
			t.Errorf("AdoptionMetadataFor(%s): %v", resource.Type, err)
		}
	}
	if explicit != 33 {
		t.Fatalf("explicit adoption metadata entries = %d, want 33", explicit)
	}
	fixtureText, err := metadata.ReadOptionalUTF8(filepath.Join(repositoryRoot, "node-tests", "fixtures", "zia-adoption-classification-v4.7.26.json"), "adoption classification fixture")
	if err != nil || fixtureText == nil {
		t.Fatalf("read classification fixture: %v", err)
	}
	fixture, err := canonjson.ParseDataJSONLosslessly(*fixtureText)
	if err != nil {
		t.Fatalf("parse classification fixture: %v", err)
	}
	resources := fixture.(map[string]any)["resources"].(map[string]any)
	for _, resourceType := range canonjson.SortedStrings(adoptMapKeys(resources)) {
		evidence := resources[resourceType].(map[string]any)
		combined := make([]any, 0)
		counts := make(map[string]int)
		for _, field := range []string{"skip", "system_skip", "unsupported", "keep"} {
			items, _ := evidence[field].([]any)
			counts[field] = len(items)
			combined = append(combined, items...)
		}
		classified, err := ClassifyAdoptionRawItems(combined, root.Resources[resourceType])
		if err != nil {
			t.Fatalf("ClassifyAdoptionRawItems(%s): %v", resourceType, err)
		}
		if got, want := len(classified.Skipped), counts["skip"]+counts["system_skip"]; got != want {
			t.Errorf("%s skipped = %d, want %d", resourceType, got, want)
		}
		if got, want := len(classified.Unsupported), counts["unsupported"]; got != want {
			t.Errorf("%s unsupported = %d, want %d", resourceType, got, want)
		}
		if got, want := len(classified.Eligible), counts["keep"]; got != want {
			t.Errorf("%s eligible = %d, want %d", resourceType, got, want)
		}
	}
}
