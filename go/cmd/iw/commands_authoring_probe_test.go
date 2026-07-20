package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/artifactpublish"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/providerprobe"
)

func authoringProbeTestDependencies(stdout, stderr *bytes.Buffer) authoringProbeDependencies {
	dependencies := defaultAuthoringProbeDependencies()
	dependencies.core.stdout = stdout
	dependencies.core.stderr = stderr
	dependencies.inspectMode = func(string) (providerprobe.Mode, error) { return providerprobe.LegacyV1, nil }
	dependencies.prepareWorkDirectory = func(string) error { return nil }
	return dependencies
}

func authoringProbeArtifacts(mode providerprobe.Mode, optionalMap bool) []providerprobe.Artifact {
	names := []string{
		"source-registry.json",
		"source-diagnostics.json",
		"summary.json",
		"summary.md",
	}
	if mode == providerprobe.LegacyV1 {
		names = []string{
			"source-registry.json",
			"source-diagnostics.json",
			"openapi-map.json",
			"summary.json",
			"summary.md",
		}
	} else {
		names = append(names, "input-provenance.json", "openapi-diagnostics.json")
		if optionalMap {
			names = append(names, "openapi-map.json")
		}
	}
	artifacts := make([]providerprobe.Artifact, len(names))
	for index, name := range names {
		artifacts[index] = providerprobe.Artifact{Name: name, Bytes: []byte("sealed:" + name + "\n")}
	}
	return artifacts
}

func publishedProbeArtifact(artifacts []artifactpublish.Artifact, name string) ([]byte, bool) {
	for _, artifact := range artifacts {
		if artifact.Name == name {
			return artifact.Bytes, true
		}
	}
	return nil, false
}

type authoringProbeMemoryFiles struct {
	directories []string
	files       map[string][]byte
}

func (files *authoringProbeMemoryFiles) dependencies(stdout, stderr *bytes.Buffer) authoringProbeDependencies {
	dependencies := authoringProbeTestDependencies(stdout, stderr)
	files.files = make(map[string][]byte)
	dependencies.core.mkdirAll = func(directory string, _ os.FileMode) error {
		files.directories = append(files.directories, directory)
		return nil
	}
	dependencies.core.writeFile = func(filename string, contents []byte, _ os.FileMode) error {
		files.files[filename] = append([]byte(nil), contents...)
		return nil
	}
	return dependencies
}

func TestProviderProbeCommandPublishesSealedLegacySetThenCopiesInNodeOrder(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var files authoringProbeMemoryFiles
	dependencies := files.dependencies(&stdout, &stderr)
	workDirectory := filepath.Join(t.TempDir(), "work")
	expectedEnvironment := map[string]string{"PATH": "/fixture/bin", "TOKEN": "fixture"}
	dependencies.environment = func() map[string]string { return expectedEnvironment }
	runCalls := 0
	dependencies.run = func(_ context.Context, options providerprobe.RunOptions) (authoringProbeResult, error) {
		runCalls++
		expectedWorkDirectory, absoluteErr := filepath.Abs("requested-work")
		if absoluteErr != nil {
			t.Fatal(absoluteErr)
		}
		if options.RecipePath != "recipe.json" || options.WorkDirectory != expectedWorkDirectory {
			t.Fatalf("provider probe run options = %#v", options)
		}
		if diff := reflect.DeepEqual(options.Environment, expectedEnvironment); !diff {
			t.Fatalf("provider probe environment = %#v, want %#v", options.Environment, expectedEnvironment)
		}
		options.Environment["TOKEN"] = "mutated"
		return authoringProbeResult{
			artifacts:     authoringProbeArtifacts(providerprobe.LegacyV1, false),
			markdownCopy:  []byte("copy:summary.md\n"),
			mode:          providerprobe.LegacyV1,
			workDirectory: workDirectory,
		}, nil
	}
	var published artifactpublish.Options
	publishCalls := 0
	dependencies.publish = func(_ context.Context, options artifactpublish.Options) error {
		publishCalls++
		published = options
		return nil
	}
	output := filepath.Join(t.TempDir(), "copies", "summary.json")
	markdown := filepath.Join(t.TempDir(), "copies", "summary.md")
	status, err := providerProbeCommandWithDependencies([]string{
		"recipe.json", "--work-dir", "requested-work", "--out", output, "--markdown", markdown,
	}, dependencies)
	if err != nil || status != 0 {
		t.Fatalf("providerProbeCommandWithDependencies = (%d, %v), want (0, nil)", status, err)
	}
	if runCalls != 1 || publishCalls != 1 {
		t.Fatalf("run/publish calls = %d/%d, want 1/1", runCalls, publishCalls)
	}
	if expectedEnvironment["TOKEN"] != "fixture" {
		t.Fatalf("command environment mutated = %#v", expectedEnvironment)
	}
	if got, want := published.Destination, filepath.Join(workDirectory, "artifacts"); got != want {
		t.Errorf("publication destination = %q, want %q", got, want)
	}
	if got, want := published.Vocabulary, providerProbeLegacyVocabulary; !reflect.DeepEqual(got, want) {
		t.Errorf("publication vocabulary = %#v, want %#v", got, want)
	}
	publishedSummary, _ := publishedProbeArtifact(published.Artifacts, "summary.json")
	if got, want := string(publishedSummary), "sealed:summary.json\n"; got != want {
		t.Errorf("published summary bytes = %q, want %q", got, want)
	}
	publishedMarkdown, _ := publishedProbeArtifact(published.Artifacts, "summary.md")
	if got, want := string(publishedMarkdown), "sealed:summary.md\n"; got != want {
		t.Errorf("published Markdown bytes = %q, want %q", got, want)
	}
	for _, copyCase := range []struct{ path, want string }{
		{output, "sealed:summary.json\n"},
		{markdown, "copy:summary.md\n"},
	} {
		got, found := files.files[copyCase.path]
		if !found {
			t.Fatalf("missing copied artifact %s", copyCase.path)
		}
		if string(got) != copyCase.want {
			t.Errorf("copied artifact %s = %q, want %q", copyCase.path, got, copyCase.want)
		}
	}
	wantStdout := "wrote " + filepath.Join(workDirectory, "artifacts", "summary.json") + "\n" +
		"wrote " + filepath.Join(workDirectory, "artifacts", "summary.md") + "\n"
	if got := stdout.String(); got != wantStdout {
		t.Errorf("stdout = %q, want %q", got, wantStdout)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

func TestProviderProbeCommandQualifiedRequiresWorkDirectoryBeforeRunOrPublish(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dependencies := authoringProbeTestDependencies(&stdout, &stderr)
	dependencies.inspectMode = func(string) (providerprobe.Mode, error) { return providerprobe.QualifiedV2, nil }
	runCalls, publishCalls := 0, 0
	dependencies.run = func(_ context.Context, _ providerprobe.RunOptions) (authoringProbeResult, error) {
		runCalls++
		return authoringProbeResult{artifacts: authoringProbeArtifacts(providerprobe.QualifiedV2, false), markdownCopy: []byte("sealed:summary.md\n"), mode: providerprobe.QualifiedV2}, nil
	}
	dependencies.publish = func(context.Context, artifactpublish.Options) error {
		publishCalls++
		return nil
	}
	status, err := providerProbeCommandWithDependencies([]string{"recipe.json"}, dependencies)
	if status != 0 {
		t.Fatalf("qualified providerProbeCommandWithDependencies status = %d, want deferred usage status", status)
	}
	requireSourceUsageError(t, err, "provider-probe source-first mode requires --work-dir")
	if runCalls != 0 || publishCalls != 0 {
		t.Errorf("qualified run/publish calls = %d/%d, want 0/0", runCalls, publishCalls)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("qualified stderr = %q, want top-level usage rendering only", got)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("qualified stdout = %q, want empty", got)
	}
}

func TestProviderProbeCommandQualifiedPublishesCompleteVocabularyAndOptionalMap(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dependencies := authoringProbeTestDependencies(&stdout, &stderr)
	dependencies.inspectMode = func(string) (providerprobe.Mode, error) { return providerprobe.QualifiedV2, nil }
	workDirectory := filepath.Join(t.TempDir(), "fresh-work")
	dependencies.prepareWorkDirectory = prepareProviderProbeWorkDirectory
	dependencies.run = func(_ context.Context, options providerprobe.RunOptions) (authoringProbeResult, error) {
		if options.WorkDirectory != workDirectory {
			t.Fatalf("qualified work directory = %q, want %q", options.WorkDirectory, workDirectory)
		}
		if options.ExpectedMode != providerprobe.QualifiedV2 {
			t.Fatalf("qualified expected mode = %q, want %q", options.ExpectedMode, providerprobe.QualifiedV2)
		}
		info, err := os.Lstat(workDirectory)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("qualified prepared work directory = (%v, %v), want private 0700", info, err)
		}
		return authoringProbeResult{artifacts: authoringProbeArtifacts(providerprobe.QualifiedV2, true), markdownCopy: []byte("sealed:summary.md\n"), mode: providerprobe.QualifiedV2}, nil
	}
	var published artifactpublish.Options
	dependencies.publish = func(_ context.Context, options artifactpublish.Options) error {
		published = options
		return nil
	}
	status, err := providerProbeCommandWithDependencies([]string{"recipe.json", "--work-dir", workDirectory}, dependencies)
	if err != nil || status != 0 {
		t.Fatalf("qualified providerProbeCommandWithDependencies = (%d, %v), want (0, nil)", status, err)
	}
	if got, want := published.Destination, filepath.Join(workDirectory, "artifacts"); got != want {
		t.Errorf("qualified publication destination = %q, want %q", got, want)
	}
	if got, want := published.Vocabulary, providerProbeQualifiedVocabulary; !reflect.DeepEqual(got, want) {
		t.Errorf("qualified publication vocabulary = %#v, want %#v", got, want)
	}
	if _, found := publishedProbeArtifact(published.Artifacts, "openapi-map.json"); !found {
		t.Fatal("qualified optional OpenAPI map did not reach the complete-set publisher")
	}
	if got, want := stdout.String(), "wrote "+filepath.Join(workDirectory, "artifacts", "summary.json")+"\n"+"wrote "+filepath.Join(workDirectory, "artifacts", "summary.md")+"\n"; got != want {
		t.Errorf("qualified stdout = %q, want %q", got, want)
	}
}

func TestProviderProbeCommandQualifiedReplacementRemovesOptionalMapOnlyAfterSuccess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dependencies := authoringProbeTestDependencies(&stdout, &stderr)
	dependencies.inspectMode = func(string) (providerprobe.Mode, error) { return providerprobe.QualifiedV2, nil }
	dependencies.prepareWorkDirectory = prepareProviderProbeWorkDirectory
	dependencies.publish = artifactpublish.Publish
	workDirectory := filepath.Join(t.TempDir(), "fresh-work")
	optionalMap := true
	dependencies.run = func(context.Context, providerprobe.RunOptions) (authoringProbeResult, error) {
		return authoringProbeResult{
			artifacts:    authoringProbeArtifacts(providerprobe.QualifiedV2, optionalMap),
			markdownCopy: []byte("sealed:summary.md\n"),
			mode:         providerprobe.QualifiedV2,
		}, nil
	}
	run := func() (int, error) {
		return providerProbeCommandWithDependencies([]string{"recipe.json", "--work-dir", workDirectory}, dependencies)
	}
	if status, err := run(); err != nil || status != 0 {
		t.Fatalf("qualified publish with optional map = (%d, %v), want (0, nil)", status, err)
	}
	mapPath := filepath.Join(workDirectory, "artifacts", "openapi-map.json")
	if _, err := os.Stat(mapPath); err != nil {
		t.Fatalf("published optional map: %v", err)
	}

	optionalMap = false
	if status, err := run(); err != nil || status != 0 {
		t.Fatalf("qualified publish without optional map = (%d, %v), want (0, nil)", status, err)
	}
	if _, err := os.Stat(mapPath); !os.IsNotExist(err) {
		t.Fatalf("stale optional map after successful replacement: %v", err)
	}

	optionalMap = true
	if status, err := run(); err != nil || status != 0 {
		t.Fatalf("qualified republish with optional map = (%d, %v), want (0, nil)", status, err)
	}
	dependencies.publish = func(context.Context, artifactpublish.Options) error {
		return errors.New("injected publication failure")
	}
	optionalMap = false
	if status, err := run(); err != nil || status != 2 {
		t.Fatalf("qualified failed replacement = (%d, %v), want contained status 2", status, err)
	}
	if _, err := os.Stat(mapPath); err != nil {
		t.Fatalf("failed replacement removed previously committed optional map: %v", err)
	}
}

func TestProviderProbeCommandContainsOperationalFailuresAndDebugOrdering(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dependencies := authoringProbeTestDependencies(&stdout, &stderr)
	dependencies.environment = func() map[string]string { return map[string]string{"INFRAWRIGHT_DEBUG_TRACEBACK": " yes "} }
	dependencies.run = func(context.Context, providerprobe.RunOptions) (authoringProbeResult, error) {
		return authoringProbeResult{}, errors.New("fixture runner failed")
	}
	dependencies.publish = func(context.Context, artifactpublish.Options) error {
		t.Fatal("publisher ran after runner failure")
		return nil
	}
	status, err := providerProbeCommandWithDependencies([]string{"recipe.json"}, dependencies)
	if err != nil || status != 2 {
		t.Fatalf("providerProbeCommandWithDependencies = (%d, %v), want (2, nil)", status, err)
	}
	got := stderr.String()
	if !strings.Contains(got, "fixture runner failed\n") || !strings.Contains(got, "goroutine ") {
		t.Errorf("debug stderr = %q, want error plus stack", got)
	}
	if !strings.HasSuffix(got, "error: fixture runner failed\n") {
		t.Errorf("debug stderr suffix = %q, want concise final error", got)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestProviderProbeCommandRefusesToRebuildMissingMarkdownCopy(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dependencies := authoringProbeTestDependencies(&stdout, &stderr)
	dependencies.run = func(context.Context, providerprobe.RunOptions) (authoringProbeResult, error) {
		return authoringProbeResult{
			artifacts:     authoringProbeArtifacts(providerprobe.LegacyV1, false),
			mode:          providerprobe.LegacyV1,
			workDirectory: t.TempDir(),
		}, nil
	}
	publishCalls := 0
	dependencies.publish = func(context.Context, artifactpublish.Options) error {
		publishCalls++
		return nil
	}
	status, err := providerProbeCommandWithDependencies([]string{"recipe.json", "--markdown", filepath.Join(t.TempDir(), "copy.md")}, dependencies)
	if err != nil || status != 2 {
		t.Fatalf("providerProbeCommandWithDependencies = (%d, %v), want (2, nil)", status, err)
	}
	if publishCalls != 1 {
		t.Errorf("publish calls = %d, want 1 before copy failure", publishCalls)
	}
	if got, want := stderr.String(), "error: provider probe result has no Markdown copy\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
}

func TestProviderProbeCommandUsageFailureDoesNotRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dependencies := authoringProbeTestDependencies(&stdout, &stderr)
	dependencies.run = func(context.Context, providerprobe.RunOptions) (authoringProbeResult, error) {
		t.Fatal("runner called for usage failure")
		return authoringProbeResult{}, nil
	}
	_, err := providerProbeCommandWithDependencies([]string{"--out", "summary.json"}, dependencies)
	requireCLIExit(t, err, 2, false)
}
