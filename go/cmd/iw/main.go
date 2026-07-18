// Command iw is the Go port of the Infrawright CLI entry point
// (node-src/cli/main.ts). The current Go slice carries the credential-free
// command families through adopt and exact saved-plan Apply; the usage text,
// dispatch shape, exit codes, and error rendering reproduce the Node CLI
// byte-for-byte for every surface the differential corpus covers.
//
// Pre-cutover divergence (deliberate, excluded from the differential corpus):
// commands that exist in the Node CLI but are not yet ported fail loudly with
// "not yet ported" instead of pretending to be unknown. Filesystem error text
// is Go-native throughout (docs/go-runtime-v2.md §2: Node's filesystem-error
// wording is explicitly not part of the compatibility contract).
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/cliargs"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

// usageFixture is the byte-captured stdout of `iw root-catalog -h` from the
// Node CLI (dist/infrawright-cli.mjs at the oracle build), including its
// trailing newline. The differential harness compares help output against
// the live oracle, so drift here fails loudly.
//
//go:embed usage.txt
var usageFixture string

// usageText mirrors the USAGE constant in node-src/cli/main.ts: the fixture
// without the trailing newline the Node CLI appends when printing.
var usageText = strings.TrimSuffix(usageFixture, "\n")

// cliExit ports the CliExit class in node-src/cli/main.ts: a terminal
// outcome carrying its exit status and output stream selection.
type cliExit struct {
	message string
	status  int
	stdout  bool
}

func (e *cliExit) Error() string { return e.message }

// usageError ports usageError in node-src/cli/main.ts.
func usageError(message string) error {
	return &cliExit{message: message, status: 2}
}

var unknownArgumentMessage = regexp.MustCompile(`^unknown argument (.+)$`)

// commandBehavior ports the behavior options of commandArguments in
// node-src/cli/main.ts. Only the fields the ported commands use are
// carried; helpStatus/helpStdout keep the TS defaults (0, true).
type commandBehavior struct {
	command string
}

// commandArguments ports commandArguments in node-src/cli/main.ts: parse
// failures become usage errors (optionally reworded per command), and
// --help short-circuits with the full usage text on stdout.
func commandArguments(
	arguments []string,
	config cliargs.ParseConfig,
	behavior commandBehavior,
) (cliargs.ParsedArguments, error) {
	parsed, err := cliargs.ParseCommandArguments(arguments, config)
	if err != nil {
		var parseError *cliargs.CliArgumentParseError
		if errors.As(err, &parseError) {
			match := unknownArgumentMessage.FindStringSubmatch(parseError.Message)
			if behavior.command != "" && match != nil {
				return cliargs.ParsedArguments{}, usageError(
					fmt.Sprintf("%s does not accept %s", behavior.command, match[1]),
				)
			}
			return cliargs.ParsedArguments{}, usageError(parseError.Message)
		}
		return cliargs.ParsedArguments{}, err
	}
	if _, help := parsed.Flags["--help"]; help {
		return cliargs.ParsedArguments{}, &cliExit{message: usageText, status: 0, stdout: true}
	}
	return parsed, nil
}

// packageRoot ports packageRoot in node-src/cli/main.ts: walk up from the
// executable (the Node CLI walks up from its bundle path) until a directory
// containing package.json is found.
func packageRoot() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	current := filepath.Dir(executable)
	for {
		if _, err := os.Stat(filepath.Join(current, "package.json")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("unable to locate the Infrawright package root")
		}
		current = parent
	}
}

// lookupEnv mirrors the TS `?? process.env.X` nullish reads in rootCatalog:
// an empty-but-set variable is used as-is (unlike the `||` reads other
// commands use). Presence, not non-emptiness, decides.
func lookupEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}

// rootCatalog ports the rootCatalog command in node-src/cli/main.ts.
func rootCatalog(arguments []string) (int, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return 0, err
	}
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		Values: map[string]cliargs.ValueOption{
			"--catalog":   {},
			"--check":     {},
			"--out":       {},
			"--profile":   {},
			"--providers": {},
			"--root":      {},
		},
	}, commandBehavior{})
	if err != nil {
		return 0, err
	}
	output, hasOutput := cliargs.LastOption(parsed, "--out")
	check, hasCheck := cliargs.LastOption(parsed, "--check")
	if hasOutput && hasCheck {
		return 0, usageError("root-catalog accepts only one of --out or --check")
	}
	providersValue, hasProviders := cliargs.LastOption(parsed, "--providers")
	var providers []string
	if hasProviders {
		for _, provider := range strings.Split(providersValue, ",") {
			if provider != "" {
				providers = append(providers, provider)
			}
		}
		if len(providers) == 0 {
			return 0, usageError("--providers requires at least one provider")
		}
	}
	root, hasRoot := cliargs.LastOption(parsed, "--root")
	if !hasRoot {
		if env, ok := lookupEnv("INFRAWRIGHT_PACKS"); ok {
			root = env
		} else {
			root = filepath.Join(rootDirectory, "packs")
		}
	}
	profile, hasProfile := cliargs.LastOption(parsed, "--profile")
	if !hasProfile {
		if env, ok := lookupEnv("INFRAWRIGHT_PACK_PROFILE"); ok {
			profile = env
		} else {
			profile = filepath.Join(rootDirectory, "packsets", "full.json")
		}
	}
	catalog, hasCatalog := cliargs.LastOption(parsed, "--catalog")
	if !hasCatalog {
		catalog = filepath.Join(rootDirectory, "packsets", "full.json")
	}
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   root,
		ProfilePath: &profile,
		CatalogPath: &catalog,
	})
	if err != nil {
		return 0, err
	}
	rendered, err := metadata.RenderRootCatalog(loaded, providers)
	if err != nil {
		return 0, err
	}
	if hasCheck {
		actual, err := os.ReadFile(check)
		if err != nil {
			return 0, err
		}
		if string(actual) != rendered {
			return 0, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
				Code:     "STALE_ROOT_CATALOG",
				Category: procerr.CategoryDomain,
				Message:  fmt.Sprintf("root catalog is stale: %s", check),
			})
		}
		return 0, nil
	}
	if hasOutput {
		err := os.WriteFile(output, []byte(rendered), 0o666)
		return 0, err
	}
	_, err = os.Stdout.WriteString(rendered)
	return 0, err
}

// transformCommand ports the transform command in node-src/cli/main.ts.
// Unlike rootCatalog's nullish env reads, transform uses `||` semantics for
// INFRAWRIGHT_PACKS / INFRAWRIGHT_PACK_PROFILE: an empty-but-set variable
// falls through to the default — a genuine per-command asymmetry in the
// Node source, preserved deliberately.
func transformCommand(arguments []string) (int, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return 0, err
	}
	parsed, err := commandArguments(arguments, cliargs.ParseConfig{
		Values: map[string]cliargs.ValueOption{
			"--catalog":    {},
			"--deployment": {},
			"--in":         {},
			"--profile":    {},
			"--resource":   {},
			"--root":       {},
			"--tenant":     {},
		},
	}, commandBehavior{command: "transform"})
	if err != nil {
		return 0, err
	}
	root, hasRoot := cliargs.LastOption(parsed, "--root")
	if !hasRoot {
		if env := os.Getenv("INFRAWRIGHT_PACKS"); env != "" {
			root = env
		} else {
			root = filepath.Join(rootDirectory, "packs")
		}
	}
	profile, hasProfile := cliargs.LastOption(parsed, "--profile")
	if !hasProfile {
		if env := os.Getenv("INFRAWRIGHT_PACK_PROFILE"); env != "" {
			profile = env
		} else {
			profile = filepath.Join(rootDirectory, "packsets", "full.json")
		}
	}
	catalog, hasCatalog := cliargs.LastOption(parsed, "--catalog")
	if !hasCatalog {
		catalog = filepath.Join(rootDirectory, "packsets", "full.json")
	}
	input, hasInput := cliargs.LastOption(parsed, "--in")
	tenant, hasTenant := cliargs.LastOption(parsed, "--tenant")
	if !hasInput || !hasTenant {
		return 0, usageError("transform requires --in and --tenant")
	}
	selectedDeployment, hasDeployment := cliargs.LastOption(parsed, "--deployment")
	if !hasDeployment {
		selectedDeployment, err = deployment.DeploymentPath(deployment.DeploymentPathOptions{})
		if err != nil {
			return 0, err
		}
	}
	loadedRoot, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   root,
		ProfilePath: &profile,
		CatalogPath: &catalog,
	})
	if err != nil {
		return 0, err
	}
	loadedDeployment, err := deployment.LoadDeployment(selectedDeployment)
	if err != nil {
		return 0, err
	}
	environment := map[string]string{}
	if value, ok := os.LookupEnv("DROPS_CHECK"); ok {
		environment["DROPS_CHECK"] = value
	}
	result, err := transformrun.RunTransformBatch(transformrun.RunTransformBatchOptions{
		Deployment:     loadedDeployment,
		Environment:    environment,
		InputDirectory: input,
		OnDiagnostic: func(message string) {
			fmt.Fprintf(os.Stderr, "%s\n", message)
		},
		Root:      loadedRoot,
		Selectors: parsed.Options["--resource"],
		Tenant:    tenant,
	})
	if err != nil {
		return 0, err
	}
	if len(result.Failed) == 0 {
		return 0, nil
	}
	// Post-#229 contract: exit 4 only when every failure is a DROPS_CHECK
	// classification failure (the documented sentinel); any other failure
	// in the batch keeps the generic exit 1.
	dropCheckFailed := map[string]bool{}
	for _, resourceType := range result.DropCheckFailed {
		dropCheckFailed[resourceType] = true
	}
	for _, resourceType := range result.Failed {
		if !dropCheckFailed[resourceType] {
			return 1, nil
		}
	}
	return 4, nil
}

// knownCommands derives the Node CLI's command inventory from the embedded
// usage text ("  iw <command> ..." lines), so the not-yet-ported guard can
// never drift from the usage fixture.
func knownCommands() map[string]bool {
	known := make(map[string]bool)
	for _, line := range strings.Split(usageText, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "iw" {
			known[fields[1]] = true
		}
	}
	return known
}

var terraformCommandValueOptions = map[string]bool{
	"--backend":        true,
	"--backend-config": true,
	"--catalog":        true,
	"--deployment":     true,
	"--in":             true,
	"--main-branch":    true,
	"--out":            true,
	"--policy":         true,
	"--profile":        true,
	"--report":         true,
	"--resource":       true,
	"--root":           true,
	"--tenant":         true,
	"--terraform":      true,
}

var terraformCommandFlags = map[string]bool{
	"--allow-destroy":      true,
	"--allow-non-main":     true,
	"--allow-plan-changes": true,
	"--imports-only":       true,
	"--save":               true,
	"--state-aware":        true,
}

// hasStandaloneTerraformHelp ports the deliberately shallow source scan. It
// recognizes help only while walking a syntactically complete prefix of known
// Terraform-command options; an unknown token or missing value stops the scan.
func hasStandaloneTerraformHelp(arguments []string) bool {
	for index := 1; index < len(arguments); {
		argument := arguments[index]
		if argument == "-h" || argument == "--help" {
			return true
		}
		if terraformCommandValueOptions[argument] {
			if index+1 >= len(arguments) {
				return false
			}
			index += 2
			continue
		}
		if terraformCommandFlags[argument] ||
			(index == 1 && arguments[0] == "modules" && argument == "generate") {
			index++
			continue
		}
		return false
	}
	return false
}

// requiresTerraformExecution ports the source's pre-dispatch platform-gate
// selection. Standalone command help is available on every platform.
func requiresTerraformExecution(arguments []string) bool {
	if hasStandaloneTerraformHelp(arguments) {
		return false
	}
	if len(arguments) == 0 {
		return false
	}
	command := arguments[0]
	return command == "adopt" ||
		command == "gen-env" ||
		command == "plan" ||
		command == "assert-clean" ||
		command == "assert-adoptable" ||
		command == "apply" ||
		(command == "modules" && len(arguments) > 1 && arguments[1] == "generate") ||
		(command == "stage-imports" && slicesContain(arguments, "--state-aware"))
}

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// run ports the main dispatch in node-src/cli/main.ts for the commands this
// slice carries, and fails loudly for the rest.
func run(arguments []string) (int, error) {
	if requiresTerraformExecution(arguments) {
		if err := terraformcmd.AssertSupportedTerraformExecutionPlatform(""); err != nil {
			return 0, err
		}
	}
	if len(arguments) == 0 {
		return 0, usageError(usageText)
	}
	command := arguments[0]
	switch command {
	case "root-catalog":
		return rootCatalog(arguments[1:])
	case "transform":
		return transformCommand(arguments[1:])
	case "adopt":
		return adoptCommand(arguments[1:])
	case "gen-env":
		return genEnvCommand(arguments[1:])
	case "stage-imports":
		return stageImportsCommand(arguments[1:])
	case "unstage-imports":
		return unstageImportsCommand(arguments[1:])
	case "modules":
		return modulesCommand(arguments[1:])
	case "resources":
		return legacyPlanLifecycleCommand(func() (int, error) { return resourcesCommand(arguments[1:]) })
	case "roots":
		return legacyPlanLifecycleCommand(func() (int, error) { return rootsCommand(arguments[1:]) })
	case "scope-paths":
		return legacyPlanLifecycleCommand(func() (int, error) { return scopePathsCommand(arguments[1:]) })
	case "plan-roots":
		return legacyPlanLifecycleCommand(func() (int, error) { return planRootsCommand(arguments[1:]) })
	case "plan":
		return legacyPlanLifecycleCommand(func() (int, error) { return planCommand(arguments[1:]) })
	case "clean-plans":
		return legacyPlanLifecycleCommand(func() (int, error) { return cleanPlansCommand(arguments[1:]) })
	case "assert-clean":
		return legacyPlanLifecycleCommand(func() (int, error) { return assertCleanCommand(arguments[1:]) })
	case "assert-adoptable":
		return legacyPlanLifecycleCommand(func() (int, error) { return assertAdoptableCommand(arguments[1:]) })
	case "apply":
		return legacyPlanLifecycleCommand(func() (int, error) { return applyCommand(arguments[1:]) })
	case "fetch":
		return fetchCommand(arguments[1:], nil)
	case "fetch-diag":
		return fetchDiagCommand(arguments[1:])
	case "-h", "--help":
		_, err := os.Stdout.WriteString(usageText + "\n")
		return 0, err
	}
	if knownCommands()[command] {
		return 0, fmt.Errorf(
			"command %s is not yet ported to the Go runtime (see docs/go-runtime-plan.md)",
			command,
		)
	}
	return 0, usageError("unknown command " + command + "\n" + usageText)
}

func main() {
	// The Node entry point's top-level try/catch converts every thrown
	// value -- including programmer-error TypeErrors -- into the generic
	// "error: <message>" branch. recover() reproduces that catch-all.
	code := func() (code int) {
		defer func() {
			if recovered := recover(); recovered != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", recovered)
				code = 1
			}
		}()
		status, err := run(os.Args[1:])
		if err == nil {
			return status
		}
		var exit *cliExit
		if errors.As(err, &exit) {
			stream := os.Stderr
			prefix := "error: "
			if exit.stdout {
				stream = os.Stdout
				prefix = ""
			}
			fmt.Fprintf(stream, "%s%s\n", prefix, exit.message)
			return exit.status
		}
		var failure *procerr.ProcessFailure
		if errors.As(err, &failure) {
			os.Stderr.WriteString(procerr.RenderCLIProcessFailure(failure))
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: %s\n", err.Error())
		return 1
	}()
	os.Exit(code)
}
