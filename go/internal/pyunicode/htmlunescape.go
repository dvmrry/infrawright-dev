package pyunicode

// htmlunescape.go ports node-src/domain/python-html-unescape.ts's
// pythonHtmlUnescapeGeneric: Python's html.unescape() semantics, layered on
// top of a WHATWG-compliant HTML entity decoder rather than a hand-rolled
// entity table.
//
// The Node source delegates ordinary entity decoding (named/legacy entities,
// and the WHATWG numeric-character-reference replacement table for the
// codepoints Python's own unescape also special-cases: NUL, CR, and the
// C1 control block 0x80-0x9F) to the npm "entities" package's decodeHTML,
// and only intercepts the numeric-reference path itself to apply Python's
// additional invalid-codepoint and surrogate/out-of-range rules, which
// decodeHTML does not implement (WHATWG leaves those references as literal
// codepoints; Python's html.unescape strips or replaces them).
//
// Go's stdlib html.UnescapeString is this port's substitute for decodeHTML.
// It was verified (see the goprobe comparison run during this port, and the
// vector table in htmlunescape_test.go) to agree with decodeHTML on every
// case this file actually routes to it: named/legacy entity matching
// (including partial/prefix matches like "&notit;" -> "¬it;", and
// semicolon-optional legacy entities like "&copy" -> "©"), and the WHATWG
// numeric replacement table for 0, 13, and 0x80-0x9F. Go's html.UnescapeString
// is known to diverge from Python's html.unescape on other numeric-reference
// classes (e.g. invalid codepoints like &#1; or noncharacters like
// &#xFDD0;, which Python strips to "" and Go's stdlib does not) -- this file
// never reaches html.UnescapeString for those classes in the first place:
// pythonInvalidCodepoint and the surrogate/out-of-range check below intercept
// them first, exactly mirroring the Node source's control flow, so that
// stdlib divergence is structurally unreachable here.
//
// Vector-by-vector behavior for the numeric-reference classes below (probed
// against the real node-src module via esbuild+node during this port; see
// htmlunescape_test.go) is pinned as the differential oracle until the Go
// runtime port is qualified per docs/go-runtime-plan.md.

import (
	"html"
	"math/big"
	"regexp"
	"strings"
)

// characterReference ports the CHARACTER_REFERENCE regexp from
// node-src/domain/python-html-unescape.ts verbatim. Go's RE2 engine accepts
// this pattern unchanged (no backreferences or lookaround are used).
var characterReference = regexp.MustCompile(`&(#[0-9]+;?|#[xX][0-9a-fA-F]+;?|[^\t\n\f <&#;]{1,32};?)`)

var (
	big0      = big.NewInt(0)
	big1      = big.NewInt(1)
	big8      = big.NewInt(8)
	big11     = big.NewInt(11)
	big13     = big.NewInt(13)
	big14     = big.NewInt(14)
	big31     = big.NewInt(31)
	big127    = big.NewInt(127)
	big128    = big.NewInt(128)
	big159    = big.NewInt(159)
	bigFDD0   = big.NewInt(0xfdd0)
	bigFDEF   = big.NewInt(0xfdef)
	bigFFFE   = big.NewInt(0xfffe)
	bigFFFF16 = big.NewInt(0xffff)
	bigD800   = big.NewInt(0xd800)
	bigDFFF   = big.NewInt(0xdfff)
	big10FFFF = big.NewInt(0x10ffff)
)

// pythonInvalidCodepoint ports pythonInvalidCodepoint from
// node-src/domain/python-html-unescape.ts. codepoint is always
// non-negative (parsed from an unsigned decimal or hexadecimal digit run),
// so the big.Int bitwise And below matches the source's BigInt `&`
// (two's-complement AND is equivalent to ordinary AND for non-negative
// operands).
func pythonInvalidCodepoint(codepoint *big.Int) bool {
	switch {
	case between(codepoint, big1, big8):
		return true
	case codepoint.Cmp(big11) == 0:
		return true
	case between(codepoint, big14, big31):
		return true
	case codepoint.Cmp(big127) == 0:
		return true
	case between(codepoint, bigFDD0, bigFDEF):
		return true
	}
	if codepoint.Cmp(bigFFFE) < 0 {
		return false
	}
	low := new(big.Int).And(codepoint, bigFFFF16)
	return low.Cmp(bigFFFE) >= 0
}

func between(v, lo, hi *big.Int) bool {
	return v.Cmp(lo) >= 0 && v.Cmp(hi) <= 0
}

// PythonHTMLUnescapeGeneric ports pythonHtmlUnescapeGeneric from
// node-src/domain/python-html-unescape.ts: "Python html.unescape without
// sourcing tables from a product transition catalog."
func PythonHTMLUnescapeGeneric(value string) string {
	if !strings.Contains(value, "&") {
		return value
	}
	return characterReference.ReplaceAllStringFunc(value, func(matched string) string {
		// The regexp's sole capture group spans everything after the
		// leading "&", so matched[1:] is that group's text without needing
		// Go's separate (and ReplaceAllStringFunc-incompatible) submatch
		// API.
		reference := matched[1:]
		if !strings.HasPrefix(reference, "#") {
			return html.UnescapeString(matched)
		}
		hexadecimal := len(reference) > 1 && (reference[1] == 'x' || reference[1] == 'X')
		start := 1
		if hexadecimal {
			start = 2
		}
		digits := strings.TrimSuffix(reference[start:], ";")
		base := 10
		if hexadecimal {
			base = 16
		}
		codepoint, ok := new(big.Int).SetString(digits, base)
		if !ok {
			// Unreachable: the regexp only ever admits digit runs valid in
			// the chosen base.
			return matched
		}
		// WHATWG replacements take precedence over Python's invalid-codepoint set.
		if codepoint.Sign() == 0 || codepoint.Cmp(big13) == 0 || between(codepoint, big128, big159) {
			return html.UnescapeString(matched)
		}
		if pythonInvalidCodepoint(codepoint) {
			return ""
		}
		if between(codepoint, bigD800, bigDFFF) || codepoint.Cmp(big10FFFF) > 0 {
			return "�"
		}
		return string(rune(codepoint.Int64()))
	})
}
