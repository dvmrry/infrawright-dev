package canonjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Decode parses one JSON document into this package's dynamic Value tree
// (see the Value doc comment in render.go), per the Slice 0 design in
// the Go runtime contract: encoding/json's own Decoder with UseNumber
// enabled, so every JSON number surfaces as a json.Number holding the exact
// source lexeme rather than a lossily-parsed float64 -- the Go analogue of
// the lossless-json parse the Node source (the original source treejson/control.js's
// parseDataJsonLosslessly) performs before handing values to
// renderPythonCompatibleJson.
//
// This intentionally does not reimplement control.ts's stricter structural
// validation (duplicate-key rejection, nesting-depth limits, safe-integer
// checks for the "control" dialect): those are separate concerns layered on
// top of decoding in the Node source. It does enforce the shared JSON-string
// invariant that UTF-16 surrogate units occur only as pairs, because
// encoding/json otherwise silently replaces a lone escape with U+FFFD.
// Object-key absence is represented the same way a Go map already represents
// it, and duplicate keys resolve exactly the way encoding/json resolves them:
// the last occurrence of a repeated key wins, silently discarding earlier ones.
//
// Decode rejects trailing content after the first JSON value (anything
// other than trailing whitespace), so callers get a clear error instead of
// silently ignoring extra bytes.
func Decode(data []byte) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value Value
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("canonjson: decode: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("canonjson: decode: trailing content after JSON value")
		}
		return nil, fmt.Errorf("canonjson: decode: %w", err)
	}
	// Keep encoding/json's syntax failures authoritative. Only after it has
	// accepted the complete document do we inspect raw string tokens, because
	// encoding/json otherwise normalizes lone surrogate escapes to U+FFFD.
	if err := validateJSONDocumentSurrogates(string(data)); err != nil {
		return nil, err
	}
	return value, nil
}
