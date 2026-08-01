package tfrender

// These tests pin DeriveGeneratedBindings's Task-2 behaviour: resolve()
// must consume the qualified reference tokens ("<referent>.<key>") that a
// later substitution pass (plan Task 1) will start committing, while
// still accepting today's raw-ID shape indefinitely (the migration path).
// See docs/superpowers/specs/2026-07-30-reference-tokens-design.md and
// docs/superpowers/plans/2026-07-30-reference-tokens-plan.md.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
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
	want := `data.terraform_remote_state.zpa_custom.outputs.iw_reference_ids.zpa_segment_group["segment_one"]`
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
	want := `data.terraform_remote_state.zpa_custom.outputs.iw_reference_ids.zpa_segment_group["segment_one"]`
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
	want := `[data.terraform_remote_state.zpa_app.outputs.iw_reference_ids.zpa_server_group["known"], "sg-missing"]`
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
		`data.terraform_remote_state.zia_firewall_filtering_network_service.outputs.iw_reference_ids.zia_firewall_filtering_network_service["service_one"]` +
		", 456] }]"
	if got := binding["expression"]; got != want {
		t.Errorf("expression = %q, want %q", got, want)
	}
}

// substitutionContext is tokenTopLevelContext extended with a second,
// dotted non-set reference so one context exercises the scalar, dotted and
// guard paths of the P1 substitution.
func substitutionContext() BindingContext {
	context := tokenTopLevelContext()
	context.Generated["zpa_server_group"] = true
	context.References["nested.server_group_id"] = TransformReferenceSpec{NameField: "name", Referent: "zpa_server_group"}
	context.ResourceRoots["zpa_server_group"] = "zpa_custom"
	return context
}

// TestSubstituteReferenceTokensScalarNumberAndDotted pins the P1 rewrite at
// its three basic shapes: a top-level scalar string ID, a json.Number ID
// (deliberately becoming a string token), and a dotted non-set path.
func TestSubstituteReferenceTokensScalarNumberAndDotted(t *testing.T) {
	items := map[string]map[string]any{
		"app_one": {
			"segment_group_id": "sg-1",
			"nested":           map[string]any{"server_group_id": json.Number("77")},
		},
		"app_two": {"segment_group_id": json.Number("42")},
	}
	lookupKeys := map[string]map[string]string{
		"zpa_segment_group": {"sg-1": "segment_one", "42": "segment_two"},
		"zpa_server_group":  {"77": "srv_one"},
	}
	substituteReferenceTokens(items, substitutionContext(), "zpa_application_segment", lookupKeys)
	if got := items["app_one"]["segment_group_id"]; got != "zpa_segment_group.segment_one" {
		t.Errorf("scalar = %#v, want token", got)
	}
	if got := items["app_two"]["segment_group_id"]; got != "zpa_segment_group.segment_two" {
		t.Errorf("number scalar = %#v, want string token", got)
	}
	nested := items["app_one"]["nested"].(map[string]any)
	if got := nested["server_group_id"]; got != "zpa_server_group.srv_one" {
		t.Errorf("dotted leaf = %#v, want token", got)
	}
}

// TestSubstituteReferenceTokensListMixing pins element-wise rewriting: known
// IDs become tokens in place while sentinels, unknown IDs and zero sentinels
// ride along untouched, in their original positions.
func TestSubstituteReferenceTokensListMixing(t *testing.T) {
	items := map[string]map[string]any{
		"app_one": {"segment_group_id": []any{"ANY", "sg-1", "sg-missing", json.Number("0")}},
	}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	substituteReferenceTokens(items, tokenTopLevelContext(), "zpa_application_segment", lookupKeys)
	got := items["app_one"]["segment_group_id"].([]any)
	want := []any{"ANY", "zpa_segment_group.segment_one", "sg-missing", json.Number("0")}
	if len(got) != len(want) {
		t.Fatalf("list = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("list[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestSubstituteReferenceTokensSetBlockMembers pins the set-nested shape:
// the same leaves renderSetBlockLeaf resolves must be rewritten inside each
// concrete member, through the list-of-members fan-out.
func TestSubstituteReferenceTokensSetBlockMembers(t *testing.T) {
	items := map[string]map[string]any{
		"iot_device_services": {
			"services": []any{
				map[string]any{"id": []any{json.Number("123"), json.Number("456")}},
			},
		},
	}
	lookupKeys := map[string]map[string]string{
		"zia_firewall_filtering_network_service": {"123": "service_one"},
	}
	substituteReferenceTokens(
		items,
		setBlockBindingContext(map[string]int{"services.id": 0}),
		"zia_firewall_filtering_network_service_groups",
		lookupKeys,
	)
	member := items["iot_device_services"]["services"].([]any)[0].(map[string]any)
	ids := member["id"].([]any)
	if ids[0] != "zia_firewall_filtering_network_service.service_one" {
		t.Errorf("ids[0] = %#v, want token", ids[0])
	}
	if ids[1] != json.Number("456") {
		t.Errorf("ids[1] = %#v, want untouched unknown id", ids[1])
	}
}

// TestSubstituteReferenceTokensGuards pins every field-level gate shared
// with DeriveGeneratedBindings: a missing lookup, a self-reference, disabled
// binding mode, and an unbindable (non-generated) referent must each leave
// the value untouched.
func TestSubstituteReferenceTokensGuards(t *testing.T) {
	makeItems := func() map[string]map[string]any {
		return map[string]map[string]any{"app_one": {"segment_group_id": "sg-1"}}
	}
	assertUntouched := func(t *testing.T, items map[string]map[string]any) {
		t.Helper()
		if got := items["app_one"]["segment_group_id"]; got != "sg-1" {
			t.Errorf("value = %#v, want untouched raw ID", got)
		}
	}
	t.Run("missing lookup", func(t *testing.T) {
		items := makeItems()
		substituteReferenceTokens(items, tokenTopLevelContext(), "zpa_application_segment",
			map[string]map[string]string{"zpa_segment_group": nil})
		assertUntouched(t, items)
	})
	t.Run("self reference", func(t *testing.T) {
		items := makeItems()
		substituteReferenceTokens(items, tokenTopLevelContext(), "zpa_segment_group",
			map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}})
		assertUntouched(t, items)
	})
	t.Run("disabled mode", func(t *testing.T) {
		items := makeItems()
		context := tokenTopLevelContext()
		context.Mode = deployment.ReferenceBindingDisabled
		substituteReferenceTokens(items, context, "zpa_application_segment",
			map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}})
		assertUntouched(t, items)
	})
	t.Run("unbindable referent", func(t *testing.T) {
		items := makeItems()
		context := tokenTopLevelContext()
		delete(context.Generated, "zpa_segment_group")
		substituteReferenceTokens(items, context, "zpa_application_segment",
			map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}})
		assertUntouched(t, items)
	})
}

// TestSubstituteReferenceTokensIdempotent pins re-run stability: a second
// pass over already-tokenised items changes nothing, so re-running transform
// or adopt over committed tokens can never flap the artifact.
func TestSubstituteReferenceTokensIdempotent(t *testing.T) {
	items := map[string]map[string]any{"app_one": {"segment_group_id": "sg-1"}}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	substituteReferenceTokens(items, tokenTopLevelContext(), "zpa_application_segment", lookupKeys)
	first := items["app_one"]["segment_group_id"]
	substituteReferenceTokens(items, tokenTopLevelContext(), "zpa_application_segment", lookupKeys)
	if got := items["app_one"]["segment_group_id"]; got != first {
		t.Errorf("second pass changed %#v to %#v", first, got)
	}
	if first != "zpa_segment_group.segment_one" {
		t.Errorf("token = %#v, want zpa_segment_group.segment_one", first)
	}
}

// TestSubstituteReferenceTokensUnsafeKeyLeavesID pins the interpolation
// guard at the substitution layer: a referent key carrying a template
// sequence must never be minted into a committed token; the raw ID stays
// and the derive layer keeps reporting it.
func TestSubstituteReferenceTokensUnsafeKeyLeavesID(t *testing.T) {
	items := map[string]map[string]any{"app_one": {"segment_group_id": "sg-1"}}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "seg${ment"}}
	substituteReferenceTokens(items, tokenTopLevelContext(), "zpa_application_segment", lookupKeys)
	if got := items["app_one"]["segment_group_id"]; got != "sg-1" {
		t.Errorf("value = %#v, want untouched raw ID for an unsafe key", got)
	}
}

// TestDeriveGeneratedBindingsTokenWithUnsafeKey pins the derive-layer
// interpolation guard for tokens (the carry-forward from Task 2's review):
// a token whose embedded key carries a template sequence is skipped under
// unsafe_key, exactly like an ID resolving to that key.
func TestDeriveGeneratedBindingsTokenWithUnsafeKey(t *testing.T) {
	items := map[string]map[string]any{"app_one": {"segment_group_id": "zpa_segment_group.seg${ment"}}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "seg${ment"}}
	result, err := DeriveGeneratedBindings(tokenTopLevelContext(), items, lookupKeys, "zpa_application_segment")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	if len(result.Resources) != 0 {
		t.Errorf("resources = %#v, want nothing bound for an unsafe key", result.Resources)
	}
	wantNotes := []string{
		`zpa_application_segment.app_one.segment_group_id value "zpa_segment_group.seg${ment" skipped; referent key contains a template interpolation`,
		"zpa_application_segment: 0 bound, 1 skipped (unsafe_key=1)",
	}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestRenderTransformLookupEmitsIDByKey pins the producer-side lookup
// extension the plan-time fallback expression indexes: the sidecar carries
// id_by_key alongside key_by_id, both derived from the same rows.
func TestRenderTransformLookupEmitsIDByKey(t *testing.T) {
	items := map[string]map[string]any{"segment_one": {"id": "sg-1", "name": "Segment One"}}
	text, err := RenderTransformLookup(items, map[string]map[string]any{}, "name")
	if err != nil {
		t.Fatalf("RenderTransformLookup: %v", err)
	}
	parsed, err := canonjson.ParseDataJSONLosslessly(text)
	if err != nil {
		t.Fatalf("parse lookup: %v", err)
	}
	data, err := ParseLookupSidecar(parsed)
	if err != nil {
		t.Fatalf("ParseLookupSidecar: %v", err)
	}
	if got := data.IDByKey["segment_one"]; got != "sg-1" {
		t.Errorf("id_by_key[segment_one] = %q, want sg-1", got)
	}
	if got := data.KeyByID["sg-1"]; got != "segment_one" {
		t.Errorf("key_by_id[sg-1] = %q, want segment_one", got)
	}
}

// TestParseLookupSidecarInvertsWhenIDByKeyAbsent pins migration coverage
// for sidecars written before id_by_key existed: the parser derives the
// inverse from key_by_id so every committed lookup decodes both directions.
func TestParseLookupSidecarInvertsWhenIDByKeyAbsent(t *testing.T) {
	data, err := ParseLookupSidecar(map[string]any{
		"by_id":     map[string]any{"sg-1": "Segment One"},
		"key_by_id": map[string]any{"sg-1": "segment_one"},
	})
	if err != nil {
		t.Fatalf("ParseLookupSidecar: %v", err)
	}
	if got := data.IDByKey["segment_one"]; got != "sg-1" {
		t.Errorf("inverted id_by_key[segment_one] = %q, want sg-1", got)
	}
}

// TestDeriveHclCommentsResolvesTokenDisplay pins the comment path over
// tokenised values (pulled forward from plan Task 4 to keep HCL trees
// readable the moment tokens land): a token resolves key -> id -> display
// through the lookup instead of rendering "<unknown>".
func TestDeriveHclCommentsResolvesTokenDisplay(t *testing.T) {
	items := map[string]map[string]any{
		"app_one": {"segment_group_id": "zpa_segment_group.segment_one"},
	}
	references := map[string]TransformReferenceSpec{
		"segment_group_id": {NameField: "name", Referent: "zpa_segment_group"},
	}
	overrides := map[string]*TransformLookupData{
		"zpa_segment_group": {
			ByID:    map[string]string{"sg-1": "Segment One"},
			KeyByID: map[string]string{"sg-1": "segment_one"},
			IDByKey: map[string]string{"segment_one": "sg-1"},
		},
	}
	comments, err := deriveHclComments("", items, references, overrides)
	if err != nil {
		t.Fatalf("deriveHclComments: %v", err)
	}
	key := HclTfvarsCommentKey("app_one", "segment_group_id", nil)
	if got := comments[key]; got != "Segment One" {
		t.Errorf("comment = %q, want Segment One", got)
	}
}

// TestResolveLookupPrefersCurrentPathOverLegacy pins Part B's dual-read
// preference order: when a lookup exists at both the current location
// (config/<tenant>/lookups/<type>.lookup.json) and the pre-migration legacy
// location (config/<tenant>/<type>.lookup.json), resolveLookup's on-disk
// callers -- lookupKeyMaps (via LookupKeyMaps) and deriveHclComments -- both
// serve the current copy.
func TestResolveLookupPrefersCurrentPathOverLegacy(t *testing.T) {
	configDirectory := t.TempDir()
	references := map[string]TransformReferenceSpec{
		"segment_group_id": {NameField: "name", Referent: "zpa_segment_group"},
	}
	writeFileMkdir(t, filepath.Join(configDirectory, "lookups", "zpa_segment_group.lookup.json"),
		`{"by_id":{"sg-1":"Current Segment"},"id_by_key":{"segment_one":"sg-1"},"key_by_id":{"sg-1":"segment_one"}}`)
	writeFileMkdir(t, filepath.Join(configDirectory, "zpa_segment_group.lookup.json"),
		`{"by_id":{"sg-1":"Legacy Segment"},"id_by_key":{"segment_one":"sg-1"},"key_by_id":{"sg-1":"legacy_key"}}`)

	keyMaps, err := LookupKeyMaps(configDirectory, references)
	if err != nil {
		t.Fatalf("LookupKeyMaps: %v", err)
	}
	if got := keyMaps["zpa_segment_group"]["sg-1"]; got != "segment_one" {
		t.Errorf("keyMaps[zpa_segment_group][sg-1] = %q, want %q (the current-path lookup)", got, "segment_one")
	}

	items := map[string]map[string]any{
		"app_one": {"segment_group_id": "zpa_segment_group.segment_one"},
	}
	comments, err := deriveHclComments(configDirectory, items, references, nil)
	if err != nil {
		t.Fatalf("deriveHclComments: %v", err)
	}
	key := HclTfvarsCommentKey("app_one", "segment_group_id", nil)
	if got := comments[key]; got != "Current Segment" {
		t.Errorf("comment = %q, want %q (the current-path lookup)", got, "Current Segment")
	}
}

// TestResolveLookupFallsBackToLegacyPath pins Part B's migration bridge: a
// lookup that has not yet been relocated to config/<tenant>/lookups/ -- it
// exists only at the pre-migration config/<tenant>/<type>.lookup.json path
// -- still resolves for both bindings derivation (lookupKeyMaps) and HCL
// tfvars comments (deriveHclComments). A tree mid-migration renders exactly
// as today.
func TestResolveLookupFallsBackToLegacyPath(t *testing.T) {
	configDirectory := t.TempDir()
	references := map[string]TransformReferenceSpec{
		"segment_group_id": {NameField: "name", Referent: "zpa_segment_group"},
	}
	writeFileMkdir(t, filepath.Join(configDirectory, "zpa_segment_group.lookup.json"),
		`{"by_id":{"sg-1":"Segment One"},"id_by_key":{"segment_one":"sg-1"},"key_by_id":{"sg-1":"segment_one"}}`)

	keyMaps, err := LookupKeyMaps(configDirectory, references)
	if err != nil {
		t.Fatalf("LookupKeyMaps: %v", err)
	}
	if got := keyMaps["zpa_segment_group"]["sg-1"]; got != "segment_one" {
		t.Errorf("keyMaps[zpa_segment_group][sg-1] = %q, want %q (fallback to the legacy-path lookup)", got, "segment_one")
	}

	items := map[string]map[string]any{
		"app_one": {"segment_group_id": "zpa_segment_group.segment_one"},
	}
	comments, err := deriveHclComments(configDirectory, items, references, nil)
	if err != nil {
		t.Fatalf("deriveHclComments: %v", err)
	}
	key := HclTfvarsCommentKey("app_one", "segment_group_id", nil)
	if got := comments[key]; got != "Segment One" {
		t.Errorf("comment = %q, want %q (fallback to the legacy-path lookup)", got, "Segment One")
	}
}

// TestRemoveLookupRefusedWhileTokensDependOnIt pins the lookup's
// load-bearing role: once committed configs reference a type by token, its
// lookup sidecar is the only decoder those tokens have, and inferred-
// lifecycle removal must refuse -- loudly, naming a dependent -- instead of
// stranding them.
func TestRemoveLookupRefusedWhileTokensDependOnIt(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_group")
	options.LookupNameField = nil
	options.RemoveLookupWhenAbsent = true
	paths := mustComputePaths(t, options)
	dependent := filepath.Join(filepath.Dir(paths.Config), "sample_referrer.auto.tfvars.json")
	writeFileMkdir(t, dependent, `{"items":{"one":{"group_id":"sample_group.some_key"}}}`)

	_, err := CompileTransformArtifacts(options)
	if err == nil || !strings.Contains(err.Error(), "sample_referrer.auto.tfvars.json") ||
		!strings.Contains(err.Error(), "token") {
		t.Fatalf("CompileTransformArtifacts error = %v, want a refusal naming the token-bearing dependent", err)
	}
}

// TestRemoveLookupIgnoresLookupsSubdirectory guards tokenDependents against
// the Part B lookup migration: its os.ReadDir scan of configDirectory is
// non-recursive and already skips every directory entry (entry.IsDir()), so
// the new lookups/ subdirectory sitting alongside the *.auto.tfvars.json
// files it scans must neither be descended into nor mistaken for a
// dependent config file.
func TestRemoveLookupIgnoresLookupsSubdirectory(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_group")
	options.LookupNameField = nil
	options.RemoveLookupWhenAbsent = true
	paths := mustComputePaths(t, options)
	configDirectory := filepath.Dir(paths.Config)
	// A lookups/ subdirectory entry, and -- to prove it is never descended
	// into -- a file inside it shaped so it WOULD read as a dependent if
	// tokenDependents recursed.
	writeFileMkdir(t, filepath.Join(configDirectory, "lookups", "sample_referrer.auto.tfvars.json"),
		`{"items":{"one":{"group_id":"sample_group.some_key"}}}`)

	if _, err := CompileTransformArtifacts(options); err != nil {
		t.Fatalf("CompileTransformArtifacts error = %v, want nil: a lookups/ subdirectory entry must not be treated as (or descended into for) a token dependent", err)
	}
}

// TestRemoveLookupProceedsWithoutTokenDependents pins the unchanged case:
// with no committed token referencing the type, inferred-lifecycle removal
// compiles exactly as before.
func TestRemoveLookupProceedsWithoutTokenDependents(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_group")
	options.LookupNameField = nil
	options.RemoveLookupWhenAbsent = true
	paths := mustComputePaths(t, options)
	dependent := filepath.Join(filepath.Dir(paths.Config), "sample_referrer.auto.tfvars.json")
	writeFileMkdir(t, dependent, `{"items":{"one":{"group_id":"raw-id-1"}}}`)

	if _, err := CompileTransformArtifacts(options); err != nil {
		t.Fatalf("CompileTransformArtifacts error = %v, want nil without token dependents", err)
	}
}

// TestSubstituteLeavesUnbindableListUntouched pins bail parity with
// bindValue (adversarial-review finding): a list holding any element the
// binder calls unbindable -- null here -- must not have its other elements
// tokenised, because derivation will refuse the whole list and the tokens
// would have no resolver.
func TestSubstituteLeavesUnbindableListUntouched(t *testing.T) {
	items := map[string]map[string]any{
		"app_one": {"segment_group_id": []any{"sg-1", nil}},
	}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	minted := substituteReferenceTokens(items, tokenTopLevelContext(), "zpa_application_segment", lookupKeys)
	got := items["app_one"]["segment_group_id"].([]any)
	if got[0] != "sg-1" {
		t.Errorf("list[0] = %#v, want untouched raw ID beside an unbindable element", got[0])
	}
	if len(minted) != 0 {
		t.Errorf("minted = %#v, want none for a list derivation refuses", minted)
	}
}

// TestMintedTokensRecordCoverablePaths pins the minted-token record the
// compile-level coverage assert consumes: candidate-style paths for plain,
// dotted, and set-nested shapes, each covered by its derived binding path.
func TestMintedTokensRecordCoverablePaths(t *testing.T) {
	items := map[string]map[string]any{
		"iot_device_services": {
			"services": []any{map[string]any{"id": []any{json.Number("123")}}},
		},
	}
	lookupKeys := map[string]map[string]string{
		"zia_firewall_filtering_network_service": {"123": "service_one"},
	}
	context := setBlockBindingContext(map[string]int{"services.id": 0})
	minted := substituteReferenceTokens(items, context, "zia_firewall_filtering_network_service_groups", lookupKeys)
	if len(minted) != 1 {
		t.Fatalf("minted = %#v, want exactly one set-nested token", minted)
	}
	if minted[0].Path != "services[0].id" {
		t.Errorf("minted path = %q, want services[0].id", minted[0].Path)
	}
	binding, err := DeriveGeneratedBindings(context, items, lookupKeys, "zia_firewall_filtering_network_service_groups")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	if err := assertMintedTokensCovered(minted, binding, "zia_firewall_filtering_network_service_groups"); err != nil {
		t.Errorf("assertMintedTokensCovered = %v, want covered", err)
	}
}

// TestAssertMintedTokensCoveredRefusesGaps pins the divergence backstop
// itself: a minted token with no covering binding fails the compile naming
// the leaf, never publishing an unresolvable token.
func TestAssertMintedTokensCoveredRefusesGaps(t *testing.T) {
	minted := []mintedReferenceToken{{ItemKey: "app_one", Path: "segment_group_id", Token: "zpa_segment_group.ghost"}}
	err := assertMintedTokensCovered(minted, GeneratedBindingsResult{Resources: map[string]any{}}, "zpa_application_segment")
	if err == nil || !strings.Contains(err.Error(), "segment_group_id") ||
		!strings.Contains(err.Error(), "traversal divergence") {
		t.Fatalf("assertMintedTokensCovered = %v, want a refusal naming the uncovered leaf", err)
	}
}

// TestRemoveLookupIgnoresDottedKeyNames pins the retirement guard's
// precision (adversarial-review finding): a dotted string appearing as a
// JSON map key -- never a value -- must not block lookup retirement.
func TestRemoveLookupIgnoresDottedKeyNames(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_group")
	options.LookupNameField = nil
	options.RemoveLookupWhenAbsent = true
	paths := mustComputePaths(t, options)
	dependent := filepath.Join(filepath.Dir(paths.Config), "sample_referrer.auto.tfvars.json")
	writeFileMkdir(t, dependent, `{"items":{"one":{"sample_group.k":"raw-id"}}}`)

	if _, err := CompileTransformArtifacts(options); err != nil {
		t.Fatalf("CompileTransformArtifacts error = %v, want nil for a dotted key name", err)
	}
}

// TestSetBlockTwoMembersBindOnceWithoutOverwrite pins, against the
// re-review's overwrite claim, what the walk actually does: the set-block
// leaf renders the WHOLE member list as one expression at one unique path
// per occurrence (arrays above the block fan with index bookkeeping), so
// two tokenised members can never collide in assign().
func TestSetBlockTwoMembersBindOnceWithoutOverwrite(t *testing.T) {
	items := map[string]map[string]any{
		"iot_device_services": {
			"services": []any{
				map[string]any{"id": []any{json.Number("123")}},
				map[string]any{"id": []any{json.Number("789")}},
			},
		},
	}
	lookupKeys := map[string]map[string]string{
		"zia_firewall_filtering_network_service": {"123": "service_one", "789": "service_two"},
	}
	context := setBlockBindingContext(map[string]int{"services.id": 0})
	minted := substituteReferenceTokens(items, context, "zia_firewall_filtering_network_service_groups", lookupKeys)
	if len(minted) != 2 {
		t.Fatalf("minted = %#v, want both members' tokens", minted)
	}
	binding, err := DeriveGeneratedBindings(context, items, lookupKeys, "zia_firewall_filtering_network_service_groups")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	fields := binding.Resources["zia_firewall_filtering_network_service_groups.iot_device_services"].(map[string]any)
	expression := fields["services"].(map[string]any)["expression"].(string)
	if !strings.Contains(expression, `["service_one"]`) || !strings.Contains(expression, `["service_two"]`) {
		t.Fatalf("expression = %q, want both members resolved in one block binding", expression)
	}
	if err := assertMintedTokensCovered(minted, binding, "zia_firewall_filtering_network_service_groups"); err != nil {
		t.Errorf("assertMintedTokensCovered = %v, want both covered", err)
	}
}

// TestHclDeploymentsMintNoTokens pins the JSON-only contract at the
// producer: an HCL-format deployment's config keeps literal IDs entirely.
func TestHclDeploymentsMintNoTokens(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_item")
	options.Deployment = testDeployment(workspace, true)
	options.References = map[string]TransformReferenceSpec{
		"group_id": {NameField: "name", Referent: "sample_group"},
	}
	options.BindingContext = BindingContext{
		Mode:      deployment.ReferenceBindingCrossState,
		Derived:   map[string]bool{},
		Generated: map[string]bool{"sample_group": true, "sample_item": true},
		ResourceRoots: map[string]string{
			"sample_group": "sample_group", "sample_item": "sample_item",
		},
		References: options.References,
	}
	options.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"item": {"group_id": "group-1", "name": "Item"}},
		Originals: map[string]map[string]any{"item": {"id": "item-1", "name": "Item"}},
	}
	options.LookupOverrides = map[string]*TransformLookupData{
		"sample_group": {ByID: map[string]string{"group-1": "Group"}, KeyByID: map[string]string{"group-1": "group_key"}},
	}
	compiled, err := CompileTransformArtifacts(options)
	if err != nil {
		t.Fatalf("CompileTransformArtifacts: %v", err)
	}
	if !strings.Contains(compiled.ConfigText, `"group-1"`) || strings.Contains(compiled.ConfigText, "sample_group.group_key") {
		t.Fatalf("config = %q, want the literal ID retained for an HCL deployment", compiled.ConfigText)
	}
}

// TestDeriveRefusesTwoReferenceFieldsInOneSetBlock pins the re-review's
// set-block finding in its ACTUAL shape -- two different reference fields
// crossing the same set block -- which my earlier two-members-one-field
// test did not cover: each field's pass would bind the complete block at
// the same (item, path) key, the second overwriting the first and
// reproducing its references literally. The shape is refused up front.
func TestDeriveRefusesTwoReferenceFieldsInOneSetBlock(t *testing.T) {
	context := BindingContext{
		Derived: map[string]bool{},
		Generated: map[string]bool{
			"sample_groups": true, "referent_a": true, "referent_b": true,
		},
		Mode: deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"services.a_id": {NameField: "name", Referent: "referent_a"},
			"services.b_id": {NameField: "name", Referent: "referent_b"},
		},
		SetBlockFields: map[string]int{"services.a_id": 0, "services.b_id": 0},
		ResourceRoots: map[string]string{
			"sample_groups": "sample_groups", "referent_a": "referent_a", "referent_b": "referent_b",
		},
	}
	items := map[string]map[string]any{
		"one": {"services": []any{map[string]any{"a_id": "A", "b_id": "B"}}},
	}
	lookupKeys := map[string]map[string]string{
		"referent_a": {"A": "a"}, "referent_b": {"B": "b"},
	}
	_, err := DeriveGeneratedBindings(context, items, lookupKeys, "sample_groups")
	if err == nil || !strings.Contains(err.Error(), "services.a_id") ||
		!strings.Contains(err.Error(), "services.b_id") {
		t.Fatalf("DeriveGeneratedBindings error = %v, want the two-fields-one-block refusal", err)
	}
}

// TestDeriveGeneratedBindingsTokensOnlySkipsRawIDs pins the render-derivation
// option added for round-4 finding 2. Raw-ID derivation is transform-only: at
// render time the deriver must skip a raw ID WITHOUT consulting the lookup, so
// the emitted expression cannot vary with lookup contents no transform saw. The
// tokenised sibling in the same items map still binds, which is what makes the
// per-type derivation trigger safe over a mixed config.
func TestDeriveGeneratedBindingsTokensOnlySkipsRawIDs(t *testing.T) {
	context := tokenTopLevelContext()
	context.TokensOnly = true
	items := map[string]map[string]any{
		"app_one": {"segment_group_id": "zpa_segment_group.segment_one"},
		"app_two": {"segment_group_id": "sg-1"},
	}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	result, err := DeriveGeneratedBindings(context, items, lookupKeys, "zpa_application_segment")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	if _, bound := result.Resources["zpa_application_segment.app_two"]; bound {
		t.Errorf("resources = %#v, want the raw-ID item unbound at render", result.Resources)
	}
	fields, ok := result.Resources["zpa_application_segment.app_one"].(map[string]any)
	if !ok {
		t.Fatalf("resources = %#v, want the tokenised item bound", result.Resources)
	}
	binding := fields["segment_group_id"].(map[string]any)
	want := `data.terraform_remote_state.zpa_custom.outputs.iw_reference_ids.zpa_segment_group["segment_one"]`
	if got := binding["expression"]; got != want {
		t.Errorf("expression = %q, want %q", got, want)
	}
	wantNotes := []string{
		`zpa_application_segment.app_two.segment_group_id value "sg-1" skipped; raw ids bind at transform, not render`,
		"zpa_application_segment: 1 bound, 1 skipped (raw_id_render_only=1)",
	}
	if !stringSlicesEqual(result.Notes, wantNotes) {
		t.Fatalf("notes = %v, want %v", result.Notes, wantNotes)
	}
}

// TestDeriveGeneratedBindingsTokensOnlyDefaultsOff pins that the transform
// path is untouched: with the option unset, the identical inputs bind the raw
// ID exactly as they always have.
func TestDeriveGeneratedBindingsTokensOnlyDefaultsOff(t *testing.T) {
	items := map[string]map[string]any{"app_two": {"segment_group_id": "sg-1"}}
	lookupKeys := map[string]map[string]string{"zpa_segment_group": {"sg-1": "segment_one"}}
	result, err := DeriveGeneratedBindings(tokenTopLevelContext(), items, lookupKeys, "zpa_application_segment")
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	if _, bound := result.Resources["zpa_application_segment.app_two"]; !bound {
		t.Errorf("resources = %#v, want the raw ID bound at transform", result.Resources)
	}
}

// The tests below pin the round-5 re-review's still-broken blocker. gen-env's
// dropped-edge orphan scan recognises a committed token by lookup membership,
// which is only sound if a key cannot leave a lookup while committed tokens
// still name it. tokenDependents guarded whole-lookup REMOVAL; nothing guarded
// key-set SHRINKAGE, which an ordinary referent re-transform (item renamed or
// deleted) performs. These make the invariant enforced rather than assumed.

// committedBookAtCurrentPath writes a lookup for resourceType at the current
// lookups/ location with the given key -> id rows.
func committedBookAtCurrentPath(t *testing.T, options TransformArtifactCompileOptions, rows map[string]string) {
	t.Helper()
	paths := mustComputePaths(t, options)
	byID := map[string]any{}
	idByKey := map[string]any{}
	keyByID := map[string]any{}
	for key, id := range rows {
		byID[id] = key
		idByKey[key] = id
		keyByID[id] = key
	}
	encoded, err := json.Marshal(map[string]any{"by_id": byID, "id_by_key": idByKey, "key_by_id": keyByID})
	if err != nil {
		t.Fatalf("marshal lookup: %v", err)
	}
	writeFileMkdir(t, paths.Lookup, string(encoded))
}

// shrinkingBookOptions is a sample_group compile whose fresh lookup decodes
// only "example": the committed lookup on disk additionally carries "retired",
// so compiling drops that key.
func shrinkingBookOptions(t *testing.T, workspace string) TransformArtifactCompileOptions {
	t.Helper()
	options := newArtifactOptions(workspace, "sample_group")
	committedBookAtCurrentPath(t, options, map[string]string{"example": "id-1", "retired": "id-2"})
	return options
}

// TestLookupKeyShrinkageWithTokenDependentRefused is the blocker's regression: a
// key still named by a committed token must not be allowed to leave the lookup,
// or the token becomes undecodable and every render-time gate that keys on
// lookup membership goes blind to it.
func TestLookupKeyShrinkageWithTokenDependentRefused(t *testing.T) {
	workspace := t.TempDir()
	options := shrinkingBookOptions(t, workspace)
	paths := mustComputePaths(t, options)
	writeFileMkdir(t, filepath.Join(filepath.Dir(paths.Config), "sample_referrer.auto.tfvars.json"),
		`{"items":{"one":{"group_id":"sample_group.retired"}}}`)

	_, err := CompileTransformArtifacts(options)
	if err == nil {
		t.Fatalf("CompileTransformArtifacts error = nil, want a refusal for a key leaving the lookup while a token names it")
	}
	for _, want := range []string{
		"sample_group.retired", "sample_referrer.auto.tfvars.json",
		"nothing published", "will NOT help", "by hand",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("CompileTransformArtifacts error = %q, want it to name %q", err, want)
		}
	}
}

// TestLookupKeyShrinkageWithoutDependentsProceeds keeps the guard honest: a key
// no committed token names may leave the lookup freely. Without this, a guard
// that refused every shrinkage would satisfy the test above.
func TestLookupKeyShrinkageWithoutDependentsProceeds(t *testing.T) {
	workspace := t.TempDir()
	options := shrinkingBookOptions(t, workspace)
	paths := mustComputePaths(t, options)
	// References a key that SURVIVES, so the dropped key has no dependent.
	writeFileMkdir(t, filepath.Join(filepath.Dir(paths.Config), "sample_referrer.auto.tfvars.json"),
		`{"items":{"one":{"group_id":"sample_group.example"}}}`)

	if _, err := CompileTransformArtifacts(options); err != nil {
		t.Fatalf("CompileTransformArtifacts error = %v, want nil when the dropped key has no dependent", err)
	}
}

// TestLookupKeyShrinkageDetectsLegacyPathDependents pins the migration bridge on
// the guard's INPUT side: a tenant that has not re-transformed since the lookup
// moved has its committed lookup only at config/<tenant>/<type>.lookup.json.
// Reading only the current path would see no committed lookup at all, compute no
// dropped keys, and let the shrinkage through.
func TestLookupKeyShrinkageDetectsLegacyPathDependents(t *testing.T) {
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, "sample_group")
	paths := mustComputePaths(t, options)
	writeFileMkdir(t, paths.LegacyLookup,
		`{"by_id":{"id-1":"example","id-2":"retired"},"id_by_key":{"example":"id-1","retired":"id-2"},"key_by_id":{"id-1":"example","id-2":"retired"}}`)
	writeFileMkdir(t, filepath.Join(filepath.Dir(paths.Config), "sample_referrer.auto.tfvars.json"),
		`{"items":{"one":{"group_id":"sample_group.retired"}}}`)

	_, err := CompileTransformArtifacts(options)
	if err == nil || !strings.Contains(err.Error(), "sample_group.retired") {
		t.Fatalf("CompileTransformArtifacts error = %v, want the shrinkage refusal against the legacy-path lookup", err)
	}
}

// TestLookupKeyShrinkageScansHclDependents pins that the dependent scan serves
// both committed tfvars formats. An HCL config cannot be parsed here, so the
// scan is textual -- but it must still find the token.
func TestLookupKeyShrinkageScansHclDependents(t *testing.T) {
	workspace := t.TempDir()
	options := shrinkingBookOptions(t, workspace)
	paths := mustComputePaths(t, options)
	writeFileMkdir(t, filepath.Join(filepath.Dir(paths.Config), "sample_referrer.auto.tfvars"),
		"items = {\n  one = {\n    group_id = \"sample_group.retired\"\n  }\n}\n")

	_, err := CompileTransformArtifacts(options)
	if err == nil || !strings.Contains(err.Error(), "sample_referrer.auto.tfvars") {
		t.Fatalf("CompileTransformArtifacts error = %v, want the HCL dependent named", err)
	}
}

// The three tests below pin the round-4 re-review's own-config hole. The guard
// skipped the compiling type's own config on the argument that a type never
// mints a token naming itself. That argument covers MINTING a declared
// self-reference; it says nothing about a string value that merely looks like
// one, and envgen's token contract is lookup membership over EVERY string leaf
// with no own-config exception. A self-prefixed value in the referent's own
// config is a token claim to gen-env, so a lookup update that stops decoding it
// strands it exactly like any other.
//
// The check is two-sided, and each side guards a different failure:
//
//   - the committed copy on disk guards the publication window. The single
//     publisher writes the lookup BEFORE the config, so a crash between the two
//     leaves the old config beside the new lookup.
//   - the freshly compiled ConfigText guards the steady state after a
//     successful publish: a value this compile is about to commit that the new
//     lookup will not decode.
//
// Neither subsumes the other, so both are checked.

// ownConfigStrandingOptions is a sample_group compile whose fresh lookup drops
// "retired" (as shrinkingBookOptions) and whose own committed and/or projected
// config carries a self-prefixed value naming that key, per the caller.
func ownConfigStrandingOptions(t *testing.T, workspace string, committed, projected bool) TransformArtifactCompileOptions {
	t.Helper()
	options := shrinkingBookOptions(t, workspace)
	paths := mustComputePaths(t, options)
	description := "an ordinary description"
	if committed {
		description = "sample_group.retired"
	}
	writeFileMkdir(t, paths.Config, `{"items":{"example":{"description":`+jsonQuote(description)+`,"name":"Example"}}}`)
	item := map[string]any{"name": "Example"}
	if projected {
		item["description"] = "sample_group.retired"
	} else {
		item["description"] = "an ordinary description"
	}
	options.Result = PullTransformResult{
		Drops:     []string{},
		Items:     map[string]map[string]any{"example": item},
		Originals: map[string]map[string]any{"example": {"id": "id-1", "name": "Example"}},
	}
	return options
}

// TestLookupKeyShrinkageRefusesOwnConfigStranding is the re-review's four-step
// repro verbatim: lookup carries example and retired, the type's own committed
// config carries a string leaf naming sample_group.retired, and the fresh
// compile both drops the key from the lookup and preserves that value.
func TestLookupKeyShrinkageRefusesOwnConfigStranding(t *testing.T) {
	workspace := t.TempDir()
	options := ownConfigStrandingOptions(t, workspace, true, true)

	_, err := CompileTransformArtifacts(options)
	if err == nil {
		t.Fatalf("CompileTransformArtifacts error = nil, want a refusal: the type's own config strands the dropped key too")
	}
	if !strings.Contains(err.Error(), "sample_group.retired") ||
		!strings.Contains(err.Error(), "sample_group.auto.tfvars.json") {
		t.Errorf("CompileTransformArtifacts error = %q, want it to name the token and this type's own config", err)
	}
}

// TestLookupKeyShrinkageRefusesOwnCommittedConfigAlone isolates the disk side.
// The projected config legitimately drops the value, so after a SUCCESSFUL
// publish the tree would be consistent -- but the lookup is written before the
// config, so a failure between the two leaves the committed value beside a
// lookup that no longer decodes it.
func TestLookupKeyShrinkageRefusesOwnCommittedConfigAlone(t *testing.T) {
	workspace := t.TempDir()
	options := ownConfigStrandingOptions(t, workspace, true, false)

	_, err := CompileTransformArtifacts(options)
	if err == nil {
		t.Fatalf("CompileTransformArtifacts error = nil, want a refusal: publication is not atomic across the lookup and the config")
	}
	if !strings.Contains(err.Error(), "sample_group.retired") {
		t.Errorf("CompileTransformArtifacts error = %q, want it to name the token", err)
	}
}

// TestLookupKeyShrinkageRefusesOwnPendingConfigAlone isolates the fresh-text
// side: nothing on disk names the key, so a disk-only scan sees a clean tree,
// yet this very compile is about to commit a value the new lookup will not
// decode.
func TestLookupKeyShrinkageRefusesOwnPendingConfigAlone(t *testing.T) {
	workspace := t.TempDir()
	options := ownConfigStrandingOptions(t, workspace, false, true)

	_, err := CompileTransformArtifacts(options)
	if err == nil {
		t.Fatalf("CompileTransformArtifacts error = nil, want a refusal: this compile itself commits the stranded value")
	}
	if !strings.Contains(err.Error(), "sample_group.retired") {
		t.Errorf("CompileTransformArtifacts error = %q, want it to name the token", err)
	}
}

// TestLookupKeyShrinkageAllowsCleanOwnConfig keeps the two-sided check honest: a
// type whose own config names the departing key on neither side publishes the
// shrinkage exactly as before.
func TestLookupKeyShrinkageAllowsCleanOwnConfig(t *testing.T) {
	workspace := t.TempDir()
	options := ownConfigStrandingOptions(t, workspace, false, false)

	if _, err := CompileTransformArtifacts(options); err != nil {
		t.Fatalf("CompileTransformArtifacts error = %v, want nil when neither the committed nor the pending own config names the key", err)
	}
}
