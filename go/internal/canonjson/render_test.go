package canonjson

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"
)

// TestSharedPythonStringSemantics ports the
// "shared Python string semantics preserve exact sequence and code-point
// order" test from node-tests/json.test.ts, including its exact
// -vs-\u{10000} boundary vectors:  is the smallest BMP private
// use character, and must sort before \u{10000} (the smallest astral
// character) under code-point order even though a naive UTF-16-code-unit
// comparison (which this package deliberately avoids -- see
// ComparePythonStrings's doc comment) would put the astral character's
// leading surrogate (>= 0xD800) ahead of  (0xE000 > 0xD800 as a code
// unit, but 0xE000 < 0x10000 as a code point).
func TestSharedPythonStringSemantics(t *testing.T) {
	if !SameStringSequence([]string{"a", "a", "b"}, []string{"a", "a", "b"}) {
		t.Error(`SameStringSequence(["a","a","b"], ["a","a","b"]) = false, want true`)
	}
	if SameStringSequence([]string{"a"}, []string{"a", "b"}) {
		t.Error(`SameStringSequence(["a"], ["a","b"]) = true, want false`)
	}
	if SameStringSequence([]string{"a", "b"}, []string{"b", "a"}) {
		t.Error(`SameStringSequence(["a","b"], ["b","a"]) = true, want false`)
	}
	if SameStringSequence([]string{"a", "a"}, []string{"a", "b"}) {
		t.Error(`SameStringSequence(["a","a"], ["a","b"]) = true, want false`)
	}
	bmpPrivateUse := ""
	smallestAstral := "\U00010000"
	if ComparePythonStrings(bmpPrivateUse, smallestAstral) >= 0 {
		t.Errorf("ComparePythonStrings(U+E000, U+10000) = %d, want < 0", ComparePythonStrings(bmpPrivateUse, smallestAstral))
	}
	if ComparePythonStrings(smallestAstral, bmpPrivateUse) <= 0 {
		t.Errorf("ComparePythonStrings(U+10000, U+E000) = %d, want > 0", ComparePythonStrings(smallestAstral, bmpPrivateUse))
	}
}

// codePointCompare is an explicit, JS-codePointAt-style walk over Unicode
// code points, i.e. a direct Go transliteration of comparePythonStrings's
// algorithm in node-src/json/python-compatible.ts. ComparePythonStrings
// itself takes a completely different (and, for valid UTF-8, equivalent)
// route -- see its doc comment -- so this function exists purely as an
// independent cross-check in TestComparePythonStringsMatchesCodePointWalk,
// not as part of the package's implementation.
func codePointCompare(left, right string) int {
	l, r := []rune(left), []rune(right)
	for i := 0; i < len(l) && i < len(r); i++ {
		if l[i] != r[i] {
			return int(l[i]) - int(r[i])
		}
	}
	return len(l) - len(r)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// TestComparePythonStringsMatchesCodePointWalk verifies
// ComparePythonStrings's byte-comparison shortcut (see its doc comment for
// why UTF-8 byte order equals code-point order) against the explicit
// code-point walk above, across the ported test vectors, boundary
// characters around the UTF-16 surrogate range, and combined
// astral/BMP/ASCII strings.
func TestComparePythonStringsMatchesCodePointWalk(t *testing.T) {
	boundary := []string{
		"",
		"a",
		"b",
		"ab",
		"ba",
		"",                          // smallest BMP private-use character, right after the surrogate range
		"\U00010000",                 // smallest astral character (needs a surrogate pair in UTF-16)
		"￿",                          // largest BMP character
		"\U0010ffff",                 // largest astral character (largest valid Unicode code point)
		"퟿",                          // last BMP character before the surrogate range starts
		"\U0001F600",                 // astral emoji
		"\U0001F601",                 // a different astral emoji
		"\U0001F600\U0001F601 mix é", // combined astral + BMP
		"combining Zálgo",           // combining diacritic
		"\x00",                       // NUL
		"\x7f",                       // DEL
		"",                         // repeated BMP private-use
	}
	for i, a := range boundary {
		for j, b := range boundary {
			gotSign := sign(ComparePythonStrings(a, b))
			wantSign := sign(codePointCompare(a, b))
			if gotSign != wantSign {
				t.Errorf("ComparePythonStrings(%d, %d) sign = %d, want %d (a=%q b=%q)", i, j, gotSign, wantSign, a, b)
			}
		}
	}
}

// TestRenderIntegerOnlyCompatibility ports the
// "integer-only compatibility renderer matches Python bytes" test from
// node-tests/json.test.ts. The original test shells out to a Python
// oracle at run time; this port instead hardcodes the same oracle's
// output, captured once via `python3 -c
// "json.dumps(value, indent=2, sort_keys=True)"` against the identical
// value (see the task's verification notes), so this suite stays
// stdlib-only.
func TestRenderIntegerOnlyCompatibility(t *testing.T) {
	value := map[string]any{
		"2":      "two",
		"10":     "ten",
		"ascii":  "é/\\\"\n",
		"astral": "\U0001F600",
		"bmp":    "",
		"nested": []any{true, nil, float64(9007199254740991)},
	}
	want := "{\n  \"10\": \"ten\",\n  \"2\": \"two\",\n  \"ascii\": \"\\u00e9/\\\\\\\"\\n\",\n  \"astral\": \"\\ud83d\\ude00\",\n  \"bmp\": \"\\ue000\",\n  \"nested\": [\n    true,\n    null,\n    9007199254740991\n  ]\n}\n"

	got, err := Render(value)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != want {
		t.Fatalf("Render mismatch:\n got: %q\nwant: %q", got, want)
	}
	if idx10, idx2 := strings.Index(got, `"10"`), strings.Index(got, `"2"`); idx10 >= idx2 {
		t.Errorf(`"10" should sort before "2" (code-point order): index("10")=%d index("2")=%d`, idx10, idx2)
	}
	if !strings.Contains(got, "\\u00e9") {
		t.Error(`rendered output should contain the é escape`)
	}
	if !strings.Contains(got, "\\ud83d\\ude00") {
		t.Error(`rendered output should contain the 😀 surrogate pair escape`)
	}

	wantLen := len(want)
	length, err := ByteLength(value)
	if err != nil {
		t.Fatalf("ByteLength: %v", err)
	}
	if length != wantLen {
		t.Errorf("ByteLength(value) = %d, want %d", length, wantLen)
	}
	length, err = ByteLength(value, wantLen)
	if err != nil {
		t.Fatalf("ByteLength(value, wantLen): %v", err)
	}
	if length != wantLen {
		t.Errorf("ByteLength(value, %d) = %d, want %d", wantLen, length, wantLen)
	}
	length, err = ByteLength(value, wantLen-1)
	if err != nil {
		t.Fatalf("ByteLength(value, wantLen-1): %v", err)
	}
	if length != wantLen {
		t.Errorf("ByteLength(value, %d) = %d, want %d (over-limit rendering must report exactly limit+1)", wantLen-1, length, wantLen)
	}
}

// TestRenderFloatSpellingAndLosslessTokens ports the
// "compatibility renderer preserves Python float spelling and numeric
// tokens" test from node-tests/json.test.ts.
func TestRenderFloatSpellingAndLosslessTokens(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"one half", map[string]any{"value": 0.5}, "{\n  \"value\": 0.5\n}\n"},
		{"negative zero", map[string]any{"value": math.Copysign(0, -1)}, "{\n  \"value\": -0.0\n}\n"},
		{"one micro", map[string]any{"value": 1e-6}, "{\n  \"value\": 1e-06\n}\n"},
		{"1e20", map[string]any{"value": 1e20}, "{\n  \"value\": 1e+20\n}\n"},
		{"lossless 1.0 token", map[string]any{"value": json.Number("1.0")}, "{\n  \"value\": 1.0\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Render(c.value)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != c.want {
				t.Errorf("Render(%v) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}

// TestSortedStringsLargeCommonPrefix ports the
// "Python string ordering handles a large common-prefix set without
// sort-key retention" test from node-tests/json.test.ts: 25,000 strings
// sharing a long astral-character prefix, differing only in a
// zero-padded numeric suffix, sorted in reverse numeric order and
// expected to come back out in forward numeric order.
func TestSortedStringsLargeCommonPrefix(t *testing.T) {
	prefix := strings.Repeat("\U0001F600", 120)
	const n = 25_000
	values := make([]string, n)
	for i := 0; i < n; i++ {
		values[i] = fmt.Sprintf("%s%05d", prefix, n-1-i)
	}
	sorted := SortedStrings(values)
	if sorted[0] != prefix+"00000" {
		t.Errorf("sorted[0] = %q, want %q", sorted[0], prefix+"00000")
	}
	if last := sorted[len(sorted)-1]; last != fmt.Sprintf("%s%05d", prefix, n-1) {
		t.Errorf("sorted[last] = %q, want %q", last, fmt.Sprintf("%s%05d", prefix, n-1))
	}
	if !sort.SliceIsSorted(sorted, func(i, j int) bool { return ComparePythonStrings(sorted[i], sorted[j]) < 0 }) {
		t.Error("SortedStrings output is not actually sorted by ComparePythonStrings")
	}
}

// TestRenderEmptyContainers ports the empty-array/empty-object shorthand
// ("[]" / "{}", no internal newline) documented in node-src's encode.
func TestRenderEmptyContainers(t *testing.T) {
	got, err := Render(map[string]any{"a": []any{}, "b": map[string]any{}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "{\n  \"a\": [],\n  \"b\": {}\n}\n"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestRenderRejectsNonFiniteFloat mirrors node-src/json/python-compatible.ts's
// encodeNumber throwing for a non-finite plain `number`.
func TestRenderRejectsNonFiniteFloat(t *testing.T) {
	if _, err := Render(map[string]any{"value": math.NaN()}); err == nil {
		t.Error("Render(NaN) should return an error")
	}
	if _, err := Render(map[string]any{"value": math.Inf(1)}); err == nil {
		t.Error("Render(+Inf) should return an error")
	}
}

// TestUTF16UnitsHelperMatchesManualEncoding sanity-checks the utf16Units
// helper against Go's own unicode/utf16 package for a string containing
// both BMP and astral characters, since encodeString/encodedStringLength
// both depend on it walking UTF-16 code units (not Unicode code points)
// the same way the Node source's charCodeAt-based loop does.
func TestUTF16UnitsHelperMatchesManualEncoding(t *testing.T) {
	s := "a\U0001F600b"
	got := utf16Units(s)
	want := utf16.Encode([]rune(s))
	if len(got) != len(want) {
		t.Fatalf("utf16Units(%q) length = %d, want %d", s, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("utf16Units(%q)[%d] = %#x, want %#x", s, i, got[i], want[i])
		}
	}
	// An astral character is exactly 2 UTF-16 units and 1 rune.
	if len(want) != len([]rune(s))+1 {
		t.Fatalf("sanity check failed: expected exactly one surrogate pair in %q", s)
	}
}

func TestByteLengthRejectsOutOfRangeLimit(t *testing.T) {
	if _, err := ByteLength("x", -1); err == nil {
		t.Error("ByteLength(_, -1) should return an error")
	}
	if _, err := ByteLength("x", MaxByteLengthLimit+1); err == nil {
		t.Error("ByteLength(_, MaxByteLengthLimit+1) should return an error")
	}
	if _, err := ByteLength("x", MaxByteLengthLimit); err != nil {
		t.Errorf("ByteLength(_, MaxByteLengthLimit) should be accepted: %v", err)
	}
}

func TestByteLengthPanicsOnTooManyArguments(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("ByteLength with two maximumBytes arguments should panic")
		}
	}()
	_, _ = ByteLength("x", 1, 2)
}

// TestFormatNumberSafeIntegerBoundary exercises the plain-float64 branch of
// formatNumber/encodeNumber at JS's Number.isSafeInteger boundary:
// Number.MAX_SAFE_INTEGER is 2^53 - 1, not 2^53 -- 2^53 itself is exactly
// representable as a float64 but is not "safe" (its neighbor 2^53 + 1 is
// not distinguishably representable), so it must fall through to the
// float-repr path, which for this magnitude still renders as a whole
// number with a ".0" suffix since it falls inside the fixed-notation
// exponent range. One more representable step past it (2^53 + 2; doubles
// only have integer precision up to 2^53 and then step by 2) behaves the
// same way.
func TestFormatNumberSafeIntegerBoundary(t *testing.T) {
	cases := []struct {
		name  string
		value float64
		want  string
	}{
		{"2^53 - 1 (safe, MAX_SAFE_INTEGER)", 9007199254740991, "9007199254740991"},
		{"2^53 (unsafe, via float-repr path)", 9007199254740992, "9007199254740992.0"},
		{"2^53 + 2 (unsafe, via float-repr path)", 9007199254740994, "9007199254740994.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := formatNumber(c.value)
			if err != nil {
				t.Fatalf("formatNumber(%v): %v", c.value, err)
			}
			if got != c.want {
				t.Errorf("formatNumber(%v) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}
