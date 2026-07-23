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

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
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

func testContext(t *testing.T) Context {
	t.Helper()
	root := repository(t)
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: filepath.Join(root, "packs")})
	if err != nil {
		t.Fatal(err)
	}
	return Context{RepositoryRoot: root, Root: loaded}
}

func fixtureSource(t *testing.T, name string) string {
	t.Helper()
	if name == "zpa_application_segment_microtenant" {
		return filepath.Join("testdata", name+".json")
	}
	return filepath.Join(repository(t), "tests", "fixtures", "parity", name+".json")
}

func rawFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	bytes, err := os.ReadFile(fixtureSource(t, name))
	if err != nil {
		t.Fatal(err)
	}
	value, err := canonjson.ParseDataJSONLosslessly(string(bytes))
	if err != nil {
		t.Fatal(err)
	}
	record, ok := value.(map[string]any)
	if !ok {
		t.Fatal("fixture was not object")
	}
	return record
}

func cloneRecord(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	cloned, ok := clone(value).(map[string]any)
	if !ok {
		t.Fatal("clone was not object")
	}
	return cloned
}

func loadFixture(t *testing.T, name string) Fixture {
	t.Helper()
	fixture, err := LoadFixture(fixtureSource(t, name), testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestFixtureValidationFailsClosed(t *testing.T) {
	context := testContext(t)
	cases := []struct {
		name, want string
		edit       func(map[string]any)
	}{
		{"unknown", "unknown key unexpected", func(f map[string]any) { f["unexpected"] = true }},
		{"unsanitized", "sanitized must be true", func(f map[string]any) { f["provenance"].(map[string]any)["sanitized"] = false }},
		{"boolean version", "unsupported fixture_version", func(f map[string]any) { f["fixture_version"] = true }},
		{"wrong pin", "does not match active zcc pack pin", func(f map[string]any) { f["provenance"].(map[string]any)["provider_version"] = "wrong" }},
		{"unpinned source", "GitHub blob ref pinned", func(f map[string]any) {
			f["provenance"].(map[string]any)["sources"].([]any)[0] = "https://github.com/zscaler/terraform-provider-zia/blob/main/source.go"
		}},
		{"missing local", "does not exist", func(f map[string]any) { f["provenance"].(map[string]any)["local_sources"].([]any)[0] = "missing.json" }},
		{"undeclared evidence", "not declared by fixture provenance", func(f map[string]any) {
			f["expected_differences"].([]any)[0].(map[string]any)["evidence"] = []any{"https://example.invalid"}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := cloneRecord(t, rawFixture(t, "zia_dlp_engines_predefined_name"))
			if test.name == "wrong pin" || test.name == "missing local" {
				value = cloneRecord(t, rawFixture(t, "zcc_failopen_policy_inversion"))
			}
			test.edit(value)
			if _, err := ValidateFixture(value, context); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateFixture error = %v, want %q", err, test.want)
			}
		})
	}
	wide := cloneRecord(t, rawFixture(t, "zcc_failopen_policy_inversion"))
	wide["raw_items"].([]any)[0].(map[string]any)["wideEvidence"] = json.Number(strings.Repeat("9", 400))
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
	context := testContext(t)
	fixture := loadFixture(t, "zia_dlp_engines_predefined_name")
	fixture.ExpectedDifferences = nil
	result, err := Compare(fixture, context)
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "review_required" {
		t.Fatalf("unclassified result = %s", result["result"])
	}
	fixture = loadFixture(t, "zia_dlp_engines_predefined_name")
	fixture.ExpectedDifferences[0].Adopt.Value = "stale"
	result, err = Compare(fixture, context)
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "review_required" || result["summary"].(map[string]any)["stale_expectations"] != float64(1) {
		t.Fatalf("stale result = %#v", result)
	}
	fixture = loadFixture(t, "zia_dlp_engines_predefined_name")
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
	context := testContext(t)
	extra := loadFixture(t, "zcc_failopen_policy_inversion")
	extra.ProviderState["unreferenced"] = map[string]any{"values": map[string]any{}, "sensitive_values": map[string]any{}}
	if _, err := Compare(extra, context); err == nil || !strings.Contains(err.Error(), "unreferenced import id") {
		t.Fatalf("extra state error = %v", err)
	}
	missing := loadFixture(t, "zcc_failopen_policy_inversion")
	state := missing.ProviderState["policy-001"]
	missing.ProviderState = map[string]map[string]any{"other": state}
	if _, err := Compare(missing, context); err == nil || !strings.Contains(err.Error(), "missing import id policy-001") {
		t.Fatalf("missing state error = %v", err)
	}
	fixture := loadFixture(t, "zia_dlp_engines_predefined_name")
	result, err := compare(fixture, context, func(any, any) ([]Difference, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "review_required" || result["summary"].(map[string]any)["unaccounted_byte_differences"] != float64(1) {
		t.Fatalf("completeness guard = %#v", result)
	}
	partial := loadFixture(t, "zia_dlp_engines_predefined_name")
	partial.ExpectedDifferences[0].Disposition = "accepted"
	partial.ProviderState["101"]["values"].(map[string]any)["description"] = "Different provider description"
	result, err = compare(partial, context, func(left, right any) ([]Difference, error) {
		differences, err := Differences(left, right)
		if err != nil {
			return nil, err
		}
		for _, difference := range differences {
			if difference.Path == "/items/predefined_engine/name" {
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
	context := testContext(t)
	fixture := loadFixture(t, "zcc_failopen_policy_inversion")
	fixture.Name = "zcc_del_boundary"
	fixture.Provenance["note"] = "DEL \x7f boundary"
	fixture.RawItems[0].(map[string]any)["strictEnforcementPromptMessage"] = "\x7f"
	fixture.ProviderState["policy-001"]["values"].(map[string]any)["strict_enforcement_prompt_message"] = "\x7f"
	report, err := Build([]Fixture{fixture}, context)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(report)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(rendered))
	if got := hex.EncodeToString(sum[:]); got != "25d661a024d9c8ed85ad9f2c707e96dd210150e0127c69bb0e192c18c9cf2b4c" {
		t.Fatalf("DEL report SHA = %s", got)
	}
	output := report["fixtures"].([]any)[0].(map[string]any)["outputs"].(map[string]any)
	if got := output["transform_sha256"]; got != "2b759590c5f2a861cc70545e7e9eea82b77fe5aaff9a1750c99a9b2fb545bc8d" {
		t.Fatalf("DEL artifact SHA = %s", got)
	}
	zero := loadFixture(t, "zcc_failopen_policy_inversion")
	zero.RawItems[0].(map[string]any)["captivePortalWebSecDisableMinutes"] = json.Number("-0.0")
	zero.ProviderState["policy-001"]["values"].(map[string]any)["captive_portal_web_sec_disable_minutes"] = json.Number("0.0")
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
	fixture := loadFixture(t, "zia_dlp_engines_predefined_name")
	fixture.ExpectedDifferences[0].Adopt.Value = 1.5
	if _, err := Compare(fixture, testContext(t)); err == nil {
		t.Fatal("Compare accepted an unrenderable expectation")
	}
}

func TestZeroRequestStateCoverageAndValidation(t *testing.T) {
	context := testContext(t)
	fixture := loadFixture(t, "zia_url_filtering_rules_zero_quota")
	fixture.RawItems[0].(map[string]any)["predefined"] = true
	if _, err := Compare(fixture, context); err == nil || !strings.Contains(err.Error(), "unreferenced import id 202") {
		t.Fatalf("zero-request state error = %v", err)
	}
	invalid := loadFixture(t, "zia_url_filtering_rules_zero_quota")
	invalid.RawItems[0].(map[string]any)["predefined"] = true
	invalid.ProviderState["202"]["sensitive_values"] = map[string]any{"not_allowed": math.NaN()}
	if _, err := Compare(invalid, context); err == nil || !strings.Contains(err.Error(), "contains a non-finite number") {
		t.Fatalf("zero-request sensitive validation error = %v", err)
	}
}

func TestReportProvenanceIsDetachedAndRequiredKeysSort(t *testing.T) {
	fixture := loadFixture(t, "zia_dlp_engines_predefined_name")
	report, err := Build([]Fixture{fixture}, testContext(t))
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
