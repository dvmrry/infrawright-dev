package sourceanalysis

import (
	"context"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

const shapeWitnessResource = "example_nested_rule"

func TestAnalyzeUnverifiedFieldWitnessesSeparatesEvidenceFamilies(t *testing.T) {
	report, err := AnalyzeUnverifiedFieldWitnesses(context.Background(), shapeWitnessInputs(t))
	if err != nil {
		t.Fatalf("AnalyzeUnverifiedFieldWitnesses(shapeWitnessInputs) error = %v, want nil", err)
	}
	resource := report.Resources[shapeWitnessResource]

	safe := requireFieldWitness(t, resource, "safe_targets")
	if safe.Disposition != FieldWitnessCorroborated {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(safe_targets).Disposition = %q, want corroborated", safe.Disposition)
	}
	if safe.Assessment.Declaration != FieldDeclarationConsistent ||
		safe.Assessment.Read != FieldReadShapeConsistent ||
		safe.Assessment.Write != FieldWriteObserved ||
		safe.Assessment.Acceptance != FieldAcceptanceSilent {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(safe_targets).Assessment = %#v, want consistent declaration/read, observed write, and silent acceptance", safe.Assessment)
	}
	if len(safe.ReadBacks) != 1 || safe.ReadBacks[0].ShapeAssessment == nil ||
		safe.ReadBacks[0].ShapeAssessment.Status != ReadBackShapeConsistent {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(safe_targets).ReadBacks = %#v, want one shape-consistent Read witness", safe.ReadBacks)
	}
	if len(safe.WriteInputs) == 0 {
		t.Error("AnalyzeUnverifiedFieldWitnesses(safe_targets).WriteInputs is empty, want call-bound ResourceData.Get witness")
	}

	incomplete := requireFieldWitness(t, resource, "incomplete_targets")
	if incomplete.Assessment.Read != FieldReadShapeUnresolved || incomplete.Disposition != FieldWitnessCorroborated {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(incomplete_targets) = disposition %q assessment %#v, want corroborated literal-key Read with unresolved shape", incomplete.Disposition, incomplete.Assessment)
	}
	if !hasFieldWitnessDiagnosticForPath(resource.Diagnostics, "provider_value_shape_unresolved", "incomplete_targets") {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(incomplete_targets) diagnostics = %#v, want explicit provider shape limitation", resource.Diagnostics)
	}

	external := requireFieldWitness(t, resource, "external_targets")
	if external.Assessment.Read != FieldReadShapeUnresolved || external.Disposition != FieldWitnessCorroborated {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(external_targets) = disposition %q assessment %#v, want visible unresolved external Read call", external.Disposition, external.Assessment)
	}

	coerced := requireFieldWitness(t, resource, "coerced_count")
	if coerced.Assessment.Read != FieldReadShapePartial {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(coerced_count).Assessment.Read = %q, want partial Plugin SDK scalar-coercion evidence", coerced.Assessment.Read)
	}
	coercedReview := requireFieldWitnessReviewItem(t, resource, "coerced_count")
	if !containsString(coercedReview.ReasonCodes, "read_back_shape_partial") {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(coerced_count) review = %#v, want partial-shape guidance", coercedReview)
	}
	if !hasFieldWitnessDiagnosticForPath(resource.Diagnostics, "read_back_shape_unresolved", "external_targets") {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(external_targets) diagnostics = %#v, want uncaptured call surfaced", resource.Diagnostics)
	}

	unsupported := requireFieldWitness(t, resource, "unsupported_targets")
	if unsupported.Assessment.Read != FieldReadShapeUnresolved || len(unsupported.Conflicts) != 0 {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(unsupported_targets) = assessment %#v conflicts %#v, want unsupported control flow to prevent a positive conflict", unsupported.Assessment, unsupported.Conflicts)
	}
	if !hasFieldWitnessDiagnosticForPath(resource.Diagnostics, "read_back_shape_unresolved", "unsupported_targets") {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(unsupported_targets) diagnostics = %#v, want unsupported statement surfaced", resource.Diagnostics)
	}

	for _, fieldPath := range []string{"mixed_targets", "appended_targets", "assigned_unknown_targets"} {
		witness := requireFieldWitness(t, resource, fieldPath)
		if witness.Assessment.Read != FieldReadShapeUnresolved {
			t.Errorf("AnalyzeUnverifiedFieldWitnesses(%s).Assessment.Read = %q, want unresolved mixed known/unknown shape", fieldPath, witness.Assessment.Read)
		}
		if !hasFieldWitnessDiagnosticForPath(resource.Diagnostics, "read_back_shape_unresolved", fieldPath) {
			t.Errorf("AnalyzeUnverifiedFieldWitnesses(%s) diagnostics = %#v, want unresolved shape diagnostic", fieldPath, resource.Diagnostics)
		}
	}

	nullable := requireFieldWitness(t, resource, "nullable_targets")
	if nullable.Assessment.Read != FieldReadShapeConsistent {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(nullable_targets).Assessment.Read = %q, want proven nil path kept distinct from arbitrary unknown", nullable.Assessment.Read)
	}

	unwired := requireFieldWitness(t, resource, "unwired_targets")
	if unwired.Disposition != FieldWitnessUntested {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(unwired_targets).Disposition = %q, want untested", unwired.Disposition)
	}
	if unwired.Assessment.Declaration != FieldDeclarationConsistent ||
		unwired.Assessment.Read != FieldReadAbsent || unwired.Assessment.Write != FieldWriteAbsent {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(unwired_targets).Assessment = %#v, want declaration-only evidence", unwired.Assessment)
	}
	review := requireFieldWitnessReviewItem(t, resource, "unwired_targets")
	if review.Priority != FieldWitnessReviewMedium ||
		!containsString(review.ReasonCodes, "read_back_absent") ||
		!containsString(review.ReasonCodes, "write_input_absent") {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(unwired_targets) review = %#v, want missing Read/write guidance", review)
	}
	if !hasFieldWitnessDiagnosticForPath(resource.Diagnostics, "write_helper_unresolved", "unwired_targets") {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(unwired_targets) diagnostics = %#v, want unsupported variadic write helper surfaced", resource.Diagnostics)
	}
}

func TestAnalyzeUnverifiedFieldWitnessesFindsGenericReadShapeConflict(t *testing.T) {
	report, err := AnalyzeUnverifiedFieldWitnesses(context.Background(), shapeWitnessInputs(t))
	if err != nil {
		t.Fatalf("AnalyzeUnverifiedFieldWitnesses(shapeWitnessInputs) error = %v, want nil", err)
	}
	resource := report.Resources[shapeWitnessResource]
	broken := requireFieldWitness(t, resource, "broken_targets")
	if broken.Disposition != FieldWitnessConflicting {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(broken_targets).Disposition = %q, want conflicting", broken.Disposition)
	}
	if broken.Assessment.Read != FieldReadShapeConflicting {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(broken_targets).Assessment.Read = %q, want shape_conflicting", broken.Assessment.Read)
	}
	if len(broken.ReadBacks) != 1 || broken.ReadBacks[0].ShapeAssessment == nil ||
		broken.ReadBacks[0].ShapeAssessment.Status != ReadBackShapeConflicting {
		t.Fatalf("AnalyzeUnverifiedFieldWitnesses(broken_targets).ReadBacks = %#v, want one shape-conflicting Read witness", broken.ReadBacks)
	}
	if !containsSubstring(broken.Conflicts, `undeclared object field "id"`) ||
		!containsSubstring(broken.Conflicts, `undeclared object field "name"`) {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(broken_targets).Conflicts = %#v, want generic unexpected-object-field conflicts", broken.Conflicts)
	}
	review := requireFieldWitnessReviewItem(t, resource, "broken_targets")
	if review.Priority != FieldWitnessReviewHigh || !containsString(review.ReasonCodes, "read_back_shape_mismatch") {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(broken_targets) review = %#v, want high shape-mismatch guidance", review)
	}
	if hasFieldWitnessDiagnostic(resource.Diagnostics, "provider_field_name_dynamic") {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses() diagnostics = %#v, want helper-bound nested field names resolved", resource.Diagnostics)
	}
	if _, ok := resource.Fields["broken_targets[].ids"]; !ok {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses() fields = %#v, want helper-bound nested ids field", resource.Fields)
	}
}

func TestCompareFieldValueShapesUsesPluginSDKAssignmentSemantics(t *testing.T) {
	tests := []struct {
		name         string
		expected     *FieldValueShape
		observed     *FieldValueShape
		wantPartial  bool
		wantConflict string
	}{
		{
			name: "known extra key in open map",
			expected: &FieldValueShape{
				Kind:   FieldValueShapeObject,
				Fields: map[string]*FieldValueShape{"ids": {Kind: FieldValueShapeSet, Element: &FieldValueShape{Kind: FieldValueShapeString}}},
				Closed: true,
			},
			observed: &FieldValueShape{
				Kind:   FieldValueShapeMap,
				Fields: map[string]*FieldValueShape{"name": {Kind: FieldValueShapeString}},
			},
			wantPartial:  true,
			wantConflict: `undeclared object field "name"`,
		},
		{
			name: "set accepts slice representation",
			expected: &FieldValueShape{
				Kind:     FieldValueShapeSet,
				Element:  &FieldValueShape{Kind: FieldValueShapeString},
				MaxItems: intPointer(1),
			},
			observed: &FieldValueShape{
				Kind:    FieldValueShapeList,
				Element: &FieldValueShape{Kind: FieldValueShapeString},
				Length:  intPointer(2),
			},
		},
		{
			name:        "primitive conversion remains partial",
			expected:    &FieldValueShape{Kind: FieldValueShapeInt},
			observed:    &FieldValueShape{Kind: FieldValueShapeString},
			wantPartial: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conflicts, partial := compareFieldValueShapes("targets", test.expected, test.observed)
			if partial != test.wantPartial {
				t.Errorf("compareFieldValueShapes() partial = %t, want %t", partial, test.wantPartial)
			}
			if test.wantConflict == "" && len(conflicts) != 0 {
				t.Errorf("compareFieldValueShapes() conflicts = %#v, want none", conflicts)
			}
			if test.wantConflict != "" && !containsSubstring(conflicts, test.wantConflict) {
				t.Errorf("compareFieldValueShapes() conflicts = %#v, want substring %q", conflicts, test.wantConflict)
			}
		})
	}
}

func TestFieldEvidenceFamilyCountKeepsAxesIndependent(t *testing.T) {
	witness := FieldWitness{Assessment: FieldWitnessAssessment{
		Declaration: FieldDeclarationConsistent,
		Read:        FieldReadShapeUnresolved,
		Write:       FieldWriteAbsent,
		Acceptance:  FieldAcceptanceConfiguredAndAsserted,
	}}
	if got := fieldEvidenceFamilyCount(witness); got != 3 {
		t.Errorf("fieldEvidenceFamilyCount() = %d, want declaration + Read + one acceptance family", got)
	}
}

func shapeWitnessInputs(t *testing.T) sourcebind.UnverifiedInputs {
	t.Helper()
	root := t.TempDir()
	files := []string{"main.go", "provider/provider.go", "provider/resource_nested_rule.go", "provider/schemautil/schema.go"}
	writeFieldWitnessFile(t, root, "main.go", `package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"example.invalid/terraform-provider-shapes/provider"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{ProviderFunc: provider.Provider})
}
`)
	writeFieldWitnessFile(t, root, "provider/provider.go", `package provider

import "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

func Provider() *schema.Provider {
	return &schema.Provider{ResourcesMap: map[string]*schema.Resource{
		"example_nested_rule": resourceNestedRule(),
	}}
}
`)
	writeFieldWitnessFile(t, root, "provider/resource_nested_rule.go", `package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"example.invalid/terraform-provider-shapes/provider/schemautil"
	"example.invalid/terraform-provider-shapes/unseen"
)

const nestedIDKey = "ids"

func resourceNestedRule() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceNestedRuleCreate,
		UpdateContext: resourceNestedRuleUpdate,
		ReadContext: resourceNestedRuleRead,
		Schema: map[string]*schema.Schema{
			"appended_targets": schemautil.NestedStringSetSchema(nestedIDKey),
			"assigned_unknown_targets": schemautil.NestedStringSetSchema(nestedIDKey),
			"broken_targets": schemautil.NestedStringSetSchema(nestedIDKey),
			"coerced_count": {Type: schema.TypeInt, Optional: true},
			"external_targets": {Type: schema.TypeString, Optional: true},
			"incomplete_targets": {Type: schema.TypeSet, Optional: true},
			"mixed_targets": schemautil.NestedStringSetSchema(nestedIDKey),
			"nullable_targets": schemautil.NestedStringSetSchema(nestedIDKey),
			"safe_targets": schemautil.NestedStringSetSchema(nestedIDKey),
			"unsupported_targets": schemautil.NestedStringSetSchema(nestedIDKey),
			"unwired_targets": schemautil.NestedStringSetSchema(nestedIDKey),
		},
	}
}

func resourceNestedRuleCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	_ = expandTargets(d, "broken_targets")
	_ = expandTargets(d, "safe_targets")
	_ = expandTargets(nil, "unwired_targets")
	expandTwoResourceData(d, nil, "safe_targets", "unwired_targets")
	variadicExpand(d, "unwired_targets")
	return resourceNestedRuleRead(ctx, d, meta)
}

func resourceNestedRuleUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	return resourceNestedRuleCreate(ctx, d, meta)
}

func expandTargets(d *schema.ResourceData, key string) []string {
	value, _ := d.Get(key).([]string)
	return value
}

func expandTwoResourceData(active, inactive *schema.ResourceData, activeKey, inactiveKey string) {
	_ = active.Get(activeKey)
	_ = inactive.Get(inactiveKey)
}

func variadicExpand(d *schema.ResourceData, keys ...string) {
	_ = d.Get(keys[0])
}

func resourceNestedRuleRead(_ context.Context, d *schema.ResourceData, _ any) diag.Diagnostics {
	var response struct {
		Broken []string
		Safe []string
	}
	_ = d.Set("broken_targets", flattenBrokenTargets(response.Broken))
	_ = d.Set("coerced_count", string(1))
	_ = d.Set("external_targets", unseen.Flatten(response.Safe))
	_ = d.Set("incomplete_targets", flattenSafeTargets(response.Safe))
	_ = d.Set("mixed_targets", flattenMixedTargets(response.Safe))
	_ = d.Set("nullable_targets", flattenNullableTargets(response.Safe))
	_ = d.Set("appended_targets", flattenAppendedTargets(response.Safe))
	_ = d.Set("assigned_unknown_targets", flattenAssignedUnknownTargets(response.Safe))
	_ = d.Set("safe_targets", flattenSafeTargets(response.Safe))
	_ = d.Set("unsupported_targets", flattenUnsupportedTargets(response.Safe))
	_ = d.Get("unwired_targets")
	return nil
}

func flattenBrokenTargets(values []string) []any {
	if len(values) == 0 {
		return []any{}
	}
	out := make([]any, len(values))
	for index, value := range values {
		out[index] = map[string]any{"id": value, "name": value}
	}
	return out
}

func flattenSafeTargets(_ []string) []any {
	return []any{map[string]any{"ids": []string{"one"}}}
}

func flattenMixedTargets(values []string) []any {
	if len(values) != 0 {
		return []any{map[string]any{"ids": []string{"one"}}}
	}
	return uncapturedMixedTargets(values)
}

func flattenAppendedTargets(values []string) []any {
	out := []any{map[string]any{"ids": []string{"one"}}}
	return append(out, uncapturedAppendedTargets(values)...)
}

func flattenNullableTargets(values []string) []any {
	if len(values) == 0 {
		return nil
	}
	return []any{map[string]any{"ids": []string{"one"}}}
}

func flattenAssignedUnknownTargets(values []string) []any {
	item := map[string]any{"ids": []string{"one"}}
	item["ids"] = unseen.Flatten(values)
	return []any{item}
}

func flattenUnsupportedTargets(_ []string) []any {
	defer func() {}()
	return []any{map[string]any{"bad": "one"}}
}
`)
	writeFieldWitnessFile(t, root, "provider/schemautil/schema.go", `package schemautil

import "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

const nestedIDKey = "wrong-package-key"

func NestedStringSetSchema(innerKey string) *schema.Schema {
	ids := &schema.Schema{Type: schema.TypeSet, Optional: true, Elem: &schema.Schema{Type: schema.TypeString}}
	return &schema.Schema{
		Type: schema.TypeSet,
		Optional: true,
		MaxItems: 1,
		Elem: &schema.Resource{Schema: map[string]*schema.Schema{innerKey: ids}},
	}
}
`)
	writeFieldWitnessFile(t, root, "provider-schema.json", shapeWitnessSchema)

	inputs, err := sourcebind.LoadUnverified(context.Background(), sourcebind.UnverifiedRoots{
		ProviderRoot:       root,
		ProviderModulePath: "example.invalid/terraform-provider-shapes",
		ProviderFiles:      files,
		SchemaRoot:         root,
		TerraformSchema:    "provider-schema.json",
		SDKRoots:           map[string]string{},
		SDKFiles:           map[string][]string{},
		SDKVersions:        map[string]string{},
		Selection: contracts.SelectionBinding{
			ResourceTypes: []string{shapeWitnessResource},
			Filters:       []contracts.SelectionFilterBinding{},
		},
	})
	if err != nil {
		t.Fatalf("sourcebind.LoadUnverified(shapeWitnessInputs) error = %v, want nil", err)
	}
	return inputs
}

func containsSubstring(values []string, target string) bool {
	for _, value := range values {
		if strings.Contains(value, target) {
			return true
		}
	}
	return false
}

func hasFieldWitnessDiagnosticForPath(values []FieldWitnessDiagnostic, code, fieldPath string) bool {
	for _, value := range values {
		if value.Code == code && value.FieldPath == fieldPath {
			return true
		}
	}
	return false
}

const shapeWitnessSchema = `{
  "format_version": "1.0",
  "provider_schemas": {
    "registry.terraform.io/example/shapes": {
      "resource_schemas": {
        "example_nested_rule": {
		  "block": {
			"attributes": {
			  "coerced_count": {"type": "number", "optional": true},
			  "external_targets": {"type": "string", "optional": true}
			},
            "block_types": {
              "appended_targets": {
                "nesting_mode": "set",
                "block": {"attributes": {"ids": {"type": ["set", "string"], "optional": true}}}
              },
              "assigned_unknown_targets": {
                "nesting_mode": "set",
                "block": {"attributes": {"ids": {"type": ["set", "string"], "optional": true}}}
              },
              "broken_targets": {
                "nesting_mode": "set",
                "block": {"attributes": {"ids": {"type": ["set", "string"], "optional": true}}}
              },
              "incomplete_targets": {
                "nesting_mode": "set",
                "block": {"attributes": {"ids": {"type": ["set", "string"], "optional": true}}}
              },
              "mixed_targets": {
                "nesting_mode": "set",
                "block": {"attributes": {"ids": {"type": ["set", "string"], "optional": true}}}
              },
              "nullable_targets": {
                "nesting_mode": "set",
                "block": {"attributes": {"ids": {"type": ["set", "string"], "optional": true}}}
              },
              "safe_targets": {
                "nesting_mode": "set",
                "block": {"attributes": {"ids": {"type": ["set", "string"], "optional": true}}}
              },
              "unsupported_targets": {
                "nesting_mode": "set",
                "block": {"attributes": {"ids": {"type": ["set", "string"], "optional": true}}}
              },
              "unwired_targets": {
                "nesting_mode": "set",
                "block": {"attributes": {"ids": {"type": ["set", "string"], "optional": true}}}
              }
            }
          }
        }
      }
    }
  }
}`
