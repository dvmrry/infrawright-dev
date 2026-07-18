package main

// commands_metadata.go ports the three metadata-only CLI commands from
// node-src/cli/main.ts: checkPack (lines 241-271), checkPackSet (273-320),
// and deployment (374-412). The metadata and deployment packages retain all
// domain behavior; this file owns only argv/env precedence, output, and exits.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/cliargs"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
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
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		AllowPositionals: true,
		Values: map[string]cliargs.ValueOption{
			"--pack": {},
			"--root": {},
		},
	}, commandBehavior{helpStatus: 2, helpStderr: true})
	if err != nil {
		return 0, err
	}
	var selectedPack *string
	for _, occurrence := range parsed.Occurrences {
		if occurrence.Kind == cliargs.OccurrenceOption {
			if occurrence.Name == "--pack" {
				value := occurrence.Value
				selectedPack = &value
			}
			continue
		}
		if !strings.HasPrefix(occurrence.Value, "PACK=") {
			return 0, usageError("unknown argument " + occurrence.Value)
		}
		value := strings.TrimPrefix(occurrence.Value, "PACK=")
		if value == "" {
			return 0, usageError("PACK= requires a value")
		}
		selectedPack = &value
	}

	root, hasRoot := cliargs.LastOption(parsed, "--root")
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
	root, err = dependencies.absolutePath(root)
	if err != nil {
		return 0, err
	}
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
		profile = filepath.Join(rootDirectory, "packsets", "full.json")
	}
	catalog := filepath.Join(rootDirectory, "packsets", "full.json")
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{Values: map[string]cliargs.ValueOption{
		"--catalog": {}, "--profile": {}, "--requirements": {}, "--root": {},
	}}, commandBehavior{})
	if err != nil {
		return 0, err
	}
	if value, ok := cliargs.LastOption(parsed, "--root"); ok {
		root = value
	}
	if value, ok := cliargs.LastOption(parsed, "--profile"); ok {
		profile = value
	}
	if value, ok := cliargs.LastOption(parsed, "--catalog"); ok {
		catalog = value
	}
	if requirements, ok := cliargs.LastOption(parsed, "--requirements"); ok {
		result, checkErr := dependencies.checkPackRequirements(metadata.CheckPackRequirementsOptions{
			RequirementsPath: requirements,
			Root:             root,
			CatalogPath:      &catalog,
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
		CatalogPath: &catalog,
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
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		AllowPositionals: true,
		Values:           map[string]cliargs.ValueOption{"--deployment": {}},
	}, commandBehavior{helpStatus: 2, helpStderr: true})
	if err != nil {
		return 0, err
	}
	if len(parsed.Positionals) == 0 {
		return 0, usageError("deployment requires a verb")
	}
	var explicit *string
	if value, ok := cliargs.LastOption(parsed, "--deployment"); ok {
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
