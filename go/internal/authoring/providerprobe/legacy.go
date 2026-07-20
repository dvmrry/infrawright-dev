package providerprobe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapimap"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceoperation"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/oasdiff/yaml"
	yaml3 "github.com/oasdiff/yaml3"
)

const (
	legacySourceMarker     = ".infrawright-provider-probe-source"
	legacySourceMarkerText = "owned by engine.provider_probe; safe to replace on next probe run\n"
	legacyWorkMarker       = ".infrawright-provider-probe-work"
	legacyWorkMarkerText   = "owned by engine.provider_probe; private legacy work directory\n"
)

// legacyAfterWorkRootBound is test-only synchronization for the rebind
// regression. Production leaves it nil.
var legacyAfterWorkRootBound func()

// runLegacy executes only the frozen compatibility path. It returns detached
// artifacts and never creates the public artifacts directory named in the
// rendered Markdown.
func runLegacy(ctx context.Context, recipe loadedRecipe, options RunOptions) (Result, error) {
	if recipe.mode != LegacyV1 {
		return Result{}, fmt.Errorf("legacy runner requires a legacy recipe")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	work, err := legacyWorkDirectory(recipe, options.WorkDirectory)
	if err != nil {
		return Result{}, err
	}
	_, schema, err := prepareLegacySchema(ctx, recipe, work, options)
	if err != nil {
		return Result{}, err
	}
	_, openAPI, err := prepareLegacyOpenAPI(ctx, recipe, work, options)
	if err != nil {
		return Result{}, err
	}
	sourceRoot, err := prepareLegacySource(ctx, recipe, work, options)
	if err != nil {
		return Result{}, err
	}
	document, err := legacyDocument(ctx, openAPI)
	if err != nil {
		return Result{}, err
	}
	providerSource := optional(recipe.provider)
	resourcePrefix := stringOr(recipe.resource, "")
	sourceReport, err := sourceoperation.DeriveLegacySourceOperationRegistry(sourceoperation.LegacyOptions{SchemaData: schema, OpenAPI: openAPI, SourceRoot: sourceRoot, ProviderSource: deref(providerSource), ResourcePrefix: resourcePrefix})
	if err != nil {
		return Result{}, fmt.Errorf("derive legacy source registry: %w", err)
	}
	mapReport, err := openapimap.Build(ctx, openapimap.Options{SchemaData: schema, Document: document, ProviderSource: providerSource, ResourcePrefix: resourcePrefix, APIPrefix: optional(recipe.api), RegistryData: objectPointer(sourceReport, "registry")})
	if err != nil {
		return Result{}, fmt.Errorf("build legacy OpenAPI map: %w", err)
	}
	openAPIReport := mapReport.Data()
	profile, err := openAPIOperationProfile(openAPI)
	if err != nil {
		return Result{}, err
	}
	summary, err := buildLegacySummary(recipe, sourceReport, openAPIReport, profile)
	if err != nil {
		return Result{}, err
	}
	paths := legacyArtifactPaths(work)
	registry, ok := sourceReport["registry"].(map[string]any)
	if !ok {
		return Result{}, fmt.Errorf("source registry must be an object")
	}
	diagnostics := map[string]any{"diagnostics": sourceReport["diagnostics"], "summary": sourceReport["summary"]}
	registryBytes, err := renderLegacyJSON(registry)
	if err != nil {
		return Result{}, err
	}
	diagnosticsBytes, err := renderLegacyJSON(diagnostics)
	if err != nil {
		return Result{}, err
	}
	mapBytes, err := renderLegacyJSON(openAPIReport)
	if err != nil {
		return Result{}, err
	}
	summaryBytes, err := renderLegacyJSON(summary)
	if err != nil {
		return Result{}, err
	}
	markdown, err := renderLegacyMarkdown(summary, paths)
	if err != nil {
		return Result{}, err
	}
	return Result{mode: LegacyV1, workDirectory: work, artifacts: []Artifact{{Name: "source-registry.json", Bytes: registryBytes}, {Name: "source-diagnostics.json", Bytes: diagnosticsBytes}, {Name: "openapi-map.json", Bytes: mapBytes}, {Name: "summary.json", Bytes: summaryBytes}, {Name: "summary.md", Bytes: []byte(markdown)}}}, nil
}

func legacyWorkDirectory(recipe loadedRecipe, requested string) (string, error) {
	if requested != "" {
		absolute, err := filepath.Abs(requested)
		if err != nil {
			return "", err
		}
		if err := claimLegacyWorkDirectory(absolute); err != nil {
			return "", err
		}
		binding, bindErr := bindLegacyWorkspace(absolute)
		if bindErr != nil {
			return "", bindErr
		}
		defer binding.Close()
		if validateErr := binding.rejectStaticAliases(); validateErr != nil {
			return "", validateErr
		}
		return absolute, nil
	}
	work, err := os.MkdirTemp("", "infrawright-provider-probe-")
	if err != nil {
		return "", err
	}
	if err := writeLegacyMarker(work, legacyWorkMarker, legacyWorkMarkerText); err != nil {
		return "", err
	}
	binding, bindErr := bindLegacyWorkspace(work)
	if bindErr != nil {
		return "", bindErr
	}
	defer binding.Close()
	if validateErr := binding.rejectStaticAliases(); validateErr != nil {
		return "", validateErr
	}
	return work, nil
}

func claimLegacyWorkDirectory(work string) error {
	info, err := os.Lstat(work)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(work, 0700); err != nil {
			return fmt.Errorf("create legacy work directory: %w", err)
		}
		return writeLegacyMarker(work, legacyWorkMarker, legacyWorkMarkerText)
	}
	if err != nil {
		return fmt.Errorf("inspect legacy work directory: %w", err)
	}
	if !privateDirectory(info) {
		return fmt.Errorf("legacy work directory must be a private non-symlink directory: %s", work)
	}
	empty, marked, err := legacyDirectoryState(work, legacyWorkMarker, legacyWorkMarkerText)
	if err != nil {
		return err
	}
	if !empty && !marked {
		return fmt.Errorf("refusing to use existing legacy work directory without probe marker: %s", work)
	}
	if empty {
		return writeLegacyMarker(work, legacyWorkMarker, legacyWorkMarkerText)
	}
	return nil
}

func privateDirectory(info os.FileInfo) bool {
	return info.Mode().IsDir() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm() == 0700
}

func legacyDirectoryState(root, marker, expected string) (empty bool, marked bool, err error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false, false, fmt.Errorf("read legacy directory: %w", err)
	}
	if len(entries) == 0 {
		return true, false, nil
	}
	valid, err := legacyMarker(root, marker, expected)
	if err != nil {
		return false, false, err
	}
	return false, valid, nil
}

func legacyMarker(root, marker, expected string) (bool, error) {
	path := filepath.Join(root, marker)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect legacy probe marker: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read legacy probe marker: %w", err)
	}
	return string(bytes) == expected, nil
}

func writeLegacyMarker(root, marker, contents string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.Mode().IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("legacy probe directory is not a non-symlink directory: %s", root)
	}
	bound, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("bind legacy probe directory: %w", err)
	}
	defer bound.Close()
	boundInfo, err := bound.Stat(".")
	if err != nil || !boundInfo.IsDir() || boundInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, boundInfo) {
		return fmt.Errorf("legacy probe directory changed while binding: %s", root)
	}
	if err := legacyAtomicWrite(bound, marker, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("write legacy probe marker: %w", err)
	}
	return nil
}

func legacyInputsRoot(work string) (*legacyWorkspaceBinding, *os.Root, os.FileInfo, string, error) {
	binding, err := bindLegacyWorkspace(work)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if err := binding.rejectStaticAliases(); err != nil {
		_ = binding.Close()
		return nil, nil, nil, "", err
	}
	inputs, info, err := binding.directory("inputs")
	if err != nil {
		_ = binding.Close()
		return nil, nil, nil, "", err
	}
	return binding, inputs, info, filepath.Join(work, "inputs"), nil
}

func prepareLegacySchema(ctx context.Context, recipe loadedRecipe, work string, options RunOptions) (string, map[string]any, error) {
	binding, inputsRoot, _, inputs, err := legacyInputsRoot(work)
	if err != nil {
		return "", nil, err
	}
	defer binding.Close()
	defer inputsRoot.Close()
	output := filepath.Join(inputs, "provider-schema.json")
	if !falsey(recipe.schema.path) {
		bytes, readErr := os.ReadFile(recipePath(recipe, *recipe.schema.path))
		if readErr != nil {
			return "", nil, readErr
		}
		data, decodeErr := decodeLegacyJSON(bytes)
		if decodeErr != nil {
			return "", nil, decodeErr
		}
		rendered, renderErr := renderLegacyJSON(data)
		if renderErr != nil {
			return "", nil, renderErr
		}
		if writeErr := legacyAtomicWrite(inputsRoot, "provider-schema.json", rendered, 0o600); writeErr != nil {
			return "", nil, writeErr
		}
		return output, data, nil
	}
	if options.LegacyHost == nil {
		return "", nil, fmt.Errorf("legacy host is required to capture Terraform schema")
	}
	provider := stringOr(recipe.provider, "")
	terraformRoot, terraformInfo, err := binding.directory("terraform-schema")
	if err != nil {
		return "", nil, err
	}
	defer terraformRoot.Close()
	terraformDir := filepath.Join(work, "terraform-schema")
	hcl := []byte(terraformSchemaHCL(recipe.terraform, provider, recipe.version))
	if err := binding.verifyDirectoryPath("terraform-schema", terraformInfo); err != nil {
		return "", nil, err
	}
	bytes, captureErr := options.LegacyHost.CaptureTerraformSchema(ctx, TerraformSchemaRequest{TerraformExecutable: stringOr(recipe.tools.terraform, "terraform"), Directory: terraformDir, MainHCL: hcl, Environment: cloneStringMap(options.Environment), legacyWorkspace: binding, legacyDirectory: "terraform-schema", legacyDirectoryInfo: terraformInfo, legacyDirectoryRoot: terraformRoot})
	if captureErr != nil {
		return "", nil, captureErr
	}
	data, decodeErr := decodeLegacyJSON(bytes)
	if decodeErr != nil {
		return "", nil, decodeErr
	}
	rendered, renderErr := renderLegacyJSON(data)
	if renderErr != nil {
		return "", nil, renderErr
	}
	if writeErr := legacyAtomicWrite(inputsRoot, "provider-schema.json", rendered, 0o600); writeErr != nil {
		return "", nil, writeErr
	}
	return output, data, nil
}

func prepareLegacyOpenAPI(ctx context.Context, recipe loadedRecipe, work string, options RunOptions) (string, map[string]any, error) {
	binding, inputsRoot, inputsInfo, inputs, err := legacyInputsRoot(work)
	if err != nil {
		return "", nil, err
	}
	defer binding.Close()
	defer inputsRoot.Close()
	output := filepath.Join(inputs, "openapi.json")
	var bytes []byte
	if !falsey(recipe.openAPI.path) {
		bytes, err = os.ReadFile(recipePath(recipe, *recipe.openAPI.path))
		if err != nil {
			return "", nil, err
		}
	} else {
		if options.LegacyHost == nil {
			return "", nil, fmt.Errorf("legacy host is required to download OpenAPI input")
		}
		raw := filepath.Join(inputs, "openapi.raw")
		if err = binding.verifyDirectoryPath("inputs", inputsInfo); err != nil {
			return "", nil, err
		}
		if err = options.LegacyHost.Download(ctx, DownloadRequest{URL: stringOr(recipe.openAPI.url, ""), Destination: raw, legacyDestinationRoot: inputsRoot, legacyDestinationName: "openapi.raw"}); err != nil {
			return "", nil, err
		}
		bytes, err = legacyReadRegular(inputsRoot, "openapi.raw")
		if err != nil {
			return "", nil, err
		}
	}
	data, err := decodeLegacyOpenAPI(bytes, isYAML(recipe.openAPI, recipe.openAPI.path, recipe.openAPI.url))
	if err != nil {
		return "", nil, err
	}
	rendered, err := renderLegacyJSON(data)
	if err != nil {
		return "", nil, err
	}
	if err = legacyAtomicWrite(inputsRoot, "openapi.json", rendered, 0o600); err != nil {
		return "", nil, err
	}
	return output, data, nil
}
func isYAML(spec recipeOpenAPI, path, url *string) bool {
	if !falsey(spec.format) {
		v := strings.ToLower(*spec.format)
		return v == "yaml" || v == "yml"
	}
	target := ""
	if !falsey(path) {
		target = *path
	} else if !falsey(url) {
		target = *url
	}
	target = strings.ToLower(target)
	return strings.HasSuffix(target, ".yaml") || strings.HasSuffix(target, ".yml")
}
func prepareLegacySource(ctx context.Context, recipe loadedRecipe, work string, options RunOptions) (string, error) {
	root := ""
	if !falsey(recipe.source.path) {
		root = recipePath(recipe, *recipe.source.path)
	} else {
		if options.LegacyHost == nil {
			return "", fmt.Errorf("legacy host is required to clone provider source")
		}
		root = filepath.Join(work, "source")
		workInfo, err := clearLegacyCloneDestination(work)
		if err != nil {
			return "", err
		}
		if err := recheckLegacyWorkPath(work, workInfo); err != nil {
			return "", err
		}
		binding, err := bindLegacyWorkspace(work)
		if err != nil {
			return "", err
		}
		if err := binding.rejectStaticAliases(); err != nil {
			_ = binding.Close()
			return "", err
		}
		if err := binding.recheckPublicPath(); err != nil {
			_ = binding.Close()
			return "", err
		}
		cloneErr := options.LegacyHost.Clone(ctx, CloneRequest{Repository: stringOr(recipe.source.git, ""), Revision: stringOr(recipe.source.ref, ""), Destination: root, legacyWorkspace: binding})
		closeErr := binding.Close()
		if cloneErr != nil {
			return "", cloneErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if err := recheckLegacyWorkPath(work, workInfo); err != nil {
			return "", err
		}
		if err := writeLegacyMarker(root, legacySourceMarker, legacySourceMarkerText); err != nil {
			return "", err
		}
	}
	if !falsey(recipe.source.subdir) {
		root = filepath.Join(root, *recipe.source.subdir)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("provider source root does not exist: %s", root)
	}
	return root, nil
}

func clearLegacyCloneDestination(work string) (os.FileInfo, error) {
	workInfo, err := os.Lstat(work)
	if err != nil || !privateDirectory(workInfo) {
		return nil, fmt.Errorf("legacy work directory is not a private non-symlink directory: %s", work)
	}
	workRoot, err := os.OpenRoot(work)
	if err != nil {
		return nil, fmt.Errorf("bind legacy work directory: %w", err)
	}
	defer workRoot.Close()
	boundWorkInfo, err := workRoot.Stat(".")
	if err != nil || !os.SameFile(workInfo, boundWorkInfo) {
		return nil, fmt.Errorf("legacy work directory changed while binding: %s", work)
	}
	if legacyAfterWorkRootBound != nil {
		legacyAfterWorkRootBound()
	}
	info, err := workRoot.Lstat("source")
	if errors.Is(err, os.ErrNotExist) {
		return workInfo, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect provider source destination: %w", err)
	}
	if !info.Mode().IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to replace provider source destination that is not a directory: %s", filepath.Join(work, "source"))
	}
	sourceRoot, err := workRoot.OpenRoot("source")
	if err != nil {
		return nil, fmt.Errorf("bind provider source destination: %w", err)
	}
	defer sourceRoot.Close()
	boundSourceInfo, err := sourceRoot.Stat(".")
	if err != nil || !os.SameFile(info, boundSourceInfo) {
		return nil, fmt.Errorf("provider source destination changed while binding: %s", filepath.Join(work, "source"))
	}
	empty, marked, err := legacyBoundDirectoryState(sourceRoot, legacySourceMarker, legacySourceMarkerText)
	if err != nil {
		return nil, err
	}
	if !empty && !marked {
		return nil, fmt.Errorf("refusing to replace existing provider source directory without probe marker: %s", filepath.Join(work, "source"))
	}
	latest, err := workRoot.Lstat("source")
	if err != nil || !latest.Mode().IsDir() || latest.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, latest) {
		return nil, fmt.Errorf("provider source destination changed before replacement: %s", filepath.Join(work, "source"))
	}
	empty, marked, err = legacyBoundDirectoryState(sourceRoot, legacySourceMarker, legacySourceMarkerText)
	if err != nil || (!empty && !marked) {
		return nil, fmt.Errorf("provider source destination is no longer probe-owned: %s", filepath.Join(work, "source"))
	}
	if err := workRoot.RemoveAll("source"); err != nil {
		return nil, fmt.Errorf("remove prior probe-owned provider source: %w", err)
	}
	return workInfo, nil
}

func legacyBoundDirectoryState(root *os.Root, marker, expected string) (empty bool, marked bool, err error) {
	directory, err := root.Open(".")
	if err != nil {
		return false, false, fmt.Errorf("open bound legacy directory: %w", err)
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return false, false, fmt.Errorf("read bound legacy directory: %w", readErr)
	}
	if closeErr != nil {
		return false, false, fmt.Errorf("close bound legacy directory: %w", closeErr)
	}
	if len(entries) == 0 {
		return true, false, nil
	}
	valid, err := legacyBoundMarker(root, marker, expected)
	return false, valid, err
}

func legacyBoundMarker(root *os.Root, marker, expected string) (bool, error) {
	info, err := root.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect bound legacy probe marker: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	bytes, err := root.ReadFile(marker)
	if err != nil {
		return false, fmt.Errorf("read bound legacy probe marker: %w", err)
	}
	return string(bytes) == expected, nil
}

func recheckLegacyWorkPath(work string, expected os.FileInfo) error {
	current, err := os.Lstat(work)
	if err != nil || !privateDirectory(current) || !os.SameFile(expected, current) {
		return fmt.Errorf("legacy work directory changed before clone: %s", work)
	}
	return nil
}

func decodeLegacyJSON(bytes []byte) (map[string]any, error) {
	value, err := canonjson.ParseDataJSONLosslessly(string(bytes))
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("input must contain a JSON object")
	}
	return object, nil
}
func decodeLegacyOpenAPI(bytes []byte, yamlInput bool) (map[string]any, error) {
	if !yamlInput {
		return decodeLegacyJSON(bytes)
	}
	var node yaml3.Node
	if err := yaml3.Unmarshal(bytes, &node); err != nil {
		return nil, fmt.Errorf("parse OpenAPI as YAML: %w", err)
	}
	if err := validateLegacyYAMLTags(&node); err != nil {
		return nil, err
	}
	var raw any
	if _, err := yaml.Unmarshal(bytes, &raw, yaml.DecodeOpts{DisableTimestamps: true}); err != nil {
		return nil, fmt.Errorf("parse OpenAPI as YAML: %w", err)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	return decodeLegacyJSON(encoded)
}

func validateLegacyYAMLTags(node *yaml3.Node) error {
	switch node.Tag {
	case "", "!!map", "!!seq", "!!str", "!!int", "!!float", "!!bool", "!!null":
	default:
		return fmt.Errorf("parse OpenAPI as YAML: unsupported YAML tag %q", node.Tag)
	}
	for _, child := range node.Content {
		if err := validateLegacyYAMLTags(child); err != nil {
			return err
		}
	}
	return nil
}
func legacyDocument(ctx context.Context, openAPI map[string]any) (openapiadapter.Document, error) {
	if err := validateLegacyOpenAPI(openAPI); err != nil {
		return openapiadapter.Document{}, err
	}
	bytes, err := renderLegacyJSON(openAPI)
	if err != nil {
		return openapiadapter.Document{}, err
	}
	sum := sha256.Sum256(bytes)
	return openapiadapter.ParseForMetadata(ctx, sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{{Path: "openapi.json", Bytes: bytes, SHA256: hex.EncodeToString(sum[:])}}})
}
func optional(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func objectPointer(object map[string]any, key string) *map[string]any {
	value, ok := object[key].(map[string]any)
	if !ok {
		return nil
	}
	return &value
}
func legacyArtifactPaths(work string) map[string]string {
	artifacts := filepath.Join(work, "artifacts")
	return map[string]string{"markdown": filepath.Join(artifacts, "summary.md"), "openapi_map": filepath.Join(artifacts, "openapi-map.json"), "source_diagnostics": filepath.Join(artifacts, "source-diagnostics.json"), "source_registry": filepath.Join(artifacts, "source-registry.json"), "summary": filepath.Join(artifacts, "summary.json")}
}
