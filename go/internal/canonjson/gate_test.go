package canonjson

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot walks up from this test file's directory until it finds a
// directory containing both "catalogs" and "demo", per the Slice 0 gate
// spec. It fails the test rather than guessing if no such ancestor exists.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		_, catalogsErr := os.Stat(filepath.Join(dir, "catalogs"))
		_, demoErr := os.Stat(filepath.Join(dir, "demo"))
		if catalogsErr == nil && demoErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up to filesystem root from %s without finding a directory containing both catalogs/ and demo/", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

// gateTargets returns every fixture path the Slice 0 round-trip gate covers:
// the frozen v1 provenance catalog, the active Go-authoritative v2 catalog,
// and every demo/config/demo/*.json file (both *.auto.tfvars.json and
// *.lookup.json).
func gateTargets(t *testing.T, root string) []string {
	t.Helper()
	targets := []string{
		filepath.Join(root, "catalogs", "zscaler-root-catalog.v1.json"),
		filepath.Join(root, "catalogs", "zscaler-root-catalog.v2.json"),
	}
	demoMatches, err := filepath.Glob(filepath.Join(root, "demo", "config", "demo", "*.json"))
	if err != nil {
		t.Fatalf("globbing demo fixtures: %v", err)
	}
	if len(demoMatches) == 0 {
		t.Fatal("expected at least one demo/config/demo/*.json fixture; none found")
	}
	return append(targets, demoMatches...)
}

// TestRoundTripGate is the Slice 0 gate: decoding and re-rendering every
// committed canonical JSON fixture must reproduce the original bytes
// exactly.
//
// catalogs/zscaler-root-catalog.v1.json is frozen Node-v1
// renderPythonCompatibleJson output retained as provenance. The active Go
// `root-catalog` target writes only catalogs/zscaler-root-catalog.v2.json;
// this gate protects both the immutable v1 bytes and the v2 renderer output.
//
// demo/config/demo/*.json is, by contrast, NOT renderPythonCompatibleJson
// output: every file there is written by the sibling renderer
// renderPythonLosslessArtifactJson (node-src/json/python-lossless-artifact.ts),
// reached via node-src/domain/transform-artifacts.ts's
// renderDeploymentTfvars (*.auto.tfvars.json) and renderTransformLookup
// (*.lookup.json), themselves invoked by the Node CLI's `transform`
// command that the demo Makefile's `demo` target runs. Per this port's
// mandate ("if a fixture fails round-trip and is not
// renderPythonCompatibleJson output, exclude it -- never bend the
// renderer to fit"), these files would need excluding if they failed.
// They do not: the two renderers are known to diverge in exactly two
// ways (see TestEncodeStringLeavesDELUnescaped below for the first), and
// neither divergence is reachable through this package's Decode -> Render
// round-trip:
//
//  1. String encoding: renderPythonLosslessArtifactJson's
//     encodePythonString additionally escapes U+007F (DEL), where
//     renderPythonCompatibleJson's encodeString (ported as encodeString
//     in render.go) does not. This *is* reachable through a round-trip,
//     but only if some fixture string contains a literal DEL byte; none
//     of the committed fixtures do.
//  2. Number encoding: renderPythonLosslessArtifactJson's encodeNumber
//     special-cases a plain (non-lossless) `-0` to render as "0" instead
//     of "-0.0". This is NOT reachable through a round-trip at all: Decode
//     (like the Node parsers feeding both renderers here) always produces
//     a lossless token -- json.Number in this package, LosslessNumber in
//     the Node source -- never a plain float64/`number`, so this
//     package's Render and the real producer's encodeNumber take the same
//     canonicalPythonNumberToken-backed branch for every number in these
//     files regardless of which renderer wrote them.
//
// If a future demo fixture introduces a DEL byte in a string and this
// test starts failing, that is an expected consequence of divergence #1
// above, not a bug in Render: exclude that specific file from
// gateTargets with a comment, rather than teaching encodeString to escape
// DEL (which would then make it diverge from python-compatible.ts, the
// module this package actually ports).
func TestRoundTripGate(t *testing.T) {
	root := repoRoot(t)
	for _, path := range gateTargets(t, root) {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			assertRoundTrips(t, path, original)
		})
	}
}

// assertRoundTrips decodes original, re-renders it, and requires the
// result to match original byte-for-byte.
func assertRoundTrips(t *testing.T, path string, original []byte) {
	t.Helper()
	value, err := Decode(original)
	if err != nil {
		t.Fatalf("Decode(%s): %v", path, err)
	}
	rendered, err := Render(value)
	if err != nil {
		t.Fatalf("Render(%s): %v", path, err)
	}
	if rendered != string(original) {
		reportMismatch(t, path, original, []byte(rendered))
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

// TestEncodeStringLeavesDELUnescaped pins divergence #1 documented on
// TestRoundTripGate above: python-compatible.ts's encodeString only
// escapes characters >= U+0080, so U+007F (DEL) passes through literally,
// unlike true CPython json.dumps(..., ensure_ascii=True), which does
// escape it. This was discovered by reading the sibling renderer
// node-src/json/python-lossless-artifact.ts, whose encodePythonString has
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
