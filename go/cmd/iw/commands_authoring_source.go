package main

// commands_authoring_source.go composes the accepted A1-A3 source-analysis
// capabilities into the source-first v2 bundle modes. It owns no evidence
// inference: qualified and unverified inputs stay separated through
// sourcebind, sourceanalysis, and sourceoperation.

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/artifactpublish"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceanalysis"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceoperation"
	"github.com/spf13/cobra"
)

var sourceBundleVocabulary = artifactpublish.Vocabulary{Required: []string{
	"source-registry.json",
	"source-diagnostics.json",
	"summary.json",
	"summary.md",
	"input-provenance.json",
	"openapi-diagnostics.json",
}}

type authoringSourceDependencies struct {
	core              authoringCoreDependencies
	absolutePath      func(string) (string, error)
	loadVerified      func(context.Context, sourcebind.LocalRoots) (sourcebind.VerifiedInputs, error)
	requireQualified  func(sourcebind.VerifiedInputs) (sourcebind.QualifiedInputs, error)
	analyzeQualified  func(context.Context, sourcebind.QualifiedInputs) (sourceanalysis.QualifiedEvidence, error)
	compileQualified  func(context.Context, sourceanalysis.QualifiedEvidence, sourcebind.QualifiedInputs) (sourceoperation.Bundle, error)
	loadUnverified    func(context.Context, sourcebind.UnverifiedRoots) (sourcebind.UnverifiedInputs, error)
	analyzeUnverified func(context.Context, sourcebind.UnverifiedInputs) (sourceanalysis.UnverifiedEvidence, error)
	compileUnverified func(context.Context, sourceanalysis.UnverifiedEvidence, sourcebind.UnverifiedInputs) (sourceoperation.Bundle, error)
	bundleStatus      func(sourceoperation.Bundle) (sourceoperation.BundleStatus, error)
	publish           func(context.Context, artifactpublish.Options) error
}

func defaultAuthoringSourceDependencies() authoringSourceDependencies {
	return authoringSourceDependencies{
		core:              defaultAuthoringCoreDependencies(),
		absolutePath:      filepath.Abs,
		loadVerified:      sourcebind.LoadVerified,
		requireQualified:  sourcebind.RequireQualification,
		analyzeQualified:  sourceanalysis.Analyze,
		compileQualified:  sourceoperation.CompileQualified,
		loadUnverified:    sourcebind.LoadUnverified,
		analyzeUnverified: sourceanalysis.AnalyzeUnverified,
		compileUnverified: sourceoperation.CompileUnverified,
		bundleStatus:      func(bundle sourceoperation.Bundle) (sourceoperation.BundleStatus, error) { return bundle.Status() },
		publish:           artifactpublish.Publish,
	}
}

type sourceCommandMode int

const (
	sourceModeLegacy sourceCommandMode = iota
	sourceModeQualified
	sourceModeUnverified
)

func sourceOperationMapCommandWithDependencies(arguments []string, dependencies authoringSourceDependencies) (int, error) {
	return executeStandaloneCobra(newSourceOperationMapCobraCommand(dependencies), arguments)
}

func newSourceOperationMapCobraCommand(dependencies authoringSourceDependencies) *cobra.Command {
	spec := authoringCobraSpec("source-operation-map", "Derive source-backed provider operation evidence",
		[]string{
			"--artifact-dir", "--openapi", "--provider-file", "--provider-module",
			"--resources", "--schema", "--sdk-file", "--sdk-root",
			"--source-manifest", "--source-root",
		},
		[]string{"--provider-file", "--sdk-file", "--sdk-root"},
		[]string{"--allow-unverified-source"},
		nil,
	)
	spec.run = func(parsed commandInput) (int, error) {
		return sourceOperationMapCommandInput(parsed, dependencies)
	}
	return newTypedCobraCommand(spec)
}

func sourceOperationMapCommandInput(parsed commandInput, dependencies authoringSourceDependencies) (int, error) {
	if len(parsed.Positionals) != 0 {
		return 0, usageError("source-operation-map does not accept positional arguments")
	}
	mode, err := sourceMode(parsed)
	if err != nil {
		return 0, err
	}
	if mode == sourceModeLegacy {
		// The legacy text-scanner mapping lane was retired with the LegacyV1
		// provider-probe proof of concept.
		return 0, usageError("source-operation-map requires --source-manifest (qualified) or --allow-unverified-source; the legacy text-scanner mode is retired")
	}
	if err := validateV2SourceOptions(parsed, mode, "source-operation-map", "--artifact-dir"); err != nil {
		return 0, err
	}
	destination, err := authoringRequiredOption(parsed, "--artifact-dir")
	if err != nil {
		return 0, err
	}
	bundle, err := compileSourceBundle(context.Background(), parsed, mode, dependencies)
	if err != nil {
		return 0, err
	}
	return completeSourceBundle(context.Background(), destination, bundle, parsed, mode, false, dependencies)
}

func sourceEvidenceEvalCommandWithDependencies(arguments []string, dependencies authoringSourceDependencies) (int, error) {
	return executeStandaloneCobra(newSourceEvidenceEvalCobraCommand(dependencies), arguments)
}

func newSourceEvidenceEvalCobraCommand(dependencies authoringSourceDependencies) *cobra.Command {
	spec := authoringCobraSpec("source-evidence-eval", "Evaluate source-backed provider evidence",
		[]string{
			"--openapi", "--out-dir", "--provider-file", "--provider-module",
			"--resources", "--schema", "--sdk-file",
			"--sdk-root", "--source-manifest", "--source-root",
		},
		[]string{"--provider-file", "--sdk-file", "--sdk-root"},
		[]string{"--allow-unverified-source", "--fail-on-regression"},
		nil,
	)
	spec.run = func(parsed commandInput) (int, error) {
		return sourceEvidenceEvalCommandInput(parsed, dependencies)
	}
	return newTypedCobraCommand(spec)
}

func sourceEvidenceEvalCommandInput(parsed commandInput, dependencies authoringSourceDependencies) (int, error) {
	if len(parsed.Positionals) != 0 {
		return 0, usageError("source-evidence-eval does not accept positional arguments")
	}
	mode, err := sourceMode(parsed)
	if err != nil {
		return 0, err
	}
	if mode == sourceModeLegacy {
		// The legacy facts A/B evaluation lane is retired: the in-engine
		// source-first analysis (sourceanalysis) subsumed the external AST
		// facts workflow.
		return 0, usageError("source-evidence-eval requires --source-manifest (qualified) or --allow-unverified-source; the legacy --source-facts evaluation mode is retired")
	}
	if err := validateV2SourceOptions(parsed, mode, "source-evidence-eval", "--out-dir"); err != nil {
		return 0, err
	}
	destination, err := authoringRequiredOption(parsed, "--out-dir")
	if err != nil {
		return 0, err
	}
	bundle, err := compileSourceBundle(context.Background(), parsed, mode, dependencies)
	if err != nil {
		return 0, err
	}
	return completeSourceBundle(
		context.Background(), destination, bundle, parsed, mode,
		parsed.Flags.Has("--fail-on-regression"), dependencies,
	)
}

func sourceMode(parsed commandInput) (sourceCommandMode, error) {
	qualified := authoringLastOption(parsed, "--source-manifest") != nil
	unverified := parsed.Flags.Has("--allow-unverified-source")
	if qualified && unverified {
		return sourceModeLegacy, usageError("--source-manifest and --allow-unverified-source are mutually exclusive")
	}
	if qualified {
		return sourceModeQualified, nil
	}
	if unverified {
		return sourceModeUnverified, nil
	}
	return sourceModeLegacy, nil
}

func validateV2SourceOptions(parsed commandInput, mode sourceCommandMode, command, destination string) error {
	if authoringLastOption(parsed, destination) == nil {
		return usageError(destination + " is required")
	}
	if mode == sourceModeQualified {
		return rejectOptions(parsed, command+" qualified mode", "--provider-file", "--provider-module", "--resources", "--sdk-file")
	}
	if authoringLastOption(parsed, "--openapi") != nil {
		return usageError("--openapi is not accepted with --allow-unverified-source; unverified OpenAPI state is sealed absent")
	}
	return nil
}

func compileSourceBundle(ctx context.Context, parsed commandInput, mode sourceCommandMode, dependencies authoringSourceDependencies) (sourceoperation.Bundle, error) {
	if mode == sourceModeQualified {
		roots, err := qualifiedSourceRoots(parsed, dependencies)
		if err != nil {
			return sourceoperation.Bundle{}, err
		}
		verified, err := dependencies.loadVerified(ctx, roots)
		if err != nil {
			return sourceoperation.Bundle{}, err
		}
		qualified, err := dependencies.requireQualified(verified)
		if err != nil {
			return sourceoperation.Bundle{}, err
		}
		evidence, err := dependencies.analyzeQualified(ctx, qualified)
		if err != nil {
			return sourceoperation.Bundle{}, err
		}
		return dependencies.compileQualified(ctx, evidence, qualified)
	}
	roots, err := unverifiedSourceRoots(parsed, dependencies)
	if err != nil {
		return sourceoperation.Bundle{}, err
	}
	inputs, err := dependencies.loadUnverified(ctx, roots)
	if err != nil {
		return sourceoperation.Bundle{}, err
	}
	evidence, err := dependencies.analyzeUnverified(ctx, inputs)
	if err != nil {
		return sourceoperation.Bundle{}, err
	}
	return dependencies.compileUnverified(ctx, evidence, inputs)
}

func qualifiedSourceRoots(parsed commandInput, dependencies authoringSourceDependencies) (sourcebind.LocalRoots, error) {
	manifestPath, err := requiredAbsoluteOption(parsed, "--source-manifest", dependencies)
	if err != nil {
		return sourcebind.LocalRoots{}, err
	}
	manifestBytes, err := dependencies.core.readFile(manifestPath)
	if err != nil {
		return sourcebind.LocalRoots{}, err
	}
	manifest, err := contracts.DecodeSourceProvenance(manifestBytes)
	if err != nil {
		return sourcebind.LocalRoots{}, err
	}
	providerRoot, err := requiredAbsoluteOption(parsed, "--source-root", dependencies)
	if err != nil {
		return sourcebind.LocalRoots{}, err
	}
	schemaFile, err := requiredAbsoluteOption(parsed, "--schema", dependencies)
	if err != nil {
		return sourcebind.LocalRoots{}, err
	}
	schemaRoot, err := rootForSelectedFile(schemaFile, manifest.TerraformSchema.Path, "--schema")
	if err != nil {
		return sourcebind.LocalRoots{}, err
	}
	sdkRoots, err := qualifiedSDKRoots(parsed.Options["--sdk-root"], dependencies)
	if err != nil {
		return sourcebind.LocalRoots{}, err
	}
	result := sourcebind.LocalRoots{
		ManifestPath: manifestPath,
		ProviderRoot: providerRoot,
		SchemaRoot:   schemaRoot,
		SDKRoots:     sdkRoots,
	}
	if openAPIFile := authoringLastOption(parsed, "--openapi"); openAPIFile != nil {
		if manifest.OpenAPI == nil {
			return sourcebind.LocalRoots{}, usageError("--openapi requires an OpenAPI binding in --source-manifest")
		}
		absolute, absErr := dependencies.absolutePath(*openAPIFile)
		if absErr != nil {
			return sourcebind.LocalRoots{}, absErr
		}
		result.OpenAPIRoot, err = rootForSelectedFile(absolute, manifest.OpenAPI.Document.Path, "--openapi")
		if err != nil {
			return sourcebind.LocalRoots{}, err
		}
	}
	return result, nil
}

func unverifiedSourceRoots(parsed commandInput, dependencies authoringSourceDependencies) (sourcebind.UnverifiedRoots, error) {
	providerRoot, err := requiredAbsoluteOption(parsed, "--source-root", dependencies)
	if err != nil {
		return sourcebind.UnverifiedRoots{}, err
	}
	providerModule, err := authoringRequiredOption(parsed, "--provider-module")
	if err != nil {
		return sourcebind.UnverifiedRoots{}, err
	}
	providerFiles, err := sortedUniqueRequired(parsed.Options["--provider-file"], "--provider-file")
	if err != nil {
		return sourcebind.UnverifiedRoots{}, err
	}
	if err := validateExplicitRelativeFiles(providerFiles, "--provider-file"); err != nil {
		return sourcebind.UnverifiedRoots{}, err
	}
	schemaFile, err := requiredAbsoluteOption(parsed, "--schema", dependencies)
	if err != nil {
		return sourcebind.UnverifiedRoots{}, err
	}
	resources := sourceResourceFilter(authoringLastOption(parsed, "--resources"))
	resources, err = sortedUniqueRequired(resources, "--resources")
	if err != nil {
		return sourcebind.UnverifiedRoots{}, err
	}
	sdkRoots, sdkVersions, err := unverifiedSDKRoots(parsed.Options["--sdk-root"], dependencies)
	if err != nil {
		return sourcebind.UnverifiedRoots{}, err
	}
	sdkFiles, err := unverifiedSDKFiles(parsed.Options["--sdk-file"], sdkRoots)
	if err != nil {
		return sourcebind.UnverifiedRoots{}, err
	}
	return sourcebind.UnverifiedRoots{
		ProviderRoot:       providerRoot,
		ProviderModulePath: providerModule,
		ProviderFiles:      providerFiles,
		SchemaRoot:         filepath.Dir(schemaFile),
		TerraformSchema:    filepath.Base(schemaFile),
		SDKRoots:           sdkRoots,
		SDKFiles:           sdkFiles,
		SDKVersions:        sdkVersions,
		Selection: contracts.SelectionBinding{
			ResourceTypes: resources,
			Filters:       []contracts.SelectionFilterBinding{},
		},
	}, nil
}

func qualifiedSDKRoots(values []string, dependencies authoringSourceDependencies) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for _, value := range values {
		module, root, ok := strings.Cut(value, "=")
		if !ok || module == "" || root == "" {
			return nil, usageError("--sdk-root requires MODULE=DIR in qualified mode")
		}
		if _, duplicate := result[module]; duplicate {
			return nil, usageError("--sdk-root contains duplicate module " + module)
		}
		absolute, err := dependencies.absolutePath(root)
		if err != nil {
			return nil, err
		}
		result[module] = absolute
	}
	return result, nil
}

func unverifiedSDKRoots(values []string, dependencies authoringSourceDependencies) (map[string]string, map[string]string, error) {
	roots := make(map[string]string, len(values))
	versions := make(map[string]string, len(values))
	for _, value := range values {
		identity, root, ok := strings.Cut(value, "=")
		separator := strings.LastIndex(identity, "@")
		if !ok || separator <= 0 || separator == len(identity)-1 || root == "" {
			return nil, nil, usageError("--sdk-root requires MODULE@VERSION=DIR in unverified mode")
		}
		module, version := identity[:separator], identity[separator+1:]
		if _, duplicate := roots[module]; duplicate {
			return nil, nil, usageError("--sdk-root contains duplicate module " + module)
		}
		absolute, err := dependencies.absolutePath(root)
		if err != nil {
			return nil, nil, err
		}
		roots[module], versions[module] = absolute, version
	}
	return roots, versions, nil
}

func unverifiedSDKFiles(values []string, roots map[string]string) (map[string][]string, error) {
	result := make(map[string][]string, len(roots))
	for _, value := range values {
		module, file, ok := strings.Cut(value, "=")
		if !ok || module == "" || file == "" {
			return nil, usageError("--sdk-file requires MODULE=RELATIVE")
		}
		if _, exists := roots[module]; !exists {
			return nil, usageError("--sdk-file has no matching --sdk-root for module " + module)
		}
		result[module] = append(result[module], file)
	}
	for module := range roots {
		files, err := sortedUniqueRequired(result[module], "--sdk-file for "+module)
		if err != nil {
			return nil, err
		}
		if err := validateExplicitRelativeFiles(files, "--sdk-file for "+module); err != nil {
			return nil, err
		}
		result[module] = files
	}
	return result, nil
}

func validateExplicitRelativeFiles(values []string, option string) error {
	for _, value := range values {
		if value == "." || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) ||
			strings.HasPrefix(value, "/") || strings.HasPrefix(value, "../") || path.Clean(value) != value {
			return usageError(option + " must use portable paths inside its explicit root")
		}
	}
	return nil
}

func requiredAbsoluteOption(parsed commandInput, name string, dependencies authoringSourceDependencies) (string, error) {
	value, err := authoringRequiredOption(parsed, name)
	if err != nil {
		return "", err
	}
	return dependencies.absolutePath(value)
}

func rootForSelectedFile(selected, relative, option string) (string, error) {
	if relative == "" || strings.Contains(relative, "\\") || filepath.IsAbs(relative) {
		return "", usageError(option + " manifest binding is not a portable relative path")
	}
	parts := strings.Split(relative, "/")
	root := selected
	for range parts {
		root = filepath.Dir(root)
	}
	if filepath.Clean(filepath.Join(root, filepath.FromSlash(relative))) != filepath.Clean(selected) {
		return "", usageError(option + " must name the file bound by --source-manifest")
	}
	return root, nil
}

func sortedUniqueRequired(values []string, option string) ([]string, error) {
	if len(values) == 0 {
		return nil, usageError(option + " is required")
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	for index, value := range result {
		if value == "" {
			return nil, usageError(option + " requires non-empty values")
		}
		if index > 0 && result[index-1] == value {
			return nil, usageError(option + " contains duplicate value " + value)
		}
	}
	return result, nil
}

func sourceResourceFilter(value *string) []string {
	if value == nil {
		return nil
	}
	result := []string{}
	for _, resource := range strings.Split(*value, ",") {
		if resource = strings.TrimSpace(resource); resource != "" {
			result = append(result, resource)
		}
	}
	return result
}

func rejectOptions(parsed commandInput, mode string, names ...string) error {
	for _, name := range names {
		if authoringLastOption(parsed, name) != nil {
			return usageError(name + " is not accepted in " + mode)
		}
	}
	return nil
}

func publishSourceBundle(ctx context.Context, destination string, bundle sourceoperation.Bundle, dependencies authoringSourceDependencies) error {
	artifacts := bundle.Artifacts()
	prepared := make([]artifactpublish.Artifact, len(artifacts))
	for index, artifact := range artifacts {
		prepared[index] = artifactpublish.Artifact{Name: artifact.Name, Bytes: artifact.Bytes}
	}
	return publishArtifacts(ctx, destination, sourceBundleVocabulary, prepared, dependencies)
}

func completeSourceBundle(
	ctx context.Context,
	destination string,
	bundle sourceoperation.Bundle,
	parsed commandInput,
	mode sourceCommandMode,
	failOnRegression bool,
	dependencies authoringSourceDependencies,
) (int, error) {
	if err := publishSourceBundle(ctx, destination, bundle, dependencies); err != nil {
		return 0, err
	}
	status, err := dependencies.bundleStatus(bundle)
	if err != nil {
		return 0, err
	}
	if err := emitSourceBundleWarning(parsed, mode, status, dependencies); err != nil {
		return 0, err
	}
	if failOnRegression && status.OpenAPIConflict {
		return 1, nil
	}
	return 0, nil
}

func emitSourceBundleWarning(parsed commandInput, mode sourceCommandMode, status sourceoperation.BundleStatus, dependencies authoringSourceDependencies) error {
	if mode != sourceModeQualified || authoringLastOption(parsed, "--openapi") == nil {
		return nil
	}
	if status.DocumentState != contracts.OpenAPIUnavailable && status.DocumentState != contracts.OpenAPIDegraded {
		return nil
	}
	reason := "unknown"
	if status.ReasonCode != nil {
		reason = string(*status.ReasonCode)
	}
	_, err := fmt.Fprintf(
		dependencies.core.stderr,
		"warning: OpenAPI input %s (%s); source evidence remains valid\n",
		status.DocumentState,
		reason,
	)
	return err
}

func publishArtifacts(ctx context.Context, destination string, vocabulary artifactpublish.Vocabulary, artifacts []artifactpublish.Artifact, dependencies authoringSourceDependencies) error {
	absolute, err := dependencies.absolutePath(destination)
	if err != nil {
		return err
	}
	if err := dependencies.core.mkdirAll(filepath.Dir(absolute), 0o777); err != nil {
		return err
	}
	return dependencies.publish(ctx, artifactpublish.Options{
		Destination: absolute,
		Vocabulary:  vocabulary,
		Artifacts:   artifacts,
	})
}
