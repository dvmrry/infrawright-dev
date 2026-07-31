// Package providerprobe orchestrates the source-first provider probe library
// without owning CLI output or public artifact publication.
package providerprobe

import (
	"fmt"
)

// Mode identifies the categorical provider-probe contract selected by a
// recipe. QualifiedV2, the manifest-bound source-first contract, is the only
// mode: the frozen LegacyV1 compatibility lane was retired with the
// proof-of-concept recipes that used it.
type Mode string

const (
	// QualifiedV2 is the local-only source-first contract from
	// the authoring artifact contract section 4.
	QualifiedV2 Mode = "qualified_v2"
)

// Artifact is one detached provider-probe output: the qualified six-file core
// plus an optional diagnostic-only openapi-map.json.
type Artifact struct {
	Name  string
	Bytes []byte
}

// Result is one sealed in-memory provider-probe result. A4 deliberately does
// not publish these bytes to a caller-selected output directory; A6 owns that
// complete-set transaction.
type Result struct {
	mode         Mode
	artifacts    []Artifact
	markdownCopy []byte
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
// --markdown copy destination. A zero or manually incomplete Result is
// rejected rather than returning an ambiguous nil byte stream.
func (r Result) MarkdownCopy() ([]byte, error) {
	if r.mode != QualifiedV2 {
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

// RunOptions selects a recipe. RecipePath must identify a local recipe.
type RunOptions struct {
	RecipePath string
	// ExpectedMode, when non-empty, fails closed if the recipe changed modes
	// after a caller performed a read-only mode preflight.
	ExpectedMode Mode
}
