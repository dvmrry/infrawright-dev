package main

// commands_refresh.go owns the composition layer for `iw refresh`: the
// reconciliation command that writes refreshed values into state and touches
// no remote object. The operation itself lives in internal/plan; this file
// owns only argument precedence, lazy Terraform adapter construction, and
// diagnostics.
//
// The command takes no --allow-* flag by design. Its safety comes from the
// operation's semantics, which internal/plan enforces by refusing any
// refresh-only plan that is not state-only.

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
	"github.com/spf13/cobra"
)

type refreshCLIOptions struct {
	backendConfig *string
	pack          packOptionDefaults
	deployment    string
	resources     []string
	tenant        string
	terraform     *string
}

type refreshCommandDependencies struct {
	createRefreshTerraform     func(plan.CreateRefreshTerraformOptions) (plan.RefreshTerraform, error)
	currentDirectory           func() (string, error)
	deploymentPath             func(map[string]string) (string, error)
	environment                func() map[string]string
	loadPackAndDeployment      func(packOptionDefaults, string) (metadata.LoadedPackRoot, deployment.Deployment, error)
	packageRoot                func() (string, error)
	refreshEnvironmentRoots    func(plan.RefreshEnvironmentRootsOptions) (plan.RefreshRunResult, error)
	resolveTerraformExecutable func(string, map[string]string) (string, error)
	stderr                     io.Writer
}

func defaultRefreshCommandDependencies() refreshCommandDependencies {
	planDependencies := defaultPlanCommandDependencies()
	return refreshCommandDependencies{
		createRefreshTerraform:     plan.CreateRefreshTerraform,
		currentDirectory:           planDependencies.currentDirectory,
		deploymentPath:             planDependencies.deploymentPath,
		environment:                planDependencies.environment,
		loadPackAndDeployment:      loadPackAndDeployment,
		packageRoot:                planDependencies.packageRoot,
		refreshEnvironmentRoots:    plan.RefreshEnvironmentRoots,
		resolveTerraformExecutable: terraformcmd.ResolveTerraformExecutable,
		stderr:                     os.Stderr,
	}
}

func refreshCliOptionsWithDependencies(
	parsed commandInput,
	dependencies refreshCommandDependencies,
) (refreshCLIOptions, error) {
	rootDirectory, err := dependencies.packageRoot()
	if err != nil {
		return refreshCLIOptions{}, err
	}
	environment := dependencies.environment()
	deploymentPathValue, hasDeployment := lastCommandOption(parsed, "--deployment")
	if !hasDeployment {
		deploymentPathValue, err = dependencies.deploymentPath(environment)
		if err != nil {
			return refreshCLIOptions{}, err
		}
	}
	tenant, hasTenant := lastCommandOption(parsed, "--tenant")
	if !hasTenant {
		return refreshCLIOptions{}, usageError("refresh requires --tenant")
	}
	options := refreshCLIOptions{
		pack:       planPackOptions(rootDirectory, environment, parsed),
		deployment: deploymentPathValue,
		resources:  append([]string(nil), parsed.Options["--resource"]...),
		tenant:     tenant,
	}
	if backendConfig, ok := lastCommandOption(parsed, "--backend-config"); ok {
		options.backendConfig = &backendConfig
	}
	if terraform, ok := lastCommandOption(parsed, "--terraform"); ok {
		options.terraform = &terraform
	}
	return options, nil
}

// lazyRefreshTerraform defers executable resolution to the first Terraform
// call, matching lazyPlanTerraform: a run that selects no root never demands a
// Terraform binary.
type lazyRefreshTerraform struct {
	create      func(plan.CreateRefreshTerraformOptions) (plan.RefreshTerraform, error)
	environment func() map[string]string
	resolve     func(string, map[string]string) (string, error)
	selected    *string

	adapter     plan.RefreshTerraform
	initialized bool
	resolveErr  error
}

func (adapter *lazyRefreshTerraform) Initialize(request plan.PlanTerraformRequest) error {
	if !adapter.initialized {
		adapter.initialized = true
		environment := adapter.environment()
		selected := environment["TF"]
		if adapter.selected != nil {
			selected = *adapter.selected
		}
		terraformExecutable, err := adapter.resolve(selected, environment)
		if err != nil {
			adapter.resolveErr = err
		} else {
			adapter.adapter, adapter.resolveErr = adapter.create(plan.CreateRefreshTerraformOptions{
				Environment:         environment,
				TerraformExecutable: terraformExecutable,
			})
		}
	}
	if adapter.resolveErr != nil {
		return adapter.resolveErr
	}
	return adapter.adapter.Initialize(request)
}

func (adapter *lazyRefreshTerraform) ready() error {
	if !adapter.initialized {
		return errors.New("Terraform refresh adapter was used before initialization")
	}
	return adapter.resolveErr
}

func (adapter *lazyRefreshTerraform) PlanRefreshOnly(request plan.RefreshPlanRequest) error {
	if err := adapter.ready(); err != nil {
		return err
	}
	return adapter.adapter.PlanRefreshOnly(request)
}

func (adapter *lazyRefreshTerraform) Show(request plan.RefreshShowRequest) (canonjson.Value, error) {
	if err := adapter.ready(); err != nil {
		return nil, err
	}
	return adapter.adapter.Show(request)
}

func (adapter *lazyRefreshTerraform) Apply(request plan.RefreshApplyRequest) error {
	if err := adapter.ready(); err != nil {
		return err
	}
	return adapter.adapter.Apply(request)
}

func refreshCommandWithDependencies(
	arguments []string,
	dependencies refreshCommandDependencies,
) (int, error) {
	return executeStandaloneCobra(newRefreshCobraCommand(dependencies), arguments)
}

func newRefreshCobraCommand(dependencies refreshCommandDependencies) *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use:   "refresh",
		short: "Reconcile recorded state with reality without changing anything remote",
		valueFlags: []string{
			"--tenant", "--resource", "--backend-config",
			"--terraform", "--deployment", "--root", "--profile",
		},
		allowEmpty: []string{"--tenant"},
		run: func(parsed commandInput) (int, error) {
			return legacyPlanLifecycleCommand(func() (int, error) {
				if len(parsed.Positionals) != 0 {
					return 0, usageError("refresh does not accept positional arguments")
				}
				return refreshCommandInput(parsed, dependencies)
			})
		},
	})
}

func refreshCommandInput(parsed commandInput, dependencies refreshCommandDependencies) (int, error) {
	options, err := refreshCliOptionsWithDependencies(parsed, dependencies)
	if err != nil {
		return 0, err
	}
	loadedRoot, loadedDeployment, err := dependencies.loadPackAndDeployment(
		options.pack,
		options.deployment,
	)
	if err != nil {
		return 0, err
	}
	workspace, err := dependencies.currentDirectory()
	if err != nil {
		return 0, err
	}
	adapter := &lazyRefreshTerraform{
		create:      dependencies.createRefreshTerraform,
		environment: dependencies.environment,
		resolve:     dependencies.resolveTerraformExecutable,
		selected:    options.terraform,
	}
	emit := func(message string) {
		fmt.Fprintf(dependencies.stderr, "%s\n", message)
	}
	result, err := dependencies.refreshEnvironmentRoots(plan.RefreshEnvironmentRootsOptions{
		BackendConfig: options.backendConfig,
		Deployment:    loadedDeployment,
		OnDiagnostic:  emit,
		Root:          loadedRoot,
		Selectors:     options.resources,
		Tenant:        options.tenant,
		Terraform:     adapter,
		Workspace:     workspace,
	})
	if err != nil {
		return 0, err
	}
	emit(fmt.Sprintf(
		"%d root(s) refreshed; %d resource(s) reconciled",
		result.Refreshed,
		result.Reconciled,
	))
	return 0, nil
}
