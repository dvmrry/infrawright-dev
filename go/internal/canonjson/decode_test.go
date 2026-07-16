package canonjson

import (
	"encoding/json"
	"testing"
)

// TestDecodeProducesLosslessNumbers checks the core Slice 0 design
// decision (docs/go-runtime-plan.md): Decode must surface every JSON
// number as a json.Number holding the exact source lexeme, not a lossily
// parsed float64, so that e.g. an integer beyond float64's safe range
// survives unchanged.
func TestDecodeProducesLosslessNumbers(t *testing.T) {
	value, err := Decode([]byte(`{"a":9007199254740993,"b":-0.0,"c":1.50}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("Decode did not produce a map[string]any: %#v", value)
	}
	for key, want := range map[string]json.Number{
		"a": "9007199254740993",
		"b": "-0.0",
		"c": "1.50",
	} {
		got, ok := obj[key].(json.Number)
		if !ok {
			t.Fatalf("obj[%q] is %T, want json.Number", key, obj[key])
		}
		if got != want {
			t.Errorf("obj[%q] = %q, want %q", key, got, want)
		}
	}
}

// TestDecodeDuplicateKeysLastWins pins the design doc's explicit call-out:
// Decode does not reimplement control.ts's duplicate-key rejection: it
// inherits encoding/json's own behavior, which keeps the last occurrence
// of a repeated object key and silently discards earlier ones.
func TestDecodeDuplicateKeysLastWins(t *testing.T) {
	value, err := Decode([]byte(`{"a":1,"a":2}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	obj := value.(map[string]any)
	if len(obj) != 1 {
		t.Fatalf("len(obj) = %d, want 1", len(obj))
	}
	if got := obj["a"]; got != json.Number("2") {
		t.Errorf(`obj["a"] = %v, want json.Number("2") (last occurrence wins)`, got)
	}
}

// TestDecodeKeyAbsenceIsMapKeyAbsence pins the design doc's other explicit
// call-out: a JSON object missing a key decodes to a Go map simply
// lacking that key -- there is no separate "undefined" sentinel value,
// unlike the JS/TS source, which must distinguish undefined from null.
func TestDecodeKeyAbsenceIsMapKeyAbsence(t *testing.T) {
	value, err := Decode([]byte(`{"present":null}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	obj := value.(map[string]any)
	if v, ok := obj["present"]; !ok || v != nil {
		t.Errorf(`obj["present"] = (%v, %v), want (nil, true)`, v, ok)
	}
	if v, ok := obj["absent"]; ok || v != nil {
		t.Errorf(`obj["absent"] = (%v, %v), want (nil, false)`, v, ok)
	}
}

func TestDecodeRejectsTrailingContent(t *testing.T) {
	if _, err := Decode([]byte(`{"a":1} garbage`)); err == nil {
		t.Error(`Decode(trailing garbage) should return an error`)
	}
	if _, err := Decode([]byte(`{"a":1}{"b":2}`)); err == nil {
		t.Error(`Decode(two JSON values) should return an error`)
	}
}

func TestDecodeAllowsTrailingWhitespace(t *testing.T) {
	if _, err := Decode([]byte("{\"a\":1}\n")); err != nil {
		t.Errorf(`Decode(value + trailing newline) should succeed, got %v`, err)
	}
}

func TestDecodeRejectsMalformedJSON(t *testing.T) {
	if _, err := Decode([]byte(`{`)); err == nil {
		t.Error(`Decode("{") should return an error`)
	}
}
