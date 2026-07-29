package plan

// refresh.go owns the refresh-only reconciliation path: the operation that
// writes refreshed values into state and touches no remote object.
//
// It deliberately does not route through the saved-plan classifier used by
// assert-clean, assert-adoptable, and apply. Those gates refuse a plan whose
// refresh found state stale, and that refusal cannot be cleared beforehand,
// because persisting the refreshed values is the apply being refused. The
// classifier's guards are also entangled: exact-plan apply gates both the
// destroy refusal and the blocked-plan refusal on the same Blocked status, so
// relaxing drift there would stop the destroy guard firing for a
// drift-sourced delete. Keeping this path off that classifier leaves every
// one of those guards reachable on every other apply path.
//
// Safety here comes from the operation's semantics rather than from an
// operator permission, so no allow-flag gates it. That is enforced rather
// than documented: the saved plan produced by -refresh-only is read back and
// refused unless every resource change in it is a no-op.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// RefreshPlanRequest describes one `terraform plan -refresh-only` invocation.
// SnapshotPath is the absolute path the child writes the saved plan to.
type RefreshPlanRequest struct {
	BackendConfig *string
	BackendKey    *string
	Directory     string
	Environment   map[string]string
	SnapshotPath  string
	VarFiles      []string
}

// RefreshShowRequest reads back a refresh-only saved plan through an already
// open descriptor, so no path is re-resolved between verification and apply.
type RefreshShowRequest struct {
	Directory    string
	SnapshotFile *os.File
}

// RefreshApplyRequest applies the exact descriptor that was verified.
type RefreshApplyRequest struct {
	Directory    string
	SnapshotFile *os.File
}

// RefreshTerraform is the Terraform capability set for reconciliation.
type RefreshTerraform interface {
	Initialize(request PlanTerraformRequest) error
	PlanRefreshOnly(request RefreshPlanRequest) error
	Show(request RefreshShowRequest) (canonjson.Value, error)
	Apply(request RefreshApplyRequest) error
}

// RefreshEnvironmentRootsOptions selects the roots to reconcile.
type RefreshEnvironmentRootsOptions struct {
	BackendConfig *string
	Deployment    deployment.Deployment
	OnDiagnostic  func(string)
	Root          metadata.LoadedPackRoot
	Selectors     []string
	Tenant        string
	Terraform     RefreshTerraform
	Workspace     string
}

// RefreshRunResult counts the roots reconciled and the resources whose
// recorded values the reconciliation moved.
type RefreshRunResult struct {
	Refreshed  int
	Reconciled int
}

// RefreshedResource names one resource whose state the refresh reconciled.
type RefreshedResource struct {
	Address string
	Actions []string
}

func refreshFailure(code, message string, category procerr.Category) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: category,
		Message:  message,
	})
}

func refreshRecordActions(record any) ([]string, bool) {
	object, ok := record.(map[string]any)
	if !ok {
		return nil, false
	}
	change, ok := object["change"].(map[string]any)
	if !ok {
		return nil, false
	}
	rawActions, ok := change["actions"].([]any)
	if !ok {
		return nil, false
	}
	actions := make([]string, 0, len(rawActions))
	for _, rawAction := range rawActions {
		action, ok := rawAction.(string)
		if !ok {
			return nil, false
		}
		actions = append(actions, action)
	}
	return actions, true
}

func refreshRecordAddress(record any) string {
	object, ok := record.(map[string]any)
	if !ok {
		return ""
	}
	address, _ := object["address"].(string)
	return address
}

func refreshRecordImports(record any) bool {
	object, ok := record.(map[string]any)
	if !ok {
		return false
	}
	change, ok := object["change"].(map[string]any)
	if !ok {
		return false
	}
	importing, ok := change["importing"].(map[string]any)
	return ok && len(importing) > 0
}

// requireStateOnlyRefreshPlan is the enforcement that makes an allow-flag
// unnecessary. A refresh-only plan reconciles recorded values and writes
// nothing remote, so every entry in resource_changes must be a no-op. Anything
// else means the artifact about to be applied is not the operation this
// command claims to perform, whatever produced it.
func requireStateOnlyRefreshPlan(planValue any, label string) error {
	planObject, ok := planValue.(map[string]any)
	if !ok {
		return refreshFailure(
			"INVALID_REFRESH_PLAN",
			"Terraform show did not emit a plan object for "+label,
			procerr.CategoryDomain,
		)
	}
	records, _ := planObject["resource_changes"].([]any)
	offending := make([]string, 0)
	for _, record := range records {
		actions, ok := refreshRecordActions(record)
		if !ok {
			return refreshFailure(
				"INVALID_REFRESH_PLAN",
				label+" refresh-only plan contains an unreadable resource change",
				procerr.CategoryDomain,
			)
		}
		stateOnly := len(actions) == 1 && actions[0] == "no-op" && !refreshRecordImports(record)
		if !stateOnly {
			address := refreshRecordAddress(record)
			if address == "" {
				address = "<unknown>"
			}
			offending = append(offending, address+" "+strings.Join(actions, ","))
		}
	}
	if len(offending) > 0 {
		return refreshFailure(
			"REFRESH_PLAN_NOT_STATE_ONLY",
			label+" refresh-only plan would change "+
				fmt.Sprintf("%d resource(s)", len(offending))+
				" instead of only recorded state: "+strings.Join(canonjson.SortedStrings(offending), "; ")+
				". Refused; this path may never create, modify, or destroy.",
			procerr.CategoryDomain,
		)
	}
	return nil
}

// refreshReconciledResources reports what the refresh moved. Terraform records
// stale values in resource_drift, which is exactly the reconciliation this
// command performs and is never a reason to refuse here.
func refreshReconciledResources(planValue any) []RefreshedResource {
	planObject, ok := planValue.(map[string]any)
	if !ok {
		return []RefreshedResource{}
	}
	records, _ := planObject["resource_drift"].([]any)
	reconciled := make([]RefreshedResource, 0, len(records))
	for _, record := range records {
		actions, ok := refreshRecordActions(record)
		if !ok {
			continue
		}
		reconciled = append(reconciled, RefreshedResource{
			Address: refreshRecordAddress(record),
			Actions: actions,
		})
	}
	return reconciled
}

// RefreshEnvironmentRoots reconciles recorded state with reality for every
// selected root and reports what moved.
func RefreshEnvironmentRoots(options RefreshEnvironmentRootsOptions) (RefreshRunResult, error) {
	if err := roots.ValidateTenant(options.Tenant); err != nil {
		return RefreshRunResult{}, err
	}
	if options.Terraform == nil {
		return RefreshRunResult{}, refreshFailure(
			"INVALID_REFRESH_REQUEST",
			"refresh requires a Terraform adapter",
			procerr.CategoryDomain,
		)
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
		return RefreshRunResult{}, err
	}
	onDiagnostic := options.OnDiagnostic
	if onDiagnostic == nil {
		onDiagnostic = func(string) {}
	}
	diagnostics := make(map[string]roots.WholeRootDiagnostic, len(selected.Diagnostics))
	for _, diagnostic := range selected.Diagnostics {
		diagnostics[diagnostic.Root] = diagnostic
	}
	backendConfig, err := lifecycleBackend(PlanEnvironmentRootsOptions{
		BackendConfig: options.BackendConfig,
		Workspace:     options.Workspace,
	})
	if err != nil {
		return RefreshRunResult{}, err
	}

	result := RefreshRunResult{}
	for _, selectedRoot := range selected.Result.Roots {
		if diagnostic, ok := diagnostics[selectedRoot.Label]; ok {
			noteWholeRoot(diagnostic, onDiagnostic)
		}
		directory := lifecycleWorkspacePath(options.Workspace, selectedRoot.EnvDir)
		varFiles := make([]string, 0, len(selectedRoot.Members))
		missing := make([]string, 0)
		for _, resourceType := range selectedRoot.Members {
			paths, err := tfrender.ComputeTransformArtifactPaths(
				options.Deployment,
				resourceType,
				options.Tenant,
			)
			if err != nil {
				return RefreshRunResult{}, err
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
			return RefreshRunResult{}, err
		}
		onDiagnostic("== refresh " + selectedRoot.Label)

		var backendKey *string
		if backendConfig != nil {
			value := options.Tenant + "/" + selectedRoot.Label + ".tfstate"
			backendKey = &value
		}
		requiresReferenceEnvironment, err := rootRequiresReferenceBackendEnvironment(directory)
		if err != nil {
			return RefreshRunResult{}, err
		}
		environment, err := referenceEnvironment(requiresReferenceEnvironment, backendConfig)
		if err != nil {
			return RefreshRunResult{}, err
		}
		reconciled, err := refreshSingleRoot(
			options,
			selectedRoot.Label,
			directory,
			backendConfig,
			backendKey,
			environment,
			varFiles,
		)
		if err != nil {
			return RefreshRunResult{}, err
		}
		if len(reconciled) == 0 {
			onDiagnostic("   state already matches reality")
		}
		for _, resource := range reconciled {
			onDiagnostic("   reconciled " + resource.Address + " " + strings.Join(resource.Actions, ","))
		}
		result.Refreshed++
		result.Reconciled += len(reconciled)
	}
	if result.Refreshed == 0 {
		return RefreshRunResult{}, refreshFailure(
			"NO_ROOTS_REFRESHED",
			"no environment roots were refreshed",
			procerr.CategoryDomain,
		)
	}
	return result, nil
}

func refreshSingleRoot(
	options RefreshEnvironmentRootsOptions,
	label string,
	directory string,
	backendConfig *string,
	backendKey *string,
	environment map[string]string,
	varFiles []string,
) (reconciled []RefreshedResource, returnedErr error) {
	if err := options.Terraform.Initialize(PlanTerraformRequest{
		BackendConfig: backendConfig,
		BackendKey:    backendKey,
		Directory:     directory,
		Environment:   environment,
		Save:          false,
		VarFiles:      []string{},
	}); err != nil {
		return nil, err
	}
	// The saved plan lives outside the workspace so it can never be mistaken
	// for a `plan --save` artifact or reach a fingerprint.
	temporary, err := os.MkdirTemp("", "infrawright-refresh-")
	if err != nil {
		return nil, refreshFailure(
			"REFRESH_SNAPSHOT_UNAVAILABLE",
			"unable to create the refresh-only plan directory for "+label,
			procerr.CategoryIO,
		)
	}
	defer func() {
		if err := os.RemoveAll(temporary); err != nil && returnedErr == nil {
			returnedErr = refreshFailure(
				"REFRESH_SNAPSHOT_RETAINED",
				"unable to remove the refresh-only plan directory for "+label,
				procerr.CategoryIO,
			)
		}
	}()
	snapshotPath := filepath.Join(temporary, "tfplan.refresh")
	if err := options.Terraform.PlanRefreshOnly(RefreshPlanRequest{
		BackendConfig: backendConfig,
		BackendKey:    backendKey,
		Directory:     directory,
		Environment:   environment,
		SnapshotPath:  snapshotPath,
		VarFiles:      append([]string(nil), varFiles...),
	}); err != nil {
		return nil, err
	}
	snapshotFile, err := os.Open(snapshotPath)
	if err != nil {
		return nil, refreshFailure(
			"REFRESH_SNAPSHOT_UNAVAILABLE",
			"Terraform did not produce a refresh-only plan for "+label,
			procerr.CategoryIO,
		)
	}
	defer func() {
		if err := snapshotFile.Close(); err != nil && returnedErr == nil {
			returnedErr = refreshFailure(
				"REFRESH_SNAPSHOT_RETAINED",
				"unable to close the refresh-only plan for "+label,
				procerr.CategoryIO,
			)
		}
	}()
	shown, err := options.Terraform.Show(RefreshShowRequest{
		Directory:    directory,
		SnapshotFile: snapshotFile,
	})
	if err != nil {
		return nil, err
	}
	// Verify before applying, and apply the same open descriptor that was
	// verified rather than re-resolving the path.
	if err := requireStateOnlyRefreshPlan(shown, label); err != nil {
		return nil, err
	}
	if err := options.Terraform.Apply(RefreshApplyRequest{
		Directory:    directory,
		SnapshotFile: snapshotFile,
	}); err != nil {
		return nil, err
	}
	return refreshReconciledResources(shown), nil
}

// CreateRefreshTerraformOptions builds the production refresh adapter.
type CreateRefreshTerraformOptions struct {
	Environment         map[string]string
	Limits              *terraformcmd.TerraformCommandLimits
	ShowLimits          *terraformcmd.TerraformShowLimits
	TerraformExecutable string
}

type refreshTerraformAdapter struct {
	environment         map[string]string
	showEnvironment     map[string]string
	limits              *terraformcmd.TerraformCommandLimits
	showLimits          *terraformcmd.TerraformShowLimits
	terraformation      PlanTerraform
	terraformExecutable string
}

// CreateRefreshTerraform binds the refresh path to real Terraform. Init reuses
// the plan adapter so backend handling cannot drift between the two paths.
func CreateRefreshTerraform(options CreateRefreshTerraformOptions) (RefreshTerraform, error) {
	if options.Environment == nil {
		return nil, refreshFailure(
			"INVALID_REFRESH_REQUEST",
			"refresh Terraform requires an explicit environment",
			procerr.CategoryDomain,
		)
	}
	environment := cloneEnvironment(options.Environment)
	showEnvironment, err := terraformcmd.OperationalTerraformShowEnvironment(environment)
	if err != nil {
		return nil, err
	}
	limits := cloneTerraformLimits(options.Limits)
	return &refreshTerraformAdapter{
		environment:     environment,
		showEnvironment: showEnvironment,
		limits:          limits,
		showLimits:      options.ShowLimits,
		terraformation: CreatePlanTerraform(CreatePlanTerraformOptions{
			Environment:         environment,
			Limits:              limits,
			TerraformExecutable: options.TerraformExecutable,
		}),
		terraformExecutable: options.TerraformExecutable,
	}, nil
}

func (adapter *refreshTerraformAdapter) commandEnvironment(request map[string]string) map[string]string {
	environment := cloneEnvironment(adapter.environment)
	for key, value := range request {
		environment[key] = value
	}
	return environment
}

func (adapter *refreshTerraformAdapter) Initialize(request PlanTerraformRequest) error {
	return adapter.terraformation.Initialize(request)
}

// refreshPlanArgv is the whole reason this path cannot write remote state:
// -refresh-only is what makes Terraform produce a state-only plan, and -out is
// what lets the result be verified before it is applied.
func refreshPlanArgv(request RefreshPlanRequest) []string {
	argv := []string{"plan", "-input=false", "-refresh-only"}
	for _, file := range request.VarFiles {
		argv = append(argv, "-var-file="+file)
	}
	return append(argv, "-out="+request.SnapshotPath)
}

func (adapter *refreshTerraformAdapter) PlanRefreshOnly(request RefreshPlanRequest) error {
	_, err := terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: adapter.terraformExecutable,
		Argv:                refreshPlanArgv(request),
		CWD:                 request.Directory,
		Environment:         adapter.commandEnvironment(request.Environment),
		Limits:              cloneTerraformLimits(adapter.limits),
		Output:              terraformcmd.TerraformCommandOutputInherit,
	})
	return err
}

func (adapter *refreshTerraformAdapter) Show(request RefreshShowRequest) (canonjson.Value, error) {
	return terraformcmd.TerraformShowPlan(terraformcmd.TerraformShowOptions{
		TerraformExecutable: adapter.terraformExecutable,
		EnvDir:              request.Directory,
		SnapshotFile:        request.SnapshotFile,
		Environment:         cloneEnvironment(adapter.showEnvironment),
		Limits:              adapter.showLimits,
	})
}

func (adapter *refreshTerraformAdapter) Apply(request RefreshApplyRequest) error {
	if request.SnapshotFile == nil {
		return refreshFailure(
			"REFRESH_SNAPSHOT_UNAVAILABLE",
			"refresh-only plan is unavailable",
			procerr.CategoryIO,
		)
	}
	childSnapshotPath, err := terraformcmd.InheritedPlanFilePath()
	if err != nil {
		return refreshFailure(
			"REFRESH_SNAPSHOT_UNAVAILABLE",
			"refresh-only plan is unavailable",
			procerr.CategoryIO,
		)
	}
	_, err = terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: adapter.terraformExecutable,
		Argv:                []string{"apply", "-input=false", childSnapshotPath},
		CWD:                 request.Directory,
		Environment:         adapter.commandEnvironment(nil),
		Limits:              cloneTerraformLimits(adapter.limits),
		Output:              terraformcmd.TerraformCommandOutputInherit,
		SnapshotFile:        request.SnapshotFile,
	})
	return err
}
