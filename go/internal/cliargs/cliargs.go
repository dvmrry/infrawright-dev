package cliargs

// ParseCommandArguments parses one command's argv strictly, reproducing the
// observable behavior of arguments.ts's parseCommandArguments. See the
// package doc comment for the porting approach, and cliargs_test.go for the
// full behavior matrix (including every Node probe that pinned an
// underlying-parseArgs-dependent edge case).
//
// Returned option keys in ParsedArguments.Options retain their leading
// "--", matching command code and diagnostics built around the original
// CLI spelling.
//
// May panic with *InvalidDeclarationError (an invalid Flags/Values name) or
// *StringOptionTypeError (the Values/Flags declaration-collision case) --
// both the Go analogues of a TypeError arguments.ts throws uncaught,
// outside its own error-conversion path, because both indicate a bug in
// the calling command's own configuration rather than bad CLI input.
func ParseCommandArguments(args []string, config ParseConfig) (ParsedArguments, error) {
	declarations := buildDeclarations(config) // may panic with *InvalidDeclarationError

	truncated, err := scanThroughHelp(args, config.Values)
	if err != nil {
		return ParsedArguments{}, err
	}

	bound := bindSeparateValues(truncated, separateValueOptionNames(config.Values))

	raw, err := resolve(bound, declarations, config.AllowPositionals)
	if err != nil {
		return ParsedArguments{}, err
	}

	options, err := assembleOptions(config.Values, declarations, raw) // may panic with *StringOptionTypeError
	if err != nil {
		return ParsedArguments{}, err
	}

	return ParsedArguments{
		Flags:       assembleFlags(config, raw),
		Options:     options,
		Positionals: raw.positionals,
		Occurrences: raw.occurrences,
	}, nil
}

// LastOption is the Go analogue of arguments.ts's lastOption: it returns the
// most recently supplied value for a repeatable option. ok is false when
// the option was never supplied at all, distinguishing that from a
// legitimately-empty last value (possible when ValueOption.AllowEmpty is
// set) -- the TS function collapses both cases to `undefined` via
// `parsed.options[name]?.at(-1)`, which Go's zero-value string cannot
// represent unambiguously on its own.
func LastOption(parsed ParsedArguments, name string) (value string, ok bool) {
	values := parsed.Options[name]
	if len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
}
