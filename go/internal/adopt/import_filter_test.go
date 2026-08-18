package adopt

import (
	"errors"
	"reflect"
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

func renderedImports(t *testing.T, resourceType string, pairs ...tfrender.GeneratedImportPair) string {
	t.Helper()
	rendered, err := tfrender.RenderGeneratedImports(resourceType, pairs)
	if err != nil {
		t.Fatalf("tfrender.RenderGeneratedImports(%q, %v) error: %v", resourceType, pairs, err)
	}
	return rendered
}

func TestFilterGeneratedImportsKeepsOnlyUnmanagedPairs(t *testing.T) {
	dangerousKey := "line\nkey\ttail\\\" }"
	dangerous, err := tfrender.RenderGeneratedImports("zia_fake", []tfrender.GeneratedImportPair{{
		Key:      dangerousKey,
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
				Text:    renderedImports(t, "zia_fake", tfrender.GeneratedImportPair{Key: "needs_import", ImportID: "id-1"}),
				Kept:    1,
				Skipped: 1,
			},
		},
		{
			name:      "every_import_managed",
			text:      stagingImports(t, "zia_fake", "one", "two"),
			addresses: []string{stagingImportAddress(t, "zia_fake", "one"), stagingImportAddress(t, "zia_fake", "two")},
			want:      FilteredGeneratedImports{Text: "", Kept: 0, Skipped: 2},
		},
		{
			name: "nothing_managed_is_identity",
			text: stagingImports(t, "zia_fake", "one", "two"),
			want: FilteredGeneratedImports{
				Text: stagingImports(t, "zia_fake", "one", "two"),
				Kept: 2,
			},
		},
		{
			name:      "quoted_braces_managed",
			text:      dangerous,
			addresses: []string{stagingImportAddress(t, "zia_fake", dangerousKey)},
			want:      FilteredGeneratedImports{Text: "", Kept: 0, Skipped: 1},
		},
		{
			name: "quoted_braces_kept",
			text: dangerous,
			want: FilteredGeneratedImports{Text: dangerous, Kept: 1, Skipped: 0},
		},
		{
			name: "empty_file",
			want: FilteredGeneratedImports{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := FilterGeneratedImports("zia_fake", test.text, test.addresses)
			if err != nil {
				t.Fatalf("FilterGeneratedImports(%q, %v) error: %v", test.text, test.addresses, err)
			}
			if got != test.want {
				t.Errorf("FilterGeneratedImports(%q, %v) = %#v, want %#v", test.text, test.addresses, got, test.want)
			}
		})
	}
}

// TestFilterGeneratedImportsRoundTripsThroughParser pins the invariant #324
// made load-bearing: anything the filter writes to a staged imports file must
// parse back through tfrender.ParseGeneratedImports, because
// stagedImportTargets derives the imports-only -target set from the staged
// file. The pre-#325 splicing filter violated this exactly in the common
// steady-state -- at least one skipped block and at least one kept block in
// the same file left the skipped blocks' separators behind, and the strict
// parser refused the staged artifact at plan time (downstream hit this as one
// new rule among 137 managed).
func TestFilterGeneratedImportsRoundTripsThroughParser(t *testing.T) {
	const total = 6
	keys := make([]string, total)
	for index := range keys {
		keys[index] = "key-" + strconv.Itoa(index)
	}
	text := stagingImports(t, "zia_fake", keys...)
	for managedCount := 0; managedCount <= total; managedCount++ {
		addresses := make([]string, 0, managedCount)
		for _, key := range keys[:managedCount] {
			addresses = append(addresses, stagingImportAddress(t, "zia_fake", key))
		}
		filtered, err := FilterGeneratedImports("zia_fake", text, addresses)
		if err != nil {
			t.Fatalf("FilterGeneratedImports(%d managed) error: %v", managedCount, err)
		}
		if filtered.Kept != total-managedCount || filtered.Skipped != managedCount {
			t.Fatalf("FilterGeneratedImports(%d managed) kept/skipped = %d/%d, want %d/%d",
				managedCount, filtered.Kept, filtered.Skipped, total-managedCount, managedCount)
		}
		pairs, err := tfrender.ParseGeneratedImports("zia_fake", filtered.Text)
		if err != nil {
			t.Fatalf("ParseGeneratedImports(filtered, %d managed) error = %v, want round-trip", managedCount, err)
		}
		wantKeys := keys[managedCount:]
		gotKeys := make([]string, len(pairs))
		for index, pair := range pairs {
			gotKeys[index] = pair.Key
		}
		if len(wantKeys) == 0 {
			wantKeys = []string{}
		}
		if !reflect.DeepEqual(gotKeys, wantKeys) {
			t.Errorf("round-trip keys (%d managed) = %v, want %v", managedCount, gotKeys, wantKeys)
		}
	}
}

// TestFilterGeneratedImportsRefusesNonCanonicalInput pins fail-closed
// staging: an imports artifact that is not a complete canonical generated
// import file is refused when it is filtered, not passed through to fail at
// plan time. The splicing filter used to pass surrounding HCL and malformed
// text through unchanged.
func TestFilterGeneratedImportsRefusesNonCanonicalInput(t *testing.T) {
	managed := stagingImports(t, "zia_fake", "managed")
	texts := map[string]struct {
		text     string
		wantCode string
	}{
		"unterminated_block": {"import {\n  to = module.zia_fake.zia_fake.this[\"danger\"]\n  id = \"abc}def\"\n", "INVALID_GENERATED_IMPORTS"},
		"invalid_escape":     {"import {\n  to = module.zia_fake.zia_fake.this[\"danger\"]\n  id = \"bad\\u0020escape\"\n}\n", "INVALID_HCL_QUOTED_STRING"},
		"surrounding_hcl":    {"resource \"x\" \"y\" {\n  value = \"not an import } block\"\n}\n" + managed, "INVALID_GENERATED_IMPORTS"},
		"leading_whitespace": {" " + managed, "INVALID_GENERATED_IMPORTS"},
		"orphan_separator":   {"\n" + managed, "INVALID_GENERATED_IMPORTS"},
		"wrong_resource":     {stagingImports(t, "zpa_other", "key"), "INVALID_GENERATED_IMPORTS"},
	}
	for name, test := range texts {
		t.Run(name, func(t *testing.T) {
			_, err := FilterGeneratedImports("zia_fake", test.text, []string{stagingImportAddress(t, "zia_fake", "danger")})
			var failure *procerr.ProcessFailure
			if !errors.As(err, &failure) || failure.Code != test.wantCode {
				t.Errorf("FilterGeneratedImports(%q) error = %v, want %s", test.text, err, test.wantCode)
			}
		})
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
