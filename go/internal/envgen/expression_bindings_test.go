package envgen

// expression_bindings_test.go ports every test in
// the original test corpus verbatim (same scenarios, same
// literal fixtures, same expected values/messages), against this package's
// Go port in expression_bindings.go. No fixture files or external oracle
// are needed: every ported test builds its own in-memory JSON-shaped
// documents, exactly as the Node test does.

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// binding ports the `binding` test helper from
// the original test corpus.
func binding(expression string, sensitive *bool) map[string]any {
	entry := map[string]any{"expression": expression}
	if sensitive != nil {
		entry["sensitive"] = *sensitive
	}
	return map[string]any{
		"resources": map[string]any{
			"sample_resource.example": map[string]any{
				"nested.target": entry,
			},
		},
	}
}

// bindingAt ports the `bindingAt` test helper from
// the original test corpus.
func bindingAt(path string, expression string) map[string]any {
	if expression == "" {
		expression = "var.value"
	}
	return map[string]any{
		"resources": map[string]any{
			"sample_resource.example": map[string]any{
				path: map[string]any{"expression": expression},
			},
		},
	}
}

func mustParse(t *testing.T, data any, resourceType string) []ExpressionBinding {
	t.Helper()
	bindings, err := ParseExpressionBindings(data, resourceType)
	if err != nil {
		t.Fatalf("ParseExpressionBindings: %v", err)
	}
	return bindings
}

func boolPtr(b bool) *bool { return &b }

func TestBindingParsingNestedApplicationHclAndTerraformJson(t *testing.T) {
	parsed := mustParse(t, binding("var.client_secret", boolPtr(true)), "sample_resource")
	want := []ExpressionBinding{{
		Address:    "sample_resource.example",
		Key:        "example",
		Path:       "nested.target",
		PathParts:  []any{"nested", "target"},
		Expression: "var.client_secret",
		Sensitive:  true,
		Reason:     nil,
	}}
	if !reflect.DeepEqual(parsed, want) {
		t.Fatalf("parsed = %+v, want %+v", parsed, want)
	}

	variables := ExpressionVariables(parsed)
	if !reflect.DeepEqual(variables, map[string]bool{"client_secret": true}) {
		t.Fatalf("ExpressionVariables = %v", variables)
	}

	applied, err := ApplyExpressionBindings(map[string]any{
		"example": map[string]any{
			"nested": map[string]any{"literal": "unchanged", "target": "old"},
		},
	}, parsed)
	if err != nil {
		t.Fatalf("ApplyExpressionBindings: %v", err)
	}
	nested := applied["example"].(map[string]any)["nested"].(map[string]any)
	if nested["literal"] != "unchanged" {
		t.Fatalf("literal = %v", nested["literal"])
	}
	if _, ok := nested["target"].(*HclExpression); !ok {
		t.Fatalf("target is not *HclExpression: %#v", nested["target"])
	}
	rendered, err := RenderExpressionHclValue(nested, 0)
	if err != nil {
		t.Fatalf("RenderExpressionHclValue: %v", err)
	}
	if want := "{\n  literal = \"unchanged\"\n  target = var.client_secret\n}"; rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
	tfjson := ToTerraformJsonValue(nested)
	wantJSON := map[string]any{"literal": "unchanged", "target": "${var.client_secret}"}
	if !reflect.DeepEqual(tfjson, wantJSON) {
		t.Fatalf("ToTerraformJsonValue = %#v, want %#v", tfjson, wantJSON)
	}

	hcl, err := RenderExpressionBindingsHcl(parsed, RenderExpressionBindingsHclOptions{})
	if err != nil {
		t.Fatalf("RenderExpressionBindingsHcl: %v", err)
	}
	mustMatch(t, hcl, `variable "client_secret"`)
	mustMatch(t, hcl, `sensitive = true`)
	mustMatch(t, hcl, `target = var\.client_secret`)
	mustMatch(t, hcl, `try\(var\.items\["example"\]\.nested, null\)`)
}

func mustMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("expected %q to match /%s/", text, pattern)
	}
}

func mustNotMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("expected %q not to match /%s/", text, pattern)
	}
}

func TestAllowlistAcceptsSelectorsAndGeneratedLists(t *testing.T) {
	allowed := []string{
		"var.secret",
		"local.shared",
		`data.external.example.result["id"]`,
		`module.groups.items["one"].id`,
		"module.groups.items[0].id",
		`[module.groups.items["one"].id, "literal"]`,
		"[]",
	}
	for _, expression := range allowed {
		if _, err := ParseExpressionBindings(binding(expression, nil), "sample_resource"); err != nil {
			t.Errorf("expression %q: unexpected error: %v", expression, err)
		}
	}
	targets := ExpressionModuleTargets(`[module.groups.items["module.ignored"].id, "module.also_ignored"]`)
	if !reflect.DeepEqual(targets, []string{"groups"}) {
		t.Fatalf("ExpressionModuleTargets = %v", targets)
	}
}

func TestRemoteStateDiscoveryAcceptsOnlyExactCanonicalSelector(t *testing.T) {
	canonical := `data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]`
	refs, err := ExpressionRemoteStateReferences(canonical)
	if err != nil {
		t.Fatalf("ExpressionRemoteStateReferences: %v", err)
	}
	want := []RemoteStateReference{{Key: "segment_one", ResourceType: "zpa_segment_group", Root: "zpa_segment_group"}}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %+v, want %+v", refs, want)
	}
	refs2, err := ExpressionRemoteStateReferences("[" + canonical + `, "literal"]`)
	if err != nil {
		t.Fatalf("ExpressionRemoteStateReferences: %v", err)
	}
	if !reflect.DeepEqual(refs2, want) {
		t.Fatalf("refs2 = %+v, want %+v", refs2, want)
	}
	for _, expression := range []string{
		"data.terraform_remote_state.zpa_segment_group.outputs.other",
		canonical + ".bogus",
	} {
		_, err := ExpressionRemoteStateReferences(expression)
		if err == nil {
			t.Errorf("expression %q: expected error", expression)
			continue
		}
		if !regexp.MustCompile(`canonical|must end`).MatchString(err.Error()) {
			t.Errorf("expression %q: error %q does not match /canonical|must end/", expression, err.Error())
		}
	}
}

func TestMalformedExpressionsPathsAddressesFailClosed(t *testing.T) {
	expressions := []string{
		"aws_secret.value",
		"${var.secret}",
		`module.groups.items["${unsafe}"].id`,
		"module.groups.items[-1].id",
		"module.groups.items[1.2].id",
		"module.groups.items[01x].id",
		"var.secret\n",
		"[\uFEFF]",
		"",
	}
	for _, expression := range expressions {
		_, err := ParseExpressionBindings(binding(expression, nil), "sample_resource")
		if err == nil {
			t.Errorf("expression %q: expected error", expression)
			continue
		}
		if !strings.Contains(err.Error(), "expression") {
			t.Errorf("expression %q: error %q does not contain \"expression\"", expression, err.Error())
		}
	}

	if _, err := ParseExpressionBindings(map[string]any{"resources": nil}, "sample_resource"); err == nil || !strings.Contains(err.Error(), "resources must be an object") {
		t.Fatalf("resources:null error = %v", err)
	}

	_, err := ParseExpressionBindings(map[string]any{
		"resources": map[string]any{
			"sample_resource.example": map[string]any{
				"value": map[string]any{"expression": "var.x", "sensitive": nil},
			},
		},
	}, "sample_resource")
	if err == nil || !strings.Contains(err.Error(), "sensitive must be a boolean") {
		t.Fatalf("sensitive:null error = %v", err)
	}

	_, err = ParseExpressionBindings(map[string]any{
		"resources": map[string]any{
			"other.example": map[string]any{"value": map[string]any{"expression": "var.x"}},
		},
	}, "sample_resource")
	if err == nil || !strings.Contains(err.Error(), "address must be sample_resource.<key>") {
		t.Fatalf("wrong-prefix error = %v", err)
	}

	for _, path := range []string{"items[]", "items[*]", "items[-1]", "items[01]", `items["0"]`, "items[9007199254740992]", "items[0]id"} {
		_, err := ParseExpressionBindings(bindingAt(path, ""), "sample_resource")
		if err == nil {
			t.Errorf("path %q: expected error", path)
			continue
		}
		if !regexp.MustCompile(`selector|segment|safe integer`).MatchString(err.Error()) {
			t.Errorf("path %q: error %q does not match /selector|segment|safe integer/", path, err.Error())
		}
	}

	for _, extra := range []string{"value", "secret", "secret_value", "credential"} {
		_, err := ParseExpressionBindings(map[string]any{
			"resources": map[string]any{
				"sample_resource.example": map[string]any{
					"value": map[string]any{"expression": "var.x", extra: "leak"},
				},
			},
		}, "sample_resource")
		if err == nil || !strings.Contains(err.Error(), "unknown key") {
			t.Errorf("extra key %q: error = %v", extra, err)
		}
	}
}

func TestTerraformJsonConversionPreservesArbitrarySizeNumericScalars(t *testing.T) {
	converted := ToTerraformJsonValue(map[string]any{
		"decimal": json.Number("1.2500"),
		"integer": json.Number("900719925474099312345"),
		"nested":  []any{json.Number("2"), &HclExpression{Expression: "local.value"}},
	})
	rendered, err := canonjson.RenderLosslessArtifactJSON(converted)
	if err != nil {
		t.Fatalf("RenderLosslessArtifactJSON: %v", err)
	}
	want := strings.Join([]string{
		"{",
		`  "decimal": 1.25,`,
		`  "integer": 900719925474099312345,`,
		`  "nested": [`,
		"    2,",
		`    "${local.value}"`,
		"  ]",
		"}",
		"",
	}, "\n")
	if rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
}

func TestBindingPathValidationRejectsUnknownMissingConflicts(t *testing.T) {
	parsed := mustParse(t, binding("var.secret", nil), "sample_resource")
	if _, err := ApplyExpressionBindings(map[string]any{}, parsed); err == nil || !strings.Contains(err.Error(), "unknown resource address") {
		t.Fatalf("empty items error = %v", err)
	}
	if _, err := ApplyExpressionBindings(map[string]any{"example": map[string]any{}}, parsed); err == nil || !strings.Contains(err.Error(), "missing parent path") {
		t.Fatalf("missing parent error = %v", err)
	}
	if _, err := ApplyExpressionBindings(map[string]any{"example": map[string]any{"nested": map[string]any{}}}, parsed); err == nil || !strings.Contains(err.Error(), "missing target leaf") {
		t.Fatalf("missing leaf error = %v", err)
	}

	conflicts := mustParse(t, map[string]any{
		"resources": map[string]any{
			"sample_resource.example": map[string]any{
				"nested":        map[string]any{"expression": "var.parent"},
				"nested.target": map[string]any{"expression": "var.child"},
			},
		},
	}, "sample_resource")
	if _, err := RenderExpressionBindingsHcl(conflicts, RenderExpressionBindingsHclOptions{}); err == nil || !strings.Contains(err.Error(), "conflicting expression binding") {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestExactNumericListSelectorsPreserveSiblingsAndRenderListEdits(t *testing.T) {
	parsed := mustParse(t, bindingAt("nested[1].target", ""), "sample_resource")
	if !reflect.DeepEqual(parsed[0].PathParts, []any{"nested", 1, "target"}) {
		t.Fatalf("PathParts = %+v", parsed[0].PathParts)
	}
	if parsed[0].Path != "nested[1].target" {
		t.Fatalf("Path = %q", parsed[0].Path)
	}
	applied, err := ApplyExpressionBindings(map[string]any{
		"example": map[string]any{
			"nested": []any{
				map[string]any{"target": "first", "untouched": json.Number("1")},
				map[string]any{"target": "second", "untouched": json.Number("2")},
				map[string]any{"target": "third", "untouched": json.Number("3")},
			},
		},
	}, parsed)
	if err != nil {
		t.Fatalf("ApplyExpressionBindings: %v", err)
	}
	nestedArr := applied["example"].(map[string]any)["nested"].([]any)
	if got := nestedArr[0].(map[string]any)["target"]; got != "first" {
		t.Fatalf("nested[0].target = %v", got)
	}
	if got := nestedArr[2].(map[string]any)["target"]; got != "third" {
		t.Fatalf("nested[2].target = %v", got)
	}
	if _, ok := nestedArr[1].(map[string]any)["target"].(*HclExpression); !ok {
		t.Fatalf("nested[1].target is not *HclExpression: %#v", nestedArr[1])
	}
	if got := nestedArr[1].(map[string]any)["untouched"]; got != json.Number("2") {
		t.Fatalf("nested[1].untouched = %v", got)
	}

	rendered, err := RenderExpressionBindingsHcl(parsed, RenderExpressionBindingsHclOptions{})
	if err != nil {
		t.Fatalf("RenderExpressionBindingsHcl: %v", err)
	}
	mustMatch(t, rendered, `concat\(slice\(`)
	mustMatch(t, rendered, `nested\[1\]`)
	mustMatch(t, rendered, `target = var\.value`)

	if _, err := ApplyExpressionBindings(map[string]any{
		"example": map[string]any{"nested": []any{map[string]any{"target": "first"}}},
	}, parsed); err == nil || !strings.Contains(err.Error(), "out-of-range list index [1]") {
		t.Fatalf("out-of-range error = %v", err)
	}

	withoutIndex := mustParse(t, bindingAt("nested.target", ""), "sample_resource")
	if _, err := ApplyExpressionBindings(map[string]any{
		"example": map[string]any{"nested": []any{map[string]any{"target": "first"}}},
	}, withoutIndex); err == nil || !regexp.MustCompile(`traverses a list.*exact numeric list selector`).MatchString(err.Error()) {
		t.Fatalf("traverses-list error = %v", err)
	}

	indexOnObject := mustParse(t, bindingAt("nested[0].target", ""), "sample_resource")
	if _, err := ApplyExpressionBindings(map[string]any{
		"example": map[string]any{"nested": map[string]any{"target": "first"}},
	}, indexOnObject); err == nil || !regexp.MustCompile(`indexes a non-list`).MatchString(err.Error()) {
		t.Fatalf("indexes-non-list error = %v", err)
	}
}

func TestManyExactIndexEditsRenderLinearlyFromOneStableListBase(t *testing.T) {
	renderCount := func(count int) string {
		paths := map[string]any{}
		for index := 0; index < count; index++ {
			paths[fmtPath(index)] = map[string]any{"expression": "var.value"}
		}
		parsed := mustParse(t, map[string]any{
			"resources": map[string]any{"sample_resource.example": paths},
		}, "sample_resource")
		rendered, err := RenderExpressionBindingsHcl(parsed, RenderExpressionBindingsHclOptions{})
		if err != nil {
			t.Fatalf("RenderExpressionBindingsHcl: %v", err)
		}
		return rendered
	}
	fifty := renderCount(50)
	hundred := renderCount(100)
	if n := strings.Count(hundred, "concat("); n != 1 {
		t.Fatalf("concat( count = %d, want 1", n)
	}
	mustMatch(t, hundred, `var\.items\["example"\]\.nested\[99\]`)
	if !(len(hundred) < len(fifty)*21/10) {
		t.Fatalf("%d -> %d not linear enough", len(fifty), len(hundred))
	}
	if len(hundred) >= 100_000 {
		t.Fatalf("unexpected indexed binding output size: %d", len(hundred))
	}
}

func fmtPath(index int) string {
	return "nested[" + itoa(index) + "].target"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestProviderSchemaValidationDistinguishesListsFromSets(t *testing.T) {
	schema := metadata.JsonObject{
		"block": metadata.JsonObject{
			"attributes": metadata.JsonObject{},
			"block_types": metadata.JsonObject{
				"list_block": metadata.JsonObject{
					"nesting_mode": "list",
					"block": metadata.JsonObject{
						"attributes": metadata.JsonObject{
							"id": metadata.JsonObject{"optional": true, "type": "string"},
						},
					},
				},
				"set_block": metadata.JsonObject{
					"nesting_mode": "set",
					"block": metadata.JsonObject{
						"attributes": metadata.JsonObject{
							"id": metadata.JsonObject{"optional": true, "type": "string"},
						},
					},
				},
				"singleton_set": metadata.JsonObject{
					"nesting_mode": "set",
					"max_items":    json.Number("1"),
					"block": metadata.JsonObject{
						"attributes": metadata.JsonObject{
							"id": metadata.JsonObject{"optional": true, "type": []any{"set", "number"}},
						},
					},
				},
			},
		},
	}
	must := func(path string) []ExpressionBinding { return mustParse(t, bindingAt(path, ""), "sample_resource") }

	if err := ValidateExpressionBindingSchemaPaths(schema, "sample_resource", must("list_block[0].id")); err != nil {
		t.Fatalf("list_block[0].id: %v", err)
	}
	if err := ValidateExpressionBindingSchemaPaths(schema, "sample_resource", must("singleton_set.id")); err != nil {
		t.Fatalf("singleton_set.id: %v", err)
	}
	if err := ValidateExpressionBindingSchemaPaths(schema, "sample_resource", must("list_block.id")); err == nil || !regexp.MustCompile(`list block.*without an exact numeric selector`).MatchString(err.Error()) {
		t.Fatalf("list_block.id error = %v", err)
	}
	if err := ValidateExpressionBindingSchemaPaths(schema, "sample_resource", must("set_block[0].id")); err == nil || !regexp.MustCompile(`unordered set block`).MatchString(err.Error()) {
		t.Fatalf("set_block[0].id error = %v", err)
	}
}

func TestLayerMergeGeneratedFirstOperatorLastVariableSensitivityOr(t *testing.T) {
	generated := mustParse(t, binding(`module.other.items["generated"].id`, nil), "sample_resource")
	operator := mustParse(t, binding("var.operator", boolPtr(true)), "sample_resource")
	merged := MergeExpressionBindingLayers([][]ExpressionBinding{generated, operator})
	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d", len(merged))
	}
	if merged[0].Expression != "var.operator" {
		t.Fatalf("merged[0].Expression = %q", merged[0].Expression)
	}

	duplicateVariable := mustParse(t, map[string]any{
		"resources": map[string]any{
			"sample_resource.one": map[string]any{"value": map[string]any{"expression": "var.shared", "sensitive": false}},
			"sample_resource.two": map[string]any{"value": map[string]any{"expression": "var.shared", "sensitive": true}},
		},
	}, "sample_resource")
	variables := ExpressionVariables(duplicateVariable)
	if !reflect.DeepEqual(variables, map[string]bool{"shared": true}) {
		t.Fatalf("ExpressionVariables = %v", variables)
	}
}

func TestNativeHclRenderingRetainsLosslessNumericAndNonIdentifierKeys(t *testing.T) {
	rendered, err := RenderExpressionHclValue(json.Number("900719925474099312345"), 0)
	if err != nil {
		t.Fatalf("RenderExpressionHclValue: %v", err)
	}
	if rendered != "900719925474099312345" {
		t.Fatalf("rendered = %q", rendered)
	}
	rendered2, err := RenderExpressionHclValue(map[string]any{
		"not-an-ident": []any{&HclExpression{Expression: "local.value"}, "literal"},
	}, 0)
	if err != nil {
		t.Fatalf("RenderExpressionHclValue: %v", err)
	}
	want := "{\n  \"not-an-ident\" = [local.value, \"literal\"]\n}"
	if rendered2 != want {
		t.Fatalf("rendered2 = %q, want %q", rendered2, want)
	}
}
