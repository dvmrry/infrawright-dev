package transform

// snake.go ports the snake_case/slug naming and Python-numeric-string
// parsing helpers from the original implementation: snakeName,
// snakeKeys/SnakeJSONKeys/SnakeJSONKeysForAuthoring, slugifyTransformKey,
// and the Python str.isdigit()/int()/float() compatible numeric-string
// parsing pull-transform.ts's numeric coercion depends on
// (PYTHON_DECIMAL_ZEROS, pythonDecimalDigit, normalizePythonDecimalDigits,
// isPythonNumericWhitespace, trimPythonNumericWhitespace,
// parsePythonInteger, normalizedPythonFloatString).

import (
	"encoding/json"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/textcompat"
)

// snakeWordBoundary and snakeAcronymBoundary port the two regexps chained
// in snakeName from the original implementation. Go's regexp package
// operates on runes (full Unicode code points) by default, matching the
// Node source's `u`-flagged `.`/`[^\n]` semantics with no extra flag
// needed; unlike JS's `u` flag, there is no separate non-Unicode mode to
// differ from.
var (
	snakeWordBoundary    = regexp.MustCompile(`(.)([A-Z][a-z]+)`)
	snakeAcronymBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// SnakeName ports snakeName from the original implementation.
//
// Note the replacement templates below use explicit ${1}/${2} braces, not
// Go's bare $1_$2: Go's regexp.Expand template syntax treats the longest
// run of letters/digits/underscores after "$" as one name, so a bare
// "$1_$2" template would look up a non-existent group named "1_" instead
// of group 1 followed by a literal underscore, silently eliding it. This
// has no JS analogue (JS's $1/$2 replacement syntax has no such ambiguity)
// and was caught by a direct probe against Go's regexp package during this
// port, not by inspection of the Node source alone.
func SnakeName(name string) string {
	half := snakeWordBoundary.ReplaceAllString(name, "${1}_${2}")
	return textcompat.Lower151(snakeAcronymBoundary.ReplaceAllString(half, "${1}_${2}"))
}

// snakeKeys recursively normalizes object keys. Sorted traversal makes
// collision resolution deterministic: the alphabetically last raw key wins.
func snakeKeys(value any, path string) any {
	if arr, ok := value.([]any); ok {
		out := make([]any, len(arr))
		for i, item := range arr {
			out[i] = snakeKeys(item, indexPath(path, i))
		}
		return out
	}
	if obj, ok := value.(map[string]any); ok {
		output := make(map[string]any, len(obj))
		for _, key := range sortedObjectKeys(obj) {
			normalized := SnakeName(key)
			output[normalized] = snakeKeys(obj[key], path+"."+key)
		}
		return output
	}
	return cloneJson(value)
}

func indexPath(path string, index int) string {
	return path + "[" + strconv.Itoa(index) + "]"
}

// SnakeJSONKeys ports snakeJsonKeys from the original implementation:
// "Recursively snake-case a losslessly parsed JSON value using Python
// rules."
func SnakeJSONKeys(value any) any {
	return snakeKeys(value, "$raw")
}

// SnakeJSONKeysForAuthoring ports snakeJsonKeysForAuthoring from
// the original implementation: "Recursively snake-case authoring
// inputs, which may be constructed with ordinary finite JavaScript numbers
// instead of coming from the lossless runtime JSON parser." The Go
// analogue accepts a bare float64 leaf (this package's stand-in for a raw
// JS `number`) instead of requiring json.Number.
func SnakeJSONKeysForAuthoring(value any) any {
	switch v := value.(type) {
	case float64:
		return v
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = SnakeJSONKeysForAuthoring(item)
		}
		return out
	}
	if obj, ok := value.(map[string]any); ok {
		out := make(map[string]any, len(obj))
		for _, key := range sortedObjectKeys(obj) {
			out[SnakeName(key)] = SnakeJSONKeysForAuthoring(obj[key])
		}
		return out
	}
	return cloneJson(value)
}

// nonAlnumRun matches slugifyTransformKey's `[^a-z0-9]+` replacement
// pattern from the original implementation.
var nonAlnumRun = regexp.MustCompile(`[^a-z0-9]+`)

// leadingOrTrailingUnderscores matches slugifyTransformKey's
// `^_+|_+$` trim pattern.
var leadingOrTrailingUnderscores = regexp.MustCompile(`^_+|_+$`)

// SlugifyTransformKey ports slugifyTransformKey from
// the original implementation.
func SlugifyTransformKey(value string) string {
	lowered := textcompat.Lower151(value)
	collapsed := nonAlnumRun.ReplaceAllString(lowered, "_")
	return leadingOrTrailingUnderscores.ReplaceAllString(collapsed, "")
}

// pythonDecimalZeros mirrors PYTHON_DECIMAL_ZEROS from
// the original implementation verbatim: the Unicode 15.1
// Decimal_Number zero code points, matching the Python 3.13 authoring
// oracle. Every Nd block is one contiguous run of ten values.
var pythonDecimalZeros = []int{
	0x30, 0x660, 0x6f0, 0x7c0, 0x966, 0x9e6, 0xa66, 0xae6,
	0xb66, 0xbe6, 0xc66, 0xce6, 0xd66, 0xde6, 0xe50, 0xed0,
	0xf20, 0x1040, 0x1090, 0x17e0, 0x1810, 0x1946, 0x19d0, 0x1a80,
	0x1a90, 0x1b50, 0x1bb0, 0x1c40, 0x1c50, 0xa620, 0xa8d0, 0xa900,
	0xa9d0, 0xa9f0, 0xaa50, 0xabf0, 0xff10, 0x104a0, 0x10d30, 0x11066,
	0x110f0, 0x11136, 0x111d0, 0x112f0, 0x11450, 0x114d0, 0x11650, 0x116c0,
	0x11730, 0x118e0, 0x11950, 0x11c50, 0x11d50, 0x11da0, 0x11f50, 0x16a60,
	0x16ac0, 0x16b50, 0x1d7ce, 0x1d7d8, 0x1d7e2, 0x1d7ec, 0x1d7f6,
	0x1e140, 0x1e2f0, 0x1e4f0, 0x1e950, 0x1fbf0,
}

// pythonDecimalDigit ports pythonDecimalDigit from
// the original implementation: a binary search over
// pythonDecimalZeros for the Decimal_Number block containing codePoint,
// returning the digit value 0-9 or nil (Go analogue of null) if codePoint
// is not a Unicode decimal digit.
func pythonDecimalDigit(codePoint rune) (int, bool) {
	low, high := 0, len(pythonDecimalZeros)-1
	zero := -1
	for low <= high {
		middle := (low + high) / 2
		candidate := pythonDecimalZeros[middle]
		if candidate <= int(codePoint) {
			zero = candidate
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	value := int(codePoint) - zero
	if zero >= 0 && value >= 0 && value <= 9 {
		return value, true
	}
	return 0, false
}

// normalizePythonDecimalDigits ports normalizePythonDecimalDigits from
// the original implementation: every Unicode decimal digit in value
// is replaced by its ASCII digit; every other character passes through
// unchanged.
func normalizePythonDecimalDigits(value string) string {
	var sb strings.Builder
	sb.Grow(len(value))
	for _, r := range value {
		if digit, ok := pythonDecimalDigit(r); ok {
			sb.WriteByte(byte('0' + digit))
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// isPythonNumericWhitespace ports isPythonNumericWhitespace from
// the original implementation: the codepoints Python's
// str.strip()/int()/float() treat as whitespace for numeric-string parsing.
func isPythonNumericWhitespace(codePoint rune) bool {
	switch {
	case codePoint >= 0x09 && codePoint <= 0x0d:
		return true
	case codePoint == 0x20:
		return true
	case codePoint == 0x85:
		return true
	case codePoint == 0xa0:
		return true
	case codePoint == 0x1680:
		return true
	case codePoint >= 0x2000 && codePoint <= 0x200a:
		return true
	case codePoint == 0x2028:
		return true
	case codePoint == 0x2029:
		return true
	case codePoint == 0x202f:
		return true
	case codePoint == 0x205f:
		return true
	case codePoint == 0x3000:
		return true
	default:
		return false
	}
}

// trimPythonNumericWhitespace ports trimPythonNumericWhitespace from
// the original implementation. Go's strings.TrimFunc over runes
// already operates code-point-at-a-time (unlike the Node source's manual
// UTF-16 surrogate-pair-aware walk, which exists only to work around JS
// strings being UTF-16 code unit sequences), so it is the exact Go
// analogue with no manual width bookkeeping needed.
func trimPythonNumericWhitespace(value string) string {
	return strings.TrimFunc(value, isPythonNumericWhitespace)
}

// pythonIntegerToken matches parsePythonInteger's grammar from
// the original implementation: an optional sign, then digits
// optionally separated by single underscores.
var pythonIntegerToken = regexp.MustCompile(`^[+-]?[0-9](?:_?[0-9])*$`)

// pythonFloatToken matches normalizedPythonFloatString's grammar from
// the original implementation.
var pythonFloatToken = regexp.MustCompile(
	`(?i)^[+-]?(?:(?:[0-9](?:_?[0-9])*(?:\.(?:[0-9](?:_?[0-9])*)?)?|\.[0-9](?:_?[0-9])*)(?:[eE][+-]?[0-9](?:_?[0-9])*)?|inf(?:inity)?|nan)$`,
)

// pythonParsedInteger is the Go analogue of parsePythonInteger's `number |
// LosslessNumber` return union: Safe holds a JS-safe-integer-range value
// (mirrored here as an int64, always exactly representable), Big holds the
// canonicalized json.Number token for anything outside that range.
// Exactly one of the two is populated when Ok is true.
type pythonParsedInteger struct {
	Ok   bool
	Safe int64
	Big  json.Number
	// IsBig reports which of Safe/Big is populated.
	IsBig bool
}

// jsMinSafeInteger / jsMaxSafeInteger mirror Number.MIN_SAFE_INTEGER /
// Number.MAX_SAFE_INTEGER, the boundary parsePythonInteger uses to decide
// between returning a plain `number` and a LosslessNumber.
var (
	jsMinSafeInteger = big.NewInt(-(1<<53 - 1))
	jsMaxSafeInteger = big.NewInt(1<<53 - 1)
)

// parsePythonInteger ports parsePythonInteger from
// the original implementation.
func parsePythonInteger(value string) pythonParsedInteger {
	stripped := normalizePythonDecimalDigits(trimPythonNumericWhitespace(value))
	if !pythonIntegerToken.MatchString(stripped) {
		return pythonParsedInteger{}
	}
	digits := strings.ReplaceAll(stripped, "_", "")
	integer, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		// Unreachable: pythonIntegerToken only admits a valid base-10
		// integer lexeme (with optional sign).
		return pythonParsedInteger{}
	}
	if integer.Cmp(jsMinSafeInteger) >= 0 && integer.Cmp(jsMaxSafeInteger) <= 0 {
		return pythonParsedInteger{Ok: true, Safe: integer.Int64()}
	}
	return pythonParsedInteger{Ok: true, IsBig: true, Big: json.Number(integer.String())}
}

// AsNumber renders a pythonParsedInteger the way pull-transform.ts's own
// callers consume parsePythonInteger's `number | LosslessNumber` result:
// as a single json.Number token, this package's uniform numeric
// representation (see the package doc comment). This has no direct Node
// analogue -- it exists only because Go, unlike TS, cannot return a `number
// | json.Number` union transparently usable as either -- and is used
// wherever the Node source's `number`-vs-`LosslessNumber` branch collapses
// to "produce this transform's canonical numeric leaf" (coerceValue's
// "number" case, dividedValue, etc).
func (p pythonParsedInteger) AsNumber() json.Number {
	if p.IsBig {
		return p.Big
	}
	return json.Number(strconv.FormatInt(p.Safe, 10))
}

// normalizedPythonFloatString ports normalizedPythonFloatString from
// the original implementation.
func normalizedPythonFloatString(value string) (string, bool) {
	stripped := normalizePythonDecimalDigits(trimPythonNumericWhitespace(value))
	if !pythonFloatToken.MatchString(stripped) {
		return "", false
	}
	return strings.ReplaceAll(stripped, "_", ""), true
}

// canonicalNumberToken is a small (string, bool) wrapper over
// canonjson.CanonicalNumberToken, this package's stand-in for the Node
// source's canonicalPythonNumberToken(...) returning null.
func canonicalNumberToken(token string) (string, bool) {
	canonical, err := canonjson.CanonicalNumberToken(token)
	if err != nil {
		return "", false
	}
	return canonical, true
}

// finiteFloatToken is a small (string, bool) wrapper over
// canonjson.FiniteFloatToken, this package's stand-in for the Node
// source's pythonFiniteFloatToken(...) returning null.
func finiteFloatToken(value float64) (string, bool) {
	token, err := canonjson.FiniteFloatToken(value)
	if err != nil {
		return "", false
	}
	return token, true
}
