// Ports the original implementation: the artifact JSON
// contract used for the demo/config/demo/*.json fixtures (and, in the Node
// source, every deployment tfvars/lookup artifact the transform pipeline
// writes) -- "plain JSON values with finite lossless numbers" only,
// rendered as
// json.dumps(value, ensure_ascii=True, indent=2, sort_keys=True) + "\n".
//
// This is a deliberately independent sibling of render.go's Render (which
// ports python-compatible.ts's renderPythonCompatibleJson): the two Node
// source files do not share an encoder either, and differ in exactly the
// two ways documented on TestRoundTripGate and
// TestEncodeStringLeavesDELUnescaped in gate_test.go. Point 1 (DEL
// escaping) is reproduced here as encodeArtifactStringUnit's `>= 0x7f`
// threshold, one character earlier than render.go's `>= 0x80`.
package canonjson

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// ErrInvalidArtifactJSON is the single error RenderLosslessArtifactJSON
// ever returns on failure, ported from python-lossless-artifact.ts's
// invalidArtifactJson(): a *procerr.ProcessFailure carrying the same
// {code, category, message} triple that helper's
// `new ProcessFailure({code, category, message})` call constructs --
// verbatim, including the message text -- and retryable false/no details,
// the same constructor defaults that call site relies on (see
// procerr.NewProcessFailure). Using procerr.ProcessFailure here (rather
// than a canonjson-local error type) means this sentinel is already the
// exact type the CLI's procerr.RenderCLIProcessFailure renders, with no
// translation step at that boundary.
//
// The Node source's own top-level try/catch collapses literally any
// failure during encoding -- an unsupported value, a cyclic reference, or
// even a caller-supplied error thrown from arbitrary code reachable during
// encoding -- into this exact same ProcessFailure, specifically so that no
// detail about the offending value (e.g. a hidden property's name or a
// getter's return value) can leak into the error text. This package's
// Value model has no analogue for hidden properties, getters, or proxies,
// but the fail-closed, no-detail contract is preserved: every failure path
// in this file returns this identical sentinel, never a value- or
// type-specific message. Every ported test compares against this sentinel
// by pointer identity (`err != ErrInvalidArtifactJSON`), never by field
// access, so it stays a package-level var rather than something
// constructed fresh per call.
var ErrInvalidArtifactJSON = procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
	Code:     "INVALID_ARTIFACT_JSON",
	Category: procerr.CategoryDomain,
	Message:  "artifact JSON must contain plain JSON values with finite lossless numbers",
})

// artifactAncestors tracks the map/slice data pointers currently being
// encoded on the current recursion path, ported from python-lossless-
// artifact.ts's `ancestors: WeakSet<object>` cycle guard. Go's Value trees
// are built from map[string]any and []any, both reference types, so a
// self-referential map (m["k"] = m) or slice (s[0] = s) is constructible
// and must be rejected the same way a cyclic JS object is.
type artifactAncestors map[uintptr]bool

// RenderLosslessArtifactJSON matches
// json.dumps(value, ensure_ascii=True, indent=2, sort_keys=True) + "\n" for
// the finite-lossless-number artifact contract. Ports
// renderPythonLosslessArtifactJson from
// the original implementation.
//
// Every failure -- an unsupported Go value, a non-finite or non-safe-
// integer plain float64, an out-of-grammar json.Number lexeme, or a
// reference cycle -- returns ErrInvalidArtifactJSON and nothing else,
// matching the Node function's fail-closed collapse of every internal
// error into one fixed ProcessFailure. The recover() below is a defensive
// backstop for the same reason the Node source's catch-all is: no failure
// path, however it arises, should surface as anything but the generic
// error.
func RenderLosslessArtifactJSON(value Value) (rendered string, err error) {
	defer func() {
		if r := recover(); r != nil {
			rendered, err = "", ErrInvalidArtifactJSON
		}
	}()
	encoded, encodeErr := encodeArtifactValue(value, 0, artifactAncestors{})
	if encodeErr != nil {
		return "", ErrInvalidArtifactJSON
	}
	return encoded + "\n", nil
}

// encodeArtifactValue ports python-lossless-artifact.ts's encode. Every Go
// value expressible in this package's Value model (see render.go's Value
// doc comment) is handled explicitly; anything else -- a Go type outside
// that model, e.g. a custom struct, channel, or *big.Int -- is rejected the
// same way the Node source rejects a JS value that is not a plain JSON
// container/primitive (a Date, a class instance, a BigInt, and so on).
func encodeArtifactValue(value any, level int, ancestors artifactAncestors) (string, error) {
	switch v := value.(type) {
	case nil:
		return "null", nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case string:
		return encodeArtifactString(v), nil
	case json.Number:
		return encodeArtifactNumber(v)
	case float64:
		return encodeArtifactFloat(v)
	case []any:
		return encodeArtifactArray(v, level, ancestors)
	case map[string]any:
		return encodeArtifactRecord(v, level, ancestors)
	default:
		return "", ErrInvalidArtifactJSON
	}
}

// encodeArtifactNumber ports encodeNumber's LosslessNumber branch:
// canonicalize the exact source lexeme through Python's numeric model
// (CanonicalNumberToken, this package's port of canonicalPythonNumberToken
// from python-number.ts), preserving arbitrary-size integer tokens exactly.
func encodeArtifactNumber(value json.Number) (string, error) {
	token, err := CanonicalNumberToken(string(value))
	if err != nil {
		return "", ErrInvalidArtifactJSON
	}
	return token, nil
}

// encodeArtifactFloat ports encodeNumber's plain-`number` branch: unlike
// render.go's formatNumber (which also accepts arbitrary finite floats via
// FiniteFloatToken, matching python-compatible.ts's more permissive
// contract), the lossless-artifact contract only accepts a plain float64
// that is a safe integer -- anything else (a genuine fraction, or an
// integer-valued but unsafe magnitude) must have arrived as a lossless
// json.Number token instead, and is rejected here.
func encodeArtifactFloat(value float64) (string, error) {
	if !isSafeInteger(value) {
		return "", ErrInvalidArtifactJSON
	}
	if value == 0 && math.Signbit(value) {
		return "0", nil
	}
	return strconv.FormatInt(int64(value), 10), nil
}

// encodeArtifactString ports encodePythonString: JSON string escaping
// (quote, backslash, the standard \b\t\n\f\r control-char shorthands, and
// \uXXXX for every other unit below 0x20) plus \uXXXX for every unit at or
// above 0x7F -- one code point earlier than render.go's encodeString, which
// leaves 0x7F (DEL) literal. See this file's package doc comment.
func encodeArtifactString(value string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, unit := range utf16Units(value) {
		encodeArtifactStringUnit(&sb, unit)
	}
	sb.WriteByte('"')
	return sb.String()
}

// encodeArtifactStringUnit writes the escaped or literal form of one
// UTF-16 code unit, per encodeArtifactString's doc comment.
func encodeArtifactStringUnit(sb *strings.Builder, unit uint16) {
	switch unit {
	case 0x22:
		sb.WriteString(`\"`)
		return
	case 0x5c:
		sb.WriteString(`\\`)
		return
	case 0x08:
		sb.WriteString(`\b`)
		return
	case 0x09:
		sb.WriteString(`\t`)
		return
	case 0x0a:
		sb.WriteString(`\n`)
		return
	case 0x0c:
		sb.WriteString(`\f`)
		return
	case 0x0d:
		sb.WriteString(`\r`)
		return
	}
	if unit < 0x20 || unit >= 0x7f {
		fmt.Fprintf(sb, `\u%04x`, unit)
		return
	}
	sb.WriteByte(byte(unit))
}

// containerIdentity returns the runtime data pointer backing a non-empty
// map or slice, for artifactAncestors's cycle guard. Callers only invoke
// this for len(v) > 0 containers: a zero-length map or slice cannot
// possibly contain a reference to itself, and zero-size Go allocations can
// legitimately share a single runtime address, which would otherwise make
// two unrelated empty containers indistinguishable to this identity check.
func containerIdentity(value any) uintptr {
	return reflect.ValueOf(value).Pointer()
}

// encodeArtifactArray ports encodeArray, minus the property-descriptor
// checks that TypeScript function performs (rejecting non-Array-prototype
// array-likes, sparse holes, and non-index/non-enumerable/accessor
// properties): none of that has a Go analogue, since this package's Value
// model represents a JSON array only as []any, a plain, dense,
// data-only slice. Cycle detection (an array containing itself, or part of
// a longer reference cycle through other arrays/records) is still both
// possible and checked, since Go slices are reference types.
func encodeArtifactArray(items []any, level int, ancestors artifactAncestors) (string, error) {
	if len(items) == 0 {
		return "[]", nil
	}
	id := containerIdentity(items)
	if ancestors[id] {
		return "", ErrInvalidArtifactJSON
	}
	ancestors[id] = true
	defer delete(ancestors, id)

	currentIndent := strings.Repeat("  ", level)
	childIndent := strings.Repeat("  ", level+1)
	parts := make([]string, len(items))
	for i, item := range items {
		encoded, err := encodeArtifactValue(item, level+1, ancestors)
		if err != nil {
			return "", err
		}
		parts[i] = childIndent + encoded
	}
	return "[\n" + strings.Join(parts, ",\n") + "\n" + currentIndent + "]", nil
}

// encodeArtifactRecord ports encodeRecord, minus the same property-
// descriptor/prototype checks encodeArtifactArray's doc comment describes
// (no Go analogue: map[string]any is always a plain, string-keyed,
// data-only map). Keys are sorted with SortedStrings (this package's port
// of sortedStrings from python-compatible.ts), matching sort_keys=True.
func encodeArtifactRecord(object map[string]any, level int, ancestors artifactAncestors) (string, error) {
	if len(object) == 0 {
		return "{}", nil
	}
	id := containerIdentity(object)
	if ancestors[id] {
		return "", ErrInvalidArtifactJSON
	}
	ancestors[id] = true
	defer delete(ancestors, id)

	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	keys = SortedStrings(keys)

	currentIndent := strings.Repeat("  ", level)
	childIndent := strings.Repeat("  ", level+1)
	parts := make([]string, len(keys))
	for i, key := range keys {
		encoded, err := encodeArtifactValue(object[key], level+1, ancestors)
		if err != nil {
			return "", err
		}
		parts[i] = childIndent + encodeArtifactString(key) + ": " + encoded
	}
	return "{\n" + strings.Join(parts, ",\n") + "\n" + currentIndent + "}", nil
}
