package assessment

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

const guidanceStatusEffect = "informational only; plan remains blocked"

type rawManifestGuidance struct {
	providers      []string
	providerConfig []map[string]any
	absentDefaults []map[string]any
	dynamicSchema  []map[string]any
}

// AssessmentGuidanceSource is an immutable snapshot of the active pack
// metadata used solely to explain blocked saved-plan findings.
type AssessmentGuidanceSource struct {
	providersByResource map[string]string
	providerPrefixes    map[string]string
	manifests           []rawManifestGuidance
}

// AssessmentGuidanceGroup contains the explanatory annotations for one
// assessed root. Guidance is informational and cannot change classification.
type AssessmentGuidanceGroup struct {
	Tenant  string           `json:"tenant"`
	Label   string           `json:"label"`
	Entries []map[string]any `json:"entries"`
}

// CollectAssessmentGuidanceOptions supplies one classified plan and its
// source-backed pack metadata to CollectAssessmentGuidance.
type CollectAssessmentGuidanceOptions struct {
	Source   AssessmentGuidanceSource
	Tenant   string
	Label    string
	Members  []string
	Plan     map[string]any
	Findings []PlanFinding
}

type candidatePath struct {
	source       string
	address      string
	resourceType string
	before       any
	path         PlanPath
	formatted    string
}

type guidanceEntry map[string]any

type guidanceScope struct {
	kind  string
	value string
}

type validatedLaneRule struct {
	raw      map[string]any
	provider string
	path     string
	scope    guidanceScope
	version  *string
}

var (
	providerRemediationKeys = guidanceStringSet("kind", "mode", "evidence", "safety")
	providerModes           = guidanceStringSet("diagnostic_only", "required_external", "renderable_default")
	absentKinds             = guidanceStringSet(
		"api_absent",
		"api_explicit_default",
		"provider_absent_placeholder",
		"terraform_schema_optional_default",
		"real_configured_falsey",
		"provider_server_side_singleton_default",
		"paid_disabled_or_api_boundary_default",
	)
	absentActions = guidanceStringSet(
		"diagnostic_only",
		"manual_review_required",
		"preserve_explicit_falsey",
	)
	absentAcceptedKeys = guidanceStringSet(
		"id", "provider", "resource_type", "resource_prefix", "path", "kind",
		"observed_value", "action", "evidence", "reason", "plan_path",
		"raw_api_path", "provider_state_path",
	)
	absentKindActions = map[string]map[string]struct{}{
		"api_absent":                             guidanceStringSet("diagnostic_only", "manual_review_required"),
		"api_explicit_default":                   guidanceStringSet("diagnostic_only", "manual_review_required"),
		"provider_absent_placeholder":            guidanceStringSet("diagnostic_only", "manual_review_required"),
		"terraform_schema_optional_default":      guidanceStringSet("diagnostic_only", "manual_review_required"),
		"real_configured_falsey":                 guidanceStringSet("preserve_explicit_falsey", "diagnostic_only", "manual_review_required"),
		"provider_server_side_singleton_default": guidanceStringSet("diagnostic_only", "manual_review_required"),
		"paid_disabled_or_api_boundary_default":  guidanceStringSet("diagnostic_only", "manual_review_required"),
	}
	absentObservedKinds = guidanceStringSet(
		"provider_absent_placeholder",
		"api_explicit_default",
		"terraform_schema_optional_default",
	)
	dynamicKinds = guidanceStringSet(
		"provider_state_only",
		"provider_computed_map",
		"freeform_object",
		"opaque_json_blob",
		"map_key_discovered_after_import",
		"unstable_collection_identity",
		"schema_unknown_but_provider_observed",
		"raw_api_only_provider_blind",
		"provider_observed_projection_unsafe",
	)
	dynamicOwnerships   = guidanceStringSet("user_owned", "provider_computed", "server_owned", "unknown")
	dynamicActions      = guidanceStringSet("diagnostic_only", "manual_review_required")
	dynamicAcceptedKeys = guidanceStringSet(
		"id", "provider", "provider_version_constraint", "resource_type",
		"resource_prefix", "path", "kind", "ownership", "action", "evidence",
		"reason", "raw_api_path", "projected_path", "plan_path",
	)
	dynamicKindOwnerships = map[string]map[string]struct{}{
		"provider_state_only":                  guidanceStringSet("provider_computed", "server_owned", "unknown"),
		"provider_computed_map":                guidanceStringSet("provider_computed", "server_owned", "unknown"),
		"freeform_object":                      guidanceStringSet("user_owned", "provider_computed", "server_owned", "unknown"),
		"opaque_json_blob":                     guidanceStringSet("provider_computed", "server_owned", "unknown"),
		"map_key_discovered_after_import":      guidanceStringSet("provider_computed", "server_owned", "unknown"),
		"unstable_collection_identity":         guidanceStringSet("provider_computed", "server_owned", "unknown"),
		"schema_unknown_but_provider_observed": guidanceStringSet("user_owned", "provider_computed", "server_owned", "unknown"),
		"raw_api_only_provider_blind":          guidanceStringSet("unknown"),
		"provider_observed_projection_unsafe":  guidanceStringSet("provider_computed", "server_owned", "unknown"),
	}
)

func guidanceStringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func hasString(set map[string]struct{}, value string) bool {
	_, ok := set[value]
	return ok
}

// NewAssessmentGuidanceSource snapshots the original active pack guidance
// without creating a second guidance catalog.
func NewAssessmentGuidanceSource(root metadata.LoadedPackRoot) AssessmentGuidanceSource {
	providersByResource := make(map[string]string, len(root.Resources))
	for resourceType, resource := range root.Resources {
		providersByResource[resourceType] = resource.Provider
	}
	providerPrefixes := make(map[string]string, len(root.Packs.ProviderPrefixes))
	for prefix, provider := range root.Packs.ProviderPrefixes {
		providerPrefixes[prefix] = provider
	}
	manifests := make([]rawManifestGuidance, len(root.Packs.Manifests))
	for index, manifest := range root.Packs.Manifests {
		providers := make(map[string]struct{}, len(manifest.ProviderPrefixes))
		for _, provider := range manifest.ProviderPrefixes {
			providers[provider] = struct{}{}
		}
		providerList := make([]string, 0, len(providers))
		for provider := range providers {
			providerList = append(providerList, provider)
		}
		providerList = canonjson.SortedStrings(providerList)
		manifests[index] = rawManifestGuidance{
			providers:      providerList,
			providerConfig: groupRules(manifest.Data, "provider_config", "requirements"),
			absentDefaults: groupRules(manifest.Data, "absent_defaults", "rules"),
			dynamicSchema:  groupRules(manifest.Data, "dynamic_schema", "rules"),
		}
	}
	return AssessmentGuidanceSource{
		providersByResource: providersByResource,
		providerPrefixes:    providerPrefixes,
		manifests:           manifests,
	}
}

func groupRules(data map[string]any, group, field string) []map[string]any {
	rawGroup, ok := data[group].(map[string]any)
	if !ok {
		return nil
	}
	rawRules, ok := rawGroup[field].([]any)
	if !ok {
		return nil
	}
	rules := make([]map[string]any, len(rawRules))
	for index, raw := range rawRules {
		record, ok := raw.(map[string]any)
		if !ok {
			return nil
		}
		rules[index] = cloneGuidanceRecord(record)
	}
	return rules
}

func cloneGuidanceRecord(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = cloneGuidanceValue(value)
	}
	return output
}

func cloneGuidanceValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneGuidanceRecord(typed)
	case []any:
		output := make([]any, len(typed))
		for index, child := range typed {
			output[index] = cloneGuidanceValue(child)
		}
		return output
	case json.Number:
		return json.Number(string(typed))
	default:
		return typed
	}
}

func inferredProvider(raw map[string]any, manifest rawManifestGuidance) (string, bool) {
	if provider, ok := raw["provider"].(string); ok && len(provider) > 0 {
		return provider, true
	}
	if len(manifest.providers) == 1 {
		return manifest.providers[0], true
	}
	return "", false
}

func guidanceText(value any, field string) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a non-empty string", field)
	}
	trimmed := strings.TrimFunc(text, isJavaScriptTrimRune)
	if len(trimmed) == 0 {
		return "", fmt.Errorf("%s must be a non-empty string", field)
	}
	return trimmed, nil
}

func isJavaScriptTrimRune(value rune) bool {
	switch {
	case value >= '\u0009' && value <= '\u000d':
		return true
	case value == '\u0020', value == '\u00a0', value == '\u1680', value == '\u2028',
		value == '\u2029', value == '\u202f', value == '\u205f', value == '\u3000', value == '\ufeff':
		return true
	case value >= '\u2000' && value <= '\u200a':
		return true
	default:
		return false
	}
}

func optionalStrings(value any, present bool, field string) ([]string, error) {
	if !present {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a list of non-empty strings", field)
	}
	unique := make(map[string]struct{}, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a list of non-empty strings", field)
		}
		trimmed := strings.TrimFunc(text, isJavaScriptTrimRune)
		if len(trimmed) == 0 {
			return nil, fmt.Errorf("%s must be a list of non-empty strings", field)
		}
		unique[trimmed] = struct{}{}
	}
	output := make([]string, 0, len(unique))
	for item := range unique {
		output = append(output, item)
	}
	return canonjson.SortedStrings(output), nil
}

func reportGuidanceValue(value any, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("guidance value is too deeply nested")
	}
	switch typed := value.(type) {
	case nil, string, bool:
		return typed, nil
	case json.Number:
		if _, err := canonjson.CanonicalNumberToken(string(typed)); err != nil {
			return nil, errors.New("guidance number cannot be represented exactly")
		}
		return json.Number(string(typed)), nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || (typed == 0 && math.Signbit(typed)) {
			return nil, errors.New("guidance value is not JSON-compatible")
		}
		if math.Trunc(typed) == typed && math.Abs(typed) > 9007199254740991 {
			return nil, errors.New("guidance value is not JSON-compatible")
		}
		return typed, nil
	case []any:
		output := make([]any, len(typed))
		for index, child := range typed {
			copied, err := reportGuidanceValue(child, depth+1)
			if err != nil {
				return nil, err
			}
			output[index] = copied
		}
		return output, nil
	case map[string]any:
		output := make(map[string]any, len(typed))
		for key, child := range typed {
			copied, err := reportGuidanceValue(child, depth+1)
			if err != nil {
				return nil, err
			}
			output[key] = copied
		}
		return output, nil
	default:
		return nil, errors.New("guidance value is not JSON-compatible")
	}
}

func splitReportPath(value, field string) ([]string, error) {
	parts := make([]string, 0, strings.Count(value, ".")+1)
	var buffer strings.Builder
	quoted := false
	escaped := false
	for _, character := range value {
		switch {
		case escaped:
			buffer.WriteRune(character)
			escaped = false
		case character == '\\' && quoted:
			buffer.WriteRune(character)
			escaped = true
		case character == '"':
			buffer.WriteRune(character)
			quoted = !quoted
		case character == '.' && !quoted:
			parts = append(parts, buffer.String())
			buffer.Reset()
		default:
			buffer.WriteRune(character)
		}
	}
	if quoted {
		return nil, fmt.Errorf("%s contains an unterminated quote", field)
	}
	parts = append(parts, buffer.String())
	return parts, nil
}

func schemaGuidancePath(value any, field string) (string, error) {
	raw, err := guidanceText(value, field)
	if err != nil {
		return "", err
	}
	if raw == "<root>" {
		return raw, nil
	}
	parts, err := splitReportPath(raw, field)
	if err != nil {
		return "", err
	}
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			return "", fmt.Errorf("%s contains an empty segment", field)
		}
		if !strings.ContainsAny(part, "[]") {
			if part == "*" {
				return "", fmt.Errorf("%s contains a bare wildcard segment", field)
			}
			segments = append(segments, part)
			continue
		}
		parsed, parseErr := metadata.ParsePolicyPath(part, field)
		if parseErr != nil {
			return "", parseErr
		}
		segments = append(segments, metadata.NormalizePolicyPath(parsed)...)
	}
	rendered := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "[]" {
			if len(rendered) == 0 {
				rendered = append(rendered, "[]")
			} else {
				rendered[len(rendered)-1] += "[]"
			}
			continue
		}
		rendered = append(rendered, segment)
	}
	if len(rendered) == 0 {
		return "<root>", nil
	}
	return strings.Join(rendered, "."), nil
}

func formatConcreteGuidancePath(path PlanPath) string {
	if len(path) == 0 {
		return "<root>"
	}
	parts := make([]string, 0, len(path))
	appendSelector := func(selector string) {
		if len(parts) == 0 {
			parts = append(parts, selector)
			return
		}
		parts[len(parts)-1] += selector
	}
	for _, segment := range path {
		switch typed := segment.(type) {
		case string:
			if typed == "[]" || typed == "*" {
				appendSelector("[]")
			} else {
				parts = append(parts, typed)
			}
		case int:
			appendSelector("[" + strconv.Itoa(typed) + "]")
		default:
			parts = append(parts, fmt.Sprint(typed))
		}
	}
	return strings.Join(parts, ".")
}

func formatSchemaGuidancePath(path PlanPath) string {
	schema := make(PlanPath, len(path))
	for index, segment := range path {
		switch typed := segment.(type) {
		case int:
			schema[index] = "[]"
		case string:
			if typed == "*" {
				schema[index] = "[]"
			} else {
				schema[index] = typed
			}
		default:
			schema[index] = typed
		}
	}
	return formatConcreteGuidancePath(schema)
}

func planGuidanceRecords(plan map[string]any, resourceType string) []candidatePath {
	var candidates []candidatePath
	for _, source := range []string{"resource_changes", "resource_drift"} {
		changes, ok := plan[source].([]any)
		if !ok {
			continue
		}
		for _, raw := range changes {
			record, ok := raw.(map[string]any)
			if !ok || record["type"] != resourceType {
				continue
			}
			address, ok := record["address"].(string)
			if !ok {
				continue
			}
			change, ok := record["change"].(map[string]any)
			if !ok || !stringArrayIncludes(change["actions"], "update") {
				continue
			}
			paths := make(map[string]PlanPath)
			for _, path := range append(DiffPaths(change["before"], change["after"]), TruthyPaths(change["after_unknown"])...) {
				paths[pathMarker(path)] = clonePath(path)
			}
			for _, path := range paths {
				candidates = append(candidates, candidatePath{
					source:       source,
					address:      address,
					resourceType: resourceType,
					before:       change["before"],
					path:         path,
					formatted:    formatSchemaGuidancePath(path),
				})
			}
		}
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		leftCandidate := candidates[left]
		rightCandidate := candidates[right]
		for _, pair := range [][2]string{
			{leftCandidate.source, rightCandidate.source},
			{leftCandidate.address, rightCandidate.address},
			{leftCandidate.formatted, rightCandidate.formatted},
			{guidancePathSortKey(leftCandidate.path), guidancePathSortKey(rightCandidate.path)},
		} {
			compared := canonjson.ComparePythonStrings(pair[0], pair[1])
			if compared != 0 {
				return compared < 0
			}
		}
		return false
	})
	return candidates
}

func stringArrayIncludes(value any, expected string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == expected {
			return true
		}
	}
	return false
}

func guidancePathSortKey(path PlanPath) string {
	parts := make([]string, len(path))
	for index, segment := range path {
		switch typed := segment.(type) {
		case string:
			parts[index] = typed
		case int:
			parts[index] = strconv.Itoa(typed)
		default:
			parts[index] = fmt.Sprint(typed)
		}
	}
	return strings.Join(parts, "\x00")
}

func providerConfigGuidance(
	source AssessmentGuidanceSource,
	plan map[string]any,
	resourceType string,
) ([]guidanceEntry, error) {
	provider, ok := source.providersByResource[resourceType]
	if !ok {
		return nil, errors.New("unknown guidance resource")
	}
	candidates := planGuidanceRecords(plan, resourceType)
	var output []guidanceEntry
	seenSettings := make(map[string]struct{})
	for _, manifest := range source.manifests {
		for _, raw := range manifest.providerConfig {
			requirementProvider, inferred := inferredProvider(raw, manifest)
			if !inferred || requirementProvider != provider {
				continue
			}
			if _, err := guidanceText(raw["id"], "provider_config.id"); err != nil {
				return nil, err
			}
			setting, err := guidanceText(raw["setting"], "provider_config.setting")
			if err != nil {
				return nil, err
			}
			reason, err := guidanceText(raw["reason"], "provider_config.reason")
			if err != nil {
				return nil, err
			}
			rawPaths, ok := raw["plan_paths"].([]any)
			if !ok || len(rawPaths) == 0 {
				return nil, errors.New("provider_config.plan_paths must be a non-empty list")
			}
			paths := make(map[string]struct{}, len(rawPaths))
			for _, rawPath := range rawPaths {
				path, pathErr := schemaGuidancePath(rawPath, "provider_config.plan_path")
				if pathErr != nil {
					return nil, pathErr
				}
				paths[path] = struct{}{}
			}
			resourceTypesValue, hasResourceTypes := raw["resource_types"]
			resourceTypes, err := optionalStrings(resourceTypesValue, hasResourceTypes, "provider_config.resource_types")
			if err != nil {
				return nil, err
			}
			resourcePrefixesValue, hasResourcePrefixes := raw["resource_prefixes"]
			resourcePrefixes, err := optionalStrings(resourcePrefixesValue, hasResourcePrefixes, "provider_config.resource_prefixes")
			if err != nil {
				return nil, err
			}
			if len(resourceTypes) > 0 && !sliceIncludes(resourceTypes, resourceType) {
				continue
			}
			if len(resourcePrefixes) > 0 && !hasPrefix(resourceType, resourcePrefixes) {
				continue
			}

			remediationValue, hasRemediation := raw["remediation"]
			mode := "diagnostic_only"
			var remediation map[string]any
			if hasRemediation {
				var objectOK bool
				remediation, objectOK = remediationValue.(map[string]any)
				if !objectOK {
					return nil, errors.New("provider_config.remediation must be an object")
				}
				mode, err = guidanceText(remediation["mode"], "provider_config.remediation.mode")
				if err != nil {
					return nil, err
				}
			}
			if !hasString(providerModes, mode) {
				return nil, errors.New("provider_config remediation mode is invalid")
			}
			if remediation != nil {
				for key := range remediation {
					if !hasString(providerRemediationKeys, key) {
						return nil, errors.New("provider_config remediation contains an unknown key")
					}
				}
				if remediation["kind"] != "provider_argument" {
					return nil, errors.New("provider_config remediation kind is invalid")
				}
			}
			value, hasValue := raw["value"]
			if !hasValue && mode != "required_external" {
				return nil, errors.New("provider_config value is required")
			}
			if mode == "renderable_default" {
				if !renderableDefault(value) {
					return nil, errors.New("provider_config renderable value is invalid")
				}
				if hasResourceTypes || hasResourcePrefixes {
					return nil, errors.New("provider_config renderable default must be global")
				}
				safety, safetyOK := remediation["safety"].(map[string]any)
				evidence, evidenceOK := remediation["evidence"].(string)
				if !safetyOK || safety["non_sensitive"] != true ||
					safety["not_tenant_specific"] != true || safety["not_destructive"] != true ||
					!evidenceOK || len(strings.TrimFunc(evidence, isJavaScriptTrimRune)) == 0 {
					return nil, errors.New("provider_config renderable safety evidence is invalid")
				}
			}
			settingKey := provider + "\x00" + setting
			if _, duplicate := seenSettings[settingKey]; duplicate {
				return nil, errors.New("provider_config setting is duplicated")
			}
			seenSettings[settingKey] = struct{}{}
			if mode != "required_external" && mode != "renderable_default" {
				continue
			}
			evidence := ""
			if remediation != nil {
				evidence, _ = remediation["evidence"].(string)
			}
			for _, candidate := range candidates {
				if _, matches := paths[candidate.formatted]; !matches {
					continue
				}
				expectedValue := any(nil)
				if hasValue {
					expectedValue, err = reportGuidanceValue(value, 0)
					if err != nil {
						return nil, err
					}
				}
				output = append(output, guidanceEntry{
					"lane":              "provider_config",
					"provider":          provider,
					"resource_type":     resourceType,
					"address":           candidate.address,
					"source":            candidate.source,
					"matched_plan_path": candidate.formatted,
					"status_effect":     guidanceStatusEffect,
					"setting":           setting,
					"expected_value":    expectedValue,
					"mode":              mode,
					"reason":            reason,
					"evidence":          evidence,
				})
			}
		}
	}
	return output, nil
}

func renderableDefault(value any) bool {
	switch typed := value.(type) {
	case bool:
		return true
	case json.Number:
		_, err := canonjson.CanonicalNumberToken(string(typed))
		return err == nil
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0)
	default:
		return false
	}
}

func sliceIncludes(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func hasPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func scopedGuidanceProvider(resourceType string, providerPrefixes map[string]string) (string, bool) {
	prefixes := make([]string, 0, len(providerPrefixes))
	for prefix := range providerPrefixes {
		prefixes = append(prefixes, prefix)
	}
	sort.SliceStable(prefixes, func(left, right int) bool {
		leftLength := len(utf16.Encode([]rune(prefixes[left])))
		rightLength := len(utf16.Encode([]rune(prefixes[right])))
		if leftLength != rightLength {
			return leftLength > rightLength
		}
		return canonjson.ComparePythonStrings(prefixes[left], prefixes[right]) < 0
	})
	for _, prefix := range prefixes {
		if strings.HasPrefix(resourceType, prefix) {
			return providerPrefixes[prefix], true
		}
	}
	return "", false
}

func requireKnownGuidanceKeys(raw map[string]any, accepted map[string]struct{}, lane string) error {
	unknown := make([]string, 0)
	for key := range raw {
		if !hasString(accepted, key) {
			unknown = append(unknown, key)
		}
	}
	unknown = canonjson.SortedStrings(unknown)
	if len(unknown) > 0 {
		return fmt.Errorf("%s contains unknown key %s", lane, unknown[0])
	}
	return nil
}

func validateGuidanceScope(
	raw map[string]any,
	provider string,
	source AssessmentGuidanceSource,
	lane string,
) (guidanceScope, error) {
	_, hasType := raw["resource_type"]
	_, hasPrefix := raw["resource_prefix"]
	if hasType == hasPrefix {
		return guidanceScope{}, fmt.Errorf("%s requires exactly one resource scope", lane)
	}
	if hasType {
		resourceType, err := guidanceText(raw["resource_type"], lane+".resource_type")
		if err != nil {
			return guidanceScope{}, err
		}
		owner, found := scopedGuidanceProvider(resourceType, source.providerPrefixes)
		if !found || owner != provider {
			return guidanceScope{}, fmt.Errorf("%s.resource_type is outside its provider scope", lane)
		}
		return guidanceScope{kind: "type", value: resourceType}, nil
	}
	resourcePrefix, err := guidanceText(raw["resource_prefix"], lane+".resource_prefix")
	if err != nil {
		return guidanceScope{}, err
	}
	if source.providerPrefixes[resourcePrefix] != provider {
		return guidanceScope{}, fmt.Errorf("%s.resource_prefix is outside its provider scope", lane)
	}
	return guidanceScope{kind: "prefix", value: resourcePrefix}, nil
}

func validateGuidanceLaneRule(
	raw map[string]any,
	provider string,
	source AssessmentGuidanceSource,
	lane string,
) (validatedLaneRule, error) {
	accepted := dynamicAcceptedKeys
	if lane == "absent_default" {
		accepted = absentAcceptedKeys
	}
	if err := requireKnownGuidanceKeys(raw, accepted, lane); err != nil {
		return validatedLaneRule{}, err
	}
	if _, err := guidanceText(raw["id"], lane+".id"); err != nil {
		return validatedLaneRule{}, err
	}
	if _, err := guidanceText(raw["provider"], lane+".provider"); err != nil {
		return validatedLaneRule{}, err
	}
	path, err := schemaGuidancePath(raw["path"], lane+".path")
	if err != nil {
		return validatedLaneRule{}, err
	}
	if _, err := guidanceText(raw["evidence"], lane+".evidence"); err != nil {
		return validatedLaneRule{}, err
	}
	if _, err := guidanceText(raw["reason"], lane+".reason"); err != nil {
		return validatedLaneRule{}, err
	}
	scope, err := validateGuidanceScope(raw, provider, source, lane)
	if err != nil {
		return validatedLaneRule{}, err
	}
	optionalFields := []string{"raw_api_path", "projected_path"}
	if lane == "absent_default" {
		optionalFields = []string{"raw_api_path", "provider_state_path"}
	}
	for _, field := range optionalFields {
		if value, present := raw[field]; present {
			if _, err := guidanceText(value, lane+"."+field); err != nil {
				return validatedLaneRule{}, err
			}
		}
	}
	if planPath, present := raw["plan_path"]; present {
		if lane == "absent_default" {
			if _, err := schemaGuidancePath(planPath, lane+".plan_path"); err != nil {
				return validatedLaneRule{}, err
			}
		} else if _, err := guidanceText(planPath, lane+".plan_path"); err != nil {
			return validatedLaneRule{}, err
		}
	}
	kind, err := guidanceText(raw["kind"], lane+".kind")
	if err != nil {
		return validatedLaneRule{}, err
	}
	action, err := guidanceText(raw["action"], lane+".action")
	if err != nil {
		return validatedLaneRule{}, err
	}
	normalizedRaw := cloneGuidanceRecord(raw)
	normalizedRaw["path"] = path
	if lane == "absent_default" {
		if planPath, present := raw["plan_path"]; present {
			normalizedPlanPath, pathErr := schemaGuidancePath(planPath, lane+".plan_path")
			if pathErr != nil {
				return validatedLaneRule{}, pathErr
			}
			normalizedRaw["plan_path"] = normalizedPlanPath
		}
		if !hasString(absentKinds, kind) || !hasString(absentActions, action) {
			return validatedLaneRule{}, errors.New("absent_default rule vocabulary is invalid")
		}
		if !hasString(absentKindActions[kind], action) {
			return validatedLaneRule{}, errors.New("absent_default kind/action combination is invalid")
		}
		_, hasObserved := raw["observed_value"]
		if (hasString(absentObservedKinds, kind) || action == "preserve_explicit_falsey") && !hasObserved {
			return validatedLaneRule{}, errors.New("absent_default observed value is required")
		}
		return validatedLaneRule{
			raw:      normalizedRaw,
			provider: provider,
			path:     path,
			scope:    scope,
		}, nil
	}

	ownership, err := guidanceText(raw["ownership"], "dynamic_schema.ownership")
	if err != nil {
		return validatedLaneRule{}, err
	}
	version, err := guidanceText(raw["provider_version_constraint"], "dynamic_schema.provider_version_constraint")
	if err != nil {
		return validatedLaneRule{}, err
	}
	if !hasString(dynamicKinds, kind) || !hasString(dynamicOwnerships, ownership) || !hasString(dynamicActions, action) {
		return validatedLaneRule{}, errors.New("dynamic_schema rule vocabulary is invalid")
	}
	if !hasString(dynamicKindOwnerships[kind], ownership) {
		return validatedLaneRule{}, errors.New("dynamic_schema kind/ownership combination is invalid")
	}
	id, _ := guidanceText(raw["id"], lane+".id")
	normalizedRaw["id"] = id
	normalizedRaw["provider"] = provider
	normalizedRaw["provider_version_constraint"] = version
	normalizedRaw["kind"] = kind
	normalizedRaw["ownership"] = ownership
	normalizedRaw["action"] = action
	versionCopy := version
	return validatedLaneRule{
		raw:      normalizedRaw,
		provider: provider,
		path:     path,
		scope:    scope,
		version:  &versionCopy,
	}, nil
}

func providerGuidanceLaneRules(
	source AssessmentGuidanceSource,
	provider, lane string,
) ([]validatedLaneRule, error) {
	var selected []map[string]any
	for _, manifest := range source.manifests {
		rules := manifest.dynamicSchema
		if lane == "absent_default" {
			rules = manifest.absentDefaults
		}
		for _, raw := range rules {
			candidateProvider, inferred := inferredProvider(raw, manifest)
			if !inferred || candidateProvider != provider {
				continue
			}
			candidate := cloneGuidanceRecord(raw)
			candidate["provider"] = candidateProvider
			selected = append(selected, candidate)
		}
	}
	validated := make([]validatedLaneRule, len(selected))
	for index, raw := range selected {
		rule, err := validateGuidanceLaneRule(raw, provider, source, lane)
		if err != nil {
			return nil, err
		}
		validated[index] = rule
	}
	type identity struct {
		provider   string
		version    string
		hasVersion bool
		scopeKind  string
		scopeValue string
		path       string
	}
	identities := make(map[identity]struct{}, len(validated))
	for _, rule := range validated {
		version := ""
		if rule.version != nil {
			version = *rule.version
		}
		key := identity{
			provider: rule.provider, version: version, hasVersion: rule.version != nil,
			scopeKind: rule.scope.kind, scopeValue: rule.scope.value, path: rule.path,
		}
		if _, duplicate := identities[key]; duplicate {
			return nil, fmt.Errorf("%s rule is duplicated", lane)
		}
		identities[key] = struct{}{}
	}
	for _, typeRule := range validated {
		if typeRule.scope.kind != "type" {
			continue
		}
		for _, prefixRule := range validated {
			if prefixRule.scope.kind != "prefix" || prefixRule.provider != typeRule.provider ||
				!sameOptionalString(prefixRule.version, typeRule.version) || prefixRule.path != typeRule.path {
				continue
			}
			if strings.HasPrefix(typeRule.scope.value, prefixRule.scope.value) {
				return nil, fmt.Errorf("%s resource scopes overlap", lane)
			}
		}
	}
	return validated, nil
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func valueAtGuidancePath(value any, candidate PlanPath) (any, bool) {
	current := value
	for _, segment := range candidate {
		switch typed := segment.(type) {
		case int:
			array, ok := current.([]any)
			if !ok || typed < 0 || typed >= len(array) {
				return nil, false
			}
			current = array[typed]
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				return nil, false
			}
			var present bool
			current, present = object[typed]
			if !present {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return current, true
}

func ruleGuidance(
	source AssessmentGuidanceSource,
	plan map[string]any,
	resourceType, lane string,
) ([]guidanceEntry, error) {
	provider, ok := source.providersByResource[resourceType]
	if !ok {
		return nil, errors.New("unknown guidance resource")
	}
	candidates := planGuidanceRecords(plan, resourceType)
	rules, err := providerGuidanceLaneRules(source, provider, lane)
	if err != nil {
		return nil, err
	}
	var output []guidanceEntry
	for _, validated := range rules {
		raw := validated.raw
		if raw["action"] != "manual_review_required" {
			continue
		}
		exactType, hasExactType := raw["resource_type"].(string)
		prefix, hasPrefixScope := raw["resource_prefix"].(string)
		if (hasExactType && exactType != resourceType) ||
			(!hasExactType && (!hasPrefixScope || !strings.HasPrefix(resourceType, prefix))) {
			continue
		}
		rule, err := guidanceText(raw["id"], lane+".id")
		if err != nil {
			return nil, err
		}
		pathValue := raw["path"]
		if planPath, present := raw["plan_path"]; present && planPath != nil {
			pathValue = planPath
		}
		matchedPath, err := schemaGuidancePath(pathValue, lane+".path")
		if err != nil {
			return nil, err
		}
		kind, err := guidanceText(raw["kind"], lane+".kind")
		if err != nil {
			return nil, err
		}
		reason, err := guidanceText(raw["reason"], lane+".reason")
		if err != nil {
			return nil, err
		}
		evidence, err := guidanceText(raw["evidence"], lane+".evidence")
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			if candidate.formatted != matchedPath {
				continue
			}
			observedValue, hasObservedValue := raw["observed_value"]
			if lane == "absent_default" && hasObservedValue {
				observed, present := valueAtGuidancePath(candidate.before, candidate.path)
				if !present || !canonjson.TerraformJSONEqual(observed, observedValue) {
					continue
				}
			}
			if lane == "absent_default" {
				reportedObserved := any(nil)
				if hasObservedValue {
					reportedObserved, err = reportGuidanceValue(observedValue, 0)
					if err != nil {
						return nil, err
					}
				}
				output = append(output, guidanceEntry{
					"lane":              lane,
					"provider":          provider,
					"resource_type":     resourceType,
					"address":           candidate.address,
					"source":            candidate.source,
					"matched_plan_path": matchedPath,
					"status_effect":     guidanceStatusEffect,
					"rule":              rule,
					"kind":              kind,
					"action":            "manual_review_required",
					"observed_value":    reportedObserved,
					"reason":            reason,
					"evidence":          evidence,
				})
				continue
			}
			ownership, ownershipErr := guidanceText(raw["ownership"], "dynamic_schema.ownership")
			if ownershipErr != nil {
				return nil, ownershipErr
			}
			var version any
			if rawVersion, ok := raw["provider_version_constraint"].(string); ok {
				version = rawVersion
			}
			output = append(output, guidanceEntry{
				"lane":                        lane,
				"provider":                    provider,
				"resource_type":               resourceType,
				"address":                     candidate.address,
				"source":                      candidate.source,
				"matched_plan_path":           matchedPath,
				"status_effect":               guidanceStatusEffect,
				"rule":                        rule,
				"kind":                        kind,
				"ownership":                   ownership,
				"action":                      "manual_review_required",
				"provider_version_constraint": version,
				"reason":                      reason,
				"evidence":                    evidence,
			})
		}
	}
	return output, nil
}

func safeCollectGuidance(operation func() ([]guidanceEntry, error)) (output []guidanceEntry) {
	defer func() {
		if recover() != nil {
			output = nil
		}
	}()
	output, err := operation()
	if err != nil {
		return nil
	}
	return output
}

func joinBlockedGuidance(findings []PlanFinding, annotations []guidanceEntry) []guidanceEntry {
	var output []guidanceEntry
	for _, finding := range findings {
		if finding.Status != Blocked {
			continue
		}
		for _, findingPath := range finding.Paths {
			matched := formatSchemaGuidancePath(findingPath)
			for _, annotation := range annotations {
				if annotation["source"] != finding.Source || annotation["address"] != finding.Address ||
					annotation["matched_plan_path"] != matched {
					continue
				}
				entry := make(guidanceEntry, len(annotation)+1)
				for key, value := range annotation {
					entry[key] = value
				}
				entry["finding_path"] = formatConcreteGuidancePath(findingPath)
				output = append(output, entry)
			}
		}
	}
	sort.SliceStable(output, func(left, right int) bool {
		leftKey := guidanceSortKey(output[left])
		rightKey := guidanceSortKey(output[right])
		for index := 0; index < max(len(leftKey), len(rightKey)); index++ {
			leftPart := ""
			if index < len(leftKey) {
				leftPart = leftKey[index]
			}
			rightPart := ""
			if index < len(rightKey) {
				rightPart = rightKey[index]
			}
			compared := canonjson.ComparePythonStrings(leftPart, rightPart)
			if compared != 0 {
				return compared < 0
			}
		}
		return false
	})
	return output
}

func guidanceSortKey(entry guidanceEntry) []string {
	lane, _ := entry["lane"].(string)
	laneOrder := "99"
	switch lane {
	case "provider_config":
		laneOrder = "00"
	case "absent_default":
		laneOrder = "01"
	case "dynamic_schema":
		laneOrder = "02"
	}
	if lane == "provider_config" {
		return []string{
			laneOrder,
			guidanceString(entry["provider"]),
			guidanceString(entry["setting"]),
			guidanceString(entry["matched_plan_path"]),
		}
	}
	return []string{
		laneOrder,
		guidanceString(entry["provider"]),
		guidanceString(entry["resource_type"]),
		guidanceString(entry["matched_plan_path"]),
		guidanceString(entry["rule"]),
	}
}

func guidanceString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

// CollectAssessmentGuidance collects source-backed explanations for blocked
// findings. Malformed lanes fail closed to no annotations and never alter the
// supplied classification.
func CollectAssessmentGuidance(options CollectAssessmentGuidanceOptions) AssessmentGuidanceGroup {
	var annotations []guidanceEntry
	for _, resourceType := range options.Members {
		annotations = append(annotations, safeCollectGuidance(func() ([]guidanceEntry, error) {
			return providerConfigGuidance(options.Source, options.Plan, resourceType)
		})...)
		annotations = append(annotations, safeCollectGuidance(func() ([]guidanceEntry, error) {
			return ruleGuidance(options.Source, options.Plan, resourceType, "absent_default")
		})...)
		annotations = append(annotations, safeCollectGuidance(func() ([]guidanceEntry, error) {
			return ruleGuidance(options.Source, options.Plan, resourceType, "dynamic_schema")
		})...)
	}
	joined := joinBlockedGuidance(options.Findings, annotations)
	entries := make([]map[string]any, len(joined))
	for index, entry := range joined {
		entries[index] = map[string]any(entry)
	}
	return AssessmentGuidanceGroup{
		Tenant:  options.Tenant,
		Label:   options.Label,
		Entries: entries,
	}
}
