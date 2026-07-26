package zpacorpus

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

const (
	zpaPreviousProviderSchemaEnv        = "ZPA_PREVIOUS_PROVIDER_SCHEMA"
	zpaProvider446SemanticProjectionSHA = "1b3af8e5a63c2b51e38eda1910b7c8de5b16f42452d2deeb4a0f901df30b3961"
	refreshAbsent                       = "null"
	refreshOptionalBool                 = `{"kind":"attribute","optional":true,"type":"bool"}`
	refreshOptionalComputedBool         = `{"computed":true,"kind":"attribute","optional":true,"type":"bool"}`
	refreshList                         = `{"kind":"block_type","nesting_mode":"list"}`
	refreshListMaxOne                   = `{"kind":"block_type","max_items":1,"nesting_mode":"list"}`
	refreshListMinOne                   = `{"kind":"block_type","min_items":1,"nesting_mode":"list"}`
	refreshSet                          = `{"kind":"block_type","nesting_mode":"set"}`
	refreshSetMinOne                    = `{"kind":"block_type","min_items":1,"nesting_mode":"set"}`
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

type refreshSchemaTransition struct {
	Path        string
	Before      string
	After       string
	Disposition string
}

func refreshDisposition(path, before, after, disposition string) refreshSchemaTransition {
	return refreshSchemaTransition{Path: path, Before: before, After: after, Disposition: disposition}
}

var zpaProvider449TransitionDispositions = []refreshSchemaTransition{
	refreshDisposition("resource_schemas/zpa_application_segment/block/attributes/bypass_on_reauth", refreshOptionalComputedBool, refreshOptionalBool,
		"accept provider ownership change; keep the generated input optional"),
	refreshDisposition("resource_schemas/zpa_application_segment_browser_access/block/block_types/clientless_apps", refreshListMinOne, refreshSetMinOne,
		"accept list-to-set module shape; no item identity derives from this block"),
	refreshDisposition("resource_schemas/zpa_application_segment_pra/block/block_types/common_apps_dto/block/block_types/apps_config", refreshList, refreshSet,
		"accept nested list-to-set module shape; no item identity derives from this block"),
	refreshDisposition("resource_schemas/zpa_browser_access/block/block_types/clientless_apps", refreshListMinOne, refreshSetMinOne,
		"accept list-to-set module shape; no item identity derives from this block"),
	refreshDisposition("resource_schemas/zpa_lss_config_controller/block/block_types/policy_rule_resource/block/attributes/device_posture_failure_notification_enabled", refreshAbsent, refreshOptionalBool,
		"retain as schema-visible but not source-verified provider behavior"),
	refreshDisposition("resource_schemas/zpa_policy_access_rule/block/attributes/device_posture_failure_notification_enabled", refreshAbsent, refreshOptionalBool,
		"admit from acknowledged drops with direct provider Read and expand evidence"),
	refreshDisposition("resource_schemas/zpa_policy_access_rule_v2/block/attributes/device_posture_failure_notification_enabled", refreshAbsent, refreshOptionalBool,
		"expose the source-verified optional bool in the generated module"),
	refreshDisposition("resource_schemas/zpa_policy_capabilities_rule/block/block_types/privileged_capabilities/block/attributes/control_session", refreshAbsent, refreshOptionalBool,
		"accept the new optional capability input"),
	refreshDisposition("resource_schemas/zpa_policy_capabilities_rule/block/block_types/privileged_capabilities/block/attributes/join_session", refreshAbsent, refreshOptionalBool,
		"accept the new optional capability input"),
	refreshDisposition("resource_schemas/zpa_policy_forwarding_rule/block/attributes/device_posture_failure_notification_enabled", refreshAbsent, refreshOptionalBool,
		"retain as schema-visible but not source-verified provider behavior"),
	refreshDisposition("resource_schemas/zpa_policy_portal_access_rule/block/block_types/privileged_portal_capabilities", refreshListMaxOne, refreshList,
		"retain provider schema and impose a source-backed singleton module boundary"),
	refreshDisposition("resource_schemas/zpa_policy_portal_access_rule/block/block_types/privileged_portal_capabilities/block/attributes/access_uninspected_file_sandbox", refreshAbsent, refreshOptionalBool,
		"accept the new optional portal capability input"),
	refreshDisposition("resource_schemas/zpa_policy_portal_access_rule/block/block_types/privileged_portal_capabilities/block/attributes/upload_inspected_sandbox", refreshAbsent, refreshOptionalBool,
		"accept the new optional portal capability input"),
	refreshDisposition("resource_schemas/zpa_policy_portal_access_rule/block/block_types/privileged_portal_capabilities/block/attributes/upload_inspected_scan", refreshAbsent, refreshOptionalBool,
		"accept the new optional portal capability input"),
	refreshDisposition("resource_schemas/zpa_policy_redirection_rule/block/attributes/device_posture_failure_notification_enabled", refreshAbsent, refreshOptionalBool,
		"retain as schema-visible but not source-verified provider behavior"),
	refreshDisposition("resource_schemas/zpa_policy_timeout_rule/block/attributes/device_posture_failure_notification_enabled", refreshAbsent, refreshOptionalBool,
		"retain as schema-visible but not source-verified provider behavior"),
}

func refreshNode(kind string, source metadata.JsonObject, keys ...string) (string, error) {
	node := metadata.JsonObject{"kind": kind}
	for _, key := range keys {
		if value, exists := source[key]; exists {
			node[key] = value
		}
	}
	encoded, err := json.Marshal(node)
	if err != nil {
		return "", fmt.Errorf("marshal %s schema node: %w", kind, err)
	}
	return string(encoded), nil
}

func refreshSemanticProjection(data []byte) (map[string]string, error) {
	var root metadata.JsonObject
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decode provider schema: %w", err)
	}
	projection := map[string]string{}
	var walkBlock func(metadata.JsonObject, string) error
	walkBlock = func(block metadata.JsonObject, path string) error {
		projection[path] = `{"kind":"block"}`
		attributes, err := metadata.TerraformAttributesForBlock(block, path)
		if err != nil {
			return err
		}
		for name, rawAttribute := range attributes {
			attribute, err := metadata.TerraformRequireObject(rawAttribute, path+".attributes."+name)
			if err != nil {
				return err
			}
			projection[path+"/attributes/"+name], err = refreshNode("attribute", attribute, "type", "required", "optional", "computed")
			if err != nil {
				return err
			}
		}
		blockTypes, err := metadata.TerraformBlockTypesForBlock(block, path)
		if err != nil {
			return err
		}
		for name, rawBlockType := range blockTypes {
			blockTypePath := path + "/block_types/" + name
			blockType, err := metadata.TerraformRequireObject(rawBlockType, blockTypePath)
			if err != nil {
				return err
			}
			projection[blockTypePath], err = refreshNode("block_type", blockType, "nesting_mode", "min_items", "max_items")
			if err != nil {
				return err
			}
			child, err := metadata.TerraformRequireObject(blockType["block"], blockTypePath+".block")
			if err != nil {
				return err
			}
			if err := walkBlock(child, blockTypePath+"/block"); err != nil {
				return err
			}
		}
		return nil
	}
	for _, surfaceName := range []string{"resource_schemas", "data_source_schemas"} {
		surface, err := metadata.TerraformRequireObject(root[surfaceName], surfaceName)
		if err != nil {
			return nil, err
		}
		for resourceType, rawSchema := range surface {
			path := surfaceName + "/" + resourceType
			projection[path] = `{"kind":"schema"}`
			schema, err := metadata.TerraformRequireObject(rawSchema, path)
			if err != nil {
				return nil, err
			}
			block, err := metadata.TerraformBlockForSchema(schema, path)
			if err != nil {
				return nil, err
			}
			if err := walkBlock(block, path+"/block"); err != nil {
				return nil, err
			}
		}
	}
	return projection, nil
}

func refreshSchemaTransitions(before, after map[string]string) []refreshSchemaTransition {
	paths := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		paths[path] = struct{}{}
	}
	for path := range after {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	var transitions []refreshSchemaTransition
	for _, path := range ordered {
		beforeValue, afterValue := before[path], after[path]
		if beforeValue == "" {
			beforeValue = refreshAbsent
		}
		if afterValue == "" {
			afterValue = refreshAbsent
		}
		if beforeValue != afterValue {
			transitions = append(transitions, refreshSchemaTransition{Path: path, Before: beforeValue, After: afterValue})
		}
	}
	return transitions
}

func refreshTransitionKey(transition refreshSchemaTransition) string {
	return transition.Path + "\x00" + transition.Before + "\x00" + transition.After
}

func requireRefreshTransitionSet(t *testing.T, actual, dispositions []refreshSchemaTransition) {
	t.Helper()
	actualKeys := make(map[string]string, len(actual))
	for _, transition := range actual {
		actualKeys[refreshTransitionKey(transition)] = fmt.Sprintf("%s: %s -> %s", transition.Path, transition.Before, transition.After)
	}
	dispositionKeys := make(map[string]string, len(dispositions))
	for _, transition := range dispositions {
		if strings.TrimSpace(transition.Disposition) == "" {
			t.Errorf("transition %s has an empty disposition", transition.Path)
		}
		key := refreshTransitionKey(transition)
		if _, duplicate := dispositionKeys[key]; duplicate {
			t.Errorf("transition %s has a duplicate disposition", transition.Path)
		}
		dispositionKeys[key] = fmt.Sprintf("%s: %s -> %s", transition.Path, transition.Before, transition.After)
	}
	var missing, stale []string
	for key, transition := range actualKeys {
		if _, exists := dispositionKeys[key]; !exists {
			missing = append(missing, transition)
		}
	}
	for key, transition := range dispositionKeys {
		if _, exists := actualKeys[key]; !exists {
			stale = append(stale, transition)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		t.Errorf("provider schema transition dispositions differ\nmissing dispositions:\n%s\nstale dispositions:\n%s",
			strings.Join(missing, "\n"), strings.Join(stale, "\n"))
	}
}

func TestProvider449SchemaTransitionDispositionsAreExact(t *testing.T) {
	root := repositoryRoot(t)
	currentPath := filepath.Join(root, "packs", "zpa", "schemas", "provider", "zpa.json")
	currentData, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", currentPath, err)
	}
	current, err := refreshSemanticProjection(currentData)
	if err != nil {
		t.Fatalf("refreshSemanticProjection(%q) error = %v, want nil", currentPath, err)
	}

	// Reversing every disposition must reproduce the pre-bump semantic
	// fingerprint. This rejects both undispositioned and stale transitions
	// without retaining a second full provider schema.
	reconstructed := make(map[string]string, len(current))
	for path, value := range current {
		reconstructed[path] = value
	}
	for _, transition := range zpaProvider449TransitionDispositions {
		actual := current[transition.Path]
		if actual == "" {
			actual = refreshAbsent
		}
		if actual != transition.After {
			t.Errorf("current schema transition %s = %s, want disposition after value %s", transition.Path, actual, transition.After)
		}
		if transition.Before == refreshAbsent {
			delete(reconstructed, transition.Path)
		} else {
			reconstructed[transition.Path] = transition.Before
		}
	}
	rendered, err := json.Marshal(reconstructed)
	if err != nil {
		t.Fatalf("json.Marshal(reconstructed pre-bump schema semantics) error = %v, want nil", err)
	}
	hash := sha256.Sum256(rendered)
	if got := fmt.Sprintf("%x", hash); got != zpaProvider446SemanticProjectionSHA {
		t.Errorf("reconstructed ZPA 4.4.6 semantic projection SHA-256 = %s, want %s; a transition is missing a disposition or a disposition is stale", got, zpaProvider446SemanticProjectionSHA)
	}

	// Save the checked-in schema before the next refresh and point this test at
	// it to print and enforce the exact new transition/disposition set.
	previousPath := strings.TrimSpace(os.Getenv(zpaPreviousProviderSchemaEnv))
	if previousPath == "" {
		return
	}
	previousData, err := os.ReadFile(previousPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q from %s) error = %v, want nil", previousPath, zpaPreviousProviderSchemaEnv, err)
	}
	previous, err := refreshSemanticProjection(previousData)
	if err != nil {
		t.Fatalf("refreshSemanticProjection(%q) error = %v, want nil", previousPath, err)
	}
	requireRefreshTransitionSet(t, refreshSchemaTransitions(previous, current), zpaProvider449TransitionDispositions)
	for _, transition := range zpaProvider449TransitionDispositions {
		t.Logf("schema transition %s: %s", transition.Path, transition.Disposition)
	}
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

	portalData, err := os.ReadFile(filepath.Join(root, "packs", "zpa", "overrides", "zpa_policy_portal_access_rule.json"))
	if err != nil {
		t.Fatalf("read portal-rule override: %v", err)
	}
	var portalOverride struct {
		ModuleSingleBlocks []string `json:"module_single_blocks"`
	}
	if err := json.Unmarshal(portalData, &portalOverride); err != nil {
		t.Fatalf("decode portal-rule override: %v", err)
	}
	wantPortalSingletons := []string{"privileged_portal_capabilities"}
	if !reflect.DeepEqual(portalOverride.ModuleSingleBlocks, wantPortalSingletons) {
		t.Errorf("portal module_single_blocks = %v, want %v", portalOverride.ModuleSingleBlocks, wantPortalSingletons)
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
