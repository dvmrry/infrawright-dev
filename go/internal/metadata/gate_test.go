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

func TestRootCatalogSchemasAreVersionExclusive(t *testing.T) {
	root := repoRoot(t)
	v1Schema := compileRootCatalogSchema(
		t,
		root,
		"root-catalog.schema.json",
		"https://infrawright.local/schemas/root-catalog.schema.json",
	)
	v2Schema := compileRootCatalogSchema(
		t,
		root,
		"root-catalog.v2.schema.json",
		"https://infrawright.local/schemas/root-catalog.v2.schema.json",
	)
	v1Catalog := parseControlJSONFile(t, filepath.Join(root, "catalogs", "zscaler-root-catalog.v1.json"))
	v2Catalog := parseControlJSONFile(t, filepath.Join(root, "catalogs", "zscaler-root-catalog.v2.json"))

	tests := []struct {
		name         string
		schema       *jsonschema.Schema
		ownCatalog   canonjson.Value
		otherCatalog canonjson.Value
	}{
		{
			name:         "v1",
			schema:       v1Schema,
			ownCatalog:   v1Catalog,
			otherCatalog: v2Catalog,
		},
		{
			name:         "v2",
			schema:       v2Schema,
			ownCatalog:   v2Catalog,
			otherCatalog: v1Catalog,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.schema.Validate(test.ownCatalog); err != nil {
				t.Errorf("%s schema rejected its own catalog: %v", test.name, err)
			}
			if err := test.schema.Validate(test.otherCatalog); err == nil {
				t.Errorf("%s schema accepted the other catalog version", test.name)
			}
		})
	}
}

func compileRootCatalogSchema(t *testing.T, root, filename, schemaID string) *jsonschema.Schema {
	t.Helper()
	schemaValue := parseControlJSONFile(t, filepath.Join(root, "docs", "schemas", filename))
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(schemaID, schemaValue); err != nil {
		t.Fatalf("registering root catalog schema %s: %v", filename, err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		t.Fatalf("compiling root catalog schema %s: %v", filename, err)
	}
	return compiled
}

func parseControlJSONFile(t *testing.T, path string) canonjson.Value {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading control JSON %s: %v", path, err)
	}
	value, err := canonjson.ParseControlJSON(string(content))
	if err != nil {
		t.Fatalf("parsing control JSON %s: %v", path, err)
	}
	return value
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
