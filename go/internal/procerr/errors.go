// Package procerr ports the original implementation (the ProcessFailure
// domain type) and the original implementation (the CLI's rendering of
// that type), together forming the shared error spine other Go packages in
// this port either produce (as a sentinel ProcessFailure, e.g.
// go/internal/canonjson's ErrInvalidArtifactJSON) or, eventually, render at
// a CLI boundary via RenderCLIProcessFailure.
//
// the original implementation's MetadataError-adjacent sibling -- the bare
// `class MetadataError extends Error {}` ported as go/internal/metadata's
// own unstructured MetadataError -- is deliberately not part of this
// package: that type carries no code/category/retryable/details fields to
// port, and the Node CLI renders it as a plain "error: <message>" with none
// of RenderCLIProcessFailure's structured suffix lines. See
// go/internal/metadata/validation.go's MetadataError doc comment.
package procerr

// Category is the Go analogue of the ErrorCategory string-literal union in
// the original implementation: "request" | "domain" | "io" | "internal".
// RenderCLIProcessFailure renders it verbatim as the "  category: " line;
// no Go code in this package otherwise branches on its value.
type Category string

// The four ErrorCategory literals from the original implementation.
const (
	CategoryRequest  Category = "request"
	CategoryDomain   Category = "domain"
	CategoryIO       Category = "io"
	CategoryInternal Category = "internal"
)

// ErrorDetail is the Go analogue of the ErrorDetail interface in
// the original implementation: one structured, path-addressed fact about a
// ProcessFailure (e.g. which input field was invalid and why). Field order
// mirrors the TS interface's declaration order; RenderCLIProcessFailure
// renders Path and Code inline and Message through indent (see cli.go).
type ErrorDetail struct {
	Path    string
	Code    string
	Message string
}

// ProcessFailure is the Go analogue of the ProcessFailure class in
// the original implementation: the single structured failure shape threaded
// from domain/io code up to a process boundary (today, the CLI; see
// RenderCLIProcessFailure). Fields mirror the TS class's public readonly
// properties exactly. Go has no exception hierarchy for this type to
// participate in the way ProcessFailure extends Error in TS; Error below is
// this package's equivalent of that inherited behavior.
type ProcessFailure struct {
	Code      string
	Category  Category
	Message   string
	Retryable bool
	Details   []ErrorDetail
}

// Error implements the error interface, returning Message -- exactly what
// a caller reading a TS ProcessFailure's inherited .message property would
// see, per the TS constructor's `super(options.message)` call.
func (f *ProcessFailure) Error() string {
	return f.Message
}

// NewProcessFailureOptions mirrors the options object the original source treedomain/
// errors.ts's ProcessFailure constructor accepts. Code, Category, and
// Message correspond to that constructor's required (no-default) fields.
// Retryable and Details correspond to its optional fields
// (`retryable?: boolean`, `details?: readonly ErrorDetail[]`), which
// default to false and an empty array there via `options.retryable ??
// false` and `options.details ?? []`; NewProcessFailure applies the same
// defaults (see its doc comment).
type NewProcessFailureOptions struct {
	Code      string
	Category  Category
	Message   string
	Retryable bool
	Details   []ErrorDetail
}

// NewProcessFailure builds a *ProcessFailure from opts, ported from the
// ProcessFailure constructor in the original implementation.
//
// Retryable's Go zero value (false) already matches the TS
// `options.retryable ?? false` default with no extra code. Details is
// normalized from a nil slice (the Go zero value for an omitted field) to
// an empty, non-nil []ErrorDetail{}, matching the TS constructor's
// `options.details ?? []`: callers that range over Details see zero
// iterations either way, but a non-nil empty slice keeps this type's
// runtime shape aligned with the TS default for any caller that
// distinguishes "no details" from "nil details" (e.g. JSON encoding, or a
// future reflect.DeepEqual-based test).
func NewProcessFailure(opts NewProcessFailureOptions) *ProcessFailure {
	details := opts.Details
	if details == nil {
		details = []ErrorDetail{}
	}
	return &ProcessFailure{
		Code:      opts.Code,
		Category:  opts.Category,
		Message:   opts.Message,
		Retryable: opts.Retryable,
		Details:   details,
	}
}
