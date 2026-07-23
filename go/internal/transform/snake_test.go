package transform

// snake_test.go ports the SnakeName/SlugifyTransformKey vectors from
// node-tests/python-lower-151.test.ts ("snake regex matches Python dot
// boundaries and Unicode code points" and "slug output and collisions
// retain Python expansion behavior") -- the rest of that Node test file
// exercises go/internal/pyunicode.PythonLower151 directly, already covered
// by that package's own (green) test suite, so is not re-ported here.
// Also covers parsePythonInteger/normalizedPythonFloatString, the
// Python-compatible numeric-string grammar coercePrimitive's "number"
// branch depends on.

import (
	"testing"
)

func TestSnakeNameMatchesPythonDotBoundariesAndUnicodeCodePoints(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// A literal CR before an [A-Z][a-z]+ run gets a boundary underscore.
		{"\rFoo", "\r_foo"},
		// A literal LF does not: snakeWordBoundary's first capture group is
		// `(.)` (Go regexp's "." already excludes newline by default,
		// matching the Node source's own `[^\n]` exactly), so no boundary
		// match occurs before "Foo" -- only pythonLower151's plain
		// lowercasing applies.
		{"\nFoo", "\nfoo"},
		{" Foo", " _foo"},
		{" Foo", " _foo"},
		{"\U0001F600Foo", "\U0001F600_foo"},
		// The Node vector's "\ud800Foo" (a lone UTF-16 surrogate half) has
		// no Go string analogue -- Go strings are UTF-8 and cannot hold an
		// unpaired surrogate -- so it is not reproduced here; its intent (a
		// non-newline code unit before an [A-Z][a-z]+ run gets a boundary
		// underscore) is already covered by the vectors above.
		//
		// U+A7CB has no Python-compatible lowercase mapping (the
		// runtime-vs-Python delta python-lower-151.test.ts pins), so it
		// passes through pythonLower151 unchanged; only the ASCII "Name"
		// suffix lowercases, with a boundary underscore inserted before it.
		{"ꟋName", "Ɤ_name"},
	}
	for _, c := range cases {
		if got := SnakeName(c.input); got != c.want {
			t.Errorf("SnakeName(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestSlugifyTransformKeyRetainsPythonExpansionBehavior(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// U+0130 (LATIN CAPITAL LETTER I WITH DOT ABOVE) expands under
		// Python's lowering to "i" + a combining dot-above; slugifyTransformKey
		// then strips the non-alphanumeric combining mark, leaving "i".
		{"İ", "i"},
		// U+03A3 (GREEK CAPITAL LETTER SIGMA) lowers to final sigma (a
		// non-ASCII character) at this word-final position, itself
		// collapsed to the "_" separator by slugifyTransformKey's
		// [^a-z0-9]+ replacement.
		{"AΣC", "a_c"},
		// U+A7CB has no Python-compatible lowercase mapping and is not
		// ASCII alphanumeric, so it collapses to a separator that is then
		// trimmed away entirely by the leading/trailing "_" strip.
		{"Ɤ", ""},
	}
	for _, c := range cases {
		if got := SlugifyTransformKey(c.input); got != c.want {
			t.Errorf("SlugifyTransformKey(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParsePythonIntegerGrammar(t *testing.T) {
	cases := []struct {
		input   string
		wantOk  bool
		wantStr string
	}{
		{"42", true, "42"},
		{"-42", true, "-42"},
		{"+42", true, "42"},
		{"1_000_000", true, "1000000"},
		{"  42  ", true, "42"},
		{" 42 ", true, "42"}, // NBSP
		{"０１", true, "1"},    // fullwidth digits 0,1 -> normalized "01" -> integer 1
		{"9007199254740993", true, "9007199254740993"},
		{"", false, ""},
		{"4.2", false, ""},
		{"0x10", false, ""},
		{"1__000", false, ""}, // double underscore not allowed
		{"_42", false, ""},    // leading underscore not allowed
		{"42_", false, ""},    // trailing underscore not allowed
		{"abc", false, ""},
	}
	for _, c := range cases {
		got := parsePythonInteger(c.input)
		if got.Ok != c.wantOk {
			t.Errorf("parsePythonInteger(%q).Ok = %v, want %v", c.input, got.Ok, c.wantOk)
			continue
		}
		if c.wantOk && string(got.AsNumber()) != c.wantStr {
			t.Errorf("parsePythonInteger(%q).AsNumber() = %q, want %q", c.input, got.AsNumber(), c.wantStr)
		}
	}
}

func TestParsePythonIntegerSafeVsBigBoundary(t *testing.T) {
	// Number.MAX_SAFE_INTEGER = 2^53 - 1 = 9007199254740991.
	safe := parsePythonInteger("9007199254740991")
	if !safe.Ok || safe.IsBig {
		t.Fatalf("9007199254740991: Ok=%v IsBig=%v, want Ok=true IsBig=false", safe.Ok, safe.IsBig)
	}
	big := parsePythonInteger("9007199254740992")
	if !big.Ok || !big.IsBig {
		t.Fatalf("9007199254740992: Ok=%v IsBig=%v, want Ok=true IsBig=true", big.Ok, big.IsBig)
	}
	if string(big.AsNumber()) != "9007199254740992" {
		t.Fatalf("9007199254740992: AsNumber() = %q", big.AsNumber())
	}
}

func TestNormalizedPythonFloatStringGrammar(t *testing.T) {
	cases := []struct {
		input  string
		wantOk bool
		want   string
	}{
		{"1.5", true, "1.5"},
		{".5", true, ".5"},
		{"5.", true, "5."},
		{"1_0.2_5", true, "10.25"},
		{"1e10", true, "1e10"},
		{"1E-10", true, "1E-10"},
		{"inf", true, "inf"},
		{"Infinity", true, "Infinity"},
		{"nan", true, "nan"},
		{"  1.5  ", true, "1.5"},
		{"", false, ""},
		{"abc", false, ""},
		{"1.2.3", false, ""},
	}
	for _, c := range cases {
		got, ok := normalizedPythonFloatString(c.input)
		if ok != c.wantOk {
			t.Errorf("normalizedPythonFloatString(%q) ok = %v, want %v", c.input, ok, c.wantOk)
			continue
		}
		if c.wantOk && got != c.want {
			t.Errorf("normalizedPythonFloatString(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
