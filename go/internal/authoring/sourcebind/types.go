package sourcebind

import (
	"context"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

// LocalRoots supplies the only local locations accepted by LoadVerified. All
// roots and ManifestPath must be absolute, non-NUL paths and must remain
// immutable for the duration of loading. Every analyzer byte is exact-hash
// bound and post-Git checked. The provider go.mod local-replace path is used
// only to prove directory identity; SDKRoots remains the sole SDK byte locator.
type LocalRoots struct {
	ManifestPath string
	ProviderRoot string
	SDKRoots     map[string]string
	SchemaRoot   string
	OpenAPIRoot  string
}

// CapturedFile is one caller-owned, stable byte snapshot. Path is always the
// portable manifest-relative path; no local root is retained or rendered.
type CapturedFile struct {
	Path   string
	Bytes  []byte
	SHA256 string
}

// CapturedTree contains only bytes already accepted by the source binding.
// Files are sorted by their portable paths.
type CapturedTree struct {
	ModulePath string
	Files      []CapturedFile
}

// OpenAPIStatus isolates an optional adapter result from verified source
// inputs. An unavailable adapter never invalidates Provider, SDKs, or Schema.
type OpenAPIStatus struct {
	Available bool
	Files     []CapturedFile
	Err       error
}

type verifiedState struct {
	Manifest              contracts.SourceProvenance
	ManifestBytes         []byte
	ManifestSHA256        string
	Provider              CapturedTree
	ProviderModule        []CapturedFile
	SDKs                  map[string]CapturedTree
	TerraformSchema       CapturedFile
	OpenAPI               OpenAPIStatus
	InputProvenance       contracts.InputProvenance
	InputProvenanceBytes  []byte
	InputProvenanceSHA256 string
}

// VerifiedInputs is the opaque result of LoadVerified. Its loader-owned state
// cannot be mutated outside this package.
type VerifiedInputs struct{ state *verifiedState }

// QualifiedInputs is the opaque A1-facing view returned by
// RequireQualification. Snapshot returns detached data for analysis.
type QualifiedInputs struct{ state *verifiedState }

// VerifiedSnapshot is one detached copy of loader-owned verified input state.
// Mutating any field, map, slice, pointer, or byte buffer cannot change later
// snapshots from the same QualifiedInputs.
type VerifiedSnapshot struct {
	Manifest              contracts.SourceProvenance
	ManifestBytes         []byte
	ManifestSHA256        string
	Provider              CapturedTree
	ProviderModule        []CapturedFile
	SDKs                  map[string]CapturedTree
	TerraformSchema       CapturedFile
	OpenAPI               OpenAPIStatus
	InputProvenance       contracts.InputProvenance
	InputProvenanceBytes  []byte
	InputProvenanceSHA256 string
}

// RequireQualification is the A1 consumption seam for qualifying source
// evidence. It rejects zero or manually assembled VerifiedInputs. Contract
// renderers are document codecs, not trust attestations, and do not mint this
// view.
func RequireQualification(inputs VerifiedInputs) (QualifiedInputs, error) {
	if inputs.state == nil {
		return QualifiedInputs{}, failure(ErrorQualification, "qualification", "verified source inputs must come from LoadVerified")
	}
	return QualifiedInputs{state: inputs.state}, nil
}

// Snapshot returns a defensive deep copy of the exact loader-owned state and
// rejects a zero or manually constructed qualified view.
func (inputs QualifiedInputs) Snapshot() (VerifiedSnapshot, error) {
	if inputs.state == nil {
		return VerifiedSnapshot{}, failure(ErrorQualification, "qualification", "qualified inputs must come from RequireQualification")
	}
	return cloneVerifiedState(inputs.state), nil
}

// UnverifiedRoots explicitly opts into diagnostic-only local source loading.
// No manifest, revision, or repository identity is accepted or claimed.
type UnverifiedRoots struct {
	ProviderRoot       string
	ProviderModulePath string
	ProviderFiles      []string
	SchemaRoot         string
	TerraformSchema    string
	SDKRoots           map[string]string
	SDKFiles           map[string][]string
	SDKVersions        map[string]string
	Selection          contracts.SelectionBinding
}

// UnverifiedInputs contains an explicitly non-qualifying captured source set.
// It intentionally has no qualification capability or manifest digest.
type UnverifiedInputs struct {
	Observation           contracts.UnverifiedSourceObservation
	Provider              CapturedTree
	SDKs                  map[string]CapturedTree
	TerraformSchema       CapturedFile
	InputProvenance       contracts.InputProvenance
	InputProvenanceBytes  []byte
	InputProvenanceSHA256 string
}

// GitResult is the bounded output of a local-only Git command.
type GitResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// GitRunner runs one local Git command. Implementations must not invoke a
// shell, clone/fetch, prompt, or access a network remote.
type GitRunner interface {
	Run(context.Context, string, []string) (GitResult, error)
}

type loadOptions struct {
	gitRunner GitRunner
	timeout   time.Duration
	read      artifacts.StableReadOptions
}
