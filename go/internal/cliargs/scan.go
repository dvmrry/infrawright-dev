package cliargs

import "strings"

// scanThroughHelp is the Go analogue of arguments.ts's argumentsThroughHelp.
// It walks args left to right against the ORIGINAL Values declarations
// (never the merged/possibly-Flags-clobbered declarations map -- see
// buildDeclarations), and:
//
//   - Skips past the token immediately following any bare occurrence of a
//     declared Values option name, without inspecting it at all -- this is
//     the "next-token binding" that lets a value beginning with "-", or
//     equal to "--", "--help", or another declared option's own name, bind
//     to a preceding declared option instead of being independently
//     rejected or (for --help/-h) treated as a help cutoff. If that option
//     is InlineOnly, the bare occurrence itself is rejected immediately
//     instead (InlineOnly options must use "--opt=value" spelling).
//   - Truncates (returns args[:index+1]) at the first *unbound* "--help" or
//     "-h" token: everything after help is discarded before the rest of
//     this package ever sees it. This checks the literal token text only,
//     regardless of whether help is actually enabled in the config -- see
//     "help cutoff still truncates trailing args even when help is
//     disabled" in TestParseCommandArgumentsErrors, pinned by probe.
//   - Rejects a bare "--" unconditionally (even when AllowPositionals is
//     set): arguments.ts never lets "--" reach parseArgs as an
//     option-terminator token, which is why OccurrenceKind has no
//     "option-terminator" analogue -- see resolve.go's file comment.
//   - Rejects "--opt=value" spelling unless opt is declared InlineOnly
//     *and* (AllowedValues is unset or contains value) -- see
//     ValueOption.AllowedValues' doc comment for the resulting gap where a
//     non-InlineOnly option's AllowedValues goes unenforced.
//   - Rejects any other single-dash token (e.g. "-x", "-xyz", "-hh": all
//     distinct from the exact literal "-h" handled above) outright.
func scanThroughHelp(args []string, values map[string]ValueOption) ([]string, error) {
	for index := 0; index < len(args); index++ {
		argument := args[index]

		if decl, ok := values[argument]; ok {
			if decl.InlineOnly {
				return nil, unknownArgument(argument)
			}
			index++ // bind (and skip validating) the next token as this option's value
			continue
		}

		if argument == "--help" || argument == "-h" {
			return args[:index+1], nil
		}
		if argument == "--" {
			return nil, unknownArgument("--")
		}

		if strings.HasPrefix(argument, "--") {
			if eq := strings.IndexByte(argument, '='); eq >= 0 {
				name := argument[:eq]
				value := argument[eq+1:]
				decl, declared := values[name]
				if !declared || !decl.InlineOnly || !allowedValueOK(decl.AllowedValues, value) {
					return nil, unknownArgument(argument)
				}
			}
			continue
		}
		if strings.HasPrefix(argument, "-") {
			return nil, unknownArgument(argument)
		}
	}
	return args, nil
}

// allowedValueOK reports whether value passes an AllowedValues restriction;
// an empty/nil list means unrestricted.
func allowedValueOK(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == value {
			return true
		}
	}
	return false
}

// unknownArgument builds the "unknown argument <token>" CliArgumentParseError
// every rejection path in this package (except the two panic-only TypeError
// analogues) ultimately produces.
func unknownArgument(token string) error {
	return &CliArgumentParseError{Message: "unknown argument " + token}
}

// bindSeparateValues is the Go analogue of arguments.ts's
// bindStringOptionValues: it rewrites an "--opt", "value" pair, where opt
// is one of separate's declared non-InlineOnly option names, into a single
// "--opt=value" token, so resolve only ever has to understand the inline
// spelling. Runs on scanThroughHelp's (possibly truncated) output.
//
// A value token is bound verbatim regardless of its own shape -- including
// an empty string, since args[index+1] being present (as opposed to absent
// past the end of the slice) is all that is checked, exactly like
// arguments_[index + 1] !== undefined in the TS source.
func bindSeparateValues(args []string, separate map[string]struct{}) []string {
	bound := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if _, ok := separate[argument]; ok && index+1 < len(args) {
			bound = append(bound, argument+"="+args[index+1])
			index++
			continue
		}
		bound = append(bound, argument)
	}
	return bound
}
