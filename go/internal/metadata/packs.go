package metadata

// packs.go ports the original implementation: pack.json/registry.json
// pack-set validation, provider-prefix ownership, manifest loading, and
// pack-set/profile checks.
//
// Every exported function here is a thin (defer recoverMetadataError(&err))
// wrapper around an unexported function of the same name (see the
// fail/recoverMetadataError doc comments in validation.go for why): the
// unexported function holds the actual ported logic and panics on
// validation failure exactly like the Node source's `throw`; any other
// package-private function in this port that needs the same operation
// calls the unexported version directly, so panics propagate naturally up
// to whichever exported entry point is on the call stack, never through
// more than one recover.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// PACK_SET_KIND, REQUIREMENTS_KIND, and PACK_SET_VERSION port the
// like-named constants from the original implementation.
const (
	PackSetKind      = "infrawright.pack-set"
	RequirementsKind = "infrawright.pack-requirements"
	PackSetVersion   = 1
)

// componentName ports COMPONENT_NAME from the original implementation.
var componentName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

var packSetKeys = stringSet("kind", "version", "packs", "shared")

var manifestKeys = stringSet(
	"absent_defaults", "drift_policy", "dynamic_schema", "lookup_sources",
	"pin", "provider_config", "provider_prefixes", "provider_sources",
	"references", "requires_shared", "scope_segments", "sensitive_required",
	"unescape_products", "vendor",
)

var manifestObjectKeys = []string{
	"absent_defaults", "drift_policy", "dynamic_schema", "lookup_sources",
	"provider_config", "provider_prefixes", "provider_sources",
	"references", "scope_segments", "sensitive_required",
}

var manifestStringKeys = []string{"pin", "vendor"}
var manifestListKeys = []string{"requires_shared", "unescape_products"}

// PackSelection ports the PackSelection interface from
// the original implementation.
type PackSelection struct {
	Packs  []string
	Shared []string
}

// PackSetDocument ports the PackSetDocument interface from
// the original implementation.
type PackSetDocument struct {
	Kind    string
	Version int
	PackSelection
}

// PackManifest ports the PackManifest interface from
// the original implementation.
type PackManifest struct {
	Name             string
	Directory        string
	Path             string
	Data             JsonObject
	ProviderPrefixes map[string]string
	ProviderSources  map[string]string
	RequiresShared   []string
}

// PackMetadata ports the PackMetadata interface from
// the original implementation.
type PackMetadata struct {
	Root             string
	Manifests        []PackManifest
	ProviderPrefixes map[string]string
	ProviderSources  map[string]string
	ProviderOwners   map[string]string
}

// ActivePackSetResult ports the ActivePackSetResult interface from
// the original implementation.
type ActivePackSetResult struct {
	Profile  PackSetDocument
	Active   PackSelection
	Metadata PackMetadata
}

// RequirementsResult ports the RequirementsResult interface from
// the original implementation.
type RequirementsResult struct {
	Requirements PackSetDocument
	Active       PackSelection
	Missing      PackSelection
	Available    bool
}

func isDirectory(candidate string) bool {
	info, err := os.Stat(candidate)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func isFile(candidate string) bool {
	info, err := os.Stat(candidate)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// setKeys returns set's members as a plain slice, in no particular order.
func setKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}

// orEmptyObject ports a JavaScript `value ?? {}` nullish-coalesce: a
// missing map key (Go's zero `any`, nil) and an explicit JSON null both
// decode to Go nil here, exactly the two cases `??` treats as absent.
func orEmptyObject(value any) any {
	if value == nil {
		return JsonObject{}
	}
	return value
}

// validateNames ports validateNames from the original implementation.
func validateNames(value any, label string) []string {
	arr, ok := value.([]any)
	if !ok {
		failf("%s must be a list", label)
		return nil
	}
	names := make([]string, 0, len(arr))
	seen := make(map[string]struct{}, len(arr))
	for index, item := range arr {
		s, isString := item.(string)
		if !isString || !componentName.MatchString(s) {
			failf("%s[%d] must be a lowercase pack name", label, index)
		}
		if _, duplicate := seen[s]; duplicate {
			failf("%s duplicates %s", label, jsonQuote(s))
		}
		seen[s] = struct{}{}
		names = append(names, s)
	}
	if !canonjson.SameStringSequence(names, canonjson.SortedStrings(names)) {
		failf("%s must be sorted", label)
	}
	return names
}

// isPackSetVersionOne reports whether value is the pack-set version 1,
// accepting either a plain float64 1 or a losslessly preserved json.Number
// token "1" -- the original implementation's own version check explicitly
// special-cases a LosslessNumber here (`data.version instanceof
// LosslessNumber && data.version.toString() === "1"`), unlike
// isDriftPolicyVersionOne's bare `!== 1` in driftpolicy.go.
func isPackSetVersionOne(value any) bool {
	switch v := value.(type) {
	case float64:
		return v == float64(PackSetVersion)
	case json.Number:
		return string(v) == "1"
	default:
		return false
	}
}

// validatePackSetDocument ports validatePackSetDocument from
// the original implementation.
func validatePackSetDocument(value any, source, expectedKind string) PackSetDocument {
	data := requireObject(value, source)
	rejectUnknownKeys(data, packSetKeys, source)
	requireKeys(data, packSetKeys, source)
	if kind, ok := data["kind"].(string); !ok || kind != expectedKind {
		failf("%s.kind must be %s", source, jsonQuote(expectedKind))
	}
	if !isPackSetVersionOne(data["version"]) {
		failf("%s.version must be %d", source, PackSetVersion)
	}
	return PackSetDocument{
		Kind:    expectedKind,
		Version: PackSetVersion,
		PackSelection: PackSelection{
			Packs:  validateNames(data["packs"], source+".packs"),
			Shared: validateNames(data["shared"], source+".shared"),
		},
	}
}

// ValidatePackSetDocument ports validatePackSetDocument from
// the original implementation.
func ValidatePackSetDocument(value any, source, expectedKind string) (doc PackSetDocument, err error) {
	defer recoverMetadataError(&err)
	return validatePackSetDocument(value, source, expectedKind), nil
}

// loadPackSetDocument ports loadPackSetDocument from
// the original implementation.
func loadPackSetDocument(source, expectedKind string) PackSetDocument {
	absolute, err := filepath.Abs(source)
	if err != nil {
		failf("failed to resolve %s: %s", source, err.Error())
	}
	value := readJSON(absolute, readJSONOptions{preserveNumericTokens: true})
	return validatePackSetDocument(value, absolute, expectedKind)
}

// LoadPackSetDocument ports loadPackSetDocument from
// the original implementation.
func LoadPackSetDocument(source, expectedKind string) (doc PackSetDocument, err error) {
	defer recoverMetadataError(&err)
	return loadPackSetDocument(source, expectedKind), nil
}

// discoverDirectories ports discoverDirectories from
// the original implementation. A missing root returns a non-nil empty slice;
// every other readdir failure propagates as the raw Go filesystem error.
func discoverDirectories(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}
		}
		propagateFilesystemError(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if isDirectory(filepath.Join(root, entry.Name())) {
			names = append(names, entry.Name())
		}
	}
	return canonjson.SortedStrings(names)
}

// activePackSelection ports activePackSelection from
// the original implementation.
func activePackSelection(root string) PackSelection {
	absolute, err := filepath.Abs(root)
	if err != nil {
		failf("failed to resolve %s: %s", root, err.Error())
	}
	directories := discoverDirectories(absolute)
	packs := make([]string, 0, len(directories))
	for _, name := range directories {
		if name != "_shared" {
			packs = append(packs, name)
		}
	}
	sharedRoot := filepath.Join(absolute, "_shared")
	return PackSelection{Packs: packs, Shared: discoverDirectories(sharedRoot)}
}

// ActivePackSelection ports activePackSelection from
// the original implementation.
func ActivePackSelection(root string) (selection PackSelection, err error) {
	defer recoverMetadataError(&err)
	return activePackSelection(root), nil
}

func requireObjectKey(data JsonObject, key, source string) {
	if value, ok := data[key]; ok {
		if !isObject(value) {
			failf("%s.%s must be an object", source, key)
		}
	}
}

func validateRuleGroup(data JsonObject, key, source string) {
	groupValue, ok := data[key]
	if !ok {
		return
	}
	group, ok := groupValue.(JsonObject)
	if !ok {
		failf("%s.%s must be an object", source, key)
		return
	}
	rulesKeySet := stringSet("rules")
	label := fmt.Sprintf("%s.%s", source, key)
	rejectUnknownKeys(group, rulesKeySet, label)
	requireKeys(group, rulesKeySet, label)
	if _, isArray := group["rules"].([]any); !isArray {
		failf("%s.rules must be a list", label)
	}
}

func validateLookupSources(value JsonObject, source string) {
	for _, resourceType := range sortedKeys(value) {
		item := value[resourceType]
		if len(resourceType) == 0 {
			failf("%s keys must be non-empty strings", source)
		}
		itemObj, ok := item.(JsonObject)
		if !ok {
			failf("%s.%s must be an object", source, resourceType)
			continue
		}
		label := fmt.Sprintf("%s.%s", source, resourceType)
		keys := stringSet("name_field")
		rejectUnknownKeys(itemObj, keys, label)
		requireKeys(itemObj, keys, label)
		requireNonEmptyString(itemObj["name_field"], label+".name_field")
	}
}

func validateReferences(value JsonObject, source string) {
	for _, resourceType := range sortedKeys(value) {
		rawFields := value[resourceType]
		if len(resourceType) == 0 {
			failf("%s keys must be non-empty strings", source)
		}
		fieldsObj, ok := rawFields.(JsonObject)
		if !ok {
			failf("%s.%s must be an object", source, resourceType)
			continue
		}
		for _, field := range sortedKeys(fieldsObj) {
			rawReference := fieldsObj[field]
			if len(field) == 0 {
				failf("%s.%s keys must be non-empty strings", source, resourceType)
			}
			label := fmt.Sprintf("%s.%s.%s", source, resourceType, field)
			referenceObj, ok := rawReference.(JsonObject)
			if !ok {
				failf("%s must be an object", label)
				continue
			}
			keys := stringSet("name_field", "referent")
			rejectUnknownKeys(referenceObj, keys, label)
			requireKeys(referenceObj, keys, label)
			requireNonEmptyString(referenceObj["name_field"], label+".name_field")
			requireNonEmptyString(referenceObj["referent"], label+".referent")
		}
	}
}

// validateDeclaredReferenceCycles rejects cycles in the directed graph formed
// by references.<referrer>.<field>.referent declarations. Structural
// validation runs before this helper, so its defensive type assertions are
// only a guard against future callers passing partially validated metadata.
//
// The traversal deliberately uses canonjson's Python-code-point ordering for
// both roots and neighbours. A DFS back-edge therefore reports one stable
// first cycle, and the stack slice gives the complete closed path rather than
// merely the edge that completed it.
func validateDeclaredReferenceCycles(manifests []JsonObject) {
	// Pack reference tables merge per resource type and field. Manifest order
	// is load-bearing: a later declaration of the same referrer+field replaces
	// the earlier declaration, matching MergedTransformReferences. Build that
	// effective table before deriving edges so shadowed declarations cannot
	// create false cycles.
	effective := make(map[string]map[string]string)
	for _, manifest := range manifests {
		references, ok := manifest["references"].(JsonObject)
		if !ok {
			continue
		}
		for _, referrer := range sortedKeys(references) {
			fields, ok := references[referrer].(JsonObject)
			if !ok {
				continue
			}
			if effective[referrer] == nil {
				effective[referrer] = make(map[string]string)
			}
			for _, field := range sortedKeys(fields) {
				reference, ok := fields[field].(JsonObject)
				if !ok {
					continue
				}
				referent, ok := reference["referent"].(string)
				if !ok || referent == "" {
					continue
				}
				effective[referrer][field] = referent
			}
		}
	}

	graph := make(map[string]map[string]struct{})
	for _, referrer := range sortedMapKeys(effective) {
		if _, exists := graph[referrer]; !exists {
			graph[referrer] = nil
		}
		for _, field := range sortedMapKeys(effective[referrer]) {
			referent := effective[referrer][field]
			if graph[referrer] == nil {
				graph[referrer] = make(map[string]struct{})
			}
			graph[referrer][referent] = struct{}{}
			if _, exists := graph[referent]; !exists {
				graph[referent] = nil
			}
		}
	}

	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(graph))
	stack := make([]string, 0, len(graph))
	stackIndex := make(map[string]int, len(graph))
	var visit func(string)
	visit = func(referrer string) {
		state[referrer] = visiting
		stackIndex[referrer] = len(stack)
		stack = append(stack, referrer)

		for _, referent := range canonjson.SortedStrings(setKeys(graph[referrer])) {
			switch state[referent] {
			case unvisited:
				visit(referent)
			case visiting:
				cycle := append([]string(nil), stack[stackIndex[referent]:]...)
				cycle = append(cycle, referent)
				failf(
					"declared reference cycle: %s; resolve one direction via a literal ID or operator expression",
					strings.Join(cycle, " -> "),
				)
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackIndex, referrer)
		state[referrer] = visited
	}

	for _, resourceType := range sortedMapKeys(graph) {
		if state[resourceType] == unvisited {
			visit(resourceType)
		}
	}
}

func validateProviderConfig(value JsonObject, source string) {
	keys := stringSet("requirements")
	rejectUnknownKeys(value, keys, source)
	requireKeys(value, keys, source)
	if _, isArray := value["requirements"].([]any); !isArray {
		failf("%s.requirements must be a list", source)
	}
}

// validatePackManifestStructure validates one pack manifest without applying
// aggregate semantic checks. loadPackMetadata uses this form because a cycle
// in one provisional reference table can be removed by a later manifest's
// same-referrer+field overwrite.
func validatePackManifestStructure(value any, source string) JsonObject {
	data := requireObject(value, source)
	rejectUnknownKeys(data, manifestKeys, source)
	for _, key := range manifestObjectKeys {
		requireObjectKey(data, key, source)
	}
	for _, key := range manifestStringKeys {
		if v, ok := data[key]; ok {
			requireNonEmptyString(v, fmt.Sprintf("%s.%s", source, key))
		}
	}
	for _, key := range manifestListKeys {
		if v, ok := data[key]; ok {
			if _, isArray := v.([]any); !isArray {
				failf("%s.%s must be a list", source, key)
			}
		}
	}
	validateStringMap(orEmptyObject(data["provider_prefixes"]), source+".provider_prefixes")
	validateStringMap(orEmptyObject(data["provider_sources"]), source+".provider_sources")
	validateStringMap(orEmptyObject(data["scope_segments"]), source+".scope_segments")
	if arr, ok := data["unescape_products"].([]any); ok {
		for index, item := range arr {
			requireNonEmptyString(item, fmt.Sprintf("%s.unescape_products[%d]", source, index))
		}
	}
	if arr, ok := data["requires_shared"].([]any); ok {
		dependencies := make([]string, 0, len(arr))
		seen := make(map[string]struct{}, len(arr))
		for index, item := range arr {
			s, isString := item.(string)
			if !isString || !componentName.MatchString(s) {
				failf("%s.requires_shared[%d] must be a lowercase shared-component name", source, index)
			}
			if _, duplicate := seen[s]; duplicate {
				failf("%s.requires_shared duplicates %s", source, jsonQuote(s))
			}
			seen[s] = struct{}{}
			dependencies = append(dependencies, s)
		}
		if !canonjson.SameStringSequence(dependencies, canonjson.SortedStrings(dependencies)) {
			failf("%s.requires_shared must be sorted", source)
		}
	}
	if lookupSources, ok := data["lookup_sources"].(JsonObject); ok {
		validateLookupSources(lookupSources, source+".lookup_sources")
	}
	if references, ok := data["references"].(JsonObject); ok {
		validateReferences(references, source+".references")
	}
	for _, key := range []string{"absent_defaults", "dynamic_schema", "sensitive_required"} {
		validateRuleGroup(data, key, source)
	}
	if driftPolicyValue, hasDriftPolicy := data["drift_policy"]; hasDriftPolicy {
		if driftErr := validateDriftPolicy(driftPolicyValue, source+".drift_policy"); driftErr != nil {
			fail(driftErr.Error())
		}
	}
	if providerConfig, ok := data["provider_config"].(JsonObject); ok {
		validateProviderConfig(providerConfig, source+".provider_config")
	}
	return data
}

// validatePackManifest ports validatePackManifest from
// the original implementation. A standalone manifest is its own effective
// reference table, so semantic cycle validation follows structural validation
// immediately at this boundary.
func validatePackManifest(value any, source string) JsonObject {
	data := validatePackManifestStructure(value, source)
	validateDeclaredReferenceCycles([]JsonObject{data})
	return data
}

// ValidatePackManifest ports validatePackManifest from
// the original implementation.
func ValidatePackManifest(value any, source string) (data JsonObject, err error) {
	defer recoverMetadataError(&err)
	return validatePackManifest(value, source), nil
}

func manifestRecord(name, directory, manifestPath string, data JsonObject) PackManifest {
	var requiresShared []string
	if arr, ok := data["requires_shared"].([]any); ok {
		requiresShared = make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				requiresShared = append(requiresShared, s)
			}
		}
	}
	return PackManifest{
		Name:      name,
		Directory: directory,
		Path:      manifestPath,
		Data:      data,
		ProviderPrefixes: validateStringMap(
			orEmptyObject(data["provider_prefixes"]), manifestPath+".provider_prefixes",
		),
		ProviderSources: validateStringMap(
			orEmptyObject(data["provider_sources"]), manifestPath+".provider_sources",
		),
		RequiresShared: requiresShared,
	}
}

// loadPackMetadata ports loadPackMetadata from
// the original implementation.
func loadPackMetadata(root string) PackMetadata {
	absolute, err := filepath.Abs(root)
	if err != nil {
		failf("failed to resolve %s: %s", root, err.Error())
	}
	var manifests []PackManifest
	for _, name := range discoverDirectories(absolute) {
		if name == "_shared" {
			continue
		}
		directory := filepath.Join(absolute, name)
		manifestPath := filepath.Join(directory, "pack.json")
		if !isFile(manifestPath) {
			continue
		}
		data := validatePackManifestStructure(readJSON(manifestPath, readJSONOptions{
			preserveNumericTokensUnderKeys: stringSet("observed_value", "value"),
		}), manifestPath)
		manifests = append(manifests, manifestRecord(name, directory, manifestPath, data))
	}
	manifestData := make([]JsonObject, 0, len(manifests))
	for _, manifest := range manifests {
		manifestData = append(manifestData, manifest.Data)
	}
	validateDeclaredReferenceCycles(manifestData)

	prefixes := make(map[string]string)
	sources := make(map[string]string)
	providerOwners := make(map[string]string)
	prefixOwners := make(map[string]string)
	for _, manifest := range manifests {
		for _, prefix := range canonjson.SortedStrings(setKeys(toStringSet(manifest.ProviderPrefixes))) {
			if prior, ok := prefixOwners[prefix]; ok && prior != manifest.Name {
				failf(
					"provider prefix %s is declared by multiple packs: %s, %s",
					jsonQuote(prefix), prior, manifest.Name,
				)
			}
			provider, ok := manifest.ProviderPrefixes[prefix]
			if !ok {
				continue
			}
			prefixOwners[prefix] = manifest.Name
			prefixes[prefix] = provider
			if providerPrior, ok := providerOwners[provider]; ok && providerPrior != manifest.Name {
				failf(
					"provider %s is declared by multiple packs: %s, %s",
					jsonQuote(provider), providerPrior, manifest.Name,
				)
			}
			providerOwners[provider] = manifest.Name
		}
		for key, value := range manifest.ProviderSources {
			sources[key] = value
		}
	}
	return PackMetadata{
		Root:             absolute,
		Manifests:        manifests,
		ProviderPrefixes: prefixes,
		ProviderSources:  sources,
		ProviderOwners:   providerOwners,
	}
}

func toStringSet(m map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for key := range m {
		out[key] = struct{}{}
	}
	return out
}

// LoadPackMetadata ports loadPackMetadata from
// the original implementation.
func LoadPackMetadata(root string) (metadata PackMetadata, err error) {
	defer recoverMetadataError(&err)
	return loadPackMetadata(root), nil
}

// validateSharedDependencies ports validateSharedDependencies from
// the original implementation. packNames nil means "no restriction" (every
// manifest), matching the Node source's `packNames?: readonly string[]`
// left undefined; a non-nil (even empty) slice restricts to exactly those
// pack names, matching a defined (possibly empty) array there.
func validateSharedDependencies(metadata PackMetadata, packNames []string) {
	var selected map[string]struct{}
	if packNames != nil {
		selected = make(map[string]struct{}, len(packNames))
		for _, name := range packNames {
			selected[name] = struct{}{}
		}
	}
	for _, manifest := range metadata.Manifests {
		if selected != nil {
			if _, ok := selected[manifest.Name]; !ok {
				continue
			}
		}
		for _, dependency := range manifest.RequiresShared {
			sharedRoot := filepath.Join(metadata.Root, "_shared")
			if !isDirectory(filepath.Join(sharedRoot, dependency)) {
				failf(
					"pack %s requires missing shared component %s under %s",
					manifest.Name, dependency, sharedRoot,
				)
			}
		}
	}
}

// ValidateSharedDependencies ports validateSharedDependencies from
// the original implementation.
func ValidateSharedDependencies(metadata PackMetadata, packNames []string) (err error) {
	defer recoverMetadataError(&err)
	validateSharedDependencies(metadata, packNames)
	return nil
}

func selectionDelta(expected, actual []string) (missing, extra []string) {
	expectedSet := make(map[string]struct{}, len(expected))
	for _, name := range expected {
		expectedSet[name] = struct{}{}
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, name := range actual {
		actualSet[name] = struct{}{}
	}
	var missingList, extraList []string
	for name := range expectedSet {
		if _, ok := actualSet[name]; !ok {
			missingList = append(missingList, name)
		}
	}
	for name := range actualSet {
		if _, ok := expectedSet[name]; !ok {
			extraList = append(extraList, name)
		}
	}
	return canonjson.SortedStrings(missingList), canonjson.SortedStrings(extraList)
}

// ValidateActivePackSetOptions identifies the exact profile and installed
// pack root to compare.
type ValidateActivePackSetOptions struct {
	ProfilePath string
	Root        string
}

// validateActivePackSet ports validateActivePackSet from
// the original implementation.
func validateActivePackSet(options ValidateActivePackSetOptions) ActivePackSetResult {
	profile := loadPackSetDocument(options.ProfilePath, PackSetKind)
	active := activePackSelection(options.Root)
	packMissing, packExtra := selectionDelta(profile.Packs, active.Packs)
	sharedMissing, sharedExtra := selectionDelta(profile.Shared, active.Shared)
	var errorList []string
	if len(packMissing) > 0 {
		errorList = append(errorList, fmt.Sprintf("missing packs: %s", strings.Join(packMissing, ", ")))
	}
	if len(packExtra) > 0 {
		errorList = append(errorList, fmt.Sprintf("undeclared packs: %s", strings.Join(packExtra, ", ")))
	}
	if len(sharedMissing) > 0 {
		errorList = append(errorList, fmt.Sprintf("missing shared: %s", strings.Join(sharedMissing, ", ")))
	}
	if len(sharedExtra) > 0 {
		errorList = append(errorList, fmt.Sprintf("undeclared shared: %s", strings.Join(sharedExtra, ", ")))
	}
	if len(errorList) > 0 {
		failf("pack set mismatch; %s", strings.Join(errorList, "; "))
	}
	metadata := loadPackMetadata(options.Root)
	validateSharedDependencies(metadata, profile.Packs)
	return ActivePackSetResult{Profile: profile, Active: active, Metadata: metadata}
}

// ValidateActivePackSet ports validateActivePackSet from
// the original implementation.
func ValidateActivePackSet(options ValidateActivePackSetOptions) (result ActivePackSetResult, err error) {
	defer recoverMetadataError(&err)
	return validateActivePackSet(options), nil
}

// CheckPackRequirementsOptions ports the options bag
// checkPackRequirements accepts in the original implementation.
type CheckPackRequirementsOptions struct {
	RequirementsPath string
	Root             string
}

// checkPackRequirements ports checkPackRequirements from
// the original implementation.
func checkPackRequirements(options CheckPackRequirementsOptions) RequirementsResult {
	requirements := loadPackSetDocument(options.RequirementsPath, RequirementsKind)
	active := activePackSelection(options.Root)
	activePacks := make(map[string]struct{}, len(active.Packs))
	for _, name := range active.Packs {
		activePacks[name] = struct{}{}
	}
	activeShared := make(map[string]struct{}, len(active.Shared))
	for _, name := range active.Shared {
		activeShared[name] = struct{}{}
	}
	missingPacks := make([]string, 0)
	for _, name := range requirements.Packs {
		if _, ok := activePacks[name]; !ok {
			missingPacks = append(missingPacks, name)
		}
	}
	missingShared := make([]string, 0)
	for _, name := range requirements.Shared {
		if _, ok := activeShared[name]; !ok {
			missingShared = append(missingShared, name)
		}
	}
	return RequirementsResult{
		Requirements: requirements,
		Active:       active,
		Missing:      PackSelection{Packs: missingPacks, Shared: missingShared},
		Available:    len(missingPacks) == 0 && len(missingShared) == 0,
	}
}

// CheckPackRequirements ports checkPackRequirements from
// the original implementation.
func CheckPackRequirements(options CheckPackRequirementsOptions) (result RequirementsResult, err error) {
	defer recoverMetadataError(&err)
	return checkPackRequirements(options), nil
}

// ProviderForResource ports providerForResource from
// the original implementation. It never fails: an unrecognized resource
// type falls back to its own leading `_`-delimited segment, exactly as
// the Node source does.
func ProviderForResource(metadata PackMetadata, resourceType string) string {
	prefixes := canonjson.SortedStrings(setKeys(toStringSet(metadata.ProviderPrefixes)))
	sort.SliceStable(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })
	for _, prefix := range prefixes {
		if strings.HasPrefix(resourceType, prefix) {
			if provider, ok := metadata.ProviderPrefixes[prefix]; ok {
				return provider
			}
		}
	}
	if index := strings.IndexByte(resourceType, '_'); index >= 0 {
		return resourceType[:index]
	}
	return resourceType
}

func manifestForProvider(metadata PackMetadata, provider string) PackManifest {
	owner, ok := metadata.ProviderOwners[provider]
	if !ok {
		failf("no pack declares provider %s", jsonQuote(provider))
		return PackManifest{}
	}
	for _, manifest := range metadata.Manifests {
		if manifest.Name == owner {
			return manifest
		}
	}
	failf("no manifest found for provider owner %s", jsonQuote(owner))
	return PackManifest{}
}

// ManifestForProvider ports manifestForProvider from
// the original implementation.
func ManifestForProvider(metadata PackMetadata, provider string) (manifest PackManifest, err error) {
	defer recoverMetadataError(&err)
	return manifestForProvider(metadata, provider), nil
}

func packDirectoryForProvider(metadata PackMetadata, provider string) string {
	return manifestForProvider(metadata, provider).Directory
}

// PackDirectoryForProvider ports packDirectoryForProvider from
// the original implementation.
func PackDirectoryForProvider(metadata PackMetadata, provider string) (directory string, err error) {
	defer recoverMetadataError(&err)
	return packDirectoryForProvider(metadata, provider), nil
}

// ValidatePackAuthoringOptions ports the options bag
// validatePackAuthoring accepts in the original implementation. Pack nil
// means the optional `pack` field was omitted (validate every manifest).
type ValidatePackAuthoringOptions struct {
	Root string
	Pack *string
}

// ValidatePackAuthoringResult ports validatePackAuthoring's return shape
// from the original implementation.
type ValidatePackAuthoringResult struct {
	Names    []string
	Metadata PackMetadata
}

func validatePackAuthoring(options ValidatePackAuthoringOptions) ValidatePackAuthoringResult {
	root, err := filepath.Abs(options.Root)
	if err != nil {
		failf("failed to resolve %s: %s", options.Root, err.Error())
	}
	if options.Pack != nil && *options.Pack == "_shared" {
		fail("_shared is a reserved component root, not a pack")
	}
	metadata := loadPackMetadata(root)
	var names []string
	if options.Pack == nil {
		names = make([]string, 0, len(metadata.Manifests))
		for _, manifest := range metadata.Manifests {
			names = append(names, manifest.Name)
		}
	} else {
		names = []string{*options.Pack}
		found := false
		for _, manifest := range metadata.Manifests {
			if manifest.Name == *options.Pack {
				found = true
				break
			}
		}
		if !found {
			failf("unknown pack %s under %s", jsonQuote(*options.Pack), root)
		}
	}
	validateSharedDependencies(metadata, names)
	return ValidatePackAuthoringResult{Names: names, Metadata: metadata}
}

// ValidatePackAuthoring ports validatePackAuthoring from
// the original implementation.
func ValidatePackAuthoring(options ValidatePackAuthoringOptions) (result ValidatePackAuthoringResult, err error) {
	defer recoverMetadataError(&err)
	return validatePackAuthoring(options), nil
}
