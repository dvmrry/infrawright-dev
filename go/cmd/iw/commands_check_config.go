package main

// commands_check_config.go owns the composition layer for `iw check-config`:
// the credential-free assertion that every resource type a consumer commits
// config for can also be fetched.
//
// It sits in the same lane as check-pack and check-pack-set deliberately.
// Those validate each file's shape; this one is the only check that looks
// across the boundary between committed config and the registry, which is
// where a dropped fetch block hides.

import (
	"fmt"
	"io"
	"os"

	"github.com/dvmrry/infrawright-dev/go/internal/configcheck"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/spf13/cobra"
)

type checkConfigCommandDependencies struct {
	checkFetchable        func(configcheck.CheckFetchableOptions) (configcheck.CheckFetchableResult, error)
	currentDirectory      func() (string, error)
	deploymentPath        func(map[string]string) (string, error)
	environment           func() map[string]string
	loadPackAndDeployment func(packOptionDefaults, string) (metadata.LoadedPackRoot, deployment.Deployment, error)
	packageRoot           func() (string, error)
	stderr                io.Writer
}

func defaultCheckConfigCommandDependencies() checkConfigCommandDependencies {
	planDependencies := defaultPlanCommandDependencies()
	return checkConfigCommandDependencies{
		checkFetchable:        configcheck.CheckFetchable,
		currentDirectory:      planDependencies.currentDirectory,
		deploymentPath:        planDependencies.deploymentPath,
		environment:           planDependencies.environment,
		loadPackAndDeployment: loadPackAndDeployment,
		packageRoot:           planDependencies.packageRoot,
		stderr:                os.Stderr,
	}
}

func newCheckConfigCobraCommand(dependencies checkConfigCommandDependencies) *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use:   "check-config",
		short: "Require every committed resource type to be fetchable",
		valueFlags: []string{
			"--tenant", "--deployment", "--root", "--profile",
		},
		allowEmpty: []string{"--tenant"},
		run: func(parsed commandInput) (int, error) {
			if len(parsed.Positionals) != 0 {
				return 0, usageError("check-config does not accept positional arguments")
			}
			return checkConfigInput(parsed, dependencies)
		},
	})
}

func checkConfigCommand(arguments []string) (int, error) {
	return checkConfigCommandWithDependencies(arguments, defaultCheckConfigCommandDependencies())
}

func checkConfigCommandWithDependencies(
	arguments []string,
	dependencies checkConfigCommandDependencies,
) (int, error) {
	return executeStandaloneCobra(newCheckConfigCobraCommand(dependencies), arguments)
}

func checkConfigInput(parsed commandInput, dependencies checkConfigCommandDependencies) (int, error) {
	rootDirectory, err := dependencies.packageRoot()
	if err != nil {
		return 0, err
	}
	environment := dependencies.environment()
	deploymentPathValue, hasDeployment := lastCommandOption(parsed, "--deployment")
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
	var tenants []string
	if value, ok := lastCommandOption(parsed, "--tenant"); ok {
		tenants = []string{value}
	}
	result, err := dependencies.checkFetchable(configcheck.CheckFetchableOptions{
		Workspace:  workspace,
		Deployment: loadedDeployment,
		Root:       loadedRoot,
		Tenants:    tenants,
	})
	if err != nil {
		return 0, err
	}
	if err := configcheck.FetchableFailure(result); err != nil {
		return 0, err
	}
	fmt.Fprintf(
		dependencies.stderr,
		"%d committed resource type(s) across %d tenant(s) are fetchable (%d declared unfetchable)\n",
		result.Checked-result.Skipped,
		len(result.Tenants),
		result.Skipped,
	)
	return 0, nil
}
