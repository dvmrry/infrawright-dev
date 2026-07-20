package main

// commands_authoring_core.go composes the accepted reconciliation, generic
// OpenAPI-map, and Transform/Adopt-parity kernels into their frozen Node-v1
// authoring CLI contracts. The shared Cobra tree owns central routing; this
// file owns only the authoring command adapters.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapimap"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/providerprobe"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/reconcile"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/transformadoptparity"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/spf13/cobra"
)

// authoringCoreDependencies contains the process and filesystem boundary for
// the core A6 commands. Domain results remain sealed in their owning packages;
// this command layer only renders, writes, and maps their typed outcomes.
type authoringCoreDependencies struct {
	environment  func(string) string
	loadPackRoot func(metadata.LoadPackRootOptions) (metadata.LoadedPackRoot, error)
	packageRoot  func() (string, error)
	readFile     func(string) ([]byte, error)
	stdout       io.Writer
	stderr       io.Writer
	writeFile    func(string, []byte, os.FileMode) error
	mkdirAll     func(string, os.FileMode) error
}

func defaultAuthoringCoreDependencies() authoringCoreDependencies {
	return authoringCoreDependencies{
		environment:  os.Getenv,
		loadPackRoot: metadata.LoadPackRoot,
		packageRoot:  packageRoot,
		readFile:     os.ReadFile,
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		writeFile:    os.WriteFile,
		mkdirAll:     os.MkdirAll,
	}
}

func authoringCobraSpec(
	use string,
	short string,
	values []string,
	repeatable []string,
	flags []string,
	run func(commandInput) (int, error),
) typedCobraCommandSpec {
	repeats := make(map[string]bool, len(repeatable))
	for _, name := range repeatable {
		repeats[name] = true
	}
	rejectDuplicates := make([]string, 0, len(values))
	for _, name := range values {
		if !repeats[name] {
			rejectDuplicates = append(rejectDuplicates, name)
		}
	}
	return typedCobraCommandSpec{
		use: use, short: short, valueFlags: values, boolFlags: flags,
		rejectDuplicates: rejectDuplicates, run: run,
	}
}

func authoringLastOption(parsed commandInput, name string) *string {
	value, found := lastCommandOption(parsed, name)
	if !found {
		return nil
	}
	return &value
}

func authoringRequiredOption(parsed commandInput, name string) (string, error) {
	value, found := lastCommandOption(parsed, name)
	if !found {
		return "", usageError(name + " is required")
	}
	return value, nil
}

// authoringReadJSON preserves every JSON number lexeme, as Node's
// parseDataJsonLosslessly does for authoring inputs.
func authoringReadJSON(dependencies authoringCoreDependencies, filename string) (any, error) {
	bytes, err := dependencies.readFile(filename)
	if err != nil {
		return nil, err
	}
	return canonjson.ParseDataJSONLosslessly(string(bytes))
}

func authoringReadObject(dependencies authoringCoreDependencies, filename string) (map[string]any, error) {
	value, err := authoringReadJSON(dependencies, filename)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must contain a JSON object", filename)
	}
	return object, nil
}

// authoringRenderJSON is the exact authoring JSON dialect. The two ratio
// fields intentionally retain a trailing .0 for integral float values, just
// as authoring/json.ts restores LosslessNumber before rendering.
func authoringRenderJSON(value any) ([]byte, error) {
	normalized, err := authoringNumbers(value, "")
	if err != nil {
		return nil, err
	}
	rendered, err := canonjson.Render(normalized)
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}

func authoringNumbers(value any, key string) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			normalized, err := authoringNumbers(child, childKey)
			if err != nil {
				return nil, err
			}
			out[childKey] = normalized
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			normalized, err := authoringNumbers(child, "")
			if err != nil {
				return nil, err
			}
			out[i] = normalized
		}
		return out, nil
	case []string:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = child
		}
		return out, nil
	case float64:
		if key == "coverage_ratio" || key == "operation_id_coverage_ratio" {
			if typed == float64(int64(typed)) {
				return json.Number(fmt.Sprintf("%.1f", typed)), nil
			}
			return json.Number(fmt.Sprint(typed)), nil
		}
	case int:
		return float64(typed), nil
	case int8:
		return float64(typed), nil
	case int16:
		return float64(typed), nil
	case int32:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case uint:
		if uint64(typed) > uint64(1<<63-1) {
			return nil, fmt.Errorf("authoring JSON unsigned integer exceeds signed binary64 input boundary")
		}
		return float64(typed), nil
	case uint8:
		return float64(typed), nil
	case uint16:
		return float64(typed), nil
	case uint32:
		return float64(typed), nil
	case uint64:
		if typed > uint64(1<<63-1) {
			return nil, fmt.Errorf("authoring JSON unsigned integer exceeds signed binary64 input boundary")
		}
		return float64(typed), nil
	}
	return value, nil
}

// authoringWriteJSON always renders before it prepares a parent directory or
// opens the destination. Thus malformed/unrenderable results cannot truncate
// an existing output file. It ports writeJson from authoring/cli.ts.
func authoringWriteJSON(dependencies authoringCoreDependencies, value any, destination *string) error {
	rendered, err := authoringRenderJSON(value)
	if err != nil {
		return err
	}
	if destination == nil {
		_, err = dependencies.stdout.Write(rendered)
		return err
	}
	return authoringWritePrepared(dependencies, *destination, rendered)
}

// authoringWriteText ports writeText from authoring/cli.ts. Callers hand it
// fully rendered text, so parent preparation happens before the destination is
// opened and cannot corrupt a previous file on preparation failure.
func authoringWriteText(dependencies authoringCoreDependencies, value, destination string) error {
	return authoringWritePrepared(dependencies, destination, []byte(value))
}

func authoringWritePrepared(dependencies authoringCoreDependencies, destination string, rendered []byte) error {
	abs, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := dependencies.mkdirAll(filepath.Dir(abs), 0o777); err != nil {
		return err
	}
	return dependencies.writeFile(destination, rendered, 0o666)
}

func authoringOpenAPIDocument(ctx context.Context, dependencies authoringCoreDependencies, filename string) (openapiadapter.Document, error) {
	value, err := authoringReadObject(dependencies, filename)
	if err != nil {
		return openapiadapter.Document{}, err
	}
	if err := providerprobe.ValidateLegacyOpenAPI(value); err != nil {
		return openapiadapter.Document{}, err
	}
	bytes, err := authoringRenderJSON(value)
	if err != nil {
		return openapiadapter.Document{}, err
	}
	sum := sha256.Sum256(bytes)
	return openapiadapter.ParseForMetadata(ctx, sourcebind.OpenAPIStatus{Available: true, Files: []sourcebind.CapturedFile{{
		Path:   "openapi.json",
		Bytes:  bytes,
		SHA256: hex.EncodeToString(sum[:]),
	}}})
}

func authoringOperationReferences(values []string) []openapiadapter.OperationReference {
	result := make([]openapiadapter.OperationReference, len(values))
	for i, value := range values {
		result[i] = openapiadapter.OperationReference(value)
	}
	return result
}

func authoringPacksRoot(dependencies authoringCoreDependencies) (string, error) {
	if root := dependencies.environment("INFRAWRIGHT_PACKS"); root != "" {
		return root, nil
	}
	repositoryRoot, err := dependencies.packageRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(repositoryRoot, "packs"), nil
}

// reconcileCommand composes the retained Node-v1 reconcile contract.
func reconcileCommand(arguments []string) (int, error) {
	return reconcileCommandWithDependencies(arguments, defaultAuthoringCoreDependencies())
}

func reconcileCommandWithDependencies(arguments []string, dependencies authoringCoreDependencies) (int, error) {
	return executeStandaloneCobra(newReconcileCobraCommand(dependencies), arguments)
}

func newReconcileCobraCommand(dependencies authoringCoreDependencies) *cobra.Command {
	spec := authoringCobraSpec("reconcile <resource-type>", "Compare API JSON with a Terraform schema",
		[]string{"--api", "--api-options", "--openapi", "--openapi-read", "--openapi-write", "--out", "--override", "--provider-source", "--schema"},
		[]string{"--api", "--api-options", "--openapi-read", "--openapi-write"},
		[]string{"--fail-on-unknown"},
		nil,
	)
	spec.run = func(parsed commandInput) (int, error) {
		return reconcileCommandInput(parsed, dependencies)
	}
	return newTypedCobraCommand(spec)
}

func reconcileCommandInput(parsed commandInput, dependencies authoringCoreDependencies) (int, error) {
	if len(parsed.Positionals) != 1 {
		return 0, usageError("reconcile requires one resource type")
	}
	resourceType := parsed.Positionals[0]
	apiPaths := parsed.Options["--api"]
	if len(apiPaths) == 0 {
		return 0, usageError("--api is required")
	}
	providerSource := authoringLastOption(parsed, "--provider-source")
	var resourceSchema map[string]any
	if schemaPath := authoringLastOption(parsed, "--schema"); schemaPath != nil {
		schemaData, readErr := authoringReadObject(dependencies, *schemaPath)
		if readErr != nil {
			return 0, readErr
		}
		resourceSchema, readErr = reconcile.ResourceSchemaFromData(schemaData, resourceType, providerSource)
		if readErr != nil {
			return 0, readErr
		}
	} else {
		packsRoot, rootErr := authoringPacksRoot(dependencies)
		if rootErr != nil {
			return 0, rootErr
		}
		root, loadErr := dependencies.loadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot})
		if loadErr != nil {
			return 0, loadErr
		}
		resourceSchema, loadErr = root.LoadResourceSchema(resourceType)
		if loadErr != nil {
			return 0, loadErr
		}
	}
	items := make([]any, 0)
	for _, apiPath := range apiPaths {
		value, readErr := authoringReadJSON(dependencies, apiPath)
		if readErr != nil {
			return 0, readErr
		}
		apiItems, itemsErr := reconcile.APIItemsFrom(value, apiPath)
		if itemsErr != nil {
			return 0, itemsErr
		}
		for _, item := range apiItems {
			items = append(items, item)
		}
	}
	optionDocuments := make([]any, 0, len(parsed.Options["--api-options"]))
	for _, filename := range parsed.Options["--api-options"] {
		value, readErr := authoringReadJSON(dependencies, filename)
		if readErr != nil {
			return 0, readErr
		}
		optionDocuments = append(optionDocuments, value)
	}
	metadataParts := []reconcile.APIMetadata{}
	if openAPIPath := authoringLastOption(parsed, "--openapi"); openAPIPath != nil {
		document, openAPIErr := authoringOpenAPIDocument(context.Background(), dependencies, *openAPIPath)
		if openAPIErr != nil {
			return 0, openAPIErr
		}
		metadataPart, metadataErr := document.Metadata(context.Background(), openapiadapter.MetadataOptions{
			ReadOperations:  authoringOperationReferences(parsed.Options["--openapi-read"]),
			WriteOperations: authoringOperationReferences(parsed.Options["--openapi-write"]),
		})
		if metadataErr != nil {
			return 0, metadataErr
		}
		metadataParts = append(metadataParts, metadataPart)
	}
	apiMetadata, metadataErr := reconcile.MergeAPIMetadata(optionDocuments, metadataParts...)
	if metadataErr != nil {
		return 0, metadataErr
	}
	var override map[string]any
	if overridePath := authoringLastOption(parsed, "--override"); overridePath != nil {
		value, readErr := authoringReadJSON(dependencies, *overridePath)
		if readErr != nil {
			return 0, readErr
		}
		override, readErr = metadata.ValidateOverride(value, *overridePath)
		if readErr != nil {
			return 0, readErr
		}
	}
	report, reconcileErr := reconcile.ReconcileItems(reconcile.ReconcileOptions{
		ResourceType:   resourceType,
		Items:          items,
		ResourceSchema: resourceSchema,
		Override:       override,
		APIMetadata:    apiMetadata,
	})
	if reconcileErr != nil {
		return 0, reconcileErr
	}
	if writeErr := authoringWriteJSON(dependencies, report.AsMap(), authoringLastOption(parsed, "--out")); writeErr != nil {
		return 0, writeErr
	}
	if parsed.Flags.Has("--fail-on-unknown") && report.HasUnknowns() {
		_, writeErr := fmt.Fprintf(dependencies.stderr, "error: %s has unknown API surface; review report\n", resourceType)
		return 4, writeErr
	}
	return 0, nil
}

// openAPIMapCommand composes the frozen generic OpenAPI diagnostic view. It
// never treats the generic map as source-first readiness evidence.
func openAPIMapCommand(arguments []string) (int, error) {
	return openAPIMapCommandWithDependencies(arguments, defaultAuthoringCoreDependencies())
}

func openAPIMapCommandWithDependencies(arguments []string, dependencies authoringCoreDependencies) (int, error) {
	return executeStandaloneCobra(newOpenAPIMapCobraCommand(dependencies), arguments)
}

func newOpenAPIMapCobraCommand(dependencies authoringCoreDependencies) *cobra.Command {
	spec := authoringCobraSpec("openapi-map", "Map provider resources to OpenAPI operations",
		[]string{"--api-prefix", "--openapi", "--out", "--provider-source", "--registry", "--resource-prefix", "--schema"}, nil, nil, nil)
	spec.run = func(parsed commandInput) (int, error) { return openAPIMapCommandInput(parsed, dependencies) }
	return newTypedCobraCommand(spec)
}

func openAPIMapCommandInput(parsed commandInput, dependencies authoringCoreDependencies) (int, error) {
	if len(parsed.Positionals) != 0 {
		return 0, usageError("openapi-map does not accept positional arguments")
	}
	openAPIPath, err := authoringRequiredOption(parsed, "--openapi")
	if err != nil {
		return 0, err
	}
	document, err := authoringOpenAPIDocument(context.Background(), dependencies, openAPIPath)
	if err != nil {
		return 0, err
	}
	var registryData *map[string]any
	if registryPath := authoringLastOption(parsed, "--registry"); registryPath != nil {
		registry, readErr := authoringReadObject(dependencies, *registryPath)
		if readErr != nil {
			return 0, readErr
		}
		registryData = &registry
	}
	schemaPath, err := authoringRequiredOption(parsed, "--schema")
	if err != nil {
		return 0, err
	}
	schemaData, err := authoringReadObject(dependencies, schemaPath)
	if err != nil {
		return 0, err
	}
	resourcePrefix := ""
	if value := authoringLastOption(parsed, "--resource-prefix"); value != nil {
		resourcePrefix = *value
	}
	report, err := openapimap.Build(context.Background(), openapimap.Options{
		SchemaData:     schemaData,
		Document:       document,
		ProviderSource: authoringLastOption(parsed, "--provider-source"),
		ResourcePrefix: resourcePrefix,
		APIPrefix:      authoringLastOption(parsed, "--api-prefix"),
		RegistryData:   registryData,
	})
	if err != nil {
		return 0, err
	}
	rendered, err := report.Render()
	if err != nil {
		return 0, err
	}
	if destination := authoringLastOption(parsed, "--out"); destination != nil {
		return 0, authoringWritePrepared(dependencies, *destination, rendered)
	}
	_, err = dependencies.stdout.Write(rendered)
	return 0, err
}

// transformAdoptParityCommand ports the contained operational-error contract:
// usage errors remain top-level status 2, while fixture/domain failures emit a
// concise stderr line and return status 2 after parsing succeeds.
func transformAdoptParityCommand(arguments []string) (int, error) {
	return transformAdoptParityCommandWithDependencies(arguments, defaultAuthoringCoreDependencies())
}

func transformAdoptParityCommandWithDependencies(arguments []string, dependencies authoringCoreDependencies) (status int, err error) {
	return executeStandaloneCobra(newTransformAdoptParityCobraCommand(dependencies), arguments)
}

func newTransformAdoptParityCobraCommand(dependencies authoringCoreDependencies) *cobra.Command {
	spec := authoringCobraSpec(
		"transform-adopt-parity <fixture.json> [fixture.json...]",
		"Compare Transform and Adopt fixture behavior", nil, nil, nil, nil,
	)
	spec.run = func(parsed commandInput) (int, error) {
		return transformAdoptParityCommandInput(parsed, dependencies)
	}
	return newTypedCobraCommand(spec)
}

func transformAdoptParityCommandInput(parsed commandInput, dependencies authoringCoreDependencies) (status int, err error) {
	if len(parsed.Positionals) == 0 {
		return 0, usageError("transform-adopt-parity requires at least one fixture path")
	}
	defer func() {
		if err == nil {
			return
		}
		domainErr := err
		_, writeErr := fmt.Fprintf(dependencies.stderr, "error: %s\n", domainErr)
		if writeErr != nil {
			err = writeErr
			return
		}
		status = 2
		err = nil
	}()
	packsRoot, err := authoringPacksRoot(dependencies)
	if err != nil {
		return 0, err
	}
	repositoryRoot, err := dependencies.packageRoot()
	if err != nil {
		return 0, err
	}
	root, err := dependencies.loadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot})
	if err != nil {
		return 0, err
	}
	parityContext := transformadoptparity.Context{RepositoryRoot: repositoryRoot, Root: root}
	fixtures := make([]transformadoptparity.Fixture, len(parsed.Positionals))
	for i, source := range parsed.Positionals {
		fixtures[i], err = transformadoptparity.LoadFixture(source, parityContext)
		if err != nil {
			return 0, err
		}
	}
	report, err := transformadoptparity.Build(fixtures, parityContext)
	if err != nil {
		return 0, err
	}
	rendered, err := transformadoptparity.Render(report)
	if err != nil {
		return 0, err
	}
	if _, err = io.WriteString(dependencies.stdout, rendered); err != nil {
		return 0, err
	}
	result, err := transformadoptparity.ResultClassification(report)
	if err != nil {
		return 0, err
	}
	if result == transformadoptparity.ResultEqual || result == transformadoptparity.ResultClassifiedDifferences {
		return 0, nil
	}
	return 1, nil
}
