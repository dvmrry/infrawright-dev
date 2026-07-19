package sourcebind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

const defaultGitTimeout = 5 * time.Second

// LoadVerified captures and verifies every source input named by a canonical
// source-provenance-v1 manifest. It never reopens accepted input bytes, makes
// no network request, and never invokes the Go toolchain. Source-provenance-v1
// supports local replacement directories only. The parent process PATH is a
// trusted input used to locate Git; checkout configuration cannot redirect the
// selected worktree or enable hooks, fsmonitor, paging, or prompting.
func LoadVerified(ctx context.Context, roots LocalRoots) (VerifiedInputs, error) {
	return loadVerified(ctx, roots, loadOptions{gitRunner: localGitRunner{}, timeout: defaultGitTimeout})
}

func loadVerified(ctx context.Context, roots LocalRoots, options loadOptions) (VerifiedInputs, error) {
	if err := ctx.Err(); err != nil {
		return VerifiedInputs{}, failure(ErrorRead, "context", "source input capture was cancelled")
	}
	if err := validateVerifiedRoots(roots); err != nil {
		return VerifiedInputs{}, err
	}
	if options.gitRunner == nil {
		options.gitRunner = localGitRunner{}
	}
	if options.timeout <= 0 {
		options.timeout = defaultGitTimeout
	}

	budget := artifacts.NewDefaultReadBudget()
	manifestSnapshot, err := captureAbsolute(roots.ManifestPath, budget, options.read)
	if err != nil {
		return VerifiedInputs{}, readFailure("manifest", err)
	}
	manifest, err := contracts.DecodeSourceProvenance(manifestSnapshot.Bytes)
	if err != nil {
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, failure(ErrorManifest, "manifest", "must be a valid source-provenance-v1 document")
	}
	canonical, err := contracts.RenderSourceProvenance(manifest)
	if err != nil || string(manifestSnapshot.Bytes) != canonical {
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, failure(ErrorManifest, "manifest", "must equal its canonical source-provenance-v1 bytes")
	}
	if err := validateManifestBindings(manifest); err != nil {
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, err
	}
	if !sameSDKModuleSet(roots.SDKRoots, manifest.SDKs) {
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, failure(ErrorInvalidRoots, "sdks", "SDK root keys must exactly match manifest SDK module keys")
	}
	if err := verifyLocalReplaceTargets(roots.ProviderRoot, roots.SDKRoots, manifest.ProviderModule.LocalReplaces); err != nil {
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, err
	}
	if err := verifyGitSnapshot(ctx, roots, manifest, options); err != nil {
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, err
	}

	// All later reads are serial and ordered by manifest fields. That keeps the
	// shared ReadBudget's observable failures deterministic.
	provider, providerModule, err := captureProvider(roots.ProviderRoot, manifest, budget, options.read)
	if err != nil {
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, err
	}
	schema, err := captureBoundFile(roots.SchemaRoot, manifest.TerraformSchema, budget, options.read, "terraform_schema")
	if err != nil {
		clearTree(provider)
		clearModuleFiles(providerModule)
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, err
	}
	sdks, err := captureSDKs(roots.SDKRoots, manifest.SDKs, budget, options.read)
	if err != nil {
		clearTree(provider)
		clearModuleFiles(providerModule)
		clearBytes(schema.Bytes)
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, err
	}
	if err := verifyModules(manifest, providerModule, sdks); err != nil {
		clearTree(provider)
		clearModuleFiles(providerModule)
		clearBytes(schema.Bytes)
		clearTrees(sdks)
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, err
	}
	if err := verifyTreeDigests(manifest, provider, providerModule, sdks); err != nil {
		clearTree(provider)
		clearModuleFiles(providerModule)
		clearBytes(schema.Bytes)
		clearTrees(sdks)
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, err
	}

	// The optional adapter is intentionally isolated. Its read failure is
	// captured as adapter status after core source trust has already succeeded.
	openAPI := captureOpenAPI(roots.OpenAPIRoot, manifest.OpenAPI, budget, options.read)
	if err := verifyGitSnapshot(ctx, roots, manifest, options); err != nil {
		clearTree(provider)
		clearModuleFiles(providerModule)
		clearBytes(schema.Bytes)
		clearTrees(sdks)
		clearOpenAPI(openAPI)
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, err
	}

	manifestSHA := digest(manifestSnapshot.Bytes)
	input := contracts.InputProvenance{
		Kind:                 "infrawright.input_provenance",
		SchemaVersion:        1,
		SourceTrust:          contracts.SourceTrustVerified,
		SourceManifestSHA256: &manifestSHA,
		SourceManifest:       &manifest,
	}
	inputBytes, err := contracts.RenderInputProvenance(input)
	if err != nil {
		clearTree(provider)
		clearModuleFiles(providerModule)
		clearBytes(schema.Bytes)
		clearTrees(sdks)
		clearOpenAPI(openAPI)
		clearBytes(manifestSnapshot.Bytes)
		return VerifiedInputs{}, failure(ErrorManifest, "input_provenance", "could not render verified input provenance")
	}
	return VerifiedInputs{state: &verifiedState{
		Manifest:              manifest,
		ManifestBytes:         manifestSnapshot.Bytes,
		ManifestSHA256:        manifestSHA,
		Provider:              provider,
		ProviderModule:        sortedModuleFiles(providerModule),
		SDKs:                  sdks,
		TerraformSchema:       schema,
		OpenAPI:               openAPI,
		InputProvenance:       input,
		InputProvenanceBytes:  []byte(inputBytes),
		InputProvenanceSHA256: digest([]byte(inputBytes)),
	}}, nil
}

func validateVerifiedRoots(roots LocalRoots) error {
	if err := validateAbsoluteFile(roots.ManifestPath, "manifest"); err != nil {
		return err
	}
	if err := validateAbsoluteRoot(roots.ProviderRoot, "provider"); err != nil {
		return err
	}
	if err := validateAbsoluteRoot(roots.SchemaRoot, "schema"); err != nil {
		return err
	}
	if roots.OpenAPIRoot != "" {
		if err := validateAbsoluteRoot(roots.OpenAPIRoot, "openapi"); err != nil {
			return err
		}
	}
	if roots.SDKRoots == nil {
		return failure(ErrorInvalidRoots, "sdks", "SDK roots must be an object")
	}
	for module, root := range roots.SDKRoots {
		if module == "" {
			return failure(ErrorInvalidRoots, "sdks", "SDK module keys must be non-empty")
		}
		if err := validateAbsoluteRoot(root, "sdk"); err != nil {
			return err
		}
	}
	return nil
}

func validateAbsoluteRoot(value, binding string) error {
	if value == "" || strings.IndexByte(value, 0) >= 0 || !filepath.IsAbs(value) {
		return failure(ErrorInvalidRoots, binding, "must be an absolute non-NUL path")
	}
	info, err := os.Lstat(value)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return failure(ErrorInvalidRoots, binding, "must name a local non-symlink directory")
	}
	return nil
}

func validateAbsoluteFile(value, binding string) error {
	if value == "" || strings.IndexByte(value, 0) >= 0 || !filepath.IsAbs(value) {
		return failure(ErrorInvalidRoots, binding, "must be an absolute non-NUL path")
	}
	info, err := os.Lstat(value)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return failure(ErrorInvalidRoots, binding, "must name a local non-symlink regular file")
	}
	return nil
}

func validateManifestBindings(manifest contracts.SourceProvenance) error {
	if err := module.CheckPath(manifest.Provider.ModulePath); err != nil {
		return failure(ErrorModule, "provider.module_path", "must be a valid Go module path")
	}
	reserved := map[string]struct{}{manifest.ProviderModule.GoMod.Path: {}}
	if manifest.ProviderModule.GoSum != nil {
		reserved[manifest.ProviderModule.GoSum.Path] = struct{}{}
	}
	for _, file := range manifest.Provider.Files {
		if _, exists := reserved[file.Path]; exists {
			return failure(ErrorManifest, "provider.files", "must not overlap provider go.mod or go.sum bindings")
		}
	}
	for index, sdk := range manifest.SDKs {
		if err := module.CheckPath(sdk.ModulePath); err != nil {
			return failure(ErrorModule, "sdks["+strconv.Itoa(index)+"]", "must be a valid Go module path")
		}
		if err := module.Check(sdk.ModulePath, sdk.ModuleVersion); err != nil {
			return failure(ErrorModule, "sdks["+strconv.Itoa(index)+"]", "must have a valid Go module version")
		}
	}
	return nil
}

func captureProvider(root string, manifest contracts.SourceProvenance, budget *artifacts.ReadBudget, options artifacts.StableReadOptions) (CapturedTree, map[string]CapturedFile, error) {
	files, err := captureBindings(root, manifest.Provider.Files, budget, options, "provider")
	if err != nil {
		return CapturedTree{}, nil, err
	}
	goMod, err := captureBoundFile(root, manifest.ProviderModule.GoMod, budget, options, "provider_module.go_mod")
	if err != nil {
		clearFiles(files)
		return CapturedTree{}, nil, err
	}
	module := map[string]CapturedFile{"go.mod": goMod}
	if manifest.ProviderModule.GoSum != nil {
		goSum, err := captureBoundFile(root, *manifest.ProviderModule.GoSum, budget, options, "provider_module.go_sum")
		if err != nil {
			clearFiles(files)
			clearBytes(goMod.Bytes)
			return CapturedTree{}, nil, err
		}
		module["go.sum"] = goSum
	}
	return CapturedTree{ModulePath: manifest.Provider.ModulePath, Files: files}, module, nil
}

func captureSDKs(roots map[string]string, bindings []contracts.SDKSourceBinding, budget *artifacts.ReadBudget, options artifacts.StableReadOptions) (map[string]CapturedTree, error) {
	if !sameSDKModuleSet(roots, bindings) {
		return nil, failure(ErrorInvalidRoots, "sdks", "SDK root keys must exactly match manifest SDK module keys")
	}
	result := make(map[string]CapturedTree, len(bindings))
	for _, binding := range bindings {
		root, exists := roots[binding.ModulePath]
		if !exists {
			clearTrees(result)
			return nil, failure(ErrorInvalidRoots, "sdks", "SDK root keys must exactly match manifest SDK module keys")
		}
		files, err := captureBindings(root, binding.Files, budget, options, "sdks."+binding.ModulePath)
		if err != nil {
			clearTrees(result)
			return nil, err
		}
		if !hasPath(files, "go.mod") {
			clearFiles(files)
			clearTrees(result)
			return nil, failure(ErrorModule, "sdks."+binding.ModulePath, "must bind go.mod for module identity")
		}
		result[binding.ModulePath] = CapturedTree{ModulePath: binding.ModulePath, Files: files}
	}
	return result, nil
}

func sameSDKModuleSet(roots map[string]string, bindings []contracts.SDKSourceBinding) bool {
	if len(roots) != len(bindings) {
		return false
	}
	for _, binding := range bindings {
		if _, exists := roots[binding.ModulePath]; !exists {
			return false
		}
	}
	return true
}

func captureBindings(root string, bindings []contracts.FileBinding, budget *artifacts.ReadBudget, options artifacts.StableReadOptions, label string) ([]CapturedFile, error) {
	files := make([]CapturedFile, 0, len(bindings))
	for _, binding := range bindings {
		file, err := captureBoundFile(root, binding, budget, options, label)
		if err != nil {
			clearFiles(files)
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func captureBoundFile(root string, binding contracts.FileBinding, budget *artifacts.ReadBudget, options artifacts.StableReadOptions, label string) (CapturedFile, error) {
	path, err := boundPath(root, binding.Path)
	if err != nil {
		return CapturedFile{}, err
	}
	snapshot, err := artifacts.ReadBoundedFileBytes(path, budget, options)
	if err != nil {
		return CapturedFile{}, readFailure(label+":"+binding.Path, err)
	}
	if snapshot.Digest.SHA256 != binding.SHA256 {
		clearBytes(snapshot.Bytes)
		return CapturedFile{}, failure(ErrorBinding, label+":"+binding.Path, "captured SHA-256 does not match manifest")
	}
	return CapturedFile{Path: binding.Path, Bytes: snapshot.Bytes, SHA256: snapshot.Digest.SHA256}, nil
}

func captureAbsolute(filePath string, budget *artifacts.ReadBudget, options artifacts.StableReadOptions) (CapturedFile, error) {
	snapshot, err := artifacts.ReadBoundedFileBytes(filePath, budget, options)
	if err != nil {
		return CapturedFile{}, err
	}
	return CapturedFile{Bytes: snapshot.Bytes, SHA256: snapshot.Digest.SHA256}, nil
}

func boundPath(root, portable string) (string, error) {
	if portable == "" || strings.Contains(portable, "\\") || strings.ContainsRune(portable, 0) || strings.HasPrefix(portable, "/") || strings.HasPrefix(portable, "../") || filepath.Clean(filepath.FromSlash(portable)) != filepath.FromSlash(portable) {
		return "", failure(ErrorBinding, portable, "must be a validated portable relative binding")
	}
	path := root
	for _, part := range strings.Split(portable, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", failure(ErrorRead, portable, "bound path is unavailable")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", failure(ErrorBinding, portable, "bound path must not traverse a symbolic link")
		}
	}
	return path, nil
}

func verifyTreeDigests(manifest contracts.SourceProvenance, provider CapturedTree, providerModule map[string]CapturedFile, sdks map[string]CapturedTree) error {
	providerFiles := append([]CapturedFile(nil), provider.Files...)
	for _, file := range providerModule {
		providerFiles = append(providerFiles, file)
	}
	if got := treeDigest(providerFiles); got != manifest.Provider.TreeSHA256 {
		return failure(ErrorBinding, "provider.tree_sha256", "does not match captured bound bytes")
	}
	for _, sdk := range manifest.SDKs {
		if sdk.TreeSHA256 == nil {
			continue
		}
		if got := treeDigest(sdks[sdk.ModulePath].Files); got != *sdk.TreeSHA256 {
			return failure(ErrorBinding, "sdks."+sdk.ModulePath+".tree_sha256", "does not match captured bound bytes")
		}
	}
	return nil
}

func treeDigest(files []CapturedFile) string {
	ordered := append([]CapturedFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	hash := sha256.New()
	for _, file := range ordered {
		_, _ = hash.Write([]byte(file.Path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(file.SHA256))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func verifyModules(manifest contracts.SourceProvenance, providerModule map[string]CapturedFile, sdks map[string]CapturedTree) error {
	provider, err := modfile.Parse("go.mod", providerModule["go.mod"].Bytes, nil)
	if err != nil || provider.Module == nil || provider.Module.Mod.Path != manifest.Provider.ModulePath {
		return failure(ErrorModule, "provider.go.mod", "module path does not match manifest")
	}
	requires := make(map[string]string, len(provider.Require))
	for _, require := range provider.Require {
		requires[require.Mod.Path] = require.Mod.Version
	}
	for _, sdk := range manifest.SDKs {
		if got, exists := requires[sdk.ModulePath]; !exists || got != sdk.ModuleVersion {
			return failure(ErrorModule, "provider.go.mod", "SDK module requirement does not match manifest")
		}
		sdkModule, err := modfile.Parse("go.mod", fileFor(sdks[sdk.ModulePath], "go.mod").Bytes, nil)
		if err != nil || sdkModule.Module == nil || sdkModule.Module.Mod.Path != sdk.ModulePath {
			return failure(ErrorModule, "sdks."+sdk.ModulePath+".go.mod", "module path does not match manifest")
		}
	}
	for _, sdk := range manifest.UnavailableSDKs {
		if got, exists := requires[sdk.ModulePath]; !exists || got != sdk.ModuleVersion {
			return failure(ErrorModule, "provider.go.mod", "unavailable SDK module requirement does not match manifest")
		}
	}
	return verifyLocalReplaces(manifest.ProviderModule.LocalReplaces, provider.Replace)
}

func verifyLocalReplaces(expected []contracts.LocalModuleReplaceBinding, actual []*modfile.Replace) error {
	gotReplaces := append([]*modfile.Replace(nil), actual...)
	for _, replacement := range gotReplaces {
		if replacement.New.Version != "" {
			return failure(ErrorModule, "provider.go.mod", "source-provenance-v1 accepts only local replacement directories")
		}
	}
	if len(expected) != len(actual) {
		return failure(ErrorModule, "provider.go.mod", "local replace directives do not exactly match manifest")
	}
	sort.Slice(gotReplaces, func(left, right int) bool {
		return replaceKey(gotReplaces[left].Old.Path, gotReplaces[left].Old.Version) <
			replaceKey(gotReplaces[right].Old.Path, gotReplaces[right].Old.Version)
	})
	wantReplaces := append([]contracts.LocalModuleReplaceBinding(nil), expected...)
	sort.Slice(wantReplaces, func(left, right int) bool {
		return replaceKey(wantReplaces[left].ModulePath, optionalString(wantReplaces[left].ModuleVersion)) <
			replaceKey(wantReplaces[right].ModulePath, optionalString(wantReplaces[right].ModuleVersion))
	})
	for index, want := range wantReplaces {
		got := gotReplaces[index]
		if got.Old.Path != want.ModulePath || got.Old.Version != optionalString(want.ModuleVersion) || got.New.Version != "" || got.New.Path != want.LocalPath {
			return failure(ErrorModule, "provider.go.mod", "local replace directives do not exactly match manifest")
		}
	}
	return nil
}

func replaceKey(modulePath, version string) string { return modulePath + "\x00" + version }

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func fileFor(tree CapturedTree, path string) CapturedFile {
	for _, file := range tree.Files {
		if file.Path == path {
			return file
		}
	}
	return CapturedFile{}
}

func hasPath(files []CapturedFile, path string) bool {
	return fileFor(CapturedTree{Files: files}, path).Path != ""
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readFailure(binding string, err error) error {
	return failure(ErrorRead, binding, "could not stably capture bound input")
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func clearFiles(files []CapturedFile) {
	for index := range files {
		clearBytes(files[index].Bytes)
	}
}

func clearTree(tree CapturedTree) { clearFiles(tree.Files) }

func clearTrees(trees map[string]CapturedTree) {
	for _, tree := range trees {
		clearTree(tree)
	}
}

func clearOpenAPI(status OpenAPIStatus) { clearFiles(status.Files) }

func clearModuleFiles(files map[string]CapturedFile) {
	for _, file := range files {
		clearBytes(file.Bytes)
	}
}

func sortedModuleFiles(files map[string]CapturedFile) []CapturedFile {
	result := make([]CapturedFile, 0, len(files))
	for _, file := range files {
		result = append(result, file)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Path < result[right].Path })
	return result
}
