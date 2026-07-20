package canonjson

import (
	"math"
	"testing"
)

// TestFiniteFloatToken exercises pythonFiniteFloatToken's spelling rules
// from node-src/json/python-number.ts: negative zero, the fixed/scientific
// notation boundary at decimal exponents -4 and 16, and zero-padded signed
// scientific exponents.
//
// Values and expected spellings were cross-checked against CPython's
// repr(float) (python3 -c 'print(repr(float(...)))') across ~2000 random
// and boundary/subnormal doubles during development, using a throwaway Go
// harness outside this package (not shipped, to keep this test suite
// stdlib-only with no runtime Python dependency); every case matched. The
// cases below are a representative subset of that larger sweep, hardcoded
// so this test has no external dependency.
func TestFiniteFloatToken(t *testing.T) {
	cases := []struct {
		name  string
		value float64
		want  string
	}{
		{"negative zero", math.Copysign(0, -1), "-0.0"},
		{"positive zero", 0, "0.0"},
		{"one half", 0.5, "0.5"},
		{"small negative exponent stays fixed", 1e-4, "0.0001"},
		{"just below fixed range goes scientific", 1e-5, "1e-05"},
		{"one micro", 1e-6, "1e-06"},
		{"integer gets trailing .0", 1.0, "1.0"},
		{"negative integer", -123.0, "-123.0"},
		{"exponent 15 stays fixed", 9007199254740991.0, "9007199254740991.0"},
		{"exponent 16 goes scientific", 1e16, "1e+16"},
		{"exponent 20 scientific", 1e20, "1e+20"},
		{"non-integer fixed", 123.456, "123.456"},
		{"many digit fixed", 3.14159265358979, "3.14159265358979"},
		{"smallest subnormal", 5e-324, "5e-324"},
		{"largest double", math.MaxFloat64, "1.7976931348623157e+308"},
		{"two", 2.0, "2.0"},
		{"one hundred", 100.0, "100.0"},
		{"multi-digit coefficient fixed", 1.5e10, "15000000000.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FiniteFloatToken(c.value)
			if err != nil {
				t.Fatalf("FiniteFloatToken(%v) returned error: %v", c.value, err)
			}
			if got != c.want {
				t.Errorf("FiniteFloatToken(%v) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}

func TestFiniteFloatTokenRejectsNonFinite(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := FiniteFloatToken(v); err != ErrNotFinite {
			t.Errorf("FiniteFloatToken(%v) error = %v, want ErrNotFinite", v, err)
		}
	}
}

// TestCanonicalNumberToken ports canonicalPythonNumberToken's contract from
// node-src/json/python-number.ts: arbitrary-size integer tokens normalize
// through big-integer parsing (including the "-0" -> "0" oddity, since
// BigInt/*big.Int have no signed zero), while any other syntactically
// valid JSON number token is re-rendered through the finite float64 path.
//
// Expectations were cross-checked against `json.loads`/`repr` (for floats)
// or `str(int(...))` (for integers) via python3 during development, the
// same way as TestFiniteFloatToken above.
func TestCanonicalNumberToken(t *testing.T) {
	cases := []struct {
		token string
		want  string
	}{
		{"0", "0"},
		{"-0", "0"}, // bare integer -0 collapses to 0 (BigInt has no signed zero)
		{"1", "1"},
		{"-1", "-1"},
		{"123", "123"},
		{"-123", "-123"},
		{"9007199254740991", "9007199254740991"},
		{"9007199254740992", "9007199254740992"},
		{"9007199254740993", "9007199254740993"}, // exact beyond float64's safe range
		{"123456789012345678901234567890", "123456789012345678901234567890"},
		{"-123456789012345678901234567890", "-123456789012345678901234567890"},
		{"1.0", "1.0"},
		{"-1.0", "-1.0"},
		{"-0.0", "-0.0"}, // float token -0.0 keeps its sign, unlike bare "-0"
		{"0.5", "0.5"},
		{"1e-6", "1e-06"},
		{"1e20", "1e+20"},
		{"1e16", "1e+16"},
		{"1e15", "1000000000000000.0"},
		{"1.5e10", "15000000000.0"},
		{"10e-1", "1.0"},
		{"0.10e1", "1.0"},
		{"1E5", "100000.0"},
		{"1e+5", "100000.0"},
		{"1e-05", "1e-05"},
		{"100", "100"},
		{"100.0", "100.0"},
		{"3.14159265358979", "3.14159265358979"},
		{"0.0001", "0.0001"},
		{"0.00001", "1e-05"},
		{"9007199254740991.0", "9007199254740991.0"},
		{"9007199254740992.0", "9007199254740992.0"},
	}
	for _, c := range cases {
		t.Run(c.token, func(t *testing.T) {
			got, err := CanonicalNumberToken(c.token)
			if err != nil {
				t.Fatalf("CanonicalNumberToken(%q) returned error: %v", c.token, err)
			}
			if got != c.want {
				t.Errorf("CanonicalNumberToken(%q) = %q, want %q", c.token, got, c.want)
			}
		})
	}
}

// TestCanonicalNumberTokenRejectsInvalid covers overflow (an exponent so
// large the float path saturates to +/-Inf, which Number.isFinite/
// FiniteFloatToken then rejects) and lexically invalid tokens.
func TestCanonicalNumberTokenRejectsInvalid(t *testing.T) {
	for _, token := range []string{
		"1e400", "1e999999", "-1e400", // overflow to +/-Inf
		"007",      // leading zero: not valid JSON integer syntax
		"abc",      // not a number at all
		"1.",       // JSON requires digits after the decimal point
		".5",       // JSON requires digits before the decimal point
		"1e",       // exponent marker with no digits
		"",         // empty
		"01",       // leading zero
		"+1",       // JSON numbers never have a leading +
		"Infinity", // JS-only spelling, not a JSON number token
		"NaN",
	} {
		t.Run(token, func(t *testing.T) {
			if got, err := CanonicalNumberToken(token); err == nil {
				t.Errorf("CanonicalNumberToken(%q) = %q, want error", token, got)
			}
		})
	}
}
