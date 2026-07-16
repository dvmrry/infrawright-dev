package cliargs

import (
	"fmt"
	"sort"
	"strings"
)

// rawResult accumulates everything resolve extracts from one bound token
// stream, keyed by each declaration's *stripped* name (no leading "--") --
// the same keying arguments.ts's parsed.values object uses internally.
type rawResult struct {
	occurrences []Occurrence
	// stringValues holds every value bound to a declString-kind option, in
	// occurrence order. Only ever populated for a name whose declaration
	// (at resolve time, i.e. post Values/Flags collision) is declString.
	stringValues map[string][]string
	// boolSeen records every declBoolean-kind option actually supplied as a
	// bare flag, including "help" and, in the Values/Flags collision case,
	// a Values-declared name that a Flags entry clobbered to boolean. This
	// is deliberately one map covering all three cases, because
	// assembleOptions' StringOptionTypeError check and assembleFlags' Flags
	// output both need to ask "was this stripped name seen as a bool flag"
	// without caring which of the three reasons applies.
	boolSeen    map[string]bool
	positionals []string
}

// resolve plays the role parseArgs itself plays in arguments.ts, but only
// for the narrow token surface bindSeparateValues ever produces: every
// string-option occurrence has already been normalized to "--opt=value"
// (whether the user wrote it inline or separately), so this function never
// needs to look ahead for a following value token itself.
//
// It does NOT need to handle:
//   - a bare "--" option-terminator token: scanThroughHelp rejects every
//     bare "--" before this point, so kind == "option-terminator" (which
//     arguments.ts's assembly loop explicitly skips: `if (token.kind ===
//     "option-terminator") continue;`) can never actually arise here. There
//     is deliberately no OccurrenceKind for it.
//   - single-dash short options other than the exact literal "-h":
//     scanThroughHelp rejects every other single-dash token outright.
//   - an inline "--opt=value" token whose opt isn't a declared Values
//     entry: scanThroughHelp already rejects every inline token not naming
//     an InlineOnly-declared (and allowed-value) option.
//
// It DOES need to handle the Values/Flags collision case (see
// buildDeclarations): an inline token whose *resolved* declaration is
// declBoolean (because a Flags entry clobbered a same-named Values entry)
// produces the verbatim Node message "Option '--name' does not take an
// argument" -- pinned by probe, see TestValuesFlagsCollision -- since that
// message doesn't match any of arguments.ts's parseFailure regexes and so
// passes through unmodified.
func resolve(bound []string, declarations map[string]declaration, allowPositionals bool) (*rawResult, error) {
	result := &rawResult{
		stringValues: make(map[string][]string),
		boolSeen:     make(map[string]bool),
	}

	for _, token := range bound {
		switch {
		case token == "-h":
			decl, ok := declarations["help"]
			if !ok || decl.short != "h" {
				// Help disabled entirely, or "help" was registered without
				// the short alias (pre-declared via Flags or Values) --
				// see the "-h rejected when help is disabled"/"-h rejected
				// when --help was pre-declared as a plain flag" cases in
				// TestParseCommandArgumentsErrors, and
				// TestHelpDeclaredAsValuesOption.
				return nil, unknownArgument("-h")
			}
			result.boolSeen["help"] = true
			result.occurrences = append(result.occurrences, Occurrence{Kind: OccurrenceOption, Name: "--help"})

		case strings.HasPrefix(token, "--"):
			if eq := strings.IndexByte(token, '='); eq >= 0 {
				name, value := token[:eq], token[eq+1:]
				stripped := name[2:]
				decl, ok := declarations[stripped]
				if !ok {
					return nil, unknownArgument(token)
				}
				if decl.kind == declBoolean {
					return nil, &CliArgumentParseError{
						Message: fmt.Sprintf("Option '--%s' does not take an argument", stripped),
					}
				}
				result.stringValues[stripped] = append(result.stringValues[stripped], value)
				result.occurrences = append(result.occurrences, Occurrence{
					Kind: OccurrenceOption, Name: name, Value: value, HasValue: true,
				})
				continue
			}
			stripped := token[2:]
			decl, ok := declarations[stripped]
			if !ok {
				return nil, unknownArgument(token)
			}
			if decl.kind == declString {
				return nil, &CliArgumentParseError{Message: fmt.Sprintf("--%s requires a value", stripped)}
			}
			result.boolSeen[stripped] = true
			result.occurrences = append(result.occurrences, Occurrence{Kind: OccurrenceOption, Name: token})

		default:
			if !allowPositionals {
				return nil, unknownArgument(token)
			}
			result.positionals = append(result.positionals, token)
			result.occurrences = append(result.occurrences, Occurrence{
				Kind: OccurrencePositional, Value: token, HasValue: true,
			})
		}
	}
	return result, nil
}

// assembleOptions reproduces arguments.ts's per-Values-declaration assembly
// loop: for every declared option (visited in sorted order -- see
// ParseConfig.Values), read back whatever resolve collected for its
// stripped name and apply the multiple/allowEmpty checks, or skip the key
// entirely if it was never supplied.
//
// May panic with *StringOptionTypeError (the Values/Flags collision case
// where the option was supplied as a bare flag, not an inline value --
// resolve already rejects the inline-value collision directly).
func assembleOptions(values map[string]ValueOption, declarations map[string]declaration, raw *rawResult) (map[string][]string, error) {
	options := make(map[string][]string, len(values))

	names := make([]string, 0, len(values))
	for option := range values {
		names = append(names, option)
	}
	sort.Strings(names)

	for _, option := range names {
		decl := values[option]
		stripped := option[2:] // already validated by buildDeclarations
		resolved := declarations[stripped]

		if resolved.kind == declBoolean {
			if !raw.boolSeen[stripped] {
				continue // never supplied; JS: parsed.values[key] === undefined
			}
			panic(&StringOptionTypeError{
				Message: fmt.Sprintf("%s did not parse as a string option", option),
			})
		}

		strs, ok := raw.stringValues[stripped]
		if !ok {
			continue
		}
		if decl.RejectDuplicates && len(strs) > 1 {
			return nil, &CliArgumentParseError{Message: fmt.Sprintf("%s may be specified only once", option)}
		}
		if !decl.AllowEmpty {
			for _, value := range strs {
				if value == "" {
					return nil, &CliArgumentParseError{Message: fmt.Sprintf("%s requires a value", option)}
				}
			}
		}
		options[option] = strs
	}
	return options, nil
}

// assembleFlags reproduces arguments.ts's flags Set assembly: every
// configured Flags entry that was actually seen, plus "--help" when help is
// enabled and was seen -- mirroring the TS source's two separate (and, for
// a caller-declared "--help" flag, redundant-but-harmless) insertions.
func assembleFlags(config ParseConfig, raw *rawResult) Flags {
	flags := make(Flags, len(config.Flags)+1)
	for _, flag := range config.Flags {
		stripped := flag[2:] // already validated by buildDeclarations
		if raw.boolSeen[stripped] {
			flags[flag] = struct{}{}
		}
	}
	if !config.HelpDisabled && raw.boolSeen["help"] {
		flags["--help"] = struct{}{}
	}
	return flags
}
