package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/artifactpublish"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourceoperation"
)

func publishedSourceArtifact(t *testing.T, artifacts []artifactpublish.Artifact, name string) []byte {
	t.Helper()
	for _, artifact := range artifacts {
		if artifact.Name == name {
			return artifact.Bytes
		}
	}
	t.Fatalf("published artifacts have no %q", name)
	return nil
}

func decodedSourceArtifact(t *testing.T, artifacts []artifactpublish.Artifact, name string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(publishedSourceArtifact(t, artifacts, name), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", name, err)
	}
	return decoded
}

func TestSourceCommandsRejectModeConflictsBeforeReadsOrPublication(t *testing.T) {
	dependencies := defaultAuthoringSourceDependencies()
	dependencies.core.readFile = func(string) ([]byte, error) {
		t.Fatal("usage failure read an input")
		return nil, nil
	}
	dependencies.publish = func(context.Context, artifactpublish.Options) error {
		t.Fatal("usage failure published artifacts")
		return nil
	}

	tests := []struct {
		name    string
		command func([]string, authoringSourceDependencies) (int, error)
		args    []string
		want    string
	}{
		{
			name: "mutually exclusive trust roots", command: sourceOperationMapCommandWithDependencies,
			args: []string{"--source-manifest", "manifest.json", "--allow-unverified-source"},
			want: "--source-manifest and --allow-unverified-source are mutually exclusive",
		},
		{
			name: "qualified bundle destination required", command: sourceOperationMapCommandWithDependencies,
			args: []string{"--source-manifest", "manifest.json"}, want: "--artifact-dir is required",
		},
		{
			name: "qualified rejects legacy destination", command: sourceOperationMapCommandWithDependencies,
			args: []string{"--source-manifest", "manifest.json", "--artifact-dir", "artifacts", "--out", "registry.json"},
			want: "--out is not accepted in source-operation-map source-first mode",
		},
		{
			name: "qualified selection comes only from manifest", command: sourceOperationMapCommandWithDependencies,
			args: []string{"--source-manifest", "manifest.json", "--artifact-dir", "artifacts", "--resources", "example_a"},
			want: "--resources is not accepted in source-operation-map qualified mode",
		},
		{
			name: "unverified OpenAPI stays absent", command: sourceOperationMapCommandWithDependencies,
			args: []string{"--allow-unverified-source", "--artifact-dir", "artifacts", "--openapi", "openapi.json"},
			want: "--openapi is not accepted with --allow-unverified-source; unverified OpenAPI state is sealed absent",
		},
		{
			name: "legacy map source facts comparison requires facts", command: sourceOperationMapCommandWithDependencies,
			args: []string{
				"--openapi", "openapi.json", "--schema", "schema.json", "--source-root", "source",
				"--source-facts-compare", "comparison.json",
			},
			want: "--source-facts-compare requires --source-facts",
		},
		{
			name: "legacy map rejects repeated sdk roots", command: sourceOperationMapCommandWithDependencies,
			args: []string{"--sdk-root", "one", "--sdk-root", "two"}, want: "--sdk-root may be passed only once",
		},
		{
			name: "legacy eval requires explicit facts", command: sourceEvidenceEvalCommandWithDependencies,
			args: []string{"--out-dir", "artifacts", "--source-root", "source"},
			want: "--source-facts is required; automatic external AST collection is retired",
		},
		{
			name: "legacy external collector is retired", command: sourceEvidenceEvalCommandWithDependencies,
			args: []string{"--out-dir", "artifacts", "--source-root", "source", "--source-facts", "facts.json", "--ast-tool-dir", "tool"},
			want: "--ast-tool-dir requires the retired external source-evidence collector",
		},
		{
			name: "legacy map preserves required input priority", command: sourceOperationMapCommandWithDependencies,
			args: []string{"--source-facts-compare", "comparison.json"}, want: "--openapi is required",
		},
		{
			name: "legacy eval requires destination first", command: sourceEvidenceEvalCommandWithDependencies,
			args: nil, want: "--out-dir is required",
		},
		{
			name: "legacy eval requires source root before reads", command: sourceEvidenceEvalCommandWithDependencies,
			args: []string{"--out-dir", "artifacts", "--source-facts", "facts.json", "--openapi", "openapi.json"},
			want: "--source-root is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.command(test.args, dependencies)
			requireSourceUsageError(t, err, test.want)
		})
	}
}

func TestCompleteSourceBundlePublishesBeforeWarningAndDecision(t *testing.T) {
	parsed := commandInput{Options: map[string][]string{"--openapi": {"openapi.json"}}}
	for _, state := range []contracts.OpenAPIDocumentState{contracts.OpenAPIUnavailable, contracts.OpenAPIDegraded} {
		t.Run(string(state), func(t *testing.T) {
			var stderr bytes.Buffer
			dependencies := defaultAuthoringSourceDependencies()
			dependencies.core.stderr = &stderr
			dependencies.absolutePath = func(value string) (string, error) { return value, nil }
			dependencies.core.mkdirAll = func(string, os.FileMode) error { return nil }
			published := false
			dependencies.bundleStatus = func(sourceoperation.Bundle) (sourceoperation.BundleStatus, error) {
				if !published {
					t.Fatal("bundle status read before publication completed")
				}
				reason := contracts.OpenAPIReasonUnreadable
				return sourceoperation.BundleStatus{DocumentState: state, ReasonCode: &reason}, nil
			}
			publishFailure := errors.New("publisher failed")
			dependencies.publish = func(context.Context, artifactpublish.Options) error { return publishFailure }
			if _, err := completeSourceBundle(context.Background(), "bundle", sourceoperation.Bundle{}, parsed, sourceModeQualified, false, dependencies); !errors.Is(err, publishFailure) {
				t.Fatalf("completeSourceBundle(publish failure) error = %v, want publisher failure", err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("warning before failed publication = %q, want empty", stderr.String())
			}

			dependencies.publish = func(context.Context, artifactpublish.Options) error {
				published = true
				return nil
			}
			status, err := completeSourceBundle(context.Background(), "bundle", sourceoperation.Bundle{}, parsed, sourceModeQualified, false, dependencies)
			if err != nil || status != 0 {
				t.Fatalf("completeSourceBundle(success) = (%d, %v), want (0, nil)", status, err)
			}
			want := "warning: OpenAPI input " + string(state) + " (unreadable); source evidence remains valid\n"
			if stderr.String() != want {
				t.Fatalf("warning after publication = %q, want %q", stderr.String(), want)
			}
		})
	}
}

func TestUnverifiedSourceGrammarRejectsUnboundedOrUnjoinedInputs(t *testing.T) {
	dependencies := defaultAuthoringSourceDependencies()
	parsed := func(t *testing.T, arguments ...string) []string {
		t.Helper()
		return append([]string{
			"--allow-unverified-source", "--artifact-dir", "artifacts",
			"--source-root", t.TempDir(), "--schema", filepath.Join(t.TempDir(), "schema.json"),
			"--provider-module", "example.invalid/provider", "--resources", "example_a",
		}, arguments...)
	}

	_, err := sourceOperationMapCommandWithDependencies(parsed(t, "--provider-file", "../outside.go"), dependencies)
	requireSourceUsageError(t, err, "--provider-file must use portable paths inside its explicit root")

	_, err = sourceOperationMapCommandWithDependencies(parsed(t,
		"--provider-file", "provider.go",
		"--sdk-file", "example.invalid/sdk=client.go",
	), dependencies)
	requireSourceUsageError(t, err, "--sdk-file has no matching --sdk-root for module example.invalid/sdk")

	_, err = sourceOperationMapCommandWithDependencies(parsed(t,
		"--provider-file", "provider.go", "--provider-file", "provider.go",
	), dependencies)
	requireSourceUsageError(t, err, "--provider-file contains duplicate value provider.go")
}

func TestSourceEvidenceLegacyPublishesCompleteSetBeforeRegressionExit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"openapi.json", "schema.json", "facts.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout bytes.Buffer
	dependencies := defaultAuthoringSourceDependencies()
	dependencies.core.stdout = &stdout
	dependencies.validateLegacyAPI = func(map[string]any) error { return nil }
	deriveCalls := 0
	dependencies.deriveLegacy = func(options sourceoperation.LegacyOptions) (map[string]any, error) {
		deriveCalls++
		return map[string]any{
			"diagnostics": []any{},
			"registry":    map[string]any{},
			"summary":     map[string]any{"resources": float64(0)},
		}, nil
	}
	dependencies.compareLegacy = func(map[string]any, map[string]any) map[string]any {
		return map[string]any{"changes": []any{}, "summary": map[string]any{}}
	}
	dependencies.evaluateLegacy = func(map[string]any, map[string]any) map[string]any {
		return map[string]any{"changes": []any{}, "summary": map[string]any{"regressions": float64(1)}}
	}
	dependencies.renderLegacy = func(map[string]any, ...string) string { return "# evaluation\n" }
	dependencies.legacyRegression = func(evaluation map[string]any) bool {
		if !strings.Contains(stdout.String(), "\"regressions\": 1") {
			t.Fatal("regression decision ran before published evaluation reached stdout")
		}
		return true
	}
	var published artifactpublish.Options
	dependencies.publish = func(_ context.Context, options artifactpublish.Options) error {
		published = options
		return nil
	}
	output := filepath.Join(root, "evaluation")
	status, err := sourceEvidenceEvalCommandWithDependencies([]string{
		"--openapi", filepath.Join(root, "openapi.json"),
		"--schema", filepath.Join(root, "schema.json"),
		"--source-root", root,
		"--source-facts", filepath.Join(root, "facts.json"),
		"--out-dir", output,
		"--fail-on-regression",
	}, dependencies)
	if err != nil || status != 1 {
		t.Fatalf("sourceEvidenceEvalCommandWithDependencies() = (%d, %v), want (1, nil)", status, err)
	}
	if deriveCalls != 2 {
		t.Fatalf("derive calls = %d, want candidate and control", deriveCalls)
	}
	if published.Destination != output || !reflect.DeepEqual(published.Vocabulary.Required, legacyEvaluationVocabulary.Required) {
		t.Fatalf("published options = %#v", published)
	}
	if len(published.Artifacts) != len(legacyEvaluationVocabulary.Required) {
		t.Fatalf("published artifact count = %d, want %d", len(published.Artifacts), len(legacyEvaluationVocabulary.Required))
	}
	for index, name := range legacyEvaluationVocabulary.Required {
		if published.Artifacts[index].Name != name || len(published.Artifacts[index].Bytes) == 0 {
			t.Errorf("published artifact[%d] = %#v, want nonempty %q", index, published.Artifacts[index], name)
		}
	}
}

func TestSourceOperationUnverifiedRunsInProcessAndPublishesSealedCore(t *testing.T) {
	repository := filepath.Clean(filepath.Join("..", "..", ".."))
	fixture := filepath.Join(repository, "tests", "fixtures", "authoring", "source-first-v2")
	provider := filepath.Join(fixture, "provider")
	sdk := filepath.Join(fixture, "sdk")
	schema := filepath.Join(fixture, "provider-schema.json")
	providerFiles := []string{
		"internal/fixture/runtime.go", "provider.go", "resource_ambiguous.go", "resource_direct_http.go",
		"resource_dynamic.go", "resource_sdk_http.go", "resource_sdk_symbol.go", "resource_unresolved.go",
	}
	sdkFiles := []string{"alpha/client.go", "beta/client.go", "catalog/catalog.go", "go.mod", "opaque/opaque.go"}
	resources := strings.Join([]string{
		"sourcefirst_ambiguous", "sourcefirst_direct_http", "sourcefirst_dynamic", "sourcefirst_no_source",
		"sourcefirst_sdk_http", "sourcefirst_sdk_symbol", "sourcefirst_unresolved",
	}, ",")
	arguments := []string{
		"--allow-unverified-source", "--artifact-dir", filepath.Join(t.TempDir(), "bundle"),
		"--source-root", provider, "--schema", schema,
		"--provider-module", "example.invalid/terraform-provider-sourcefirst", "--resources", resources,
		"--sdk-root", "example.invalid/sourcefirst-sdk@v0.0.0=" + sdk,
	}
	for _, name := range providerFiles {
		arguments = append(arguments, "--provider-file", name)
	}
	for _, name := range sdkFiles {
		arguments = append(arguments, "--sdk-file", "example.invalid/sourcefirst-sdk="+name)
	}
	dependencies := defaultAuthoringSourceDependencies()
	var published artifactpublish.Options
	dependencies.publish = func(_ context.Context, options artifactpublish.Options) error {
		published = options
		return nil
	}
	status, err := sourceOperationMapCommandWithDependencies(arguments, dependencies)
	if err != nil || status != 0 {
		t.Fatalf("unverified source-operation-map = (%d, %v), want (0, nil)", status, err)
	}
	if len(published.Artifacts) != len(sourceBundleVocabulary.Required) {
		t.Fatalf("published artifacts = %d, want %d", len(published.Artifacts), len(sourceBundleVocabulary.Required))
	}
	for index, name := range sourceBundleVocabulary.Required {
		if published.Artifacts[index].Name != name {
			t.Errorf("published artifact[%d] = %q, want %q", index, published.Artifacts[index].Name, name)
		}
	}

	summaryArtifact := decodedSourceArtifact(t, published.Artifacts, "summary.json")
	summary := summaryArtifact["source_summary"].(map[string]any)
	for name, want := range map[string]float64{
		"selected_total": 7, "applicable_total": 7, "source_call_observed_total": 4, "endpoint_observed_total": 2,
	} {
		if got := summary[name]; got != want {
			t.Errorf("summary.json source_summary[%q] = %#v, want %v", name, got, want)
		}
	}
	wantCounts := map[string]any{
		"observed_http": float64(2), "observed_sdk_call": float64(1), "ambiguous": float64(1), "dynamic": float64(1),
		"unresolved": float64(1), "no_source": float64(1), "not_applicable": float64(0),
	}
	if got := summary["classification_counts"]; !reflect.DeepEqual(got, wantCounts) {
		t.Errorf("summary.json classification_counts = %#v, want %#v", got, wantCounts)
	}
	if got := summary["endpoint_coverage"]; !reflect.DeepEqual(got, map[string]any{"state": "ratio", "numerator": float64(2), "denominator": float64(7)}) {
		t.Errorf("summary.json endpoint_coverage = %#v, want fixed 2/7 ratio", got)
	}

	registryArtifact := decodedSourceArtifact(t, published.Artifacts, "source-registry.json")
	registry := registryArtifact["resources"].(map[string]any)
	wantResources := map[string]struct {
		classification string
		reason         any
		chains         int
	}{
		"sourcefirst_ambiguous":   {classification: "ambiguous", reason: "multiple_viable_candidates", chains: 2},
		"sourcefirst_direct_http": {classification: "observed_http", reason: nil, chains: 1},
		"sourcefirst_dynamic":     {classification: "dynamic", reason: nil, chains: 1},
		"sourcefirst_no_source":   {classification: "no_source", reason: "provider_source_missing", chains: 0},
		"sourcefirst_sdk_http":    {classification: "observed_http", reason: nil, chains: 1},
		"sourcefirst_sdk_symbol":  {classification: "observed_sdk_call", reason: nil, chains: 1},
		"sourcefirst_unresolved":  {classification: "unresolved", reason: "call_chain_unresolved", chains: 1},
	}
	for resource, want := range wantResources {
		entry := registry[resource].(map[string]any)
		chains := entry["chains"].([]any)
		if got := entry["classification"]; got != want.classification {
			t.Errorf("source-registry.json %s classification = %#v, want %q", resource, got, want.classification)
		}
		if got := entry["reason_code"]; got != want.reason {
			t.Errorf("source-registry.json %s reason_code = %#v, want %#v", resource, got, want.reason)
		}
		if len(chains) != want.chains {
			t.Errorf("source-registry.json %s chains = %d, want %d", resource, len(chains), want.chains)
		}
	}
	for index, wantPath := range []string{"/v1/alpha/{id}", "/v2/beta/{id}"} {
		chains := registry["sourcefirst_ambiguous"].(map[string]any)["chains"].([]any)
		endpoint := chains[index].(map[string]any)["endpoint"].(map[string]any)
		if got := endpoint["path_template"]; got != wantPath {
			t.Errorf("sourcefirst_ambiguous chain[%d] path_template = %#v, want %q", index, got, wantPath)
		}
	}
	sdkHTTPChains := registry["sourcefirst_sdk_http"].(map[string]any)["chains"].([]any)
	sdkHTTPEndpoint := sdkHTTPChains[0].(map[string]any)["endpoint"].(map[string]any)
	if gotPath, gotOrigin := sdkHTTPEndpoint["path_template"], sdkHTTPEndpoint["origin"]; gotPath != "/v1/catalog/{id}" || gotOrigin != "sdk" {
		t.Errorf("sourcefirst_sdk_http endpoint path/origin = %#v/%#v, want /v1/catalog/{id}/sdk", gotPath, gotOrigin)
	}
}

func requireSourceUsageError(t *testing.T, err error, want string) {
	t.Helper()
	var exit *cliExit
	if !errors.As(err, &exit) || exit.status != 2 || exit.message != want {
		t.Fatalf("error = %#v, want usage status 2 %q", err, want)
	}
}
