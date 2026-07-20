// Command iw is the Go port of the Infrawright CLI entry point
// (node-src/cli/main.ts). Cobra owns command discovery, parsing, help,
// completion, and usage errors. Domain outputs, artifacts, reports, exit
// classifications, environment precedence, and lifecycle safety gates retain
// the qualified Go-port contracts. Filesystem and CLI presentation text are
// Go-native throughout (docs/go-runtime-v2.md §2).
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

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

type commandFlags map[string]struct{}

func (f commandFlags) Has(name string) bool {
	_, ok := f[name]
	return ok
}

// commandInput is the small, parser-neutral value Cobra passes to command
// domain adapters after it has resolved flags, options, and positionals.
type commandInput struct {
	Flags       commandFlags
	Options     map[string][]string
	Positionals []string
}

func lastCommandOption(parsed commandInput, name string) (string, bool) {
	values := parsed.Options[name]
	if len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
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
	return executeStandaloneCobra(newRootCatalogCobraCommand(), arguments)
}

func rootCatalogInput(parsed commandInput) (int, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return 0, err
	}
	output, hasOutput := lastCommandOption(parsed, "--out")
	check, hasCheck := lastCommandOption(parsed, "--check")
	if hasOutput && hasCheck {
		return 0, usageError("root-catalog accepts only one of --out or --check")
	}
	providersValue, hasProviders := lastCommandOption(parsed, "--providers")
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
	root, hasRoot := lastCommandOption(parsed, "--root")
	if !hasRoot {
		if env, ok := lookupEnv("INFRAWRIGHT_PACKS"); ok {
			root = env
		} else {
			root = filepath.Join(rootDirectory, "packs")
		}
	}
	profile, hasProfile := lastCommandOption(parsed, "--profile")
	if !hasProfile {
		if env, ok := lookupEnv("INFRAWRIGHT_PACK_PROFILE"); ok {
			profile = env
		} else {
			profile = filepath.Join(rootDirectory, "packsets", "full.json")
		}
	}
	catalog, hasCatalog := lastCommandOption(parsed, "--catalog")
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
	return executeStandaloneCobra(newTransformCobraCommand(), arguments)
}

func transformCommandInput(parsed commandInput) (int, error) {
	rootDirectory, err := packageRoot()
	if err != nil {
		return 0, err
	}
	root, hasRoot := lastCommandOption(parsed, "--root")
	if !hasRoot {
		if env := os.Getenv("INFRAWRIGHT_PACKS"); env != "" {
			root = env
		} else {
			root = filepath.Join(rootDirectory, "packs")
		}
	}
	profile, hasProfile := lastCommandOption(parsed, "--profile")
	if !hasProfile {
		if env := os.Getenv("INFRAWRIGHT_PACK_PROFILE"); env != "" {
			profile = env
		} else {
			profile = filepath.Join(rootDirectory, "packsets", "full.json")
		}
	}
	catalog, hasCatalog := lastCommandOption(parsed, "--catalog")
	if !hasCatalog {
		catalog = filepath.Join(rootDirectory, "packsets", "full.json")
	}
	input, hasInput := lastCommandOption(parsed, "--in")
	tenant, hasTenant := lastCommandOption(parsed, "--tenant")
	if !hasInput || !hasTenant {
		return 0, usageError("transform requires --in and --tenant")
	}
	selectedDeployment, hasDeployment := lastCommandOption(parsed, "--deployment")
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

// run ports the main dispatch in node-src/cli/main.ts. The retained authoring
// surface is served by this same binary; there is no Node fallback or second
// authoring executable.
func run(arguments []string) (int, error) {
	return runCobra(arguments)
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
