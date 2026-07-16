package canonjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Decode parses one JSON document into this package's dynamic Value tree
// (see the Value doc comment in render.go), per the Slice 0 design in
// docs/go-runtime-plan.md: encoding/json's own Decoder with UseNumber
// enabled, so every JSON number surfaces as a json.Number holding the exact
// source lexeme rather than a lossily-parsed float64 -- the Go analogue of
// the lossless-json parse the Node source (node-src/json/control.js's
// parseDataJsonLosslessly) performs before handing values to
// renderPythonCompatibleJson.
//
// This intentionally does not reimplement control.ts's stricter validation
// (duplicate-key rejection, nesting-depth limits, safe-integer checks for
// the "control" dialect): those are separate concerns layered on top of
// decoding in the Node source, out of scope for this package. Object-key
// absence is represented the same way a Go map already represents it, and
// duplicate keys resolve exactly the way encoding/json resolves them: the
// last occurrence of a key wins, silently discarding earlier ones.
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
	return value, nil
}
