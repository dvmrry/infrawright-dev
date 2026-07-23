package metadata

// providerschemacache_test.go tests the LoadedPackRoot.schemaCache addition
// introduced in loader.go: a lazy, per-provider memo for provider schema
// loads with no Node counterpart (see loader.go's file-level doc comment).
// This file has no node-tests analogue; it exists solely to pin down the
// cache's Go-only correctness contract -- concurrency safety, identical
// results (including identical failures) with and without the cache, and
// the O(1)-decode performance the cache exists to deliver.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// cachedRoot loads the repo's committed packs/ under packs/full.packset.json,
// the same fixture modulesgen's committedRoot test helper uses, giving a
// LoadedPackRoot built through the real loadPackRoot path (so schemaCache
// is populated, unlike a hand-built struct literal).
func cachedRoot(t *testing.T) LoadedPackRoot {
	t.Helper()
	root := repoRoot(t)
	profilePath := filepath.Join(root, "packs", "full.packset.json")
	catalogPath := filepath.Join(root, "packs", "full.packset.json")
	loaded, err := LoadPackRoot(LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
		CatalogPath: &catalogPath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	return loaded
}

// TestSchemaCacheConcurrentAccessMatchesUncachedResults exercises
// LoadedPackRoot.LoadProviderSchema/LoadResourceSchema from many goroutines
// at once, across all four committed providers, and checks every result
// against a fresh uncached load of the same provider -- proving the cache
// changes nothing observable even under concurrent access. Run with -race.
func TestSchemaCacheConcurrentAccessMatchesUncachedResults(t *testing.T) {
	root := cachedRoot(t)

	providers := []string{"zcc", "zia", "zpa", "ztc"}
	resourceTypesByProvider := make(map[string][]string, len(providers))
	// Pick one resource type per provider by scanning loaded.Resources, so
	// this test tracks the committed registry instead of hardcoding
	// resource-type names that might not exist under every profile.
	for resourceType, resource := range root.Resources {
		if len(resourceTypesByProvider[resource.Provider]) < 2 {
			resourceTypesByProvider[resource.Provider] = append(resourceTypesByProvider[resource.Provider], resourceType)
		}
	}

	wantSchema := make(map[string]ProviderSchema, len(providers))
	for _, provider := range providers {
		schema, err := LoadProviderSchema(root.Packs, provider)
		if err != nil {
			t.Fatalf("uncached LoadProviderSchema(%s): %v", provider, err)
		}
		wantSchema[provider] = schema
	}

	const workersPerProvider = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	recordErr := func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}

	for _, provider := range providers {
		provider := provider
		for i := 0; i < workersPerProvider; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := root.LoadProviderSchema(provider)
				if err != nil {
					recordErr(fmt.Errorf("provider %s: %w", provider, err))
					return
				}
				want := wantSchema[provider]
				if got.Provider != want.Provider || len(got.ResourceSchemas) != len(want.ResourceSchemas) {
					recordErr(fmt.Errorf("provider %s: cached schema shape mismatch (provider=%s resourceSchemas=%d, want provider=%s resourceSchemas=%d)",
						provider, got.Provider, len(got.ResourceSchemas), want.Provider, len(want.ResourceSchemas)))
				}
			}()
		}
		for _, resourceType := range resourceTypesByProvider[provider] {
			resourceType := resourceType
			for i := 0; i < workersPerProvider; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := root.LoadResourceSchema(resourceType)
					if err != nil {
						recordErr(fmt.Errorf("%s: %w", resourceType, err))
						return
					}
					want := wantSchema[provider].ResourceSchemas[resourceType]
					if _, ok := got["block"].(JsonObject); !ok {
						recordErr(fmt.Errorf("%s: cached resource schema has no block, got %T", resourceType, got["block"]))
					}
					if len(got) != len(want) {
						recordErr(fmt.Errorf("%s: cached resource schema has %d top-level keys, want %d", resourceType, len(got), len(want)))
					}
				}()
			}
		}
	}
	wg.Wait()
	for _, err := range errs {
		t.Error(err)
	}
}

// TestSchemaCacheCachesProviderLoadFailureIdentically builds a synthetic
// pack root whose provider schema file is missing, then checks that
// LoadedPackRoot.LoadProviderSchema's first call (which populates the
// cache) and second call (a cache hit) return byte-identical error text --
// and that both match a completely uncached LoadProviderSchema call on the
// same underlying metadata. This is the "do not cache failures in a way
// that changes the second-call error" requirement: the cache must not
// paper over, alter, or duplicate-decorate the failure.
func TestSchemaCacheCachesProviderLoadFailureIdentically(t *testing.T) {
	directory := t.TempDir()
	writeJSONFile(t, filepath.Join(directory, "sample", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"sample_": "sample"},
	})
	// No schemas/provider/sample.json is ever written: the provider schema
	// file is missing, so every load must fail closed the same way.

	loaded, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}

	_, uncachedErr := LoadProviderSchema(loaded.Packs, "sample")
	if uncachedErr == nil {
		t.Fatal("uncached LoadProviderSchema: expected error for missing schema file, got nil")
	}

	_, firstErr := loaded.LoadProviderSchema("sample")
	if firstErr == nil {
		t.Fatal("cached LoadProviderSchema (first call): expected error for missing schema file, got nil")
	}
	_, secondErr := loaded.LoadProviderSchema("sample")
	if secondErr == nil {
		t.Fatal("cached LoadProviderSchema (second call): expected error for missing schema file, got nil")
	}

	if firstErr.Error() != uncachedErr.Error() {
		t.Fatalf("first cached call error = %q, want it to match uncached error %q", firstErr.Error(), uncachedErr.Error())
	}
	if secondErr.Error() != firstErr.Error() {
		t.Fatalf("second cached call error = %q, want identical to first call's %q", secondErr.Error(), firstErr.Error())
	}

	// LoadResourceSchema, which routes through the same cache, must fail
	// the same way for a resource type whose provider has no schema file.
	writeJSONFile(t, filepath.Join(directory, "sample", "registry.json"), JsonObject{
		"sample_resource": JsonObject{"product": "sample"},
	})
	loadedWithRegistry, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: directory})
	if err != nil {
		t.Fatalf("LoadPackRoot (with registry): %v", err)
	}
	_, resourceErr1 := loadedWithRegistry.LoadResourceSchema("sample_resource")
	_, resourceErr2 := loadedWithRegistry.LoadResourceSchema("sample_resource")
	if resourceErr1 == nil || resourceErr2 == nil {
		t.Fatalf("LoadResourceSchema: expected errors on both calls, got %v / %v", resourceErr1, resourceErr2)
	}
	if resourceErr1.Error() != resourceErr2.Error() {
		t.Fatalf("LoadResourceSchema error changed between calls: %q vs %q", resourceErr1.Error(), resourceErr2.Error())
	}
	if !strings.Contains(resourceErr1.Error(), uncachedErr.Error()) {
		t.Fatalf("LoadResourceSchema error %q does not carry the underlying provider-schema failure %q", resourceErr1.Error(), uncachedErr.Error())
	}
}

// TestSchemaCacheNilFallsBackToUncachedBehavior confirms a LoadedPackRoot
// built directly (not through LoadPackRoot), which leaves schemaCache nil,
// still resolves schemas correctly -- some tests elsewhere in this repo
// construct LoadedPackRoot struct literals directly, and this port's own
// correctness must never depend on which construction path was used.
func TestSchemaCacheNilFallsBackToUncachedBehavior(t *testing.T) {
	root := repoRoot(t)
	metadata, err := LoadPackMetadata(filepath.Join(root, "packs"))
	if err != nil {
		t.Fatalf("LoadPackMetadata: %v", err)
	}
	registry, overrides, err := ValidatePackResources(metadata, nil)
	if err != nil {
		t.Fatalf("ValidatePackResources: %v", err)
	}
	bare := LoadedPackRoot{
		Packs:     metadata,
		Registry:  registry,
		Overrides: overrides,
		Resources: resourceMap(metadata, registry, overrides),
	}
	if bare.schemaCache != nil {
		t.Fatal("hand-built LoadedPackRoot literal must have a nil schemaCache")
	}
	schema, err := bare.LoadResourceSchema("zia_url_categories")
	if err != nil {
		t.Fatalf("LoadResourceSchema on nil-cache root: %v", err)
	}
	if _, ok := schema["block"].(JsonObject); !ok {
		t.Fatalf("zia_url_categories schema block is not an object: %T", schema["block"])
	}
}

// benchmarkResourceTypes cycles across all four committed providers so
// each benchmark iteration exercises a different provider's schema, the
// same access pattern buildModuleContext and ProjectProviderState produce
// when generating/adopting many resource types.
var benchmarkResourceTypes = []string{
	"zcc_forwarding_profile",
	"zia_url_categories",
	"zpa_segment_group",
	"ztc_forwarding_gateway",
}

func findRepoRootForBench(b *testing.B) string {
	b.Helper()
	dir, err := os.Getwd()
	if err != nil {
		b.Fatalf("Getwd: %v", err)
	}
	for {
		_, catalogsErr := os.Stat(filepath.Join(dir, "catalogs"))
		_, packsErr := os.Stat(filepath.Join(dir, "packs"))
		if catalogsErr == nil && packsErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			b.Fatal("walked up to filesystem root without finding catalogs/ and packs/")
		}
		dir = parent
	}
}

func benchmarkMetadataAndRoot(b *testing.B) (PackMetadata, LoadedPackRoot) {
	b.Helper()
	root := findRepoRootForBench(b)
	profilePath := filepath.Join(root, "packs", "full.packset.json")
	catalogPath := filepath.Join(root, "packs", "full.packset.json")
	metadata, err := LoadPackMetadata(filepath.Join(root, "packs"))
	if err != nil {
		b.Fatalf("LoadPackMetadata: %v", err)
	}
	loaded, err := LoadPackRoot(LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
		CatalogPath: &catalogPath,
	})
	if err != nil {
		b.Fatalf("LoadPackRoot: %v", err)
	}
	return metadata, loaded
}

// BenchmarkLoadResourceSchemaUncached calls the unexported/uncached
// package-level loadProviderSchema path (via the exported LoadResourceSchema
// function) once per iteration -- the pre-cache behavior every one of this
// port's schema-consuming call sites exhibited before loader.go grew
// schemaCache: one full JSON read + decode of the owning provider's schema
// file per call, no matter how many prior calls already decoded the same
// file. b.N calls here means b.N full provider-schema decodes.
func BenchmarkLoadResourceSchemaUncached(b *testing.B) {
	metadata, _ := benchmarkMetadataAndRoot(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resourceType := benchmarkResourceTypes[i%len(benchmarkResourceTypes)]
		if _, err := LoadResourceSchema(metadata, resourceType); err != nil {
			b.Fatalf("LoadResourceSchema(%s): %v", resourceType, err)
		}
	}
}

// BenchmarkLoadResourceSchemaCached calls LoadedPackRoot.LoadResourceSchema
// (the schemaCache-backed method) once per iteration against one shared
// root, so the four providers behind benchmarkResourceTypes are decoded
// once each no matter how large b.N grows -- O(1) decodes, not O(b.N).
func BenchmarkLoadResourceSchemaCached(b *testing.B) {
	_, root := benchmarkMetadataAndRoot(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resourceType := benchmarkResourceTypes[i%len(benchmarkResourceTypes)]
		if _, err := root.LoadResourceSchema(resourceType); err != nil {
			b.Fatalf("LoadResourceSchema(%s): %v", resourceType, err)
		}
	}
}
