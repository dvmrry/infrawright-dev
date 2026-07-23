package transform

// kernel.go ports the top-level orchestration from
// node-src/domain/pull-transform.ts: PullTransformResult,
// validateLoadedOverride, executeTransform, TransformLoadedItemsOptions,
// TransformLoadedItems, compareDerivedRules, and DeriveReorderItems.

import (
	"math/big"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// PullTransformResult ports the exported PullTransformResult interface
// from node-src/domain/pull-transform.ts. Items/Originals are plain
// (order-less) Go maps: every real consumer of this shape in the Node
// source (transform-artifacts.ts) already walks Object.keys(...) through
// sortedStrings before doing anything with it, never relying on the
// Node Map/object's insertion order -- see this package's determinism
// convention in transform.go's file doc comment for the general policy
// this follows.
type PullTransformResult struct {
	Items     map[string]TransformRecord
	Originals map[string]TransformRecord
	Drops     []string
}

// validateLoadedOverride ports validateLoadedOverride from
// node-src/domain/pull-transform.ts.
func validateLoadedOverride(resourceType string, override map[string]any, block metadata.JsonObject) {
	divide := objectMap(override["divide"], resourceType+".override.divide")
	// Sorted (the Node source's own `for (const [field, divisor] of
	// Object.entries(divide))` is not): this loop can fail() when a divisor
	// is exactly zero, so which field's error surfaces first when more than
	// one divisor is zero must not depend on Go's randomized map iteration
	// order -- see this package's determinism convention in transform.go's
	// file doc comment.
	for _, field := range canonjson.SortedStrings(mapKeys(divide)) {
		if integer, ok := integerValue(divide[field]); ok && integer.Sign() == 0 {
			failf("divide divisor for %s must be non-zero", jsonQuote(field))
		}
	}

	renames := stringValueMap(override["renames"], resourceType+".override.renames")
	drops := stringArraySlice(override["drops"], resourceType+".override.drops")
	var topDrops []string
	for _, field := range drops {
		if !strings.Contains(field, ".") {
			topDrops = append(topDrops, field)
		}
	}
	var conflicts []string
	for _, field := range topDrops {
		if _, renamed := renames[field]; renamed {
			conflicts = append(conflicts, field)
		}
	}
	conflicts = canonjson.SortedStrings(conflicts)
	if len(conflicts) > 0 {
		failf("drops uses pre-rename name(s) %s — renames run first; drop the NEW name instead", strings.Join(conflicts, ", "))
	}

	sortLists := stringArraySlice(override["sort_lists"], resourceType+".override.sort_lists")
	var dottedSorts []string
	for _, field := range sortLists {
		if strings.Contains(field, ".") {
			dottedSorts = append(dottedSorts, field)
		}
	}
	if len(dottedSorts) > 0 {
		failf("sort_lists does not support nested (dotted) paths: %s", strings.Join(dottedSorts, ", "))
	}

	dropDefaults := objectMap(override["drop_if_default"], resourceType+".override.drop_if_default")
	var dotted []string
	for _, field := range drops {
		if strings.Contains(field, ".") {
			dotted = append(dotted, field)
		}
	}
	for field := range dropDefaults {
		if strings.Contains(field, ".") {
			dotted = append(dotted, field)
		}
	}
	for _, field := range canonjson.SortedStrings(dotted) {
		segments := strings.Split(field, ".")
		current := block
		for _, segment := range segments[:len(segments)-1] {
			inputBlocks, err := metadata.TerraformInputBlockTypes(current, resourceType)
			if err != nil {
				fail(err.Error())
			}
			blockType, ok := inputBlocks[segment]
			if !ok {
				failf("dotted path %s: %s is not a nested block in the %s schema", jsonQuote(field), jsonQuote(segment), resourceType)
			}
			current = requireBlockObject(blockType["block"], resourceType+"."+field+"."+segment)
		}
		last := segments[len(segments)-1]
		attributes, err := metadata.TerraformAttributesForBlock(current, resourceType)
		if err != nil {
			fail(err.Error())
		}
		if _, hasAttribute := attributes[last]; !hasAttribute {
			failf("dotted path %s: %s is not an attribute of that block in the %s schema", jsonQuote(field), jsonQuote(last), resourceType)
		}
	}
}

// executeTransform ports executeTransform from
// node-src/domain/pull-transform.ts.
func executeTransform(
	rawItems []any,
	resource *runtimeTransformResource,
	htmlUnescape func(string) string,
	onSkip func(item any, reason string),
) PullTransformResult {
	items := make(map[string]TransformRecord)
	originals := make(map[string]TransformRecord)
	var drops []string

	acknowledgedSlice := stringArraySlice(resource.Override["acknowledged_drops"], resource.Type+".override.acknowledged_drops")
	acknowledged := make(map[string]struct{}, len(acknowledgedSlice))
	for _, path := range acknowledgedSlice {
		acknowledged[path] = struct{}{}
	}

	dropsSlice := stringArraySlice(resource.Override["drops"], resource.Type+".override.drops")
	nestedDrops := make(map[string]struct{})
	for _, field := range dropsSlice {
		if strings.Contains(field, ".") {
			nestedDrops[field] = struct{}{}
		}
	}

	dropDefaults := objectMap(resource.Override["drop_if_default"], resource.Type+".override.drop_if_default")
	nestedDropDefaults := make(map[string]any)
	for field, value := range dropDefaults {
		if strings.Contains(field, ".") {
			nestedDropDefaults[field] = value
		}
	}

	for _, raw := range rawItems {
		snakeRawValue := snakeKeys(raw, "$raw", resource.StrictFrozenCompatibility)
		snakeRaw, isObject := snakeRawValue.(map[string]any)
		if !isObject {
			fail("each raw transform item must be a JSON object")
		}
		unescapeDisplayFields(snakeRaw, resource, htmlUnescape)
		reason, skipped := skipMatchReason(snakeRaw, resource)
		if skipped {
			if onSkip != nil {
				onSkip(snakeRaw, reason)
			}
			continue
		}
		normalized := applyReachableOverrides(snakeRaw, resource)
		escapeHtmlFields(normalized, resource, htmlUnescape)
		key := deriveKey(normalized, resource)
		if _, exists := items[key]; exists {
			failf("duplicate derived key %s for %s; set a different key_field in the override map", jsonQuote(key), resource.Type)
		}
		filtered := filterItem(normalized, resource.Projection, "", &drops, acknowledged, nestedDrops, nestedDropDefaults)
		items[key] = coerceItem(filtered, resource.Projection)
		originals[key] = normalized
	}

	seen := make(map[string]struct{}, len(drops))
	finalDrops := make([]string, 0, len(drops))
	for _, drop := range drops {
		if _, acked := acknowledged[drop]; acked {
			continue
		}
		if _, duplicate := seen[drop]; duplicate {
			continue
		}
		seen[drop] = struct{}{}
		finalDrops = append(finalDrops, drop)
	}
	finalDrops = canonjson.SortedStrings(finalDrops)

	return PullTransformResult{
		Items:     items,
		Originals: originals,
		Drops:     finalDrops,
	}
}

// TransformLoadedItemsOptions ports the exported TransformLoadedItemsOptions
// interface from node-src/domain/pull-transform.ts.
type TransformLoadedItemsOptions struct {
	Resource metadata.LoadedResourceMetadata
	Schema   metadata.JsonObject
	RawItems []any
	// HTMLUnescape is one Python-compatible html.unescape pass; the kernel
	// applies it twice where the resource's projection calls for HTML
	// unescaping. nil is the Go analogue of the Node source's
	// options.htmlUnescape being undefined.
	HTMLUnescape func(string) string
	// UnescapeHTML is true only when the owning pack lists this resource
	// prefix for unescape (the Go analogue of options.unescapeHtml === true
	// -- a plain bool field already matches that strict-true semantics for
	// every other value, including an unset/absent flag).
	UnescapeHTML bool
	OnSkip       func(item any, reason string)
}

// TransformLoadedItems ports the exported transformLoadedItems from
// node-src/domain/pull-transform.ts: "Transform already-collected items
// directly from active pack metadata."
func TransformLoadedItems(options TransformLoadedItemsOptions) (result PullTransformResult, err error) {
	defer recoverErr(&err)
	override := options.Resource.Override
	if override == nil {
		override = map[string]any{}
	}
	block, blockErr := metadata.TerraformBlockForSchema(options.Schema, options.Resource.Type)
	if blockErr != nil {
		fail(blockErr.Error())
	}
	validateLoadedOverride(options.Resource.Type, override, block)

	mergeBlocksSlice := stringArraySlice(override["merge_blocks"], options.Resource.Type+".override.merge_blocks")
	mergeBlocks := make(map[string]struct{}, len(mergeBlocksSlice))
	for _, name := range mergeBlocksSlice {
		mergeBlocks[name] = struct{}{}
	}
	noHTMLUnescape, _ := override["no_html_unescape"].(bool)
	htmlUnescapePasses := 0
	if options.UnescapeHTML && !noHTMLUnescape {
		htmlUnescapePasses = 2
	}

	resource := &runtimeTransformResource{
		Type:                      options.Resource.Type,
		Override:                  override,
		Projection:                compileProjection(block, options.Resource.Type+".block", mergeBlocks, true),
		HTMLUnescapePasses:        htmlUnescapePasses,
		StrictFrozenCompatibility: false,
	}
	return executeTransform(options.RawItems, resource, options.HTMLUnescape, options.OnSkip), nil
}

// derivedRule is the Go analogue of deriveReorderItems's inline
// `{ id: string; order: string }` rule shape from
// node-src/domain/pull-transform.ts.
type derivedRule struct {
	ID    string
	Order string
}

// derivedRuleInteger mirrors compareDerivedRules's
// `integerValue(parsePythonInteger(order))` double-conversion: parsing
// order as a Python integer string first, then re-normalizing that result
// through integerValue. The second step always succeeds whenever the first
// one does (parsePythonInteger's own return value is always accepted by
// losslessIntegerToken/integerValue), so this is kept only for direct
// structural correspondence with the Node source, not because the second
// conversion can independently fail here.
func derivedRuleInteger(order string) (*big.Int, bool) {
	parsed := parsePythonInteger(order)
	if !parsed.Ok {
		return nil, false
	}
	return integerValue(parsed.AsNumber())
}

// compareDerivedRules ports compareDerivedRules from
// node-src/domain/pull-transform.ts.
func compareDerivedRules(left, right derivedRule) int {
	leftInteger, leftHasInteger := derivedRuleInteger(left.Order)
	rightInteger, rightHasInteger := derivedRuleInteger(right.Order)
	if leftHasInteger && rightHasInteger {
		if cmp := leftInteger.Cmp(rightInteger); cmp != 0 {
			return cmp
		}
	} else if leftHasInteger {
		return -1
	} else if rightHasInteger {
		return 1
	} else {
		if cmp := canonjson.ComparePythonStrings(left.Order, right.Order); cmp != 0 {
			return cmp
		}
	}
	return canonjson.ComparePythonStrings(left.ID, right.ID)
}

// DeriveReorderItems ports the exported deriveReorderItems from
// node-src/domain/pull-transform.ts: "Port of the registry-driven,
// config-only reorder derivation."
func DeriveReorderItems(rawItems []any, derive map[string]any) (result map[string]TransformRecord, err error) {
	defer recoverErr(&err)
	source, sourceOK := derive["from"].(string)
	if !sourceOK || source == "" {
		fail("derive.from must be a non-empty string")
	}
	policyType, policyOK := derive["policy_type"].(string)
	if !policyOK || policyType == "" {
		fail("derive.policy_type must be a non-empty string")
	}
	var rules []derivedRule
	for _, raw := range rawItems {
		item, isObject := snakeKeys(raw, "$raw", false).(map[string]any)
		if !isObject {
			fail("each derived source item must be a JSON object")
		}
		id, hasID := item["id"]
		order, hasOrder := item["rule_order"]
		if !hasID || id == nil || !hasOrder || order == nil {
			missing := "rule_order"
			if !hasID || id == nil {
				missing = "id"
			}
			failf("cannot derive the reorder resource from %s: a source rule is missing %s — refusing to emit a partial reorder", source, missing)
		}
		rules = append(rules, derivedRule{
			ID:    identityComponent(id, "id", false),
			Order: identityComponent(order, "rule_order", false),
		})
	}
	// SliceStable, not Slice: the Node source's Array.prototype.sort is
	// stable (guaranteed since ES2019), and rules with equal
	// compareDerivedRules keys must keep their original (source rawItems)
	// order.
	sort.SliceStable(rules, func(i, j int) bool {
		return compareDerivedRules(rules[i], rules[j]) < 0
	})
	if len(rules) == 0 {
		return map[string]TransformRecord{}, nil
	}
	renderedRules := make([]any, len(rules))
	for i, rule := range rules {
		renderedRules[i] = map[string]any{"id": rule.ID, "order": rule.Order}
	}
	return map[string]TransformRecord{
		policyType: {
			"policy_type": policyType,
			"rules":       renderedRules,
		},
	}, nil
}
