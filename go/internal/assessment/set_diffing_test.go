package assessment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
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
			// The values are asserted, not just the shape. Checking only the
			// count, kind, and path leaves a fallback that emits an empty
			// scalar change indistinguishable from one that carries the
			// attribute -- null to ["a"] would render as null to null.
			want := []PlanChange{{
				Path:   PlanPath{"db_categorized_urls"},
				Kind:   ScalarChange,
				Before: mustParseDataJSON(t, test.before).(map[string]any)["db_categorized_urls"],
				After:  mustParseDataJSON(t, test.after).(map[string]any)["db_categorized_urls"],
			}}
			if !reflect.DeepEqual(changes, want) {
				t.Errorf("DiffChangesWithSets(%s) = %#v, want %#v", test.name, changes, want)
			}
		})
	}
}

// TestDiffChangesSetSensitivityWithholdsTheDelta pins that naming what entered
// a set is naming its contents, so a sensitivity mask anywhere under the
// attribute withholds the members while keeping the change visible.
//
// Each mask is exercised on both sides. Supplying only before_sensitive would
// leave after_sensitive unread by any test, and a member that is sensitive only
// after the change -- the newly written secret -- is exactly the one whose
// exposure matters most.
func TestDiffChangesSetSensitivityWithholdsTheDelta(t *testing.T) {
	for _, test := range []struct{ name, mask string }{
		{"whole_attribute", `{"db_categorized_urls":true}`},
		{"one_member", `{"db_categorized_urls":[false,true]}`},
	} {
		for _, side := range []string{"before", "after"} {
			t.Run(test.name+"_"+side, func(t *testing.T) {
				var beforeMask, afterMask any
				if side == "before" {
					beforeMask = mustParseDataJSON(t, test.mask)
				} else {
					afterMask = mustParseDataJSON(t, test.mask)
				}
				changes := DiffChangesWithSets(
					mustParseDataJSON(t, `{"db_categorized_urls":["a","old-secret"]}`),
					mustParseDataJSON(t, `{"db_categorized_urls":["a","new-secret"]}`),
					beforeMask,
					afterMask,
					urlCategoriesSetAttributes(),
				)
				want := []PlanChange{{
					Path: PlanPath{"db_categorized_urls"}, Kind: SetChange, Sensitive: true,
				}}
				if !reflect.DeepEqual(changes, want) {
					t.Errorf("DiffChangesWithSets(%s sensitive on %s) = %#v, want the path with the delta withheld",
						test.name, side, changes)
				}
			})
		}
	}
}

// TestDiffChangesSetMembersCompareExactly pins that membership is decided by
// exact Terraform-number equality, not by the walk's ordinary comparison.
//
// The ordinary comparison rounds a non-integer through binary64, so these two
// values collapse to the same float and a set whose membership really moved
// reads as unchanged. That answer reaches the branch that clears a record
// outright, which is why the comparison here has to be the exact one.
func TestDiffChangesSetMembersCompareExactly(t *testing.T) {
	before := mustParseDataJSON(t, `{"db_categorized_urls":[1, 9007199254740992.1]}`)
	after := mustParseDataJSON(t, `{"db_categorized_urls":[9007199254740992.2, 1]}`)
	changes := DiffChangesWithSets(before, after, nil, nil, urlCategoriesSetAttributes())
	if len(changes) != 1 || len(changes[0].Added) != 1 || len(changes[0].Removed) != 1 {
		t.Fatalf("DiffChangesWithSets(binary64-colliding members) = %#v, want one member in and one out", changes)
	}
}

// TestClassifyPlanExactNumbersCannotBeClearedByRounding is the same defect at
// the boundary that matters: a record whose only difference is two set members
// that collide under binary64 must not be reported clean.
func TestClassifyPlanExactNumbersCannotBeClearedByRounding(t *testing.T) {
	planValue := mustParseDataJSON(t, setPlanJSON(
		"resource_drift",
		`{"db_categorized_urls":[1, 9007199254740992.1]}`,
		`{"db_categorized_urls":[9007199254740992.2, 1]}`,
	))
	classification, err := ClassifyPlanWithOptions(planValue, nil, nil, ClassifyPlanOptions{
		SchemaTypes: urlCategoriesSchemaTypes(),
	})
	if err != nil {
		t.Fatalf("ClassifyPlanWithOptions(colliding numbers) error = %v, want nil", err)
	}
	if classification.Status != Blocked {
		t.Errorf("ClassifyPlanWithOptions(colliding numbers).Status = %s, want blocked; %#v",
			classification.Status, classification.Findings)
	}
}

// TestDiffChangesSetHandlesUncomparableMembers pins that membership works for a
// set of objects. Real packs carry them -- zia_sub_cloud.dcs is set(object) --
// and comparing members with Go's == panics on a map.
func TestDiffChangesSetHandlesUncomparableMembers(t *testing.T) {
	before := mustParseDataJSON(t, `{"db_categorized_urls":[{"id":1},{"id":2}]}`)
	after := mustParseDataJSON(t, `{"db_categorized_urls":[{"id":2},{"id":3}]}`)
	changes := DiffChangesWithSets(before, after, nil, nil, urlCategoriesSetAttributes())
	want := []PlanChange{{
		Path: PlanPath{"db_categorized_urls"}, Kind: SetChange,
		Added:   []any{map[string]any{"id": mustParseDataJSON(t, `3`)}},
		Removed: []any{map[string]any{"id": mustParseDataJSON(t, `1`)}},
	}}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("DiffChangesWithSets(set of objects) = %#v, want %#v", changes, want)
	}
}

// TestSetMembersAreNotBackedByThePlanDocument pins that a reported member
// cannot be rewritten by a later mutation of the plan it came from.
func TestSetMembersAreNotBackedByThePlanDocument(t *testing.T) {
	before := mustParseDataJSON(t, `{"db_categorized_urls":[]}`)
	after := mustParseDataJSON(t, `{"db_categorized_urls":[{"host":"new"}]}`)
	changes := DiffChangesWithSets(before, after, nil, nil, urlCategoriesSetAttributes())
	afterObject := after.(map[string]any)
	member := afterObject["db_categorized_urls"].([]any)[0].(map[string]any)
	member["host"] = "mutated-after-classification"
	got := changes[0].Added[0].(map[string]any)["host"]
	if got != "new" {
		t.Errorf("reported member changed to %#v when the plan was mutated, want the value observed at classification", got)
	}
}

// TestDiffChangesSetWillNotInventAMemberFromAnUnknown pins that an
// unknown-until-apply placeholder is never reported as a member that entered
// the set. A Terraform set cannot hold a null, so a null in the serialized
// array is a placeholder; counting it produces evidence the plan never carried.
func TestDiffChangesSetWillNotInventAMemberFromAnUnknown(t *testing.T) {
	changes := DiffChangesWithSets(
		mustParseDataJSON(t, `{"db_categorized_urls":["a"]}`),
		mustParseDataJSON(t, `{"db_categorized_urls":["a",null]}`),
		nil, nil,
		urlCategoriesSetAttributes(),
	)
	for _, change := range changes {
		for _, member := range append(append([]any{}, change.Added...), change.Removed...) {
			if member == nil {
				t.Errorf("DiffChangesWithSets(unknown member) reported %#v, want no null member", change)
			}
		}
	}
	// The attribute is still reported: equality cannot be proven either, so
	// staying silent would be the under-reporting half of the same mistake.
	paths := DiffPathsWithSets(
		mustParseDataJSON(t, `{"db_categorized_urls":["a"]}`),
		mustParseDataJSON(t, `{"db_categorized_urls":["a",null]}`),
		urlCategoriesSetAttributes(),
	)
	if want := []PlanPath{{"db_categorized_urls"}}; !reflect.DeepEqual(paths, want) {
		t.Errorf("DiffPathsWithSets(unknown member) = %#v, want %#v", paths, want)
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

// TestSetChangeSurvivesTheRealReportPath is the test whose absence let a set
// change be unpublishable while every other test passed.
//
// Two validators guard the report and only one of them is the JSON Schema file.
// The hand-written semantics validator runs on every successful report and
// carried its own copy of the kind enum, which still allowed "scalar" alone --
// so the feature's motivating output was rejected with INVALID_ASSESSMENT_REPORT
// at the point of publication. Rendering a hand-built report, which is what the
// other report test does, never reaches that validator. This one goes through
// BuildSavedPlanAssessmentReport, the path both assertion modes use.
func TestSetChangeSurvivesTheRealReportPath(t *testing.T) {
	core := SavedPlanAssessmentCore{
		Status: Blocked, Checked: 1, Blocked: 1,
		Roots: []AssessedSavedPlanRoot{{
			Tenant: "tenant", Label: "zia_url_categories",
			Members: []string{"zia_url_categories"}, Status: Blocked,
			Plan: AssessedPlanEvidence{
				SHA256:           strings.Repeat("b", 64),
				FormatVersion:    reportStringPointer("1.2"),
				TerraformVersion: reportStringPointer("1.15.4"),
			},
			PlanFingerprint: plan.PlanFingerprintV2{Version: 2, SHA256: strings.Repeat("c", 64)},
			Findings: []AssessmentFinding{{
				Status: Blocked, Source: "resource_changes",
				Address:      "zia_url_categories.this",
				ResourceType: reportStringPointer("zia_url_categories"),
				Actions:      []string{"update"},
				Paths:        []PlanPath{{"db_categorized_urls"}},
				Changes: []PlanChange{{
					Path: PlanPath{"db_categorized_urls"}, Kind: SetChange,
					Added: []any{"new.example"}, Removed: []any{"old.example"},
				}},
			}},
		}},
		StalePolicy: []metadata.StalePolicyEntry{},
	}
	report, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode:    AssertClean,
		Request: AssessmentReportRequest{Tenant: reportStringPointer("tenant")},
		Core:    core,
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentReport(set change) error = %v, want a publishable report", err)
	}
	rendered, err := RenderAssessmentReport(report)
	if err != nil {
		t.Fatalf("RenderAssessmentReport(set change) error = %v, want nil", err)
	}
	for _, required := range []string{`"kind": "set"`, "new.example", "old.example"} {
		if !strings.Contains(rendered, required) {
			t.Errorf("published report is missing %s:\n%s", required, rendered)
		}
	}
}

// TestPublishedKindEnumMatchesTheValidator pins the two copies of the kind enum
// against each other. They are in different files -- the published JSON Schema
// and the hand-written semantics validator -- and nothing but this connects
// them, which is how they drifted.
func TestPublishedKindEnumMatchesTheValidator(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "published-assessment-schema.json"))
	if err != nil {
		t.Fatalf("os.ReadFile(published schema) error = %v, want nil", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("json.Unmarshal(published schema) error = %v, want nil", err)
	}
	defs := document["$defs"].(map[string]any)
	change := defs["change"].(map[string]any)["properties"].(map[string]any)
	published := change["kind"].(map[string]any)["enum"].([]any)

	accepted := map[string]struct{}{}
	for _, kind := range published {
		text := kind.(string)
		validation := &assessmentValidation{}
		validateReportChange(
			map[string]any{
				"path": "p", "kind": text, "sensitive": false,
				"before": nil, "after": nil, "added": []any{}, "removed": []any{},
			},
			"/c", map[string]struct{}{"p": {}}, validation,
		)
		if len(validation.details) == 0 {
			accepted[text] = struct{}{}
		}
	}
	for _, kind := range published {
		if _, ok := accepted[kind.(string)]; !ok {
			t.Errorf("published schema allows kind %q but the semantics validator rejects it; "+
				"every report carrying that kind is unpublishable", kind)
		}
	}
}

// TestNewPlanSchemaTypesFailsDeterministically pins that a pack with more than
// one malformed resource type always reports the same one.
//
// The builder ranges resource types; Go randomises map iteration, so without an
// ordering the error message -- and the report digest that carries it -- differs
// between identical runs on identical input.
func TestNewPlanSchemaTypesFailsDeterministically(t *testing.T) {
	root := schemaTypesPackRoot(t, nil)
	root.Resources = map[string]metadata.LoadedResourceMetadata{}
	for _, resourceType := range []string{
		"sample_missing_alpha", "sample_missing_beta",
		"sample_missing_gamma", "sample_missing_delta",
	} {
		root.Resources[resourceType] = metadata.LoadedResourceMetadata{
			Type: resourceType, Product: "sample", Provider: "sample",
		}
	}
	first := ""
	for attempt := range 200 {
		_, err := NewPlanSchemaTypes(root)
		if err == nil {
			t.Fatalf("NewPlanSchemaTypes(several malformed types) error = nil on attempt %d, want a refusal", attempt)
		}
		if attempt == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("NewPlanSchemaTypes reported %q on attempt %d but %q on the first, want one deterministic failure",
				err.Error(), attempt, first)
		}
	}
}

// TestClearingRuleWillNotSkipALossilyEqualKey pins the per-key comparison
// inside the clearing rule, and the fixture that reaches it is narrow enough
// to deserve its shape spelled out.
//
// The rule is consulted only when the value walk found nothing. The walk
// compares set attributes by exact membership, so an exactly-differing set
// never gets here -- it produces a real path first. What can get here is a
// non-set attribute whose two values collapse to the same float64: the walk's
// ordinary comparison calls them equal and emits nothing. The clearing rule
// re-examines every key exactly, refuses to explain the record, and it stays
// blocked as an opaque update. A lossy comparison in that re-examination
// skips the key as equal, lets the honest set reorder beside it explain the
// record, and clears drift the walk never saw.
func TestClearingRuleWillNotSkipALossilyEqualKey(t *testing.T) {
	planValue := mustParseDataJSON(t, setPlanJSON(
		"resource_drift",
		`{"db_categorized_urls":["a","b"],"val":9007199254740992.1}`,
		`{"db_categorized_urls":["b","a"],"val":9007199254740992.2}`,
	))
	classification, err := ClassifyPlanWithOptions(planValue, nil, nil, ClassifyPlanOptions{
		SchemaTypes: urlCategoriesSchemaTypes(),
	})
	if err != nil {
		t.Fatalf("ClassifyPlanWithOptions(sub-float64 scalar beside honest reorder) error = %v, want nil", err)
	}
	if classification.Status != Blocked ||
		!reflect.DeepEqual(classification.Findings[0].Paths, []PlanPath{{OpaqueUpdate}}) {
		t.Errorf("ClassifyPlanWithOptions(sub-float64 scalar beside honest reorder) = %#v, "+
			"want blocked on the opaque marker: the walk cannot see this drift and the "+
			"clearing rule must not explain it away", classification)
	}
}

// The three tests below exist because a reviewer proved their absence: with
// every unit test above green, discarding the SchemaTypes wiring at the
// runner, at apply, and at guidance collection each survived the whole suite.
// Unit tests hand the classifier a schema directly, so they cannot notice a
// production entry point that never passes one. Each test here rides the real
// path from a pack root on disk to the observable outcome, and each is backed
// by a battery mutation that discards exactly one wiring site.

// TestRunSavedPlanAssertionCollapsesSetsFromTheInstalledSchema pins the
// assert-clean gate end to end: the provider schema on disk declares
// db_categorized_urls a set, the saved plan's only difference is a pure
// reorder of it, and the run must come back clean. If the runner stops
// handing schema types to the assessment, the reorder is positional drift
// and this run fails.
func TestRunSavedPlanAssertionCollapsesSetsFromTheInstalledSchema(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("saved-plan snapshot cleanup is deliberately fail-closed on this platform")
	}
	workspace := t.TempDir()
	resourceType := "zia_url_categories"
	envDir := filepath.Join(workspace, "envs", "tenant", resourceType)
	moduleDir := filepath.Join(workspace, "modules", resourceType)
	for _, directory := range []string{envDir, moduleDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v, want nil", directory, err)
		}
	}
	relativeModule, err := filepath.Rel(envDir, moduleDir)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error = %v, want nil", envDir, moduleDir, err)
	}
	writeAssessmentTransactionFile(t, filepath.Join(moduleDir, "main.tf"), []byte("# module\n"), 0o600)
	writeAssessmentTransactionFile(t, filepath.Join(envDir, "main.tf"), []byte(strings.Join([]string{
		`module "` + resourceType + `" {`,
		`  source = "` + filepath.ToSlash(relativeModule) + `"`,
		"  items = var." + resourceType + "_items",
		"}",
		"",
	}, "\n")), 0o600)
	writeAssessmentTransactionFile(t, filepath.Join(envDir, "tfplan"), []byte("opaque saved plan\n"), 0o600)
	fingerprint, err := plan.FingerprintPlanV2(plan.PlanFingerprintInput{
		EnvDir:      envDir,
		VarFiles:    []string{},
		MemberTypes: []string{resourceType},
	}, nil)
	if err != nil {
		t.Fatalf("plan.FingerprintPlanV2(set wiring slice) error = %v, want nil", err)
	}
	writeAssessmentTransactionFile(
		t,
		filepath.Join(envDir, "tfplan.sources"),
		[]byte(`{"version":2,"sha256":"`+fingerprint.SHA256+`"}`+"\n"),
		0o600,
	)
	planJSON := `{
		"format_version":"1.2","terraform_version":"1.15.4",
		"complete":true,"errored":false,
		"resource_changes":[],
		"resource_drift":[{
			"address":"` + resourceType + `.this","type":"` + resourceType + `",
			"change":{"actions":["update"],
				"before":{"db_categorized_urls":["a.example","b.example","c.example"]},
				"after":{"db_categorized_urls":["c.example","a.example","b.example"]}}
		}],
		"output_changes":{}
	}`
	executable := assessmentExecutable(t, workspace, "printf '%s' "+assessmentShellLiteral(planJSON))
	reportPath := "-"
	diagnostics := []string{}
	var stdout strings.Builder
	err = RunSavedPlanAssertion(RunSavedPlanAssertionOptions{
		Workspace: workspace,
		Mode:      AssertClean,
		Tenant:    runnerTestString("tenant"),
		Selectors: []string{resourceType},
		Inputs: &SavedPlanAssertionInputs{
			Deployment: deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
			Root:       loadedAssessmentPack(t),
		},
		TerraformExecutable: executable,
		ReportPath:          &reportPath,
		OnDiagnostic: func(message string) {
			diagnostics = append(diagnostics, message)
		},
		Stdout: func(text string) error {
			stdout.WriteString(text)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunSavedPlanAssertion(pure set reorder) error = %v, want a clean run", err)
	}
	if !reflect.DeepEqual(diagnostics, []string{
		"all 1 saved plan(s) clean (no-op/imports only)",
	}) {
		t.Errorf("RunSavedPlanAssertion(pure set reorder) diagnostics = %#v, want exactly the clean line", diagnostics)
	}
}

// TestApplyExactSavedPlansCollapsesSetsFromTheInstalledSchema pins the apply
// gate the same way. The apply and assertion gates read the same pack root; a
// plan the assertion cleared must not be refused at apply because only one of
// the two consulted the schema.
func TestApplyExactSavedPlansCollapsesSetsFromTheInstalledSchema(t *testing.T) {
	fixture := newExactApplyFixture(t)
	planValue := mustParseDataJSON(t, `{
		"format_version":"1.2","terraform_version":"1.15.4",
		"complete":true,"errored":false,
		"resource_changes":[],
		"resource_drift":[{
			"address":"zia_url_categories.this","type":"zia_url_categories",
			"change":{"actions":["update"],
				"before":{"db_categorized_urls":["a.example","b.example"]},
				"after":{"db_categorized_urls":["b.example","a.example"]}}
		}],
		"output_changes":{}
	}`)
	fake := &fakeExactPlanApplyTerraform{currentPlan: planValue}
	result, err := applyExactSavedPlans(exactApplyOptions(fixture, fake), exactApplyTestHooks(fixture))
	if err != nil {
		t.Fatalf("applyExactSavedPlans(pure set reorder) error = %v, want the plan applied", err)
	}
	if result.Applied != 1 || len(fake.applied) != 1 {
		t.Errorf("applyExactSavedPlans(pure set reorder) applied = %d/%d calls, want 1/1",
			result.Applied, len(fake.applied))
	}
}

// TestAssessmentGuidanceJoinsSetCollapsedFindings pins that guidance is
// collected under the same schema types the findings were classified under.
// joinBlockedGuidance requires exact path equality, so if the classifier
// names db_categorized_urls while guidance recomputes positional indexes, the
// annotation detaches with no error anywhere -- the failure this test exists
// to make loud.
func TestAssessmentGuidanceJoinsSetCollapsedFindings(t *testing.T) {
	fixture := newAssessmentTransactionFixture(t)
	planJSON := `{
		"format_version":"1.2","terraform_version":"1.15.4",
		"complete":true,"errored":false,
		"resource_changes":[{
			"address":"zpa_sample.this","type":"zpa_sample",
			"change":{"actions":["update"],
				"before":{"members":["a.example","b.example"]},
				"after":{"members":["a.example","c.example"]}}
		}],
		"output_changes":{}
	}`
	executable := assessmentExecutable(t, fixture.root, "printf '%s' "+assessmentShellLiteral(planJSON))
	pack := "sample"
	guidanceSource := NewAssessmentGuidanceSource(metadata.LoadedPackRoot{
		Packs: metadata.PackMetadata{
			Manifests: []metadata.PackManifest{{
				Name:             "sample",
				Directory:        "/packs/sample",
				Path:             "/packs/sample/pack.json",
				ProviderPrefixes: map[string]string{"zpa_": "zpa"},
				ProviderSources:  map[string]string{"zpa": "example/zpa"},
				Data: map[string]any{
					"provider_config": map[string]any{
						"requirements": []any{map[string]any{
							"id":         "zpa_member_management",
							"setting":    "manage_members",
							"value":      false,
							"reason":     "membership is provider-managed",
							"plan_paths": []any{"members"},
							"remediation": map[string]any{
								"kind":     "provider_argument",
								"mode":     "required_external",
								"evidence": "provider.md",
							},
						}},
					},
				},
			}},
			ProviderPrefixes: map[string]string{"zpa_": "zpa"},
			ProviderSources:  map[string]string{"zpa": "example/zpa"},
			ProviderOwners:   map[string]string{"zpa": "sample"},
		},
		Resources: map[string]metadata.LoadedResourceMetadata{
			"zpa_sample": {
				Type: "zpa_sample", Product: "zpa", Provider: "zpa",
				Pack: &pack, Registry: map[string]any{"generate": true},
			},
		},
	})
	outcome, err := AssessSavedPlansReport(AssessSavedPlansReportOptions{
		Assessment: SavedPlanAssessmentTransactionOptions{
			Assessment: assessmentOptions(fixture, executable, nil),
		},
		Mode:           AssertAdoptable,
		Request:        AssessmentReportRequest{Tenant: reportStringPointer("tenant")},
		GuidanceSource: &guidanceSource,
		SchemaTypes: PlanSchemaTypes{setAttributes: map[string]map[string]struct{}{
			"zpa_sample": {"members": {}},
		}},
	})
	if err != nil {
		t.Fatalf("AssessSavedPlansReport(set membership guidance) error = %v, want a report", err)
	}
	if len(outcome.Report.Roots) != 1 {
		t.Fatalf("AssessSavedPlansReport(set membership guidance) roots = %d, want 1", len(outcome.Report.Roots))
	}
	root := outcome.Report.Roots[0]
	joined := false
	for _, entry := range root.Guidance {
		if entry["lane"] == "provider_config" &&
			entry["matched_plan_path"] == "members" &&
			entry["finding_path"] == "members" {
			joined = true
		}
	}
	if !joined {
		t.Errorf("AssessSavedPlansReport(set membership guidance) guidance = %#v, "+
			"want a provider_config entry joined on the collapsed path \"members\"", root.Guidance)
	}
}
