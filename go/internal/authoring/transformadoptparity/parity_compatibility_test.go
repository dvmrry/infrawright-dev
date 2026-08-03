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
	"runtime"
	"slices"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/adopt"
	"github.com/dvmrry/infrawright-dev/go/internal/fixtureupdate"
	"github.com/dvmrry/infrawright-dev/go/internal/textcompat"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

const parityCompatibilitySHA256 = "baeeca9097824387c1779f2dba3e05be5e3e2c2480c94c1cf29cf6270332d106"

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

// Field order is marshal order: the update mode re-encodes this struct, and
// the committed snapshot keeps its keys alphabetical.
type parityCompatibilityResult struct {
	AdoptImports    string   `json:"adopt_imports"`
	AdoptTFVars     string   `json:"adopt_tfvars"`
	Drops           []string `json:"drops"`
	Name            string   `json:"name"`
	ResourceType    string   `json:"resource_type"`
	TransformTFVars string   `json:"transform_tfvars"`
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

// updateParityCompatibility is the IW_UPDATE_FIXTURES=1 refresh path. It
// reloads the committed parity fixtures - whose provenance validation
// fail-closes against the active pack pins, so stale provenance must be
// re-verified and hand-updated in the fixtures FIRST - then recomputes the
// retained outputs and the rendered report through the same production
// kernels the comparing test uses, and rewrites the snapshot plus its pinned
// constant. Fixture membership stays a reviewed hand edit.
func updateParityCompatibility(t *testing.T, fixturePath string, fixtureBytes []byte) {
	t.Helper()
	compatibility, err := decodeParityCompatibilityFixture(fixtureBytes)
	if err != nil {
		t.Fatalf("decodeParityCompatibilityFixture(%q) error: %v", fixturePath, err)
	}
	context := committedTestContext(t)
	fixtures := make([]Fixture, 0, len(compatibility.Results))
	for index := range compatibility.Results {
		result := &compatibility.Results[index]
		loaded := loadCommittedFixture(t, context, result.Name)
		result.ResourceType = loaded.ResourceType
		resource := context.Root.Resources[loaded.ResourceType]
		identities, err := adopt.DeriveAdoptionIdentities(loaded.RawItems, resource)
		if err != nil {
			t.Fatalf("DeriveAdoptionIdentities(%q) error: %v", result.Name, err)
		}
		pairs := make([]tfrender.GeneratedImportPair, len(identities.Identities))
		for i, identity := range identities.Identities {
			pairs[i] = tfrender.GeneratedImportPair{Key: identity.Key, ImportID: identity.ImportID}
		}
		if result.AdoptImports, err = tfrender.RenderGeneratedImports(loaded.ResourceType, pairs); err != nil {
			t.Fatalf("RenderGeneratedImports(%q) error: %v", result.Name, err)
		}
		schema, err := context.Root.LoadResourceSchema(loaded.ResourceType)
		if err != nil {
			t.Fatalf("LoadResourceSchema(%q) error: %v", result.Name, err)
		}
		transformed, err := transform.TransformLoadedItems(transform.TransformLoadedItemsOptions{
			Resource:     resource,
			Schema:       schema,
			RawItems:     loaded.RawItems,
			HTMLUnescape: textcompat.HTMLUnescape,
			UnescapeHTML: transformrun.ShouldUnescapeForTransform(context.Root, loaded.ResourceType),
		})
		if err != nil {
			t.Fatalf("TransformLoadedItems(%q) error: %v", result.Name, err)
		}
		result.Drops = append([]string{}, transformed.Drops...)
		if result.TransformTFVars, _, err = renderedItems(transformed.Items); err != nil {
			t.Fatalf("renderedItems(transform %q) error: %v", result.Name, err)
		}
		policy, err := adopt.LoadAdoptionPolicy(context.Root, nil)
		if err != nil {
			t.Fatalf("LoadAdoptionPolicy(%q) error: %v", result.Name, err)
		}
		tracker := newFixtureStateTracker(loaded)
		adopted, err := adopt.AdoptResourceItems(policy, loaded.RawItems, resource, context.Root, tracker.loader, nil)
		if err != nil {
			t.Fatalf("AdoptResourceItems(%q) error: %v", result.Name, err)
		}
		if err := tracker.checkCoverage(); err != nil {
			t.Fatalf("provider-state coverage (%q): %v", result.Name, err)
		}
		if result.AdoptTFVars, _, err = renderedItems(adopted.Items); err != nil {
			t.Fatalf("renderedItems(adopt %q) error: %v", result.Name, err)
		}
		fixtures = append(fixtures, loaded)
	}
	report, err := Build(fixtures, context)
	if err != nil {
		t.Fatalf("Build(compatibility fixtures) error: %v", err)
	}
	if compatibility.Report, err = Render(report); err != nil {
		t.Fatalf("Render(compatibility report) error: %v", err)
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(compatibility); err != nil {
		t.Fatalf("encode parity compatibility fixture: %v", err)
	}
	if err := os.WriteFile(fixturePath, encoded.Bytes(), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(encoded.Bytes())
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	if err := fixtureupdate.ReplaceConst(sourcePath, "parityCompatibilitySHA256", hex.EncodeToString(digest[:])); err != nil {
		t.Fatalf("fixtureupdate.ReplaceConst error: %v", err)
	}
	t.Skipf("parity compatibility snapshot regenerated; review the diff before committing")
}

func TestTransformAdoptParityCompatibility(t *testing.T) {
	fixturePath := filepath.Join("testdata", "parity_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	if fixtureupdate.Requested() {
		updateParityCompatibility(t, fixturePath, fixtureBytes)
		return
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

	context := committedTestContext(t)
	fixtures := make([]Fixture, 0, len(compatibility.Results))
	for _, expected := range compatibility.Results {
		loaded := loadCommittedFixture(t, context, expected.Name)
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
