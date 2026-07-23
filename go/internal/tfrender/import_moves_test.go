package tfrender

// import_moves_test.go pins import_moves.go's behavior against
// node-tests/import-moves-differential.test.ts. That file's headline test
// ("generated imports and safe move derivation match Python bytes and
// semantics") drives its CASES table through PYTHON_ORACLE (a live Python
// process), which this Go-only wave cannot invoke (Python is mid-archival
// per docs/go-runtime-plan.md). Rather than skip that coverage, this file
// instead replays the same CASES table through the compiled TypeScript
// directly -- an equally strong byte-exact oracle for this port's purposes,
// since import-moves.ts (not the Python engine.transform module) is what
// this Go package ports.
//
// Probe methodology (reproduced so this file's fixture is independently
// re-derivable): from the repo root,
//
//	npx esbuild node-src/domain/import-moves.ts --bundle --platform=node \
//	  --format=esm --outfile=/tmp/import-moves.mjs
//
// then a small Node driver script (run-import-moves.mjs, not committed)
// imported renderGeneratedImports/parseGeneratedImports/deriveImportMoves/
// renderMovedBlocks/renderHclQuotedString/parseHclQuotedString from that
// bundle, replayed the CASES table transcribed verbatim from
// node-tests/import-moves-differential.test.ts (every field: name, old,
// next), plus the quoted-string round-trip values and the
// caller-order-preservation unsorted-moves example from that same test
// file, and dumped {cases: [...], quotedRoundTrip: [...],
// unsortedMovesRendered} JSON -- committed verbatim as
// testdata/import_moves_differential_probe.json.
//
// The rest of node-tests/import-moves-differential.test.ts's tests
// ("HCL quoted strings round-trip...", "parser accepts empty output and
// rejects...", "duplicate addresses and unsafe resource interpolation...",
// "move rendering preserves caller order...", "duplicate-id candidate
// amplification...") never call pythonDifferential/PYTHON_ORACLE at all --
// they assert directly against renderGeneratedImports/parseGeneratedImports/
// deriveImportMoves/renderMovedBlocks/renderHclQuotedString/
// parseHclQuotedString, so they are ported directly below with no probe
// step needed. "typed boundary is prototype-safe, immutable, and rejects
// ill-typed values" is TS-runtime-specific (prototype pollution via
// __proto__/constructor keys, Object.freeze immutability, and
// null/NaN/non-integer `start` arguments) and is ported only for the
// subset that has a real Go analogue -- see
// TestTypedBoundaryPlainStringKeys and TestParseHclQuotedStringBadStart's
// doc comments for exactly what is and is not portable.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const importMovesResourceType = "zia_rule_labels"

type probePair struct {
	Key      string `json:"key"`
	ImportID string `json:"importId"`
}

type probeMove struct {
	OldKey string `json:"oldKey"`
	NewKey string `json:"newKey"`
}

type probeSuppression struct {
	OldKey   string `json:"oldKey"`
	NewKey   string `json:"newKey"`
	ImportID string `json:"importId"`
	Reason   string `json:"reason"`
}

type probeDerivation struct {
	Moves      []probeMove        `json:"moves"`
	Suppressed []probeSuppression `json:"suppressed"`
}

type probeDifferentialCase struct {
	Name       string          `json:"name"`
	OldText    string          `json:"oldText"`
	NewText    string          `json:"newText"`
	OldPairs   []probePair     `json:"oldPairs"`
	NewPairs   []probePair     `json:"newPairs"`
	Derivation probeDerivation `json:"derivation"`
	MovesText  string          `json:"movesText"`
}

type probeQuotedRoundTrip struct {
	Value    string `json:"value"`
	Rendered string `json:"rendered"`
	Parsed   struct {
		Value string `json:"value"`
		End   int    `json:"end"`
	} `json:"parsed"`
}

type importMovesDifferentialProbe struct {
	Cases                 []probeDifferentialCase `json:"cases"`
	QuotedRoundTrip       []probeQuotedRoundTrip  `json:"quotedRoundTrip"`
	UnsortedMovesRendered string                  `json:"unsortedMovesRendered"`
}

func loadImportMovesDifferentialProbe(t *testing.T) importMovesDifferentialProbe {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "import_moves_differential_probe.json"))
	if err != nil {
		t.Fatalf("reading probe fixture: %v", err)
	}
	var probe importMovesDifferentialProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("decoding probe fixture: %v", err)
	}
	return probe
}

func toGeneratedImportPairs(pairs []probePair) []GeneratedImportPair {
	out := make([]GeneratedImportPair, len(pairs))
	for i, p := range pairs {
		out[i] = GeneratedImportPair{Key: p.Key, ImportID: p.ImportID}
	}
	return out
}

// TestImportMovesDifferentialProbe ports "generated imports and safe move
// derivation match Python bytes and semantics" and "all four unsafe move
// classes remain explicitly suppressed" from
// node-tests/import-moves-differential.test.ts, against the TypeScript
// probe fixture described in this file's package doc comment (in place of
// the Python oracle those tests use, unavailable in this Go-only wave).
func TestImportMovesDifferentialProbe(t *testing.T) {
	probe := loadImportMovesDifferentialProbe(t)
	suppressionReasonCounts := map[string][]string{}

	for _, item := range probe.Cases {
		t.Run(item.Name, func(t *testing.T) {
			oldPairs := toGeneratedImportPairs(item.OldPairs)
			newPairs := toGeneratedImportPairs(item.NewPairs)

			gotOldText, err := RenderGeneratedImports(importMovesResourceType, oldPairs)
			if err != nil {
				t.Fatalf("RenderGeneratedImports(old): %v", err)
			}
			if gotOldText != item.OldText {
				t.Fatalf("old imports bytes: got %q, want %q", gotOldText, item.OldText)
			}
			gotNewText, err := RenderGeneratedImports(importMovesResourceType, newPairs)
			if err != nil {
				t.Fatalf("RenderGeneratedImports(new): %v", err)
			}
			if gotNewText != item.NewText {
				t.Fatalf("new imports bytes: got %q, want %q", gotNewText, item.NewText)
			}

			gotOldPairs, err := ParseGeneratedImports(importMovesResourceType, item.OldText)
			if err != nil {
				t.Fatalf("ParseGeneratedImports(old): %v", err)
			}
			assertPairsEqual(t, "old parse", gotOldPairs, oldPairs)
			gotNewPairs, err := ParseGeneratedImports(importMovesResourceType, item.NewText)
			if err != nil {
				t.Fatalf("ParseGeneratedImports(new): %v", err)
			}
			assertPairsEqual(t, "new parse", gotNewPairs, newPairs)

			derivation, err := DeriveImportMoves(importMovesResourceType, item.OldText, item.NewText)
			if err != nil {
				t.Fatalf("DeriveImportMoves: %v", err)
			}
			assertMovesEqual(t, derivation.Moves, item.Derivation.Moves)
			assertSuppressedEqual(t, derivation.Suppressed, item.Derivation.Suppressed)

			for _, s := range derivation.Suppressed {
				suppressionReasonCounts[item.Name] = append(suppressionReasonCounts[item.Name], string(s.Reason))
			}

			movesText, err := RenderMovedBlocks(importMovesResourceType, derivation.Moves)
			if err != nil {
				t.Fatalf("RenderMovedBlocks: %v", err)
			}
			if movesText != item.MovesText {
				t.Fatalf("moves bytes: got %q, want %q", movesText, item.MovesText)
			}
		})
	}

	// "all four unsafe move classes remain explicitly suppressed"
	wantReasons := map[string][]string{
		"key-swap":             {"key_swap", "key_swap"},
		"destination-occupied": {"destination_occupied"},
		"duplicate-from":       {"duplicate_from", "duplicate_from"},
		"ambiguous-old-id":     {"ambiguous", "ambiguous"},
	}
	for name, want := range wantReasons {
		got := suppressionReasonCounts[name]
		if len(got) != len(want) {
			t.Fatalf("%s: got %d suppression reasons %v, want %v", name, len(got), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: suppression[%d] = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
}

func assertPairsEqual(t *testing.T, label string, got, want []GeneratedImportPair) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d pairs, want %d", label, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s[%d]: got %+v, want %+v", label, i, got[i], want[i])
		}
	}
}

func assertMovesEqual(t *testing.T, got []ImportMove, want []probeMove) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("moves: got %d, want %d (%+v vs %+v)", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i].OldKey != want[i].OldKey || got[i].NewKey != want[i].NewKey {
			t.Fatalf("moves[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func assertSuppressedEqual(t *testing.T, got []ImportMoveSuppression, want []probeSuppression) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("suppressed: got %d, want %d (%+v vs %+v)", len(got), len(want), got, want)
	}
	for i := range got {
		g := got[i]
		w := want[i]
		if g.OldKey != w.OldKey || g.NewKey != w.NewKey || g.ImportID != w.ImportID || string(g.Reason) != w.Reason {
			t.Fatalf("suppressed[%d]: got %+v, want %+v", i, g, w)
		}
	}
}

// TestRenderHclQuotedStringRoundTrip ports "HCL quoted strings round-trip
// only the generated escape grammar" from
// node-tests/import-moves-differential.test.ts.
func TestRenderHclQuotedStringRoundTrip(t *testing.T) {
	probe := loadImportMovesDifferentialProbe(t)
	for _, item := range probe.QuotedRoundTrip {
		rendered, err := RenderHclQuotedString(item.Value)
		if err != nil {
			t.Fatalf("RenderHclQuotedString(%q): %v", item.Value, err)
		}
		if rendered != item.Rendered {
			t.Fatalf("RenderHclQuotedString(%q) = %q, want %q", item.Value, rendered, item.Rendered)
		}
		parsed, err := ParseHclQuotedString(rendered, 0)
		if err != nil {
			t.Fatalf("ParseHclQuotedString(%q): %v", rendered, err)
		}
		if parsed.Value != item.Value || parsed.End != len(rendered) {
			t.Fatalf("ParseHclQuotedString(%q) = %+v, want value=%q end=%d", rendered, parsed, item.Value, len(rendered))
		}
	}

	// Ports the TS source's `parseHclQuotedString('"bad\\u0020escape"')`:
	// a JS single-quoted string literal where `\\` is one literal
	// backslash, so the runtime text contains a literal backslash-u
	// escape this grammar does not support (only \n, \r, \t, \", and \\
	// are recognized). badEscape is a Go raw (backtick) string, so its
	// backslash is likewise literal, not a Go escape.
	badEscape := "\"bad" + `\` + "u0020escape\""
	if _, err := ParseHclQuotedString(badEscape, 0); err == nil {
		t.Fatal("expected an error for an unsupported escape sequence")
	} else if pf, ok := err.(*procerr.ProcessFailure); !ok || pf.Code != "INVALID_HCL_QUOTED_STRING" {
		t.Fatalf("expected a ProcessFailure with code INVALID_HCL_QUOTED_STRING, got %v", err)
	}
	if _, err := RenderHclQuotedString("bad\x00value"); err == nil {
		t.Fatal("expected an error for a NUL byte")
	} else if pf, ok := err.(*procerr.ProcessFailure); !ok || pf.Code != "INVALID_HCL_QUOTED_STRING" {
		t.Fatalf("expected a ProcessFailure with code INVALID_HCL_QUOTED_STRING, got %v", err)
	}
}

// TestParseGeneratedImportsRejectsNoncanonicalEvidence ports "parser
// accepts empty output and rejects noncanonical or incomplete import
// evidence" from node-tests/import-moves-differential.test.ts.
func TestParseGeneratedImportsRejectsNoncanonicalEvidence(t *testing.T) {
	empty, err := ParseGeneratedImports(importMovesResourceType, "")
	if err != nil || len(empty) != 0 {
		t.Fatalf("ParseGeneratedImports(\"\") = %+v, %v; want empty, nil", empty, err)
	}

	alpha, err := RenderGeneratedImports(importMovesResourceType, []GeneratedImportPair{
		{Key: "alpha", ImportID: "secret-alpha-id"},
	})
	if err != nil {
		t.Fatalf("RenderGeneratedImports(alpha): %v", err)
	}
	beta, err := RenderGeneratedImports(importMovesResourceType, []GeneratedImportPair{
		{Key: "beta", ImportID: "secret-beta-id"},
	})
	if err != nil {
		t.Fatalf("RenderGeneratedImports(beta): %v", err)
	}
	canonicalTwo, err := RenderGeneratedImports(importMovesResourceType, []GeneratedImportPair{
		{Key: "alpha", ImportID: "secret-alpha-id"},
		{Key: "beta", ImportID: "secret-beta-id"},
	})
	if err != nil {
		t.Fatalf("RenderGeneratedImports(two): %v", err)
	}

	malformed := []string{
		"# comment\n" + alpha,
		" " + alpha,
		alpha + "\n",
		strings.ReplaceAll(alpha, "\n", "\r\n"),
		strings.Replace(alpha, "import {", "import  {", 1),
		strings.Replace(alpha, "  to =", "  from =", 1),
		strings.Replace(alpha, "module."+importMovesResourceType, "module.zia_other", 1),
		strings.Replace(alpha, "."+importMovesResourceType+".this", ".zia_other.this", 1),
		strings.Replace(alpha, "\n  id =", "\n  unexpected = \"x\"\n  id =", 1),
		strings.Replace(alpha, "\n  id =", "\n  id = \"duplicate\"\n  id =", 1),
		strings.Replace(alpha, "\n  id =", "", 1),
		alpha[:len(alpha)-1],
		strings.Replace(alpha, "secret-alpha-id", "bad\\u0020id", 1),
		strings.Replace(alpha, "secret-alpha-id", "${raw_interpolation}", 1),
		alpha + "\n" + alpha,
		beta + "\n" + alpha,
		strings.Replace(canonicalTwo, "\n\nimport {", "\nimport {", 1),
		canonicalTwo + "unexpected",
	}

	for i, text := range malformed {
		_, err := ParseGeneratedImports(importMovesResourceType, text)
		if err == nil {
			t.Fatalf("malformed[%d] %q: expected an error", i, text)
		}
		pf, ok := err.(*procerr.ProcessFailure)
		if !ok {
			t.Fatalf("malformed[%d]: expected a *procerr.ProcessFailure, got %T: %v", i, err, err)
		}
		if pf.Category != procerr.CategoryDomain {
			t.Fatalf("malformed[%d]: category = %q, want domain", i, pf.Category)
		}
		if pf.Retryable {
			t.Fatalf("malformed[%d]: retryable = true, want false", i)
		}
		if pf.Code != "INVALID_GENERATED_IMPORTS" && pf.Code != "INVALID_HCL_QUOTED_STRING" {
			t.Fatalf("malformed[%d]: code = %q, want INVALID_GENERATED_IMPORTS or INVALID_HCL_QUOTED_STRING", i, pf.Code)
		}
		if strings.Contains(pf.Message, "secret-alpha-id") || strings.Contains(pf.Message, "secret-beta-id") {
			t.Fatalf("malformed[%d]: message leaked secret content: %q", i, pf.Message)
		}
		if len(pf.Details) != 0 {
			t.Fatalf("malformed[%d]: details = %+v, want empty", i, pf.Details)
		}
	}
}

// TestDuplicateAddressesAndUnsafeInterpolationFailValueSafely ports
// "duplicate addresses and unsafe resource interpolation fail
// value-safely" from node-tests/import-moves-differential.test.ts.
func TestDuplicateAddressesAndUnsafeInterpolationFailValueSafely(t *testing.T) {
	_, err := RenderGeneratedImports(importMovesResourceType, []GeneratedImportPair{
		{Key: "private-address", ImportID: "first-private-id"},
		{Key: "private-address", ImportID: "second-private-id"},
	})
	if err == nil {
		t.Fatal("expected an error for a duplicate Terraform address")
	}
	pf, ok := err.(*procerr.ProcessFailure)
	if !ok || pf.Code != "INVALID_GENERATED_IMPORTS" {
		t.Fatalf("expected code INVALID_GENERATED_IMPORTS, got %v", err)
	}
	if strings.Contains(pf.Message, "private") || strings.Contains(pf.Message, "first") || strings.Contains(pf.Message, "second") {
		t.Fatalf("message leaked secret content: %q", pf.Message)
	}

	_, err = RenderMovedBlocks("zia_rule_labels] malicious-private-text", []ImportMove{
		{OldKey: "private-old", NewKey: "private-new"},
	})
	if err == nil {
		t.Fatal("expected an error for an invalid resource type")
	}
	pf, ok = err.(*procerr.ProcessFailure)
	if !ok || pf.Code != "INVALID_IMPORT_RESOURCE_TYPE" {
		t.Fatalf("expected code INVALID_IMPORT_RESOURCE_TYPE, got %v", err)
	}
	if strings.Contains(pf.Message, "malicious") || strings.Contains(pf.Message, "private") {
		t.Fatalf("message leaked secret content: %q", pf.Message)
	}
}

// TestMoveRenderingPreservesCallerOrder ports "move rendering preserves
// caller order while derived moves are sorted" from
// node-tests/import-moves-differential.test.ts.
func TestMoveRenderingPreservesCallerOrder(t *testing.T) {
	probe := loadImportMovesDifferentialProbe(t)
	unsorted := []ImportMove{{OldKey: "z", NewKey: "z-new"}, {OldKey: "a", NewKey: "a-new"}}
	rendered, err := RenderMovedBlocks(importMovesResourceType, unsorted)
	if err != nil {
		t.Fatalf("RenderMovedBlocks: %v", err)
	}
	if rendered != probe.UnsortedMovesRendered {
		t.Fatalf("got %q, want %q", rendered, probe.UnsortedMovesRendered)
	}
	if !(strings.Index(rendered, `this["z"]`) < strings.Index(rendered, `this["a"]`)) {
		t.Fatalf("expected caller order (z before a) preserved in %q", rendered)
	}

	oldText, err := RenderGeneratedImports(importMovesResourceType, []GeneratedImportPair{
		{Key: "z", ImportID: "1"}, {Key: "a", ImportID: "2"},
	})
	if err != nil {
		t.Fatalf("RenderGeneratedImports(old): %v", err)
	}
	newText, err := RenderGeneratedImports(importMovesResourceType, []GeneratedImportPair{
		{Key: "z-new", ImportID: "1"}, {Key: "a-new", ImportID: "2"},
	})
	if err != nil {
		t.Fatalf("RenderGeneratedImports(new): %v", err)
	}
	derivation, err := DeriveImportMoves(importMovesResourceType, oldText, newText)
	if err != nil {
		t.Fatalf("DeriveImportMoves: %v", err)
	}
	want := []ImportMove{{OldKey: "a", NewKey: "a-new"}, {OldKey: "z", NewKey: "z-new"}}
	assertMovesEqual(t, derivation.Moves, []probeMove{{OldKey: want[0].OldKey, NewKey: want[0].NewKey}, {OldKey: want[1].OldKey, NewKey: want[1].NewKey}})
}

// TestDuplicateIDCandidateAmplificationBounded ports "duplicate-id
// candidate amplification fails at a value-safe fixed bound" from
// node-tests/import-moves-differential.test.ts.
func TestDuplicateIDCandidateAmplificationBounded(t *testing.T) {
	const count = 225
	oldPairs := make([]GeneratedImportPair, count)
	newPairs := make([]GeneratedImportPair, count)
	for i := 0; i < count; i++ {
		oldPairs[i] = GeneratedImportPair{Key: "old-" + pad3(i), ImportID: "private-repeated-id"}
		newPairs[i] = GeneratedImportPair{Key: "new-" + pad3(i), ImportID: "private-repeated-id"}
	}
	oldText, err := RenderGeneratedImports(importMovesResourceType, oldPairs)
	if err != nil {
		t.Fatalf("RenderGeneratedImports(old): %v", err)
	}
	newText, err := RenderGeneratedImports(importMovesResourceType, newPairs)
	if err != nil {
		t.Fatalf("RenderGeneratedImports(new): %v", err)
	}
	_, err = DeriveImportMoves(importMovesResourceType, oldText, newText)
	if err == nil {
		t.Fatal("expected IMPORT_MOVE_LIMIT_EXCEEDED")
	}
	pf, ok := err.(*procerr.ProcessFailure)
	if !ok || pf.Code != "IMPORT_MOVE_LIMIT_EXCEEDED" {
		t.Fatalf("expected code IMPORT_MOVE_LIMIT_EXCEEDED, got %v", err)
	}
	if strings.Contains(pf.Message, "private") || strings.Contains(pf.Message, "old-") || strings.Contains(pf.Message, "new-") {
		t.Fatalf("message leaked candidate content: %q", pf.Message)
	}
	if len(pf.Details) != 0 {
		t.Fatalf("details = %+v, want empty", pf.Details)
	}
}

func pad3(i int) string {
	s := strconv.Itoa(i)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

// TestTypedBoundaryPlainStringKeys ports the data-shape-independent half of
// "typed boundary is prototype-safe, immutable, and rejects ill-typed
// values" from node-tests/import-moves-differential.test.ts: using
// JS-prototype-name-shaped strings (__proto__, constructor, prototype,
// toString) as ordinary key/importId values. Go has no object-prototype-
// pollution concept for map[string]string keys (a Go map is never backed
// by a shared prototype chain the way a JS object literal is), so the
// TS test's specific concern -- that a JSON-derived value literally named
// "__proto__" doesn't silently mutate Object.prototype -- has no Go
// analogue to fail; this test instead confirms these strings are treated
// as perfectly ordinary opaque key/id text, which is the only assertion
// from that TS test with a real Go equivalent. The TS test's
// Object.isFrozen(...) assertions (immutability enforcement) are skipped
// entirely: Go has no runtime object-freezing concept, and this package's
// return values are ordinary mutable Go values by construction.
func TestTypedBoundaryPlainStringKeys(t *testing.T) {
	oldPairs := []GeneratedImportPair{
		{Key: "__proto__", ImportID: "constructor"},
		{Key: "constructor", ImportID: "__proto__"},
	}
	newPairs := []GeneratedImportPair{
		{Key: "prototype", ImportID: "constructor"},
		{Key: "toString", ImportID: "__proto__"},
	}
	oldText, err := RenderGeneratedImports(importMovesResourceType, oldPairs)
	if err != nil {
		t.Fatalf("RenderGeneratedImports(old): %v", err)
	}
	newText, err := RenderGeneratedImports(importMovesResourceType, newPairs)
	if err != nil {
		t.Fatalf("RenderGeneratedImports(new): %v", err)
	}
	gotOldPairs, err := ParseGeneratedImports(importMovesResourceType, oldText)
	if err != nil {
		t.Fatalf("ParseGeneratedImports(old): %v", err)
	}
	assertPairsEqual(t, "old parse", gotOldPairs, oldPairs)

	derivation, err := DeriveImportMoves(importMovesResourceType, oldText, newText)
	if err != nil {
		t.Fatalf("DeriveImportMoves: %v", err)
	}
	want := []probeMove{
		{OldKey: "__proto__", NewKey: "prototype"},
		{OldKey: "constructor", NewKey: "toString"},
	}
	assertMovesEqual(t, derivation.Moves, want)
}

// TestParseHclQuotedStringBadStart ports the Go-portable subset of
// ParseHclQuotedString's `start` boundary checks from "typed boundary is
// prototype-safe, immutable, and rejects ill-typed values": a negative
// start, and a start at or beyond the text's length. The TS test's other
// probed `start` values (0.5, NaN, +Infinity) have no Go analogue: Go's
// `int` parameter type cannot hold a non-integer, NaN, or infinite value in
// the first place, so there is no runtime check to port for them -- the Go
// compiler's static type already provides the guarantee those TS runtime
// checks exist to enforce. The TS test's `parseHclQuotedString(null)` case
// is similarly inapplicable: this port's `text string` parameter cannot be
// Go nil.
func TestParseHclQuotedStringBadStart(t *testing.T) {
	for _, start := range []int{-1, 3} {
		_, err := ParseHclQuotedString(`"x"`, start)
		if err == nil {
			t.Fatalf("start=%d: expected an error", start)
		}
		pf, ok := err.(*procerr.ProcessFailure)
		if !ok || pf.Code != "INVALID_HCL_QUOTED_STRING" {
			t.Fatalf("start=%d: expected code INVALID_HCL_QUOTED_STRING, got %v", start, err)
		}
	}
}

// TestRenderGeneratedImportsAndMovedBlocksAcceptNilSlices documents a
// deliberate Go/TS divergence noted in this file's package doc comment:
// node-tests/import-moves-differential.test.ts asserts
// `renderGeneratedImports(RESOURCE_TYPE, null)` and
// `renderMovedBlocks(RESOURCE_TYPE, null)` both throw
// INVALID_GENERATED_IMPORTS/INVALID_IMPORT_MOVES, because the TS source
// runs a `!Array.isArray(...)` runtime guard against a value whose static
// type promises an array but whose runtime value might not be one. Go's
// []GeneratedImportPair/[]ImportMove parameter types make that situation
// unreachable: a Go nil slice is not an anomalous value requiring
// rejection, it is simply an empty slice (len(nil) == 0), and both
// functions below produce exactly the same empty-pairs output as an
// explicit empty, non-nil slice would.
func TestRenderGeneratedImportsAndMovedBlocksAcceptNilSlices(t *testing.T) {
	text, err := RenderGeneratedImports(importMovesResourceType, nil)
	if err != nil || text != "" {
		t.Fatalf("RenderGeneratedImports(nil) = %q, %v; want \"\", nil", text, err)
	}
	moved, err := RenderMovedBlocks(importMovesResourceType, nil)
	if err != nil || moved != "" {
		t.Fatalf("RenderMovedBlocks(nil) = %q, %v; want \"\", nil", moved, err)
	}
}
