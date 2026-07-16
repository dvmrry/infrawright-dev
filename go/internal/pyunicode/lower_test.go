package pyunicode

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"unicode"
)

// expandRanges turns a flattened [first, last, first, last, ...] pair list
// into an explicit slice of every codepoint it covers. Ported from the
// `expanded` helper in node-tests/python-lower-151.test.ts.
func expandRanges(t *testing.T, ranges []rune) []rune {
	t.Helper()
	if len(ranges)%2 != 0 {
		t.Fatalf("malformed ranges: odd length %d", len(ranges))
	}
	var out []rune
	for i := 0; i < len(ranges); i += 2 {
		for cp := ranges[i]; cp <= ranges[i+1]; cp++ {
			out = append(out, cp)
		}
	}
	return out
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestRuntimeDeltaTables ports "generated runtime deltas are closed over the
// reviewed Node 24 Unicode tables" from node-tests/python-lower-151.test.ts.
// It checks the mechanically transcribed tables.go against the same
// documented values and cardinalities the Node test checks (see also
// docs/python-lower-unicode-contract.md), verifying the transcription is
// faithful. Go maps have no defined iteration order, so key checks compare
// sorted key sets rather than JS's insertion-ordered Object.keys.
func TestRuntimeDeltaTables(t *testing.T) {
	if got, want := mapKeys(UCDSources), []string{"15.1.0", "16.0.0", "17.0.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("UCDSources keys = %v, want %v", got, want)
	}
	if got, want := mapKeys(RuntimeDeltas), []string{"16.0", "17.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RuntimeDeltas keys = %v, want %v", got, want)
	}

	unicode16 := RuntimeDeltas["16.0"]
	unicode17 := RuntimeDeltas["17.0"]

	if got, want := unicode16.RuntimeOnlyLowercaseSourceRanges, []rune{
		0x1c89, 0x1c89,
		0xa7cb, 0xa7cc,
		0xa7da, 0xa7da,
		0xa7dc, 0xa7dc,
		0x10d50, 0x10d65,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("16.0 RuntimeOnlyLowercaseSourceRanges = %#x, want %#x", got, want)
	}
	if got, want := unicode17.RuntimeOnlyLowercaseSourceRanges, []rune{
		0x1c89, 0x1c89,
		0xa7cb, 0xa7cc,
		0xa7ce, 0xa7ce,
		0xa7d2, 0xa7d2,
		0xa7d4, 0xa7d4,
		0xa7da, 0xa7da,
		0xa7dc, 0xa7dc,
		0x10d50, 0x10d65,
		0x16ea0, 0x16eb8,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("17.0 RuntimeOnlyLowercaseSourceRanges = %#x, want %#x", got, want)
	}

	type expectedCounts struct {
		runtimeOnlyLowercase int
		runtimeOnlyCased     int
		pythonOnlyCased      int
		runtimeOnlyCaseIgn   int
	}
	for _, tc := range []struct {
		name  string
		delta RuntimeDelta
		want  expectedCounts
	}{
		{"16.0", unicode16, expectedCounts{27, 52, 0, 43}},
		{"17.0", unicode17, expectedCounts{55, 107, 1, 88}},
	} {
		delta := tc.delta
		if got := len(expandRanges(t, delta.RuntimeOnlyLowercaseSourceRanges)); got != tc.want.runtimeOnlyLowercase {
			t.Errorf("%s: len(RuntimeOnlyLowercaseSourceRanges expanded) = %d, want %d", tc.name, got, tc.want.runtimeOnlyLowercase)
		}
		if got := delta.PythonOnlyLowercaseSourceRanges; len(got) != 0 {
			t.Errorf("%s: PythonOnlyLowercaseSourceRanges = %#x, want empty", tc.name, got)
		}
		if got := delta.ChangedCommonLowercaseSourceRanges; len(got) != 0 {
			t.Errorf("%s: ChangedCommonLowercaseSourceRanges = %#x, want empty", tc.name, got)
		}
		if got := len(expandRanges(t, delta.RuntimeOnlyCasedRanges)); got != tc.want.runtimeOnlyCased {
			t.Errorf("%s: len(RuntimeOnlyCasedRanges expanded) = %d, want %d", tc.name, got, tc.want.runtimeOnlyCased)
		}
		if got := len(expandRanges(t, delta.PythonOnlyCasedRanges)); got != tc.want.pythonOnlyCased {
			t.Errorf("%s: len(PythonOnlyCasedRanges expanded) = %d, want %d", tc.name, got, tc.want.pythonOnlyCased)
		}
		if got := len(expandRanges(t, delta.RuntimeOnlyCaseIgnorableRanges)); got != tc.want.runtimeOnlyCaseIgn {
			t.Errorf("%s: len(RuntimeOnlyCaseIgnorableRanges expanded) = %d, want %d", tc.name, got, tc.want.runtimeOnlyCaseIgn)
		}
		if got, want := delta.PythonOnlyCaseIgnorableRanges, []rune{0x1171e, 0x1171e}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: PythonOnlyCaseIgnorableRanges = %#x, want %#x", tc.name, got, want)
		}
	}

	if got := unicode16.PythonOnlyCasedRanges; len(got) != 0 {
		t.Errorf("16.0: PythonOnlyCasedRanges = %#x, want empty", got)
	}
	if got, want := unicode17.PythonOnlyCasedRanges, []rune{0x295, 0x295}; !reflect.DeepEqual(got, want) {
		t.Errorf("17.0: PythonOnlyCasedRanges = %#x, want %#x", got, want)
	}
}

// TestPreservesRuntimeOnlyLowercaseSources ports "Python lowercase preserves
// every direct mapping source added by this runtime" from
// node-tests/python-lower-151.test.ts, generalized to check both the 16.0
// and 17.0 deltas (rather than only whichever process.versions.unicode the
// executing Node happened to report), since Go has no equivalent live
// runtime-version signal to select just one.
//
// The Node test also asserts that the *native* runtime's own
// character.toLowerCase() would have changed the character absent the
// delta -- proving V8's Unicode data is genuinely ahead of Python's here.
// That assertion has no faithful Go equivalent: Go's stdlib unicode.ToLower
// does not change these codepoints either (see the package doc comment),
// so PythonLower151 leaves them unchanged as a direct consequence of Go's
// own stdlib tables, not because deltaForVersion is actively correcting
// anything for Go's real unicode.Version. It is intentionally not ported.
func TestPreservesRuntimeOnlyLowercaseSources(t *testing.T) {
	for _, deltaKey := range []string{"16.0", "17.0"} {
		delta := RuntimeDeltas[deltaKey]
		additions := expandRanges(t, delta.RuntimeOnlyLowercaseSourceRanges)
		if len(additions) == 0 {
			t.Fatalf("%s: expected non-empty runtime-only lowercase sources", deltaKey)
		}
		found := false
		for _, cp := range additions {
			if cp == 0xa7cb {
				found = true
			}
			character := string(cp)
			if got := PythonLower151(character); got != character {
				t.Errorf("%s: PythonLower151(%q) = %q, want unchanged (U+%04X)", deltaKey, character, got, cp)
			}
		}
		if !found {
			t.Errorf("%s: expected 0xa7cb among runtime-only lowercase sources", deltaKey)
		}
	}

	if got, want := PythonLower151("꟎"), "꟎"; got != want {
		t.Errorf("PythonLower151(U+A7CE) = %q, want %q", got, want)
	}
	if got, want := PythonLower151("\U00016ea0"), "\U00016ea0"; got != want {
		t.Errorf("PythonLower151(U+16EA0) = %q, want %q", got, want)
	}
	if got, want := PythonLower151("İ"), "i̇"; got != want {
		t.Errorf("PythonLower151(U+0130) = %q, want %q", got, want)
	}
}

// TestFinalSigmaContext ports "Final Sigma uses Unicode 15.1 Cased and
// Case_Ignorable context" from node-tests/python-lower-151.test.ts verbatim.
func TestFinalSigmaContext(t *testing.T) {
	cases := []struct{ input, want string }{
		{"ᲉΣ", "Ᲊσ"},
		{"AΣᲉ", "aςᲉ"},
		{"AࢗΣ", "aࢗσ"},
		{"AΣࢗA", "aςࢗa"},
		{"꟎Σ", "꟎σ"},
		{"AΣ꟎", "aς꟎"},
		{"ʕΣ", "ʕς"},
		{"AΣʕ", "aσʕ"},
		{"A᫏Σ", "a᫏σ"},
		{"AΣ᫏A", "aς᫏a"},
		{"A\U0001171eΣ", "a\U0001171eς"},
		{"AΣ\U0001171eA", "aσ\U0001171ea"},
	}
	for _, tc := range cases {
		if got := PythonLower151(tc.input); got != tc.want {
			t.Errorf("PythonLower151(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}

	// U+02B0 is both Cased and Case_Ignorable. Final_Sigma must ignore it
	// before considering whether the next significant character is Cased.
	if got, want := PythonLower151("1ʰΣ"), "1ʰσ"; got != want {
		t.Errorf("PythonLower151(%q) = %q, want %q", "1ʰΣ", got, want)
	}
	if got, want := PythonLower151("AΣʰ1A"), "aςʰ1a"; got != want {
		t.Errorf("PythonLower151(%q) = %q, want %q", "AΣʰ1A", got, want)
	}
}

// TestDeltaForVersion adapts "lowercase helper rejects Node Unicode runtime
// drift" from node-tests/python-lower-151.test.ts. Node's requireRuntimeDelta
// is exercised there by mutating the live, mutable process.versions.unicode
// and asserting pythonLower151 throws for an unreviewed value. Go has no
// equivalent mutable runtime-version signal (unicode.Version is a compile-
// time constant), so this instead directly tests deltaForVersion -- the
// pure function currentDelta delegates to -- across reviewed and
// unreviewed version strings: reviewed versions resolve to their reviewed
// deltas ("15.0.0" is the verified no-correction baseline), and any other
// value panics -- the same fail-closed contract as Node's
// requireRuntimeDelta throw.
func TestDeltaForVersion(t *testing.T) {
	if got, want := deltaForVersion("16.0.0"), RuntimeDeltas["16.0"]; !reflect.DeepEqual(got, want) {
		t.Errorf("deltaForVersion(16.0.0) = %+v, want %+v", got, want)
	}
	if got, want := deltaForVersion("17.0.0"), RuntimeDeltas["17.0"]; !reflect.DeepEqual(got, want) {
		t.Errorf("deltaForVersion(17.0.0) = %+v, want %+v", got, want)
	}
	if got := deltaForVersion("15.0.0"); !reflect.DeepEqual(got, RuntimeDelta{}) {
		t.Errorf("deltaForVersion(15.0.0) = %+v, want the zero-value RuntimeDelta", got)
	}
	for _, v := range []string{"15.1.0", "18.0.0", "", "not-a-version"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("deltaForVersion(%q) did not panic; unreviewed Unicode versions must fail closed", v)
				}
			}()
			deltaForVersion(v)
		}()
	}

	// Canary: this package's design rationale (see the package doc comment)
	// depends on Go's stdlib unicode.Version being "15.0.0" -- older than or
	// equal to Python's frozen 15.1 target, never ahead of it the way V8's
	// can be. If a future Go toolchain changes this, the empirical
	// equivalence this package relies on has not been re-verified for the
	// new version and TestExhaustiveDigestMatchesPythonOracle below is the
	// test that would actually catch a real regression.
	if unicode.Version != "15.0.0" {
		t.Logf("Go stdlib unicode.Version is %q, not the \"15.0.0\" this package's design rationale was verified against; re-verify casedBase/caseIgnorableBase/fullLower against a live Python 3.12/3.13 oracle for the new version", unicode.Version)
	}
}

// TestExhaustiveDigestMatchesPythonOracle ports "all Unicode scalars match
// the supported live Python oracle" from
// node-tests/python-lower-151.test.ts -- the strongest available parity
// evidence: a SHA-256 digest folding in PythonLower151's output for every
// Unicode scalar value (surrogates excluded) across 5 context vectors.
//
// The Node test spawns a live Python 3.12/3.13 interpreter at test time and
// asserts equality both with its freshly computed digest *and* with a
// hardcoded hex string literal. There is no separate committed digest
// fixture file for either language: the digest is that hardcoded string
// literal inline in the Node test source itself, not a standalone fixture.
// This Go test hardcodes the same value directly rather than spawning
// python3, keeping `go test` hermetic and independent of whatever Python
// (if any) happens to be installed. The value below was independently
// confirmed during development by running the exact Python oracle script
// embedded in node-tests/python-lower-151.test.ts (PYTHON_EXHAUSTIVE_ORACLE)
// against a live Python 3.13.13 interpreter reporting
// unicodedata.unidata_version == "15.1.0": it produced this exact digest.
func TestExhaustiveDigestMatchesPythonOracle(t *testing.T) {
	const want = "93acb44d32a0d2dffc6d8151c78420d4f35aea2764a74cfa939b315eb68f5db1"
	const hashContract = "infrawright-python-lower-15.1-exhaustive-v1\x00"

	h := sha256.New()
	h.Write([]byte(hashContract))
	var lenBuf [4]byte
	for cp := 0; cp <= 0x10ffff; cp++ {
		if cp >= 0xd800 && cp <= 0xdfff {
			continue
		}
		character := string(rune(cp))
		fmt.Fprintf(h, "%06x", cp)
		for _, vector := range []string{
			character,
			"AΣ" + character,
			"AΣ" + character + "A",
			character + "Σ",
			"A" + character + "Σ",
		} {
			payload := []byte(PythonLower151(vector))
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
			h.Write(lenBuf[:])
			h.Write(payload)
		}
	}

	if got := fmt.Sprintf("%x", h.Sum(nil)); got != want {
		t.Errorf("exhaustive digest = %s, want %s (see node-tests/python-lower-151.test.ts)", got, want)
	}
}
