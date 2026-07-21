package adopt

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func runnerTestRoot(t *testing.T, resourceTypes ...string) metadata.LoadedPackRoot {
	t.Helper()
	directory := t.TempDir()
	resources := make(map[string]metadata.LoadedResourceMetadata, len(resourceTypes))
	resourceSchemas := make(map[string]any, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		resources[resourceType] = metadata.LoadedResourceMetadata{
			Type: resourceType, Product: "test", Provider: testProvider,
			Registry: metadata.JsonObject{"generate": true, "product": "test"},
		}
		resourceSchemas[resourceType] = metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"id":   metadata.JsonObject{"computed": true, "optional": true, "type": "string"},
			"name": metadata.JsonObject{"required": true, "type": "string"},
		}}}
	}
	schema, err := json.Marshal(metadata.JsonObject{"resource_schemas": resourceSchemas})
	if err != nil {
		t.Fatalf("json.Marshal schema: %v", err)
	}
	schemaDirectory := filepath.Join(directory, "schemas", "provider")
	if err := os.MkdirAll(schemaDirectory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(schemaDirectory, testProvider+".json"), schema, 0o600); err != nil {
		t.Fatalf("os.WriteFile schema: %v", err)
	}
	manifest := metadata.PackManifest{
		Name: "test-pack", Directory: directory, Data: metadata.JsonObject{},
		ProviderPrefixes: map[string]string{"test_": testProvider}, ProviderSources: map[string]string{testProvider: "hashicorp/test"},
	}
	return metadata.LoadedPackRoot{
		Active: metadata.PackSelection{Packs: []string{"test-pack"}},
		Packs: metadata.PackMetadata{
			Manifests: []metadata.PackManifest{manifest}, ProviderPrefixes: map[string]string{"test_": testProvider},
			ProviderSources: map[string]string{testProvider: "hashicorp/test"}, ProviderOwners: map[string]string{testProvider: "test-pack"},
		},
		Resources: resources,
	}
}

func runnerTestDeployment(workspace string, _ []string) deployment.Deployment {
	return deployment.Deployment{
		Overlay: workspace,
		Roots:   map[string]deployment.RootProviderConfig{testProvider: {}},
	}
}

func writeRunnerInput(t *testing.T, directory, resourceType, text string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll input: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, resourceType+".json"), []byte(text), 0o600); err != nil {
		t.Fatalf("os.WriteFile input: %v", err)
	}
}

func emptyRunnerPolicy(t *testing.T) *metadata.DriftPolicy {
	t.Helper()
	policy, err := metadata.NewDriftPolicy(nil, "runner test")
	if err != nil {
		t.Fatalf("metadata.NewDriftPolicy: %v", err)
	}
	return policy
}

func stateForRunnerRequest(request AdoptionStateRequest) map[string]OracleStateObject {
	output := make(map[string]OracleStateObject, len(request.KeyToImportID))
	for key := range request.KeyToImportID {
		output[key] = OracleStateObject{Values: map[string]any{"name": key}, SensitiveValues: map[string]any{}}
	}
	return output
}

func snapshotRunnerTree(t *testing.T, root string) map[string]string {
	t.Helper()
	output := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		output[relative] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot output tree: %v", err)
	}
	return output
}

func TestRunAdoptBatchPerResourcePendingMoveAppearsAfterLoader(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceType := "test_alpha"
	root := runnerTestRoot(t, resourceType)
	dep := runnerTestDeployment(workspace, nil)
	writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"alpha"}]`)
	loaderCalls := 0
	loader := func(request AdoptionStateRequest) (map[string]OracleStateObject, error) {
		loaderCalls++
		pending, err := pendingMovesPath(dep, resourceType, "tenant")
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(pending), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(pending, []byte("pending"), 0o600); err != nil {
			return nil, err
		}
		return stateForRunnerRequest(request), nil
	}
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		Deployment: dep, InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root,
		Selectors: []string{resourceType}, StateLoader: loader, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if loaderCalls != 1 || !reflect.DeepEqual(result.Failed, []string{resourceType}) {
		t.Fatalf("loader calls/result = %d/%#v, want one call and resource failure", loaderCalls, result)
	}
	paths, err := pendingMovesPath(dep, resourceType, "tenant")
	if err != nil {
		t.Fatalf("pendingMovesPath: %v", err)
	}
	if tree := snapshotRunnerTree(t, filepath.Dir(filepath.Dir(paths))); len(tree) != 1 {
		t.Fatalf("output tree = %#v, want only injected pending marker", tree)
	}
}

func TestRunAdoptBatchSingletonFailureDoesNotPublishUnselectedType(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	dep := runnerTestDeployment(workspace, resourceTypes)
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"same"}]`)
	}
	loaded := make([]string, 0, 1)
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		Deployment: dep, InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root, Selectors: []string{resourceTypes[1]},
		StateLoader: func(request AdoptionStateRequest) (map[string]OracleStateObject, error) {
			loaded = append(loaded, request.ResourceType)
			return nil, errors.New("selected singleton provider read failed")
		},
		Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if !reflect.DeepEqual(result.Failed, []string{resourceTypes[1]}) || !reflect.DeepEqual(loaded, []string{resourceTypes[1]}) {
		t.Fatalf("failed/loaded = %v/%v, want only selected singleton %s", result.Failed, loaded, resourceTypes[1])
	}
	if tree := snapshotRunnerTree(t, workspace); len(tree) != 0 {
		t.Fatalf("atomic failure published files: %#v", tree)
	}
}

func TestRunAdoptBatchUnsupportedPreflightNeverLoadsState(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceType := "test_alpha"
	root := runnerTestRoot(t, resourceType)
	resource := root.Resources[resourceType]
	resource.Registry["adopt"] = metadata.JsonObject{"unsupported_if": []any{metadata.JsonObject{
		"evidence": []any{"fixture"}, "match": metadata.JsonObject{"system": true},
		"provider": metadata.JsonObject{"source": "example/test", "version": "1.0.0"}, "reason": "unsupported",
	}}}
	root.Resources[resourceType] = resource
	writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"alpha","system":true}]`)
	called := false
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		Deployment: runnerTestDeployment(workspace, nil), InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root,
		Selectors: []string{resourceType}, StateLoader: func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
			called = true
			return nil, nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if called || !reflect.DeepEqual(result.Failed, []string{resourceType}) {
		t.Fatalf("state called/result = %v/%#v, want preflight failure", called, result)
	}
	if tree := snapshotRunnerTree(t, workspace); len(tree) != 0 {
		t.Fatalf("unsupported preflight published files: %#v", tree)
	}
}

func TestRunnerHasNoCredentialOrLiveApplySurface(t *testing.T) {
	files := []string{"runner.go", "runner_loaders.go"}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("os.ReadFile(%s): %v", file, err)
		}
		text := string(content)
		if strings.Contains(text, "terraform apply") || strings.Contains(text, "ALLOW_LIVE") || strings.Contains(text, "ZSCALER_") {
			t.Errorf("%s contains a forbidden live/credential execution surface", file)
		}
	}
}

func TestRunAdoptBatchAndAdoptResourceRejectNilPolicyBeforeWork(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceType := "test_alpha"
	root := runnerTestRoot(t, resourceType)
	writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"alpha"}]`)
	called := false
	loader := func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
		called = true
		return nil, nil
	}
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		Deployment: runnerTestDeployment(workspace, nil), InputDirectory: input,
		Policy: nil, Root: root, Selectors: []string{resourceType}, StateLoader: loader, Tenant: "tenant",
	})
	if err == nil || !strings.Contains(err.Error(), "requires a drift policy") {
		t.Fatalf("RunAdoptBatch nil-policy error = %v", err)
	}
	if called || len(result.Failed)+len(result.Processed)+len(result.Skipped) != 0 {
		t.Fatalf("RunAdoptBatch performed work: called=%v result=%#v", called, result)
	}
	if tree := snapshotRunnerTree(t, workspace); len(tree) != 0 {
		t.Fatalf("RunAdoptBatch nil policy published files: %#v", tree)
	}

	_, err = AdoptResourceItems(nil, mustLosslessAdoptionItems(t, `[{"id":"1","name":"alpha"}]`), root.Resources[resourceType], root, loader, nil)
	if err == nil || !strings.Contains(err.Error(), "requires a drift policy") {
		t.Fatalf("AdoptResourceItems nil-policy error = %v", err)
	}
	if called {
		t.Fatal("AdoptResourceItems nil policy invoked loader")
	}
}

func TestDefaultAdoptionLoadersValidateTimeoutEagerly(t *testing.T) {
	options := DefaultAdoptionLoaderOptions{
		Environment: map[string]string{"INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS": "invalid"},
		Root:        runnerTestRoot(t, "test_alpha"), TerraformExecutable: "/not/executed/terraform",
	}
	loader, err := DefaultAdoptionStateLoader(options)
	if err == nil || loader != nil || !strings.Contains(err.Error(), "must be a positive number") {
		t.Fatalf("DefaultAdoptionStateLoader = %v, %v; want eager timeout error", loader, err)
	}
}

func TestDefaultAdoptionLoadersRejectNilEnvironment(t *testing.T) {
	t.Setenv("INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS", "15")
	options := DefaultAdoptionLoaderOptions{
		Environment: nil, Root: runnerTestRoot(t, "test_alpha"),
		TerraformExecutable: "/not/executed/terraform",
	}
	loader, err := DefaultAdoptionStateLoader(options)
	if err == nil || loader != nil || !strings.Contains(err.Error(), "requires an explicit environment") {
		t.Fatalf("DefaultAdoptionStateLoader = %v, %v; want nil-environment rejection", loader, err)
	}
}

func TestRunAdoptBatchDerivationFailureStillEmitsClassifiedCounts(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceType := "test_alpha"
	root := runnerTestRoot(t, resourceType)
	resource := root.Resources[resourceType]
	resource.Registry["adopt"] = metadata.JsonObject{
		"identity_fields": metadata.JsonObject{"import_id": "details.missing"},
	}
	root.Resources[resourceType] = resource
	writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"alpha"}]`)
	diagnostics := make([]string, 0)
	called := false
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		Deployment:     runnerTestDeployment(workspace, nil),
		InputDirectory: input, OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Policy: emptyRunnerPolicy(t), Root: root, Selectors: []string{resourceType},
		StateLoader: func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
			called = true
			return nil, nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if called || !reflect.DeepEqual(result.Failed, []string{resourceType}) {
		t.Fatalf("derivation failure called/result = %v/%#v", called, result)
	}
	if len(diagnostics) < 3 {
		t.Fatalf("diagnostics = %#v, want error, counts, and final summary", diagnostics)
	}
	if !strings.HasPrefix(diagnostics[0], "error: test_alpha: ") {
		t.Fatalf("first diagnostic = %q, want derivation error", diagnostics[0])
	}
	wantCounts := "adopt counts test_alpha: fetched=1 system_skipped=0 unsupported=0 eligible=1 published=0 failed=1"
	if diagnostics[1] != wantCounts {
		t.Fatalf("second diagnostic = %q, want %q", diagnostics[1], wantCounts)
	}
	if tree := snapshotRunnerTree(t, workspace); len(tree) != 0 {
		t.Fatalf("derivation failure published files: %#v", tree)
	}
}

func TestRunAdoptBatchUsesOnlyStateLoaderInReferentFirstOrder(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	root.Packs.Manifests[0].Data["references"] = metadata.JsonObject{
		"test_beta": metadata.JsonObject{"alpha_id": metadata.JsonObject{"referent": "test_alpha", "name_field": "name"}},
	}
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, "[{\"id\":\"1\",\"name\":\"same\"}]")
	}
	loaded := make([]string, 0, len(resourceTypes))
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		Deployment: runnerTestDeployment(workspace, resourceTypes), InputDirectory: input,
		Policy: emptyRunnerPolicy(t), Root: root, Selectors: []string{"test_beta", "test_alpha"},
		StateLoader: func(request AdoptionStateRequest) (map[string]OracleStateObject, error) {
			loaded = append(loaded, request.ResourceType)
			return stateForRunnerRequest(request), nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if want := []string{"test_alpha", "test_beta"}; !reflect.DeepEqual(loaded, want) || !reflect.DeepEqual(result.Processed, want) {
		t.Fatalf("state-loader order/result = %v/%#v, want %v", loaded, result, want)
	}
}

func TestRunAdoptBatchSingletonArtifactsRemainStable(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, "[{\"id\":\"1\",\"name\":\"same\"}]")
	}
	options := RunAdoptBatchOptions{
		Deployment: runnerTestDeployment(workspace, resourceTypes), InputDirectory: input,
		Policy: emptyRunnerPolicy(t), Root: root, Selectors: resourceTypes,
		StateLoader: func(request AdoptionStateRequest) (map[string]OracleStateObject, error) {
			return stateForRunnerRequest(request), nil
		}, Tenant: "tenant",
	}
	if _, err := RunAdoptBatch(options); err != nil {
		t.Fatalf("first RunAdoptBatch: %v", err)
	}
	first := snapshotRunnerTree(t, workspace)
	if _, err := RunAdoptBatch(options); err != nil {
		t.Fatalf("second RunAdoptBatch: %v", err)
	}
	if second := snapshotRunnerTree(t, workspace); !reflect.DeepEqual(second, first) {
		t.Fatalf("singleton artifacts changed on repeat\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func TestRunAdoptBatchFailureIsolationUsesOnlyStateLoader(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, "[{\"id\":\"1\",\"name\":\"same\"}]")
	}
	loaded := make([]string, 0, len(resourceTypes))
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		Deployment: runnerTestDeployment(workspace, resourceTypes), InputDirectory: input,
		Policy: emptyRunnerPolicy(t), Root: root, Selectors: resourceTypes,
		StateLoader: func(request AdoptionStateRequest) (map[string]OracleStateObject, error) {
			loaded = append(loaded, request.ResourceType)
			if request.ResourceType == "test_beta" {
				return nil, errors.New("provider read failed")
			}
			return stateForRunnerRequest(request), nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if want := []string{"test_alpha", "test_beta"}; !reflect.DeepEqual(loaded, want) || !reflect.DeepEqual(result.Processed, []string{"test_alpha"}) || !reflect.DeepEqual(result.Failed, []string{"test_beta"}) {
		t.Fatalf("state-loader isolation = loaded:%v result:%#v", loaded, result)
	}
}
