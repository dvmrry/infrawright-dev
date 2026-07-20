package assessment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
	tfjson "github.com/hashicorp/terraform-json"
)

const maximumApplyGitBranchBytes = 64 * 1024

const maximumApplyGitWaitDelay = 500 * time.Millisecond

// ExactPlanApplyShowRequest ports the show request in ExactPlanApplyTerraform
// from node-src/domain/exact-plan-apply.ts.
type ExactPlanApplyShowRequest struct {
	Directory    string
	SnapshotFile *os.File
}

// ExactPlanApplyRequest ports the apply request in ExactPlanApplyTerraform
// from node-src/domain/exact-plan-apply.ts.
type ExactPlanApplyRequest struct {
	Directory    string
	SnapshotFile *os.File
}

// ExactPlanApplyShownPlan preserves the lossless plan value used by the
// assessment contract alongside terraform-json's typed safety fields.
type ExactPlanApplyShownPlan struct {
	Typed *tfjson.Plan
	Raw   canonjson.Value
}

// ExactPlanApplyTerraform is the narrow Terraform capability required by
// exact-plan Apply. Tests inject this interface and never resolve host
// Terraform or provider credentials.
type ExactPlanApplyTerraform interface {
	Initialize(plan.PlanTerraformRequest) error
	Show(ExactPlanApplyShowRequest) (ExactPlanApplyShownPlan, error)
	Apply(ExactPlanApplyRequest) error
}

// ExactPlanApplyInputs ports ExactPlanApplyInputs from
// node-src/domain/exact-plan-apply.ts.
type ExactPlanApplyInputs struct {
	Deployment   deployment.Deployment
	Root         metadata.LoadedPackRoot
	ControlFiles []controlevidence.BoundAssessmentControlFile
}

// ExactPlanApplyResult ports ExactPlanApplyResult from
// node-src/domain/exact-plan-apply.ts.
type ExactPlanApplyResult struct {
	Applied int
}

// ExactPlanApplyOptions ports ExactPlanApplyOptions from
// node-src/domain/exact-plan-apply.ts. Tenant nil represents source null.
type ExactPlanApplyOptions struct {
	Workspace        string
	Tenant           *string
	Selectors        []string
	BackendConfig    *string
	PolicyPath       *string
	MainBranch       *string
	AllowDestroy     bool
	AllowNonMain     bool
	AllowPlanChanges bool
	CurrentBranch    func() string
	LoadInputs       func() (ExactPlanApplyInputs, error)
	Terraform        ExactPlanApplyTerraform
	OnDiagnostic     func(string)
}

// CreateExactPlanApplyTerraformOptions ports the adapter options from
// node-src/domain/exact-plan-apply.ts.
type CreateExactPlanApplyTerraformOptions struct {
	Environment         map[string]string
	Limits              *terraformcmd.TerraformCommandLimits
	ShowLimits          *terraformcmd.TerraformShowLimits
	TerraformExecutable string
}

// CurrentApplyBranchOptions ports currentApplyBranch options from
// node-src/domain/exact-plan-apply.ts. GitBranch is a deterministic test seam;
// nil selects the bounded shell-free Git probe.
type CurrentApplyBranchOptions struct {
	CWD         string
	Environment map[string]string
	GitBranch   func(string) string
}

type exactPlanApplyTerraformAdapter struct {
	environment         map[string]string
	showEnvironment     map[string]string
	limits              *terraformcmd.TerraformCommandLimits
	showLimits          *terraformcmd.TerraformShowLimits
	terraformation      plan.PlanTerraform
	terraformExecutable string
}

type exactPlanApplyHooks struct {
	makeTemporary func() (string, error)
	cleanupHooks  assessmentCleanupHooks
}

func exactPlanApplyFailure(code, message string, category procerr.Category) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: category,
		Message:  message,
	})
}

func cloneExactApplyEnvironment(environment map[string]string) map[string]string {
	cloned := make(map[string]string, len(environment))
	for key, value := range environment {
		cloned[key] = value
	}
	return cloned
}

func cloneExactApplyCommandLimits(limits *terraformcmd.TerraformCommandLimits) *terraformcmd.TerraformCommandLimits {
	if limits == nil {
		return nil
	}
	cloned := *limits
	if limits.TimeoutMs != nil {
		timeout := *limits.TimeoutMs
		cloned.TimeoutMs = &timeout
	}
	return &cloned
}

func cloneExactApplyShowLimits(limits *terraformcmd.TerraformShowLimits) *terraformcmd.TerraformShowLimits {
	if limits == nil {
		return nil
	}
	cloned := *limits
	return &cloned
}

// CreateExactPlanApplyTerraform adapts the already-qualified bounded
// terraformcmd runner to init, show, and exact saved-plan Apply. It does not
// resolve a binary from PATH and does not inherit an implicit environment.
func CreateExactPlanApplyTerraform(options CreateExactPlanApplyTerraformOptions) (ExactPlanApplyTerraform, error) {
	if options.Environment == nil {
		return nil, errors.New("exact-plan Apply Terraform requires an explicit environment")
	}
	environment := cloneExactApplyEnvironment(options.Environment)
	showEnvironment, err := terraformcmd.OperationalTerraformShowEnvironment(environment)
	if err != nil {
		return nil, err
	}
	limits := cloneExactApplyCommandLimits(options.Limits)
	return &exactPlanApplyTerraformAdapter{
		environment:     environment,
		showEnvironment: showEnvironment,
		limits:          limits,
		showLimits:      cloneExactApplyShowLimits(options.ShowLimits),
		terraformation: plan.CreatePlanTerraform(plan.CreatePlanTerraformOptions{
			Environment:         environment,
			Limits:              limits,
			TerraformExecutable: options.TerraformExecutable,
		}),
		terraformExecutable: options.TerraformExecutable,
	}, nil
}

func (adapter *exactPlanApplyTerraformAdapter) Initialize(request plan.PlanTerraformRequest) error {
	return adapter.terraformation.Initialize(request)
}

func (adapter *exactPlanApplyTerraformAdapter) Show(request ExactPlanApplyShowRequest) (ExactPlanApplyShownPlan, error) {
	raw, err := terraformcmd.TerraformShowPlan(terraformcmd.TerraformShowOptions{
		TerraformExecutable: adapter.terraformExecutable,
		EnvDir:              request.Directory,
		SnapshotFile:        request.SnapshotFile,
		Environment:         cloneExactApplyEnvironment(adapter.showEnvironment),
		Limits:              cloneExactApplyShowLimits(adapter.showLimits),
	})
	if err != nil {
		return ExactPlanApplyShownPlan{}, err
	}
	typed, err := decodeExactApplyTypedPlan(raw)
	if err != nil {
		return ExactPlanApplyShownPlan{}, err
	}
	return ExactPlanApplyShownPlan{Typed: typed, Raw: raw}, nil
}

func (adapter *exactPlanApplyTerraformAdapter) Apply(request ExactPlanApplyRequest) error {
	if request.SnapshotFile == nil {
		return exactPlanApplyFailure("INVALID_PLAN_SNAPSHOT", "saved-plan snapshot is unavailable", procerr.CategoryIO)
	}
	childSnapshotPath, err := terraformcmd.InheritedPlanFilePath()
	if err != nil {
		return exactPlanApplyFailure("INVALID_PLAN_SNAPSHOT", "saved-plan snapshot is unavailable", procerr.CategoryIO)
	}
	_, err = terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: adapter.terraformExecutable,
		Argv:                []string{"apply", "-input=false", childSnapshotPath},
		CWD:                 request.Directory,
		Environment:         cloneExactApplyEnvironment(adapter.environment),
		Limits:              cloneExactApplyCommandLimits(adapter.limits),
		Output:              terraformcmd.TerraformCommandOutputInherit,
		SnapshotFile:        request.SnapshotFile,
	})
	return err
}

type applyGitOutput struct {
	buffer     bytes.Buffer
	onOverflow context.CancelFunc
	overflow   bool
}

func (output *applyGitOutput) Write(value []byte) (int, error) {
	if output.overflow {
		return 0, errors.New("Git branch output exceeds limit")
	}
	remaining := maximumApplyGitBranchBytes - output.buffer.Len()
	if len(value) > remaining {
		if remaining > 0 {
			_, _ = output.buffer.Write(value[:remaining])
		}
		output.overflow = true
		if output.onOverflow != nil {
			output.onOverflow()
		}
		return remaining, errors.New("Git branch output exceeds limit")
	}
	return output.buffer.Write(value)
}

type applyGitProcessCleanup func(*exec.Cmd) error

func runGitApplyBranch(cwd, executable string) string {
	return runGitApplyBranchWithCleanup(cwd, executable, cleanupApplyGitProcessGroup)
}

func runGitApplyBranchWithCleanup(cwd, executable string, cleanup applyGitProcessCleanup) (branch string) {
	branch = "unknown"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, executable, "rev-parse", "--abbrev-ref", "HEAD")
	configureApplyGitProcess(command)
	defer func() {
		if err := cleanup(command); err != nil {
			branch = "unknown"
		}
	}()
	command.Dir = cwd
	command.WaitDelay = maximumApplyGitWaitDelay
	stdout := applyGitOutput{onOverflow: cancel}
	stderr := applyGitOutput{onOverflow: cancel}
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil || stdout.overflow || stderr.overflow {
		return "unknown"
	}
	return strings.TrimSpace(stdout.buffer.String())
}

func gitApplyBranch(cwd string) string {
	return runGitApplyBranch(cwd, "git")
}

// CurrentApplyBranch resolves the current branch with the legacy CI
// environment priority from node-src/domain/exact-plan-apply.ts.
func CurrentApplyBranch(options CurrentApplyBranchOptions) string {
	if options.Environment == nil {
		return "unknown"
	}
	for _, name := range []string{"BUILD_SOURCEBRANCH", "GITHUB_REF", "BITBUCKET_BRANCH"} {
		if value := options.Environment[name]; value != "" {
			parts := strings.Split(value, "refs/heads/")
			return parts[len(parts)-1]
		}
	}
	gitBranch := options.GitBranch
	if gitBranch == nil {
		gitBranch = gitApplyBranch
	}
	branch := "unknown"
	func() {
		defer func() { _ = recover() }()
		branch = gitBranch(options.CWD)
	}()
	return branch
}

func decodeExactApplyTypedPlan(raw canonjson.Value) (*tfjson.Plan, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, exactPlanApplyFailure(
			"INVALID_TERRAFORM_PLAN",
			"Terraform show returned a plan outside the typed plan contract",
			procerr.CategoryDomain,
		)
	}
	var typed tfjson.Plan
	if err := json.Unmarshal(data, &typed); err != nil {
		return nil, exactPlanApplyFailure(
			"INVALID_TERRAFORM_PLAN",
			"Terraform show returned a plan outside the typed plan contract",
			procerr.CategoryDomain,
		)
	}
	return &typed, nil
}

func requireExactApplyTypedComplete(shown ExactPlanApplyShownPlan) error {
	if shown.Typed == nil || shown.Typed.Complete == nil || !*shown.Typed.Complete {
		// Keep the independent terraform-json typed gate before raw
		// classification, but preserve plan-contract.ts's operator-visible
		// failure bytes instead of inventing a second CLI classification.
		return errors.New("plan must be complete before assessment")
	}
	return nil
}

func pythonApplyStringRepr(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	value = strings.ReplaceAll(value, "\t", `\t`)
	value = strings.ReplaceAll(value, "\r", `\r`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return "'" + value + "'"
}

func exactApplyDestroyCount(planValue any) int {
	planObject, ok := planValue.(map[string]any)
	if !ok {
		return 0
	}
	count := 0
	for _, field := range []string{"resource_changes", "resource_drift"} {
		records, ok := planObject[field].([]any)
		if !ok {
			continue
		}
		for _, rawRecord := range records {
			record, ok := rawRecord.(map[string]any)
			if !ok {
				continue
			}
			change, ok := record["change"].(map[string]any)
			if !ok {
				continue
			}
			actions, ok := change["actions"].([]any)
			if !ok {
				continue
			}
			for _, action := range actions {
				if action == "delete" {
					count++
					break
				}
			}
		}
	}
	return count
}

func exactApplyBackendRequest(backendConfig *string, root SavedPlanAssessmentRootInput) (*string, *string) {
	if backendConfig == nil {
		return nil, nil
	}
	config := *backendConfig
	key := root.Tenant + "/" + root.Label + ".tfstate"
	return &config, &key
}

func exactApplyBudget(limits artifacts.BoundedReadLimits) (*artifacts.ReadBudget, error) {
	return newAssessmentBudget(limits)
}

func recheckExactApplyEvidence(
	context LoadedSavedPlanAssessmentContext,
	controlFiles []controlevidence.BoundAssessmentControlFile,
	evidence *plan.SavedPlanEvidence,
	expectedRoots []SavedPlanAssessmentRootInput,
	policy BoundDriftPolicy,
) error {
	if err := controlevidence.RecheckAssessmentControlFiles(controlFiles); err != nil {
		return err
	}
	if err := RecheckLoadedSavedPlanAssessmentContext(context, expectedRoots); err != nil {
		return err
	}
	fingerprintBudget, err := exactApplyBudget(artifacts.DefaultBoundedReadLimits())
	if err != nil {
		return err
	}
	savedPlanBudget, err := exactApplyBudget(defaultSavedPlanLimits())
	if err != nil {
		return err
	}
	if err := plan.RecheckSavedPlanEvidence(plan.RecheckSavedPlanEvidenceOptions{
		Evidence:          evidence,
		FingerprintBudget: fingerprintBudget,
		SavedPlanBudget:   savedPlanBudget,
	}); err != nil {
		return err
	}
	policyBudget, err := exactApplyBudget(defaultPolicyLimits())
	if err != nil {
		return err
	}
	if err := RecheckBoundDriftPolicy(policy, policyBudget); err != nil {
		return err
	}
	return controlevidence.RecheckAssessmentControlFiles(controlFiles)
}

func exactApplyContract(root SavedPlanAssessmentRootInput) *plan.AssessmentPlanContract {
	if root.ReferenceOutputTypes == nil {
		return nil
	}
	return &plan.AssessmentPlanContract{
		ReferenceOutputTypes: append([]string(nil), root.ReferenceOutputTypes...),
	}
}

func defaultExactPlanApplyHooks() exactPlanApplyHooks {
	return exactPlanApplyHooks{
		makeTemporary: func() (string, error) {
			return makeAssessmentTemporaryDirectory(os.TempDir())
		},
	}
}

func applyExactPlanRoot(
	options ExactPlanApplyOptions,
	context LoadedSavedPlanAssessmentContext,
	controlFiles []controlevidence.BoundAssessmentControlFile,
	expectedRoots []SavedPlanAssessmentRootInput,
	policy BoundDriftPolicy,
	root SavedPlanAssessmentRootInput,
	hooks exactPlanApplyHooks,
) (applied bool, returnedErr error) {
	temporary, err := hooks.makeTemporary()
	if err != nil {
		return false, err
	}
	temporaryIdentity, err := directorySafeIdentity(temporary)
	if err != nil {
		return false, err
	}
	var evidence *plan.SavedPlanEvidence
	var snapshotFile *os.File
	cleanupSnapshots := make([]assessmentCleanupSnapshot, 0, 1)
	defer func() {
		if snapshotFile != nil {
			if closeErr := snapshotFile.Close(); closeErr != nil && returnedErr == nil {
				returnedErr = exactPlanApplyFailure("PLAN_SNAPSHOT_CHANGED", "saved-plan snapshot could not be closed", procerr.CategoryIO)
			}
		}
		var cleanupErr error
		if evidence != nil {
			cleanupErr = plan.CleanupSavedPlanEvidence(evidence)
		}
		if cleanupErr == nil {
			if failure := cleanupAssessmentTemporaryDirectory(
				temporary,
				temporaryIdentity,
				cleanupSnapshots,
				hooks.cleanupHooks,
			); failure != nil {
				cleanupErr = failure
			}
		}
		if returnedErr == nil && cleanupErr != nil {
			returnedErr = cleanupErr
		}
	}()

	backendConfig, backendKey := exactApplyBackendRequest(options.BackendConfig, root)
	fingerprintBudget, err := exactApplyBudget(artifacts.DefaultBoundedReadLimits())
	if err != nil {
		return false, err
	}
	savedPlanBudget, err := exactApplyBudget(defaultSavedPlanLimits())
	if err != nil {
		return false, err
	}
	evidence, err = plan.PrepareSavedPlanEvidence(plan.PrepareSavedPlanEvidenceOptions{
		SavedPlanPath:   root.SavedPlanPath,
		FingerprintPath: root.FingerprintPath,
		FingerprintInput: plan.PlanFingerprintInput{
			EnvDir:        root.EnvDir,
			VarFiles:      append([]string(nil), root.VarFiles...),
			MemberTypes:   append([]string(nil), root.Members...),
			BackendConfig: backendConfig,
			BackendKey:    backendKey,
		},
		SnapshotDirectory: temporary,
		FingerprintBudget: fingerprintBudget,
		SavedPlanBudget:   savedPlanBudget,
	})
	if err != nil {
		return false, err
	}
	cleanupSnapshot, err := assessmentSnapshotCleanupBinding(temporary, evidence.Snapshot)
	if err != nil {
		return false, err
	}
	cleanupSnapshots = append(cleanupSnapshots, cleanupSnapshot)
	if err := plan.RequireBackendConfiguration(options.BackendConfig, root.EnvDir, root.Label); err != nil {
		return false, err
	}
	onDiagnostic := options.OnDiagnostic
	if onDiagnostic == nil {
		onDiagnostic = func(string) {}
	}
	onDiagnostic("== apply " + root.Tenant + "/" + root.Label)
	if err := options.Terraform.Initialize(plan.PlanTerraformRequest{
		BackendConfig: backendConfig,
		BackendKey:    backendKey,
		Directory:     root.EnvDir,
		Save:          false,
		VarFiles:      []string{},
	}); err != nil {
		return false, err
	}
	if err := recheckExactApplyEvidence(context, controlFiles, evidence, expectedRoots, policy); err != nil {
		return false, err
	}
	snapshotBudget, err := exactApplyBudget(defaultSavedPlanLimits())
	if err != nil {
		return false, err
	}
	snapshotFile, err = plan.OpenSavedPlanSnapshot(evidence, snapshotBudget)
	if err != nil {
		return false, err
	}
	shownPlan, err := options.Terraform.Show(ExactPlanApplyShowRequest{
		Directory:    root.EnvDir,
		SnapshotFile: snapshotFile,
	})
	if err != nil {
		return false, err
	}
	if err := requireExactApplyTypedComplete(shownPlan); err != nil {
		return false, err
	}
	classification, err := ClassifyPlan(shownPlan.Raw, policy.Policy, exactApplyContract(root))
	if err != nil {
		return false, err
	}
	destroys := exactApplyDestroyCount(shownPlan.Raw)
	if classification.Status == Blocked && destroys > 0 && !options.AllowDestroy {
		return false, exactPlanApplyFailure(
			"APPLY_DESTROY_REFUSED",
			fmt.Sprintf(
				"%s/%s saved plan destroys (or replaces) %d resource(s) - refused",
				root.Tenant,
				root.Label,
				destroys,
			),
			procerr.CategoryDomain,
		)
	}
	if classification.Status == Blocked && !options.AllowPlanChanges {
		return false, exactPlanApplyFailure(
			"APPLY_BLOCKED_PLAN_REFUSED",
			root.Tenant+"/"+root.Label+" saved plan is blocked by untolerated changes; refused. "+
				"Run assert-adoptable for review, pass POLICY=<file> for explicit tolerated drift, "+
				"or use --allow-plan-changes only as a broad unsafe override.",
			procerr.CategoryDomain,
		)
	}
	if classification.Status == Tolerated {
		onDiagnostic("TOLERATED: " + root.Tenant + "/" + root.Label + " saved plan has consumer-tolerated drift")
	} else if classification.Status == Blocked {
		onDiagnostic("WARNING: applying BLOCKED " + root.Tenant + "/" + root.Label +
			" saved plan because --allow-plan-changes was set")
	}
	if err := recheckExactApplyEvidence(context, controlFiles, evidence, expectedRoots, policy); err != nil {
		return false, err
	}
	snapshotBudget, err = exactApplyBudget(defaultSavedPlanLimits())
	if err != nil {
		return false, err
	}
	if err := plan.RecheckSavedPlanSnapshot(evidence, snapshotFile, snapshotBudget); err != nil {
		return false, err
	}
	if err := options.Terraform.Apply(ExactPlanApplyRequest{Directory: root.EnvDir, SnapshotFile: snapshotFile}); err != nil {
		return false, err
	}
	applied = true
	if err := snapshotFile.Close(); err != nil {
		snapshotFile = nil
		return true, exactPlanApplyFailure("PLAN_SNAPSHOT_CHANGED", "saved-plan snapshot could not be closed", procerr.CategoryIO)
	}
	snapshotFile = nil
	_, err = plan.RemoveSavedPlanArtifacts(root.EnvDir)
	return applied, err
}

// ApplyExactSavedPlans applies only selected, current, fully classified saved
// plans in Python root order. It ports applyExactSavedPlans from
// node-src/domain/exact-plan-apply.ts.
func ApplyExactSavedPlans(options ExactPlanApplyOptions) (ExactPlanApplyResult, error) {
	return applyExactSavedPlans(options, defaultExactPlanApplyHooks())
}

func applyExactSavedPlans(options ExactPlanApplyOptions, hooks exactPlanApplyHooks) (ExactPlanApplyResult, error) {
	result := ExactPlanApplyResult{}
	if !filepath.IsAbs(options.Workspace) {
		return result, exactPlanApplyFailure("INVALID_WORKSPACE", "Apply workspace must be absolute", procerr.CategoryDomain)
	}
	if options.BackendConfig != nil && !filepath.IsAbs(*options.BackendConfig) ||
		options.PolicyPath != nil && !filepath.IsAbs(*options.PolicyPath) {
		return result, exactPlanApplyFailure(
			"UNRESOLVED_APPLY_PATH",
			"saved-plan Apply paths must be absolute",
			procerr.CategoryDomain,
		)
	}
	mainBranch := "main"
	if options.MainBranch != nil && *options.MainBranch != "" {
		mainBranch = *options.MainBranch
	}
	if options.CurrentBranch == nil {
		return result, exactPlanApplyFailure(
			"APPLY_BRANCH_UNAVAILABLE",
			"apply branch resolver is required",
			procerr.CategoryDomain,
		)
	}
	branch := options.CurrentBranch()
	if branch != mainBranch && !options.AllowNonMain {
		return result, exactPlanApplyFailure(
			"APPLY_BRANCH_REFUSED",
			"apply refused from "+pythonApplyStringRepr(branch)+" - only merged "+mainBranch+
				" config gets applied (use ALLOW_NON_MAIN=1 for an intentional exception)",
			procerr.CategoryDomain,
		)
	}
	policyBudget, err := exactApplyBudget(defaultPolicyLimits())
	if err != nil {
		return result, err
	}
	policy, err := LoadBoundDriftPolicy(options.PolicyPath, policyBudget)
	if err != nil {
		return result, err
	}
	onDiagnostic := options.OnDiagnostic
	if onDiagnostic == nil {
		onDiagnostic = func(string) {}
	}
	if options.AllowPlanChanges {
		onDiagnostic("WARNING: --allow-plan-changes is a broad legacy override for BLOCKED saved plans; " +
			"prefer POLICY=<file> for explicit tolerated drift.")
	}
	if options.LoadInputs == nil {
		return result, exactPlanApplyFailure("APPLY_INPUTS_REQUIRED", "saved-plan Apply inputs are required", procerr.CategoryDomain)
	}
	if options.Terraform == nil {
		return result, exactPlanApplyFailure("TERRAFORM_REQUIRED", "saved-plan Apply requires Terraform", procerr.CategoryDomain)
	}
	inputs, err := options.LoadInputs()
	if err != nil {
		return result, err
	}
	controlFiles, err := controlevidence.CopyAssessmentControlFiles(inputs.ControlFiles)
	if err != nil {
		return result, err
	}
	if err := controlevidence.RecheckAssessmentControlFiles(controlFiles); err != nil {
		return result, err
	}
	context := CopyLoadedSavedPlanAssessmentContext(LoadedSavedPlanAssessmentContext{
		Workspace:  options.Workspace,
		Deployment: inputs.Deployment,
		Root:       inputs.Root,
		Tenant:     cloneString(options.Tenant),
		Selectors:  append([]string(nil), options.Selectors...),
	})
	selectedRoots, diagnostics, err := MaterializeLoadedSavedPlanAssessmentRoots(context)
	if err != nil {
		return result, err
	}
	for _, diagnostic := range diagnostics {
		onDiagnostic("NOTE: " + diagnostic.Message)
	}
	if len(selectedRoots) == 0 {
		return result, exactPlanApplyFailure(
			"NO_SAVED_PLANS",
			"no saved plans found - run make plan SAVE=1 first",
			procerr.CategoryDomain,
		)
	}
	for index, root := range selectedRoots {
		applied, err := applyExactPlanRoot(
			options,
			context,
			controlFiles,
			append([]SavedPlanAssessmentRootInput(nil), selectedRoots[index:]...),
			policy,
			root,
			hooks,
		)
		if applied {
			result.Applied++
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}
