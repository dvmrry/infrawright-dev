package canonjson

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRenderLosslessArtifactJSONMatchesPythonForIntegerJSON ports
// "lossless artifact renderer matches Python bytes for integer JSON" from
// the original test corpus. The Node test shells out to
// a Python oracle at run time; this port instead hardcodes that same
// oracle's output (`python3 -c "json.dumps(value, indent=2,
// sort_keys=True)"` against the identical decoded value -- see this
// package's Go test conventions in number_test.go/render_test.go), so this
// suite stays stdlib-only.
func TestRenderLosslessArtifactJSONMatchesPythonForIntegerJSON(t *testing.T) {
	// Exactly the source text the original test corpus
	// builds via its `[...].join("")` array (verified byte-for-byte
	// against the .ts source file's own bytes during development).
	source := "{\"2\":\"two\",\"10\":\"ten\",\"ascii\":\"é/\\\\\\\"\\n\",\"astral\":\"😀\",\"bmp\":\"\",\"huge\":900719925474099312345678901234567890,\"negative_zero\":-0,\"nested\":[true,null,9007199254740991]}"

	value, err := Decode([]byte(source))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	rendered, err := RenderLosslessArtifactJSON(value)
	if err != nil {
		t.Fatalf("RenderLosslessArtifactJSON: %v", err)
	}

	want := "{\n  \"10\": \"ten\",\n  \"2\": \"two\",\n  \"ascii\": \"\\u00e9/\\\\\\\"\\n\",\n  \"astral\": \"\\ud83d\\ude00\",\n  \"bmp\": \"\\ue000\",\n  \"huge\": 900719925474099312345678901234567890,\n  \"negative_zero\": 0,\n  \"nested\": [\n    true,\n    null,\n    9007199254740991\n  ]\n}\n"
	if rendered != want {
		t.Fatalf("RenderLosslessArtifactJSON mismatch:\n got: %q\nwant: %q", rendered, want)
	}
	if !strings.Contains(rendered, "900719925474099312345678901234567890") {
		t.Error("rendered output should preserve the 36-digit integer token exactly")
	}
	if !strings.Contains(rendered, "\"negative_zero\": 0") {
		t.Error(`rendered output should render the bare integer token "-0" as "0"`)
	}
	if !strings.Contains(rendered, "\\u00e9") {
		t.Error("rendered output should contain the é escape")
	}
	if !strings.Contains(rendered, "\\ud83d\\ude00") {
		t.Error("rendered output should contain the 😀 surrogate-pair escape")
	}
	if idx10, idx2 := strings.Index(rendered, `"10"`), strings.Index(rendered, `"2"`); idx10 >= idx2 {
		t.Errorf(`"10" should sort before "2" (code-point order): index("10")=%d index("2")=%d`, idx10, idx2)
	}
	if idxBmp, idxHuge := strings.Index(rendered, `"bmp"`), strings.Index(rendered, `"huge"`); idxBmp >= idxHuge {
		t.Errorf(`"bmp" should sort before "huge": index("bmp")=%d index("huge")=%d`, idxBmp, idxHuge)
	}
}

// TestRenderLosslessArtifactJSONEscapeBoundariesMatchPython ports
// "ASCII and Unicode escape boundaries match Python in keys and values"
// from the original test corpus. The Node test builds
// its value directly as a JS object (via Object.create(null) plus
// per-character own properties, one entry per boundary code point, keyed
// and valued by the same single character) rather than by parsing JSON
// text; this port does the same, building a plain map[string]any (this
// package's Value analogue of a null-prototype plain object -- see
// IsJSONRecord's doc comment in equality.go). Expected output was captured
// once via `python3 -c "json.dumps(value, indent=2, sort_keys=True)"`
// against the equivalent Python dict (see this file's development notes),
// then hardcoded to keep this suite stdlib-only.
func TestRenderLosslessArtifactJSONEscapeBoundariesMatchPython(t *testing.T) {
	boundaryPoints := []rune{
		0x00, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x1f,
		0x20, 0x21, 0x22, 0x2f, 0x5c, 0x7e, 0x7f, 0x80, 0x85, 0x9f,
		0xa0, 0x2028, 0x1f600,
	}
	value := map[string]any{}
	var sequence strings.Builder
	for _, point := range boundaryPoints {
		character := string(point)
		value[character] = character
		sequence.WriteString(character)
	}
	value["sequence"] = sequence.String()

	rendered, err := RenderLosslessArtifactJSON(value)
	if err != nil {
		t.Fatalf("RenderLosslessArtifactJSON: %v", err)
	}

	want := "{\n  \"\\u0000\": \"\\u0000\",\n  \"\\u0007\": \"\\u0007\",\n  \"\\b\": \"\\b\",\n  \"\\t\": \"\\t\",\n  \"\\n\": \"\\n\",\n  \"\\u000b\": \"\\u000b\",\n  \"\\f\": \"\\f\",\n  \"\\r\": \"\\r\",\n  \"\\u000e\": \"\\u000e\",\n  \"\\u001f\": \"\\u001f\",\n  \" \": \" \",\n  \"!\": \"!\",\n  \"\\\"\": \"\\\"\",\n  \"/\": \"/\",\n  \"\\\\\": \"\\\\\",\n  \"sequence\": \"\\u0000\\u0007\\b\\t\\n\\u000b\\f\\r\\u000e\\u001f !\\\"/\\\\~\\u007f\\u0080\\u0085\\u009f\\u00a0\\u2028\\ud83d\\ude00\",\n  \"~\": \"~\",\n  \"\\u007f\": \"\\u007f\",\n  \"\\u0080\": \"\\u0080\",\n  \"\\u0085\": \"\\u0085\",\n  \"\\u009f\": \"\\u009f\",\n  \"\\u00a0\": \"\\u00a0\",\n  \"\\u2028\": \"\\u2028\",\n  \"\\ud83d\\ude00\": \"\\ud83d\\ude00\"\n}\n"
	if rendered != want {
		t.Fatalf("RenderLosslessArtifactJSON mismatch:\n got: %q\nwant: %q", rendered, want)
	}
	if !strings.Contains(rendered, "\"\\u007f\": \"\\u007f\"") {
		t.Error(`rendered output should escape U+007F (DEL) in both key and value position -- the documented divergence from render.go's Render`)
	}
	if !strings.Contains(rendered, "\"\\u0080\": \"\\u0080\"") {
		t.Error(`rendered output should escape U+0080`)
	}
	if !strings.Contains(rendered, "\"~\": \"~\"") {
		t.Error("rendered output should leave U+007E (~) literal")
	}
}

// TestRenderLosslessArtifactJSONSafeIntegersAndUnboundedTokens ports
// "safe native integers and integral lossless tokens render canonically"
// from the original test corpus, minus its
// __proto__/constructor property-descriptor trickery (forcing literal own
// properties named "__proto__"/"constructor" without touching the real
// prototype chain -- meaningful only for a JS object; a Go map's
// "__proto__" key is just an ordinary string key with no special
// behavior, so this keeps those two keys to prove they sort and render
// like any other key, dropping only the JS-specific mechanism used to
// create them).
func TestRenderLosslessArtifactJSONSafeIntegersAndUnboundedTokens(t *testing.T) {
	shared := map[string]any{"value": json.Number("-0")}
	value := map[string]any{
		"__proto__":            shared,
		"constructor":          shared,
		"maximum":              float64(9007199254740991),  // Number.MAX_SAFE_INTEGER
		"minimum":              float64(-9007199254740991), // Number.MIN_SAFE_INTEGER
		"native_negative_zero": math.Copysign(0, -1),
		"unbounded":            json.Number("-900719925474099312345678901234567890"),
	}

	rendered, err := RenderLosslessArtifactJSON(value)
	if err != nil {
		t.Fatalf("RenderLosslessArtifactJSON: %v", err)
	}
	want := strings.Join([]string{
		"{",
		`  "__proto__": {`,
		`    "value": 0`,
		"  },",
		`  "constructor": {`,
		`    "value": 0`,
		"  },",
		`  "maximum": 9007199254740991,`,
		`  "minimum": -9007199254740991,`,
		`  "native_negative_zero": 0,`,
		`  "unbounded": -900719925474099312345678901234567890`,
		"}",
		"",
	}, "\n")
	if rendered != want {
		t.Fatalf("RenderLosslessArtifactJSON mismatch:\n got: %q\nwant: %q", rendered, want)
	}
}

// TestRenderLosslessArtifactJSONFloatNotationBoundaries ports
// "finite lossless floats match Python bytes across notation boundaries"
// from the original test corpus: an array of
// json.Number tokens spanning fixed/scientific notation boundaries,
// subnormals, and both extremes of float64 magnitude. Expected output was
// captured once via `python3 -c "json.dumps([json.loads(t) for t in
// tokens], indent=2)"` against the identical tokens, then hardcoded to
// keep this suite stdlib-only.
func TestRenderLosslessArtifactJSONFloatNotationBoundaries(t *testing.T) {
	tokens := []string{
		"0.0", "-0.0", "1e0", "1e-4", "1e-5", "1e15", "1e16", "1e20",
		"0.00009999999999999999", "100000000000000.1", "1.0000000000000002",
		"1e-999", "5e-324", "1.7976931348623157e308",
	}
	value := make([]any, len(tokens))
	for i, token := range tokens {
		value[i] = json.Number(token)
	}

	rendered, err := RenderLosslessArtifactJSON(value)
	if err != nil {
		t.Fatalf("RenderLosslessArtifactJSON: %v", err)
	}
	want := "[\n  0.0,\n  -0.0,\n  1.0,\n  0.0001,\n  1e-05,\n  1000000000000000.0,\n  1e+16,\n  1e+20,\n  9.999999999999999e-05,\n  100000000000000.1,\n  1.0000000000000002,\n  0.0,\n  5e-324,\n  1.7976931348623157e+308\n]\n"
	if rendered != want {
		t.Fatalf("RenderLosslessArtifactJSON mismatch:\n got: %q\nwant: %q", rendered, want)
	}
}

// TestRenderLosslessArtifactJSONFloatsRoundTripAcrossDeterministicSweep
// ports "finite float spelling matches Python across deterministic binary64
// values" from the original test corpus, including its
// exact splitmix64-style generator (state = state*6364136223846793005 +
// 1442695040888963407, reading the resulting bits directly as a float64 --
// the Node source's `view.setBigUint64(0, state, false)` immediately
// followed by `view.getFloat64(0, false)` is exactly a bit-for-bit
// reinterpretation, since both calls use the same byte order, matching
// Go's math.Float64frombits).
//
// The compact CPython output length and digest are retained as a neutral,
// inert compatibility fixture. The test also keeps the bit-for-bit
// round-trip check and exercises the artifact renderer's array pipeline.
func TestRenderLosslessArtifactJSONFloatsRoundTripAcrossDeterministicSweep(t *testing.T) {
	state := uint64(0x9e3779b97f4a7c15)
	var tokens []any
	var compactTokens []string
	for i := 0; i < 2048; i++ {
		state = state*6364136223846793005 + 1442695040888963407
		value := math.Float64frombits(state)
		if !isFiniteFloat(value) {
			continue
		}
		token, err := FiniteFloatToken(value)
		if err != nil {
			t.Fatalf("FiniteFloatToken(%v): %v", value, err)
		}
		reparsed, err := strconv.ParseFloat(token, 64)
		if err != nil {
			t.Fatalf("strconv.ParseFloat(%q): %v", token, err)
		}
		if math.Float64bits(reparsed) != math.Float64bits(value) {
			t.Errorf("FiniteFloatToken(%v) = %q, which reparses to %v (bits %x), want the original bits %x",
				value, token, reparsed, math.Float64bits(reparsed), math.Float64bits(value))
		}
		tokens = append(tokens, json.Number(token))
		compactTokens = append(compactTokens, token)
	}
	if got, want := len(tokens), 2047; got != want {
		t.Fatalf("deterministic sweep produced %d finite doubles, want %d", got, want)
	}

	fixturePath := filepath.Join("testdata", "lossless_binary64_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	fixtureDigest := sha256.Sum256(fixtureBytes)
	if got, want := hex.EncodeToString(fixtureDigest[:]), "68061b2be7c3a06620238cf9116660e0254cf038b76e194d644be6826c7a1664"; got != want {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, want)
	}
	var compatibility struct {
		SchemaVersion int `json:"schema_version"`
		Corpus        struct {
			GeneratedValues int `json:"generated_values"`
			FiniteValues    int `json:"finite_values"`
		} `json:"corpus"`
		Output struct {
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"output"`
	}
	if err := json.Unmarshal(fixtureBytes, &compatibility); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if compatibility.SchemaVersion != 1 || compatibility.Corpus.GeneratedValues != 2048 || compatibility.Corpus.FiniteValues != len(tokens) {
		t.Fatalf("%s schema/generated/finite = %d/%d/%d, want 1/2048/%d", fixturePath, compatibility.SchemaVersion, compatibility.Corpus.GeneratedValues, compatibility.Corpus.FiniteValues, len(tokens))
	}
	compactRendered := "[" + strings.Join(compactTokens, ",") + "]"
	compactDigest := sha256.Sum256([]byte(compactRendered))
	compactSHA256 := hex.EncodeToString(compactDigest[:])
	if len(compactRendered) != compatibility.Output.Bytes || compactSHA256 != compatibility.Output.SHA256 {
		t.Errorf("compact deterministic corpus length/SHA256 = %d/%s, want %d/%s", len(compactRendered), compactSHA256, compatibility.Output.Bytes, compatibility.Output.SHA256)
	}

	rendered, err := RenderLosslessArtifactJSON(tokens)
	if err != nil {
		t.Fatalf("RenderLosslessArtifactJSON: %v", err)
	}
	decoded, err := Decode([]byte(rendered))
	if err != nil {
		t.Fatalf("Decode(rendered): %v", err)
	}
	decodedArray, ok := decoded.([]any)
	if !ok || len(decodedArray) != len(tokens) {
		t.Fatalf("Decode(rendered) = %#v, want an array of %d elements", decoded, len(tokens))
	}
}

func isFiniteFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// TestRenderLosslessArtifactJSONRejectsNativeFloatsAndUnsafeNumbers ports
// "native floats, non-finite lexemes, and unsafe native numbers fail" from
// the original test corpus.
func TestRenderLosslessArtifactJSONRejectsNativeFloatsAndUnsafeNumbers(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"plain non-integer float64", 1.5},
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"MAX_SAFE_INTEGER + 1 as plain float64", float64(9007199254740992)},
		{"lossless overflowing token", json.Number("1e400")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := RenderLosslessArtifactJSON(c.value); err != ErrInvalidArtifactJSON {
				t.Errorf("RenderLosslessArtifactJSON(%v) error = %v, want ErrInvalidArtifactJSON", c.value, err)
			}
		})
	}
}

// TestRenderLosslessArtifactJSONFailsClosedOnUnsupportedValuesAndCycles
// ports the spirit of "non-JSON containers, hidden state, and cycles fail
// closed" from the original test corpus. Most of that
// Node test exercises JS-runtime-specific ways to smuggle data past plain
// JSON semantics -- a sparse array hole, a non-enumerable/accessor
// property, a throwing Proxy trap -- none of which are constructible at
// all in this package's Value model (see render.go's Value doc comment):
// map[string]any and []any are always plain, dense, data-only containers,
// so there is no Go analogue to port for those cases. What *does* port
// directly:
//
//   - a Go value outside the Value model (this package's stand-in for the
//     Node test's `undefined`, `1n` BigInt, and `new Date(0)` cases, all of
//     which are likewise "not a JSON value" for encode's default branch);
//   - a genuine reference cycle, since map[string]any and []any are Go
//     reference types and so, unlike JS arrays/objects, a Go cycle really is
//     constructible without any special trickery;
//   - the fail-closed, no-detail contract itself: every failure, however
//     deeply nested, surfaces as the exact same ErrInvalidArtifactJSON
//     sentinel (checked here with ==, not just errors.Is), never a
//     value-specific message -- this is what made the Node test's
//     hidden-property/getter/throwing-Proxy assertions (that a "secret"
//     name or value never appears in the error) hold, and is preserved
//     here even though this package cannot construct those specific inputs.
func TestRenderLosslessArtifactJSONFailsClosedOnUnsupportedValuesAndCycles(t *testing.T) {
	t.Run("unsupported Go types", func(t *testing.T) {
		type customStruct struct{ X int }
		for _, value := range []any{
			int(1),
			complex128(1),
			[]int{1, 2, 3},         // a slice, but not []any
			map[string]int{"a": 1}, // a map, but not map[string]any
			customStruct{X: 1},
			make(chan int),
		} {
			if _, err := RenderLosslessArtifactJSON(value); err != ErrInvalidArtifactJSON {
				t.Errorf("RenderLosslessArtifactJSON(%#v) error = %v, want ErrInvalidArtifactJSON", value, err)
			}
		}
	})

	t.Run("self-referential map", func(t *testing.T) {
		cyclic := map[string]any{}
		cyclic["self"] = cyclic
		if _, err := RenderLosslessArtifactJSON(cyclic); err != ErrInvalidArtifactJSON {
			t.Errorf("RenderLosslessArtifactJSON(self-referential map) error = %v, want ErrInvalidArtifactJSON", err)
		}
	})

	t.Run("self-referential slice", func(t *testing.T) {
		cyclic := make([]any, 1)
		cyclic[0] = cyclic
		if _, err := RenderLosslessArtifactJSON(cyclic); err != ErrInvalidArtifactJSON {
			t.Errorf("RenderLosslessArtifactJSON(self-referential slice) error = %v, want ErrInvalidArtifactJSON", err)
		}
	})

	t.Run("mutual cycle through mixed containers", func(t *testing.T) {
		object := map[string]any{}
		array := []any{object}
		object["array"] = array
		if _, err := RenderLosslessArtifactJSON(object); err != ErrInvalidArtifactJSON {
			t.Errorf("RenderLosslessArtifactJSON(mutual array/object cycle) error = %v, want ErrInvalidArtifactJSON", err)
		}
	})

	t.Run("a deeply nested failure still surfaces the exact sentinel, unwrapped", func(t *testing.T) {
		value := map[string]any{
			"a": []any{
				map[string]any{
					"b": []any{1.5}, // fails deep inside: not a safe integer
				},
			},
		}
		_, err := RenderLosslessArtifactJSON(value)
		if err != ErrInvalidArtifactJSON {
			t.Errorf("deeply nested failure error = %v, want the exact ErrInvalidArtifactJSON sentinel (no wrapping)", err)
		}
	})

	t.Run("distinct empty containers as siblings are not mistaken for a cycle", func(t *testing.T) {
		// Regression check for encodeArtifactArray/encodeArtifactRecord's
		// choice to skip cycle bookkeeping for zero-length containers:
		// two unrelated empty arrays (or objects) appearing as siblings
		// must both render successfully, even though Go may in principle
		// give zero-size allocations the same runtime address.
		value := map[string]any{
			"a": []any{},
			"b": []any{},
			"c": map[string]any{},
			"d": map[string]any{},
		}
		rendered, err := RenderLosslessArtifactJSON(value)
		if err != nil {
			t.Fatalf("RenderLosslessArtifactJSON(sibling empty containers): %v", err)
		}
		want := "{\n  \"a\": [],\n  \"b\": [],\n  \"c\": {},\n  \"d\": {}\n}\n"
		if rendered != want {
			t.Errorf("RenderLosslessArtifactJSON(sibling empty containers) = %q, want %q", rendered, want)
		}
	})
}

// TestLosslessArtifactFixturesRoundTrip is the new gate this port adds:
// every demo/config/demo/*.json fixture is, per gate_test.go's
// TestRoundTripGate doc comment, actually renderPythonLosslessArtifactJson
// output (not renderPythonCompatibleJson output) -- written by
// the original implementation's renderDeploymentTfvars/
// renderTransformLookup, both of which call renderPythonLosslessArtifactJson
// after parsing with parseDataJsonLosslessly. This decodes each committed
// fixture and re-renders it through this file's RenderLosslessArtifactJSON,
// requiring byte-identical output -- the real round-trip guarantee those
// fixtures are supposed to uphold, as opposed to TestRoundTripGate's
// Render (python-compatible.ts), which those fixtures merely happen not to
// violate today (see TestRoundTripGate's own doc comment on the two known
// divergences).
//
// repoRoot and reportMismatch are reused, unmodified, from gate_test.go
// (both are ordinary unexported test helpers visible package-wide); only
// the demo/config/demo fixture set is gathered independently here.
func TestLosslessArtifactFixturesRoundTrip(t *testing.T) {
	root := repoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "demo", "config", "demo", "*.json"))
	if err != nil {
		t.Fatalf("globbing demo fixtures: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one demo/config/demo/*.json fixture; none found")
	}
	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			value, err := Decode(original)
			if err != nil {
				t.Fatalf("Decode(%s): %v", path, err)
			}
			rendered, err := RenderLosslessArtifactJSON(value)
			if err != nil {
				t.Fatalf("RenderLosslessArtifactJSON(%s): %v", path, err)
			}
			if rendered != string(original) {
				reportMismatch(t, path, original, []byte(rendered))
			}
		})
	}
}
