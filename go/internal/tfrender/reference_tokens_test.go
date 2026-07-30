package tfrender

// These tests pin DeriveGeneratedBindings's Task-2 behaviour: resolve()
// must consume the qualified reference tokens ("<referent>.<key>") that a
// later substitution pass (plan Task 1) will start committing, while
// still accepting today's raw-ID shape indefinitely (the migration path).
// See docs/superpowers/specs/2026-07-30-reference-tokens-design.md and
// docs/superpowers/plans/2026-07-30-reference-tokens-plan.md.

import (
	"encoding/json"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
)

// tokenTopLevelContext mirrors TestDeriveGeneratedBindingsTopLevel's
// context exactly, so the only variable between that test and these is the
// committed value's shape (ID vs token).
func tokenTopLevelContext() BindingContext {
	return BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"zpa_application_segment": true, "zpa_segment_group": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"segment_group_id": {NameField: "name", Referent: "zpa_segment_group"},
		},
		ResourceRoots: map[string]string{
			"zpa_application_segment": "zpa_custom",
			"zpa_segment_group":       "zpa_custom",
		},
	}
}

// TestDeriveGeneratedBindingsTokenResolvesWithoutIDHop pins the P1
// substitution shape: a committed value already carrying the qualified
// token derives the identical expression TestDeriveGeneratedBindingsTopLevel
// derives from the raw ID -- resolve never touches key_by_id's ID->key
// mapping for it, because the key is already in the value.
func TestDeriveGeneratedBindingsTokenResolvesWithoutIDHop(t *testing.T) {
	items := map[string]map[string]any{"app_one": {"segment_group_id": "zpa_segment_group.segment_one"}}
	// The ID->key entry is deliberately absent (only "sg-1" would map to
	// "segment_one" via an ID hop); only key membership matters for a token.
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	result, err := DeriveGeneratedBindings(tokenTopLevelContext(), items, lookupKeys, "zpa_application_segment")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	fields, ok := result.Resources["zpa_application_segment.app_one"].(map[string]any)
	if !ok {
		t.Fatalf("resources = %#v, want app_one bound", result.Resources)
	}
	binding := fields["segment_group_id"].(map[string]any)
	want := `data.terraform_remote_state.zpa_custom.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`
	if got := binding["expression"]; got != want {
		t.Errorf("expression = %q, want %q", got, want)
	}
	wantNotes := []string{"zpa_application_segment: 1 bound, 0 skipped"}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestDeriveGeneratedBindingsTokenWrongReferentMismatch pins the loud
// failure mode for a token qualified for a different referent than the
// field declares: skipped and counted under token_referent_mismatch, never
// silently treated as an unresolvable ID.
func TestDeriveGeneratedBindingsTokenWrongReferentMismatch(t *testing.T) {
	items := map[string]map[string]any{"app_one": {"segment_group_id": "zpa_server_group.segment_one"}}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	result, err := DeriveGeneratedBindings(tokenTopLevelContext(), items, lookupKeys, "zpa_application_segment")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	if len(result.Resources) != 0 {
		t.Errorf("resources = %#v, want nothing bound", result.Resources)
	}
	wantNotes := []string{
		`zpa_application_segment.app_one.segment_group_id value "zpa_server_group.segment_one" skipped; token does not name zpa_segment_group`,
		"zpa_application_segment: 0 bound, 1 skipped (token_referent_mismatch=1)",
	}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestDeriveGeneratedBindingsTokenKeyUnknown pins the loud failure mode for
// a correctly-qualified token whose key the referent's key_by_id map does
// not contain: skipped and counted under token_key_unknown.
func TestDeriveGeneratedBindingsTokenKeyUnknown(t *testing.T) {
	items := map[string]map[string]any{"app_one": {"segment_group_id": "zpa_segment_group.ghost"}}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	result, err := DeriveGeneratedBindings(tokenTopLevelContext(), items, lookupKeys, "zpa_application_segment")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	if len(result.Resources) != 0 {
		t.Errorf("resources = %#v, want nothing bound", result.Resources)
	}
	wantNotes := []string{
		`zpa_application_segment.app_one.segment_group_id value "zpa_segment_group.ghost" skipped; token key is unknown to zpa_segment_group`,
		"zpa_application_segment: 0 bound, 1 skipped (token_key_unknown=1)",
	}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestDeriveGeneratedBindingsPlainIDStillResolves pins the migration path:
// an old-shape committed value (a raw tenant ID, not a token) resolves
// exactly as before -- old-shape configs remain valid indefinitely, with no
// flag day forcing a rewrite.
func TestDeriveGeneratedBindingsPlainIDStillResolves(t *testing.T) {
	items := map[string]map[string]any{"app_one": {"segment_group_id": "sg-1"}}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	result, err := DeriveGeneratedBindings(tokenTopLevelContext(), items, lookupKeys, "zpa_application_segment")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	fields := result.Resources["zpa_application_segment.app_one"].(map[string]any)
	binding := fields["segment_group_id"].(map[string]any)
	want := `data.terraform_remote_state.zpa_custom.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`
	if got := binding["expression"]; got != want {
		t.Errorf("expression = %q, want %q", got, want)
	}
	wantNotes := []string{"zpa_application_segment: 1 bound, 0 skipped"}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestDeriveGeneratedBindingsTokenListMixing extends
// TestDeriveGeneratedBindingsRetainsUnresolvedDiagnostics's list shape with a
// token element: tokens, plain IDs and unresolvable values must be able to
// sit side by side in one list, exactly as mixed ID lists do today.
func TestDeriveGeneratedBindingsTokenListMixing(t *testing.T) {
	context := BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"zpa_app_connector_group": true, "zpa_server_group": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"server_groups.id": {NameField: "name", Referent: "zpa_server_group"},
		},
		ResourceRoots: map[string]string{
			"zpa_app_connector_group": "zpa_app",
			"zpa_server_group":        "zpa_app",
		},
	}
	items := map[string]map[string]any{
		"connector_one": {
			"server_groups": []any{
				map[string]any{"id": []any{"zpa_server_group.known", "sg-missing"}},
			},
		},
	}
	// No ID->key entry for "known" at all -- the token must resolve purely
	// from key membership, never an ID hop.
	lookupKeys := map[string]map[string]string{"zpa_server_group": {"sg-known": "known"}}
	result, err := DeriveGeneratedBindings(context, items, lookupKeys, "zpa_app_connector_group")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	fields, ok := result.Resources["zpa_app_connector_group.connector_one"].(map[string]any)
	if !ok {
		t.Fatalf("resources = %#v, want connector_one bound", result.Resources)
	}
	binding, ok := fields["server_groups[0].id"].(map[string]any)
	if !ok {
		t.Fatalf("bindings = %#v, want server_groups[0].id bound", fields)
	}
	want := `[data.terraform_remote_state.zpa_app.outputs.infrawright_reference_ids.zpa_server_group["known"], "sg-missing"]`
	if got := binding["expression"]; got != want {
		t.Errorf("expression = %q, want %q", got, want)
	}
	wantNotes := []string{
		`zpa_app_connector_group.connector_one.server_groups[0].id[1] value "sg-missing" skipped; id is absent from zpa_server_group lookup`,
		"zpa_app_connector_group: 1 bound, 1 skipped (id_absent=1)",
	}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestDeriveGeneratedBindingsTokenInSetBlock pins token resolution through
// the set-block complete-leaf path (bindSetBlockField / renderSetBlockMember):
// every path that reaches resolve() must accept a token exactly like
// TestDeriveGeneratedBindingsSetBlockBindsTheCompleteLeaf's raw-ID case.
func TestDeriveGeneratedBindingsTokenInSetBlock(t *testing.T) {
	items := map[string]map[string]any{
		"iot_device_services": {
			"name": "IoT Device Services",
			"services": []any{
				map[string]any{"id": []any{"zia_firewall_filtering_network_service.service_one", json.Number("456")}},
			},
		},
	}
	lookupKeys := map[string]map[string]string{
		"zia_firewall_filtering_network_service": {"123": "service_one"},
	}
	result, err := DeriveGeneratedBindings(
		setBlockBindingContext(map[string]int{"services.id": 0}),
		items, lookupKeys, "zia_firewall_filtering_network_service_groups",
	)
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings(set block token): %v", err)
	}
	fields, ok := result.Resources["zia_firewall_filtering_network_service_groups.iot_device_services"].(map[string]any)
	if !ok {
		t.Fatalf("resources = %#v, want iot_device_services bound", result.Resources)
	}
	binding, ok := fields["services"].(map[string]any)
	if !ok {
		t.Fatalf("bindings = %#v, want services bound", fields)
	}
	want := "[{ id = [" +
		`data.terraform_remote_state.zia_firewall_filtering_network_service.outputs.infrawright_reference_ids.zia_firewall_filtering_network_service["service_one"]` +
		", 456] }]"
	if got := binding["expression"]; got != want {
		t.Errorf("expression = %q, want %q", got, want)
	}
}
