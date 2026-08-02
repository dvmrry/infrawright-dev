package transform

import "strings"

// DataReferentTransformOptions describes the intentionally small projection
// used for a data-only referent. A data source is instantiated by name, so
// the committed config keeps only that argument while the in-memory original
// keeps the ID needed to build the lookup sidecar.
type DataReferentTransformOptions struct {
	NameField    string
	RawItems     []any
	ResourceType string
}

// TransformDataReferentItems converts fetched data-source objects into the
// name-keyed config and lookup inputs. It does not consult a resource schema
// or registry overrides: data-referent metadata guarantees that the source is
// itself and the pack's lookup_sources name_field is the complete config
// contract.
func TransformDataReferentItems(options DataReferentTransformOptions) (result PullTransformResult, err error) {
	defer recoverErr(&err)
	if strings.TrimSpace(options.NameField) == "" {
		failf("data referent %s requires a non-empty lookup name_field", jsonQuote(options.ResourceType))
	}
	items := make(map[string]TransformRecord, len(options.RawItems))
	originals := make(map[string]TransformRecord, len(options.RawItems))
	seenIDs := make(map[string]string, len(options.RawItems))
	for index, raw := range options.RawItems {
		normalizedValue := SnakeJSONKeys(raw)
		normalized, ok := normalizedValue.(map[string]any)
		if !ok {
			failf("data referent %s item %d must be a JSON object", jsonQuote(options.ResourceType), index)
		}
		name, ok := normalized[options.NameField].(string)
		if !ok || strings.TrimSpace(name) == "" {
			failf(
				"data referent %s item %d requires a non-empty string %s field",
				jsonQuote(options.ResourceType), index, jsonQuote(options.NameField),
			)
		}
		id, ok := normalized["id"]
		if !ok || id == nil {
			failf("data referent %s item %d requires an id field", jsonQuote(options.ResourceType), index)
		}
		idToken := identityComponent(id)
		if strings.TrimSpace(idToken) == "" {
			failf(
				"data referent %s item %d requires a non-empty canonical id",
				jsonQuote(options.ResourceType), index,
			)
		}
		key := SlugifyTransformKey(name)
		if key == "" {
			key = "id_" + SlugifyTransformKey(idToken)
		}
		if _, exists := items[key]; exists {
			failf(
				"duplicate data referent key %s for %s; names must produce unique lookup keys",
				jsonQuote(key), jsonQuote(options.ResourceType),
			)
		}
		if previousKey, exists := seenIDs[idToken]; exists {
			failf(
				"duplicate data referent canonical ID %s for keys %s and %s in %s; IDs must be unique",
				jsonQuote(idToken), jsonQuote(previousKey), jsonQuote(key), jsonQuote(options.ResourceType),
			)
		}
		seenIDs[idToken] = key
		items[key] = TransformRecord{options.NameField: name}
		originals[key] = TransformRecord{"id": id, options.NameField: name}
	}
	return PullTransformResult{Items: items, Originals: originals, Drops: []string{}}, nil
}
