package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type filesystemProbeOutcome struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type filesystemProbeResults struct {
	MissingProfile   filesystemProbeOutcome `json:"missingProfile"`
	RegularPackRoot  filesystemProbeOutcome `json:"regularPackRoot"`
	RegularOverrides filesystemProbeOutcome `json:"regularOverrides"`
	RegistryDir      filesystemProbeOutcome `json:"registryDir"`
	MissingPackRoot  filesystemProbeOutcome `json:"missingPackRoot"`
	MissingOverrides filesystemProbeOutcome `json:"missingOverrides"`
}

type filesystemProbeInput struct {
	Bundle                   string `json:"bundle"`
	Root                     string `json:"root"`
	MissingProfile           string `json:"missingProfile"`
	RegularPackRoot          string `json:"regularPackRoot"`
	ManifestDirectory        string `json:"manifestDirectory"`
	ManifestPath             string `json:"manifestPath"`
	MissingPackRoot          string `json:"missingPackRoot"`
	MissingOverridesDir      string `json:"missingOverridesDir"`
	MissingOverridesManifest string `json:"missingOverridesManifest"`
}

// TestMetadataFilesystemErrorsMatchNode2415 compiles the authoritative
// TypeScript metadata modules and compares their Node 24.15 error bytes with
// the Go integration. The quote in each failing path pins Node's deliberately
// unescaped SystemError path rendering as well as the operation/code wording.
func TestMetadataFilesystemErrorsMatchNode2415(t *testing.T) {
	repository := repoRoot(t)
	node, bundle := buildMetadataFilesystemOracle(t, repository)

	fixtureRoot := t.TempDir()
	missingProfile := filepath.Join(fixtureRoot, "missing'profile.json")
	regularPackRoot := filepath.Join(fixtureRoot, "packs'file")
	writeRawFile(t, regularPackRoot, "not a directory")

	manifestDirectory := filepath.Join(fixtureRoot, "pack'quoted")
	manifestPath := filepath.Join(manifestDirectory, "pack.json")
	writeRawFile(t, manifestPath, "{}")
	writeRawFile(t, filepath.Join(manifestDirectory, "overrides"), "not a directory")
	if err := os.Mkdir(filepath.Join(manifestDirectory, "registry.json"), 0o755); err != nil {
		t.Fatalf("os.Mkdir(registry directory %q) error = %v, want nil", manifestDirectory, err)
	}

	missingPackRoot := filepath.Join(fixtureRoot, "missing'packs")
	missingOverridesDir := filepath.Join(fixtureRoot, "pack'without-overrides")
	missingOverridesManifest := filepath.Join(missingOverridesDir, "pack.json")
	writeRawFile(t, missingOverridesManifest, "{}")

	input := filesystemProbeInput{
		Bundle:                   bundle,
		Root:                     fixtureRoot,
		MissingProfile:           missingProfile,
		RegularPackRoot:          regularPackRoot,
		ManifestDirectory:        manifestDirectory,
		ManifestPath:             manifestPath,
		MissingPackRoot:          missingPackRoot,
		MissingOverridesDir:      missingOverridesDir,
		MissingOverridesManifest: missingOverridesManifest,
	}
	want := runMetadataFilesystemOracle(t, node, input)

	metadata := probePackMetadata(fixtureRoot, manifestDirectory, manifestPath)
	missingOverridesMetadata := probePackMetadata(
		fixtureRoot, missingOverridesDir, missingOverridesManifest,
	)
	loadedRoot := LoadedPackRoot{
		Root:      fixtureRoot,
		Packs:     metadata,
		Resources: map[string]LoadedResourceMetadata{},
	}

	_, missingProfileErr := LoadPackSetDocument(missingProfile, PackSetKind)
	_, regularPackRootErr := LoadPackMetadata(regularPackRoot)
	_, regularOverridesErr := LoadOverrides(metadata, nil)
	_, registryDirErr := BuildRootCatalog(loadedRoot, nil)
	_, missingPackRootErr := LoadPackMetadata(missingPackRoot)
	_, missingOverridesErr := LoadOverrides(missingOverridesMetadata, nil)

	assertFilesystemProbeError(t, "LoadPackSetDocument(missing profile)", missingProfileErr, want.MissingProfile)
	assertFilesystemProbeError(t, "LoadPackMetadata(regular-file root)", regularPackRootErr, want.RegularPackRoot)
	assertFilesystemProbeError(t, "LoadOverrides(regular-file overrides)", regularOverridesErr, want.RegularOverrides)
	assertFilesystemProbeError(t, "BuildRootCatalog(registry directory)", registryDirErr, want.RegistryDir)
	assertFilesystemProbeSuccess(t, "LoadPackMetadata(missing root)", missingPackRootErr, want.MissingPackRoot)
	assertFilesystemProbeSuccess(t, "LoadOverrides(missing overrides)", missingOverridesErr, want.MissingOverrides)

	for name, outcome := range map[string]filesystemProbeOutcome{
		"missing profile":        want.MissingProfile,
		"regular pack root":      want.RegularPackRoot,
		"regular overrides path": want.RegularOverrides,
	} {
		if !strings.Contains(outcome.Error, "'") {
			t.Errorf("Node 24.15 %s error = %q, want raw single-quoted path evidence", name, outcome.Error)
		}
	}
	if strings.Contains(want.RegistryDir.Error, filepath.Join(manifestDirectory, "registry.json")) {
		t.Errorf("Node 24.15 registry-directory error = %q, want EISDIR read form without a path", want.RegistryDir.Error)
	}

	var metadataErr *MetadataError
	if !errors.As(missingProfileErr, &metadataErr) {
		t.Errorf("LoadPackSetDocument(%q) error type = %T, want *MetadataError", missingProfile, missingProfileErr)
	}
	for name, err := range map[string]error{
		"LoadPackMetadata": regularPackRootErr,
		"LoadOverrides":    regularOverridesErr,
		"BuildRootCatalog": registryDirErr,
	} {
		metadataErr = nil
		if errors.As(err, &metadataErr) {
			t.Errorf("%s raw filesystem error type = %T, must not be *MetadataError", name, err)
		}
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) {
			t.Errorf("%s raw filesystem error chain = %v, want retained *os.PathError cause", name, err)
		}
	}
}

func probePackMetadata(root, directory, manifestPath string) PackMetadata {
	manifest := PackManifest{
		Name:             "sample",
		Directory:        directory,
		Path:             manifestPath,
		Data:             JsonObject{},
		ProviderPrefixes: map[string]string{"sample_": "sample"},
		ProviderSources:  map[string]string{},
		RequiresShared:   []string{},
	}
	return PackMetadata{
		Root:             root,
		Manifests:        []PackManifest{manifest},
		ProviderPrefixes: map[string]string{"sample_": "sample"},
		ProviderSources:  map[string]string{},
		ProviderOwners:   map[string]string{"sample": "sample"},
	}
}

func buildMetadataFilesystemOracle(t *testing.T, repository string) (string, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node 24.15 metadata differential unavailable: %v", err)
	}
	version, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Skipf("Node 24.15 metadata differential unavailable: node --version: %v", err)
	}
	if got := strings.TrimSpace(string(version)); !strings.HasPrefix(got, "v24.15.") {
		t.Skipf("Node 24.15 metadata differential requires v24.15.x, got %s", got)
	}

	esbuild := filepath.Join(repository, "node_modules", ".bin", "esbuild")
	if _, err := os.Stat(esbuild); err != nil {
		t.Skipf("Node metadata differential requires the pinned esbuild install: %v", err)
	}

	temporary := t.TempDir()
	entryPath := filepath.Join(temporary, "metadata-filesystem-oracle.ts")
	bundlePath := filepath.Join(temporary, "metadata-filesystem-oracle.mjs")
	metadataSource := filepath.ToSlash(filepath.Join(repository, "node-src", "metadata"))
	entry := fmt.Sprintf(
		"export { loadPackMetadata, loadPackSetDocument } from %q;\n"+
			"export { loadOverrides } from %q;\n"+
			"export { buildRootCatalog } from %q;\n",
		metadataSource+"/packs.ts",
		metadataSource+"/resources.ts",
		metadataSource+"/root-catalog.ts",
	)
	if err := os.WriteFile(entryPath, []byte(entry), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", entryPath, err)
	}
	command := exec.Command(
		esbuild,
		entryPath,
		"--bundle",
		"--platform=node",
		"--format=esm",
		"--outfile="+bundlePath,
	)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("esbuild metadata filesystem oracle error = %v, want nil; output:\n%s", err, output)
	}
	return node, bundlePath
}

func runMetadataFilesystemOracle(t *testing.T, node string, input filesystemProbeInput) filesystemProbeResults {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal(filesystemProbeInput) error = %v, want nil", err)
	}
	const script = `
import { pathToFileURL } from "node:url";

let encodedInput = "";
for await (const chunk of process.stdin) encodedInput += chunk;
const input = JSON.parse(encodedInput);
const oracle = await import(pathToFileURL(input.bundle).href);
const capture = async (work) => {
  try {
    await work();
    return { ok: true, error: "" };
  } catch (error) {
    return {
      ok: false,
      error: error instanceof Error ? error.message : String(error),
    };
  }
};
const metadataFor = (directory, manifestPath) => ({
  root: input.root,
  manifests: [{
    name: "sample",
    directory,
    path: manifestPath,
    data: {},
    providerPrefixes: { "sample_": "sample" },
    providerSources: {},
    requiresShared: [],
  }],
  providerPrefixes: { "sample_": "sample" },
  providerSources: {},
  providerOwners: { sample: "sample" },
});
const metadata = metadataFor(input.manifestDirectory, input.manifestPath);
const missingOverridesMetadata = metadataFor(
  input.missingOverridesDir,
  input.missingOverridesManifest,
);
const root = { root: input.root, packs: metadata, resources: new Map() };
const results = {
  missingProfile: await capture(() => oracle.loadPackSetDocument(
    input.missingProfile,
    "infrawright.pack-set",
  )),
  regularPackRoot: await capture(() => oracle.loadPackMetadata(input.regularPackRoot)),
  regularOverrides: await capture(() => oracle.loadOverrides(metadata)),
  registryDir: await capture(() => oracle.buildRootCatalog(root)),
  missingPackRoot: await capture(() => oracle.loadPackMetadata(input.missingPackRoot)),
  missingOverrides: await capture(() => oracle.loadOverrides(missingOverridesMetadata)),
};
process.stdout.write(JSON.stringify(results));
`
	command := exec.Command(node, "--input-type=module", "--eval", script)
	command.Stdin = bytes.NewReader(encoded)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Node metadata filesystem oracle error = %v, want nil; stderr:\n%s", err, stderr.String())
	}
	var results filesystemProbeResults
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatalf("json.Unmarshal(Node metadata filesystem oracle %q) error = %v, want nil", output, err)
	}
	return results
}

func assertFilesystemProbeError(t *testing.T, call string, got error, want filesystemProbeOutcome) {
	t.Helper()
	if want.OK {
		t.Errorf("Node 24.15 %s success = true, want an error for this fixture", call)
		return
	}
	if got == nil {
		t.Errorf("%s error = nil, want %q", call, want.Error)
		return
	}
	if got.Error() != want.Error {
		t.Errorf("%s error = %q, want Node 24.15 error %q", call, got.Error(), want.Error)
	}
}

func assertFilesystemProbeSuccess(t *testing.T, call string, got error, want filesystemProbeOutcome) {
	t.Helper()
	if !want.OK {
		t.Errorf("Node 24.15 %s error = %q, want success for the ENOENT branch", call, want.Error)
		return
	}
	if got != nil {
		t.Errorf("%s error = %v, want nil to match Node 24.15", call, got)
	}
}

// TestRecoverMetadataErrorDoesNotCatchArbitraryErrorPanic locks the private
// passthrough boundary: an unrelated error panic remains a programmer bug and
// must not be coerced into an ordinary metadata return.
func TestRecoverMetadataErrorDoesNotCatchArbitraryErrorPanic(t *testing.T) {
	marker := errors.New("arbitrary programmer panic")
	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_ = func() (err error) {
			defer recoverMetadataError(&err)
			panic(marker)
		}()
	}()
	if recovered != marker {
		t.Errorf("recoverMetadataError(arbitrary error panic) recovered = %v, want original marker %v", recovered, marker)
	}
}
