package transform

// coerce.go ports pull-transform.ts's schema-driven value coercion:
// losslessIntegerToken, integerValue, coerceBoolean, coercePrimitive,
// coerceValue, coerceObjectMembers, unwrapReference, pythonSetSortKey,
// divideInteger, and dividedValue.
//
// # A deliberate simplification: internally produced integers are always json.Number
//
// The Node source's parsePythonInteger and dividedValue return a plain JS
// `number` for a JS-safe-integer-range result and a LosslessNumber for
// anything larger, and coercePrimitive's "number" encoding branch passes
// that union straight through as a coerced field's value. This package
// represents both branches as json.Number uniformly instead of splitting
// them into float64 (this package's Go analogue of a bare `number`; see
// transform.go's cloneJson doc comment) vs json.Number.
//
// This is safe because every function in this package that later consumes
// such a value already normalizes across the two representations before
// doing anything observable with it: losslessIntegerToken/integerValue
// (both branches route to the same BigInt), canonjson's numeric equality
// (numericValue handles float64 and json.Number identically for integers),
// pythonSetSortKey, and identityComponent's non-strict fallback all produce
// the same output for a same-valued integer regardless of which
// representation it arrives in. float64 is still accepted (and, for
// cloneJson specifically, rejected as invalid *input*) elsewhere in this
// package wherever the Node source's authoring-seam functions
// (ApplyTransformOverridesForAuthoring, CoerceTransformPrimitiveForAuthoring)
// must accept it -- node-src/domain/pull-transform.ts's own
// snakeJsonKeysForAuthoring doc comment notes authoring inputs "may be
// constructed with ordinary finite JavaScript numbers instead of coming
// from the lossless runtime JSON parser" -- this simplification only
// applies to values this package's own divide/integer-parsing logic
// *produces*, never to values a caller supplies.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

var safeIntegerToken = regexp.MustCompile(`^-?[0-9]+$`)

// losslessIntegerToken ports losslessIntegerToken from
// node-src/domain/pull-transform.ts. The Go analogue of a plain JS
// `number` here is a bare float64 (see cloneJson's doc comment): produced
// internally by this package's own coercion pipeline (e.g. dividedValue,
// coercePrimitive's "number" branch), never by decoding raw JSON.
func losslessIntegerToken(value any) (string, bool) {
	if n, ok := value.(json.Number); ok {
		token := string(n)
		if safeIntegerToken.MatchString(token) {
			return token, true
		}
		return "", false
	}
	if f, ok := value.(float64); ok && isSafeInteger(f) {
		return strconv.FormatInt(int64(f), 10), true
	}
	return "", false
}

// isSafeInteger mirrors Number.isSafeInteger.
func isSafeInteger(v float64) bool {
	return v == math.Trunc(v) && !math.IsInf(v, 0) && math.Abs(v) <= (1<<53-1)
}

// integerValue ports integerValue from node-src/domain/pull-transform.ts.
func integerValue(value any) (*big.Int, bool) {
	token, ok := losslessIntegerToken(value)
	if !ok {
		return nil, false
	}
	n, ok := new(big.Int).SetString(token, 10)
	return n, ok
}

// coerceBoolean ports coerceBoolean from
// node-src/domain/pull-transform.ts. String comparisons against the four
// ASCII literals "true"/"1"/"false"/"0" use strings.ToLower rather than
// go/internal/pyunicode.PythonLower151: the Node source itself uses
// value.toLowerCase() here, not the Python-compatible pythonLower151 this
// package uses for snake-casing/collation elsewhere, and ordinary
// ASCII-range case folding (which strings.ToLower and JS's toLowerCase
// agree on) is all either runtime needs to recognize these four literals.
func coerceBoolean(value any) any {
	if b, ok := value.(bool); ok {
		return b
	}
	if s, ok := value.(string); ok {
		switch strings.ToLower(s) {
		case "true", "1":
			return true
		case "false", "0":
			return false
		default:
			return s
		}
	}
	if token, ok := losslessIntegerToken(value); ok {
		n, _ := new(big.Int).SetString(token, 10)
		return n.Sign() != 0
	}
	return value
}

// coercePrimitive ports coercePrimitive from
// node-src/domain/pull-transform.ts.
func coercePrimitive(value any, encoding string) any {
	switch encoding {
	case "string":
		if b, ok := value.(bool); ok {
			if b {
				return "true"
			}
			return "false"
		}
		if n, ok := value.(json.Number); ok {
			token, ok := canonicalNumberToken(string(n))
			if !ok {
				fail("transform string coercion requires a finite JSON number")
			}
			return token
		}
		if f, ok := value.(float64); ok {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				fail("transform string coercion requires a finite number")
			}
			if isSafeInteger(f) {
				return strconv.FormatInt(int64(f), 10)
			}
			token, ok := finiteFloatToken(f)
			if !ok {
				fail("transform string coercion requires a finite number")
			}
			return token
		}
		return value
	case "number":
		s, ok := value.(string)
		if !ok {
			return value
		}
		if integer := parsePythonInteger(s); integer.Ok {
			return integer.AsNumber()
		}
		if floatText, ok := normalizedPythonFloatString(s); ok {
			parsed, err := strconv.ParseFloat(floatText, 64)
			// A parse error and a non-finite parsed value (Inf/-Inf/NaN,
			// including from the grammar's "inf"/"infinity"/"nan" forms --
			// see the finiteFloatToken/parsePythonFloat doc note on why
			// Go's strconv.ParseFloat and JS's Number() can reach that
			// state via different intermediate routes but always agree on
			// the end result here) both fail the same
			// finite-numbers-only check the Node source applies via
			// pythonFiniteFloatToken.
			if err != nil {
				parsed = math.NaN()
			}
			token, ok := finiteFloatToken(parsed)
			if !ok {
				fail("transform numeric coercion accepts finite numbers only")
			}
			return json.Number(token)
		}
		return value
	default: // "bool"
		return coerceBoolean(value)
	}
}

// unwrapReference ports unwrapReference from
// node-src/domain/pull-transform.ts.
func unwrapReference(value any) any {
	if obj, ok := value.(map[string]any); ok {
		if hasOwn(obj, "id") {
			return obj["id"]
		}
	}
	return value
}

// pythonSetSortKey ports pythonSetSortKey from
// node-src/domain/pull-transform.ts.
func pythonSetSortKey(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	case json.Number:
		if token, ok := canonicalNumberToken(string(v)); ok {
			return token
		}
		return string(v)
	case float64:
		return formatJSNumber(v)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = pythonSetSortKey(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := sortedObjectKeys(v)
		parts := make([]string, len(keys))
		for i, key := range keys {
			parts[i] = jsonQuote(key) + ": " + pythonSetSortKey(v[key])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmtString(value)
	}
}

// formatJSNumber renders a bare float64 the way JS's String(value) would
// for the plain `number` branch of pythonSetSortKey (`typeof value ===
// "number" ? String(value)`). Every float64 reaching this package's
// coercion pipeline is already a safe (json-integer-safe) value produced
// internally by dividedValue/coercePrimitive, so ordinary base-10
// formatting -- not the fuller ECMA-262 Number::toString algorithm -- is
// sufficient here.
func formatJSNumber(v float64) string {
	if v == math.Trunc(v) && !math.IsInf(v, 0) {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// fmtString is pythonSetSortKey's fallback `String(value)` coercion for any
// value outside this package's closed JSON value vocabulary; unreachable in
// practice given that vocabulary, but ported for parity with the Node
// source's own unconditional `return String(value);` fallback.
func fmtString(value any) string {
	return fmt.Sprintf("%v", value)
}

// coerceObjectMembers ports coerceObjectMembers from
// node-src/domain/pull-transform.ts.
func coerceObjectMembers(value any, members map[string]metadata.TerraformTypeEncoding, strictFrozenCompatibility bool) any {
	obj, ok := value.(map[string]any)
	if !ok {
		return value
	}
	out := make(map[string]any)
	for _, key := range sortedObjectKeys(obj) {
		member, ok := members[key]
		if !ok {
			continue
		}
		out[key] = coerceValue(obj[key], member, strictFrozenCompatibility)
	}
	return out
}

// coerceValue ports coerceValue from node-src/domain/pull-transform.ts.
func coerceValue(value any, encoding metadata.TerraformTypeEncoding, strictFrozenCompatibility bool) any {
	switch enc := encoding.(type) {
	case metadata.TerraformPrimitiveType:
		return coercePrimitive(unwrapReference(value), string(enc))
	case metadata.TerraformObjectType:
		return coerceObjectMembers(value, enc.Members, strictFrozenCompatibility)
	case metadata.TerraformCollectionType:
		if enc.Kind == "map" {
			obj, ok := value.(map[string]any)
			if !ok {
				return value
			}
			out := make(map[string]any, len(obj))
			for _, key := range sortedObjectKeys(obj) {
				out[key] = coerceValue(obj[key], enc.Inner, strictFrozenCompatibility)
			}
			return out
		}
		if s, ok := value.(string); ok && s == "" {
			return []any{}
		}
		var output []any
		switch v := value.(type) {
		case []any:
			output = make([]any, len(v))
			for i, item := range v {
				output[i] = coerceValue(item, enc.Inner, strictFrozenCompatibility)
			}
		case nil:
			return nil
		default:
			output = []any{coerceValue(value, enc.Inner, strictFrozenCompatibility)}
		}
		if enc.Kind == "set" {
			return coerceSetValues(output, enc.Inner, strictFrozenCompatibility)
		}
		return output
	default:
		// Unreachable: metadata.TerraformTypeEncoding is a closed union of
		// the three cases above.
		return value
	}
}

type setSortEntry struct {
	index int
	item  any
	key   string
}

// coerceSetValues ports the "set" branch tail of coerceValue from
// node-src/domain/pull-transform.ts: a stable sort by Python-compatible
// string key, ties broken by original index (Go's sort.SliceStable makes
// the explicit index-tiebreak in the Node source's comparator belt, but not
// braces, since the Node source's own `left.index - right.index` fallback
// is exactly what SliceStable already guarantees for any two elements the
// primary key comparison treats as equal).
func coerceSetValues(output []any, childEncoding metadata.TerraformTypeEncoding, strictFrozenCompatibility bool) []any {
	if strictFrozenCompatibility && childEncoding == metadata.TerraformPrimitiveType("string") {
		for _, item := range output {
			if item == nil {
				continue
			}
			if _, ok := item.(string); !ok {
				fail("set(string) coercion produced a non-string provider value")
			}
		}
	}
	entries := make([]setSortEntry, len(output))
	for i, item := range output {
		entries[i] = setSortEntry{index: i, item: item, key: pythonSetSortKey(item)}
	}
	sortSetSortEntriesStable(entries)
	sorted := make([]any, len(entries))
	for i, entry := range entries {
		sorted[i] = entry.item
	}
	return sorted
}

func sortSetSortEntriesStable(entries []setSortEntry) {
	// sort.SliceStable with a comparator matching
	// comparePythonStrings(left.key, right.key) || left.index - right.index
	// from node-src/domain/pull-transform.ts's coerceValue. (The explicit
	// index tiebreak in the Node source is redundant with SliceStable's own
	// stability guarantee, but is kept as the comparator's second key for
	// direct textual correspondence with the ported source.)
	sort.SliceStable(entries, func(i, j int) bool {
		cmp := canonjson.ComparePythonStrings(entries[i].key, entries[j].key)
		if cmp != 0 {
			return cmp < 0
		}
		return entries[i].index < entries[j].index
	})
}

// divideInteger ports divideInteger from node-src/domain/pull-transform.ts:
// truncated (round-toward-zero) quotient/remainder via big.Int's own
// Quo/Rem (which already truncate toward zero, exactly like JS BigInt's
// `/`/`%`), adjusted to floor semantics (Python's `//`) when the
// remainder is non-zero and the operands have different signs.
func divideInteger(value, divisor *big.Int) *big.Int {
	quotient := new(big.Int).Quo(value, divisor)
	remainder := new(big.Int).Rem(value, divisor)
	if remainder.Sign() != 0 && (value.Sign() < 0) != (divisor.Sign() < 0) {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient
}

// dividedValue ports dividedValue from node-src/domain/pull-transform.ts.
func dividedValue(value, divisorValue any, label string) any {
	divisor, ok := integerValue(divisorValue)
	if !ok || divisor.Sign() == 0 {
		failf("%s must be a non-zero integer", label)
	}
	candidate := value
	if s, ok := candidate.(string); ok {
		parsed := parsePythonInteger(s)
		if !parsed.Ok {
			return value
		}
		candidate = parsed.AsNumber()
	}
	if _, ok := candidate.(bool); ok {
		return value
	}
	integer, ok := integerValue(candidate)
	if !ok {
		return value
	}
	divided := divideInteger(integer, divisor)
	// The Node source returns a plain `number` in the JS-safe-integer range
	// (`Number(divided)`) and a LosslessNumber otherwise. This package
	// represents both as json.Number uniformly: every consumer of a
	// dividedValue result in this kernel (losslessIntegerToken/
	// integerValue, canonjson.JSONEqual's numeric comparison,
	// pythonSetSortKey, identityComponent's non-strict branch) already
	// treats a LosslessNumber/json.Number holding an integer token and a
	// same-valued plain number/float64 identically -- see this function's
	// doc comment in coerce.go's file header for the fuller justification.
	// This is a deliberate representational simplification, not a Node
	// behavior it fails to reproduce.
	return json.Number(divided.String())
}
