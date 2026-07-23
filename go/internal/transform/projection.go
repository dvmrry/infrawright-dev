package transform

// projection.go ports the schema-projection compiler and the two
// item-shaping passes that run against it from
// the original implementation: RuntimeProjection/RuntimeProjectionBlock
// (types only in the Node source; TypeScript interfaces, not runtime code),
// compileProjection, filterItem, mergeSingleBlockElements, isNullObject,
// nullStubValue, childPath, coerceItem, and the exported
// projectLoadedRawField seam.

import (
	"encoding/json"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// runtimeProjectionBlock ports the RuntimeProjectionBlock interface from
// the original implementation.
type runtimeProjectionBlock struct {
	Cardinality string // "many" | "single"
	Merge       bool
	Projection  runtimeProjection
}

// runtimeProjection ports the RuntimeProjection interface from
// the original implementation.
type runtimeProjection struct {
	Attributes                map[string]metadata.TerraformTypeEncoding
	Blocks                    map[string]runtimeProjectionBlock
	KnownMembers              []string
	SilentlyIgnoredAttributes []string
}

// runtimeTransformResource ports the RuntimeTransformResource interface
// from the original implementation.
type runtimeTransformResource struct {
	Type               string
	Projection         runtimeProjection
	Override           map[string]any
	HTMLUnescapePasses int // 0 | 2
}

// requireBlockObject is a small (metadata error -> panic) adapter: every
// metadata.TerraformXxx helper this file calls returns (T, error) rather
// than throwing (see terraformschema.go's file doc comment), so call sites
// that need this package's own throw-via-panic convention (fail/failf,
// composing with recoverErr at this package's exported entry points) route
// a non-nil error through fail(err.Error()) -- this is that adapter
// specialized to terraformRequireObject, the single most common such call.
func requireBlockObject(value any, label string) metadata.JsonObject {
	obj, err := metadata.TerraformRequireObject(value, label)
	if err != nil {
		fail(err.Error())
	}
	return obj
}

// compileProjection ports compileProjection from
// the original implementation. mergeBlocks is only consulted at the
// top level (mirroring the Node source's options.topLevel &&
// options.mergeBlocks.has(name) gate); every recursive call passes an empty
// set for it, exactly like the Node source's own recursive call always
// passes `new Set()`.
func compileProjection(
	block metadata.JsonObject,
	label string,
	mergeBlocks map[string]struct{},
	topLevel bool,
) runtimeProjection {
	var classified metadata.TerraformClassifiedAttributes
	var err error
	if topLevel {
		classified, err = metadata.TerraformResourceInputAttributes(block, label)
	} else {
		classified, err = metadata.TerraformClassifyAttributes(block, label)
	}
	if err != nil {
		fail(err.Error())
	}
	rawAttributes, err := metadata.TerraformAttributesForBlock(block, label)
	if err != nil {
		fail(err.Error())
	}

	// [...classified.required, ...classified.optional]: required names
	// first (already alphabetical, from TerraformClassifyAttributes'
	// sortedKeys walk), then optional names (same), matching the Node
	// source's own concatenation order exactly -- this also fixes the
	// order in which a malformed attribute's error surfaces, should more
	// than one attribute be malformed.
	attributeNames := make([]string, 0, len(classified.Required)+len(classified.Optional))
	attributeNames = append(attributeNames, classified.Required...)
	attributeNames = append(attributeNames, classified.Optional...)
	attributes := make(map[string]metadata.TerraformTypeEncoding, len(attributeNames))
	for _, name := range attributeNames {
		attributeLabel := label + ".attributes." + name
		attrObject := requireBlockObject(rawAttributes[name], attributeLabel)
		encoding, err := metadata.TerraformAttributeType(attrObject, attributeLabel)
		if err != nil {
			fail(err.Error())
		}
		attributes[name] = encoding
	}

	rawBlockTypes, err := metadata.TerraformBlockTypesForBlock(block, label)
	if err != nil {
		fail(err.Error())
	}
	inputBlockTypes, err := metadata.TerraformInputBlockTypes(block, label)
	if err != nil {
		fail(err.Error())
	}
	// Sorted by name (metadata.TerraformInputBlockTypes itself returns a
	// plain, order-less Go map -- see that function's own doc comment):
	// this recursion can panic on a malformed child block, so per this
	// package's own determinism convention (see the package doc comment in
	// transform.go), which child's error surfaces first when more than one
	// is malformed must not depend on Go's randomized map iteration order.
	blocks := make(map[string]runtimeProjectionBlock, len(inputBlockTypes))
	for _, name := range canonjson.SortedStrings(mapKeys(inputBlockTypes)) {
		blockType := inputBlockTypes[name]
		childLabel := label + ".block_types." + name + ".block"
		cardinality := "many"
		if metadata.TerraformBlockIsSingle(blockType) {
			cardinality = "single"
		}
		_, merge := mergeBlocks[name]
		childBlock := requireBlockObject(blockType["block"], childLabel)
		blocks[name] = runtimeProjectionBlock{
			Cardinality: cardinality,
			Merge:       topLevel && merge,
			Projection:  compileProjection(childBlock, childLabel, map[string]struct{}{}, false),
		}
	}

	id, hasID := rawAttributes["id"]
	silentlyIgnored := []string{}
	if topLevel && hasID {
		idObject := requireBlockObject(id, label+".attributes.id")
		if metadata.TerraformBooleanField(idObject, "computed") {
			silentlyIgnored = []string{"id"}
		}
	}

	knownSet := make(map[string]struct{}, len(rawAttributes)+len(rawBlockTypes))
	for name := range rawAttributes {
		knownSet[name] = struct{}{}
	}
	for name := range rawBlockTypes {
		knownSet[name] = struct{}{}
	}
	knownMembers := canonjson.SortedStrings(mapKeys(knownSet))

	return runtimeProjection{
		Attributes:                attributes,
		Blocks:                    blocks,
		KnownMembers:              knownMembers,
		SilentlyIgnoredAttributes: silentlyIgnored,
	}
}

// childPath ports childPath from the original implementation.
func childPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// nullStubValue ports nullStubValue from the original implementation.
func nullStubValue(value any) bool {
	if _, ok := value.(bool); ok {
		return false
	}
	if value == nil {
		return true
	}
	if s, ok := value.(string); ok && (s == "" || s == "0") {
		return true
	}
	if arr, ok := value.([]any); ok && len(arr) == 0 {
		return true
	}
	if integer, ok := integerValue(value); ok {
		return integer.Sign() == 0
	}
	// The Node source's final fallback is `value instanceof LosslessNumber &&
	// Number(value.toString()) === 0`: a lossless number that
	// losslessIntegerToken (consulted inside integerValue above) already
	// rejected (so it is not an integral token, e.g. "0.0") but is still
	// numerically zero as a float. This is deliberately gated on
	// json.Number (this package's LosslessNumber analogue) specifically,
	// not float64 (the bare-JS-number analogue) -- exactly like the Node
	// source's `instanceof LosslessNumber` check does not also accept a
	// bare `typeof value === "number"`.
	if n, ok := value.(json.Number); ok {
		f, err := n.Float64()
		return err == nil && f == 0
	}
	return false
}

// isNullObject ports isNullObject from the original implementation.
func isNullObject(
	value any,
	projection runtimeProjection,
	path string,
	acknowledgedDrops map[string]struct{},
) bool {
	obj, ok := value.(map[string]any)
	if !ok || len(obj) == 0 {
		return false
	}
	_, hasID := obj["id"]
	if !hasID {
		allEndInID := true
		for key := range obj {
			if !strings.HasSuffix(key, "id") {
				allEndInID = false
				break
			}
		}
		if !allEndInID {
			return false
		}
	}
	known := make(map[string]struct{}, len(projection.KnownMembers))
	for _, member := range projection.KnownMembers {
		known[member] = struct{}{}
	}
	for key := range obj {
		currentPath := childPath(path, key)
		_, isKnown := known[key]
		identityKey := key == "id"
		_, acked := acknowledgedDrops[currentPath]
		if !isKnown && !identityKey && !acked {
			return false
		}
	}
	for _, memberValue := range obj {
		if !nullStubValue(memberValue) {
			return false
		}
	}
	return true
}

// mergeSingleBlockElements ports mergeSingleBlockElements from
// the original implementation. drops is a pointer to the same
// accumulating slice executeTransform threads through the whole filterItem
// traversal (the Go analogue of the Node source's mutable `drops: string[]`
// parameter); the final drops list is deduplicated and sorted once, by
// executeTransform, so the order elements are appended in here does not
// need to match the Node source's Set/array iteration order byte-for-byte
// -- only the *set* of paths recorded must match.
func mergeSingleBlockElements(
	elements []map[string]any,
	projection runtimeProjection,
	path string,
	drops *[]string,
	acknowledgedDrops map[string]struct{},
) map[string]any {
	entries := make(map[string]any)
	known := make(map[string]struct{}, len(projection.KnownMembers))
	for _, member := range projection.KnownMembers {
		known[member] = struct{}{}
	}
	for _, element := range elements {
		for _, key := range canonjson.SortedStrings(mapKeys(element)) {
			value := element[key]
			if value == nil {
				memberPath := childPath(path, key)
				_, isKnown := known[key]
				identityKey := key == "id"
				_, acked := acknowledgedDrops[memberPath]
				if !isKnown && !identityKey && !acked && !sliceContainsString(*drops, memberPath) {
					*drops = append(*drops, memberPath)
				}
				continue
			}
			encoding, hasEncoding := projection.Attributes[key]
			if coll, isCollection := encoding.(metadata.TerraformCollectionType); isCollection && (coll.Kind == "list" || coll.Kind == "set") {
				bucket, _ := entries[key].([]any)
				if s, isString := value.(string); !isString || s != "" {
					if arr, isArray := value.([]any); isArray {
						bucket = append(bucket, arr...)
					} else {
						bucket = append(bucket, value)
					}
				}
				entries[key] = bucket
				continue
			}
			if _, exists := entries[key]; !exists {
				entries[key] = value
			} else if hasEncoding && !canonjson.JSONEqual(entries[key], value) {
				*drops = append(*drops, path+"."+key+" (conflicting values across merged elements; kept first)")
			}
		}
	}
	return entries
}

// filterItem ports filterItem from the original implementation.
func filterItem(
	item map[string]any,
	projection runtimeProjection,
	path string,
	drops *[]string,
	acknowledgedDrops map[string]struct{},
	overrideDrops map[string]struct{},
	overrideDropDefaults map[string]any,
) map[string]any {
	output := make(map[string]any)
	ignored := make(map[string]struct{}, len(projection.SilentlyIgnoredAttributes))
	for _, attribute := range projection.SilentlyIgnoredAttributes {
		ignored[attribute] = struct{}{}
	}
	for _, key := range canonjson.SortedStrings(mapKeys(item)) {
		value := item[key]
		currentPath := childPath(path, key)
		if _, hasEncoding := projection.Attributes[key]; hasEncoding {
			dotted := strings.ReplaceAll(currentPath, "[]", "")
			if _, dropped := overrideDrops[dotted]; dropped {
				continue
			}
			if defaultValue, hasDefault := overrideDropDefaults[dotted]; hasDefault && MatchesTransformDefault(value, defaultValue) {
				continue
			}
			output[key] = value
			continue
		}
		block, hasBlock := projection.Blocks[key]
		if !hasBlock {
			if _, isIgnored := ignored[key]; !isIgnored {
				*drops = append(*drops, currentPath)
			}
			continue
		}
		if block.Cardinality == "single" {
			single := value
			if arr, isArray := single.([]any); isArray {
				if len(arr) == 0 {
					continue
				}
				var elements []map[string]any
				for _, entry := range arr {
					obj, isObject := entry.(map[string]any)
					if !isObject {
						continue
					}
					elements = append(elements, obj)
				}
				if len(elements) == 0 {
					*drops = append(*drops, currentPath)
					continue
				}
				if len(elements) == 1 {
					single = elements[0]
				} else {
					single = mergeSingleBlockElements(elements, block.Projection, currentPath, drops, acknowledgedDrops)
				}
			}
			if obj, isObject := single.(map[string]any); isObject {
				if !isNullObject(obj, block.Projection, currentPath, acknowledgedDrops) {
					output[key] = filterItem(obj, block.Projection, currentPath, drops, acknowledgedDrops, overrideDrops, overrideDropDefaults)
				}
			} else {
				*drops = append(*drops, currentPath)
			}
			continue
		}

		manyPath := currentPath + "[]"
		if arr, isArray := value.([]any); isArray {
			var elements []map[string]any
			for _, entry := range arr {
				obj, isObject := entry.(map[string]any)
				if !isObject {
					continue
				}
				if !isNullObject(obj, block.Projection, manyPath, acknowledgedDrops) {
					elements = append(elements, obj)
				}
			}
			var shaped []map[string]any
			if block.Merge && len(elements) > 1 {
				shaped = []map[string]any{mergeSingleBlockElements(elements, block.Projection, manyPath, drops, acknowledgedDrops)}
			} else {
				shaped = elements
			}
			filtered := make([]any, len(shaped))
			for i, entry := range shaped {
				filtered[i] = filterItem(entry, block.Projection, manyPath, drops, acknowledgedDrops, overrideDrops, overrideDropDefaults)
			}
			output[key] = filtered
		} else if obj, isObject := value.(map[string]any); isObject {
			if isNullObject(obj, block.Projection, manyPath, acknowledgedDrops) {
				output[key] = []any{}
			} else {
				output[key] = []any{filterItem(obj, block.Projection, manyPath, drops, acknowledgedDrops, overrideDrops, overrideDropDefaults)}
			}
		} else {
			*drops = append(*drops, currentPath)
		}
	}
	return output
}

// coerceItem ports coerceItem from the original implementation.
func coerceItem(item map[string]any, projection runtimeProjection) map[string]any {
	output := make(map[string]any, len(item))
	for _, key := range canonjson.SortedStrings(mapKeys(item)) {
		value := item[key]
		if block, hasBlock := projection.Blocks[key]; hasBlock {
			if block.Cardinality == "single" {
				if obj, isObject := value.(map[string]any); isObject {
					output[key] = coerceItem(obj, block.Projection)
				} else {
					output[key] = value
				}
			} else if arr, isArray := value.([]any); isArray {
				coerced := make([]any, len(arr))
				for i, entry := range arr {
					if obj, isObject := entry.(map[string]any); isObject {
						coerced[i] = coerceItem(obj, block.Projection)
					} else {
						coerced[i] = entry
					}
				}
				output[key] = coerced
			} else {
				output[key] = value
			}
			continue
		}
		if encoding, hasEncoding := projection.Attributes[key]; hasEncoding {
			output[key] = coerceValue(value, encoding)
		} else {
			output[key] = value
		}
	}
	return output
}

// ProjectLoadedRawFieldOptions ports projectLoadedRawField's inline options
// parameter type from the original implementation.
type ProjectLoadedRawFieldOptions struct {
	RawValue     any
	ResourceType string
	Schema       metadata.JsonObject
	Target       string
}

// ProjectLoadedRawField ports the exported projectLoadedRawField from
// the original implementation: "Shape one raw API value through the
// ordinary loaded-resource schema kernel." A nil result with no error means
// the target field is absent after filtering (the Go analogue of the Node
// source's `unknown | undefined` return: this package already conflates
// "undefined" and JSON null into a single nil representation everywhere
// else, see e.g. stringArraySlice/objectMap/stringValueMap).
func ProjectLoadedRawField(options ProjectLoadedRawFieldOptions) (result any, err error) {
	defer recoverErr(&err)
	block, blockErr := metadata.TerraformBlockForSchema(options.Schema, options.ResourceType)
	if blockErr != nil {
		fail(blockErr.Error())
	}
	projection := compileProjection(block, options.ResourceType+".block", map[string]struct{}{}, true)
	shaped := map[string]any{
		options.Target: snakeKeys(options.RawValue, "$raw."+options.Target),
	}
	drops := []string{}
	filtered := filterItem(shaped, projection, "", &drops, map[string]struct{}{}, map[string]struct{}{}, map[string]any{})
	coerced := coerceItem(filtered, projection)
	return coerced[options.Target], nil
}

// sliceContainsString is a small linear-scan membership test, the Go
// analogue of Array.prototype.includes used at mergeSingleBlockElements's
// one call site -- kept as a plain linear scan (not a set) for parity with
// the Node source's own drops.includes(memberPath) over its plain array.
func sliceContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
