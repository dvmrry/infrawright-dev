package assessment

import (
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
