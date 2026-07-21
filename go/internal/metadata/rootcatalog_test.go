package metadata

// rootcatalog_test.go exercises the Go-authoritative singleton-state v2
// catalog renderer. Its byte gate lives in gate_test.go.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// activeRoot loads the committed full pack root, matching root-catalog.test.ts's
// activeRoot() helper (with its INFRAWRIGHT_PACKS/PACK_PROFILE/PACK_CATALOG
// environment-variable overrides omitted, since this port always exercises
// the committed repo fixtures).
func activeRoot(t *testing.T, root string) LoadedPackRoot {
	t.Helper()
	profilePath := filepath.Join(root, "packsets", "full.json")
	catalogPath := filepath.Join(root, "packsets", "full.json")
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

// evidenceDigest ports the evidenceDigest test helper from
// node-tests/root-catalog.test.ts.
func evidenceDigest(t *testing.T, root string, relativePaths []string) string {
	t.Helper()
	hasher := sha256.New()
	for _, relative := range relativePaths {
		hasher.Write([]byte(relative))
		hasher.Write([]byte{0})
		content, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatalf("reading %s: %v", relative, err)
		}
		hasher.Write(content)
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// TestProviderSelectionScopesResourcesAndSourceEvidence ports "provider
// selection scopes resources and source evidence deterministically".
func TestProviderSelectionScopesResourcesAndSourceEvidence(t *testing.T) {
	root := repoRoot(t)
	catalog, err := BuildRootCatalog(activeRoot(t, root), []string{"zia", "zcc", "zia"})
	if err != nil {
		t.Fatalf("BuildRootCatalog: %v", err)
	}
	if !reflect.DeepEqual(catalog.DeclaredProviders, []string{"zcc", "zia"}) {
		t.Fatalf("declaredProviders = %v", catalog.DeclaredProviders)
	}
	wantFiles := []string{"zcc/pack.json", "zcc/registry.json", "zia/pack.json", "zia/registry.json"}
	if !reflect.DeepEqual(catalog.SourceFiles, wantFiles) {
		t.Fatalf("sourceFiles = %v, want %v", catalog.SourceFiles, wantFiles)
	}
	if len(catalog.Resources) == 0 {
		t.Fatal("expected at least one resource")
	}
	for _, resource := range catalog.Resources {
		if resource.Provider != "zcc" && resource.Provider != "zia" {
			t.Fatalf("unexpected provider %q on resource %q", resource.Provider, resource.Type)
		}
	}
	if matched, _ := regexp.MatchString(`^[0-9a-f]{64}$`, catalog.SourcesSHA256); !matched {
		t.Fatalf("sourcesSha256 = %q, does not look like a sha256 hex digest", catalog.SourcesSHA256)
	}
}

// TestUnknownProviderSelectionFailsBeforeCatalogPublication ports "unknown
// provider selection fails before catalog publication".
func TestUnknownProviderSelectionFailsBeforeCatalogPublication(t *testing.T) {
	root := repoRoot(t)
	if _, err := BuildRootCatalog(activeRoot(t, root), []string{"unknown"}); err == nil ||
		!strings.Contains(err.Error(), "unknown provider(s): unknown") {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

// TestCatalogRenderingPreservesAsciiEscapingForNonAsciiMetadata ports
// "catalog rendering preserves Python ASCII escaping for non-ASCII
// metadata".
func TestCatalogRenderingPreservesAsciiEscapingForNonAsciiMetadata(t *testing.T) {
	temporary := t.TempDir()
	writeJSONFile(t, filepath.Join(temporary, "sample", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"x_": "π"},
	})
	writeJSONFile(t, filepath.Join(temporary, "sample", "registry.json"), JsonObject{
		"x_resource": JsonObject{"generate": true, "product": "café"},
	})
	digest := evidenceDigest(t, temporary, []string{"sample/pack.json", "sample/registry.json"})

	loaded, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: temporary})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	rendered, err := RenderRootCatalog(loaded, nil)
	if err != nil {
		t.Fatalf("RenderRootCatalog: %v", err)
	}

	// Built from interpreted (double-quoted) string literals with doubled
	// backslashes, so the \u sequences below land in the comparison value
	// as the literal six ASCII characters this package's canonical
	// renderer must emit for non-ASCII input (canonjson.Render
	// ASCII-escapes every character above U+007F) -- not as Go's own
	// \uXXXX rune-escape syntax, which a raw or single-backslash form
	// would trigger instead.
	escapedPi := "\\u03c0"
	escapedEAcute := "\\u00e9"
	lines := []string{
		"{",
		"  \"declared_providers\": [",
		"    \"" + escapedPi + "\"",
		"  ],",
		"  \"kind\": \"infrawright.root_catalog\",",
		"  \"resources\": [",
		"    {",
		"      \"bare_name\": \"resource\",",
		"      \"derived\": false,",
		"      \"generated\": true,",
		"      \"product\": \"caf" + escapedEAcute + "\",",
		"      \"provider\": \"" + escapedPi + "\",",
		"      \"type\": \"x_resource\"",
		"    }",
		"  ],",
		"  \"schema_version\": 2,",
		"  \"source_files\": [",
		"    \"sample/pack.json\",",
		"    \"sample/registry.json\"",
		"  ],",
		"  \"sources_sha256\": \"" + digest + "\"",
		"}",
		"",
	}
	want := strings.Join(lines, "\n")

	if rendered != want {
		t.Fatalf("rendered mismatch:\ngot:  %q\nwant: %q", rendered, want)
	}
}

// TestSelectedPackWithoutRegistryContributesManifestEvidenceOnly ports "a
// selected pack without a registry contributes manifest evidence and no
// resources".
func TestSelectedPackWithoutRegistryContributesManifestEvidenceOnly(t *testing.T) {
	temporary := t.TempDir()
	writeJSONFile(t, filepath.Join(temporary, "sample", "pack.json"), JsonObject{
		"provider_prefixes": map[string]string{"sample_": "sample"},
	})
	loaded, err := LoadPackRoot(LoadPackRootOptions{PacksRoot: temporary})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	catalog, err := BuildRootCatalog(loaded, nil)
	if err != nil {
		t.Fatalf("BuildRootCatalog: %v", err)
	}
	if !reflect.DeepEqual(catalog.DeclaredProviders, []string{"sample"}) {
		t.Fatalf("declaredProviders = %v", catalog.DeclaredProviders)
	}
	if len(catalog.Resources) != 0 {
		t.Fatalf("resources = %v, want none", catalog.Resources)
	}
	if !reflect.DeepEqual(catalog.SourceFiles, []string{"sample/pack.json"}) {
		t.Fatalf("sourceFiles = %v", catalog.SourceFiles)
	}
	want := evidenceDigest(t, temporary, []string{"sample/pack.json"})
	if catalog.SourcesSHA256 != want {
		t.Fatalf("sourcesSha256 = %s, want %s", catalog.SourcesSHA256, want)
	}
}
