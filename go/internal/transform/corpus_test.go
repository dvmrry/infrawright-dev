package transform

// corpus_test.go wires the kernel-consumable fixtures under
// tests/fixtures/transform/ (raw provider API payloads paired with the
// engine.transform Python reference implementation's expected
// .auto.tfvars.json output -- tests/test_transform.py's own corpus) into
// this package's own TransformLoadedItems pipeline, run against the same
// real committed pack/schema metadata tests/test_transform.py itself
// resolves through engine.tfschema.load_resource. Each fixture directory's
// api.json is decoded losslessly (canonjson.Decode, matching how a real
// provider API payload reaches this package -- NOT a hand-authored float64
// literal, which this package's own cloneJson rejects) and run through
// TransformLoadedItems against the resourceType named by the fixture
// directory; the resulting Items, wrapped the same way
// engine.transform.render_tfvars wraps them ({"items": ...}), must match
// expected.auto.tfvars.json under this package's usual JSON-normalized
// comparison.
//
// This is a genuine end-to-end parity corpus, not merely another unit
// test: it exercises real committed zia_*/zpa_* schemas and override
// documents this port never hand-authored, so a systematic kernel bug that
// happened to dodge every hand-written vector above would very likely
// still surface here.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func TestTransformFixtureCorpus(t *testing.T) {
	root := repoRoot(t)
	corpusRoot := filepath.Join(root, "tests", "fixtures", "transform")
	entries, err := os.ReadDir(corpusRoot)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", corpusRoot, err)
	}

	profilePath := filepath.Join(root, "packs", "full.packset.json")
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   filepath.Join(root, "packs"),
		ProfilePath: &profilePath,
	})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}

	found := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		resourceType := entry.Name()
		fixtureDir := filepath.Join(corpusRoot, resourceType)
		apiPath := filepath.Join(fixtureDir, "api.json")
		expectedPath := filepath.Join(fixtureDir, "expected.auto.tfvars.json")
		if _, err := os.Stat(apiPath); err != nil {
			continue
		}
		if _, err := os.Stat(expectedPath); err != nil {
			continue
		}
		found++

		t.Run(resourceType, func(t *testing.T) {
			resource, ok := loaded.Resources[resourceType]
			if !ok {
				t.Fatalf("resource %s not found in committed pack root", resourceType)
			}
			schema, err := loaded.LoadResourceSchema(resourceType)
			if err != nil {
				t.Fatalf("LoadResourceSchema: %v", err)
			}

			apiData, err := os.ReadFile(apiPath)
			if err != nil {
				t.Fatalf("read api.json: %v", err)
			}
			decoded, err := canonjson.Decode(apiData)
			if err != nil {
				t.Fatalf("decode api.json: %v", err)
			}
			rawItems, ok := decoded.([]any)
			if !ok {
				t.Fatalf("api.json must decode to a JSON array, got %T", decoded)
			}

			expectedData, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read expected.auto.tfvars.json: %v", err)
			}
			expected, err := canonjson.Decode(expectedData)
			if err != nil {
				t.Fatalf("decode expected.auto.tfvars.json: %v", err)
			}

			result, err := TransformLoadedItems(TransformLoadedItemsOptions{
				Resource: resource,
				Schema:   schema,
				RawItems: rawItems,
			})
			if err != nil {
				t.Fatalf("TransformLoadedItems: %v", err)
			}

			assertJSONEqual(t, resourceType, map[string]any{"items": result.Items}, expected)
		})
	}

	if found == 0 {
		t.Fatalf("no fixture directories with both api.json and expected.auto.tfvars.json found under %s", corpusRoot)
	}
}
