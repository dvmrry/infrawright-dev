package sourcebind

import "fmt"

// ErrorCode identifies one stable source-input verification failure.
type ErrorCode string

const (
	// ErrorInvalidRoots means the caller supplied an unsafe local root.
	ErrorInvalidRoots ErrorCode = "invalid_roots"
	// ErrorManifest means the verified manifest was not canonical or valid.
	ErrorManifest ErrorCode = "manifest_invalid"
	// ErrorBinding means a manifest binding did not match captured bytes.
	ErrorBinding ErrorCode = "binding_mismatch"
	// ErrorModule means captured module metadata does not match the manifest.
	ErrorModule ErrorCode = "module_mismatch"
	// ErrorRevision means local Git state does not match the reviewed revision.
	ErrorRevision ErrorCode = "revision_mismatch"
	// ErrorRead means a bound input could not be stably captured.
	ErrorRead ErrorCode = "input_read_failed"
	// ErrorQualification means data did not come from verified source loading.
	ErrorQualification ErrorCode = "qualification_required"
)

// Error is a project-owned source-input failure. Its text intentionally names
// only portable manifest paths and never a caller's checkout root.
type Error struct {
	Code    ErrorCode
	Binding string
	Detail  string
}

func (e *Error) Error() string {
	if e.Binding == "" {
		return fmt.Sprintf("authoring source input %s: %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("authoring source input %s for %s: %s", e.Code, e.Binding, e.Detail)
}

func failure(code ErrorCode, binding, detail string) error {
	return &Error{Code: code, Binding: binding, Detail: detail}
}
