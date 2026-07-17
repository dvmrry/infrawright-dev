package terraformcmd

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"unicode"
)

func TestNodeUnicode16Lower(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "unconditional expansion", input: ".İXE", want: ".i\u0307xe"},
		{name: "final sigma", input: "ΟΣ", want: "ος"},
		{name: "nonfinal sigma", input: "ΟΣΑ", want: "οσα"},
		{name: "final sigma across Unicode 15 case ignorable", input: "A\u0301Σ", want: "a\u0301ς"},
		{name: "final sigma across Unicode 16 case ignorable", input: "A\u0897Σ", want: "a\u0897ς"},
		{name: "Unicode 16 removal from case ignorable", input: "A\U0001171EΣ", want: "a\U0001171Eσ"},
		{name: "case ignorable wins over cased", input: "ʰΣ", want: "ʰσ"},
		{name: "case ignorable before end", input: "AΣʰ", want: "aςʰ"},
		{name: "case ignorable before cased", input: "AΣʰA", want: "aσʰa"},
		{name: "Unicode 16 Cyrillic mapping and context", input: "\u1C89Σ", want: "\u1C8Aς"},
		{name: "Unicode 16 Garay mapping and context", input: "\U00010D50Σ", want: "\U00010D70ς"},
		{name: "Unicode 16 Latin mappings", input: "\uA7CB\uA7CC\uA7DA\uA7DC", want: "\u0264\uA7CD\uA7DB\u019B"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := nodeUnicode16Lower(test.input)
			if err != nil {
				t.Fatalf("nodeUnicode16Lower(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Errorf("nodeUnicode16Lower(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNodeUnicode16LowerBaseVersion(t *testing.T) {
	if unicode.Version != nodeUnicode16LowerBaseVersion {
		t.Fatalf("unicode.Version = %q, want frozen base %q", unicode.Version, nodeUnicode16LowerBaseVersion)
	}
	err := requireNodeUnicode16LowerBase("16.0.0")
	requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_COMMAND_PATH")
}

// TestNodeUnicode16LowerExhaustiveDigest covers every Unicode scalar's full
// lowercase mapping and the three-way character class that controls
// Final_Sigma. The Node 24.15 oracle and corpus definition are frozen in
// testdata/node24-unicode-lower-oracle.mjs.
func TestNodeUnicode16LowerExhaustiveDigest(t *testing.T) {
	digest := sha256.New()
	var length [4]byte
	var class [1]byte
	for value := rune(0); value <= unicode.MaxRune; value++ {
		if value >= 0xD800 && value <= 0xDFFF {
			continue
		}
		lowered, err := nodeUnicode16Lower(string(value))
		if err != nil {
			t.Fatalf("nodeUnicode16Lower(U+%04X): %v", value, err)
		}
		binary.BigEndian.PutUint32(length[:], uint32(len(lowered)))
		digest.Write(length[:])
		digest.Write([]byte(lowered))

		switch {
		case nodeUnicode16CaseIgnorable(value):
			class[0] = 0
		case nodeUnicode16Cased(value):
			class[0] = 1
		default:
			class[0] = 2
		}
		digest.Write(class[:])
	}
	got := hex.EncodeToString(digest.Sum(nil))
	const want = "6ad4680180cd0875945037489e01452934e65cf1156ece8ed386829a524d4b9f"
	if got != want {
		t.Errorf("Node 24.15 Unicode-lower corpus SHA-256 = %q, want %q", got, want)
	}
}
