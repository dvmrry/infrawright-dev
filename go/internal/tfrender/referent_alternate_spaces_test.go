package tfrender

// These tests pin Phase 3 of the referent-alternate-id-spaces design (see
// docs/superpowers/specs/2026-08-04-referent-alternate-id-spaces.md): the
// transform compile path selecting the edge's declared space end to end.
// TransformReferenceSpec.IDField carries the edge's referent_id_field, and
// resolve()/lookupKeyMaps must key off it consistently.

import (
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
)

// TestDeriveGeneratedBindingsAlternateSpaceEmitsSiblingSelector pins the
// exact selector string an IDField-declaring edge derives: the sibling
// output "iw_reference_ids_<field>", never the canonical "iw_reference_ids"
// -- and proves the referrer's raw alternate-space value (not shaped like a
// token) still resolves via the alternate key map.
func TestDeriveGeneratedBindingsAlternateSpaceEmitsSiblingSelector(t *testing.T) {
	context := BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"sample_group": true, "sample_rule": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"group_ref": {IDField: "val", NameField: "name", Referent: "sample_group"},
		},
		ResourceRoots: map[string]string{
			"sample_group": "sample_root",
			"sample_rule":  "sample_root",
		},
	}
	items := map[string]map[string]any{"rule_one": {"group_ref": "501"}}
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

// TestDeriveGeneratedBindingsDefaultSpaceSelectorUnchanged is the byte-
// identical control: an edge with no declared IDField still emits the
// canonical "iw_reference_ids" selector, proving IDField's zero value is a
// true no-op.
func TestDeriveGeneratedBindingsDefaultSpaceSelectorUnchanged(t *testing.T) {
	context := BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"sample_group": true, "sample_rule": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"group_id": {NameField: "name", Referent: "sample_group"},
		},
		ResourceRoots: map[string]string{
			"sample_group": "sample_root",
			"sample_rule":  "sample_root",
		},
	}
	items := map[string]map[string]any{"rule_one": {"group_id": "id-1"}}
	lookupKeys := map[string]map[string]string{
		lookupKeyMapKey("sample_group", ""): {"id-1": "group_one"},
	}
	result, err := DeriveGeneratedBindings(context, items, lookupKeys, "sample_rule")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	fields := result.Resources["sample_rule.rule_one"].(map[string]any)
	binding := fields["group_id"].(map[string]any)
	want := `data.terraform_remote_state.sample_root.outputs.iw_reference_ids.sample_group["group_one"]`
	if got := binding["expression"]; got != want {
		t.Errorf("expression = %q, want %q", got, want)
	}
}

// TestCompileTransformArtifactsAlternateSpaceMissingDerivesNothing pins
// invariant 4 of the design: an edge citing a space the referent's
// committed sidecar does not carry derives nothing (reported as a missing
// lookup) and never mis-resolves or binds past -- the value stays literal
// in committed config, exactly as a referent with no lookup at all would.
func TestCompileTransformArtifactsAlternateSpaceMissingDerivesNothing(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_rule")
	options.References = map[string]TransformReferenceSpec{
		"group_ref": {IDField: "val", NameField: "name", Referent: "sample_group"},
	}
	options.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"rule_one": {"group_ref": "501", "name": "Rule One"}},
		Originals: map[string]map[string]any{"rule_one": {"id": "rule-1", "group_ref": "501", "name": "Rule One"}},
	}
	options.BindingContext = BindingContext{
		Mode:       deployment.ReferenceBindingCrossState,
		Derived:    map[string]bool{},
		Generated:  map[string]bool{"sample_group": true, "sample_rule": true},
		References: options.References,
		ResourceRoots: map[string]string{
			"sample_group": "sample_group",
			"sample_rule":  "sample_rule",
		},
	}
	// The stale sidecar on disk (via LookupOverrides, the test seam for
	// injected lookup data) carries the canonical id space but no "val"
	// space at all -- the shape a sidecar written before the pack declared
	// the alternate space would have.
	options.LookupOverrides = map[string]*TransformLookupData{
		"sample_group": {KeyByID: map[string]string{"CUSTOM_1": "group_one"}},
	}

	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifacts: %v", err)
	}
	if len(compiled.Binding.Resources) != 0 {
		t.Fatalf("binding resources = %#v, want nothing derived for a missing space", compiled.Binding.Resources)
	}
	foundMissingLookupNote := false
	for _, note := range compiled.Binding.Notes {
		if strings.Contains(note, "lookup for sample_group is missing") {
			foundMissingLookupNote = true
		}
	}
	if !foundMissingLookupNote {
		t.Errorf("binding notes = %v, want a missing-lookup note for sample_group", compiled.Binding.Notes)
	}
	if !strings.Contains(compiled.ConfigText, `"501"`) {
		t.Errorf("config text = %q, want the literal alternate-space value untouched", compiled.ConfigText)
	}
}
