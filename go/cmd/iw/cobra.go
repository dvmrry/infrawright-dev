package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/assessment"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
	"github.com/spf13/cobra"
)

// cobraCommandStatus carries a successful non-zero command outcome through
// Cobra without turning it into a rendered error. Commands such as
// check-pack-set intentionally use non-zero statuses as part of their public
// automation contract.
type cobraCommandStatus struct {
	code int
}

func (e *cobraCommandStatus) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}

func finishCobraCommand(status int, err error) error {
	if err != nil {
		return err
	}
	if status != 0 {
		return &cobraCommandStatus{code: status}
	}
	return nil
}

type typedCobraCommandSpec struct {
	use              string
	short            string
	valueFlags       []string
	allowEmpty       []string
	boolFlags        []string
	rejectDuplicates []string
	run              func(commandInput) (int, error)
}

type exactStringValues struct {
	values []string
}

func (v *exactStringValues) Set(value string) error {
	v.values = append(v.values, value)
	return nil
}

func (v *exactStringValues) String() string {
	return strings.Join(v.values, ",")
}

func (*exactStringValues) Type() string { return "string" }

func newTypedCobraCommand(spec typedCobraCommandSpec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   spec.use,
		Short: spec.short,
		Args:  cobra.ArbitraryArgs,
	}
	for _, name := range spec.valueFlags {
		cmd.Flags().Var(&exactStringValues{}, strings.TrimPrefix(name, "--"), cobraFlagDescription(name))
	}
	for _, name := range spec.boolFlags {
		cmd.Flags().Bool(strings.TrimPrefix(name, "--"), false, cobraFlagDescription(name))
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError(err.Error())
	})
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		parsed, err := typedCobraInput(cmd, args, spec)
		if err != nil {
			return err
		}
		status, err := spec.run(parsed)
		return finishCobraCommand(status, err)
	}
	return cmd
}

func typedCobraInput(cmd *cobra.Command, args []string, spec typedCobraCommandSpec) (commandInput, error) {
	parsed := commandInput{
		Flags:       make(commandFlags, len(spec.boolFlags)),
		Options:     make(map[string][]string, len(spec.valueFlags)),
		Positionals: append([]string(nil), args...),
	}
	for _, name := range spec.valueFlags {
		flag := cmd.Flags().Lookup(strings.TrimPrefix(name, "--"))
		if flag == nil || !flag.Changed {
			continue
		}
		values, ok := flag.Value.(*exactStringValues)
		if !ok {
			return commandInput{}, fmt.Errorf("read %s: unexpected Cobra value type %T", name, flag.Value)
		}
		parsed.Options[name] = append([]string(nil), values.values...)
	}
	for _, name := range spec.boolFlags {
		flag := cmd.Flags().Lookup(strings.TrimPrefix(name, "--"))
		if flag == nil || !flag.Changed {
			continue
		}
		value, err := cmd.Flags().GetBool(strings.TrimPrefix(name, "--"))
		if err != nil {
			return commandInput{}, fmt.Errorf("read %s: %w", name, err)
		}
		if value {
			parsed.Flags[name] = struct{}{}
		}
	}
	allowEmpty := make(map[string]struct{}, len(spec.allowEmpty))
	for _, name := range spec.allowEmpty {
		allowEmpty[name] = struct{}{}
	}
	for name, values := range parsed.Options {
		if _, allowed := allowEmpty[name]; allowed {
			continue
		}
		for _, value := range values {
			if value == "" {
				return commandInput{}, usageError(name + " requires a value")
			}
		}
	}
	for _, name := range spec.rejectDuplicates {
		if len(parsed.Options[name]) > 1 {
			return commandInput{}, usageError(name + " may be specified only once")
		}
	}
	return parsed, nil
}

func executeStandaloneCobra(command *cobra.Command, arguments []string) (int, error) {
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetOut(os.Stdout)
	command.SetErr(os.Stderr)
	command.SetArgs(arguments)
	return cobraExecutionResult(command.Execute())
}

// parseTypedCobraArguments exercises Cobra without running a command's domain
// operation. Focused option-resolution tests use it to preserve lazy I/O
// assertions at the command/domain boundary.
func parseTypedCobraArguments(arguments []string, spec typedCobraCommandSpec) (commandInput, error) {
	var parsed commandInput
	called := false
	spec.run = func(input commandInput) (int, error) {
		parsed = input
		called = true
		return 0, nil
	}
	status, err := executeStandaloneCobra(newTypedCobraCommand(spec), arguments)
	if err != nil {
		return commandInput{}, err
	}
	if status != 0 || !called {
		return commandInput{}, errors.New("Cobra exited before parsing command input")
	}
	return parsed, nil
}

func cobraExecutionResult(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var status *cobraCommandStatus
	if errors.As(err, &status) {
		return status.code, nil
	}
	return 0, err
}

var cobraFlagDescriptions = map[string]string{
	"--allow-destroy":           "allow a saved plan containing destroys",
	"--allow-non-main":          "allow Apply outside the configured main branch",
	"--allow-plan-changes":      "allow a saved plan containing non-import changes",
	"--allow-unverified-source": "analyze explicitly bounded source without qualified provenance",
	"--api":                     "API response JSON path (repeatable where supported)",
	"--api-options":             "API comparison options JSON path",
	"--api-prefix":              "OpenAPI path prefix",
	"--artifact-dir":            "complete source-evidence artifact directory",
	"--backend":                 "Terraform backend name",
	"--backend-config":          "Terraform backend configuration path",
	"--catalog":                 "pack or root catalog path",
	"--check":                   "compare generated output with this path",
	"--concurrency":             "maximum concurrent fetch operations",
	"--debug-traceback":         "include debug traceback details",
	"--deployment":              "deployment overlay path",
	"--diagnostics":             "diagnostics output path",
	"--fail-on-regression":      "return non-zero when comparison regresses",
	"--fail-on-unknown":         "return non-zero for unknown reconciliation results",
	"--imports-only":            "require an import-only Terraform plan",
	"--in":                      "input directory",
	"--main-branch":             "branch treated as the protected main branch",
	"--markdown":                "Markdown summary copy destination",
	"--openapi":                 "OpenAPI document path",
	"--openapi-read":            "expected OpenAPI read operation",
	"--openapi-write":           "expected OpenAPI write operation",
	"--order":                   "resource output ordering",
	"--out":                     "output path",
	"--out-dir":                 "output directory",
	"--override":                "reconciliation override path",
	"--pack":                    "pack name",
	"--path":                    "changed path",
	"--paths-json":              "changed-path JSON file or standard input",
	"--policy":                  "drift or adoption policy path",
	"--profile":                 "pack profile path",
	"--provider-file":           "manifest-relative provider source file",
	"--provider-module":         "provider Go module identity",
	"--provider-source":         "Terraform provider source address",
	"--providers":               "comma-separated provider filter",
	"--registry":                "registry metadata path",
	"--report":                  "assessment report destination or standard output",
	"--requirements":            "pack requirements path",
	"--resource":                "resource selector (repeatable)",
	"--resource-prefix":         "Terraform resource-type prefix",
	"--resources":               "comma-separated resource filter",
	"--root":                    "pack root directory",
	"--save":                    "save the generated Terraform plan",
	"--schema":                  "Terraform provider schema path",
	"--sdk-file":                "module-qualified SDK source file",
	"--sdk-root":                "SDK module and local source root",
	"--source-facts":            "precomputed source-facts path",
	"--source-facts-compare":    "source-facts comparison path",
	"--source-manifest":         "qualified source manifest path",
	"--source-root":             "provider source root directory",
	"--state-aware":             "inspect local ephemeral state while staging imports",
	"--tenant":                  "deployment tenant label",
	"--terraform":               "Terraform executable path",
	"--work-dir":                "private provider-probe work directory",
	"--ast-tool-dir":            "legacy AST tool directory",
}

func cobraFlagDescription(name string) string {
	if description, ok := cobraFlagDescriptions[name]; ok {
		return description
	}
	return strings.TrimPrefix(name, "--")
}

func newRootCatalogCobraCommand() *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: "root-catalog", short: "Generate or verify the compatibility root catalog",
		valueFlags: []string{"--providers", "--out", "--check", "--root", "--profile", "--catalog"},
		run: func(parsed commandInput) (int, error) {
			if len(parsed.Positionals) != 0 {
				return 0, usageError("root-catalog does not accept positional arguments")
			}
			return rootCatalogInput(parsed)
		},
	})
}

func newTransformCobraCommand() *cobra.Command {
	return newTypedCobraCommand(typedCobraCommandSpec{
		use: "transform", short: "Transform pulled provider JSON",
		valueFlags: []string{"--in", "--tenant", "--resource", "--deployment", "--root", "--profile", "--catalog"},
		run: func(parsed commandInput) (int, error) {
			if len(parsed.Positionals) != 0 {
				return 0, usageError("transform does not accept positional arguments")
			}
			return transformCommandInput(parsed)
		},
	})
}

func newCobraRoot() *cobra.Command {
	return newCobraRootWithTerraformPreflight(func() error {
		return terraformcmd.AssertSupportedTerraformExecutionPlatform("")
	})
}

func newCobraRootWithTerraformPreflight(preflight func() error) *cobra.Command {
	root := &cobra.Command{
		Use:           "iw",
		Short:         "Generate, adopt, assess, and apply infrastructure configuration",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return usageError(strings.TrimSpace(cmd.UsageString()))
		},
	}
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError(err.Error())
	})
	root.PersistentPreRunE = func(command *cobra.Command, _ []string) error {
		required, err := cobraCommandRequiresTerraformExecution(command)
		if err != nil {
			return err
		}
		if required {
			return preflight()
		}
		return nil
	}

	root.AddCommand(
		newCheckPackCobraCommand(defaultMetadataCommandDependencies()),
		newCheckPackSetCobraCommand(defaultMetadataCommandDependencies()),
		newRootCatalogCobraCommand(),
		newDeploymentCobraCommand(defaultMetadataCommandDependencies()),
		newTransformCobraCommand(),
		newAdoptCobraCommand(defaultBlockDCommandDependencies()),
		newGenEnvCobraCommand(),
		newImportStagingCobraCommand("stage-imports", defaultBlockDCommandDependencies()),
		newImportStagingCobraCommand("unstage-imports", defaultBlockDCommandDependencies()),
		newResourcesCobraCommand(),
		newRootQueryCobraCommand("roots", "Emit root topology", rootsInput),
		newScopePathsCobraCommand(),
		newRootQueryCobraCommand("plan-roots", "Enumerate plan roots and artifacts", planRootsInput),
		newPlanCobraCommand(defaultPlanCommandDependencies()),
		newCleanPlansCobraCommand(defaultPlanCommandDependencies()),
		newAssessmentCobraCommand(assessment.AssertClean),
		newAssessmentCobraCommand(assessment.AssertAdoptable),
		newApplyCobraCommand(defaultBlockDCommandDependencies()),
		newFetchCobraCommand(nil),
		newFetchDiagCobraCommand(),
		newReconcileCobraCommand(defaultAuthoringCoreDependencies()),
		newOpenAPIMapCobraCommand(defaultAuthoringCoreDependencies()),
		newSourceOperationMapCobraCommand(defaultAuthoringSourceDependencies()),
		newSourceEvidenceEvalCobraCommand(defaultAuthoringSourceDependencies()),
		newProviderProbeCobraCommand(defaultAuthoringProbeDependencies()),
		newTransformAdoptParityCobraCommand(defaultAuthoringCoreDependencies()),
	)
	root.AddCommand(newModulesCobraCommand())
	return root
}

// cobraCommandRequiresTerraformExecution applies the platform gate to the
// command Cobra actually resolved and the effective flag values Cobra parsed.
// Help and parse errors never reach PersistentPreRunE, so they remain available
// on every platform without a second, divergent argv parser.
func cobraCommandRequiresTerraformExecution(command *cobra.Command) (bool, error) {
	switch command.CommandPath() {
	case "iw adopt", "iw gen-env", "iw plan", "iw assert-clean", "iw assert-adoptable", "iw apply", "iw modules generate":
		return true, nil
	case "iw stage-imports":
		enabled, err := command.Flags().GetBool("state-aware")
		if err != nil {
			return false, fmt.Errorf("read --state-aware: %w", err)
		}
		return enabled, nil
	default:
		return false, nil
	}
}

func runCobra(arguments []string) (int, error) {
	root := newCobraRoot()
	root.SetArgs(arguments)
	status, err := cobraExecutionResult(root.Execute())
	if err == nil {
		return status, nil
	}
	var exit *cliExit
	if errors.As(err, &exit) {
		return 0, err
	}
	if strings.HasPrefix(err.Error(), "unknown command ") {
		return 0, usageError(err.Error())
	}
	return 0, err
}
