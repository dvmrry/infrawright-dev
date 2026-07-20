package main

// commands_authoring_probe.go composes the accepted provider-probe kernel into
// the A6 CLI contract. It owns no evidence construction: providerprobe returns
// sealed detached bytes and artifactpublish owns complete-set replacement.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/artifactpublish"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/providerprobe"
)

var providerProbeLegacyVocabulary = artifactpublish.Vocabulary{Required: []string{
	"source-registry.json",
	"source-diagnostics.json",
	"openapi-map.json",
	"summary.json",
	"summary.md",
}}

var providerProbeQualifiedVocabulary = artifactpublish.Vocabulary{
	Required: []string{
		"source-registry.json",
		"source-diagnostics.json",
		"summary.json",
		"summary.md",
		"input-provenance.json",
		"openapi-diagnostics.json",
	},
	Optional: []string{"openapi-map.json"},
}

// authoringProbeResult keeps the providerprobe sealed result at the CLI
// boundary. Tests may inject this narrow value, but production builds it only
// from providerprobe.Result's defensive-copy accessors.
type authoringProbeResult struct {
	artifacts     []providerprobe.Artifact
	markdownCopy  []byte
	mode          providerprobe.Mode
	workDirectory string
}

type authoringProbeDependencies struct {
	core                 authoringCoreDependencies
	environment          func() map[string]string
	inspectMode          func(string) (providerprobe.Mode, error)
	prepareWorkDirectory func(string) error
	publish              func(context.Context, artifactpublish.Options) error
	run                  func(context.Context, providerprobe.RunOptions) (authoringProbeResult, error)
}

func defaultAuthoringProbeDependencies() authoringProbeDependencies {
	return authoringProbeDependencies{
		core:                 defaultAuthoringCoreDependencies(),
		environment:          environMap,
		inspectMode:          providerprobe.InspectRecipeMode,
		prepareWorkDirectory: prepareProviderProbeWorkDirectory,
		publish:              artifactpublish.Publish,
		run: func(ctx context.Context, options providerprobe.RunOptions) (authoringProbeResult, error) {
			result, err := providerprobe.Run(ctx, options)
			if err != nil {
				return authoringProbeResult{}, err
			}
			markdownCopy, err := result.MarkdownCopy()
			if err != nil {
				return authoringProbeResult{}, err
			}
			return authoringProbeResult{
				artifacts:     result.Artifacts(),
				markdownCopy:  markdownCopy,
				mode:          result.Mode(),
				workDirectory: result.WorkDirectory(),
			}, nil
		},
	}
}

// providerProbeCommand ports providerProbeCommand from
// node-src/authoring/cli.ts. The frozen v1 contract retains the legacy
// work-directory destination. Qualified v2 requires an explicit work root so
// the caller grants the complete-set publisher ownership of its artifacts
// child and its sibling transaction names.
func providerProbeCommand(arguments []string) (int, error) {
	return providerProbeCommandWithDependencies(arguments, defaultAuthoringProbeDependencies())
}

func providerProbeCommandWithDependencies(arguments []string, dependencies authoringProbeDependencies) (int, error) {
	parsed, err := authoringParseArguments(
		arguments,
		[]string{"--markdown", "--out", "--work-dir"},
		nil,
		[]string{"--debug-traceback"},
	)
	if err != nil {
		return 0, err
	}
	if len(parsed.Positionals) != 1 {
		return 0, usageError("provider-probe requires one recipe JSON path")
	}
	debugRequested := parsed.Flags.Has("--debug-traceback")

	environment := dependencies.environment()
	mode, modeErr := dependencies.inspectMode(parsed.Positionals[0])
	if modeErr != nil {
		return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, modeErr)
	}
	workDirectory := authoringLastOption(parsed, "--work-dir")
	if mode == providerprobe.QualifiedV2 && workDirectory == nil {
		return 0, usageError("provider-probe source-first mode requires --work-dir")
	}
	runWorkDirectory := ""
	if workDirectory != nil {
		absolute, absoluteErr := filepath.Abs(*workDirectory)
		if absoluteErr != nil {
			return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, absoluteErr)
		}
		runWorkDirectory = absolute
		if prepareErr := dependencies.prepareWorkDirectory(runWorkDirectory); prepareErr != nil {
			return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, prepareErr)
		}
	}
	result, runErr := dependencies.run(context.Background(), providerprobe.RunOptions{
		RecipePath:    parsed.Positionals[0],
		WorkDirectory: runWorkDirectory,
		Environment:   cloneCommandEnvironment(environment),
		ExpectedMode:  mode,
	})
	if runErr != nil {
		return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, runErr)
	}

	artifactsDirectory, destinationErr := providerProbeArtifactsDirectory(result, workDirectory)
	if destinationErr != nil {
		return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, destinationErr)
	}
	options, optionsErr := providerProbePublishOptions(result, artifactsDirectory)
	if optionsErr != nil {
		return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, optionsErr)
	}
	if publishErr := dependencies.publish(context.Background(), options); publishErr != nil {
		return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, publishErr)
	}

	if output := authoringLastOption(parsed, "--out"); output != nil {
		if copyErr := authoringWritePrepared(dependencies.core, *output, providerProbeArtifactBytes(result.artifacts, "summary.json")); copyErr != nil {
			return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, copyErr)
		}
	}
	if markdown := authoringLastOption(parsed, "--markdown"); markdown != nil {
		if result.markdownCopy == nil {
			return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, fmt.Errorf("provider probe result has no Markdown copy"))
		}
		if copyErr := authoringWritePrepared(dependencies.core, *markdown, result.markdownCopy); copyErr != nil {
			return providerProbeFailure(dependencies.core.stderr, environment, debugRequested, copyErr)
		}
	}
	if _, writeErr := fmt.Fprintf(dependencies.core.stdout, "wrote %s\n", filepath.Join(artifactsDirectory, "summary.json")); writeErr != nil {
		return 0, writeErr
	}
	_, writeErr := fmt.Fprintf(dependencies.core.stdout, "wrote %s\n", filepath.Join(artifactsDirectory, "summary.md"))
	return 0, writeErr
}

func prepareProviderProbeWorkDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create provider-probe work directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect provider-probe work directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("provider-probe work directory must be a private non-symlink directory: %s", path)
	}
	return nil
}

func providerProbeArtifactsDirectory(result authoringProbeResult, requestedWorkDirectory *string) (string, error) {
	var root string
	switch result.mode {
	case providerprobe.LegacyV1:
		if result.workDirectory == "" {
			return "", fmt.Errorf("legacy provider probe returned no work directory")
		}
		root = result.workDirectory
	case providerprobe.QualifiedV2:
		if requestedWorkDirectory == nil || *requestedWorkDirectory == "" {
			return "", fmt.Errorf("provider-probe source-first mode requires --work-dir")
		}
		absolute, err := filepath.Abs(*requestedWorkDirectory)
		if err != nil {
			return "", fmt.Errorf("resolve provider-probe work directory: %w", err)
		}
		root = absolute
	default:
		return "", fmt.Errorf("unsupported provider-probe mode %q", result.mode)
	}
	return filepath.Join(root, "artifacts"), nil
}

func providerProbePublishOptions(result authoringProbeResult, destination string) (artifactpublish.Options, error) {
	vocabulary := providerProbeLegacyVocabulary
	if result.mode == providerprobe.QualifiedV2 {
		vocabulary = providerProbeQualifiedVocabulary
	}
	artifacts := make([]artifactpublish.Artifact, len(result.artifacts))
	for index, artifact := range result.artifacts {
		artifacts[index] = artifactpublish.Artifact{Name: artifact.Name, Bytes: append([]byte(nil), artifact.Bytes...)}
	}
	if _, found := providerProbeArtifact(result.artifacts, "summary.json"); !found {
		return artifactpublish.Options{}, fmt.Errorf("provider probe result is missing summary.json")
	}
	if _, found := providerProbeArtifact(result.artifacts, "summary.md"); !found {
		return artifactpublish.Options{}, fmt.Errorf("provider probe result is missing summary.md")
	}
	return artifactpublish.Options{Destination: destination, Vocabulary: vocabulary, Artifacts: artifacts}, nil
}

func providerProbeArtifactBytes(artifacts []providerprobe.Artifact, name string) []byte {
	bytes, _ := providerProbeArtifact(artifacts, name)
	return bytes
}

func providerProbeArtifact(artifacts []providerprobe.Artifact, name string) ([]byte, bool) {
	for _, artifact := range artifacts {
		if artifact.Name == name {
			return append([]byte(nil), artifact.Bytes...), true
		}
	}
	return nil, false
}

func providerProbeFailure(stderr io.Writer, environment map[string]string, debugRequested bool, err error) (int, error) {
	if debugRequested || providerProbeTruthy(environment["INFRAWRIGHT_DEBUG_TRACEBACK"]) {
		// Node's Error.stack is platform/runtime-specific. Preserve the observable
		// ordering and diagnostic capability without claiming byte-identical stack
		// frames across runtimes.
		_, _ = fmt.Fprintf(stderr, "%v\n%s", err, debug.Stack())
	}
	_, writeErr := fmt.Fprintf(stderr, "error: %v\n", err)
	if writeErr != nil {
		return 0, writeErr
	}
	return 2, nil
}

func providerProbeTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
