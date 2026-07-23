package procerr

import "testing"

// TestRenderCLIProcessFailurePreservesEveryOperatorFacingField ports
// "CLI ProcessFailure rendering preserves every operator-facing field"
// from the original test corpus verbatim: a multiline
// message, one detail with its own multiline message, retryable true, and
// category "domain". This is the ported suite's primary byte-parity
// vector for indent()'s multiline continuation behavior on both the
// top-level message and a detail message.
func TestRenderCLIProcessFailurePreservesEveryOperatorFacingField(t *testing.T) {
	rendered := RenderCLIProcessFailure(&ProcessFailure{
		Category: CategoryDomain,
		Code:     "EXAMPLE_FAILURE",
		Details: []ErrorDetail{
			{
				Code:    "INVALID_FIELD",
				Message: "must be set\nfor this provider",
				Path:    "items.example.field",
			},
		},
		Message:   "operation failed\nwithout exposing raw state",
		Retryable: true,
	})

	want := "error: operation failed\n" +
		"  without exposing raw state\n" +
		"  code: EXAMPLE_FAILURE\n" +
		"  category: domain\n" +
		"  retryable: yes\n" +
		"  detail: items.example.field [INVALID_FIELD] must be set\n" +
		"  for this provider\n"
	if rendered != want {
		t.Fatalf("RenderCLIProcessFailure mismatch:\n got: %q\nwant: %q", rendered, want)
	}
}

// TestRenderCLIProcessFailureNoDetailsRetryableFalse pins the suffix-only
// shape (no "  detail: " lines, "  retryable: no") that
// the original test corpuscli-failure-assertions.ts's assertCliFailureExtendsLegacy
// asserts across several real CLI failures -- e.g. plan-cli.test.ts's
// UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM case, whose exact message text
// is reproduced here verbatim from that test's `legacy` argument. This is
// the shared expectation the original test corpuscli-failure-assertions.ts encodes for
// every ProcessFailure without details: this port has no direct analogue
// of that Node helper (there is nothing to "extend" here, since this
// package's own test above already covers the full rendering, details
// included), so its invariant is instead pinned directly against this
// exact real vector.
func TestRenderCLIProcessFailureNoDetailsRetryableFalse(t *testing.T) {
	rendered := RenderCLIProcessFailure(&ProcessFailure{
		Category:  CategoryDomain,
		Code:      "UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM",
		Message:   "Terraform execution through Infrawright is supported on Linux and macOS; Windows is not a supported operational platform.",
		Retryable: false,
	})

	want := "error: Terraform execution through Infrawright is supported on Linux and macOS; " +
		"Windows is not a supported operational platform.\n" +
		"  code: UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM\n" +
		"  category: domain\n" +
		"  retryable: no\n"
	if rendered != want {
		t.Fatalf("RenderCLIProcessFailure mismatch:\n got: %q\nwant: %q", rendered, want)
	}
}

// TestRenderCLIProcessFailureInternalCategory pins the "  category:
// internal" line against a real vector: the original test corpus's
// ASSESSMENT_FAILED case (asserted there via assertCliFailureExtendsLegacy
// with category "internal", retryable false). That test does not fix an
// exact message (it compares against a dynamically-produced Python
// oracle's stderr), so a synthetic message stands in here; only the
// category/code/retryable/no-details shape is asserted against that real
// vector's metadata.
func TestRenderCLIProcessFailureInternalCategory(t *testing.T) {
	rendered := RenderCLIProcessFailure(&ProcessFailure{
		Category:  CategoryInternal,
		Code:      "ASSESSMENT_FAILED",
		Message:   "assessment failed",
		Retryable: false,
	})

	want := "error: assessment failed\n" +
		"  code: ASSESSMENT_FAILED\n" +
		"  category: internal\n" +
		"  retryable: no\n"
	if rendered != want {
		t.Fatalf("RenderCLIProcessFailure mismatch:\n got: %q\nwant: %q", rendered, want)
	}
}

// TestRenderCLIProcessFailureRemainingCategories rounds out coverage of
// Category's two literals with no real-fixture vector in either
// the original test corpus or
// the original test corpuscli-failure-assertions.ts's call sites ("request" and "io"):
// synthetic, but exercising the identical rendering path as the pinned
// real vectors above.
func TestRenderCLIProcessFailureRemainingCategories(t *testing.T) {
	cases := []struct {
		name     string
		category Category
	}{
		{"request", CategoryRequest},
		{"io", CategoryIO},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rendered := RenderCLIProcessFailure(&ProcessFailure{
				Category: c.category,
				Code:     "EXAMPLE_FAILURE",
				Message:  "example failure",
			})
			want := "error: example failure\n" +
				"  code: EXAMPLE_FAILURE\n" +
				"  category: " + c.name + "\n" +
				"  retryable: no\n"
			if rendered != want {
				t.Fatalf("RenderCLIProcessFailure mismatch:\n got: %q\nwant: %q", rendered, want)
			}
		})
	}
}

// TestRenderCLIProcessFailureMultipleDetails checks that Details renders
// as one "  detail: " line per entry, in order, ported behavior from the
// Node source's `for (const detail of failure.details)` loop (the ported
// Node test only exercises a single detail).
func TestRenderCLIProcessFailureMultipleDetails(t *testing.T) {
	rendered := RenderCLIProcessFailure(&ProcessFailure{
		Category: CategoryDomain,
		Code:     "EXAMPLE_FAILURE",
		Message:  "operation failed",
		Details: []ErrorDetail{
			{Path: "items.first", Code: "FIRST_CODE", Message: "first message"},
			{Path: "items.second", Code: "SECOND_CODE", Message: "second message"},
		},
	})

	want := "error: operation failed\n" +
		"  code: EXAMPLE_FAILURE\n" +
		"  category: domain\n" +
		"  retryable: no\n" +
		"  detail: items.first [FIRST_CODE] first message\n" +
		"  detail: items.second [SECOND_CODE] second message\n"
	if rendered != want {
		t.Fatalf("RenderCLIProcessFailure mismatch:\n got: %q\nwant: %q", rendered, want)
	}
}

// TestIndentCollapsesCarriageReturnLineFeed exercises indent()'s /\r?\n/gu
// port on a "\r\n" pair specifically (the ported Node test only ever
// supplies bare "\n"): the "\r" must not survive into the rendered output,
// matching the regex consuming an optional "\r" immediately before "\n".
func TestIndentCollapsesCarriageReturnLineFeed(t *testing.T) {
	rendered := RenderCLIProcessFailure(&ProcessFailure{
		Category: CategoryDomain,
		Code:     "EXAMPLE_FAILURE",
		Message:  "first line\r\nsecond line",
	})

	want := "error: first line\n" +
		"  second line\n" +
		"  code: EXAMPLE_FAILURE\n" +
		"  category: domain\n" +
		"  retryable: no\n"
	if rendered != want {
		t.Fatalf("RenderCLIProcessFailure mismatch:\n got: %q\nwant: %q", rendered, want)
	}
}

// TestIndentDirect exercises the unexported indent() helper directly for
// the handful of shapes RenderCLIProcessFailure's own tests don't already
// cover in isolation: no line breaks at all, three or more lines, and a
// trailing line break.
func TestIndentDirect(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"no line breaks", "single line", "single line"},
		{"three lines", "one\ntwo\nthree", "one\n  two\n  three"},
		{"trailing newline", "one\n", "one\n  "},
		{"crlf", "one\r\ntwo", "one\n  two"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := indent(c.input); got != c.want {
				t.Fatalf("indent(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}
