package providerprobe

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The three schemas are verbatim assets from @apidevtools/openapi-schemas
// 2.1.0. Their hashes, license, and the exact Node validator source hashes
// are recorded beside the assets in openapi_schemas/PROVENANCE.md.
//
//go:embed openapi_schemas/v2.0/schema.json
var legacySwagger2Schema []byte

//go:embed openapi_schemas/v3.0/schema.json
var legacyOpenAPI30Schema []byte

//go:embed openapi_schemas/v3.1/schema.json
var legacyOpenAPI31Schema []byte

const (
	legacyValidationMaxDepth = 512
	legacyValidationMaxNodes = 100000
)

var (
	legacyOpenAPIValidators     legacyValidatorSet
	legacyOpenAPIValidatorsOnce sync.Once
	legacyOpenAPIValidatorsErr  error
	legacyPlaceholderRE         = regexp.MustCompile(`\{([^/}]+)}`)
)

type legacyValidatorSet struct {
	swagger2  *jsonschema.Schema
	openAPI30 *jsonschema.Schema
	openAPI31 *jsonschema.Schema
}

// validateLegacyOpenAPI ports the validation sequence in
// node-src/authoring/openapi.ts: SwaggerParser.validate first dereferences
// local references with external resolution disabled, then validates the
// selected schema, and finally performs swagger-parser's Swagger 2-only
// supplemental checks. It intentionally has no qualified-v2 callers.
func validateLegacyOpenAPI(document map[string]any) error {
	graph, err := legacyValidationGraph(document)
	if err != nil {
		return legacyValidationError("deref", err)
	}
	root, ok := graph.(map[string]any)
	if !ok {
		return legacyValidationError("version", fmt.Errorf("OpenAPI root must be an object"))
	}
	kind, err := legacyOpenAPIKind(root)
	if err != nil {
		return legacyValidationError("version", err)
	}
	if err := rejectLegacyNativeCycle(root); err != nil {
		return legacyValidationError("deref", err)
	}
	resolved, err := dereferenceLegacyOpenAPI(root)
	if err != nil {
		return legacyValidationError("deref", err)
	}
	validators, err := loadLegacyOpenAPIValidators()
	if err != nil {
		return legacyValidationError("schema", err)
	}
	var validator *jsonschema.Schema
	switch kind {
	case "swagger2":
		validator = validators.swagger2
	case "openapi30":
		validator = validators.openAPI30
	case "openapi31":
		validator = validators.openAPI31
	default:
		return legacyValidationError("version", fmt.Errorf("unsupported OpenAPI version"))
	}
	if err := validator.Validate(resolved); err != nil {
		return legacyValidationError("schema", err)
	}
	if kind == "swagger2" {
		if err := validateLegacySwagger2Spec(resolved); err != nil {
			return legacyValidationError("spec", err)
		}
	}
	return nil
}

func legacyValidationError(phase string, err error) error {
	return fmt.Errorf("legacy OpenAPI validation %s: %w", phase, err)
}

func legacyOpenAPIKind(root map[string]any) (string, error) {
	if swagger := root["swagger"]; legacyJSTruthy(swagger) {
		if _, numeric := swagger.(float64); numeric {
			return "", fmt.Errorf("Swagger version number must be a string")
		}
		info := legacyObject(root["info"])
		if info != nil {
			if _, numeric := info["version"].(float64); numeric {
				return "", fmt.Errorf("API version number must be a string")
			}
		}
		if swagger == "2.0" {
			return "swagger2", nil
		}
		return "", fmt.Errorf("unrecognized Swagger version %v", swagger)
	}
	openapi := root["openapi"]
	version, _ := openapi.(string)
	if _, pathsPresent := root["paths"]; !pathsPresent {
		if _, webhooksPresent := root["webhooks"]; legacyOpenAPI31Version(version) && webhooksPresent {
			return "openapi31", nil
		}
		return "", fmt.Errorf("not a valid OpenAPI definition")
	}
	if _, numeric := openapi.(float64); numeric {
		return "", fmt.Errorf("OpenAPI version number must be a string")
	}
	info := legacyObject(root["info"])
	if info != nil {
		if _, numeric := info["version"].(float64); numeric {
			return "", fmt.Errorf("API version number must be a string")
		}
	}
	if legacyOpenAPI30Version(version) {
		return "openapi30", nil
	}
	if legacyOpenAPI31Version(version) {
		return "openapi31", nil
	}
	return "", fmt.Errorf("unsupported OpenAPI version %q", version)
}

func legacyOpenAPI30Version(version string) bool {
	switch version {
	case "3.0.0", "3.0.1", "3.0.2", "3.0.3", "3.0.4":
		return true
	default:
		return false
	}
}

func legacyOpenAPI31Version(version string) bool {
	switch version {
	case "3.1.0", "3.1.1", "3.1.2":
		return true
	default:
		return false
	}
}

func legacyJSTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0 && !math.IsNaN(typed)
	default:
		return true
	}
}

func loadLegacyOpenAPIValidators() (legacyValidatorSet, error) {
	legacyOpenAPIValidatorsOnce.Do(func() {
		legacyOpenAPIValidators, legacyOpenAPIValidatorsErr = compileLegacyOpenAPIValidators()
	})
	return legacyOpenAPIValidators, legacyOpenAPIValidatorsErr
}

func compileLegacyOpenAPIValidators() (legacyValidatorSet, error) {
	swagger2, err := compileLegacyOpenAPISchema("https://infrawright.invalid/openapi/v2/schema.json", legacySwagger2Schema, jsonschema.Draft4, false)
	if err != nil {
		return legacyValidatorSet{}, fmt.Errorf("compile Swagger 2 schema: %w", err)
	}
	openAPI30, err := compileLegacyOpenAPISchema("https://infrawright.invalid/openapi/v3.0/schema.json", legacyOpenAPI30Schema, jsonschema.Draft4, false)
	if err != nil {
		return legacyValidatorSet{}, fmt.Errorf("compile OpenAPI 3.0 schema: %w", err)
	}
	openAPI31, err := compileLegacyOpenAPISchema("https://infrawright.invalid/openapi/v3.1/schema.json", legacyOpenAPI31Schema, jsonschema.Draft2020, true)
	if err != nil {
		return legacyValidatorSet{}, fmt.Errorf("compile OpenAPI 3.1 schema: %w", err)
	}
	return legacyValidatorSet{swagger2: swagger2, openAPI30: openAPI30, openAPI31: openAPI31}, nil
}

func compileLegacyOpenAPISchema(location string, source []byte, draft *jsonschema.Draft, patch31 bool) (*jsonschema.Schema, error) {
	var schema any
	decoder := json.NewDecoder(strings.NewReader(string(source)))
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil {
		return nil, fmt.Errorf("decode embedded schema: %w", err)
	}
	root, ok := schema.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("embedded schema root is not an object")
	}
	stripLegacySchemaFormats(root)
	if patch31 {
		if err := patchLegacyOpenAPI31DynamicRef(root); err != nil {
			return nil, err
		}
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(draft)
	compiler.UseLoader(legacyRejectSchemaLoader{})
	if err := compiler.AddResource(location, root); err != nil {
		return nil, fmt.Errorf("register embedded schema: %w", err)
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return nil, err
	}
	return compiled, nil
}

type legacyRejectSchemaLoader struct{}

func (legacyRejectSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("embedded OpenAPI schema attempted external resource %q", location)
}

// stripLegacySchemaFormats matches SwaggerParser's Ajv option
// validateFormats:false. jsonschema/v6 treats some older-draft formats as
// assertions. This walks JSON-Schema positions rather than every map key: a
// property named "format" is an OpenAPI field definition and must remain.
func stripLegacySchemaFormats(value any) {
	schema := legacyObject(value)
	if schema == nil {
		return
	}
	delete(schema, "format")
	for _, key := range []string{"additionalItems", "additionalProperties", "contains", "contentSchema", "else", "if", "items", "not", "propertyNames", "then", "unevaluatedItems", "unevaluatedProperties"} {
		stripLegacySchemaValue(schema[key])
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		for _, child := range legacyArray(schema[key]) {
			stripLegacySchemaFormats(child)
		}
	}
	for _, key := range []string{"$defs", "definitions", "dependentSchemas", "patternProperties", "properties"} {
		for _, name := range legacySortedKeys(legacyObject(schema[key])) {
			stripLegacySchemaFormats(legacyObject(schema[key])[name])
		}
	}
	for _, name := range legacySortedKeys(legacyObject(schema["dependencies"])) {
		stripLegacySchemaValue(legacyObject(schema["dependencies"])[name])
	}
}

func stripLegacySchemaValue(value any) {
	if array := legacyArray(value); array != nil {
		for _, child := range array {
			stripLegacySchemaFormats(child)
		}
		return
	}
	stripLegacySchemaFormats(value)
}

// patchLegacyOpenAPI31DynamicRef ports the temporary mutation in
// swagger-parser/lib/validators/schema.js. Ajv 2020 could not evaluate the
// OpenAPI 3.1 schema's dynamic reference arrangement, so the Node validator
// replaces each affected occurrence with the shared schema definition.
func patchLegacyOpenAPI31DynamicRef(root map[string]any) error {
	defs := legacyObject(root["$defs"])
	schema := legacyObject(defs["schema"])
	components := legacyObject(defs["components"])
	header := legacyObject(defs["header"])
	mediaType := legacyObject(defs["media-type"])
	parameter := legacyObject(defs["parameter"])
	if defs == nil || schema == nil || components == nil || header == nil || mediaType == nil || parameter == nil {
		return fmt.Errorf("embedded OpenAPI 3.1 schema lacks SwaggerParser patch targets")
	}
	delete(schema, "$dynamicAnchor")
	componentProperties := legacyObject(components["properties"])
	schemas := legacyObject(componentProperties["schemas"])
	if componentProperties == nil || schemas == nil {
		return fmt.Errorf("embedded OpenAPI 3.1 components patch target is absent")
	}
	schemas["additionalProperties"] = schema
	if err := patchNestedLegacySchema(header, []string{"dependentSchemas", "schema", "properties"}, "schema", schema); err != nil {
		return err
	}
	if err := patchNestedLegacySchema(mediaType, []string{"properties"}, "schema", schema); err != nil {
		return err
	}
	if err := patchNestedLegacySchema(parameter, []string{"properties"}, "schema", schema); err != nil {
		return err
	}
	return nil
}

func patchNestedLegacySchema(root map[string]any, path []string, key string, replacement map[string]any) error {
	current := root
	for _, part := range path {
		current = legacyObject(current[part])
		if current == nil {
			return fmt.Errorf("embedded OpenAPI 3.1 patch target %q is absent", strings.Join(path, "."))
		}
	}
	current[key] = replacement
	return nil
}

// legacyValidationGraph builds the detached native-number graph SwaggerParser
// sees. canonjson keeps every numeric lexeme as json.Number; Node converts it
// through Number(), including signed zero and finite signed-max clamping.
func legacyValidationGraph(value any) (any, error) {
	seen := 0
	maps := make(map[uintptr]map[string]any)
	slices := make(map[uintptr][]any)
	return legacyValidationGraphValue(value, 0, &seen, maps, slices)
}

func legacyValidationGraphValue(value any, depth int, seen *int, maps map[uintptr]map[string]any, slices map[uintptr][]any) (any, error) {
	if depth > legacyValidationMaxDepth {
		return nil, fmt.Errorf("OpenAPI graph exceeds depth limit")
	}
	*seen++
	if *seen > legacyValidationMaxNodes {
		return nil, fmt.Errorf("OpenAPI graph exceeds node limit")
	}
	switch typed := value.(type) {
	case json.Number:
		return legacyValidationNumber(string(typed))
	case map[string]any:
		identity := reflect.ValueOf(typed).Pointer()
		if existing, ok := maps[identity]; ok {
			return existing, nil
		}
		copy := make(map[string]any, len(typed))
		maps[identity] = copy
		for _, key := range legacySortedKeys(typed) {
			child := typed[key]
			converted, err := legacyValidationGraphValue(child, depth+1, seen, maps, slices)
			if err != nil {
				return nil, err
			}
			copy[key] = converted
		}
		return copy, nil
	case []any:
		identity := reflect.ValueOf(typed).Pointer()
		if existing, ok := slices[identity]; ok {
			return existing, nil
		}
		copy := make([]any, len(typed))
		slices[identity] = copy
		for index, child := range typed {
			converted, err := legacyValidationGraphValue(child, depth+1, seen, maps, slices)
			if err != nil {
				return nil, err
			}
			copy[index] = converted
		}
		return copy, nil
	default:
		return value, nil
	}
}

func legacyValidationNumber(token string) (float64, error) {
	number, err := strconv.ParseFloat(token, 64)
	if err != nil {
		var numberError *strconv.NumError
		if !errors.As(err, &numberError) || !errors.Is(numberError.Err, strconv.ErrRange) {
			return 0, fmt.Errorf("convert JSON number %q: %w", token, err)
		}
	}
	if math.IsInf(number, 1) {
		return math.MaxFloat64, nil
	}
	if math.IsInf(number, -1) {
		return -math.MaxFloat64, nil
	}
	return number, nil
}

// rejectLegacyNativeCycle gives the descriptor-free Go graph the same safety
// boundary as SwaggerParser's first dereference pass. Shared subgraphs are
// allowed; only an active object/array edge is rejected. This runs after the
// detached number-converting clone so it cannot mutate the source graph.
func rejectLegacyNativeCycle(value any) error {
	seen := 0
	activeMaps := make(map[uintptr]bool)
	activeSlices := make(map[uintptr]bool)
	var walk func(any, int) error
	walk = func(current any, depth int) error {
		if depth > legacyValidationMaxDepth {
			return fmt.Errorf("OpenAPI graph exceeds depth limit")
		}
		seen++
		if seen > legacyValidationMaxNodes {
			return fmt.Errorf("OpenAPI graph exceeds node limit")
		}
		switch typed := current.(type) {
		case map[string]any:
			identity := reflect.ValueOf(typed).Pointer()
			if activeMaps[identity] {
				return fmt.Errorf("OpenAPI graph contains a native object cycle")
			}
			activeMaps[identity] = true
			defer delete(activeMaps, identity)
			for _, key := range legacySortedKeys(typed) {
				if err := walk(typed[key], depth+1); err != nil {
					return err
				}
			}
		case []any:
			identity := reflect.ValueOf(typed).Pointer()
			if activeSlices[identity] {
				return fmt.Errorf("OpenAPI graph contains a native array cycle")
			}
			activeSlices[identity] = true
			defer delete(activeSlices, identity)
			for _, child := range typed {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(value, 0)
}

// dereferenceLegacyOpenAPI ports the first validate() pass. It resolves only
// local JSON Pointers into a detached graph. URL, file, and relative refs are
// deliberately retained because SwaggerParser receives external/file/http:
// false. A cyclic local chain remains a reference at the cycle edge, matching
// SwaggerParser's circular:"ignore" schema-validation pass.
func dereferenceLegacyOpenAPI(root map[string]any) (map[string]any, error) {
	seen := 0
	resolved, err := dereferenceLegacyOpenAPIValue(root, root, 0, &seen, make(map[string]bool), make(map[string]any))
	if err != nil {
		return nil, err
	}
	object := legacyObject(resolved)
	if object == nil {
		return nil, fmt.Errorf("dereferenced OpenAPI root is not an object")
	}
	return object, nil
}

func dereferenceLegacyOpenAPIValue(root map[string]any, value any, depth int, seen *int, active map[string]bool, cache map[string]any) (any, error) {
	if depth > legacyValidationMaxDepth {
		return nil, fmt.Errorf("OpenAPI reference graph exceeds depth limit")
	}
	*seen++
	if *seen > legacyValidationMaxNodes {
		return nil, fmt.Errorf("OpenAPI reference graph exceeds node limit")
	}
	switch typed := value.(type) {
	case map[string]any:
		if ref, local := typed["$ref"].(string); local && isLegacyAllowedLocalRef(ref) {
			target, err := resolveLegacyJSONPointer(root, ref)
			if err != nil {
				return nil, err
			}
			if active[ref] {
				return cloneLegacyReferenceObject(typed, root, depth, seen, active, cache)
			}
			resolvedTarget, cached := cache[ref]
			if !cached {
				active[ref] = true
				resolvedTarget, err = dereferenceLegacyOpenAPIValue(root, target, depth+1, seen, active, cache)
				delete(active, ref)
				if err != nil {
					return nil, err
				}
			}
			if len(typed) == 1 {
				if !cached {
					// Match ref-parser's plain-reference cache. Repeated refs
					// share one detached target instead of charging the full
					// target graph against the work bound for every use.
					cache[ref] = resolvedTarget
				}
				return resolvedTarget, nil
			}

			// $Ref.dereference extends only truthy JavaScript objects. Maps
			// merge normally; arrays contribute their enumerable numeric keys
			// to a plain object. Primitive, boolean, and null targets replace
			// the reference completely, discarding every sibling.
			switch resolvedTarget.(type) {
			case map[string]any, []any:
				// Extended JavaScript objects merge below.
			default:
				// Primitive targets replace the reference before the walker can
				// observe or dereference any discarded sibling.
				return resolvedTarget, nil
			}

			merged := make(map[string]any, len(typed))
			for _, key := range legacySortedKeys(typed) {
				child := typed[key]
				if key == "$ref" {
					continue
				}
				resolvedChild, err := dereferenceLegacyOpenAPIValue(root, child, depth+1, seen, active, cache)
				if err != nil {
					return nil, err
				}
				merged[key] = resolvedChild
			}
			switch target := resolvedTarget.(type) {
			case map[string]any:
				for _, key := range legacySortedKeys(target) {
					if _, overridden := merged[key]; !overridden {
						merged[key] = target[key]
					}
				}
			case []any:
				for index, child := range target {
					key := strconv.Itoa(index)
					if _, overridden := merged[key]; !overridden {
						merged[key] = child
					}
				}
			}
			return merged, nil
		}
		return cloneLegacyReferenceObject(typed, root, depth, seen, active, cache)
	case []any:
		copy := make([]any, len(typed))
		for index, child := range typed {
			resolved, err := dereferenceLegacyOpenAPIValue(root, child, depth+1, seen, active, cache)
			if err != nil {
				return nil, err
			}
			copy[index] = resolved
		}
		return copy, nil
	default:
		return value, nil
	}
}

func cloneLegacyReferenceObject(value map[string]any, root map[string]any, depth int, seen *int, active map[string]bool, cache map[string]any) (map[string]any, error) {
	copy := make(map[string]any, len(value))
	for _, key := range legacySortedKeys(value) {
		child := value[key]
		resolved, err := dereferenceLegacyOpenAPIValue(root, child, depth+1, seen, active, cache)
		if err != nil {
			return nil, err
		}
		copy[key] = resolved
	}
	return copy, nil
}

func resolveLegacyJSONPointer(root map[string]any, ref string) (any, error) {
	return resolveLegacyJSONPointerState(root, ref, make(map[string]bool), 0)
}

func resolveLegacyJSONPointerState(root map[string]any, ref string, active map[string]bool, depth int) (any, error) {
	if depth > legacyValidationMaxDepth {
		return nil, fmt.Errorf("OpenAPI reference resolution exceeds depth limit")
	}
	if ref == "#" {
		return root, nil
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported local OpenAPI reference %q", ref)
	}
	rawTokens := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	tokens := make([]string, len(rawTokens))
	for index, raw := range rawTokens {
		tokens[index] = legacyDecodePointerToken(raw)
	}
	var current any = root
	for index := 0; index < len(tokens); index++ {
		var err error
		current, err = resolveLegacyPointerIntermediate(root, current, active, depth+1)
		if err != nil {
			return nil, err
		}
		token := tokens[index]
		switch node := current.(type) {
		case map[string]any:
			value, ok := node[token]
			if !ok {
				// json-schema-ref-parser retains this compatibility fallback:
				// when a raw slash split a property name, try the longest
				// remaining token sequence as one key before declaring the
				// pointer missing.
				for end := len(tokens) - 1; end > index; end-- {
					joined := strings.Join(tokens[index:end+1], "/")
					if candidate, exists := node[joined]; exists {
						value, ok, index = candidate, true, end
						break
					}
				}
			}
			if !ok {
				return nil, fmt.Errorf("OpenAPI reference %q does not exist", ref)
			}
			current = value
		case []any:
			if !regexp.MustCompile(`^(0|[1-9][0-9]*)$`).MatchString(token) {
				return nil, fmt.Errorf("OpenAPI reference %q indexes an invalid array entry", ref)
			}
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(node) {
				return nil, fmt.Errorf("OpenAPI reference %q indexes an invalid array entry", ref)
			}
			current = node[index]
		default:
			return nil, fmt.Errorf("OpenAPI reference %q does not exist", ref)
		}
	}
	return resolveLegacyPointerIntermediate(root, current, active, depth+1)
}

// resolveLegacyPointerIntermediate mirrors Pointer.resolve's resolveIf$Ref
// call before each remaining token and after the final token. It is a shallow
// reference extension; the outer dereference walker owns recursive crawling.
func resolveLegacyPointerIntermediate(root map[string]any, current any, active map[string]bool, depth int) (any, error) {
	object := legacyObject(current)
	if object == nil {
		return current, nil
	}
	ref, ok := object["$ref"].(string)
	if !ok || !isLegacyAllowedLocalRef(ref) {
		return current, nil
	}
	if active[ref] {
		return current, nil
	}
	active[ref] = true
	target, err := resolveLegacyJSONPointerState(root, ref, active, depth+1)
	delete(active, ref)
	if err != nil {
		return nil, err
	}
	if len(object) == 1 {
		return target, nil
	}
	switch typed := target.(type) {
	case map[string]any:
		merged := make(map[string]any, len(object)+len(typed))
		for _, key := range legacySortedKeys(object) {
			if key != "$ref" {
				merged[key] = object[key]
			}
		}
		for _, key := range legacySortedKeys(typed) {
			if _, overridden := merged[key]; !overridden {
				merged[key] = typed[key]
			}
		}
		return merged, nil
	case []any:
		merged := make(map[string]any, len(object)+len(typed))
		for _, key := range legacySortedKeys(object) {
			if key != "$ref" {
				merged[key] = object[key]
			}
		}
		for index, child := range typed {
			key := strconv.Itoa(index)
			if _, overridden := merged[key]; !overridden {
				merged[key] = child
			}
		}
		return merged, nil
	default:
		return target, nil
	}
}

func isLegacyAllowedLocalRef(ref string) bool {
	return ref == "#" || strings.HasPrefix(ref, "#/")
}

// legacyDecodePointerToken mirrors Pointer.parse in the pinned
// json-schema-ref-parser: RFC 6901 tilde decoding occurs before a safe
// decodeURIComponent. Malformed escapes or invalid UTF-8 remain literal.
func legacyDecodePointerToken(raw string) string {
	token := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
	decoded, err := url.PathUnescape(token)
	if err != nil || !utf8.ValidString(decoded) {
		return token
	}
	return decoded
}

func legacyObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func legacyArray(value any) []any {
	array, _ := value.([]any)
	return array
}

func legacyString(value any) string {
	text, _ := value.(string)
	return text
}

// validateLegacySwagger2Spec is a direct structural port of
// swagger-parser/lib/validators/spec.js (SHA recorded in provenance). Do not
// add rules here without first changing the frozen Node authority.
func validateLegacySwagger2Spec(api map[string]any) error {
	if legacyJSTruthy(api["openapi"]) {
		return nil
	}
	paths := legacyObject(api["paths"])
	operationIDs := make(map[string]struct{})
	for _, pathName := range legacySortedKeys(paths) {
		rawPath := paths[pathName]
		path := legacyObject(rawPath)
		if path == nil || !strings.HasPrefix(pathName, "/") {
			continue
		}
		if err := validateLegacySwagger2Path(api, path, "/paths"+pathName, operationIDs); err != nil {
			return err
		}
	}
	definitions := legacyObject(api["definitions"])
	for _, name := range legacySortedKeys(definitions) {
		rawDefinition := definitions[name]
		if err := validateLegacySwagger2Required(api, rawDefinition, "/definitions/"+name, make(map[string]bool)); err != nil {
			return err
		}
	}
	return nil
}

var legacySwaggerMethods = [...]string{"get", "put", "post", "delete", "options", "head", "patch"}

func validateLegacySwagger2Path(api, path map[string]any, pathID string, operationIDs map[string]struct{}) error {
	for _, method := range legacySwaggerMethods {
		rawOperation, present := path[method]
		if !present || !legacyJSTruthy(rawOperation) {
			continue
		}
		operation, err := resolveLegacySwagger2Object(api, rawOperation, make(map[string]bool), 0)
		if err != nil {
			return err
		}
		operationID := pathID + "/" + method
		if declared := legacyString(operation["operationId"]); declared != "" {
			if _, duplicate := operationIDs[declared]; duplicate {
				return fmt.Errorf("Validation failed. Duplicate operation id %q", declared)
			}
			operationIDs[declared] = struct{}{}
		}
		if err := validateLegacySwagger2Parameters(api, path, pathID, operation, operationID); err != nil {
			return err
		}
		responses := legacyObject(operation["responses"])
		for _, responseName := range legacySortedKeys(responses) {
			rawResponse := responses[responseName]
			response := map[string]any{}
			if legacyJSTruthy(rawResponse) {
				var err error
				response, err = resolveLegacySwagger2Object(api, rawResponse, make(map[string]bool), 0)
				if err != nil {
					return err
				}
			}
			if err := validateLegacySwagger2Response(responseName, response, operationID+"/responses/"+responseName); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateLegacySwagger2Parameters(api, path map[string]any, pathID string, operation map[string]any, operationID string) error {
	pathParameters, err := resolveLegacySwagger2Parameters(api, legacyArray(path["parameters"]))
	if err != nil {
		return err
	}
	operationParameters, err := resolveLegacySwagger2Parameters(api, legacyArray(operation["parameters"]))
	if err != nil {
		return err
	}
	if err := legacySwagger2Duplicates(pathParameters, pathID); err != nil {
		return err
	}
	if err := legacySwagger2Duplicates(operationParameters, operationID); err != nil {
		return err
	}
	parameters := append([]map[string]any(nil), operationParameters...)
	for _, parameter := range pathParameters {
		if !legacySwagger2ContainsParameter(parameters, parameter) {
			parameters = append(parameters, parameter)
		}
	}
	if err := validateLegacySwagger2BodyParameters(parameters, operationID); err != nil {
		return err
	}
	if err := validateLegacySwagger2PathParameters(parameters, pathID, operationID); err != nil {
		return err
	}
	for _, parameter := range parameters {
		parameterID := operationID + "/parameters/" + legacyString(parameter["name"])
		schema := parameter
		validTypes := legacyPrimitiveTypes[:]
		allowUndefinedType := false
		switch legacyString(parameter["in"]) {
		case "body":
			schema = legacyObject(parameter["schema"])
			validTypes = legacySchemaTypes[:]
			allowUndefinedType = true
		case "formData":
			validTypes = append(append([]string(nil), legacyPrimitiveTypes[:]...), "file")
		}
		if err := validateLegacySwagger2Schema(schema, parameterID, validTypes, allowUndefinedType); err != nil {
			return err
		}
		if err := validateLegacySwagger2Required(api, schema, parameterID, make(map[string]bool)); err != nil {
			return err
		}
		if legacyString(schema["type"]) == "file" {
			consumes := legacyArray(operation["consumes"])
			if consumes == nil {
				consumes = legacyArray(api["consumes"])
			}
			valid := false
			for _, item := range consumes {
				mime := legacyString(item)
				if regexp.MustCompile(`multipart/(.*\+)?form-data`).MatchString(mime) || regexp.MustCompile(`application/(.*\+)?x-www-form-urlencoded`).MatchString(mime) {
					valid = true
					break
				}
			}
			if !valid {
				return fmt.Errorf("Validation failed. %s has a file parameter, so it must consume multipart/form-data or application/x-www-form-urlencoded", operationID)
			}
		}
	}
	return nil
}

var (
	legacyPrimitiveTypes = [...]string{"array", "boolean", "integer", "number", "string"}
	legacySchemaTypes    = [...]string{"array", "boolean", "integer", "number", "string", "object", "null"}
)

func resolveLegacySwagger2Parameters(api map[string]any, raw []any) ([]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	parameters := make([]map[string]any, len(raw))
	for index, value := range raw {
		parameter, err := resolveLegacySwagger2Object(api, value, make(map[string]bool), 0)
		if err != nil {
			return nil, err
		}
		parameters[index] = parameter
	}
	return parameters, nil
}

func legacySwagger2Duplicates(parameters []map[string]any, identifier string) error {
	for first := 0; first < len(parameters)-1; first++ {
		for second := first + 1; second < len(parameters); second++ {
			if legacyString(parameters[first]["name"]) == legacyString(parameters[second]["name"]) && legacyString(parameters[first]["in"]) == legacyString(parameters[second]["in"]) {
				return fmt.Errorf("Validation failed. %s has duplicate parameters", identifier)
			}
		}
	}
	return nil
}

func legacySwagger2ContainsParameter(parameters []map[string]any, candidate map[string]any) bool {
	for _, parameter := range parameters {
		if legacyString(parameter["name"]) == legacyString(candidate["name"]) && legacyString(parameter["in"]) == legacyString(candidate["in"]) {
			return true
		}
	}
	return false
}

func validateLegacySwagger2BodyParameters(parameters []map[string]any, operationID string) error {
	body, form := 0, 0
	for _, parameter := range parameters {
		switch legacyString(parameter["in"]) {
		case "body":
			body++
		case "formData":
			form++
		}
	}
	if body > 1 {
		return fmt.Errorf("Validation failed. %s has %d body parameters. Only one is allowed.", operationID, body)
	}
	if body > 0 && form > 0 {
		return fmt.Errorf("Validation failed. %s has body parameters and formData parameters. Only one or the other is allowed.", operationID)
	}
	return nil
}

func validateLegacySwagger2PathParameters(parameters []map[string]any, pathID, operationID string) error {
	placeholders := legacyPlaceholderRE.FindAllString(pathID, -1)
	for first := 0; first < len(placeholders); first++ {
		for second := first + 1; second < len(placeholders); second++ {
			if placeholders[first] == placeholders[second] {
				return fmt.Errorf("Validation failed. %s has multiple path placeholders named %s", operationID, placeholders[first])
			}
		}
	}
	for _, parameter := range parameters {
		if legacyString(parameter["in"]) != "path" {
			continue
		}
		name := legacyString(parameter["name"])
		if required, _ := parameter["required"].(bool); !required {
			return fmt.Errorf("Validation failed. Path parameters cannot be optional. Set required=true for the %q parameter at %s", name, operationID)
		}
		placeholder := "{" + name + "}"
		match := -1
		for index, current := range placeholders {
			if current == placeholder {
				match = index
				break
			}
		}
		if match < 0 {
			return fmt.Errorf("Validation failed. %s has a path parameter named %q, but there is no corresponding %s in the path string", operationID, name, placeholder)
		}
		placeholders = append(placeholders[:match], placeholders[match+1:]...)
	}
	if len(placeholders) != 0 {
		return fmt.Errorf("Validation failed. %s is missing path parameter(s) for %v", operationID, placeholders)
	}
	return nil
}

func validateLegacySwagger2Response(code string, response map[string]any, responseID string) error {
	if code != "default" {
		number, err := strconv.Atoi(code)
		if err != nil || number < 100 || number > 599 {
			return fmt.Errorf("Validation failed. %s has an invalid response code (%s)", responseID, code)
		}
	}
	headers := legacyObject(response["headers"])
	for _, name := range legacySortedKeys(headers) {
		rawHeader := headers[name]
		if err := validateLegacySwagger2Schema(legacyObject(rawHeader), responseID+"/headers/"+name, legacyPrimitiveTypes[:], false); err != nil {
			return err
		}
	}
	if rawSchema, present := response["schema"]; present && rawSchema != nil {
		valid := append(append([]string(nil), legacySchemaTypes[:]...), "file")
		schema := legacyObject(rawSchema)
		if !legacySwagger2SchemaTypeAllowed(schema, valid, true) {
			return fmt.Errorf("Validation failed. %s has an invalid response schema type (%v)", responseID, schema["type"])
		}
		if err := validateLegacySwagger2Schema(schema, responseID+"/schema", valid, true); err != nil {
			return err
		}
	}
	return nil
}

func validateLegacySwagger2Schema(schema map[string]any, schemaID string, validTypes []string, allowUndefinedType bool) error {
	if schema == nil {
		return fmt.Errorf("Validation failed. %s has an invalid type ()", schemaID)
	}
	if !legacySwagger2SchemaTypeAllowed(schema, validTypes, allowUndefinedType) {
		return fmt.Errorf("Validation failed. %s has an invalid type (%v)", schemaID, schema["type"])
	}
	typeName := legacyString(schema["type"])
	if typeName == "array" && schema["items"] == nil {
		return fmt.Errorf("Validation failed. %s is an array, so it must include an \"items\" schema", schemaID)
	}
	return nil
}

func legacySwagger2SchemaTypeAllowed(schema map[string]any, allowed []string, allowUndefined bool) bool {
	typeValue, exists := schema["type"]
	if !exists {
		return allowUndefined
	}
	typeName, ok := typeValue.(string)
	if !ok {
		return false
	}
	for _, item := range allowed {
		if typeName == item {
			return true
		}
	}
	return false
}

func validateLegacySwagger2Required(api map[string]any, raw any, schemaID string, active map[string]bool) error {
	schema, err := resolveLegacySwagger2Object(api, raw, active, 0)
	if err != nil {
		return err
	}
	typeValue, exists := schema["type"]
	if !exists {
		return nil
	}
	if types, ok := typeValue.([]any); ok {
		object := false
		for _, item := range types {
			if legacyString(item) == "object" {
				object = true
				break
			}
		}
		if !object {
			return nil
		}
	} else if exists && legacyString(typeValue) != "object" {
		return nil
	}
	required := legacyArray(schema["required"])
	if required == nil {
		return nil
	}
	properties := make(map[string]struct{})
	if err := collectLegacySwagger2Properties(api, schema, properties, make(map[string]bool), 0); err != nil {
		return err
	}
	for _, item := range required {
		name := legacyString(item)
		if _, exists := properties[name]; !exists {
			return fmt.Errorf("Validation failed. Property %q listed as required but does not exist in %q", name, schemaID)
		}
	}
	return nil
}

func collectLegacySwagger2Properties(api, raw map[string]any, properties map[string]struct{}, active map[string]bool, depth int) error {
	if depth > legacyValidationMaxDepth {
		return fmt.Errorf("OpenAPI schema inheritance exceeds depth limit")
	}
	if ref, ok := raw["$ref"].(string); ok && isLegacyAllowedLocalRef(ref) {
		if active[ref] {
			// The pinned SwaggerParser performs a second full dereference
			// before spec.js. A circular allOf therefore becomes a native
			// cycle, and spec.js rejects (via recursion overflow) only when
			// its required-property traversal reaches that cycle. Preserve
			// the acceptance boundary without reproducing an unsafe overflow.
			return fmt.Errorf("OpenAPI required-property inheritance contains a circular reference")
		}
		active[ref] = true
		defer delete(active, ref)
		target, err := resolveLegacyJSONPointer(api, ref)
		if err != nil {
			return err
		}
		raw = legacyObject(target)
	}
	for _, name := range legacySortedKeys(legacyObject(raw["properties"])) {
		properties[name] = struct{}{}
	}
	for _, parent := range legacyArray(raw["allOf"]) {
		if err := collectLegacySwagger2Properties(api, legacyObject(parent), properties, active, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func resolveLegacySwagger2Object(root map[string]any, raw any, active map[string]bool, depth int) (map[string]any, error) {
	if depth > legacyValidationMaxDepth {
		return nil, fmt.Errorf("OpenAPI reference resolution exceeds depth limit")
	}
	object := legacyObject(raw)
	if object == nil {
		return nil, fmt.Errorf("OpenAPI reference target is not an object")
	}
	ref, hasRef := object["$ref"].(string)
	if !hasRef || !isLegacyAllowedLocalRef(ref) {
		return object, nil
	}
	if active[ref] {
		return object, nil
	}
	active[ref] = true
	defer delete(active, ref)
	target, err := resolveLegacyJSONPointer(root, ref)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveLegacySwagger2Object(root, target, active, depth+1)
	if err != nil {
		return nil, err
	}
	// ref-parser dereferences the target but retains sibling fields. Copying the
	// target first and then overlaying siblings expresses that behavior without
	// mutating the input used by sourceoperation/openapimap.
	merged := make(map[string]any, len(resolved)+len(object))
	for _, key := range legacySortedKeys(resolved) {
		value := resolved[key]
		merged[key] = value
	}
	for _, key := range legacySortedKeys(object) {
		value := object[key]
		if key != "$ref" {
			merged[key] = value
		}
	}
	return merged, nil
}

// legacyOpenAPISchemaHashes supports the offline asset-integrity test. It is
// intentionally not an exported runtime API.
func legacyOpenAPISchemaHashes() map[string]string {
	assets := map[string][]byte{"v2.0/schema.json": legacySwagger2Schema, "v3.0/schema.json": legacyOpenAPI30Schema, "v3.1/schema.json": legacyOpenAPI31Schema}
	result := make(map[string]string, len(assets))
	for name, bytes := range assets {
		sum := sha256.Sum256(bytes)
		result[name] = hex.EncodeToString(sum[:])
	}
	return result
}

func legacySortedKeys(object map[string]any) []string {
	// Node's Object.keys retains input insertion order. The legacy JSON/YAML
	// decoder intentionally exposes Go maps, so that order is no longer
	// available at this layer. Sorting supplies a stable documented precedence
	// among independently-invalid siblings; it does not alter accepted graphs
	// or artifact bytes because validation fails before artifact construction.
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
