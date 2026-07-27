package transformadoptparity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func repository(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "../../../.."))
}

func committedTestContext(t *testing.T) Context {
	t.Helper()
	root := repository(t)
	packsRoot := filepath.Join(root, "packs")
	for _, required := range []string{"zcc", "zia", "zpa", "ztc", filepath.Join("_shared", "zscaler")} {
		path := filepath.Join(packsRoot, required)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				t.Skipf("Zscaler parity contract requires installed pack path %s", required)
			}
			t.Fatalf("os.Stat(%q) error: %v", path, err)
		}
	}
	entries, err := os.ReadDir(packsRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error: %v", packsRoot, err)
	}
	packs := make([]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "_shared" {
			packs = append(packs, entry.Name())
		}
	}
	sharedEntries, err := os.ReadDir(filepath.Join(packsRoot, "_shared"))
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error: %v", filepath.Join(packsRoot, "_shared"), err)
	}
	shared := make([]any, 0, len(sharedEntries))
	for _, entry := range sharedEntries {
		if entry.IsDir() {
			shared = append(shared, entry.Name())
		}
	}
	profile := filepath.Join(t.TempDir(), "installed.packset.json")
	writeSyntheticJSON(t, profile, map[string]any{
		"kind": "infrawright.pack-set", "version": 1, "packs": packs, "shared": shared,
	})
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot, ProfilePath: &profile})
	if err != nil {
		t.Fatal(err)
	}
	return Context{RepositoryRoot: root, Root: loaded}
}

func writeSyntheticJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(path), err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%q) error: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", path, err)
	}
}

func syntheticTestContext(t *testing.T) Context {
	t.Helper()
	packsRoot := t.TempDir()
	writeSyntheticJSON(t, filepath.Join(packsRoot, "sample", "pack.json"), map[string]any{
		"pin":               "1.2.3",
		"provider_prefixes": map[string]any{"sample_": "sample"},
		"provider_sources":  map[string]any{"sample": "example/sample"},
	})
	writeSyntheticJSON(t, filepath.Join(packsRoot, "sample", "registry.json"), map[string]any{
		"sample_resource": map[string]any{"generate": true, "product": "sample"},
	})
	writeSyntheticJSON(t, filepath.Join(packsRoot, "sample", "schemas", "provider", "sample.json"), map[string]any{
		"resource_schemas": map[string]any{
			"sample_resource": map[string]any{"block": map[string]any{"attributes": map[string]any{
				"count":       map[string]any{"optional": true, "type": "number"},
				"description": map[string]any{"optional": true, "type": "string"},
				"id":          map[string]any{"computed": true, "optional": true, "type": "string"},
				"name":        map[string]any{"required": true, "type": "string"},
				"note":        map[string]any{"optional": true, "type": "string"},
				"predefined":  map[string]any{"optional": true, "type": "bool"},
			}}},
		},
	})
	writeSyntheticJSON(t, filepath.Join(packsRoot, "sample", "overrides", "sample_resource.json"), map[string]any{
		"import_id": "{id}",
		"key_field": "name",
		"skip_if":   []any{map[string]any{"predefined": true}},
	})
	profile := filepath.Join(packsRoot, "profile.json")
	writeSyntheticJSON(t, profile, map[string]any{
		"kind": "infrawright.pack-set", "version": 1, "packs": []any{"sample"}, "shared": []any{},
	})
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot, ProfilePath: &profile})
	if err != nil {
		t.Fatalf("LoadPackRoot(synthetic) error: %v", err)
	}
	return Context{RepositoryRoot: repository(t), Root: loaded}
}

func fixtureSource(t *testing.T, name string) string {
	t.Helper()
	if name == "zpa_application_segment_microtenant" {
		return filepath.Join("testdata", name+".json")
	}
	return filepath.Join(repository(t), "tests", "fixtures", "parity", name+".json")
}

func cloneRecord(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	cloned, ok := clone(value).(map[string]any)
	if !ok {
		t.Fatal("clone was not object")
	}
	return cloned
}

func loadCommittedFixture(t *testing.T, context Context, name string) Fixture {
	t.Helper()
	fixture, err := LoadFixture(fixtureSource(t, name), context)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func syntheticFixtureRecord(mismatch bool) map[string]any {
	providerName := "Example"
	expected := []any{}
	if mismatch {
		providerName = "Provider Example"
		expected = []any{map[string]any{
			"adopt":          map[string]any{"present": true, "value": providerName},
			"classification": "semantic_mismatch",
			"disposition":    "evidence_gate",
			"evidence":       []any{"go/internal/authoring/transformadoptparity/parity_test.go"},
			"path":           "/items/example/name",
			"reason":         "Synthetic mismatch used to exercise parity classification.",
			"transform":      map[string]any{"present": true, "value": "Example"},
		}}
	}
	return map[string]any{
		"expected_differences": expected,
		"fixture_version":      float64(1),
		"name":                 "sample_parity",
		"provenance": map[string]any{
			"dependency_sources": []any{},
			"local_sources":      []any{"go/internal/authoring/transformadoptparity/parity_test.go"},
			"note":               "Synthetic parity fixture for generic engine behavior.",
			"provider_version":   "1.2.3",
			"sanitized":          true,
			"sources":            []any{"https://github.com/example/terraform-provider-sample/blob/v1.2.3/sample/resource.go"},
			"status":             "source_derived",
		},
		"provider_state": map[string]any{
			"item-1": map[string]any{
				"sensitive_values": map[string]any{},
				"values": map[string]any{
					"count": json.Number("5"), "description": "description", "id": "item-1",
					"name": providerName, "note": "", "predefined": false,
				},
			},
		},
		"raw_items": []any{map[string]any{
			"count": json.Number("5"), "description": "description", "id": "item-1",
			"name": "Example", "note": "", "predefined": false,
		}},
		"resource_type": "sample_resource",
	}
}

func syntheticFixture(t *testing.T, context Context, mismatch bool) Fixture {
	t.Helper()
	fixture, err := ValidateFixture(syntheticFixtureRecord(mismatch), context)
	if err != nil {
		t.Fatalf("ValidateFixture(synthetic) error: %v", err)
	}
	return fixture
}

func TestFixtureValidationFailsClosed(t *testing.T) {
	context := syntheticTestContext(t)
	cases := []struct {
		name, want string
		edit       func(map[string]any)
	}{
		{"unknown", "unknown key unexpected", func(f map[string]any) { f["unexpected"] = true }},
		{"unsanitized", "sanitized must be true", func(f map[string]any) { f["provenance"].(map[string]any)["sanitized"] = false }},
		{"boolean version", "unsupported fixture_version", func(f map[string]any) { f["fixture_version"] = true }},
		{"wrong pin", "does not match active sample pack pin", func(f map[string]any) { f["provenance"].(map[string]any)["provider_version"] = "wrong" }},
		{"unpinned source", "GitHub blob ref pinned", func(f map[string]any) {
			f["provenance"].(map[string]any)["sources"].([]any)[0] = "https://github.com/example/terraform-provider-sample/blob/main/source.go"
		}},
		{"missing local", "does not exist", func(f map[string]any) { f["provenance"].(map[string]any)["local_sources"].([]any)[0] = "missing.json" }},
		{"undeclared evidence", "not declared by fixture provenance", func(f map[string]any) {
			f["expected_differences"].([]any)[0].(map[string]any)["evidence"] = []any{"https://example.invalid"}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := cloneRecord(t, syntheticFixtureRecord(test.name == "undeclared evidence"))
			test.edit(value)
			if _, err := ValidateFixture(value, context); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateFixture error = %v, want %q", err, test.want)
			}
		})
	}
	wide := cloneRecord(t, syntheticFixtureRecord(false))
	wide["raw_items"].([]any)[0].(map[string]any)["count"] = json.Number(strings.Repeat("9", 400))
	if _, err := ValidateFixture(wide, context); err != nil {
		t.Fatalf("wide integer rejected: %v", err)
	}
}

func TestStrictDifferencesAndReplay(t *testing.T) {
	for _, pair := range [][2]any{
		{map[string]any{"value": false}, map[string]any{"value": json.Number("0")}},
		{map[string]any{"value": json.Number("1")}, map[string]any{"value": json.Number("1.0")}},
		{map[string]any{"value": json.Number("-0.0")}, map[string]any{"value": json.Number("0.0")}},
		{map[string]any{"a/b~c": json.Number("1")}, map[string]any{"a/b~c": json.Number("2")}},
	} {
		differences, err := Differences(pair[0], pair[1])
		if err != nil || len(differences) != 1 {
			t.Fatalf("differences = %#v, %v", differences, err)
		}
	}
	escaped, err := Differences(map[string]any{"a/b~c": json.Number("1")}, map[string]any{"a/b~c": json.Number("2")})
	if err != nil {
		t.Fatal(err)
	}
	if got := escaped[0].Path; got != "/a~1b~0c" {
		t.Fatalf("escaped pointer = %s", got)
	}
	short := map[string]any{"items": map[string]any{"one": map[string]any{"values": []any{json.Number("1"), json.Number("2"), json.Number("3")}}}}
	long := map[string]any{"items": map[string]any{"one": map[string]any{"values": []any{json.Number("1"), json.Number("4")}}}}
	differences, err := Differences(short, long)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := Replay(short, differences)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := strictEqual(replayed, long)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatalf("short replay = %#v", replayed)
	}
	extended := map[string]any{"items": map[string]any{"one": map[string]any{"values": []any{json.Number("1"), json.Number("4"), json.Number("5")}}}}
	differences, err = Differences(long, extended)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err = Replay(long, differences)
	if err != nil {
		t.Fatal(err)
	}
	equal, err = strictEqual(replayed, extended)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatalf("long replay = %#v", replayed)
	}
}

func TestComparisonReviewAndAcceptedClassification(t *testing.T) {
	context := syntheticTestContext(t)
	fixture := syntheticFixture(t, context, true)
	fixture.ExpectedDifferences = nil
	result, err := Compare(fixture, context)
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "review_required" {
		t.Fatalf("unclassified result = %s", result["result"])
	}
	fixture = syntheticFixture(t, context, true)
	fixture.ExpectedDifferences[0].Adopt.Value = "stale"
	result, err = Compare(fixture, context)
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "review_required" || result["summary"].(map[string]any)["stale_expectations"] != float64(1) {
		t.Fatalf("stale result = %#v", result)
	}
	fixture = syntheticFixture(t, context, true)
	fixture.ExpectedDifferences[0].Disposition = "accepted"
	result, err = Compare(fixture, context)
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "classified_differences" {
		t.Fatalf("accepted result = %s", result["result"])
	}
}

func TestProviderStateCoverageAndCompletenessGuard(t *testing.T) {
	context := syntheticTestContext(t)
	extra := syntheticFixture(t, context, false)
	extra.ProviderState["unreferenced"] = map[string]any{"values": map[string]any{}, "sensitive_values": map[string]any{}}
	if _, err := Compare(extra, context); err == nil || !strings.Contains(err.Error(), "unreferenced import id") {
		t.Fatalf("extra state error = %v", err)
	}
	missing := syntheticFixture(t, context, false)
	state := missing.ProviderState["item-1"]
	missing.ProviderState = map[string]map[string]any{"other": state}
	if _, err := Compare(missing, context); err == nil || !strings.Contains(err.Error(), "missing import id item-1") {
		t.Fatalf("missing state error = %v", err)
	}
	fixture := syntheticFixture(t, context, true)
	result, err := compare(fixture, context, func(any, any) ([]Difference, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "review_required" || result["summary"].(map[string]any)["unaccounted_byte_differences"] != float64(1) {
		t.Fatalf("completeness guard = %#v", result)
	}
	partial := syntheticFixture(t, context, true)
	partial.ExpectedDifferences[0].Disposition = "accepted"
	partial.ProviderState["item-1"]["values"].(map[string]any)["description"] = "Different provider description"
	result, err = compare(partial, context, func(left, right any) ([]Difference, error) {
		differences, err := Differences(left, right)
		if err != nil {
			return nil, err
		}
		for _, difference := range differences {
			if difference.Path == "/items/example/name" {
				return []Difference{difference}, nil
			}
		}
		t.Fatal("known difference not present")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "review_required" || result["summary"].(map[string]any)["unaccounted_byte_differences"] != float64(1) {
		t.Fatalf("partial completeness guard = %#v", result)
	}
}

func TestDELAndSignedZeroFullComparison(t *testing.T) {
	context := syntheticTestContext(t)
	fixture := syntheticFixture(t, context, false)
	fixture.Name = "sample_del_boundary"
	fixture.Provenance["note"] = "DEL \x7f boundary"
	fixture.RawItems[0].(map[string]any)["note"] = "\x7f"
	fixture.ProviderState["item-1"]["values"].(map[string]any)["note"] = "\x7f"
	report, err := Build([]Fixture{fixture}, context)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(report)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(rendered))
	if got := hex.EncodeToString(sum[:]); got != "da07fac937b8817b3d778460f61f877fc21e991b38f947572988b0eb7856f0b5" {
		t.Fatalf("DEL report SHA = %s", got)
	}
	output := report["fixtures"].([]any)[0].(map[string]any)["outputs"].(map[string]any)
	if got := output["transform_sha256"]; got != "5c6a06c27ddb3096c50f0f9bb98db043a83c855029324ac41c350786d4cecf40" {
		t.Fatalf("DEL artifact SHA = %s", got)
	}
	zero := syntheticFixture(t, context, false)
	zero.RawItems[0].(map[string]any)["count"] = json.Number("-0.0")
	zero.ProviderState["item-1"]["values"].(map[string]any)["count"] = json.Number("0.0")
	result, err := Compare(zero, context)
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "review_required" || result["outputs"].(map[string]any)["byte_equal"] != false {
		t.Fatalf("signed zero result = %#v", result)
	}
}

func TestDifferencesFailClosedForUnrenderableValues(t *testing.T) {
	for _, value := range []any{math.NaN(), json.Number("1e999"), 1.5} {
		if _, err := Differences(map[string]any{"value": value}, map[string]any{"value": value}); err == nil {
			t.Fatalf("Differences accepted %#v", value)
		}
	}
	differences, err := Differences(map[string]any{"value": json.Number("1.5")}, map[string]any{"value": json.Number("2.5")})
	if err != nil || len(differences) != 1 {
		t.Fatalf("lossless fractions = %#v, %v", differences, err)
	}
	context := syntheticTestContext(t)
	fixture := syntheticFixture(t, context, true)
	fixture.ExpectedDifferences[0].Adopt.Value = 1.5
	if _, err := Compare(fixture, context); err == nil {
		t.Fatal("Compare accepted an unrenderable expectation")
	}
}

func TestZeroRequestStateCoverageAndValidation(t *testing.T) {
	context := syntheticTestContext(t)
	fixture := syntheticFixture(t, context, false)
	fixture.RawItems[0].(map[string]any)["predefined"] = true
	if _, err := Compare(fixture, context); err == nil || !strings.Contains(err.Error(), "unreferenced import id item-1") {
		t.Fatalf("zero-request state error = %v", err)
	}
	invalid := syntheticFixture(t, context, false)
	invalid.RawItems[0].(map[string]any)["predefined"] = true
	invalid.ProviderState["item-1"]["sensitive_values"] = map[string]any{"not_allowed": math.NaN()}
	if _, err := Compare(invalid, context); err == nil || !strings.Contains(err.Error(), "contains a non-finite number") {
		t.Fatalf("zero-request sensitive validation error = %v", err)
	}
}

func TestReportProvenanceIsDetachedAndRequiredKeysSort(t *testing.T) {
	context := syntheticTestContext(t)
	fixture := syntheticFixture(t, context, true)
	report, err := Build([]Fixture{fixture}, context)
	if err != nil {
		t.Fatal(err)
	}
	fixture.Provenance["note"] = "mutated after build"
	got := report["fixtures"].([]any)[0].(map[string]any)["provenance"].(map[string]any)["note"]
	if got == "mutated after build" {
		t.Fatal("report provenance aliases input fixture")
	}
	err = requireKeys(map[string]any{}, []string{"zeta", "alpha"}, "fixture")
	if err == nil || !strings.Contains(err.Error(), "required key alpha") {
		t.Fatalf("sorted missing key error = %v", err)
	}
}
