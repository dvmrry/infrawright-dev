package providerprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/httptransport"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

const (
	legacyDownloadTimeout       = 60 * time.Second
	legacyDownloadLimit   int64 = 64 * 1024 * 1024
	legacyGitTimeout            = 60 * time.Second
	legacyGitStreamLimit  int64 = 64 * 1024
)

// defaultLegacyHost is the production implementation of the deliberately
// narrow legacy-only preparation boundary. Its environment is a snapshot: no
// subprocess assembled here inherits the parent process environment.
type defaultLegacyHost struct {
	environment      map[string]string
	newHTTPTransport func(context.Context) (collectors.HttpTransport, error)
	gitTimeout       time.Duration
	terraformTimeout time.Duration
}

var _ LegacyHost = (*defaultLegacyHost)(nil)

// newDefaultLegacyHost builds the legacy preparation host with a detached
// complete child environment. Qualified v2 never constructs or uses it.
func newDefaultLegacyHost(environment map[string]string) *defaultLegacyHost {
	snapshot := cloneStringMap(environment)
	if snapshot == nil {
		snapshot = make(map[string]string)
	}
	return &defaultLegacyHost{
		environment: snapshot,
		newHTTPTransport: func(parent context.Context) (collectors.HttpTransport, error) {
			timeoutMilliseconds := int(legacyDownloadTimeout / time.Millisecond)
			responseLimit := int(legacyDownloadLimit)
			return httptransport.NewContext(parent, snapshot, httptransport.Options{
				RequestTimeoutMs:   &timeoutMilliseconds,
				ResponseLimitBytes: &responseLimit,
			})
		},
		gitTimeout:       legacyGitTimeout,
		terraformTimeout: 5 * time.Minute,
	}
}

// Download materializes a file, HTTP, or HTTPS OpenAPI input through a
// private mode-0600 sibling temporary file. Error messages intentionally do
// not include the request URL or destination, both of which may be sensitive.
func (h *defaultLegacyHost) Download(ctx context.Context, request DownloadRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parsed, err := url.Parse(request.URL)
	if err != nil || parsed == nil {
		return errors.New("legacy OpenAPI download URL is invalid")
	}

	var body []byte
	switch parsed.Scheme {
	case "file":
		body, err = readStableLegacyFile(parsed)
	case "http", "https":
		body, err = h.downloadHTTP(ctx, parsed)
	default:
		return errors.New("legacy OpenAPI download URL uses an unsupported scheme")
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("legacy OpenAPI download failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.legacyDestinationRoot != nil && request.legacyDestinationName != "" {
		if err := materializeBoundLegacyDownload(ctx, request.legacyDestinationRoot, request.legacyDestinationName, body); err != nil {
			return err
		}
		return nil
	}
	if err := materializeLegacyDownload(ctx, request.Destination, body); err != nil {
		return err
	}
	return nil
}

func materializeBoundLegacyDownload(ctx context.Context, root *os.Root, name string, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := legacyAtomicWrite(root, name, body, 0o600); err != nil {
		return errors.New("legacy OpenAPI download could not materialize its destination")
	}
	return nil
}

func (h *defaultLegacyHost) downloadHTTP(ctx context.Context, target *url.URL) ([]byte, error) {
	transport, err := h.newHTTPTransport(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = transport.Close() }()

	response, err := transport.Request(collectors.HTTPRequest{
		Method:    http.MethodGet,
		URL:       target,
		Headers:   map[string]string{},
		TimeoutMs: int(legacyDownloadTimeout / time.Millisecond),
	})
	if err != nil {
		return nil, err
	}
	if response.Status < http.StatusOK || response.Status >= http.StatusMultipleChoices {
		return nil, errors.New("unexpected HTTP status")
	}
	return append([]byte(nil), response.Body...), nil
}

func readStableLegacyFile(target *url.URL) ([]byte, error) {
	if target.Host != "" && !strings.EqualFold(target.Host, "localhost") {
		return nil, errors.New("remote file URL host is not allowed")
	}
	path := filepath.FromSlash(target.Path)
	metadata, err := os.Lstat(path)
	if err != nil || !metadata.Mode().IsRegular() {
		return nil, errors.New("file URL does not identify a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedMetadata, err := file.Stat()
	if err != nil || !openedMetadata.Mode().IsRegular() || !os.SameFile(metadata, openedMetadata) {
		return nil, errors.New("file URL changed while opening")
	}
	reader := io.LimitReader(file, legacyDownloadLimit+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > legacyDownloadLimit {
		return nil, errors.New("file URL input exceeds limit")
	}
	return body, nil
}

func materializeLegacyDownload(ctx context.Context, destination string, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return errors.New("legacy OpenAPI download could not create its destination")
	}
	temporary, err := os.CreateTemp(directory, ".provider-probe-download-*")
	if err != nil {
		return errors.New("legacy OpenAPI download could not create its temporary file")
	}
	temporaryName := temporary.Name()
	temporaryInfo, err := temporary.Stat()
	if err != nil || !temporaryInfo.Mode().IsRegular() {
		_ = temporary.Close()
		return errors.New("legacy OpenAPI download could not inspect its temporary file")
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			removeCreatedRegularFile(temporaryName, temporaryInfo)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return errors.New("legacy OpenAPI download could not secure its temporary file")
	}
	if written, err := temporary.Write(body); err != nil || written != len(body) {
		_ = temporary.Close()
		return errors.New("legacy OpenAPI download could not write its temporary file")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("legacy OpenAPI download could not finish its temporary file")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return errors.New("legacy OpenAPI download could not materialize its destination")
	}
	removeTemporary = false
	return nil
}

func removeCreatedRegularFile(path string, created fs.FileInfo) {
	metadata, err := os.Lstat(path)
	if err == nil && metadata.Mode().IsRegular() && os.SameFile(created, metadata) {
		_ = os.Remove(path)
	}
}

// Clone runs the one pinned, non-interactive Git clone command allowed by the
// legacy contract. Neither command output nor repository inputs enter errors.
func (h *defaultLegacyHost) Clone(ctx context.Context, request CloneRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	executable, err := resolveLegacyGitExecutable(h.environment)
	if err != nil {
		return errors.New("legacy Git executable is unavailable")
	}
	if request.legacyWorkspace != nil {
		if err := request.legacyWorkspace.recheckPublicPath(); err != nil {
			return errors.New("legacy Git destination is unavailable")
		}
	}
	if err := runLegacyGit(ctx, executable, request, h.gitEnvironment(), h.gitTimeout); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return errors.New("legacy Git clone failed")
	}
	return nil
}

func (h *defaultLegacyHost) gitEnvironment() map[string]string {
	environment := cloneStringMap(h.environment)
	environment["LC_ALL"] = "C"
	environment["GIT_TERMINAL_PROMPT"] = "0"
	environment["GIT_ASKPASS"] = legacyGitAskPass()
	environment["GIT_CONFIG_NOSYSTEM"] = "1"
	environment["GIT_CONFIG_SYSTEM"] = legacyGitNullDevice()
	environment["GIT_CONFIG_GLOBAL"] = legacyGitNullDevice()
	environment["GIT_CONFIG_COUNT"] = "0"
	environment["GIT_PAGER"] = "cat"
	environment["PAGER"] = "cat"
	return environment
}

func resolveLegacyGitExecutable(environment map[string]string) (string, error) {
	pathValue, ok := environment["PATH"]
	if !ok {
		return "", fs.ErrNotExist
	}
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			directory = "."
		}
		candidate := filepath.Join(directory, legacyGitExecutableName())
		metadata, err := os.Stat(candidate)
		if err != nil || !metadata.Mode().IsRegular() || !legacyGitExecutableAllowed(metadata) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		metadata, err = os.Stat(resolved)
		if err != nil || !metadata.Mode().IsRegular() || !legacyGitExecutableAllowed(metadata) {
			continue
		}
		absolute, err := filepath.Abs(resolved)
		if err == nil {
			return absolute, nil
		}
	}
	return "", fs.ErrNotExist
}

// CaptureTerraformSchema writes the exact request configuration privately,
// runs the two fixed Terraform commands, and returns one detached JSON object.
func (h *defaultLegacyHost) CaptureTerraformSchema(ctx context.Context, request TerraformSchemaRequest) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directoryRoot, directoryInfo, closeRoot, err := legacyTerraformDirectory(request)
	if err != nil {
		return nil, errors.New("legacy Terraform schema directory could not be created")
	}
	defer closeRoot()
	if err := legacyAtomicWrite(directoryRoot, "main.tf", request.MainHCL, 0o600); err != nil {
		return nil, errors.New("legacy Terraform configuration could not be written")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.legacyWorkspace != nil {
		if err := request.legacyWorkspace.verifyDirectoryPath(request.legacyDirectory, directoryInfo); err != nil {
			return nil, errors.New("legacy Terraform schema directory changed before execution")
		}
	} else if err := verifyLegacyTerraformDirectoryPath(request.Directory, directoryInfo); err != nil {
		return nil, errors.New("legacy Terraform schema directory changed before execution")
	}
	environment := cloneStringMap(request.Environment)
	executable, err := terraformcmd.ResolveTerraformExecutable(request.TerraformExecutable, environment)
	if err != nil {
		return nil, errors.New("legacy Terraform executable is unavailable")
	}
	timeoutMilliseconds := int64(h.terraformTimeout / time.Millisecond)
	limits := terraformcmd.DefaultTerraformCommandLimits()
	limits.TimeoutMs = &timeoutMilliseconds
	if _, err := terraformcmd.RunTerraformCommandContext(ctx, terraformcmd.TerraformCommandOptions{
		TerraformExecutable: executable,
		Argv:                []string{"init", "-backend=false"},
		CWD:                 request.Directory,
		Environment:         environment,
		Limits:              &limits,
		Output:              terraformcmd.TerraformCommandOutputDiscard,
	}); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("legacy Terraform initialization failed: %w", err)
	}
	result, err := terraformcmd.RunTerraformCommandContext(ctx, terraformcmd.TerraformCommandOptions{
		TerraformExecutable: executable,
		Argv:                []string{"providers", "schema", "-json"},
		CWD:                 request.Directory,
		Environment:         environment,
		Limits:              &limits,
		Output:              terraformcmd.TerraformCommandOutputCapture,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("legacy Terraform schema capture failed: %w", err)
	}
	if !validJSONObject(result.Stdout) {
		return nil, errors.New("legacy Terraform schema output must be exactly one JSON object")
	}
	return append([]byte(nil), result.Stdout...), nil
}

// legacyTerraformDirectory returns a descriptor-bound private directory for
// main.tf. The normal legacy path supplies the verified parent binding; the
// fallback preserves the public host contract for direct callers and test
// doubles, but cannot protect an untrusted caller's parent pathname.
func legacyTerraformDirectory(request TerraformSchemaRequest) (*os.Root, os.FileInfo, func(), error) {
	if request.legacyDirectoryRoot != nil && request.legacyDirectoryInfo != nil {
		bound, err := request.legacyDirectoryRoot.Stat(".")
		if err != nil || !privateDirectory(bound) || !os.SameFile(request.legacyDirectoryInfo, bound) {
			return nil, nil, func() {}, errors.New("bound Terraform directory changed")
		}
		return request.legacyDirectoryRoot, request.legacyDirectoryInfo, func() {}, nil
	}
	if err := os.MkdirAll(request.Directory, 0o700); err != nil {
		return nil, nil, func() {}, err
	}
	info, err := os.Lstat(request.Directory)
	if err != nil || !privateDirectory(info) {
		return nil, nil, func() {}, errors.New("Terraform directory is unsafe")
	}
	root, err := os.OpenRoot(request.Directory)
	if err != nil {
		return nil, nil, func() {}, err
	}
	bound, err := root.Stat(".")
	if err != nil || !privateDirectory(bound) || !os.SameFile(info, bound) {
		_ = root.Close()
		return nil, nil, func() {}, errors.New("Terraform directory changed while binding")
	}
	return root, info, func() { _ = root.Close() }, nil
}

func verifyLegacyTerraformDirectoryPath(directory string, expected os.FileInfo) error {
	current, err := os.Lstat(directory)
	if err != nil || !privateDirectory(current) || !os.SameFile(expected, current) {
		return errors.New("Terraform directory changed")
	}
	return nil
}

func validJSONObject(input []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(input))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return false
	}
	if object == nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func sortedEnvironment(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}

type boundedGitOutput struct {
	mu       sync.Mutex
	count    int64
	limit    int64
	exceeded bool
	stop     func()
}

func (output *boundedGitOutput) Write(value []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if int64(len(value)) <= output.limit-output.count {
		output.count += int64(len(value))
		return len(value), nil
	}
	output.exceeded = true
	output.stop()
	return 0, errors.New("legacy Git output exceeded the stream limit")
}

func (output *boundedGitOutput) Exceeded() bool {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.exceeded
}

func runLegacyGit(ctx context.Context, executable string, request CloneRequest, environment map[string]string, timeout time.Duration) error {
	return runLegacyGitProcess(ctx, executable, []string{
		"clone", "--depth", "1", "--branch", request.Revision, "--", request.Repository, request.Destination,
	}, sortedEnvironment(environment), timeout, legacyGitStreamLimit)
}

func legacyGitExecutableAllowed(metadata fs.FileInfo) bool {
	return runtime.GOOS == "windows" || metadata.Mode().Perm()&0o111 != 0
}
