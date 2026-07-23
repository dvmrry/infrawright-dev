package metadata

import (
	"errors"
	"fmt"
	"os"
	"unicode/utf8"
)

// readOptionalUtf8 reads path as UTF-8 text, returning (nil, nil) when the
// file does not exist. Ports readOptionalUtf8 from the original implementation,
// kept package-private per this port's scope (this package does not create
// a separate io package): the original implementation's
// loadResourceMainOverride is the only caller this package ports, and it
// only ever calls the optional variant.
//
// Deliberately not ported: readRequiredUtf8 (the original implementation's other
// export, unused by anything in this package's scope) and the
// the original implementation ProcessFailure error model both functions
// raise there (code/category/retryable/details); nothing in this port
// inspects those fields, only the error's message, so plain Go errors
// carrying the same message text are a faithful substitute here.
func readOptionalUtf8(path, label string) (*string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to read %s", label)
	}
	if !utf8.Valid(content) {
		return nil, fmt.Errorf("%s is not valid UTF-8", label)
	}
	text := string(content)
	return &text, nil
}

// ReadOptionalUTF8 exposes this package's readOptionalUtf8 (the port of
// readOptionalUtf8 in the original implementation) to the transform runner, which
// needs the identical absent-file-is-nil / other-error-fails contract for
// pull inputs and shared adoption-status sidecars. Same semantics, same
// error text; label feeds the error message exactly as in the Node source.
func ReadOptionalUTF8(path, label string) (*string, error) {
	return readOptionalUtf8(path, label)
}
