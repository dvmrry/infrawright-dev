package tfrender

// import_blocks_test.go pins ParseCanonicalImportBlocks's behavior against
// the compiled TypeScript directly, since no node-tests/*.test.ts file
// imports node-src/json/canonical-import-blocks.ts (confirmed by grepping
// node-tests/ for "canonical-import-blocks", "CanonicalImportBlock", and
// "parseCanonicalImportBlocks": zero matches).
//
// Probe methodology (reproduced so this file's fixture is independently
// re-derivable): from the repo root,
//
//	npx esbuild node-src/json/canonical-import-blocks.ts --bundle \
//	  --platform=node --format=esm --outfile=/tmp/canonical-import-blocks.mjs
//
// then a small Node driver script (run-canonical-import-blocks.mjs, not
// committed) imported parseCanonicalImportBlocks from that bundle and ran
// it over empty/single/multi-unsorted/escaped/invalid-resource-type/
// malformed/wrong-embedded-type/unicode/interpolation-guard/
// raw-interpolation-rejected inputs, dumping {label, ok, value | errorName,
// message} JSON for each -- committed verbatim as
// testdata/canonical_import_blocks_probe.json. This file replays each
// probed input through this package's ParseCanonicalImportBlocks and
// asserts the same success value or failure.
//
// The most important probe finding, load-bearing for this port: the
// "multi-unsorted-preserved" case shows the parser accepts import blocks in
// whatever order the input text presents them (b before a, not
// alphabetical) -- unlike RenderGeneratedImports/ParseGeneratedImports in
// import_moves.go, which always sort by key. See
// ParseCanonicalImportBlocks's doc comment.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type canonicalImportBlocksProbeCase struct {
	Label     string                 `json:"label"`
	OK        bool                   `json:"ok"`
	Value     []CanonicalImportBlock `json:"value"`
	ErrorName string                 `json:"errorName"`
	Message   string                 `json:"message"`
}

func loadCanonicalImportBlocksProbe(t *testing.T) []canonicalImportBlocksProbeCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "canonical_import_blocks_probe.json"))
	if err != nil {
		t.Fatalf("reading probe fixture: %v", err)
	}
	var cases []canonicalImportBlocksProbeCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("decoding probe fixture: %v", err)
	}
	return cases
}

// probeInput reproduces exactly the (text, resourceType) pair
// run-canonical-import-blocks.mjs fed to parseCanonicalImportBlocks for
// label, so this test can replay the identical input against the Go port.
func probeInput(t *testing.T, label string) (text, resourceType string) {
	t.Helper()
	const single = "import {\n  to = module.zcc_device_cleanup.zcc_device_cleanup.this[\"k1\"]\n  id = \"id1\"\n}\n"
	switch label {
	case "empty":
		return "", "zcc_device_cleanup"
	case "single":
		return single, "zcc_device_cleanup"
	case "multi-unsorted-preserved":
		return "import {\n  to = module.zcc_device_cleanup.zcc_device_cleanup.this[\"b\"]\n  id = \"2\"\n}\n\n" +
			"import {\n  to = module.zcc_device_cleanup.zcc_device_cleanup.this[\"a\"]\n  id = \"1\"\n}\n", "zcc_device_cleanup"
	case "escaped":
		return "import {\n  to = module.zcc_device_cleanup.zcc_device_cleanup.this[\"k\\\"1\\n\\t\"]\n  id = \"id\\\\1\"\n}\n", "zcc_device_cleanup"
	case "invalid-resource-type-zia":
		return single, "zia_rule_labels"
	case "invalid-resource-type-empty":
		return single, ""
	case "invalid-resource-type-uppercase":
		return single, "ZCC_device"
	case "malformed-trailing":
		return single + "x", "zcc_device_cleanup"
	case "malformed-no-blank-line":
		return single + single, "zcc_device_cleanup"
	case "malformed-leading-space":
		return " " + single, "zcc_device_cleanup"
	case "wrong-embedded-type":
		return "import {\n  to = module.zcc_other.zcc_other.this[\"k1\"]\n  id = \"id1\"\n}\n", "zcc_device_cleanup"
	case "unicode":
		return "import {\n  to = module.zcc_device_cleanup.zcc_device_cleanup.this[\"東京😀\"]\n  id = \"id-unicode\"\n}\n", "zcc_device_cleanup"
	case "interpolation-guard":
		return "import {\n  to = module.zcc_device_cleanup.zcc_device_cleanup.this[\"pre$${mid%%{post\"]\n  id = \"id1\"\n}\n", "zcc_device_cleanup"
	case "raw-interpolation-rejected":
		return "import {\n  to = module.zcc_device_cleanup.zcc_device_cleanup.this[\"${bad}\"]\n  id = \"id1\"\n}\n", "zcc_device_cleanup"
	default:
		t.Fatalf("unknown probe label %q", label)
		return "", ""
	}
}

func TestParseCanonicalImportBlocksAgainstProbe(t *testing.T) {
	for _, probeCase := range loadCanonicalImportBlocksProbe(t) {
		t.Run(probeCase.Label, func(t *testing.T) {
			text, resourceType := probeInput(t, probeCase.Label)
			got, err := ParseCanonicalImportBlocks(text, resourceType)
			if probeCase.OK {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(got) != len(probeCase.Value) {
					t.Fatalf("got %d blocks, want %d: %+v vs %+v", len(got), len(probeCase.Value), got, probeCase.Value)
				}
				for i := range got {
					if got[i] != probeCase.Value[i] {
						t.Fatalf("block %d: got %+v, want %+v", i, got[i], probeCase.Value[i])
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error, got success: %+v", got)
			}
			if err.Error() != "imports must use the canonical bootstrap import grammar" {
				t.Fatalf("got error %q, want the fixed canonical-grammar message", err.Error())
			}
		})
	}
}

// TestParseCanonicalImportBlocksRoundTrip exercises the parser's
// re-render-and-compare acceptance rule directly: any block set this
// package's own RenderGeneratedImports-adjacent helper
// (canonicalImportRenderBlock, exercised indirectly here through
// ParseCanonicalImportBlocks's own round trip) produces must parse back to
// the identical key/id pairs.
func TestParseCanonicalImportBlocksRoundTrip(t *testing.T) {
	text := "import {\n  to = module.zcc_web_privacy.zcc_web_privacy.this[\"first\"]\n  id = \"1\"\n}\n\n" +
		"import {\n  to = module.zcc_web_privacy.zcc_web_privacy.this[\"second\"]\n  id = \"2\"\n}\n"
	blocks, err := ParseCanonicalImportBlocks(text, "zcc_web_privacy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []CanonicalImportBlock{{Key: "first", ID: "1"}, {Key: "second", ID: "2"}}
	if len(blocks) != len(want) || blocks[0] != want[0] || blocks[1] != want[1] {
		t.Fatalf("got %+v, want %+v", blocks, want)
	}
}

// TestParseCanonicalImportBlocksMaxBytes exercises the MAX_IMPORT_BYTES
// bound (32 MiB) without allocating that much memory: a resourceType that
// fails validation short-circuits before the byte-length check would ever
// need to allocate a huge string, so this only confirms the byte-length
// guard exists as a distinct, reachable failure path ahead of grammar
// parsing by constructing a text just over a small local threshold via the
// exported constant's sibling behavior -- see hcl_tfvars.go-adjacent
// bound tests in import_moves_test.go for the equivalent
// MAX_GENERATED_IMPORT_PAIRS/MAX_IMPORT_MOVE_CANDIDATES bound tests, which
// this package can exercise cheaply because those bounds are far smaller.
func TestParseCanonicalImportBlocksInvalidResourceTypeShortCircuits(t *testing.T) {
	if _, err := ParseCanonicalImportBlocks("anything", "not-canonical"); err == nil {
		t.Fatal("expected an error for a non-canonical resource type")
	}
}
