package pyunicode

import "testing"

// TestPythonHTMLUnescapeGeneric pins the exact behavior of
// node-src/domain/python-html-unescape.ts's pythonHtmlUnescapeGeneric,
// probed directly against the compiled TS module during this port:
//
//	npx esbuild node-src/domain/python-html-unescape.ts --bundle \
//	  --format=esm --outfile=/tmp/probe/probe.mjs --platform=node
//	node -e '<vector battery, see below>'
//
// Every "probe:" case below reproduces one line of that battery's output
// verbatim. The combined case at the end reproduces the exact assertion in
// node-tests/transform-runtime-artifacts.test.ts's "generic Python HTML
// unescape covers named, prefix, numeric, invalid, and two-pass inputs".
func TestPythonHTMLUnescapeGeneric(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		// --- ported directly from node-tests/transform-runtime-artifacts.test.ts ---
		{
			name:  "node-tests: combined named/prefix/numeric/invalid vector",
			input: "&NotEqualTilde; &notit; &#x80; &#1; &#xD800; &#xFDD0;",
			want:  "≂̸ ¬it; €  � ",
		},
		{
			name:  "node-tests: single-pass double-escaped lt",
			input: "&amp;lt;",
			want:  "&lt;",
		},
		// deliberately not a table row: exercises unescaping twice, matching
		// the node-tests call `pythonHtmlUnescapeGeneric(pythonHtmlUnescapeGeneric("&amp;lt;"))`.

		// --- probe: named/legacy entity decoding (delegated to html.UnescapeString) ---
		{name: "probe: named amp", input: "&amp;", want: "&"},
		{name: "probe: legacy prefix match notit", input: "&notit;", want: "¬it;"},
		{name: "probe: complex named entity with combining mark", input: "&NotEqualTilde;", want: "≂̸"},
		{name: "probe: legacy entity without trailing semicolon", input: "&copy", want: "©"},
		{name: "probe: legacy entity uppercase with semicolon", input: "&COPY;", want: "©"},
		{name: "probe: unknown entity name is left untouched", input: "&unknown;", want: "&unknown;"},
		{name: "probe: bare ampersand followed by space never matches", input: "Tom & Jerry", want: "Tom & Jerry"},

		// --- probe: ordinary numeric character references ---
		{name: "probe: decimal numeric reference", input: "&#65;", want: "A"},
		{name: "probe: decimal numeric reference without semicolon", input: "&#65", want: "A"},
		{name: "probe: hex numeric reference lowercase x", input: "&#x2603;", want: "☃"},
		{name: "probe: hex numeric reference uppercase X", input: "&#X2603;", want: "☃"},

		// --- probe: WHATWG numeric replacement table (takes precedence over
		// Python's invalid-codepoint set) ---
		{name: "probe: WHATWG NUL replacement", input: "&#0;", want: "�"},
		{name: "probe: WHATWG CR passthrough", input: "&#13;", want: "\r"},
		{name: "probe: WHATWG C1 0x80 euro sign", input: "&#x80;", want: "€"},
		{name: "probe: WHATWG C1 0x9F (159) upper bound", input: "&#x9F;", want: "Ÿ"},

		// --- probe: Python invalid-codepoint classes (KNOWN DIVERGENCE from
		// Go's stdlib html.UnescapeString, which does not strip these; this
		// file never delegates to it for these codepoints, so the
		// divergence is inert here) ---
		{name: "probe: boundary 127 (DEL, invalid, not a WHATWG override)", input: "&#127;", want: ""},
		{name: "probe: boundary 160 (NBSP, valid, not a WHATWG override)", input: "&#160;", want: " "},
		{name: "probe: invalid codepoint 1 (low end of 1-8)", input: "&#1;", want: ""},
		{name: "probe: invalid codepoint 8 (high end of 1-8)", input: "&#8;", want: ""},
		{name: "probe: codepoint 9 (TAB) is valid, just outside 1-8", input: "&#9;", want: "\t"},
		{name: "probe: invalid codepoint 11 (VT)", input: "&#11;", want: ""},
		{name: "probe: invalid codepoint 14 (low end of 14-31)", input: "&#14;", want: ""},
		{name: "probe: invalid codepoint 31 (high end of 14-31)", input: "&#31;", want: ""},
		{name: "probe: codepoint 32 (space) is valid, just outside 14-31", input: "&#32;", want: " "},
		{name: "probe: invalid noncharacter 0xFDD0 (low end of range)", input: "&#xFDD0;", want: ""},
		{name: "probe: invalid noncharacter 0xFDEF (high end of range)", input: "&#xFDEF;", want: ""},
		{name: "probe: 0xFDCF is valid, just below the 0xFDD0-0xFDEF range", input: "&#xFDCF;", want: "﷏"},
		{name: "probe: 0xFDF0 is valid, just above the 0xFDD0-0xFDEF range", input: "&#xFDF0;", want: "ﷰ"},
		{name: "probe: plane-0 noncharacter 0xFFFE", input: "&#xFFFE;", want: ""},
		{name: "probe: plane-0 noncharacter 0xFFFF", input: "&#xFFFF;", want: ""},
		{name: "probe: plane-1 noncharacter 0x1FFFE (per-plane pattern)", input: "&#x1FFFE;", want: ""},
		{name: "probe: plane-16 noncharacter 0x10FFFF (max codepoint, also a noncharacter)", input: "&#x10FFFF;", want: ""},
		{name: "probe: 0xFFFD (replacement char itself) is valid, not a noncharacter", input: "&#xFFFD;", want: "�"},

		// --- probe: surrogate and out-of-range replacement ---
		{name: "probe: lone high surrogate D800", input: "&#xD800;", want: "�"},
		{name: "probe: lone low surrogate DFFF", input: "&#xDFFF;", want: "�"},
		{name: "probe: mid-range surrogate DC00", input: "&#xDC00;", want: "�"},
		{name: "probe: just beyond max codepoint 0x110000", input: "&#x110000;", want: "�"},
		{name: "probe: astronomically large numeric reference", input: "&#x99999999999999999999;", want: "�"},

		// --- probe: no-match / fast-path cases ---
		{name: "probe: no ampersand takes the fast path unchanged", input: "plain text", want: "plain text"},
		{name: "probe: empty string", input: "", want: ""},
		{name: "probe: trailing ampersand with nothing after it", input: "trailing&", want: "trailing&"},
		{name: "probe: hash with no digits never matches (# excluded from 3rd alt)", input: "&#;", want: "&#;"},
		{name: "probe: bare ampersand-semicolon never matches (; excluded from 3rd alt)", input: "&;", want: "&;"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PythonHTMLUnescapeGeneric(tc.input); got != tc.want {
				t.Errorf("PythonHTMLUnescapeGeneric(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}

	t.Run("node-tests: two-pass unescape fully resolves double-escaped lt", func(t *testing.T) {
		once := PythonHTMLUnescapeGeneric("&amp;lt;")
		twice := PythonHTMLUnescapeGeneric(once)
		if twice != "<" {
			t.Errorf("double-unescape = %q, want %q", twice, "<")
		}
	})
}
