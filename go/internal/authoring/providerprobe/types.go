// Package providerprobe orchestrates the legacy and source-first provider
// probe libraries without owning CLI output or public artifact publication.
package providerprobe

import (
	"context"
	"fmt"
	"os"
)

// Mode identifies the categorical provider-probe contract selected by a
// recipe. LegacyV1 preserves the original implementation output;
// QualifiedV2 is the manifest-bound source-first contract.
type Mode string

const (
	// LegacyV1 is the frozen compatibility contract from
	// the original implementation.
	LegacyV1 Mode = "legacy_v1"
	// QualifiedV2 is the local-only source-first contract from
	// the authoring artifact contract section 4.
	QualifiedV2 Mode = "qualified_v2"
)

// Artifact is one detached provider-probe output. The allowed names depend on
// Result.Mode: the legacy five-file set or the qualified six-file core plus an
// optional diagnostic-only openapi-map.json.
type Artifact struct {
	Name  string
	Bytes []byte
}

// Result is one sealed in-memory provider-probe result. A4 deliberately does
// not publish these bytes to a caller-selected output directory; A6 owns that
// complete-set transaction.
type Result struct {
	mode          Mode
	artifacts     []Artifact
	markdownCopy  []byte
	workDirectory string
}

// Mode returns the selected provider-probe contract.
func (r Result) Mode() Mode { return r.mode }

// Artifacts returns a detached copy in deterministic contract order.
func (r Result) Artifacts() []Artifact {
	result := make([]Artifact, len(r.artifacts))
	for i, artifact := range r.artifacts {
		result[i] = Artifact{Name: artifact.Name, Bytes: append([]byte(nil), artifact.Bytes...)}
	}
	return result
}

// MarkdownCopy returns the sealed Markdown bytes for provider-probe's
// --markdown copy destination. Legacy v1 intentionally omits the published
// artifact-path appendix, matching the original implementation; qualified v2's
// source-first summary has no such appendix and reuses its sealed summary.md.
// A zero or manually incomplete Result is rejected rather than returning an
// ambiguous nil byte stream.
func (r Result) MarkdownCopy() ([]byte, error) {
	if r.mode != LegacyV1 && r.mode != QualifiedV2 {
		return nil, fmt.Errorf("provider probe result has unsupported mode %q", r.mode)
	}
	if r.markdownCopy == nil {
		return nil, fmt.Errorf("provider probe result has no Markdown copy")
	}
	return append([]byte(nil), r.markdownCopy...), nil
}

func markdownCopyFromArtifacts(artifacts []Artifact) ([]byte, error) {
	for _, artifact := range artifacts {
		if artifact.Name == "summary.md" {
			return append([]byte(nil), artifact.Bytes...), nil
		}
	}
	return nil, fmt.Errorf("provider probe result is missing summary.md")
}

// WorkDirectory returns the private legacy preparation directory. Qualified
// v2 performs no preparation and returns an empty path. The caller owns the
// legacy directory's lifecycle; A4 never recursively deletes a pathname.
func (r Result) WorkDirectory() string { return r.workDirectory }

// RunOptions selects a recipe and the explicitly bounded legacy preparation
// seams. RecipePath must identify a local recipe. WorkDirectory is used only
// by legacy v1; when empty, Run creates a private randomized directory and
// returns it in Result. Environment is the complete child environment for
// legacy Git/Terraform adapters and is never inherited implicitly.
type RunOptions struct {
	RecipePath    string
	WorkDirectory string
	Environment   map[string]string
	LegacyHost    LegacyHost
	// ExpectedMode, when non-empty, fails closed if the recipe changed modes
	// after a caller performed a read-only mode preflight.
	ExpectedMode Mode
}

// LegacyHost contains the only external preparation capabilities used by the
// frozen the original implementation compatibility path. Qualified v2
// never calls this interface.
type LegacyHost interface {
	Download(context.Context, DownloadRequest) error
	Clone(context.Context, CloneRequest) error
	CaptureTerraformSchema(context.Context, TerraformSchemaRequest) ([]byte, error)
}

// DownloadRequest asks the legacy host to materialize one pinned OpenAPI input
// inside the probe-owned private work directory.
type DownloadRequest struct {
	URL         string
	Destination string

	// legacyDestinationRoot/name are package-private descriptor bindings for
	// the production legacy host. Third-party test doubles receive the public
	// pathname contract unchanged.
	legacyDestinationRoot *os.Root
	legacyDestinationName string
}

// CloneRequest asks the legacy host to create one pinned provider checkout in
// the probe-owned private work directory.
type CloneRequest struct {
	Repository  string
	Revision    string
	Destination string

	legacyWorkspace *legacyWorkspaceBinding
}

// TerraformSchemaRequest asks the legacy host to run Terraform's provider
// schema capture in one probe-owned directory. MainHCL is the exact
// deterministic configuration derived from the original implementation.
type TerraformSchemaRequest struct {
	TerraformExecutable string
	Directory           string
	MainHCL             []byte
	Environment         map[string]string

	legacyWorkspace     *legacyWorkspaceBinding
	legacyDirectory     string
	legacyDirectoryInfo os.FileInfo
	legacyDirectoryRoot *os.Root
}

// cloneStringMap detaches caller-owned string maps at package boundaries.
func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
