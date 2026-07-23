package canonjson

import (
	"encoding/json"
	"testing"
)

// TestJSONEqualLosslessIntegerFloatBehavior ports the
// "lossless number equality follows Python integer/float behavior" test
// from the original test corpus: values decoded from
// "[9007199254740993,9007199254740993.0,1,1.0,true,false,0,-0.0]" (all
// json.Number except the two booleans, matching parseDataJsonLosslessly's
// LosslessNumber-for-every-number behavior).
func TestJSONEqualLosslessIntegerFloatBehavior(t *testing.T) {
	values := decodeAllJSON(t, "[9007199254740993,9007199254740993.0,1,1.0,true,false,0,-0.0]")

	// 9007199254740993 (exact big integer) vs 9007199254740993.0 (a float
	// token that rounds to the adjacent representable double,
	// 9007199254740992.0): not equal, because the port must not truncate
	// the integer token to compare it.
	if JSONEqual(values[0], values[1]) {
		t.Error("JSONEqual(9007199254740993, 9007199254740993.0) = true, want false")
	}
	// 1 vs 1.0: equal.
	if !JSONEqual(values[2], values[3]) {
		t.Error("JSONEqual(1, 1.0) = false, want true")
	}
	// true vs 1: JSON booleans are numerically 0/1 under JSONEqual.
	if !JSONEqual(values[4], values[2]) {
		t.Error("JSONEqual(true, 1) = false, want true")
	}
	// false vs 0.
	if !JSONEqual(values[5], values[6]) {
		t.Error("JSONEqual(false, 0) = false, want true")
	}
	// 0 vs -0.0.
	if !JSONEqual(values[6], values[7]) {
		t.Error("JSONEqual(0, -0.0) = false, want true")
	}
}

// TestTerraformJSONExactlyEqualAvoidsBinaryRounding ports the
// "exact Terraform evidence equality avoids binary rounding" test from
// the original test corpus.
func TestTerraformJSONExactlyEqualAvoidsBinaryRounding(t *testing.T) {
	values := decodeAllJSON(t, "[1,1.0,10e-1,0.10e1,9007199254740992.0,9007199254740993.0,1e100000,10e99999]")

	if !TerraformJSONExactlyEqual(values[0], values[1]) {
		t.Error("TerraformJSONExactlyEqual(1, 1.0) = false, want true")
	}
	if !TerraformJSONExactlyEqual(values[1], values[2]) {
		t.Error("TerraformJSONExactlyEqual(1.0, 10e-1) = false, want true")
	}
	if !TerraformJSONExactlyEqual(values[2], values[3]) {
		t.Error("TerraformJSONExactlyEqual(10e-1, 0.10e1) = false, want true")
	}
	if TerraformJSONExactlyEqual(values[4], values[5]) {
		t.Error("TerraformJSONExactlyEqual(9007199254740992.0, 9007199254740993.0) = true, want false (must not round through binary64)")
	}
	if !TerraformJSONExactlyEqual(values[6], values[7]) {
		t.Error("TerraformJSONExactlyEqual(1e100000, 10e99999) = false, want true (same exact decimal value)")
	}
	if TerraformJSONExactlyEqual(true, values[0]) {
		t.Error("TerraformJSONExactlyEqual(true, 1) = true, want false (Terraform booleans are never numeric)")
	}

	// Existing parity and plan-classification callers retain Python's
	// numeric equality contract; only the accepted-plan authorization gate
	// is exact: 9007199254740992.0 and 9007199254740993.0 both parse to the
	// same nearest float64, so the non-exact comparator says they're equal.
	if !TerraformJSONEqual(values[4], values[5]) {
		t.Error("TerraformJSONEqual(9007199254740992.0, 9007199254740993.0) = false, want true (both round to the same float64)")
	}
}

// TestJSONEqualBooleanVsTerraformBoolean documents the one contract
// difference between JSONEqual (Python `==`, where bool is numeric 0/1)
// and the Terraform variants (bool is its own cty type, never equal to a
// number), both ported from the original implementation.
func TestJSONEqualBooleanVsTerraformBoolean(t *testing.T) {
	one := json.Number("1")
	if !JSONEqual(true, one) {
		t.Error("JSONEqual(true, 1) = false, want true (Python bool is numeric)")
	}
	if TerraformJSONEqual(true, one) {
		t.Error("TerraformJSONEqual(true, 1) = true, want false (cty bool is never numeric)")
	}
	if TerraformJSONExactlyEqual(true, one) {
		t.Error("TerraformJSONExactlyEqual(true, 1) = true, want false (cty bool is never numeric)")
	}
}

// TestJSONEqualStructuralCases exercises the null/string/array/object
// recursion shared by JSONEqual, TerraformJSONEqual, and
// TerraformJSONExactlyEqual (jsonEqual in equality.go), including
// Python-ordered key comparison for objects with differently-ordered keys
// and the "one side has a value, the other doesn't" cases that all three
// Node functions treat as unequal rather than panicking.
func TestJSONEqualStructuralCases(t *testing.T) {
	left := map[string]any{"a": json.Number("1"), "b": []any{nil, "x"}}
	right := map[string]any{"b": []any{nil, "x"}, "a": json.Number("1")}
	if !JSONEqual(left, right) {
		t.Error("JSONEqual should ignore object key order")
	}
	if !JSONEqual(nil, nil) {
		t.Error("JSONEqual(null, null) = false, want true")
	}
	if JSONEqual(nil, json.Number("0")) {
		t.Error("JSONEqual(null, 0) = true, want false")
	}
	if JSONEqual("1", json.Number("1")) {
		t.Error(`JSONEqual("1", 1) = true, want false (string never equals a number)`)
	}
	if JSONEqual([]any{json.Number("1")}, []any{json.Number("1"), json.Number("2")}) {
		t.Error("JSONEqual should require equal-length arrays")
	}
	if JSONEqual(map[string]any{"a": json.Number("1")}, []any{json.Number("1")}) {
		t.Error("JSONEqual should never equate an object and an array")
	}
}

// TestIsJSONRecord ports isJsonRecord from the original implementation.
func TestIsJSONRecord(t *testing.T) {
	if !IsJSONRecord(map[string]any{}) {
		t.Error("IsJSONRecord(map[string]any{}) = false, want true")
	}
	for _, v := range []any{nil, "x", json.Number("1"), []any{}, true, 1.5} {
		if IsJSONRecord(v) {
			t.Errorf("IsJSONRecord(%#v) = true, want false", v)
		}
	}
}

// decodeAllJSON decodes source as a JSON array and returns its elements,
// using this package's own Decode so every number in source surfaces as a
// json.Number -- the lossless token type these equality tests depend on,
// mirroring how the original test corpus feed these functions values from
// parseDataJsonLosslessly rather than plain JSON.parse.
func decodeAllJSON(t *testing.T, source string) []any {
	t.Helper()
	value, err := Decode([]byte(source))
	if err != nil {
		t.Fatalf("Decode(%q): %v", source, err)
	}
	arr, ok := value.([]any)
	if !ok {
		t.Fatalf("Decode(%q) did not produce an array: %#v", source, value)
	}
	return arr
}
