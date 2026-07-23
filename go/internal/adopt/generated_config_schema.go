package adopt

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

// ProjectionError ports ProjectionError from
// the original implementation.
type ProjectionError struct{ Message string }

// Error implements error.
func (e *ProjectionError) Error() string { return e.Message }

func projectionErrorf(format string, args ...any) error {
	return &ProjectionError{Message: fmt.Sprintf(format, args...)}
}

func classifiedAttributes(block metadata.JsonObject, label string, resourceTop bool) (metadata.TerraformClassifiedAttributes, error) {
	if resourceTop {
		return metadata.TerraformResourceInputAttributes(block, label)
	}
	return metadata.TerraformClassifyAttributes(block, label)
}

func stripCollection(path []any) []any {
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

func schemaStatusEncoding(encoding metadata.TerraformTypeEncoding, path []any, base string) string {
	if len(path) == 0 {
		return base
	}
	switch typed := encoding.(type) {
	case metadata.TerraformPrimitiveType:
		return "unknown"
	case metadata.TerraformCollectionType:
		switch typed.Kind {
		case "list", "set":
			return schemaStatusEncoding(typed.Inner, stripCollection(path), base)
		case "map":
			return base
		default:
			return "unknown"
		}
	case metadata.TerraformObjectType:
		segment, ok := path[0].(string)
		inner, exists := typed.Members[segment]
		if ok && exists {
			return schemaStatusEncoding(inner, path[1:], base)
		}
	}
	return "unknown"
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func requiredNestedBlock(blockType metadata.JsonObject) bool {
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

func schemaStatusBlock(block metadata.JsonObject, path []any, label string, resourceTop, requiredness bool) (string, error) {
	if len(path) == 0 {
		return "block", nil
	}
	segment, ok := path[0].(string)
	if !ok || segment == "*" {
		return "unknown", nil
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, label)
	if err != nil {
		return "", err
	}
	inputs, err := classifiedAttributes(block, label, resourceTop)
	if err != nil {
		return "", err
	}
	if contains(inputs.Required, segment) || contains(inputs.Optional, segment) {
		base := "optional"
		if contains(inputs.Required, segment) {
			base = "required"
		}
		if len(path) == 1 {
			return base, nil
		}
		attribute, err := metadata.TerraformRequireObject(attributes[segment], label+".attributes."+segment)
		if err != nil {
			return "", err
		}
		encoding, err := metadata.TerraformAttributeType(attribute, label+".attributes."+segment)
		if err != nil {
			return "", err
		}
		return schemaStatusEncoding(encoding, path[1:], base), nil
	}
	allBlocks, err := metadata.TerraformBlockTypesForBlock(block, label)
	if err != nil {
		return "", err
	}
	inputBlocks, err := metadata.TerraformInputBlockTypes(block, label)
	if err != nil {
		return "", err
	}
	if blockType, exists := inputBlocks[segment]; exists {
		if len(path) == 1 && requiredness {
			if requiredNestedBlock(blockType) {
				return "required", nil
			}
			return "optional", nil
		}
		child, err := metadata.TerraformRequireObject(blockType["block"], label+".block_types."+segment+".block")
		if err != nil {
			return "", err
		}
		return schemaStatusBlock(child, stripCollection(path[1:]), label+".block_types."+segment+".block", false, requiredness)
	}
	if _, exists := attributes[segment]; exists {
		return "computed_only", nil
	}
	if _, exists := allBlocks[segment]; exists {
		return "computed_only", nil
	}
	return "unknown", nil
}

// ProviderSchemaStatus ports providerSchemaStatus from
// the original implementation. D3 reuses this narrow D1 substrate rather
// than maintaining a second schema walker.
func ProviderSchemaStatus(schema metadata.JsonObject, resourceType string, path []any, requiredness bool) (string, error) {
	block, err := metadata.TerraformBlockForSchema(schema, resourceType)
	if err != nil {
		return "", err
	}
	return schemaStatusBlock(block, path, resourceType, true, requiredness)
}

func attributeSensitive(attribute metadata.JsonObject) (bool, error) {
	if metadata.TerraformBooleanField(attribute, "sensitive") {
		return true, nil
	}
	nested, ok := attribute["nested_type"].(metadata.JsonObject)
	if !ok {
		return false, nil
	}
	attributes, err := metadata.TerraformAttributesForBlock(nested, "nested_type")
	if err != nil {
		return false, err
	}
	for _, name := range canonSortedKeys(attributes) {
		child, ok := attributes[name].(metadata.JsonObject)
		if !ok {
			continue
		}
		sensitive, err := attributeSensitive(child)
		if err != nil || sensitive {
			return sensitive, err
		}
	}
	return false, nil
}

func blockContainsSensitive(block metadata.JsonObject, label string) (bool, error) {
	attributes, err := metadata.TerraformAttributesForBlock(block, label)
	if err != nil {
		return false, err
	}
	for _, name := range canonSortedKeys(attributes) {
		attribute, ok := attributes[name].(metadata.JsonObject)
		if !ok {
			continue
		}
		sensitive, err := attributeSensitive(attribute)
		if err != nil || sensitive {
			return sensitive, err
		}
	}
	blocks, err := metadata.TerraformBlockTypesForBlock(block, label)
	if err != nil {
		return false, err
	}
	for _, name := range canonSortedKeys(blocks) {
		blockType, ok := blocks[name].(metadata.JsonObject)
		if !ok {
			continue
		}
		child, err := metadata.TerraformRequireObject(blockType["block"], label+".block_types."+name+".block")
		if err != nil {
			return false, err
		}
		sensitive, err := blockContainsSensitive(child, label+".block_types."+name+".block")
		if err != nil || sensitive {
			return sensitive, err
		}
	}
	return false, nil
}

func emptyFill(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case []any:
		if len(typed) == 0 {
			return true
		}
		for _, child := range typed {
			if !emptyFill(child) {
				return false
			}
		}
		return true
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

// ProjectionFillValue ports projectionFillValue from
// the original implementation.
func ProjectionFillValue(entry metadata.PolicyEntry, rawItem map[string]any, resourceType string, schema metadata.JsonObject) (any, bool, error) {
	data := entry.Data()
	target, _ := data["path"].(string)
	source, _ := data["source"].(string)
	targetPath, err := metadata.ParsePolicyPath(target)
	if err != nil {
		return nil, false, err
	}
	status, err := ProviderSchemaStatus(schema, resourceType, targetPath, true)
	if err != nil {
		return nil, false, err
	}
	if status != "required" && status != "optional" {
		return nil, false, projectionErrorf("refusing to projection_fill path %s of %s: not a writable input", target, resourceType)
	}
	name, ok := targetPath[0].(string)
	if !ok {
		return nil, false, projectionErrorf("invalid projection_fill path %s", target)
	}
	block, err := metadata.TerraformBlockForSchema(schema, resourceType)
	if err != nil {
		return nil, false, err
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, resourceType)
	if err != nil {
		return nil, false, err
	}
	if attribute, ok := attributes[name].(metadata.JsonObject); ok {
		sensitive, err := attributeSensitive(attribute)
		if err != nil {
			return nil, false, err
		}
		if sensitive {
			return nil, false, projectionErrorf("refusing to projection_fill sensitive path %s of %s", target, resourceType)
		}
	}
	blocks, err := metadata.TerraformInputBlockTypes(block, resourceType)
	if err != nil {
		return nil, false, err
	}
	if blockType, exists := blocks[name]; exists {
		child, err := metadata.TerraformRequireObject(blockType["block"], resourceType+".block_types."+name+".block")
		if err != nil {
			return nil, false, err
		}
		sensitive, err := blockContainsSensitive(child, resourceType+".block_types."+name+".block")
		if err != nil {
			return nil, false, err
		}
		if sensitive {
			return nil, false, projectionErrorf("refusing to projection_fill sensitive block %s of %s", target, resourceType)
		}
	}
	raw, exists := rawItem[source]
	if !exists || emptyFill(raw) {
		return nil, false, nil
	}
	value, err := transform.ProjectLoadedRawField(transform.ProjectLoadedRawFieldOptions{RawValue: raw, ResourceType: resourceType, Schema: schema, Target: name})
	if err != nil {
		return nil, false, err
	}
	if emptyFill(value) {
		return nil, false, nil
	}
	return value, true, nil
}

func canonSortedKeys[V any](value map[string]V) []string {
	keys := mapKeys(value)
	sort.Strings(keys)
	return keys
}
