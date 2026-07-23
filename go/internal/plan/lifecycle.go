package plan

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// PlanTerraformRequest ports PlanTerraformRequest from
// the original implementation. Nil BackendConfig, BackendKey, and
// Environment represent the source interface's omitted optional properties.
type PlanTerraformRequest struct {
	BackendConfig *string
	BackendKey    *string
	Directory     string
	Environment   map[string]string
	Save          bool
	VarFiles      []string
}

// PlanTerraform ports PlanTerraform from the original implementation.
type PlanTerraform interface {
	Initialize(request PlanTerraformRequest) error
	Plan(request PlanTerraformRequest) error
}

// CreatePlanTerraformOptions ports createPlanTerraform's options object from
// the original implementation.
type CreatePlanTerraformOptions struct {
	Environment         map[string]string
	Limits              *terraformcmd.TerraformCommandLimits
	TerraformExecutable string
}

// PlanRunResult ports PlanRunResult from the original implementation.
type PlanRunResult struct {
	Planned int
}

// CleanPlansResult ports CleanPlansResult from
// the original implementation.
type CleanPlansResult struct {
	Removed int
}

// PlanEnvironmentRootsOptions ports planEnvironmentRoots's options object
// from the original implementation.
type PlanEnvironmentRootsOptions struct {
	BackendConfig *string
	Deployment    deployment.Deployment
	ImportsOnly   bool
	OnDiagnostic  func(string)
	Root          metadata.LoadedPackRoot
	Save          bool
	Selectors     []string
	Tenant        string
	Terraform     PlanTerraform
	Workspace     string
}

// CleanPlansOptions ports cleanPlans's options object from
// the original implementation. Tenant nil represents the source's null.
type CleanPlansOptions struct {
	Deployment   deployment.Deployment
	OnDiagnostic func(string)
	Root         metadata.LoadedPackRoot
	Selectors    []string
	Tenant       *string
	Workspace    string
}

type terraformCommandRunner func(terraformcmd.TerraformCommandOptions) (terraformcmd.TerraformCommandResult, error)

type planTerraformAdapter struct {
	environment         map[string]string
	limits              *terraformcmd.TerraformCommandLimits
	terraformExecutable string
	run                 terraformCommandRunner
}

func lifecycleFailure(code, message string, category procerr.Category) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: category,
		Message:  message,
	})
}

func cloneEnvironment(environment map[string]string) map[string]string {
	cloned := make(map[string]string, len(environment))
	for key, value := range environment {
		cloned[key] = value
	}
	return cloned
}

func clonePlanTerraformRequest(request PlanTerraformRequest) PlanTerraformRequest {
	cloned := request
	if request.BackendConfig != nil {
		backendConfig := *request.BackendConfig
		cloned.BackendConfig = &backendConfig
	}
	if request.BackendKey != nil {
		backendKey := *request.BackendKey
		cloned.BackendKey = &backendKey
	}
	if request.Environment != nil {
		cloned.Environment = cloneEnvironment(request.Environment)
	}
	cloned.VarFiles = append([]string(nil), request.VarFiles...)
	return cloned
}

func cloneTerraformLimits(limits *terraformcmd.TerraformCommandLimits) *terraformcmd.TerraformCommandLimits {
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

// CreatePlanTerraform ports createPlanTerraform from
// the original implementation, adapting the shell-free Terraform runner
// to ordinary deployment init and plan operations.
func CreatePlanTerraform(options CreatePlanTerraformOptions) PlanTerraform {
	return &planTerraformAdapter{
		environment:         cloneEnvironment(options.Environment),
		limits:              cloneTerraformLimits(options.Limits),
		terraformExecutable: options.TerraformExecutable,
		run:                 terraformcmd.RunTerraformCommand,
	}
}

func (adapter *planTerraformAdapter) commandEnvironment(request map[string]string) map[string]string {
	environment := cloneEnvironment(adapter.environment)
	for key, value := range request {
		environment[key] = value
	}
	return environment
}

// Initialize runs Terraform init under the exact createPlanTerraform argv and
// output policy from the original implementation.
func (adapter *planTerraformAdapter) Initialize(request PlanTerraformRequest) error {
	request = clonePlanTerraformRequest(request)
	argv := []string{"init", "-input=false"}
	if request.BackendConfig != nil {
		backendKey := ""
		if request.BackendKey != nil {
			backendKey = *request.BackendKey
		}
		argv = append(
			argv,
			"-reconfigure",
			"-backend-config="+*request.BackendConfig,
			"-backend-config=key="+backendKey,
		)
	}
	_, err := adapter.run(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: adapter.terraformExecutable,
		Argv:                argv,
		CWD:                 request.Directory,
		Environment:         adapter.commandEnvironment(request.Environment),
		Limits:              cloneTerraformLimits(adapter.limits),
		Output:              terraformcmd.TerraformCommandOutputInheritStderr,
	})
	return err
}

// Plan runs Terraform plan under the exact createPlanTerraform argv and output
// policy from the original implementation.
func (adapter *planTerraformAdapter) Plan(request PlanTerraformRequest) error {
	request = clonePlanTerraformRequest(request)
	argv := []string{"plan", "-input=false"}
	for _, file := range request.VarFiles {
		argv = append(argv, "-var-file="+file)
	}
	if request.Save {
		argv = append(argv, "-out=tfplan")
	}
	_, err := adapter.run(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: adapter.terraformExecutable,
		Argv:                argv,
		CWD:                 request.Directory,
		Environment:         adapter.commandEnvironment(request.Environment),
		Limits:              cloneTerraformLimits(adapter.limits),
		Output:              terraformcmd.TerraformCommandOutputInherit,
	})
	return err
}

func lifecycleExists(candidate string) bool {
	_, err := os.Stat(candidate)
	return err == nil
}

func lifecycleIsFile(candidate string) bool {
	info, err := os.Stat(candidate)
	return err == nil && info.Mode().IsRegular()
}

func removeLifecycleFileIfPresent(candidate string) (bool, error) {
	info, err := os.Lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, &os.PathError{Op: "unlink", Path: candidate, Err: errors.New("is a directory")}
	}
	err = os.Remove(candidate)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func lifecycleWorkspacePath(workspace, candidate string) string {
	if filepath.IsAbs(candidate) {
		return candidate
	}
	resolved, err := filepath.Abs(filepath.Join(workspace, candidate))
	if err != nil {
		// filepath.Abs only fails when the process working directory cannot be
		// obtained; path.resolve would likewise fail to produce a usable path.
		return filepath.Join(workspace, candidate)
	}
	return resolved
}

func normalizedLifecycleLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

// RequireBackendConfiguration ports requireBackendConfiguration from
// the original implementation.
func RequireBackendConfiguration(backendConfig *string, directory, label string) error {
	if backendConfig != nil {
		return nil
	}
	mainPath := filepath.Join(directory, "main.tf")
	if !lifecycleExists(mainPath) {
		return nil
	}
	content, err := os.ReadFile(mainPath)
	if err != nil {
		return lifecycleFailure(
			"READ_FAILED",
			"unable to read "+label+" environment root",
			procerr.CategoryIO,
		)
	}
	if !utf8.Valid(content) {
		return lifecycleFailure(
			"INVALID_UTF8",
			label+" environment root is not valid UTF-8",
			procerr.CategoryDomain,
		)
	}
	for _, line := range normalizedLifecycleLines(string(content)) {
		if strings.HasPrefix(line, `  backend "`) {
			return lifecycleFailure(
				"BACKEND_CONFIG_REQUIRED",
				label+" declares a remote backend; run with BACKEND_CONFIG=<file>",
				procerr.CategoryDomain,
			)
		}
	}
	return nil
}

func rootRequiresReferenceBackendEnvironment(directory string) (bool, error) {
	mainPath := filepath.Join(directory, "main.tf")
	if !lifecycleExists(mainPath) {
		return false, nil
	}
	content, err := os.ReadFile(mainPath)
	if err != nil || !utf8.Valid(content) {
		return false, lifecycleFailure(
			"READ_FAILED",
			"unable to inspect cross-state environment root",
			procerr.CategoryIO,
		)
	}
	want := `variable "` + ReferenceBackendVariable + `" {`
	for _, line := range normalizedLifecycleLines(string(content)) {
		if line == want {
			return true, nil
		}
	}
	return false, nil
}

func lifecycleRootIsDerived(root metadata.LoadedPackRoot, resourceType string) bool {
	resource, ok := root.Resources[resourceType]
	if !ok {
		return false
	}
	_, ok = resource.Registry["derive"].(map[string]any)
	return ok
}

func sameLifecycleEnvironment(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		rightValue, present := right[key]
		if !present || rightValue != value {
			return false
		}
	}
	return true
}

func sameLifecycleFingerprint(left, right PlanFingerprintV2) bool {
	return left.Version == right.Version && left.SHA256 == right.SHA256
}

func lifecycleFingerprintText(fingerprint PlanFingerprintV2) string {
	return fmt.Sprintf(`{"sha256": "%s", "version": %d}`+"\n", fingerprint.SHA256, fingerprint.Version)
}

type lifecycleSavedPaths struct {
	plan    string
	sources string
}

func savedLifecyclePaths(directory string) lifecycleSavedPaths {
	return lifecycleSavedPaths{
		plan:    filepath.Join(directory, "tfplan"),
		sources: filepath.Join(directory, "tfplan.sources"),
	}
}

// RemoveSavedPlanArtifacts ports removeSavedPlanArtifacts from
// the original implementation.
func RemoveSavedPlanArtifacts(directory string) (bool, error) {
	saved := savedLifecyclePaths(directory)
	planRemoved, err := removeLifecycleFileIfPresent(saved.plan)
	if err != nil {
		return false, err
	}
	sourcesRemoved, err := removeLifecycleFileIfPresent(saved.sources)
	if err != nil {
		return false, err
	}
	return planRemoved || sourcesRemoved, nil
}

func noteWholeRoot(diagnostic roots.WholeRootDiagnostic, onDiagnostic func(string)) {
	onDiagnostic("NOTE: " + diagnostic.Message)
}

func referenceEnvironment(required bool, backendConfig *string) (map[string]string, error) {
	if !required || backendConfig == nil {
		return map[string]string{}, nil
	}
	return ReferenceBackendEnvironmentFromConfig(*backendConfig)
}

func lifecycleBackend(options PlanEnvironmentRootsOptions) (*string, error) {
	if options.BackendConfig == nil || *options.BackendConfig == "" {
		return nil, nil
	}
	resolved := lifecycleWorkspacePath(options.Workspace, *options.BackendConfig)
	return &resolved, nil
}

func lifecycleFingerprintInputs(
	directory string,
	members []string,
	varFiles []string,
	backendConfig *string,
	backendKey *string,
) (InitFingerprintInput, PlanFingerprintInput) {
	return InitFingerprintInput{
			EnvDir:        directory,
			MemberTypes:   members,
			BackendConfig: backendConfig,
			BackendKey:    backendKey,
		}, PlanFingerprintInput{
			EnvDir:        directory,
			MemberTypes:   members,
			VarFiles:      varFiles,
			BackendConfig: backendConfig,
			BackendKey:    backendKey,
		}
}

func runLifecyclePlan(
	options PlanEnvironmentRootsOptions,
	selectedRoot roots.MaterializedPlanRoot,
	request PlanTerraformRequest,
	requiredReferenceEnvironment bool,
	initialReferenceEnvironment map[string]string,
	initInput InitFingerprintInput,
	planInput PlanFingerprintInput,
) error {
	var initIdentity string
	if options.Save {
		payload, err := CaptureInitSourcesPayload(initInput, nil)
		if err != nil {
			return err
		}
		initIdentity = InitSourcesSHA256(payload)
	}

	runErr := func() error {
		if err := options.Terraform.Initialize(clonePlanTerraformRequest(request)); err != nil {
			return err
		}
		if options.Save {
			payload, err := CaptureInitSourcesPayload(initInput, nil)
			if err != nil {
				return err
			}
			if InitSourcesSHA256(payload) != initIdentity {
				return lifecycleFailure(
					"INIT_INPUTS_CHANGED",
					selectedRoot.EnvDir+": init inputs changed while init was running - re-run make plan SAVE=1",
					procerr.CategoryDomain,
				)
			}
		}

		referenceEnvironmentAfterInit, err := referenceEnvironment(
			requiredReferenceEnvironment,
			request.BackendConfig,
		)
		if err != nil {
			return err
		}
		if !sameLifecycleEnvironment(referenceEnvironmentAfterInit, initialReferenceEnvironment) {
			return lifecycleFailure(
				"INIT_INPUTS_CHANGED",
				selectedRoot.EnvDir+": cross-state backend inputs changed while init was running - re-run make plan SAVE=1",
				procerr.CategoryDomain,
			)
		}

		var fingerprint PlanFingerprintV2
		if options.Save {
			fingerprint, err = FingerprintPlanV2(planInput, nil)
			if err != nil {
				return err
			}
		}
		confirmedReferenceEnvironment, err := referenceEnvironment(
			requiredReferenceEnvironment,
			request.BackendConfig,
		)
		if err != nil {
			return err
		}
		if !sameLifecycleEnvironment(confirmedReferenceEnvironment, referenceEnvironmentAfterInit) {
			return lifecycleFailure(
				"PLAN_INPUTS_CHANGED",
				selectedRoot.EnvDir+": cross-state backend inputs changed before planning - re-run make plan SAVE=1",
				procerr.CategoryDomain,
			)
		}
		planRequest := request
		if len(referenceEnvironmentAfterInit) > 0 {
			planRequest.Environment = cloneEnvironment(referenceEnvironmentAfterInit)
		}
		if err := options.Terraform.Plan(clonePlanTerraformRequest(planRequest)); err != nil {
			return err
		}
		if !options.Save {
			return nil
		}

		current, err := FingerprintPlanV2(planInput, nil)
		if err != nil {
			return err
		}
		if !sameLifecycleFingerprint(current, fingerprint) {
			return lifecycleFailure(
				"PLAN_INPUTS_CHANGED",
				selectedRoot.EnvDir+": saved plan is stale relative to the current root configuration - re-run make plan SAVE=1 (plan inputs changed while the plan was running)",
				procerr.CategoryDomain,
			)
		}
		saved := savedLifecyclePaths(request.Directory)
		if !lifecycleIsFile(saved.plan) {
			return lifecycleFailure(
				"MISSING_SAVED_PLAN",
				selectedRoot.EnvDir+": Terraform did not write tfplan",
				procerr.CategoryDomain,
			)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(saved.plan, 0o600); err != nil {
				return err
			}
		}
		if err := os.WriteFile(saved.sources, []byte(lifecycleFingerprintText(fingerprint)), 0o600); err != nil {
			return err
		}
		return nil
	}()
	if runErr == nil {
		return nil
	}
	if options.Save {
		if _, cleanupErr := RemoveSavedPlanArtifacts(request.Directory); cleanupErr != nil {
			return cleanupErr
		}
	}
	return runErr
}

// PlanEnvironmentRoots ports planEnvironmentRoots from
// the original implementation, including saved-pair cleanup and the init
// and plan input freshness checks.
func PlanEnvironmentRoots(options PlanEnvironmentRootsOptions) (PlanRunResult, error) {
	if err := roots.ValidateTenant(options.Tenant); err != nil {
		return PlanRunResult{}, err
	}
	tenant := options.Tenant
	selected, err := roots.LoadedPlanRoots(roots.LoadedPlanRootsOptions{
		Workspace:  options.Workspace,
		Deployment: options.Deployment,
		Root:       options.Root,
		Tenant:     &tenant,
		Selectors:  options.Selectors,
	})
	if err != nil {
		return PlanRunResult{}, err
	}
	onDiagnostic := options.OnDiagnostic
	if onDiagnostic == nil {
		onDiagnostic = func(string) {}
	}
	diagnostics := make(map[string]roots.WholeRootDiagnostic, len(selected.Diagnostics))
	for _, diagnostic := range selected.Diagnostics {
		diagnostics[diagnostic.Root] = diagnostic
	}
	backendConfig, err := lifecycleBackend(options)
	if err != nil {
		return PlanRunResult{}, err
	}

	planned := 0
	for _, selectedRoot := range selected.Result.Roots {
		if diagnostic, ok := diagnostics[selectedRoot.Label]; ok {
			noteWholeRoot(diagnostic, onDiagnostic)
		}
		derived := make([]string, 0)
		for _, member := range selectedRoot.Members {
			if lifecycleRootIsDerived(options.Root, member) {
				derived = append(derived, member)
			}
		}
		derived = canonjson.SortedStrings(derived)
		if options.ImportsOnly && len(derived) > 0 {
			onDiagnostic(fmt.Sprintf(
				"skip %s (IMPORTS_ONLY: derived/non-importable member %s)",
				selectedRoot.Label,
				strings.Join(derived, ", "),
			))
			continue
		}

		directory := lifecycleWorkspacePath(options.Workspace, selectedRoot.EnvDir)
		if options.Save {
			if _, err := RemoveSavedPlanArtifacts(directory); err != nil {
				return PlanRunResult{}, err
			}
		}

		varFiles := make([]string, 0, len(selectedRoot.Members))
		missing := make([]string, 0)
		for _, resourceType := range selectedRoot.Members {
			paths, err := tfrender.ComputeTransformArtifactPaths(
				options.Deployment,
				resourceType,
				options.Tenant,
			)
			if err != nil {
				return PlanRunResult{}, err
			}
			resolved := lifecycleWorkspacePath(options.Workspace, paths.Config)
			if lifecycleIsFile(resolved) {
				varFiles = append(varFiles, resolved)
			} else {
				missing = append(missing, paths.Config)
			}
		}
		if len(varFiles) == 0 {
			for _, file := range missing {
				onDiagnostic(fmt.Sprintf("skip %s (no %s)", selectedRoot.Label, file))
			}
			continue
		}
		if err := RequireBackendConfiguration(backendConfig, directory, selectedRoot.Label); err != nil {
			return PlanRunResult{}, err
		}
		onDiagnostic("== plan " + selectedRoot.Label)

		var backendKey *string
		if backendConfig != nil {
			value := options.Tenant + "/" + selectedRoot.Label + ".tfstate"
			backendKey = &value
		}
		requiresReferenceEnvironment, err := rootRequiresReferenceBackendEnvironment(directory)
		if err != nil {
			return PlanRunResult{}, err
		}
		initialReferenceEnvironment, err := referenceEnvironment(
			requiresReferenceEnvironment,
			backendConfig,
		)
		if err != nil {
			return PlanRunResult{}, err
		}
		request := PlanTerraformRequest{
			BackendConfig: backendConfig,
			BackendKey:    backendKey,
			Directory:     directory,
			Save:          options.Save,
			VarFiles:      append([]string(nil), varFiles...),
		}
		initInput, planInput := lifecycleFingerprintInputs(
			directory,
			selectedRoot.Members,
			varFiles,
			backendConfig,
			backendKey,
		)
		if err := runLifecyclePlan(
			options,
			selectedRoot,
			request,
			requiresReferenceEnvironment,
			initialReferenceEnvironment,
			initInput,
			planInput,
		); err != nil {
			return PlanRunResult{}, err
		}
		planned++
	}
	if planned == 0 {
		return PlanRunResult{}, lifecycleFailure(
			"NO_ROOTS_PLANNED",
			fmt.Sprintf(
				"no roots planned for TENANT=%s (missing env roots or config?)",
				options.Tenant,
			),
			procerr.CategoryDomain,
		)
	}
	return PlanRunResult{Planned: planned}, nil
}

// CleanPlans ports cleanPlans from the original implementation and
// removes only tfplan/tfplan.sources pairs from selected materialized roots.
func CleanPlans(options CleanPlansOptions) (CleanPlansResult, error) {
	if options.Tenant != nil {
		if err := roots.ValidateTenant(*options.Tenant); err != nil {
			return CleanPlansResult{}, err
		}
	}
	selected, err := roots.LoadedPlanRoots(roots.LoadedPlanRootsOptions{
		Workspace:  options.Workspace,
		Deployment: options.Deployment,
		Root:       options.Root,
		Tenant:     options.Tenant,
		Selectors:  options.Selectors,
	})
	if err != nil {
		return CleanPlansResult{}, err
	}
	onDiagnostic := options.OnDiagnostic
	if onDiagnostic == nil {
		onDiagnostic = func(string) {}
	}
	diagnostics := make(map[string]roots.WholeRootDiagnostic, len(selected.Diagnostics))
	for _, diagnostic := range selected.Diagnostics {
		diagnostics[diagnostic.Root] = diagnostic
	}

	removed := 0
	for _, selectedRoot := range selected.Result.Roots {
		if diagnostic, ok := diagnostics[selectedRoot.Label]; ok {
			noteWholeRoot(diagnostic, onDiagnostic)
		}
		removedAny := false
		for _, name := range []string{"tfplan", "tfplan.sources"} {
			logical := path.Join(selectedRoot.EnvDir, name)
			wasRemoved, err := removeLifecycleFileIfPresent(
				lifecycleWorkspacePath(options.Workspace, logical),
			)
			if err != nil {
				return CleanPlansResult{}, err
			}
			if wasRemoved {
				onDiagnostic("removed " + logical)
				removedAny = true
			}
		}
		if removedAny {
			removed++
		}
	}
	onDiagnostic(fmt.Sprintf("%d stale plan(s) removed", removed))
	return CleanPlansResult{Removed: removed}, nil
}
