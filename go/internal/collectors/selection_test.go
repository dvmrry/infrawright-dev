package collectors

// selection_test.go ports the "selectors use original active registry
// metadata and derived resources fetch their source" test from
// the original test corpus, against the same committed
// packs/full.packset.json root the Node test loads.

import (
	"path/filepath"
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
		if hasFetchEntry(resource) {
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
