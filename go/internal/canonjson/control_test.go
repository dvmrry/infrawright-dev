package canonjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestParseControlJSONRejectsDuplicateKeysAndUnsafeIntegers ports the
// "control parser rejects duplicate keys and unsafe integers" test from
// the original test corpus.
func TestParseControlJSONRejectsDuplicateKeysAndUnsafeIntegers(t *testing.T) {
	for _, source := range []string{
		`{"a":1,"a":2}`,
		`{"a":1,"a":1}`,
		`{"a":1,"` + "\\" + `u0061":1}`, // a decodes to "a": a duplicate after escape decoding, not just lexically
		`{"__proto__":{"first":1},"__proto__":{"second":2}}`,
		`{"id":9007199254740993}`, // one past Number.MAX_SAFE_INTEGER
	} {
		t.Run(source, func(t *testing.T) {
			if _, err := ParseControlJSON(source); err == nil {
				t.Errorf("ParseControlJSON(%s) = nil error, want an error", source)
			}
		})
	}

	// assert.deepEqual(parseControlJson('{"id":9007199254740991}'), { id: 9007199254740991 })
	//
	// The Node function returns a plain JS number here (parseControlJson
	// re-parses with plain JSON.parse once validation has proven the
	// token safe); this package's ParseControlJSON instead hands validated
	// text to this package's own Decode, which always yields json.Number
	// (see decode.go) -- a representational difference, not a behavioral
	// one, since Decode's json.Number for this exact lexeme carries the
	// same value.
	value, err := ParseControlJSON(`{"id":9007199254740991}`)
	if err != nil {
		t.Fatalf(`ParseControlJSON('{"id":9007199254740991}'): %v`, err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("ParseControlJSON result = %#v (%T), want map[string]any", value, value)
	}
	if len(object) != 1 || object["id"] != json.Number("9007199254740991") {
		t.Errorf(`ParseControlJSON result = %#v, want {"id": json.Number("9007199254740991")}`, object)
	}
}

// TestJSONParsersRejectAdversarialNestingBeforeRecursiveParsing ports the
// "JSON parsers reject adversarial nesting before recursive parsing" test
// from the original test corpus.
func TestJSONParsersRejectAdversarialNestingBeforeRecursiveParsing(t *testing.T) {
	nested := strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129)

	if _, err := ParseControlJSON(nested); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Errorf(`ParseControlJSON(129-deep nesting) error = %v, want an error containing "nesting exceeds"`, err)
	}
	if _, err := ParseDataJSONLosslessly(nested); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Errorf(`ParseDataJSONLosslessly(129-deep nesting) error = %v, want an error containing "nesting exceeds"`, err)
	}
	if !errors.Is(func() error { _, err := ParseControlJSON(nested); return err }(), ErrJSONNestingTooDeep) {
		t.Error("ParseControlJSON(129-deep nesting) error does not match ErrJSONNestingTooDeep via errors.Is")
	}

	// assert.doesNotThrow(() => parseControlJson('{"text":"[[[{{{"}'));
	// Brackets/braces inside a string literal must not count toward
	// nesting depth.
	if _, err := ParseControlJSON(`{"text":"[[[{{{"}`); err != nil {
		t.Errorf(`ParseControlJSON('{"text":"[[[{{{"}') = %v, want nil`, err)
	}
}

// TestParseDataJSONLosslesslyPreservesNumericLexemesBeyondJSPrecision ports
// the "data parser preserves numeric lexemes beyond JavaScript precision"
// test from the original test corpus. The Node test additionally verifies
// stringifyLosslessly(parsed) round-trips the original compact source
// text byte-for-byte; that assertion exercises lossless-json's own
// serializer, which this package does not port (ParseDataJSONLosslessly's
// job is decoding, not re-serializing), so this instead asserts each
// numeric field decoded to the exact source lexeme as a json.Number,
// which is the property the round-trip assertion was actually protecting.
func TestParseDataJSONLosslesslyPreservesNumericLexemesBeyondJSPrecision(t *testing.T) {
	source := `{"a":9007199254740992,"b":9007199254740993,"f":-0.0}`
	value, err := ParseDataJSONLosslessly(source)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(%s): %v", source, err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("ParseDataJSONLosslessly result = %#v (%T), want map[string]any", value, value)
	}
	want := map[string]json.Number{
		"a": "9007199254740992",
		"b": "9007199254740993",
		"f": "-0.0",
	}
	for key, wantToken := range want {
		if got := object[key]; got != wantToken {
			t.Errorf("object[%q] = %#v, want json.Number(%q)", key, got, wantToken)
		}
	}

	if _, err := ParseDataJSONLosslessly(`{"a":1,"a":1}`); err == nil {
		t.Error(`ParseDataJSONLosslessly('{"a":1,"a":1}') = nil error, want an error (duplicate key)`)
	}
	if _, err := ParseDataJSONLosslessly(`{"__proto__":{"first":1},"__proto__":{"second":2}}`); err == nil {
		t.Error("ParseDataJSONLosslessly(duplicate __proto__ keys) = nil error, want an error")
	}
}

// The remaining tests in this file are Go-only: the original test corpus
// checks control.ts's validation rules with assert.throws/assert.doesNotThrow
// (occasionally against a /nesting exceeds/ regexp) but never pins down the
// exact PythonJsonDecodeError message text or the plain SyntaxError text for
// duplicate keys/whitespace/non-finite-number rejections. These vectors were
// derived by hand-tracing control.ts's/this file's own scanner algorithm
// (documented inline) rather than against an external executable, the same
// way TestFiniteFloatToken's boundary cases in number_test.go were derived
// from documented algorithmic reasoning rather than a live process.

// TestPythonJSONDecodeErrorMessageFormat pins the CPython-style
// "reason: line L column C (char P)" formatting (and the underlying
// line/column derivation: lineno = 1 + newlines before the position,
// colno = position - index of the last newline before it) against a
// handful of hand-traced control-JSON failures.
func TestPythonJSONDecodeErrorMessageFormat(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "unterminated string at start of document",
			source: `"abc`,
			want:   "Unterminated string starting at: line 1 column 1 (char 0)",
		},
		{
			name:   "expecting value after trailing whitespace",
			source: `   `,
			want:   "Expecting value: line 1 column 4 (char 3)",
		},
		{
			name:   "expecting property name without quotes",
			source: `{a:1}`,
			want:   "Expecting property name enclosed in double quotes: line 1 column 2 (char 1)",
		},
		{
			name:   "expecting colon delimiter",
			source: `{"a" 1}`,
			want:   "Expecting ':' delimiter: line 1 column 6 (char 5)",
		},
		{
			name:   "expecting comma delimiter across a newline",
			source: "[1\n2]",
			want:   "Expecting ',' delimiter: line 2 column 1 (char 3)",
		},
		{
			name:   "extra data after a valid value",
			source: "1 2",
			want:   "Extra data: line 1 column 3 (char 2)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseControlJSON(c.source)
			if err == nil {
				t.Fatalf("ParseControlJSON(%q) = nil error, want %q", c.source, c.want)
			}
			var decodeErr *PythonJSONDecodeError
			if !errors.As(err, &decodeErr) {
				t.Fatalf("ParseControlJSON(%q) error = %v (%T), want *PythonJSONDecodeError", c.source, err, err)
			}
			if got := decodeErr.Error(); got != c.want {
				t.Errorf("ParseControlJSON(%q) error = %q, want %q", c.source, got, c.want)
			}
		})
	}
}

// TestSkipWhitespaceRejectsNonJSONWhitespace ports control.ts's
// skipWhitespace quirk: a character JavaScript's `\s` regexp matches but
// that the JSON grammar does not accept as whitespace (form feed, here)
// must be rejected with a distinct message, not silently skipped or folded
// into "Expecting value".
func TestSkipWhitespaceRejectsNonJSONWhitespace(t *testing.T) {
	source := "{\x0c\"a\":1}" // form feed (U+000C) right after '{'
	_, err := ParseControlJSON(source)
	if err == nil {
		t.Fatalf("ParseControlJSON(%q) = nil error, want an error", source)
	}
	if !errors.Is(err, ErrInvalidJSONWhitespace) {
		t.Errorf("ParseControlJSON(%q) error = %v, want ErrInvalidJSONWhitespace", source, err)
	}
	if want := "invalid JSON whitespace at offset 1"; !strings.Contains(err.Error(), want) {
		t.Errorf("ParseControlJSON(%q) error = %q, want it to contain %q", source, err.Error(), want)
	}
}

// TestParseControlJSONNumberValidationSentinels exercises
// validateControlNumber's two rejection paths (ported from
// control.ts's parseControlNumber) via errors.Is, beyond the duplicate-
// key/depth vectors already ported above.
func TestParseControlJSONNumberValidationSentinels(t *testing.T) {
	if _, err := ParseControlJSON(`{"x":1e999}`); !errors.Is(err, ErrNonFiniteControlNumber) {
		t.Errorf(`ParseControlJSON('{"x":1e999}') error = %v, want ErrNonFiniteControlNumber`, err)
	}
	if _, err := ParseControlJSON(`{"x":9007199254740993}`); !errors.Is(err, ErrUnsafeControlInteger) {
		t.Errorf(`ParseControlJSON('{"x":9007199254740993}') error = %v, want ErrUnsafeControlInteger`, err)
	}
	// ParseDataJSONLosslessly does not validate number safety at all
	// (validateNumbers=false), so the same unsafe/non-finite lexemes are
	// accepted -- only structural rules (duplicate keys, depth) apply.
	if _, err := ParseDataJSONLosslessly(`{"x":9007199254740993}`); err != nil {
		t.Errorf(`ParseDataJSONLosslessly('{"x":9007199254740993}') = %v, want nil`, err)
	}
}

// TestDuplicateJSONKeyMessageText pins the exact
// `duplicate JSON key ${JSON.stringify(key)}` message construction from
// control.ts's scanObject, including that the key text is JSON-stringify
// quoted (not Go's %q quoting, which differs for non-ASCII).
func TestDuplicateJSONKeyMessageText(t *testing.T) {
	_, err := ParseControlJSON(`{"a":1,"a":2}`)
	if !errors.Is(err, ErrDuplicateJSONKey) {
		t.Fatalf(`ParseControlJSON('{"a":1,"a":2}') error = %v, want ErrDuplicateJSONKey`, err)
	}
	if want := `duplicate JSON key "a"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// TestJSONContractScannerIgnoresBracketsWithinStrings is a direct,
// non-ParseControlJSON-mediated check that controlScanner's depth counter
// only advances on structural brackets, never on bracket characters that
// appear inside a JSON string literal (this is what makes
// '{"text":"[[[{{{"}' above pass).
func TestJSONContractScannerIgnoresBracketsWithinStrings(t *testing.T) {
	source := `["` + strings.Repeat("[", 500) + `"]`
	if err := validateJSONContract(source, false); err != nil {
		t.Errorf("validateJSONContract(%d brackets inside a string) = %v, want nil", 500, err)
	}
}

func TestControlParsersRejectUnpairedUTF16SurrogatesAtRawOffsets(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		position int
	}{
		{name: "high value", source: `{"value":"\ud800"}`, position: 10},
		{name: "low value", source: `{"value":"\udfff"}`, position: 10},
		{name: "high key", source: `{"\ud800":true}`, position: 2},
		{name: "low key", source: `{"\udfff":true}`, position: 2},
		{name: "adjacent highs", source: `{"value":"\ud800\ud800"}`, position: 10},
		{name: "high before scalar", source: `{"value":"\ud800x"}`, position: 10},
		{name: "high key before replacement key", source: `{"\ud800":1,"�":2}`, position: 2},
		{name: "high key before low key", source: `{"\ud800":1,"\udfff":2}`, position: 2},
		{name: "repeated high keys", source: `{"\ud800":1,"\ud800":2}`, position: 2},
	}
	parsers := []struct {
		name  string
		parse func(string) (Value, error)
	}{
		{name: "control", parse: ParseControlJSON},
		{name: "data", parse: ParseDataJSONLosslessly},
	}
	for _, parser := range parsers {
		for _, tc := range cases {
			t.Run(parser.name+"/"+tc.name, func(t *testing.T) {
				_, err := parser.parse(tc.source)
				var decodeErr *PythonJSONDecodeError
				if !errors.As(err, &decodeErr) {
					t.Fatalf("error = %v (%T), want *PythonJSONDecodeError", err, err)
				}
				want := fmt.Sprintf("Unpaired UTF-16 surrogate: line 1 column %d (char %d)", tc.position+1, tc.position)
				if decodeErr.Error() != want {
					t.Errorf("error = %q, want %q", decodeErr.Error(), want)
				}
			})
		}
	}
}

func TestControlParsersAllowPairedAndNonSurrogateStrings(t *testing.T) {
	for _, parse := range []func(string) (Value, error){ParseControlJSON, ParseDataJSONLosslessly} {
		for _, source := range []string{
			`{"value":"\ud83d\ude00"}`,
			`{"value":"😀"}`,
			`{"value":"�"}`,
		} {
			if _, err := parse(source); err != nil {
				t.Errorf("parse(%s): %v", source, err)
			}
		}
	}
}

func TestControlParsersRejectEveryOrdinaryEscapeBetweenSurrogateHalves(t *testing.T) {
	escapes := []string{`\"`, `\\`, `\/`, `\b`, `\f`, `\n`, `\r`, `\t`}
	parsers := []struct {
		name  string
		parse func(string) (Value, error)
	}{
		{name: "control", parse: ParseControlJSON},
		{name: "data", parse: ParseDataJSONLosslessly},
	}
	for _, parser := range parsers {
		for _, escape := range escapes {
			for _, source := range []string{
				`{"value":"\ud800` + escape + `\udc00"}`,
				`{"\ud800` + escape + `\udc00":true}`,
			} {
				t.Run(parser.name+"/"+escape+"/"+source[:2], func(t *testing.T) {
					_, err := parser.parse(source)
					var decodeErr *PythonJSONDecodeError
					if !errors.As(err, &decodeErr) {
						t.Fatalf("error = %v (%T), want *PythonJSONDecodeError", err, err)
					}
					if decodeErr.Reason != reasonUnpairedUTF16Surrogate {
						t.Errorf("reason = %q, want %q", decodeErr.Reason, reasonUnpairedUTF16Surrogate)
					}
					if want := strings.Index(source, `\ud800`); decodeErr.Position != want {
						t.Errorf("position = %d, want %d", decodeErr.Position, want)
					}
				})
			}
		}
	}
}

func TestSurrogateTokenScannerAllowsMixedRawAndEscapedPairs(t *testing.T) {
	for _, units := range [][]uint16{
		{'"', 0xd800, '\\', 'u', 'd', 'c', '0', '0', '"'},
		{'"', '\\', 'u', 'd', '8', '0', '0', 0xdc00, '"'},
	} {
		if got := firstUnpairedJSONStringSurrogateOffset(units, 0, len(units)); got != -1 {
			t.Errorf("firstUnpairedJSONStringSurrogateOffset(%#v) = %d, want -1", units, got)
		}
	}
}

func TestControlParsersPreserveContractErrorsAheadOfSurrogates(t *testing.T) {
	cases := []struct {
		source string
		reason string
	}{
		{source: `{"value":"\ud800",}`, reason: reasonExpectingPropertyName},
		{source: `{"value":"\ud800" "next":1}`, reason: reasonExpectingComma},
		{source: `{"value":"\ud800"} garbage`, reason: reasonExtraData},
		{source: `{"value":"\ud800","next":?}`, reason: reasonExpectingValue},
	}
	for _, parse := range []func(string) (Value, error){ParseControlJSON, ParseDataJSONLosslessly} {
		for _, tc := range cases {
			_, err := parse(tc.source)
			var decodeErr *PythonJSONDecodeError
			if !errors.As(err, &decodeErr) || decodeErr.Reason != tc.reason {
				t.Errorf("parse(%q) error = %v, want reason %q", tc.source, err, tc.reason)
			}
		}
	}
}
