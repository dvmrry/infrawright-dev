package httptransport

import "testing"

// TestDefaultTimeoutPinsOperationalValue pins the 60-second default
// per-attempt timeout that PR #300 raised from the ported 30s after slow
// tenant APIs exceeded it in production. The value is operational policy,
// not ported parity: a merge resolution or refactor that quietly restores
// the old constant must fail here rather than in a production fetch.
func TestDefaultTimeoutPinsOperationalValue(t *testing.T) {
	if DefaultTimeoutMs != 60_000 {
		t.Fatalf("DefaultTimeoutMs = %d, want the operational 60_000 (see PR #300); do not resolve merges back to the retired 30_000 default", DefaultTimeoutMs)
	}
}
