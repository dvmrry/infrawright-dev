package adopt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

// AdoptionMetadata ports AdoptionMetadata from
// the original implementation.
type AdoptionMetadata struct {
	ConstantKey     *string
	IdentityFields  map[string]string
	IdentityRenames map[string]string
	ImportID        string
	KeyFields       []string
	// KeyFieldReferences names key components resolved through a referenced
	// object's name instead of its raw ID, keyed by key field. Adoption
	// derives keys independently of the transform kernel, so this mirrors
	// transform's key_field_references exactly: the two lanes must agree on
	// every key or the same object lands under two identities.
	KeyFieldReferences map[string]AdoptionKeyFieldReference
	SkipIf             []map[string]any
	SkipIfLTE          []map[string]any
}

// AdoptionIdentity ports AdoptionIdentity from
// the original implementation.
type AdoptionIdentity struct {
	ImportID string
	Item     map[string]any
	Key      string
	Raw      map[string]any
}

// AdoptionSkippedItem is one identity-level skip classification.
type AdoptionSkippedItem struct {
	Item   map[string]any
	Reason string
}

// AdoptionIdentityResult ports AdoptionIdentityResult from
// the original implementation.
type AdoptionIdentityResult struct {
	Identities []AdoptionIdentity
	Skipped    []AdoptionSkippedItem
}

// AdoptionUnsupportedRule ports AdoptionUnsupportedRule from
// the original implementation.
type AdoptionUnsupportedRule struct {
	Evidence         []string
	Match            map[string]any
	MatchAnyNonempty []string
	ProviderSource   string
	ProviderVersion  string
	Reason           string
}

// AdoptionUnsupportedItem records the first unsupported rule matching an
// item. Rule identity is retained for source-order diagnostic de-duplication.
type AdoptionUnsupportedItem struct {
	Item map[string]any
	Rule *AdoptionUnsupportedRule
}

// AdoptionRawClassification ports AdoptionRawClassification from
// the original implementation.
type AdoptionRawClassification struct {
	Eligible    []map[string]any
	Skipped     []AdoptionSkippedItem
	Unsupported []AdoptionUnsupportedItem
}

func adoptionRecord(value any, label string) (map[string]any, error) {
	record, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	return record, nil
}

func adoptionStringMap(value any, label string) (map[string]string, error) {
	if value == nil {
		return map[string]string{}, nil
	}
	record, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	output := make(map[string]string, len(record))
	originalKeys := make(map[string]string, len(record))
	for _, key := range canonjson.SortedStrings(adoptMapKeys(record)) {
		item, ok := record[key].(string)
		if !ok {
			return nil, fmt.Errorf("%s.%s must be a string", label, key)
		}
		normalized := transform.SnakeName(key)
		if previous, duplicate := originalKeys[normalized]; duplicate {
			return nil, fmt.Errorf(
				"%s has normalized alias collision: %s and %s both map to %s",
				label, adoptionJSONString(previous), adoptionJSONString(key), adoptionJSONString(normalized),
			)
		}
		originalKeys[normalized] = key
		output[normalized] = item
	}
	return output, nil
}

func adoptionMatcherList(value any, label string) ([]map[string]any, error) {
	if value == nil {
		return []map[string]any{}, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a list of objects", label)
	}
	output := make([]map[string]any, len(items))
	for index, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be a list of objects", label)
		}
		output[index] = cloneAdoptionRecord(record)
	}
	return output, nil
}

func adoptionFieldList(value any, label string) ([]string, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty list of field names", label)
	}
	output := make([]string, 0, len(items))
	originalKeys := make(map[string]string, len(items))
	for index, item := range items {
		field, ok := item.(string)
		if !ok || field == "" {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string", label, index)
		}
		normalized := transform.SnakeName(field)
		if previous, duplicate := originalKeys[normalized]; duplicate {
			return nil, fmt.Errorf(
				"%s has normalized alias collision: %s and %s both map to %s",
				label, adoptionJSONString(previous), adoptionJSONString(field), adoptionJSONString(normalized),
			)
		}
		originalKeys[normalized] = field
		output = append(output, normalized)
	}
	return canonjson.SortedStrings(output), nil
}

func adoptMapKeys[T any](input map[string]T) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	return keys
}

func cloneAdoptionValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAdoptionRecord(typed)
	case []any:
		output := make([]any, len(typed))
		for index, item := range typed {
			output[index] = cloneAdoptionValue(item)
		}
		return output
	default:
		return value
	}
}

func cloneAdoptionRecord(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = cloneAdoptionValue(value)
	}
	return output
}

func adoptionObjectField(resource metadata.LoadedResourceMetadata, field string) map[string]any {
	value, _ := resource.Registry[field].(map[string]any)
	return value
}

// AdoptionUnsupportedRules resolves already-validated, version-scoped
// unsupported metadata, porting adoptionUnsupportedRules.
func AdoptionUnsupportedRules(resource metadata.LoadedResourceMetadata) ([]*AdoptionUnsupportedRule, error) {
	adopt := adoptionObjectField(resource, "adopt")
	rawRules, _ := adopt["unsupported_if"].([]any)
	output := make([]*AdoptionUnsupportedRule, 0, len(rawRules))
	for index, rawRule := range rawRules {
		label := fmt.Sprintf("%s.adopt.unsupported_if[%d]", resource.Type, index)
		rule, err := adoptionRecord(rawRule, label)
		if err != nil {
			return nil, err
		}
		provider, err := adoptionRecord(rule["provider"], label+".provider")
		if err != nil {
			return nil, err
		}
		matchRaw, hasMatch := rule["match"]
		match, matchOK := matchRaw.(map[string]any)
		nonemptyRaw, hasAnyNonempty := rule["match_any_nonempty"]
		reason, reasonOK := rule["reason"].(string)
		if hasMatch == hasAnyNonempty || (hasMatch && (!matchOK || len(match) == 0)) || !reasonOK {
			return nil, fmt.Errorf("%s is not valid unsupported adoption metadata", label)
		}
		var matchAnyNonempty []string
		if hasAnyNonempty {
			matchAnyNonempty, err = adoptionFieldList(nonemptyRaw, label+".match_any_nonempty")
			if err != nil {
				return nil, err
			}
		}
		source, sourceOK := provider["source"].(string)
		version, versionOK := provider["version"].(string)
		if !sourceOK || !versionOK {
			return nil, fmt.Errorf("%s.provider is not valid unsupported adoption metadata", label)
		}
		rawEvidence, evidenceOK := rule["evidence"].([]any)
		if !evidenceOK {
			return nil, fmt.Errorf("%s.evidence is not valid unsupported adoption metadata", label)
		}
		evidence := make([]string, len(rawEvidence))
		for evidenceIndex, item := range rawEvidence {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s.evidence is not valid unsupported adoption metadata", label)
			}
			evidence[evidenceIndex] = text
		}
		output = append(output, &AdoptionUnsupportedRule{
			Evidence:         evidence,
			Match:            cloneAdoptionRecord(match),
			MatchAnyNonempty: matchAnyNonempty,
			ProviderSource:   source,
			ProviderVersion:  version,
			Reason:           reason,
		})
	}
	return output, nil
}

func adoptionValueIsNonempty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return typed != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func adoptionUnsupportedRuleMatches(item map[string]any, rule *AdoptionUnsupportedRule) bool {
	if len(rule.MatchAnyNonempty) == 0 {
		return transform.StrictJsonScalarMatcherMatches(item, rule.Match)
	}
	for _, field := range rule.MatchAnyNonempty {
		if value, present := item[field]; present && adoptionValueIsNonempty(value) {
			return true
		}
	}
	return false
}

// AdoptionMetadataFor resolves registry adoption metadata before legacy
// transform identity fallback, porting adoptionMetadata.
func AdoptionMetadataFor(resource metadata.LoadedResourceMetadata) (AdoptionMetadata, error) {
	explicit := adoptionObjectField(resource, "adopt")
	override := resource.Override
	if override == nil {
		override = map[string]any{}
	}
	explicitFields, hasExplicitFields := explicit["identity_fields"]
	if !hasExplicitFields {
		explicitFields = override["identity_fields"]
	}
	identityFields, err := adoptionStringMap(explicitFields, resource.Type+".adopt.identity_fields")
	if err != nil {
		return AdoptionMetadata{}, err
	}
	importValue, hasImport := explicit["import_id"]
	if !hasImport {
		importValue, hasImport = override["import_id"]
	}
	if !hasImport {
		if _, alias := identityFields["import_id"]; alias {
			importValue = "{import_id}"
		} else {
			importValue = "{id}"
		}
	}
	importID, ok := importValue.(string)
	if !ok {
		return AdoptionMetadata{}, fmt.Errorf("%s.adopt.import_id must be a string", resource.Type)
	}
	keyValue, hasKey := explicit["key_field"]
	if !hasKey {
		keyValue, hasKey = override["key_field"]
	}
	if !hasKey {
		keyValue = "name"
	}
	keyFields, err := adoptionKeyFields(keyValue)
	if err != nil {
		return AdoptionMetadata{}, fmt.Errorf("%s.adopt.key_field must be a string or list of strings", resource.Type)
	}
	renames, hasRenames := explicit["identity_renames"]
	if !hasRenames {
		renames = override["renames"]
	}
	identityRenames, err := adoptionStringMap(renames, resource.Type+".adopt.identity_renames")
	if err != nil {
		return AdoptionMetadata{}, err
	}
	skipIf, hasSkipIf := explicit["skip_if"]
	if !hasSkipIf {
		skipIf = override["skip_if"]
	}
	skipIfLTE, hasSkipLTE := explicit["skip_if_lte"]
	if !hasSkipLTE {
		skipIfLTE = override["skip_if_lte"]
	}
	matchers, err := adoptionMatcherList(skipIf, resource.Type+".adopt.skip_if")
	if err != nil {
		return AdoptionMetadata{}, err
	}
	lteMatchers, err := adoptionMatcherList(skipIfLTE, resource.Type+".adopt.skip_if_lte")
	if err != nil {
		return AdoptionMetadata{}, err
	}
	var constant *string
	if text, ok := explicit["constant_key"].(string); ok {
		constant = &text
	}
	return AdoptionMetadata{
		ConstantKey:        constant,
		IdentityFields:     identityFields,
		IdentityRenames:    identityRenames,
		ImportID:           importID,
		KeyFields:          keyFields,
		KeyFieldReferences: adoptionKeyFieldReferences(override),
		SkipIf:             matchers,
		SkipIfLTE:          lteMatchers,
	}, nil
}

// AdoptionKeyFieldReference mirrors transform's resolving key component.
type AdoptionKeyFieldReference struct {
	IDField   string
	NameField string
	Referent  string
}

// adoptionKeyFieldReferences reads the override's key_field_references into
// the adoption metadata. Shape is already validated by registry/override
// validation; this reads it.
func adoptionKeyFieldReferences(override map[string]any) map[string]AdoptionKeyFieldReference {
	raw, ok := override["key_field_references"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	output := make(map[string]AdoptionKeyFieldReference, len(raw))
	for field, value := range raw {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		nameField, _ := entry["name_field"].(string)
		referent, _ := entry["referent"].(string)
		idField, ok := entry["id_field"].(string)
		if !ok || idField == "" {
			idField = "id"
		}
		output[field] = AdoptionKeyFieldReference{IDField: idField, NameField: nameField, Referent: referent}
	}
	return output
}

func adoptionKeyFields(value any) ([]string, error) {
	if text, ok := value.(string); ok {
		return []string{text}, nil
	}
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return append([]string(nil), strings...), nil
		}
		return nil, fmt.Errorf("invalid key fields")
	}
	output := make([]string, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("invalid key fields")
		}
		output[index] = text
	}
	return output, nil
}

func adoptionPathValue(value map[string]any, rawPath string) (any, bool) {
	var current any = value
	for _, rawSegment := range strings.Split(rawPath, ".") {
		segment := transform.SnakeName(rawSegment)
		record, ok := current.(map[string]any)
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

func validateAdoptionSnakeKeyCollisions(value any, path string) error {
	switch typed := value.(type) {
	case []any:
		for index, item := range typed {
			if err := validateAdoptionSnakeKeyCollisions(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case map[string]any:
		originalKeys := make(map[string]string, len(typed))
		for _, key := range canonjson.SortedStrings(adoptMapKeys(typed)) {
			normalized := transform.SnakeName(key)
			if previous, duplicate := originalKeys[normalized]; duplicate {
				return fmt.Errorf(
					"snake_case key collision at %s: %s and %s both map to %s",
					path, adoptionJSONString(previous), adoptionJSONString(key), adoptionJSONString(normalized),
				)
			}
			originalKeys[normalized] = key
			if err := validateAdoptionSnakeKeyCollisions(typed[key], path+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

// AdoptionIdentityItem shapes a raw object for identity only; it never
// decides Terraform coverage. It ports adoptionIdentityItem.
func AdoptionIdentityItem(meta AdoptionMetadata, raw any, resourceType string) (map[string]any, error) {
	if err := validateAdoptionSnakeKeyCollisions(raw, "$raw"); err != nil {
		return nil, err
	}
	rawRecord, err := adoptionRecord(transform.SnakeJSONKeys(raw), resourceType+" raw item")
	if err != nil {
		return nil, err
	}
	item := cloneAdoptionRecord(rawRecord)
	for _, oldName := range canonjson.SortedStrings(adoptMapKeys(meta.IdentityRenames)) {
		oldSnake := transform.SnakeName(oldName)
		newSnake := transform.SnakeName(meta.IdentityRenames[oldName])
		if value, present := item[oldSnake]; present {
			item[newSnake] = value
			delete(item, oldSnake)
		}
	}
	for _, alias := range canonjson.SortedStrings(adoptMapKeys(meta.IdentityFields)) {
		rawPath := meta.IdentityFields[alias]
		selected, found := adoptionPathValue(rawRecord, rawPath)
		if !found {
			selected, found = adoptionPathValue(item, rawPath)
		}
		if !found {
			return nil, fmt.Errorf("%s adopt.identity_fields.%s path %s missing from item", resourceType, alias, adoptionJSONString(rawPath))
		}
		if existing, present := item[alias]; present && !canonjson.JSONEqual(existing, selected) {
			return nil, fmt.Errorf("%s adopt.identity_fields.%s path %s would overwrite existing field %s", resourceType, alias, adoptionJSONString(rawPath), adoptionJSONString(alias))
		}
		item[alias] = cloneAdoptionValue(selected)
	}
	return item, nil
}

// DeriveAdoptionKey ports deriveAdoptionKey, with one deliberate extension
// over the ported behavior: an absent key field is treated exactly like a
// present-but-empty one.
//
// The two are the same statement from the API. ZIA omits empty fields on the
// wire, so a predefined url category carries no configured_name key at all
// while a custom one carries a real name -- the very shape key_field +
// fallback exists for. Erroring on absence made that wire-format detail fatal
// to an entire fetch (a downstream run failed 235 of 235 items) even though
// the identical item spelled configured_name:"" adopts cleanly through the
// id_<slug> fallback below.
//
// The cost, stated openly: a mistyped key_field no longer errors -- every
// item quietly takes the id_<slug> fallback instead. That trade is
// deliberate. The failure it buys is loud in a different register (every
// readable key in the generated tree collapses to id_...), while the failure
// it removes blocked correct configurations on data the operator does not
// control.
// DeriveAdoptionKeyResolved is DeriveAdoptionKey with the batch's
// ID-to-name index for resolving key components (see
// AdoptionMetadata.KeyFieldReferences). A resolving component that does not
// resolve contributes nothing, exactly as transform's deriveKey omits it, so
// a top-level object carrying a sentinel parent keeps the key it had before
// its children needed disambiguating.
func DeriveAdoptionKeyResolved(item map[string]any, meta AdoptionMetadata, nameByID map[string]map[string]string) (string, error) {
	if meta.ConstantKey != nil || len(meta.KeyFieldReferences) == 0 {
		return DeriveAdoptionKey(item, meta)
	}
	parts := make([]string, 0, len(meta.KeyFields))
	for _, field := range meta.KeyFields {
		value, found := adoptionPathValue(item, field)
		reference, resolving := meta.KeyFieldReferences[field]
		if !found {
			continue
		}
		part, err := tfrender.PythonTransformStringForAdopt(value)
		if err != nil {
			return "", err
		}
		if !resolving {
			parts = append(parts, part)
			continue
		}
		if name, ok := nameByID[field][part]; ok && strings.TrimSpace(name) != "" {
			parts = append(parts, name)
		}
		_ = reference
	}
	key := transform.SlugifyTransformKey(strings.Join(parts, " "))
	if key != "" {
		return key, nil
	}
	return adoptionFallbackKey(item, meta, parts)
}

// adoptionKeyReferenceIndex builds the ID-to-name index adoption's resolving
// key components read, from the same raw items the batch derives keys for.
// Registry validation restricts the referent to the resource's own type, so
// the index is exactly this batch.
func adoptionKeyReferenceIndex(rawItems []any, meta AdoptionMetadata, resourceType string) (map[string]map[string]string, error) {
	if len(meta.KeyFieldReferences) == 0 {
		return nil, nil
	}
	output := make(map[string]map[string]string, len(meta.KeyFieldReferences))
	for field, reference := range meta.KeyFieldReferences {
		if reference.Referent != resourceType {
			return nil, fmt.Errorf(
				"%s.override.key_field_references.%s referent %s must be this resource's own type; cross-type key resolution is not supported",
				resourceType, field, adoptionJSONString(reference.Referent),
			)
		}
		nameByID := make(map[string]string, len(rawItems))
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			identity, hasIdentity := adoptionPathValue(item, reference.IDField)
			name, hasName := adoptionPathValue(item, reference.NameField)
			if !hasIdentity || !hasName {
				continue
			}
			identityPart, err := tfrender.PythonTransformStringForAdopt(identity)
			if err != nil {
				return nil, err
			}
			namePart, err := tfrender.PythonTransformStringForAdopt(name)
			if err != nil {
				return nil, err
			}
			nameByID[identityPart] = namePart
		}
		output[field] = nameByID
	}
	return output, nil
}

func DeriveAdoptionKey(item map[string]any, meta AdoptionMetadata) (string, error) {
	if meta.ConstantKey != nil {
		if *meta.ConstantKey == "" {
			return "", fmt.Errorf("adopt.constant_key must be a non-empty string")
		}
		return *meta.ConstantKey, nil
	}
	parts := make([]string, len(meta.KeyFields))
	for index, field := range meta.KeyFields {
		value, found := adoptionPathValue(item, field)
		if !found {
			// Contributes the empty part a present-but-empty value would;
			// whether the whole key is empty is decided after the join.
			continue
		}
		part, err := tfrender.PythonTransformStringForAdopt(value)
		if err != nil {
			return "", err
		}
		parts[index] = part
	}
	key := transform.SlugifyTransformKey(strings.Join(parts, " "))
	if key != "" {
		return key, nil
	}
	return adoptionFallbackKey(item, meta, parts)
}

// adoptionFallbackKey is the shared id_<slug> fallback both key derivations
// end in when composition produced nothing sluggable.
func adoptionFallbackKey(item map[string]any, meta AdoptionMetadata, parts []string) (string, error) {
	id, present := item["id"]
	if !present || id == nil {
		return "", fmt.Errorf("derived key is empty for %s (value(s) %s absent or without ASCII letters/digits) and item has no 'id' to fall back on", adoptionJSONValue(meta.KeyFields), adoptionJSONValue(parts))
	}
	rendered, err := tfrender.PythonTransformStringForAdopt(id)
	if err != nil {
		return "", err
	}
	return "id_" + transform.SlugifyTransformKey(rendered), nil
}

// ClassifyAdoptionRawItems classifies raw items before identity shaping or
// Terraform execution, porting classifyAdoptionRawItems.
func ClassifyAdoptionRawItems(rawItems []any, resource metadata.LoadedResourceMetadata) (classification AdoptionRawClassification, err error) {
	defer recoverAdoptionTransformError(&err)
	meta, err := AdoptionMetadataFor(resource)
	if err != nil {
		return classification, err
	}
	rules, err := AdoptionUnsupportedRules(resource)
	if err != nil {
		return classification, err
	}
	classification.Eligible = []map[string]any{}
	classification.Skipped = []AdoptionSkippedItem{}
	classification.Unsupported = []AdoptionUnsupportedItem{}
	for _, raw := range rawItems {
		rawItem, err := adoptionRecord(raw, resource.Type+" raw item")
		if err != nil {
			return classification, err
		}
		if err := validateAdoptionSnakeKeyCollisions(rawItem, "$raw"); err != nil {
			return classification, err
		}
		item, err := adoptionRecord(transform.SnakeJSONKeys(rawItem), resource.Type+" raw item")
		if err != nil {
			return classification, err
		}
		skipData := map[string]any{
			"skip_if":     matcherListAny(meta.SkipIf),
			"skip_if_lte": matcherListAny(meta.SkipIfLTE),
		}
		reason, matched := transform.TransformSkipMatchReason(item, skipData, resource.Type+".adopt")
		if matched {
			classification.Skipped = append(classification.Skipped, AdoptionSkippedItem{Item: item, Reason: reason})
			continue
		}
		var unsupported *AdoptionUnsupportedRule
		for _, rule := range rules {
			if adoptionUnsupportedRuleMatches(item, rule) {
				unsupported = rule
				break
			}
		}
		if unsupported != nil {
			classification.Unsupported = append(classification.Unsupported, AdoptionUnsupportedItem{Item: item, Rule: unsupported})
			continue
		}
		classification.Eligible = append(classification.Eligible, cloneAdoptionRecord(rawItem))
	}
	return classification, nil
}

func matcherListAny(input []map[string]any) []any {
	output := make([]any, len(input))
	for index, item := range input {
		output[index] = item
	}
	return output
}

func recoverAdoptionTransformError(err *error) {
	if recovered := recover(); recovered != nil {
		if transformErr, ok := recovered.(*transform.TransformError); ok {
			*err = transformErr
			return
		}
		panic(recovered)
	}
}

// DeriveAdoptionIdentities derives, validates, and de-duplicates a resource's
// raw adoption identities, porting deriveAdoptionIdentities.
func DeriveAdoptionIdentities(rawItems []any, resource metadata.LoadedResourceMetadata) (AdoptionIdentityResult, error) {
	meta, err := AdoptionMetadataFor(resource)
	if err != nil {
		return AdoptionIdentityResult{}, err
	}
	classified, err := ClassifyAdoptionRawItems(rawItems, resource)
	if err != nil {
		return AdoptionIdentityResult{}, err
	}
	if len(classified.Unsupported) > 0 {
		rule := classified.Unsupported[0].Rule
		return AdoptionIdentityResult{}, fmt.Errorf("%s contains %d item(s) unsupported by provider %s %s", resource.Type, len(classified.Unsupported), rule.ProviderSource, rule.ProviderVersion)
	}
	type retainedItem struct {
		item map[string]any
		raw  map[string]any
	}
	retained := make([]retainedItem, 0, len(classified.Eligible))
	for _, raw := range classified.Eligible {
		item, err := AdoptionIdentityItem(meta, raw, resource.Type)
		if err != nil {
			return AdoptionIdentityResult{}, err
		}
		retained = append(retained, retainedItem{item: item, raw: cloneAdoptionRecord(raw)})
	}
	if meta.ConstantKey != nil && len(retained) > 1 {
		return AdoptionIdentityResult{}, fmt.Errorf("%s adopt.constant_key %s is only valid for singleton adoption; read produced %d items after skip predicates", resource.Type, adoptionJSONString(*meta.ConstantKey), len(retained))
	}
	nameByID, err := adoptionKeyReferenceIndex(rawItems, meta, resource.Type)
	if err != nil {
		return AdoptionIdentityResult{}, err
	}
	result := AdoptionIdentityResult{Identities: []AdoptionIdentity{}, Skipped: classified.Skipped}
	keys := make(map[string]struct{}, len(retained))
	importIDs := make(map[string]string, len(retained))
	for _, entry := range retained {
		key, err := DeriveAdoptionKeyResolved(entry.item, meta, nameByID)
		if err != nil {
			return AdoptionIdentityResult{}, err
		}
		if _, duplicate := keys[key]; duplicate {
			return AdoptionIdentityResult{}, fmt.Errorf("duplicate derived key %s for %s", adoptionJSONString(key), resource.Type)
		}
		importID, err := tfrender.FormatImportTemplateForAdopt(meta.ImportID, entry.item)
		if err != nil {
			return AdoptionIdentityResult{}, err
		}
		if prior, duplicate := importIDs[importID]; duplicate {
			return AdoptionIdentityResult{}, fmt.Errorf("%s duplicate import_id for keys %s and %s", resource.Type, adoptionJSONString(prior), adoptionJSONString(key))
		}
		keys[key] = struct{}{}
		importIDs[importID] = key
		result.Identities = append(result.Identities, AdoptionIdentity{ImportID: importID, Item: entry.item, Key: key, Raw: entry.raw})
	}
	return result, nil
}

func adoptionJSONString(value string) string {
	encoded, _ := adoptionJSONMarshal(value)
	return string(encoded)
}

func adoptionJSONValue(value any) string {
	encoded, err := adoptionJSONMarshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

type adoptionLosslessNumberJSON struct {
	IsLosslessNumber bool   `json:"isLosslessNumber"`
	Value            string `json:"value"`
}

func adoptionDiagnosticJSONValue(value any) any {
	switch typed := value.(type) {
	case json.Number:
		return adoptionLosslessNumberJSON{IsLosslessNumber: true, Value: string(typed)}
	case []any:
		output := make([]any, len(typed))
		for index, child := range typed {
			output[index] = adoptionDiagnosticJSONValue(child)
		}
		return output
	case map[string]any:
		output := make(map[string]any, len(typed))
		for key, child := range typed {
			output[key] = adoptionDiagnosticJSONValue(child)
		}
		return output
	default:
		return value
	}
}

func adoptionJSONWithLiteralLineSeparators(encoded []byte) []byte {
	var output bytes.Buffer
	for index := 0; index < len(encoded); {
		if index+6 <= len(encoded) && encoded[index] == '\\' &&
			(string(encoded[index:index+6]) == `\u2028` || string(encoded[index:index+6]) == `\u2029`) {
			precedingBackslashes := 0
			for prior := index - 1; prior >= 0 && encoded[prior] == '\\'; prior-- {
				precedingBackslashes++
			}
			if precedingBackslashes%2 == 0 {
				if encoded[index+5] == '8' {
					output.WriteRune('\u2028')
				} else {
					output.WriteRune('\u2029')
				}
				index += 6
				continue
			}
		}
		output.WriteByte(encoded[index])
		index++
	}
	return output.Bytes()
}

// adoptionJSONMarshal matches JSON.stringify's non-HTML JSON spelling for
// reachable scalar diagnostic bytes. Go's encoding/json defaults to HTML
// escaping and represents json.Number as a bare token, while the Node source
// emits literal line separators and lossless-json's LosslessNumber object.
func adoptionJSONMarshal(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(adoptionDiagnosticJSONValue(value)); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(output.Bytes(), []byte{'\n'})
	return adoptionJSONWithLiteralLineSeparators(encoded), nil
}
