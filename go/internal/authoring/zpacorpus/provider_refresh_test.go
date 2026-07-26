package zpacorpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type refreshSchema struct {
	ResourceSchemas   map[string]refreshResource `json:"resource_schemas"`
	DataSourceSchemas map[string]any             `json:"data_source_schemas"`
}

type refreshResource struct {
	Block refreshBlock `json:"block"`
}

type refreshBlock struct {
	Attributes map[string]refreshAttribute `json:"attributes"`
	BlockTypes map[string]refreshBlockType `json:"block_types"`
}

type refreshAttribute struct {
	Optional bool `json:"optional"`
	Computed bool `json:"computed"`
}

type refreshBlockType struct {
	Block       refreshBlock `json:"block"`
	NestingMode string       `json:"nesting_mode"`
	MaxItems    *int         `json:"max_items"`
}

func TestProvider449SchemaRefreshBoundary(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "packs", "zpa", "schemas", "provider", "zpa.json"))
	if err != nil {
		t.Fatalf("read ZPA schema: %v", err)
	}
	var schema refreshSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode ZPA schema: %v", err)
	}
	if got := len(schema.ResourceSchemas); got != 55 {
		t.Errorf("ZPA provider resource schemas = %d, want 55", got)
	}
	if got := len(schema.DataSourceSchemas); got != 71 {
		t.Errorf("ZPA provider data-source schemas = %d, want 71", got)
	}

	bypass := schema.ResourceSchemas["zpa_application_segment"].Block.Attributes["bypass_on_reauth"]
	if !bypass.Optional || bypass.Computed {
		t.Errorf("zpa_application_segment.bypass_on_reauth = %+v, want Optional without Computed", bypass)
	}
	assertRefreshNestingMode(t, schema, "zpa_application_segment_browser_access", []string{"clientless_apps"}, "set")
	assertRefreshNestingMode(t, schema, "zpa_browser_access", []string{"clientless_apps"}, "set")
	assertRefreshNestingMode(t, schema, "zpa_application_segment_pra", []string{"common_apps_dto", "apps_config"}, "set")

	wantDevicePostureSchemas := []string{
		"zpa_lss_config_controller",
		"zpa_policy_access_rule",
		"zpa_policy_access_rule_v2",
		"zpa_policy_forwarding_rule",
		"zpa_policy_redirection_rule",
		"zpa_policy_timeout_rule",
	}
	var gotDevicePostureSchemas []string
	for resourceType, resource := range schema.ResourceSchemas {
		if refreshBlockContainsAttribute(resource.Block, "device_posture_failure_notification_enabled") {
			gotDevicePostureSchemas = append(gotDevicePostureSchemas, resourceType)
		}
	}
	sort.Strings(gotDevicePostureSchemas)
	if !reflect.DeepEqual(gotDevicePostureSchemas, wantDevicePostureSchemas) {
		t.Errorf("schemas containing device posture notification = %v, want %v", gotDevicePostureSchemas, wantDevicePostureSchemas)
	}

	capabilities := schema.ResourceSchemas["zpa_policy_capabilities_rule"].Block.BlockTypes["privileged_capabilities"].Block.Attributes
	for _, field := range []string{"control_session", "join_session"} {
		if !capabilities[field].Optional {
			t.Errorf("zpa_policy_capabilities_rule.privileged_capabilities.%s is not optional", field)
		}
	}
	portal := schema.ResourceSchemas["zpa_policy_portal_access_rule"].Block.BlockTypes["privileged_portal_capabilities"]
	if portal.MaxItems != nil {
		t.Errorf("zpa_policy_portal_access_rule.privileged_portal_capabilities.max_items = %v, want absent", *portal.MaxItems)
	}
	for _, field := range []string{"access_uninspected_file_sandbox", "upload_inspected_sandbox", "upload_inspected_scan"} {
		if !portal.Block.Attributes[field].Optional {
			t.Errorf("zpa_policy_portal_access_rule.privileged_portal_capabilities.%s is not optional", field)
		}
	}
}

func TestProvider449RefreshKeepsClaimsBoundedToExercisedRawSurface(t *testing.T) {
	root := repositoryRoot(t)
	registryData, err := os.ReadFile(filepath.Join(root, "packs", "zpa", "registry.json"))
	if err != nil {
		t.Fatalf("read ZPA registry: %v", err)
	}
	var registry map[string]any
	if err := json.Unmarshal(registryData, &registry); err != nil {
		t.Fatalf("decode ZPA registry: %v", err)
	}
	if got := len(registry); got != 54 {
		t.Errorf("registered ZPA resources = %d, want 54", got)
	}
	fixtures, err := filepath.Glob(filepath.Join(root, "packs", "_shared", "zscaler", "demo", "zpa_*.json"))
	if err != nil {
		t.Fatalf("glob ZPA raw fixtures: %v", err)
	}
	if got := len(fixtures); got != 7 {
		t.Errorf("exercised ZPA raw fixtures = %d, want 7; update the bounded coverage claim when the corpus changes", got)
	}

	overrideData, err := os.ReadFile(filepath.Join(root, "packs", "zpa", "overrides", "zpa_policy_access_rule.json"))
	if err != nil {
		t.Fatalf("read access-rule override: %v", err)
	}
	var override struct {
		AcknowledgedDrops []string       `json:"acknowledged_drops"`
		DropIfDefault     map[string]any `json:"drop_if_default"`
	}
	if err := json.Unmarshal(overrideData, &override); err != nil {
		t.Fatalf("decode access-rule override: %v", err)
	}
	for _, field := range override.AcknowledgedDrops {
		if field == "device_posture_failure_notification_enabled" {
			t.Fatal("device_posture_failure_notification_enabled remains acknowledged as a dropped access-rule field")
		}
	}

	for _, resourceType := range []string{"zpa_application_segment_browser_access", "zpa_browser_access"} {
		browserData, err := os.ReadFile(filepath.Join(root, "packs", "zpa", "overrides", resourceType+".json"))
		if err != nil {
			t.Fatalf("read %s override: %v", resourceType, err)
		}
		var browserOverride struct {
			DropIfDefault map[string]any `json:"drop_if_default"`
		}
		if err := json.Unmarshal(browserData, &browserOverride); err != nil {
			t.Fatalf("decode %s override: %v", resourceType, err)
		}
		for path := range browserOverride.DropIfDefault {
			for _, field := range []string{"ext_domain", "ext_label"} {
				if path == field || strings.HasSuffix(path, "."+field) {
					t.Errorf("%s %s has an evidence-free drop_if_default rule", resourceType, path)
				}
			}
		}
	}
}

func assertRefreshNestingMode(t *testing.T, schema refreshSchema, resourceType string, path []string, want string) {
	t.Helper()
	block := schema.ResourceSchemas[resourceType].Block
	for index, name := range path {
		blockType, exists := block.BlockTypes[name]
		if !exists {
			t.Fatalf("%s block path %v is missing %q", resourceType, path, name)
		}
		if index == len(path)-1 && blockType.NestingMode != want {
			t.Errorf("%s block path %v nesting mode = %q, want %q", resourceType, path, blockType.NestingMode, want)
		}
		block = blockType.Block
	}
}

func refreshBlockContainsAttribute(block refreshBlock, name string) bool {
	if _, exists := block.Attributes[name]; exists {
		return true
	}
	for _, nested := range block.BlockTypes {
		if refreshBlockContainsAttribute(nested.Block, name) {
			return true
		}
	}
	return false
}
