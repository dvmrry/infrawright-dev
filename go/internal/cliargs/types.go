// Package cliargs ports node-src/cli/arguments.ts: the strict CLI argument
// parser adapter every Infrawright command builds its argv handling on.
//
// The Node source is a thin wrapper around node:util's parseArgs, but the
// CONTRACT this package ports is the *wrapper's* observable behavior, not
// parseArgs itself -- arguments.ts layers its own pre-validation
// (argumentsThroughHelp), its own token rewriting (bindStringOptionValues),
// and its own post-validation (the multiple/allowEmpty checks in
// parseCommandArguments) around a parseArgs call, and callers only ever see
// the combined result. This package reimplements that combined behavior
// directly, without calling any argv-parsing library, because Go has no
// parseArgs equivalent to wrap.
//
// Where arguments.ts's behavior depends on parseArgs internals (exact
// wording of underlying errors, token shapes, precedence when multiple
// error conditions coexist), this package was written by first probing the
// actual compiled adapter (via esbuild, against this repo's node-src) for
// every case in cliargs_test.go, rather than guessing from reading
// parseArgs' documentation. Each probe is called out in the test file next
// to the case(s) it pins.
package cliargs

import "fmt"

// CliArgumentParseError is the Go analogue of arguments.ts's
// CliArgumentParseError class: every user-facing (i.e. bad-CLI-input, as
// opposed to bad-declaration) parse failure is returned as one of these, so
// callers can branch on the type the way Node callers branch on
// `error instanceof CliArgumentParseError`.
type CliArgumentParseError struct {
	Message string
}

func (e *CliArgumentParseError) Error() string { return e.Message }

// InvalidDeclarationError is the Go analogue of the TypeError arguments.ts's
// optionKey throws when a configured option/flag name doesn't match
// ^--[a-z][a-z0-9-]*$. ParseCommandArguments panics with this error, rather
// than returning it, because -- exactly as in the Node source, where this
// TypeError is thrown outside parseArgs' try/catch and so propagates
// uncaught -- an invalid declaration is a programmer/configuration bug in
// the calling command's own setup, not a CLI-input error a caller is
// expected to recover from at the argument-parsing call site.
type InvalidDeclarationError struct {
	Message string
}

func (e *InvalidDeclarationError) Error() string { return e.Message }

// StringOptionTypeError is the Go analogue of arguments.ts's other uncaught
// `throw new TypeError(...)`: "${option} did not parse as a string option".
// It fires when a Values-declared option's stripped name collides with a
// Flags-declared name (Flags declarations are registered after Values
// declarations and win on the shared key -- see buildDeclarations), *and*
// that name was actually supplied on the command line as a bare boolean
// flag. See TestValuesFlagsCollision for the probe that pinned this.
//
// Like InvalidDeclarationError, ParseCommandArguments panics with this
// rather than returning it, matching the Node source's uncaught TypeError.
type StringOptionTypeError struct {
	Message string
}

func (e *StringOptionTypeError) Error() string { return e.Message }

// ValueOption configures one repeatable, string-valued CLI option. Mirrors
// arguments.ts's CliValueOption interface.
type ValueOption struct {
	// AllowEmpty permits an empty-string value ("--opt=" or a bound
	// "--opt", "" pair). When false (the default), any empty-string
	// occurrence fails with "<option> requires a value" -- arguments.ts
	// lines 191-193.
	AllowEmpty bool

	// AllowedValues restricts which inline values are accepted. Per the
	// "AllowedValues goes unenforced for a non-InlineOnly option's separate
	// spelling" and "inline spelling rejected for non-InlineOnly option
	// even with an otherwise-allowed value" cases in
	// TestParseCommandArgumentsSuccess/Errors (both pinned by probe),
	// arguments.ts only ever consults this list from within its inline
	// "--opt=value" pre-validation (argumentsThroughHelp), a path only
	// reachable when InlineOnly is also true: a non-InlineOnly option's
	// AllowedValues is accepted at configuration time but silently never
	// enforced against values supplied in the separate "--opt value"
	// spelling. This port reproduces that gap faithfully rather than
	// closing it. nil/empty means unrestricted.
	AllowedValues []string

	// InlineOnly requires "--opt=value" spelling for this option and
	// rejects the separate "--opt value" spelling outright as an unknown
	// argument (arguments.ts's argumentsThroughHelp, rule for a token that
	// exactly equals a declared value option's name).
	InlineOnly bool

	// RejectDuplicates enforces a single occurrence of this option -- the
	// Go analogue of an explicit `multiple: false` in CliValueOption.
	//
	// The TS field is a tri-state optional boolean: `undefined` and `true`
	// both permit repeats, since arguments.ts's enforcement check is the
	// strict-equality test `declaration.multiple === false`, which only
	// fires on a literal false. Go's bool zero value is false, so this
	// field is named for the *rejecting* behavior (and inverted relative to
	// the TS name) precisely so its zero value reproduces the permissive
	// TS default instead of silently opting every option into
	// single-occurrence rejection.
	RejectDuplicates bool
}

// ParseConfig configures one call to ParseCommandArguments. Mirrors
// arguments.ts's ParseCommandArgumentsOptions interface.
type ParseConfig struct {
	// AllowPositionals permits bare (non-option) arguments; when false (the
	// default) any such argument fails as an unknown argument.
	AllowPositionals bool

	// Flags lists declared boolean flag names (each must match
	// ^--[a-z][a-z0-9-]*$; ParseCommandArguments panics with
	// InvalidDeclarationError otherwise).
	Flags []string

	// HelpDisabled turns off the default -h/--help registration. The
	// inverse of arguments.ts's `help` option (which defaults to true via
	// `configuration.help ?? true`), inverted so this field's Go zero
	// value (false) reproduces that default -- see the same zero-value
	// reasoning as ValueOption.RejectDuplicates.
	HelpDisabled bool

	// Values declares repeatable string-valued options, keyed by their
	// full "--name" spelling (each key must match ^--[a-z][a-z0-9-]*$;
	// ParseCommandArguments panics with InvalidDeclarationError otherwise).
	//
	// Go map iteration order is unspecified, unlike a JS object's
	// guaranteed insertion-order iteration; ParseCommandArguments visits
	// Values in sorted key order for its own internal determinism. The one
	// place this could observably differ from arguments.ts is if a caller
	// supplies *multiple* simultaneously-invalid declarations: Node would
	// deterministically panic on whichever one it declared first, while
	// this port panics on whichever sorts first. A caller with more than
	// one invalid declaration at once has a configuration bug either way;
	// which specific name is named in the panic text is not a behavior any
	// caller should rely on.
	Values map[string]ValueOption
}

// Flags is the Go analogue of arguments.ts's `ReadonlySet<string>` flags
// output: the set of declared flags (including "--help", if enabled) that
// were actually supplied.
type Flags map[string]struct{}

// Has reports whether name (spelled with its leading "--", e.g. "--help")
// was supplied.
func (f Flags) Has(name string) bool {
	_, ok := f[name]
	return ok
}

// OccurrenceKind distinguishes the two ParsedCommandArgumentOccurrence
// variants from arguments.ts's discriminated union.
type OccurrenceKind int

const (
	// OccurrenceOption is the Go analogue of the TS union's
	// `{ kind: "option"; name; value? }` variant.
	OccurrenceOption OccurrenceKind = iota
	// OccurrencePositional is the Go analogue of the TS union's
	// `{ kind: "positional"; value }` variant.
	OccurrencePositional
)

// Occurrence is one entry in the ordered Occurrences stream: every option
// or positional argument the command line actually contained, in the order
// it appeared, after help-cutoff truncation. Mirrors arguments.ts's
// ParsedCommandArgumentOccurrence.
type Occurrence struct {
	Kind OccurrenceKind

	// Name is set only when Kind == OccurrenceOption, and always includes
	// the leading "--" (e.g. "--help", "--pack").
	Name string

	// Value holds the bound value for a string-option occurrence, or the
	// positional text when Kind == OccurrencePositional. HasValue
	// distinguishes a present-but-empty string value (possible when
	// AllowEmpty is set) from a boolean/help occurrence carrying no value
	// at all -- the Go analogue of the TS union's optional `value?: string`
	// versus an absent key.
	Value    string
	HasValue bool
}

func (k OccurrenceKind) String() string {
	switch k {
	case OccurrenceOption:
		return "option"
	case OccurrencePositional:
		return "positional"
	default:
		return fmt.Sprintf("OccurrenceKind(%d)", int(k))
	}
}

// ParsedArguments is the Go analogue of arguments.ts's
// ParsedCommandArguments: the full result of one ParseCommandArguments
// call.
type ParsedArguments struct {
	Flags       Flags
	Options     map[string][]string
	Positionals []string
	Occurrences []Occurrence
}
