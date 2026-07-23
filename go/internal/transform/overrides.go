package transform

// overrides.go ports the override vocabulary and the two authoring seams
// built on it from the original implementation: keyFields,
// identityComponent, deriveKey, goHtmlEscape, escapeHtmlFields,
// unescapeDisplayFields, matchesTransformDefault, applyReachableOverrides,
// applyTransformOverridesForAuthoring, coerceTransformPrimitiveForAuthoring,
// and transformValueMatchesDefaultForAuthoring.

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// keyFields ports keyFields from the original implementation.
func keyFields(resource *runtimeTransformResource) []string {
	field, present := resource.Override["key_field"]
	if !present || field == nil {
		return []string{"name"}
	}
	if s, ok := field.(string); ok {
		return []string{s}
	}
	return stringArraySlice(field, resource.Type+".override.key_field")
}

// identityComponent renders a resource identity component.
func identityComponent(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	if b, ok := value.(bool); ok {
		if b {
			return "True"
		}
		return "False"
	}
	if n, ok := value.(json.Number); ok {
		if token, ok := canonicalNumberToken(string(n)); ok {
			return token
		}
	}
	// Dead in ordinary practice (every numeric leaf this package's own
	// coercion/decoding pipeline produces is a json.Number, never a bare
	// float64 -- see coerce.go's file doc comment), but ported for direct
	// correspondence with the Node source's own
	// `typeof value === "number" && Number.isFinite(value)` branch, which is
	// equally dead for the same reason on the Node side (every numeric leaf
	// reaching identityComponent has already passed through
	// snakeKeys/cloneJson, which reject a bare `number`).
	if f, ok := value.(float64); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
		return formatJSNumber(f)
	}
	if value == nil {
		return "None"
	}
	return fmtString(value)
}

// deriveKey ports deriveKey from the original implementation.
func deriveKey(item map[string]any, resource *runtimeTransformResource) string {
	fields := keyFields(resource)
	parts := make([]string, len(fields))
	for i, field := range fields {
		value, ok := item[field]
		if !ok {
			failf("key field %s missing from item; set key_field in the override map", jsonQuote(field))
		}
		parts[i] = identityComponent(value)
	}
	key := SlugifyTransformKey(strings.Join(parts, " "))
	if key != "" {
		return key
	}
	if _, ok := item["id"]; !ok {
		fail("derived key is empty and item has no 'id' to fall back on")
	}
	fallback := SlugifyTransformKey(identityComponent(item["id"]))
	return "id_" + fallback
}

// goHtmlEscape ports goHtmlEscape from the original implementation:
// the fixed five-entity HTML escape Terraform's own `html_escape_fields`
// override applies after two htmlUnescape passes.
//
// strings.NewReplacer performs one left-to-right scan rather than five
// sequential full passes (the Node source's chained .replaceAll calls), but
// produces byte-identical output here: "&" is the only source character
// whose replacement text ("&amp;") itself contains a character targeted by
// another rule, and since "&" already appears earliest in both the Node
// source's chain and this replacer's single scan, that replacement text is
// never re-scanned by a later rule in either implementation.
var goHTMLEscapeReplacer = strings.NewReplacer(
	"&", "&amp;",
	"'", "&#39;",
	"\"", "&#34;",
	"<", "&lt;",
	">", "&gt;",
)

func goHtmlEscape(value string) string {
	return goHTMLEscapeReplacer.Replace(value)
}

// escapeHtmlFields ports escapeHtmlFields from
// the original implementation. item is mutated in place, exactly like
// the Node source's own item[field] = ... assignment.
func escapeHtmlFields(item map[string]any, resource *runtimeTransformResource, htmlUnescape func(string) string) {
	fields := stringArraySlice(resource.Override["html_escape_fields"], resource.Type+".override.html_escape_fields")
	for _, field := range fields {
		value, ok := item[field].(string)
		if !ok {
			continue
		}
		if htmlUnescape == nil {
			failf("%s HTML escaping requires a Python-compatible HTML decoder", resource.Type)
		}
		item[field] = goHtmlEscape(htmlUnescape(htmlUnescape(value)))
	}
}

// unescapeDisplayFields ports unescapeDisplayFields from
// the original implementation. item is mutated in place.
func unescapeDisplayFields(item map[string]any, resource *runtimeTransformResource, htmlUnescape func(string) string) {
	if resource.HTMLUnescapePasses == 0 {
		return
	}
	if htmlUnescape == nil {
		failf("%s requires Python-compatible HTML unescape metadata", resource.Type)
	}
	for _, field := range [...]string{"name", "description"} {
		if s, ok := item[field].(string); ok {
			item[field] = htmlUnescape(htmlUnescape(s))
		}
	}
}

// MatchesTransformDefault ports the exported matchesTransformDefault from
// the original implementation.
func MatchesTransformDefault(value, defaultValue any) bool {
	_, defaultIsInteger := integerValue(defaultValue)
	comparable := value
	if defaultIsInteger {
		if s, ok := value.(string); ok {
			parsed := parsePythonInteger(s)
			if parsed.Ok {
				comparable = parsed.AsNumber()
			}
		}
	}
	return canonjson.JSONEqual(comparable, defaultValue)
}

// applyReachableOverrides ports applyReachableOverrides from
// the original implementation: the ordinary override vocabulary
// (renames, split_csv, sort_lists, drops, references, divide, invert_bool,
// value_map, strip_prefix, defaults, drop_if_default), applied in that
// exact order against a shallow copy of item.
func applyReachableOverrides(item map[string]any, resource *runtimeTransformResource) map[string]any {
	output := make(map[string]any, len(item))
	for key, value := range item {
		output[key] = value
	}
	override := resource.Override

	renames := stringValueMap(override["renames"], resource.Type+".override.renames")
	for _, oldName := range canonjson.SortedStrings(mapKeys(renames)) {
		if value, ok := output[oldName]; ok {
			newName := renames[oldName]
			delete(output, oldName)
			output[newName] = value
		}
	}

	for _, field := range canonjson.SortedStrings(stringArraySlice(override["split_csv"], resource.Type+".override.split_csv")) {
		if value, ok := output[field].(string); ok {
			var parts []any
			for _, part := range strings.Split(value, ",") {
				trimmed := strings.TrimSpace(part)
				if trimmed != "" {
					parts = append(parts, trimmed)
				}
			}
			if parts == nil {
				parts = []any{}
			}
			output[field] = parts
		}
	}

	for _, field := range canonjson.SortedStrings(stringArraySlice(override["sort_lists"], resource.Type+".override.sort_lists")) {
		if value, ok := output[field].([]any); ok && everyElementIsString(value) {
			sorted := make([]string, len(value))
			for i, item := range value {
				sorted[i] = item.(string)
			}
			sortStrings(sorted)
			out := make([]any, len(sorted))
			for i, s := range sorted {
				out[i] = s
			}
			output[field] = out
		}
	}

	for _, field := range canonjson.SortedStrings(stringArraySlice(override["drops"], resource.Type+".override.drops")) {
		delete(output, field)
	}

	references := objectMap(override["references"], resource.Type+".override.references")
	for _, field := range canonjson.SortedStrings(mapKeys(references)) {
		value, ok := output[field]
		if !ok {
			continue
		}
		if arr, isArray := value.([]any); isArray {
			unwrapped := make([]any, len(arr))
			for i, item := range arr {
				unwrapped[i] = unwrapReference(item)
			}
			output[field] = unwrapped
		} else {
			output[field] = unwrapReference(value)
		}
	}

	divide := objectMap(override["divide"], resource.Type+".override.divide")
	for _, field := range canonjson.SortedStrings(mapKeys(divide)) {
		if _, ok := output[field]; ok {
			output[field] = dividedValue(output[field], divide[field], resource.Type+".override.divide."+field)
		}
	}

	for _, field := range canonjson.SortedStrings(stringArraySlice(override["invert_bool"], resource.Type+".override.invert_bool")) {
		if _, ok := output[field]; ok {
			if coerced, isBool := coerceBoolean(output[field]).(bool); isBool {
				output[field] = !coerced
			}
		}
	}

	valueMap := objectMap(override["value_map"], resource.Type+".override.value_map")
	for _, field := range canonjson.SortedStrings(mapKeys(valueMap)) {
		mapping := objectMap(valueMap[field], resource.Type+".override.value_map."+field)
		if value, ok := output[field].(string); ok {
			if mapped, hasMapping := mapping[value]; hasMapping {
				output[field] = cloneJson(mapped)
			}
		}
	}

	stripPrefix := stringValueMap(override["strip_prefix"], resource.Type+".override.strip_prefix")
	for _, field := range canonjson.SortedStrings(mapKeys(stripPrefix)) {
		prefix := stripPrefix[field]
		value := output[field]
		if s, ok := value.(string); ok {
			if strings.HasPrefix(s, prefix) {
				output[field] = s[len(prefix):]
			}
		} else if arr, ok := value.([]any); ok {
			trimmed := make([]any, len(arr))
			for i, item := range arr {
				if s, ok := item.(string); ok && strings.HasPrefix(s, prefix) {
					trimmed[i] = s[len(prefix):]
				} else {
					trimmed[i] = item
				}
			}
			output[field] = trimmed
		}
	}

	defaults := objectMap(override["defaults"], resource.Type+".override.defaults")
	for _, field := range canonjson.SortedStrings(mapKeys(defaults)) {
		value := output[field]
		empty := value == nil
		if !empty {
			if s, ok := value.(string); ok && s == "" {
				empty = true
			} else if arr, ok := value.([]any); ok && len(arr) == 0 {
				empty = true
			}
		}
		if empty {
			output[field] = cloneJson(defaults[field])
		}
	}

	dropDefaults := objectMap(override["drop_if_default"], resource.Type+".override.drop_if_default")
	for _, field := range canonjson.SortedStrings(mapKeys(dropDefaults)) {
		if value, ok := output[field]; ok && MatchesTransformDefault(value, dropDefaults[field]) {
			delete(output, field)
		}
	}

	return output
}

func everyElementIsString(values []any) bool {
	for _, value := range values {
		if _, ok := value.(string); !ok {
			return false
		}
	}
	return true
}

// ApplyTransformOverridesForAuthoring ports the exported
// applyTransformOverridesForAuthoring from
// the original implementation: "Apply the ordinary read-side override
// vocabulary without schema shaping."
func ApplyTransformOverridesForAuthoring(item map[string]any, override map[string]any, resourceType string) map[string]any {
	copied := make(map[string]any, len(item))
	for key, value := range item {
		copied[key] = value
	}
	resource := &runtimeTransformResource{
		Type:               resourceType,
		Override:           override,
		HTMLUnescapePasses: 0,
		Projection: runtimeProjection{
			Attributes:                map[string]metadata.TerraformTypeEncoding{},
			Blocks:                    map[string]runtimeProjectionBlock{},
			KnownMembers:              []string{},
			SilentlyIgnoredAttributes: []string{},
		},
	}
	return applyReachableOverrides(copied, resource)
}

// CoerceTransformPrimitiveForAuthoring ports the exported
// coerceTransformPrimitiveForAuthoring from
// the original implementation: "Authoring classification seam for the
// runtime's primitive coercion rules."
func CoerceTransformPrimitiveForAuthoring(value any, primitive metadata.TerraformPrimitiveType) any {
	return coerceValue(value, primitive)
}

// TransformValueMatchesDefaultForAuthoring ports the exported
// transformValueMatchesDefaultForAuthoring from
// the original implementation: "Authoring classification seam for
// runtime drop-if-default comparison."
func TransformValueMatchesDefaultForAuthoring(value, defaultValue any) bool {
	return MatchesTransformDefault(value, defaultValue)
}
