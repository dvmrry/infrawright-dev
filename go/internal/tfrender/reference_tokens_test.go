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

// TestRenderTransformLookupEmitsIDByKey pins the producer-side book
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
// inverse from key_by_id so every committed book decodes both directions.
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
// through the book instead of rendering "<unknown>".
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

// TestRemoveLookupRefusedWhileTokensDependOnIt pins the book's
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
// JSON map key -- never a value -- must not block book retirement.
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
