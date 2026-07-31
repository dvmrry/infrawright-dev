package collectors

// These tests exercise selector behavior against the committed pack root.

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
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

// testExpectsFetchable is deliberately a second implementation rather than a
// call to hasFetchEntry. Deriving the expectation from the same predicate
// production uses makes the oracle circular: a mutation widening
// hasFetchEntry moves both sides together and the comparison still passes,
// however wrong selection has become. Restating the rule against the raw
// registry is what makes the agreement assertion mean something.
func testExpectsFetchable(resource metadata.LoadedResourceMetadata) bool {
	_, isFetchBlock := resource.Registry["fetch"].(map[string]any)
	return isFetchBlock
}

// TestFetchPredicateAgainstAHandWrittenOracle pins the predicate itself
// against cases whose answers are written out, not computed. The committed
// root exercises the predicate over real data but cannot pin what it should
// say: every entry there is one the predicate already classifies.
func TestFetchPredicateAgainstAHandWrittenOracle(t *testing.T) {
	tests := []struct {
		name     string
		registry metadata.JsonObject
		want     bool
	}{
		{"fetch_block", metadata.JsonObject{"fetch": map[string]any{"path": "/x"}}, true},
		{"empty_fetch_block", metadata.JsonObject{"fetch": map[string]any{}}, true},
		{"no_fetch_key", metadata.JsonObject{"generate": true}, false},
		{"fetch_false", metadata.JsonObject{"fetch": false}, false},
		// "fetch": true is refused by metadata validation because it does not
		// describe how to fetch; the predicate must not treat it as one.
		{"fetch_true", metadata.JsonObject{"fetch": true}, false},
		{"fetch_string", metadata.JsonObject{"fetch": "/x"}, false},
		{"fetch_array", metadata.JsonObject{"fetch": []any{"/x"}}, false},
		{"fetch_null", metadata.JsonObject{"fetch": nil}, false},
		{"derive_only", metadata.JsonObject{"derive": map[string]any{"from": "other"}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resource := metadata.LoadedResourceMetadata{
				Type: "sample_resource", Product: "zia", Registry: test.registry,
			}
			if got := hasFetchEntry(resource); got != test.want {
				t.Errorf("hasFetchEntry(%s) = %v, want %v", test.name, got, test.want)
			}
			// The test-side oracle has to agree, or the derived expectation
			// in the committed-root test is measuring a different rule.
			if got := testExpectsFetchable(resource); got != test.want {
				t.Errorf("testExpectsFetchable(%s) = %v, want %v", test.name, got, test.want)
			}
		})
	}
}

func TestSelectFetchResourcesAgainstCommittedRoot(t *testing.T) {
	packRoot := loadFullRoot(t)

	all, err := SelectFetchResources(SelectFetchResourcesOptions{Root: packRoot})
	if err != nil {
		t.Fatalf("SelectFetchResources(no selectors): %v", err)
	}
	// The expectation is derived from the loaded registry rather than written
	// as a literal. loadFullRoot reads whatever packs/ the consuming repo
	// has, and packs are the downstream-owned layer, so asserting an exact
	// cardinality here would make this engine test a constraint on what
	// consumers may put in their own packs: adding a legitimate fetch block
	// downstream would fail a test that repo neither owns nor can fix without
	// diverging a vendored file. The property worth pinning is that selection
	// agrees with the registry, which holds for any pack tree.
	expected := map[string][]string{}
	for resourceType, resource := range packRoot.Resources {
		if testExpectsFetchable(resource) {
			expected[resource.Product] = append(expected[resource.Product], resourceType)
		}
	}
	total := 0
	for product := range expected {
		expected[product] = canonjson.SortedStrings(expected[product])
		total += len(expected[product])
	}
	if len(all) != total {
		t.Errorf("len(all) = %d, want %d fetch-bearing registry entries", len(all), total)
	}

	// A floor, not an equality: it still catches fetch blocks disappearing
	// from upstream's own packs, and a consuming tree only ever adds to them.
	const upstreamFetchResourceFloor = 92
	if len(all) < upstreamFetchResourceFloor {
		t.Errorf("len(all) = %d, want at least %d", len(all), upstreamFetchResourceFloor)
	}

	for product, want := range expected {
		got, err := SelectFetchResources(SelectFetchResourcesOptions{Root: packRoot, Selectors: []string{product}})
		if err != nil {
			t.Fatalf("SelectFetchResources(%q): %v", product, err)
		}
		if !equalStrings(got, want) {
			t.Errorf("SelectFetchResources(%q) = %v, want %v", product, got, want)
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

	// Product selection is already compared against the derived expectation
	// above, for every product the loaded tree carries. A second literal list
	// here would reintroduce the same coupling for one product.

	// The valid-product list is likewise derived: a consuming tree with an
	// extra product would otherwise fail this on a message that correctly
	// describes its own packs.
	_, err = SelectFetchResources(SelectFetchResourcesOptions{Root: packRoot, Selectors: []string{"unknown"}})
	wantProducts := "valid products: " + strings.Join(FetchProducts(packRoot), ", ")
	if err == nil || !strings.Contains(err.Error(), wantProducts) {
		t.Errorf("SelectFetchResources(unknown) error = %v, want it to contain %q", err, wantProducts)
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
