package metadata

import (
	"errors"
	"fmt"
	"os"
	"unicode/utf8"
)

// readOptionalUtf8 reads path as UTF-8 text, returning (nil, nil) when the
// file does not exist. Ports readOptionalUtf8 from node-src/io/files.ts,
// kept package-private per this port's scope (this package does not create
// a separate io package): node-src/metadata/resources.ts's
// loadResourceMainOverride is the only caller this package ports, and it
// only ever calls the optional variant.
//
// Deliberately not ported: readRequiredUtf8 (node-src/io/files.ts's other
// export, unused by anything in this package's scope) and the
// node-src/domain/errors.ts ProcessFailure error model both functions
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
