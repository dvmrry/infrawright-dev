// Package modulesgen ports node-src/modules/generator.ts: the Terraform
// module generator/validator behind the gen-modules / validate-modules /
// check-modules commands -- per-resource module trees (main.tf,
// variables.tf, outputs.tf, versions.tf, README.md,
// tests/defaults.tftest.hcl, tests/sample.auto.tfvars.json; see
// EXPECTED_MODULE_FILES, which is exactly the seven-entry
// EXPECTED_MODULE_FILES tuple the TS source freezes),
// ActiveGeneratedResourceTypes, GenerateModule, GenerateActiveModules, and
// ValidateGeneratedModuleTree.
//
// This package depends only on go/internal/metadata (for provider-schema
// type encodings and the loaded pack root), go/internal/canonjson (for
// sorted-key ordering and the lossless-artifact JSON renderer), and the
// standard library -- never on go/internal/roots or go/internal/envgen,
// matching node-src/modules/generator.ts's own dependency set
// (metadata/packs.js, metadata/loader.js, metadata/validation.js,
// metadata/terraform-schema.js, json/python-lossless-artifact.js,
// json/python-compatible.js).
//
// Every exported symbol's doc comment names the
// node-src/modules/generator.ts export it ports; that TypeScript remains
// the differential oracle until this port is independently qualified, per
// docs/go-runtime-plan.md.
package modulesgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// ModuleFileName is the Go analogue of the ModuleFileName string-literal
// union derived from EXPECTED_MODULE_FILES in node-src/modules/generator.ts.
type ModuleFileName string

// The seven ModuleFileName literals from EXPECTED_MODULE_FILES in
// node-src/modules/generator.ts.
const (
	FileMain         ModuleFileName = "main.tf"
	FileVariables    ModuleFileName = "variables.tf"
	FileOutputs      ModuleFileName = "outputs.tf"
	FileVersions     ModuleFileName = "versions.tf"
	FileReadme       ModuleFileName = "README.md"
	FileDefaultsTest ModuleFileName = "tests/defaults.tftest.hcl"
	FileSampleTfvars ModuleFileName = "tests/sample.auto.tfvars.json"
)

// ExpectedModuleFiles ports EXPECTED_MODULE_FILES from
// node-src/modules/generator.ts, in the same declared order.
var ExpectedModuleFiles = []ModuleFileName{
	FileMain,
	FileVariables,
	FileOutputs,
	FileVersions,
	FileReadme,
	FileDefaultsTest,
	FileSampleTfvars,
}

// ModuleFile is one rendered file within a RenderedModule.
type ModuleFile struct {
	Name    ModuleFileName
	Content string
}

// RenderedModule ports the RenderedModule interface from
// node-src/modules/generator.ts. Files preserves the exact insertion order
// the TS source's renderModuleFiles builds its Map in (main.tf,
// variables.tf, outputs.tf, versions.tf, README.md,
// tests/defaults.tftest.hcl, tests/sample.auto.tfvars.json) -- a plain Go
// map has no equivalent guarantee, so this is a slice rather than a
// map[ModuleFileName]string.
type RenderedModule struct {
	ResourceType string
	Files        []ModuleFile
}

// Get returns the content rendered for name, and whether it was present.
func (m RenderedModule) Get(name ModuleFileName) (string, bool) {
	for _, file := range m.Files {
		if file.Name == name {
			return file.Content, true
		}
	}
	return "", false
}

// Names returns this module's file names in insertion order, the Go
// analogue of the TS test suite's `[...segment.files.keys()]`.
func (m RenderedModule) Names() []ModuleFileName {
	names := make([]ModuleFileName, len(m.Files))
	for i, file := range m.Files {
		names[i] = file.Name
	}
	return names
}

// GeneratedModule ports the GeneratedModule interface from
// node-src/modules/generator.ts. Files lists the written destination paths
// in sortedStrings(rendered.files.keys()) order -- alphabetical, NOT
// RenderedModule.Files's insertion order -- exactly as GenerateModule's
// write loop iterates in the TS source.
type GeneratedModule struct {
	ResourceType string
	Files        []string
}

// moduleContext is the Go analogue of the RenderContext interface in
// node-src/modules/generator.ts. Named moduleContext rather than
// RenderContext, and built by buildModuleContext rather than a function
// literally named renderContext, purely because Go (unlike TypeScript)
// does not allow a function and a type to share one name in the same
// package -- see go/internal/roots's RootTopology/RootTopologyFromCatalog
// for the same naming workaround applied to the same underlying clash
// shape elsewhere in this port.
type moduleContext struct {
	ResourceType   string
	Provider       string
	ProviderSource string
	ProviderPin    string
	Schema         metadata.JsonObject
	// MainOverride is nil for the TS source's `mainOverride: string |
	// null` being null.
	MainOverride *string
	// SampleOverride is nil for the TS source's `sampleOverride:
	// JsonObject | null` being null.
	SampleOverride metadata.JsonObject
}

// jsonQuote ports the JSON.stringify(string) calls node-src/modules/
// generator.ts's error messages interpolate (e.g. `unknown active
// resource type ${JSON.stringify(resourceType)}`).
func jsonQuote(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		// Unreachable: json.Marshal only fails on unsupported types
		// (channels, functions, cyclic structures), none of which a
		// string can be.
		return `"` + s + `"`
	}
	return string(data)
}

// mapKeys returns m's keys in unspecified order, for any string-keyed map
// this package walks (attribute/block-type objects, type-encoding member
// maps). Callers that need a deterministic (and TS-parity) order pass the
// result through canonjson.SortedStrings themselves.
func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// sortedInputBlockTypes calls metadata.TerraformInputBlockTypes and
// returns its names in sorted order alongside the map itself.
// metadata.TerraformInputBlockTypes deliberately returns a plain
// (unordered) Go map since its own package has no caller that depends on
// iteration order (see that function's doc comment); this package is the
// one caller across the whole port that does -- every renderer below
// walks input block types in the same sorted-name order the TS source's
// terraformInputBlockTypes ReadonlyMap iterates in (ported from that
// function's own `sortedStrings(Object.keys(nested))` build loop) --
// so this wrapper restores that ordering at the boundary.
func sortedInputBlockTypes(block metadata.JsonObject, label string) ([]string, map[string]metadata.JsonObject, error) {
	inputBlocks, err := metadata.TerraformInputBlockTypes(block, label)
	if err != nil {
		return nil, nil, err
	}
	return canonjson.SortedStrings(mapKeys(inputBlocks)), inputBlocks, nil
}

// hclType ports hclType from node-src/modules/generator.ts. Unlike the TS
// source (which operates on `encoding: unknown` and therefore re-validates
// its shape defensively at every call), this operates on the already
// type-checked metadata.TerraformTypeEncoding union that
// metadata.TerraformAttributeType returns, so the TS source's "unsupported
// primitive type encoding"/generic "unsupported type encoding" branches
// (reachable there only for a malformed `encoding` value that
// terraformEncoding/terraformAttributeType would themselves already have
// rejected before hclType ever saw it) have no reachable Go analogue; the
// `default` cases below exist purely so this switch is total over the
// TerraformTypeEncoding interface, not because any committed schema or
// ported test can reach them.
func hclType(encoding metadata.TerraformTypeEncoding, indent int) (string, error) {
	switch enc := encoding.(type) {
	case metadata.TerraformPrimitiveType:
		switch string(enc) {
		case "string", "bool", "number":
			return string(enc), nil
		default:
			return "", fmt.Errorf("unsupported primitive type encoding: %s", jsonQuote(string(enc)))
		}
	case metadata.TerraformObjectType:
		pad := strings.Repeat(" ", indent+2)
		names := canonjson.SortedStrings(mapKeys(enc.Members))
		lines := make([]string, len(names))
		for i, name := range names {
			rendered, err := hclType(enc.Members[name], indent+2)
			if err != nil {
				return "", err
			}
			lines[i] = fmt.Sprintf("%s%s = optional(%s)", pad, name, rendered)
		}
		return fmt.Sprintf("object({\n%s\n%s})", strings.Join(lines, "\n"), strings.Repeat(" ", indent)), nil
	case metadata.TerraformCollectionType:
		rendered, err := hclType(enc.Inner, indent)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s(%s)", enc.Kind, rendered), nil
	default:
		return "", fmt.Errorf("unsupported type encoding")
	}
}

// blockObjectType ports blockObjectType from
// node-src/modules/generator.ts.
func blockObjectType(block metadata.JsonObject, indent int, label string) (string, error) {
	classified, err := metadata.TerraformClassifyAttributes(block, label)
	if err != nil {
		return "", err
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, label)
	if err != nil {
		return "", err
	}
	pad := strings.Repeat(" ", indent+2)
	var lines []string
	for _, name := range append(append([]string{}, classified.Required...), classified.Optional...) {
		attrLabel := fmt.Sprintf("%s.attributes.%s", label, name)
		attribute, err := metadata.TerraformRequireObject(attributes[name], attrLabel)
		if err != nil {
			return "", err
		}
		encoding, err := metadata.TerraformAttributeType(attribute, attrLabel)
		if err != nil {
			return "", err
		}
		rendered, err := hclType(encoding, indent+2)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%s%s = optional(%s)", pad, name, rendered))
	}
	names, inputBlocks, err := sortedInputBlockTypes(block, label)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		rendered, err := blockInputType(inputBlocks[name], indent+2, fmt.Sprintf("%s.block_types.%s", label, name))
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%s%s = optional(%s)", pad, name, rendered))
	}
	return fmt.Sprintf("object({\n%s\n%s})", strings.Join(lines, "\n"), strings.Repeat(" ", indent)), nil
}

// blockInputType ports blockInputType from node-src/modules/generator.ts.
func blockInputType(blockType metadata.JsonObject, indent int, label string) (string, error) {
	block, err := metadata.TerraformRequireObject(blockType["block"], label+".block")
	if err != nil {
		return "", err
	}
	inner, err := blockObjectType(block, indent, label+".block")
	if err != nil {
		return "", err
	}
	if metadata.TerraformBlockIsSingle(blockType) {
		return inner, nil
	}
	modeValue := blockType["nesting_mode"]
	if mode, ok := modeValue.(string); ok && (mode == "list" || mode == "set") {
		return fmt.Sprintf("%s(%s)", mode, inner), nil
	}
	return "", fmt.Errorf("unsupported nesting_mode %s", jsShow(modeValue))
}

// jsShow renders value the way a TS template literal's
// `${JSON.stringify(value)}` would for the "unsupported nesting_mode"
// error: a quoted string for a string value, or the bare literal
// "undefined" when the key was absent (Go's nil map read), matching
// JSON.stringify(undefined) === undefined stringifying to "undefined" when
// interpolated. Any other JSON-representable value (not expected at this
// call site, since nesting_mode is always either absent or a string in
// every committed schema) falls back to json.Marshal.
func jsShow(value any) string {
	if s, ok := value.(string); ok {
		return jsonQuote(s)
	}
	if value == nil {
		return "undefined"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

// renderBlockBody ports renderBlockBody from
// node-src/modules/generator.ts.
func renderBlockBody(block metadata.JsonObject, reference string, indent int, label string, topLevel bool) ([]string, error) {
	pad := strings.Repeat(" ", indent)
	var classified metadata.TerraformClassifiedAttributes
	var err error
	if topLevel {
		classified, err = metadata.TerraformResourceInputAttributes(block, label)
	} else {
		classified, err = metadata.TerraformClassifyAttributes(block, label)
	}
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, name := range append(append([]string{}, classified.Required...), classified.Optional...) {
		lines = append(lines, fmt.Sprintf("%s%s = %s.%s", pad, name, reference, name))
	}
	names, inputBlocks, err := sortedInputBlockTypes(block, label)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		blockType := inputBlocks[name]
		source := fmt.Sprintf("%s.%s", reference, name)
		var iterable string
		if metadata.TerraformBlockIsSingle(blockType) {
			iterable = fmt.Sprintf("%s == null ? [] : [%s]", source, source)
		} else {
			iterable = fmt.Sprintf("%s == null ? [] : %s", source, source)
		}
		child, err := metadata.TerraformRequireObject(blockType["block"], fmt.Sprintf("%s.block_types.%s.block", label, name))
		if err != nil {
			return nil, err
		}
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("%sdynamic \"%s\" {", pad, name))
		lines = append(lines, fmt.Sprintf("%s  for_each = %s", pad, iterable))
		lines = append(lines, fmt.Sprintf("%s  content {", pad))
		nested, err := renderBlockBody(child, name+".value", indent+4, fmt.Sprintf("%s.block_types.%s.block", label, name), false)
		if err != nil {
			return nil, err
		}
		lines = append(lines, nested...)
		lines = append(lines, fmt.Sprintf("%s  }", pad))
		lines = append(lines, fmt.Sprintf("%s}", pad))
	}
	return lines, nil
}

// encodingHasSensitive ports encodingHasSensitive from
// node-src/modules/generator.ts. attribute is nil for the TS source's
// `attribute?: JsonObject` being omitted (every recursive call omits it;
// only blockHasSensitive's top-level call supplies one).
func encodingHasSensitive(encoding metadata.TerraformTypeEncoding, attribute metadata.JsonObject) bool {
	if attribute != nil && metadata.TerraformBooleanField(attribute, "sensitive") {
		return true
	}
	switch enc := encoding.(type) {
	case metadata.TerraformCollectionType:
		return encodingHasSensitive(enc.Inner, nil)
	case metadata.TerraformObjectType:
		for _, member := range enc.Members {
			if encodingHasSensitive(member, nil) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// blockHasSensitive ports blockHasSensitive from
// node-src/modules/generator.ts. Iterates attributes/nested block types in
// sorted-name order; the TS source iterates Object.keys() insertion order
// instead (this loop only ever short-circuits on a boolean result, which
// does not depend on visit order, so the two orders are behaviorally
// equivalent for every ported test vector -- see this package's doc
// comment on sortedInputBlockTypes for the broader rationale for sorting
// unordered Go maps at this boundary).
func blockHasSensitive(block metadata.JsonObject, label string) (bool, error) {
	attributes, err := metadata.TerraformAttributesForBlock(block, label)
	if err != nil {
		return false, err
	}
	for _, name := range canonjson.SortedStrings(mapKeys(attributes)) {
		attrLabel := fmt.Sprintf("%s.attributes.%s", label, name)
		attribute, err := metadata.TerraformRequireObject(attributes[name], attrLabel)
		if err != nil {
			return false, err
		}
		encoding, err := metadata.TerraformAttributeType(attribute, attrLabel)
		if err != nil {
			return false, err
		}
		if encodingHasSensitive(encoding, attribute) {
			return true, nil
		}
	}
	nested, err := metadata.TerraformBlockTypesForBlock(block, label)
	if err != nil {
		return false, err
	}
	for _, name := range canonjson.SortedStrings(mapKeys(nested)) {
		blockType, err := metadata.TerraformRequireObject(nested[name], fmt.Sprintf("%s.block_types.%s", label, name))
		if err != nil {
			return false, err
		}
		child, err := metadata.TerraformRequireObject(blockType["block"], fmt.Sprintf("%s.block_types.%s.block", label, name))
		if err != nil {
			return false, err
		}
		has, err := blockHasSensitive(child, fmt.Sprintf("%s.block_types.%s.block", label, name))
		if err != nil {
			return false, err
		}
		if has {
			return true, nil
		}
	}
	return false, nil
}

// header ports header from node-src/modules/generator.ts.
func header(provider string) string {
	return fmt.Sprintf(
		"# GENERATED by iw modules generate from packs/%s/schemas/provider/%s.json — do not edit.\n# Regenerate: make gen-modules\n\n",
		provider, provider,
	)
}

// renderMain ports renderMain from node-src/modules/generator.ts.
func renderMain(context moduleContext) (string, error) {
	if context.MainOverride != nil {
		return *context.MainOverride, nil
	}
	block, err := metadata.TerraformBlockForSchema(context.Schema, context.ResourceType)
	if err != nil {
		return "", err
	}
	body, err := renderBlockBody(block, "each.value", 2, context.ResourceType+".block", true)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%sresource \"%s\" \"this\" {\n  for_each = var.items\n\n%s\n}\n",
		header(context.Provider), context.ResourceType, strings.Join(body, "\n"),
	), nil
}

// renderVariables ports renderVariables from
// node-src/modules/generator.ts.
func renderVariables(context moduleContext) (string, error) {
	block, err := metadata.TerraformBlockForSchema(context.Schema, context.ResourceType)
	if err != nil {
		return "", err
	}
	blockLabel := context.ResourceType + ".block"
	classified, err := metadata.TerraformResourceInputAttributes(block, blockLabel)
	if err != nil {
		return "", err
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, blockLabel)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, name := range classified.Required {
		attrLabel := fmt.Sprintf("%s.attributes.%s", blockLabel, name)
		attribute, err := metadata.TerraformRequireObject(attributes[name], attrLabel)
		if err != nil {
			return "", err
		}
		encoding, err := metadata.TerraformAttributeType(attribute, attrLabel)
		if err != nil {
			return "", err
		}
		rendered, err := hclType(encoding, 4)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("    %s = %s", name, rendered))
	}
	for _, name := range classified.Optional {
		attrLabel := fmt.Sprintf("%s.attributes.%s", blockLabel, name)
		attribute, err := metadata.TerraformRequireObject(attributes[name], attrLabel)
		if err != nil {
			return "", err
		}
		encoding, err := metadata.TerraformAttributeType(attribute, attrLabel)
		if err != nil {
			return "", err
		}
		rendered, err := hclType(encoding, 4)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("    %s = optional(%s)", name, rendered))
	}
	names, inputBlocks, err := sortedInputBlockTypes(block, blockLabel)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		rendered, err := blockInputType(inputBlocks[name], 4, fmt.Sprintf("%s.block_types.%s", blockLabel, name))
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("    %s = optional(%s)", name, rendered))
	}
	return fmt.Sprintf(
		"%svariable \"items\" {\n  description = \"%s instances, keyed by a stable identifier.\"\n  type = map(object({\n%s\n  }))\n}\n",
		header(context.Provider), context.ResourceType, strings.Join(lines, "\n"),
	), nil
}

// emitsNameToId ports emitsNameToId from node-src/modules/generator.ts.
func emitsNameToId(schema metadata.JsonObject, resourceType string) (bool, error) {
	block, err := metadata.TerraformBlockForSchema(schema, resourceType)
	if err != nil {
		return false, err
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, resourceType+".block")
	if err != nil {
		return false, err
	}
	nameValue, hasName := attributes["name"]
	if !hasName {
		return false, nil
	}
	nameObject, err := metadata.TerraformRequireObject(nameValue, resourceType+".block.attributes.name")
	if err != nil {
		return false, err
	}
	if !metadata.TerraformBooleanField(nameObject, "required") {
		return false, nil
	}
	_, hasID := attributes["id"]
	return hasID, nil
}

// renderOutputs ports renderOutputs from node-src/modules/generator.ts.
func renderOutputs(context moduleContext) (string, error) {
	block, err := metadata.TerraformBlockForSchema(context.Schema, context.ResourceType)
	if err != nil {
		return "", err
	}
	blockLabel := context.ResourceType + ".block"
	attributes, err := metadata.TerraformAttributesForBlock(block, blockLabel)
	if err != nil {
		return "", err
	}

	var deprecatedNames, keptNames []string
	for _, name := range mapKeys(attributes) {
		attribute, err := metadata.TerraformRequireObject(attributes[name], fmt.Sprintf("%s.attributes.%s", blockLabel, name))
		if err != nil {
			return "", err
		}
		if metadata.TerraformBooleanField(attribute, "deprecated") {
			deprecatedNames = append(deprecatedNames, name)
		} else {
			keptNames = append(keptNames, name)
		}
	}
	deprecated := canonjson.SortedStrings(deprecatedNames)
	kept := canonjson.SortedStrings(keptNames)

	hasSensitive, err := blockHasSensitive(block, blockLabel)
	if err != nil {
		return "", err
	}
	sensitiveLine := ""
	if hasSensitive {
		sensitiveLine = "  sensitive   = true\n"
	}

	var output string
	if len(deprecated) > 0 {
		nested, err := metadata.TerraformBlockTypesForBlock(block, blockLabel)
		if err != nil {
			return "", err
		}
		members := append(append([]string{}, kept...), canonjson.SortedStrings(mapKeys(nested))...)
		projections := make([]string, len(members))
		for i, member := range members {
			projections[i] = fmt.Sprintf("      %s = v.%s", member, member)
		}
		output = fmt.Sprintf(
			"%soutput \"items\" {\n  description = \"All managed %s resources (excludes deprecated: %s), keyed as in var.items.\"\n%s  value = {\n    for k, v in %s.this : k => {\n%s\n    }\n  }\n}\n",
			header(context.Provider), context.ResourceType, strings.Join(deprecated, ", "), sensitiveLine, context.ResourceType, strings.Join(projections, "\n"),
		)
	} else {
		output = fmt.Sprintf(
			"%soutput \"items\" {\n  description = \"All managed %s resources, keyed as in var.items.\"\n%s  value = %s.this\n}\n",
			header(context.Provider), context.ResourceType, sensitiveLine, context.ResourceType,
		)
	}
	emits, err := emitsNameToId(context.Schema, context.ResourceType)
	if err != nil {
		return "", err
	}
	if emits {
		output += fmt.Sprintf(
			"\noutput \"name_to_id\" {\n  description = \"Map of resource name to provider-assigned id.\"\n  value       = { for k, v in %s.this : v.name => v.id... }\n}\n",
			context.ResourceType,
		)
	}
	return output, nil
}

// renderVersions ports renderVersions from node-src/modules/generator.ts.
func renderVersions(context moduleContext) string {
	return fmt.Sprintf(
		"%sterraform {\n  required_providers {\n    %s = {\n      source = \"%s\"\n      version = \"%s\"\n    }\n  }\n}\n",
		header(context.Provider), context.Provider, context.ProviderSource, context.ProviderPin,
	)
}

// renderReadme ports renderReadme from node-src/modules/generator.ts.
func renderReadme(context moduleContext) string {
	return fmt.Sprintf(
		"# %s (generated module)\n\nManages `%s` via a typed `items` map. GENERATED — do not edit by\nhand (AGENTS.md rule 6). Regenerate with `iw modules generate` or `make gen-modules`.\n",
		context.ResourceType, context.ResourceType,
	)
}

// sampleValue ports sampleValue from node-src/modules/generator.ts. The
// `default: return []` fallback in the TS source (reachable there only for
// a malformed raw encoding value) has no reachable Go analogue for the
// same reason described on hclType's doc comment; it is kept here purely
// so the function is total.
func sampleValue(encoding metadata.TerraformTypeEncoding) any {
	switch enc := encoding.(type) {
	case metadata.TerraformPrimitiveType:
		switch string(enc) {
		case "string":
			return "example"
		case "bool":
			return true
		case "number":
			return float64(1)
		default:
			return "example"
		}
	case metadata.TerraformCollectionType:
		switch enc.Kind {
		case "list", "set":
			return []any{sampleValue(enc.Inner)}
		case "map":
			return metadata.JsonObject{"example": sampleValue(enc.Inner)}
		}
	case metadata.TerraformObjectType:
		output := metadata.JsonObject{}
		for _, name := range canonjson.SortedStrings(mapKeys(enc.Members)) {
			output[name] = sampleValue(enc.Members[name])
		}
		return output
	}
	return []any{}
}

// minItemsAtLeastOne reports whether value is a JSON number >= 1, ported
// from sampleItem's `typeof minimum === "number" && minimum >= 1` inline
// check. Accepts either a plain float64 or a json.Number token since
// min_items is read directly off the raw block-type JsonObject rather than
// through a normalizing accessor.
func minItemsAtLeastOne(value any) bool {
	switch v := value.(type) {
	case float64:
		return v >= 1
	case json.Number:
		f, err := v.Float64()
		return err == nil && f >= 1
	default:
		return false
	}
}

// sampleItem ports sampleItem from node-src/modules/generator.ts.
func sampleItem(block metadata.JsonObject, label string) (metadata.JsonObject, error) {
	item := metadata.JsonObject{}
	classified, err := metadata.TerraformClassifyAttributes(block, label)
	if err != nil {
		return nil, err
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, label)
	if err != nil {
		return nil, err
	}
	for _, name := range classified.Required {
		attrLabel := fmt.Sprintf("%s.attributes.%s", label, name)
		attribute, err := metadata.TerraformRequireObject(attributes[name], attrLabel)
		if err != nil {
			return nil, err
		}
		encoding, err := metadata.TerraformAttributeType(attribute, attrLabel)
		if err != nil {
			return nil, err
		}
		item[name] = sampleValue(encoding)
	}
	names, inputBlocks, err := sortedInputBlockTypes(block, label)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		blockType := inputBlocks[name]
		if minItemsAtLeastOne(blockType["min_items"]) {
			child, err := metadata.TerraformRequireObject(blockType["block"], fmt.Sprintf("%s.block_types.%s.block", label, name))
			if err != nil {
				return nil, err
			}
			inner, err := sampleItem(child, fmt.Sprintf("%s.block_types.%s.block", label, name))
			if err != nil {
				return nil, err
			}
			if metadata.TerraformBlockIsSingle(blockType) {
				item[name] = inner
			} else {
				item[name] = []any{inner}
			}
		}
	}
	return item, nil
}

// renderSample ports renderSample from node-src/modules/generator.ts.
func renderSample(context moduleContext) (string, error) {
	block, err := metadata.TerraformBlockForSchema(context.Schema, context.ResourceType)
	if err != nil {
		return "", err
	}
	item, err := sampleItem(block, context.ResourceType+".block")
	if err != nil {
		return "", err
	}
	if context.SampleOverride != nil {
		for key, value := range context.SampleOverride {
			item[key] = value
		}
	}
	rendered, err := canonjson.RenderLosslessArtifactJSON(metadata.JsonObject{
		"items": metadata.JsonObject{"example": item},
	})
	if err != nil {
		return "", err
	}
	return rendered, nil
}

// renderTest ports renderTest from node-src/modules/generator.ts.
func renderTest(context moduleContext) string {
	return fmt.Sprintf(
		"# GENERATED smoke test — plan against a mocked provider; no credentials.\nmock_provider \"%s\" {}\n\nrun \"defaults_plan\" {\n  command = plan\n\n  assert {\n    condition     = length(var.items) == 1\n    error_message = \"sample fixture must contain exactly one item\"\n  }\n}\n",
		context.Provider,
	)
}

// buildModuleContext ports renderContext from
// node-src/modules/generator.ts. See moduleContext's doc comment for the
// naming rationale.
func buildModuleContext(root metadata.LoadedPackRoot, resourceType string) (moduleContext, error) {
	resource, ok := root.Resources[resourceType]
	if !ok {
		return moduleContext{}, fmt.Errorf("unknown active resource type %s", jsonQuote(resourceType))
	}
	generate, _ := resource.Registry["generate"].(bool)
	if !generate {
		return moduleContext{}, fmt.Errorf("resource type %s is not generated", jsonQuote(resourceType))
	}
	manifest, err := metadata.ManifestForProvider(root.Packs, resource.Provider)
	if err != nil {
		return moduleContext{}, err
	}
	providerSource, hasProviderSource := root.Packs.ProviderSources[resource.Provider]
	ownerSource, hasOwnerSource := manifest.ProviderSources[resource.Provider]
	if !hasProviderSource || !hasOwnerSource {
		return moduleContext{}, fmt.Errorf("provider %s has no source in pack metadata", jsonQuote(resource.Provider))
	}
	if providerSource != ownerSource {
		return moduleContext{}, fmt.Errorf("provider %s has contradictory source metadata", jsonQuote(resource.Provider))
	}
	providerPin, _ := manifest.Data["pin"].(string)
	if providerPin == "" {
		return moduleContext{}, fmt.Errorf("provider %s has no version pin in pack metadata", jsonQuote(resource.Provider))
	}
	var sampleOverride metadata.JsonObject
	if rawSample, hasSample := resource.Override["sample"]; hasSample {
		sample, ok := rawSample.(metadata.JsonObject)
		if !ok {
			return moduleContext{}, fmt.Errorf("%s override sample must be an object", resourceType)
		}
		sampleOverride = sample
	}
	// root.LoadResourceSchema already returns a typed metadata.JsonObject
	// (or fails), so the TS source's own
	// `requireObject(await root.loadResourceSchema(resourceType), ...)`
	// wrapper -- there purely to narrow an `unknown` return type -- has no
	// reachable Go analogue: the type system already guarantees this.
	schema, err := root.LoadResourceSchema(resourceType)
	if err != nil {
		return moduleContext{}, err
	}
	mainOverride, err := root.LoadResourceMainOverride(resourceType)
	if err != nil {
		return moduleContext{}, err
	}
	return moduleContext{
		ResourceType:   resourceType,
		Provider:       resource.Provider,
		ProviderSource: providerSource,
		ProviderPin:    providerPin,
		Schema:         schema,
		MainOverride:   mainOverride,
		SampleOverride: sampleOverride,
	}, nil
}

// RenderModuleFiles ports renderModuleFiles from
// node-src/modules/generator.ts.
func RenderModuleFiles(root metadata.LoadedPackRoot, resourceType string) (RenderedModule, error) {
	context, err := buildModuleContext(root, resourceType)
	if err != nil {
		return RenderedModule{}, err
	}
	main, err := renderMain(context)
	if err != nil {
		return RenderedModule{}, err
	}
	variables, err := renderVariables(context)
	if err != nil {
		return RenderedModule{}, err
	}
	outputs, err := renderOutputs(context)
	if err != nil {
		return RenderedModule{}, err
	}
	sample, err := renderSample(context)
	if err != nil {
		return RenderedModule{}, err
	}
	return RenderedModule{
		ResourceType: resourceType,
		Files: []ModuleFile{
			{FileMain, main},
			{FileVariables, variables},
			{FileOutputs, outputs},
			{FileVersions, renderVersions(context)},
			{FileReadme, renderReadme(context)},
			{FileDefaultsTest, renderTest(context)},
			{FileSampleTfvars, sample},
		},
	}, nil
}

// ActiveGeneratedResourceTypes ports activeGeneratedResourceTypes from
// node-src/modules/generator.ts.
func ActiveGeneratedResourceTypes(root metadata.LoadedPackRoot) []string {
	var types []string
	for _, resource := range root.Resources {
		if generate, _ := resource.Registry["generate"].(bool); generate {
			types = append(types, resource.Type)
		}
	}
	return canonjson.SortedStrings(types)
}

// needsTerraformFormat ports needsTerraformFormat from
// node-src/modules/generator.ts.
func needsTerraformFormat(file ModuleFileName) bool {
	return strings.HasSuffix(string(file), ".tf") || strings.HasSuffix(string(file), ".tftest.hcl")
}

// GenerateModuleOptions mirrors the options bag generateModule accepts in
// node-src/modules/generator.ts.
type GenerateModuleOptions struct {
	OutputRoot string
	FormatHCL  HclFormatter
	// OnWrite is called with each destination path as it is written, in
	// write order. Nil is equivalent to the TS source's omitted
	// `options.onWrite`.
	OnWrite func(path string)
}

// GenerateModule ports generateModule from node-src/modules/generator.ts.
func GenerateModule(root metadata.LoadedPackRoot, resourceType string, options GenerateModuleOptions) (GeneratedModule, error) {
	rendered, err := RenderModuleFiles(root, resourceType)
	if err != nil {
		return GeneratedModule{}, err
	}
	base := filepath.Join(options.OutputRoot, resourceType)
	testsDirectory := filepath.Join(base, "tests")
	if err := os.MkdirAll(testsDirectory, 0o755); err != nil {
		return GeneratedModule{}, err
	}
	names := make([]string, len(rendered.Files))
	for i, file := range rendered.Files {
		names[i] = string(file.Name)
	}
	var written []string
	for _, relative := range canonjson.SortedStrings(names) {
		name := ModuleFileName(relative)
		source, ok := rendered.Get(name)
		if !ok {
			return GeneratedModule{}, fmt.Errorf("renderer omitted %s", relative)
		}
		output := source
		if needsTerraformFormat(name) {
			formatted, err := options.FormatHCL.FormatHCL(source)
			if err != nil {
				return GeneratedModule{}, err
			}
			output = formatted
		}
		destination := filepath.Join(base, relative)
		if err := os.WriteFile(destination, []byte(output), 0o644); err != nil {
			return GeneratedModule{}, err
		}
		written = append(written, destination)
		if options.OnWrite != nil {
			options.OnWrite(destination)
		}
	}
	return GeneratedModule{ResourceType: resourceType, Files: written}, nil
}

// GenerateActiveModules ports generateActiveModules from
// node-src/modules/generator.ts.
func GenerateActiveModules(root metadata.LoadedPackRoot, options GenerateModuleOptions) ([]GeneratedModule, error) {
	var generated []GeneratedModule
	for _, resourceType := range ActiveGeneratedResourceTypes(root) {
		module, err := GenerateModule(root, resourceType, options)
		if err != nil {
			return nil, err
		}
		generated = append(generated, module)
	}
	return generated, nil
}

// ValidateGeneratedModuleTree ports validateGeneratedModuleTree from
// node-src/modules/generator.ts.
func ValidateGeneratedModuleTree(moduleRoot string, resourceTypes []string) ([]string, error) {
	var missing []string
	for _, resourceType := range resourceTypes {
		for _, relative := range ExpectedModuleFiles {
			candidate := filepath.Join(moduleRoot, resourceType, string(relative))
			info, statErr := os.Stat(candidate)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					missing = append(missing, filepath.Join(resourceType, string(relative)))
					continue
				}
				return nil, statErr
			}
			if !info.Mode().IsRegular() {
				missing = append(missing, filepath.Join(resourceType, string(relative)))
			}
		}
	}
	if len(missing) > 0 {
		limit := len(missing)
		if limit > 20 {
			limit = 20
		}
		lines := make([]string, limit)
		for i, item := range missing[:limit] {
			lines[i] = "  - " + item
		}
		extra := ""
		if len(missing) > 20 {
			extra = fmt.Sprintf("\n  ... %d more", len(missing)-20)
		}
		return nil, fmt.Errorf(
			"generated module tree %s is missing %d expected file(s):\n%s%s",
			moduleRoot, len(missing), strings.Join(lines, "\n"), extra,
		)
	}
	return append([]string{}, resourceTypes...), nil
}
