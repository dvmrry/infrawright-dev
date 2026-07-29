package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

const planClassificationCompatibilitySHA256 = "b2472cff7f7feff00c610d8657153d004a4ba7724542096831e93d63e692917c"

type planClassificationCompatibilityCase struct {
	Name      string          `json:"name"`
	InputJSON string          `json:"input_json"`
	Result    json.RawMessage `json:"result"`
	Stale     json.RawMessage `json:"stale"`
}

type planClassificationCompatibilityFixture struct {
	SchemaVersion int                                   `json:"schema_version"`
	Cases         []planClassificationCompatibilityCase `json:"cases"`
}

func TestPlanClassificationCompatibility(t *testing.T) {
	fixturePath := filepath.Join("testdata", "plan_classification_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != planClassificationCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, planClassificationCompatibilitySHA256)
	}
	var fixture planClassificationCompatibilityFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("%s schema_version = %d, want 1", fixturePath, fixture.SchemaVersion)
	}
	if got, want := len(fixture.Cases), 16; got != want {
		t.Fatalf("%s cases = %d, want %d", fixturePath, got, want)
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			input := mustParseDataJSON(t, test.InputJSON)
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
				t.Fatalf("ClassifyPlan(%q) error: %v", test.Name, err)
			}
			wantResult := mustParseDataJSON(t, string(test.Result))
			// The fixture is the retired implementation's output, digest-pinned
			// and not regenerable: regenerating it destroys the evidence it
			// exists to provide. Findings now carry an additional `changes`
			// key, so the comparison projects that key out. Everything the
			// retired implementation specified -- statuses, sources, actions,
			// and every path -- stays a live byte assertion, and the only
			// admitted divergence is the additive key.
			gotResult := withoutFindingChanges(classificationCompatibilityJSON(t, classification))
			if !canonjson.JSONEqual(gotResult, wantResult) {
				t.Errorf("ClassifyPlan(%q) = %#v, want %#v", test.Name, gotResult, wantResult)
			}
			if string(test.Stale) == "null" {
				return
			}
			stale := policy.StaleEntries(metadata.StaleEntriesOptions{
				ResourceTypes: map[string]struct{}{"sample_resource": {}},
				Modes:         []metadata.PolicyMode{metadata.PolicyPlanTolerate},
			})
			wantStale := mustParseDataJSON(t, string(test.Stale))
			gotStale := classificationCompatibilityJSON(t, stale)
			if !canonjson.JSONEqual(gotStale, wantStale) {
				t.Errorf("StaleEntries(%q) = %#v, want %#v", test.Name, gotStale, wantStale)
			}
		})
	}
}

func classificationCompatibilityJSON(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T) error: %v", value, err)
	}
	result, err := canonjson.ParseDataJSONLosslessly(string(data))
	if err != nil {
		t.Fatalf("canonjson.ParseDataJSONLosslessly(json.Marshal(%T)) error: %v", value, err)
	}
	return result
}

// withoutFindingChanges removes the additive `changes` key from every finding
// so a v1 fixture can still assert every byte it originally specified. It
// deliberately removes nothing else: a change to `paths`, `status`, or any
// other v1 key must still fail against the retired implementation's output.
func withoutFindingChanges(value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	findings, ok := object["findings"].([]any)
	if !ok {
		return value
	}
	projectedFindings := make([]any, len(findings))
	for index, rawFinding := range findings {
		finding, ok := rawFinding.(map[string]any)
		if !ok {
			projectedFindings[index] = rawFinding
			continue
		}
		projected := make(map[string]any, len(finding))
		for key, keyValue := range finding {
			if key == "changes" {
				continue
			}
			projected[key] = keyValue
		}
		projectedFindings[index] = projected
	}
	projectedObject := make(map[string]any, len(object))
	for key, keyValue := range object {
		projectedObject[key] = keyValue
	}
	projectedObject["findings"] = projectedFindings
	return projectedObject
}
