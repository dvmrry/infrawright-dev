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

// assertRefreshDriftStance compares what this test is about -- the status a
// finding receives and which section it came from -- rather than deep-equalling
// whole findings. A finding grows fields for reasons unrelated to the demotion
// stance, and pinning the entire struct here makes this test fail for those
// reasons instead, in a file whose author is not looking at this behaviour.
func assertRefreshDriftStance(
	t *testing.T,
	label string,
	got, want PlanClassification,
) {
	t.Helper()
	if got.Status != want.Status {
		t.Errorf("%s status = %q, want %q", label, got.Status, want.Status)
	}
	if len(got.Findings) != len(want.Findings) {
		t.Fatalf("%s findings = %#v, want %d findings", label, got.Findings, len(want.Findings))
	}
	for index, wantFinding := range want.Findings {
		gotFinding := got.Findings[index]
		if gotFinding.Status != wantFinding.Status ||
			gotFinding.Source != wantFinding.Source ||
			gotFinding.Address != wantFinding.Address {
			t.Errorf("%s findings[%d] = %+v, want status %q source %q address %q",
				label, index, gotFinding,
				wantFinding.Status, wantFinding.Source, wantFinding.Address)
		}
		if !reflect.DeepEqual(gotFinding.Actions, wantFinding.Actions) {
			t.Errorf("%s findings[%d].Actions = %#v, want %#v",
				label, index, gotFinding.Actions, wantFinding.Actions)
		}
		if !reflect.DeepEqual(gotFinding.Paths, wantFinding.Paths) {
			t.Errorf("%s findings[%d].Paths = %#v, want %#v",
				label, index, gotFinding.Paths, wantFinding.Paths)
		}
	}
}

func TestClassifyPlanRefreshDriftStanceIsExplicitAndScoped(t *testing.T) {
	// An import-only plan whose refresh found the recorded values stale. This
	// is the adoption deadlock: resource_drift carries a change that only the
	// guarded apply can settle.
	adoptionPlan := mustParseDataJSON(t, `{
		"format_version":"1.2","complete":true,"errored":false,
		"resource_changes":[{"address":"sample_resource.this","type":"sample_resource",
		"change":{"actions":["create"],"importing":{"id":"existing"}}}],
		"resource_drift":[{"address":"sample_resource.this","type":"sample_resource",
		"change":{"actions":["update"],"before":{"status":"recorded"},"after":{"status":"remote"}}},
		{"address":"sample_resource.other","type":"sample_resource",
		"change":{"actions":["create"],"importing":{"id":"existing"}}}]
	}`)

	strict, err := ClassifyPlan(adoptionPlan, nil, nil)
	if err != nil {
		t.Fatalf("ClassifyPlan(refresh drift) error = %v, want nil", err)
	}
	wantStrict := PlanClassification{
		Status: Blocked,
		Findings: []PlanFinding{
			{Status: Clean, Source: "resource_changes", Address: "sample_resource.this",
				Actions: []string{"create"}, Paths: []PlanPath{}},
			{Status: Blocked, Source: "resource_drift", Address: "sample_resource.this",
				Actions: []string{"update"}, Paths: []PlanPath{{"status"}}},
			{Status: Clean, Source: "resource_drift", Address: "sample_resource.other",
				Actions: []string{"create"}, Paths: []PlanPath{}},
		},
	}
	assertRefreshDriftStance(t, "ClassifyPlan(refresh drift)", strict, wantStrict)

	adopting, err := ClassifyPlanWithOptions(
		adoptionPlan, nil, nil, ClassifyPlanOptions{TolerateRefreshDrift: true},
	)
	if err != nil {
		t.Fatalf("ClassifyPlanWithOptions(tolerate refresh drift) error = %v, want nil", err)
	}
	wantAdopting := PlanClassification{
		Status: Tolerated,
		Findings: []PlanFinding{
			{Status: Clean, Source: "resource_changes", Address: "sample_resource.this",
				Actions: []string{"create"}, Paths: []PlanPath{}},
			{Status: Tolerated, Source: "resource_drift", Address: "sample_resource.this",
				Actions: []string{"update"}, Paths: []PlanPath{{"status"}}},
			// The stance only relaxes what blocks. A drift record that
			// classified clean stays clean rather than being inflated into
			// tolerated drift.
			{Status: Clean, Source: "resource_drift", Address: "sample_resource.other",
				Actions: []string{"create"}, Paths: []PlanPath{}},
		},
	}
	assertRefreshDriftStance(
		t, "ClassifyPlanWithOptions(tolerate refresh drift)", adopting, wantAdopting,
	)

	// The same change reported by resource_changes is a real pending write,
	// not a stale record, and stays blocked under the adoption stance.
	changesPlan := mustParseDataJSON(t, `{
		"format_version":"1.2","complete":true,"errored":false,
		"resource_changes":[{"address":"sample_resource.this","type":"sample_resource",
		"change":{"actions":["update"],"before":{"status":"recorded"},"after":{"status":"remote"}}}]
	}`)
	scoped, err := ClassifyPlanWithOptions(
		changesPlan, nil, nil, ClassifyPlanOptions{TolerateRefreshDrift: true},
	)
	if err != nil {
		t.Fatalf("ClassifyPlanWithOptions(pending change) error = %v, want nil", err)
	}
	if scoped.Status != Blocked || len(scoped.Findings) != 1 ||
		scoped.Findings[0].Status != Blocked {
		t.Errorf("ClassifyPlanWithOptions(pending change) = %#v, want blocked resource_changes finding", scoped)
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

// TestDiffPathsComparesEveryArrayPositionally pins that no array is treated as
// a set. A Terraform set really is order-insensitive, so a reorder reported
// here is a false positive -- kept deliberately, because the alternative
// suppresses real reorders of ordered lists. The reorder case is listed first
// so the cost of that choice stays visible rather than implied.
func TestDiffPathsComparesEveryArrayPositionally(t *testing.T) {
	tests := []struct {
		name      string
		before    any
		after     any
		wantPaths int
	}{
		{
			// The known false positive. A set(string) reorder is not a change
			// Terraform would apply, and it is reported anyway: telling it
			// apart from a list(string) reorder needs provider schema types.
			name:      "reordered scalars are reported positionally",
			before:    []any{"a", "b", "c"},
			after:     []any{"c", "a", "b"},
			wantPaths: 3,
		},
		{
			// The reason the case above is tolerated: for list(string) this
			// is a real change, and suppressing all-scalar arrays would drop
			// it silently.
			name:      "ordered list reorder is a real change",
			before:    []any{"first", "second"},
			after:     []any{"second", "first"},
			wantPaths: 2,
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
			name:      "identical arrays report nothing",
			before:    []any{"a", "b"},
			after:     []any{"a", "b"},
			wantPaths: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DiffPaths(test.before, test.after)
			if len(got) != test.wantPaths {
				t.Errorf("DiffPaths(%s) = %#v (%d paths), want %d",
					test.name, got, len(got), test.wantPaths)
			}
		})
	}
}

// TestDiffChangesReportsContentByAttributeKind pins the shape a reviewer
// receives for each kind of difference. Arrays keep positional paths, so the
// value carried at each index is what makes the finding readable: an index on
// its own names neither what moved nor which way.
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
			// The values are the point. Positionally this is indices 2 and 3,
			// which name nothing; with the values a reviewer can read the
			// domains being allowed.
			name:   "array_addition_names_the_values",
			before: `{"urls":["a.example","b.example"]}`,
			after:  `{"urls":["a.example","b.example","substrate.office.com","support.devrev.ai"]}`,
			want: []PlanChange{
				{
					Path: PlanPath{"urls", 2}, Kind: ScalarChange,
					Before: nil, After: "substrate.office.com",
				},
				{
					Path: PlanPath{"urls", 3}, Kind: ScalarChange,
					Before: nil, After: "support.devrev.ai",
				},
			},
		},
		{
			name:   "array_removal_names_the_value",
			before: `{"urls":["a.example","gone.example"]}`,
			after:  `{"urls":["a.example"]}`,
			want: []PlanChange{{
				Path: PlanPath{"urls", 1}, Kind: ScalarChange,
				Before: "gone.example", After: nil,
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
			// The known false positive, pinned rather than hidden: for a
			// set(string) this is not a change Terraform would apply, and it
			// is reported anyway because nothing here can tell a set from an
			// ordered list.
			name:   "reorder_is_reported_positionally",
			before: `{"urls":["a.example","b.example"]}`,
			after:  `{"urls":["b.example","a.example"]}`,
			want: []PlanChange{
				{
					Path: PlanPath{"urls", 0}, Kind: ScalarChange,
					Before: "a.example", After: "b.example",
				},
				{
					Path: PlanPath{"urls", 1}, Kind: ScalarChange,
					Before: "b.example", After: "a.example",
				},
			},
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

// A mask may collapse a whole subtree to true, and a masked array must not
// leak its elements either.
func TestDiffChangesHonoursCollapsedAndArraySensitivityMasks(t *testing.T) {
	tests := []struct {
		name           string
		before, after  string
		beforeMask     string
		afterMask      string
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
			// After-side only: a newly introduced secret has no before value
			// to protect, and dropping the after-side check would leak it
			// while every both-sides and before-side case stayed green.
			name:   "after_only_mask_withholds_a_new_secret",
			before: `{"token":null}`, after: `{"token":"freshly-minted"}`,
			beforeMask: `{"token":false}`, afterMask: `{"token":true}`,
			wantKind: ScalarChange, wantPathLength: 1,
		},
		{
			name:   "sensitive_array_withholds_elements",
			before: `{"urls":["a.example"]}`, after: `{"urls":["a.example","b.example"]}`,
			beforeMask: `{"urls":true}`,
			wantKind:   ScalarChange, wantPathLength: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var afterMask any
			if test.afterMask != "" {
				afterMask = mustParseDataJSON(t, test.afterMask)
			}
			got := DiffChanges(
				mustParseDataJSON(t, test.before),
				mustParseDataJSON(t, test.after),
				mustParseDataJSON(t, test.beforeMask),
				afterMask,
			)
			if len(got) != 1 {
				t.Fatalf("DiffChanges(%s) = %#v, want one change", test.name, got)
			}
			// The content is the point: a redacted change must say it moved
			// and carry nothing.
			if got[0].Sensitive && (got[0].Before != nil || got[0].After != nil) {
				t.Errorf("DiffChanges(%s) = %#v, want content withheld", test.name, got[0])
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

// TestDiffChangesPathsAgreeWithDiffPaths pins the invariant that keeps a
// finding's guidance attached to it. planGuidanceRecords matches guidance by
// recomputing DiffPaths, while findings carry values from DiffChanges, and
// joinBlockedGuidance requires exact path equality. If the two walks ever
// disagree -- as they would if one collapsed arrays and the other did not --
// the finding survives and its guidance silently disappears.
func TestDiffChangesPathsAgreeWithDiffPaths(t *testing.T) {
	tests := []struct{ name, before, after string }{
		{"scalar", `{"count":36}`, `{"count":38}`},
		{"array_addition", `{"urls":["a"]}`, `{"urls":["a","b","c"]}`},
		{"array_removal", `{"urls":["a","b"]}`, `{"urls":["a"]}`},
		{"array_reorder", `{"urls":["a","b"]}`, `{"urls":["b","a"]}`},
		{"block_collection", `{"rules":[{"id":"1"}]}`, `{"rules":[{"id":"9"}]}`},
		{"nested_object", `{"a":{"b":{"c":1}}}`, `{"a":{"b":{"c":2}}}`},
		{"mixed_siblings", `{"n":1,"urls":["a"]}`, `{"n":2,"urls":["a","b"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := mustParseDataJSON(t, test.before)
			after := mustParseDataJSON(t, test.after)
			paths := DiffPaths(before, after)
			changes := DiffChanges(before, after, nil, nil)
			if len(paths) != len(changes) {
				t.Fatalf("DiffPaths = %#v (%d), DiffChanges = %#v (%d), want the same walk",
					paths, len(paths), changes, len(changes))
			}
			for index, path := range paths {
				if !reflect.DeepEqual(path, changes[index].Path) {
					t.Errorf("path %d: DiffPaths = %#v, DiffChanges = %#v", index, path, changes[index].Path)
				}
			}
		})
	}
}
