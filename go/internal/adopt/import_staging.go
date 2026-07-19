package adopt

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// ImportStagingTerraformRequest ports the ImportStagingTerraformRequest
// interface from node-src/domain/import-staging.ts.
type ImportStagingTerraformRequest struct {
	BackendConfig *string
	Directory     string
	Label         string
	Tenant        string
}

// ImportStagingStateResult ports the success/stdout result returned by
// ImportStagingTerraform.listState in node-src/domain/import-staging.ts.
type ImportStagingStateResult struct {
	Success bool
	Stdout  string
}

// ImportStagingTerraform ports the ImportStagingTerraform interface from
// node-src/domain/import-staging.ts.
type ImportStagingTerraform interface {
	Initialize(ImportStagingTerraformRequest) error
	ListState(ImportStagingTerraformRequest) (ImportStagingStateResult, error)
}

// ImportStagingTerraformOptions ports the options accepted by
// createImportStagingTerraform in node-src/domain/import-staging.ts.
type ImportStagingTerraformOptions struct {
	Environment         map[string]string
	TerraformExecutable string
}

// StageImportsResult ports the StageImportsResult interface from
// node-src/domain/import-staging.ts.
type StageImportsResult struct {
	Sources int
	Staged  int
}

// UnstageImportsResult ports the UnstageImportsResult interface from
// node-src/domain/import-staging.ts.
type UnstageImportsResult struct {
	Removed int
}

// StageImportsOptions ports the options accepted by stageImports in
// node-src/domain/import-staging.ts.
type StageImportsOptions struct {
	BackendConfig *string
	Deployment    deployment.Deployment
	OnDiagnostic  func(string)
	Root          metadata.LoadedPackRoot
	Selectors     []string
	StateAware    bool
	Tenant        string
	Terraform     ImportStagingTerraform
	Workspace     string
	copyHooks     *stagingCopyHooks
}

// UnstageImportsOptions ports the options accepted by unstageImports in
// node-src/domain/import-staging.ts.
type UnstageImportsOptions struct {
	Deployment   deployment.Deployment
	OnDiagnostic func(string)
	Root         metadata.LoadedPackRoot
	Selectors    []string
	Tenant       string
	Workspace    string
}

type importStagingTerraform struct {
	environment         map[string]string
	terraformExecutable string
}

// stagingCopyHooks is a dependency-injected test seam for deterministic
// failures after each preparation phase. Production always passes nil.
type stagingCopyHooks struct {
	afterCopy        func() error
	afterChmod       func() error
	afterClose       func() error
	closeAfterRename func(*os.Root) error
}

var _ ImportStagingTerraform = (*importStagingTerraform)(nil)

var errStagingArtifactIdentityChanged = errors.New(
	"staging artifact transaction identity changed before publication",
)

func stagingFailure(code, message string, category procerr.Category) error {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: category,
		Message:  message,
	})
}

func cloneStagingEnvironment(environment map[string]string) map[string]string {
	output := make(map[string]string, len(environment))
	for key, value := range environment {
		output[key] = value
	}
	return output
}

// CreateImportStagingTerraform adapts the bounded generic Terraform runner for
// staging-only init/state-list calls. It ports createImportStagingTerraform
// from node-src/domain/import-staging.ts.
func CreateImportStagingTerraform(options ImportStagingTerraformOptions) ImportStagingTerraform {
	return &importStagingTerraform{
		environment:         cloneStagingEnvironment(options.Environment),
		terraformExecutable: options.TerraformExecutable,
	}
}

func (t *importStagingTerraform) Initialize(request ImportStagingTerraformRequest) error {
	argv := []string{"init", "-input=false"}
	if request.BackendConfig != nil && *request.BackendConfig != "" {
		argv = append(argv,
			"-reconfigure",
			"-backend-config="+*request.BackendConfig,
			fmt.Sprintf("-backend-config=key=%s/%s.tfstate", request.Tenant, request.Label),
		)
	}
	_, err := terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: t.terraformExecutable,
		Argv:                argv,
		CWD:                 request.Directory,
		Environment:         t.environment,
		Output:              terraformcmd.TerraformCommandOutputDiscard,
	})
	return err
}

func decodeTerraformStateList(content []byte) (string, error) {
	if !utf8.Valid(content) {
		return "", stagingFailure(
			"INVALID_TERRAFORM_STATE_LIST",
			"terraform state list output is not valid UTF-8",
			procerr.CategoryDomain,
		)
	}
	return string(content), nil
}

func (t *importStagingTerraform) ListState(request ImportStagingTerraformRequest) (ImportStagingStateResult, error) {
	result, err := terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: t.terraformExecutable,
		Argv:                []string{"state", "list"},
		CWD:                 request.Directory,
		Environment:         t.environment,
		Output:              terraformcmd.TerraformCommandOutputCapture,
	})
	if err != nil {
		var failure *procerr.ProcessFailure
		if errors.As(err, &failure) && failure.Code == "TERRAFORM_COMMAND_FAILED" {
			return ImportStagingStateResult{Success: false, Stdout: ""}, nil
		}
		return ImportStagingStateResult{}, err
	}
	stdout, err := decodeTerraformStateList(result.Stdout)
	if err != nil {
		return ImportStagingStateResult{}, err
	}
	return ImportStagingStateResult{Success: true, Stdout: stdout}, nil
}

func stagingExists(file string) bool {
	_, err := os.Stat(file)
	return err == nil
}

func stagingIsDirectory(directory string) bool {
	info, err := os.Stat(directory)
	return err == nil && info.IsDir()
}

func removeStagedFileIfPresent(file string) (bool, error) {
	if !stagingExists(file) {
		return false, nil
	}
	info, err := os.Lstat(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() {
		return false, &os.PathError{Op: "unlink", Path: file, Err: errors.New("is a directory")}
	}
	if err := os.Remove(file); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func workspacePath(workspace, candidate string) string {
	if filepath.IsAbs(candidate) {
		return candidate
	}
	resolved, err := filepath.Abs(filepath.Join(workspace, filepath.FromSlash(candidate)))
	if err != nil {
		return filepath.Clean(filepath.Join(workspace, filepath.FromSlash(candidate)))
	}
	return resolved
}

func environmentRootDirectory(workspace string, dep deployment.Deployment, tenant, label string) (string, error) {
	envs, err := deployment.DeploymentEnvsDir(dep, tenant)
	if err != nil {
		return "", err
	}
	return workspacePath(workspace, filepath.Join(filepath.FromSlash(envs), label)), nil
}

func noteWholeRoot(diagnostic roots.WholeRootDiagnostic, onDiagnostic func(string)) {
	onDiagnostic("NOTE: " + diagnostic.Message)
}

func readPythonTextUTF8(file, label string) (string, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return "", stagingFailure("READ_FAILED", "unable to read "+label, procerr.CategoryIO)
	}
	if !utf8.Valid(content) {
		return "", stagingFailure("INVALID_UTF8", label+" is not valid UTF-8", procerr.CategoryDomain)
	}
	text := string(content)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n"), nil
}

func requireBackendConfiguration(backendConfig *string, directory, label string) error {
	if backendConfig != nil && *backendConfig != "" {
		return nil
	}
	mainPath := filepath.Join(directory, "main.tf")
	if !stagingExists(mainPath) {
		return nil
	}
	text, err := readPythonTextUTF8(mainPath, label+" environment root")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, `  backend "`) {
			return stagingFailure(
				"BACKEND_CONFIG_REQUIRED",
				label+" declares a remote backend; run with BACKEND_CONFIG=<file>",
				procerr.CategoryDomain,
			)
		}
	}
	return nil
}

func terraformStateSeparator(text string, index int) int {
	if text[index] == '\r' && index+1 < len(text) && text[index+1] == '\n' {
		return 2
	}
	r, size := utf8.DecodeRuneInString(text[index:])
	switch r {
	case '\n', '\v', '\f', '\r', '\x1c', '\x1d', '\x1e', '\x85', '\u2028', '\u2029':
		return size
	default:
		return 0
	}
}

func stateAddresses(stdout string) []string {
	if stdout == "" {
		return []string{}
	}
	addresses := []string{}
	start := 0
	for index := 0; index < len(stdout); {
		separator := terraformStateSeparator(stdout, index)
		if separator == 0 {
			_, size := utf8.DecodeRuneInString(stdout[index:])
			index += size
			continue
		}
		addresses = append(addresses, stdout[start:index])
		index += separator
		start = index
	}
	if start < len(stdout) {
		addresses = append(addresses, stdout[start:])
	}
	return addresses
}

func createStagingTemporaryFile(root *os.Root, destinationBase string) (*os.File, string, error) {
	for range 16 {
		name := "." + destinationBase + ".infrawright-stage-" + rand.Text()
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("unable to create randomized staging artifact transaction")
}

func joinStagingCopyFailure(primary error, cleanup ...error) error {
	errs := []error{primary}
	for _, err := range cleanup {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func copyStagingArtifact(source, destination string, hooks *stagingCopyHooks) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	info, err := input.Stat()
	if err != nil {
		return joinStagingCopyFailure(err, input.Close())
	}

	directory, destinationBase := filepath.Dir(destination), filepath.Base(destination)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return joinStagingCopyFailure(err, input.Close())
	}
	output, temporaryName, err := createStagingTemporaryFile(root, destinationBase)
	if err != nil {
		return joinStagingCopyFailure(err, input.Close(), root.Close())
	}
	outputOpen := true
	inputOpen := true
	abort := func(primary error) error {
		var outputCloseErr, inputCloseErr error
		if outputOpen {
			outputCloseErr = output.Close()
			outputOpen = false
		}
		if inputOpen {
			inputCloseErr = input.Close()
			inputOpen = false
		}
		removeErr := root.Remove(temporaryName)
		rootCloseErr := root.Close()
		return joinStagingCopyFailure(primary, outputCloseErr, inputCloseErr, removeErr, rootCloseErr)
	}

	_, copyErr := io.Copy(output, input)
	if copyErr != nil {
		return abort(copyErr)
	}
	if hooks != nil && hooks.afterCopy != nil {
		if err := hooks.afterCopy(); err != nil {
			return abort(err)
		}
	}
	chmodErr := output.Chmod(info.Mode().Perm())
	if chmodErr != nil {
		return abort(chmodErr)
	}
	if hooks != nil && hooks.afterChmod != nil {
		if err := hooks.afterChmod(); err != nil {
			return abort(err)
		}
	}
	temporaryInfo, err := output.Stat()
	if err != nil {
		return abort(err)
	}
	if err := output.Close(); err != nil {
		outputOpen = false
		return abort(err)
	}
	outputOpen = false
	if err := input.Close(); err != nil {
		inputOpen = false
		return abort(err)
	}
	inputOpen = false
	if hooks != nil && hooks.afterClose != nil {
		if err := hooks.afterClose(); err != nil {
			return abort(err)
		}
	}
	currentInfo, err := root.Lstat(temporaryName)
	if err != nil {
		return abort(err)
	}
	if !currentInfo.Mode().IsRegular() || currentInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(temporaryInfo, currentInfo) {
		return abort(errStagingArtifactIdentityChanged)
	}
	if err := root.Rename(temporaryName, destinationBase); err != nil {
		return abort(err)
	}
	// Rename is the transaction commit point: the destination now contains the
	// complete, verified replacement. A subsequent directory-descriptor close
	// error cannot roll that publication back, so reporting it as an operation
	// failure would falsely tell the caller that staging did not occur.
	if hooks != nil && hooks.closeAfterRename != nil {
		_ = hooks.closeAfterRename(root)
	} else {
		_ = root.Close()
	}
	return nil
}

// StageImports copies generated import/move artifacts into complete
// deployment-selected roots. It ports stageImports from
// node-src/domain/import-staging.ts.
func StageImports(options StageImportsOptions) (StageImportsResult, error) {
	if err := roots.ValidateTenant(options.Tenant); err != nil {
		return StageImportsResult{}, err
	}
	tenant := options.Tenant
	selected, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root:       options.Root,
		Deployment: options.Deployment,
		Tenant:     &tenant,
		Selectors:  append([]string(nil), options.Selectors...),
	})
	if err != nil {
		return StageImportsResult{}, err
	}
	onDiagnostic := options.OnDiagnostic
	if onDiagnostic == nil {
		onDiagnostic = func(string) {}
	}
	for _, diagnostic := range selected.Diagnostics {
		noteWholeRoot(diagnostic, onDiagnostic)
	}

	var backendConfig *string
	if options.BackendConfig != nil && *options.BackendConfig != "" {
		resolved := workspacePath(options.Workspace, *options.BackendConfig)
		backendConfig = &resolved
	}
	sources := 0
	staged := 0
	for _, selectedRoot := range selected.Topology.Roots {
		directory, err := environmentRootDirectory(
			options.Workspace,
			options.Deployment,
			options.Tenant,
			selectedRoot.Label,
		)
		if err != nil {
			return StageImportsResult{}, err
		}
		stateLoaded := false
		var rootStateAddresses []string
		loadRootStateAddresses := func() ([]string, error) {
			if stateLoaded {
				return rootStateAddresses, nil
			}
			if options.Terraform == nil {
				return nil, stagingFailure(
					"TERRAFORM_REQUIRED",
					"state-aware import staging requires Terraform",
					procerr.CategoryDomain,
				)
			}
			if err := requireBackendConfiguration(backendConfig, directory, selectedRoot.Label); err != nil {
				return nil, err
			}
			request := ImportStagingTerraformRequest{
				BackendConfig: backendConfig,
				Directory:     directory,
				Label:         selectedRoot.Label,
				Tenant:        options.Tenant,
			}
			if err := options.Terraform.Initialize(request); err != nil {
				return nil, err
			}
			state, err := options.Terraform.ListState(request)
			if err != nil {
				return nil, err
			}
			if state.Success {
				rootStateAddresses = stateAddresses(state.Stdout)
			}
			stateLoaded = true
			return rootStateAddresses, nil
		}
		for _, resourceType := range selectedRoot.Members {
			artifacts, err := tfrender.ComputeTransformArtifactPaths(
				options.Deployment,
				resourceType,
				options.Tenant,
			)
			if err != nil {
				return StageImportsResult{}, err
			}
			artifactSources := []struct {
				kind   string
				source string
			}{
				{kind: "imports", source: artifacts.Imports},
				{kind: "moves", source: artifacts.Moves},
			}
			for _, artifact := range artifactSources {
				source := workspacePath(options.Workspace, artifact.source)
				if !stagingExists(source) {
					continue
				}
				sources++
				basename := filepath.Base(source)
				if !stagingIsDirectory(directory) {
					onDiagnostic(fmt.Sprintf(
						"skip %s (no env root %s - run make gen-env)",
						basename,
						directory,
					))
					continue
				}
				destination := filepath.Join(directory, basename)
				if artifact.kind == "imports" && options.StateAware {
					addresses, err := loadRootStateAddresses()
					if err != nil {
						return StageImportsResult{}, err
					}
					text, err := readPythonTextUTF8(source, resourceType+" imports")
					if err != nil {
						return StageImportsResult{}, err
					}
					filtered, err := FilterGeneratedImports(text, addresses)
					if err != nil {
						return StageImportsResult{}, err
					}
					if filtered.Text != "" {
						if err := os.WriteFile(destination, []byte(filtered.Text), 0o666); err != nil {
							return StageImportsResult{}, err
						}
						onDiagnostic(fmt.Sprintf(
							"%d import(s) kept, %d already managed (skipped)",
							filtered.Kept,
							filtered.Skipped,
						))
					} else {
						if _, err := removeStagedFileIfPresent(destination); err != nil {
							return StageImportsResult{}, err
						}
						onDiagnostic("skip " + basename + " (every import already managed - delta is empty)")
						continue
					}
				} else if err := copyStagingArtifact(source, destination, options.copyHooks); err != nil {
					return StageImportsResult{}, err
				}
				onDiagnostic("staged " + destination)
				staged++
			}
		}
	}
	if sources == 0 {
		return StageImportsResult{}, stagingFailure(
			"NO_IMPORT_ARTIFACTS",
			"nothing to stage for TENANT="+options.Tenant+" (run make transform or make adopt first)",
			procerr.CategoryDomain,
		)
	}
	if staged == 0 {
		onDiagnostic("NOTE: 0 staged - every import is already managed or no roots matched")
	}
	return StageImportsResult{Sources: sources, Staged: staged}, nil
}

// UnstageImports removes only staged import/move copies from selected
// materialized roots. It ports unstageImports from
// node-src/domain/import-staging.ts.
func UnstageImports(options UnstageImportsOptions) (UnstageImportsResult, error) {
	if err := roots.ValidateTenant(options.Tenant); err != nil {
		return UnstageImportsResult{}, err
	}
	tenant := options.Tenant
	selected, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root:       options.Root,
		Deployment: options.Deployment,
		Tenant:     &tenant,
		Selectors:  append([]string(nil), options.Selectors...),
	})
	if err != nil {
		return UnstageImportsResult{}, err
	}
	onDiagnostic := options.OnDiagnostic
	if onDiagnostic == nil {
		onDiagnostic = func(string) {}
	}
	diagnostics := make(map[string]roots.WholeRootDiagnostic, len(selected.Diagnostics))
	for _, diagnostic := range selected.Diagnostics {
		diagnostics[diagnostic.Root] = diagnostic
	}

	removed := 0
	for _, selectedRoot := range selected.Topology.Roots {
		directory, err := environmentRootDirectory(
			options.Workspace,
			options.Deployment,
			options.Tenant,
			selectedRoot.Label,
		)
		if err != nil {
			return UnstageImportsResult{}, err
		}
		if !stagingIsDirectory(directory) {
			continue
		}
		if diagnostic, ok := diagnostics[selectedRoot.Label]; ok {
			noteWholeRoot(diagnostic, onDiagnostic)
		}
		for _, resourceType := range selectedRoot.Members {
			for _, suffix := range []string{"_imports.tf", "_moves.tf"} {
				target := filepath.Join(directory, resourceType+suffix)
				removedNow, err := removeStagedFileIfPresent(target)
				if err != nil {
					return UnstageImportsResult{}, err
				}
				if removedNow {
					onDiagnostic("removed " + target)
					removed++
				}
			}
		}
	}
	onDiagnostic(fmt.Sprintf("%d file(s) removed", removed))
	return UnstageImportsResult{Removed: removed}, nil
}
