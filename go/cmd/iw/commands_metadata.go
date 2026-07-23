package main

// commands_metadata.go ports the three metadata-only CLI commands from
// the original implementation: checkPack (lines 241-271), checkPackSet (273-320),
// and deployment (374-412). The metadata and deployment packages retain all
// domain behavior; this file owns only argv/env precedence, output, and exits.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/spf13/cobra"
)

type metadataCommandDependencies struct {
	absolutePath           func(string) (string, error)
	checkPackRequirements  func(metadata.CheckPackRequirementsOptions) (metadata.RequirementsResult, error)
	deploymentConfigDir    func(deployment.Deployment, string) (string, error)
	deploymentEnvsDir      func(deployment.Deployment, string) (string, error)
	deploymentImportsDir   func(deployment.Deployment, string) (string, error)
	deploymentModuleDir    func(deployment.Deployment) (string, error)
	deploymentOverlay      func(deployment.Deployment) (string, error)
	deploymentPath         func(deployment.DeploymentPathOptions) (string, error)
	deploymentTenantRoot   func(deployment.Deployment, string) (string, error)
	deploymentTfvarsFormat func(deployment.Deployment) (string, error)
	environment            func(string) string
	loadDeployment         func(string) (deployment.Deployment, error)
	packageRoot            func() (string, error)
	stdout                 io.Writer
	validateActivePackSet  func(metadata.ValidateActivePackSetOptions) (metadata.ActivePackSetResult, error)
	validatePackAuthoring  func(metadata.ValidatePackAuthoringOptions) (metadata.ValidatePackAuthoringResult, error)
	validatePackResources  func(metadata.PackMetadata, []string) (metadata.LoadedRegistry, metadata.LoadedOverrides, error)
}

func defaultMetadataCommandDependencies() metadataCommandDependencies {
	return metadataCommandDependencies{
		absolutePath:           filepath.Abs,
		checkPackRequirements:  metadata.CheckPackRequirements,
		deploymentConfigDir:    deployment.DeploymentConfigDir,
		deploymentEnvsDir:      deployment.DeploymentEnvsDir,
		deploymentImportsDir:   deployment.DeploymentImportsDir,
		deploymentModuleDir:    deployment.DeploymentModuleDir,
		deploymentOverlay:      deployment.DeploymentOverlay,
		deploymentPath:         deployment.DeploymentPath,
		deploymentTenantRoot:   deployment.DeploymentTenantRoot,
		deploymentTfvarsFormat: deployment.DeploymentTfvarsFormat,
		environment:            os.Getenv,
		loadDeployment:         deployment.LoadDeployment,
		packageRoot:            packageRoot,
		stdout:                 os.Stdout,
		validateActivePackSet:  metadata.ValidateActivePackSet,
		validatePackAuthoring:  metadata.ValidatePackAuthoring,
		validatePackResources:  metadata.ValidatePackResources,
	}
}

func checkPackCommand(arguments []string) (int, error) {
	return checkPackCommandWithDependencies(arguments, defaultMetadataCommandDependencies())
}

func checkPackCommandWithDependencies(arguments []string, dependencies metadataCommandDependencies) (int, error) {
	return executeStandaloneCobra(newCheckPackCobraCommand(dependencies), arguments)
}

func newCheckPackCobraCommand(dependencies metadataCommandDependencies) *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: "check-pack [PACK=<name>]", short: "Validate pack and registry metadata",
		valueFlags: []string{"--pack", "--root"},
		run: func(parsed commandInput) (int, error) {
			return checkPackInput(parsed, dependencies)
		},
	})
}

func checkPackInput(parsed commandInput, dependencies metadataCommandDependencies) (int, error) {
	if len(parsed.Positionals) > 1 {
		return 0, usageError("check-pack accepts at most one PACK=<name> argument")
	}
	var selectedPack *string
	if value, ok := lastCommandOption(parsed, "--pack"); ok {
		selectedPack = &value
	}
	if len(parsed.Positionals) == 1 {
		if selectedPack != nil {
			return 0, usageError("check-pack accepts only one of --pack or PACK=<name>")
		}
		argument := parsed.Positionals[0]
		if !strings.HasPrefix(argument, "PACK=") {
			return 0, usageError("unknown argument " + argument)
		}
		value := strings.TrimPrefix(argument, "PACK=")
		if value == "" {
			return 0, usageError("PACK= requires a value")
		}
		selectedPack = &value
	}

	root, hasRoot := lastCommandOption(parsed, "--root")
	if !hasRoot {
		root = dependencies.environment("INFRAWRIGHT_PACKS")
		if root == "" {
			rootDirectory, rootErr := dependencies.packageRoot()
			if rootErr != nil {
				return 0, rootErr
			}
			root = filepath.Join(rootDirectory, "packs")
		}
	}
	absoluteRoot, err := dependencies.absolutePath(root)
	if err != nil {
		return 0, err
	}
	root = absoluteRoot
	result, err := dependencies.validatePackAuthoring(metadata.ValidatePackAuthoringOptions{
		Root: root,
		Pack: selectedPack,
	})
	if err != nil {
		return 0, err
	}
	if _, _, err := dependencies.validatePackResources(result.Metadata, result.Names); err != nil {
		return 0, err
	}
	validated := "none"
	if len(result.Names) > 0 {
		validated = strings.Join(result.Names, ", ")
	}
	_, err = fmt.Fprintf(dependencies.stdout, "validated packs: %s\n", validated)
	return 0, err
}

func checkPackSetCommand(arguments []string) (int, error) {
	return checkPackSetCommandWithDependencies(arguments, defaultMetadataCommandDependencies())
}

func checkPackSetCommandWithDependencies(arguments []string, dependencies metadataCommandDependencies) (int, error) {
	return executeStandaloneCobra(newCheckPackSetCobraCommand(dependencies), arguments)
}

func newCheckPackSetCobraCommand(dependencies metadataCommandDependencies) *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: "check-pack-set", short: "Validate an installed pack set",
		valueFlags: []string{"--profile", "--requirements", "--root"},
		run: func(parsed commandInput) (int, error) {
			if len(parsed.Positionals) != 0 {
				return 0, usageError("check-pack-set does not accept positional arguments")
			}
			return checkPackSetInput(parsed, dependencies)
		},
	})
}

func checkPackSetInput(parsed commandInput, dependencies metadataCommandDependencies) (int, error) {
	rootDirectory, err := dependencies.packageRoot()
	if err != nil {
		return 0, err
	}
	root := dependencies.environment("INFRAWRIGHT_PACKS")
	if root == "" {
		root = filepath.Join(rootDirectory, "packs")
	}
	profile := dependencies.environment("INFRAWRIGHT_PACK_PROFILE")
	if profile == "" {
		profile = filepath.Join(rootDirectory, "packs", "full.packset.json")
	}
	if value, ok := lastCommandOption(parsed, "--root"); ok {
		root = value
	}
	if value, ok := lastCommandOption(parsed, "--profile"); ok {
		profile = value
	}
	if requirements, ok := lastCommandOption(parsed, "--requirements"); ok {
		result, checkErr := dependencies.checkPackRequirements(metadata.CheckPackRequirementsOptions{
			RequirementsPath: requirements,
			Root:             root,
		})
		if checkErr != nil {
			return 0, checkErr
		}
		if !result.Available {
			pieces := make([]string, 0, 2)
			if len(result.Missing.Packs) > 0 {
				pieces = append(pieces, "packs="+strings.Join(result.Missing.Packs, ","))
			}
			if len(result.Missing.Shared) > 0 {
				pieces = append(pieces, "shared="+strings.Join(result.Missing.Shared, ","))
			}
			if _, writeErr := fmt.Fprintf(dependencies.stdout, "requirements unavailable: %s\n", strings.Join(pieces, " ")); writeErr != nil {
				return 0, writeErr
			}
			return 3, nil
		}
		_, err = fmt.Fprintf(
			dependencies.stdout,
			"requirements satisfied: packs=[%s] shared=[%s]\n",
			strings.Join(result.Active.Packs, ","),
			strings.Join(result.Active.Shared, ","),
		)
		return 0, err
	}
	result, err := dependencies.validateActivePackSet(metadata.ValidateActivePackSetOptions{
		ProfilePath: profile,
		Root:        root,
	})
	if err != nil {
		return 0, err
	}
	_, err = fmt.Fprintf(
		dependencies.stdout,
		"validated pack set: packs=[%s] shared=[%s]\n",
		strings.Join(result.Active.Packs, ","),
		strings.Join(result.Active.Shared, ","),
	)
	return 0, err
}

func deploymentCommand(arguments []string) (int, error) {
	return deploymentCommandWithDependencies(arguments, defaultMetadataCommandDependencies())
}

func deploymentCommandWithDependencies(arguments []string, dependencies metadataCommandDependencies) (int, error) {
	return executeStandaloneCobra(newDeploymentCobraCommand(dependencies), arguments)
}

func newDeploymentCobraCommand(dependencies metadataCommandDependencies) *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: "deployment <query> [tenant]", short: "Query deployment metadata",
		valueFlags: []string{"--deployment"},
		run: func(parsed commandInput) (int, error) {
			return deploymentInput(parsed, dependencies)
		},
	})
}

func deploymentInput(parsed commandInput, dependencies metadataCommandDependencies) (int, error) {
	if len(parsed.Positionals) == 0 {
		return 0, usageError("deployment requires a verb")
	}
	if len(parsed.Positionals) > 2 {
		return 0, usageError("deployment accepts only a query and optional tenant")
	}
	var explicit *string
	if value, ok := lastCommandOption(parsed, "--deployment"); ok {
		explicit = &value
	}
	pathOptions := deployment.DeploymentPathOptions{Explicit: explicit}
	if explicit == nil {
		pathOptions.Environment = map[string]string{
			"INFRAWRIGHT_DEPLOYMENT": dependencies.environment("INFRAWRIGHT_DEPLOYMENT"),
		}
	}
	source, err := dependencies.deploymentPath(pathOptions)
	if err != nil {
		return 0, err
	}
	loaded, err := dependencies.loadDeployment(source)
	if err != nil {
		return 0, err
	}
	verb := parsed.Positionals[0]
	var value string
	switch verb {
	case "overlay":
		value, err = dependencies.deploymentOverlay(loaded)
	case "tfvars-format":
		value, err = dependencies.deploymentTfvarsFormat(loaded)
	case "module-dir":
		value, err = dependencies.deploymentModuleDir(loaded)
	case "tenant-root", "config-dir", "imports-dir", "envs-dir":
		if len(parsed.Positionals) < 2 {
			return 0, usageError(verb + " requires a tenant")
		}
		tenant := parsed.Positionals[1]
		switch verb {
		case "tenant-root":
			value, err = dependencies.deploymentTenantRoot(loaded, tenant)
		case "config-dir":
			value, err = dependencies.deploymentConfigDir(loaded, tenant)
		case "imports-dir":
			value, err = dependencies.deploymentImportsDir(loaded, tenant)
		case "envs-dir":
			value, err = dependencies.deploymentEnvsDir(loaded, tenant)
		}
	default:
		return 0, usageError("unknown deployment verb " + jsonStringifyStringCLI(verb))
	}
	if err != nil {
		return 0, err
	}
	_, err = fmt.Fprintln(dependencies.stdout, value)
	return 0, err
}
