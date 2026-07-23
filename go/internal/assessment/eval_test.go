package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
)

const planEvalAuthoritySHA256 = "83924f81dc073e2dc9fef5f20ec96331fa674db09de9ab3bfac9b8770df0eaf8"

type planEvalAuthorityCase struct {
	Group     string          `json:"group"`
	Name      string          `json:"name"`
	InputJSON string          `json:"input_json"`
	Result    json.RawMessage `json:"result"`
	Stale     json.RawMessage `json:"stale"`
}

type planEvalAuthority struct {
	Kind      string `json:"kind"`
	Version   int    `json:"version"`
	Baseline  string `json:"baseline"`
	Authority struct {
		Implementation string `json:"implementation"`
		Python         string `json:"python"`
		Unicode        string `json:"unicode"`
	} `json:"authority"`
	SourceBlobs   map[string]string       `json:"source_blobs"`
	Normalization string                  `json:"normalization"`
	Cases         []planEvalAuthorityCase `json:"cases"`
}

func loadPlanEvalAuthority(t *testing.T) planEvalAuthority {
	t.Helper()
	filePath := filepath.Join("..", "..", "..", "node-tests", "fixtures", "python-plan-eval-v1.json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", filePath, err)
	}
	digest := sha256.Sum256(data)
	if got := hex.EncodeToString(digest[:]); got != planEvalAuthoritySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", filePath, got, planEvalAuthoritySHA256)
	}
	var authority planEvalAuthority
	if err := json.Unmarshal(data, &authority); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v, want nil", filePath, err)
	}
	return authority
}

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

func classificationJSONValue(t *testing.T, classification PlanClassification) any {
	t.Helper()
	data, err := json.Marshal(classification)
	if err != nil {
		t.Fatalf("json.Marshal(%+v) error = %v, want nil", classification, err)
	}
	value, err := canonjson.ParseDataJSONLosslessly(string(data))
	if err != nil {
		t.Fatalf("canonjson.ParseDataJSONLosslessly(json.Marshal(classification)) error = %v, want nil", err)
	}
	return value
}

func staleJSONValue(t *testing.T, stale []metadata.StalePolicyEntry) any {
	t.Helper()
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("json.Marshal(%+v) error = %v, want nil", stale, err)
	}
	value, err := canonjson.ParseDataJSONLosslessly(string(data))
	if err != nil {
		t.Fatalf("canonjson.ParseDataJSONLosslessly(json.Marshal(stale)) error = %v, want nil", err)
	}
	return value
}

func TestPlanEvalFrozenPythonAuthority(t *testing.T) {
	authority := loadPlanEvalAuthority(t)
	if authority.Kind != "infrawright.python-plan-eval-authority" || authority.Version != 1 ||
		authority.Baseline != "397a30c1dc6996283729648d16c1e258ec3627ec" ||
		authority.Normalization != "none" {
		t.Fatalf("plan-eval authority header = %+v, want frozen v1 CPython authority", authority)
	}
	if got, want := authority.Authority, (struct {
		Implementation string `json:"implementation"`
		Python         string `json:"python"`
		Unicode        string `json:"unicode"`
	}{Implementation: "cpython", Python: "3.13.13", Unicode: "15.1.0"}); got != want {
		t.Fatalf("plan-eval authority metadata = %+v, want %+v", got, want)
	}
	wantBlobs := map[string]string{
		"test":                "396c74bb12ab34b66a7bac2ba4944a93f1bf4abe",
		"python_plan_eval":    "f15e4f44193d517384065a1d320533ea74a47a15",
		"python_drift_policy": "852517958dc18f37019f369a08ab9bfbd91441c9",
		"python_paths":        "63ffb562172405c27a880345cd85b93af7b1ba94",
		"node_plan_eval":      "af72faf37582142d51f1bf3e854ae94ccb9fdc0a",
		"node_drift_policy":   "ac6f61ece107213e23a5ef9533fa2477448915d1",
	}
	if !reflect.DeepEqual(authority.SourceBlobs, wantBlobs) {
		t.Fatalf("plan-eval source_blobs = %#v, want %#v", authority.SourceBlobs, wantBlobs)
	}
	if got, want := len(authority.Cases), 16; got != want {
		t.Fatalf("plan-eval authority case count = %d, want %d", got, want)
	}

	for _, frozen := range authority.Cases {
		t.Run(frozen.Name, func(t *testing.T) {
			input := mustParseDataJSON(t, frozen.InputJSON)
			planValue := input
			var policy *metadata.DriftPolicy
			if wrapper, ok := input.(map[string]any); ok {
				if wrappedPlan, exists := wrapper["plan"]; exists {
					planValue = wrappedPlan
					policy = mustPolicy(t, wrapper["policy"])
				}
			}
			classification, err := ClassifyPlan(planValue, policy, nil)
			if err != nil {
				t.Fatalf("ClassifyPlan(frozen %q) error = %v, want nil", frozen.Name, err)
			}
			wantResult := mustParseDataJSON(t, string(frozen.Result))
			if got := classificationJSONValue(t, classification); !canonjson.JSONEqual(got, wantResult) {
				t.Errorf("ClassifyPlan(frozen %q) = %#v, want %#v", frozen.Name, got, wantResult)
			}
			if string(frozen.Stale) == "null" {
				return
			}
			stale := policy.StaleEntries(metadata.StaleEntriesOptions{
				ResourceTypes: map[string]struct{}{"sample_resource": {}},
				Modes:         []metadata.PolicyMode{metadata.PolicyPlanTolerate},
			})
			wantStale := mustParseDataJSON(t, string(frozen.Stale))
			if got := staleJSONValue(t, stale); !canonjson.JSONEqual(got, wantStale) {
				t.Errorf("StaleEntries(frozen %q) = %#v, want %#v", frozen.Name, got, wantStale)
			}
		})
	}
}

func TestDiffPathsAndTruthyPathsPreservePythonOrdering(t *testing.T) {
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
