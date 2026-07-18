package main

// commands_plan.go ports the plan and clean-plans CLI composition layer from
// node-src/cli/main.ts. The saved-plan lifecycle remains in internal/plan;
// this file owns only argument and environment precedence, lazy Terraform
// adapter construction, diagnostics, and workspace composition.
//
// Node's supported-platform check runs before command dispatch (except for
// standalone help), so it deliberately does not live in planCommand. The C4
// dispatch patch must retain that ordering; clean-plans never takes the gate.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/cliargs"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

type planCLIOptions struct {
	backendConfig *string
	pack          packOptionDefaults
	deployment    string
	importsOnly   bool
	resources     []string
	save          bool
	tenant        string
	terraform     *string
}

// planCommandDependencies is the narrow command-composition seam used by the
// focused CLI tests. Production binds it directly to the already-qualified
// deployment, plan, and Terraform packages; it is not a second runtime
// abstraction.
type planCommandDependencies struct {
	createPlanTerraform        func(plan.CreatePlanTerraformOptions) plan.PlanTerraform
	currentDirectory           func() (string, error)
	deploymentPath             func(map[string]string) (string, error)
	environment                func() map[string]string
	loadPackAndDeployment      func(packOptionDefaults, string) (metadata.LoadedPackRoot, deployment.Deployment, error)
	packageRoot                func() (string, error)
	planEnvironmentRoots       func(plan.PlanEnvironmentRootsOptions) (plan.PlanRunResult, error)
	cleanPlans                 func(plan.CleanPlansOptions) (plan.CleanPlansResult, error)
	resolveTerraformExecutable func(string, map[string]string) (string, error)
	stderr                     io.Writer
}

func defaultPlanCommandDependencies() planCommandDependencies {
	return planCommandDependencies{
		createPlanTerraform: plan.CreatePlanTerraform,
		currentDirectory:    os.Getwd,
		deploymentPath: func(environment map[string]string) (string, error) {
			return deployment.DeploymentPath(deployment.DeploymentPathOptions{
				Environment: environment,
			})
		},
		environment:                environMap,
		loadPackAndDeployment:      loadPackAndDeployment,
		packageRoot:                packageRoot,
		planEnvironmentRoots:       plan.PlanEnvironmentRoots,
		cleanPlans:                 plan.CleanPlans,
		resolveTerraformExecutable: terraformcmd.ResolveTerraformExecutable,
		stderr:                     os.Stderr,
	}
}

func planPackOptions(
	rootDirectory string,
	environment map[string]string,
	parsed cliargs.ParsedArguments,
) packOptionDefaults {
	options := packOptionDefaults{
		root:    filepath.Join(rootDirectory, "packs"),
		profile: filepath.Join(rootDirectory, "packsets", "full.json"),
		catalog: filepath.Join(rootDirectory, "packsets", "full.json"),
	}
	if value := environment["INFRAWRIGHT_PACKS"]; value != "" {
		options.root = value
	}
	if value := environment["INFRAWRIGHT_PACK_PROFILE"]; value != "" {
		options.profile = value
	}
	if value, ok := cliargs.LastOption(parsed, "--root"); ok {
		options.root = value
	}
	if value, ok := cliargs.LastOption(parsed, "--profile"); ok {
		options.profile = value
	}
	if value, ok := cliargs.LastOption(parsed, "--catalog"); ok {
		options.catalog = value
	}
	return options
}

// planCliOptions ports planCliOptions from node-src/cli/main.ts.
func planCliOptionsWithDependencies(
	arguments []string,
	dependencies planCommandDependencies,
) (planCLIOptions, error) {
	rootDirectory, err := dependencies.packageRoot()
	if err != nil {
		return planCLIOptions{}, err
	}
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		Flags: []string{"--imports-only", "--save"},
		Values: map[string]cliargs.ValueOption{
			"--backend-config": {},
			"--catalog":        {},
			"--deployment":     {},
			"--profile":        {},
			"--resource":       {},
			"--root":           {},
			"--tenant":         {AllowEmpty: true},
			"--terraform":      {},
		},
	}, commandBehavior{command: "plan"})
	if err != nil {
		return planCLIOptions{}, err
	}
	environment := dependencies.environment()
	deploymentPathValue, hasDeployment := cliargs.LastOption(parsed, "--deployment")
	if !hasDeployment {
		deploymentPathValue, err = dependencies.deploymentPath(environment)
		if err != nil {
			return planCLIOptions{}, err
		}
	}
	tenant, hasTenant := cliargs.LastOption(parsed, "--tenant")
	if !hasTenant {
		return planCLIOptions{}, usageError("plan requires --tenant")
	}
	options := planCLIOptions{
		pack:        planPackOptions(rootDirectory, environment, parsed),
		deployment:  deploymentPathValue,
		importsOnly: parsed.Flags.Has("--imports-only"),
		resources:   append([]string(nil), parsed.Options["--resource"]...),
		save:        parsed.Flags.Has("--save"),
		tenant:      tenant,
	}
	if backendConfig, ok := cliargs.LastOption(parsed, "--backend-config"); ok {
		options.backendConfig = &backendConfig
	}
	if terraform, ok := cliargs.LastOption(parsed, "--terraform"); ok {
		options.terraform = &terraform
	}
	return options, nil
}

type lazyPlanTerraform struct {
	create      func(plan.CreatePlanTerraformOptions) plan.PlanTerraform
	environment func() map[string]string
	resolve     func(string, map[string]string) (string, error)
	selected    *string

	adapter     plan.PlanTerraform
	initialized bool
	resolveErr  error
}

func (adapter *lazyPlanTerraform) Initialize(request plan.PlanTerraformRequest) error {
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
			adapter.adapter = adapter.create(plan.CreatePlanTerraformOptions{
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

func (adapter *lazyPlanTerraform) Plan(request plan.PlanTerraformRequest) error {
	if !adapter.initialized {
		return errors.New("Terraform plan adapter was used before initialization")
	}
	if adapter.resolveErr != nil {
		return adapter.resolveErr
	}
	return adapter.adapter.Plan(request)
}

// planCommand ports planCommand from node-src/cli/main.ts.
func planCommand(arguments []string) (int, error) {
	return planCommandWithDependencies(arguments, defaultPlanCommandDependencies())
}

func planCommandWithDependencies(
	arguments []string,
	dependencies planCommandDependencies,
) (int, error) {
	options, err := planCliOptionsWithDependencies(arguments, dependencies)
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
	adapter := &lazyPlanTerraform{
		create:      dependencies.createPlanTerraform,
		environment: dependencies.environment,
		resolve:     dependencies.resolveTerraformExecutable,
		selected:    options.terraform,
	}
	_, err = dependencies.planEnvironmentRoots(plan.PlanEnvironmentRootsOptions{
		BackendConfig: options.backendConfig,
		Deployment:    loadedDeployment,
		ImportsOnly:   options.importsOnly,
		OnDiagnostic: func(message string) {
			fmt.Fprintf(dependencies.stderr, "%s\n", message)
		},
		Root:      loadedRoot,
		Save:      options.save,
		Selectors: options.resources,
		Tenant:    options.tenant,
		Terraform: adapter,
		Workspace: workspace,
	})
	if err != nil {
		return 0, err
	}
	return 0, nil
}

// cleanPlansCommand ports cleanPlansCommand from node-src/cli/main.ts.
func cleanPlansCommand(arguments []string) (int, error) {
	return cleanPlansCommandWithDependencies(arguments, defaultPlanCommandDependencies())
}

func cleanPlansCommandWithDependencies(
	arguments []string,
	dependencies planCommandDependencies,
) (int, error) {
	rootDirectory, err := dependencies.packageRoot()
	if err != nil {
		return 0, err
	}
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		Values: map[string]cliargs.ValueOption{
			"--catalog":    {},
			"--deployment": {},
			"--profile":    {},
			"--resource":   {},
			"--root":       {},
			"--tenant":     {AllowEmpty: true, RejectDuplicates: true},
		},
	}, commandBehavior{command: "clean-plans"})
	if err != nil {
		return 0, err
	}
	environment := dependencies.environment()
	deploymentPathValue, hasDeployment := cliargs.LastOption(parsed, "--deployment")
	if !hasDeployment {
		deploymentPathValue, err = dependencies.deploymentPath(environment)
		if err != nil {
			return 0, err
		}
	}
	loadedRoot, loadedDeployment, err := dependencies.loadPackAndDeployment(
		planPackOptions(rootDirectory, environment, parsed),
		deploymentPathValue,
	)
	if err != nil {
		return 0, err
	}
	workspace, err := dependencies.currentDirectory()
	if err != nil {
		return 0, err
	}
	var tenant *string
	if value, ok := cliargs.LastOption(parsed, "--tenant"); ok {
		tenant = &value
	}
	_, err = dependencies.cleanPlans(plan.CleanPlansOptions{
		Deployment: loadedDeployment,
		OnDiagnostic: func(message string) {
			fmt.Fprintf(dependencies.stderr, "%s\n", message)
		},
		Root:      loadedRoot,
		Selectors: append([]string(nil), parsed.Options["--resource"]...),
		Tenant:    tenant,
		Workspace: workspace,
	})
	if err != nil {
		return 0, err
	}
	return 0, nil
}
