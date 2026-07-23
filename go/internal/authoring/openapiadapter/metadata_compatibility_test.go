package openapiadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

const metadataCompatibilitySHA256 = "913a12b0dc58d5e6aa394a73c8cc75d2dd2e1f1cf55f874541740beb8c18bf62"

type metadataCompatibilityFixture struct {
	SchemaVersion int                         `json:"schema_version"`
	Cases         []metadataCompatibilityCase `json:"cases"`
}

type metadataCompatibilityCase struct {
	Name  string `json:"name"`
	Input struct {
		Spec  any      `json:"spec"`
		Read  []string `json:"read_operations"`
		Write []string `json:"write_operations"`
	} `json:"input"`
	Output map[string]map[string]any `json:"output"`
}

func TestMetadataCompatibility(t *testing.T) {
	fixturePath := filepath.Join("testdata", "metadata_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != metadataCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, metadataCompatibilitySHA256)
	}
	var fixture metadataCompatibilityFixture
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
		t.Run(test.Name, func(t *testing.T) {
			encoded, err := json.Marshal(test.Input.Spec)
			if err != nil {
				t.Fatalf("json.Marshal(spec) error: %v", err)
			}
			document, err := ParseForMetadata(context.Background(), sourcebind.OpenAPIStatus{
				Available: true,
				Files:     []sourcebind.CapturedFile{captured("root.json", encoded)},
			})
			if err != nil {
				t.Fatalf("ParseForMetadata() error: %v", err)
			}
			read := make([]OperationReference, len(test.Input.Read))
			for index, operation := range test.Input.Read {
				read[index] = OperationReference(operation)
			}
			write := make([]OperationReference, len(test.Input.Write))
			for index, operation := range test.Input.Write {
				write[index] = OperationReference(operation)
			}
			got, err := document.Metadata(context.Background(), MetadataOptions{
				ReadOperations:  read,
				WriteOperations: write,
			})
			if err != nil {
				t.Fatalf("Document.Metadata() error: %v", err)
			}
			if !reflect.DeepEqual(map[string]map[string]any(got), test.Output) {
				t.Errorf("Document.Metadata() = %#v, want %#v", got, test.Output)
			}
		})
	}
}
