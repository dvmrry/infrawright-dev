package assessment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// urlCategoriesSchemaTypes is the fixture the whole set-typed walk turns on.
//
// zia_url_categories really does carry db_categorized_urls as set(string) and
// urls as list(string) on one resource (packs/zia/schemas/provider/zia.json),
// which is what makes "an all-scalar array is a set" unusable as a heuristic:
// applied here it gets one of the two attributes wrong whichever way it
// guesses. Every test below exercises both attributes on the same record so a
// mechanism that cannot tell them apart fails rather than passing by luck.
func urlCategoriesSchemaTypes() PlanSchemaTypes {
	return PlanSchemaTypes{setAttributes: map[string]map[string]struct{}{
		"zia_url_categories": {"db_categorized_urls": {}},
	}}
}

func urlCategoriesSetAttributes() map[string]struct{} {
	return urlCategoriesSchemaTypes().SetAttributes("zia_url_categories")
}

// TestDiffPathsCollapsesOnlySchemaDeclaredSets pins that set-ness comes from
// the schema and nothing else: two attributes holding the same kind of values,
// changed the same way, on the same resource, are reported differently.
func TestDiffPathsCollapsesOnlySchemaDeclaredSets(t *testing.T) {
	before := mustParseDataJSON(t, `{
		"db_categorized_urls":["a.example","b.example","c.example"],
		"urls":["a.example","b.example","c.example"]
	}`)
	after := mustParseDataJSON(t, `{
		"db_categorized_urls":["a.example","b.example","c.example","d.example"],
		"urls":["a.example","b.example","c.example","d.example"]
	}`)
	paths := DiffPathsWithSets(before, after, urlCategoriesSetAttributes())
	want := []PlanPath{
		{"db_categorized_urls"},
		{"urls", 3},
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("DiffPathsWithSets(set and list side by side) = %#v, want %#v", paths, want)
	}
}

// TestDiffPathsSetInsertionDoesNotShiftEveryPosition is the reported defect.
// Inserting one member of a set moves every member ordered after it, and the
// positional walk calls each of those a change. The list beside it is left
// positional on purpose: for an ordered list those really are changes.
func TestDiffPathsSetInsertionDoesNotShiftEveryPosition(t *testing.T) {
	members := make([]string, 0, 400)
	for index := range 400 {
		members = append(members, `"m`+string(rune('a'+index%26))+string(rune('a'+index/26))+`"`)
	}
	beforeList := strings.Join(members, ",")
	// The new member sorts ahead of every existing one, so every position after
	// it shifts by one -- the shape a set insertion actually produces.
	afterList := `"aaa-first",` + beforeList
	before := mustParseDataJSON(t, `{"db_categorized_urls":[`+beforeList+`],"urls":[`+beforeList+`]}`)
	after := mustParseDataJSON(t, `{"db_categorized_urls":[`+afterList+`],"urls":[`+afterList+`]}`)

	positional := DiffPaths(before, after)
	if len(positional) < 700 {
		t.Fatalf("DiffPaths(shifted insertion) = %d paths, want the positional blow-up this test exists to remove", len(positional))
	}
	setAware := DiffPathsWithSets(before, after, urlCategoriesSetAttributes())
	setPaths := 0
	listPaths := 0
	for _, path := range setAware {
		switch path[0] {
		case "db_categorized_urls":
			setPaths++
		case "urls":
			listPaths++
		}
	}
	if setPaths != 1 {
		t.Errorf("DiffPathsWithSets(shifted insertion) set paths = %d, want exactly one naming the attribute", setPaths)
	}
	if listPaths != 401 {
		t.Errorf("DiffPathsWithSets(shifted insertion) list paths = %d, want every shifted position of the ordered list", listPaths)
	}
}

// TestDiffChangesCarriesSetMembershipDelta pins that the set change says what
// entered and left rather than pairing unrelated members by position, which is
// the claim the positional rendering was making that was not true.
func TestDiffChangesCarriesSetMembershipDelta(t *testing.T) {
	before := mustParseDataJSON(t, `{"db_categorized_urls":["keep.example","gone.example","also-gone.example"]}`)
	after := mustParseDataJSON(t, `{"db_categorized_urls":["keep.example","new-one.example","new-two.example"]}`)
	changes := DiffChangesWithSets(before, after, nil, nil, urlCategoriesSetAttributes())
	want := []PlanChange{{
		Path:    PlanPath{"db_categorized_urls"},
		Kind:    SetChange,
		Added:   []any{"new-one.example", "new-two.example"},
		Removed: []any{"gone.example", "also-gone.example"},
	}}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("DiffChangesWithSets(membership move) = %#v, want %#v", changes, want)
	}
}

// TestDiffChangesSetMultiplicityIsCounted pins that the delta respects
// multiplicity. A Terraform set cannot hold a duplicate, but this walk reads
// what the plan contains rather than what the schema promises, and collapsing
// repeats would report a real change as no change.
func TestDiffChangesSetMultiplicityIsCounted(t *testing.T) {
	// The duplicate is on the after side on purpose. With it on the before
	// side, a walk that lets one before member satisfy two after members still
	// produces the right answer, because the second match lands on a position
	// that was going to be reported anyway -- the fixture cannot observe the
	// difference. Here the second "a" has nothing left to pair with, so a walk
	// that reuses a matched member reports no addition at all.
	before := mustParseDataJSON(t, `{"db_categorized_urls":["a","b"]}`)
	after := mustParseDataJSON(t, `{"db_categorized_urls":["a","a"]}`)
	changes := DiffChangesWithSets(before, after, nil, nil, urlCategoriesSetAttributes())
	want := []PlanChange{{
		Path:    PlanPath{"db_categorized_urls"},
		Kind:    SetChange,
		Added:   []any{"a"},
		Removed: []any{"b"},
	}}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("DiffChangesWithSets(repeated member) = %#v, want %#v", changes, want)
	}
}

// TestDiffChangesSetEqualMembersReportNothing pins the one case where set
// awareness suppresses rather than collapses: two serializations with the same
// members are the same Terraform value.
func TestDiffChangesSetEqualMembersReportNothing(t *testing.T) {
	before := mustParseDataJSON(t, `{"db_categorized_urls":["a","b","c"],"urls":["a","b","c"]}`)
	after := mustParseDataJSON(t, `{"db_categorized_urls":["c","a","b"],"urls":["c","a","b"]}`)
	// Asserted exactly rather than "something was reported". A weaker check
	// passes even if the ordered list is itself wrongly collapsed to one path,
	// which is the regression most likely to follow from a change here.
	paths := DiffPathsWithSets(before, after, urlCategoriesSetAttributes())
	want := []PlanPath{{"urls", 0}, {"urls", 1}, {"urls", 2}}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("DiffPathsWithSets(reordered set beside reordered list) = %#v, want the set silent and every list position reported %#v",
			paths, want)
	}
}

// TestDiffChangesSetFallsBackWhenNotAnArray pins that an attribute the schema
// calls a set but the plan does not render as a collection is still reported.
// Nothing may be skipped for want of the expected shape.
//
// It also pins the scalar kind for these, which is the deliberate half of an
// asymmetry: null to populated reports scalar while [] to populated reports
// set. Terraform distinguishes an unset set from an empty one, and only the
// scalar form carries that -- it shows the null. A membership delta would
// render both transitions identically.
func TestDiffChangesSetFallsBackWhenNotAnArray(t *testing.T) {
	for _, test := range []struct{ name, before, after string }{
		{"created", `{"db_categorized_urls":null}`, `{"db_categorized_urls":["a"]}`},
		{"destroyed", `{"db_categorized_urls":["a"]}`, `{"db_categorized_urls":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			changes := DiffChangesWithSets(
				mustParseDataJSON(t, test.before),
				mustParseDataJSON(t, test.after),
				nil, nil,
				urlCategoriesSetAttributes(),
			)
			if len(changes) != 1 || changes[0].Kind != ScalarChange ||
				!reflect.DeepEqual(changes[0].Path, PlanPath{"db_categorized_urls"}) {
				t.Errorf("DiffChangesWithSets(%s) = %#v, want one scalar change naming the attribute", test.name, changes)
			}
		})
	}
}

// TestDiffChangesSetSensitivityWithholdsTheDelta pins that naming what entered
// a set is naming its contents, so a sensitivity mask anywhere under the
// attribute withholds the members while keeping the change visible.
func TestDiffChangesSetSensitivityWithholdsTheDelta(t *testing.T) {
	for _, test := range []struct{ name, mask string }{
		{"whole_attribute", `{"db_categorized_urls":true}`},
		{"one_member", `{"db_categorized_urls":[false,true]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			changes := DiffChangesWithSets(
				mustParseDataJSON(t, `{"db_categorized_urls":["a","b"]}`),
				mustParseDataJSON(t, `{"db_categorized_urls":["a","c"]}`),
				mustParseDataJSON(t, test.mask),
				nil,
				urlCategoriesSetAttributes(),
			)
			want := []PlanChange{{
				Path: PlanPath{"db_categorized_urls"}, Kind: SetChange, Sensitive: true,
			}}
			if !reflect.DeepEqual(changes, want) {
				t.Errorf("DiffChangesWithSets(%s sensitive) = %#v, want the path with the delta withheld", test.name, changes)
			}
		})
	}
}

// TestDiffChangesWithSetsPathsAgreeWithDiffPathsWithSets extends the invariant
// TestDiffChangesPathsAgreeWithDiffPaths pins, to the set-aware walks. Guidance
// is joined to findings by exact path equality, so the two walks disagreeing
// leaves a finding standing with its explanation silently detached.
func TestDiffChangesWithSetsPathsAgreeWithDiffPathsWithSets(t *testing.T) {
	tests := []struct{ name, before, after string }{
		{"set_addition", `{"db_categorized_urls":["a"]}`, `{"db_categorized_urls":["a","b"]}`},
		{"set_removal", `{"db_categorized_urls":["a","b"]}`, `{"db_categorized_urls":["a"]}`},
		{"set_reorder", `{"db_categorized_urls":["a","b"]}`, `{"db_categorized_urls":["b","a"]}`},
		{"set_replaced", `{"db_categorized_urls":["a"]}`, `{"db_categorized_urls":["b"]}`},
		{"set_became_null", `{"db_categorized_urls":["a"]}`, `{"db_categorized_urls":null}`},
		{"set_and_list", `{"db_categorized_urls":["a"],"urls":["a"]}`, `{"db_categorized_urls":["a","b"],"urls":["b","a"]}`},
		{"set_and_scalar", `{"db_categorized_urls":["a"],"n":1}`, `{"db_categorized_urls":[],"n":2}`},
		{"set_untouched", `{"db_categorized_urls":["a"],"n":1}`, `{"db_categorized_urls":["a"],"n":2}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := mustParseDataJSON(t, test.before)
			after := mustParseDataJSON(t, test.after)
			sets := urlCategoriesSetAttributes()
			paths := DiffPathsWithSets(before, after, sets)
			changes := DiffChangesWithSets(before, after, nil, nil, sets)
			if len(paths) != len(changes) {
				t.Fatalf("DiffPathsWithSets = %#v (%d), DiffChangesWithSets = %#v (%d), want the same walk",
					paths, len(paths), changes, len(changes))
			}
			for index, path := range paths {
				if !reflect.DeepEqual(path, changes[index].Path) {
					t.Errorf("path %d: DiffPathsWithSets = %#v, DiffChangesWithSets = %#v",
						index, path, changes[index].Path)
				}
			}
		})
	}
}

// TestZeroPlanSchemaTypesLeavesTheWalkPositional pins that the new field is
// opt-in. Every caller that supplies no schema must see exactly what it saw
// before schema types existed.
func TestZeroPlanSchemaTypesLeavesTheWalkPositional(t *testing.T) {
	before := mustParseDataJSON(t, `{"db_categorized_urls":["a","b"]}`)
	after := mustParseDataJSON(t, `{"db_categorized_urls":["b","a"]}`)
	if got, want := DiffPathsWithSets(before, after, PlanSchemaTypes{}.SetAttributes("zia_url_categories")),
		DiffPaths(before, after); !reflect.DeepEqual(got, want) {
		t.Errorf("DiffPathsWithSets(zero schema) = %#v, want DiffPaths = %#v", got, want)
	}
	if empty := (PlanSchemaTypes{}); !empty.Empty() {
		t.Error("PlanSchemaTypes{}.Empty() = false, want true")
	}
}

// TestUnknownResourceTypeStaysPositional pins the direction the walk fails when
// it has no schema evidence for a resource type: toward reporting too much,
// never toward assuming order is meaningless.
func TestUnknownResourceTypeStaysPositional(t *testing.T) {
	types := urlCategoriesSchemaTypes()
	if got := types.SetAttributes("zia_firewall_filtering_rule"); got != nil {
		t.Errorf("SetAttributes(resource type with no entry) = %#v, want nil", got)
	}
}

func setPlanJSON(source, before, after string) string {
	return `{"format_version":"1.2","complete":true,"errored":false,"` + source + `":[{
		"address":"zia_url_categories.this","type":"zia_url_categories",
		"change":{"actions":["update"],"before":` + before + `,"after":` + after + `}
	}]}`
}

// TestClassifyPlanSetMembershipIsOneBlockedPath walks the whole classifier for
// the reported case, in both plan sections. The two sections are pinned
// together deliberately: set-ness is a property of the attribute, not of which
// section reported it, and discriminating by source here would make the same
// resource classify two ways depending on which gate ran.
func TestClassifyPlanSetMembershipIsOneBlockedPath(t *testing.T) {
	for _, source := range []string{"resource_changes", "resource_drift"} {
		t.Run(source, func(t *testing.T) {
			planValue := mustParseDataJSON(t, setPlanJSON(
				source,
				`{"db_categorized_urls":["keep","gone"],"urls":["x","y"]}`,
				`{"db_categorized_urls":["keep","added"],"urls":["y","x"]}`,
			))
			classification, err := ClassifyPlanWithOptions(planValue, nil, nil, ClassifyPlanOptions{
				SchemaTypes: urlCategoriesSchemaTypes(),
			})
			if err != nil {
				t.Fatalf("ClassifyPlanWithOptions(%s) error = %v, want nil", source, err)
			}
			want := PlanClassification{
				Status: Blocked,
				Findings: []PlanFinding{{
					Status: Blocked, Source: source, Address: "zia_url_categories.this",
					Actions: []string{"update"},
					Paths: []PlanPath{
						{"db_categorized_urls"},
						{"urls", 0},
						{"urls", 1},
					},
					Changes: []PlanChange{
						{
							Path: PlanPath{"db_categorized_urls"}, Kind: SetChange,
							Added: []any{"added"}, Removed: []any{"gone"},
						},
						{Path: PlanPath{"urls", 0}, Kind: ScalarChange, Before: "x", After: "y"},
						{Path: PlanPath{"urls", 1}, Kind: ScalarChange, Before: "y", After: "x"},
					},
				}},
			}
			if !reflect.DeepEqual(classification, want) {
				t.Errorf("ClassifyPlanWithOptions(%s) = %#v, want %#v", source, classification, want)
			}
		})
	}
}

// TestClassifyPlanSetEqualityExplainsAnOtherwiseOpaqueUpdate covers the case
// that decides whether the collapse is usable at all. An update whose values
// diff to nothing normally means "something moved that this walk cannot see",
// and blocks. A set serialized two ways with the same members has not moved,
// and calling that opaque asserts ignorance where there is none.
func TestClassifyPlanSetEqualityExplainsAnOtherwiseOpaqueUpdate(t *testing.T) {
	for _, source := range []string{"resource_changes", "resource_drift"} {
		t.Run(source, func(t *testing.T) {
			planValue := mustParseDataJSON(t, setPlanJSON(
				source,
				`{"db_categorized_urls":["a","b","c"],"configured_name":"same"}`,
				`{"db_categorized_urls":["c","b","a"],"configured_name":"same"}`,
			))
			strict, err := ClassifyPlan(planValue, nil, nil)
			if err != nil {
				t.Fatalf("ClassifyPlan(%s reorder, no schema) error = %v, want nil", source, err)
			}
			if strict.Status != Blocked {
				t.Fatalf("ClassifyPlan(%s reorder, no schema).Status = %s, want blocked", source, strict.Status)
			}
			classification, err := ClassifyPlanWithOptions(planValue, nil, nil, ClassifyPlanOptions{
				SchemaTypes: urlCategoriesSchemaTypes(),
			})
			if err != nil {
				t.Fatalf("ClassifyPlanWithOptions(%s reorder) error = %v, want nil", source, err)
			}
			want := PlanClassification{
				Status: Clean,
				Findings: []PlanFinding{{
					Status: Clean, Source: source, Address: "zia_url_categories.this",
					Actions: []string{"update"}, Paths: []PlanPath{},
				}},
			}
			if !reflect.DeepEqual(classification, want) {
				t.Errorf("ClassifyPlanWithOptions(%s reorder) = %#v, want an examined-and-clear finding %#v",
					source, classification, want)
			}
		})
	}
}

// TestClassifyPlanKeepsOpaqueUpdateWithoutASetExplanation pins the fail-closed
// default the cleared case is carved out of. Only a record whose entire
// difference is accounted for by matching set members may be cleared.
func TestClassifyPlanKeepsOpaqueUpdateWithoutASetExplanation(t *testing.T) {
	// An update record whose before and after are identical carries no set
	// comparison at all, so nothing explains it and the marker stands. This is
	// the shape the clearing rule must not swallow.
	planValue := mustParseDataJSON(t, setPlanJSON(
		"resource_drift",
		`{"configured_name":"same"}`,
		`{"configured_name":"same"}`,
	))
	classification, err := ClassifyPlanWithOptions(planValue, nil, nil, ClassifyPlanOptions{
		SchemaTypes: urlCategoriesSchemaTypes(),
	})
	if err != nil {
		t.Fatalf("ClassifyPlanWithOptions(no visible difference) error = %v, want nil", err)
	}
	if classification.Status != Blocked ||
		!reflect.DeepEqual(classification.Findings[0].Paths, []PlanPath{{OpaqueUpdate}}) {
		t.Errorf("ClassifyPlanWithOptions(no visible difference) = %#v, want a blocked opaque update", classification)
	}
}

// TestClassifyPlanSuppressedSetReorderDoesNotClearTheRecord pins that
// suppressing a set reorder clears that attribute and nothing else. A record
// carrying a real change beside the reorder still blocks on the real change,
// and the set contributes no path either way.
func TestClassifyPlanSuppressedSetReorderDoesNotClearTheRecord(t *testing.T) {
	for _, test := range []struct {
		name, before, after string
		wantPaths           []PlanPath
	}{
		{
			"beside_a_changed_scalar",
			`{"db_categorized_urls":["a","b"],"configured_name":"old"}`,
			`{"db_categorized_urls":["b","a"],"configured_name":"new"}`,
			[]PlanPath{{"configured_name"}},
		},
		{
			"beside_a_reordered_ordered_list",
			`{"db_categorized_urls":["a","b"],"urls":["x","y"]}`,
			`{"db_categorized_urls":["b","a"],"urls":["y","x"]}`,
			[]PlanPath{{"urls", 0}, {"urls", 1}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			planValue := mustParseDataJSON(t, setPlanJSON("resource_drift", test.before, test.after))
			classification, err := ClassifyPlanWithOptions(planValue, nil, nil, ClassifyPlanOptions{
				SchemaTypes: urlCategoriesSchemaTypes(),
			})
			if err != nil {
				t.Fatalf("ClassifyPlanWithOptions(%s) error = %v, want nil", test.name, err)
			}
			if classification.Status != Blocked {
				t.Fatalf("ClassifyPlanWithOptions(%s).Status = %s, want blocked", test.name, classification.Status)
			}
			if got := classification.Findings[0].Paths; !reflect.DeepEqual(got, test.wantPaths) {
				t.Errorf("ClassifyPlanWithOptions(%s).Paths = %#v, want %#v", test.name, got, test.wantPaths)
			}
		})
	}
}

// TestClassifyPlanSetEqualityDoesNotClearNonValueDrift pins the boundary of
// the clearing rule from the other side. Identity metadata, the sensitivity
// mask, and unknown-until-apply values all contribute paths without
// contributing values, so a record can carry a set whose members match and
// still have drift the value walk never sees. Set equality explains the values
// only; it may not clear the record.
//
// The root-level unknown case is the one that needs saying out loud. When
// Terraform marks the entire post-apply value unknown, after_unknown is a bare
// true, TruthyPaths yields one zero-length path, and that path becomes the
// opaque marker rather than a named one -- so the record reaches the clearing
// rule looking exactly like a record whose values diffed to nothing. Nothing in
// before or after distinguishes it, which means explainedBySetEquality cannot;
// only the after_unknown guard can, and this is what proves the guard is doing
// that work.
func TestClassifyPlanSetEqualityDoesNotClearNonValueDrift(t *testing.T) {
	for _, test := range []struct {
		name, extra string
		wantPath    PlanPath
	}{
		{
			"identity_moved",
			`"before_identity":{"id":"old"},"after_identity":{"id":"new"},`,
			PlanPath{IdentityChange},
		},
		{
			"sensitivity_mask_moved",
			`"before_sensitive":{},"after_sensitive":{"configured_name":true},`,
			PlanPath{SensitivityChange},
		},
		{
			"whole_value_unknown_until_apply",
			`"after_unknown":true,`,
			PlanPath{OpaqueUpdate},
		},
		{
			"whole_set_unknown_until_apply",
			`"after_unknown":{"db_categorized_urls":true},`,
			PlanPath{"db_categorized_urls"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			planValue := mustParseDataJSON(t, `{
				"format_version":"1.2","complete":true,"errored":false,
				"resource_drift":[{
					"address":"zia_url_categories.this","type":"zia_url_categories",
					"change":{"actions":["update"],`+test.extra+`
						"before":{"db_categorized_urls":["a","b"]},
						"after":{"db_categorized_urls":["b","a"]}}
				}]
			}`)
			classification, err := ClassifyPlanWithOptions(planValue, nil, nil, ClassifyPlanOptions{
				SchemaTypes: urlCategoriesSchemaTypes(),
			})
			if err != nil {
				t.Fatalf("ClassifyPlanWithOptions(%s) error = %v, want nil", test.name, err)
			}
			want := []PlanPath{test.wantPath}
			if classification.Status != Blocked ||
				!reflect.DeepEqual(classification.Findings[0].Paths, want) {
				t.Errorf("ClassifyPlanWithOptions(%s) = %#v, want blocked on %#v",
					test.name, classification, want)
			}
		})
	}
}

// TestClassifyPlanReorderOfAnOrderedListStillBlocks pins that the suppression
// is scoped to what the schema calls a set. For list(string), a reorder is a
// real change and must survive.
func TestClassifyPlanReorderOfAnOrderedListStillBlocks(t *testing.T) {
	planValue := mustParseDataJSON(t, setPlanJSON(
		"resource_drift",
		`{"urls":["a","b"]}`,
		`{"urls":["b","a"]}`,
	))
	classification, err := ClassifyPlanWithOptions(planValue, nil, nil, ClassifyPlanOptions{
		SchemaTypes: urlCategoriesSchemaTypes(),
	})
	if err != nil {
		t.Fatalf("ClassifyPlanWithOptions(ordered list reorder) error = %v, want nil", err)
	}
	want := []PlanPath{{"urls", 0}, {"urls", 1}}
	if classification.Status != Blocked || !reflect.DeepEqual(classification.Findings[0].Paths, want) {
		t.Errorf("ClassifyPlanWithOptions(ordered list reorder) = %#v, want blocked on %#v", classification, want)
	}
}

// TestClassifyPlanUnknownSetMemberCollapsesToTheAttribute pins that
// after_unknown, which is walked separately from the values, produces the same
// spelling of the attribute. Two spellings of one attribute means only one of
// them can be named by a drift-policy entry.
//
// The first case makes before and after identical. That is what forces the
// assertion to be about after_unknown at all: with any difference between them
// the value walk supplies PlanPath{"db_categorized_urls"} on its own, the
// collapsed unknown path dedupes into it, and the test passes even if
// after_unknown is never read. The second case keeps the mixed shape, where
// what is being pinned is that the two sources agree rather than double-count.
func TestClassifyPlanUnknownSetMemberCollapsesToTheAttribute(t *testing.T) {
	for _, test := range []struct{ name, before, after, unknown string }{
		{
			"unknown_is_the_only_signal",
			`{"db_categorized_urls":["a","b"]}`,
			`{"db_categorized_urls":["a","b"]}`,
			`{"db_categorized_urls":[false,true]}`,
		},
		{
			"unknown_beside_a_membership_change",
			`{"db_categorized_urls":["a"]}`,
			`{"db_categorized_urls":["a",null]}`,
			`{"db_categorized_urls":[false,true]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			planValue := mustParseDataJSON(t, `{
				"format_version":"1.2","complete":true,"errored":false,
				"resource_drift":[{
					"address":"zia_url_categories.this","type":"zia_url_categories",
					"change":{"actions":["update"],
						"before":`+test.before+`,
						"after":`+test.after+`,
						"after_unknown":`+test.unknown+`}
				}]
			}`)
			classification, err := ClassifyPlanWithOptions(planValue, nil, nil, ClassifyPlanOptions{
				SchemaTypes: urlCategoriesSchemaTypes(),
			})
			if err != nil {
				t.Fatalf("ClassifyPlanWithOptions(%s) error = %v, want nil", test.name, err)
			}
			want := []PlanPath{{"db_categorized_urls"}}
			if got := classification.Findings[0].Paths; !reflect.DeepEqual(got, want) {
				t.Errorf("ClassifyPlanWithOptions(%s).Paths = %#v, want %#v", test.name, got, want)
			}
		})
	}
}

// TestSetPathChangesWhichDriftPolicyEntryMatches pins the behaviour change this
// carries for anyone who already wrote a policy: a set attribute is now named
// without an index, so the indexed and wildcard spellings that used to match it
// no longer do. Stated as a test rather than left to be discovered from a gate
// that stopped tolerating.
func TestSetPathChangesWhichDriftPolicyEntryMatches(t *testing.T) {
	planValue := mustParseDataJSON(t, setPlanJSON(
		"resource_changes",
		`{"db_categorized_urls":["a","b"]}`,
		`{"db_categorized_urls":["a","c"]}`,
	))
	for _, test := range []struct {
		name      string
		path      string
		tolerated bool
	}{
		{"attribute_name_now_matches", "db_categorized_urls", true},
		{"wildcard_element_no_longer_matches", "db_categorized_urls[]", false},
		{"indexed_element_no_longer_matches", "db_categorized_urls[1]", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policyValue := mustParseDataJSON(t, `{
				"version":1,"resource_types":{"zia_url_categories":{"plan_tolerate":[{
					"path":"`+test.path+`","reason":"test","approved_by":"unit"
				}]}}
			}`)
			classification, err := ClassifyPlanWithOptions(
				planValue, mustPolicy(t, policyValue), nil,
				ClassifyPlanOptions{SchemaTypes: urlCategoriesSchemaTypes()},
			)
			if err != nil {
				t.Fatalf("ClassifyPlanWithOptions(%s) error = %v, want nil", test.name, err)
			}
			want := Blocked
			if test.tolerated {
				want = Tolerated
			}
			if classification.Status != want {
				t.Errorf("ClassifyPlanWithOptions(policy path %q).Status = %s, want %s",
					test.path, classification.Status, want)
			}
		})
	}
}

// TestPlanGuidanceRecordsAgreeWithFindingPaths pins the join that step 5 of the
// issue warned about: guidance recomputes the walk independently, and if it
// collapses set attributes differently from the walk that produced the
// findings, every annotation detaches without a symptom.
func TestPlanGuidanceRecordsAgreeWithFindingPaths(t *testing.T) {
	planText := setPlanJSON(
		"resource_changes",
		`{"db_categorized_urls":["a","b"],"urls":["x"]}`,
		`{"db_categorized_urls":["a","c"],"urls":["y"]}`,
	)
	planObject := mustParseDataJSON(t, planText).(map[string]any)
	classification, err := ClassifyPlanWithOptions(planObject, nil, nil, ClassifyPlanOptions{
		SchemaTypes: urlCategoriesSchemaTypes(),
	})
	if err != nil {
		t.Fatalf("ClassifyPlanWithOptions(guidance agreement) error = %v, want nil", err)
	}
	candidates := planGuidanceRecords(planObject, "zia_url_categories", urlCategoriesSchemaTypes())
	guidancePaths := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		guidancePaths[pathMarker(candidate.path)] = struct{}{}
	}
	for _, path := range classification.Findings[0].Paths {
		if _, ok := guidancePaths[pathMarker(path)]; !ok {
			t.Errorf("finding path %#v has no guidance candidate; guidance would silently detach", path)
		}
	}
	if len(guidancePaths) != len(classification.Findings[0].Paths) {
		t.Errorf("guidance candidates = %d, finding paths = %d, want the same walk",
			len(guidancePaths), len(classification.Findings[0].Paths))
	}
}

// TestNewPlanSchemaTypesReadsTheInstalledPacks reads the real provider schemas
// rather than a fixture, because the whole change rests on the claim that the
// schema distinguishes these two attributes. A synthetic schema saying so
// proves only that the fixture was written to agree with the test.
func TestNewPlanSchemaTypesReadsTheInstalledPacks(t *testing.T) {
	root := installedAssessmentPack(t)
	if _, installed := root.Resources["zia_url_categories"]; !installed {
		t.Skip("zia pack is not installed in the selected profile")
	}
	types, err := NewPlanSchemaTypes(root)
	if err != nil {
		t.Fatalf("NewPlanSchemaTypes(installed packs) error = %v, want nil", err)
	}
	sets := types.SetAttributes("zia_url_categories")
	if _, ok := sets["db_categorized_urls"]; !ok {
		t.Errorf("SetAttributes(zia_url_categories) = %#v, want db_categorized_urls declared a set", sets)
	}
	if _, ok := sets["urls"]; ok {
		t.Error("SetAttributes(zia_url_categories) declares urls a set, but the provider declares it list(string)")
	}
	if types.Empty() {
		t.Error("NewPlanSchemaTypes(installed packs).Empty() = true, want the installed packs to declare sets")
	}
}

// TestRunnerRendersSetMembershipWithoutInventingAValuePair pins that the
// console does not print "null -> null" for a change that carries no value
// pair, and that the delta reads the same as the positional summary's.
func TestRunnerRendersSetMembershipWithoutInventingAValuePair(t *testing.T) {
	lines := runnerFindingLines(NormalizedAssessmentFinding{
		Paths: []string{"db_categorized_urls"},
		Changes: []NormalizedPlanChange{{
			Path:    "db_categorized_urls",
			Kind:    string(SetChange),
			Added:   []any{"new.example"},
			Removed: []any{"old.example", "older.example"},
		}},
	})
	want := []string{"db_categorized_urls: +1 (new.example), -2 (old.example, older.example)"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("runnerFindingLines(set change) = %#v, want %#v", lines, want)
	}
}

// TestRunnerRendersSensitiveSetMembership pins that a withheld delta does not
// render as an empty change.
func TestRunnerRendersSensitiveSetMembership(t *testing.T) {
	lines := runnerFindingLines(NormalizedAssessmentFinding{
		Paths: []string{"db_categorized_urls"},
		Changes: []NormalizedPlanChange{{
			Path: "db_categorized_urls", Kind: string(SetChange), Sensitive: true,
		}},
	})
	want := []string{"db_categorized_urls: (sensitive value changed)"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("runnerFindingLines(sensitive set change) = %#v, want %#v", lines, want)
	}
}

// TestPublishedReportNeverEmitsNullForRequiredChangeArrays pins the published
// contract for added and removed, which the schema marks required and typed
// array for every change, not only for a set change.
//
// The internal PlanChange leaves both nil for a scalar, and Go marshals a nil
// slice as null. Nothing in the type system stops that reaching the report; the
// only thing that does is the report writer copying through append([]any{},
// ...), which turns nil into an empty slice. That is one call site with no test
// behind it, so a later simplification to change.Added would emit null for
// every scalar change and break every conforming consumer, silently.
func TestPublishedReportNeverEmitsNullForRequiredChangeArrays(t *testing.T) {
	report := SavedPlanAssessmentReport{
		Kind: "infrawright.saved_plan_assessment", SchemaVersion: 2, Mode: AssertClean,
		Roots: []AssessmentReportRoot{{
			Tenant: "tenant", Label: "zia_url_categories",
			Findings: []NormalizedAssessmentFinding{{
				Status: "blocked", Source: "resource_changes",
				Address: "zia_url_categories.this",
				Paths:   []string{"configured_name", "db_categorized_urls"},
				Changes: []NormalizedPlanChange{
					{Path: "configured_name", Kind: string(ScalarChange), Before: "old", After: "new"},
					{
						Path: "db_categorized_urls", Kind: string(SetChange),
						Added: []any{"new.example"}, Removed: []any{"old.example"},
					},
				},
			}},
		}},
	}
	rendered, err := RenderAssessmentReport(report)
	if err != nil {
		t.Fatalf("RenderAssessmentReport(mixed kinds) error = %v, want nil", err)
	}
	for _, forbidden := range []string{`"added": null`, `"removed": null`, `"added":null`, `"removed":null`} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("RenderAssessmentReport emitted %s; the schema requires an array", forbidden)
		}
	}
	// Exactly one empty pair, from the scalar change; the set change's pair
	// carries its members. This also pins that the copy is not quietly
	// emptying a populated delta on its way out.
	if got := strings.Count(rendered, `"added": []`); got != 1 {
		t.Errorf("rendered %d empty added arrays, want exactly the scalar change's one:\n%s", got, rendered)
	}
	for _, member := range []string{"new.example", "old.example"} {
		if !strings.Contains(rendered, member) {
			t.Errorf("RenderAssessmentReport dropped set member %q:\n%s", member, rendered)
		}
	}
}

// schemaTypesPackRoot builds a one-resource pack root carrying the supplied
// resource schema, so a malformed schema can be put in front of
// NewPlanSchemaTypes without hand-building a LoadedPackRoot -- which would
// bypass the loader whose failures this is meant to exercise.
func schemaTypesPackRoot(t *testing.T, resourceSchema any) metadata.LoadedPackRoot {
	t.Helper()
	directory := t.TempDir()
	pack := filepath.Join(directory, "sample")
	write := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal(%q) error = %v, want nil", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v, want nil", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v, want nil", path, err)
		}
	}
	write(filepath.Join(pack, "pack.json"), metadata.JsonObject{
		"provider_prefixes": metadata.JsonObject{"sample_": "sample"},
		"provider_sources":  metadata.JsonObject{"sample": "example/sample"},
	})
	write(filepath.Join(pack, "registry.json"), metadata.JsonObject{
		"sample_resource": metadata.JsonObject{"generate": true, "product": "sample"},
	})
	schemas := metadata.JsonObject{}
	if resourceSchema != nil {
		schemas["sample_resource"] = resourceSchema
	}
	write(filepath.Join(pack, "schemas", "provider", "sample.json"), metadata.JsonObject{
		"resource_schemas": schemas,
	})
	profile := filepath.Join(directory, "profile.json")
	write(profile, metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1,
		"packs": []string{"sample"}, "shared": []string{},
	})
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: directory, ProfilePath: &profile,
	})
	if err != nil {
		t.Fatalf("metadata.LoadPackRoot(schema-types fixture) error = %v, want nil", err)
	}
	return root
}

// TestNewPlanSchemaTypesRefusesAnUnreadableSchema pins the failure half of the
// deliberate decision to error rather than fall back to positional comparison.
//
// A silent fallback would be invisible: the gate would keep running and simply
// stop collapsing sets, which looks exactly like a plan that has none. Since
// that stance is the reason two test fixtures had to grow provider schemas, the
// error paths deserve the same standard as the success path -- five of them
// existed with only the happy path covered.
func TestNewPlanSchemaTypesRefusesAnUnreadableSchema(t *testing.T) {
	for _, test := range []struct {
		name   string
		schema any
	}{
		{"resource type absent from the provider schema", nil},
		{"schema is not an object", "not-an-object"},
		{"block is missing", metadata.JsonObject{}},
		{"attributes is not an object", metadata.JsonObject{
			"block": metadata.JsonObject{"attributes": "not-an-object"},
		}},
		{"attribute is not an object", metadata.JsonObject{
			"block": metadata.JsonObject{"attributes": metadata.JsonObject{"name": "not-an-object"}},
		}},
		{"attribute type is unparseable", metadata.JsonObject{
			"block": metadata.JsonObject{"attributes": metadata.JsonObject{
				"name": metadata.JsonObject{"type": []any{"set"}},
			}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			types, err := NewPlanSchemaTypes(schemaTypesPackRoot(t, test.schema))
			if err == nil {
				t.Fatalf("NewPlanSchemaTypes(%s) error = nil, want a refusal; got %#v", test.name, types)
			}
			if !types.Empty() {
				t.Errorf("NewPlanSchemaTypes(%s) returned %#v alongside its error, want the zero value", test.name, types)
			}
		})
	}
}

// TestNewPlanSchemaTypesReadsAWellFormedSyntheticPack is the success half of
// the pair above, on the same fixture builder, so the refusals are known to
// come from the malformation rather than from the fixture never working.
func TestNewPlanSchemaTypesReadsAWellFormedSyntheticPack(t *testing.T) {
	root := schemaTypesPackRoot(t, metadata.JsonObject{
		"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"members": metadata.JsonObject{"type": []any{"set", "string"}},
			"ordered": metadata.JsonObject{"type": []any{"list", "string"}},
			"name":    metadata.JsonObject{"type": "string"},
		}},
	})
	types, err := NewPlanSchemaTypes(root)
	if err != nil {
		t.Fatalf("NewPlanSchemaTypes(well-formed synthetic pack) error = %v, want nil", err)
	}
	want := map[string]struct{}{"members": {}}
	if got := types.SetAttributes("sample_resource"); !reflect.DeepEqual(got, want) {
		t.Errorf("SetAttributes(sample_resource) = %#v, want only the set-typed attribute %#v", got, want)
	}
}
