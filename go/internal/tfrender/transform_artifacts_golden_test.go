package tfrender

// transform_artifacts_golden_test.go wires every artifact-level golden
// under tests/fixtures/demo-expected/ and tests/fixtures/transform/*/expected*
// that is consumable without node-src/domain/transform-runner.ts's
// runTransformBatch -- i.e. without node-src/metadata/loader.ts's pack
// loading or node-src/domain/pull-transform.ts's raw-API-to-item
// projection, both out of this package's scope (the sibling finisher's
// go/internal/transform package, per this task's brief).
//
// # Reconstruction strategy
//
// Each golden pair is exactly ({resourceType}.tfvars.json or
// expected.auto.tfvars.json, {resourceType}_imports.tf or
// expected_imports.tf): the byte-exact output of
// WriteTransformArtifacts/PublishCompiledTransformArtifacts for a
// PullTransformResult this test never gets to see directly (it was
// produced by the full pull-transform pipeline this package does not
// port). This file reconstructs an equivalent-enough PullTransformResult
// directly from the golden files themselves, then re-renders it and checks
// for byte-for-byte equality against those same golden files -- which is a
// faithful byte-exactness gate, not an approximation, because:
//
//  1. Items: the golden tfvars.json's own "items" object literally IS
//     PullTransformResult.Items (parsed losslessly, so every number token
//     round-trips through canonjson.RenderLosslessArtifactJSON unchanged).
//  2. Originals: renderTransformImports only ever reads one field from
//     each original -- whichever field(s) the import_id template
//     references (here, always the default "{id}" template, so only
//     original[key].id) -- and formats it through pythonTransformString,
//     which for a plain Go string returns it unchanged. So setting
//     Originals[key] = {"id": <the exact ImportID text already recorded in
//     the golden imports file, parsed via this package's own
//     ParseGeneratedImports>} reproduces the golden imports file's bytes
//     exactly when re-rendered, without needing the real (numeric or
//     string) original id value or any other original field.
//
// None of the 27 golden resource types here is a "derived" resource
// (writeDerivedTransformArtifact's config-only, no-imports path) or uses a
// non-default import_id template -- confirmed by their presence in this
// port's DEMO_RESOURCES/FIXTURE_RESOURCES lists (transcribed from
// node-tests/transform-runtime-artifacts.test.ts) and by this test's own
// assertion that every item key has a matching import pair.
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// demoGoldenResourceTypes ports DEMO_RESOURCES from
// node-tests/transform-runtime-artifacts.test.ts.
var demoGoldenResourceTypes = []string{
	"zcc_device_cleanup",
	"zcc_failopen_policy",
	"zcc_forwarding_profile",
	"zcc_trusted_network",
	"zcc_web_privacy",
	"zia_bandwidth_control_rule",
	"zia_cloud_app_control_rule",
	"zia_dlp_web_rules",
	"zia_location_management",
	"zia_rule_labels",
	"zia_ssl_inspection_rules",
	"zia_url_categories",
	"zia_url_filtering_rules",
	"zpa_app_connector_group",
	"zpa_application_segment",
	"zpa_application_server",
	"zpa_microtenant_controller",
	"zpa_policy_access_rule",
	"zpa_segment_group",
	"zpa_server_group",
}

// fixtureGoldenResourceTypes ports FIXTURE_RESOURCES from
// node-tests/transform-runtime-artifacts.test.ts.
var fixtureGoldenResourceTypes = []string{
	"zia_cloud_app_control_rule",
	"zia_location_management",
	"zia_ssl_inspection_rules",
	"zia_url_categories",
	"zpa_application_segment",
	"zpa_segment_group",
	"zpa_server_group",
}

// repoRoot locates the repository root, three levels above
// go/internal/tfrender, so this test can reach
// tests/fixtures/... regardless of `go test`'s working directory
// convention (always the package directory).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "packs", "full.packset.json")); err != nil {
		t.Fatalf("expected %s to contain packs/full.packset.json (repo root detection failed): %v", root, err)
	}
	return root
}

// reconstructResultFromGolden ports this file's "reconstruction strategy"
// doc comment: build a PullTransformResult from a golden tfvars.json +
// imports.tf pair such that re-rendering it reproduces both files
// byte-for-byte.
func reconstructResultFromGolden(t *testing.T, resourceType, tfvarsPath, importsPath string) PullTransformResult {
	t.Helper()
	tfvarsText, err := os.ReadFile(tfvarsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", tfvarsPath, err)
	}
	decoded, err := canonjson.ParseDataJSONLosslessly(string(tfvarsText))
	if err != nil {
		t.Fatalf("parsing %s: %v", tfvarsPath, err)
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("%s: top-level value is not an object", tfvarsPath)
	}
	itemsValue, ok := root["items"]
	if !ok {
		t.Fatalf("%s: missing \"items\" key", tfvarsPath)
	}
	itemsRecord, ok := itemsValue.(map[string]any)
	if !ok {
		t.Fatalf("%s: \"items\" is not an object", tfvarsPath)
	}

	importsText, err := os.ReadFile(importsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", importsPath, err)
	}
	pairs, err := ParseGeneratedImports(resourceType, string(importsText))
	if err != nil {
		t.Fatalf("ParseGeneratedImports(%s): %v", importsPath, err)
	}
	importIDByKey := map[string]string{}
	for _, pair := range pairs {
		importIDByKey[pair.Key] = pair.ImportID
	}

	items := make(map[string]map[string]any, len(itemsRecord))
	originals := make(map[string]map[string]any, len(itemsRecord))
	for key, value := range itemsRecord {
		fields, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s: items[%q] is not an object", tfvarsPath, key)
		}
		items[key] = fields
		importID, ok := importIDByKey[key]
		if !ok {
			t.Fatalf("%s: item key %q has no matching import pair in %s", tfvarsPath, key, importsPath)
		}
		originals[key] = map[string]any{"id": importID}
	}
	if len(items) != len(pairs) {
		t.Fatalf("%s/%s: %d items but %d import pairs (expected a 1:1 key correspondence)", tfvarsPath, importsPath, len(items), len(pairs))
	}

	return PullTransformResult{Drops: []string{}, Items: items, Originals: originals}
}

func runGoldenArtifactCase(t *testing.T, resourceType, tfvarsPath, importsPath string) {
	t.Helper()
	result := reconstructResultFromGolden(t, resourceType, tfvarsPath, importsPath)
	workspace := t.TempDir()
	options := newArtifactOptions(workspace, resourceType)
	options.Result = result
	options.LookupNameField = nil // these golden fixtures have no lookup sidecar

	written, err := WriteTransformArtifacts(options)
	if err != nil {
		t.Fatalf("WriteTransformArtifacts(%s): %v", resourceType, err)
	}

	gotConfig := readFileText(t, written.Paths.Config)
	wantConfig := readFileText(t, tfvarsPath)
	if gotConfig != wantConfig {
		t.Fatalf("%s: tfvars bytes mismatch\ngot:\n%s\nwant:\n%s", resourceType, gotConfig, wantConfig)
	}
	gotImports := readFileText(t, written.Paths.Imports)
	wantImports := readFileText(t, importsPath)
	if gotImports != wantImports {
		t.Fatalf("%s: imports bytes mismatch\ngot:\n%s\nwant:\n%s", resourceType, gotImports, wantImports)
	}
}

// TestDemoExpectedGoldenArtifacts wires every tests/fixtures/demo-expected/
// tfvars.json + _imports.tf pair (20 resources; the runner-level "demo"
// end-to-end test that also produces these bytes,
// "runTransformBatch materializes all 20 demo fixture goldens exactly", is
// skipped -- see this package's doc.go and transform_artifacts_test.go's
// doc comment).
func TestDemoExpectedGoldenArtifacts(t *testing.T) {
	root := repoRoot(t)
	demoExpected := filepath.Join(root, "tests", "fixtures", "demo-expected")
	for _, resourceType := range demoGoldenResourceTypes {
		t.Run(resourceType, func(t *testing.T) {
			runGoldenArtifactCase(
				t, resourceType,
				filepath.Join(demoExpected, resourceType+".tfvars.json"),
				filepath.Join(demoExpected, resourceType+"_imports.tf"),
			)
		})
	}
}

// TestTransformFixtureGoldenArtifacts wires every
// tests/fixtures/transform/*/expected.auto.tfvars.json + expected_imports.tf
// pair (7 resources; the runner-level end-to-end test,
// "runTransformBatch materializes all seven detailed transform goldens
// exactly", is skipped for the same reason as TestDemoExpectedGoldenArtifacts).
func TestTransformFixtureGoldenArtifacts(t *testing.T) {
	root := repoRoot(t)
	transformFixtures := filepath.Join(root, "tests", "fixtures", "transform")
	for _, resourceType := range fixtureGoldenResourceTypes {
		t.Run(resourceType, func(t *testing.T) {
			dir := filepath.Join(transformFixtures, resourceType)
			runGoldenArtifactCase(
				t, resourceType,
				filepath.Join(dir, "expected.auto.tfvars.json"),
				filepath.Join(dir, "expected_imports.tf"),
			)
		})
	}
}
