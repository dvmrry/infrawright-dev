package modulesgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const moduleHCLCompatibilitySHA256 = "28988580eb08bc16fb759ed3d7fab42bba6bcc5db11bd3b750c6e34c91f20f75"

type moduleHCLCompatibilityFixture struct {
	SchemaVersion int                          `json:"schema_version"`
	ResourceCount int                          `json:"resource_count"`
	Files         []moduleHCLCompatibilityFile `json:"files"`
}

type moduleHCLCompatibilityFile struct {
	Path   string `json:"path"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

func TestCommittedModuleHCLCompatibility(t *testing.T) {
	fixturePath := filepath.Join("testdata", "module_hcl_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != moduleHCLCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, moduleHCLCompatibilitySHA256)
	}
	var fixture moduleHCLCompatibilityFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("%s schema_version = %d, want 1", fixturePath, fixture.SchemaVersion)
	}
	if fixture.ResourceCount != 17 || len(fixture.Files) != 68 {
		t.Fatalf("%s resource/file counts = %d/%d, want 17/68", fixturePath, fixture.ResourceCount, len(fixture.Files))
	}

	root := committedRoot(t)
	formatter := NewHCLFormatter()
	rendered := map[string]RenderedModule{}
	resources := map[string]bool{}
	paths := map[string]bool{}
	for _, expected := range fixture.Files {
		resourceType, fileName, ok := strings.Cut(expected.Path, "/")
		if !ok || resourceType == "" || fileName == "" || strings.Contains(fileName, "/") {
			t.Fatalf("compatibility path %q is not resource/file", expected.Path)
		}
		if paths[expected.Path] {
			t.Fatalf("duplicate compatibility path %q", expected.Path)
		}
		paths[expected.Path] = true
		resources[resourceType] = true

		files, present := rendered[resourceType]
		if !present {
			files, err = RenderModuleFiles(root, resourceType)
			if err != nil {
				t.Fatalf("RenderModuleFiles(%q) error: %v", resourceType, err)
			}
			rendered[resourceType] = files
		}
		source, present := files.Get(ModuleFileName(fileName))
		if !present {
			t.Fatalf("RenderModuleFiles(%q) omitted %q", resourceType, fileName)
		}
		actual, err := formatter.FormatHCL(source)
		if err != nil {
			t.Fatalf("FormatHCL(%q) error: %v", expected.Path, err)
		}
		actualDigest := sha256.Sum256([]byte(actual))
		actualSHA256 := hex.EncodeToString(actualDigest[:])
		if len(actual) != expected.Length || actualSHA256 != expected.SHA256 {
			t.Errorf("rendered %s length/SHA256 = %d/%s, want %d/%s", expected.Path, len(actual), actualSHA256, expected.Length, expected.SHA256)
		}
	}
	if got := len(resources); got != fixture.ResourceCount {
		t.Errorf("compatibility resources = %d, want %d", got, fixture.ResourceCount)
	}
}
