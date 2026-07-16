package cliargs

import (
	"testing"
)

// Every case in this file that depends on parseArgs internals (exact
// message wording, token/collision precedence) was pinned by compiling
// node-src/cli/arguments.ts with esbuild against this repo's node
// (v24.15.0 at the time of writing) and calling the real
// parseCommandArguments/parseArgs directly -- never guessed from reading
// documentation. Each such case names the probe script inline; the probe
// transcripts are reproduced in the comment blocks below so a future reader
// does not have to re-run node to see what was observed.

func optOcc(name string) Occurrence {
	return Occurrence{Kind: OccurrenceOption, Name: name}
}

func optOccVal(name, value string) Occurrence {
	return Occurrence{Kind: OccurrenceOption, Name: name, Value: value, HasValue: true}
}

func posOcc(value string) Occurrence {
	return Occurrence{Kind: OccurrencePositional, Value: value, HasValue: true}
}

func mustParse(t *testing.T, args []string, config ParseConfig) ParsedArguments {
	t.Helper()
	parsed, err := ParseCommandArguments(args, config)
	if err != nil {
		t.Fatalf("ParseCommandArguments(%q, %+v): unexpected error: %v", args, config, err)
	}
	return parsed
}

func assertParseError(t *testing.T, args []string, config ParseConfig, wantMessage string) {
	t.Helper()
	_, err := ParseCommandArguments(args, config)
	if err == nil {
		t.Fatalf("ParseCommandArguments(%q, %+v): expected error %q, got success", args, config, wantMessage)
	}
	cliErr, ok := err.(*CliArgumentParseError)
	if !ok {
		t.Fatalf("ParseCommandArguments(%q, %+v): expected *CliArgumentParseError, got %T: %v", args, config, err, err)
	}
	if cliErr.Message != wantMessage {
		t.Fatalf("ParseCommandArguments(%q, %+v): error message = %q, want %q", args, config, cliErr.Message, wantMessage)
	}
}

// --- Success-path behavior matrix -------------------------------------------

func TestParseCommandArgumentsSuccess(t *testing.T) {
	type testCase struct {
		name   string
		args   []string
		config ParseConfig

		wantFlags       []string
		wantOptions     map[string][]string
		wantPositionals []string
		wantOccurrences []Occurrence
	}

	cases := []testCase{
		{
			// probe A-neg-value-separate: a value beginning with "-" binds
			// to the preceding declared option instead of being rejected
			// as an unknown single-dash token.
			name:            "value beginning with dash binds to preceding option",
			args:            []string{"--pack", "-x"},
			config:          ParseConfig{Values: map[string]ValueOption{"--pack": {}}},
			wantOptions:     map[string][]string{"--pack": {"-x"}},
			wantOccurrences: []Occurrence{optOccVal("--pack", "-x")},
		},
		{
			// probe B-dashdash-as-value: a literal "--" binds as a value
			// rather than being rejected as a bare option-terminator.
			name:            "literal -- binds to preceding option as a value",
			args:            []string{"--pack", "--"},
			config:          ParseConfig{Values: map[string]ValueOption{"--pack": {}}},
			wantOptions:     map[string][]string{"--pack": {"--"}},
			wantOccurrences: []Occurrence{optOccVal("--pack", "--")},
		},
		{
			// probe C-help-as-value: a literal "--help" bound as a value
			// does not trigger the help cutoff or set the help flag.
			name:            "literal --help binds to preceding option as a value",
			args:            []string{"--pack", "--help"},
			config:          ParseConfig{Values: map[string]ValueOption{"--pack": {}}},
			wantOptions:     map[string][]string{"--pack": {"--help"}},
			wantOccurrences: []Occurrence{optOccVal("--pack", "--help")},
		},
		{
			// probe D2-empty-separate-value-allowEmpty
			name:            "empty separate value accepted with AllowEmpty",
			args:            []string{"--pack", ""},
			config:          ParseConfig{Values: map[string]ValueOption{"--pack": {AllowEmpty: true}}},
			wantOptions:     map[string][]string{"--pack": {""}},
			wantOccurrences: []Occurrence{optOccVal("--pack", "")},
		},
		{
			// probe E-repeated-flag
			name:            "repeated boolean flag",
			args:            []string{"--verbose", "--verbose"},
			config:          ParseConfig{Flags: []string{"--verbose"}},
			wantFlags:       []string{"--verbose"},
			wantOccurrences: []Occurrence{optOcc("--verbose"), optOcc("--verbose")},
		},
		{
			// probe F-help-cutoff-mixed: everything from --help onward
			// (including a second declared-option occurrence) is dropped.
			name:            "help cutoff drops everything after it, including reused option names",
			args:            []string{"--pack", "x", "--help", "--unknown", "--pack"},
			config:          ParseConfig{Values: map[string]ValueOption{"--pack": {}}},
			wantFlags:       []string{"--help"},
			wantOptions:     map[string][]string{"--pack": {"x"}},
			wantOccurrences: []Occurrence{optOccVal("--pack", "x"), optOcc("--help")},
		},
		{
			// probe G-dash-h
			name:            "-h alone sets the help flag",
			args:            []string{"-h"},
			config:          ParseConfig{},
			wantFlags:       []string{"--help"},
			wantOccurrences: []Occurrence{optOcc("--help")},
		},
		{
			// probe K-positionals
			name:            "bare positionals when allowed",
			args:            []string{"a", "b", "c"},
			config:          ParseConfig{AllowPositionals: true},
			wantPositionals: []string{"a", "b", "c"},
			wantOccurrences: []Occurrence{posOcc("a"), posOcc("b"), posOcc("c")},
		},
		{
			// probe O-dup-default: RejectDuplicates left false (the Go
			// zero value) permits repeats, matching `multiple: undefined`.
			name:            "duplicate values allowed by default",
			args:            []string{"--value", "a", "--value", "b"},
			config:          ParseConfig{Values: map[string]ValueOption{"--value": {}}},
			wantOptions:     map[string][]string{"--value": {"a", "b"}},
			wantOccurrences: []Occurrence{optOccVal("--value", "a"), optOccVal("--value", "b")},
		},
		{
			// probe multiple-true-explicit-dup-ok: an explicit `multiple:
			// true` behaves identically to leaving it unset.
			name: "duplicate values allowed with RejectDuplicates false explicitly",
			args: []string{"--value", "a", "--value", "b"},
			config: ParseConfig{Values: map[string]ValueOption{
				"--value": {RejectDuplicates: false},
			}},
			wantOptions:     map[string][]string{"--value": {"a", "b"}},
			wantOccurrences: []Occurrence{optOccVal("--value", "a"), optOccVal("--value", "b")},
		},
		{
			// probe Q-inline-only-ok
			name: "inline spelling accepted for an InlineOnly option with an allowed value",
			args: []string{"--order=references"},
			config: ParseConfig{Values: map[string]ValueOption{
				"--order": {AllowedValues: []string{"references"}, InlineOnly: true},
			}},
			wantOptions:     map[string][]string{"--order": {"references"}},
			wantOccurrences: []Occurrence{optOccVal("--order", "references")},
		},
		{
			// probe V-value-with-equals: an "=" inside a bound value does
			// not get reinterpreted; only the token's own first "=" (if
			// any) matters, and this token has none of its own.
			name:            "bound value containing = is not reinterpreted",
			args:            []string{"--pack", "a=b"},
			config:          ParseConfig{Values: map[string]ValueOption{"--pack": {}}},
			wantOptions:     map[string][]string{"--pack": {"a=b"}},
			wantOccurrences: []Occurrence{optOccVal("--pack", "a=b")},
		},
		{
			// probe X-order-independent
			name: "interleaved distinct value options each accumulate independently, in order",
			args: []string{"--a", "1", "--b", "2", "--a", "3"},
			config: ParseConfig{Values: map[string]ValueOption{
				"--a": {AllowEmpty: true},
				"--b": {AllowEmpty: true},
			}},
			wantOptions: map[string][]string{"--a": {"1", "3"}, "--b": {"2"}},
			wantOccurrences: []Occurrence{
				optOccVal("--a", "1"), optOccVal("--b", "2"), optOccVal("--a", "3"),
			},
		},
		{
			// probe Y-custom-help-flag-collision: pre-declaring "--help" as
			// a plain flag doesn't produce two separate flag entries (Set
			// semantics) and default help handling still recognizes it.
			name:            "explicit --help flag declaration coexists with default help handling",
			args:            []string{"--help"},
			config:          ParseConfig{Flags: []string{"--help"}},
			wantFlags:       []string{"--help"},
			wantOccurrences: []Occurrence{optOcc("--help")},
		},
		{
			// Direct port of node-tests/property-invariants.test.ts's
			// "parseArgs adapter retains mixed option and positional
			// occurrence order" test.
			name: "mixed option and positional occurrence order (ported node-tests case)",
			args: []string{"PACK=first", "--pack", "second", "PACK=third"},
			config: ParseConfig{
				AllowPositionals: true,
				Values:           map[string]ValueOption{"--pack": {}},
			},
			wantPositionals: []string{"PACK=first", "PACK=third"},
			wantOptions:     map[string][]string{"--pack": {"second"}},
			wantOccurrences: []Occurrence{
				posOcc("PACK=first"), optOccVal("--pack", "second"), posOcc("PACK=third"),
			},
		},
		{
			// probe help-consumed-then-real-help-trailing: the first
			// "--help" is consumed as --pack's bound value; the second is
			// the real help occurrence, which still truncates a further
			// trailing "--zzz".
			name: "a bound --help value doesn't shadow a later real --help",
			args: []string{"--pack", "--help", "--help", "--zzz"},
			config: ParseConfig{
				Values: map[string]ValueOption{"--pack": {}},
			},
			wantFlags:       []string{"--help"},
			wantOptions:     map[string][]string{"--pack": {"--help"}},
			wantOccurrences: []Occurrence{optOccVal("--pack", "--help"), optOcc("--help")},
		},
		{
			// probe inlineOnly-as-bound-value: an InlineOnly option's own
			// "--opt=value" spelling, when it is itself bound as someone
			// else's value, is never independently parsed.
			name: "an InlineOnly option's own spelling can be swallowed as another option's bound value",
			args: []string{"--pack", "--order=references"},
			config: ParseConfig{Values: map[string]ValueOption{
				"--pack":  {},
				"--order": {InlineOnly: true, AllowedValues: []string{"references"}},
			}},
			wantOptions:     map[string][]string{"--pack": {"--order=references"}},
			wantOccurrences: []Occurrence{optOccVal("--pack", "--order=references")},
		},
		{
			// probe value-literal-equals-form: same as above, but with a
			// value that would have failed AllowedValues had --order ever
			// actually been parsed as itself -- proving it never is.
			name: "a swallowed InlineOnly spelling is never validated against AllowedValues",
			args: []string{"--pack", "--order=bad"},
			config: ParseConfig{Values: map[string]ValueOption{
				"--pack":  {},
				"--order": {InlineOnly: true, AllowedValues: []string{"references"}},
			}},
			wantOptions:     map[string][]string{"--pack": {"--order=bad"}},
			wantOccurrences: []Occurrence{optOccVal("--pack", "--order=bad")},
		},
		{
			// probe empty-args
			name:   "empty argv with default config",
			args:   []string{},
			config: ParseConfig{},
		},
		{
			// probe allowedValues-no-inlineOnly-separate: AllowedValues is
			// only ever enforced from the inline-spelling path, which is
			// itself only reachable for InlineOnly options -- so a
			// non-InlineOnly option's AllowedValues goes unenforced in the
			// (only spelling it accepts) separate form. See
			// ValueOption.AllowedValues' doc comment.
			name: "AllowedValues goes unenforced for a non-InlineOnly option's separate spelling",
			args: []string{"--order", "bogus"},
			config: ParseConfig{Values: map[string]ValueOption{
				"--order": {AllowedValues: []string{"references"}},
			}},
			wantOptions:     map[string][]string{"--order": {"bogus"}},
			wantOccurrences: []Occurrence{optOccVal("--order", "bogus")},
		},
		{
			// probe occurrence-order-mixed2
			name: "flags and value options interleave correctly in occurrence order",
			args: []string{"--verbose", "--pack", "a", "--verbose"},
			config: ParseConfig{
				Flags:  []string{"--verbose"},
				Values: map[string]ValueOption{"--pack": {}},
			},
			wantFlags:   []string{"--verbose"},
			wantOptions: map[string][]string{"--pack": {"a"}},
			wantOccurrences: []Occurrence{
				optOcc("--verbose"), optOccVal("--pack", "a"), optOcc("--verbose"),
			},
		},
		{
			// probe never-supplied: an unsupplied Values option is simply
			// absent from Options, not present with an empty slice.
			name:        "unsupplied value option is absent from Options",
			args:        []string{},
			config:      ParseConfig{Values: map[string]ValueOption{"--pack": {}}},
			wantOptions: map[string][]string{},
		},
		{
			// An InlineOnly option with no AllowedValues restriction,
			// exercised through a genuine (not swallowed-as-someone-else's-
			// value) inline occurrence: unlike the Q-inline-only-ok probe,
			// this has no allow-list to satisfy at all.
			name:            "InlineOnly option with no AllowedValues accepts any inline value",
			args:            []string{"--order=anything"},
			config:          ParseConfig{Values: map[string]ValueOption{"--order": {InlineOnly: true}}},
			wantOptions:     map[string][]string{"--order": {"anything"}},
			wantOccurrences: []Occurrence{optOccVal("--order", "anything")},
		},
		{
			// A Values/Flags name collision (see TestValuesFlagsCollision)
			// that is simply never supplied on the command line: assembleOptions
			// must skip it silently rather than treating "never supplied" as
			// a StringOptionTypeError.
			name: "an unsupplied Values/Flags collision name causes neither an error nor a panic",
			args: []string{},
			config: ParseConfig{
				Values: map[string]ValueOption{"--x": {}},
				Flags:  []string{"--x"},
			},
			wantOptions: map[string][]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := mustParse(t, tc.args, tc.config)

			gotFlags := make([]string, 0, len(parsed.Flags))
			for name := range parsed.Flags {
				gotFlags = append(gotFlags, name)
			}
			wantFlagSet := make(map[string]struct{}, len(tc.wantFlags))
			for _, name := range tc.wantFlags {
				wantFlagSet[name] = struct{}{}
			}
			if len(gotFlags) != len(wantFlagSet) {
				t.Errorf("Flags = %v, want %v", gotFlags, tc.wantFlags)
			}
			for _, name := range tc.wantFlags {
				if !parsed.Flags.Has(name) {
					t.Errorf("Flags missing %q; got %v", name, gotFlags)
				}
			}

			wantOptions := tc.wantOptions
			if wantOptions == nil {
				wantOptions = map[string][]string{}
			}
			if len(parsed.Options) != len(wantOptions) || !optionsEqual(parsed.Options, wantOptions) {
				t.Errorf("Options = %#v, want %#v", parsed.Options, wantOptions)
			}

			if !stringSlicesEqual(parsed.Positionals, tc.wantPositionals) {
				t.Errorf("Positionals = %#v, want %#v", parsed.Positionals, tc.wantPositionals)
			}

			if !occurrencesEqual(parsed.Occurrences, tc.wantOccurrences) {
				t.Errorf("Occurrences = %#v, want %#v", parsed.Occurrences, tc.wantOccurrences)
			}
		})
	}
}

func optionsEqual(got, want map[string][]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, wantValues := range want {
		if !stringSlicesEqual(got[key], wantValues) {
			return false
		}
	}
	return true
}

func stringSlicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func occurrencesEqual(got, want []Occurrence) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// --- Error-path behavior matrix ---------------------------------------------

func TestParseCommandArgumentsErrors(t *testing.T) {
	type testCase struct {
		name        string
		args        []string
		config      ParseConfig
		wantMessage string
	}

	cases := []testCase{
		{
			// probe D-empty-separate-value
			name:        "empty separate value rejected without AllowEmpty",
			args:        []string{"--pack", ""},
			config:      ParseConfig{Values: map[string]ValueOption{"--pack": {}}},
			wantMessage: "--pack requires a value",
		},
		{
			// probe allowEmpty-false-explicit
			name:        "empty value rejected with AllowEmpty explicitly false",
			args:        []string{"--value", ""},
			config:      ParseConfig{Values: map[string]ValueOption{"--value": {AllowEmpty: false}}},
			wantMessage: "--value requires a value",
		},
		{
			// probe H-dash-hh: distinct from the literal "-h" alias.
			name:        "-hh is rejected, not treated as help",
			args:        []string{"-hh"},
			config:      ParseConfig{},
			wantMessage: "unknown argument -hh",
		},
		{
			// probe I-help-disabled
			name:        "-h rejected when help is disabled",
			args:        []string{"-h"},
			config:      ParseConfig{HelpDisabled: true},
			wantMessage: "unknown argument -h",
		},
		{
			// probe J-bare-dashdash
			name:        "bare -- always rejected",
			args:        []string{"--"},
			config:      ParseConfig{},
			wantMessage: "unknown argument --",
		},
		{
			// probe dashdash-with-allowPositionals-true: AllowPositionals
			// does not change this.
			name:        "bare -- rejected even when positionals are allowed",
			args:        []string{"--"},
			config:      ParseConfig{AllowPositionals: true},
			wantMessage: "unknown argument --",
		},
		{
			// probe L-positional-rejected
			name:        "bare positional rejected when not allowed",
			args:        []string{"a"},
			config:      ParseConfig{},
			wantMessage: "unknown argument a",
		},
		{
			// probe M-unknown-long
			name:        "undeclared long option rejected",
			args:        []string{"--zzz"},
			config:      ParseConfig{},
			wantMessage: "unknown argument --zzz",
		},
		{
			// probe N-dup-reject
			name:        "duplicate rejected when RejectDuplicates is set",
			args:        []string{"--value", "a", "--value", "b"},
			config:      ParseConfig{Values: map[string]ValueOption{"--value": {RejectDuplicates: true}}},
			wantMessage: "--value may be specified only once",
		},
		{
			// probe P-inline-rejected
			name:        "inline spelling rejected for a non-InlineOnly option",
			args:        []string{"--value=x"},
			config:      ParseConfig{Values: map[string]ValueOption{"--value": {}}},
			wantMessage: "unknown argument --value=x",
		},
		{
			// property-invariants.test.ts: ["--value=inline"] with no
			// declared values at all.
			name:        "inline spelling rejected for a completely undeclared option",
			args:        []string{"--value=inline"},
			config:      ParseConfig{},
			wantMessage: "unknown argument --value=inline",
		},
		{
			// probe U-short-x
			name:        "single-dash short option always rejected, even if the long form is a declared flag",
			args:        []string{"-x"},
			config:      ParseConfig{Flags: []string{"--x"}},
			wantMessage: "unknown argument -x",
		},
		{
			// probe unexpected/-x/-xyz family from property-invariants.test.ts
			name:        "multi-character single-dash option rejected",
			args:        []string{"-xyz"},
			config:      ParseConfig{},
			wantMessage: "unknown argument -xyz",
		},
		{
			// probe W-trailing-declared-opt
			name:        "declared value option with nothing following it requires a value",
			args:        []string{"--pack"},
			config:      ParseConfig{Values: map[string]ValueOption{"--pack": {}}},
			wantMessage: "--pack requires a value",
		},
		{
			// probe inlineOnly-separate-with-dash-value: separate spelling
			// is rejected outright for an InlineOnly option, before the
			// bound value (here "-x") is ever examined.
			name:        "separate spelling rejected for an InlineOnly option regardless of its value's shape",
			args:        []string{"--order", "-x"},
			config:      ParseConfig{Values: map[string]ValueOption{"--order": {InlineOnly: true}}},
			wantMessage: "unknown argument --order",
		},
		{
			// probe Q's sibling in property-invariants.test.ts: separate
			// spelling rejected even with a "legal" following token.
			name:        "separate spelling rejected for an InlineOnly option with allowed-looking value",
			args:        []string{"--order", "references"},
			config:      ParseConfig{Values: map[string]ValueOption{"--order": {AllowedValues: []string{"references"}, InlineOnly: true}}},
			wantMessage: "unknown argument --order",
		},
		{
			// property-invariants.test.ts: inline spelling with a
			// disallowed value, immediately followed by --help (never
			// reached: the error fires before help cutoff scanning gets
			// that far).
			name:        "inline spelling with a disallowed value is rejected before reaching a trailing --help",
			args:        []string{"--order=bad", "--help"},
			config:      ParseConfig{Values: map[string]ValueOption{"--order": {AllowedValues: []string{"references"}, InlineOnly: true}}},
			wantMessage: "unknown argument --order=bad",
		},
		{
			// probe allowedValues-no-inlineOnly-inline-allowed: inline
			// spelling is rejected for a non-InlineOnly option even when
			// the value itself would satisfy AllowedValues -- the
			// rejection is about the spelling, not the value.
			name:        "inline spelling rejected for non-InlineOnly option even with an otherwise-allowed value",
			args:        []string{"--order=references"},
			config:      ParseConfig{Values: map[string]ValueOption{"--order": {AllowedValues: []string{"references"}}}},
			wantMessage: "unknown argument --order=references",
		},
		{
			// probe help-flag-then-dash-h: pre-declaring "--help" via
			// Flags registers the "help" key without the short alias.
			name:        "-h rejected when --help was pre-declared as a plain flag",
			args:        []string{"-h"},
			config:      ParseConfig{Flags: []string{"--help"}},
			wantMessage: "unknown argument -h",
		},
		{
			// probe help-false-long-form
			name:        "--help itself rejected as unknown when help is disabled",
			args:        []string{"--help"},
			config:      ParseConfig{HelpDisabled: true},
			wantMessage: "unknown argument --help",
		},
		{
			// probe help-disabled-cutoff-still-drops-trailing: the cutoff
			// scan still truncates on the literal "--help" text even
			// though help ends up rejected -- so the trailing "--zzz"
			// contributes nothing to the error, but the error is
			// still about "--help", not "--zzz".
			name:        "help cutoff still truncates trailing args even when help is disabled",
			args:        []string{"--help", "--zzz"},
			config:      ParseConfig{HelpDisabled: true},
			wantMessage: "unknown argument --help",
		},
		{
			// probe consecutive-declared-opt-as-value: chained binding
			// (a declared option's own name bound as its own value) still
			// leaves the final trailing token subject to ordinary
			// positional rejection.
			name:        "a declared option's own name can be chained as its own bound value",
			args:        []string{"--pack", "--pack", "x"},
			config:      ParseConfig{Values: map[string]ValueOption{"--pack": {RejectDuplicates: true}}},
			wantMessage: "unknown argument x",
		},
		{
			// probe collision-value-and-flag
			name: "a name declared as both a Values option and a Flags entry rejects an inline value",
			args: []string{"--x", "1"},
			config: ParseConfig{
				Values: map[string]ValueOption{"--x": {}},
				Flags:  []string{"--x"},
			},
			wantMessage: "Option '--x' does not take an argument",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertParseError(t, tc.args, tc.config, tc.wantMessage)
		})
	}
}

// --- Declaration validation (panics) -----------------------------------------

func expectInvalidDeclarationPanic(t *testing.T, config ParseConfig, wantMessage string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic for config %+v, got none", config)
		}
		declErr, ok := r.(*InvalidDeclarationError)
		if !ok {
			t.Fatalf("expected panic value *InvalidDeclarationError, got %T: %v", r, r)
		}
		if declErr.Message != wantMessage {
			t.Fatalf("panic message = %q, want %q", declErr.Message, wantMessage)
		}
	}()
	_, _ = ParseCommandArguments(nil, config)
}

func TestInvalidOptionDeclarationsPanic(t *testing.T) {
	// probes decl-uppercase, decl-digit-start, decl-dash-start,
	// decl-no-prefix, decl-single-dash-prefix, decl-empty-after-dashdash,
	// decl-uppercase-in-tail, plus R/S from the first probe batch.
	cases := []struct {
		name        string
		config      ParseConfig
		wantMessage string
	}{
		{"uppercase flag", ParseConfig{Flags: []string{"--Bad_Flag"}}, `invalid CLI option declaration "--Bad_Flag"`},
		{"uppercase leading letter", ParseConfig{Flags: []string{"--Foo"}}, `invalid CLI option declaration "--Foo"`},
		{"digit as first character", ParseConfig{Flags: []string{"--1foo"}}, `invalid CLI option declaration "--1foo"`},
		{"three leading dashes", ParseConfig{Flags: []string{"---foo"}}, `invalid CLI option declaration "---foo"`},
		{"no dash prefix", ParseConfig{Flags: []string{"foo"}}, `invalid CLI option declaration "foo"`},
		{"single dash prefix", ParseConfig{Flags: []string{"-foo"}}, `invalid CLI option declaration "-foo"`},
		{"bare dashes with nothing after", ParseConfig{Flags: []string{"--"}}, `invalid CLI option declaration "--"`},
		{"uppercase in tail", ParseConfig{Flags: []string{"--fooBar"}}, `invalid CLI option declaration "--fooBar"`},
		{
			"invalid Values key uses the raw (unstripped) key in the message",
			ParseConfig{Values: map[string]ValueOption{"notdashdash": {}}},
			`invalid CLI option declaration "notdashdash"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectInvalidDeclarationPanic(t, tc.config, tc.wantMessage)
		})
	}
}

func TestValidOptionDeclarationsDoNotPanic(t *testing.T) {
	// probes decl-single-char, decl-with-digits-dashes, decl-trailing-dash
	cases := []struct {
		name   string
		config ParseConfig
	}{
		{"single character after dashes", ParseConfig{Flags: []string{"--a"}}},
		{"digits and dashes in tail", ParseConfig{Flags: []string{"--a-b2-c3"}}},
		{"trailing dash", ParseConfig{Flags: []string{"--foo-"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseCommandArguments(nil, tc.config); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// --- Values/Flags declaration collision (an edge case beyond the documented
// contract, included because it is the only way this port found to trigger
// arguments.ts's raw-message passthrough in parseFailure, and the only way
// to trigger its second uncaught TypeError). ---------------------------------

func TestValuesFlagsCollision(t *testing.T) {
	// probe collision-value-and-flag: a Flags entry sharing a Values key
	// clobbers it to boolean (buildDeclarations registers Values first,
	// then Flags); supplying it with an inline value produces Node's raw
	// "does not take an argument" message, which does not match any of
	// arguments.ts's parseFailure regexes and so passes through unchanged.
	t.Run("inline value on a collided name raises the raw parseArgs message", func(t *testing.T) {
		assertParseError(t,
			[]string{"--x", "1"},
			ParseConfig{Values: map[string]ValueOption{"--x": {}}, Flags: []string{"--x"}},
			"Option '--x' does not take an argument",
		)
	})

	// probe collision-bare-flag: supplying the same collided name as a
	// bare boolean flag (no value at all -- so resolve's inline-value
	// check above never fires) instead reaches assembleOptions, which
	// still iterates the original Values declaration and finds a bool
	// where it expects a string.
	t.Run("bare flag occurrence on a collided name panics with StringOptionTypeError", func(t *testing.T) {
		config := ParseConfig{Values: map[string]ValueOption{"--x": {}}, Flags: []string{"--x"}}
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic, got none")
			}
			typeErr, ok := r.(*StringOptionTypeError)
			if !ok {
				t.Fatalf("expected *StringOptionTypeError, got %T: %v", r, r)
			}
			const want = "--x did not parse as a string option"
			if typeErr.Message != want {
				t.Fatalf("panic message = %q, want %q", typeErr.Message, want)
			}
		}()
		_, _ = ParseCommandArguments([]string{"--x"}, config)
	})
}

// --- Repeatable value ordering (ported from
// node-tests/property-invariants.test.ts's fast-check property tests, as a
// fixed representative sample rather than a fuzz run: this package has no
// fast-check equivalent in scope, and the property itself -- "N occurrences
// of --value round-trip in order" -- is already exercised generatively by
// the ordering/interleaving cases in TestParseCommandArgumentsSuccess). -----

func TestRepeatableValuesPreserveOrder(t *testing.T) {
	samples := [][]string{
		{},
		{"only"},
		{"a", "b", "c"},
		{"", "non-empty", ""},
		{"dup", "dup", "dup"},
		{"has spaces", "has\ttabs", "has\nnewlines"},
		{"unicode: héllo", "unicode: 世界", "unicode: 🎉"},
	}
	for _, values := range samples {
		args := make([]string, 0, len(values)*2)
		for _, v := range values {
			args = append(args, "--value", v)
		}
		parsed := mustParse(t, args, ParseConfig{
			Values: map[string]ValueOption{"--value": {AllowEmpty: true}},
		})
		got := parsed.Options["--value"]
		if len(values) == 0 {
			if len(got) != 0 {
				t.Errorf("Options[--value] = %#v, want empty/absent for %#v", got, values)
			}
			continue
		}
		if !stringSlicesEqual(got, values) {
			t.Errorf("Options[--value] = %#v, want %#v", got, values)
		}
	}
}

// --- LastOption --------------------------------------------------------------

func TestLastOption(t *testing.T) {
	parsed := mustParse(t,
		[]string{"--value", "a", "--value", "b"},
		ParseConfig{Values: map[string]ValueOption{"--value": {AllowEmpty: true}}},
	)

	t.Run("returns the most recent value", func(t *testing.T) {
		got, ok := LastOption(parsed, "--value")
		if !ok || got != "b" {
			t.Fatalf("LastOption(--value) = (%q, %v), want (\"b\", true)", got, ok)
		}
	})

	t.Run("reports absence for an option never supplied", func(t *testing.T) {
		got, ok := LastOption(parsed, "--nope")
		if ok || got != "" {
			t.Fatalf("LastOption(--nope) = (%q, %v), want (\"\", false)", got, ok)
		}
	})

	t.Run("distinguishes an absent option from a present empty value", func(t *testing.T) {
		emptyOnly := mustParse(t,
			[]string{"--value", ""},
			ParseConfig{Values: map[string]ValueOption{"--value": {AllowEmpty: true}}},
		)
		got, ok := LastOption(emptyOnly, "--value")
		if !ok || got != "" {
			t.Fatalf("LastOption(--value) = (%q, %v), want (\"\", true)", got, ok)
		}
	})
}

// --- Flags.Has ---------------------------------------------------------------

func TestFlagsHas(t *testing.T) {
	parsed := mustParse(t, []string{"--verbose"}, ParseConfig{Flags: []string{"--verbose"}})
	if !parsed.Flags.Has("--verbose") {
		t.Error("Flags.Has(--verbose) = false, want true")
	}
	if parsed.Flags.Has("--quiet") {
		t.Error("Flags.Has(--quiet) = true, want false")
	}
}

// --- Error type assertions ----------------------------------------------------

func TestCliArgumentParseErrorType(t *testing.T) {
	_, err := ParseCommandArguments([]string{"--zzz"}, ParseConfig{})
	var target *CliArgumentParseError
	if !errorsAsCliArgumentParseError(err, &target) {
		t.Fatalf("expected *CliArgumentParseError, got %T: %v", err, err)
	}
	if target.Error() != "unknown argument --zzz" {
		t.Fatalf("Error() = %q, want %q", target.Error(), "unknown argument --zzz")
	}
}

func errorsAsCliArgumentParseError(err error, target **CliArgumentParseError) bool {
	cliErr, ok := err.(*CliArgumentParseError)
	if !ok {
		return false
	}
	*target = cliErr
	return true
}

// sanity check that occurrences/positionals/options come back as the
// documented types even in a completely empty-config, empty-args call.
func TestEmptyArgsEmptyConfig(t *testing.T) {
	parsed := mustParse(t, nil, ParseConfig{})
	if len(parsed.Flags) != 0 {
		t.Errorf("Flags = %v, want empty", parsed.Flags)
	}
	if len(parsed.Options) != 0 {
		t.Errorf("Options = %v, want empty", parsed.Options)
	}
	if len(parsed.Positionals) != 0 {
		t.Errorf("Positionals = %v, want empty", parsed.Positionals)
	}
	if len(parsed.Occurrences) != 0 {
		t.Errorf("Occurrences = %v, want empty", parsed.Occurrences)
	}
}

// --- Error() string forms of the two panic types -----------------------------

func TestPanicErrorTypesImplementError(t *testing.T) {
	t.Run("InvalidDeclarationError", func(t *testing.T) {
		defer func() {
			r := recover()
			declErr, ok := r.(*InvalidDeclarationError)
			if !ok {
				t.Fatalf("expected *InvalidDeclarationError, got %T", r)
			}
			var err error = declErr
			const want = `invalid CLI option declaration "--Bad"`
			if err.Error() != want {
				t.Fatalf("Error() = %q, want %q", err.Error(), want)
			}
		}()
		_, _ = ParseCommandArguments(nil, ParseConfig{Flags: []string{"--Bad"}})
	})

	t.Run("StringOptionTypeError", func(t *testing.T) {
		defer func() {
			r := recover()
			typeErr, ok := r.(*StringOptionTypeError)
			if !ok {
				t.Fatalf("expected *StringOptionTypeError, got %T", r)
			}
			var err error = typeErr
			const want = "--x did not parse as a string option"
			if err.Error() != want {
				t.Fatalf("Error() = %q, want %q", err.Error(), want)
			}
		}()
		config := ParseConfig{Values: map[string]ValueOption{"--x": {}}, Flags: []string{"--x"}}
		_, _ = ParseCommandArguments([]string{"--x"}, config)
	})
}

// --- OccurrenceKind.String() --------------------------------------------------

func TestOccurrenceKindString(t *testing.T) {
	cases := []struct {
		kind OccurrenceKind
		want string
	}{
		{OccurrenceOption, "option"},
		{OccurrencePositional, "positional"},
		{OccurrenceKind(99), "OccurrenceKind(99)"},
	}
	for _, tc := range cases {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("OccurrenceKind(%d).String() = %q, want %q", int(tc.kind), got, tc.want)
		}
	}
}

// --- Defensive branch in resolve() --------------------------------------------
//
// resolve's "unknown inline option" branch (an inline "--name=value" token
// whose stripped name isn't in declarations at all) can never actually be
// reached through the public ParseCommandArguments API: scanThroughHelp
// rejects every inline token whose name isn't a declared Values entry
// before bindSeparateValues or resolve ever run (see scanThroughHelp's doc
// comment), and buildDeclarations registers every Values key unconditionally
// (Flags can only overwrite a Values key's *kind*, never remove it). This
// test calls the unexported resolve directly with a hand-built token that
// bypasses that upstream guarantee, purely to exercise the defensive
// `if !ok` check itself -- mirroring how arguments.ts's own analogous
// defensive checks (e.g. the "did not parse as a string option" TypeError)
// are written for a condition its author believed impossible in practice.
// --- "--help" declared as a Values (string) option ----------------------------
//
// buildDeclarations only adds the default boolean help declaration when the
// "help" key doesn't already exist -- and Values are registered before that
// check runs, so declaring "--help" itself as a Values entry claims the key
// first and suppresses the default entirely, leaving "help" a *string*
// option (with no -h short alias) even though HelpDisabled was never set.
// Pinned via probes help-as-values-string-{bare,dashh,withvalue}.
func TestHelpDeclaredAsValuesOption(t *testing.T) {
	config := ParseConfig{Values: map[string]ValueOption{"--help": {}}}

	t.Run("bare --help now requires a value instead of being the help flag", func(t *testing.T) {
		assertParseError(t, []string{"--help"}, config, "--help requires a value")
	})

	t.Run("-h is rejected since no short alias was registered", func(t *testing.T) {
		assertParseError(t, []string{"-h"}, config, "unknown argument -h")
	})

	t.Run("--help with a bound value behaves like an ordinary string option", func(t *testing.T) {
		parsed := mustParse(t, []string{"--help", "x"}, config)
		if len(parsed.Flags) != 0 {
			t.Errorf("Flags = %v, want empty (help flag never set)", parsed.Flags)
		}
		want := map[string][]string{"--help": {"x"}}
		if !optionsEqual(parsed.Options, want) {
			t.Errorf("Options = %#v, want %#v", parsed.Options, want)
		}
	})
}

func TestResolveDefensiveUnknownInlineOption(t *testing.T) {
	_, err := resolve([]string{"--ghost=value"}, map[string]declaration{}, false)
	if err == nil {
		t.Fatal("expected an error for an inline token naming an undeclared option")
	}
	cliErr, ok := err.(*CliArgumentParseError)
	if !ok {
		t.Fatalf("expected *CliArgumentParseError, got %T: %v", err, err)
	}
	const want = "unknown argument --ghost=value"
	if cliErr.Message != want {
		t.Fatalf("error = %q, want %q", cliErr.Message, want)
	}
}
