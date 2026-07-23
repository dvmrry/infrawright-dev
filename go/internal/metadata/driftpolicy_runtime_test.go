package metadata

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func runtimePolicyEntry(path string, extra JsonObject) JsonObject {
	entry := JsonObject{
		"path":        path,
		"reason":      "test",
		"approved_by": "unit",
	}
	for key, value := range extra {
		entry[key] = value
	}
	return entry
}

func newRuntimePolicy(t *testing.T, resourceTypes JsonObject) *DriftPolicy {
	t.Helper()
	policy, err := NewDriftPolicy(JsonObject{
		"version":        float64(1),
		"resource_types": resourceTypes,
	}, "runtime test")
	if err != nil {
		t.Fatalf("NewDriftPolicy(resourceTypes=%v) error = %v, want nil", resourceTypes, err)
	}
	return policy
}

func TestNewDriftPolicyNilAndTypedFailure(t *testing.T) {
	policy, err := NewDriftPolicy(nil, "nil policy")
	if err != nil {
		t.Fatalf("NewDriftPolicy(nil) error = %v, want nil", err)
	}
	if got := policy.StaleEntries(StaleEntriesOptions{}); len(got) != 0 {
		t.Errorf("NewDriftPolicy(nil).StaleEntries({}) = %v, want []", got)
	}

	_, err = NewDriftPolicy(JsonObject{}, "broken")
	var metadataErr *MetadataError
	if !errors.As(err, &metadataErr) {
		t.Fatalf("NewDriftPolicy({}) error = %v (%T), want *MetadataError", err, err)
	}
	if got, want := metadataErr.Error(), "broken: drift policy missing version"; got != want {
		t.Errorf("NewDriftPolicy({}) error = %q, want %q", got, want)
	}
}

func TestDriftPolicyNilReceiverAndZeroEntryBoundary(t *testing.T) {
	var policy *DriftPolicy
	if got := policy.Entries("sample_resource", PolicyProjectionOmit); len(got) != 0 {
		t.Errorf("nil DriftPolicy.Entries(...) = %#v, want []", got)
	}
	if policy.ProjectionOmits("sample_resource", []any{"field"}) {
		t.Error("nil DriftPolicy.ProjectionOmits(...) = true, want false")
	}
	if policy.ToleratesPlanPath("sample_resource", []any{"field"}, "update") {
		t.Error("nil DriftPolicy.ToleratesPlanPath(...) = true, want false")
	}
	policy.MarkMatched(PolicyEntry{})
	if got := policy.StaleEntries(StaleEntriesOptions{}); len(got) != 0 {
		t.Errorf("nil DriftPolicy.StaleEntries({}) = %#v, want []", got)
	}
	if got := (PolicyEntry{}).Data(); got != nil {
		t.Errorf("zero PolicyEntry.Data() = %#v, want nil", got)
	}
}

// TestPolicyPathRuntimeVectors ports "policy paths retain exact indexes and
// wildcard only matches list indexes" from the original test corpus.
func TestPolicyPathRuntimeVectors(t *testing.T) {
	parsed, err := ParsePolicyPath(`rules[*].tags["Name"]`)
	if err != nil {
		t.Fatalf("ParsePolicyPath(rules[*].tags[\"Name\"]) error = %v, want nil", err)
	}
	if want := []any{"rules", "*", "tags", "Name"}; !reflect.DeepEqual(parsed, want) {
		t.Errorf("ParsePolicyPath(rules[*].tags[\"Name\"]) = %#v, want %#v", parsed, want)
	}

	wildcard, err := ParsePolicyPath("rules[].status")
	if err != nil {
		t.Fatalf("ParsePolicyPath(rules[].status) error = %v, want nil", err)
	}
	if !PolicySelectorMatches(wildcard, []any{"rules", 0, "status"}) {
		t.Error("PolicySelectorMatches(rules[].status, rules[0].status) = false, want true")
	}
	if PolicySelectorMatches(wildcard, []any{"rules", "0", "status"}) {
		t.Error("PolicySelectorMatches(rules[].status, rules.\"0\".status) = true, want false")
	}

	exact, err := ParsePolicyPath("rules[2].status")
	if err != nil {
		t.Fatalf("ParsePolicyPath(rules[2].status) error = %v, want nil", err)
	}
	if !PolicySelectorMatches(exact, []any{"rules", 2, "status"}) {
		t.Error("PolicySelectorMatches(rules[2].status, rules[2].status) = false, want true")
	}

	escaped, err := ParsePolicyPath(`rules[0].labels["a.b"]["q\"uote"]`)
	if err != nil {
		t.Fatalf("ParsePolicyPath(escaped selector) error = %v, want nil", err)
	}
	if want := []any{"rules", int64(0), "labels", "a.b", `q"uote`}; !reflect.DeepEqual(escaped, want) {
		t.Errorf("ParsePolicyPath(escaped selector) = %#v, want %#v", escaped, want)
	}

	_, err = ParsePolicyPath("rules[١].status")
	var metadataErr *MetadataError
	if !errors.As(err, &metadataErr) {
		t.Errorf("ParsePolicyPath(non-ASCII index) error = %v (%T), want *MetadataError", err, err)
	}
	_, err = ParsePolicyPath("", "assessment path")
	metadataErr = nil
	if !errors.As(err, &metadataErr) {
		t.Errorf("ParsePolicyPath(empty, assessment path) error = %v (%T), want *MetadataError", err, err)
	} else if got, want := metadataErr.Error(), "assessment path must be a non-empty string"; got != want {
		t.Errorf("ParsePolicyPath(empty, assessment path) error = %q, want %q", got, want)
	}
}

// TestPolicyPathNormalizeAndFormatProbe pins untested exported helper semantics
// from the original implementation. Probe commands (Node v24.15.0):
//
// From the repository root, /tmp/node_modules was absent before this setup.
// The external flag is mandatory: bundling lossless-json duplicates its class
// and breaks the source's instanceof checks.
//
// ln -s "$PWD/node_modules" /tmp/node_modules
//
//	npx esbuild the original implementation --bundle --format=esm --external:lossless-json --outfile=/tmp/probe.mjs
//	node -e 'import("file:///tmp/probe.mjs").then(({parsePolicyPath,normalizePolicyPath,formatPolicyPath})=>{const p=[parsePolicyPath("quoted[\"*\"]"),parsePolicyPath("literal[\"[]\"]"),parsePolicyPath("huge[9007199254740992]")];console.log(JSON.stringify({normalized:p.map(normalizePolicyPath),formatted:[formatPolicyPath([]),...p.map(formatPolicyPath),formatPolicyPath(["[]"]),formatPolicyPath(["field","[]"])]}));})'
//
// unlink /tmp/node_modules
//
// The exact observation was
// {"normalized":[["quoted","[]"],["literal","[]"],["huge","[]"]],
// "formatted":["<root>","quoted[]","literal[]",
// "huge[9007199254740992]","[]","field[]"]}.
func TestPolicyPathNormalizeAndFormatProbe(t *testing.T) {
	tests := []struct {
		path       string
		normalized []string
		formatted  string
	}{
		{path: `quoted["*"]`, normalized: []string{"quoted", "[]"}, formatted: "quoted[]"},
		{path: `literal["[]"]`, normalized: []string{"literal", "[]"}, formatted: "literal[]"},
		{path: "huge[9007199254740992]", normalized: []string{"huge", "[]"}, formatted: "huge[9007199254740992]"},
	}
	if got := formatPolicyPath(nil); got != "<root>" {
		t.Errorf("formatPolicyPath(nil) = %q, want %q", got, "<root>")
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			path, err := ParsePolicyPath(test.path)
			if err != nil {
				t.Fatalf("ParsePolicyPath(%q) error = %v, want nil", test.path, err)
			}
			if got := NormalizePolicyPath(path); !reflect.DeepEqual(got, test.normalized) {
				t.Errorf("NormalizePolicyPath(ParsePolicyPath(%q)) = %#v, want %#v", test.path, got, test.normalized)
			}
			if got := formatPolicyPath(path); got != test.formatted {
				t.Errorf("formatPolicyPath(ParsePolicyPath(%q)) = %q, want %q", test.path, got, test.formatted)
			}
		})
	}
	if got := formatPolicyPath([]any{"[]"}); got != "[]" {
		t.Errorf("formatPolicyPath([\"[]\"]) = %q, want %q", got, "[]")
	}
	if got := formatPolicyPath([]any{"field", "[]"}); got != "field[]" {
		t.Errorf("formatPolicyPath([\"field\", \"[]\"]) = %q, want %q", got, "field[]")
	}
}

// TestDriftPolicyRejectsUnsafeOrAmbiguousNodeVectors ports "full policy
// validation rejects unsafe or ambiguous entries" from
// the original test corpus.
func TestDriftPolicyRejectsUnsafeOrAmbiguousNodeVectors(t *testing.T) {
	document := func(resource JsonObject) JsonObject {
		return JsonObject{
			"version": float64(1),
			"resource_types": JsonObject{
				"bad": resource,
			},
		}
	}
	invalid := []any{
		JsonObject{},
		JsonObject{"version": true, "resource_types": JsonObject{}},
		JsonObject{"version": float64(2), "resource_types": JsonObject{}},
		JsonObject{"version": float64(1), "resource_types": []any{}},
		document(JsonObject{"surprise": []any{}}),
		document(JsonObject{"plan_tolerate": nil}),
		document(JsonObject{"plan_tolerate": []any{
			runtimePolicyEntry("x", JsonObject{"actions": nil}),
		}}),
		document(JsonObject{"plan_tolerate": []any{
			runtimePolicyEntry("x", JsonObject{"actions": []any{"delete"}}),
		}}),
		document(JsonObject{"plan_tolerate": []any{
			runtimePolicyEntry("x", nil), runtimePolicyEntry("x", nil),
		}}),
		document(JsonObject{
			"projection_fill": []any{JsonObject{
				"path": "x", "source": "raw", "reason": "r", "approved_by": "a",
			}},
			"projection_omit": []any{runtimePolicyEntry("x", nil)},
		}),
	}
	for index, value := range invalid {
		if _, err := NewDriftPolicy(value, "<memory>"); err == nil {
			t.Errorf("NewDriftPolicy(Node invalid vector %d) error = nil, want *MetadataError", index)
		}
	}
}

func TestDriftPolicyValidationPreservesFrozenPythonVector(t *testing.T) {
	valid := JsonObject{
		"version": float64(1),
		"resource_types": JsonObject{
			"sample_resource": JsonObject{
				"plan_tolerate": []any{runtimePolicyEntry(`rules[].tags["Name"]`, nil)},
			},
		},
	}
	document := func(resources JsonObject) JsonObject {
		return JsonObject{"version": float64(1), "resource_types": resources}
	}
	resource := func(config JsonObject) JsonObject {
		return document(JsonObject{"sample_resource": config})
	}
	corpus := []any{
		nil,
		valid,
		JsonObject{},
		[]any{},
		JsonObject{"version": float64(2), "resource_types": JsonObject{}},
		document(JsonObject{"bad-name": JsonObject{}}),
		resource(JsonObject{"unknown": []any{}}),
		resource(JsonObject{"plan_tolerate": nil}),
		resource(JsonObject{"plan_tolerate": []any{
			runtimePolicyEntry("x", JsonObject{"actions": nil}),
		}}),
		resource(JsonObject{"plan_tolerate": []any{
			runtimePolicyEntry("x", JsonObject{"actions": []any{}}),
		}}),
		resource(JsonObject{"plan_tolerate": []any{
			runtimePolicyEntry("x", JsonObject{"actions": []any{"update", "update"}}),
		}}),
		resource(JsonObject{"projection_omit_if": []any{
			runtimePolicyEntry("x", JsonObject{"values": []any{}}),
		}}),
		resource(JsonObject{"projection_omit_if": []any{
			runtimePolicyEntry("x", JsonObject{"values": []any{JsonObject{}}}),
		}}),
		resource(JsonObject{"projection_sync": []any{JsonObject{
			"target_path": "x", "source_path": "x", "reason": "r", "approved_by": "a",
		}}}),
		resource(JsonObject{"projection_fill": []any{JsonObject{
			"path": "x[].y", "source": "raw", "reason": "r", "approved_by": "a",
		}}}),
		resource(JsonObject{"projection_omit": []any{
			runtimePolicyEntry("x", nil), runtimePolicyEntry("x", nil),
		}}),
	}
	wantAccepted := []bool{
		true, true, false, false, false, false, false, false,
		false, false, false, false, false, false, false, false,
	}
	for index, value := range corpus {
		_, err := NewDriftPolicy(value, "<memory>")
		gotAccepted := err == nil
		if gotAccepted != wantAccepted[index] {
			t.Errorf("NewDriftPolicy(frozen Python corpus[%d]) accepted = %t (error %v), want %t", index, gotAccepted, err, wantAccepted[index])
		}
	}
}

// TestDriftPolicyAcceptsCompleteNonPlanModeVector ports "valid non-plan modes
// remain accepted by the complete validator" from
// the original test corpus.
func TestDriftPolicyAcceptsCompleteNonPlanModeVector(t *testing.T) {
	policy, err := NewDriftPolicy(JsonObject{
		"version": float64(1),
		"resource_types": JsonObject{
			"sample_resource": JsonObject{
				"projection_omit": []any{runtimePolicyEntry("description", nil)},
				"projection_sync": []any{JsonObject{
					"target_path": "categories", "source_path": "raw_categories",
					"reason": "test", "approved_by": "unit",
				}},
				"projection_fill": []any{JsonObject{
					"path": "profile", "source": "raw_profile",
					"reason": "test", "approved_by": "unit",
				}},
				"projection_omit_if": []any{
					runtimePolicyEntry("ports[].end", JsonObject{"values": []any{float64(0), nil}}),
				},
			},
		},
	}, "<memory>")
	if err != nil {
		t.Fatalf("NewDriftPolicy(complete non-plan modes) error = %v, want nil", err)
	}
	for _, mode := range []PolicyMode{
		PolicyProjectionOmit, PolicyProjectionSync, PolicyProjectionFill, PolicyProjectionOmitIf,
	} {
		if got := len(policy.Entries("sample_resource", mode)); got != 1 {
			t.Errorf("Entries(sample_resource, %s) length = %d, want 1", mode, got)
		}
	}
}

func TestDriftPolicyAcceptsLosslessNumericPolicyAndRejectsEquivalentDuplicateScopes(t *testing.T) {
	parsed, err := canonjson.ParseDataJSONLosslessly(`{
		"version": 10e-1,
		"resource_types": {
			"sample_resource": {
				"projection_omit_if": [{
					"path": "quota",
					"values": [900719925474099312345678901],
					"reason": "test",
					"approved_by": "unit"
				}]
			}
		}
	}`)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(lossless policy) error = %v, want nil", err)
	}
	policy, err := NewDriftPolicy(parsed, "lossless policy")
	if err != nil {
		t.Fatalf("NewDriftPolicy(lossless policy) error = %v, want nil", err)
	}
	entries := policy.Entries("sample_resource", PolicyProjectionOmitIf)
	if len(entries) != 1 {
		t.Fatalf("Entries(sample_resource, projection_omit_if) length = %d, want 1", len(entries))
	}
	values, ok := entries[0].Data()["values"].([]any)
	if !ok || len(values) != 1 || values[0] != json.Number("900719925474099312345678901") {
		t.Errorf("lossless policy values = %#v, want preserved json.Number", values)
	}

	roundedNearOne, err := canonjson.ParseDataJSONLosslessly(`{"version":1.0000000000000000000000001,"resource_types":{}}`)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(near-one policy) error = %v, want nil", err)
	}
	if _, err := NewDriftPolicy(roundedNearOne, "near-one policy"); err == nil || !strings.Contains(err.Error(), "unsupported drift policy version") {
		t.Errorf("NewDriftPolicy(near-one policy) error = %v, want unsupported-version error", err)
	}

	duplicateNumbers, err := canonjson.ParseDataJSONLosslessly(`{
		"version": 1,
		"resource_types": {
			"sample_resource": {
				"projection_omit_if": [
					{"path":"quota","values":[0],"reason":"first","approved_by":"unit"},
					{"path":"quota","values":[0.0],"reason":"second","approved_by":"unit"}
				]
			}
		}
	}`)
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(duplicate numeric scopes) error = %v, want nil", err)
	}
	if _, err := NewDriftPolicy(duplicateNumbers, "duplicate numeric scopes"); err == nil || !strings.Contains(err.Error(), "duplicate projection_omit_if") {
		t.Errorf("NewDriftPolicy(duplicate numeric scopes) error = %v, want duplicate-scope error", err)
	}
}

func TestDriftPolicyNumericDuplicateScopeMarkers(t *testing.T) {
	tests := []struct {
		name          string
		first         string
		second        string
		wantDuplicate bool
	}{
		{name: "integral spellings", first: "0", second: "0.0", wantDuplicate: true},
		{name: "signed zero", first: "-0", second: "0.0", wantDuplicate: true},
		{name: "fractional spellings", first: "1e-1", second: "0.1", wantDuplicate: true},
		{name: "unsafe integral spelling", first: "9007199254740992", second: "9007199254740992.0", wantDuplicate: true},
		{name: "boolean and number", first: "false", second: "0", wantDuplicate: false},
		{name: "string and number", first: `"0"`, second: "0", wantDuplicate: false},
		{name: "different fractions", first: "0.1", second: "0.2", wantDuplicate: false},
		{name: "exact unsafe integer versus rounded float", first: "9007199254740993", second: "9007199254740992.0", wantDuplicate: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text := `{"version":1,"resource_types":{"sample_resource":{"projection_omit_if":[` +
				`{"path":"quota","values":[` + test.first + `],"reason":"first","approved_by":"unit"},` +
				`{"path":"quota","values":[` + test.second + `],"reason":"second","approved_by":"unit"}` +
				`]}}}`
			data, err := canonjson.ParseDataJSONLosslessly(text)
			if err != nil {
				t.Fatalf("ParseDataJSONLosslessly(%s) error = %v, want nil", text, err)
			}
			_, err = NewDriftPolicy(data, test.name)
			gotDuplicate := err != nil && strings.Contains(err.Error(), "duplicate projection_omit_if")
			if gotDuplicate != test.wantDuplicate {
				t.Errorf("NewDriftPolicy(%s, %s) duplicate = %t (error %v), want %t", test.first, test.second, gotDuplicate, err, test.wantDuplicate)
			}
		})
	}

	nonFinite := JsonObject{
		"version": float64(1),
		"resource_types": JsonObject{
			"sample_resource": JsonObject{
				"projection_omit_if": []any{
					runtimePolicyEntry("quota", JsonObject{"values": []any{math.Inf(1)}}),
				},
			},
		},
	}
	if _, err := NewDriftPolicy(nonFinite, "non-finite native number"); err == nil || !strings.Contains(err.Error(), "only JSON scalars") {
		t.Errorf("NewDriftPolicy(non-finite native number) error = %v, want JSON-scalar error", err)
	}
}

func TestDriftPolicyPlanToleranceTracksStaleEntries(t *testing.T) {
	policy := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"plan_tolerate": []any{runtimePolicyEntry("rules[].status", nil)},
		},
	})
	wantStale := []StalePolicyEntry{{
		ResourceType: "sample_resource",
		Mode:         PolicyPlanTolerate,
		Path:         "rules[].status",
	}}
	if got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyPlanTolerate}}); !reflect.DeepEqual(got, wantStale) {
		t.Errorf("StaleEntries(plan_tolerate) = %#v, want %#v", got, wantStale)
	}
	if policy.ToleratesPlanPath("sample_resource", []any{"rules", 0, "status"}, "delete") {
		t.Error("ToleratesPlanPath(sample_resource, rules[0].status, delete) = true, want false")
	}
	if policy.ToleratesPlanPath("sample_resource", []any{"rules", "0", "status"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, rules.\"0\".status, update) = true, want false")
	}
	if !policy.ToleratesPlanPath("sample_resource", []any{"rules", 0, "status"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, rules[0].status, update) = false, want true")
	}
	if got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyPlanTolerate}}); len(got) != 0 {
		t.Errorf("StaleEntries(plan_tolerate) after match = %#v, want []", got)
	}
}

// TestDriftPolicyPlanAssessmentToleranceVector ports the drift-policy unit
// vector from "policy bytes drive tolerated classification and stale-entry
// reporting" in the original test corpus. Filesystem hashing,
// Terraform execution, and report assembly belong to later parcels.
func TestDriftPolicyPlanAssessmentToleranceVector(t *testing.T) {
	policy := newRuntimePolicy(t, JsonObject{
		"zpa_sample": JsonObject{
			"plan_tolerate": []any{
				runtimePolicyEntry("status", JsonObject{"reason": "known", "approved_by": "owner"}),
				runtimePolicyEntry("unused", JsonObject{"reason": "stale", "approved_by": "owner"}),
			},
		},
	})
	if !policy.ToleratesPlanPath("zpa_sample", []any{"status"}, "update") {
		t.Error("ToleratesPlanPath(zpa_sample, status, update) = false, want true")
	}
	want := []StalePolicyEntry{{
		ResourceType: "zpa_sample",
		Mode:         PolicyPlanTolerate,
		Path:         "unused",
	}}
	got := policy.StaleEntries(StaleEntriesOptions{
		ResourceTypes: map[string]struct{}{"zpa_sample": {}},
		Modes:         []PolicyMode{PolicyPlanTolerate},
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StaleEntries(plan-assessment vector) = %#v, want %#v", got, want)
	}
}

// TestDriftPolicyPlanPolicyRuntimeVectors ports the two DriftPolicy-only
// assertions from "drift policy is parsed from and later bound to exact stable
// bytes" and "an absent policy has no mutable file evidence" in
// the original test corpus. File binding and recheck behavior belong to
// the future policy-file consumer.
func TestDriftPolicyPlanPolicyRuntimeVectors(t *testing.T) {
	policy := newRuntimePolicy(t, JsonObject{
		"zpa_sample": JsonObject{
			"plan_tolerate": []any{runtimePolicyEntry("status", nil)},
		},
	})
	if !policy.ToleratesPlanPath("zpa_sample", []any{"status"}, "update") {
		t.Error("ToleratesPlanPath(zpa_sample, status, update) = false, want true")
	}
	empty, err := NewDriftPolicy(nil, "<memory>")
	if err != nil {
		t.Fatalf("NewDriftPolicy(nil) error = %v, want nil", err)
	}
	if got := empty.StaleEntries(StaleEntriesOptions{}); len(got) != 0 {
		t.Errorf("NewDriftPolicy(nil).StaleEntries({}) = %#v, want []", got)
	}
}

// TestDriftPolicyPlanEvalPathVectors ports the matcher-level inputs from
// "prototype-like own keys cannot disappear behind tolerated drift" and
// "partial tolerance reports only unmatched paths in Python order" in
// the original test corpus.
func TestDriftPolicyPlanEvalPathVectors(t *testing.T) {
	prototype := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"plan_tolerate": []any{runtimePolicyEntry("a", nil)},
		},
	})
	if prototype.ToleratesPlanPath("sample_resource", []any{"__proto__"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, __proto__, update) = true, want false")
	}
	if !prototype.ToleratesPlanPath("sample_resource", []any{"a"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, a, update) = false, want true")
	}

	partial := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"plan_tolerate": []any{runtimePolicyEntry("rules[2].status", nil)},
		},
	})
	if !partial.ToleratesPlanPath("sample_resource", []any{"rules", 2, "status"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, rules[2].status, update) = false, want true")
	}
	if partial.ToleratesPlanPath("sample_resource", []any{"rules", 10, "status"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, rules[10].status, update) = true, want false")
	}

	// Ports the two special-path non-matches from "identity and sensitivity
	// deltas cannot hide behind tolerated drift" in plan-eval.test.ts. The
	// evaluator owns the decision to synthesize these paths before matching.
	special := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"plan_tolerate": []any{runtimePolicyEntry("status", nil)},
		},
	})
	for _, path := range []string{"<identity_change>", "<sensitivity_change>"} {
		if special.ToleratesPlanPath("sample_resource", []any{path}, "update") {
			t.Errorf("ToleratesPlanPath(sample_resource, %s, update) = true, want false", path)
		}
	}
}

// TestDriftPolicyPlanEvalWildcardStaleVector ports the matcher and stale-entry
// observations from "policy classification and stale tracking match Python"
// in the original test corpus.
func TestDriftPolicyPlanEvalWildcardStaleVector(t *testing.T) {
	policy := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"plan_tolerate": []any{
				runtimePolicyEntry("rules[].status", JsonObject{"actions": []any{"update"}}),
				runtimePolicyEntry("unused", nil),
			},
		},
	})
	if !policy.ToleratesPlanPath("sample_resource", []any{"rules", 0, "status"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, rules[0].status, update) = false, want true")
	}
	want := []StalePolicyEntry{{
		ResourceType: "sample_resource", Mode: PolicyPlanTolerate, Path: "unused",
	}}
	if got := policy.StaleEntries(StaleEntriesOptions{
		ResourceTypes: map[string]struct{}{"sample_resource": {}},
		Modes:         []PolicyMode{PolicyPlanTolerate},
	}); !reflect.DeepEqual(got, want) {
		t.Errorf("StaleEntries(plan-eval wildcard vector) = %#v, want %#v", got, want)
	}
}

// TestDriftPolicyStateProjectRuntimeVectors ports the DriftPolicy-only
// observations from "sensitivity masks are validated completely before
// projection and may be explicitly omitted" and "projection policy order is
// sync, fill, then conditional omit" in the original test corpus.
func TestDriftPolicyStateProjectRuntimeVectors(t *testing.T) {
	policy := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"projection_omit": []any{runtimePolicyEntry("secret", nil)},
			"projection_sync": []any{JsonObject{
				"target_path": "target_categories", "source_path": "source_categories",
				"reason": "mirrored", "approved_by": "unit",
			}},
			"projection_fill": []any{JsonObject{
				"path": "filled", "source": "rawFilled",
				"reason": "omitted", "approved_by": "unit",
			}},
			"projection_omit_if": []any{
				runtimePolicyEntry("filled", JsonObject{"values": []any{"DROP"}}),
				runtimePolicyEntry("number_value", JsonObject{"values": []any{false}}),
			},
		},
	})
	if !policy.ProjectionOmits("sample_resource", []any{"secret"}) {
		t.Error("ProjectionOmits(sample_resource, secret) = false, want true")
	}
	for _, mode := range []PolicyMode{PolicyProjectionSync, PolicyProjectionFill} {
		entries := policy.Entries("sample_resource", mode)
		if len(entries) != 1 {
			t.Fatalf("Entries(sample_resource, %s) length = %d, want 1", mode, len(entries))
		}
		policy.MarkMatched(entries[0])
	}
	conditional := policy.Entries("sample_resource", PolicyProjectionOmitIf)
	if len(conditional) != 2 {
		t.Fatalf("Entries(sample_resource, projection_omit_if) length = %d, want 2", len(conditional))
	}
	policy.MarkMatched(conditional[0])
	want := []StalePolicyEntry{{
		ResourceType: "sample_resource", Mode: PolicyProjectionOmitIf, Path: "number_value",
	}}
	if got := policy.StaleEntries(StaleEntriesOptions{}); !reflect.DeepEqual(got, want) {
		t.Errorf("StaleEntries(state-project vector) = %#v, want %#v", got, want)
	}
}

func markRuntimeEntries(t *testing.T, policy *DriftPolicy, resourceType string, mode PolicyMode, indexes ...int) {
	t.Helper()
	entries := policy.Entries(resourceType, mode)
	for _, index := range indexes {
		if index < 0 || index >= len(entries) {
			t.Fatalf("Entries(%s, %s) index %d outside length %d", resourceType, mode, index, len(entries))
		}
		policy.MarkMatched(entries[index])
	}
}

// TestDriftPolicyGeneratedConfigAccountingVectors ports all seven explicit
// staleEntries assertions in the original test corpus. The
// consumer-owned HCL/schema/value logic decides which handles to mark; these
// subtests pin the resulting DriftPolicy identity accounting only.
func TestDriftPolicyGeneratedConfigAccountingVectors(t *testing.T) {
	t.Run("scalar wildcard and fill matched", func(t *testing.T) {
		policy := newRuntimePolicy(t, JsonObject{
			"sample_resource": JsonObject{
				"projection_omit": []any{runtimePolicyEntry("description", nil)},
				"projection_fill": []any{JsonObject{
					"path": "filled", "source": "rawFilled",
					"reason": "test", "approved_by": "unit",
				}},
				"projection_omit_if": []any{
					runtimePolicyEntry("rules[*].order", JsonObject{"values": []any{float64(0)}}),
				},
			},
		})
		for _, mode := range []PolicyMode{PolicyProjectionOmit, PolicyProjectionFill, PolicyProjectionOmitIf} {
			markRuntimeEntries(t, policy, "sample_resource", mode, 0)
		}
		if got := policy.StaleEntries(StaleEntriesOptions{}); len(got) != 0 {
			t.Errorf("StaleEntries(generated-config matched vector) = %#v, want []", got)
		}
	})

	t.Run("pack default wins and policy stays stale", func(t *testing.T) {
		policy := newRuntimePolicy(t, JsonObject{
			"sample_resource": JsonObject{
				"projection_omit_if": []any{
					runtimePolicyEntry("size_quota", JsonObject{"values": []any{float64(0)}}),
				},
			},
		})
		want := []StalePolicyEntry{{
			ResourceType: "sample_resource", Mode: PolicyProjectionOmitIf, Path: "size_quota",
		}}
		if got := policy.StaleEntries(StaleEntriesOptions{}); !reflect.DeepEqual(got, want) {
			t.Errorf("StaleEntries(pack-default precedence vector) = %#v, want %#v", got, want)
		}
	})

	t.Run("sibling policies account independently", func(t *testing.T) {
		first := newRuntimePolicy(t, JsonObject{
			"sample_resource": JsonObject{
				"projection_omit": []any{runtimePolicyEntry("description", nil)},
			},
		})
		sibling := newRuntimePolicy(t, JsonObject{
			"sibling_resource": JsonObject{
				"projection_omit_if": []any{
					runtimePolicyEntry("description", JsonObject{"values": []any{"SIBLING"}}),
				},
			},
		})
		markRuntimeEntries(t, first, "sample_resource", PolicyProjectionOmit, 0)
		markRuntimeEntries(t, sibling, "sibling_resource", PolicyProjectionOmitIf, 0)
		if got := first.StaleEntries(StaleEntriesOptions{}); len(got) != 0 {
			t.Errorf("first StaleEntries(sibling vector) = %#v, want []", got)
		}
		if got := sibling.StaleEntries(StaleEntriesOptions{}); len(got) != 0 {
			t.Errorf("sibling StaleEntries(sibling vector) = %#v, want []", got)
		}
	})

	t.Run("exact index is deferred and stays stale", func(t *testing.T) {
		policy := newRuntimePolicy(t, JsonObject{
			"sample_resource": JsonObject{
				"projection_omit": []any{runtimePolicyEntry("rules[0].order", nil)},
			},
		})
		want := []StalePolicyEntry{{
			ResourceType: "sample_resource", Mode: PolicyProjectionOmit, Path: "rules[0].order",
		}}
		if got := policy.StaleEntries(StaleEntriesOptions{}); !reflect.DeepEqual(got, want) {
			t.Errorf("StaleEntries(exact-index deferred vector) = %#v, want %#v", got, want)
		}
	})

	t.Run("fill then conditional omit are both matched", func(t *testing.T) {
		policy := newRuntimePolicy(t, JsonObject{
			"sample_resource": JsonObject{
				"projection_fill": []any{JsonObject{
					"path": "description", "source": "rawDescription",
					"reason": "test", "approved_by": "unit",
				}},
				"projection_omit_if": []any{
					runtimePolicyEntry("description", JsonObject{"values": []any{"DROP"}}),
				},
			},
		})
		markRuntimeEntries(t, policy, "sample_resource", PolicyProjectionFill, 0)
		markRuntimeEntries(t, policy, "sample_resource", PolicyProjectionOmitIf, 0)
		if got := policy.StaleEntries(StaleEntriesOptions{}); len(got) != 0 {
			t.Errorf("StaleEntries(fill-then-omit vector) = %#v, want []", got)
		}
	})

	t.Run("unknown compound value leaves policy stale", func(t *testing.T) {
		policy := newRuntimePolicy(t, JsonObject{
			"sample_resource": JsonObject{
				"projection_omit_if": []any{
					runtimePolicyEntry("description", JsonObject{"values": []any{"DROP"}}),
				},
			},
		})
		want := []StalePolicyEntry{{
			ResourceType: "sample_resource", Mode: PolicyProjectionOmitIf, Path: "description",
		}}
		if got := policy.StaleEntries(StaleEntriesOptions{}); !reflect.DeepEqual(got, want) {
			t.Errorf("StaleEntries(unknown-compound vector) = %#v, want %#v", got, want)
		}
	})
}

// TestDriftPolicyImportOracleAccountingVectors ports the two explicit
// projection_omit staleEntries assertions in the original test corpus.
// Terraform command sequencing and corrected-plan authorization remain owned
// by the future import-oracle consumer.
func TestDriftPolicyImportOracleAccountingVectors(t *testing.T) {
	for _, name := range []string{
		"provider batch applies per-type generated-config policy",
		"policy edits generated config before authorization",
	} {
		t.Run(name, func(t *testing.T) {
			policy := newRuntimePolicy(t, JsonObject{
				"sample_resource": JsonObject{
					"projection_omit": []any{runtimePolicyEntry("description", nil)},
				},
			})
			markRuntimeEntries(t, policy, "sample_resource", PolicyProjectionOmit, 0)
			if got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyProjectionOmit}}); len(got) != 0 {
				t.Errorf("StaleEntries(import-oracle %q vector) = %#v, want []", name, got)
			}
		})
	}
}

func TestDriftPolicyProjectionOmitPreservesFirstMatchOrder(t *testing.T) {
	policy := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"projection_omit": []any{
				runtimePolicyEntry("field[]", JsonObject{"reason": "wildcard"}),
				runtimePolicyEntry("field[0]", JsonObject{"reason": "exact"}),
			},
		},
	})
	if !policy.ProjectionOmits("sample_resource", []any{"field", 0}) {
		t.Error("ProjectionOmits(sample_resource, field[0]) = false, want true")
	}
	want := []StalePolicyEntry{{
		ResourceType: "sample_resource", Mode: PolicyProjectionOmit, Path: "field[0]",
	}}
	if got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyProjectionOmit}}); !reflect.DeepEqual(got, want) {
		t.Errorf("StaleEntries(projection_omit first-match vector) = %#v, want %#v", got, want)
	}
}

func TestDriftPolicyPlanTolerancePreservesSourcePrecedence(t *testing.T) {
	tests := []struct {
		name    string
		entries []any
		want    []StalePolicyEntry
	}{
		{
			name: "exact before wildcard and canonical alias",
			entries: []any{
				runtimePolicyEntry("field[0]", JsonObject{"reason": "exact"}),
				runtimePolicyEntry("field[00]", JsonObject{"reason": "alias"}),
				runtimePolicyEntry("field[]", JsonObject{"reason": "wildcard"}),
			},
			want: []StalePolicyEntry{
				{ResourceType: "sample_resource", Mode: PolicyPlanTolerate, Path: "field[00]"},
				{ResourceType: "sample_resource", Mode: PolicyPlanTolerate, Path: "field[]"},
			},
		},
		{
			name: "wildcard before exact",
			entries: []any{
				runtimePolicyEntry("field[]", JsonObject{"reason": "wildcard"}),
				runtimePolicyEntry("field[0]", JsonObject{"reason": "exact"}),
			},
			want: []StalePolicyEntry{
				{ResourceType: "sample_resource", Mode: PolicyPlanTolerate, Path: "field[0]"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := newRuntimePolicy(t, JsonObject{
				"sample_resource": JsonObject{"plan_tolerate": test.entries},
			})
			if !policy.ToleratesPlanPath("sample_resource", []any{"field", 0}, "update") {
				t.Error("ToleratesPlanPath(sample_resource, field[0], update) = false, want true")
			}
			got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyPlanTolerate}})
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("StaleEntries(plan_tolerate) = %#v, want %#v", got, test.want)
			}
		})
	}
}

// Repeating the policy-paths esbuild/symlink setup recorded above, this exact
// probe observed true for -1 and -0, and false for 1.5, NaN, and Infinity:
//
//	node -e 'import("file:///tmp/probe.mjs").then(({parsePolicyPath,policySelectorMatches})=>{const s=parsePolicyPath("items[]");console.log([-1,-0,1.5,NaN,Infinity].map((n)=>policySelectorMatches(s,["items",n])));})'
//
// These cases pin JavaScript Number.isInteger rather than a non-negative
// array-bound check.
func TestDriftPolicySelectorEdgeCases(t *testing.T) {
	policy := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"plan_tolerate": []any{
				runtimePolicyEntry(`labels["x/string:y"]`, nil),
				runtimePolicyEntry(`quoted["*"]`, nil),
				runtimePolicyEntry("huge[9007199254740992]", nil),
			},
		},
	})
	tests := []struct {
		name string
		path []any
		want bool
	}{
		{name: "segment boundary collision rejected", path: []any{"labels", "x", "y"}, want: false},
		{name: "quoted key exact", path: []any{"labels", "x/string:y"}, want: true},
		{name: "quoted star retains wildcard ambiguity", path: []any{"quoted", -1}, want: true},
		{name: "wildcard accepts negative zero", path: []any{"quoted", math.Copysign(0, -1)}, want: true},
		{name: "quoted star does not match string star", path: []any{"quoted", "*"}, want: false},
		{name: "wildcard rejects fraction", path: []any{"quoted", 1.5}, want: false},
		{name: "wildcard rejects NaN", path: []any{"quoted", math.NaN()}, want: false},
		{name: "wildcard rejects infinity", path: []any{"quoted", math.Inf(1)}, want: false},
		{name: "wildcard rejects lossless token", path: []any{"quoted", json.Number("1")}, want: false},
		{name: "bigint selector never equals concrete number", path: []any{"huge", int64(9007199254740992)}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := policy.ToleratesPlanPath("sample_resource", test.path, "update")
			if got != test.want {
				t.Errorf("ToleratesPlanPath(sample_resource, %#v, update) = %t, want %t", test.path, got, test.want)
			}
		})
	}
}

// TypeScript's readonly surface is not frozen at runtime. Projection matching
// and stale display read later mutations, while plan-tolerance selectors were
// compiled by the constructor. This Go boundary deliberately uses one simpler,
// stricter snapshot contract. From the repository root, with /tmp/node_modules
// absent, the exact source observation used:
//
// ln -s "$PWD/node_modules" /tmp/node_modules
//
//	npx esbuild the original implementation --bundle --format=esm --external:lossless-json --outfile=/tmp/probe.mjs
//	node -e 'import("file:///tmp/probe.mjs").then(({DriftPolicy})=>{const make=(path)=>({path,reason:"r",approved_by:"a"});const projectionEntry=make("before");const projection=new DriftPolicy({version:1,resource_types:{sample_resource:{projection_omit:[projectionEntry]}}});projectionEntry.path="after";const planEntry=make("before");const plan=new DriftPolicy({version:1,resource_types:{sample_resource:{plan_tolerate:[planEntry]}}});planEntry.path="after";const staleEntry=make("before");const staleDisplay=new DriftPolicy({version:1,resource_types:{sample_resource:{plan_tolerate:[staleEntry]}}});staleEntry.path="after";const appendedResource={projection_omit:[],plan_tolerate:[]};const appended=new DriftPolicy({version:1,resource_types:{sample_resource:appendedResource}});appendedResource.projection_omit.push(make("projection_late"));appendedResource.plan_tolerate.push(make("plan_late"));const removedResource={projection_omit:[make("projection_existing")],plan_tolerate:[make("plan_existing")]};const removed=new DriftPolicy({version:1,resource_types:{sample_resource:removedResource}});removedResource.projection_omit.length=0;removedResource.plan_tolerate.length=0;console.log(JSON.stringify({path_mutation:{projection_old:projection.projectionOmits("sample_resource",["before"]),projection_new:projection.projectionOmits("sample_resource",["after"]),plan_old:plan.toleratesPlanPath("sample_resource",["before"],"update"),plan_new:plan.toleratesPlanPath("sample_resource",["after"],"update"),stale_display:staleDisplay.staleEntries({modes:["plan_tolerate"]})},append_after_construction:{projection:appended.projectionOmits("sample_resource",["projection_late"]),plan:appended.toleratesPlanPath("sample_resource",["plan_late"],"update"),stale:appended.staleEntries()},remove_after_construction:{projection:removed.projectionOmits("sample_resource",["projection_existing"]),plan:removed.toleratesPlanPath("sample_resource",["plan_existing"],"update"),stale:removed.staleEntries()}}));})'
//
// unlink /tmp/node_modules
//
// Node v24.15.0 printed path_mutation {projection_old:false,
// projection_new:true, plan_old:true, plan_new:false, stale_display path
// "after"}; appended projection matched but appended plan did not; removed
// projection did not match, removed plan did, and its stale list was empty.
// The test below pins the intentionally narrower immutable Go contract.
func TestDriftPolicySnapshotsInputAndDetachesOutputs(t *testing.T) {
	values := []any{float64(0), nil}
	entry := runtimePolicyEntry("rules[].status", JsonObject{"values": values})
	compiledPlanEntry := runtimePolicyEntry("plan_before", nil)
	stalePlanEntry := runtimePolicyEntry("stale_before", nil)
	data := JsonObject{
		"version": float64(1),
		"resource_types": JsonObject{
			"sample_resource": JsonObject{
				"projection_omit_if": []any{entry},
				"projection_omit":    []any{runtimePolicyEntry("description", nil)},
				"plan_tolerate": []any{
					compiledPlanEntry,
					stalePlanEntry,
				},
			},
		},
	}
	policy, err := NewDriftPolicy(data, "snapshot")
	if err != nil {
		t.Fatalf("NewDriftPolicy(snapshot input) error = %v, want nil", err)
	}

	entry["path"] = "mutated"
	values[0] = float64(99)
	compiledPlanEntry["path"] = "plan_after"
	stalePlanEntry["path"] = "stale_after"
	resources := data["resource_types"].(JsonObject)
	resource := resources["sample_resource"].(JsonObject)
	resource["projection_omit"] = []any{runtimePolicyEntry("projection_late", nil)}
	resource["plan_tolerate"] = append(
		resource["plan_tolerate"].([]any),
		runtimePolicyEntry("plan_late", nil),
	)

	if !policy.ProjectionOmits("sample_resource", []any{"description"}) {
		t.Error("ProjectionOmits(sample_resource, description) after input mutation = false, want true")
	}
	if policy.ProjectionOmits("sample_resource", []any{"projection_late"}) {
		t.Error("ProjectionOmits(sample_resource, projection_late) after input append = true, want false")
	}
	if !policy.ToleratesPlanPath("sample_resource", []any{"plan_before"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, plan_before, update) after input mutation = false, want true")
	}
	if policy.ToleratesPlanPath("sample_resource", []any{"plan_after"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, plan_after, update) after input mutation = true, want false")
	}
	if policy.ToleratesPlanPath("sample_resource", []any{"plan_late"}, "update") {
		t.Error("ToleratesPlanPath(sample_resource, plan_late, update) after input append = true, want false")
	}
	wantStalePlan := []StalePolicyEntry{{
		ResourceType: "sample_resource",
		Mode:         PolicyPlanTolerate,
		Path:         "stale_before",
	}}
	if got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyPlanTolerate}}); !reflect.DeepEqual(got, wantStalePlan) {
		t.Errorf("StaleEntries(plan_tolerate) after input mutation = %#v, want %#v", got, wantStalePlan)
	}
	conditional := policy.Entries("sample_resource", PolicyProjectionOmitIf)
	if got := len(conditional); got != 1 {
		t.Fatalf("Entries(sample_resource, projection_omit_if) length = %d, want 1", got)
	}
	entryCopy := conditional[0].Data()
	entryCopy["path"] = "copy-mutated"
	entryCopy["values"].([]any)[0] = float64(42)
	if got := conditional[0].Data()["path"]; got != "rules[].status" {
		t.Errorf("PolicyEntry.Data().path after detached mutation = %v, want %q", got, "rules[].status")
	}
	if got := conditional[0].Data()["values"]; !reflect.DeepEqual(got, []any{float64(0), nil}) {
		t.Errorf("PolicyEntry.Data().values after detached mutation = %#v, want %#v", got, []any{float64(0), nil})
	}

	returned := policy.Entries("sample_resource", PolicyProjectionOmitIf)
	returned[0] = PolicyEntry{}
	if got := len(policy.Entries("sample_resource", PolicyProjectionOmitIf)); got != 1 {
		t.Errorf("Entries(sample_resource, projection_omit_if) after slice mutation length = %d, want 1", got)
	}
}

// Node v24.15.0's WeakSet crosses policy instances only for the same raw entry
// object. From the repository root, with /tmp/node_modules absent, this probe:
//
// ln -s "$PWD/node_modules" /tmp/node_modules
// npx esbuild the original implementation --bundle --format=esm --external:lossless-json --outfile=/tmp/probe.mjs
// node -e 'import("file:///tmp/probe.mjs").then(({DriftPolicy})=>{const make=()=>({path:"field",reason:"r",approved_by:"a"});const shared=make();const data={version:1,resource_types:{sample_resource:{projection_omit:[shared]}}};const sameA=new DriftPolicy(data);const sameB=new DriftPolicy(data);sameA.markMatched(sameB.entries("sample_resource","projection_omit")[0]);const separateA=new DriftPolicy({version:1,resource_types:{sample_resource:{projection_omit:[make()]}}});const separateB=new DriftPolicy({version:1,resource_types:{sample_resource:{projection_omit:[make()]}}});separateA.markMatched(separateB.entries("sample_resource","projection_omit")[0]);console.log(JSON.stringify({same_input:sameA.staleEntries(),separate_input:separateA.staleEntries()}));})'
// unlink /tmp/node_modules
//
// printed {"same_input":[],"separate_input":[{"resource_type":
// "sample_resource","mode":"projection_omit","path":"field"}]}.
func TestDriftPolicyMarkMatchedUsesEntryIdentity(t *testing.T) {
	resourceTypes := JsonObject{
		"sample_resource": JsonObject{
			"projection_sync": []any{
				JsonObject{
					"target_path": "first",
					"source_path": "raw_first",
					"reason":      "test", "approved_by": "unit",
				},
				JsonObject{
					"target_path": "second",
					"source_path": "raw_second",
					"reason":      "test", "approved_by": "unit",
				},
			},
		},
	}
	policy := newRuntimePolicy(t, resourceTypes)
	other := newRuntimePolicy(t, resourceTypes)
	separate := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"projection_sync": []any{
				JsonObject{
					"target_path": "first",
					"source_path": "raw_first",
					"reason":      "test", "approved_by": "unit",
				},
			},
		},
	})
	policy.MarkMatched(PolicyEntry{})
	policy.MarkMatched(separate.Entries("sample_resource", PolicyProjectionSync)[0])
	if got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyProjectionSync}}); len(got) != 2 {
		t.Errorf("StaleEntries(projection_sync) after zero and equal-but-distinct marks length = %d, want 2; entries = %#v", len(got), got)
	}
	policy.MarkMatched(other.Entries("sample_resource", PolicyProjectionSync)[0])
	want := []StalePolicyEntry{{
		ResourceType: "sample_resource",
		Mode:         PolicyProjectionSync,
		Path:         "second",
	}}
	if got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyProjectionSync}}); !reflect.DeepEqual(got, want) {
		t.Errorf("StaleEntries(projection_sync) after shared-source foreign mark = %#v, want %#v", got, want)
	}
}

func TestDriftPolicyPreservesSharedSourceEntryIdentity(t *testing.T) {
	shared := runtimePolicyEntry("field", nil)
	policy := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{
			"projection_omit": []any{shared},
			"plan_tolerate":   []any{shared},
		},
	})
	omit := policy.Entries("sample_resource", PolicyProjectionOmit)
	plan := policy.Entries("sample_resource", PolicyPlanTolerate)
	if len(omit) != 1 || len(plan) != 1 {
		t.Fatalf("Entries(shared source record) lengths = omit %d, plan %d; want 1, 1", len(omit), len(plan))
	}
	if omit[0] != plan[0] {
		t.Error("Entries(shared source record) returned distinct identity handles, want one source identity")
	}
	if !policy.ProjectionOmits("sample_resource", []any{"field"}) {
		t.Error("ProjectionOmits(sample_resource, field) = false, want true")
	}
	if got := policy.StaleEntries(StaleEntriesOptions{}); len(got) != 0 {
		t.Errorf("StaleEntries({}) after matching shared record = %#v, want []", got)
	}
}

// TestDriftPolicyStaleFiltersPreserveSourceOrder includes both vectors from
// "empty stale filters preserve the Python all-entry default".
func TestDriftPolicyStaleFiltersPreserveSourceOrder(t *testing.T) {
	policy := newRuntimePolicy(t, JsonObject{
		"z_resource": JsonObject{
			"projection_omit": []any{runtimePolicyEntry("z_path", nil)},
		},
		"a_resource": JsonObject{
			"projection_omit": []any{runtimePolicyEntry("a_path", nil)},
			"plan_tolerate":   []any{runtimePolicyEntry("plan_path", nil)},
		},
	})
	wantAll := []StalePolicyEntry{
		{ResourceType: "a_resource", Mode: PolicyProjectionOmit, Path: "a_path"},
		{ResourceType: "a_resource", Mode: PolicyPlanTolerate, Path: "plan_path"},
		{ResourceType: "z_resource", Mode: PolicyProjectionOmit, Path: "z_path"},
	}
	if got := policy.StaleEntries(StaleEntriesOptions{ResourceTypes: map[string]struct{}{}}); !reflect.DeepEqual(got, wantAll) {
		t.Errorf("StaleEntries(empty resource filter) = %#v, want %#v", got, wantAll)
	}
	if got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{}}); !reflect.DeepEqual(got, wantAll) {
		t.Errorf("StaleEntries(empty mode filter) = %#v, want %#v", got, wantAll)
	}
	wantFiltered := []StalePolicyEntry{
		{ResourceType: "a_resource", Mode: PolicyPlanTolerate, Path: "plan_path"},
		{ResourceType: "a_resource", Mode: PolicyProjectionOmit, Path: "a_path"},
	}
	got := policy.StaleEntries(StaleEntriesOptions{
		ResourceTypes: map[string]struct{}{"a_resource": {}},
		Modes:         []PolicyMode{PolicyPlanTolerate, PolicyProjectionOmit},
	})
	if !reflect.DeepEqual(got, wantFiltered) {
		t.Errorf("StaleEntries(filtered reversed modes) = %#v, want %#v", got, wantFiltered)
	}
}

func TestDriftPolicyConcurrentMatchAccounting(t *testing.T) {
	entries := make([]any, 64)
	for index := range entries {
		entries[index] = runtimePolicyEntry("field["+strconv.Itoa(index)+"]", nil)
	}
	policy := newRuntimePolicy(t, JsonObject{
		"sample_resource": JsonObject{"plan_tolerate": entries},
	})

	var wait sync.WaitGroup
	for index := range entries {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if !policy.ToleratesPlanPath("sample_resource", []any{"field", index}, "update") {
				t.Errorf("ToleratesPlanPath(sample_resource, field[%d], update) = false, want true", index)
			}
			_ = policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyPlanTolerate}})
		}()
	}
	wait.Wait()
	if got := policy.StaleEntries(StaleEntriesOptions{Modes: []PolicyMode{PolicyPlanTolerate}}); len(got) != 0 {
		t.Errorf("StaleEntries(plan_tolerate) after concurrent matches = %#v, want []", got)
	}
}
