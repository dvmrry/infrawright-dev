// Package pyunicode ports a narrow, frozen Node compatibility helper to Go:
// reproducing Python 3.12/3.13 str.lower() under Unicode Character Database
// (UCD) version 15.1, byte-for-byte, regardless of which Unicode version the
// executing Go toolchain's stdlib actually carries.
//
// It ports two Node sources (both in this repository):
//
//   - node-src/generated/python-lower-15.1.ts, a generated delta table,
//     mechanically transcribed into tables.go (see that file's header for
//     provenance and how it was produced).
//   - node-src/json/python-lower-151.ts, the algorithm, ported into this
//     file: the Final_Sigma contextual rule, Cased/Case_Ignorable range
//     classification, and binary search over delta ranges.
//
// See docs/python-lower-unicode-contract.md for the full background on why
// this exists: Python's str.lower() implements the Unicode default case
// conversion algorithm (simple/full lowercase mappings plus the
// locale-independent Final_Sigma context), and callers elsewhere in this
// codebase need Go output to match it exactly, because Python-produced keys
// and paths must round-trip byte-for-byte through a Go reimplementation of
// the same pipeline.
//
// # Why Go's design differs from Node's at the runtime-version boundary
//
// Node's pythonLower151 calls out to two runtime primitives that are
// inherently tied to whatever Unicode/ICU version the executing V8 build
// carries: the regular expressions \p{Cased} and \p{Case_Ignorable}, and
// String.prototype.toLowerCase(). Because V8's bundled Unicode version can be
// *ahead* of Python's frozen 15.1 contract (Node 24 patch releases have
// shipped Unicode 16.0 and 17.0), Node's implementation requires a reviewed,
// generated delta for whichever runtime version is live, and fails closed
// (throws) for any other version -- see requireRuntimeDelta in
// node-src/json/python-lower-151.ts and PYTHON_LOWER_151_RUNTIME_DELTAS in
// node-src/generated/python-lower-15.1.ts.
//
// Go has no equivalent of \p{Cased}/\p{Case_Ignorable}: the stdlib "unicode"
// package does not expose either derived property (confirmed empirically --
// neither compiles as a regexp character class, and neither appears in
// unicode.Properties). This package therefore does not lean on any single
// runtime primitive the way Node does. Instead:
//
//   - casedBase and caseIgnorableBase reconstruct the Unicode Cased and
//     Case_Ignorable derived properties directly from Go's stdlib category
//     tables (Lu/Ll/Lt/Other_Uppercase/Other_Lowercase for Cased;
//     Mn/Me/Cf/Lm/Sk plus a small closed Word_Break punctuation set for
//     Case_Ignorable -- see the doc comment on caseIgnorableWordBreak).
//   - fullLower reconstructs the one unconditional full lowercase mapping
//     that differs from a simple 1:1 rune mapping (U+0130), since Go's
//     unicode.ToLower cannot express a one-rune-to-many-runes mapping.
//
// Go's stdlib unicode.Version is a fixed string burned in at Go-toolchain
// build time (currently "15.0.0" as of Go 1.26.5), not a value that can be
// ahead of Python's 15.1 contract the way V8's can. Every codepoint the
// transcribed 16.0/17.0 deltas flag as a *newer-runtime-only* Cased,
// Case_Ignorable, or lowercase-mapped codepoint was cross-checked against
// Go's stdlib-derived classification above and against a live Python
// 3.13/UCD 15.1 oracle during development (see lower_test.go); in every
// case, Go's own tables already agree with Python 15.1 without any delta
// correction, because Go's bundled data predates the divergences introduced
// in V8's later Unicode revisions. The full simple-lowercase-mapping table
// was independently diffed against a live Python 3.13/UCD 15.1 oracle across
// every Unicode scalar value (surrogates excluded) with zero mismatches
// (besides the expected, explicitly handled U+0130 case).
//
// currentDelta/deltaForVersion fail *closed*, exactly like Node's
// requireRuntimeDelta: only reviewed unicode.Version values are accepted.
// "15.0.0" is the empirically verified baseline above and resolves to the
// zero-value RuntimeDelta (every delta-aware check below is a fast no-op and
// falls through to the stdlib-derived base classification); "16.0.0" and
// "17.0.0" resolve to the transcribed corrections in tables.go. Any other
// version — including a future Go toolchain's newer bundle, which changes
// the *base* unicode category tables underneath this package — panics
// rather than silently drifting, because module toolchain preferences do
// not stop a newer local toolchain from building this code. Adding a new
// version requires the same reviewed-delta process node-src describes:
// re-run the exhaustive digest test in lower_test.go against a supported
// live Python oracle, then allowlist the version with its (possibly empty)
// delta.
package pyunicode

import (
	"strings"
	"unicode"
)

// Mirrors CAPITAL_SIGMA, SMALL_SIGMA, and FINAL_SIGMA in
// node-src/json/python-lower-151.ts.
const (
	capitalSigma = rune(0x03a3) // GREEK CAPITAL LETTER SIGMA (Σ)
	smallSigma   = rune(0x03c3) // GREEK SMALL LETTER SIGMA (σ)
	finalSigma   = rune(0x03c2) // GREEK SMALL LETTER FINAL SIGMA (ς)
)

// inRanges reports whether codePoint falls within any [first, last]
// inclusive pair in ranges, a flattened, sorted, non-overlapping list.
// Ported from inRanges in node-src/json/python-lower-151.ts.
func inRanges(codePoint rune, ranges []rune) bool {
	low, high := 0, len(ranges)/2-1
	for low <= high {
		middle := (low + high) / 2
		first := ranges[middle*2]
		last := ranges[middle*2+1]
		switch {
		case codePoint < first:
			high = middle - 1
		case codePoint > last:
			low = middle + 1
		default:
			return true
		}
	}
	return false
}

// deltaForVersion resolves a Unicode Character Database version string (as
// found in Go's unicode.Version, e.g. "15.0.0", "16.0.0", "17.0.0") to a
// reviewed RuntimeDelta. It mirrors requireRuntimeDelta in
// node-src/json/python-lower-151.ts, keyed the same way (by UCD version, via
// the "16.0"/"17.0" delta-table keys) and equally fail-closed: an
// unreviewed version panics instead of silently lowercasing with unverified
// base tables. "15.0.0" is the verified no-correction baseline — see the
// package doc comment.
func deltaForVersion(ucdVersion string) RuntimeDelta {
	switch ucdVersion {
	case "15.0.0":
		return RuntimeDelta{}
	case "16.0.0":
		return RuntimeDeltas["16.0"]
	case "17.0.0":
		return RuntimeDeltas["17.0"]
	default:
		panic("pyunicode: unreviewed Go unicode.Version " + ucdVersion +
			"; re-run the exhaustive Python-oracle digest test and allowlist a reviewed delta before lowercasing with this toolchain")
	}
}

// currentDelta resolves the reviewed RuntimeDelta, if any, for the Go
// toolchain's own bundled unicode.Version.
func currentDelta() RuntimeDelta {
	return deltaForVersion(unicode.Version)
}

// casedBase reconstructs Unicode's Cased derived property (Uppercase +
// Lowercase + Lt, i.e. Lu + Ll + Lt + Other_Uppercase + Other_Lowercase per
// DerivedCoreProperties.txt) directly from Go's stdlib unicode category
// tables. See the package doc comment for how this was verified against a
// live Python 3.13/UCD 15.1 oracle.
//
// casedBase is only ever consulted (via isCased) for a codepoint that has
// already failed isCaseIgnorable -- exactly mirroring the priority order in
// hasCasedBefore/hasCasedAfter below and in the Node source, where
// Case_Ignorable is tested first because some codepoints (e.g. U+02B0) are
// both Cased and Case_Ignorable. Its behavior for a codepoint that is *also*
// Case_Ignorable is therefore unreachable in practice and intentionally
// unverified for that overlap.
func casedBase(r rune) bool {
	return unicode.Is(unicode.Lu, r) ||
		unicode.Is(unicode.Ll, r) ||
		unicode.Is(unicode.Lt, r) ||
		unicode.Is(unicode.Other_Uppercase, r) ||
		unicode.Is(unicode.Other_Lowercase, r)
}

// caseIgnorableWordBreak is the closed set of codepoints with Word_Break =
// MidLetter, MidNumLet, or Single_Quote: the punctuation-driven half of
// Unicode's Case_Ignorable derived property that General_Category alone
// (Mn/Me/Cf/Lm/Sk) does not capture. Go's stdlib does not expose Word_Break
// data, so this list is transcribed as a small, closed, historically stable
// set rather than derived. It was recovered empirically (not from memory of
// WordBreakProperty.txt) by differentially testing every Unicode scalar
// value's Final_Sigma context against a live Python 3.13/UCD 15.1 oracle --
// see caseIgnorableBase's verification in lower_test.go.
var caseIgnorableWordBreak = []rune{
	0x0027, // APOSTROPHE (Single_Quote)
	0x002e, // FULL STOP (MidNumLet)
	0x003a, // COLON (MidLetter)
	0x00b7, // MIDDLE DOT (MidLetter)
	0x0387, // GREEK ANO TELEIA (MidLetter)
	0x055f, // ARMENIAN ABBREVIATION MARK (MidLetter)
	0x05f4, // HEBREW PUNCTUATION GERSHAYIM (MidLetter)
	0x2018, // LEFT SINGLE QUOTATION MARK (MidNumLet)
	0x2019, // RIGHT SINGLE QUOTATION MARK (MidNumLet)
	0x2024, // ONE DOT LEADER (MidNumLet)
	0x2027, // HYPHENATION POINT (MidLetter)
	0xfe13, // PRESENTATION FORM FOR VERTICAL COLON (MidLetter)
	0xfe52, // SMALL FULL STOP (MidNumLet)
	0xfe55, // SMALL COLON (MidLetter)
	0xff07, // FULLWIDTH APOSTROPHE (MidNumLet)
	0xff0e, // FULLWIDTH FULL STOP (MidNumLet)
	0xff1a, // FULLWIDTH COLON (MidLetter)
}

// caseIgnorableBase reconstructs Unicode's Case_Ignorable derived property
// (General_Category in {Mn, Me, Cf, Lm, Sk}, or Word_Break in {MidLetter,
// MidNumLet, Single_Quote}) using Go's stdlib category tables plus the
// closed caseIgnorableWordBreak set above.
func caseIgnorableBase(r rune) bool {
	if unicode.Is(unicode.Mn, r) ||
		unicode.Is(unicode.Me, r) ||
		unicode.Is(unicode.Cf, r) ||
		unicode.Is(unicode.Lm, r) ||
		unicode.Is(unicode.Sk, r) {
		return true
	}
	for _, wb := range caseIgnorableWordBreak {
		if wb == r {
			return true
		}
	}
	return false
}

// isCaseIgnorable ports isCaseIgnorable in node-src/json/python-lower-151.ts:
// apply the delta correction first (a runtime-only-newer codepoint is never
// Case_Ignorable under the frozen 15.1 contract; a Python-only codepoint
// always is), then fall back to caseIgnorableBase.
func isCaseIgnorable(r rune, delta RuntimeDelta) bool {
	if inRanges(r, delta.RuntimeOnlyCaseIgnorableRanges) {
		return false
	}
	if inRanges(r, delta.PythonOnlyCaseIgnorableRanges) {
		return true
	}
	return caseIgnorableBase(r)
}

// isCased ports isCased in node-src/json/python-lower-151.ts: apply the
// delta correction first, then fall back to casedBase.
func isCased(r rune, delta RuntimeDelta) bool {
	if inRanges(r, delta.RuntimeOnlyCasedRanges) {
		return false
	}
	if inRanges(r, delta.PythonOnlyCasedRanges) {
		return true
	}
	return casedBase(r)
}

// hasCasedBefore ports hasCasedBefore in node-src/json/python-lower-151.ts:
// scan backward from index, skipping Case_Ignorable codepoints (checked
// first, since some codepoints are both Cased and Case_Ignorable), and
// return whether the first non-ignorable codepoint found is Cased. Returns
// false if the scan reaches the start of chars without finding one.
func hasCasedBefore(chars []rune, index int, delta RuntimeDelta) bool {
	for cursor := index - 1; cursor >= 0; cursor-- {
		if isCaseIgnorable(chars[cursor], delta) {
			continue
		}
		return isCased(chars[cursor], delta)
	}
	return false
}

// hasCasedAfter ports hasCasedAfter in node-src/json/python-lower-151.ts:
// the forward-scanning mirror of hasCasedBefore.
func hasCasedAfter(chars []rune, index int, delta RuntimeDelta) bool {
	for cursor := index + 1; cursor < len(chars); cursor++ {
		if isCaseIgnorable(chars[cursor], delta) {
			continue
		}
		return isCased(chars[cursor], delta)
	}
	return false
}

// fullLower applies Unicode's full (possibly multi-rune) unconditional
// lowercase mapping for a single codepoint. U+0130 (LATIN CAPITAL LETTER I
// WITH DOT ABOVE) is the sole codepoint in Unicode 15.1 whose unconditional
// full lowercase mapping differs from its simple (one-rune) mapping -- it
// expands to U+0069 U+0307 ("i" + COMBINING DOT ABOVE) -- so it is
// special-cased explicitly; Go's unicode.ToLower cannot express a
// one-rune-to-many mapping. This mirrors the "i + dot" comment in
// pythonLower151 in node-src/json/python-lower-151.ts, where
// character.toLowerCase() produces the same expansion via JavaScript's
// built-in Unicode default case conversion. Verified to be the *only*
// exception across every Unicode scalar value against a live Python
// 3.13/UCD 15.1 oracle (see lower_test.go).
func fullLower(r rune) string {
	if r == 0x0130 {
		return "i̇"
	}
	return string(unicode.ToLower(r))
}

// PythonLower151 ports pythonLower151 in node-src/json/python-lower-151.ts:
// it reproduces Python 3.12/3.13 str.lower() (Unicode 15.0/15.1 -- see
// docs/python-lower-unicode-contract.md for why those two are treated as
// equivalent here) byte-for-byte. This is an internal migration
// compatibility seam, not a general Unicode case-mapping API.
func PythonLower151(value string) string {
	delta := currentDelta()
	chars := []rune(value)
	var out strings.Builder
	out.Grow(len(value))
	for index, r := range chars {
		switch {
		case r == capitalSigma:
			if hasCasedBefore(chars, index, delta) && !hasCasedAfter(chars, index, delta) {
				out.WriteRune(finalSigma)
			} else {
				out.WriteRune(smallSigma)
			}
		case inRanges(r, delta.RuntimeOnlyLowercaseSourceRanges):
			// Per-runtime-newer lowercase source that Unicode 15.1 does not
			// recognize: leave unchanged, matching Python.
			out.WriteRune(r)
		default:
			// Per-code-point conversion preserves the one unconditional full
			// mapping (U+0130) without invoking any runtime's own
			// Final_Sigma context -- moot here since capitalSigma is
			// intercepted above and every other codepoint is converted in
			// isolation.
			out.WriteString(fullLower(r))
		}
	}
	return out.String()
}
