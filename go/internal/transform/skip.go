package transform

// skip.go ports the skip_if / skip_if_lte matching vocabulary from
// the original implementation: skipMatchers, jsonScalarKind,
// strictJsonScalarMatcherMatches, LteNumber, lteNumber, numberIsLte,
// skipMatchReason, and transformSkipMatchReason.

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// skipMatchers ports skipMatchers from the original implementation.
func skipMatchers(value any, label string) []map[string]any {
	if value == nil {
		return nil
	}
	arr, ok := value.([]any)
	if !ok {
		failf("%s must be a list", label)
	}
	matchers := make([]map[string]any, len(arr))
	for index, item := range arr {
		obj, isObject := item.(map[string]any)
		if !isObject || len(obj) == 0 {
			failf("%s[%d] must be a non-empty object", label, index)
		}
		matchers[index] = obj
	}
	return matchers
}

// jsonScalarKindValue is the Go analogue of jsonScalarKind's return union
// ("boolean" | "null" | "number" | "string" | null) from
// the original implementation.
type jsonScalarKindValue int

const (
	scalarKindNone jsonScalarKindValue = iota
	scalarKindNull
	scalarKindBoolean
	scalarKindString
	scalarKindNumber
)

// jsonScalarKind ports jsonScalarKind from
// the original implementation. The float64 case is dead in ordinary
// practice (see coerce.go's file doc comment: every numeric leaf this
// package's own pipeline produces is a json.Number), ported anyway for
// direct correspondence with the Node source's own
// `typeof value === "number"` arm, which is equally dead there.
func jsonScalarKind(value any) jsonScalarKindValue {
	if value == nil {
		return scalarKindNull
	}
	if _, ok := value.(bool); ok {
		return scalarKindBoolean
	}
	if _, ok := value.(string); ok {
		return scalarKindString
	}
	if _, ok := value.(json.Number); ok {
		return scalarKindNumber
	}
	if _, ok := value.(float64); ok {
		return scalarKindNumber
	}
	return scalarKindNone
}

// StrictJsonScalarMatcherMatches ports the exported
// strictJsonScalarMatcherMatches from the original implementation:
// "Match a snake-cased item against exact, presence-aware JSON scalar
// fields." Iterates matcher in unspecified (Go map) order: the Node
// source's Object.entries(matcher).every(...) is a short-circuiting
// conjunction with no side effect other than its boolean result, so unlike
// this package's many key-ordered walks (see transform.go's file doc
// comment), the order entries are visited in cannot change the outcome
// here.
func StrictJsonScalarMatcherMatches(item map[string]any, matcher map[string]any) bool {
	for rawField, expected := range matcher {
		field := SnakeName(rawField)
		value, ok := item[field]
		if !ok {
			return false
		}
		expectedKind := jsonScalarKind(expected)
		if expectedKind == scalarKindNone || jsonScalarKind(value) != expectedKind {
			return false
		}
		if expectedKind == scalarKindNumber {
			if !canonjson.JSONEqual(value, expected) {
				return false
			}
		} else if value != expected {
			return false
		}
	}
	return true
}

// lteNumberKind is the Go analogue of LteNumber's "integer" | "float"
// discriminant from the original implementation.
type lteNumberKind int

const (
	lteNumberInteger lteNumberKind = iota
	lteNumberFloat
)

// lteNumberValue is the Go analogue of the LteNumber union type from
// the original implementation.
type lteNumberValue struct {
	Kind    lteNumberKind
	Integer *big.Int
	Float   float64
}

// lteNumber ports lteNumber from the original implementation.
func lteNumber(value any) (lteNumberValue, bool) {
	if value == nil {
		return lteNumberValue{}, false
	}
	if _, ok := value.(bool); ok {
		return lteNumberValue{}, false
	}
	if n, ok := value.(json.Number); ok {
		if token, ok := losslessIntegerToken(n); ok {
			integer, _ := new(big.Int).SetString(token, 10)
			return lteNumberValue{Kind: lteNumberInteger, Integer: integer}, true
		}
		f, err := n.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return lteNumberValue{}, false
		}
		return lteNumberValue{Kind: lteNumberFloat, Float: f}, true
	}
	// Dead in ordinary practice (see jsonScalarKind's doc comment); ported
	// for parity with the Node source's own `typeof value === "number"` arm.
	if f, ok := value.(float64); ok {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return lteNumberValue{}, false
		}
		if isSafeInteger(f) {
			return lteNumberValue{Kind: lteNumberInteger, Integer: big.NewInt(int64(f))}, true
		}
		return lteNumberValue{Kind: lteNumberFloat, Float: f}, true
	}
	if s, ok := value.(string); ok {
		normalized, ok := normalizedPythonFloatString(s)
		if !ok {
			return lteNumberValue{}, false
		}
		f, err := strconv.ParseFloat(normalized, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return lteNumberValue{}, false
		}
		return lteNumberValue{Kind: lteNumberFloat, Float: f}, true
	}
	return lteNumberValue{}, false
}

// floatToBigInt converts an already-integral float64 (the result of
// math.Floor/math.Ceil) to the exact *big.Int it represents, the Go
// analogue of BigInt(Math.floor(...)) / BigInt(Math.ceil(...)) in
// the original implementation's numberIsLte: a plain int64
// conversion would silently truncate for a magnitude beyond int64's range
// (a float64 mantissa is only 53 bits, but its exponent can push the value
// far beyond 2^63), so this goes through big.Float, which holds any
// float64 exactly, to recover the precise integer.
func floatToBigInt(f float64) *big.Int {
	integer, _ := new(big.Float).SetFloat64(f).Int(nil)
	return integer
}

// numberIsLte ports numberIsLte from the original implementation.
func numberIsLte(value, threshold lteNumberValue) bool {
	if value.Kind == lteNumberInteger && threshold.Kind == lteNumberInteger {
		return value.Integer.Cmp(threshold.Integer) <= 0
	}
	if value.Kind == lteNumberFloat && threshold.Kind == lteNumberFloat {
		return value.Float <= threshold.Float
	}
	if value.Kind == lteNumberInteger && threshold.Kind == lteNumberFloat {
		return value.Integer.Cmp(floatToBigInt(math.Floor(threshold.Float))) <= 0
	}
	// The only remaining case: value.Kind == lteNumberFloat && threshold.Kind
	// == lteNumberInteger.
	return floatToBigInt(math.Ceil(value.Float)).Cmp(threshold.Integer) <= 0
}

// skipMatchReason ports skipMatchReason from
// the original implementation.
func skipMatchReason(item map[string]any, resource *runtimeTransformResource) (string, bool) {
	return TransformSkipMatchReason(item, resource.Override, resource.Type)
}

// TransformSkipMatchReason ports the exported transformSkipMatchReason from
// the original implementation: "Evaluate the transform/adoption skip
// vocabulary against a snake-cased item." A "" result with matched=false is
// the Go analogue of the Node source's null ("no skip vocabulary entry
// matched"); matched=true pairs with reason "skip_if" or "skip_if_lte".
func TransformSkipMatchReason(item map[string]any, metadata map[string]any, label string) (reason string, matched bool) {
	for _, matcher := range skipMatchers(metadata["skip_if"], label+".skip_if") {
		if StrictJsonScalarMatcherMatches(item, matcher) {
			return "skip_if", true
		}
	}
	for _, matcher := range skipMatchers(metadata["skip_if_lte"], label+".skip_if_lte") {
		allMatch := true
		// Sorted (unlike StrictJsonScalarMatcherMatches's unordered walk
		// above): unlike that function, this loop can fail() on a
		// non-numeric threshold, so which field's error message surfaces
		// first when more than one is malformed must be deterministic --
		// see this package's determinism convention in transform.go's file
		// doc comment.
		for _, field := range canonjson.SortedStrings(mapKeys(matcher)) {
			thresholdValue := matcher[field]
			threshold, ok := lteNumber(thresholdValue)
			if !ok {
				failf("skip_if_lte threshold for %s must be numeric", jsonQuote(field))
			}
			value, ok := lteNumber(item[SnakeName(field)])
			if !ok || !numberIsLte(value, threshold) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return "skip_if_lte", true
		}
	}
	return "", false
}
