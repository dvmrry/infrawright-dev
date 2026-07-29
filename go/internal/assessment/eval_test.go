package assessment

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
)

func mustParseDataJSON(t *testing.T, text string) any {
	t.Helper()
	value, err := canonjson.ParseDataJSONLosslessly(text)
	if err != nil {
		t.Fatalf("canonjson.ParseDataJSONLosslessly(%q) error = %v, want nil", text, err)
	}
	return value
}

func mustPolicy(t *testing.T, value any) *metadata.DriftPolicy {
	t.Helper()
	policy, err := metadata.NewDriftPolicy(value, "<test-policy>")
	if err != nil {
		t.Fatalf("metadata.NewDriftPolicy(%#v) error = %v, want nil", value, err)
	}
	return policy
}

func TestDiffPathsAndTruthyPathsUseDeterministicOrdering(t *testing.T) {
	before := mustParseDataJSON(t, `{"a":[{"b":1}],"z":true}`)
	after := mustParseDataJSON(t, `{"a":[{"b":2}],"z":true}`)
	if got, want := DiffPaths(before, after), []PlanPath{{"a", 0, "b"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("DiffPaths(%#v, %#v) = %#v, want %#v", before, after, got, want)
	}
	if got := DiffPaths(map[string]any{}, map[string]any{"missing": nil}); len(got) != 0 {
		t.Errorf("DiffPaths({}, {missing:null}) = %#v, want []", got)
	}
	if got := DiffPaths([]any{}, []any{nil}); len(got) != 0 {
		t.Errorf("DiffPaths([], [null]) = %#v, want []", got)
	}
	mask := map[string]any{
		"z": true,
		"a": []any{false, map[string]any{"b": true}},
	}
	if got, want := TruthyPaths(mask), []PlanPath{{"a", 1, "b"}, {"z"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("TruthyPaths(%#v) = %#v, want %#v", mask, got, want)
	}
}

func TestClassifyPlanPreservesCoreAndPartialToleranceSemantics(t *testing.T) {
	planValue := mustParseDataJSON(t, `{
		"format_version":"1.2","complete":true,"errored":false,
		"resource_changes":[{
			"address":"sample_resource.this","type":"sample_resource",
			"change":{"actions":["update"],
				"before":{"rules":[
					{"status":"same"},{"status":"same"},{"status":"before"},
					{"status":"same"},{"status":"same"},{"status":"same"},
					{"status":"same"},{"status":"same"},{"status":"same"},
					{"status":"same"},{"status":"before"}
				]},
				"after":{"rules":[
					{"status":"same"},{"status":"same"},{"status":"after"},
					{"status":"same"},{"status":"same"},{"status":"same"},
					{"status":"same"},{"status":"same"},{"status":"same"},
					{"status":"same"},{"status":"after"}
				]}}
		}]
	}`)
	policyValue := mustParseDataJSON(t, `{
		"version":1,"resource_types":{"sample_resource":{"plan_tolerate":[{
			"path":"rules[2].status","reason":"test","approved_by":"unit"
		}]}}
	}`)
	unfiltered, err := ClassifyPlan(planValue, nil, nil)
	if err != nil {
		t.Fatalf("ClassifyPlan(unfiltered path order) error = %v, want nil", err)
	}
	if got, want := unfiltered.Findings[0].Paths, []PlanPath{
		{"rules", 10, "status"},
		{"rules", 2, "status"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("ClassifyPlan(unfiltered path order).Paths = %#v, want %#v", got, want)
	}
	classification, err := ClassifyPlan(planValue, mustPolicy(t, policyValue), nil)
	if err != nil {
		t.Fatalf("ClassifyPlan(partial tolerance) error = %v, want nil", err)
	}
	want := PlanClassification{
		Status: Blocked,
		Findings: []PlanFinding{{
			Status: Blocked, Source: "resource_changes", Address: "sample_resource.this",
			Actions: []string{"update"}, Paths: []PlanPath{{"rules", 10, "status"}},
			// A leaf inside a block collection keeps its positional path and
			// gains the values behind it.
			Changes: []PlanChange{{
				Path: PlanPath{"rules", 10, "status"}, Kind: ScalarChange,
				Before: "before", After: "after",
			}},
		}},
	}
	if !reflect.DeepEqual(classification, want) {
		t.Errorf("ClassifyPlan(partial tolerance) = %#v, want %#v", classification, want)
	}

	opaquePlan := mustParseDataJSON(t, `{
		"format_version":"1.2","complete":true,"errored":false,
		"resource_changes":[{"address":"sample_resource.this","type":"sample_resource",
		"change":{"actions":["update"],"before":{"a":"same"},"after":{"a":"same"}}}]
	}`)
	opaque, err := ClassifyPlan(opaquePlan, nil, nil)
	if err != nil {
		t.Fatalf("ClassifyPlan(opaque update) error = %v, want nil", err)
	}
	if got, want := opaque.Findings[0].Paths, []PlanPath{{OpaqueUpdate}}; !reflect.DeepEqual(got, want) {
		t.Errorf("ClassifyPlan(opaque update).Paths = %#v, want %#v", got, want)
	}

	importPlan := mustParseDataJSON(t, `{
		"format_version":"1.2","complete":true,"errored":false,
		"resource_changes":[{"address":"sample_resource.this","type":"sample_resource",
		"change":{"actions":["create"],"importing":{"id":"secret"}}}]
	}`)
	imported, err := ClassifyPlan(importPlan, nil, nil)
	if err != nil {
		t.Fatalf("ClassifyPlan(import) error = %v, want nil", err)
	}
	if imported.Status != Clean || len(imported.Findings) != 1 || imported.Findings[0].Status != Clean {
		t.Errorf("ClassifyPlan(import) = %#v, want clean finding", imported)
	}
}

func TestClassifyPlanRejectsIncompleteBeforePolicyMatching(t *testing.T) {
	policyValue := mustParseDataJSON(t, `{
		"version":1,"resource_types":{"sample_resource":{"plan_tolerate":[{
			"path":"status","reason":"test","approved_by":"unit"
		}]}}
	}`)
	policy := mustPolicy(t, policyValue)
	planValue := mustParseDataJSON(t, `{
		"format_version":"1.2","complete":false,"errored":false,
		"resource_changes":[{"address":"sample_resource.this","type":"sample_resource",
		"change":{"actions":["update"],"before":{"status":"a"},"after":{"status":"b"}}}]
	}`)
	_, err := ClassifyPlan(planValue, policy, nil)
	var contractFailure *plan.AssessmentPlanError
	if !errors.As(err, &contractFailure) {
		t.Fatalf("ClassifyPlan(complete:false) error = %T(%v), want *plan.AssessmentPlanError", err, err)
	}
	if got := policy.StaleEntries(metadata.StaleEntriesOptions{
		Modes: []metadata.PolicyMode{metadata.PolicyPlanTolerate},
	}); len(got) != 1 {
		t.Errorf("policy stale entries after rejected plan = %#v, want original entry still stale", got)
	}
}

func TestClassifyPlanDetectsIdentityAndSensitivityChanges(t *testing.T) {
	policyValue := mustParseDataJSON(t, `{
		"version":1,"resource_types":{"sample_resource":{"plan_tolerate":[{
			"path":"status","reason":"test","approved_by":"unit"
		}]}}
	}`)
	tests := []struct {
		name       string
		metadata   string
		wantMarker string
	}{
		{
			name:       "identity",
			metadata:   `,"before_identity":{"id":"old"},"after_identity":{"id":"new"}`,
			wantMarker: IdentityChange,
		},
		{
			name:       "sensitivity",
			metadata:   `,"before_sensitive":{"secret":true},"after_sensitive":{}`,
			wantMarker: SensitivityChange,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planText := `{"format_version":"1.2","complete":true,"errored":false,` +
				`"resource_changes":[{"address":"sample_resource.this","type":"sample_resource",` +
				`"change":{"actions":["update"],"before":{"status":"a"},"after":{"status":"b"}` +
				test.metadata + `}}]}`
			classification, err := ClassifyPlan(
				mustParseDataJSON(t, planText),
				mustPolicy(t, policyValue),
				nil,
			)
			if err != nil {
				t.Fatalf("ClassifyPlan(%s change) error = %v, want nil", test.name, err)
			}
			if got, want := classification.Findings[0].Paths, []PlanPath{{test.wantMarker}}; !reflect.DeepEqual(got, want) {
				t.Errorf("ClassifyPlan(%s change).Paths = %#v, want %#v", test.name, got, want)
			}
		})
	}
}

// TestDiffPathsTreatsScalarArraysAsSets pins the four cases that separate a
// set reorder from a real change. The last one is the guard against fixing
// this too broadly: block collections and ordered lists keep positional
// comparison, because for those a reorder is a genuine difference.
func TestDiffPathsTreatsScalarArraysAsSets(t *testing.T) {
	tests := []struct {
		name      string
		before    any
		after     any
		wantPaths int
	}{
		{
			name:      "reordered scalar set is clean",
			before:    []any{"a", "b", "c"},
			after:     []any{"c", "a", "b"},
			wantPaths: 0,
		},
		{
			name:      "membership change is reported",
			before:    []any{"a", "b", "c"},
			after:     []any{"a", "b", "d"},
			wantPaths: 1,
		},
		{
			name:      "length change is reported",
			before:    []any{"a", "b"},
			after:     []any{"a", "b", "c"},
			wantPaths: 1,
		},
		{
			// Same members, different multiplicities: not a reorder, so the
			// multiset check declines and positional comparison reports the
			// one index that differs.
			name:      "duplicate counts are respected",
			before:    []any{"a", "a", "b"},
			after:     []any{"a", "b", "b"},
			wantPaths: 1,
		},
		{
			name:      "object list stays positional",
			before:    []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}},
			after:     []any{map[string]any{"name": "b"}, map[string]any{"name": "a"}},
			wantPaths: 2,
		},
		{
			name:      "nested array list stays positional",
			before:    []any{[]any{"a"}, []any{"b"}},
			after:     []any{[]any{"b"}, []any{"a"}},
			wantPaths: 2,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := DiffPaths(testCase.before, testCase.after)
			if len(got) != testCase.wantPaths {
				t.Errorf("DiffPaths() = %v (%d paths), want %d", got, len(got), testCase.wantPaths)
			}
		})
	}
}

// TestDiffPathsSetComparisonIsScopedToTheAttribute pins that the reorder
// suppression applies where the set actually lives, not to its whole parent:
// a sibling attribute changing alongside a reordered set is still reported.
func TestDiffPathsSetComparisonIsScopedToTheAttribute(t *testing.T) {
	before := map[string]any{
		"db_categorized_urls": []any{"a.example", "b.example", "c.example"},
		"description":         "before",
	}
	after := map[string]any{
		"db_categorized_urls": []any{"c.example", "a.example", "b.example"},
		"description":         "after",
	}
	got := DiffPaths(before, after)
	if len(got) != 1 {
		t.Fatalf("DiffPaths() = %v, want exactly the description path", got)
	}
	if len(got[0]) != 1 || got[0][0] != "description" {
		t.Errorf("DiffPaths() = %v, want [[description]]", got)
	}
}

// TestDiffChangesReportsContentByAttributeKind pins the shape a reviewer
// receives for each kind of difference. The set case is the reason this
// exists: reported positionally, a two-member addition reads as a contiguous
// run of unrelated indices that names neither what moved nor which way.
func TestDiffChangesReportsContentByAttributeKind(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
		want   []PlanChange
	}{
		{
			name:   "scalar_reports_before_and_after",
			before: `{"count":36}`,
			after:  `{"count":38}`,
			want: []PlanChange{{
				Path: PlanPath{"count"}, Kind: ScalarChange,
				Before: json.Number("36"), After: json.Number("38"),
			}},
		},
		{
			name:   "set_reports_members_not_indices",
			before: `{"urls":["a.example","b.example"]}`,
			after:  `{"urls":["b.example","a.example","substrate.office.com","support.devrev.ai"]}`,
			want: []PlanChange{{
				Path: PlanPath{"urls"}, Kind: SetChange,
				Added:   []any{"substrate.office.com", "support.devrev.ai"},
				Removed: []any{},
			}},
		},
		{
			name:   "set_reports_removals",
			before: `{"urls":["a.example","gone.example"]}`,
			after:  `{"urls":["a.example"]}`,
			want: []PlanChange{{
				Path: PlanPath{"urls"}, Kind: SetChange,
				Added: []any{}, Removed: []any{"gone.example"},
			}},
		},
		{
			name:   "block_collection_keeps_positional_paths",
			before: `{"rules":[{"id":"1"},{"id":"2"}]}`,
			after:  `{"rules":[{"id":"1"},{"id":"9"}]}`,
			want: []PlanChange{{
				Path: PlanPath{"rules", 1, "id"}, Kind: ScalarChange,
				Before: "2", After: "9",
			}},
		},
		{
			name:   "reordered_set_reports_nothing",
			before: `{"urls":["a.example","b.example"]}`,
			after:  `{"urls":["b.example","a.example"]}`,
			want:   []PlanChange{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DiffChanges(
				mustParseDataJSON(t, test.before),
				mustParseDataJSON(t, test.after),
				nil,
				nil,
			)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("DiffChanges(%s, %s) = %#v, want %#v", test.before, test.after, got, test.want)
			}
		})
	}
}

// TestDiffChangesRedactsSensitiveContentWithoutHidingTheChange is the rule
// that has a real downside if it is got wrong in either direction: emitting
// the value puts a credential in whatever log reads the report, and dropping
// the entry hides that a secret moved at all.
func TestDiffChangesRedactsSensitiveContentWithoutHidingTheChange(t *testing.T) {
	before := mustParseDataJSON(t, `{"token":"old-secret","name":"visible-before"}`)
	after := mustParseDataJSON(t, `{"token":"new-secret","name":"visible-after"}`)
	got := DiffChanges(
		before,
		after,
		mustParseDataJSON(t, `{"token":true}`),
		mustParseDataJSON(t, `{"token":true}`),
	)
	want := []PlanChange{
		{Path: PlanPath{"name"}, Kind: ScalarChange, Before: "visible-before", After: "visible-after"},
		{Path: PlanPath{"token"}, Kind: ScalarChange, Sensitive: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DiffChanges(sensitive token) = %#v, want %#v", got, want)
	}
	for _, change := range got {
		if change.Sensitive && (change.Before != nil || change.After != nil) {
			t.Errorf("sensitive change %#v carries content, want it withheld", change)
		}
	}
}

// A mask may collapse a whole subtree to true, and a sensitive set must not
// leak its members either.
func TestDiffChangesHonoursCollapsedAndSetSensitivityMasks(t *testing.T) {
	tests := []struct {
		name           string
		before, after  string
		beforeMask     string
		wantKind       PlanChangeKind
		wantPathLength int
	}{
		{
			name:   "collapsed_subtree_mask",
			before: `{"block":{"secret":"old"}}`, after: `{"block":{"secret":"new"}}`,
			beforeMask: `{"block":true}`,
			wantKind:   ScalarChange, wantPathLength: 2,
		},
		{
			name:   "sensitive_set_withholds_members",
			before: `{"urls":["a.example"]}`, after: `{"urls":["a.example","b.example"]}`,
			beforeMask: `{"urls":true}`,
			wantKind:   SetChange, wantPathLength: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DiffChanges(
				mustParseDataJSON(t, test.before),
				mustParseDataJSON(t, test.after),
				mustParseDataJSON(t, test.beforeMask),
				nil,
			)
			if len(got) != 1 {
				t.Fatalf("DiffChanges(%s) = %#v, want one change", test.name, got)
			}
			change := got[0]
			if !change.Sensitive || change.Kind != test.wantKind ||
				len(change.Path) != test.wantPathLength ||
				change.Before != nil || change.After != nil ||
				len(change.Added) != 0 || len(change.Removed) != 0 {
				t.Errorf("DiffChanges(%s) = %#v, want a redacted %s change", test.name, change, test.wantKind)
			}
		})
	}
}
