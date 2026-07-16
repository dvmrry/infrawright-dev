package canonjson

import (
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// IsJSONRecord reports whether value is a JSON object in this package's
// value tree, i.e. map[string]any. Ports isJsonRecord from
// node-src/json/python-equality.ts.
//
// The Node version additionally excludes LosslessNumber instances (which
// are themselves objects at the JS runtime level, but never valid JSON
// records). That exclusion has no Go analogue: json.Number is a string
// type, not a map, so it can never satisfy a map[string]any type assertion
// in the first place.
func IsJSONRecord(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

// numericKind distinguishes the two branches Python's own numeric tower
// distinguishes for JSON purposes: exact arbitrary-precision integers, and
// binary64 floats.
type numericKind int

const (
	numericInteger numericKind = iota
	numericFloat
)

// numeric is the Go analogue of the NumericValue interface in
// node-src/json/python-equality.ts.
type numeric struct {
	kind    numericKind
	integer *big.Int
	float   float64
}

// numericValue classifies value as a JSON boolean, lossless integer/float
// token, or plain float64, mirroring numericValue in
// node-src/json/python-equality.ts. It returns (numeric{}, false) for any
// value with no numeric interpretation at all (null, string, array,
// object).
//
// For a json.Number holding a non-integer lexeme, the Node source parses it
// with `Number(token)` unconditionally -- never rejecting the token, even
// if it were malformed -- so this always reports ok=true once value is
// known to be a json.Number; strconv.ParseFloat's only possible failure
// here is ErrRange (an enormous exponent saturating to +/-Inf, which
// JS's Number() also does silently), which is not an error for our
// purposes: the resulting +/-Inf is a legitimate (if exotic) numeric
// classification that later comparisons handle explicitly.
func numericValue(value any) (numeric, bool) {
	switch v := value.(type) {
	case bool:
		i := int64(0)
		if v {
			i = 1
		}
		return numeric{kind: numericInteger, integer: big.NewInt(i)}, true
	case json.Number:
		token := string(v)
		if jsonIntegerToken.MatchString(token) {
			n, ok := new(big.Int).SetString(token, 10)
			if ok {
				return numeric{kind: numericInteger, integer: n}, true
			}
			// Unreachable: SetString cannot fail for a token that just
			// matched the integer grammar.
		}
		f, _ := strconv.ParseFloat(token, 64)
		return numeric{kind: numericFloat, float: f}, true
	case float64:
		if isSafeInteger(v) && !(v == 0 && math.Signbit(v)) {
			return numeric{kind: numericInteger, integer: big.NewInt(int64(v))}, true
		}
		return numeric{kind: numericFloat, float: v}, true
	default:
		return numeric{}, false
	}
}

// numericallyEqual ports numericallyEqual from
// node-src/json/python-equality.ts: same-kind values compare directly
// (bigint equality, or float64 `==` so NaN-vs-NaN is false and 0-vs--0 is
// true, exactly like JS's `===`), while an integer compared against a
// float requires the float to be finite, integral, and equal in value to
// the integer -- matching Python's `1 == 1.0` without truncating either
// operand.
func numericallyEqual(left, right numeric) bool {
	if left.kind == numericInteger && right.kind == numericInteger {
		return left.integer.Cmp(right.integer) == 0
	}
	if left.kind == numericFloat && right.kind == numericFloat {
		return left.float == right.float
	}
	var integer *big.Int
	var floatValue float64
	if left.kind == numericInteger {
		integer, floatValue = left.integer, right.float
	} else {
		integer, floatValue = right.integer, left.float
	}
	if !isFiniteInteger(floatValue) {
		return false
	}
	return bigIntFromFloat(floatValue).Cmp(integer) == 0
}

func isFiniteInteger(f float64) bool {
	return !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f)
}

// bigIntFromFloat converts a finite, integral float64 to an exact *big.Int,
// mirroring the Node source's `BigInt(float)` (valid there for the same
// reason: float is checked integral and finite first).
func bigIntFromFloat(f float64) *big.Int {
	bf := new(big.Float).SetFloat64(f)
	i, _ := bf.Int(nil)
	return i
}

// exactDecimal is the Go analogue of the ExactDecimalValue interface in
// node-src/json/python-equality.ts: an exact decimal value represented as a
// signed integer coefficient with trailing/leading zeros normalized away
// and a base-10 exponent, so comparison never touches binary floating
// point.
type exactDecimal struct {
	coefficient string
	exponent    *big.Int
	sign        int
}

// exactDecimalToken matches the full JSON number grammar with capture
// groups for sign, integer part, fraction, and exponent. Ports
// EXACT_DECIMAL from node-src/json/python-equality.ts.
var exactDecimalToken = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

// exactDecimalValue parses value into an exact decimal (coefficient +
// exponent + sign), losslessly: unlike numericValue, this never routes
// through a binary64 float comparison, so it can tell 9007199254740993
// apart from 9007199254740993.0 exactly. Ports exactDecimalValue from
// node-src/json/python-equality.ts.
//
// The Node source calls `value.toString()` for a plain, non-lossless
// `number`, relying on it being a valid JSON-number-shaped decimal string.
// This reuses FiniteFloatToken (this package's own shortest-round-trip
// spelling) for the equivalent Go float64 case instead of reimplementing
// JS's Number.prototype.toString() cosmetic fixed/scientific thresholds
// (which differ from Python's): both are shortest-round-trip digit
// sequences for the same float64, just split into fixed/scientific
// notation at different exponent thresholds, and exactDecimalValue's
// subsequent coefficient/exponent normalization below is notation-agnostic
// -- it produces the same ExactDecimal regardless of which valid spelling
// it is fed.
func exactDecimalValue(value any) (exactDecimal, bool) {
	var token string
	switch v := value.(type) {
	case json.Number:
		token = string(v)
	case float64:
		t, err := FiniteFloatToken(v)
		if err != nil {
			return exactDecimal{}, false
		}
		token = t
	default:
		return exactDecimal{}, false
	}
	match := exactDecimalToken.FindStringSubmatch(token)
	if match == nil {
		return exactDecimal{}, false
	}
	signText, integerPart, fraction, exponentText := match[1], match[2], match[3], match[4]

	coefficient := strings.TrimLeft(integerPart+fraction, "0")
	if coefficient == "" {
		return exactDecimal{coefficient: "0", exponent: big.NewInt(0), sign: 0}, true
	}
	trailingZeros := 0
	for strings.HasSuffix(coefficient, "0") {
		coefficient = coefficient[:len(coefficient)-1]
		trailingZeros++
	}
	exponent := big.NewInt(0)
	if exponentText != "" {
		e, ok := new(big.Int).SetString(exponentText, 10)
		if !ok {
			return exactDecimal{}, false
		}
		exponent = e
	}
	exponent = new(big.Int).Sub(exponent, big.NewInt(int64(len(fraction))))
	exponent = new(big.Int).Add(exponent, big.NewInt(int64(trailingZeros)))

	sign := 1
	if signText == "-" {
		sign = -1
	}
	return exactDecimal{coefficient: coefficient, exponent: exponent, sign: sign}, true
}

func exactDecimalsEqual(left, right exactDecimal) bool {
	return left.sign == right.sign &&
		left.coefficient == right.coefficient &&
		left.exponent.Cmp(right.exponent) == 0
}

// jsonEqual implements the shared recursive comparison behind JSONEqual,
// TerraformJSONEqual, and TerraformJSONExactlyEqual. The three Node
// functions they port (pythonJsonEqual, terraformJsonEqual,
// terraformJsonExactlyEqual) differ only in how they classify+compare
// numbers and whether JSON booleans participate in that numeric
// comparison; the null/string/array/object recursion (including
// Python-ordered key comparison for objects) is identical, word-for-word,
// three times over in the Node source. numberEqual and boolIsNumeric let
// one Go implementation parametrize over that difference instead of
// tripling the recursion.
//
//   - boolIsNumeric true (JSONEqual/pythonJsonEqual): a JSON boolean is
//     compared as the integer 0 or 1, e.g. true == 1.
//   - boolIsNumeric false (both Terraform variants): a JSON boolean only
//     ever equals another JSON boolean with the same value; it is checked,
//     and short-circuited on, before numberEqual ever runs.
func jsonEqual(left, right any, boolIsNumeric bool, numberEqual func(left, right any) (equal, anyNumeric bool)) bool {
	if !boolIsNumeric {
		leftBool, leftIsBool := left.(bool)
		rightBool, rightIsBool := right.(bool)
		if leftIsBool || rightIsBool {
			return leftIsBool && rightIsBool && leftBool == rightBool
		}
	}
	if equal, anyNumeric := numberEqual(left, right); anyNumeric {
		return equal
	}
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftStr, leftIsStr := left.(string)
	rightStr, rightIsStr := right.(string)
	if leftIsStr || rightIsStr {
		return leftIsStr && rightIsStr && leftStr == rightStr
	}
	leftArr, leftIsArr := left.([]any)
	rightArr, rightIsArr := right.([]any)
	if leftIsArr || rightIsArr {
		if !leftIsArr || !rightIsArr || len(leftArr) != len(rightArr) {
			return false
		}
		for i := range leftArr {
			if !jsonEqual(leftArr[i], rightArr[i], boolIsNumeric, numberEqual) {
				return false
			}
		}
		return true
	}
	if IsJSONRecord(left) || IsJSONRecord(right) {
		leftObj, leftIsObj := left.(map[string]any)
		rightObj, rightIsObj := right.(map[string]any)
		if !leftIsObj || !rightIsObj {
			return false
		}
		leftKeys := SortedStrings(keysOf(leftObj))
		rightKeys := SortedStrings(keysOf(rightObj))
		if !SameStringSequence(leftKeys, rightKeys) {
			return false
		}
		for _, key := range leftKeys {
			if !jsonEqual(leftObj[key], rightObj[key], boolIsNumeric, numberEqual) {
				return false
			}
		}
		return true
	}
	return left == right
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// pythonNumberEqual is the numberEqual callback for JSONEqual and
// TerraformJSONEqual, both of which use Python's binary64 numeric equality
// (numericValue/numericallyEqual above).
func pythonNumberEqual(left, right any) (equal, anyNumeric bool) {
	leftNumber, leftOK := numericValue(left)
	rightNumber, rightOK := numericValue(right)
	if !leftOK && !rightOK {
		return false, false
	}
	return leftOK && rightOK && numericallyEqual(leftNumber, rightNumber), true
}

// exactNumberEqual is the numberEqual callback for
// TerraformJSONExactlyEqual, which never rounds through binary64
// (exactDecimalValue/exactDecimalsEqual above).
func exactNumberEqual(left, right any) (equal, anyNumeric bool) {
	leftDecimal, leftOK := exactDecimalValue(left)
	rightDecimal, rightOK := exactDecimalValue(right)
	if !leftOK && !rightOK {
		return false, false
	}
	return leftOK && rightOK && exactDecimalsEqual(leftDecimal, rightDecimal), true
}

// JSONEqual implements Python's `==` semantics across JSON int/float/bool
// values without truncating large integer tokens: JSON booleans are
// numerically 0/1 (Python's `True == 1`), and an arbitrary-precision
// integer token compares equal to a float token only when the float is
// finite, integral, and exactly that integer. Ports pythonJsonEqual from
// node-src/json/python-equality.ts.
func JSONEqual(left, right any) bool {
	return jsonEqual(left, right, true, pythonNumberEqual)
}

// TerraformJSONEqual compares Terraform JSON values with the same numeric
// equality as JSONEqual (an integer and its exactly-equivalent float
// spelling for the same cty number compare equal), except that JSON
// booleans are cty's distinct boolean type and never compare equal to 0 or
// 1. Ports terraformJsonEqual from node-src/json/python-equality.ts.
func TerraformJSONEqual(left, right any) bool {
	return jsonEqual(left, right, false, pythonNumberEqual)
}

// TerraformJSONExactlyEqual is TerraformJSONEqual's stricter sibling,
// reserved for authorization boundaries that must prove two independently
// emitted plan surfaces carry the same exact cty number: it compares
// numbers as exact decimals (coefficient + base-10 exponent), never
// rounding through binary64, so e.g. 1, 1.0, and 10e-1 all compare equal
// without precision loss from parsing huge exponents into float64. Ports
// terraformJsonExactlyEqual from node-src/json/python-equality.ts.
func TerraformJSONExactlyEqual(left, right any) bool {
	return jsonEqual(left, right, false, exactNumberEqual)
}
