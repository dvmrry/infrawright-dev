package assessment

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

const exactApplyTenant = "tenant"

type exactApplyFixture struct {
	workspace  string
	scratch    string
	deployment deployment.Deployment
	inputs     ExactPlanApplyInputs
	roots      []SavedPlanAssessmentRootInput
}

func newExactApplyFixture(t *testing.T, resourceTypes ...string) exactApplyFixture {
	t.Helper()
	if len(resourceTypes) == 0 {
		resourceTypes = []string{"zia_url_categories"}
	}
	workspace := t.TempDir()
	scratch := filepath.Join(workspace, "scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error: %v", scratch, err)
	}
	root := loadedAssessmentPack(t)
	dep := deployment.Deployment{
		Overlay: workspace,
		Roots: map[string]deployment.RootProviderConfig{
			// Keep the generic exact-Apply safety fixture independent of the
			// singleton topology default. The dedicated reference-output test
			// below exercises omitted/default cross-state behavior for ZPA.
			"zia": {
				HasCrossStateReferences: true,
				CrossStateReferences:    false,
			},
		},
	}
	for _, resourceType := range resourceTypes {
		envDir := filepath.Join(workspace, "envs", exactApplyTenant, resourceType)
		moduleDir := filepath.Join(workspace, "modules", resourceType)
		configDir := filepath.Join(workspace, "config", exactApplyTenant)
		for _, directory := range []string{envDir, moduleDir, configDir} {
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatalf("os.MkdirAll(%q) error: %v", directory, err)
			}
		}
		relativeModule, err := filepath.Rel(envDir, moduleDir)
		if err != nil {
			t.Fatalf("filepath.Rel(%q, %q) error: %v", envDir, moduleDir, err)
		}
		writeAssessmentTransactionFile(t, filepath.Join(moduleDir, "main.tf"), []byte("# module\n"), 0o600)
		writeAssessmentTransactionFile(t, filepath.Join(envDir, "main.tf"), []byte(strings.Join([]string{
			fmt.Sprintf("module %q {", resourceType),
			fmt.Sprintf("  source = %q", filepath.ToSlash(relativeModule)),
			fmt.Sprintf("  items = var.%s_items", resourceType),
			"}",
			"",
		}, "\n")), 0o600)
		writeAssessmentTransactionFile(
			t,
			filepath.Join(configDir, resourceType+".auto.tfvars.json"),
			[]byte(fmt.Sprintf("{%q:{}}\n", resourceType+"_items")),
			0o600,
		)
		writeAssessmentTransactionFile(t, filepath.Join(envDir, "tfplan"), []byte("opaque saved plan\n"), 0o600)
	}
	tenant := exactApplyTenant
	context := CopyLoadedSavedPlanAssessmentContext(LoadedSavedPlanAssessmentContext{
		Workspace:  workspace,
		Deployment: dep,
		Root:       root,
		Tenant:     &tenant,
		Selectors:  append([]string(nil), resourceTypes...),
	})
	roots, _, err := MaterializeLoadedSavedPlanAssessmentRoots(context)
	if err != nil {
		t.Fatalf("MaterializeLoadedSavedPlanAssessmentRoots() error: %v", err)
	}
	if len(roots) != len(resourceTypes) {
		t.Fatalf("materialized roots = %d, want %d", len(roots), len(resourceTypes))
	}
	fixture := exactApplyFixture{
		workspace:  workspace,
		scratch:    scratch,
		deployment: dep,
		inputs: ExactPlanApplyInputs{
			Deployment: dep,
			Root:       root,
		},
		roots: roots,
	}
	for _, rootInput := range roots {
		fixture.restoreSavedPair(t, rootInput)
	}
	return fixture
}

func (fixture exactApplyFixture) restoreSavedPair(t *testing.T, root SavedPlanAssessmentRootInput) {
	t.Helper()
	writeAssessmentTransactionFile(t, root.SavedPlanPath, []byte("opaque saved plan\n"), 0o600)
	backendKey := (*string)(nil)
	fingerprint, err := plan.FingerprintPlanV2(plan.PlanFingerprintInput{
		EnvDir:        root.EnvDir,
		VarFiles:      append([]string(nil), root.VarFiles...),
		MemberTypes:   append([]string(nil), root.Members...),
		BackendConfig: nil,
		BackendKey:    backendKey,
	}, nil)
	if err != nil {
		t.Fatalf("plan.FingerprintPlanV2(%q) error: %v", root.Label, err)
	}
	writeAssessmentTransactionFile(
		t,
		root.FingerprintPath,
		[]byte(fmt.Sprintf("{\"version\":2,\"sha256\":%q}\n", fingerprint.SHA256)),
		0o600,
	)
}

type fakeExactPlanApplyTerraform struct {
	initialized  []plan.PlanTerraformRequest
	shown        []ExactPlanApplyShowRequest
	applied      []ExactPlanApplyRequest
	currentPlan  canonjson.Value
	typedPlan    *ExactPlanApplyShownPlan
	onInitialize func(plan.PlanTerraformRequest) error
	onShow       func(ExactPlanApplyShowRequest) (canonjson.Value, error)
	onApply      func(ExactPlanApplyRequest) error
}

func (fake *fakeExactPlanApplyTerraform) Initialize(request plan.PlanTerraformRequest) error {
	fake.initialized = append(fake.initialized, request)
	if fake.onInitialize != nil {
		return fake.onInitialize(request)
	}
	return nil
}

func (fake *fakeExactPlanApplyTerraform) Show(request ExactPlanApplyShowRequest) (ExactPlanApplyShownPlan, error) {
	fake.shown = append(fake.shown, request)
	if request.SnapshotFile == nil {
		return ExactPlanApplyShownPlan{}, errors.New("shown snapshot is not a regular file")
	}
	if info, err := request.SnapshotFile.Stat(); err != nil || !info.Mode().IsRegular() {
		return ExactPlanApplyShownPlan{}, errors.New("shown snapshot is not a regular file")
	}
	var raw canonjson.Value
	var err error
	if fake.onShow != nil {
		raw, err = fake.onShow(request)
	} else {
		raw = fake.currentPlan
	}
	if err != nil {
		return ExactPlanApplyShownPlan{}, err
	}
	if fake.typedPlan != nil {
		shown := *fake.typedPlan
		if shown.Raw == nil {
			shown.Raw = raw
		}
		return shown, nil
	}
	typed, err := decodeExactApplyTypedPlan(raw)
	if err != nil {
		return ExactPlanApplyShownPlan{}, err
	}
	return ExactPlanApplyShownPlan{Typed: typed, Raw: raw}, nil
}

func (fake *fakeExactPlanApplyTerraform) Apply(request ExactPlanApplyRequest) error {
	fake.applied = append(fake.applied, request)
	if fake.onApply != nil {
		return fake.onApply(request)
	}
	return nil
}

func exactApplyCleanPlan() map[string]any {
	return map[string]any{
		"format_version":    "1.2",
		"terraform_version": "1.15.4",
		"complete":          true,
		"errored":           false,
		"resource_changes":  []any{},
		"output_changes":    map[string]any{},
	}
}

func exactApplyReferenceOutputPlan() map[string]any {
	value := map[string]any{
		"zpa_segment_group": map[string]any{"segment_one": "72059380790653545"},
	}
	return map[string]any{
		"format_version":    "1.2",
		"terraform_version": "1.15.4",
		"complete":          true,
		"errored":           false,
		"planned_values": map[string]any{
			"outputs": map[string]any{
				"infrawright_reference_ids": map[string]any{
					"sensitive": true,
					"value":     value,
				},
			},
			"root_module": map[string]any{
				"child_modules": []any{
					map[string]any{
						"address": "module.zpa_segment_group",
						"resources": []any{
							map[string]any{
								"address": `module.zpa_segment_group.zpa_segment_group.this["segment_one"]`,
								"index":   "segment_one",
								"mode":    "managed",
								"type":    "zpa_segment_group",
								"values": map[string]any{
									"id":   "72059380790653545",
									"name": "Segment One",
								},
							},
						},
					},
				},
			},
		},
		"resource_changes": []any{},
		"output_changes": map[string]any{
			"infrawright_reference_ids": map[string]any{
				"actions":          []any{"create"},
				"before":           nil,
				"after":            value,
				"before_sensitive": false,
				"after_sensitive":  true,
				"after_unknown":    false,
			},
		},
	}
}

func exactApplyBlockedPlan(actions ...string) map[string]any {
	rawActions := make([]any, len(actions))
	for index, action := range actions {
		rawActions[index] = action
	}
	return map[string]any{
		"format_version":    "1.2",
		"terraform_version": "1.15.4",
		"complete":          true,
		"errored":           false,
		"resource_changes": []any{map[string]any{
			"address": `zia_url_categories.this["one"]`,
			"type":    "zia_url_categories",
			"change": map[string]any{
				"actions": rawActions,
				"before":  map[string]any{"status": "old"},
				"after":   map[string]any{"status": "new"},
			},
		}},
		"output_changes": map[string]any{},
	}
}

func exactApplyOptions(fixture exactApplyFixture, terraform ExactPlanApplyTerraform) ExactPlanApplyOptions {
	tenant := exactApplyTenant
	return ExactPlanApplyOptions{
		Workspace:     fixture.workspace,
		Tenant:        &tenant,
		Selectors:     append([]string(nil), fixture.roots[0].Members...),
		CurrentBranch: func() string { return "main" },
		LoadInputs: func() (ExactPlanApplyInputs, error) {
			return fixture.inputs, nil
		},
		Terraform: terraform,
	}
}

func exactApplyTestHooks(fixture exactApplyFixture) exactPlanApplyHooks {
	return exactPlanApplyHooks{makeTemporary: func() (string, error) {
		return makeAssessmentTemporaryDirectory(fixture.scratch)
	}}
}

func requireExactApplyFailure(t *testing.T, err error, code string) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T(%v), want *procerr.ProcessFailure %q", err, err, code)
	}
	if failure.Code != code {
		t.Fatalf("failure.Code = %q, want %q", failure.Code, code)
	}
	return failure
}

func TestCurrentApplyBranchPreservesPriorityAndFallback(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		git         func(string) string
		want        string
	}{
		{name: "ado", environment: map[string]string{"BUILD_SOURCEBRANCH": "refs/heads/ado", "GITHUB_REF": "refs/heads/github", "BITBUCKET_BRANCH": "bitbucket"}, want: "ado"},
		{name: "github", environment: map[string]string{"GITHUB_REF": "refs/heads/github", "BITBUCKET_BRANCH": "bitbucket"}, want: "github"},
		{name: "bitbucket", environment: map[string]string{"BITBUCKET_BRANCH": "bitbucket"}, want: "bitbucket"},
		{name: "pull_ref_unchanged", environment: map[string]string{"GITHUB_REF": "refs/pull/207/merge"}, want: "refs/pull/207/merge"},
		{name: "last_heads_segment", environment: map[string]string{"GITHUB_REF": "prefix/refs/heads/topic/refs/heads/final"}, want: "final"},
		{name: "git", environment: map[string]string{}, git: func(string) string { return "git-branch" }, want: "git-branch"},
		{name: "git_panic", environment: map[string]string{}, git: func(string) string { panic("boom") }, want: "unknown"},
		{name: "nil_environment", environment: nil, git: func(string) string { return "main" }, want: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := CurrentApplyBranch(CurrentApplyBranchOptions{
				CWD: t.TempDir(), Environment: test.environment, GitBranch: test.git,
			})
			if got != test.want {
				t.Errorf("CurrentApplyBranch() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGitApplyBranchRejectsOversizedStreams(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "stdout", body: "printf '%131073s' x"},
		{name: "stderr", body: "printf '%131073s' x >&2\nprintf 'main\\n'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			executable := assessmentExecutable(t, root, test.body)
			if got := runGitApplyBranch(root, executable); got != "unknown" {
				t.Fatalf("runGitApplyBranch(oversized %s) = %q, want unknown", test.name, got)
			}
		})
	}
}

func TestGitApplyBranchTerminatesNonExitingOverflow(t *testing.T) {
	root := t.TempDir()
	executable := assessmentExecutable(t, root, "while :; do printf '%1024s' x >&2; done")
	completed := make(chan string, 1)
	go func() {
		completed <- runGitApplyBranch(root, executable)
	}()
	select {
	case got := <-completed:
		if got != "unknown" {
			t.Fatalf("runGitApplyBranch(non-exiting overflow) = %q, want unknown", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runGitApplyBranch(non-exiting overflow) did not terminate promptly")
	}
}

func TestGitApplyBranchBoundsDescendantHeldPipesAfterOverflow(t *testing.T) {
	root := t.TempDir()
	executable := assessmentExecutable(t, root, strings.Join([]string{
		"(/bin/sleep 3) &",
		"printf '%131073s' x >&2",
		"wait",
	}, "\n"))
	started := time.Now()
	if got := runGitApplyBranch(root, executable); got != "unknown" {
		t.Fatalf("runGitApplyBranch(descendant-held pipes) = %q, want unknown", got)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("runGitApplyBranch(descendant-held pipes) took %s, want bounded post-cancel wait", elapsed)
	}
}

func TestCreateExactPlanApplyTerraformRequiresExplicitEnvironment(t *testing.T) {
	adapter, err := CreateExactPlanApplyTerraform(CreateExactPlanApplyTerraformOptions{
		TerraformExecutable: filepath.Join(t.TempDir(), "terraform"),
	})
	if err == nil || adapter != nil || !strings.Contains(err.Error(), "requires an explicit environment") {
		t.Fatalf("CreateExactPlanApplyTerraform(nil env) = %T/%v, want explicit-environment rejection", adapter, err)
	}
}

func TestApplyExactSavedPlansBranchRefusalPrecedesInputsAndTerraform(t *testing.T) {
	called := false
	options := ExactPlanApplyOptions{
		Workspace:     t.TempDir(),
		CurrentBranch: func() string { return "feature/topic" },
		LoadInputs: func() (ExactPlanApplyInputs, error) {
			called = true
			return ExactPlanApplyInputs{}, nil
		},
	}
	_, err := ApplyExactSavedPlans(options)
	failure := requireExactApplyFailure(t, err, "APPLY_BRANCH_REFUSED")
	if called {
		t.Fatal("branch refusal called LoadInputs")
	}
	if !strings.Contains(failure.Message, "'feature/topic'") {
		t.Errorf("branch failure message = %q, want Python branch repr", failure.Message)
	}
}

func TestApplyExactSavedPlansCleanFlowAndCompleteGate(t *testing.T) {
	fixture := newExactApplyFixture(t)
	unrelated := filepath.Join(fixture.roots[0].EnvDir, "generated.keep.tf")
	writeAssessmentTransactionFile(t, unrelated, []byte("# keep\n"), 0o600)
	fixture.restoreSavedPair(t, fixture.roots[0])
	fake := &fakeExactPlanApplyTerraform{currentPlan: exactApplyCleanPlan()}
	var diagnostics []string
	options := exactApplyOptions(fixture, fake)
	options.OnDiagnostic = func(message string) { diagnostics = append(diagnostics, message) }
	result, err := applyExactSavedPlans(options, exactApplyTestHooks(fixture))
	if err != nil {
		t.Fatalf("applyExactSavedPlans(clean) error: %v", err)
	}
	if result.Applied != 1 || len(fake.initialized) != 1 || len(fake.shown) != 1 || len(fake.applied) != 1 {
		t.Fatalf("clean result/calls = %#v init=%d show=%d apply=%d", result, len(fake.initialized), len(fake.shown), len(fake.applied))
	}
	request := fake.initialized[0]
	if request.Directory != fixture.roots[0].EnvDir || request.Save || len(request.VarFiles) != 0 || request.BackendConfig != nil || request.BackendKey != nil {
		t.Errorf("Initialize request = %#v, want exact no-save/no-var/no-backend request", request)
	}
	if !reflect.DeepEqual(diagnostics, []string{"== apply tenant/zia_url_categories"}) {
		t.Errorf("diagnostics = %#v", diagnostics)
	}
	for _, file := range []string{fixture.roots[0].SavedPlanPath, fixture.roots[0].FingerprintPath} {
		if _, statErr := os.Stat(file); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("os.Stat(%q) error = %v, want removed", file, statErr)
		}
	}
	if content, readErr := os.ReadFile(unrelated); readErr != nil || string(content) != "# keep\n" {
		t.Errorf("unrelated generated file = %q/%v, want preserved", content, readErr)
	}
	entries, err := os.ReadDir(fixture.scratch)
	if err != nil || len(entries) != 0 {
		t.Errorf("scratch entries/error = %v/%v, want empty", entries, err)
	}

	for _, test := range []struct {
		name  string
		value any
		omit  bool
	}{
		{name: "missing", omit: true},
		{name: "false", value: false},
		{name: "null", value: nil},
		{name: "string", value: "true"},
		{name: "number", value: 1},
	} {
		t.Run("complete_"+test.name, func(t *testing.T) {
			fixture.restoreSavedPair(t, fixture.roots[0])
			planValue := exactApplyCleanPlan()
			if test.omit {
				delete(planValue, "complete")
			} else {
				planValue["complete"] = test.value
			}
			candidate := &fakeExactPlanApplyTerraform{currentPlan: planValue}
			options := exactApplyOptions(fixture, candidate)
			options.AllowDestroy = true
			options.AllowPlanChanges = true
			_, err := applyExactSavedPlans(options, exactApplyTestHooks(fixture))
			if err == nil {
				t.Fatal("incomplete plan error = nil")
			}
			if len(candidate.applied) != 0 {
				t.Fatalf("incomplete plan Apply calls = %d, want zero", len(candidate.applied))
			}
		})
	}

	t.Run("typed_complete_false_cannot_be_bypassed", func(t *testing.T) {
		fixture.restoreSavedPair(t, fixture.roots[0])
		raw := exactApplyCleanPlan()
		typed, err := decodeExactApplyTypedPlan(raw)
		if err != nil {
			t.Fatal(err)
		}
		complete := false
		typed.Complete = &complete
		candidate := &fakeExactPlanApplyTerraform{
			currentPlan: raw,
			typedPlan:   &ExactPlanApplyShownPlan{Typed: typed, Raw: raw},
		}
		options := exactApplyOptions(fixture, candidate)
		options.AllowDestroy = true
		options.AllowPlanChanges = true
		_, err = applyExactSavedPlans(options, exactApplyTestHooks(fixture))
		if err == nil || err.Error() != "plan must be complete before assessment" {
			t.Fatalf("typed incomplete plan error = %T(%v), want plan-contract parity", err, err)
		}
		var failure *procerr.ProcessFailure
		if errors.As(err, &failure) {
			t.Fatalf("typed incomplete plan error = ProcessFailure(%s), want Node-compatible plain error", failure.Code)
		}
		if len(candidate.applied) != 0 {
			t.Fatalf("typed incomplete plan Apply calls = %d, want zero", len(candidate.applied))
		}
	})
}

func TestApplyExactSavedPlansAcceptsDefaultCrossStateReferenceOutput(t *testing.T) {
	fixture := newExactApplyFixture(t, "zpa_segment_group")
	wantOutputs := []string{"zpa_segment_group"}
	if got := fixture.roots[0].ReferenceOutputTypes; !reflect.DeepEqual(got, wantOutputs) {
		t.Fatalf(
			"MaterializeLoadedSavedPlanAssessmentRoots(default ZPA).ReferenceOutputTypes = %#v, want %#v",
			got,
			wantOutputs,
		)
	}

	planValue := exactApplyReferenceOutputPlan()
	classification, err := ClassifyPlan(planValue, nil, exactApplyContract(fixture.roots[0]))
	if err != nil {
		t.Fatalf("ClassifyPlan(default ZPA reference output) error: %v", err)
	}
	if classification.Status != Clean || len(classification.Findings) != 0 {
		t.Fatalf("ClassifyPlan(default ZPA reference output) = %#v, want clean with no findings", classification)
	}

	fake := &fakeExactPlanApplyTerraform{currentPlan: planValue}
	result, err := applyExactSavedPlans(exactApplyOptions(fixture, fake), exactApplyTestHooks(fixture))
	if err != nil {
		t.Fatalf("applyExactSavedPlans(default ZPA reference output) error: %v", err)
	}
	if got := result.Applied; got != 1 {
		t.Errorf("applyExactSavedPlans(default ZPA reference output).Applied = %d, want 1", got)
	}
	if got := len(fake.applied); got != 1 {
		t.Errorf("applyExactSavedPlans(default ZPA reference output) Apply calls = %d, want 1", got)
	} else if got, want := fake.applied[0].Directory, fixture.roots[0].EnvDir; got != want {
		t.Errorf("applyExactSavedPlans(default ZPA reference output) Apply directory = %q, want %q", got, want)
	}
}

func TestApplyExactSavedPlansUsesOneDescriptorAfterFinalRecheck(t *testing.T) {
	fixture := newExactApplyFixture(t)
	fixture.restoreSavedPair(t, fixture.roots[0])
	fake := &fakeExactPlanApplyTerraform{currentPlan: exactApplyCleanPlan()}
	fake.onApply = func(request ExactPlanApplyRequest) error {
		if len(fake.shown) != 1 || request.SnapshotFile == nil || request.SnapshotFile != fake.shown[0].SnapshotFile {
			return errors.New("Show and Apply did not share one snapshot descriptor")
		}
		root := fixture.roots[0]
		if err := os.Rename(root.SavedPlanPath, root.SavedPlanPath+".rebound"); err != nil {
			return err
		}
		if err := os.WriteFile(root.SavedPlanPath, []byte("public replacement"), 0o600); err != nil {
			return err
		}
		snapshotPath := request.SnapshotFile.Name()
		if err := os.Rename(snapshotPath, snapshotPath+".rebound"); err != nil {
			return err
		}
		if err := os.WriteFile(snapshotPath, []byte("private replacement"), 0o600); err != nil {
			return err
		}
		if _, err := request.SnapshotFile.Seek(0, io.SeekStart); err != nil {
			return err
		}
		bytes, err := io.ReadAll(request.SnapshotFile)
		if err != nil {
			return err
		}
		if string(bytes) != "opaque saved plan\n" {
			return errors.New("Apply descriptor did not retain assessed bytes")
		}
		return nil
	}
	result, err := applyExactSavedPlans(exactApplyOptions(fixture, fake), exactApplyTestHooks(fixture))
	if result.Applied != 1 || err == nil {
		t.Fatalf("applyExactSavedPlans(rebound snapshot) = %#v, %v, want committed apply plus cleanup refusal", result, err)
	}
	if len(fake.applied) != 1 {
		t.Fatalf("Apply calls = %d, want 1", len(fake.applied))
	}
	if _, err := fake.applied[0].SnapshotFile.Stat(); err == nil {
		t.Error("Apply snapshot descriptor remained open after exact Apply")
	}
}

func TestApplyExactSavedPlansRecordsCommittedApplyWhenDescriptorCloseFails(t *testing.T) {
	fixture := newExactApplyFixture(t)
	fixture.restoreSavedPair(t, fixture.roots[0])
	fake := &fakeExactPlanApplyTerraform{currentPlan: exactApplyCleanPlan()}
	fake.onApply = func(request ExactPlanApplyRequest) error {
		if request.SnapshotFile == nil {
			return errors.New("missing snapshot descriptor")
		}
		return request.SnapshotFile.Close()
	}
	result, err := applyExactSavedPlans(exactApplyOptions(fixture, fake), exactApplyTestHooks(fixture))
	if result.Applied != 1 {
		t.Errorf("applyExactSavedPlans(close failure).Applied = %d, want 1", result.Applied)
	}
	requireExactApplyFailure(t, err, "PLAN_SNAPSHOT_CHANGED")
}

func TestApplyExactSavedPlansDestroyAndBlockedOverrideMatrix(t *testing.T) {
	fixture := newExactApplyFixture(t)
	tests := []struct {
		name             string
		allowDestroy     bool
		allowPlanChanges bool
		wantCode         string
		wantApply        bool
	}{
		{name: "neither", wantCode: "APPLY_DESTROY_REFUSED"},
		{name: "destroy_only", allowDestroy: true, wantCode: "APPLY_BLOCKED_PLAN_REFUSED"},
		{name: "plan_only", allowPlanChanges: true, wantCode: "APPLY_DESTROY_REFUSED"},
		{name: "both", allowDestroy: true, allowPlanChanges: true, wantApply: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture.restoreSavedPair(t, fixture.roots[0])
			fake := &fakeExactPlanApplyTerraform{currentPlan: exactApplyBlockedPlan("delete")}
			options := exactApplyOptions(fixture, fake)
			options.AllowDestroy = test.allowDestroy
			options.AllowPlanChanges = test.allowPlanChanges
			_, err := applyExactSavedPlans(options, exactApplyTestHooks(fixture))
			if test.wantCode != "" {
				requireExactApplyFailure(t, err, test.wantCode)
			} else if err != nil {
				t.Fatalf("override error: %v", err)
			}
			if got := len(fake.applied) > 0; got != test.wantApply {
				t.Errorf("Apply called = %t, want %t", got, test.wantApply)
			}
		})
	}
}

func TestApplyExactSavedPlansRejectsEveryFreshnessClassBeforeApply(t *testing.T) {
	tests := []struct {
		name     string
		wantCode string
		setup    func(*testing.T, exactApplyFixture, *fakeExactPlanApplyTerraform, *ExactPlanApplyOptions)
	}{
		{
			name:     "control",
			wantCode: "ASSESSMENT_CONTROL_CHANGED",
			setup: func(t *testing.T, fixture exactApplyFixture, fake *fakeExactPlanApplyTerraform, options *ExactPlanApplyOptions) {
				controlPath := filepath.Join(fixture.workspace, "control.json")
				writeAssessmentTransactionFile(t, controlPath, []byte("before\n"), 0o600)
				bound, err := controlevidence.BindRequiredAssessmentControlText(controlPath, controlevidence.BindOptions{})
				if err != nil {
					t.Fatal(err)
				}
				options.LoadInputs = func() (ExactPlanApplyInputs, error) {
					inputs := fixture.inputs
					inputs.ControlFiles = []controlevidence.BoundAssessmentControlFile{bound.File}
					return inputs, nil
				}
				fake.onInitialize = func(plan.PlanTerraformRequest) error {
					return os.WriteFile(controlPath, []byte("after\n"), 0o600)
				}
			},
		},
		{
			name:     "context",
			wantCode: "ASSESSMENT_CONTEXT_CHANGED",
			setup: func(_ *testing.T, fixture exactApplyFixture, fake *fakeExactPlanApplyTerraform, _ *ExactPlanApplyOptions) {
				fake.onInitialize = func(plan.PlanTerraformRequest) error {
					return os.Remove(fixture.roots[0].SavedPlanPath)
				}
			},
		},
		{
			name:     "saved_plan",
			wantCode: "SAVED_PLAN_CHANGED",
			setup: func(_ *testing.T, fixture exactApplyFixture, fake *fakeExactPlanApplyTerraform, _ *ExactPlanApplyOptions) {
				fake.onShow = func(ExactPlanApplyShowRequest) (canonjson.Value, error) {
					return exactApplyCleanPlan(), os.WriteFile(fixture.roots[0].SavedPlanPath, []byte("changed\n"), 0o600)
				}
			},
		},
		{
			name:     "policy",
			wantCode: "DRIFT_POLICY_CHANGED",
			setup: func(t *testing.T, fixture exactApplyFixture, fake *fakeExactPlanApplyTerraform, options *ExactPlanApplyOptions) {
				policyPath := filepath.Join(fixture.workspace, "policy.json")
				writeAssessmentTransactionFile(t, policyPath, []byte(`{"version":1,"resource_types":{}}`), 0o600)
				options.PolicyPath = &policyPath
				fake.onShow = func(ExactPlanApplyShowRequest) (canonjson.Value, error) {
					return exactApplyCleanPlan(), os.WriteFile(policyPath, []byte(`{"version":1,"resource_types":{"changed":{}}}`), 0o600)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExactApplyFixture(t)
			fake := &fakeExactPlanApplyTerraform{currentPlan: exactApplyCleanPlan()}
			options := exactApplyOptions(fixture, fake)
			test.setup(t, fixture, fake, &options)
			_, err := applyExactSavedPlans(options, exactApplyTestHooks(fixture))
			requireExactApplyFailure(t, err, test.wantCode)
			if len(fake.applied) != 0 {
				t.Fatalf("freshness mutation Apply calls = %d, want zero", len(fake.applied))
			}
		})
	}
}

func TestApplyExactSavedPlansMultiRootFailurePreservesFailedAndLaterPairs(t *testing.T) {
	fixture := newExactApplyFixture(t, "zia_url_categories", "zia_admin_users", "zia_workload_groups")
	fake := &fakeExactPlanApplyTerraform{currentPlan: exactApplyCleanPlan()}
	fake.onApply = func(request ExactPlanApplyRequest) error {
		if request.Directory == fixture.roots[1].EnvDir {
			return errors.New("injected second-root failure")
		}
		return nil
	}
	options := exactApplyOptions(fixture, fake)
	options.Selectors = []string{"zia_url_categories", "zia_admin_users", "zia_workload_groups"}
	result, err := applyExactSavedPlans(options, exactApplyTestHooks(fixture))
	if err == nil || result.Applied != 1 {
		t.Fatalf("multi-root result/error = %#v/%v, want one completed then failure", result, err)
	}
	if len(fake.applied) != 2 || fake.applied[0].Directory != fixture.roots[0].EnvDir || fake.applied[1].Directory != fixture.roots[1].EnvDir {
		t.Fatalf("multi-root Apply order = %#v, want materialized root order", fake.applied)
	}
	for _, file := range []string{fixture.roots[0].SavedPlanPath, fixture.roots[0].FingerprintPath} {
		if _, statErr := os.Stat(file); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("successful root file %q remains: %v", file, statErr)
		}
	}
	for _, root := range fixture.roots[1:] {
		for _, file := range []string{root.SavedPlanPath, root.FingerprintPath} {
			if _, statErr := os.Stat(file); statErr != nil {
				t.Errorf("failed/later root file %q missing: %v", file, statErr)
			}
		}
	}
}

func TestApplyExactSavedPlansCleanupFailureHasNoRunCeiling(t *testing.T) {
	fixture := newExactApplyFixture(t)
	removeAttempts := 0
	hooks := exactApplyTestHooks(fixture)
	hooks.cleanupHooks.removeSnapshot = func(root *os.Root, name string) error {
		removeAttempts++
		if removeAttempts <= 40 {
			return errors.New("injected descriptor removal failure")
		}
		return root.Remove(name)
	}
	for run := 1; run <= 41; run++ {
		fixture.restoreSavedPair(t, fixture.roots[0])
		fake := &fakeExactPlanApplyTerraform{currentPlan: exactApplyCleanPlan()}
		result, err := applyExactSavedPlans(exactApplyOptions(fixture, fake), hooks)
		if err != nil || result.Applied != 1 {
			t.Fatalf("run %d result/error = %#v/%v, want success beyond fixed-slot ceiling", run, result, err)
		}
	}
	entries, err := os.ReadDir(fixture.scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 40 {
		t.Fatalf("scrubbed retained remnants = %d, want 40", len(entries))
	}
	for _, entry := range entries {
		info, statErr := entry.Info()
		if statErr != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			t.Errorf("remnant %q info/error = %#v/%v, want private directory", entry.Name(), info, statErr)
		}
		files, readErr := os.ReadDir(filepath.Join(fixture.scratch, entry.Name()))
		if readErr != nil || len(files) != 1 {
			t.Errorf("remnant %q contents/error = %#v/%v, want one scrubbed snapshot", entry.Name(), files, readErr)
			continue
		}
		fileInfo, statErr := files[0].Info()
		if statErr != nil || fileInfo.Size() != 0 || !fileInfo.Mode().IsRegular() {
			t.Errorf("remnant snapshot = %#v/%v, want zero regular file", fileInfo, statErr)
		}
	}
}

func TestApplyExactSavedPlansScratchSwapRefusesOutsideDeletion(t *testing.T) {
	fixture := newExactApplyFixture(t)
	victim := filepath.Join(fixture.workspace, "outside-victim")
	writeAssessmentTransactionFile(t, victim, []byte("keep me\n"), 0o600)
	hooks := exactApplyTestHooks(fixture)
	hooks.cleanupHooks.afterDirectoryIdentity = func() error {
		entries, err := os.ReadDir(fixture.scratch)
		if err != nil || len(entries) != 1 {
			return fmt.Errorf("scratch entries = %v, %v", entries, err)
		}
		public := filepath.Join(fixture.scratch, entries[0].Name())
		moved := public + "-moved"
		if err := os.Rename(public, moved); err != nil {
			return err
		}
		return os.Symlink(victim, public)
	}
	fake := &fakeExactPlanApplyTerraform{currentPlan: exactApplyCleanPlan()}
	result, err := applyExactSavedPlans(exactApplyOptions(fixture, fake), hooks)
	requireExactApplyFailure(t, err, "ASSESSMENT_CLEANUP_REFUSED")
	if result.Applied != 1 || len(fake.applied) != 1 {
		t.Fatalf("post-Apply cleanup refusal result/calls = %#v/%d, want committed Apply recorded", result, len(fake.applied))
	}
	for _, file := range []string{fixture.roots[0].SavedPlanPath, fixture.roots[0].FingerprintPath} {
		if _, statErr := os.Stat(file); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("post-Apply cleanup refusal saved pair %q remains: %v", file, statErr)
		}
	}
	content, readErr := os.ReadFile(victim)
	if readErr != nil || string(content) != "keep me\n" {
		t.Fatalf("outside victim = %q/%v, want untouched", content, readErr)
	}
}

func TestApplyExactSavedPlansPrimaryApplyFailurePrecedesCleanupRefusal(t *testing.T) {
	fixture := newExactApplyFixture(t)
	primary := errors.New("injected Apply failure")
	hooks := exactApplyTestHooks(fixture)
	hooks.cleanupHooks.afterDirectoryIdentity = func() error {
		entries, err := os.ReadDir(fixture.scratch)
		if err != nil || len(entries) != 1 {
			return fmt.Errorf("scratch entries = %v, %v", entries, err)
		}
		public := filepath.Join(fixture.scratch, entries[0].Name())
		moved := public + "-moved"
		if err := os.Rename(public, moved); err != nil {
			return err
		}
		return os.Symlink(filepath.Join(fixture.workspace, "outside"), public)
	}
	fake := &fakeExactPlanApplyTerraform{
		currentPlan: exactApplyCleanPlan(),
		onApply: func(ExactPlanApplyRequest) error {
			return primary
		},
	}
	result, err := applyExactSavedPlans(exactApplyOptions(fixture, fake), hooks)
	if !errors.Is(err, primary) || result.Applied != 0 || len(fake.applied) != 1 {
		t.Fatalf("primary+cleanup result/error/calls = %#v/%v/%d, want primary and zero committed", result, err, len(fake.applied))
	}
}

func TestApplyExactSavedPlansRecordsCommittedApplyWhenArtifactRemovalFails(t *testing.T) {
	fixture := newExactApplyFixture(t)
	fake := &fakeExactPlanApplyTerraform{
		currentPlan: exactApplyCleanPlan(),
		onApply: func(ExactPlanApplyRequest) error {
			if err := os.Remove(fixture.roots[0].SavedPlanPath); err != nil {
				return err
			}
			if err := os.Mkdir(fixture.roots[0].SavedPlanPath, 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(fixture.roots[0].SavedPlanPath, "child"), []byte("retain\n"), 0o600)
		},
	}
	result, err := applyExactSavedPlans(exactApplyOptions(fixture, fake), exactApplyTestHooks(fixture))
	if err == nil || result.Applied != 1 || len(fake.applied) != 1 {
		t.Fatalf("artifact-removal failure result/error/calls = %#v/%v/%d, want committed Apply plus error", result, err, len(fake.applied))
	}
	if _, statErr := os.Stat(fixture.roots[0].FingerprintPath); statErr != nil {
		t.Errorf("artifact-removal failure removed sources unexpectedly: %v", statErr)
	}
}

func TestExactPlanApplyAdapterUsesOnlyExplicitFakeExecutable(t *testing.T) {
	root := t.TempDir()
	envDir := filepath.Join(root, "env")
	if err := os.Mkdir(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "snapshot")
	writeAssessmentTransactionFile(t, snapshot, []byte("opaque\n"), 0o600)
	logPath := filepath.Join(root, "terraform.log")
	planJSON := `{"format_version":"1.2","terraform_version":"1.15.4","complete":true,"errored":false,"resource_changes":[],"output_changes":{}}`
	executable := assessmentExecutable(t, root, strings.Join([]string{
		"printf '%s|%s|%s\\n' \"$*\" \"${SAFE-unset}\" \"${CHECKPOINT_DISABLE-unset}\" >> " + assessmentShellLiteral(logPath),
		"if [ \"$1\" != init ]; then path=$3; [ \"$2\" = show ] && path=$4; IFS= read -r snapshot < \"$path\" || exit 97; [ \"$snapshot\" = opaque ] || exit 98; fi",
		"if [ \"$2\" = show ]; then printf '%s\\n' " + assessmentShellLiteral(planJSON) + "; fi",
	}, "\n"))
	adapter, err := CreateExactPlanApplyTerraform(CreateExactPlanApplyTerraformOptions{
		Environment: map[string]string{"SAFE": "one"}, TerraformExecutable: executable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Initialize(plan.PlanTerraformRequest{Directory: envDir, VarFiles: []string{}}); err != nil {
		t.Fatal(err)
	}
	snapshotFile, err := os.Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshotFile.Close()
	if _, err := adapter.Show(ExactPlanApplyShowRequest{Directory: envDir, SnapshotFile: snapshotFile}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(snapshot, snapshot+".rebound"); err != nil {
		t.Fatal(err)
	}
	writeAssessmentTransactionFile(t, snapshot, []byte("replacement\n"), 0o600)
	if err := adapter.Apply(ExactPlanApplyRequest{Directory: envDir, SnapshotFile: snapshotFile}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	childSnapshotPath, err := terraformcmd.InheritedPlanFilePath()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"init -input=false|one|unset",
		"-chdir=" + envDir + " show -json " + childSnapshotPath + "|unset|1",
		"apply -input=false " + childSnapshotPath + "|one|unset",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("fake Terraform log = %#v, want %#v", lines, want)
	}
}

func TestExactPlanApplyAdapterRejectsNilDescriptorBeforeSpawn(t *testing.T) {
	root := t.TempDir()
	envDir := filepath.Join(root, "env")
	if err := os.Mkdir(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "spawned")
	executable := assessmentExecutable(t, root, "printf x > "+assessmentShellLiteral(marker))
	adapter, err := CreateExactPlanApplyTerraform(CreateExactPlanApplyTerraformOptions{
		Environment: map[string]string{}, TerraformExecutable: executable,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.Apply(ExactPlanApplyRequest{Directory: envDir})
	requireExactApplyFailure(t, err, "INVALID_PLAN_SNAPSHOT")
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Errorf("Apply(nil descriptor) spawned Terraform: marker stat = %v", statErr)
	}
}

func TestExactPlanApplyProductionDoesNotResolveTerraformOrProviderSDK(t *testing.T) {
	content, err := os.ReadFile("exact_plan_apply.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, forbidden := range []string{`exec.Command("terra` + `form"`, "zscaler" + "-sdk-go"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("D4 production source contains forbidden live surface %q", forbidden)
		}
	}
}

func TestMakeAssessmentTemporaryDirectorySharedByExactApplyRemainsConcurrent(t *testing.T) {
	root := t.TempDir()
	const count = 16
	paths := make(chan string, count)
	errs := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() {
			defer group.Done()
			path, err := makeAssessmentTemporaryDirectory(root)
			if err != nil {
				errs <- err
				return
			}
			paths <- path
		}()
	}
	group.Wait()
	close(paths)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent private scratch creation error: %v", err)
	}
	seen := map[string]struct{}{}
	for path := range paths {
		if _, duplicate := seen[path]; duplicate {
			t.Errorf("duplicate randomized scratch path %q", path)
		}
		seen[path] = struct{}{}
	}
	if len(seen) != count {
		t.Errorf("concurrent scratch paths = %d, want %d", len(seen), count)
	}
}
