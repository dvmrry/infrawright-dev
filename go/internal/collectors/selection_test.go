package collectors

// selection_test.go ports the "selectors use original active registry
// metadata and derived resources fetch their source" test from
// the original test corpus, against the same committed
// packs/full.packset.json root the Node test loads.

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func loadFullRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	root := repoRoot(t)
	profilePath := filepath.Join(root, "packs", "full.packset.json")
	profile, err := metadata.LoadPackSetDocument(profilePath, metadata.PackSetKind)
	if err != nil {
		t.Fatalf("LoadPackSetDocument(%q): %v", profilePath, err)
	}
	requireCollectorPackSelection(t, profile.PackSelection)
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	return loaded
}

func TestSelectFetchResourcesAgainstCommittedRoot(t *testing.T) {
	packRoot := loadFullRoot(t)

	all, err := SelectFetchResources(SelectFetchResourcesOptions{Root: packRoot})
	if err != nil {
		t.Fatalf("SelectFetchResources(no selectors): %v", err)
	}
	if len(all) != 92 {
		t.Errorf("len(all) = %d, want 92", len(all))
	}

	wantCounts := map[string]int{"zia": 56, "zpa": 16, "zcc": 5, "ztc": 15}
	for product, want := range wantCounts {
		got, err := SelectFetchResources(SelectFetchResourcesOptions{Root: packRoot, Selectors: []string{product}})
		if err != nil {
			t.Fatalf("SelectFetchResources(%q): %v", product, err)
		}
		if len(got) != want {
			t.Errorf("len(SelectFetchResources(%q)) = %d, want %d", product, len(got), want)
		}
	}

	derived, err := SelectFetchResources(SelectFetchResourcesOptions{
		Root: packRoot, Selectors: []string{"zpa_policy_access_rule_reorder"},
	})
	if err != nil {
		t.Fatalf("SelectFetchResources(derived): %v", err)
	}
	if len(derived) != 1 || derived[0] != "zpa_policy_access_rule" {
		t.Errorf("SelectFetchResources(derived) = %v, want [zpa_policy_access_rule]", derived)
	}

	zcc, err := SelectFetchResources(SelectFetchResourcesOptions{Root: packRoot, Selectors: []string{"zcc"}})
	if err != nil {
		t.Fatalf("SelectFetchResources(zcc): %v", err)
	}
	wantZcc := []string{
		"zcc_device_cleanup", "zcc_failopen_policy", "zcc_forwarding_profile",
		"zcc_trusted_network", "zcc_web_privacy",
	}
	if !equalStrings(zcc, wantZcc) {
		t.Errorf("SelectFetchResources(zcc) = %v, want %v", zcc, wantZcc)
	}

	_, err = SelectFetchResources(SelectFetchResourcesOptions{Root: packRoot, Selectors: []string{"unknown"}})
	if err == nil || !strings.Contains(err.Error(), "valid products: zcc, zia, zpa, ztc") {
		t.Errorf("SelectFetchResources(unknown) error = %v, want a valid-products message", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func skipDeclarationRoot() metadata.LoadedPackRoot {
	return metadata.LoadedPackRoot{Resources: map[string]metadata.LoadedResourceMetadata{
		"zia_url_categories": {Type: "zia_url_categories", Product: "zia",
			Registry: metadata.JsonObject{
				"product": "zia", "fetch": map[string]any{"path": "/urlCategories"},
			}},
		"zia_gen_only": {Type: "zia_gen_only", Product: "zia",
			Registry: metadata.JsonObject{
				"product": "zia", "generate": true,
				"fetch":             false,
				"fetch_skip_reason": "generate-only; no list endpoint",
			}},
		"zia_silent_gap": {Type: "zia_silent_gap", Product: "zia",
			Registry: metadata.JsonObject{"product": "zia", "generate": true}},
		// A reason with no "fetch": false is not a declaration. Metadata
		// validation refuses the pair, but this predicate must not depend on
		// validation having run to reach it.
		"zia_stale_reason": {Type: "zia_stale_reason", Product: "zia",
			Registry: metadata.JsonObject{
				"product": "zia", "generate": true,
				"fetch_skip_reason": "left behind when the fetch block returned",
			}},
		// A working fetch block wins; a stray reason beside it changes nothing.
		"zia_fetch_and_reason": {Type: "zia_fetch_and_reason", Product: "zia",
			Registry: metadata.JsonObject{
				"product": "zia", "fetch": map[string]any{"path": "/both"},
				"fetch_skip_reason": "stale",
			}},
	}}
}

// TestSelectFetchHonoursADeclaredSkip pins that a type the registry declares
// unfetchable on purpose is skipped with its reason rather than refused as
// unknown. Selection previously routed it to the unknown-selector error, so
// the registry could state "fetch": false with a reason and the fetcher still
// answered "unknown resource type" -- untrue, and it discarded the reason.
func TestSelectFetchHonoursADeclaredSkip(t *testing.T) {
	selected, skipped, err := SelectFetchResourcesWithSkips(SelectFetchResourcesOptions{
		Root:      skipDeclarationRoot(),
		Selectors: []string{"zia_url_categories", "zia_gen_only"},
	})
	if err != nil {
		t.Fatalf("SelectFetchResourcesWithSkips(declared skip) error = %v, want nil", err)
	}
	if !reflect.DeepEqual(selected, []string{"zia_url_categories"}) {
		t.Errorf("selected = %#v, want only the fetchable type", selected)
	}
	want := []SkippedFetchSelector{
		{Type: "zia_gen_only", Reason: "generate-only; no list endpoint"},
	}
	if !reflect.DeepEqual(skipped, want) {
		t.Errorf("skipped = %#v, want %#v", skipped, want)
	}
}

// A missing fetch block is the silent gap check-config exists to refuse. It
// must keep reaching the unknown-selector error rather than being quietly
// skipped here, or this change would turn the gap into a supported state.
func TestSelectFetchStillRefusesAnUndeclaredGap(t *testing.T) {
	tests := []struct {
		name     string
		selector string
	}{
		{"no_fetch_block_at_all", "zia_silent_gap"},
		{"reason_without_the_skip", "zia_stale_reason"},
		{"not_in_the_registry", "zia_invented"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := SelectFetchResourcesWithSkips(SelectFetchResourcesOptions{
				Root: skipDeclarationRoot(), Selectors: []string{test.selector},
			})
			if err == nil {
				t.Fatalf("SelectFetchResourcesWithSkips(%s) error = nil, want a refusal", test.name)
			}
			if !strings.Contains(err.Error(), "unknown resource type") {
				t.Errorf("error = %v, want the unknown-selector refusal", err)
			}
		})
	}
}

// A skip declaration with no reason is not a declaration. Accepting it would
// let the reason-less form -- which metadata validation already refuses -- act
// as a silent opt-out through this path instead.
func TestSelectFetchRequiresAReasonToSkip(t *testing.T) {
	root := metadata.LoadedPackRoot{Resources: map[string]metadata.LoadedResourceMetadata{
		"zia_no_reason": {Type: "zia_no_reason", Product: "zia",
			Registry: metadata.JsonObject{"product": "zia", "fetch": false}},
	}}
	if _, _, err := SelectFetchResourcesWithSkips(SelectFetchResourcesOptions{
		Root: root, Selectors: []string{"zia_no_reason"},
	}); err == nil {
		t.Error("SelectFetchResourcesWithSkips(skip without a reason) error = nil, want a refusal")
	}
}

// A type with a real fetch block is fetched, not skipped, whatever else its
// entry carries.
func TestSelectFetchPrefersARealFetchBlockOverAStrayReason(t *testing.T) {
	selected, skipped, err := SelectFetchResourcesWithSkips(SelectFetchResourcesOptions{
		Root: skipDeclarationRoot(), Selectors: []string{"zia_fetch_and_reason"},
	})
	if err != nil {
		t.Fatalf("SelectFetchResourcesWithSkips() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(selected, []string{"zia_fetch_and_reason"}) {
		t.Errorf("selected = %#v, want the type fetched", selected)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %#v, want none", skipped)
	}
}

// The narrow wrapper keeps its signature for callers that do not report skips.
func TestSelectFetchResourcesWrapperDropsOnlyTheSkipList(t *testing.T) {
	selected, err := SelectFetchResources(SelectFetchResourcesOptions{
		Root:      skipDeclarationRoot(),
		Selectors: []string{"zia_url_categories", "zia_gen_only"},
	})
	if err != nil {
		t.Fatalf("SelectFetchResources(declared skip) error = %v, want nil", err)
	}
	if !reflect.DeepEqual(selected, []string{"zia_url_categories"}) {
		t.Errorf("selected = %#v, want only the fetchable type", selected)
	}
}
