package reconcile

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

// Object is a JSON object accepted by this package.
type Object = map[string]any

// APIMetadata maps normalized API field paths to their metadata objects.
// It ports ApiMetadata from the original implementation.
type APIMetadata = map[string]Object

// ReconciliationBucket identifies one of the twelve report classifications
// from the original implementation.
type ReconciliationBucket string

const (
	// BucketKept records values directly accepted as Terraform input.
	BucketKept ReconciliationBucket = "kept"
	// BucketRenamed records source fields renamed by an override.
	BucketRenamed ReconciliationBucket = "renamed"
	// BucketTransformed records values requiring an authoring transformation.
	BucketTransformed ReconciliationBucket = "transformed"
	// BucketDefaulted records fields supplied by an override default.
	BucketDefaulted ReconciliationBucket = "defaulted"
	// BucketRelationship records relationship objects mapped to *_id inputs.
	BucketRelationship ReconciliationBucket = "relationship"
	// BucketDroppedDefault records values removed by drop_if_default.
	BucketDroppedDefault ReconciliationBucket = "dropped_default"
	// BucketDroppedOverride records values removed by drops.
	BucketDroppedOverride ReconciliationBucket = "dropped_override"
	// BucketDroppedAcknowledged records intentional acknowledged drops.
	BucketDroppedAcknowledged ReconciliationBucket = "dropped_acknowledged"
	// BucketDroppedKnown records known computed, read-only, or empty values.
	BucketDroppedKnown ReconciliationBucket = "dropped_known"
	// BucketUnknown records values requiring authoring review.
	BucketUnknown ReconciliationBucket = "unknown"
	// BucketShapeMismatch records values incompatible with the Terraform shape.
	BucketShapeMismatch ReconciliationBucket = "shape_mismatch"
	// BucketSkipped records source items excluded by skip_if rules.
	BucketSkipped ReconciliationBucket = "skipped"
)

var reconciliationBuckets = [...]ReconciliationBucket{
	BucketKept, BucketRenamed, BucketTransformed, BucketDefaulted, BucketRelationship,
	BucketDroppedDefault, BucketDroppedOverride, BucketDroppedAcknowledged,
	BucketDroppedKnown, BucketUnknown, BucketShapeMismatch, BucketSkipped,
}

var transformKeys = [...]string{
	"split_csv", "sort_lists", "references", "divide", "invert_bool", "value_map", "strip_prefix", "html_escape_fields",
}

var readOnlyNames = map[string]struct{}{
	"_depth": {}, "children": {}, "created": {}, "display": {}, "display_url": {}, "last_updated": {},
	"owner": {}, "tagged_items": {}, "url": {},
}

// Buckets returns the twelve reconciliation buckets in the authority's
// stable order. The returned slice is detached from package state.
func Buckets() []ReconciliationBucket {
	return append([]ReconciliationBucket(nil), reconciliationBuckets[:]...)
}

// ProviderSchemaFromTerraformDump selects the provider schema containing
// resourceType from a Terraform provider-schema dump. It ports
// providerSchemaFromTerraformDump from the original implementation.
// A nil providerSource permits unqualified discovery. A non-nil providerSource,
// including an explicit empty string, requires an exact source key or an
// unambiguous trailing "/providerSource" match.
func ProviderSchemaFromTerraformDump(data Object, resourceType string, providerSource *string) (Object, error) {
	providers, _ := data["provider_schemas"].(map[string]any)
	if providerSource != nil {
		sourceName := *providerSource
		provider, ok := providers[sourceName].(map[string]any)
		if !ok {
			var matches []Object
			for source, candidate := range providers {
				if strings.HasSuffix(source, "/"+sourceName) {
					if schema, schemaOK := candidate.(map[string]any); schemaOK {
						matches = append(matches, schema)
					}
				}
			}
			if len(matches) == 1 {
				provider = matches[0]
			}
		}
		if provider == nil {
			return nil, fmt.Errorf("provider source %q not found in Terraform schema", sourceName)
		}
		return cloneObject(provider), nil
	}

	matches := make([]Object, 0)
	for _, candidate := range providers {
		provider, ok := candidate.(map[string]any)
		if !ok {
			continue
		}
		resources, ok := provider["resource_schemas"].(map[string]any)
		if !ok {
			continue
		}
		if _, found := resources[resourceType]; found {
			matches = append(matches, provider)
		}
	}
	switch len(matches) {
	case 1:
		return cloneObject(matches[0]), nil
	case 0:
		return nil, fmt.Errorf("resource type %q not found in Terraform schema", resourceType)
	default:
		return nil, fmt.Errorf("resource type %q appears in multiple provider schemas; pass providerSource", resourceType)
	}
}

// ResourceSchemaFromData selects resourceType from either a direct
// resource_schemas object or a full provider-schema dump. It ports
// resourceSchemaFromData from the original implementation.
func ResourceSchemaFromData(data Object, resourceType string, providerSource *string) (Object, error) {
	var schemas Object
	if direct, ok := data["resource_schemas"].(map[string]any); ok {
		schemas = direct
	} else if _, ok := data["provider_schemas"].(map[string]any); ok {
		provider, err := ProviderSchemaFromTerraformDump(data, resourceType, providerSource)
		if err != nil {
			return nil, err
		}
		var present bool
		schemas, present = provider["resource_schemas"].(map[string]any)
		if !present {
			schemas = Object{}
		}
	} else {
		return nil, fmt.Errorf("provider schema data must contain resource_schemas or provider_schemas")
	}
	schema, ok := schemas[resourceType].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resource type %q must be a JSON object", resourceType)
	}
	return cloneObject(schema), nil
}

// APIItemsFrom converts an API object, list of objects, or NetBox-style
// {results:[...]} envelope into detached object items. It ports apiItemsFrom
// from the original implementation.
func APIItemsFrom(value any, source string) ([]Object, error) {
	if source == "" {
		source = "<api>"
	}
	if list, ok := value.([]any); ok {
		items := make([]Object, len(list))
		for index, item := range list {
			object, objectOK := item.(map[string]any)
			if !objectOK {
				return nil, fmt.Errorf("%s[%d] must be a JSON object", source, index)
			}
			items[index] = cloneObject(object)
		}
		return items, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object, list, or NetBox-style {results:[...]} wrapper", source)
	}
	if results, hasResults := object["results"].([]any); hasResults {
		return APIItemsFrom(results, source+".results")
	}
	return []Object{cloneObject(object)}, nil
}

// APIMetadataFromOptions derives normalized field metadata from a DRF OPTIONS
// response. It ports apiMetadataFromOptions from
// the original implementation.
func APIMetadataFromOptions(value any, source string) (metadata APIMetadata, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			metadata = nil
			err = fmt.Errorf("reconcile input: %v", recovered)
		}
	}()
	if source == "" {
		source = "<options>"
	}
	options, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", source)
	}
	actions, _ := options["actions"].(map[string]any)
	fields := APIMetadata{}
	for _, method := range [...]string{"POST", "PUT", "PATCH"} {
		action, ok := actions[method].(map[string]any)
		if !ok {
			continue
		}
		for _, name := range canonjson.SortedStrings(objectKeys(action)) {
			meta, ok := action[name].(map[string]any)
			if !ok {
				continue
			}
			fieldPath := joinPath(joinPath(joinPath(source, "actions"), method), name)
			if validationErr := validateAuthoringValue(meta, fieldPath); validationErr != nil {
				return nil, validationErr
			}
			key := transform.SnakeName(name)
			merged := cloneObject(fields[key])
			methods := stringList(merged["methods"])
			if !containsString(methods, method) {
				methods = append(methods, method)
			}
			snaked, snakeOK := transform.SnakeJSONKeysForAuthoring(meta).(map[string]any)
			if !snakeOK {
				return nil, fmt.Errorf("%s.actions.%s.%s must be a JSON object", source, method, name)
			}
			for key, entry := range snaked {
				merged[key] = entry
			}
			merged["methods"] = toAnySlice(methods)
			if readOnly, _ := merged["read_only"].(bool); !readOnly {
				merged["writable"] = true
			}
			fields[key] = merged
		}
	}
	return cloneMetadata(fields), nil
}

// MergeAPIMetadata combines OPTIONS-derived and already-normalized metadata
// in their supplied order. It is the non-OpenAPI portion of mergeApiMetadata
// from the original implementation.
func MergeAPIMetadata(optionDocuments []any, normalized ...APIMetadata) (metadata APIMetadata, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			metadata = nil
			err = fmt.Errorf("reconcile input: %v", recovered)
		}
	}()
	merged := APIMetadata{}
	for _, document := range optionDocuments {
		fields, fieldsErr := APIMetadataFromOptions(document, "<options>")
		if fieldsErr != nil {
			return nil, fieldsErr
		}
		for path, field := range fields {
			merged[path] = cloneObject(field)
		}
	}
	for _, fields := range normalized {
		for path, field := range fields {
			merged[path] = cloneObject(field)
		}
	}
	return cloneMetadata(merged), nil
}

// ReconciliationFieldAlias resolves a source key against Terraform input and
// computed names. It ports reconciliationFieldAlias from
// the original implementation.
func ReconciliationFieldAlias(key string, keep, computed map[string]struct{}) (alias, kind, reason string, found bool) {
	candidates := make([][2]string, 0, 6)
	if mapped, ok := map[string]string{"address": "ip_address", "color": "color_hex", "face": "rack_face", "time_zone": "timezone"}[key]; ok {
		candidates = append(candidates, [2]string{mapped, "field_alias"})
	}
	candidates = append(candidates, [2]string{key + "_id", "relationship_id"}, [2]string{key + "_ids", "relationship_ids"}, [2]string{"rack_" + key, "field_alias"})
	if strings.HasPrefix(key, "vc_") {
		candidates = append(candidates, [2]string{"virtual_chassis_" + key[3:], "field_alias"})
	}
	if strings.HasSuffix(key, "4") {
		candidates = append(candidates, [2]string{key[:len(key)-1] + "v4", "field_alias"})
	}
	if strings.HasSuffix(key, "6") {
		candidates = append(candidates, [2]string{key[:len(key)-1] + "v6", "field_alias"})
	}
	for _, candidate := range candidates {
		if _, ok := keep[candidate[0]]; ok {
			return candidate[0], "input", candidate[1], true
		}
		if _, ok := computed[candidate[0]]; ok {
			return candidate[0], "computed", candidate[1], true
		}
	}
	return "", "", "", false
}

// ReconcileOptions supplies the dependency-free reconciliation kernel's
// complete in-memory inputs. It ports reconcileItems's options from
// the original implementation.
type ReconcileOptions struct {
	// ResourceType is the Terraform resource type used in report output.
	ResourceType string
	// Items contains the API objects to reconcile.
	Items []any
	// ResourceSchema is the in-memory Terraform resource schema.
	ResourceSchema Object
	// Override is the optional authoring override object.
	Override Object
	// APIMetadata is optional normalized API field metadata.
	APIMetadata APIMetadata
}

// ReconciliationReport accumulates the private reconciliation state. Its
// query methods return detached values, so callers cannot mutate later reads.
type ReconciliationReport struct {
	resourceType string
	itemCount    int
	buckets      map[ReconciliationBucket]map[string]*reportEntry
}

type reportEntry struct {
	count   int
	path    string
	reasons map[string]int
	types   map[string]int
}

// ResourceType returns the report's resource type.
func (r *ReconciliationReport) ResourceType() string { return r.resourceType }

// ItemCount returns the number of supplied API items, including skipped items.
func (r *ReconciliationReport) ItemCount() int { return r.itemCount }

// HasUnknowns reports whether the report has unknown or shape-mismatch paths.
func (r *ReconciliationReport) HasUnknowns() bool {
	return len(r.buckets[BucketUnknown]) > 0 || len(r.buckets[BucketShapeMismatch]) > 0
}

// Paths returns detached report entries for bucket in stable code-point path
// order. An unknown bucket returns an empty slice.
func (r *ReconciliationReport) Paths(bucket ReconciliationBucket) []Object {
	entries := r.buckets[bucket]
	paths := canonjson.SortedStrings(objectKeys(entries))
	result := make([]Object, 0, len(paths))
	for _, path := range paths {
		entry := entries[path]
		result = append(result, Object{
			"count":   float64(entry.count),
			"path":    entry.path,
			"reasons": countsObject(entry.reasons),
			"types":   countsObject(entry.types),
		})
	}
	return result
}

// AsMap returns the complete report in the frozen authority's object shape.
// The returned map and every nested map or slice are detached from report state.
func (r *ReconciliationReport) AsMap() Object {
	paths := Object{}
	observations := Object{}
	uniquePaths := Object{}
	for _, bucket := range reconciliationBuckets {
		entries := r.Paths(bucket)
		paths[string(bucket)] = objectsToAny(entries)
		count := 0
		for _, entry := range entries {
			count += int(entry["count"].(float64))
		}
		observations[string(bucket)] = float64(count)
		uniquePaths[string(bucket)] = float64(len(entries))
	}
	droppedKnown := r.Paths(BucketDroppedKnown)
	providerGaps := []string{}
	for _, entry := range r.Paths(BucketUnknown) {
		reasons := entry["reasons"].(Object)
		if _, required := reasons["api_required_not_in_provider"]; required {
			providerGaps = append(providerGaps, entry["path"].(string))
			continue
		}
		if _, writable := reasons["api_writable_not_in_provider"]; writable {
			providerGaps = append(providerGaps, entry["path"].(string))
		}
	}
	reviewUnknown := make([]string, 0, len(r.buckets[BucketUnknown])+len(r.buckets[BucketShapeMismatch]))
	for _, bucket := range [...]ReconciliationBucket{BucketUnknown, BucketShapeMismatch} {
		for _, entry := range r.Paths(bucket) {
			reviewUnknown = append(reviewUnknown, entry["path"].(string))
		}
	}
	return Object{
		"items":         float64(r.itemCount),
		"paths":         paths,
		"resource_type": r.resourceType,
		"suggestions": Object{
			"acknowledged_drops": pathsFromEntries(droppedKnown),
			"provider_gaps":      toAnySlice(canonjson.SortedStrings(providerGaps)),
			"review_unknown":     toAnySlice(canonjson.SortedStrings(reviewUnknown)),
		},
		"summary": Object{"observations": observations, "unique_paths": uniquePaths},
	}
}

// ReconcileItems reconciles in-memory API objects against a Terraform resource
// schema. It ports reconcileItems from the original implementation
// and converts malformed inputs or downstream transform panics into errors.
func ReconcileItems(options ReconcileOptions) (report *ReconciliationReport, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			report = nil
			err = fmt.Errorf("reconcile input: %v", recovered)
		}
	}()
	override := options.Override
	if override == nil {
		override = Object{}
	}
	block, blockErr := metadata.TerraformBlockForSchema(metadata.JsonObject(options.ResourceSchema), options.ResourceType)
	if blockErr != nil {
		return nil, blockErr
	}
	report = newReport(options.ResourceType)
	for index, rawValue := range options.Items {
		report.itemCount++
		raw, ok := rawValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("items[%d] must be a JSON object", index)
		}
		if validationErr := validateAuthoringValue(raw, fmt.Sprintf("items[%d]", index)); validationErr != nil {
			return nil, validationErr
		}
		snakeRaw, snakeOK := transform.SnakeJSONKeysForAuthoring(raw).(map[string]any)
		if !snakeOK {
			return nil, fmt.Errorf("items[%d] must be a JSON object", index)
		}
		if reason, skipped := transform.TransformSkipMatchReason(snakeRaw, override, options.ResourceType); skipped {
			report.add(BucketSkipped, "$item", reason, firstPresent(snakeRaw, "name", "id"))
			continue
		}
		normalized := transform.ApplyTransformOverridesForAuthoring(snakeRaw, override, options.ResourceType)
		recordOverrideActions(report, snakeRaw, normalized, override)
		if walkErr := walkBlock(report, "", normalized, block, override, true, options.APIMetadata); walkErr != nil {
			return nil, walkErr
		}
	}
	return report, nil
}

func newReport(resourceType string) *ReconciliationReport {
	buckets := make(map[ReconciliationBucket]map[string]*reportEntry, len(reconciliationBuckets))
	for _, bucket := range reconciliationBuckets {
		buckets[bucket] = map[string]*reportEntry{}
	}
	return &ReconciliationReport{resourceType: resourceType, buckets: buckets}
}

func (r *ReconciliationReport) add(bucket ReconciliationBucket, path, reason string, value any) {
	entry := r.buckets[bucket][path]
	if entry == nil {
		entry = &reportEntry{path: path, reasons: map[string]int{}, types: map[string]int{}}
		r.buckets[bucket][path] = entry
	}
	entry.count++
	entry.reasons[reason]++
	entry.types[jsonTypeName(value)]++
}

func walkBlock(report *ReconciliationReport, prefix string, value any, block metadata.JsonObject, override Object, resourceTop bool, apiMetadata APIMetadata) error {
	object, ok := value.(map[string]any)
	if !ok {
		path := prefix
		if path == "" {
			path = "$item"
		}
		report.add(BucketShapeMismatch, path, "expected_object", value)
		return nil
	}
	var classified metadata.TerraformClassifiedAttributes
	var err error
	if resourceTop {
		classified, err = metadata.TerraformResourceInputAttributes(block, "resource.block")
	} else {
		classified, err = metadata.TerraformClassifyAttributes(block, pathOr(prefix, "block"))
	}
	if err != nil {
		return err
	}
	keep, computed := stringsToSet(classified.Required, classified.Optional), stringsToSet(classified.ComputedOnly)
	attributes, err := metadata.TerraformAttributesForBlock(block, pathOr(prefix, "block"))
	if err != nil {
		return err
	}
	blockTypes, err := metadata.TerraformBlockTypesForBlock(block, pathOr(prefix, "block"))
	if err != nil {
		return err
	}
	inputBlocks, err := metadata.TerraformInputBlockTypes(block, pathOr(prefix, "block"))
	if err != nil {
		return err
	}
	for _, key := range canonjson.SortedStrings(objectKeys(object)) {
		child, childValue := joinPath(prefix, key), object[key]
		if _, input := keep[key]; input {
			attribute, requireErr := metadata.TerraformRequireObject(attributes[key], child+".attribute")
			if requireErr != nil {
				return requireErr
			}
			encoding, typeErr := metadata.TerraformAttributeType(attribute, child)
			if typeErr != nil {
				return typeErr
			}
			if walkErr := markOrWalkAttribute(report, child, childValue, encoding, override, apiMetadata); walkErr != nil {
				return walkErr
			}
			continue
		}
		if _, isComputed := computed[key]; isComputed || attributes[key] != nil {
			if bucket, reason, found := overrideBucket(override, child, childValue, true); found {
				addLeaves(report, bucket, child, childValue, reason)
			} else {
				addLeaves(report, BucketDroppedKnown, child, childValue, "computed_only_attribute")
			}
			continue
		}
		if _, input := inputBlocks[key]; input {
			if bucket, reason, found := overrideBucket(override, child, childValue, false); found {
				addLeaves(report, bucket, child, childValue, reason)
				continue
			}
			blockType, requireErr := metadata.TerraformRequireObject(blockTypes[key], child)
			if requireErr != nil {
				return requireErr
			}
			if walkErr := walkBlockValue(report, child, childValue, blockType, override, apiMetadata); walkErr != nil {
				return walkErr
			}
			continue
		}
		if _, knownBlock := blockTypes[key]; knownBlock {
			if bucket, reason, found := overrideBucket(override, child, childValue, true); found {
				addLeaves(report, bucket, child, childValue, reason)
			} else {
				addLeaves(report, BucketDroppedKnown, child, childValue, "non_input_block")
			}
			continue
		}
		if bucket, reason, found := overrideBucket(override, child, childValue, true); found {
			addLeaves(report, bucket, child, childValue, reason)
		} else if isReadOnlyPath(child) {
			addLeaves(report, BucketDroppedKnown, child, childValue, "common_read_only")
		} else if alias, kind, reason, found := ReconciliationFieldAlias(key, keep, computed); found && kind == "input" && strings.HasPrefix(reason, "relationship") && isRelationshipValue(childValue) {
			report.add(BucketRelationship, child, "relationship_id:"+alias, childValue)
		} else if found && kind == "input" {
			report.add(BucketTransformed, child, reason+":"+alias, childValue)
		} else if found && kind == "computed" {
			addLeaves(report, BucketDroppedKnown, child, childValue, "computed_alias:"+alias)
		} else {
			markUnknownOrAPIKnown(report, child, childValue, apiMetadata, "no_schema_input_or_override")
		}
	}
	return nil
}

func markOrWalkAttribute(report *ReconciliationReport, path string, value any, encoding metadata.TerraformTypeEncoding, override Object, apiMetadata APIMetadata) error {
	if bucket, reason, found := overrideBucket(override, path, value, false); found {
		addLeaves(report, bucket, path, value, reason)
		return nil
	}
	return walkAttribute(report, path, value, encoding, override, apiMetadata)
}

func walkAttribute(report *ReconciliationReport, path string, value any, encoding metadata.TerraformTypeEncoding, override Object, apiMetadata APIMetadata) error {
	switch typed := encoding.(type) {
	case metadata.TerraformPrimitiveType:
		if choiceObject(value) {
			report.add(BucketTransformed, path, "choice_value", value)
		} else if primitiveMatches(value, string(typed)) {
			report.add(BucketKept, path, "terraform_input", value)
		} else if reason := primitiveTransformReason(value, typed); reason != "" {
			report.add(BucketTransformed, path, reason, value)
		} else {
			report.add(BucketShapeMismatch, path, "expected_"+string(typed), value)
		}
		return nil
	case metadata.TerraformObjectType:
		return walkObjectMembers(report, path, value, typed.Members, override, apiMetadata)
	case metadata.TerraformCollectionType:
		switch typed.Kind {
		case "map":
			if _, ok := value.(map[string]any); ok {
				report.add(BucketKept, path, "terraform_input_map", value)
			} else {
				report.add(BucketShapeMismatch, path, "expected_map", value)
			}
		case "list", "set":
			list, ok := value.([]any)
			if !ok {
				report.add(BucketShapeMismatch, path, "expected_"+typed.Kind, value)
				return nil
			}
			if primitive, primitiveOK := typed.Inner.(metadata.TerraformPrimitiveType); primitiveOK && anyObject(list) {
				convertible := everyObjectHasOne(list, "slug", "name", "id")
				reason := "expected_" + typed.Kind + "_of_" + string(primitive)
				bucket := BucketShapeMismatch
				if convertible {
					bucket, reason = BucketTransformed, "object_list_to_"+typed.Kind+"_"+string(primitive)
				}
				report.add(bucket, path, reason, value)
				return nil
			}
			if object, objectOK := typed.Inner.(metadata.TerraformObjectType); objectOK {
				for _, entry := range list {
					if err := walkObjectMembers(report, path+"[]", entry, object.Members, override, apiMetadata); err != nil {
						return err
					}
				}
				if len(list) == 0 {
					report.add(BucketKept, path, "terraform_input_empty_"+typed.Kind, value)
				}
				return nil
			}
			report.add(BucketKept, path, "terraform_input_"+typed.Kind, value)
		default:
			report.add(BucketShapeMismatch, path, "unsupported_collection_kind", value)
		}
		return nil
	default:
		return fmt.Errorf("unsupported Terraform type at %s", path)
	}
}

func walkObjectMembers(report *ReconciliationReport, path string, value any, members map[string]metadata.TerraformTypeEncoding, override Object, apiMetadata APIMetadata) error {
	object, ok := value.(map[string]any)
	if !ok {
		report.add(BucketShapeMismatch, path, "expected_object", value)
		return nil
	}
	keys := canonjson.SortedStrings(objectKeys(object))
	if len(keys) == 0 {
		report.add(BucketKept, path, "terraform_input_empty_object", value)
	}
	for _, key := range keys {
		child := joinPath(path, key)
		if encoding, declared := members[key]; declared {
			if err := markOrWalkAttribute(report, child, object[key], encoding, override, apiMetadata); err != nil {
				return err
			}
			continue
		}
		if bucket, reason, found := overrideBucket(override, child, object[key], true); found {
			addLeaves(report, bucket, child, object[key], reason)
		} else {
			markUnknownOrAPIKnown(report, child, object[key], apiMetadata, "undeclared_object_member")
		}
	}
	return nil
}

func walkBlockValue(report *ReconciliationReport, path string, value any, blockType metadata.JsonObject, override Object, apiMetadata APIMetadata) error {
	block, err := metadata.TerraformRequireObject(blockType["block"], path+".block")
	if err != nil {
		return err
	}
	if metadata.TerraformBlockIsSingle(blockType) {
		if _, ok := value.(map[string]any); ok {
			return walkBlock(report, path, value, block, override, false, apiMetadata)
		}
		if list, ok := value.([]any); ok && allObjects(list) {
			for _, entry := range list {
				if err := walkBlock(report, path, entry, block, override, false, apiMetadata); err != nil {
					return err
				}
			}
			return nil
		}
		report.add(BucketShapeMismatch, path, "expected_single_block", value)
		return nil
	}
	if list, ok := value.([]any); ok {
		for _, entry := range list {
			if _, object := entry.(map[string]any); object {
				if err := walkBlock(report, path+"[]", entry, block, override, false, apiMetadata); err != nil {
					return err
				}
			} else {
				report.add(BucketShapeMismatch, path+"[]", "expected_block_object", entry)
			}
		}
		return nil
	}
	if _, ok := value.(map[string]any); ok {
		return walkBlock(report, path+"[]", value, block, override, false, apiMetadata)
	}
	report.add(BucketShapeMismatch, path, "expected_block_list", value)
	return nil
}

func recordOverrideActions(report *ReconciliationReport, raw, normalized, override Object) {
	if renames, ok := override["renames"].(map[string]any); ok {
		for _, oldName := range canonjson.SortedStrings(objectKeys(renames)) {
			if target, stringTarget := renames[oldName].(string); stringTarget {
				if value, present := raw[oldName]; present {
					report.add(BucketRenamed, oldName, "renamed_to:"+target, value)
				}
			}
		}
	}
	for _, name := range transformKeys {
		configured := override[name]
		fields := stringList(configured)
		if object, ok := configured.(map[string]any); ok {
			fields = objectKeys(object)
		}
		for _, field := range canonjson.SortedStrings(fields) {
			if value, found := raw[field]; found {
				report.add(BucketTransformed, field, name, value)
			}
		}
	}
	for _, field := range canonjson.SortedStrings(stringList(override["drops"])) {
		if !strings.Contains(field, ".") {
			if value, found := raw[field]; found {
				report.add(BucketDroppedOverride, field, "override_drop", value)
			}
		}
	}
	if defaults, ok := override["drop_if_default"].(map[string]any); ok {
		for _, field := range canonjson.SortedStrings(objectKeys(defaults)) {
			if value, found := raw[field]; found && !strings.Contains(field, ".") && transform.TransformValueMatchesDefaultForAuthoring(value, defaults[field]) {
				report.add(BucketDroppedDefault, field, "drop_if_default", value)
			}
		}
	}
	if defaults, ok := override["defaults"].(map[string]any); ok {
		for _, field := range canonjson.SortedStrings(objectKeys(defaults)) {
			if _, rawHas := raw[field]; !rawHas {
				if value, normalizedHas := normalized[field]; normalizedHas {
					report.add(BucketDefaulted, field, "override_default", value)
				}
			}
		}
	}
}

func overrideBucket(override Object, path string, value any, acknowledged bool) (ReconciliationBucket, string, bool) {
	if containsPath(stringSet(stringList(override["drops"])), path) {
		return BucketDroppedOverride, "override_drop", true
	}
	if defaults, ok := override["drop_if_default"].(map[string]any); ok {
		if defaultValue, found := mappingValue(defaults, path); found && transform.TransformValueMatchesDefaultForAuthoring(value, defaultValue) {
			return BucketDroppedDefault, "drop_if_default", true
		}
	}
	if acknowledged && containsPath(stringSet(stringList(override["acknowledged_drops"])), path) {
		return BucketDroppedAcknowledged, "acknowledged_drop", true
	}
	return "", "", false
}

func markUnknownOrAPIKnown(report *ReconciliationReport, path string, value any, apiMetadata APIMetadata, fallback string) {
	if dropAbsent(report, path, value) {
		return
	}
	meta, found := metadataForPath(apiMetadata, path)
	if readOnly, _ := meta["read_only"].(bool); found && readOnly {
		addLeaves(report, BucketDroppedKnown, path, value, "api_read_only")
	} else if responseOnly, _ := meta["response_only"].(bool); found && responseOnly {
		addLeaves(report, BucketDroppedKnown, path, value, "api_response_only")
	} else if writable, _ := meta["writable"].(bool); found && writable {
		reason := "api_writable_not_in_provider"
		if required, _ := meta["required"].(bool); required {
			reason = "api_required_not_in_provider"
		}
		addLeaves(report, BucketUnknown, path, value, reason)
	} else if found {
		addLeaves(report, BucketUnknown, path, value, "api_spec_observed_not_in_provider")
	} else if choiceObject(value) {
		report.add(BucketDroppedKnown, path, "read_only_choice_object", value)
	} else {
		addLeaves(report, BucketUnknown, path, value, fallback)
	}
}

func addLeaves(report *ReconciliationReport, bucket ReconciliationBucket, path string, value any, reason string) {
	if object, ok := value.(map[string]any); ok {
		keys := canonjson.SortedStrings(objectKeys(object))
		if len(keys) == 0 {
			report.add(bucket, path, reason, value)
		}
		for _, key := range keys {
			addLeaves(report, bucket, joinPath(path, key), object[key], reason)
		}
		return
	}
	if list, ok := value.([]any); ok {
		if len(list) == 0 {
			report.add(bucket, path, reason, value)
		} else if allObjects(list) {
			for _, entry := range list {
				addLeaves(report, bucket, path+"[]", entry, reason)
			}
		} else {
			report.add(bucket, path, reason, value)
		}
		return
	}
	report.add(bucket, path, reason, value)
}

func primitiveTransformReason(value any, primitive metadata.TerraformPrimitiveType) string {
	coerced := transform.CoerceTransformPrimitiveForAuthoring(value, primitive)
	if !canonjson.JSONEqual(coerced, value) && primitiveMatches(coerced, string(primitive)) {
		return "coerce_" + jsonTypeName(value) + "_to_" + string(primitive)
	}
	return ""
}

func primitiveMatches(value any, primitive string) bool {
	if value == nil {
		return true
	}
	switch primitive {
	case "string":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "number":
		switch number := value.(type) {
		case float64:
			return !math.IsInf(number, 0) && !math.IsNaN(number)
		case json.Number:
			return true
		}
		return false
	}
	return true
}

func jsonTypeName(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case json.Number:
		if strings.ContainsAny(string(typed), ".eE") {
			return "float"
		}
		return "int"
	case float64:
		if math.Trunc(typed) == typed {
			return "int"
		}
		return "float"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func isReadOnlyPath(path string) bool {
	leaf := path[strings.LastIndex(path, ".")+1:]
	if _, found := readOnlyNames[leaf]; found {
		return true
	}
	return strings.HasSuffix(leaf, "_count") || strings.HasSuffix(leaf, "_url")
}

func isRelationshipValue(value any) bool {
	if value == nil {
		return true
	}
	if object, ok := value.(map[string]any); ok {
		_, hasID := object["id"]
		return hasID
	}
	if list, ok := value.([]any); ok {
		for _, entry := range list {
			object, objectOK := entry.(map[string]any)
			if !objectOK {
				return false
			}
			if _, hasID := object["id"]; !hasID {
				return false
			}
		}
		return true
	}
	return false
}

func choiceObject(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	_, hasValue := object["value"]
	_, hasLabel := object["label"]
	return hasValue && hasLabel
}

func dropAbsent(report *ReconciliationReport, path string, value any) bool {
	reason := ""
	switch typed := value.(type) {
	case nil:
		reason = "null_non_schema_field"
	case string:
		if typed == "" {
			reason = "empty_non_schema_string"
		}
	case []any:
		if len(typed) == 0 {
			reason = "empty_non_schema_list"
		}
	case map[string]any:
		if len(typed) == 0 {
			reason = "empty_non_schema_object"
		}
	}
	if reason == "" {
		return false
	}
	report.add(BucketDroppedKnown, path, reason, value)
	return true
}

func metadataForPath(metadata APIMetadata, path string) (Object, bool) {
	for _, alias := range pathAliases(path) {
		if meta, found := metadata[alias]; found {
			return meta, true
		}
	}
	return nil, false
}

func mappingValue(mapping Object, path string) (any, bool) {
	for _, alias := range pathAliases(path) {
		if value, found := mapping[alias]; found {
			return value, true
		}
	}
	return nil, false
}

func pathAliases(path string) []string {
	without := strings.ReplaceAll(path, "[]", "")
	if without == path {
		return []string{path}
	}
	return []string{path, without}
}

func containsPath(paths map[string]struct{}, path string) bool {
	for _, alias := range pathAliases(path) {
		if _, found := paths[alias]; found {
			return true
		}
	}
	return false
}

func objectKeys[T any](object map[string]T) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	return keys
}

func stringsToSet(values ...[]string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, list := range values {
		for _, value := range list {
			set[value] = struct{}{}
		}
	}
	return set
}

func stringSet(values []string) map[string]struct{} { return stringsToSet(values) }

func stringList(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	strings := make([]string, 0, len(list))
	for _, entry := range list {
		if text, ok := entry.(string); ok {
			strings = append(strings, text)
		}
	}
	return strings
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}
func pathOr(path, fallback string) string {
	if path == "" {
		return fallback
	}
	return path
}
func firstPresent(object Object, names ...string) any {
	for _, name := range names {
		if value, found := object[name]; found && value != nil {
			return value
		}
	}
	return nil
}
func allObjects(values []any) bool {
	for _, value := range values {
		if _, ok := value.(map[string]any); !ok {
			return false
		}
	}
	return true
}
func anyObject(values []any) bool {
	for _, value := range values {
		if _, ok := value.(map[string]any); ok {
			return true
		}
	}
	return false
}
func everyObjectHasOne(values []any, names ...string) bool {
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		has := false
		for _, name := range names {
			if _, found := object[name]; found {
				has = true
				break
			}
		}
		if !has {
			return false
		}
	}
	return true
}
func toAnySlice(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
func objectsToAny(values []Object) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
func pathsFromEntries(entries []Object) []any {
	result := make([]any, len(entries))
	for index, entry := range entries {
		result[index] = entry["path"]
	}
	return result
}
func countsObject(counts map[string]int) Object {
	result := Object{}
	for key, count := range counts {
		result[key] = float64(count)
	}
	return result
}
func cloneObject(value Object) Object {
	result := Object{}
	for key, entry := range value {
		result[key] = cloneJSON(entry)
	}
	return result
}
func cloneMetadata(value APIMetadata) APIMetadata {
	result := APIMetadata{}
	for key, entry := range value {
		result[key] = cloneObject(entry)
	}
	return result
}
func cloneJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneObject(typed)
	case []any:
		result := make([]any, len(typed))
		for index, entry := range typed {
			result[index] = cloneJSON(entry)
		}
		return result
	default:
		return typed
	}
}

// validateAuthoringValue enforces the subset of authoring input that Go can
// represent without changing Node's normalization semantics. In particular,
// a Go map has no JSON encounter order, so colliding snake_case keys cannot
// safely choose the same winner that a Node object would choose.
func validateAuthoringValue(value any, path string) error {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return fmt.Errorf("%s contains a non-finite number", path)
		}
		return nil
	case []any:
		for index, entry := range typed {
			if err := validateAuthoringValue(entry, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		keys := canonjson.SortedStrings(objectKeys(typed))
		normalized := make(map[string]string, len(keys))
		for _, key := range keys {
			name := transform.SnakeName(key)
			if prior, found := normalized[name]; found {
				return fmt.Errorf("%s has snake_case key collision: %q and %q both normalize to %q", path, prior, key, name)
			}
			normalized[name] = key
		}
		for _, key := range keys {
			if err := validateAuthoringValue(typed[key], joinPath(path, key)); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}
