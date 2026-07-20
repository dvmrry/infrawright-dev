package metadata

// gate_test.go is THE Wave 2 gate: loadPackRoot over the committed packs/
// and packsets/full.json, then renderRootCatalog scoped to
// providers zcc,zia,zpa,ztc, must reproduce
// catalogs/zscaler-root-catalog.v1.json byte-for-byte. Ports the
// "validated Node metadata exactly regenerates the bundled root catalog"
// test from node-tests/root-catalog.test.ts.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot walks up from this test file's directory until it finds a
// directory containing both "catalogs" and "packs", per this port's gate
// spec (docs/go-runtime-plan.md).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		_, catalogsErr := os.Stat(filepath.Join(dir, "catalogs"))
		_, packsErr := os.Stat(filepath.Join(dir, "packs"))
		if catalogsErr == nil && packsErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up to filesystem root from %s without finding a directory containing both catalogs/ and packs/", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

func TestRootCatalogByteGate(t *testing.T) {
	root := repoRoot(t)
	packsRoot := filepath.Join(root, "packs")
	profilePath := filepath.Join(root, "packsets", "full.json")
	catalogPath := filepath.Join(root, "packsets", "full.json")

	loaded, err := LoadPackRoot(LoadPackRootOptions{
		PacksRoot:   packsRoot,
		ProfilePath: &profilePath,
		CatalogPath: &catalogPath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}

	rendered, err := RenderRootCatalog(loaded, []string{"zcc", "zia", "zpa", "ztc"})
	if err != nil {
		t.Fatalf("RenderRootCatalog: %v", err)
	}

	expected, err := os.ReadFile(filepath.Join(root, "catalogs", "zscaler-root-catalog.v1.json"))
	if err != nil {
		t.Fatalf("reading golden catalog: %v", err)
	}

	if rendered != string(expected) {
		reportCatalogMismatch(t, string(expected), rendered)
	}
}

func reportCatalogMismatch(t *testing.T, want, got string) {
	t.Helper()
	limit := len(want)
	if len(got) < limit {
		limit = len(got)
	}
	firstDiff := -1
	for i := 0; i < limit; i++ {
		if want[i] != got[i] {
			firstDiff = i
			break
		}
	}
	if firstDiff == -1 {
		firstDiff = limit
	}
	window := func(s string, at int) string {
		start := at - 40
		if start < 0 {
			start = 0
		}
		end := at + 40
		if end > len(s) {
			end = len(s)
		}
		return s[start:end]
	}
	t.Fatalf(
		"root catalog byte mismatch at byte %d (want len %d, got len %d)\nwant: ...%q...\ngot:  ...%q...",
		firstDiff, len(want), len(got), window(want, firstDiff), window(got, firstDiff),
	)
}
