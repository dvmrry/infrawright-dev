package cliargs

import (
	"fmt"
	"regexp"
	"sort"
)

// validOptionName mirrors arguments.ts's optionKey validation regex
// (/^--[a-z][a-z0-9-]*$/u): two literal dashes, then a lowercase letter,
// then any run of lowercase letters, digits, or dashes (including none).
var validOptionName = regexp.MustCompile(`^--[a-z][a-z0-9-]*$`)

// mustOptionKey validates option against validOptionName and returns its
// stripped form (without the leading "--"). It panics with
// *InvalidDeclarationError on an invalid name, mirroring optionKey's thrown
// TypeError in arguments.ts.
//
// Go's %q and JS's JSON.stringify agree on every string this function's
// own callers ever pass it in practice (option/flag declarations, which are
// always plain ASCII); they are known to diverge for exotic inputs such as
// U+2028/U+2029 (which JSON.stringify does not escape but which are
// otherwise printable, so %q also would not escape them, so in fact no
// divergence exists there either) versus true non-printable or invalid-UTF8
// input, which %q escapes and JSON.stringify (operating on a JS string,
// which cannot hold invalid UTF-16) cannot represent identically. No
// committed caller passes such a name, so this is a documented, not
// practically observable, divergence.
func mustOptionKey(option string) string {
	if !validOptionName.MatchString(option) {
		panic(&InvalidDeclarationError{
			Message: fmt.Sprintf("invalid CLI option declaration %q", option),
		})
	}
	return option[2:]
}

// declKind distinguishes the two declaration shapes arguments.ts registers
// with parseArgs: `{ multiple: true, type: "string" }` for every Values
// entry, and `{ type: "boolean" }` for every Flags entry (plus the default
// `{ short: "h", type: "boolean" }` help declaration).
type declKind int

const (
	declString declKind = iota
	declBoolean
)

// declaration is this package's analogue of one parseArgs
// ParseArgsOptionsConfig[string] entry, reduced to only the fields this
// package's resolver needs.
type declaration struct {
	kind declKind
	// short is "h" for the help declaration when it carries the -h alias,
	// and empty otherwise. arguments.ts only ever registers a short alias
	// for "help", and only when no caller-supplied Flags/Values entry
	// already claims that key.
	short string
}

// buildDeclarations reproduces arguments.ts's declaration-assembly loop:
//
//	for (const option of Object.keys(valueOptions)) declarations[optionKey(option)] = ...
//	for (const flag of configuration.flags ?? []) declarations[optionKey(flag)] = ...
//	if ((configuration.help ?? true) && declarations.help === undefined) declarations.help = ...
//
// Values are registered first, then Flags -- so a name declared in both
// collapses to the Flags (boolean) declaration, exactly as the TS object
// key gets overwritten by the second loop. See StringOptionTypeError and
// TestValuesFlagsCollision for the resulting edge case this deliberately
// preserves rather than rejects.
//
// May panic with *InvalidDeclarationError (see mustOptionKey).
func buildDeclarations(config ParseConfig) map[string]declaration {
	declarations := make(map[string]declaration, len(config.Values)+len(config.Flags)+1)

	valueNames := make([]string, 0, len(config.Values))
	for option := range config.Values {
		valueNames = append(valueNames, option)
	}
	sort.Strings(valueNames) // see ParseConfig.Values' doc comment on iteration order

	for _, option := range valueNames {
		declarations[mustOptionKey(option)] = declaration{kind: declString}
	}
	for _, flag := range config.Flags {
		declarations[mustOptionKey(flag)] = declaration{kind: declBoolean}
	}
	if !config.HelpDisabled {
		if _, exists := declarations["help"]; !exists {
			declarations["help"] = declaration{kind: declBoolean, short: "h"}
		}
	}
	return declarations
}

// separateValueOptionNames returns the set of Values keys (with their
// leading "--") that are NOT InlineOnly -- the options bindSeparateValues
// is allowed to rewrite from "--opt", "value" into a single "--opt=value"
// token. Mirrors arguments.ts's separateValueOptionNames local in
// parseCommandArguments.
func separateValueOptionNames(values map[string]ValueOption) map[string]struct{} {
	names := make(map[string]struct{}, len(values))
	for option, decl := range values {
		if !decl.InlineOnly {
			names[option] = struct{}{}
		}
	}
	return names
}
