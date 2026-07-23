// Package metadata ports node-src/metadata/*.ts: pack.json/registry.json
// validation, pack-root/profile/catalog resolution, resource-schema
// loading, and the root-catalog compatibility view, all built on the
// go/internal/canonjson canonical JSON value tree (map[string]any /
// []any / json.Number / float64 / string / bool / nil) instead of
// generated structs, matching this port's Slice 0 design
// (docs/go-runtime-plan.md).
//
// Every exported symbol's doc comment names the Node source file it ports;
// those TypeScript files remain the differential oracle until this port is
// independently qualified.
package metadata

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"regexp"
	"strconv"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// JsonObject is the dynamic JSON-object shape this package validates
// against: a canonjson.Value known (once validated) to be a JSON object.
// Ports the JsonObject alias from node-src/metadata/validation.ts.
type JsonObject = map[string]any

// MetadataError reports a metadata validation failure. Its Error() text is
// asserted verbatim by this package's ported tests wherever the
// corresponding Node test asserts an exact message, per this port's
// validation-message-parity requirement. Ports the MetadataError class from
// node-src/metadata/validation.ts.
type MetadataError struct {
	message string
}

// Error implements the error interface.
func (e *MetadataError) Error() string { return e.message }

// fail panics with a *MetadataError carrying message, mirroring
// node-src/metadata/validation.ts's fail(), typed there as returning
// `never` because it always throws.
//
// Every exported entry point in this package recovers a *MetadataError
// panic at its own boundary (see recoverMetadataError) and returns it as a
// normal Go error, so callers never observe the panic. This exists only so
// the many small validation helpers ported from validation.ts, packs.ts,
// resources.ts, and driftpolicy.go can abandon the current operation from
// arbitrarily deep call nesting without every intermediate function
// threading an explicit error return -- the same non-local control flow the
// Node source gets for free from `throw`. Apart from the exact private
// filesystem passthrough payload below, every panic value that is not a
// *MetadataError is re-panicked by recoverMetadataError: only expected
// validation and source-defined filesystem failures become returned errors,
// never genuine bugs.
func fail(message string) {
	panic(&MetadataError{message: message})
}

// failf is fail with fmt.Sprintf formatting, used at call sites that would
// otherwise need a fmt.Sprintf(...) argument just to call fail.
func failf(format string, args ...any) {
	fail(fmt.Sprintf(format, args...))
}

// metadataFilesystemPassthrough is the package-private panic payload used to
// carry a raw filesystem failure through this package's throw-like validation
// helpers. It is deliberately not an error: recoverMetadataError recognizes
// only this exact payload type, never an arbitrary panic that happens to
// implement error.
type metadataFilesystemPassthrough struct {
	err error
}

// propagateFilesystemError carries a raw filesystem error to the nearest
// exported metadata boundary without turning it into a MetadataError.
// Callers must invoke it immediately after the filesystem call, after
// handling any source-defined ENOENT branch.
func propagateFilesystemError(err error) {
	panic(&metadataFilesystemPassthrough{err: err})
}

// recoverMetadataError is deferred by every exported entry point in this
// package (as `defer recoverMetadataError(&err)`) to convert a recovered
// *MetadataError panic (see fail), or the exact private filesystem passthrough
// payload above, into a normal error return. Any other recovered value is
// re-panicked, since it indicates a genuine bug rather than an expected
// validation or filesystem failure.
func recoverMetadataError(err *error) {
	if r := recover(); r != nil {
		switch recovered := r.(type) {
		case *MetadataError:
			*err = recovered
			return
		case *metadataFilesystemPassthrough:
			*err = recovered.err
			return
		default:
			panic(r)
		}
	}
}

// isObject reports whether value is a JSON object as decoded by this
// package (a map[string]any). Ports isObject from
// node-src/metadata/validation.ts; the TypeScript version additionally
// excludes arrays and LosslessNumber instances from a bare `typeof value
// === "object"` test, both of which have no Go analogue here: []any never
// satisfies a map[string]any type assertion, and json.Number is a string
// type, not a map.
func isObject(value any) bool {
	_, ok := value.(JsonObject)
	return ok
}

// requireObject ports requireObject from node-src/metadata/validation.ts.
func requireObject(value any, path string) JsonObject {
	obj, ok := value.(JsonObject)
	if !ok {
		failf("%s must contain a JSON object", path)
	}
	return obj
}

// stringSet builds a set from literal keys, used throughout this package
// wherever the Node source writes `new Set([...])`.
func stringSet(keys ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

// rejectUnknownKeys ports rejectUnknownKeys from
// node-src/metadata/validation.ts. Unknown keys are sorted before the
// first is reported (matching the Node source's explicit
// sortedStrings(...)), so which key is named is deterministic regardless
// of this package's unordered map[string]any object representation.
func rejectUnknownKeys(value JsonObject, allowed map[string]struct{}, path string) {
	unknown := make([]string, 0)
	for key := range value {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sorted := canonjson.SortedStrings(unknown)
		failf("%s: unknown key %s", path, sorted[0])
	}
}

// requireKeys ports requireKeys from node-src/metadata/validation.ts.
// Missing keys are sorted before the first is reported, matching the Node
// source's explicit sortedStrings(...).
func requireKeys(value JsonObject, required map[string]struct{}, path string) {
	missing := make([]string, 0)
	for key := range required {
		if _, ok := value[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sorted := canonjson.SortedStrings(missing)
		failf("%s: missing required key %s", path, sorted[0])
	}
}

// requireNonEmptyString ports requireNonEmptyString from
// node-src/metadata/validation.ts.
func requireNonEmptyString(value any, path string) string {
	s, ok := value.(string)
	if !ok || len(s) == 0 {
		failf("%s must be a non-empty string", path)
	}
	return s
}

// validateStringMap ports validateStringMap from
// node-src/metadata/validation.ts.
//
// The Node source iterates Object.keys(value) in source key order and
// reports the first invalid key or value it finds; this package's
// map[string]any object representation does not preserve source key
// order (encoding/json, like Go maps generally, does not track it), so
// this walks keys in sorted order instead. Every fixture this package's
// ported tests exercise has at most one invalid entry per object, so this
// difference is unobservable there; it is called out here (and in this
// port's final report) as a known, deliberate divergence for any future
// fixture with multiple simultaneous violations in one object.
func validateStringMap(value any, path string) map[string]string {
	obj, ok := value.(JsonObject)
	if !ok {
		failf("%s must be an object", path)
	}
	output := make(map[string]string, len(obj))
	for _, key := range sortedKeys(obj) {
		if len(key) == 0 {
			failf("%s keys must be non-empty strings", path)
		}
		output[key] = requireNonEmptyString(obj[key], fmt.Sprintf("%s.%s", path, key))
	}
	return output
}

// sortedKeys returns obj's keys in Python/Node-compatible code-point
// order, giving this package's validation loops a deterministic
// iteration order over Go's unordered map[string]any (see the
// validateStringMap doc comment for why this matters).
func sortedKeys(obj JsonObject) []string {
	return sortedMapKeys(obj)
}

// sortedMapKeys is sortedKeys generalized to any string-keyed map, for the
// handful of call sites (e.g. LoadedRegistry.Entries, map[string]string)
// whose value type isn't JsonObject.
func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return canonjson.SortedStrings(keys)
}

// jsonIntegerToken matches a bare JSON integer lexeme: an optional minus
// sign, then "0" or a non-zero digit followed by more digits -- no
// fraction, no exponent. Ports the JSON_INTEGER regexp from
// node-src/metadata/validation.ts.
var jsonIntegerToken = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)

// minSafeInteger and maxSafeInteger are JavaScript's
// Number.MIN_SAFE_INTEGER/MAX_SAFE_INTEGER (+/-(2^53 - 1)), the range
// node-src/metadata/validation.ts's normalizedMetadataNumber uses to
// decide whether an integer token can round-trip through a plain JS
// number.
const (
	minSafeInteger = -(int64(1)<<53 - 1)
	maxSafeInteger = int64(1)<<53 - 1
)

var (
	minSafeIntegerBig = big.NewInt(minSafeInteger)
	maxSafeIntegerBig = big.NewInt(maxSafeInteger)
)

// normalizedMetadataNumber demotes a losslessly decoded JSON number token
// (a json.Number, this package's analogue of a lossless-json
// LosslessNumber) to a plain float64 wherever that conversion is exact,
// leaving it as the original token otherwise. Ports
// normalizedMetadataNumber from node-src/metadata/validation.ts.
func normalizedMetadataNumber(value json.Number) any {
	token := string(value)
	if jsonIntegerToken.MatchString(token) {
		integer, ok := new(big.Int).SetString(token, 10)
		if !ok {
			return value
		}
		if integer.Cmp(minSafeIntegerBig) >= 0 && integer.Cmp(maxSafeIntegerBig) <= 0 {
			f, _ := new(big.Float).SetInt(integer).Float64()
			return f
		}
		return value
	}
	number, ferr := strconv.ParseFloat(token, 64)
	if ferr != nil {
		var numErr *strconv.NumError
		if ok := asNumError(ferr, &numErr); !ok || numErr.Err != strconv.ErrRange {
			// Unreachable for a syntactically valid JSON number token.
			return value
		}
	}
	if math.IsInf(number, 0) || math.IsNaN(number) {
		return value
	}
	return number
}

// asNumError is a small errors.As wrapper kept local to this file to avoid
// importing the "errors" package solely for one call site.
func asNumError(err error, target **strconv.NumError) bool {
	numErr, ok := err.(*strconv.NumError)
	if ok {
		*target = numErr
	}
	return ok
}

// normalizeNumericTokens ports normalizeNumericTokens from
// node-src/metadata/validation.ts: recursively walks a decoded JSON value,
// demoting json.Number leaves to plain float64 (see
// normalizedMetadataNumber) except within a subtree rooted at a
// preserveUnderKeys key (or when preserve is already true), where the
// original token is kept exactly, string-for-string.
func normalizeNumericTokens(value any, preserve bool, preserveUnderKeys map[string]struct{}) any {
	switch v := value.(type) {
	case json.Number:
		if preserve {
			return v
		}
		return normalizedMetadataNumber(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeNumericTokens(item, preserve, preserveUnderKeys)
		}
		return out
	case JsonObject:
		out := make(JsonObject, len(v))
		for key, item := range v {
			_, keyPreserved := preserveUnderKeys[key]
			out[key] = normalizeNumericTokens(item, preserve || keyPreserved, preserveUnderKeys)
		}
		return out
	default:
		return value
	}
}

// readJSONOptions mirrors the options bag readJson accepts in
// node-src/metadata/validation.ts.
type readJSONOptions struct {
	preserveNumericTokens          bool
	preserveNumericTokensUnderKeys map[string]struct{}
}

// readJSON reads and parses path as this package's canonical numeric-token
// dialect, panicking (see fail) with the same message shape as
// node-src/metadata/validation.ts's readJson on any I/O or parse failure.
//
// Decoding itself is delegated to canonjson.Decode, which -- like the Node
// source's lossless-json parse -- always surfaces every JSON number as its
// exact source lexeme (json.Number here, LosslessNumber there); this
// function then applies the same preserve/preserveUnderKeys demotion
// normalizeNumericTokens applies in the Node source. The Node source
// instead special-cases "neither option set" to decide each number's fate
// at parse time via a JSON.parse reviver; that is behaviorally identical to
// (and here implemented as) calling normalizeNumericTokens with preserve
// false and an empty preserveUnderKeys set, since both paths apply
// normalizedMetadataNumber's exact same decision per token.
func readJSON(path string, opts readJSONOptions) any {
	data, err := os.ReadFile(path)
	if err != nil {
		detail := err
		failf("failed to read %s: %s", path, detail.Error())
	}
	value, err := canonjson.Decode(data)
	if err != nil {
		failf("failed to read %s: %s", path, err.Error())
	}
	if opts.preserveNumericTokens {
		return value
	}
	underKeys := opts.preserveNumericTokensUnderKeys
	if underKeys == nil {
		underKeys = map[string]struct{}{}
	}
	return normalizeNumericTokens(value, false, underKeys)
}

// isFiniteJsonNumber ports isFiniteJsonNumber from
// node-src/metadata/validation.ts.
func isFiniteJsonNumber(value any) bool {
	switch v := value.(type) {
	case float64:
		return !math.IsInf(v, 0) && !math.IsNaN(v)
	case json.Number:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			var numErr *strconv.NumError
			if ok := asNumError(err, &numErr); !ok || numErr.Err != strconv.ErrRange {
				return false
			}
		}
		return !math.IsInf(f, 0) && !math.IsNaN(f)
	default:
		return false
	}
}

// isIntegerJsonNumber ports isIntegerJsonNumber from
// node-src/metadata/validation.ts.
func isIntegerJsonNumber(value any) bool {
	switch v := value.(type) {
	case float64:
		return !math.IsInf(v, 0) && v == math.Trunc(v)
	case json.Number:
		return jsonIntegerToken.MatchString(string(v))
	default:
		return false
	}
}

// isJsonScalar ports isJsonScalar from node-src/metadata/validation.ts.
func isJsonScalar(value any) bool {
	if value == nil {
		return true
	}
	switch value.(type) {
	case string, bool, float64, json.Number:
		return true
	default:
		return false
	}
}
