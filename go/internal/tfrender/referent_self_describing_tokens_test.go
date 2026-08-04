package tfrender

// These tests pin the Phase 7 design revision (see
// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md):
// reference tokens become self-describing about the identifier space they
// name. A bare token or an explicit ".id" token always means the canonical
// space; an explicit "<referent>.<key>.<field>" token names an alternate
// space regardless of what the field-level edge itself declares -- the
// TOKEN's own suffix is authoritative at resolve time, and a mismatch
// between the two is refused loudly rather than silently resolved through
// either space.

import (
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
)

func alternateSpaceContext(idField string) BindingContext {
	return BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"sample_group": true, "sample_rule": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"group_ref": {IDField: idField, NameField: "name", Referent: "sample_group"},
		},
		ResourceRoots: map[string]string{
			"sample_group": "sample_root",
			"sample_rule":  "sample_root",
		},
	}
}

// TestDeriveGeneratedBindingsExplicitIDTokenEquivalentToBare pins that an
// explicit ".id" token resolves identically to the bare token on a
// canonical (IDField "") edge -- the two spellings are synonyms on read.
func TestDeriveGeneratedBindingsExplicitIDTokenEquivalentToBare(t *testing.T) {
	context := alternateSpaceContext("")
	lookupKeys := map[string]map[string]string{
		lookupKeyMapKey("sample_group", ""): {"CUSTOM_1": "group_one"},
	}
	want := `data.terraform_remote_state.sample_root.outputs.iw_reference_ids.sample_group["group_one"]`

	for _, tokenValue := range []string{"sample_group.group_one", "sample_group.group_one.id"} {
		items := map[string]map[string]any{"rule_one": {"group_ref": tokenValue}}
		result, err := DeriveGeneratedBindings(context, items, lookupKeys, "sample_rule")
		if err != nil {
			t.Fatalf("DeriveGeneratedBindings(%q): %v", tokenValue, err)
		}
		fields, ok := result.Resources["sample_rule.rule_one"].(map[string]any)
		if !ok {
			t.Fatalf("DeriveGeneratedBindings(%q) resources = %#v, want rule_one bound", tokenValue, result.Resources)
		}
		binding, ok := fields["group_ref"].(map[string]any)
		if !ok {
			t.Fatalf("DeriveGeneratedBindings(%q) fields = %#v, want group_ref bound", tokenValue, fields)
		}
		if got := binding["expression"]; got != want {
			t.Errorf("DeriveGeneratedBindings(%q) expression = %q, want %q", tokenValue, got, want)
		}
	}
}

// TestDeriveGeneratedBindingsExplicitTokenResolvesWithoutEdgeIDField pins
// the core Phase 7 invariant: a committed ".val" token resolves through the
// val arm purely from the TOKEN's own suffix, even though the field spec
// passed to derivation here carries no IDField at all. The token, not the
// edge, is authoritative for which space a committed value decodes through.
func TestDeriveGeneratedBindingsExplicitTokenResolvesWithoutEdgeIDField(t *testing.T) {
	context := BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"sample_group": true, "sample_rule": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			// Deliberately no IDField: the field itself declares the
			// canonical space, exactly as a pre-Phase-7 pack would.
			"group_ref": {IDField: "val", NameField: "name", Referent: "sample_group"},
		},
		ResourceRoots: map[string]string{
			"sample_group": "sample_root",
			"sample_rule":  "sample_root",
		},
	}
	items := map[string]map[string]any{"rule_one": {"group_ref": "sample_group.group_one.val"}}
	lookupKeys := map[string]map[string]string{
		lookupKeyMapKey("sample_group", "val"): {"501": "group_one"},
	}
	result, err := DeriveGeneratedBindings(context, items, lookupKeys, "sample_rule")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	fields, ok := result.Resources["sample_rule.rule_one"].(map[string]any)
	if !ok {
		t.Fatalf("resources = %#v, want rule_one bound", result.Resources)
	}
	binding, ok := fields["group_ref"].(map[string]any)
	if !ok {
		t.Fatalf("fields = %#v, want group_ref bound", fields)
	}
	want := `data.terraform_remote_state.sample_root.outputs.iw_reference_ids_val.sample_group["group_one"]`
	if got := binding["expression"]; got != want {
		t.Errorf("expression = %q, want %q", got, want)
	}
}

// TestDeriveGeneratedBindingsSpaceMismatchSkipsLoudly is the control: a
// committed token whose suffix disagrees with the edge's declared space is
// never silently resolved through either space. It is skipped with a
// distinct "space_mismatch" note, and the field is left unbound.
func TestDeriveGeneratedBindingsSpaceMismatchSkipsLoudly(t *testing.T) {
	cases := []struct {
		name    string
		idField string
		token   string
	}{
		{"bare_token_on_declared_val_edge", "val", "sample_group.group_one"},
		{"explicit_id_token_on_declared_val_edge", "val", "sample_group.group_one.id"},
		{"explicit_val_token_on_undeclared_edge", "", "sample_group.group_one.val"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			context := alternateSpaceContext(tc.idField)
			items := map[string]map[string]any{"rule_one": {"group_ref": tc.token}}
			lookupKeys := map[string]map[string]string{
				lookupKeyMapKey("sample_group", tc.idField): {"CUSTOM_1": "group_one", "501": "group_one"},
			}
			result, err := DeriveGeneratedBindings(context, items, lookupKeys, "sample_rule")
			if err != nil {
				t.Fatalf("DeriveGeneratedBindings: %v", err)
			}
			if fields, ok := result.Resources["sample_rule.rule_one"].(map[string]any); ok {
				if _, bound := fields["group_ref"]; bound {
					t.Fatalf("fields = %#v, want group_ref left unbound on space mismatch", fields)
				}
			}
			foundMismatchNote := false
			for _, note := range result.Notes {
				if strings.Contains(note, "skipped; token names id space") {
					foundMismatchNote = true
				}
			}
			if !foundMismatchNote {
				t.Errorf("notes = %v, want a space-mismatch skip note", result.Notes)
			}
		})
	}
}

// TestDeriveGeneratedBindingsUnknownSuffixNotRecognizedAsToken pins that a
// suffix which is not an identifier segment (so it cannot name a space at
// all under the Phase 7 grammar) is not recognized as a valid explicit
// token for this referent: it falls to the generic token-shaped-but-
// undecodable classification (the same class an ordinary cross-referent
// token mismatch uses), never a silent resolution.
func TestDeriveGeneratedBindingsUnknownSuffixNotRecognizedAsToken(t *testing.T) {
	context := alternateSpaceContext("val")
	malformed := "sample_group.group_one.not an identifier"
	items := map[string]map[string]any{"rule_one": {"group_ref": malformed}}
	lookupKeys := map[string]map[string]string{
		lookupKeyMapKey("sample_group", "val"): {"501": "group_one"},
	}
	result, err := DeriveGeneratedBindings(context, items, lookupKeys, "sample_rule")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	if fields, ok := result.Resources["sample_rule.rule_one"].(map[string]any); ok {
		if _, bound := fields["group_ref"]; bound {
			t.Fatalf("fields = %#v, want group_ref left unbound", fields)
		}
	}
	foundMismatchNote := false
	for _, note := range result.Notes {
		if strings.Contains(note, "token does not name sample_group") {
			foundMismatchNote = true
		}
	}
	if !foundMismatchNote {
		t.Errorf("notes = %v, want the generic token-shape-mismatch note (malformed suffix is not a recognized explicit token)", result.Notes)
	}
}
