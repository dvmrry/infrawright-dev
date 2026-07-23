package transformadoptparity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const parityCompatibilitySHA256 = "e651c3b6f78d10c2889399ab31470837065646dde82d24e626e13ce66a746534"

type parityCompatibilityFixture struct {
	SchemaVersion int                         `json:"schema_version"`
	Report        string                      `json:"report"`
	Results       []parityCompatibilityResult `json:"results"`
}

type parityCompatibilityResult struct {
	Name            string `json:"name"`
	ResourceType    string `json:"resource_type"`
	TransformTFVars string `json:"transform_tfvars"`
	AdoptTFVars     string `json:"adopt_tfvars"`
}

func TestTransformAdoptParityCompatibility(t *testing.T) {
	fixturePath := filepath.Join("testdata", "parity_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != parityCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, parityCompatibilitySHA256)
	}
	var compatibility parityCompatibilityFixture
	if err := json.Unmarshal(fixtureBytes, &compatibility); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if compatibility.SchemaVersion != 1 || len(compatibility.Results) != 4 {
		t.Fatalf("%s schema/results = %d/%d, want 1/4", fixturePath, compatibility.SchemaVersion, len(compatibility.Results))
	}

	fixtures := make([]Fixture, 0, len(compatibility.Results))
	for _, expected := range compatibility.Results {
		loaded := loadFixture(t, expected.Name)
		if loaded.ResourceType != expected.ResourceType {
			t.Fatalf("fixture %q resource_type = %q, want %q", expected.Name, loaded.ResourceType, expected.ResourceType)
		}
		fixtures = append(fixtures, loaded)
	}
	report, err := Build(fixtures, testContext(t))
	if err != nil {
		t.Fatalf("Build(compatibility fixtures) error: %v", err)
	}
	rendered, err := Render(report)
	if err != nil {
		t.Fatalf("Render(compatibility report) error: %v", err)
	}
	if rendered != compatibility.Report {
		t.Errorf("Render(compatibility report) differs from fixed report")
	}

	reportFixtures, ok := report["fixtures"].([]any)
	if !ok || len(reportFixtures) != len(compatibility.Results) {
		t.Fatalf("report fixtures = %#v, want %d entries", report["fixtures"], len(compatibility.Results))
	}
	for index, expected := range compatibility.Results {
		entry := reportFixtures[index].(map[string]any)
		if got := entry["name"]; got != expected.Name {
			t.Fatalf("report fixture %d name = %v, want %q", index, got, expected.Name)
		}
		outputs := entry["outputs"].(map[string]any)
		transformDigest := sha256.Sum256([]byte(expected.TransformTFVars))
		adoptDigest := sha256.Sum256([]byte(expected.AdoptTFVars))
		if got, want := outputs["transform_sha256"], hex.EncodeToString(transformDigest[:]); got != want {
			t.Errorf("%s transform SHA256 = %v, want %s", expected.Name, got, want)
		}
		if got, want := outputs["adopt_sha256"], hex.EncodeToString(adoptDigest[:]); got != want {
			t.Errorf("%s adopt SHA256 = %v, want %s", expected.Name, got, want)
		}
	}
}
