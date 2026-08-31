package metadata

import (
	"encoding/json"
	"math/big"
)

// TerraformProviderSchemaStatus is the shared writable-path classification
// used by pack validation and adoption projection. Keeping this substrate in
// metadata makes a value_rewrite pack entry face the same input/computed-only
// distinction that projection_sync enforces at runtime.
func TerraformProviderSchemaStatus(schema JsonObject, resourceType string, path []any, requiredness bool) (string, error) {
	block, err := TerraformBlockForSchema(schema, resourceType)
	if err != nil {
		return "", err
	}
	return terraformSchemaStatusBlock(block, path, resourceType, true, requiredness)
}

func terraformSchemaStripCollection(path []any) []any {
	if len(path) == 0 {
		return path
	}
	switch value := path[0].(type) {
	case int64, *big.Int:
		return path[1:]
	case string:
		if value == "*" {
			return path[1:]
		}
	}
	return path
}

func terraformSchemaContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func terraformSchemaRequiredNestedBlock(blockType JsonObject) bool {
	switch value := blockType["min_items"].(type) {
	case float64:
		return value >= 1
	case json.Number:
		integer, err := value.Int64()
		return err == nil && integer >= 1
	default:
		return false
	}
}

func terraformSchemaStatusEncoding(encoding TerraformTypeEncoding, path []any, base string) string {
	if len(path) == 0 {
		return base
	}
	switch typed := encoding.(type) {
	case TerraformPrimitiveType:
		return "unknown"
	case TerraformCollectionType:
		switch typed.Kind {
		case "list", "set":
			return terraformSchemaStatusEncoding(typed.Inner, terraformSchemaStripCollection(path), base)
		case "map":
			return base
		default:
			return "unknown"
		}
	case TerraformObjectType:
		segment, ok := path[0].(string)
		inner, exists := typed.Members[segment]
		if ok && exists {
			return terraformSchemaStatusEncoding(inner, path[1:], base)
		}
	}
	return "unknown"
}

func terraformSchemaStatusBlock(block JsonObject, path []any, label string, resourceTop, requiredness bool) (string, error) {
	if len(path) == 0 {
		return "block", nil
	}
	segment, ok := path[0].(string)
	if !ok || segment == "*" {
		return "unknown", nil
	}
	attributes, err := TerraformAttributesForBlock(block, label)
	if err != nil {
		return "", err
	}
	var inputs TerraformClassifiedAttributes
	if resourceTop {
		inputs, err = TerraformResourceInputAttributes(block, label)
	} else {
		inputs, err = TerraformClassifyAttributes(block, label)
	}
	if err != nil {
		return "", err
	}
	if terraformSchemaContains(inputs.Required, segment) || terraformSchemaContains(inputs.Optional, segment) {
		base := "optional"
		if terraformSchemaContains(inputs.Required, segment) {
			base = "required"
		}
		if len(path) == 1 {
			return base, nil
		}
		attribute, err := TerraformRequireObject(attributes[segment], label+".attributes."+segment)
		if err != nil {
			return "", err
		}
		encoding, err := TerraformAttributeType(attribute, label+".attributes."+segment)
		if err != nil {
			return "", err
		}
		return terraformSchemaStatusEncoding(encoding, path[1:], base), nil
	}
	allBlocks, err := TerraformBlockTypesForBlock(block, label)
	if err != nil {
		return "", err
	}
	inputBlocks, err := TerraformInputBlockTypes(block, label)
	if err != nil {
		return "", err
	}
	if blockType, exists := inputBlocks[segment]; exists {
		if len(path) == 1 && requiredness {
			if terraformSchemaRequiredNestedBlock(blockType) {
				return "required", nil
			}
			return "optional", nil
		}
		child, err := TerraformRequireObject(blockType["block"], label+".block_types."+segment+".block")
		if err != nil {
			return "", err
		}
		return terraformSchemaStatusBlock(child, terraformSchemaStripCollection(path[1:]), label+".block_types."+segment+".block", false, requiredness)
	}
	if _, exists := attributes[segment]; exists {
		return "computed_only", nil
	}
	if _, exists := allBlocks[segment]; exists {
		return "computed_only", nil
	}
	return "unknown", nil
}

// TerraformValueRewriteAttribute returns the writable schema attribute at a
// parsed value_rewrite path. The bool is false for an unknown, computed-only,
// repeated, or otherwise non-attribute path. The caller separately checks the
// status to produce the pack-validation diagnostic and checks enum metadata on
// the returned terminal attribute. This walker must stay in agreement with
// ProviderSchemaStatus and guardProjectionPath; those three paths jointly
// define which value_rewrite targets are accepted and projected.
func TerraformValueRewriteAttribute(schema JsonObject, resourceType string, path []any) (JsonObject, bool, error) {
	block, err := TerraformBlockForSchema(schema, resourceType)
	if err != nil {
		return nil, false, err
	}
	return terraformValueRewriteAttributeInBlock(block, path, resourceType, true)
}

func terraformValueRewriteAttributeInBlock(block JsonObject, path []any, label string, resourceTop bool) (JsonObject, bool, error) {
	if len(path) == 0 {
		return nil, false, nil
	}
	segment, ok := path[0].(string)
	if !ok || segment == "*" {
		return nil, false, nil
	}
	attributes, err := TerraformAttributesForBlock(block, label)
	if err != nil {
		return nil, false, err
	}
	if rawAttribute, exists := attributes[segment]; exists {
		attribute, err := TerraformRequireObject(rawAttribute, label+".attributes."+segment)
		if err != nil {
			return nil, false, err
		}
		var inputs TerraformClassifiedAttributes
		if resourceTop {
			inputs, err = TerraformResourceInputAttributes(block, label)
		} else {
			inputs, err = TerraformClassifyAttributes(block, label)
		}
		if err != nil {
			return nil, false, err
		}
		if !terraformSchemaContains(inputs.Required, segment) && !terraformSchemaContains(inputs.Optional, segment) {
			return nil, false, nil
		}
		if len(path) == 1 {
			return attribute, true, nil
		}
		if nested, ok := attribute["nested_type"].(JsonObject); ok {
			// A v1 rewrite path has no collection selector, so it cannot
			// identify an element inside a repeated nested attribute. The
			// runtime projection applies the same refusal before it writes.
			if mode, _ := nested["nesting_mode"].(string); mode != "single" {
				return nil, false, nil
			}
			return terraformValueRewriteAttributeInBlock(nested, path[1:], label+".attributes."+segment+".nested_type", false)
		}
		encoding, err := TerraformAttributeType(attribute, label+".attributes."+segment)
		if err != nil {
			return nil, false, err
		}
		if terraformValueRewriteEncodingPath(encoding, path[1:]) {
			// Legacy type encodings carry no per-member attribute metadata, so
			// the containing writable attribute is the closest enum-bearing
			// schema node available.
			return attribute, true, nil
		}
		return nil, false, nil
	}

	inputBlocks, err := TerraformInputBlockTypes(block, label)
	if err != nil {
		return nil, false, err
	}
	blockType, exists := inputBlocks[segment]
	if !exists || !TerraformBlockIsSingle(blockType) {
		return nil, false, nil
	}
	child, err := TerraformRequireObject(blockType["block"], label+".block_types."+segment+".block")
	if err != nil {
		return nil, false, err
	}
	return terraformValueRewriteAttributeInBlock(child, path[1:], label+".block_types."+segment+".block", false)
}

func terraformValueRewriteEncodingPath(encoding TerraformTypeEncoding, path []any) bool {
	if len(path) == 0 {
		return true
	}
	switch typed := encoding.(type) {
	case TerraformPrimitiveType:
		return false
	case TerraformCollectionType:
		if typed.Kind == "list" || typed.Kind == "set" {
			// v1 paths are exact string-segment paths; they do not select
			// collection indexes or wildcard members.
			return false
		}
		if typed.Kind == "map" {
			return terraformValueRewriteEncodingPath(typed.Inner, path[1:])
		}
		return false
	case TerraformObjectType:
		segment, ok := path[0].(string)
		if !ok {
			return false
		}
		inner, exists := typed.Members[segment]
		return exists && terraformValueRewriteEncodingPath(inner, path[1:])
	default:
		return false
	}
}
