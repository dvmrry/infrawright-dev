// Package canonjson renders and decodes the canonical, Python-compatible
// JSON dialect used throughout infrawright: `json.dumps(value, indent=2,
// sort_keys=True)` byte semantics, backed by a dynamic value tree instead of
// generated structs. Every exported symbol documents the Node source file
// (under the original source treejson/) whose frozen behavior it ports; that TypeScript
// remains the differential oracle until this port is qualified.
package canonjson

import (
	"errors"
	"math"
	"math/big"
	"regexp"
	"strconv"
)

// ErrNotFinite is returned by FiniteFloatToken when asked to render a NaN or
// infinite value. Python JSON has no spelling for either, matching the
// Node.js pythonFiniteFloatToken contract of returning null for them.
var ErrNotFinite = errors.New("canonjson: value is not a finite number")

// ErrInvalidNumberToken is returned by CanonicalNumberToken when the input
// lexeme is not a valid JSON number token, matching
// canonicalPythonNumberToken returning null in the original implementation.
var ErrInvalidNumberToken = errors.New("canonjson: invalid JSON number token")

// jsonIntegerToken matches a bare JSON integer lexeme: an optional minus
// sign, then "0" or a non-zero digit followed by more digits. No fraction,
// no exponent. Ports the JSON_INTEGER_TOKEN regexp in the original implementation.
var jsonIntegerToken = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)

// jsonNumberToken matches the full JSON number grammar (optional fraction,
// optional exponent). Ports the JSON_NUMBER_TOKEN regexp in
// the original implementation.
var jsonNumberToken = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

// FiniteFloatToken renders one finite IEEE-754 double the way CPython's
// repr(float) (equivalently json.dumps of a float) does. It ports
// pythonFiniteFloatToken from the original implementation verbatim,
// including its two load-bearing oddities:
//
//   - negative zero always renders as the literal "-0.0";
//   - CPython's shortest round-trip representation uses fixed notation for
//     decimal exponents in [-4, 16) and scientific notation everywhere else,
//     with the scientific exponent always signed and zero-padded to at
//     least two digits (e.g. "1e-06", "1e+20").
//
// The Node source derives its decimal digits from
// Number.prototype.toExponential(), which yields the shortest decimal that
// round-trips back to the same float64. strconv.FormatFloat(v, 'e', -1, 64)
// is the Go stdlib's equivalent shortest-round-trip primitive, so this
// function reuses it for the digit string and then reapplies the Node
// reassembly logic to the parsed digits/exponent, ignoring Go's own
// notation and padding choices (which differ cosmetically from JS's but
// carry the same digits and exponent value).
//
// Returns ErrNotFinite for NaN and +/-Inf, mirroring the Node function
// returning null for non-finite input.
func FiniteFloatToken(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", ErrNotFinite
	}
	if value == 0 && math.Signbit(value) {
		return "-0.0", nil
	}

	sign := ""
	if value < 0 {
		sign = "-"
	}

	// strconv.FormatFloat with 'e' and precision -1 gives the shortest
	// decimal string that round-trips to math.Abs(value), the same
	// guarantee JS's toExponential() (no precision argument) makes. The
	// concrete formatting (padding, decimal point placement) differs
	// between the two runtimes, so only the digit string and exponent
	// magnitude are reused below.
	formatted := strconv.FormatFloat(math.Abs(value), 'e', -1, 64)
	mantissa, rawExponent, ok := splitExponential(formatted)
	if !ok {
		// Unreachable for any finite, non-NaN float64 given Go's 'e'
		// formatter, but guarded rather than assumed.
		return "", ErrInvalidNumberToken
	}
	exponent, err := strconv.Atoi(rawExponent)
	if err != nil {
		return "", ErrInvalidNumberToken
	}
	digits := removeDecimalPoint(mantissa)

	// CPython's shortest representation uses fixed notation for decimal
	// exponents in [-4, 15], and scientific notation everywhere else.
	if exponent >= -4 && exponent < 16 {
		point := exponent + 1
		var body string
		switch {
		case point <= 0:
			body = "0." + zeros(-point) + digits
		case point >= len(digits):
			body = digits + zeros(point-len(digits)) + ".0"
		default:
			body = digits[:point] + "." + digits[point:]
		}
		return sign + body, nil
	}

	var coefficient string
	if len(digits) == 1 {
		coefficient = digits
	} else {
		coefficient = digits[:1] + "." + digits[1:]
	}
	exponentSign := "+"
	absExponent := exponent
	if exponent < 0 {
		exponentSign = "-"
		absExponent = -exponent
	}
	exponentDigits := strconv.Itoa(absExponent)
	if len(exponentDigits) < 2 {
		exponentDigits = "0" + exponentDigits
	}
	return sign + coefficient + "e" + exponentSign + exponentDigits, nil
}

// splitExponential splits a strconv.FormatFloat(..., 'e', ...) result of the
// form "<mantissa>e<signed-exponent>" into its two halves.
func splitExponential(formatted string) (mantissa, exponent string, ok bool) {
	for index := 0; index < len(formatted); index++ {
		if formatted[index] == 'e' {
			return formatted[:index], formatted[index+1:], true
		}
	}
	return "", "", false
}

// removeDecimalPoint strips a single "." from a decimal digit string,
// mirroring the Node code's `mantissa.replace(".", "")`.
func removeDecimalPoint(mantissa string) string {
	for index := 0; index < len(mantissa); index++ {
		if mantissa[index] == '.' {
			return mantissa[:index] + mantissa[index+1:]
		}
	}
	return mantissa
}

// zeros returns a string of n '0' characters (n <= 0 yields "").
func zeros(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = '0'
	}
	return string(buf)
}

// CanonicalNumberToken canonicalizes one losslessly parsed JSON number
// lexeme through Python's numeric model, porting canonicalPythonNumberToken
// from the original implementation: arbitrary-size integer tokens are
// normalized (via math/big, mirroring the Node code's BigInt normalization)
// and remain exact, while any other syntactically valid JSON number token is
// re-rendered through the finite binary64 value and spelling that
// json.loads/json.dumps would produce.
//
// Note the integer-vs-float branch is decided purely by lexical shape (does
// the token contain a "." or exponent marker), matching a load-bearing
// Python oddity: the bare integer token "-0" canonicalizes to "0" (BigInt
// has no signed zero), while the float token "-0.0" canonicalizes to the
// distinct "-0.0" via the float path below.
//
// Returns ErrInvalidNumberToken for any lexeme outside the JSON number
// grammar, mirroring the Node function returning null.
func CanonicalNumberToken(token string) (string, error) {
	if jsonIntegerToken.MatchString(token) {
		n, ok := new(big.Int).SetString(token, 10)
		if !ok {
			return "", ErrInvalidNumberToken
		}
		return n.String(), nil
	}
	if !jsonNumberToken.MatchString(token) {
		return "", ErrInvalidNumberToken
	}
	value, err := strconv.ParseFloat(token, 64)
	if err != nil {
		var numErr *strconv.NumError
		// ErrRange means the token parsed but rounds to +/-Inf (e.g. an
		// enormous exponent); JS's Number(token) does the same and the
		// FiniteFloatToken call below rejects the resulting infinity,
		// matching Number.isFinite in the Node source. Any other error
		// would mean our two regexps above accepted something
		// strconv.ParseFloat cannot parse, which should not happen.
		if !errors.As(err, &numErr) || !errors.Is(numErr.Err, strconv.ErrRange) {
			return "", ErrInvalidNumberToken
		}
	}
	token, floatErr := FiniteFloatToken(value)
	if floatErr != nil {
		// Token was lexically a JSON number but overflowed to +/-Inf, e.g.
		// "1e999". JS's Number(token) does the same and pythonFiniteFloatToken
		// then rejects the infinity, matching Number.isFinite in the Node
		// source: the whole token canonicalizes to null.
		return "", ErrInvalidNumberToken
	}
	return token, nil
}
