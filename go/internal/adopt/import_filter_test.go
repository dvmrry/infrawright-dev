package adopt

import (
	"errors"
	"strconv"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

const stagingTestResource = "zia_url_categories"

func stagingImportAddress(t *testing.T, resourceType, key string) string {
	t.Helper()
	quoted, err := tfrender.RenderHclQuotedString(key)
	if err != nil {
		t.Fatalf("tfrender.RenderHclQuotedString(%q) error: %v", key, err)
	}
	return "module." + resourceType + "." + resourceType + ".this[" + quoted + "]"
}

func stagingImports(t *testing.T, resourceType string, keys ...string) string {
	t.Helper()
	pairs := make([]tfrender.GeneratedImportPair, len(keys))
	for index, key := range keys {
		pairs[index] = tfrender.GeneratedImportPair{Key: key, ImportID: "id-" + strconv.Itoa(index)}
	}
	rendered, err := tfrender.RenderGeneratedImports(resourceType, pairs)
	if err != nil {
		t.Fatalf("tfrender.RenderGeneratedImports(%q, %v) error: %v", resourceType, keys, err)
	}
	return rendered
}

// TestFilterGeneratedImportsFrozenNodeVectors ports "generated import
// filtering matches frozen Python text contracts" from
// node-tests/import-staging.test.ts.
func TestFilterGeneratedImportsFrozenNodeVectors(t *testing.T) {
	managed := stagingImports(t, "zia_fake", "managed")
	kept := stagingImports(t, "zia_fake", "keep")
	dangerous, err := tfrender.RenderGeneratedImports("zia_fake", []tfrender.GeneratedImportPair{{
		Key:      "line\nkey\ttail\\\" }",
		ImportID: "abc}def\nwith\ttab\\tail",
	}})
	if err != nil {
		t.Fatalf("tfrender.RenderGeneratedImports(dangerous) error: %v", err)
	}

	tests := []struct {
		name      string
		text      string
		addresses []string
		want      FilteredGeneratedImports
	}{
		{
			name:      "one_managed_one_kept",
			text:      stagingImports(t, "zia_fake", "already_managed", "needs_import"),
			addresses: []string{stagingImportAddress(t, "zia_fake", "already_managed")},
			want: FilteredGeneratedImports{
				Text:    "\nimport {\n  to = module.zia_fake.zia_fake.this[\"needs_import\"]\n  id = \"id-1\"\n}\n",
				Kept:    1,
				Skipped: 1,
			},
		},
		{
			name:      "quoted_braces_managed",
			text:      dangerous,
			addresses: []string{stagingImportAddress(t, "zia_fake", "line\nkey\ttail\\\" }")},
			want:      FilteredGeneratedImports{Text: "", Kept: 0, Skipped: 1},
		},
		{
			name: "quoted_braces_kept",
			text: dangerous,
			want: FilteredGeneratedImports{Text: dangerous, Kept: 1, Skipped: 0},
		},
		{
			name: "mixed_hcl",
			text: "resource \"x\" \"y\" {\n  value = \"not an import } block\"\n}\n" + managed +
				"locals {\n  keep = true\n}\n" + kept + "# tail\n",
			addresses: []string{stagingImportAddress(t, "zia_fake", "managed")},
			want: FilteredGeneratedImports{
				Text: "resource \"x\" \"y\" {\n  value = \"not an import } block\"\n}\n" +
					"locals {\n  keep = true\n}\n" + kept + "# tail\n",
				Kept: 1, Skipped: 1,
			},
		},
		{
			name:      "ordinary_resource",
			text:      "resource \"x\" \"y\" {\n  value = \"abc}def\"\n}\n",
			addresses: []string{"resource.x.y"},
			want: FilteredGeneratedImports{
				Text: "resource \"x\" \"y\" {\n  value = \"abc}def\"\n}\n",
			},
		},
		{
			name:      "nonbreaking_whitespace_does_not_anchor",
			text:      "\u00a0" + managed + "\u3000",
			addresses: []string{stagingImportAddress(t, "zia_fake", "managed")},
			want:      FilteredGeneratedImports{Text: "\u00a0" + managed + "\u3000"},
		},
		{
			name:      "carriage_return_does_not_anchor",
			text:      "# not a Python line\r" + managed,
			addresses: []string{stagingImportAddress(t, "zia_fake", "managed")},
			want:      FilteredGeneratedImports{Text: "# not a Python line\r" + managed},
		},
		{
			name:      "line_separator_does_not_anchor",
			text:      "# not a Python line\u2028" + managed,
			addresses: []string{stagingImportAddress(t, "zia_fake", "managed")},
			want:      FilteredGeneratedImports{Text: "# not a Python line\u2028" + managed},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := FilterGeneratedImports(test.text, test.addresses)
			if err != nil {
				t.Fatalf("FilterGeneratedImports(%q, %v) error: %v", test.text, test.addresses, err)
			}
			if got != test.want {
				t.Errorf("FilterGeneratedImports(%q, %v) = %#v, want %#v", test.text, test.addresses, got, test.want)
			}
		})
	}
}

// TestFilterGeneratedImportsRejectsMalformedBlocks ports "generated import
// filtering rejects malformed and unterminated strings" from
// node-tests/import-staging.test.ts.
func TestFilterGeneratedImportsRejectsMalformedBlocks(t *testing.T) {
	texts := []string{
		"import {\n  to = module.zia_fake.zia_fake.this[\"danger\"]\n  id = \"abc}def\"\n",
		"import {\n  to = module.zia_fake.zia_fake.this[\"danger\"]\n  id = \"bad\\u0020escape\"\n}\n",
	}
	for _, text := range texts {
		_, err := FilterGeneratedImports(text, []string{stagingImportAddress(t, "zia_fake", "danger")})
		var failure *procerr.ProcessFailure
		if !errors.As(err, &failure) || failure.Code != "INVALID_GENERATED_IMPORT_BLOCK" {
			t.Errorf("FilterGeneratedImports(%q) error = %v, want INVALID_GENERATED_IMPORT_BLOCK", text, err)
		}
	}
}

func TestStateAddressesPreservesWideLineContract(t *testing.T) {
	stdout := "first\r\nsecond\vthird\ffourth\rfifth\x1csixth\x1dseventh\x1eeighth\u0085ninth\u2028tenth\u2029"
	want := []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth", "ninth", "tenth"}
	got := stateAddresses(stdout)
	if len(got) != len(want) {
		t.Fatalf("stateAddresses(%q) length = %d, want %d (%v)", stdout, len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("stateAddresses(%q)[%d] = %q, want %q", stdout, index, got[index], want[index])
		}
	}
	if got := stateAddresses("\n\n"); len(got) != 2 || got[0] != "" || got[1] != "" {
		t.Errorf("stateAddresses(two trailing separators) = %#v, want two empty addresses", got)
	}
}
