package metadata

// resources.go ports the original implementation: registry.json/override
// validation, fetch/derive/adopt shape checks, and provider-schema/resource
// loading. See the file-level doc comment in packs.go for this package's
// exported-wrapper/unexported-implementation convention.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

var registryResourceKeys = stringSet(
	"adopt", "data_referent", "derive", "fetch", "fetch_skip_reason", "generate", "product",
)

var canonicalResourceType = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var fetchKeys = stringSet("envelope", "expand", "merge_paths", "optional_http_statuses", "pagination", "path", "query")

var paginationStyles = stringSet("single", "zcc_v2", "zia", "zpa")

var fetchQueryKeys = stringSet("query")

// dotPathSegment ports DOT_PATH_SEGMENT from the original implementation.
var dotPathSegment = regexp.MustCompile(`(?i)^(?:\.|%2e){1,2}$`)

// safeFetchPath ports SAFE_FETCH_PATH from the original implementation.
var safeFetchPath = regexp.MustCompile(`^(?:[A-Za-z0-9\-._~!$&'()*+,;=:@/{}]|%[0-9A-Fa-f]{2})+$`)

var deriveKeys = stringSet("from", "policy_type")

var adoptKeys = stringSet(
	"constant_key", "identity_fields", "identity_renames", "import_id",
	"key_field", "skip_if", "skip_if_lte", "unsupported_if",
)

var unsupportedIfKeys = stringSet("evidence", "match", "match_any_nonempty", "provider", "reason")
var unsupportedIfRequiredKeys = stringSet("evidence", "provider", "reason")
var unsupportedProviderKeys = stringSet("source", "version")

var overrideKeys = stringSet(
	"acknowledged_drops", "defaults", "divide", "drop_if_default", "drops",
	"html_escape_fields", "identity_fields", "import_id", "invert_bool",
	"key_field", "merge_blocks", "module_single_blocks", "no_html_unescape",
	"ranges", "references", "renames", "sample", "skip_if", "skip_if_lte",
	"sort_lists", "split_csv", "strip_prefix", "value_map",
)

// IsCanonicalResourceType reports whether value is a canonical Terraform
// resource type suitable for use as a cross-platform artifact identity. This
// is Go-only evidence-integrity hardening: lowercase ASCII prevents distinct
// registry keys from aliasing on case-folding or Unicode-normalizing filesystems.
func IsCanonicalResourceType(value string) bool {
	return canonicalResourceType.MatchString(value)
}

// LoadedRegistry ports the LoadedRegistry interface from
// the original implementation.
type LoadedRegistry struct {
	Entries map[string]JsonObject
	Sources map[string]string
}

// LoadedOverrides ports the LoadedOverrides interface from
// the original implementation.
type LoadedOverrides struct {
	Entries map[string]JsonObject
	Sources map[string]string
}

// ProviderSchema ports the ProviderSchema interface from
// the original implementation.
type ProviderSchema struct {
	Provider        string
	Path            string
	Data            JsonObject
	ResourceSchemas map[string]JsonObject
}

func validateQuery(value any, source string) {
	obj, ok := value.(JsonObject)
	if !ok {
		failf("%s must be an object", source)
		return
	}
	for _, key := range sortedKeys(obj) {
		if !isJsonScalar(obj[key]) {
			failf("%s.%s must be a scalar query value", source, key)
		}
	}
}

// fetchPathSafetyViolation reports why a collector path-like value is
// unsafe for WHATWG URL composition, or nil if it is safe. Ports
// fetchPathSafetyViolation from the original implementation.
func fetchPathSafetyViolation(value string) *string {
	violation := func(s string) *string { return &s }
	if strings.Contains(value, "\\") {
		return violation("must not contain backslashes")
	}
	if strings.Contains(value, "?") || strings.Contains(value, "#") {
		return violation("must not contain query or fragment delimiters ('?' or '#')")
	}
	if !safeFetchPath.MatchString(value) {
		return violation("must contain only RFC 3986 path characters, valid percent escapes, and expansion braces")
	}
	for _, segment := range strings.Split(value, "/") {
		if dotPathSegment.MatchString(segment) {
			return violation("must not contain raw or percent-encoded dot path segments")
		}
	}
	return nil
}

// fetchExpansionSafetyViolation reports why an expansion value is unsafe
// (expansion values are quoted as one path segment, so only dot segments
// survive quoting). Ports fetchExpansionSafetyViolation from
// the original implementation.
func fetchExpansionSafetyViolation(value string) *string {
	if value == "." || value == ".." {
		s := "must not be '.' or '..'"
		return &s
	}
	return nil
}

// FetchPathSafetyViolation is the exported form of
// fetchPathSafetyViolation, for go/internal/collectors (rest.go's
// expandedPaths, porting the original implementation), which shares this
// exact check with the original implementation's own registry
// validation -- both sides of a single source of truth in the Node source,
// where resources.ts exports the function and rest.ts imports it.
func FetchPathSafetyViolation(value string) *string {
	return fetchPathSafetyViolation(value)
}

// FetchExpansionSafetyViolation is the exported form of
// fetchExpansionSafetyViolation; see FetchPathSafetyViolation's doc
// comment.
func FetchExpansionSafetyViolation(value string) *string {
	return fetchExpansionSafetyViolation(value)
}

func validateFetchPathValue(value, source string) {
	if violation := fetchPathSafetyViolation(value); violation != nil {
		failf("%s %s", source, *violation)
	}
}

func validateExpand(value any, source string) {
	obj, ok := value.(JsonObject)
	if !ok {
		failf("%s must be an object", source)
		return
	}
	for _, key := range sortedKeys(obj) {
		if len(key) == 0 {
			failf("%s keys must be non-empty strings", source)
		}
		values, isArray := obj[key].([]any)
		if !isArray {
			failf("%s.%s must be a list", source, key)
			continue
		}
		for index, item := range values {
			label := fmt.Sprintf("%s.%s[%d]", source, key, index)
			expansion := requireNonEmptyString(item, label)
			if violation := fetchExpansionSafetyViolation(expansion); violation != nil {
				failf("%s %s", label, *violation)
			}
		}
	}
}

func validateFetchExpansionShape(fetchPath string, expand any, source string) {
	var expansions []string
	if obj, ok := expand.(JsonObject); ok {
		expansions = sortedKeys(obj)
	}
	if len(expansions) > 1 {
		failf("%s.expand supports exactly one placeholder", source)
	}
	if len(expansions) == 0 {
		if strings.Contains(fetchPath, "{") || strings.Contains(fetchPath, "}") {
			failf("%s.path must not contain undeclared expansion braces", source)
		}
		return
	}
	key := expansions[0]
	token := "{" + key + "}"
	if !strings.Contains(fetchPath, token) {
		failf("%s.expand key %s is not present in path", source, jsonQuote(key))
	}
	remainder := strings.ReplaceAll(fetchPath, token, "")
	if strings.Contains(remainder, "{") || strings.Contains(remainder, "}") {
		failf("%s.path must not contain undeclared expansion braces", source)
	}
}

func validateStatuses(value any, source string) {
	arr, ok := value.([]any)
	if !ok {
		failf("%s must be a list", source)
		return
	}
	for index, item := range arr {
		if !isIntegerJsonNumber(item) {
			failf("%s[%d] must be an integer", source, index)
		}
	}
}

func validateFetch(value any, source string) {
	obj, ok := value.(JsonObject)
	if !ok {
		failf("%s must be an object", source)
		return
	}
	rejectUnknownKeys(obj, fetchKeys, source)
	requireKeys(obj, stringSet("pagination", "path"), source)
	pagination := requireNonEmptyString(obj["pagination"], source+".pagination")
	if _, ok := paginationStyles[pagination]; !ok {
		allowed := strings.Join(canonjson.SortedStrings(setKeys(paginationStyles)), ", ")
		failf("%s.pagination unsupported value %s; allowed values: %s", source, jsonQuote(pagination), allowed)
	}
	fetchPath := requireNonEmptyString(obj["path"], source+".path")
	validateFetchPathValue(fetchPath, source+".path")
	if envelope, ok := obj["envelope"]; ok {
		requireNonEmptyString(envelope, source+".envelope")
	}
	if query, ok := obj["query"]; ok {
		validateQuery(query, source+".query")
	}
	if expand, ok := obj["expand"]; ok {
		validateExpand(expand, source+".expand")
	}
	validateFetchExpansionShape(fetchPath, obj["expand"], source)
	if merge, ok := obj["merge_paths"]; ok {
		validateMergePaths(merge, fetchPath, pagination, obj, source)
	}
	if statuses, ok := obj["optional_http_statuses"]; ok {
		validateStatuses(statuses, source+".optional_http_statuses")
	}
}

// validateMergePaths validates the singleton-merge fetch surface: additional
// endpoints whose single-object payloads merge into the base path's object
// (e.g. ZIA /security + /security/advanced, which the vendor SDK combines
// into one settings read). Merging concatenated pages or fanned-out
// expansions has no defined shape, so the key requires pagination "single"
// and excludes expand.
func validateMergePaths(value any, fetchPath, pagination string, obj JsonObject, source string) {
	label := source + ".merge_paths"
	if pagination != "single" {
		failf("%s requires pagination \"single\"; merged fetches are a singleton-object surface", label)
	}
	if _, hasExpand := obj["expand"]; hasExpand {
		failf("%s cannot be combined with expand", label)
	}
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		failf("%s must be a non-empty list of paths", label)
		return
	}
	seen := map[string]struct{}{fetchPath: {}}
	for i, raw := range list {
		entryLabel := fmt.Sprintf("%s[%d]", label, i)
		path, ok := raw.(string)
		if !ok || path == "" {
			failf("%s must be a non-empty string", entryLabel)
			continue
		}
		validateFetchPathValue(path, entryLabel)
		if _, duplicate := seen[path]; duplicate {
			failf("%s duplicates path %s", entryLabel, jsonQuote(path))
		}
		seen[path] = struct{}{}
	}
}

func validateDerive(value any, source string) {
	obj, ok := value.(JsonObject)
	if !ok {
		failf("%s must be an object", source)
		return
	}
	rejectUnknownKeys(obj, deriveKeys, source)
	requireKeys(obj, stringSet("from"), source)
	requireNonEmptyString(obj["from"], source+".from")
	if policyType, ok := obj["policy_type"]; ok {
		requireNonEmptyString(policyType, source+".policy_type")
	}
}

var snakeCaseBoundary1 = regexp.MustCompile(`(.)([A-Z][a-z]+)`)
var snakeCaseBoundary2 = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// snakeCase ports snakeCase from the original implementation.
func snakeCase(name string) string {
	result := snakeCaseBoundary1.ReplaceAllString(name, "${1}_${2}")
	result = snakeCaseBoundary2.ReplaceAllString(result, "${1}_${2}")
	return strings.ToLower(result)
}

// skipField pairs an original field name with its snake-cased form. Ports
// the anonymous `{ field, snake }` tuple validateSkipMatchers returns in
// the original implementation.
type skipField struct {
	Field string
	Snake string
}

func validateSkipMatchers(data JsonObject, source string) []skipField {
	var fields []skipField
	for _, key := range []string{"skip_if", "skip_if_lte"} {
		matchersRaw, ok := data[key]
		if !ok {
			continue
		}
		matchers, isArray := matchersRaw.([]any)
		if !isArray {
			failf("%s.%s must be a list", source, key)
			continue
		}
		for index, matcherRaw := range matchers {
			matcherPath := fmt.Sprintf("%s.%s[%d]", source, key, index)
			matcher, ok := matcherRaw.(JsonObject)
			if !ok {
				failf("%s must be an object", matcherPath)
				continue
			}
			if len(matcher) == 0 {
				failf("%s must not be empty", matcherPath)
			}
			for _, field := range sortedKeys(matcher) {
				value := matcher[field]
				if len(field) == 0 {
					failf("%s field names must be non-empty strings", matcherPath)
				}
				fields = append(fields, skipField{Field: field, Snake: snakeCase(field)})
				if key == "skip_if_lte" {
					if !isFiniteJsonNumber(value) {
						failf("%s.%s threshold must be a finite JSON number", matcherPath, field)
					}
				} else if !isJsonScalar(value) {
					failf("%s.%s value must be a scalar", matcherPath, field)
				}
			}
		}
	}
	return fields
}

func validateSkipRenameConflicts(data JsonObject, source string, fields []skipField) {
	var renames JsonObject
	if r, ok := data["renames"].(JsonObject); ok {
		renames = r
	} else if r, ok := data["identity_renames"].(JsonObject); ok {
		renames = r
	} else {
		return
	}
	renamed := make(map[string]struct{})
	for oldName, newNameRaw := range renames {
		renamed[snakeCase(oldName)] = struct{}{}
		if newName, ok := newNameRaw.(string); ok {
			renamed[snakeCase(newName)] = struct{}{}
		}
	}
	conflictSet := make(map[string]struct{})
	for _, entry := range fields {
		if _, ok := renamed[entry.Snake]; ok {
			conflictSet[entry.Field] = struct{}{}
		}
	}
	if len(conflictSet) == 0 {
		return
	}
	conflicts := canonjson.SortedStrings(setKeys(conflictSet))
	failf(
		"skip predicates in %s reference renamed field(s) %s; skip predicates run on snake-cased raw input before transform or adoption identity renames, so keep skip fields independent of renames",
		source, strings.Join(conflicts, ", "),
	)
}

// conditionKey builds an internal dedup key for an unsupported_if rule's
// `match` object, snake-casing its field names first. Both sides of every
// comparison this package makes against a conditionKey result are produced
// by this same function (encoding/json.Marshal on a Go map, which sorts
// keys), so it need not reproduce JSON.stringify's literal bytes -- ports
// the `condition` local in the original implementation's
// validateAdopt, which builds essentially the same key via
// `JSON.stringify(Object.fromEntries(...))`.
func conditionKey(match JsonObject) string {
	snakeMatch := make(JsonObject, len(match))
	for field, expected := range match {
		snakeMatch[snakeCase(field)] = expected
	}
	encoded := mustMarshalJSON(snakeMatch)
	return encoded
}

func validateAdopt(value any, source string) {
	obj, ok := value.(JsonObject)
	if !ok {
		failf("%s must be an object", source)
		return
	}
	rejectUnknownKeys(obj, adoptKeys, source)
	_, hasConstantKey := obj["constant_key"]
	_, hasKeyField := obj["key_field"]
	if hasConstantKey && hasKeyField {
		failf("%s cannot set both constant_key and key_field", source)
	}
	_, hasImportID := obj["import_id"]
	if hasConstantKey && !hasImportID {
		failf("%s.constant_key requires import_id", source)
	}
	for _, key := range []string{"constant_key", "import_id"} {
		if v, ok := obj[key]; ok {
			requireNonEmptyString(v, fmt.Sprintf("%s.%s", source, key))
		}
	}
	if keyFieldRaw, ok := obj["key_field"]; ok {
		switch kf := keyFieldRaw.(type) {
		case string:
			requireNonEmptyString(kf, source+".key_field")
		case []any:
			invalid := len(kf) == 0
			if !invalid {
				for _, field := range kf {
					s, isString := field.(string)
					if !isString || len(s) == 0 {
						invalid = true
						break
					}
				}
			}
			if invalid {
				failf("%s.key_field must be a non-empty string or list of non-empty strings", source)
			}
		default:
			failf("%s.key_field must be a non-empty string or list of non-empty strings", source)
		}
	}
	for _, key := range []string{"identity_renames", "identity_fields"} {
		if v, ok := obj[key]; ok {
			validateStringMap(v, fmt.Sprintf("%s.%s", source, key))
		}
	}
	skipFields := validateSkipMatchers(obj, source)
	if rulesRaw, ok := obj["unsupported_if"]; ok {
		rules, isArray := rulesRaw.([]any)
		if !isArray || len(rules) == 0 {
			failf("%s.unsupported_if must be a non-empty list", source)
		}
		conditions := make(map[string]struct{})
		for index, rawRule := range rules {
			ruleSource := fmt.Sprintf("%s.unsupported_if[%d]", source, index)
			rule := requireObject(rawRule, ruleSource)
			rejectUnknownKeys(rule, unsupportedIfKeys, ruleSource)
			requireKeys(rule, unsupportedIfRequiredKeys, ruleSource)
			matchRaw, hasMatch := rule["match"]
			nonemptyRaw, hasAnyNonempty := rule["match_any_nonempty"]
			if hasMatch == hasAnyNonempty {
				failf("%s must set exactly one of match or match_any_nonempty", ruleSource)
			}
			condition := ""
			if hasMatch {
				match := requireObject(matchRaw, ruleSource+".match")
				if len(match) == 0 {
					failf("%s.match must not be empty", ruleSource)
				}
				for _, field := range sortedKeys(match) {
					expected := match[field]
					if len(field) == 0 {
						failf("%s.match field names must be non-empty strings", ruleSource)
					}
					if !isJsonScalar(expected) {
						failf("%s.match.%s must be a scalar", ruleSource, field)
					}
					skipFields = append(skipFields, skipField{Field: field, Snake: snakeCase(field)})
				}
				condition = "match:" + conditionKey(match)
			} else {
				fields, ok := nonemptyRaw.([]any)
				if !ok || len(fields) == 0 {
					failf("%s.match_any_nonempty must be a non-empty list of field names", ruleSource)
				}
				normalizedFields := make([]string, 0, len(fields))
				seenFields := make(map[string]string, len(fields))
				for fieldIndex, rawField := range fields {
					field := requireNonEmptyString(rawField, fmt.Sprintf("%s.match_any_nonempty[%d]", ruleSource, fieldIndex))
					normalized := snakeCase(field)
					if previous, duplicate := seenFields[normalized]; duplicate {
						failf("%s.match_any_nonempty fields %s and %s both normalize to %s", ruleSource, jsonQuote(previous), jsonQuote(field), jsonQuote(normalized))
					}
					seenFields[normalized] = field
					normalizedFields = append(normalizedFields, normalized)
					skipFields = append(skipFields, skipField{Field: field, Snake: normalized})
				}
				normalizedFields = canonjson.SortedStrings(normalizedFields)
				condition = "match_any_nonempty:" + mustMarshalJSON(normalizedFields)
			}
			if _, duplicate := conditions[condition]; duplicate {
				failf("%s.unsupported_if contains duplicate predicate %s", source, condition)
			}
			conditions[condition] = struct{}{}
			provider := requireObject(rule["provider"], ruleSource+".provider")
			rejectUnknownKeys(provider, unsupportedProviderKeys, ruleSource+".provider")
			requireKeys(provider, unsupportedProviderKeys, ruleSource+".provider")
			requireNonEmptyString(provider["source"], ruleSource+".provider.source")
			requireNonEmptyString(provider["version"], ruleSource+".provider.version")
			requireNonEmptyString(rule["reason"], ruleSource+".reason")
			evidenceRaw, hasEvidence := rule["evidence"]
			evidence, isEvidenceArray := evidenceRaw.([]any)
			if !hasEvidence || !isEvidenceArray || len(evidence) == 0 {
				failf("%s.evidence must be a non-empty list", ruleSource)
			}
			seenEvidence := make(map[string]struct{})
			for evidenceIndex, rawEvidence := range evidence {
				item := requireNonEmptyString(rawEvidence, fmt.Sprintf("%s.evidence[%d]", ruleSource, evidenceIndex))
				if _, duplicate := seenEvidence[item]; duplicate {
					failf("%s.evidence contains duplicate %s", ruleSource, jsonQuote(item))
				}
				seenEvidence[item] = struct{}{}
			}
		}
	}
	validateSkipRenameConflicts(obj, source, skipFields)
}

// validateRegistry ports validateRegistry from
// the original implementation.
func validateRegistry(value any, source string) JsonObject {
	data := requireObject(value, source)
	for _, resourceType := range sortedKeys(data) {
		rawEntry := data[resourceType]
		if len(resourceType) == 0 {
			failf("%s resource keys must be non-empty strings", source)
		}
		if !IsCanonicalResourceType(resourceType) {
			failf(
				"%s resource key %s must match ^[a-z][a-z0-9_]*$",
				source, jsonQuote(resourceType),
			)
		}
		label := fmt.Sprintf("%s.%s", source, resourceType)
		entry, ok := rawEntry.(JsonObject)
		if !ok {
			failf("%s must be an object", label)
			continue
		}
		if _, retired := entry["slug_group"]; retired {
			failf("%s.slug_group has been removed; see docs/state-topology.md", label)
		}
		rejectUnknownKeys(entry, registryResourceKeys, label)
		requireKeys(entry, stringSet("product"), label)
		requireNonEmptyString(entry["product"], label+".product")
		if generate, ok := entry["generate"]; ok {
			if _, isBool := generate.(bool); !isBool {
				failf("%s.generate must be a boolean", label)
			}
		}
		if dataReferent, ok := entry["data_referent"]; ok {
			isDataReferent, isBool := dataReferent.(bool)
			if !isBool {
				failf("%s.data_referent must be a boolean", label)
			}
			if isDataReferent {
				// A fetch OBJECT is required, not mere key presence: the
				// registry deliberately permits `"fetch": false` +
				// fetch_skip_reason for declared-unfetchable types, and a
				// data referent whose objects cannot be fetched can never
				// mint its lookup or items.
				if _, hasFetch := entry["fetch"].(JsonObject); !hasFetch {
					failf("%s.data_referent=true requires %s.fetch", label, label)
				}
				if generated, hasGenerate := entry["generate"].(bool); hasGenerate && generated {
					failf("%s.data_referent=true forbids %s.generate=true", label, label)
				}
				if _, hasAdopt := entry["adopt"]; hasAdopt {
					failf("%s.data_referent=true forbids %s.adopt", label, label)
				}
				if _, hasDerive := entry["derive"]; hasDerive {
					failf("%s.data_referent=true forbids %s.derive", label, label)
				}
			}
		}
		validateFetchDeclaration(entry, label)
		if derive, ok := entry["derive"]; ok {
			validateDerive(derive, label+".derive")
		}
		if adopt, ok := entry["adopt"]; ok {
			validateAdopt(adopt, label+".adopt")
			if _, hasDerive := entry["derive"]; hasDerive {
				if adoptObj, ok := adopt.(JsonObject); ok {
					if _, hasUnsupportedIf := adoptObj["unsupported_if"]; hasUnsupportedIf {
						failf("%s.adopt.unsupported_if is not valid for a derived resource", label)
					}
				}
			}
		}
	}
	return data
}

// validateFetchDeclaration enforces that a resource says, as data, whether the
// engine can see its live state.
//
// A fetch block means it can. `"fetch": false` with a fetch_skip_reason means
// it deliberately cannot -- a generate-only type, say -- and is the escape
// hatch that makes the committed-config-implies-fetchable invariant safe to
// enforce. Without it, the first consumer who legitimately commits config for
// a gen-only type meets a false positive, and a nuisance gate gets disabled,
// which is worse than not having one.
//
// registry.json is strict JSON, so a comment cannot carry the reason; it has
// to be a field.
func validateFetchDeclaration(entry JsonObject, label string) {
	fetch, hasFetch := entry["fetch"]
	reason, hasReason := entry["fetch_skip_reason"]
	if hasFetch {
		if declared, isBool := fetch.(bool); isBool {
			if declared {
				failf(
					"%s.fetch must be a fetch block or false; true does not describe how to fetch",
					label,
				)
				return
			}
			if !hasReason {
				failf("%s.fetch is false but %s.fetch_skip_reason is missing", label, label)
				return
			}
			requireNonEmptyString(reason, label+".fetch_skip_reason")
			return
		}
		validateFetch(fetch, label+".fetch")
	}
	if hasReason {
		failf("%s.fetch_skip_reason is only valid with \"fetch\": false", label)
	}
}

// ValidateRegistry ports validateRegistry from
// the original implementation.
func ValidateRegistry(value any, source string) (data JsonObject, err error) {
	defer recoverMetadataError(&err)
	return validateRegistry(value, source), nil
}

// loadRegistry ports loadRegistry from the original implementation.
// packNames nil means "no restriction" (every manifest); see the
// validateSharedDependencies doc comment in packs.go for the same
// nil-versus-empty convention.
func loadRegistry(metadata PackMetadata, packNames []string) LoadedRegistry {
	var selected map[string]struct{}
	if packNames != nil {
		selected = make(map[string]struct{}, len(packNames))
		for _, name := range packNames {
			selected[name] = struct{}{}
		}
	}
	entries := make(map[string]JsonObject)
	sources := make(map[string]string)
	for _, manifest := range metadata.Manifests {
		if selected != nil {
			if _, ok := selected[manifest.Name]; !ok {
				continue
			}
		}
		registryPath := filepath.Join(manifest.Directory, "registry.json")
		if !isFile(registryPath) {
			continue
		}
		registry := validateRegistry(readJSON(registryPath, readJSONOptions{
			preserveNumericTokensUnderKeys: fetchQueryKeys,
		}), registryPath)
		for _, resourceType := range sortedKeys(registry) {
			if prior, ok := sources[resourceType]; ok {
				failf(
					"%s: duplicate resource type %s already loaded from %s",
					registryPath, jsonQuote(resourceType), prior,
				)
			}
			entryValue := registry[resourceType]
			entry, ok := entryValue.(JsonObject)
			if !ok {
				failf("%s.%s must be an object", registryPath, resourceType)
				continue
			}
			entries[resourceType] = entry
			sources[resourceType] = registryPath
		}
	}
	return LoadedRegistry{Entries: entries, Sources: sources}
}

func firstNonEmpty(candidate, fallback string) string {
	if candidate != "" {
		return candidate
	}
	return fallback
}

// validateUnsupportedProviderScopes ports
// validateUnsupportedProviderScopes from the original implementation.
func validateUnsupportedProviderScopes(metadata PackMetadata, registry LoadedRegistry) {
	for _, resourceType := range sortedMapKeys(registry.Entries) {
		entry := registry.Entries[resourceType]
		var rules []any
		if adopt, ok := entry["adopt"].(JsonObject); ok {
			if r, ok := adopt["unsupported_if"].([]any); ok {
				rules = r
			}
		}
		if len(rules) == 0 {
			continue
		}
		provider := ProviderForResource(metadata, resourceType)
		owner := manifestForProvider(metadata, provider)
		expectedSource := owner.ProviderSources[provider]
		expectedVersion, _ := owner.Data["pin"].(string)
		for index, rawRule := range rules {
			rule, ok := rawRule.(JsonObject)
			if !ok {
				continue
			}
			providerRule, ok := rule["provider"].(JsonObject)
			if !ok {
				continue
			}
			label := fmt.Sprintf(
				"%s.%s.adopt.unsupported_if[%d].provider",
				firstNonEmpty(registry.Sources[resourceType], resourceType), resourceType, index,
			)
			sourceValue, _ := providerRule["source"].(string)
			if sourceValue != expectedSource {
				failf(
					"%s.source %s does not match active provider source %s",
					label, jsonQuote(sourceValue), jsonQuote(expectedSource),
				)
			}
			versionValue, _ := providerRule["version"].(string)
			if versionValue != expectedVersion {
				failf(
					"%s.version %s does not match active provider pin %s",
					label, jsonQuote(versionValue), jsonQuote(expectedVersion),
				)
			}
		}
	}
}

// validateOverride ports validateOverride from
// the original implementation.
func validateOverride(value any, source string) JsonObject {
	data := requireObject(value, "override metadata in "+source)
	unknown := make([]string, 0)
	for key := range data {
		if _, ok := overrideKeys[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	if sorted := canonjson.SortedStrings(unknown); len(sorted) > 0 {
		failf("unknown override key %s in %s", sorted[0], source)
	}
	validateModuleSingleBlocks(data, source)
	skipFields := validateSkipMatchers(data, source)
	validateSkipRenameConflicts(data, source, skipFields)
	return data
}

// validateModuleSingleBlocks validates the generator-only escape hatch used
// when a provider's executable behavior still treats a schema-declared list or
// set block as a singleton. Paths are dotted nested-block names; existence and
// compatible nesting are checked against the exact schema by modulesgen.
func validateModuleSingleBlocks(data JsonObject, source string) {
	raw, exists := data["module_single_blocks"]
	if !exists {
		return
	}
	paths, ok := raw.([]any)
	if !ok {
		failf("%s.module_single_blocks must be an array", source)
	}
	if len(paths) == 0 {
		failf("%s.module_single_blocks must not be empty", source)
	}
	seen := make(map[string]struct{}, len(paths))
	for index, value := range paths {
		path, ok := value.(string)
		if !ok || path == "" {
			failf("%s.module_single_blocks[%d] must be a non-empty dotted block path", source, index)
		}
		for _, segment := range strings.Split(path, ".") {
			if !canonicalResourceType.MatchString(segment) {
				failf("%s.module_single_blocks[%d] must be a canonical dotted block path", source, index)
			}
		}
		if _, duplicate := seen[path]; duplicate {
			failf("%s.module_single_blocks contains duplicate path %s", source, jsonQuote(path))
		}
		seen[path] = struct{}{}
	}
}

// ValidateOverride ports validateOverride from
// the original implementation.
func ValidateOverride(value any, source string) (data JsonObject, err error) {
	defer recoverMetadataError(&err)
	return validateOverride(value, source), nil
}

// loadOverrides ports loadOverrides from the original implementation.
func loadOverrides(metadata PackMetadata, packNames []string) LoadedOverrides {
	var selected map[string]struct{}
	if packNames != nil {
		selected = make(map[string]struct{}, len(packNames))
		for _, name := range packNames {
			selected[name] = struct{}{}
		}
	}
	entries := make(map[string]JsonObject)
	sources := make(map[string]string)
	for _, manifest := range metadata.Manifests {
		if selected != nil {
			if _, ok := selected[manifest.Name]; !ok {
				continue
			}
		}
		overridesDirectory := filepath.Join(manifest.Directory, "overrides")
		dirEntries, err := os.ReadDir(overridesDirectory)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			propagateFilesystemError(err)
		}
		var candidates []string
		for _, dirEntry := range dirEntries {
			if strings.HasSuffix(dirEntry.Name(), ".json") {
				candidates = append(candidates, dirEntry.Name())
			}
		}
		var names []string
		for _, name := range candidates {
			if isFile(filepath.Join(overridesDirectory, name)) {
				names = append(names, name)
			}
		}
		names = canonjson.SortedStrings(names)
		for _, name := range names {
			overridePath := filepath.Join(overridesDirectory, name)
			resourceType := strings.TrimSuffix(name, ".json")
			if prior, ok := sources[resourceType]; ok {
				failf(
					"%s: duplicate override resource type %s already loaded from %s",
					overridePath, jsonQuote(resourceType), prior,
				)
			}
			entries[resourceType] = validateOverride(
				readJSON(overridePath, readJSONOptions{preserveNumericTokens: true}), overridePath,
			)
			sources[resourceType] = overridePath
		}
	}
	return LoadedOverrides{Entries: entries, Sources: sources}
}

func providerSchemaPath(metadata PackMetadata, provider string) string {
	return filepath.Join(manifestForProvider(metadata, provider).Directory, "schemas", "provider", provider+".json")
}

func loadProviderSchema(metadata PackMetadata, provider string) ProviderSchema {
	schemaPath := providerSchemaPath(metadata, provider)
	data := requireObject(readJSON(schemaPath, readJSONOptions{}), schemaPath)
	rawResources, ok := data["resource_schemas"].(JsonObject)
	if !ok {
		failf("%s.resource_schemas must be an object", schemaPath)
		return ProviderSchema{}
	}
	resourceSchemas := make(map[string]JsonObject, len(rawResources))
	for _, resourceType := range sortedKeys(rawResources) {
		schema, ok := rawResources[resourceType].(JsonObject)
		if !ok {
			failf("%s.resource_schemas.%s must be an object", schemaPath, resourceType)
			continue
		}
		resourceSchemas[resourceType] = schema
	}
	return ProviderSchema{Provider: provider, Path: schemaPath, Data: data, ResourceSchemas: resourceSchemas}
}

// LoadProviderSchema ports loadProviderSchema from
// the original implementation.
func LoadProviderSchema(metadata PackMetadata, provider string) (schema ProviderSchema, err error) {
	defer recoverMetadataError(&err)
	return loadProviderSchema(metadata, provider), nil
}

func loadResourceSchema(metadata PackMetadata, resourceType string) JsonObject {
	provider := ProviderForResource(metadata, resourceType)
	schema := loadProviderSchema(metadata, provider)
	resource, ok := schema.ResourceSchemas[resourceType]
	if !ok {
		failf("resource type %s not in %s schema", jsonQuote(resourceType), provider)
		return nil
	}
	return resource
}

// LoadResourceSchema ports loadResourceSchema from
// the original implementation.
func LoadResourceSchema(metadata PackMetadata, resourceType string) (schema JsonObject, err error) {
	defer recoverMetadataError(&err)
	return loadResourceSchema(metadata, resourceType), nil
}

func loadResourceMainOverride(metadata PackMetadata, resourceType string) (*string, error) {
	provider := ProviderForResource(metadata, resourceType)
	overridePath := filepath.Join(manifestForProvider(metadata, provider).Directory, "overrides", resourceType, "main.tf")
	return readOptionalUtf8(overridePath, resourceType+" main.tf override")
}

// LoadResourceMainOverride ports loadResourceMainOverride from
// the original implementation.
func LoadResourceMainOverride(metadata PackMetadata, resourceType string) (content *string, err error) {
	defer recoverMetadataError(&err)
	return loadResourceMainOverride(metadata, resourceType)
}

func validatePackResources(metadata PackMetadata, packNames []string) (LoadedRegistry, LoadedOverrides) {
	registry := loadRegistry(metadata, packNames)
	overrides := loadOverrides(metadata, packNames)
	validateUnsupportedProviderScopes(metadata, registry)
	return registry, overrides
}

// ValidatePackResources ports validatePackResources from
// the original implementation.
func ValidatePackResources(metadata PackMetadata, packNames []string) (registry LoadedRegistry, overrides LoadedOverrides, err error) {
	defer recoverMetadataError(&err)
	registry, overrides = validatePackResources(metadata, packNames)
	return registry, overrides, nil
}
