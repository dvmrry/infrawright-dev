package transform

// coerce_primitives_test.go covers the rest of coerce.go's audited surface
// not already pinned by coerce_test.go's collection-branch vectors:
// coercePrimitive's three encodings, coerceBoolean, and dividedValue's
// Python floor-division semantics (a classic sign-of-remainder bug spot).
// Vectors marked "TS:" were captured the same way as coerce_test.go's: via
// esbuild-bundled node-src/domain/pull-transform.ts, called directly from
// node (dividedValue vectors specifically via the exported
// applyTransformOverridesForAuthoring, since dividedValue itself is not
// exported -- --external:lossless-json on the esbuild bundle, so the
// probe script's own `import { LosslessNumber } from "lossless-json"`
// resolves to the SAME class instance the bundle's `instanceof
// LosslessNumber` checks compare against; without that flag, esbuild
// vendors its own copy and every instanceof check silently fails).

import (
	"encoding/json"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func TestCoercePrimitiveStringEncoding(t *testing.T) {
	// TS: coerceValue(true, "string") => "true" / coerceValue(false, ...) => "false"
	if got := coerceValue(true, metadata.TerraformPrimitiveType("string"), false); got != "true" {
		t.Errorf("string(true) = %#v, want \"true\"", got)
	}
	if got := coerceValue(false, metadata.TerraformPrimitiveType("string"), false); got != "false" {
		t.Errorf("string(false) = %#v, want \"false\"", got)
	}
	// TS: coerceValue(new LosslessNumber("9007199254740991"), "string") =>
	// "9007199254740991" (a safe integer LosslessNumber stringifies plainly)
	if got := coerceValue(json.Number("9007199254740991"), metadata.TerraformPrimitiveType("string"), false); got != "9007199254740991" {
		t.Errorf("string(json.Number(9007199254740991)) = %#v", got)
	}
	// A non-bool/non-number value (already a string) passes through
	// unchanged.
	if got := coerceValue("already", metadata.TerraformPrimitiveType("string"), false); got != "already" {
		t.Errorf("string(\"already\") = %#v", got)
	}
	// nil passes through unchanged (neither bool nor json.Number nor
	// float64 branch matches).
	if got := coerceValue(nil, metadata.TerraformPrimitiveType("string"), false); got != nil {
		t.Errorf("string(nil) = %#v, want nil", got)
	}
}

func TestCoercePrimitiveNumberEncoding(t *testing.T) {
	// TS: coerceValue("007", "number") => 7 (parsePythonInteger normalizes
	// leading zeros away)
	if got := coerceValue("007", metadata.TerraformPrimitiveType("number"), false); got != json.Number("7") {
		t.Errorf("number(\"007\") = %#v, want json.Number(7)", got)
	}
	// TS: coerceValue("-0", "number") => 0 (Number("-0") normalized through
	// pythonFiniteFloatToken/canonjson.FiniteFloatToken to a plain "0" token)
	if got := coerceValue("-0", metadata.TerraformPrimitiveType("number"), false); got != json.Number("0") {
		t.Errorf("number(\"-0\") = %#v, want json.Number(0)", got)
	}
	// A non-string value passes through unchanged (coercePrimitive's
	// "number" branch only transforms strings).
	if got := coerceValue(true, metadata.TerraformPrimitiveType("number"), false); got != true {
		t.Errorf("number(true) = %#v, want true unchanged", got)
	}
	// A string that is neither a Python integer nor float literal passes
	// through unchanged.
	if got := coerceValue("not-a-number", metadata.TerraformPrimitiveType("number"), false); got != "not-a-number" {
		t.Errorf("number(\"not-a-number\") = %#v", got)
	}
}

func TestCoercePrimitiveBoolEncoding(t *testing.T) {
	// TS: coerceValue("TRUE", "bool") => true (case-insensitive via
	// toLowerCase, not pythonLower151 -- see coerceBoolean's doc comment)
	if got := coerceValue("TRUE", metadata.TerraformPrimitiveType("bool"), false); got != true {
		t.Errorf("bool(\"TRUE\") = %#v, want true", got)
	}
	// TS: coerceValue("yes", "bool") => "yes" (not a recognized literal,
	// passes through unchanged)
	if got := coerceValue("yes", metadata.TerraformPrimitiveType("bool"), false); got != "yes" {
		t.Errorf("bool(\"yes\") = %#v, want \"yes\" unchanged", got)
	}
	if got := coerceValue("0", metadata.TerraformPrimitiveType("bool"), false); got != false {
		t.Errorf("bool(\"0\") = %#v, want false", got)
	}
	if got := coerceValue("1", metadata.TerraformPrimitiveType("bool"), false); got != true {
		t.Errorf("bool(\"1\") = %#v, want true", got)
	}
	// An integral json.Number coerces via its truthiness (BigInt(token) !== 0n).
	if got := coerceValue(json.Number("0"), metadata.TerraformPrimitiveType("bool"), false); got != false {
		t.Errorf("bool(json.Number(0)) = %#v, want false", got)
	}
	if got := coerceValue(json.Number("5"), metadata.TerraformPrimitiveType("bool"), false); got != true {
		t.Errorf("bool(json.Number(5)) = %#v, want true", got)
	}
}

func TestUnwrapReferenceAppliesBeforePrimitiveCoercion(t *testing.T) {
	// unwrapReference only fires for the primitive-encoding branch of
	// coerceValue (node-src/domain/pull-transform.ts:545): a reference
	// object with an "id" key coerces to that id's own coerced value.
	got := coerceValue(map[string]any{"id": json.Number("42"), "name": "ignored"}, metadata.TerraformPrimitiveType("string"), false)
	if got != "42" {
		t.Errorf("unwrapReference+string coercion = %#v, want \"42\"", got)
	}
	// An object with no "id" key is left as the object itself, which then
	// fails coercePrimitive's type checks and passes through unchanged.
	got = coerceValue(map[string]any{"name": "no-id"}, metadata.TerraformPrimitiveType("string"), false)
	if m, ok := got.(map[string]any); !ok || m["name"] != "no-id" {
		t.Errorf("no-id object coercion = %#v, want the object unchanged", got)
	}
}

func TestDividedValuePythonFloorDivision(t *testing.T) {
	// Python's `//` floors toward negative infinity, unlike JS BigInt's `/`
	// (truncation toward zero) or Go's big.Int Quo (also truncation).
	// dividedValue/divideInteger must apply the sign-mismatch correction to
	// recover floor semantics. Pinned against
	// applyTransformOverridesForAuthoring({x: dividend}, {divide: {x:
	// divisor}}, "t").x from the compiled TS (see file doc comment).
	cases := []struct {
		dividend, divisor string
		want              string
	}{
		{"-7", "2", "-4"}, // TS: -4
		{"7", "-2", "-4"}, // TS: -4
		{"-7", "-2", "3"}, // TS: 3
		{"7", "2", "3"},   // TS: 3
		{"0", "5", "0"},
		{"10", "5", "2"},
	}
	for _, c := range cases {
		got := dividedValue(json.Number(c.dividend), json.Number(c.divisor), "t")
		if got != json.Number(c.want) {
			t.Errorf("dividedValue(%s, %s) = %#v, want json.Number(%s)", c.dividend, c.divisor, got, c.want)
		}
	}
}

func TestDividedValueRejectsZeroDivisor(t *testing.T) {
	message := mustPanic(t, "dividedValue zero divisor", func() {
		dividedValue(json.Number("10"), json.Number("0"), "some.field")
	})
	wantSubstring(t, message, "some.field must be a non-zero integer")
}

func TestDividedValueLeavesNonIntegerValuesUnchanged(t *testing.T) {
	// A boolean dividend is left unchanged (TS: `if (typeof candidate ===
	// "boolean") return value;`).
	if got := dividedValue(true, json.Number("2"), "t"); got != true {
		t.Errorf("dividedValue(true, 2) = %#v, want true unchanged", got)
	}
	// A non-numeric string dividend is left unchanged.
	if got := dividedValue("abc", json.Number("2"), "t"); got != "abc" {
		t.Errorf("dividedValue(\"abc\", 2) = %#v, want \"abc\" unchanged", got)
	}
}
