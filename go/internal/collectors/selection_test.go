package collectors

// selection_test.go ports the "selectors use original active registry
// metadata and derived resources fetch their source" test from
// node-tests/rest-collector.test.ts, against the same committed
// packs/full.packset.json root the Node test loads.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func loadFullRoot(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	root := repoRoot(t)
	profilePath := filepath.Join(root, "packs", "full.packset.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
		CatalogPath: &profilePath,
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
