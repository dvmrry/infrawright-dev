package tfrender

// hcl_tfvars_test.go ports every test in node-tests/hcl-tfvars.test.ts.
// Every test in that file is a pure library test of renderTfvarsHcl -- there
// is no CLI/runner-level test to skip. Expected byte strings below are
// transcribed verbatim from that file (see the per-test comment for the
// exact node-tests/hcl-tfvars.test.ts test name each Go test ports).
import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// num is a small helper constructing a json.Number (this package's
// LosslessNumber analogue) from a source-text numeric lexeme, for tests
// that need the LosslessNumber branch of scalarLiteral rather than the
// plain-float64 branch.
func num(lexeme string) json.Number { return json.Number(lexeme) }

// TestRenderTfvarsHclEmptyAndNamespaced ports "empty and namespaced tfvars
// match the Python bytes".
func TestRenderTfvarsHclEmptyAndNamespaced(t *testing.T) {
	got, err := RenderTfvarsHcl(map[string]any{}, nil, "items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := HCLTfvarsHeader + "\nitems = {}\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	got, err = RenderTfvarsHcl(
		map[string]any{"rule": map[string]any{"name": "Rule"}},
		nil,
		"sample_resource_items",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := HCLTfvarsHeader + "\n" +
		"sample_resource_items = {\n" +
		"  \"rule\" = {\n" +
		"    name = \"Rule\"\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRenderTfvarsHclMixedScalars ports "mixed scalars, quoted keys,
// interpolation, and Python numbers are exact".
func TestRenderTfvarsHclMixedScalars(t *testing.T) {
	got, err := RenderTfvarsHcl(map[string]any{
		"block risky": map[string]any{
			"123abc":      "leading",
			"enabled":     true,
			"expression":  "prefix ${var.name} %{if ok}",
			"huge":        num("900719925474099312345"),
			"name":        "Rule",
			"nothing":     nil,
			"ratio":       num("0.5"),
			"signed_zero": num("-0.0"),
			"tiny":        num("1e-6"),
		},
	}, nil, "items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := HCLTfvarsHeader + "\n" +
		"items = {\n" +
		"  \"block risky\" = {\n" +
		"    \"123abc\"    = \"leading\"\n" +
		"    enabled     = true\n" +
		"    expression  = \"prefix $${var.name} %%{if ok}\"\n" +
		"    huge        = 900719925474099312345\n" +
		"    name        = \"Rule\"\n" +
		"    nothing     = null\n" +
		"    ratio       = 0.5\n" +
		"    signed_zero = -0.0\n" +
		"    tiny        = 1e-06\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRenderTfvarsHclMultilineContainers ports "multiline containers split
// alignment runs and retain trailing commas".
func TestRenderTfvarsHclMultilineContainers(t *testing.T) {
	got, err := RenderTfvarsHcl(map[string]any{
		"item": map[string]any{
			"aa":         num("1"),
			"bb":         num("22"),
			"empty_list": []any{},
			"empty_obj":  map[string]any{},
			"mm":         []any{"x"},
			"nested":     []any{[]any{"u", "v"}, []any{"w"}},
			"objs":       []any{map[string]any{"a": num("1")}, map[string]any{"b": num("2")}},
			"yy":         num("3"),
			"zzzz":       num("44"),
		},
	}, nil, "items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := HCLTfvarsHeader + "\n" +
		"items = {\n" +
		"  \"item\" = {\n" +
		"    aa         = 1\n" +
		"    bb         = 22\n" +
		"    empty_list = []\n" +
		"    empty_obj  = {}\n" +
		"    mm = [\n" +
		"      \"x\",\n" +
		"    ]\n" +
		"    nested = [\n" +
		"      [\n" +
		"        \"u\",\n" +
		"        \"v\",\n" +
		"      ],\n" +
		"      [\n" +
		"        \"w\",\n" +
		"      ],\n" +
		"    ]\n" +
		"    objs = [\n" +
		"      {\n" +
		"        a = 1\n" +
		"      },\n" +
		"      {\n" +
		"        b = 2\n" +
		"      },\n" +
		"    ]\n" +
		"    yy   = 3\n" +
		"    zzzz = 44\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRenderTfvarsHclCommentsAlign ports "scalar and list comments align
// exactly, including object closers".
func TestRenderTfvarsHclCommentsAlign(t *testing.T) {
	comments := HclTfvarsComments{
		HclTfvarsCommentKey("item", "a", nil):                  "short",
		HclTfvarsCommentKey("item", "much_longer", nil):        "long",
		HclTfvarsCommentKey("item", "z_categories", intPtr(0)): "Finance",
		HclTfvarsCommentKey("item", "z_categories", intPtr(1)): "HR",
		HclTfvarsCommentKey("item", "zz_objects", intPtr(0)):   "annotated",
	}
	got, err := RenderTfvarsHcl(map[string]any{
		"item": map[string]any{
			"a":            num("1"),
			"much_longer":  num("22"),
			"z_categories": []any{"A", "CUSTOM_02"},
			"zz_objects":   []any{map[string]any{"a": num("1")}},
		},
	}, comments, "items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := HCLTfvarsHeader + "\n" +
		"items = {\n" +
		"  \"item\" = {\n" +
		"    a           = 1  # short\n" +
		"    much_longer = 22 # long\n" +
		"    z_categories = [\n" +
		"      \"A\",         # Finance\n" +
		"      \"CUSTOM_02\", # HR\n" +
		"    ]\n" +
		"    zz_objects = [\n" +
		"      {\n" +
		"        a = 1\n" +
		"      }, # annotated\n" +
		"    ]\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRenderTfvarsHclCodePointAlignment ports "code-point lengths, not
// UTF-16 lengths, control comment alignment".
func TestRenderTfvarsHclCodePointAlignment(t *testing.T) {
	comments := HclTfvarsComments{
		HclTfvarsCommentKey("item", "a", nil):      "emoji",
		HclTfvarsCommentKey("item", "longer", nil): "ascii",
	}
	got, err := RenderTfvarsHcl(map[string]any{
		"item": map[string]any{"a": "😀", "longer": "x"},
	}, comments, "items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := HCLTfvarsHeader + "\n" +
		"items = {\n" +
		"  \"item\" = {\n" +
		"    a      = \"😀\" # emoji\n" +
		"    longer = \"x\" # ascii\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRenderTfvarsHclFailuresLoudly ports "invalid variable names, comments,
// values, and native numbers fail loudly".
func TestRenderTfvarsHclFailuresLoudly(t *testing.T) {
	got, err := RenderTfvarsHcl(map[string]any{"item": map[string]any{"value": negativeZero()}}, nil, "items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "value = -0.0") {
		t.Fatalf("got %q, want a match for /value = -0\\.0/", got)
	}

	if _, err := RenderTfvarsHcl(map[string]any{}, nil, "bad-name"); err == nil || !strings.Contains(err.Error(), "bare HCL identifier") {
		t.Fatalf("expected a 'bare HCL identifier' error, got %v", err)
	}

	if _, err := RenderTfvarsHcl(
		map[string]any{"item": map[string]any{"value": "x"}},
		HclTfvarsComments{HclTfvarsCommentKey("item", "value", nil): "line\nbreak"},
		"items",
	); err == nil || !strings.Contains(err.Error(), "single-line") {
		t.Fatalf("expected a 'single-line' error, got %v", err)
	}

	for _, value := range []float64{nan(), posInf(), negInf()} {
		if _, err := RenderTfvarsHcl(map[string]any{"item": map[string]any{"value": value}}, nil, "items"); err == nil || !strings.Contains(err.Error(), "non-finite") {
			t.Fatalf("value %v: expected a 'non-finite' error, got %v", value, err)
		}
	}

	if _, err := RenderTfvarsHcl(
		map[string]any{"item": map[string]any{"value": float64(maxSafeInteger) + 1}},
		nil, "items",
	); err == nil || !strings.Contains(err.Error(), "unsafe native integer") {
		t.Fatalf("expected an 'unsafe native integer' error, got %v", err)
	}

	// TS: `renderTfvarsHcl({ item: { value: undefined } })` -- Go's
	// map[string]any has no "undefined" analogue, so this uses an
	// unsupported Go value type (an int, never produced by canonjson.Decode
	// or this port's own tests) to exercise scalarLiteral's same default
	// "unsupported HCL tfvars value" failure path.
	if _, err := RenderTfvarsHcl(map[string]any{"item": map[string]any{"value": 7}}, nil, "items"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected an 'unsupported' error, got %v", err)
	}
}

func intPtr(i int) *int { return &i }

// negativeZero, nan, posInf, negInf isolate float64 special-value
// construction so the test bodies above read the same as their TS sources
// (Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY, -0).
func negativeZero() float64 { return math.Copysign(0, -1) }
func nan() float64          { return math.NaN() }
func posInf() float64       { return math.Inf(1) }
func negInf() float64       { return math.Inf(-1) }
