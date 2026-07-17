package terraformcmd

import (
	"strings"
	"unicode"
)

const nodeUnicode16LowerBaseVersion = "15.0.0"

// nodeUnicode16Lower reproduces the locale-independent ECMAScript
// String.prototype.toLowerCase behavior in Node 24.15, which is frozen to
// Unicode 16.0. Go 1.26 carries Unicode 15.0 tables, so the Unicode 16 simple
// mappings and casing-property deltas are explicit below. Unicode's only
// unconditional expanding lowercase mapping is U+0130; Final_Sigma is the
// only language-independent contextual lowercase mapping.
//
// The source data are Unicode 16.0 UnicodeData.txt, SpecialCasing.txt,
// DerivedCoreProperties.txt, and auxiliary/WordBreakProperty.txt.
func nodeUnicode16Lower(value string) (string, error) {
	if err := requireNodeUnicode16LowerBase(unicode.Version); err != nil {
		return "", err
	}
	characters := []rune(value)
	var result strings.Builder
	result.Grow(len(value))
	for index, character := range characters {
		switch character {
		case '\u0130':
			result.WriteString("i\u0307")
		case '\u03A3':
			if nodeUnicode16FinalSigma(characters, index) {
				result.WriteRune('\u03C2')
			} else {
				result.WriteRune('\u03C3')
			}
		default:
			result.WriteRune(nodeUnicode16SimpleLower(character))
		}
	}
	return result.String(), nil
}

func requireNodeUnicode16LowerBase(version string) error {
	if version == nodeUnicode16LowerBaseVersion {
		return nil
	}
	return domainFailure(
		"UNRESOLVED_TERRAFORM_COMMAND_PATH",
		"Terraform executable search path Unicode tables are unsupported",
	)
}

func nodeUnicode16SimpleLower(character rune) rune {
	switch character {
	case '\u1C89':
		return '\u1C8A'
	case '\uA7CB':
		return '\u0264'
	case '\uA7CC':
		return '\uA7CD'
	case '\uA7DA':
		return '\uA7DB'
	case '\uA7DC':
		return '\u019B'
	}
	if character >= '\U00010D50' && character <= '\U00010D65' {
		return character + 0x20
	}
	return unicode.ToLower(character)
}

func nodeUnicode16FinalSigma(characters []rune, index int) bool {
	precededByCased := false
	for previous := index - 1; previous >= 0; previous-- {
		character := characters[previous]
		if nodeUnicode16CaseIgnorable(character) {
			continue
		}
		precededByCased = nodeUnicode16Cased(character)
		break
	}
	if !precededByCased {
		return false
	}
	for next := index + 1; next < len(characters); next++ {
		character := characters[next]
		if nodeUnicode16CaseIgnorable(character) {
			continue
		}
		return !nodeUnicode16Cased(character)
	}
	return true
}

func nodeUnicode16Cased(character rune) bool {
	if unicode.In(character, unicode.Lu, unicode.Ll, unicode.Lt) ||
		unicode.Is(unicode.Other_Uppercase, character) ||
		unicode.Is(unicode.Other_Lowercase, character) {
		return true
	}
	return character >= '\u1C89' && character <= '\u1C8A' ||
		character >= '\uA7CB' && character <= '\uA7CD' ||
		character >= '\uA7DA' && character <= '\uA7DC' ||
		character >= '\U00010D50' && character <= '\U00010D65' ||
		character >= '\U00010D70' && character <= '\U00010D85'
}

func nodeUnicode16CaseIgnorable(character rune) bool {
	// U+1171E changed from Mn to Mc in Unicode 16 and therefore left the
	// derived Case_Ignorable property carried by Go's Unicode 15 tables.
	if character == '\U0001171E' {
		return false
	}
	if unicode.In(character, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) {
		return true
	}
	// Word_Break=MidLetter, MidNumLet, or Single_Quote adds these characters
	// to Case_Ignorable beyond the general categories above.
	switch character {
	case '\u0027', '\u002E', '\u003A', '\u00B7', '\u0387', '\u055F',
		'\u05F4', '\u2018', '\u2019', '\u2024', '\u2027', '\uFE13',
		'\uFE52', '\uFE55', '\uFF07', '\uFF0E', '\uFF1A':
		return true
	}
	// Unicode 16 additions to DerivedCoreProperties Case_Ignorable.
	return character == '\u0897' ||
		character == '\U00010D4E' ||
		character >= '\U00010D69' && character <= '\U00010D6D' ||
		character == '\U00010D6F' ||
		character == '\U00010EFC' ||
		character >= '\U000113BB' && character <= '\U000113C0' ||
		character == '\U000113CE' ||
		character == '\U000113D0' ||
		character == '\U000113D2' ||
		character >= '\U000113E1' && character <= '\U000113E2' ||
		character == '\U00011F5A' ||
		character >= '\U0001611E' && character <= '\U00016129' ||
		character >= '\U0001612D' && character <= '\U0001612F' ||
		character >= '\U00016D40' && character <= '\U00016D42' ||
		character >= '\U00016D6B' && character <= '\U00016D6C' ||
		character >= '\U0001E5EE' && character <= '\U0001E5EF'
}
