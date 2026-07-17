// Package pyoserr provides the narrow Python-compatible filesystem error
// spelling retained by Infrawright's Node implementation.
package pyoserr

import (
	"io/fs"
	"strings"
)

// Missing returns the frozen missing-path error used when Terraform executable
// resolution exhausts its candidates. The path is single-quoted after escaping
// only backslashes and apostrophes.
func Missing(path string) error {
	return missingError{path: path}
}

type missingError struct {
	path string
}

func (e missingError) Error() string {
	path := strings.ReplaceAll(e.path, `\`, `\\`)
	path = strings.ReplaceAll(path, "'", `\'`)
	return "[Errno 2] No such file or directory: '" + path + "'"
}

// Unwrap preserves the portable Go missing-file classification without
// changing the compatibility string returned by Error.
func (missingError) Unwrap() error {
	return fs.ErrNotExist
}
