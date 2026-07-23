package collectors

// retry_test.go ports the retryDelayMs/failureHints assertions from
// the original test corpus's "retry schedule and failure hints
// use structured HTTP status" test.

import (
	"strings"
	"testing"
)

func TestRetryDelayMsExponentialBackoff(t *testing.T) {
	want := []float64{1_000, 2_000, 4_000, 8_000, 16_000}
	for attempt, expected := range want {
		if got := RetryDelayMs(attempt, ""); got != expected {
			t.Errorf("RetryDelayMs(%d, \"\") = %v, want %v", attempt, got, expected)
		}
	}
}

func TestRetryDelayMsRetryAfterParsing(t *testing.T) {
	cases := []struct {
		retryAfter string
		want       float64
	}{
		{"0.25", 250},
		{"999", 30_000},
		{"", 1_000},
		{"0x10", 1_000}, // not a Python float lexeme -> falls back to exponential backoff
		{"NaN", 0},
		{"Infinity", 30_000},
		{"-Infinity", 0},
	}
	for _, tc := range cases {
		if got := RetryDelayMs(0, tc.retryAfter); got != tc.want {
			t.Errorf("RetryDelayMs(0, %q) = %v, want %v", tc.retryAfter, got, tc.want)
		}
	}
}

func TestCollectorMaxRetries(t *testing.T) {
	if got := CollectorMaxRetries(); got != 5 {
		t.Errorf("CollectorMaxRetries() = %d, want 5", got)
	}
}

func TestFailureHintsScopedNotFound(t *testing.T) {
	hints := FailureHints([]string{"GET endpoint returned HTTP 404"}, true, []int{404})
	if !containsSubstring(hints, "ONE endpoint") {
		t.Errorf("expected a hint mentioning ONE endpoint, got %v", hints)
	}
	if !containsSubstring(hints, "only= scoped") {
		t.Errorf("expected a hint mentioning only= scoped, got %v", hints)
	}
	if last := hints[len(hints)-1]; last != "Successful pulls above are unaffected either way." {
		t.Errorf("last hint = %q, want trailing reassurance line", last)
	}
}

func TestFailureHintsUnstructuredReasonSkipsSpecificHints(t *testing.T) {
	hints := FailureHints([]string{"arbitrary text saying returned HTTP 404"}, false, nil)
	if containsSubstring(hints, "ONE endpoint") {
		t.Errorf("expected no ONE-endpoint hint without a structured 404 status, got %v", hints)
	}
}

func containsSubstring(values []string, substr string) bool {
	for _, value := range values {
		if strings.Contains(value, substr) {
			return true
		}
	}
	return false
}
