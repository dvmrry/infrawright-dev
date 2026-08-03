// Package fixtureupdate is the explicit update mode for pinned compatibility
// evidence. A gate that pins derived snapshot bytes (module HCL hashes, the
// parity compatibility capture, the frozen ZPA matrix's effective-input
// bindings) opts into IW_UPDATE_FIXTURES=1: when set, the gate rewrites its
// snapshot from the current generators and inputs instead of comparing, then
// skips. The paired SHA-256 constant pinning the snapshot bytes lives in the
// gate's own Go source so that evidence changes always surface as Go-source
// diffs in review; ReplaceConst rewrites exactly that one constant in place.
package fixtureupdate

import (
	"fmt"
	"os"
	"regexp"
)

// EnvVar is the opt-in switch shared by every compatibility gate that
// supports regeneration. Values other than "1" leave the gates comparing.
const EnvVar = "IW_UPDATE_FIXTURES"

// Requested reports whether the current test run asked for snapshot
// regeneration instead of comparison.
func Requested() bool {
	return os.Getenv(EnvVar) == "1"
}

// ReplaceConst rewrites the single `<name> = "<64-hex>"` constant in a Go
// source file with a new SHA-256 value. It fails unless the file contains
// exactly one such constant, so a rename or duplicate cannot be silently
// half-updated.
func ReplaceConst(sourcePath, name, sha256Hex string) error {
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	pattern, err := regexp.Compile(regexp.QuoteMeta(name) + `(\s*=\s*)"[0-9a-f]{64}"`)
	if err != nil {
		return err
	}
	matches := pattern.FindAll(content, -1)
	if len(matches) != 1 {
		return fmt.Errorf("%s: found %d occurrences of const %s, want exactly 1", sourcePath, len(matches), name)
	}
	updated := pattern.ReplaceAll(content, []byte(name+`${1}"`+sha256Hex+`"`))
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	return os.WriteFile(sourcePath, updated, info.Mode().Perm())
}
