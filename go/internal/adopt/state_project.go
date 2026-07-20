package adopt

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

// ProjectProviderStateOptions ports projectProviderState's options bag from
// node-src/domain/state-project.ts.
type ProjectProviderStateOptions struct {
	Policy          *metadata.DriftPolicy
	RawItem         map[string]any
	ResourceType    string
	Root            *metadata.LoadedPackRoot
	SensitiveValues any
	StateValues     any
}

func projectionRecord(value any) (map[string]any, bool) {
	record, ok := value.(map[string]any)
	return record, ok
}

func cloneProjectionValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		output := make(map[string]any, len(typed))
		for key, child := range typed {
			output[key] = cloneProjectionValue(child)
		}
		return output
	case []any:
		output := make([]any, len(typed))
		for index, child := range typed {
			output[index] = cloneProjectionValue(child)
		}
		return output
	default:
		return value
	}
}

func projectionPathText(path []any) string {
	if len(path) == 0 {
		return "<root>"
	}
	parts := make([]string, 0, len(path))
	for _, segment := range path {
		switch typed := segment.(type) {
		case int:
			prior := ""
			if len(parts) > 0 {
				prior = parts[len(parts)-1]
				parts = parts[:len(parts)-1]
			}
			parts = append(parts, fmt.Sprintf("%s[%d]", prior, typed))
		case int64:
			prior := ""
			if len(parts) > 0 {
				prior = parts[len(parts)-1]
				parts = parts[:len(parts)-1]
			}
			parts = append(parts, fmt.Sprintf("%s[%d]", prior, typed))
		case string:
			parts = append(parts, typed)
		default:
			parts = append(parts, fmt.Sprint(typed))
		}
	}
	return strings.Join(parts, ".")
}

// ValidateSensitiveMaskShape requires Terraform's value-aligned sensitivity
// mask grammar, porting validateSensitiveMaskShape.
func ValidateSensitiveMaskShape(mask, value any) error {
	type frame struct {
		mask  any
		value any
		root  bool
	}
	stack := []frame{{mask: mask, value: value, root: true}}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current.mask == nil {
			continue
		}
		if _, ok := current.mask.(bool); ok {
			continue
		}
		if maskRecord, ok := projectionRecord(current.mask); ok {
			valueRecord, valueOK := projectionRecord(current.value)
			if !valueOK {
				return projectionErrorf("unsupported sensitive mask shape")
			}
			for _, key := range canonjson.SortedStrings(adoptMapKeys(maskRecord)) {
				child, present := valueRecord[key]
				if !present {
					return projectionErrorf("unsupported sensitive mask shape")
				}
				stack = append(stack, frame{mask: maskRecord[key], value: child})
			}
			continue
		}
		if maskArray, ok := current.mask.([]any); ok && !current.root {
			valueArray, valueOK := current.value.([]any)
			if !valueOK || len(valueArray) != len(maskArray) {
				return projectionErrorf("unsupported sensitive mask shape")
			}
			for index := range maskArray {
				stack = append(stack, frame{mask: maskArray[index], value: valueArray[index]})
			}
			continue
		}
		return projectionErrorf("unsupported sensitive mask shape")
	}
	return nil
}

func anySensitive(value any) bool {
	if value == true {
		return true
	}
	if array, ok := value.([]any); ok {
		for _, child := range array {
			if anySensitive(child) {
				return true
			}
		}
	}
	if record, ok := projectionRecord(value); ok {
		for _, child := range record {
			if anySensitive(child) {
				return true
			}
		}
	}
	return false
}

func sensitiveAttributeMask(mask any, name string) bool {
	record, ok := projectionRecord(mask)
	return ok && anySensitive(record[name])
}

func projectionSingleValue(value any) (map[string]any, bool, error) {
	if record, ok := projectionRecord(value); ok {
		return record, true, nil
	}
	if array, ok := value.([]any); ok {
		if len(array) == 0 {
			return nil, false, nil
		}
		if len(array) == 1 {
			if record, ok := projectionRecord(array[0]); ok {
				return record, true, nil
			}
		}
	}
	return nil, false, projectionErrorf("single nested block had unsupported state shape")
}

func projectionSingleSensitivity(mask any, path []any) (any, error) {
	if mask == true {
		return mask, nil
	}
	if _, ok := projectionRecord(mask); ok {
		return mask, nil
	}
	if array, ok := mask.([]any); ok {
		switch len(array) {
		case 0:
			return map[string]any{}, nil
		case 1:
			if array[0] == nil {
				return map[string]any{}, nil
			}
			return array[0], nil
		default:
			return nil, projectionErrorf("single nested block had unsupported sensitive shape at %s", projectionPathText(path))
		}
	}
	return map[string]any{}, nil
}

func projectionListSensitivity(mask any, index int) any {
	if array, ok := mask.([]any); ok && index < len(array) {
		if array[index] != nil {
			return array[index]
		}
	}
	if _, ok := projectionRecord(mask); ok {
		return mask
	}
	return map[string]any{}
}

type projectBlockOptions struct {
	Block        metadata.JsonObject
	Label        string
	Mask         any
	Path         []any
	Policy       *metadata.DriftPolicy
	ResourceTop  bool
	ResourceType string
	Values       any
}

func projectProviderBlock(options projectBlockOptions) (map[string]any, error) {
	if options.Mask == true {
		return nil, projectionErrorf("sensitive input path %s cannot be written to generated tfvars without an explicit secret-handling policy", projectionPathText(options.Path))
	}
	values, ok := projectionRecord(options.Values)
	if !ok {
		return nil, projectionErrorf("state path %s is not an object", projectionPathText(options.Path))
	}
	inputs, err := classifiedAttributes(options.Block, options.Label, options.ResourceTop)
	if err != nil {
		return nil, err
	}
	required := make(map[string]struct{}, len(inputs.Required))
	optional := make(map[string]struct{}, len(inputs.Optional))
	for _, name := range inputs.Required {
		required[name] = struct{}{}
	}
	for _, name := range inputs.Optional {
		optional[name] = struct{}{}
	}
	names := append(append([]string(nil), inputs.Required...), inputs.Optional...)
	sort.Strings(names)
	output := make(map[string]any)
	for _, name := range names {
		childPath := append(append([]any(nil), options.Path...), name)
		if options.Policy != nil && options.Policy.ProjectionOmits(options.ResourceType, childPath) {
			if _, isRequired := required[name]; isRequired {
				return nil, projectionErrorf("policy cannot projection_omit required path %s", projectionPathText(childPath))
			}
			continue
		}
		if sensitiveAttributeMask(options.Mask, name) {
			return nil, projectionErrorf("sensitive input path %s cannot be written to generated tfvars without an explicit secret-handling policy", projectionPathText(childPath))
		}
		value, present := values[name]
		if !present || value == nil {
			if _, isRequired := required[name]; isRequired {
				return nil, projectionErrorf("required state path missing: %s", projectionPathText(childPath))
			}
			continue
		}
		output[name] = cloneProjectionValue(value)
	}
	blocks, err := metadata.TerraformInputBlockTypes(options.Block, options.Label)
	if err != nil {
		return nil, err
	}
	for _, name := range canonjson.SortedStrings(adoptMapKeys(blocks)) {
		blockType := blocks[name]
		childPath := append(append([]any(nil), options.Path...), name)
		isRequired := requiredNestedBlock(blockType)
		if options.Policy != nil && options.Policy.ProjectionOmits(options.ResourceType, childPath) {
			if isRequired {
				return nil, projectionErrorf("policy cannot projection_omit required block %s", projectionPathText(childPath))
			}
			continue
		}
		value, present := values[name]
		if !present || value == nil {
			if isRequired {
				return nil, projectionErrorf("required state path missing: %s", projectionPathText(childPath))
			}
			continue
		}
		inner, err := metadata.TerraformRequireObject(blockType["block"], options.Label+".block_types."+name+".block")
		if err != nil {
			return nil, err
		}
		childMask := any(map[string]any{})
		if maskRecord, ok := projectionRecord(options.Mask); ok {
			childMask = maskRecord[name]
		}
		if childMask == true {
			return nil, projectionErrorf("sensitive input path %s cannot be written to generated tfvars without an explicit secret-handling policy", projectionPathText(childPath))
		}
		if metadata.TerraformBlockIsSingle(blockType) {
			single, exists, err := projectionSingleValue(value)
			if err != nil {
				return nil, err
			}
			if !exists {
				if isRequired {
					return nil, projectionErrorf("required state path missing: %s", projectionPathText(childPath))
				}
				continue
			}
			singleMask, err := projectionSingleSensitivity(childMask, childPath)
			if err != nil {
				return nil, err
			}
			projected, err := projectProviderBlock(projectBlockOptions{
				Block: inner, Label: options.Label + ".block_types." + name + ".block", Mask: singleMask,
				Path: childPath, Policy: options.Policy, ResourceType: options.ResourceType, Values: single,
			})
			if err != nil {
				return nil, err
			}
			output[name] = projected
			continue
		}
		array, ok := value.([]any)
		if !ok {
			return nil, projectionErrorf("state path %s is not a list", projectionPathText(childPath))
		}
		projected := make([]any, len(array))
		for index, member := range array {
			memberRecord, ok := projectionRecord(member)
			memberPath := append(append([]any(nil), childPath...), index)
			if !ok {
				return nil, projectionErrorf("state path %s is not an object", projectionPathText(memberPath))
			}
			projectedMember, err := projectProviderBlock(projectBlockOptions{
				Block: inner, Label: options.Label + ".block_types." + name + ".block",
				Mask: projectionListSensitivity(childMask, index), Path: memberPath,
				Policy: options.Policy, ResourceType: options.ResourceType, Values: memberRecord,
			})
			if err != nil {
				return nil, err
			}
			projected[index] = projectedMember
		}
		output[name] = projected
	}
	return output, nil
}

func projectionPathValue(value any, path []any) (any, bool) {
	current := value
	for _, rawSegment := range path {
		segment, ok := rawSegment.(string)
		if !ok {
			return nil, false
		}
		record, ok := projectionRecord(current)
		if !ok {
			return nil, false
		}
		selected, present := record[segment]
		if !present {
			return nil, false
		}
		current = selected
	}
	return current, true
}

func absentOrEmptyProjection(value any) bool {
	if value == nil {
		return true
	}
	if array, ok := value.([]any); ok {
		return len(array) == 0
	}
	if record, ok := projectionRecord(value); ok {
		return len(record) == 0
	}
	return false
}

func setProjectionPath(target map[string]any, path []any, value any) error {
	if len(path) == 0 {
		return projectionErrorf("cannot write projection path %s", adoptionJSONValue(path))
	}
	segments := make([]string, len(path))
	for index, raw := range path {
		segment, ok := raw.(string)
		if !ok {
			return projectionErrorf("cannot write projection path %s", adoptionJSONValue(path))
		}
		segments[index] = segment
	}
	current := target
	for _, segment := range segments[:len(segments)-1] {
		value, present := current[segment]
		if !present || value == nil {
			next := map[string]any{}
			current[segment] = next
			current = next
			continue
		}
		next, ok := projectionRecord(value)
		if !ok {
			return projectionErrorf("cannot projection_sync through non-object path %s", projectionPathText(path))
		}
		current = next
	}
	current[segments[len(segments)-1]] = cloneProjectionValue(value)
	return nil
}

func schemaTypeEncodingAt(encoding metadata.TerraformTypeEncoding, path []any) metadata.TerraformTypeEncoding {
	if len(path) == 0 {
		return encoding
	}
	switch typed := encoding.(type) {
	case metadata.TerraformPrimitiveType:
		return nil
	case metadata.TerraformCollectionType:
		if typed.Kind == "list" || typed.Kind == "set" {
			return schemaTypeEncodingAt(typed.Inner, stripCollection(path))
		}
		if typed.Kind == "map" {
			return schemaTypeEncodingAt(typed.Inner, path[1:])
		}
	case metadata.TerraformObjectType:
		segment, ok := path[0].(string)
		if ok {
			if inner, present := typed.Members[segment]; present {
				return schemaTypeEncodingAt(inner, path[1:])
			}
		}
	}
	return nil
}

func schemaTypeBlockAt(block metadata.JsonObject, path []any, label string, resourceTop bool) (metadata.TerraformTypeEncoding, error) {
	if len(path) == 0 {
		return nil, nil
	}
	segment, ok := path[0].(string)
	if !ok {
		return nil, nil
	}
	inputs, err := classifiedAttributes(block, label, resourceTop)
	if err != nil {
		return nil, err
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, label)
	if err != nil {
		return nil, err
	}
	if contains(inputs.Required, segment) || contains(inputs.Optional, segment) {
		attribute, err := metadata.TerraformRequireObject(attributes[segment], label+".attributes."+segment)
		if err != nil {
			return nil, err
		}
		encoding, err := metadata.TerraformAttributeType(attribute, label+".attributes."+segment)
		if err != nil {
			return nil, err
		}
		return schemaTypeEncodingAt(encoding, path[1:]), nil
	}
	blocks, err := metadata.TerraformInputBlockTypes(block, label)
	if err != nil {
		return nil, err
	}
	blockType, present := blocks[segment]
	if !present {
		return nil, nil
	}
	child, err := metadata.TerraformRequireObject(blockType["block"], label+".block_types."+segment+".block")
	if err != nil {
		return nil, err
	}
	return schemaTypeBlockAt(child, stripCollection(path[1:]), label+".block_types."+segment+".block", false)
}

func guardProjectionSyncPath(block metadata.JsonObject, path []any, label string, resourceTop bool, field, rawPath, resourceType string) error {
	if len(path) <= 1 {
		return nil
	}
	segment, ok := path[0].(string)
	if !ok {
		return nil
	}
	inputs, err := classifiedAttributes(block, label, resourceTop)
	if err != nil {
		return err
	}
	attributes, err := metadata.TerraformAttributesForBlock(block, label)
	if err != nil {
		return err
	}
	if contains(inputs.Required, segment) || contains(inputs.Optional, segment) {
		attribute, err := metadata.TerraformRequireObject(attributes[segment], label+".attributes."+segment)
		if err != nil {
			return err
		}
		encoding, err := metadata.TerraformAttributeType(attribute, label+".attributes."+segment)
		if err != nil {
			return err
		}
		rest := path[1:]
		for len(rest) > 0 {
			switch typed := encoding.(type) {
			case metadata.TerraformPrimitiveType:
				return nil
			case metadata.TerraformCollectionType:
				if typed.Kind == "list" || typed.Kind == "set" {
					return projectionErrorf("refusing to projection_sync %s %s of %s: non-terminal segment %s is a %s-typed attribute, not an object-shaped container", field, rawPath, resourceType, segment, typed.Kind)
				}
				if typed.Kind == "map" {
					rest = rest[1:]
					encoding = typed.Inner
				}
			case metadata.TerraformObjectType:
				member, memberOK := rest[0].(string)
				inner, present := typed.Members[member]
				if !memberOK || !present {
					return nil
				}
				encoding = inner
				rest = rest[1:]
			}
		}
		return nil
	}
	blocks, err := metadata.TerraformInputBlockTypes(block, label)
	if err != nil {
		return err
	}
	if blockType, present := blocks[segment]; present {
		if !metadata.TerraformBlockIsSingle(blockType) {
			return projectionErrorf("refusing to projection_sync %s %s of %s: non-terminal segment %s is a repeated block, not an object-shaped container", field, rawPath, resourceType, segment)
		}
		child, err := metadata.TerraformRequireObject(blockType["block"], label+".block_types."+segment+".block")
		if err != nil {
			return err
		}
		return guardProjectionSyncPath(child, path[1:], label+".block_types."+segment+".block", false, field, rawPath, resourceType)
	}
	return nil
}

func applyProjectionSync(block metadata.JsonObject, output map[string]any, policy *metadata.DriftPolicy, resourceType string, schema metadata.JsonObject) error {
	for _, entry := range policy.Entries(resourceType, metadata.PolicyProjectionSync) {
		data := entry.Data()
		targetText := fmt.Sprint(data["target_path"])
		sourceText := fmt.Sprint(data["source_path"])
		target, err := metadata.ParsePolicyPath(targetText)
		if err != nil {
			return err
		}
		source, err := metadata.ParsePolicyPath(sourceText)
		if err != nil {
			return err
		}
		status, err := ProviderSchemaStatus(schema, resourceType, target, false)
		if err != nil {
			return err
		}
		if status != "required" && status != "optional" {
			return projectionErrorf("refusing to projection_sync target attribute %s of %s: not a writable input attribute", targetText, resourceType)
		}
		if err := guardProjectionSyncPath(block, target, resourceType, true, "target_path", targetText, resourceType); err != nil {
			return err
		}
		if err := guardProjectionSyncPath(block, source, resourceType, true, "source_path", sourceText, resourceType); err != nil {
			return err
		}
		targetType, err := schemaTypeBlockAt(block, target, resourceType, true)
		if err != nil {
			return err
		}
		sourceType, err := schemaTypeBlockAt(block, source, resourceType, true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(targetType, sourceType) {
			return projectionErrorf("refusing to projection_sync target %s from source %s of %s: schema types differ", targetText, sourceText, resourceType)
		}
		if targetValue, present := projectionPathValue(output, target); present && !absentOrEmptyProjection(targetValue) {
			continue
		}
		sourceValue, present := projectionPathValue(output, source)
		if !present || absentOrEmptyProjection(sourceValue) {
			continue
		}
		if err := setProjectionPath(output, target, sourceValue); err != nil {
			return err
		}
		policy.MarkMatched(entry)
	}
	return nil
}

func applyProjectionFill(output map[string]any, policy *metadata.DriftPolicy, rawItem map[string]any, resourceType string, schema metadata.JsonObject) error {
	entries := policy.Entries(resourceType, metadata.PolicyProjectionFill)
	if len(entries) > 0 && rawItem == nil {
		return projectionErrorf("%s projection_fill requires the raw API item", resourceType)
	}
	for _, entry := range entries {
		data := entry.Data()
		targetText, _ := data["path"].(string)
		target, err := metadata.ParsePolicyPath(targetText)
		if err != nil {
			return err
		}
		if _, present := projectionPathValue(output, target); present {
			continue
		}
		value, present, err := ProjectionFillValue(entry, rawItem, resourceType, schema)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if err := setProjectionPath(output, target, value); err != nil {
			return err
		}
		policy.MarkMatched(entry)
	}
	return nil
}

func projectionLeaf(value any) bool {
	if _, ok := value.([]any); ok {
		return false
	}
	_, record := projectionRecord(value)
	return !record
}

func removeMatchingProjectionLeaves(value any, selector []any, values []any, path []any, ignoreCollectionIndexes bool, equality func(any, any) bool) int {
	_, removed := removeMatchingProjectionLeavesValue(value, selector, values, path, ignoreCollectionIndexes, equality)
	return removed
}

func removeMatchingProjectionLeavesValue(value any, selector []any, values []any, path []any, ignoreCollectionIndexes bool, equality func(any, any) bool) (any, int) {
	removed := 0
	if record, ok := projectionRecord(value); ok {
		for _, key := range canonjson.SortedStrings(adoptMapKeys(record)) {
			child := record[key]
			childPath := append(append([]any(nil), path...), key)
			matchPath := childPath
			if ignoreCollectionIndexes {
				matchPath = removeProjectionIndexes(childPath)
			}
			if projectionLeaf(child) && metadata.PolicySelectorMatches(selector, matchPath) && projectionAnyEqual(child, values, equality) {
				delete(record, key)
				removed++
			} else {
				updated, childRemoved := removeMatchingProjectionLeavesValue(child, selector, values, childPath, ignoreCollectionIndexes, equality)
				record[key] = updated
				removed += childRemoved
			}
		}
	} else if array, ok := value.([]any); ok {
		for index := len(array) - 1; index >= 0; index-- {
			child := array[index]
			childPath := append(append([]any(nil), path...), int64(index))
			matchPath := childPath
			if ignoreCollectionIndexes {
				matchPath = removeProjectionIndexes(childPath)
			}
			if projectionLeaf(child) && metadata.PolicySelectorMatches(selector, matchPath) && projectionAnyEqual(child, values, equality) {
				array = append(array[:index], array[index+1:]...)
				removed++
			} else {
				updated, childRemoved := removeMatchingProjectionLeavesValue(child, selector, values, childPath, ignoreCollectionIndexes, equality)
				array[index] = updated
				removed += childRemoved
			}
		}
		value = array
	}
	return value, removed
}

func removeProjectionIndexes(path []any) []any {
	output := make([]any, 0, len(path))
	for _, segment := range path {
		switch segment.(type) {
		case int, int64:
			continue
		}
		output = append(output, segment)
	}
	return output
}

func projectionAnyEqual(value any, candidates []any, equality func(any, any) bool) bool {
	for _, candidate := range candidates {
		if equality(value, candidate) {
			return true
		}
	}
	return false
}

func applyPackDropIfDefault(output map[string]any, resourceType string, root *metadata.LoadedPackRoot, schema metadata.JsonObject) error {
	resource, ok := root.Resources[resourceType]
	if !ok {
		return fmt.Errorf("unknown resource %s", resourceType)
	}
	dropDefaults, _ := resource.Override["drop_if_default"].(map[string]any)
	for _, pathText := range canonjson.SortedStrings(adoptMapKeys(dropDefaults)) {
		selector, err := metadata.ParsePolicyPath(pathText)
		if err != nil {
			return err
		}
		status, err := ProviderSchemaStatus(schema, resourceType, selector, false)
		if err != nil {
			return err
		}
		if status != "optional" {
			return projectionErrorf("%s pack drop_if_default path %s is not optional (schema status %s)", resourceType, pathText, status)
		}
		removeMatchingProjectionLeaves(output, selector, []any{dropDefaults[pathText]}, nil, true, transform.MatchesTransformDefault)
	}
	return nil
}

func applyProjectionOmitIf(output map[string]any, policy *metadata.DriftPolicy, resourceType string, schema metadata.JsonObject) error {
	for _, entry := range policy.Entries(resourceType, metadata.PolicyProjectionOmitIf) {
		data := entry.Data()
		pathText, _ := data["path"].(string)
		selector, err := metadata.ParsePolicyPath(pathText)
		if err != nil {
			return err
		}
		status, err := ProviderSchemaStatus(schema, resourceType, selector, false)
		if err != nil {
			return err
		}
		if status == "required" {
			return projectionErrorf("refusing to conditionally omit required attribute %s of %s", pathText, resourceType)
		}
		values, _ := data["values"].([]any)
		if removeMatchingProjectionLeaves(output, selector, values, nil, false, canonjson.TerraformJSONEqual) > 0 {
			policy.MarkMatched(entry)
		}
	}
	return nil
}

// ProjectProviderState projects one provider-observed resource state object
// into module input shape, porting projectProviderState. The operation order
// is schema projection, sync, fill, pack defaults, then conditional omit.
func ProjectProviderState(options ProjectProviderStateOptions) (map[string]any, error) {
	if options.Root == nil {
		return nil, fmt.Errorf("provider-state projection requires a pack root")
	}
	mask := options.SensitiveValues
	if mask == nil {
		mask = map[string]any{}
	}
	if err := ValidateSensitiveMaskShape(mask, options.StateValues); err != nil {
		return nil, err
	}
	schema, err := options.Root.LoadResourceSchema(options.ResourceType)
	if err != nil {
		return nil, err
	}
	block, err := metadata.TerraformBlockForSchema(schema, options.ResourceType)
	if err != nil {
		return nil, err
	}
	output, err := projectProviderBlock(projectBlockOptions{
		Block: block, Label: options.ResourceType, Mask: mask, Path: []any{}, Policy: options.Policy,
		ResourceTop: true, ResourceType: options.ResourceType, Values: options.StateValues,
	})
	if err != nil {
		return nil, err
	}
	if options.Policy != nil {
		if err := applyProjectionSync(block, output, options.Policy, options.ResourceType, schema); err != nil {
			return nil, err
		}
		if err := applyProjectionFill(output, options.Policy, options.RawItem, options.ResourceType, schema); err != nil {
			return nil, err
		}
	}
	if err := applyPackDropIfDefault(output, options.ResourceType, options.Root, schema); err != nil {
		return nil, err
	}
	if options.Policy != nil {
		if err := applyProjectionOmitIf(output, options.Policy, options.ResourceType, schema); err != nil {
			return nil, err
		}
	}
	return output, nil
}
