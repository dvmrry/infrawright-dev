package reconcile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const reconcileCompatibilitySHA256 = "bcf4358eae5789da6bb60715b6f98644af77e62aef0590d1e40833381751e0aa"

type reconcileCompatibilityFixture struct {
	SchemaVersion   int                          `json:"schema_version"`
	LiveReports     []reconcileCompatibilityCase `json:"live_reports"`
	RetainedReports []reconcileCompatibilityCase `json:"retained_reports"`
	HelperCases     []reconcileCompatibilityHelp `json:"helper_cases"`
}

type reconcileCompatibilityCase struct {
	Name   string `json:"name"`
	Input  Object `json:"input"`
	Report Object `json:"report"`
}

type reconcileCompatibilityHelp struct {
	Name   string `json:"name"`
	Input  Object `json:"input"`
	Output any    `json:"output"`
}

func loadReconcileCompatibility(t *testing.T) reconcileCompatibilityFixture {
	t.Helper()
	fixturePath := filepath.Join("testdata", "reconcile_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != reconcileCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, reconcileCompatibilitySHA256)
	}
	decoder := json.NewDecoder(bytes.NewReader(fixtureBytes))
	decoder.UseNumber()
	var fixture reconcileCompatibilityFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("json.Decode(%q) error: %v", fixturePath, err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("%s schema_version = %d, want 1", fixturePath, fixture.SchemaVersion)
	}
	return fixture
}

func TestReconcileCompatibilityReports(t *testing.T) {
	fixture := loadReconcileCompatibility(t)
	groups := []struct {
		name  string
		cases []reconcileCompatibilityCase
		want  int
	}{
		{name: "live", cases: fixture.LiveReports, want: 2},
		{name: "retained", cases: fixture.RetainedReports, want: 7},
	}
	for _, group := range groups {
		if got := len(group.cases); got != group.want {
			t.Fatalf("%s compatibility reports = %d, want %d", group.name, got, group.want)
		}
		for _, test := range group.cases {
			t.Run(group.name+"/"+test.Name, func(t *testing.T) {
				got := reconcileCompatibilityInput(t, test.Input)
				if !canonjson.JSONEqual(got, test.Report) {
					t.Errorf("ReconcileItems(%s) = %#v, want %#v", test.Name, got, test.Report)
				}
			})
		}
	}
}

func TestReconcileCompatibilityHelpers(t *testing.T) {
	fixture := loadReconcileCompatibility(t)
	if got, want := len(fixture.HelperCases), 3; got != want {
		t.Fatalf("compatibility helper cases = %d, want %d", got, want)
	}
	for _, test := range fixture.HelperCases {
		t.Run(test.Name, func(t *testing.T) {
			var got any
			switch test.Name {
			case "api_metadata_from_options:test_api_options_metadata_splits_read_only_and_provider_gaps#1":
				value := test.Input["value"]
				source, _ := test.Input["source"].(string)
				metadata, err := APIMetadataFromOptions(value, source)
				if err != nil {
					t.Fatalf("APIMetadataFromOptions(%s) error: %v", test.Name, err)
				}
				got = reconcileMetadataObject(metadata)
			case "load_resource_schema:test_cli_fails_on_unknown_when_requested#1", "load_resource_schema:test_loads_raw_terraform_provider_schema_shape#1":
				schema := test.Input["schema"].(map[string]any)
				jsonValue := schema["json"].(map[string]any)
				resourceType, _ := test.Input["resource_type"].(string)
				var providerSource *string
				if source, present := test.Input["provider_source"].(string); present {
					providerSource = &source
				}
				resourceSchema, err := ResourceSchemaFromData(jsonValue, resourceType, providerSource)
				if err != nil {
					t.Fatalf("ResourceSchemaFromData(%s) error: %v", test.Name, err)
				}
				got = resourceSchema
			default:
				t.Fatalf("unknown compatibility helper %q", test.Name)
			}
			if !canonjson.JSONEqual(got, test.Output) {
				t.Errorf("compatibility helper %s = %#v, want %#v", test.Name, got, test.Output)
			}
		})
	}
}

func reconcileCompatibilityInput(t *testing.T, input Object) Object {
	t.Helper()
	items, ok := input["items"].([]any)
	if !ok {
		t.Fatalf("compatibility input items = %#v, want []any", input["items"])
	}
	schema, ok := input["schema"].(map[string]any)
	if !ok {
		t.Fatalf("compatibility input schema = %#v, want object", input["schema"])
	}
	resourceType, ok := input["resource_type"].(string)
	if !ok {
		t.Fatalf("compatibility input resource_type = %#v, want string", input["resource_type"])
	}
	options := ReconcileOptions{Items: items, ResourceSchema: schema, ResourceType: resourceType}
	if override, ok := input["override"].(map[string]any); ok {
		options.Override = override
	}
	if metadata, ok := input["api_metadata"].(map[string]any); ok {
		options.APIMetadata = reconcileCompatibilityMetadata(t, metadata)
	}
	if metadata, ok := input["metadata"].(map[string]any); ok {
		options.APIMetadata = reconcileCompatibilityMetadata(t, metadata)
	}
	report, err := ReconcileItems(options)
	if err != nil {
		t.Fatalf("ReconcileItems(%s) error: %v", resourceType, err)
	}
	return report.AsMap()
}

func reconcileCompatibilityMetadata(t *testing.T, value Object) APIMetadata {
	t.Helper()
	metadata := APIMetadata{}
	for path, field := range value {
		object, ok := field.(map[string]any)
		if !ok {
			t.Fatalf("metadata field %q = %#v, want object", path, field)
		}
		metadata[path] = object
	}
	return metadata
}

func reconcileMetadataObject(value APIMetadata) Object {
	result := Object{}
	for path, field := range value {
		result[path] = field
	}
	return result
}
