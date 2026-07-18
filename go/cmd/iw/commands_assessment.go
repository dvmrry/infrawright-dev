package main

// commands_assessment.go ports the assert-clean and assert-adoptable command
// functions from node-src/cli/main.ts. Dispatch and legacy exit-code mapping
// remain in main.go so these functions stay a thin CLI-to-domain adapter.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/assessment"
	"github.com/dvmrry/infrawright-dev/go/internal/cliargs"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

type assessmentCLIOptions struct {
	pack          packOptionDefaults
	deployment    string
	tenant        *string
	resources     []string
	backendConfig *string
	policy        *string
	report        *string
	terraform     *string
}

// assessmentCLIOptionsFor ports assessmentCliOptions. The rootDirectory
// parameter is the already-resolved package root; keeping that lookup outside
// the parser makes the parsing and command body directly testable without
// weakening packageRoot's production contract.
func assessmentCLIOptionsFor(
	arguments []string,
	mode assessment.AssessmentMode,
	rootDirectory string,
) (assessmentCLIOptions, error) {
	values := map[string]cliargs.ValueOption{
		"--backend-config": {},
		"--catalog":        {},
		"--deployment":     {},
		"--profile":        {},
		"--report":         {RejectDuplicates: true},
		"--resource":       {},
		"--root":           {},
		"--tenant":         {AllowEmpty: true, RejectDuplicates: true},
		"--terraform":      {},
	}
	if mode == assessment.AssertAdoptable {
		values["--policy"] = cliargs.ValueOption{}
	}
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		Values: values,
	}, commandBehavior{command: string(mode)})
	if err != nil {
		return assessmentCLIOptions{}, err
	}

	deploymentPathValue, err := selectedDeploymentPath(parsed)
	if err != nil {
		return assessmentCLIOptions{}, err
	}
	options := assessmentCLIOptions{
		pack:       resolvePackOptions(rootDirectory, parsed),
		deployment: deploymentPathValue,
		resources:  append([]string{}, parsed.Options["--resource"]...),
	}
	if value, ok := cliargs.LastOption(parsed, "--tenant"); ok {
		options.tenant = &value
	}
	if value, ok := cliargs.LastOption(parsed, "--backend-config"); ok {
		options.backendConfig = &value
	}
	if value, ok := cliargs.LastOption(parsed, "--policy"); ok {
		options.policy = &value
	}
	if value, ok := cliargs.LastOption(parsed, "--report"); ok {
		options.report = &value
	}
	if value, ok := cliargs.LastOption(parsed, "--terraform"); ok {
		options.terraform = &value
	}
	return options, nil
}

func runAssessmentCommand(
	options assessmentCLIOptions,
	mode assessment.AssessmentMode,
	workspace string,
) (int, error) {
	err := assessment.RunSavedPlanAssertion(assessment.RunSavedPlanAssertionOptions{
		Workspace:     workspace,
		Mode:          mode,
		Tenant:        options.tenant,
		Selectors:     options.resources,
		BackendConfig: options.backendConfig,
		PolicyPath:    options.policy,
		ReportPath:    options.report,
		LoadInputs: func() (assessment.SavedPlanAssertionInputs, error) {
			loadedRoot, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
				PacksRoot:   options.pack.root,
				ProfilePath: &options.pack.profile,
				CatalogPath: &options.pack.catalog,
			})
			if err != nil {
				return assessment.SavedPlanAssertionInputs{}, err
			}
			deploymentPath, err := filepath.Abs(options.deployment)
			if err != nil {
				return assessment.SavedPlanAssertionInputs{}, err
			}
			boundDeployment, err := deployment.LoadBoundAssessmentDeployment(
				deploymentPath,
				controlevidence.BindOptions{},
			)
			if err != nil {
				return assessment.SavedPlanAssertionInputs{}, err
			}
			return assessment.SavedPlanAssertionInputs{
				Deployment:   boundDeployment.Deployment,
				Root:         loadedRoot,
				ControlFiles: []controlevidence.BoundAssessmentControlFile{boundDeployment.File},
			}, nil
		},
		ResolveTerraformExecutable: assessmentTerraformResolver(options.terraform),
		OnDiagnostic: func(message string) {
			fmt.Fprintf(os.Stderr, "%s\n", message)
		},
		Stdout: func(text string) error {
			_, err := os.Stdout.WriteString(text)
			return err
		},
	})
	if err != nil {
		return 0, err
	}
	return 0, nil
}

// assessmentTerraformResolver preserves the source's lazy lookup: TF and PATH
// are not observed until the runner has completed policy preflight, loaded the
// active inputs, and selected at least one saved-plan root. An explicit flag is
// immutable command input and therefore wins without consulting TF.
func assessmentTerraformResolver(explicit *string) func() (string, error) {
	var selected *string
	if explicit != nil {
		value := *explicit
		selected = &value
	}
	return func() (string, error) {
		value := os.Getenv("TF")
		if selected != nil {
			value = *selected
		}
		return terraformcmd.ResolveTerraformExecutable(value, environMap())
	}
}

func assessmentCommand(
	arguments []string,
	mode assessment.AssessmentMode,
) (int, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return 0, err
	}
	options, err := assessmentCLIOptionsFor(arguments, mode, rootDirectory)
	if err != nil {
		return 0, err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	return runAssessmentCommand(options, mode, workspace)
}

func assertCleanCommand(arguments []string) (int, error) {
	return assessmentCommand(arguments, assessment.AssertClean)
}

func assertAdoptableCommand(arguments []string) (int, error) {
	return assessmentCommand(arguments, assessment.AssertAdoptable)
}
