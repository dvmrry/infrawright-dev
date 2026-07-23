package main

// commands_adopt_apply.go ports the Block D command-composition layer from
// the original implementation. The domain packages own adoption, import staging, and
// exact saved-plan Apply; this file owns only CLI parsing, environment/path
// precedence, lazy Terraform construction, diagnostics, and exit status.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/adopt"
	"github.com/dvmrry/infrawright-dev/go/internal/assessment"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
	"github.com/spf13/cobra"
)

type blockDCommandDependencies struct {
	applyExactSavedPlans       func(assessment.ExactPlanApplyOptions) (assessment.ExactPlanApplyResult, error)
	createApplyTerraform       func(assessment.CreateExactPlanApplyTerraformOptions) (assessment.ExactPlanApplyTerraform, error)
	createImportTerraform      func(adopt.ImportStagingTerraformOptions) adopt.ImportStagingTerraform
	createStateLoader          func(adopt.DefaultAdoptionLoaderOptions) (adopt.AdoptionStateLoader, error)
	currentApplyBranch         func(assessment.CurrentApplyBranchOptions) string
	currentDirectory           func() (string, error)
	deploymentPath             func(map[string]string) (string, error)
	environment                func() map[string]string
	loadAdoptionPolicy         func(metadata.LoadedPackRoot, *string) (*metadata.DriftPolicy, error)
	loadBoundDeployment        func(string, controlevidence.BindOptions) (deployment.BoundAssessmentDeployment, error)
	loadDeployment             func(string) (deployment.Deployment, error)
	loadPack                   func(metadata.LoadPackRootOptions) (metadata.LoadedPackRoot, error)
	packageRoot                func() (string, error)
	resolveTerraformExecutable func(string, map[string]string) (string, error)
	runAdoptBatch              func(adopt.RunAdoptBatchOptions) (adopt.AdoptBatchResult, error)
	stageImports               func(adopt.StageImportsOptions) (adopt.StageImportsResult, error)
	unstageImports             func(adopt.UnstageImportsOptions) (adopt.UnstageImportsResult, error)
	stderr                     io.Writer
}

func defaultBlockDCommandDependencies() blockDCommandDependencies {
	return blockDCommandDependencies{
		applyExactSavedPlans:  assessment.ApplyExactSavedPlans,
		createApplyTerraform:  assessment.CreateExactPlanApplyTerraform,
		createImportTerraform: adopt.CreateImportStagingTerraform,
		createStateLoader:     adopt.DefaultAdoptionStateLoader,
		currentApplyBranch:    assessment.CurrentApplyBranch,
		currentDirectory:      os.Getwd,
		deploymentPath: func(environment map[string]string) (string, error) {
			return deployment.DeploymentPath(deployment.DeploymentPathOptions{Environment: environment})
		},
		environment:                environMap,
		loadAdoptionPolicy:         adopt.LoadAdoptionPolicy,
		loadBoundDeployment:        deployment.LoadBoundAssessmentDeployment,
		loadDeployment:             deployment.LoadDeployment,
		loadPack:                   metadata.LoadPackRoot,
		packageRoot:                packageRoot,
		resolveTerraformExecutable: terraformcmd.ResolveTerraformExecutable,
		runAdoptBatch:              adopt.RunAdoptBatch,
		stageImports:               adopt.StageImports,
		unstageImports:             adopt.UnstageImports,
		stderr:                     os.Stderr,
	}
}

type adoptCLIOptions struct {
	pack        packOptionDefaults
	deployment  string
	input       string
	policy      *string
	resources   []string
	tenant      string
	terraform   *string
	environment map[string]string
}

func adoptCLIOptionsWithDependencies(parsed commandInput, dependencies blockDCommandDependencies) (adoptCLIOptions, error) {
	rootDirectory, err := dependencies.packageRoot()
	if err != nil {
		return adoptCLIOptions{}, err
	}
	environment := dependencies.environment()
	selectedDeployment, ok := lastCommandOption(parsed, "--deployment")
	if !ok {
		selectedDeployment, err = dependencies.deploymentPath(environment)
		if err != nil {
			return adoptCLIOptions{}, err
		}
	}
	input, hasInput := lastCommandOption(parsed, "--in")
	tenant, hasTenant := lastCommandOption(parsed, "--tenant")
	if !hasInput || !hasTenant {
		return adoptCLIOptions{}, usageError("adopt requires --in and --tenant")
	}
	options := adoptCLIOptions{
		pack: planPackOptions(rootDirectory, environment, parsed), deployment: selectedDeployment,
		input: input, resources: append([]string(nil), parsed.Options["--resource"]...), tenant: tenant,
		environment: cloneCommandEnvironment(environment),
	}
	if value, present := lastCommandOption(parsed, "--policy"); present {
		options.policy = &value
	}
	if value, present := lastCommandOption(parsed, "--terraform"); present {
		options.terraform = &value
	}
	return options, nil
}

type lazyAdoptionLoaders struct {
	dependencies blockDCommandDependencies
	environment  map[string]string
	root         metadata.LoadedPackRoot
	selected     *string
	onDiagnostic func(string)

	executable       string
	executableLoaded bool
	executableErr    error
	state            adopt.AdoptionStateLoader
	stateErr         error
}

func (loaders *lazyAdoptionLoaders) terraformExecutable() (string, error) {
	if !loaders.executableLoaded {
		loaders.executableLoaded = true
		selected := loaders.environment["TF"]
		if loaders.selected != nil {
			selected = *loaders.selected
		}
		loaders.executable, loaders.executableErr = loaders.dependencies.resolveTerraformExecutable(
			selected,
			cloneCommandEnvironment(loaders.environment),
		)
	}
	return loaders.executable, loaders.executableErr
}

func (loaders *lazyAdoptionLoaders) stateLoader(request adopt.AdoptionStateRequest) (map[string]adopt.OracleStateObject, error) {
	if loaders.state == nil && loaders.stateErr == nil {
		executable, err := loaders.terraformExecutable()
		if err != nil {
			loaders.stateErr = err
		} else {
			loaders.state, loaders.stateErr = loaders.dependencies.createStateLoader(adopt.DefaultAdoptionLoaderOptions{
				Environment: cloneCommandEnvironment(loaders.environment), OnDiagnostic: loaders.onDiagnostic,
				Root: loaders.root, TerraformExecutable: executable,
			})
		}
	}
	if loaders.stateErr != nil {
		return nil, loaders.stateErr
	}
	return loaders.state(request)
}

func adoptCommand(arguments []string) (int, error) {
	return adoptCommandWithDependencies(arguments, defaultBlockDCommandDependencies())
}

func adoptCommandWithDependencies(arguments []string, dependencies blockDCommandDependencies) (int, error) {
	return executeStandaloneCobra(newAdoptCobraCommand(dependencies), arguments)
}

func newAdoptCobraCommand(dependencies blockDCommandDependencies) *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: "adopt", short: "Transform pulled JSON through the import oracle",
		valueFlags: []string{"--in", "--tenant", "--resource", "--policy", "--terraform", "--deployment", "--root", "--profile"},
		run: func(parsed commandInput) (int, error) {
			return legacyPlanLifecycleCommand(func() (int, error) {
				if len(parsed.Positionals) != 0 {
					return 0, usageError("adopt does not accept positional arguments")
				}
				return adoptCommandInput(parsed, dependencies)
			})
		},
	})
}

func adoptCommandInput(parsed commandInput, dependencies blockDCommandDependencies) (int, error) {
	options, err := adoptCLIOptionsWithDependencies(parsed, dependencies)
	if err != nil {
		return 0, err
	}
	loadedRoot, err := dependencies.loadPack(metadata.LoadPackRootOptions{
		PacksRoot: options.pack.root, ProfilePath: &options.pack.profile,
	})
	if err != nil {
		return 0, err
	}
	loadedDeployment, err := dependencies.loadDeployment(options.deployment)
	if err != nil {
		return 0, err
	}
	policy, err := dependencies.loadAdoptionPolicy(loadedRoot, options.policy)
	if err != nil {
		return 0, err
	}
	diagnostic := func(message string) { fmt.Fprintln(dependencies.stderr, message) }
	loaders := &lazyAdoptionLoaders{
		dependencies: dependencies, environment: cloneCommandEnvironment(options.environment), root: loadedRoot,
		selected: options.terraform, onDiagnostic: diagnostic,
	}
	result, err := dependencies.runAdoptBatch(adopt.RunAdoptBatchOptions{
		Deployment: loadedDeployment, InputDirectory: options.input,
		OnDiagnostic: diagnostic, Policy: policy, Root: loadedRoot,
		Selectors: options.resources, StateLoader: loaders.stateLoader, Tenant: options.tenant,
	})
	if err != nil {
		return 0, err
	}
	if len(result.Failed) > 0 {
		return 1, nil
	}
	return 0, nil
}

type importStagingCLIOptions struct {
	backendConfig *string
	pack          packOptionDefaults
	deployment    string
	resources     []string
	stateAware    bool
	tenant        string
	terraform     *string
	environment   map[string]string
}

func importStagingCLIOptionsWithDependencies(parsed commandInput, command string, dependencies blockDCommandDependencies) (importStagingCLIOptions, error) {
	rootDirectory, err := dependencies.packageRoot()
	if err != nil {
		return importStagingCLIOptions{}, err
	}
	environment := dependencies.environment()
	selectedDeployment, ok := lastCommandOption(parsed, "--deployment")
	if !ok {
		selectedDeployment, err = dependencies.deploymentPath(environment)
		if err != nil {
			return importStagingCLIOptions{}, err
		}
	}
	tenant, ok := lastCommandOption(parsed, "--tenant")
	if !ok {
		return importStagingCLIOptions{}, usageError(command + " requires --tenant")
	}
	options := importStagingCLIOptions{
		pack: planPackOptions(rootDirectory, environment, parsed), deployment: selectedDeployment,
		resources:  append([]string(nil), parsed.Options["--resource"]...),
		stateAware: parsed.Flags.Has("--state-aware"), tenant: tenant,
		environment: cloneCommandEnvironment(environment),
	}
	if value, present := lastCommandOption(parsed, "--backend-config"); present {
		options.backendConfig = &value
	}
	if value, present := lastCommandOption(parsed, "--terraform"); present {
		options.terraform = &value
	}
	return options, nil
}

type lazyImportStagingTerraform struct {
	dependencies blockDCommandDependencies
	environment  map[string]string
	selected     *string
	adapter      adopt.ImportStagingTerraform
	err          error
	loaded       bool
}

func (adapter *lazyImportStagingTerraform) load() (adopt.ImportStagingTerraform, error) {
	if !adapter.loaded {
		adapter.loaded = true
		selected := adapter.environment["TF"]
		if adapter.selected != nil {
			selected = *adapter.selected
		}
		var executable string
		executable, adapter.err = adapter.dependencies.resolveTerraformExecutable(selected, cloneCommandEnvironment(adapter.environment))
		if adapter.err == nil {
			adapter.adapter = adapter.dependencies.createImportTerraform(adopt.ImportStagingTerraformOptions{
				Environment: cloneCommandEnvironment(adapter.environment), TerraformExecutable: executable,
			})
		}
	}
	return adapter.adapter, adapter.err
}

func (adapter *lazyImportStagingTerraform) Initialize(request adopt.ImportStagingTerraformRequest) error {
	loaded, err := adapter.load()
	if err != nil {
		return err
	}
	return loaded.Initialize(request)
}

func (adapter *lazyImportStagingTerraform) ListState(request adopt.ImportStagingTerraformRequest) (adopt.ImportStagingStateResult, error) {
	loaded, err := adapter.load()
	if err != nil {
		return adopt.ImportStagingStateResult{}, err
	}
	return loaded.ListState(request)
}

func stageImportsCommand(arguments []string) (int, error) {
	return stageImportsCommandWithDependencies(arguments, defaultBlockDCommandDependencies())
}

func stageImportsCommandWithDependencies(arguments []string, dependencies blockDCommandDependencies) (int, error) {
	return executeStandaloneCobra(newImportStagingCobraCommand("stage-imports", dependencies), arguments)
}

func newImportStagingCobraCommand(command string, dependencies blockDCommandDependencies) *cobra.Command {
	valueFlags := []string{"--tenant", "--resource", "--deployment", "--root", "--profile"}
	var boolFlags []string
	short := "Remove staged import and moved blocks"
	if command == "stage-imports" {
		valueFlags = append(valueFlags, "--backend-config", "--terraform")
		boolFlags = []string{"--state-aware"}
		short = "Stage import and moved blocks"
	}
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: command, short: short, valueFlags: valueFlags, boolFlags: boolFlags,
		run: func(parsed commandInput) (int, error) {
			return legacyPlanLifecycleCommand(func() (int, error) {
				if len(parsed.Positionals) != 0 {
					return 0, usageError(command + " does not accept positional arguments")
				}
				if command == "stage-imports" {
					return stageImportsCommandInput(parsed, dependencies)
				}
				return unstageImportsCommandInput(parsed, dependencies)
			})
		},
	})
}

func stageImportsCommandInput(parsed commandInput, dependencies blockDCommandDependencies) (int, error) {
	options, err := importStagingCLIOptionsWithDependencies(parsed, "stage-imports", dependencies)
	if err != nil {
		return 0, err
	}
	loadedRoot, loadedDeployment, err := loadBlockDInputs(options.pack, options.deployment, dependencies)
	if err != nil {
		return 0, err
	}
	workspace, err := dependencies.currentDirectory()
	if err != nil {
		return 0, err
	}
	var terraform adopt.ImportStagingTerraform
	if options.stateAware {
		terraform = &lazyImportStagingTerraform{
			dependencies: dependencies, environment: cloneCommandEnvironment(options.environment), selected: options.terraform,
		}
	}
	_, err = dependencies.stageImports(adopt.StageImportsOptions{
		BackendConfig: options.backendConfig, Deployment: loadedDeployment,
		OnDiagnostic: func(message string) { fmt.Fprintln(dependencies.stderr, message) },
		Root:         loadedRoot, Selectors: options.resources, StateAware: options.stateAware,
		Tenant: options.tenant, Terraform: terraform, Workspace: workspace,
	})
	return 0, err
}

func unstageImportsCommand(arguments []string) (int, error) {
	return unstageImportsCommandWithDependencies(arguments, defaultBlockDCommandDependencies())
}

func unstageImportsCommandWithDependencies(arguments []string, dependencies blockDCommandDependencies) (int, error) {
	return executeStandaloneCobra(newImportStagingCobraCommand("unstage-imports", dependencies), arguments)
}

func unstageImportsCommandInput(parsed commandInput, dependencies blockDCommandDependencies) (int, error) {
	options, err := importStagingCLIOptionsWithDependencies(parsed, "unstage-imports", dependencies)
	if err != nil {
		return 0, err
	}
	loadedRoot, loadedDeployment, err := loadBlockDInputs(options.pack, options.deployment, dependencies)
	if err != nil {
		return 0, err
	}
	workspace, err := dependencies.currentDirectory()
	if err != nil {
		return 0, err
	}
	_, err = dependencies.unstageImports(adopt.UnstageImportsOptions{
		Deployment: loadedDeployment, OnDiagnostic: func(message string) { fmt.Fprintln(dependencies.stderr, message) },
		Root: loadedRoot, Selectors: options.resources, Tenant: options.tenant, Workspace: workspace,
	})
	return 0, err
}

func loadBlockDInputs(options packOptionDefaults, deploymentPath string, dependencies blockDCommandDependencies) (metadata.LoadedPackRoot, deployment.Deployment, error) {
	loadedRoot, err := dependencies.loadPack(metadata.LoadPackRootOptions{
		PacksRoot: options.root, ProfilePath: &options.profile,
	})
	if err != nil {
		return metadata.LoadedPackRoot{}, deployment.Deployment{}, err
	}
	loadedDeployment, err := dependencies.loadDeployment(deploymentPath)
	return loadedRoot, loadedDeployment, err
}

type applyCLIOptions struct {
	allowDestroy     bool
	allowNonMain     bool
	allowPlanChanges bool
	backendConfig    *string
	pack             packOptionDefaults
	deployment       string
	mainBranch       *string
	policy           *string
	resources        []string
	tenant           *string
	terraform        *string
	environment      map[string]string
}

func applyCLIOptionsWithDependencies(parsed commandInput, dependencies blockDCommandDependencies) (applyCLIOptions, error) {
	rootDirectory, err := dependencies.packageRoot()
	if err != nil {
		return applyCLIOptions{}, err
	}
	environment := dependencies.environment()
	selectedDeployment, ok := lastCommandOption(parsed, "--deployment")
	if !ok {
		selectedDeployment, err = dependencies.deploymentPath(environment)
		if err != nil {
			return applyCLIOptions{}, err
		}
	}
	options := applyCLIOptions{
		allowDestroy: parsed.Flags.Has("--allow-destroy"), allowNonMain: parsed.Flags.Has("--allow-non-main"),
		allowPlanChanges: parsed.Flags.Has("--allow-plan-changes"),
		pack:             planPackOptions(rootDirectory, environment, parsed), deployment: selectedDeployment,
		resources: append([]string(nil), parsed.Options["--resource"]...), environment: cloneCommandEnvironment(environment),
	}
	if value, present := lastCommandOption(parsed, "--tenant"); present {
		if err := roots.ValidateTenant(value); err != nil {
			return applyCLIOptions{}, err
		}
		options.tenant = &value
	}
	for name, target := range map[string]**string{
		"--backend-config": &options.backendConfig, "--main-branch": &options.mainBranch,
		"--policy": &options.policy, "--terraform": &options.terraform,
	} {
		if value, present := lastCommandOption(parsed, name); present {
			copyValue := value
			*target = &copyValue
		}
	}
	return options, nil
}

type lazyExactPlanApplyTerraform struct {
	dependencies blockDCommandDependencies
	environment  map[string]string
	selected     *string
	adapter      assessment.ExactPlanApplyTerraform
	err          error
	loaded       bool
}

func (adapter *lazyExactPlanApplyTerraform) load() (assessment.ExactPlanApplyTerraform, error) {
	if !adapter.loaded {
		adapter.loaded = true
		selected := adapter.environment["TF"]
		if adapter.selected != nil {
			selected = *adapter.selected
		}
		var executable string
		executable, adapter.err = adapter.dependencies.resolveTerraformExecutable(selected, cloneCommandEnvironment(adapter.environment))
		if adapter.err == nil {
			adapter.adapter, adapter.err = adapter.dependencies.createApplyTerraform(assessment.CreateExactPlanApplyTerraformOptions{
				Environment: cloneCommandEnvironment(adapter.environment), TerraformExecutable: executable,
			})
		}
	}
	return adapter.adapter, adapter.err
}

func (adapter *lazyExactPlanApplyTerraform) Initialize(request plan.PlanTerraformRequest) error {
	loaded, err := adapter.load()
	if err != nil {
		return err
	}
	return loaded.Initialize(request)
}

func (adapter *lazyExactPlanApplyTerraform) Show(request assessment.ExactPlanApplyShowRequest) (assessment.ExactPlanApplyShownPlan, error) {
	loaded, err := adapter.load()
	if err != nil {
		return assessment.ExactPlanApplyShownPlan{}, err
	}
	return loaded.Show(request)
}

func (adapter *lazyExactPlanApplyTerraform) Apply(request assessment.ExactPlanApplyRequest) error {
	loaded, err := adapter.load()
	if err != nil {
		return err
	}
	return loaded.Apply(request)
}

func cloneCommandEnvironment(environment map[string]string) map[string]string {
	cloned := make(map[string]string, len(environment))
	for name, value := range environment {
		cloned[name] = value
	}
	return cloned
}

func resolveCommandPath(workspace, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(workspace, value)
}

func applyCommand(arguments []string) (int, error) {
	return applyCommandWithDependencies(arguments, defaultBlockDCommandDependencies())
}

func applyCommandWithDependencies(arguments []string, dependencies blockDCommandDependencies) (int, error) {
	return executeStandaloneCobra(newApplyCobraCommand(dependencies), arguments)
}

func newApplyCobraCommand(dependencies blockDCommandDependencies) *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: "apply", short: "Apply exact saved Terraform plans",
		valueFlags: []string{"--tenant", "--resource", "--policy", "--backend-config", "--main-branch", "--terraform", "--deployment", "--root", "--profile"},
		allowEmpty: []string{"--tenant"},
		boolFlags:  []string{"--allow-destroy", "--allow-non-main", "--allow-plan-changes"},
		run: func(parsed commandInput) (int, error) {
			return legacyPlanLifecycleCommand(func() (int, error) {
				if len(parsed.Positionals) != 0 {
					return 0, usageError("apply does not accept positional arguments")
				}
				return applyCommandInput(parsed, dependencies)
			})
		},
	})
}

func applyCommandInput(parsed commandInput, dependencies blockDCommandDependencies) (int, error) {
	options, err := applyCLIOptionsWithDependencies(parsed, dependencies)
	if err != nil {
		return 0, err
	}
	workspace, err := dependencies.currentDirectory()
	if err != nil {
		return 0, err
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return 0, err
	}
	var backendConfig *string
	if options.backendConfig != nil {
		value := resolveCommandPath(workspace, *options.backendConfig)
		backendConfig = &value
	}
	var policyPath *string
	if options.policy != nil {
		value := resolveCommandPath(workspace, *options.policy)
		policyPath = &value
	}
	terraform := &lazyExactPlanApplyTerraform{
		dependencies: dependencies, environment: cloneCommandEnvironment(options.environment), selected: options.terraform,
	}
	_, err = dependencies.applyExactSavedPlans(assessment.ExactPlanApplyOptions{
		Workspace: workspace, Tenant: options.tenant, Selectors: options.resources,
		BackendConfig: backendConfig, PolicyPath: policyPath, MainBranch: options.mainBranch,
		AllowDestroy: options.allowDestroy, AllowNonMain: options.allowNonMain,
		AllowPlanChanges: options.allowPlanChanges,
		CurrentBranch: func() string {
			return dependencies.currentApplyBranch(assessment.CurrentApplyBranchOptions{
				CWD: workspace, Environment: cloneCommandEnvironment(options.environment),
			})
		},
		LoadInputs: func() (assessment.ExactPlanApplyInputs, error) {
			loadedRoot, loadErr := dependencies.loadPack(metadata.LoadPackRootOptions{
				PacksRoot: options.pack.root, ProfilePath: &options.pack.profile,
			})
			if loadErr != nil {
				return assessment.ExactPlanApplyInputs{}, loadErr
			}
			boundDeployment, loadErr := dependencies.loadBoundDeployment(
				resolveCommandPath(workspace, options.deployment),
				controlevidence.BindOptions{},
			)
			if loadErr != nil {
				return assessment.ExactPlanApplyInputs{}, loadErr
			}
			return assessment.ExactPlanApplyInputs{
				Deployment: boundDeployment.Deployment, Root: loadedRoot,
				ControlFiles: []controlevidence.BoundAssessmentControlFile{boundDeployment.File},
			}, nil
		},
		Terraform:    terraform,
		OnDiagnostic: func(message string) { fmt.Fprintln(dependencies.stderr, message) },
	})
	return 0, err
}
