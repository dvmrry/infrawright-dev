// Ports node-src/json/control.ts: a strict "control dialect" JSON validator
// (rejecting duplicate object keys, over-deep nesting, and, for the control
// dialect specifically, any JSON number that is not finite and, if
// integer-shaped, not exactly representable) layered on top of ordinary
// decoding, plus a CPython-JSONDecodeError-flavored error type for the
// position-anchored failures the validator's scanner reports.
package canonjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

// maxJSONDepth is the deepest array/object nesting ParseControlJSON and
// ParseDataJSONLosslessly accept. Ports MAX_JSON_DEPTH from
// node-src/json/control.ts.
const maxJSONDepth = 128

// PythonJSONDecodeError is a position-anchored parse failure whose Error()
// text matches CPython's json.JSONDecodeError formatting:
// "<reason>: line L column C (char P)". Ports control.ts's
// PythonJsonDecodeError class (there, a SyntaxError subclass carrying the
// same three derived fields plus the original message text).
//
// Position, Line, and Column are computed the same way CPython's own
// json.decoder does (colno = pos - doc.rfind("\n", 0, pos), lineno =
// doc.count("\n", 0, pos) + 1) and the same way control.ts's constructor
// does, but walking UTF-16 code units rather than Unicode code points --
// matching control.ts's indexing (a JS string is a UTF-16 sequence, and its
// scanner advances this.index one code unit at a time) rather than true
// CPython position semantics. This only matters for input containing
// astral (non-BMP) characters ahead of a parse failure; ASCII/BMP input,
// which is what the control dialect's protocol/config documents actually
// contain, reports identical positions either way.
type PythonJSONDecodeError struct {
	// Reason is the CPython-style failure description, e.g. "Expecting
	// value" or "Extra data", exactly as control.ts's scanner passes to
	// the PythonJsonDecodeError constructor.
	Reason string
	// Position is the UTF-16-code-unit offset of the failure, clamped to
	// [0, len(units)], matching the Node constructor's `bounded`.
	Position int
	// Line is the 1-based line number containing Position.
	Line int
	// Column is the 1-based column number of Position within its line.
	Column int
}

// Error renders "<reason>: line L column C (char P)", matching
// control.ts's PythonJsonDecodeError message verbatim (no package prefix:
// the entire point of this type is to reproduce CPython's own
// JSONDecodeError text).
func (e *PythonJSONDecodeError) Error() string {
	return fmt.Sprintf("%s: line %d column %d (char %d)", e.Reason, e.Line, e.Column, e.Position)
}

// newPythonJSONDecodeError builds a PythonJSONDecodeError from a UTF-16 unit
// slice and an (unbounded) unit-index position, applying the Node
// constructor's clamp-then-derive-line/column logic.
func newPythonJSONDecodeError(reason string, units []uint16, position int) *PythonJSONDecodeError {
	bounded := position
	if bounded < 0 {
		bounded = 0
	}
	if bounded > len(units) {
		bounded = len(units)
	}
	line := 1
	lastNewline := -1
	for i := 0; i < bounded; i++ {
		if units[i] == '\n' {
			line++
			lastNewline = i
		}
	}
	return &PythonJSONDecodeError{
		Reason:   reason,
		Position: bounded,
		Line:     line,
		Column:   bounded - lastNewline,
	}
}

// The CPython-style failure reasons control.ts's scanner throws, verbatim.
const (
	reasonExtraData              = "Extra data"
	reasonExpectingPropertyName  = "Expecting property name enclosed in double quotes"
	reasonExpectingColon         = "Expecting ':' delimiter"
	reasonExpectingComma         = "Expecting ',' delimiter"
	reasonUnterminatedString     = "Unterminated string starting at"
	reasonExpectingValue         = "Expecting value"
	reasonUnpairedUTF16Surrogate = "Unpaired UTF-16 surrogate"
)

// Sentinel errors for the control dialect's non-position-anchored
// SyntaxErrors (control.ts throws a plain `new SyntaxError(...)` -- not a
// PythonJsonDecodeError -- for each of these). Each is returned verbatim
// (ErrNonFiniteControlNumber, ErrUnsafeControlInteger) or wrapped with
// fmt.Errorf to append the same interpolated detail control.ts's template
// literal includes (ErrDuplicateJSONKey, ErrJSONNestingTooDeep,
// ErrInvalidJSONWhitespace); in every case the text after the "canonjson: "
// package prefix reproduces the Node message exactly.
var (
	// ErrDuplicateJSONKey: `duplicate JSON key ${JSON.stringify(key)}`.
	ErrDuplicateJSONKey = errors.New("canonjson: duplicate JSON key")
	// ErrJSONNestingTooDeep: `JSON nesting exceeds ${MAX_JSON_DEPTH}`.
	ErrJSONNestingTooDeep = errors.New("canonjson: JSON nesting exceeds")
	// ErrInvalidJSONWhitespace: `invalid JSON whitespace at offset ${index}`.
	ErrInvalidJSONWhitespace = errors.New("canonjson: invalid JSON whitespace")
	// ErrNonFiniteControlNumber: "non-finite JSON numbers are not accepted".
	ErrNonFiniteControlNumber = errors.New("canonjson: non-finite JSON numbers are not accepted")
	// ErrUnsafeControlInteger: "control JSON integers must be exactly representable".
	ErrUnsafeControlInteger = errors.New("canonjson: control JSON integers must be exactly representable")
)

// validateControlNumber ports parseControlNumber from
// node-src/json/control.ts: the token (already known to match the JSON
// number grammar) must parse to a finite value, and, if it is
// integer-shaped (no "." or exponent), that value must be an exactly
// representable (JS-)safe integer.
//
// Number(token) in the Node source never fails to parse a
// grammar-conformant token; the only way it produces a non-finite result is
// an exponent so large the value overflows to +/-Infinity, which is exactly
// strconv.ParseFloat's ErrRange case here. Underflow (an exponent so small
// the value rounds to 0) is not an error in either runtime.
func validateControlNumber(token string) error {
	value, err := strconv.ParseFloat(token, 64)
	if err != nil {
		var numErr *strconv.NumError
		if !errors.As(err, &numErr) || !errors.Is(numErr.Err, strconv.ErrRange) {
			// Unreachable: token was produced by this file's own number
			// grammar scan (scanNumber), which only ever emits lexemes
			// strconv.ParseFloat can parse.
			return fmt.Errorf("canonjson: invalid control JSON number token %q", token)
		}
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return ErrNonFiniteControlNumber
	}
	if jsonIntegerToken.MatchString(token) && !isSafeInteger(value) {
		return ErrUnsafeControlInteger
	}
	return nil
}

// jsonQuoteJSString renders value the way JavaScript's JSON.stringify
// renders a bare string: double-quoted, with the standard JSON control-char
// escapes (plus backslash and quote), but -- unlike this package's
// Python-ensure_ascii-flavored encoders in render.go and artifact.go --
// leaving every character at or above U+0080 literal (no \\uXXXX escaping,
// no HTML-entity escaping of < > &). Ports the
// `JSON.stringify(key)` call inside control.ts's duplicate-key SyntaxError
// message.
func jsonQuoteJSString(value string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			sb.WriteString(`\"`)
			continue
		case '\\':
			sb.WriteString(`\\`)
			continue
		case '\b':
			sb.WriteString(`\b`)
			continue
		case '\t':
			sb.WriteString(`\t`)
			continue
		case '\n':
			sb.WriteString(`\n`)
			continue
		case '\f':
			sb.WriteString(`\f`)
			continue
		case '\r':
			sb.WriteString(`\r`)
			continue
		}
		if r < 0x20 {
			fmt.Fprintf(&sb, `\u%04x`, r)
			continue
		}
		sb.WriteRune(r)
	}
	sb.WriteByte('"')
	return sb.String()
}

// isJSWhitespaceUnit reports whether unit is one of the UTF-16 code units
// JavaScript's regexp `\s` character class matches (without the "u" flag,
// i.e. per code unit): every ECMA-262 WhiteSpace/LineTerminator character.
// Ports the character class implicit in control.ts's
// `/\s/.test(character)` check.
func isJSWhitespaceUnit(unit uint16) bool {
	switch unit {
	case 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0xa0, 0x1680,
		0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a,
		0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return false
}

// isJSONWhitespaceUnit reports whether unit is one of the four whitespace
// characters the JSON grammar itself permits between tokens (space, tab,
// newline, CR). control.ts's skipWhitespace rejects any other
// JS-whitespace character (see isJSWhitespaceUnit) that appears where JSON
// whitespace is expected -- e.g. a literal form feed or non-breaking space
// -- rather than silently accepting it the way a bare `/\s/`-driven skip
// would.
func isJSONWhitespaceUnit(unit uint16) bool {
	switch unit {
	case 0x20, 0x0a, 0x0d, 0x09:
		return true
	}
	return false
}

// controlScanner ports control.ts's JsonContractScanner: a validating scan
// over one JSON document's UTF-16 code units (see PythonJSONDecodeError's
// doc comment for why UTF-16 units, not bytes or runes) that enforces the
// control dialect's structural contract -- rejecting duplicate object keys,
// nesting deeper than maxJSONDepth, non-JSON whitespace, and (when
// validateNumbers is set) non-finite or unsafe-integer numbers -- without
// itself building a value tree. A successful scan only proves text is
// contract-valid JSON; ParseControlJSON/ParseDataJSONLosslessly still hand
// the same text to Decode afterward to actually build the Value.
type controlScanner struct {
	units                          []uint16
	validateNumbers                bool
	index                          int
	firstUnpairedSurrogateOffset   int
	lastStringHasUnpairedSurrogate bool
}

func newControlScanner(units []uint16, validateNumbers bool) *controlScanner {
	return &controlScanner{units: units, validateNumbers: validateNumbers, firstUnpairedSurrogateOffset: -1}
}

// charAt returns the UTF-16 code unit at i, or 0 if i is out of range --
// mirroring `this.text[this.index]` being `undefined` in the Node source,
// which likewise matches none of the scanner's explicit structural-
// character branches and falls through to the number/value grammar (which
// then reports "Expecting value" for having nothing to match).
func (s *controlScanner) charAt(i int) uint16 {
	if i < 0 || i >= len(s.units) {
		return 0
	}
	return s.units[i]
}

func (s *controlScanner) decodeError(reason string, position int) error {
	return newPythonJSONDecodeError(reason, s.units, position)
}

// scan ports JsonContractScanner.scan.
func (s *controlScanner) scan() error {
	if err := s.skipWhitespace(); err != nil {
		return err
	}
	if err := s.scanValue(0); err != nil {
		return err
	}
	if err := s.skipWhitespace(); err != nil {
		return err
	}
	if s.index != len(s.units) {
		return s.decodeError(reasonExtraData, s.index)
	}
	if s.firstUnpairedSurrogateOffset >= 0 {
		return s.decodeError(reasonUnpairedUTF16Surrogate, s.firstUnpairedSurrogateOffset)
	}
	return nil
}

// scanValue ports JsonContractScanner.scanValue.
func (s *controlScanner) scanValue(depth int) error {
	if err := s.skipWhitespace(); err != nil {
		return err
	}
	switch s.charAt(s.index) {
	case '{':
		return s.scanObject(depth + 1)
	case '[':
		return s.scanArray(depth + 1)
	case '"':
		_, err := s.scanString()
		return err
	case 't':
		return s.scanLiteral("true")
	case 'f':
		return s.scanLiteral("false")
	case 'n':
		return s.scanLiteral("null")
	default:
		return s.scanNumber()
	}
}

// checkDepth ports JsonContractScanner.checkDepth.
func (s *controlScanner) checkDepth(depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("%w %d", ErrJSONNestingTooDeep, maxJSONDepth)
	}
	return nil
}

// scanObject ports JsonContractScanner.scanObject, including its duplicate-
// key rejection: keys are compared after string-escape decoding (so
// "a" and "a" collide), regardless of key name (including
// "__proto__", which collides like any other string here -- there is no
// prototype to pollute in this package's map[string]any Value model, and
// the duplicate-key check does not special-case it either, matching the
// Node source).
func (s *controlScanner) scanObject(depth int) error {
	if err := s.checkDepth(depth); err != nil {
		return err
	}
	s.index++
	if err := s.skipWhitespace(); err != nil {
		return err
	}
	if s.charAt(s.index) == '}' {
		s.index++
		return nil
	}
	seen := make(map[string]struct{})
	for {
		if s.charAt(s.index) != '"' {
			return s.decodeError(reasonExpectingPropertyName, s.index)
		}
		key, err := s.scanString()
		if err != nil {
			return err
		}
		if !s.lastStringHasUnpairedSurrogate {
			if _, dup := seen[key]; dup {
				return fmt.Errorf("%w %s", ErrDuplicateJSONKey, jsonQuoteJSString(key))
			}
			seen[key] = struct{}{}
		}
		if err := s.skipWhitespace(); err != nil {
			return err
		}
		if err := s.expect(':', reasonExpectingColon); err != nil {
			return err
		}
		if err := s.scanValue(depth); err != nil {
			return err
		}
		if err := s.skipWhitespace(); err != nil {
			return err
		}
		if s.charAt(s.index) == '}' {
			s.index++
			return nil
		}
		if err := s.expect(',', reasonExpectingComma); err != nil {
			return err
		}
		if err := s.skipWhitespace(); err != nil {
			return err
		}
	}
}

// scanArray ports JsonContractScanner.scanArray.
func (s *controlScanner) scanArray(depth int) error {
	if err := s.checkDepth(depth); err != nil {
		return err
	}
	s.index++
	if err := s.skipWhitespace(); err != nil {
		return err
	}
	if s.charAt(s.index) == ']' {
		s.index++
		return nil
	}
	for {
		if err := s.scanValue(depth); err != nil {
			return err
		}
		if err := s.skipWhitespace(); err != nil {
			return err
		}
		if s.charAt(s.index) == ']' {
			s.index++
			return nil
		}
		if err := s.expect(',', reasonExpectingComma); err != nil {
			return err
		}
		if err := s.skipWhitespace(); err != nil {
			return err
		}
	}
}

// scanString ports JsonContractScanner.scanString: it locates the closing
// quote with the same naive backslash-skipping heuristic as the Node
// source (advancing 2 units past any backslash without validating what
// follows it), then defers full escape-sequence validation and decoding to
// a real JSON string decoder over the located literal -- encoding/json's
// here, JSON.parse there. An invalid escape sequence therefore surfaces as
// whatever error that underlying decoder produces, not as a
// PythonJSONDecodeError; this is a deliberate, faithfully-ported quirk of
// the Node scanner, not an oversight.
func (s *controlScanner) scanString() (string, error) {
	start := s.index
	s.lastStringHasUnpairedSurrogate = false
	s.index++
	for s.index < len(s.units) {
		if s.units[s.index] == '"' {
			s.index++
			literal := string(utf16.Decode(s.units[start:s.index]))
			var decoded string
			if err := json.Unmarshal([]byte(literal), &decoded); err != nil {
				return "", fmt.Errorf("canonjson: %w", err)
			}
			if offset := firstUnpairedJSONStringSurrogateOffset(s.units, start, s.index); offset >= 0 {
				s.lastStringHasUnpairedSurrogate = true
				if s.firstUnpairedSurrogateOffset < 0 {
					s.firstUnpairedSurrogateOffset = offset
				}
			}
			return decoded, nil
		}
		if s.units[s.index] == '\\' {
			s.index += 2
		} else {
			s.index++
		}
	}
	return "", s.decodeError(reasonUnterminatedString, start)
}

func isHighSurrogate(unit uint16) bool {
	return unit >= 0xd800 && unit <= 0xdbff
}

func isLowSurrogate(unit uint16) bool {
	return unit >= 0xdc00 && unit <= 0xdfff
}

func hexJSONUnit(units []uint16) uint16 {
	var value uint16
	for _, unit := range units {
		value <<= 4
		switch {
		case unit >= '0' && unit <= '9':
			value |= unit - '0'
		case unit >= 'a' && unit <= 'f':
			value |= unit - 'a' + 10
		case unit >= 'A' && unit <= 'F':
			value |= unit - 'A' + 10
		}
	}
	return value
}

func standardJSONEscapeUnit(escape uint16) uint16 {
	switch escape {
	case '"', '\\', '/':
		return escape
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	default:
		return 0
	}
}

// firstUnpairedJSONStringSurrogateOffset walks decoded UTF-16 units in an
// already-syntax-checked JSON string literal and returns the first invalid raw
// offset, or -1. A \uXXXX unit reports its backslash and a literal unit its
// own UTF-16 source index.
func firstUnpairedJSONStringSurrogateOffset(units []uint16, start, end int) int {
	pendingHighOffset := -1
	accept := func(unit uint16, offset int) int {
		if pendingHighOffset >= 0 {
			if isLowSurrogate(unit) {
				pendingHighOffset = -1
				return -1
			}
			return pendingHighOffset
		}
		if isHighSurrogate(unit) {
			pendingHighOffset = offset
			return -1
		}
		if isLowSurrogate(unit) {
			return offset
		}
		return -1
	}

	for index := start + 1; index < end-1; {
		offset := index
		if units[index] == '\\' {
			if units[index+1] == 'u' {
				if invalidOffset := accept(hexJSONUnit(units[index+2:index+6]), offset); invalidOffset >= 0 {
					return invalidOffset
				}
				index += 6
			} else {
				if invalidOffset := accept(standardJSONEscapeUnit(units[index+1]), offset); invalidOffset >= 0 {
					return invalidOffset
				}
				index += 2
			}
			continue
		}
		if invalidOffset := accept(units[index], offset); invalidOffset >= 0 {
			return invalidOffset
		}
		index++
	}
	return pendingHighOffset
}

// validateJSONDocumentSurrogates is the document-level counterpart used by
// Decode after encoding/json has accepted the document. It intentionally
// imposes only the string invariant, not the control parser's duplicate-key,
// depth, or numeric restrictions.
func validateJSONDocumentSurrogates(text string) error {
	units := utf16Units(text)
	for index := 0; index < len(units); {
		if units[index] != '"' {
			index++
			continue
		}
		start := index
		index++
		for index < len(units) {
			if units[index] == '"' {
				index++
				if offset := firstUnpairedJSONStringSurrogateOffset(units, start, index); offset >= 0 {
					return newPythonJSONDecodeError(reasonUnpairedUTF16Surrogate, units, offset)
				}
				break
			}
			if units[index] == '\\' {
				index += 2
			} else {
				index++
			}
		}
	}
	return nil
}

// scanLiteral ports JsonContractScanner.scanLiteral. literal is always one
// of "true", "false", "null", each pure ASCII, so comparing one UTF-16 unit
// per byte is exact.
func (s *controlScanner) scanLiteral(literal string) error {
	for i := 0; i < len(literal); i++ {
		if s.charAt(s.index+i) != uint16(literal[i]) {
			return s.decodeError(reasonExpectingValue, s.index)
		}
	}
	s.index += len(literal)
	return nil
}

// scanNumber ports JsonContractScanner.scanNumber, including NUMBER_TOKEN's
// grammar (a hand-written equivalent of the regexp, since the token is
// always pure ASCII): an optional leading "-", a mandatory integer part
// ("0" or a non-zero digit run), an optional "." fraction, and an optional
// [eE] exponent. Each optional group either matches in full or not at all
// (never partially), exactly like the regexp's greedy-but-unambiguous
// alternation, so a hand-rolled scan reproduces its match length exactly.
func (s *controlScanner) scanNumber() error {
	start := s.index
	i := s.index
	if s.charAt(i) == '-' {
		i++
	}
	switch {
	case s.charAt(i) == '0':
		i++
	case s.charAt(i) >= '1' && s.charAt(i) <= '9':
		i++
		for s.charAt(i) >= '0' && s.charAt(i) <= '9' {
			i++
		}
	default:
		return s.decodeError(reasonExpectingValue, start)
	}
	if s.charAt(i) == '.' {
		if j := i + 1; s.charAt(j) >= '0' && s.charAt(j) <= '9' {
			i = j
			for s.charAt(i) >= '0' && s.charAt(i) <= '9' {
				i++
			}
		}
	}
	if ch := s.charAt(i); ch == 'e' || ch == 'E' {
		j := i + 1
		if s.charAt(j) == '+' || s.charAt(j) == '-' {
			j++
		}
		if s.charAt(j) >= '0' && s.charAt(j) <= '9' {
			k := j
			for s.charAt(k) >= '0' && s.charAt(k) <= '9' {
				k++
			}
			i = k
		}
	}
	token := string(utf16.Decode(s.units[start:i]))
	if s.validateNumbers {
		if err := validateControlNumber(token); err != nil {
			return err
		}
	}
	s.index = i
	return nil
}

// expect ports JsonContractScanner.expect.
func (s *controlScanner) expect(character uint16, reason string) error {
	if s.charAt(s.index) != character {
		return s.decodeError(reason, s.index)
	}
	s.index++
	return nil
}

// skipWhitespace ports JsonContractScanner.skipWhitespace, including its
// rejection of JS-whitespace characters that are not JSON whitespace (see
// isJSWhitespaceUnit vs isJSONWhitespaceUnit).
func (s *controlScanner) skipWhitespace() error {
	for s.index < len(s.units) && isJSWhitespaceUnit(s.units[s.index]) {
		if !isJSONWhitespaceUnit(s.units[s.index]) {
			return fmt.Errorf("%w at offset %d", ErrInvalidJSONWhitespace, s.index)
		}
		s.index++
	}
	return nil
}

// validateJSONContract ports validateJsonContract from
// node-src/json/control.ts.
func validateJSONContract(text string, validateNumbers bool) error {
	return newControlScanner(utf16Units(text), validateNumbers).scan()
}

// ParseControlJSON parses protocol/config JSON under the control dialect's
// full contract: no duplicate object keys, no nesting beyond maxJSONDepth,
// no JSON number that is non-finite or (if integer-shaped) not an exactly
// representable safe integer. Ports parseControlJson from
// node-src/json/control.ts.
//
// Validation runs first and entirely separately from decoding (matching
// the Node source's own two-pass structure): only once text is known to
// satisfy the contract does this hand the same bytes to Decode to build the
// Value tree.
func ParseControlJSON(text string) (Value, error) {
	if err := validateJSONContract(text, true); err != nil {
		return nil, err
	}
	return Decode([]byte(text))
}

// ParseDataJSONLosslessly parses provider/Terraform JSON under the control
// dialect's structural contract (no duplicate keys, no nesting beyond
// maxJSONDepth) but without the control dialect's numeric-safety rules, so
// every numeric token -- including ones far outside float64's safe integer
// range -- is preserved exactly via Decode's json.Number lexemes. Ports
// parseDataJsonLosslessly from node-src/json/control.ts (there, achieved by
// parsing with a reviver that swaps in a LosslessNumber built from the
// parser's raw source-text callback for every number).
func ParseDataJSONLosslessly(text string) (Value, error) {
	if err := validateJSONContract(text, false); err != nil {
		return nil, err
	}
	return Decode([]byte(text))
}
