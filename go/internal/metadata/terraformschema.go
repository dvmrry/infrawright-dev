package metadata

// terraformschema.go ports the original implementation: provider
// schema type encodings and attribute/block classification.
//
// Unlike the rest of this package, these functions are not called by
// loader.go, packs.go, or resources.go, so there is no exported-wrapper/
// unexported-implementation split here (see packs.go's file doc comment for
// that convention): every
// function below returns (T, error) directly, threading errors through its
// modest, shallow recursion the ordinary Go way rather than via this
// package's fail()/recoverMetadataError panic convention, since nothing
// here needs to compose with that machinery. the original implementation's
// own errors are a bare `throw new TypeError(message)` (not the
// MetadataError this package's other files raise), reflected here by the
// distinct TerraformSchemaError type.
import (
	"encoding/json"
	"fmt"
)

// TerraformSchemaError reports a malformed Terraform provider-schema
// document. Ports the `throw new TypeError(message)` calls in
// the original implementation's schemaError.
type TerraformSchemaError struct{ message string }

// Error implements the error interface.
func (e *TerraformSchemaError) Error() string { return e.message }

func schemaErrorf(format string, args ...any) error {
	return &TerraformSchemaError{message: fmt.Sprintf(format, args...)}
}

// TerraformTypeEncoding is the Go analogue of the recursive
// TerraformTypeEncoding union in the original implementation: a
// bare primitive (TerraformPrimitiveType), a list/map/set wrapping another
// encoding (TerraformCollectionType), or a nested object
// (TerraformObjectType).
type TerraformTypeEncoding interface {
	isTerraformTypeEncoding()
}

// TerraformPrimitiveType is a bare "bool" | "number" | "string" leaf type.
type TerraformPrimitiveType string

func (TerraformPrimitiveType) isTerraformTypeEncoding() {}

// TerraformCollectionType is a `["list" | "map" | "set", inner]` encoding.
type TerraformCollectionType struct {
	Kind  string // "list" | "map" | "set"
	Inner TerraformTypeEncoding
}

func (TerraformCollectionType) isTerraformTypeEncoding() {}

// TerraformObjectType is an `["object", members]` encoding.
type TerraformObjectType struct {
	Members map[string]TerraformTypeEncoding
}

func (TerraformObjectType) isTerraformTypeEncoding() {}

// TerraformClassifiedAttributes ports the TerraformClassifiedAttributes
// interface from the original implementation.
type TerraformClassifiedAttributes struct {
	Required     []string
	Optional     []string
	ComputedOnly []string
}

// TerraformRequireObject ports terraformRequireObject from
// the original implementation.
func TerraformRequireObject(value any, label string) (JsonObject, error) {
	obj, ok := value.(JsonObject)
	if !ok {
		return nil, schemaErrorf("%s must be an object", label)
	}
	return obj, nil
}

// TerraformBlockForSchema ports terraformBlockForSchema from
// the original implementation.
func TerraformBlockForSchema(schema JsonObject, label string) (JsonObject, error) {
	return TerraformRequireObject(schema["block"], label+".block")
}

// TerraformAttributesForBlock ports terraformAttributesForBlock from
// the original implementation.
func TerraformAttributesForBlock(block JsonObject, label string) (JsonObject, error) {
	value, hasAttributes := block["attributes"]
	if !hasAttributes || value == nil {
		return JsonObject{}, nil
	}
	return TerraformRequireObject(value, label+".attributes")
}

// TerraformBlockTypesForBlock ports terraformBlockTypesForBlock from
// the original implementation.
func TerraformBlockTypesForBlock(block JsonObject, label string) (JsonObject, error) {
	value, hasBlockTypes := block["block_types"]
	if !hasBlockTypes || value == nil {
		return JsonObject{}, nil
	}
	return TerraformRequireObject(value, label+".block_types")
}

// TerraformBooleanField ports terraformBooleanField from
// the original implementation.
func TerraformBooleanField(value JsonObject, name string) bool {
	b, _ := value[name].(bool)
	return b
}

// TerraformClassifyAttributes ports terraformClassifyAttributes from
// the original implementation.
func TerraformClassifyAttributes(block JsonObject, label string) (TerraformClassifiedAttributes, error) {
	var required, optional, computedOnly []string
	attributes, err := TerraformAttributesForBlock(block, label)
	if err != nil {
		return TerraformClassifiedAttributes{}, err
	}
	for _, name := range sortedKeys(attributes) {
		attribute, err := TerraformRequireObject(attributes[name], fmt.Sprintf("%s.attributes.%s", label, name))
		if err != nil {
			return TerraformClassifiedAttributes{}, err
		}
		switch {
		case TerraformBooleanField(attribute, "deprecated") && !TerraformBooleanField(attribute, "required"):
			computedOnly = append(computedOnly, name)
		case TerraformBooleanField(attribute, "required"):
			required = append(required, name)
		case TerraformBooleanField(attribute, "optional"):
			optional = append(optional, name)
		default:
			computedOnly = append(computedOnly, name)
		}
	}
	return TerraformClassifiedAttributes{Required: required, Optional: optional, ComputedOnly: computedOnly}, nil
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// TerraformResourceInputAttributes ports terraformResourceInputAttributes
// from the original implementation.
func TerraformResourceInputAttributes(block JsonObject, label string) (TerraformClassifiedAttributes, error) {
	classified, err := TerraformClassifyAttributes(block, label)
	if err != nil {
		return TerraformClassifiedAttributes{}, err
	}
	attributes, err := TerraformAttributesForBlock(block, label)
	if err != nil {
		return TerraformClassifiedAttributes{}, err
	}
	id, hasID := attributes["id"]
	if containsString(classified.Optional, "id") && hasID {
		idObject, err := TerraformRequireObject(id, label+".attributes.id")
		if err != nil {
			return TerraformClassifiedAttributes{}, err
		}
		if TerraformBooleanField(idObject, "computed") {
			optional := make([]string, 0, len(classified.Optional))
			for _, name := range classified.Optional {
				if name != "id" {
					optional = append(optional, name)
				}
			}
			computedOnly := make([]string, 0, len(classified.ComputedOnly)+1)
			computedOnly = append(computedOnly, classified.ComputedOnly...)
			computedOnly = append(computedOnly, "id")
			return TerraformClassifiedAttributes{
				Required:     classified.Required,
				Optional:     optional,
				ComputedOnly: computedOnly,
			}, nil
		}
	}
	return classified, nil
}

// terraformEncoding ports terraformEncoding from
// the original implementation.
func terraformEncoding(value any, label string) (TerraformTypeEncoding, error) {
	if s, ok := value.(string); ok && (s == "bool" || s == "number" || s == "string") {
		return TerraformPrimitiveType(s), nil
	}
	arr, ok := value.([]any)
	if !ok || len(arr) != 2 {
		return nil, schemaErrorf("unsupported type encoding at %s", label)
	}
	kind, _ := arr[0].(string)
	inner := arr[1]
	if kind == "object" {
		object, err := TerraformRequireObject(inner, label+"[1]")
		if err != nil {
			return nil, err
		}
		members := make(map[string]TerraformTypeEncoding, len(object))
		for _, name := range sortedKeys(object) {
			encoded, err := terraformEncoding(object[name], fmt.Sprintf("%s[1].%s", label, name))
			if err != nil {
				return nil, err
			}
			members[name] = encoded
		}
		return TerraformObjectType{Members: members}, nil
	}
	if kind == "list" || kind == "map" || kind == "set" {
		encoded, err := terraformEncoding(inner, label+"[1]")
		if err != nil {
			return nil, err
		}
		return TerraformCollectionType{Kind: kind, Inner: encoded}, nil
	}
	return nil, schemaErrorf("unsupported type encoding at %s", label)
}

// terraformNestedTypeEncoding ports terraformNestedTypeEncoding from
// the original implementation.
func terraformNestedTypeEncoding(value any, label string) (TerraformTypeEncoding, error) {
	nestedType, err := TerraformRequireObject(value, label)
	if err != nil {
		return nil, err
	}
	attributes, err := TerraformAttributesForBlock(nestedType, label)
	if err != nil {
		return nil, err
	}
	members := make(map[string]TerraformTypeEncoding)
	for _, name := range sortedKeys(attributes) {
		attribute, err := TerraformRequireObject(attributes[name], fmt.Sprintf("%s.attributes.%s", label, name))
		if err != nil {
			return nil, err
		}
		if TerraformBooleanField(attribute, "deprecated") && !TerraformBooleanField(attribute, "required") {
			continue
		}
		if TerraformBooleanField(attribute, "required") || TerraformBooleanField(attribute, "optional") {
			encoded, err := TerraformAttributeType(attribute, fmt.Sprintf("%s.attributes.%s", label, name))
			if err != nil {
				return nil, err
			}
			members[name] = encoded
		}
	}
	objectEncoding := TerraformObjectType{Members: members}
	mode, _ := nestedType["nesting_mode"].(string)
	switch mode {
	case "single":
		return objectEncoding, nil
	case "list", "map", "set":
		return TerraformCollectionType{Kind: mode, Inner: objectEncoding}, nil
	default:
		return nil, schemaErrorf("unsupported nested_type nesting_mode %s", mustMarshalJSON(nestedType["nesting_mode"]))
	}
}

// TerraformAttributeType ports terraformAttributeType from
// the original implementation.
func TerraformAttributeType(attribute JsonObject, label string) (TerraformTypeEncoding, error) {
	if typeValue, hasType := attribute["type"]; hasType {
		return terraformEncoding(typeValue, label+".type")
	}
	if nestedType, hasNestedType := attribute["nested_type"]; hasNestedType {
		return terraformNestedTypeEncoding(nestedType, label+".nested_type")
	}
	return nil, schemaErrorf("attribute has no type or nested_type: %s", label)
}

// numberEqualsOne reports whether value is the JSON number 1, accepting
// either a plain float64 or a losslessly preserved json.Number token,
// matching the original implementation's
// `maxItems.toString() === "1"` (itself already gated on
// isIntegerJsonNumber(maxItems) at the one call site, terraformBlockIsSingle).
func numberEqualsOne(value any) bool {
	switch v := value.(type) {
	case float64:
		return v == 1
	case json.Number:
		return string(v) == "1"
	default:
		return false
	}
}

// TerraformBlockIsSingle ports terraformBlockIsSingle from
// the original implementation.
func TerraformBlockIsSingle(blockType JsonObject) bool {
	if mode, _ := blockType["nesting_mode"].(string); mode == "single" {
		return true
	}
	maxItems := blockType["max_items"]
	return isIntegerJsonNumber(maxItems) && numberEqualsOne(maxItems)
}

// TerraformBlockHasInputs ports terraformBlockHasInputs from
// the original implementation.
func TerraformBlockHasInputs(block JsonObject, label string) (bool, error) {
	classified, err := TerraformClassifyAttributes(block, label)
	if err != nil {
		return false, err
	}
	if len(classified.Required) > 0 || len(classified.Optional) > 0 {
		return true, nil
	}
	nested, err := TerraformBlockTypesForBlock(block, label)
	if err != nil {
		return false, err
	}
	for _, name := range sortedKeys(nested) {
		blockType, err := TerraformRequireObject(nested[name], fmt.Sprintf("%s.block_types.%s", label, name))
		if err != nil {
			return false, err
		}
		child, err := TerraformRequireObject(blockType["block"], fmt.Sprintf("%s.block_types.%s.block", label, name))
		if err != nil {
			return false, err
		}
		has, err := TerraformBlockHasInputs(child, fmt.Sprintf("%s.block_types.%s.block", label, name))
		if err != nil {
			return false, err
		}
		if has {
			return true, nil
		}
	}
	return false, nil
}

// TerraformInputBlockTypes ports terraformInputBlockTypes from
// the original implementation. The Node source returns a
// ReadonlyMap preserving sorted-name insertion order; this returns a plain
// Go map (unordered) since nothing in this port's scope consumes iteration
// order from it.
func TerraformInputBlockTypes(block JsonObject, label string) (map[string]JsonObject, error) {
	output := make(map[string]JsonObject)
	nested, err := TerraformBlockTypesForBlock(block, label)
	if err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(nested) {
		blockType, err := TerraformRequireObject(nested[name], fmt.Sprintf("%s.block_types.%s", label, name))
		if err != nil {
			return nil, err
		}
		child, err := TerraformRequireObject(blockType["block"], fmt.Sprintf("%s.block_types.%s.block", label, name))
		if err != nil {
			return nil, err
		}
		has, err := TerraformBlockHasInputs(child, fmt.Sprintf("%s.block_types.%s.block", label, name))
		if err != nil {
			return nil, err
		}
		if has {
			output[name] = blockType
		}
	}
	return output, nil
}
