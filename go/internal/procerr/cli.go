package procerr

import (
	"regexp"
	"strings"
)

// newlineSequence matches the same "\r?\n" a JS source would step through
// with /\r?\n/gu: a bare "\n", or a "\r\n" pair collapsed together. Go
// strings are UTF-8 bytes, not UTF-16 code units, but neither \r (0x0D) nor
// \n (0x0A) can appear as a continuation byte of a multi-byte UTF-8
// sequence, so scanning byte-by-byte here (via regexp, which operates on
// bytes for a pattern this simple) finds exactly the same line breaks the
// JS regex's Unicode-aware `u` flag would.
var newlineSequence = regexp.MustCompile(`\r?\n`)

// indent ports process-failure.ts's indent(): every line break within a
// rendered field's text (the top-level failure message, or one detail's
// message) is replaced with a line break followed by a fixed two-space
// margin, so a multiline value stays visually attached to the "error: " or
// "  detail: ..." line it continues, indented one level deeper than that
// line's own two-space (or zero-space, for "error: ") prefix would suggest.
// A leading "\r" in a "\r\n" pair is dropped along with the "\n" it
// precedes; there is no path through which a "\r" survives into the
// rendered output.
func indent(value string) string {
	return newlineSequence.ReplaceAllString(value, "\n  ")
}

// RenderCLIProcessFailure renders failure as the CLI's exact
// operator-facing block, ported byte-for-byte from
// renderCliProcessFailure in the original implementation:
//
//	error: <message, indented>
//	  code: <code>
//	  category: <category>
//	  retryable: yes|no
//	  detail: <path> [<code>] <message, indented>
//	  ... (one "  detail: " line per entry in failure.Details, in order)
//
// followed by a single trailing newline. failure.Details being empty
// produces no "  detail: " lines at all, exactly matching the Node
// source's `for (const detail of failure.details)` over an empty array.
func RenderCLIProcessFailure(failure *ProcessFailure) string {
	retryable := "no"
	if failure.Retryable {
		retryable = "yes"
	}
	lines := []string{
		"error: " + indent(failure.Message),
		"  code: " + failure.Code,
		"  category: " + string(failure.Category),
		"  retryable: " + retryable,
	}
	for _, detail := range failure.Details {
		lines = append(lines, "  detail: "+detail.Path+" ["+detail.Code+"] "+indent(detail.Message))
	}
	return strings.Join(lines, "\n") + "\n"
}
