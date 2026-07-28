package collectors

// authority_test.go ports the original test corpus.

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func loadRootFromPacksDir(t *testing.T, packsRoot string) metadata.LoadedPackRoot {
	t.Helper()
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot})
	if err != nil {
		t.Fatalf("LoadPackRoot(%s): %v", packsRoot, err)
	}
	return loaded
}

// copiedRoot returns a temporary packs tree containing _shared/zscaler and
// the named product packs.
func copiedRoot(t *testing.T, products []string) string {
	t.Helper()
	requireCollectorPackSelection(t, metadata.PackSelection{Packs: products, Shared: []string{"zscaler"}})
	root := repoRoot(t)
	directory := t.TempDir()
	if err := copyDir(filepath.Join(root, "packs", "_shared", "zscaler"), filepath.Join(directory, "_shared", "zscaler")); err != nil {
		t.Fatalf("copy _shared/zscaler: %v", err)
	}
	for _, product := range products {
		if err := copyDir(filepath.Join(root, "packs", product), filepath.Join(directory, product)); err != nil {
			t.Fatalf("copy %s: %v", product, err)
		}
	}
	return directory
}

func syntheticAuthorityPacks(t *testing.T, products []string) string {
	t.Helper()
	directory := t.TempDir()
	for _, product := range products {
		writeJSON := func(path string, value any) {
			t.Helper()
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("json.Marshal(%q): %v", path, err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q): %v", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
				t.Fatalf("os.WriteFile(%q): %v", path, err)
			}
		}
		writeJSON(filepath.Join(directory, product, "pack.json"), map[string]any{
			"provider_prefixes": map[string]any{product + "_": product},
			"provider_sources":  map[string]any{product: ZscalerCollectorProviderSources[product]},
		})
		writeJSON(filepath.Join(directory, product, "registry.json"), map[string]any{
			product + "_resource": map[string]any{"product": product},
		})
	}
	return directory
}

func rewriteManifest(t *testing.T, packsRoot, product string, update func(manifest map[string]any)) {
	t.Helper()
	path := filepath.Join(packsRoot, product, "pack.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	update(manifest)
	rendered, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(rendered, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestResolveCollectorAdaptersAgainstCommittedProviderSources(t *testing.T) {
	products := []string{"zcc", "zia", "zpa", "ztc"}
	requireCollectorPackSelection(t, metadata.PackSelection{Packs: products, Shared: []string{"zscaler"}})
	root := installedCollectorRoot(t)
	resolved, err := ResolveCollectorAdapters(ResolveCollectorAdaptersOptions{
		Authorities:   CollectorAdapterAuthorities{ByProviderSource: CreateZscalerCollectorAdaptersByProviderSource()},
		ResourceTypes: []string{"zcc_device_cleanup", "zia_url_categories", "zpa_application_segment", "ztc_network_services"},
		Root:          root,
	})
	if err != nil {
		t.Fatalf("ResolveCollectorAdapters: %v", err)
	}
	if len(resolved) != len(products) {
		t.Fatalf("len(resolved) = %d, want %d", len(resolved), len(products))
	}
	for _, product := range products {
		owner := root.Packs.ProviderOwners[product]
		var manifest metadata.PackManifest
		for _, candidate := range root.Packs.Manifests {
			if candidate.Name == owner {
				manifest = candidate
				break
			}
		}
		if manifest.ProviderSources[product] != ZscalerCollectorProviderSources[product] {
			t.Errorf("provider source for %s = %s, want %s", product, manifest.ProviderSources[product], ZscalerCollectorProviderSources[product])
		}
		adapter, ok := resolved[product]
		if !ok || adapter.Product != product {
			t.Errorf("resolved[%s].Product = %v, want %s", product, adapter.Product, product)
		}
	}
}

func TestResolveCollectorAdaptersAgainstCopiedRoot(t *testing.T) {
	packsRoot := copiedRoot(t, []string{"zia"})
	root := loadRootFromPacksDir(t, packsRoot)
	resolved, err := ResolveCollectorAdapters(ResolveCollectorAdaptersOptions{
		Authorities:   CollectorAdapterAuthorities{ByProviderSource: CreateZscalerCollectorAdaptersByProviderSource()},
		ResourceTypes: []string{"zia_url_categories"},
		Root:          root,
	})
	if err != nil {
		t.Fatalf("ResolveCollectorAdapters: %v", err)
	}
	if resolved["zia"].Product != "zia" {
		t.Errorf("resolved[zia].Product = %v, want zia", resolved["zia"].Product)
	}
}

func TestResolveCollectorAdaptersFailsClosedOnMismatchedProviderSources(t *testing.T) {
	packsRoot := syntheticAuthorityPacks(t, []string{"zcc", "zia"})
	rewriteManifest(t, packsRoot, "zcc", func(manifest map[string]any) {
		manifest["provider_sources"] = map[string]any{"zcc": "example/custom-zcc"}
	})

	root := loadRootFromPacksDir(t, packsRoot)
	resolved, err := ResolveCollectorAdapters(ResolveCollectorAdaptersOptions{
		Authorities:   CollectorAdapterAuthorities{ByProviderSource: CreateZscalerCollectorAdaptersByProviderSource()},
		ResourceTypes: []string{"zia_resource"},
		Root:          root,
	})
	if err != nil || resolved["zia"].Product != "zia" {
		t.Fatalf("expected zia to still resolve, got %v / err %v", resolved, err)
	}
	_, err = ResolveCollectorAdapters(ResolveCollectorAdaptersOptions{
		Authorities:   CollectorAdapterAuthorities{ByProviderSource: CreateZscalerCollectorAdaptersByProviderSource()},
		ResourceTypes: []string{"zcc_resource"},
		Root:          root,
	})
	want := "selected fetch resource \"zcc_resource\" uses provider source \"example/custom-zcc\" and product \"zcc\", but a matching collector adapter is not available; use a caller that injects a matching collector adapter"
	if err == nil || err.Error() != want {
		t.Errorf("ResolveCollectorAdapters() error = %v, want %q", err, want)
	}

	rewriteManifest(t, packsRoot, "zia", func(manifest map[string]any) {
		manifest["provider_sources"] = map[string]any{}
	})
	root = loadRootFromPacksDir(t, packsRoot)
	_, err = ResolveCollectorAdapters(ResolveCollectorAdaptersOptions{
		Authorities:   CollectorAdapterAuthorities{ByProviderSource: CreateZscalerCollectorAdaptersByProviderSource()},
		ResourceTypes: []string{"zia_resource"},
		Root:          root,
	})
	if err == nil || !strings.Contains(err.Error(), "without a provider source") {
		t.Errorf("expected a 'without a provider source' error, got %v", err)
	}

	rewriteManifest(t, packsRoot, "zia", func(manifest map[string]any) {
		manifest["provider_sources"] = map[string]any{"zia": ZscalerCollectorProviderSources["zpa"]}
	})
	root = loadRootFromPacksDir(t, packsRoot)
	_, err = ResolveCollectorAdapters(ResolveCollectorAdaptersOptions{
		Authorities:   CollectorAdapterAuthorities{ByProviderSource: CreateZscalerCollectorAdaptersByProviderSource()},
		ResourceTypes: []string{"zia_resource"},
		Root:          root,
	})
	if err == nil || !regexp.MustCompile(`declares product "zia".*collector product "zpa"`).MatchString(err.Error()) {
		t.Errorf("expected a product-mismatch error, got %v", err)
	}
}

func TestResolveCollectorAdaptersAllowsCustomProviderSourceAdapter(t *testing.T) {
	packsRoot := syntheticAuthorityPacks(t, []string{"zia"})
	providerSource := "example/custom-zia"
	rewriteManifest(t, packsRoot, "zia", func(manifest map[string]any) {
		manifest["provider_sources"] = map[string]any{"zia": providerSource}
	})
	root := loadRootFromPacksDir(t, packsRoot)
	adapter := CollectorAdapter{
		Product: "zia",
		Acquire: func(CollectorAcquireInput) (CollectorAuthContext, error) {
			return CollectorAuthContext{Headers: map[string]string{"Accept": "application/json"}}, nil
		},
		ComposeURL: func(input CollectorComposeUrlInput) (*url.URL, error) {
			return url.Parse("https://example.invalid/" + input.Path)
		},
	}
	resolved, err := ResolveCollectorAdapters(ResolveCollectorAdaptersOptions{
		Authorities:   CollectorAdapterAuthorities{ByProviderSource: map[string]CollectorAdapter{providerSource: adapter}},
		ResourceTypes: []string{"zia_resource"},
		Root:          root,
	})
	if err != nil {
		t.Fatalf("ResolveCollectorAdapters: %v", err)
	}
	if resolved["zia"].Product != adapter.Product {
		t.Errorf("resolved[zia] did not resolve to the injected custom adapter")
	}
}

func TestResolveCollectorAdaptersRejectsBorrowedAuthority(t *testing.T) {
	packsRoot := syntheticAuthorityPacks(t, []string{"zia"})
	rewriteManifest(t, packsRoot, "zia", func(manifest map[string]any) {
		manifest["provider_prefixes"] = map[string]any{"zia_": "rogue"}
		manifest["provider_sources"] = map[string]any{"rogue": "example/rogue"}
	})
	root := loadRootFromPacksDir(t, packsRoot)
	_, err := ResolveCollectorAdapters(ResolveCollectorAdaptersOptions{
		Authorities:   CollectorAdapterAuthorities{ByProviderSource: CreateZscalerCollectorAdaptersByProviderSource()},
		ResourceTypes: []string{"zia_resource"},
		Root:          root,
	})
	if err == nil || !regexp.MustCompile(`zia_resource.*provider source "example/rogue".*not available`).MatchString(err.Error()) {
		t.Errorf("expected a borrowed-authority error, got %v", err)
	}
}
