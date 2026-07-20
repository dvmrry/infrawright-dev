package adopt

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const (
	testResourceType = "test_item"
	testProvider     = "test"
	testProviderName = "registry.terraform.io/hashicorp/test"
)

type fakeOracleRunner struct {
	plan                []byte
	state               []byte
	generatedConfig     string
	failGeneratedConfig bool
	requests            []OracleCommandRequest
}

func (f *fakeOracleRunner) Run(request OracleCommandRequest) (OracleCommandResult, error) {
	f.requests = append(f.requests, snapshotOracleCommandRequest(request))
	switch request.DebugName {
	case "plan-generate-config":
		generated := strings.TrimPrefix(request.Argv[4], "-generate-config-out=")
		text := f.generatedConfig
		if text == "" {
			text = "resource \"test_item\" \"iw_a62f2225bf70bfac\" {\n  name = \"fixture\"\n}\n"
		}
		if err := os.WriteFile(generated, []byte(text), 0o600); err != nil {
			return OracleCommandResult{}, err
		}
		if f.failGeneratedConfig {
			return OracleCommandResult{}, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
				Code: "TERRAFORM_COMMAND_FAILED", Category: procerr.CategoryDomain, Message: "redacted fake plan failure",
			})
		}
	case "show-plan":
		return OracleCommandResult{Stdout: append([]byte(nil), f.plan...)}, nil
	case "show-state":
		return OracleCommandResult{Stdout: append([]byte(nil), f.state...)}, nil
	}
	return OracleCommandResult{Stdout: []byte{}}, nil
}

func snapshotOracleCommandRequest(request OracleCommandRequest) OracleCommandRequest {
	request.Argv = append([]string(nil), request.Argv...)
	request.Environment = cloneStringMap(request.Environment)
	request.SensitiveTokens = append([]string(nil), request.SensitiveTokens...)
	return request
}

func testOracleRoot(t *testing.T) *metadata.LoadedPackRoot {
	t.Helper()
	directory := t.TempDir()
	manifest := metadata.PackManifest{
		Name:             "test-pack",
		Directory:        directory,
		Data:             metadata.JsonObject{},
		ProviderPrefixes: map[string]string{"test_": testProvider},
		ProviderSources:  map[string]string{testProvider: "hashicorp/test"},
	}
	return &metadata.LoadedPackRoot{
		Packs: metadata.PackMetadata{
			Manifests:        []metadata.PackManifest{manifest},
			ProviderPrefixes: map[string]string{"test_": testProvider},
			ProviderSources:  map[string]string{testProvider: "hashicorp/test"},
			ProviderOwners:   map[string]string{testProvider: "test-pack"},
		},
		Resources: map[string]metadata.LoadedResourceMetadata{
			testResourceType: {Type: testResourceType, Provider: testProvider},
		},
	}
}

func testStateResource(address string) map[string]any {
	return map[string]any{
		"address": address, "mode": "managed", "type": testResourceType,
		"provider_name": testProviderName, "values": map[string]any{"name": "fixture", "wide": json.Number("9007199254740993")},
		"sensitive_values": map[string]any{},
	}
}

func testPlanDocument(t *testing.T, complete any, includeAcceptedState bool) []byte {
	t.Helper()
	address := OracleAddress(testResourceType, "key")
	resource := testStateResource(address)
	change := map[string]any{
		"address": address, "mode": "managed", "type": testResourceType, "provider_name": testProviderName,
		"change": map[string]any{
			"actions": []any{"no-op"}, "importing": map[string]any{"id": "secret-import-id"},
			"before": resource["values"], "after": resource["values"], "after_unknown": false,
			"before_sensitive": map[string]any{}, "after_sensitive": map[string]any{},
		},
	}
	plan := map[string]any{
		"format_version": "1.2", "terraform_version": "1.15.4", "errored": false, "applyable": true,
		"resource_changes": []any{change},
	}
	if complete != nil {
		plan["complete"] = complete
	}
	if includeAcceptedState {
		plan["planned_values"] = map[string]any{"root_module": map[string]any{"resources": []any{resource}}}
		plan["prior_state"] = map[string]any{
			"format_version": "1.2", "terraform_version": "1.15.4",
			"values": map[string]any{"root_module": map[string]any{"resources": []any{resource}}},
		}
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("json.Marshal(plan): %v", err)
	}
	return data
}

func testStateDocument(t *testing.T) []byte {
	t.Helper()
	state := map[string]any{
		"format_version": "1.2", "terraform_version": "1.15.4",
		"values": map[string]any{"root_module": map[string]any{"resources": []any{testStateResource(OracleAddress(testResourceType, "key"))}}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("json.Marshal(state): %v", err)
	}
	return data
}

func TestImportProviderStatesAppliedStateExactFakeTranscript(t *testing.T) {
	runner := &fakeOracleRunner{plan: testPlanDocument(t, true, false), state: testStateDocument(t)}
	result, err := ImportProviderStates(ImportProviderStatesOptions{
		Environment: map[string]string{"PATH": "/fixture/bin"},
		Resources:   []OracleBatchResourceRequest{{ResourceType: testResourceType, KeyToImportID: map[string]string{"key": "secret-import-id"}}},
		Root:        testOracleRoot(t), Runner: runner, TemporaryRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ImportProviderStates: %v", err)
	}
	object := result[testResourceType]["key"]
	if object.Address != OracleAddress(testResourceType, "key") {
		t.Fatalf("result address = %q, want %q", object.Address, OracleAddress(testResourceType, "key"))
	}
	if number, ok := object.Values["wide"].(json.Number); !ok || number != "9007199254740993" {
		t.Fatalf("wide value = %#v (%T), want lossless json.Number", object.Values["wide"], object.Values["wide"])
	}
	got := debugNames(runner.requests)
	want := []string{"init", "plan-generate-config", "show-plan", "apply-imports", "show-state"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command transcript = %v, want %v", got, want)
	}
	assertExactOracleArgv(t, runner.requests)
	for _, request := range runner.requests {
		if request.Environment["PATH"] != "/fixture/bin" || request.Environment["TF_DATA_DIR"] == "" {
			t.Fatalf("request environment = %#v, want explicit PATH and TF_DATA_DIR", request.Environment)
		}
		if !reflect.DeepEqual(request.SensitiveTokens, []string{"secret-import-id"}) {
			t.Fatalf("sensitive tokens = %v, want sorted import ID", request.SensitiveTokens)
		}
	}
}

func TestImportProviderStatesAcceptedPlanNeverRequestsApply(t *testing.T) {
	runner := &fakeOracleRunner{plan: testPlanDocument(t, true, true)}
	result, err := ImportProviderStates(ImportProviderStatesOptions{
		Environment: map[string]string{"INFRAWRIGHT_ORACLE_STATE_SOURCE": "accepted-plan"},
		Resources:   []OracleBatchResourceRequest{{ResourceType: testResourceType, KeyToImportID: map[string]string{"key": "secret-import-id"}}},
		Root:        testOracleRoot(t), Runner: runner, TemporaryRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ImportProviderStates accepted-plan: %v", err)
	}
	if result[testResourceType]["key"].Address == "" {
		t.Fatal("accepted plan returned no state object")
	}
	got := debugNames(runner.requests)
	want := []string{"init", "plan-generate-config", "show-plan"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("accepted-plan transcript = %v, want %v", got, want)
	}
}

func TestCompleteGateRejectsMissingAndFalseBeforeApply(t *testing.T) {
	for _, test := range []struct {
		name     string
		complete any
	}{{"missing", nil}, {"false", false}} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeOracleRunner{plan: testPlanDocument(t, test.complete, false)}
			_, err := ImportProviderStates(ImportProviderStatesOptions{
				Environment: map[string]string{},
				Resources:   []OracleBatchResourceRequest{{ResourceType: testResourceType, KeyToImportID: map[string]string{"key": "secret-import-id"}}},
				Root:        testOracleRoot(t), Runner: runner, TemporaryRoot: t.TempDir(),
			})
			if err == nil || !strings.Contains(err.Error(), "incomplete") {
				t.Fatalf("ImportProviderStates error = %v, want incomplete-plan refusal", err)
			}
			for _, request := range runner.requests {
				if request.DebugName == "apply-imports" || request.DebugName == "show-state" {
					t.Fatalf("complete=%v reached forbidden phase %s", test.complete, request.DebugName)
				}
			}
		})
	}
}

func TestImportOnlyPlanRefusalMatrix(t *testing.T) {
	address := OracleAddress(testResourceType, "key")
	expected := map[string]string{address: "secret-import-id"}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"create", actionMutation([]any{"create"})},
		{"update", actionMutation([]any{"update"})},
		{"replace", actionMutation([]any{"delete", "create"})},
		{"destroy", actionMutation([]any{"delete"})},
		{"drift", func(plan map[string]any) { plan["resource_drift"] = []any{map[string]any{"address": address}} }},
		{"deferred", func(plan map[string]any) { plan["deferred_changes"] = []any{map[string]any{"reason": "unknown"}} }},
		{"diagnostic", func(plan map[string]any) { plan["diagnostics"] = []any{map[string]any{"detail": "secret"}} }},
		{"output", func(plan map[string]any) { plan["output_changes"] = map[string]any{"unsafe": map[string]any{}} }},
		{"coverage", func(plan map[string]any) { plan["resource_changes"] = []any{} }},
		{"wrong provider", func(plan map[string]any) {
			firstPlanChange(plan)["provider_name"] = "registry.terraform.io/evil/provider"
		}},
		{"wrong import id", func(plan map[string]any) {
			firstPlanChangeDetails(plan)["importing"] = map[string]any{"id": "wrong-secret"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := decodeTestPlan(t, testPlanDocument(t, true, false))
			test.mutate(plan)
			data := encodeTestPlan(t, plan)
			err := AssertImportOnlyPlan(data, expected, testProviderName, testResourceType)
			if err == nil || !strings.Contains(err.Error(), oraclePlanDebugHint) {
				t.Fatalf("AssertImportOnlyPlan error = %v, want refusal with KEEP hint", err)
			}
			if strings.Contains(err.Error(), "wrong-secret") || strings.Contains(err.Error(), "evil/provider") {
				t.Fatalf("refusal leaked unsafe detail: %q", err.Error())
			}
		})
	}
}

func actionMutation(actions []any) func(map[string]any) {
	return func(plan map[string]any) { firstPlanChangeDetails(plan)["actions"] = actions }
}

func decodeTestPlan(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var plan map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&plan); err != nil {
		t.Fatalf("decode test plan: %v", err)
	}
	return plan
}

func encodeTestPlan(t *testing.T, plan map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("encode test plan: %v", err)
	}
	return data
}

func firstPlanChange(plan map[string]any) map[string]any {
	return plan["resource_changes"].([]any)[0].(map[string]any)
}

func firstPlanChangeDetails(plan map[string]any) map[string]any {
	return firstPlanChange(plan)["change"].(map[string]any)
}

func TestImportOnlyPlanRequiresExactMultiResourceCoverage(t *testing.T) {
	plan := decodeTestPlan(t, testPlanDocument(t, true, false))
	first := firstPlanChange(plan)
	encoded, _ := json.Marshal(first)
	var second map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&second); err != nil {
		t.Fatalf("clone second change: %v", err)
	}
	secondAddress := testResourceType + ".iw_second"
	second["address"] = secondAddress
	second["name"] = "iw_second"
	second["change"].(map[string]any)["importing"] = map[string]any{"id": "second-id"}
	plan["resource_changes"] = append(plan["resource_changes"].([]any), second)
	expected := map[string]string{
		OracleAddress(testResourceType, "key"): "secret-import-id",
		secondAddress:                          "second-id",
	}
	if err := AssertImportOnlyPlan(encodeTestPlan(t, plan), expected, testProviderName, testResourceType); err != nil {
		t.Fatalf("two-resource AssertImportOnlyPlan: %v", err)
	}
	delete(expected, secondAddress)
	if err := AssertImportOnlyPlan(encodeTestPlan(t, plan), expected, testProviderName, testResourceType); err == nil {
		t.Fatal("two-resource plan passed with incomplete expected coverage")
	}

	firstState := testStateResource(OracleAddress(testResourceType, "key"))
	secondState := testStateResource(secondAddress)
	state := map[string]any{
		"format_version": "1.2", "terraform_version": "1.15.4",
		"values": map[string]any{"root_module": map[string]any{"resources": []any{firstState, secondState}}},
	}
	expectedState := map[string]expectedOracleInstance{
		OracleAddress(testResourceType, "key"): {Key: "key", ProviderName: testProviderName, ResourceType: testResourceType},
		secondAddress:                          {Key: "second", ProviderName: testProviderName, ResourceType: testResourceType},
	}
	stateObjects, err := exactBatchStateObjects(state, expectedState)
	if err != nil {
		t.Fatalf("two-resource exactBatchStateObjects: %v", err)
	}
	if len(stateObjects[testResourceType]) != 2 {
		t.Fatalf("two-resource state coverage = %d, want 2", len(stateObjects[testResourceType]))
	}
	delete(expectedState, secondAddress)
	if _, err := exactBatchStateObjects(state, expectedState); err == nil {
		t.Fatal("two-resource state passed with incomplete expected coverage")
	}
}

func TestGeneratedConfigFailureRetriesExactlyOneCorrectedPlan(t *testing.T) {
	policy := testPolicy(t, "projection_omit", policyEntry("description", nil))
	runner := &fakeOracleRunner{
		plan: testPlanDocument(t, true, true), failGeneratedConfig: true,
		generatedConfig: "resource \"test_item\" \"iw_a62f2225bf70bfac\" {\n  name = \"fixture\"\n  description = \"drop\"\n}\n",
	}
	_, err := ImportProviderStates(ImportProviderStatesOptions{
		Environment: map[string]string{"INFRAWRIGHT_ORACLE_STATE_SOURCE": "accepted-plan"},
		Resources:   []OracleBatchResourceRequest{{ResourceType: testResourceType, KeyToImportID: map[string]string{"key": "secret-import-id"}, Policy: policy}},
		Root:        generatedPolicyRoot(t, nil), Runner: runner, TemporaryRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("ImportProviderStates corrected plan: %v", err)
	}
	want := []string{"init", "plan-generate-config", "plan-imports", "show-plan"}
	if got := debugNames(runner.requests); !reflect.DeepEqual(got, want) {
		t.Fatalf("corrected-plan transcript = %v, want %v", got, want)
	}
}

func TestOracleScratchCleanupAndKeepWarning(t *testing.T) {
	run := func(t *testing.T, keep bool) (string, string) {
		t.Helper()
		temporaryRoot := t.TempDir()
		var diagnostic string
		runner := &fakeOracleRunner{plan: testPlanDocument(t, true, true)}
		_, err := ImportProviderStates(ImportProviderStatesOptions{
			Environment: map[string]string{"INFRAWRIGHT_ORACLE_STATE_SOURCE": "accepted-plan"}, KeepWorkdir: keep,
			OnDiagnostic: func(message string) { diagnostic = message },
			Resources:    []OracleBatchResourceRequest{{ResourceType: testResourceType, KeyToImportID: map[string]string{"key": "secret-import-id"}}},
			Root:         testOracleRoot(t), Runner: runner, TemporaryRoot: temporaryRoot,
		})
		if err != nil {
			t.Fatalf("ImportProviderStates keep=%v: %v", keep, err)
		}
		entries, err := os.ReadDir(temporaryRoot)
		if err != nil {
			t.Fatalf("read temporary root: %v", err)
		}
		path := ""
		if len(entries) > 0 {
			path = filepath.Join(temporaryRoot, entries[0].Name())
		}
		return diagnostic, path
	}
	if diagnostic, path := run(t, false); diagnostic != "" || path != "" {
		t.Fatalf("ordinary cleanup diagnostic=%q path=%q, want neither", diagnostic, path)
	}
	diagnostic, path := run(t, true)
	if path == "" || !strings.Contains(diagnostic, "WARNING: kept oracle workdir "+path) || !strings.Contains(diagnostic, "unencrypted provider state") {
		t.Fatalf("keep diagnostic=%q path=%q", diagnostic, path)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
}

func TestAcceptedPlanEvidenceFailuresMatchNodeMessages(t *testing.T) {
	address := OracleAddress(testResourceType, "key")
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "incomplete mask",
			mutate: func(plan map[string]any) {
				change := plan["resource_changes"].([]any)[0].(map[string]any)["change"].(map[string]any)
				delete(change, "before_sensitive")
			},
			want: "test_item accepted import plan did not contain exact provider-observed evidence",
		},
		{
			name: "inconsistent observations",
			mutate: func(plan map[string]any) {
				change := plan["resource_changes"].([]any)[0].(map[string]any)["change"].(map[string]any)
				change["before"] = map[string]any{"name": "different", "wide": json.Number("9007199254740993")}
			},
			want: "test_item accepted import plan provider observations were inconsistent",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var plan map[string]any
			if err := json.Unmarshal(testPlanDocument(t, true, true), &plan); err != nil {
				t.Fatalf("json.Unmarshal(plan): %v", err)
			}
			test.mutate(plan)
			data, err := json.Marshal(plan)
			if err != nil {
				t.Fatalf("json.Marshal(mutated plan): %v", err)
			}
			_, err = ExtractAcceptedPlanState(data, map[string]string{address: "key"}, map[string]string{address: "secret-import-id"}, testProviderName, testResourceType)
			if err == nil || err.Error() != test.want {
				t.Fatalf("ExtractAcceptedPlanState error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestEmptyBatchDoesNotReadOracleEnvironmentOrInvokeRunner(t *testing.T) {
	runner := &fakeOracleRunner{}
	result, err := ImportProviderStates(ImportProviderStatesOptions{
		Environment: map[string]string{"INFRAWRIGHT_ORACLE_STATE_SOURCE": "invalid"},
		Resources:   []OracleBatchResourceRequest{{ResourceType: testResourceType, KeyToImportID: map[string]string{}}},
		Runner:      runner,
	})
	if err != nil {
		t.Fatalf("empty ImportProviderStates: %v", err)
	}
	if len(result[testResourceType]) != 0 || len(runner.requests) != 0 {
		t.Fatalf("empty transaction result=%#v requests=%v", result, runner.requests)
	}
}

func TestOracleTimeoutAndStateSourceVectors(t *testing.T) {
	for raw, want := range map[string]int{"": 300000, "0.25": 250, "601": 601000, "86400": 86400000, "0x10": 16000} {
		got, err := OracleTimeoutMS(map[string]string{"INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS": raw})
		if err != nil || got != want {
			t.Errorf("OracleTimeoutMS(%q) = (%d, %v), want (%d, nil)", raw, got, err, want)
		}
	}
	for _, raw := range []string{"0", "-1", "NaN", "infinity"} {
		if _, err := OracleTimeoutMS(map[string]string{"INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS": raw}); err == nil {
			t.Errorf("OracleTimeoutMS(%q) returned nil error", raw)
		}
	}
	if _, err := OracleTimeoutMS(map[string]string{"INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS": "1e100"}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("OracleTimeoutMS(1e100) error = %v, want range error", err)
	}
	if _, err := OracleStateSourceFromEnvironment(map[string]string{"INFRAWRIGHT_ORACLE_STATE_SOURCE": " ACCEPTED-plan "}); err == nil {
		t.Error("OracleStateSourceFromEnvironment accepted case-changed value")
	}
}

func TestOracleRenderingAndFamilyAreDeterministic(t *testing.T) {
	imports, err := RenderOracleImports(testResourceType, map[string]string{"b": `${id}`, "a": "a\nvalue"})
	if err != nil {
		t.Fatalf("RenderOracleImports: %v", err)
	}
	wantFirst := OracleAddress(testResourceType, "a")
	if strings.Index(imports, wantFirst) > strings.Index(imports, OracleAddress(testResourceType, "b")) || !strings.Contains(imports, `$${id}`) {
		t.Fatalf("RenderOracleImports output = %q", imports)
	}
	if got := OracleBatchResourceFamily(nil); got != "oracle_batch." {
		t.Errorf("OracleBatchResourceFamily(nil) = %q, want oracle_batch.", got)
	}
	if got := OracleBatchResourceFamily([]string{"b", "a", "b"}); got != "oracle_batch.a.b" {
		t.Errorf("OracleBatchResourceFamily = %q, want oracle_batch.a.b", got)
	}
}

func TestOracleCommandFailureIsFailClosedAndRedacted(t *testing.T) {
	failure := oracleCommandFailure("plan-imports", errors.New("provider says secret-import-id"))
	var processFailure *procerr.ProcessFailure
	if !errors.As(failure, &processFailure) || processFailure.Code != "TERRAFORM_COMMAND_FAILED" {
		t.Fatalf("oracleCommandFailure = %#v, want ProcessFailure", failure)
	}
	if strings.Contains(failure.Error(), "secret-import-id") || failure.Error() != "terraform plan-imports failed; provider diagnostics and import IDs were redacted" {
		t.Fatalf("oracleCommandFailure message = %q", failure.Error())
	}
}

func debugNames(requests []OracleCommandRequest) []string {
	output := make([]string, len(requests))
	for index, request := range requests {
		output[index] = request.DebugName
	}
	return output
}

func assertExactOracleArgv(t *testing.T, requests []OracleCommandRequest) {
	t.Helper()
	if len(requests) != 5 {
		t.Fatalf("request count = %d, want 5", len(requests))
	}
	temporary := requests[0].CWD
	want := [][]string{
		{"init", "-input=false", "-no-color"},
		{"plan", "-input=false", "-no-color", "-lock=false", "-generate-config-out=" + filepath.Join(temporary, "generated.tf"), "-out=" + filepath.Join(temporary, "oracle.tfplan")},
		{"show", "-json", filepath.Join(temporary, "oracle.tfplan")},
		{"apply", "-input=false", "-no-color", "-lock=false", filepath.Join(temporary, "oracle.tfplan")},
		{"show", "-json", "terraform.tfstate"},
	}
	for index := range want {
		if !reflect.DeepEqual(requests[index].Argv, want[index]) {
			t.Errorf("request[%d].Argv = %v, want %v", index, requests[index].Argv, want[index])
		}
	}
}
