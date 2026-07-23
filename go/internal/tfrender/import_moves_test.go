package tfrender

import (
	"strconv"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const importMovesResourceType = "zia_rule_labels"

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

func assertMovesEqual(t *testing.T, got, want []ImportMove) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("moves: got %d, want %d (%+v vs %+v)", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("moves[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func assertSuppressedEqual(t *testing.T, got, want []ImportMoveSuppression) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("suppressed: got %d, want %d (%+v vs %+v)", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("suppressed[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func deriveImportMovesForPairs(t *testing.T, oldPairs, newPairs []GeneratedImportPair) ImportMoveDerivation {
	t.Helper()
	oldText, err := RenderGeneratedImports(importMovesResourceType, oldPairs)
	if err != nil {
		t.Fatalf("RenderGeneratedImports(old): %v", err)
	}
	newText, err := RenderGeneratedImports(importMovesResourceType, newPairs)
	if err != nil {
		t.Fatalf("RenderGeneratedImports(new): %v", err)
	}
	derivation, err := DeriveImportMoves(importMovesResourceType, oldText, newText)
	if err != nil {
		t.Fatalf("DeriveImportMoves: %v", err)
	}
	return derivation
}

func TestImportMoveDerivation(t *testing.T) {
	t.Run("safe rename", func(t *testing.T) {
		got := deriveImportMovesForPairs(t,
			[]GeneratedImportPair{{Key: "old_name", ImportID: "101"}},
			[]GeneratedImportPair{{Key: "new_name", ImportID: "101"}},
		)
		assertMovesEqual(t, got.Moves, []ImportMove{{OldKey: "old_name", NewKey: "new_name"}})
		assertSuppressedEqual(t, got.Suppressed, nil)
	})

	for _, test := range []struct {
		name     string
		oldPairs []GeneratedImportPair
		newPairs []GeneratedImportPair
		want     []ImportMoveSuppression
	}{
		{
			name:     "key swap",
			oldPairs: []GeneratedImportPair{{Key: "a", ImportID: "101"}, {Key: "b", ImportID: "102"}},
			newPairs: []GeneratedImportPair{{Key: "a", ImportID: "102"}, {Key: "b", ImportID: "101"}},
			want: []ImportMoveSuppression{
				{OldKey: "a", NewKey: "b", ImportID: "101", Reason: SuppressionKeySwap},
				{OldKey: "b", NewKey: "a", ImportID: "102", Reason: SuppressionKeySwap},
			},
		},
		{
			name:     "destination occupied",
			oldPairs: []GeneratedImportPair{{Key: "a", ImportID: "101"}, {Key: "b", ImportID: "102"}},
			newPairs: []GeneratedImportPair{{Key: "b", ImportID: "101"}},
			want:     []ImportMoveSuppression{{OldKey: "a", NewKey: "b", ImportID: "101", Reason: SuppressionDestinationOccupied}},
		},
		{
			name:     "duplicate source",
			oldPairs: []GeneratedImportPair{{Key: "a", ImportID: "101"}},
			newPairs: []GeneratedImportPair{{Key: "b", ImportID: "101"}, {Key: "c", ImportID: "101"}},
			want: []ImportMoveSuppression{
				{OldKey: "a", NewKey: "b", ImportID: "101", Reason: SuppressionDuplicateFrom},
				{OldKey: "a", NewKey: "c", ImportID: "101", Reason: SuppressionDuplicateFrom},
			},
		},
		{
			name:     "ambiguous old id",
			oldPairs: []GeneratedImportPair{{Key: "a", ImportID: "101"}, {Key: "b", ImportID: "101"}},
			newPairs: []GeneratedImportPair{{Key: "c", ImportID: "101"}},
			want: []ImportMoveSuppression{
				{OldKey: "a", NewKey: "c", ImportID: "101", Reason: SuppressionAmbiguous},
				{OldKey: "b", NewKey: "c", ImportID: "101", Reason: SuppressionAmbiguous},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := deriveImportMovesForPairs(t, test.oldPairs, test.newPairs)
			assertMovesEqual(t, got.Moves, nil)
			assertSuppressedEqual(t, got.Suppressed, test.want)
		})
	}
}

func TestRenderHclQuotedStringRoundTrip(t *testing.T) {
	for _, item := range []struct {
		value    string
		rendered string
	}{
		{"", `""`},
		{"plain", `"plain"`},
		{"quote\"slash\\line\nreturn\rtab\t", `"quote\"slash\\line\nreturn\rtab\t"`},
		{"${name} %{ if true } $${already} %%{already}", `"$${name} %%{ if true } $$${already} %%%{already}"`},
		{"東京😀", `"東京😀"`},
	} {
		rendered, err := RenderHclQuotedString(item.value)
		if err != nil {
			t.Fatalf("RenderHclQuotedString(%q): %v", item.value, err)
		}
		if rendered != item.rendered {
			t.Fatalf("RenderHclQuotedString(%q) = %q, want %q", item.value, rendered, item.rendered)
		}
		parsed, err := ParseHclQuotedString(rendered, 0)
		if err != nil {
			t.Fatalf("ParseHclQuotedString(%q): %v", rendered, err)
		}
		if parsed.Value != item.value || parsed.End != len(rendered) {
			t.Fatalf("ParseHclQuotedString(%q) = %+v, want value=%q end=%d", rendered, parsed, item.value, len(rendered))
		}
	}

	// Only \n, \r, \t, \", and \\ escapes are accepted.
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

func TestMoveRenderingPreservesCallerOrder(t *testing.T) {
	unsorted := []ImportMove{{OldKey: "z", NewKey: "z-new"}, {OldKey: "a", NewKey: "a-new"}}
	rendered, err := RenderMovedBlocks(importMovesResourceType, unsorted)
	if err != nil {
		t.Fatalf("RenderMovedBlocks: %v", err)
	}
	wantRendered := "moved {\n  from = module.zia_rule_labels.zia_rule_labels.this[\"z\"]\n  to   = module.zia_rule_labels.zia_rule_labels.this[\"z-new\"]\n}\n\nmoved {\n  from = module.zia_rule_labels.zia_rule_labels.this[\"a\"]\n  to   = module.zia_rule_labels.zia_rule_labels.this[\"a-new\"]\n}\n"
	if rendered != wantRendered {
		t.Fatalf("got %q, want %q", rendered, wantRendered)
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
	assertMovesEqual(t, derivation.Moves, want)
}

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

// TestTypedBoundaryPlainStringKeys confirms that prototype-shaped strings
// remain ordinary opaque key and import-ID values.
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
	want := []ImportMove{
		{OldKey: "__proto__", NewKey: "prototype"},
		{OldKey: "constructor", NewKey: "toString"},
	}
	assertMovesEqual(t, derivation.Moves, want)
}

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

// Nil slices are the empty input for both renderers.
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
