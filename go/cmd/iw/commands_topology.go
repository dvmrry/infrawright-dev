package main

// commands_topology.go ports the slice-4 command family from
// node-src/cli/main.ts: resources, roots, scope-paths, plan-roots, gen-env,
// and modules, plus the legacyPlanLifecycleCommand exit-code shim they
// route through.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/cliargs"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/envgen"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/modulesgen"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

// legacyUsageFailureCodes ports LEGACY_USAGE_FAILURE_CODES from
// node-src/cli/main.ts.
var legacyUsageFailureCodes = map[string]bool{
	"INVALID_CHANGED_PATHS":       true,
	"INVALID_ASSESSMENT_INPUT":    true,
	"INVALID_DEPLOYMENT":          true,
	"INVALID_ROOT_CONFIGURATION":  true,
	"INVALID_TENANT":              true,
	"INVALID_DRIFT_POLICY":        true,
	"INVALID_TERRAFORM_SHOW_JSON": true,
	"INVALID_TERRAFORM_SHOW_UTF8": true,
	"UNKNOWN_RESOURCE_SELECTOR":   true,
}

// legacyPlanLifecycleCommand ports legacyPlanLifecycleCommand: the listed
// ProcessFailure codes become usage errors (exit 2).
func legacyPlanLifecycleCommand(operation func() (int, error)) (int, error) {
	status, err := operation()
	if err != nil {
		var failure *procerr.ProcessFailure
		if errors.As(err, &failure) && legacyUsageFailureCodes[failure.Code] {
			return 0, usageError(failure.Message)
		}
		return status, err
	}
	return status, nil
}

// packOptionDefaults ports the shared `||`-semantics option resolution block
// (root/profile/catalog) every slice-4 command repeats in the Node source.
type packOptionDefaults struct {
	root    string
	profile string
	catalog string
}

func resolvePackOptions(rootDirectory string, parsed cliargs.ParsedArguments) packOptionDefaults {
	defaults := packOptionDefaults{
		root:    filepath.Join(rootDirectory, "packs"),
		profile: filepath.Join(rootDirectory, "packsets", "full.json"),
		catalog: filepath.Join(rootDirectory, "packsets", "full.json"),
	}
	if env := os.Getenv("INFRAWRIGHT_PACKS"); env != "" {
		defaults.root = env
	}
	if env := os.Getenv("INFRAWRIGHT_PACK_PROFILE"); env != "" {
		defaults.profile = env
	}
	if value, ok := cliargs.LastOption(parsed, "--root"); ok {
		defaults.root = value
	}
	if value, ok := cliargs.LastOption(parsed, "--profile"); ok {
		defaults.profile = value
	}
	if value, ok := cliargs.LastOption(parsed, "--catalog"); ok {
		defaults.catalog = value
	}
	return defaults
}

func loadPackAndDeployment(
	options packOptionDefaults,
	deploymentPathValue string,
) (metadata.LoadedPackRoot, deployment.Deployment, error) {
	loadedRoot, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   options.root,
		ProfilePath: &options.profile,
		CatalogPath: &options.catalog,
	})
	if err != nil {
		return metadata.LoadedPackRoot{}, deployment.Deployment{}, err
	}
	loadedDeployment, err := deployment.LoadDeployment(deploymentPathValue)
	if err != nil {
		return metadata.LoadedPackRoot{}, deployment.Deployment{}, err
	}
	return loadedRoot, loadedDeployment, nil
}

func selectedDeploymentPath(parsed cliargs.ParsedArguments) (string, error) {
	if value, ok := cliargs.LastOption(parsed, "--deployment"); ok {
		return value, nil
	}
	return deployment.DeploymentPath(deployment.DeploymentPathOptions{})
}

// resourcesCommand ports resourcesCommand.
func resourcesCommand(arguments []string) (int, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return 0, err
	}
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		Values: map[string]cliargs.ValueOption{
			"--catalog":  {},
			"--order":    {AllowedValues: []string{"references"}, InlineOnly: true},
			"--profile":  {},
			"--resource": {},
			"--root":     {},
		},
	}, commandBehavior{command: "resources"})
	if err != nil {
		return 0, err
	}
	options := resolvePackOptions(rootDirectory, parsed)
	loadedRoot, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   options.root,
		ProfilePath: &options.profile,
		CatalogPath: &options.catalog,
	})
	if err != nil {
		return 0, err
	}
	selected, err := roots.ExpandLoadedResources(loadedRoot, parsed.Options["--resource"])
	if err != nil {
		return 0, err
	}
	orderValue, _ := cliargs.LastOption(parsed, "--order")
	ordered := transform.TransformSelection{ResourceTypes: selected}
	if orderValue == "references" {
		ordered, err = transform.ReferenceOrder(loadedRoot, selected)
		if err != nil {
			return 0, err
		}
	}
	for _, note := range ordered.Notes {
		fmt.Fprint(os.Stderr, note)
	}
	for _, resourceType := range ordered.ResourceTypes {
		fmt.Fprintf(os.Stdout, "%s\n", resourceType)
	}
	return 0, nil
}

type rootQueryOptions struct {
	pack       packOptionDefaults
	deployment string
	resources  []string
	tenant     *string
}

// rootQueryCliOptions ports rootQueryCliOptions.
func rootQueryCliOptions(arguments []string, command string) (rootQueryOptions, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return rootQueryOptions{}, err
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
	}, commandBehavior{command: command})
	if err != nil {
		return rootQueryOptions{}, err
	}
	deploymentPathValue, err := selectedDeploymentPath(parsed)
	if err != nil {
		return rootQueryOptions{}, err
	}
	options := rootQueryOptions{
		pack:       resolvePackOptions(rootDirectory, parsed),
		deployment: deploymentPathValue,
		resources:  parsed.Options["--resource"],
	}
	if tenant, ok := cliargs.LastOption(parsed, "--tenant"); ok {
		options.tenant = &tenant
	}
	return options, nil
}

// rootsCommand ports rootsCommand.
func rootsCommand(arguments []string) (int, error) {
	options, err := rootQueryCliOptions(arguments, "roots")
	if err != nil {
		return 0, err
	}
	loadedRoot, loadedDeployment, err := loadPackAndDeployment(options.pack, options.deployment)
	if err != nil {
		return 0, err
	}
	result, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root:       loadedRoot,
		Deployment: loadedDeployment,
		Tenant:     options.tenant,
		Selectors:  options.resources,
	})
	if err != nil {
		return 0, err
	}
	fmt.Fprint(os.Stderr, roots.RenderLegacyRootDiagnostics(result.Diagnostics))
	rendered, err := roots.RenderLegacyRootTopology(result.Topology)
	if err != nil {
		return 0, err
	}
	fmt.Fprint(os.Stdout, rendered)
	return 0, nil
}

// scopePathsCommand ports scopePathsCommand.
func scopePathsCommand(arguments []string) (int, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return 0, err
	}
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		Values: map[string]cliargs.ValueOption{
			"--catalog":    {},
			"--deployment": {},
			"--path":       {AllowEmpty: true},
			"--paths-json": {RejectDuplicates: true},
			"--profile":    {},
			"--root":       {},
		},
	}, commandBehavior{command: "scope-paths"})
	if err != nil {
		return 0, err
	}
	options := resolvePackOptions(rootDirectory, parsed)
	deploymentPathValue, err := selectedDeploymentPath(parsed)
	if err != nil {
		return 0, err
	}
	paths := append([]string{}, parsed.Options["--path"]...)
	if pathsJSON, ok := cliargs.LastOption(parsed, "--paths-json"); ok {
		var text []byte
		if pathsJSON == "-" {
			text, err = io.ReadAll(os.Stdin)
		} else {
			text, err = os.ReadFile(pathsJSON)
		}
		if err != nil {
			return 0, err
		}
		var decoded any
		if err := json.Unmarshal(text, &decoded); err != nil {
			return 0, usageError(fmt.Sprintf("%s must contain a JSON array of changed paths", pathsJSON))
		}
		entries, ok := decoded.([]any)
		if !ok {
			return 0, usageError(fmt.Sprintf("%s must contain a JSON array of changed paths", pathsJSON))
		}
		for _, entry := range entries {
			text, _ := entry.(string)
			paths = append(paths, text)
		}
	}
	loadedRoot, loadedDeployment, err := loadPackAndDeployment(options, deploymentPathValue)
	if err != nil {
		return 0, err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	scope, err := roots.ChangedPathScopeLoaded(roots.ChangedPathScopeLoadedOptions{
		Root:           loadedRoot,
		Deployment:     loadedDeployment,
		DeploymentPath: deploymentPathValue,
		Paths:          paths,
		Workspace:      workspace,
	})
	if err != nil {
		return 0, err
	}
	rendered, err := roots.RenderLegacyChangedPathScope(scope)
	if err != nil {
		return 0, err
	}
	fmt.Fprint(os.Stdout, rendered)
	return 0, nil
}

// planRootsCommand ports planRootsCommand.
func planRootsCommand(arguments []string) (int, error) {
	options, err := rootQueryCliOptions(arguments, "plan-roots")
	if err != nil {
		return 0, err
	}
	loadedRoot, loadedDeployment, err := loadPackAndDeployment(options.pack, options.deployment)
	if err != nil {
		return 0, err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	result, err := roots.LoadedPlanRoots(roots.LoadedPlanRootsOptions{
		Root:       loadedRoot,
		Deployment: loadedDeployment,
		Tenant:     options.tenant,
		Selectors:  options.resources,
		Workspace:  workspace,
	})
	if err != nil {
		return 0, err
	}
	fmt.Fprint(os.Stderr, roots.RenderLegacyRootDiagnostics(result.Diagnostics))
	rendered, err := roots.RenderLegacyPlanRoots(result.Result)
	if err != nil {
		return 0, err
	}
	fmt.Fprint(os.Stdout, rendered)
	return 0, nil
}

// genEnvCommand ports genEnv.
func genEnvCommand(arguments []string) (int, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return 0, err
	}
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		Values: map[string]cliargs.ValueOption{
			"--backend":    {},
			"--catalog":    {},
			"--deployment": {},
			"--profile":    {},
			"--resource":   {},
			"--root":       {},
			"--tenant":     {},
			"--terraform":  {},
		},
	}, commandBehavior{command: "gen-env"})
	if err != nil {
		return 0, err
	}
	tenant, hasTenant := cliargs.LastOption(parsed, "--tenant")
	if !hasTenant {
		return 0, usageError("gen-env requires --tenant")
	}
	options := resolvePackOptions(rootDirectory, parsed)
	deploymentPathValue, err := selectedDeploymentPath(parsed)
	if err != nil {
		return 0, err
	}
	loadedRoot, loadedDeployment, err := loadPackAndDeployment(options, deploymentPathValue)
	if err != nil {
		return 0, err
	}
	formatterOptions := modulesgen.TerraformFormatterOptions{Environment: environMap()}
	if terraform, ok := cliargs.LastOption(parsed, "--terraform"); ok {
		formatterOptions.Executable = terraform
	}
	formatter := modulesgen.NewTerraformFormatter(formatterOptions)
	generateOptions := envgen.GenerateEnvironmentRootsOptions{
		Deployment: loadedDeployment,
		FormatHcl:  formatter.FormatHCL,
		OnDiagnostic: func(message string) {
			fmt.Fprintf(os.Stderr, "%s\n", message)
		},
		Root:      loadedRoot,
		Selectors: parsed.Options["--resource"],
		Tenant:    tenant,
	}
	if backend, ok := cliargs.LastOption(parsed, "--backend"); ok {
		generateOptions.Backend = &backend
	}
	if _, err := envgen.GenerateEnvironmentRoots(generateOptions); err != nil {
		return 0, err
	}
	return 0, nil
}

// jsonStringifyStringCLI reproduces JSON.stringify(<string>) for the
// duplicate --resource usage message (same contract as transformrun's
// diagnostic quoting: raw non-ASCII, \u00XX for controls).
func jsonStringifyStringCLI(value string) string {
	quoted := "\""
	for _, r := range value {
		switch r {
		case '"':
			quoted += "\\\""
		case '\\':
			quoted += "\\\\"
		case '\b':
			quoted += "\\b"
		case '\f':
			quoted += "\\f"
		case '\n':
			quoted += "\\n"
		case '\r':
			quoted += "\\r"
		case '\t':
			quoted += "\\t"
		default:
			if r < 0x20 {
				quoted += fmt.Sprintf("\\u%04x", r)
			} else {
				quoted += string(r)
			}
		}
	}
	return quoted + "\""
}

func environMap() map[string]string {
	output := map[string]string{}
	for _, entry := range os.Environ() {
		for index := 0; index < len(entry); index++ {
			if entry[index] == '=' {
				output[entry[:index]] = entry[index+1:]
				break
			}
		}
	}
	return output
}

// modulesCommand ports moduleOptions + modules.
func modulesCommand(arguments []string) (int, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return 0, err
	}
	if len(arguments) == 0 || (arguments[0] != "generate" && arguments[0] != "validate") {
		return 0, usageError("modules requires the generate or validate verb")
	}
	verb := arguments[0]
	parsed, err := commandArguments(arguments[1:], cliargs.ParseConfig{
		Values: map[string]cliargs.ValueOption{
			"--catalog":    {},
			"--deployment": {},
			"--out":        {},
			"--profile":    {},
			"--resource":   {},
			"--root":       {},
			"--terraform":  {},
		},
	}, commandBehavior{})
	if err != nil {
		return 0, err
	}
	options := resolvePackOptions(rootDirectory, parsed)
	deploymentPathValue, err := selectedDeploymentPath(parsed)
	if err != nil {
		return 0, err
	}
	resources := parsed.Options["--resource"]
	seen := map[string]bool{}
	for _, resource := range resources {
		if seen[resource] {
			quoted := jsonStringifyStringCLI(resource)
			return 0, usageError("duplicate --resource " + quoted)
		}
		seen[resource] = true
	}
	loadedRoot, loadedDeployment, err := loadPackAndDeployment(options, deploymentPathValue)
	if err != nil {
		return 0, err
	}
	outputRoot, hasOutput := cliargs.LastOption(parsed, "--out")
	if !hasOutput {
		outputRoot, err = deployment.DeploymentModuleDir(loadedDeployment)
		if err != nil {
			return 0, err
		}
	}
	selected := modulesgen.ActiveGeneratedResourceTypes(loadedRoot)
	if len(resources) > 0 {
		topology, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
			Root:       loadedRoot,
			Deployment: loadedDeployment,
			Tenant:     nil,
			Selectors:  resources,
		})
		if err != nil {
			return 0, err
		}
		fmt.Fprint(os.Stderr, roots.RenderLegacyRootDiagnostics(topology.Diagnostics))
		members := map[string]bool{}
		for _, root := range topology.Topology.Roots {
			for _, member := range root.Members {
				members[member] = true
			}
		}
		names := make([]string, 0, len(members))
		for member := range members {
			names = append(names, member)
		}
		selected = canonjson.SortedStrings(names)
	}
	if verb == "validate" {
		if _, err := modulesgen.ValidateGeneratedModuleTree(outputRoot, selected); err != nil {
			return 0, err
		}
		fmt.Fprintf(os.Stdout, "validated generated module tree %s: %d module(s)\n", outputRoot, len(selected))
		return 0, nil
	}
	formatterOptions := modulesgen.TerraformFormatterOptions{Environment: environMap()}
	if terraform, ok := cliargs.LastOption(parsed, "--terraform"); ok {
		formatterOptions.Executable = terraform
	}
	generateOptions := modulesgen.GenerateModuleOptions{
		OutputRoot: outputRoot,
		FormatHCL:  modulesgen.NewTerraformFormatter(formatterOptions),
		OnWrite: func(destination string) {
			fmt.Fprintf(os.Stderr, "wrote %s\n", destination)
		},
	}
	var generated []modulesgen.GeneratedModule
	if len(resources) == 0 {
		generated, err = modulesgen.GenerateActiveModules(loadedRoot, generateOptions)
		if err != nil {
			return 0, err
		}
	} else {
		for _, resourceType := range selected {
			module, err := modulesgen.GenerateModule(loadedRoot, resourceType, generateOptions)
			if err != nil {
				return 0, err
			}
			generated = append(generated, module)
		}
	}
	files := 0
	for _, module := range generated {
		files += len(module.Files)
	}
	fmt.Fprintf(os.Stdout, "generated %d module(s), %d file(s), in %s\n", len(generated), files, outputRoot)
	return 0, nil
}
