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

func runnerTestDeployment(workspace string, grouped []string) deployment.Deployment {
	rootConfig := deployment.RootProviderConfig{}
	if len(grouped) > 0 {
		rootConfig.HasGroups = true
		rootConfig.Groups = map[string][]string{"bundle": append([]string(nil), grouped...)}
	}
	return deployment.Deployment{Overlay: workspace, Roots: map[string]deployment.RootProviderConfig{testProvider: rootConfig}}
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
		Deployment: dep, Environment: map[string]string{}, InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root,
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

func TestRunAdoptBatchLogicalRootPublishesAtomicallyAndDeterministically(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	dep := runnerTestDeployment(workspace, resourceTypes)
	writeRunnerInput(t, input, resourceTypes[0], `[{"id":"1","name":"alpha"}]`)
	writeRunnerInput(t, input, resourceTypes[1], `[{"id":"2","name":"beta"}]`)
	batchCalls := 0
	batchLoader := func(requests []OracleBatchResourceRequest) (OracleBatchState, error) {
		batchCalls++
		output := make(OracleBatchState, len(requests))
		for _, request := range requests {
			state := make(map[string]OracleStateObject)
			for key := range request.KeyToImportID {
				state[key] = OracleStateObject{Values: map[string]any{"name": key}, SensitiveValues: map[string]any{}}
			}
			output[request.ResourceType] = state
		}
		return output, nil
	}
	options := RunAdoptBatchOptions{
		BatchStateLoader: batchLoader, Deployment: dep,
		Environment:    map[string]string{"INFRAWRIGHT_ORACLE_BATCH_MODE": "logical-root"},
		InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root,
		Selectors: resourceTypes, StateLoader: func(request AdoptionStateRequest) (map[string]OracleStateObject, error) {
			t.Fatalf("per-resource loader unexpectedly called for %s", request.ResourceType)
			return nil, errors.New("unreachable")
		}, Tenant: "tenant",
	}
	result, err := RunAdoptBatch(options)
	if err != nil {
		t.Fatalf("RunAdoptBatch first: %v", err)
	}
	if batchCalls != 1 || len(result.Failed) != 0 || !reflect.DeepEqual(result.Processed, resourceTypes) {
		t.Fatalf("batch calls/result = %d/%#v", batchCalls, result)
	}
	first := snapshotRunnerTree(t, workspace)
	if len(first) != 4 {
		t.Fatalf("published tree files = %d (%#v), want two config + two imports", len(first), first)
	}
	result, err = RunAdoptBatch(options)
	if err != nil {
		t.Fatalf("RunAdoptBatch second: %v", err)
	}
	if len(result.Failed) != 0 || batchCalls != 2 {
		t.Fatalf("second result/calls = %#v/%d", result, batchCalls)
	}
	if second := snapshotRunnerTree(t, workspace); !reflect.DeepEqual(second, first) {
		t.Fatalf("second output tree drifted\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func TestRunAdoptBatchLogicalRootProjectionFailurePublishesNothing(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	dep := runnerTestDeployment(workspace, resourceTypes)
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"same"}]`)
	}
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		BatchStateLoader: func(requests []OracleBatchResourceRequest) (OracleBatchState, error) {
			return OracleBatchState{
				resourceTypes[0]: {"same": {Values: map[string]any{"name": "same"}, SensitiveValues: map[string]any{}}},
				resourceTypes[1]: {},
			}, nil
		},
		Deployment: dep, Environment: map[string]string{"INFRAWRIGHT_ORACLE_BATCH_MODE": "logical-root"},
		InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root, Selectors: resourceTypes,
		StateLoader: func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
			return nil, errors.New("must not isolate projection failures")
		},
		Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if !reflect.DeepEqual(result.Failed, resourceTypes) {
		t.Fatalf("failed = %v, want %v", result.Failed, resourceTypes)
	}
	if tree := snapshotRunnerTree(t, workspace); len(tree) != 0 {
		t.Fatalf("atomic failure published files: %#v", tree)
	}
}

func TestRunAdoptBatchLogicalRootRejectsUnexpectedOracleResource(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"same"}]`)
	}
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		BatchStateLoader: func(requests []OracleBatchResourceRequest) (OracleBatchState, error) {
			output := OracleBatchState{"test_unexpected": {}}
			for _, request := range requests {
				state := make(map[string]OracleStateObject)
				for key := range request.KeyToImportID {
					state[key] = OracleStateObject{Values: map[string]any{"name": key}, SensitiveValues: map[string]any{}}
				}
				output[request.ResourceType] = state
			}
			return output, nil
		}, Deployment: runnerTestDeployment(workspace, resourceTypes),
		Environment:    map[string]string{"INFRAWRIGHT_ORACLE_BATCH_MODE": "logical-root"},
		InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root, Selectors: resourceTypes,
		StateLoader: func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
			t.Fatal("member loader called for unexpected-resource batch result")
			return nil, nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if !reflect.DeepEqual(result.Failed, resourceTypes) || len(result.Processed) != 0 {
		t.Fatalf("unexpected-resource result = %#v", result)
	}
	if tree := snapshotRunnerTree(t, workspace); len(tree) != 0 {
		t.Fatalf("unexpected-resource result published files: %#v", tree)
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
		Deployment: runnerTestDeployment(workspace, nil), Environment: map[string]string{}, InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root,
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

func TestOracleBatchModeAndBatchFailureIsolationOrder(t *testing.T) {
	if _, err := OracleBatchModeFromEnvironment(map[string]string{"INFRAWRIGHT_ORACLE_BATCH_MODE": "unsafe"}); err == nil {
		t.Fatal("OracleBatchModeFromEnvironment accepted invalid mode")
	}
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"same"}]`)
	}
	isolationOrder := make([]string, 0, len(resourceTypes))
	diagnostics := make([]string, 0)
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		BatchStateLoader: func([]OracleBatchResourceRequest) (OracleBatchState, error) { return nil, errors.New("batch failed") },
		Deployment:       runnerTestDeployment(workspace, resourceTypes),
		Environment:      map[string]string{"INFRAWRIGHT_ORACLE_BATCH_MODE": "logical-root"},
		InputDirectory:   input, Policy: emptyRunnerPolicy(t), Root: root, Selectors: resourceTypes,
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		StateLoader: func(request AdoptionStateRequest) (map[string]OracleStateObject, error) {
			isolationOrder = append(isolationOrder, request.ResourceType)
			if request.ResourceType == resourceTypes[1] {
				return nil, errors.New("isolated provider read failed")
			}
			return stateForRunnerRequest(request), nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if !reflect.DeepEqual(isolationOrder, resourceTypes) {
		t.Fatalf("isolation order = %v, want %v", isolationOrder, resourceTypes)
	}
	if !reflect.DeepEqual(result.Failed, resourceTypes) || len(result.Processed) != 0 {
		t.Fatalf("batch failure result = %#v", result)
	}
	isolationDiagnostic := "error: test_beta: isolated provider read failed"
	batchDiagnostic := "error: logical root bundle: batched Oracle failed; 1 member failure(s) identified above: batch failed"
	isolationIndex, batchIndex := -1, -1
	for index, diagnostic := range diagnostics {
		if diagnostic == isolationDiagnostic {
			isolationIndex = index
		}
		if diagnostic == batchDiagnostic {
			batchIndex = index
		}
	}
	if isolationIndex < 0 || batchIndex <= isolationIndex {
		t.Fatalf("diagnostics = %#v, want isolated member before batch summary", diagnostics)
	}
	if tree := snapshotRunnerTree(t, workspace); len(tree) != 0 {
		t.Fatalf("batch failure published files: %#v", tree)
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
		Deployment: runnerTestDeployment(workspace, nil), Environment: map[string]string{}, InputDirectory: input,
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

func TestRunAdoptBatchNilEnvironmentSnapshotsProcessEnvironment(t *testing.T) {
	t.Setenv("INFRAWRIGHT_ORACLE_BATCH_MODE", "invalid-from-process")
	workspace := t.TempDir()
	resourceType := "test_alpha"
	root := runnerTestRoot(t, resourceType)
	_, err := RunAdoptBatch(RunAdoptBatchOptions{
		Deployment: runnerTestDeployment(workspace, nil), Environment: nil,
		InputDirectory: t.TempDir(), Policy: emptyRunnerPolicy(t), Root: root,
		Selectors: []string{resourceType}, StateLoader: func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
			t.Fatal("state loader called after invalid process batch mode")
			return nil, nil
		}, Tenant: "tenant",
	})
	if err == nil || !strings.Contains(err.Error(), "INFRAWRIGHT_ORACLE_BATCH_MODE") {
		t.Fatalf("RunAdoptBatch nil-environment error = %v", err)
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
	batchLoader, err := DefaultAdoptionBatchStateLoader(options)
	if err == nil || batchLoader != nil || !strings.Contains(err.Error(), "must be a positive number") {
		t.Fatalf("DefaultAdoptionBatchStateLoader = %v, %v; want eager timeout error", batchLoader, err)
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
	batchLoader, err := DefaultAdoptionBatchStateLoader(options)
	if err == nil || batchLoader != nil || !strings.Contains(err.Error(), "requires an explicit environment") {
		t.Fatalf("DefaultAdoptionBatchStateLoader = %v, %v; want nil-environment rejection", batchLoader, err)
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
		Deployment: runnerTestDeployment(workspace, nil), Environment: map[string]string{},
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

func TestRunAdoptBatchLogicalRootUnsupportedBlocksEveryLoader(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	blocked := root.Resources[resourceTypes[1]]
	blocked.Registry["adopt"] = metadata.JsonObject{"unsupported_if": []any{metadata.JsonObject{
		"evidence": []any{"fixture"}, "match": metadata.JsonObject{"system": true},
		"provider": metadata.JsonObject{"source": "example/test", "version": "1.0.0"}, "reason": "unsupported",
	}}}
	root.Resources[resourceTypes[1]] = blocked
	writeRunnerInput(t, input, resourceTypes[0], `[{"id":"1","name":"alpha"}]`)
	writeRunnerInput(t, input, resourceTypes[1], `[{"id":"2","name":"beta","system":true}]`)
	batchCalled, memberCalled := false, false
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		BatchStateLoader: func([]OracleBatchResourceRequest) (OracleBatchState, error) {
			batchCalled = true
			return nil, nil
		}, Deployment: runnerTestDeployment(workspace, resourceTypes),
		Environment:    map[string]string{"INFRAWRIGHT_ORACLE_BATCH_MODE": "logical-root"},
		InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root, Selectors: resourceTypes,
		StateLoader: func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
			memberCalled = true
			return nil, nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if batchCalled || memberCalled || !reflect.DeepEqual(result.Failed, resourceTypes) {
		t.Fatalf("logical unsupported calls/result = %v/%v/%#v", batchCalled, memberCalled, result)
	}
	if tree := snapshotRunnerTree(t, workspace); len(tree) != 0 {
		t.Fatalf("logical unsupported published files: %#v", tree)
	}
}

func TestRunAdoptBatchLogicalRootDerivationFailureEmitsEveryClassifiedCount(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	broken := root.Resources[resourceTypes[1]]
	broken.Registry["adopt"] = metadata.JsonObject{
		"identity_fields": metadata.JsonObject{"import_id": "details.missing"},
	}
	root.Resources[resourceTypes[1]] = broken
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"same"}]`)
	}
	diagnostics := make([]string, 0)
	loaderCalled := false
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		BatchStateLoader: func([]OracleBatchResourceRequest) (OracleBatchState, error) {
			loaderCalled = true
			return nil, nil
		}, Deployment: runnerTestDeployment(workspace, resourceTypes),
		Environment:    map[string]string{"INFRAWRIGHT_ORACLE_BATCH_MODE": "logical-root"},
		InputDirectory: input, OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Policy: emptyRunnerPolicy(t), Root: root, Selectors: resourceTypes,
		StateLoader: func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
			loaderCalled = true
			return nil, nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	wantFailed := []string{resourceTypes[1], resourceTypes[0]}
	if loaderCalled || !reflect.DeepEqual(result.Failed, wantFailed) {
		t.Fatalf("logical derivation failure called/result = %v/%#v", loaderCalled, result)
	}
	wantCounts := []string{
		"adopt counts test_alpha: fetched=1 system_skipped=0 unsupported=0 eligible=1 published=0 failed=1",
		"adopt counts test_beta: fetched=1 system_skipped=0 unsupported=0 eligible=1 published=0 failed=1",
	}
	for _, want := range wantCounts {
		found := false
		for _, diagnostic := range diagnostics {
			if diagnostic == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("diagnostics missing %q: %#v", want, diagnostics)
		}
	}
}

func TestRunAdoptBatchLogicalRootPendingMoveAfterBatchLoaderPublishesNothing(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta"}
	root := runnerTestRoot(t, resourceTypes...)
	dep := runnerTestDeployment(workspace, resourceTypes)
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"same"}]`)
	}
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		BatchStateLoader: func(requests []OracleBatchResourceRequest) (OracleBatchState, error) {
			pending, err := pendingMovesPath(dep, resourceTypes[1], "tenant")
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(pending), 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(pending, []byte("pending"), 0o600); err != nil {
				return nil, err
			}
			output := make(OracleBatchState, len(requests))
			for _, request := range requests {
				state := make(map[string]OracleStateObject)
				for key := range request.KeyToImportID {
					state[key] = OracleStateObject{Values: map[string]any{"name": key}, SensitiveValues: map[string]any{}}
				}
				output[request.ResourceType] = state
			}
			return output, nil
		}, Deployment: dep, Environment: map[string]string{"INFRAWRIGHT_ORACLE_BATCH_MODE": "logical-root"},
		InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root, Selectors: resourceTypes,
		StateLoader: func(AdoptionStateRequest) (map[string]OracleStateObject, error) {
			t.Fatal("member loader called after successful batch loader")
			return nil, nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	if !reflect.DeepEqual(result.Failed, resourceTypes) {
		t.Fatalf("pending batch result = %#v", result)
	}
	tree := snapshotRunnerTree(t, workspace)
	if len(tree) != 1 {
		t.Fatalf("pending batch output tree = %#v, want only pending marker", tree)
	}
}

func TestRunAdoptBatchExternalReferentDisablesLogicalBatch(t *testing.T) {
	workspace := t.TempDir()
	input := t.TempDir()
	resourceTypes := []string{"test_alpha", "test_beta", "test_gamma"}
	root := runnerTestRoot(t, resourceTypes...)
	root.Packs.Manifests[0].Data["references"] = metadata.JsonObject{
		"test_beta": metadata.JsonObject{
			"gamma_id": metadata.JsonObject{"referent": "test_gamma", "name_field": "name"},
		},
	}
	for _, resourceType := range resourceTypes {
		writeRunnerInput(t, input, resourceType, `[{"id":"1","name":"`+strings.TrimPrefix(resourceType, "test_")+`"}]`)
	}
	batchCalls := 0
	memberOrder := make([]string, 0, len(resourceTypes))
	result, err := RunAdoptBatch(RunAdoptBatchOptions{
		BatchStateLoader: func([]OracleBatchResourceRequest) (OracleBatchState, error) {
			batchCalls++
			return nil, errors.New("external-referent root must not batch")
		}, Deployment: runnerTestDeployment(workspace, resourceTypes[:2]),
		Environment:    map[string]string{"INFRAWRIGHT_ORACLE_BATCH_MODE": "logical-root"},
		InputDirectory: input, Policy: emptyRunnerPolicy(t), Root: root,
		Selectors: []string{"test_alpha", "test_gamma"},
		StateLoader: func(request AdoptionStateRequest) (map[string]OracleStateObject, error) {
			memberOrder = append(memberOrder, request.ResourceType)
			return stateForRunnerRequest(request), nil
		}, Tenant: "tenant",
	})
	if err != nil {
		t.Fatalf("RunAdoptBatch: %v", err)
	}
	wantOrder := []string{"test_alpha", "test_gamma", "test_beta"}
	if batchCalls != 0 || !reflect.DeepEqual(memberOrder, wantOrder) || len(result.Failed) != 0 {
		t.Fatalf("external referent calls/order/result = %d/%v/%#v, want 0/%v/success", batchCalls, memberOrder, result, wantOrder)
	}
}
