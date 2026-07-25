package transformadoptparity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/adopt"
	"github.com/dvmrry/infrawright-dev/go/internal/textcompat"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

const parityCompatibilitySHA256 = "124737055c8758b617d1653744c5b8911f24b468c114b0e9ed7dca750ab15cf0"

type parityCompatibilityFixture struct {
	SchemaVersion int                         `json:"schema_version"`
	Provenance    parityCompatibilitySource   `json:"provenance"`
	Report        string                      `json:"report"`
	Results       []parityCompatibilityResult `json:"results"`
}

type parityCompatibilitySource struct {
	BaselineCommit string `json:"baseline_commit"`
	Note           string `json:"note"`
}

type parityCompatibilityResult struct {
	Name            string   `json:"name"`
	ResourceType    string   `json:"resource_type"`
	TransformTFVars string   `json:"transform_tfvars"`
	AdoptTFVars     string   `json:"adopt_tfvars"`
	AdoptImports    string   `json:"adopt_imports"`
	Drops           []string `json:"drops"`
}

func decodeParityCompatibilityFixture(data []byte) (parityCompatibilityFixture, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture parityCompatibilityFixture
	if err := decoder.Decode(&fixture); err != nil {
		return parityCompatibilityFixture{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return parityCompatibilityFixture{}, errors.New("fixture contains more than one JSON value")
		}
		return parityCompatibilityFixture{}, err
	}
	return fixture, nil
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
	compatibility, err := decodeParityCompatibilityFixture(fixtureBytes)
	if err != nil {
		t.Fatalf("decodeParityCompatibilityFixture(%q) error: %v", fixturePath, err)
	}
	if compatibility.SchemaVersion != 1 || len(compatibility.Results) != 4 {
		t.Fatalf("%s schema/results = %d/%d, want 1/4", fixturePath, compatibility.SchemaVersion, len(compatibility.Results))
	}
	if compatibility.Provenance.BaselineCommit == "" || compatibility.Provenance.Note == "" {
		t.Fatalf("%s provenance = %#v, want non-empty baseline_commit and note", fixturePath, compatibility.Provenance)
	}

	context := testContext(t)
	fixtures := make([]Fixture, 0, len(compatibility.Results))
	for _, expected := range compatibility.Results {
		loaded := loadFixture(t, expected.Name)
		if loaded.ResourceType != expected.ResourceType {
			t.Fatalf("fixture %q resource_type = %q, want %q", expected.Name, loaded.ResourceType, expected.ResourceType)
		}
		resource := context.Root.Resources[loaded.ResourceType]
		identities, err := adopt.DeriveAdoptionIdentities(loaded.RawItems, resource)
		if err != nil {
			t.Fatalf("DeriveAdoptionIdentities(%q) error: %v", expected.Name, err)
		}
		pairs := make([]tfrender.GeneratedImportPair, len(identities.Identities))
		for index, identity := range identities.Identities {
			pairs[index] = tfrender.GeneratedImportPair{Key: identity.Key, ImportID: identity.ImportID}
		}
		imports, err := tfrender.RenderGeneratedImports(loaded.ResourceType, pairs)
		if err != nil {
			t.Fatalf("RenderGeneratedImports(%q) error: %v", expected.Name, err)
		}
		if imports != expected.AdoptImports {
			t.Errorf("RenderGeneratedImports(%q) = %q, want fixed %q", expected.Name, imports, expected.AdoptImports)
		}
		schema, err := context.Root.LoadResourceSchema(loaded.ResourceType)
		if err != nil {
			t.Fatalf("LoadResourceSchema(%q) error: %v", expected.Name, err)
		}
		transformed, err := transform.TransformLoadedItems(transform.TransformLoadedItemsOptions{
			Resource:     resource,
			Schema:       schema,
			RawItems:     loaded.RawItems,
			HTMLUnescape: textcompat.HTMLUnescape,
			UnescapeHTML: transformrun.ShouldUnescapeForTransform(context.Root, loaded.ResourceType),
		})
		if err != nil {
			t.Fatalf("TransformLoadedItems(%q) error: %v", expected.Name, err)
		}
		if !slices.Equal(transformed.Drops, expected.Drops) {
			t.Errorf("TransformLoadedItems(%q) drops = %v, want %v", expected.Name, transformed.Drops, expected.Drops)
		}
		fixtures = append(fixtures, loaded)
	}
	report, err := Build(fixtures, context)
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

func TestParityCompatibilityFixtureRejectsUnknownFields(t *testing.T) {
	fixturePath := filepath.Join("testdata", "parity_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	mutated := bytes.Replace(
		fixtureBytes,
		[]byte(`"results":[{`),
		[]byte(`"results":[{"unexpected_behavioral_field":true,`),
		1,
	)
	if _, err := decodeParityCompatibilityFixture(mutated); err == nil {
		t.Fatal("decodeParityCompatibilityFixture() accepted an unknown behavioral field")
	}
}
