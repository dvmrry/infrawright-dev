package modulesgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/fixtureupdate"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

const moduleHCLCompatibilitySHA256 = "e97b6ac425bd6916ea82144788f70382b1123869ce34ff42589d2d9aefeefec5"

type moduleHCLCompatibilityFixture struct {
	SchemaVersion int                              `json:"schema_version"`
	Provenance    moduleHCLCompatibilityProvenance `json:"provenance"`
	Files         []moduleHCLCompatibilityFile     `json:"files"`
	ResourceCount int                              `json:"resource_count"`
}

type moduleHCLCompatibilityProvenance struct {
	Authority string `json:"authority"`
	Note      string `json:"note"`
}

type moduleHCLCompatibilityFile struct {
	Path   string `json:"path"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

// updateModuleHCLCompatibility is the IW_UPDATE_FIXTURES=1 refresh path: it
// re-renders every file the fixture already covers and rewrites the snapshot
// plus its pinned constant. Membership (which resources and files are
// covered) is deliberately not regenerated; extending coverage stays a
// reviewed hand edit (placeholder rows whose hashes this mode fills in),
// and resource_count is derived from the manifest rather than maintained
// by hand.
func updateModuleHCLCompatibility(t *testing.T, fixturePath string, fixtureBytes []byte) {
	t.Helper()
	var fixture moduleHCLCompatibilityFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	requireModulePackSelection(t, metadata.PackSelection{
		Packs: []string{"zcc", "zia", "zpa"}, Shared: []string{"zscaler"},
	})
	root := installedModuleRoot(t)
	formatter := NewHCLFormatter()
	rendered := map[string]RenderedModule{}
	for index, entry := range fixture.Files {
		// Same path contract as the comparing mode: exactly resource/file,
		// no nested paths. Update mode must never be able to fill and pin a
		// row the comparing mode then rejects.
		resourceType, fileName, ok := strings.Cut(entry.Path, "/")
		if !ok || resourceType == "" || fileName == "" || strings.Contains(fileName, "/") {
			t.Fatalf("compatibility path %q is not resource/file", entry.Path)
		}
		files, present := rendered[resourceType]
		if !present {
			var err error
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
		formatted, err := formatter.FormatHCL(source)
		if err != nil {
			t.Fatalf("FormatHCL(%q) error: %v", entry.Path, err)
		}
		digest := sha256.Sum256([]byte(formatted))
		fixture.Files[index].Length = len(formatted)
		fixture.Files[index].SHA256 = hex.EncodeToString(digest[:])
	}
	distinct := map[string]struct{}{}
	for _, entry := range fixture.Files {
		resourceType, _, _ := strings.Cut(entry.Path, "/")
		distinct[resourceType] = struct{}{}
	}
	fixture.ResourceCount = len(distinct)
	updated, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal(module HCL compatibility) error: %v", err)
	}
	updated = append(updated, '\n')
	if err := os.WriteFile(fixturePath, updated, 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(updated)
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	if err := fixtureupdate.ReplaceConst(sourcePath, "moduleHCLCompatibilitySHA256", hex.EncodeToString(digest[:])); err != nil {
		t.Fatalf("fixtureupdate.ReplaceConst error: %v", err)
	}
	t.Skipf("module HCL compatibility snapshot regenerated; review the diff before committing")
}

func TestCommittedModuleHCLCompatibility(t *testing.T) {
	fixturePath := filepath.Join("testdata", "module_hcl_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	if fixtureupdate.Requested() {
		updateModuleHCLCompatibility(t, fixturePath, fixtureBytes)
		return
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
	// The pinned fixture digest above is the membership authority; the
	// counts only need to be internally consistent. A hardcoded literal here
	// would correspond to no derivable set anyway -- module generation
	// covers generated types plus data referents, while this fixture's
	// membership is a curated snapshot -- so the literal bought nothing the
	// digest does not already guarantee, and cost a magic-number edit on
	// every membership change.
	distinct := map[string]struct{}{}
	for _, file := range fixture.Files {
		resourceType, _, ok := strings.Cut(file.Path, "/")
		if !ok || resourceType == "" {
			t.Fatalf("%s compatibility path %q is not resource/file", fixturePath, file.Path)
		}
		distinct[resourceType] = struct{}{}
	}
	if fixture.ResourceCount == 0 || fixture.ResourceCount != len(distinct) {
		t.Fatalf("%s resource_count = %d, want the %d distinct resource types its own manifest covers", fixturePath, fixture.ResourceCount, len(distinct))
	}

	requireModulePackSelection(t, metadata.PackSelection{
		Packs: []string{"zcc", "zia", "zpa"}, Shared: []string{"zscaler"},
	})
	root := installedModuleRoot(t)
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
