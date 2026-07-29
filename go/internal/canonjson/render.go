package canonjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Value is the dynamic canonical-JSON value tree used throughout this
// package, deliberately not a generated struct (see the Go runtime contract
// Slice 0). It is produced by encoding/json's default decoding (with
// UseNumber enabled) and is documentation-only: any Go value handled by
// Render and ByteLength below is a valid Value.
//
//   - JSON null    -> untyped nil
//   - JSON boolean  -> bool
//   - JSON number   -> json.Number (the exact source lexeme; see Decode)
//     or float64 (a plain binary64, for values built in
//     Go rather than decoded from JSON)
//   - JSON string   -> string
//   - JSON array    -> []any
//   - JSON object   -> map[string]any
//
// This mirrors the Node source's JsonValue union in
// the original implementation, where a JSON number is either a plain
// JS `number` or a lossless-json `LosslessNumber` wrapping the original
// token; json.Number here plays the LosslessNumber role since it, too, is
// just the original numeric lexeme as a string. Object-key absence is
// represented the same way Go maps already represent it -- a missing map
// key -- so there is no separate "undefined" sentinel to check for, unlike
// the TypeScript, which must distinguish `undefined` from `null` explicitly.
//
// Duplicate object keys can only arise while decoding; Decode inherits
// encoding/json's last-key-wins behavior (see decode.go).
type Value = any

// ErrNotJSONValue is returned by Render and ByteLength when they encounter a
// Go value that is not one of the Value shapes documented above, or a
// non-finite plain float64. It is this package's single sentinel for the
// several `throw new TypeError(...)` call sites scattered across
// the original implementation's encode/encodeNumber/encodedLength
// (e.g. "the Python-compatible renderer accepts finite JSON numbers only",
// "undefined is not a JSON value").
var ErrNotJSONValue = errors.New("canonjson: not a supported JSON value")

// ComparePythonStrings reports the Unicode-code-point ordering of left and
// right, exactly as the original implementation's
// comparePythonStrings does when it walks both strings with
// String.prototype.codePointAt.
//
// The Node implementation must walk UTF-16 code units by hand (advancing
// two units across a surrogate pair) because JS strings are UTF-16
// sequences and a naive per-code-unit comparison would sort an astral
// character (needing a surrogate pair, both units >= 0xD800) ahead of a
// BMP private-use character like U+E000, even though U+E000 is the smaller
// code point. Go strings are UTF-8, and UTF-8's encoding was designed so
// that byte-lexicographic order of valid UTF-8 always equals code-point
// order (each additional encoded byte only ever appears for a strictly
// larger code point range), so a plain byte comparison already gives the
// Node function's result with no manual surrogate handling required. See
// TestComparePythonStringsMatchesCodePointWalk, which cross-checks this
// against an explicit code-point walk (the Go analogue of the TS
// algorithm) over both the ported test vectors and generated astral/BMP
// cases.
func ComparePythonStrings(left, right string) int {
	return strings.Compare(left, right)
}

// SameStringSequence reports whether left and right hold the same strings
// in the same order. Ports sameStringSequence from
// the original implementation.
func SameStringSequence(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i, v := range left {
		if v != right[i] {
			return false
		}
	}
	return true
}

// SortedStrings returns a new slice holding values sorted by
// ComparePythonStrings (Unicode code-point order), leaving values
// untouched. Ports sortedStrings from the original implementation.
// Like the Node Array.prototype.sort it wraps, ties are broken stably.
func SortedStrings(values []string) []string {
	out := make([]string, len(values))
	copy(out, values)
	sort.SliceStable(out, func(i, j int) bool {
		return ComparePythonStrings(out[i], out[j]) < 0
	})
	return out
}

// utf16Units returns the UTF-16 code units of s, matching how the Node
// source (a UTF-16-based runtime) sees the same string.
func utf16Units(s string) []uint16 {
	return utf16.Encode([]rune(s))
}

// encodeStringUnit appends the ASCII bytes renderPythonCompatibleJson's
// string encoder emits for one UTF-16 code unit to sb, matching
// encodeString in the original implementation (JSON.stringify's
// standard escapes, plus \uXXXX for every unit >= 0x80 -- including each
// half of a surrogate pair, which is how astral characters end up
// round-tripping through paired \uXXXX\uXXXX escapes).
func encodeStringUnit(sb *strings.Builder, unit uint16) {
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
	if unit < 0x20 || unit >= 0x80 {
		fmt.Fprintf(sb, `\u%04x`, unit)
		return
	}
	sb.WriteByte(byte(unit))
}

// encodeString renders one JSON string literal, ASCII-escaping every
// character above 0x7F (with surrogate-pair escapes for astral characters).
// Ports encodeString in the original implementation.
func encodeString(value string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, unit := range utf16Units(value) {
		encodeStringUnit(&sb, unit)
	}
	sb.WriteByte('"')
	return sb.String()
}

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER (2^53 - 1), the
// threshold the original implementation's encodeNumber uses to
// decide whether a plain `number` prints as a bare integer or is routed
// through the float-repr path.
const maxSafeInteger = 1<<53 - 1

// isSafeInteger mirrors JavaScript's Number.isSafeInteger.
func isSafeInteger(value float64) bool {
	return value == float64(int64(value)) && value >= -maxSafeInteger && value <= maxSafeInteger
}

// formatNumber renders one JSON number value, whether it is a json.Number
// (the lossless source lexeme, the Go analogue of the TS LosslessNumber
// branch) or a plain float64 (the Go analogue of the TS plain `number`
// branch). Ports encodeNumber in the original implementation.
func formatNumber(value any) (string, error) {
	switch v := value.(type) {
	case json.Number:
		token, err := CanonicalNumberToken(string(v))
		if err != nil {
			return "", fmt.Errorf("canonjson: %w: %q", ErrNotJSONValue, string(v))
		}
		return token, nil
	case float64:
		if isSafeInteger(v) && !(v == 0 && negativeZero(v)) {
			return strconv.FormatInt(int64(v), 10), nil
		}
		token, err := FiniteFloatToken(v)
		if err != nil {
			return "", fmt.Errorf("canonjson: %w: %v is not finite", ErrNotJSONValue, v)
		}
		return token, nil
	default:
		return "", fmt.Errorf("canonjson: %w: unsupported number type %T", ErrNotJSONValue, value)
	}
}

// negativeZero reports whether v is IEEE-754 negative zero. Split out only
// so the -0 check in formatNumber reads the same way as the -0 check in
// isSafeInteger's caller.
func negativeZero(v float64) bool {
	return math.Signbit(v)
}

// encode writes value at the given indent level to sb, following
// json.dumps(..., indent=2, sort_keys=True) formatting. Ports encode in
// the original implementation.
func encode(sb *strings.Builder, value any, level int) error {
	switch v := value.(type) {
	case nil:
		sb.WriteString("null")
		return nil
	case bool:
		if v {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
		return nil
	case json.Number, float64:
		token, err := formatNumber(v)
		if err != nil {
			return err
		}
		sb.WriteString(token)
		return nil
	case string:
		sb.WriteString(encodeString(v))
		return nil
	case []any:
		return encodeArray(sb, v, level)
	case map[string]any:
		return encodeObject(sb, v, level)
	default:
		return fmt.Errorf("canonjson: %w: unsupported value type %T", ErrNotJSONValue, value)
	}
}

func encodeArray(sb *strings.Builder, items []any, level int) error {
	if len(items) == 0 {
		sb.WriteString("[]")
		return nil
	}
	childIndent := strings.Repeat("  ", level+1)
	currentIndent := strings.Repeat("  ", level)
	sb.WriteString("[\n")
	for i, item := range items {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString(childIndent)
		if err := encode(sb, item, level+1); err != nil {
			return err
		}
	}
	sb.WriteByte('\n')
	sb.WriteString(currentIndent)
	sb.WriteByte(']')
	return nil
}

func encodeObject(sb *strings.Builder, object map[string]any, level int) error {
	if len(object) == 0 {
		sb.WriteString("{}")
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	keys = SortedStrings(keys)

	childIndent := strings.Repeat("  ", level+1)
	currentIndent := strings.Repeat("  ", level)
	sb.WriteString("{\n")
	for i, key := range keys {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString(childIndent)
		sb.WriteString(encodeString(key))
		sb.WriteString(": ")
		if err := encode(sb, object[key], level+1); err != nil {
			return err
		}
	}
	sb.WriteByte('\n')
	sb.WriteString(currentIndent)
	sb.WriteByte('}')
	return nil
}

// Render matches json.dumps(..., indent=2, sort_keys=True) for the JSON
// numbers this package supports, always ending in exactly one trailing
// newline. Ports renderPythonCompatibleJson from
// the original implementation.
func Render(value Value) (string, error) {
	var sb strings.Builder
	if err := encode(&sb, value, 0); err != nil {
		return "", err
	}
	sb.WriteByte('\n')
	return sb.String(), nil
}
