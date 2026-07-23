package providerprobe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const probeArtifactCompatibilitySHA256 = "a9850b7186f5184491055f55dcf2ef40d5a62a40cfa93e1423b8402c07624b4d"

type probeArtifactCompatibilityFixture struct {
	SchemaVersion int `json:"schema_version"`
	Cases         []struct {
		Name      string            `json:"name"`
		Artifacts map[string]string `json:"artifacts"`
	} `json:"cases"`
}

func probeCompatibilityArtifacts(t *testing.T, name string) map[string]string {
	t.Helper()
	fixturePath := filepath.Join("testdata", "probe_artifact_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != probeArtifactCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, probeArtifactCompatibilitySHA256)
	}
	var fixture probeArtifactCompatibilityFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("%s schema_version = %d, want 1", fixturePath, fixture.SchemaVersion)
	}
	if got, want := len(fixture.Cases), 2; got != want {
		t.Fatalf("%s cases = %d, want %d", fixturePath, got, want)
	}
	for _, test := range fixture.Cases {
		if test.Name == name {
			return test.Artifacts
		}
	}
	t.Fatalf("probe compatibility case %q not found", name)
	return nil
}
