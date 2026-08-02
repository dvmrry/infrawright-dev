package canonjson

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot walks up from this test file until it finds the shipped demo.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		_, demoErr := os.Stat(filepath.Join(dir, "demo"))
		if demoErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up to filesystem root from %s without finding demo/", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

// reportMismatch pinpoints the first differing byte to make a round-trip
// failure diagnosable without dumping two full multi-KB JSON documents.
func reportMismatch(t *testing.T, path string, want, got []byte) {
	t.Helper()
	limit := len(want)
	if len(got) < limit {
		limit = len(got)
	}
	firstDiff := -1
	for i := 0; i < limit; i++ {
		if want[i] != got[i] {
			firstDiff = i
			break
		}
	}
	if firstDiff == -1 {
		firstDiff = limit
	}
	window := func(b []byte, at int) string {
		start := at - 20
		if start < 0 {
			start = 0
		}
		end := at + 20
		if end > len(b) {
			end = len(b)
		}
		return string(b[start:end])
	}
	t.Fatalf(
		"round-trip mismatch for %s at byte %d (want len %d, got len %d)\nwant: ...%q...\ngot:  ...%q...",
		path, firstDiff, len(want), len(got), window(want, firstDiff), window(got, firstDiff),
	)
}

// TestEncodeStringLeavesDELUnescaped pins a known divergence between the
// two sibling renderers: python-compatible.ts's encodeString only
// escapes characters >= U+0080, so U+007F (DEL) passes through literally,
// unlike true CPython json.dumps(..., ensure_ascii=True), which does
// escape it. This was discovered by reading the sibling renderer
// the original implementation, whose encodePythonString has
// a comment explicitly calling out and patching this exact gap for its
// own contract. This package intentionally reproduces the gap rather than
// closing it, per this port's byte-for-byte mandate to match
// python-compatible.ts, oddities included.
func TestEncodeStringLeavesDELUnescaped(t *testing.T) {
	got := encodeString("\x7f")
	want := "\"\x7f\""
	if got != want {
		t.Errorf("encodeString(DEL) = %q, want %q (unescaped, matching python-compatible.ts, not true CPython)", got, want)
	}
}
