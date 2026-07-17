package resthttp

import (
	"testing"

	"golang.org/x/net/idna"
)

func TestPinnedIDNAUnicodeTableVersion(t *testing.T) {
	if idna.UnicodeVersion != "15.0.0" {
		t.Fatalf("x/net/idna UnicodeVersion = %q, want reviewed table 15.0.0", idna.UnicodeVersion)
	}
}

func TestCanonicalWHATWGHostnameMatchesNode24UTS46Vectors(t *testing.T) {
	// These outputs are pinned from Node v24.15.0 (Unicode 16.0). The Go
	// profile deliberately uses x/net/idna v0.57.0's Go-1.26 Unicode 15 table.
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"NFC", "bücher.example", "xn--bcher-kva.example"},
		{"NFD normalized", "bu\u0308cher.example", "xn--bcher-kva.example"},
		{"compatibility mapped", "ⓔxample.com", "example.com"},
		{"soft hyphen ignored", "exa\u00admple.com", "example.com"},
		{"combining grapheme joiner ignored", "exa\u034fmple.com", "example.com"},
		{"capital I with dot", "İ.com", "xn--i-9bb.com"},
		{"nontransitional sharp s", "faß.de", "xn--fa-hia.de"},
		{"non-STD3 underscore", "foo_bar.example", "foo_bar.example"},
		{"hyphens not strictly checked", "-edge-.example", "-edge-.example"},
		{"IPv4-mapped IPv6", "::ffff:192.168.0.1", "[::ffff:c0a8:1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalWHATWGHostname(tc.input)
			if err != nil {
				t.Fatalf("canonicalWHATWGHostname(%q) failed: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("canonicalWHATWGHostname(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCanonicalWHATWGHostnameRejectsNode24InvalidVectors(t *testing.T) {
	cases := []string{
		"xn--",
		"xn--.com",
		"foo.xn--.com",
		"ｘｎ－－.com",
		"x\u00adn--.com",
		"xn-\u00ad-.com",
		"xn--a",
		"example／evil.com",
		"\u00ad",
		"\u034f",
		"a\u200cb.example",
		"09",
		"08",
		"1..2",
		"0xffffffffffffffffffffffffffff",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			if got, err := canonicalWHATWGHostname(input); err == nil {
				t.Errorf("canonicalWHATWGHostname(%q) = %q, want an error", input, got)
			}
		})
	}
}

func TestCanonicalWHATWGHostnameFailsClosedPastUnicode15Boundary(t *testing.T) {
	// Node v24.15.0 accepts these under Unicode 16. The pinned Unicode 15 path
	// intentionally refuses them until its IDNA table is reviewed and upgraded.
	for _, input := range []string{
		"ẞ.de",               // Mapping changed from ss to U+00DF.
		"\u1806.example",     // Status changed from disallowed to valid.
		"\u180e.example",     // Status changed from disallowed to ignored.
		"\u1c89.example",     // Added in Unicode 16.
		"\U000105c0.example", // Added in Unicode 16.
	} {
		if got, err := canonicalWHATWGHostname(input); err == nil {
			t.Errorf("post-Unicode-15 hostname %q = %q, want fail-closed error", input, got)
		}
	}
}

func TestCanonicalWHATWGHostnameFailsClosedOnNodeAdaBidiQuirk(t *testing.T) {
	// Node v24.15.0/Ada 3.4.4 serializes this as xn--a-0hc.com even
	// though the mixed-direction label fails the UTS-46 bidi rule. Keep the
	// validation rule instead of widening every bidi label to match one quirk.
	if got, err := canonicalWHATWGHostname("aא.com"); err == nil {
		t.Errorf("mixed-direction hostname = %q, want fail-closed error", got)
	}
}

func TestWHATWGURLFailsClosedOnNodeSpecialHostPunctuation(t *testing.T) {
	for _, tc := range []struct {
		input string
	}{
		{input: `http://exa"mple.com/path`},
		{input: "http://exa`mple.com/path"},
		{input: "http://exa{mple.com/path"},
		{input: "http://exa}mple.com/path"},
	} {
		if parsed, _, err := parseWHATWGURLReference(tc.input, nil); err == nil {
			t.Errorf("parseWHATWGURLReference(%q) = %v, want fail-closed error", tc.input, parsed)
		}
	}
}

func TestWHATWGSchemeRecognitionIsASCIIOnly(t *testing.T) {
	base, _, err := parseWHATWGURLReference("https://base.example/a/b", nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := parseWHATWGURLReference("Ł:next", base)
	if err != nil {
		t.Fatalf("parseWHATWGURLReference() failed: %v", err)
	}
	if got, want := whatwgURLString(parsed, true), "https://base.example/a/%C5%81:next"; got != want {
		t.Errorf("non-ASCII scheme candidate = %q, want %q", got, want)
	}
}

func TestWHATWGURLStringPreservesEncodedPercentAndRawPipe(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "https://api.example/a%25b", want: "https://api.example/a%25b"},
		{input: "https://api.example/a%2fb", want: "https://api.example/a%2fb"},
		{input: "https://api.example/a|b", want: "https://api.example/a|b"},
	} {
		parsed := mustURL(t, tc.input)
		if got := whatwgURLString(parsed, true); got != tc.want {
			t.Errorf("whatwgURLString(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
