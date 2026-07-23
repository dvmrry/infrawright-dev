// Package transform ports the pull-transform kernel and resource-selection
// logic that turn raw provider-API pull items into the snake-cased,
// schema-projected, override-applied records the rest of the runtime (the
// artifact writer and batch runner, both owned elsewhere) renders to disk.
//
// It ports two Node sources:
//
//   - node-src/domain/pull-transform.ts: the kernel. snake_case/slug naming
//     (via go/internal/pyunicode.PythonLower151), Terraform schema
//     projection compilation and application, the override vocabulary
//     (renames, key_field, invert_bool, split_csv, sort_lists, skip_if,
//     acknowledged_drops, ...), the two-pass HTML unescape/escape seam
//     (go/internal/pyunicode.PythonHTMLUnescapeGeneric is this port's
//     equivalent of the Node source's pluggable htmlUnescape parameter),
//     drop-diagnostic classification, and Python-compatible numeric/string
//     handling (via go/internal/canonjson).
//   - node-src/domain/transform-selection.ts: resource selection and
//     referent-first reference ordering (a Tarjan SCC over the merged
//     `references` tables from active pack manifests).
//
// This package deliberately does NOT include the artifact-writing or batch
// runner logic (node-src/domain/transform-artifacts.ts,
// node-src/domain/transform-runner.ts) -- those own a different, later
// slice per docs/go-runtime-plan.md and are out of this package's scope.
//
// # A note on JSON object representation and iteration order
//
// Every JSON object in this package's dynamic value tree is a Go
// map[string]any (see go/internal/canonjson's Value doc comment), which,
// unlike a JS object, has no defined iteration order -- and unlike the Node
// source's own object representation (lossless-json's parse, which
// preserves source-text key order), that order information is already
// discarded by the time a value reaches this package: go/internal/canonjson's
// decoder (encoding/json's Decoder with UseNumber) stores decoded objects in
// an ordinary Go map. This package follows the convention already
// established by go/internal/metadata (see validation.go's sortedKeys /
// validateStringMap doc comments): every place the Node source iterates
// Object.keys(x) is walked here in Python/Node-compatible sorted order
// (canonjson.SortedStrings) for deterministic, reviewable behavior, whether
// or not the Node source itself sorted that particular walk.
//
// This is judged safe (parity-preserving, not merely deterministic) almost
// everywhere in this file, because nearly every walk over an object's keys
// here feeds a canonjson.Render call downstream (owned by the artifact
// layer), which itself always re-sorts keys before emitting bytes -- so the
// *order bytes reach a reader* never depends on this package's internal
// walk order. The one call site where that reasoning does not automatically
// apply -- snakeKeys's strict-collision detection, which the Node source
// gates on Object.keys() encounter order to decide which of two colliding
// raw keys "wins" -- is called out at its own definition (see snakeName.go)
// as a known, narrow divergence, following the same "known, deliberate
// divergence" documentation convention.
package transform

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// TransformRecord is the Go analogue of the TransformRecord alias
// (Record<string, unknown>) in node-src/domain/pull-transform.ts.
type TransformRecord = map[string]any

// TransformError reports a transform kernel validation failure -- the Go
// analogue of the bare `throw new TypeError(message)` / `throw new
// Error(message)` calls pull-transform.ts and transform-selection.ts make
// directly (neither source routes these through the ProcessFailure type in
// node-src/domain/errors.ts; that type is reserved in this package for the
// small locally-ported slice of node-src/domain/roots.ts's own
// ProcessFailure-raising domainError, see selection_roots.go).
type TransformError struct{ message string }

// Error implements the error interface.
func (e *TransformError) Error() string { return e.message }

// fail panics with a *TransformError, mirroring a Node `throw new
// TypeError(...)` / `throw new Error(...)` call site. See recoverErr for how
// this composes with this package's exported entry points.
func fail(message string) {
	panic(&TransformError{message: message})
}

// failf is fail with fmt.Sprintf formatting.
func failf(format string, args ...any) {
	fail(fmt.Sprintf(format, args...))
}

// recoverErr is deferred by every exported entry point in this package (as
// `defer recoverErr(&err)`) to convert a recovered *TransformError or
// *procerr.ProcessFailure panic into a normal error return, following the
// same panic/recover convention go/internal/metadata's fail/
// recoverMetadataError establishes: the many small, deeply (and often
// mutually) recursive validation helpers ported from pull-transform.ts and
// transform-selection.ts abandon the current operation from arbitrary call
// depth via panic, exactly like the Node source's `throw`, instead of
// threading an explicit error return through every intermediate function.
// Any other recovered value is re-panicked, since it indicates a genuine bug
// rather than an expected validation failure.
func recoverErr(err *error) {
	r := recover()
	if r == nil {
		return
	}
	switch e := r.(type) {
	case *TransformError:
		*err = e
	case *procerr.ProcessFailure:
		*err = e
	default:
		panic(r)
	}
}

// hasOwn reports whether record has an explicit entry for key, the Go
// analogue of Object.prototype.hasOwnProperty.call(record, key) /
// Object.hasOwn(record, key) throughout the Node source. A Go map has no
// prototype chain to distinguish from own properties, so a plain
// comma-ok lookup is exact.
func hasOwn(record map[string]any, key string) bool {
	_, ok := record[key]
	return ok
}

// mapKeys returns m's keys in unspecified (Go map) order. Callers that need
// determinism use sortedObjectKeys / sortedKeys instead; this exists only
// for the few call sites that immediately sort or set-ify the result
// themselves.
func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// sortedObjectKeys returns m's keys in Python/Node-compatible code-point
// order (canonjson.SortedStrings(Object.keys(m))), the Go analogue of the
// Node source's pervasive `sortedStrings(Object.keys(x))` idiom -- and,
// per this package's doc comment, this package's chosen deterministic
// stand-in for the walks the Node source itself does not explicitly sort.
func sortedObjectKeys[V any](m map[string]V) []string {
	return canonjson.SortedStrings(mapKeys(m))
}

// isPlainJSONRecord reports whether value is a JSON object in this
// package's dynamic value tree (map[string]any). Ports isPlainJsonRecord
// from node-src/domain/pull-transform.ts, whose extra checks (own-property
// descriptor enumerability, absence of a non-Object.prototype prototype,
// absence of symbol keys) exist only to defend against JS objects
// constructed in ways a bare `typeof value === "object"` test would miss
// (getters/setters, Object.create(customProto), symbol-keyed properties).
// None of that has a Go analogue: every map[string]any in this package's
// tree is, by construction, exactly the shape isPlainJsonRecord verifies.
func isPlainJSONRecord(value any) bool {
	return canonjson.IsJSONRecord(value)
}

// stringArraySlice ports the local `stringArray` helper from
// node-src/domain/pull-transform.ts: value must be a JSON array of strings,
// or absent (nil/JSON null), which yields an empty result exactly like the
// Node source's `if (value === undefined || value === null) return [];`.
func stringArraySlice(value any, label string) []string {
	if value == nil {
		return nil
	}
	arr, ok := value.([]any)
	if !ok {
		failf("%s must be a list of strings", label)
	}
	out := make([]string, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			failf("%s must be a list of strings", label)
		}
		out[i] = s
	}
	return out
}

// stringValueMap ports the local `stringMap` helper from
// node-src/domain/pull-transform.ts: value must be a JSON object whose
// values are all strings, or absent, which yields an empty map.
func stringValueMap(value any, label string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	obj, ok := value.(map[string]any)
	if !ok {
		failf("%s must be an object", label)
	}
	out := make(map[string]string, len(obj))
	for _, key := range sortedObjectKeys(obj) {
		item := obj[key]
		s, ok := item.(string)
		if !ok {
			failf("%s.%s must be a string", label, key)
		}
		out[key] = s
	}
	return out
}

// objectMap ports the local `objectMap` helper from
// node-src/domain/pull-transform.ts: value must be a JSON object, or
// absent, which yields an empty object.
func objectMap(value any, label string) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	obj, ok := value.(map[string]any)
	if !ok {
		failf("%s must be an object", label)
	}
	return obj
}

// cloneJson ports cloneJson from node-src/domain/pull-transform.ts: a deep
// copy that also enforces the transform's closed value vocabulary (JSON
// null/bool/string, a canonicalized lossless number, array, or nested
// record only) -- rejecting a bare Go float64 exactly like the Node source
// rejects a bare JS `number`, since every numeric leaf reaching this
// package from decoded JSON must already be a json.Number (this package's
// analogue of LosslessNumber; see go/internal/canonjson's Value doc
// comment). A bare float64 can only appear here if Go caller code
// constructs one directly (mirroring how a raw JS `number` literal can only
// appear in a hand-built test fixture, never from lossless-json parsing);
// go/internal/transform's own tests exercise this exact rejection the same
// way transform-runtime-artifacts.test.ts does.
func cloneJson(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case bool:
		return v
	case string:
		return v
	case json.Number:
		token, err := canonjson.CanonicalNumberToken(string(v))
		if err != nil {
			fail("transform accepts only finite losslessly parsed JSON numbers")
		}
		return json.Number(token)
	case float64:
		fail("raw transform numeric tokens must be LosslessNumber values parsed from JSON")
		panic("unreachable")
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneJson(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for _, key := range sortedObjectKeys(v) {
			out[key] = cloneJson(v[key])
		}
		return out
	default:
		fail("transform input must contain JSON values only")
		panic("unreachable")
	}
}

// jsonQuote approximates JSON.stringify(s) for embedding raw values in
// human-readable validation error text, matching the convention (and the
// same "not a byte-for-byte contract" caveat) established by
// go/internal/metadata's own jsonQuote helper: this package's ported tests
// assert error text with substring/regexp matches (mirroring the Node
// tests' own assert.throws(..., /regexp/) usage), never full-string
// equality against an interpolated, quoted value.
func jsonQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// sortStrings sorts s in place by Python/Node-compatible code-point order
// (canonjson.ComparePythonStrings), the Go analogue of `[...value].sort()`
// / `Array.prototype.sort()` (which, unlike sortedStrings, mutates and
// returns the input) at the handful of Node call sites that sort a value
// already known to be a []string rather than building one via
// sortedStrings(Object.keys(...)).
func sortStrings(values []string) {
	sort.Slice(values, func(i, j int) bool {
		return canonjson.ComparePythonStrings(values[i], values[j]) < 0
	})
}
