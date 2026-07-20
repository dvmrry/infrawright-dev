package procerr

import (
	"reflect"
	"testing"
)

// TestProcessFailureError checks that Error() returns Message verbatim,
// matching a TS caller reading a ProcessFailure's inherited .message.
func TestProcessFailureError(t *testing.T) {
	failure := &ProcessFailure{Message: "operation failed\nwithout exposing raw state"}
	if got, want := failure.Error(), "operation failed\nwithout exposing raw state"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

// TestNewProcessFailureDefaults ports the ProcessFailure constructor's
// `options.retryable ?? false` and `options.details ?? []` defaults from
// node-src/domain/errors.ts: a caller supplying only the three
// no-default fields gets Retryable false and a non-nil, empty Details.
func TestNewProcessFailureDefaults(t *testing.T) {
	failure := NewProcessFailure(NewProcessFailureOptions{
		Code:     "EXAMPLE_FAILURE",
		Category: CategoryDomain,
		Message:  "example failure",
	})

	if failure.Code != "EXAMPLE_FAILURE" {
		t.Errorf("Code = %q, want %q", failure.Code, "EXAMPLE_FAILURE")
	}
	if failure.Category != CategoryDomain {
		t.Errorf("Category = %q, want %q", failure.Category, CategoryDomain)
	}
	if failure.Message != "example failure" {
		t.Errorf("Message = %q, want %q", failure.Message, "example failure")
	}
	if failure.Retryable != false {
		t.Errorf("Retryable = %v, want false (the TS `options.retryable ?? false` default)", failure.Retryable)
	}
	if failure.Details == nil {
		t.Error("Details = nil, want a non-nil empty slice (the TS `options.details ?? []` default)")
	}
	if len(failure.Details) != 0 {
		t.Errorf("Details = %#v, want empty", failure.Details)
	}
}

// TestNewProcessFailureExplicitOptions checks that every field, when
// supplied, passes through unchanged -- no default substitutes a
// caller-supplied value.
func TestNewProcessFailureExplicitOptions(t *testing.T) {
	details := []ErrorDetail{
		{Path: "items.example.field", Code: "INVALID_FIELD", Message: "must be set"},
	}
	failure := NewProcessFailure(NewProcessFailureOptions{
		Code:      "EXAMPLE_FAILURE",
		Category:  CategoryIO,
		Message:   "operation failed",
		Retryable: true,
		Details:   details,
	})

	if failure.Category != CategoryIO {
		t.Errorf("Category = %q, want %q", failure.Category, CategoryIO)
	}
	if !failure.Retryable {
		t.Error("Retryable = false, want true (explicitly supplied)")
	}
	if !reflect.DeepEqual(failure.Details, details) {
		t.Errorf("Details = %#v, want %#v", failure.Details, details)
	}
}

// TestCategoryLiterals pins the four ErrorCategory string literals from
// node-src/domain/errors.ts: Category is a plain string underneath, so any
// drift here would silently change RenderCLIProcessFailure's "  category: "
// line without a compile error.
func TestCategoryLiterals(t *testing.T) {
	cases := map[Category]string{
		CategoryRequest:  "request",
		CategoryDomain:   "domain",
		CategoryIO:       "io",
		CategoryInternal: "internal",
	}
	for category, want := range cases {
		if string(category) != want {
			t.Errorf("category constant = %q, want %q", string(category), want)
		}
	}
}
