package sourcebind

import (
	"context"
	"path"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"golang.org/x/mod/module"
)

// LoadUnverified captures explicitly selected local bytes for diagnostics only.
// It performs no Git operation and cannot mint a qualification capability.
func LoadUnverified(ctx context.Context, roots UnverifiedRoots) (UnverifiedInputs, error) {
	if err := ctx.Err(); err != nil {
		return UnverifiedInputs{}, failure(ErrorRead, "context", "source input capture was cancelled")
	}
	if err := validateUnverifiedRoots(roots); err != nil {
		return UnverifiedInputs{}, err
	}
	budget := artifacts.NewDefaultReadBudget()
	providerFiles, err := capturePaths(roots.ProviderRoot, roots.ProviderFiles, budget, "provider")
	if err != nil {
		return UnverifiedInputs{}, err
	}
	schema, err := capturePath(roots.SchemaRoot, roots.TerraformSchema, budget, "terraform_schema")
	if err != nil {
		clearFiles(providerFiles)
		return UnverifiedInputs{}, err
	}
	sdks := make(map[string]CapturedTree, len(roots.SDKRoots))
	for _, module := range sortedKeys(roots.SDKRoots) {
		files, err := capturePaths(roots.SDKRoots[module], roots.SDKFiles[module], budget, "sdks."+module)
		if err != nil {
			clearFiles(providerFiles)
			clearBytes(schema.Bytes)
			clearTrees(sdks)
			return UnverifiedInputs{}, err
		}
		sdks[module] = CapturedTree{ModulePath: module, Files: files}
	}
	observation := contracts.UnverifiedSourceObservation{
		ProviderModulePath: roots.ProviderModulePath,
		ProviderFiles:      fileBindings(providerFiles),
		TerraformSchema:    contracts.FileBinding{Path: schema.Path, SHA256: schema.SHA256},
		SDKs:               make([]contracts.UnverifiedSDKObservation, 0, len(sdks)),
		Selection:          roots.Selection,
	}
	for _, module := range sortedKeys(sdks) {
		observation.SDKs = append(observation.SDKs, contracts.UnverifiedSDKObservation{
			ModulePath:    module,
			ModuleVersion: roots.SDKVersions[module],
			Files:         fileBindings(sdks[module].Files),
		})
	}
	input := contracts.InputProvenance{
		Kind:                  "infrawright.input_provenance",
		SchemaVersion:         1,
		SourceTrust:           contracts.SourceTrustUnverified,
		UnverifiedObservation: &observation,
	}
	inputBytes, err := contracts.RenderInputProvenance(input)
	if err != nil {
		clearFiles(providerFiles)
		clearBytes(schema.Bytes)
		clearTrees(sdks)
		return UnverifiedInputs{}, failure(ErrorManifest, "input_provenance", "could not render unverified input provenance")
	}
	return UnverifiedInputs{
		Observation:           observation,
		Provider:              CapturedTree{ModulePath: roots.ProviderModulePath, Files: providerFiles},
		SDKs:                  sdks,
		TerraformSchema:       schema,
		InputProvenance:       input,
		InputProvenanceBytes:  []byte(inputBytes),
		InputProvenanceSHA256: digest([]byte(inputBytes)),
	}, nil
}

func validateUnverifiedRoots(roots UnverifiedRoots) error {
	if roots.ProviderModulePath == "" {
		return failure(ErrorInvalidRoots, "provider", "provider module path must be non-empty")
	}
	if err := module.CheckPath(roots.ProviderModulePath); err != nil {
		return failure(ErrorModule, "provider", "provider module path must be a valid Go module path")
	}
	if err := validateAbsoluteRoot(roots.ProviderRoot, "provider"); err != nil {
		return err
	}
	if err := validateAbsoluteRoot(roots.SchemaRoot, "schema"); err != nil {
		return err
	}
	if len(roots.ProviderFiles) == 0 || roots.TerraformSchema == "" {
		return failure(ErrorInvalidRoots, "inputs", "provider files and schema path must be non-empty")
	}
	if len(roots.SDKRoots) != len(roots.SDKFiles) || len(roots.SDKRoots) != len(roots.SDKVersions) {
		return failure(ErrorInvalidRoots, "sdks", "SDK roots, file keys, and version keys must exactly match")
	}
	for sdkModule, root := range roots.SDKRoots {
		if sdkModule == "" || len(roots.SDKFiles[sdkModule]) == 0 {
			return failure(ErrorInvalidRoots, "sdks", "SDK module keys and file sets must be non-empty")
		}
		if err := validateAbsoluteRoot(root, "sdk"); err != nil {
			return err
		}
		if err := module.CheckPath(sdkModule); err != nil {
			return failure(ErrorModule, "sdks", "SDK module path must be a valid Go module path")
		}
		if err := module.Check(sdkModule, roots.SDKVersions[sdkModule]); err != nil {
			return failure(ErrorModule, "sdks", "SDK module version must be a valid Go module version")
		}
	}
	return nil
}

func capturePaths(root string, paths []string, budget *artifacts.ReadBudget, label string) ([]CapturedFile, error) {
	if !sort.StringsAreSorted(paths) {
		return nil, failure(ErrorInvalidRoots, label, "explicit diagnostic paths must be sorted")
	}
	files := make([]CapturedFile, 0, len(paths))
	for _, path := range paths {
		file, err := capturePath(root, path, budget, label)
		if err != nil {
			clearFiles(files)
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func capturePath(root, path string, budget *artifacts.ReadBudget, label string) (CapturedFile, error) {
	if !isPortableDiagnosticPath(path) {
		return CapturedFile{}, failure(ErrorInvalidRoots, label, "explicit diagnostic path must be portable and relative")
	}
	local, err := boundPath(root, path)
	if err != nil {
		return CapturedFile{}, err
	}
	snapshot, err := artifacts.ReadBoundedFileBytes(local, budget, artifacts.StableReadOptions{})
	if err != nil {
		return CapturedFile{}, readFailure(label+":"+path, err)
	}
	return CapturedFile{Path: path, Bytes: snapshot.Bytes, SHA256: snapshot.Digest.SHA256}, nil
}

func isPortableDiagnosticPath(value string) bool {
	return value != "" && value != "." &&
		!strings.Contains(value, "\\") && !strings.ContainsRune(value, 0) &&
		!strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "../") &&
		path.Clean(value) == value
}

func fileBindings(files []CapturedFile) []contracts.FileBinding {
	bindings := make([]contracts.FileBinding, 0, len(files))
	for _, file := range files {
		bindings = append(bindings, contracts.FileBinding{Path: file.Path, SHA256: file.SHA256})
	}
	return bindings
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
