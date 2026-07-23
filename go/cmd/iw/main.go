// Command iw is the Infrawright CLI entry point. Cobra owns command discovery,
// parsing, help, completion, and usage errors. Domain packages own outputs,
// artifacts, reports, exit classifications, and lifecycle safety gates.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

// cliExit ports the CliExit class in the original implementation: a terminal
// outcome carrying its exit status and output stream selection.
type cliExit struct {
	message string
	status  int
	stdout  bool
}

func (e *cliExit) Error() string { return e.message }

// usageError ports usageError in the original implementation.
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

// packageRoot locates the runtime data root. An explicit
// INFRAWRIGHT_PACKAGE_ROOT wins so a released binary can live outside the
// packs tree; otherwise the search walks upward from the executable until it
// finds the packs directory and its full pack-set document shipped with the
// runtime.
func packageRoot() (string, error) {
	if configured, ok := os.LookupEnv("INFRAWRIGHT_PACKAGE_ROOT"); ok {
		if configured == "" {
			return "", errors.New("INFRAWRIGHT_PACKAGE_ROOT must not be empty")
		}
		absolute, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("resolve INFRAWRIGHT_PACKAGE_ROOT: %w", err)
		}
		return absolute, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return findPackageRoot(filepath.Dir(executable))
}

func findPackageRoot(start string) (string, error) {
	current := filepath.Clean(start)
	for {
		packs, packsErr := os.Stat(filepath.Join(current, "packs"))
		profile, profileErr := os.Stat(filepath.Join(current, "packs", "full.packset.json"))
		if packsErr == nil && profileErr == nil && packs.IsDir() && profile.Mode().IsRegular() {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("unable to locate the Infrawright package root")
		}
		current = parent
	}
}

// transformCommand runs the transform command.
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
			profile = filepath.Join(rootDirectory, "packs", "full.packset.json")
		}
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

// run ports the main dispatch in the original implementation. The retained authoring
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
