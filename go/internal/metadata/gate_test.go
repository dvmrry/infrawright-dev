package metadata

// gate_test.go is THE Wave 2 gate: loadPackRoot over the committed packs/
// and packsets/full.json, then renderRootCatalog scoped to
// providers zcc,zia,zpa,ztc, must reproduce the committed Go-authoritative
// singleton-state v2 catalog byte-for-byte. The frozen v1 catalog is not read
// or regenerated here.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const frozenV1CatalogSHA256 = "844c6c4b7d88266086732b3a68a9266f9abcfb4b00ea1177e6b4fdff92d79f10"

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

func TestRootCatalogV2ByteGate(t *testing.T) {
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

	expected, err := os.ReadFile(filepath.Join(root, "catalogs", "zscaler-root-catalog.v2.json"))
	if err != nil {
		t.Fatalf("reading golden catalog: %v", err)
	}

	if rendered != string(expected) {
		reportCatalogMismatch(t, string(expected), rendered)
	}
}

func TestFrozenV1RootCatalogSHA256(t *testing.T) {
	root := repoRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "catalogs", "zscaler-root-catalog.v1.json"))
	if err != nil {
		t.Fatalf("reading frozen v1 catalog: %v", err)
	}
	got := sha256.Sum256(content)
	if hex.EncodeToString(got[:]) != frozenV1CatalogSHA256 {
		t.Fatalf("frozen v1 catalog SHA-256 = %x, want %s", got, frozenV1CatalogSHA256)
	}
}

func TestRootCatalogV2SatisfiesCommittedSchema(t *testing.T) {
	root := repoRoot(t)
	schemaBytes, err := os.ReadFile(filepath.Join(root, "docs", "schemas", "root-catalog.schema.json"))
	if err != nil {
		t.Fatalf("reading root catalog schema: %v", err)
	}
	catalogBytes, err := os.ReadFile(filepath.Join(root, "catalogs", "zscaler-root-catalog.v2.json"))
	if err != nil {
		t.Fatalf("reading v2 root catalog: %v", err)
	}
	schemaValue, err := canonjson.ParseControlJSON(string(schemaBytes))
	if err != nil {
		t.Fatalf("parsing root catalog schema: %v", err)
	}
	catalogValue, err := canonjson.ParseControlJSON(string(catalogBytes))
	if err != nil {
		t.Fatalf("parsing v2 root catalog: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const schemaID = "https://infrawright.local/schemas/root-catalog.schema.json"
	if err := compiler.AddResource(schemaID, schemaValue); err != nil {
		t.Fatalf("registering root catalog schema: %v", err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		t.Fatalf("compiling root catalog schema: %v", err)
	}
	if err := compiled.Validate(catalogValue); err != nil {
		t.Fatalf("v2 root catalog does not satisfy committed schema: %v", err)
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
