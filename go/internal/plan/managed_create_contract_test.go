package plan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

func managedCreateShowCapture(t *testing.T, scenario string) map[string]any {
	t.Helper()
	fixtureDirectory := filepath.Join("testdata", "managed_create_capture", scenario)
	raw, err := os.ReadFile(filepath.Join(fixtureDirectory, "show.json"))
	if err != nil {
		t.Fatalf("ReadFile(%s/show.json) error = %v, want nil", fixtureDirectory, err)
	}
	showValue, err := canonjson.ParseDataJSONLosslessly(string(raw))
	if err != nil {
		t.Fatalf("ParseDataJSONLosslessly(%s/show.json) error = %v, want nil", fixtureDirectory, err)
	}
	show, ok := showValue.(map[string]any)
	if !ok {
		t.Fatalf("ParseDataJSONLosslessly(%s/show.json) = %T, want object", fixtureDirectory, showValue)
	}
	return show
}

func managedCreateContract() *AssessmentPlanContract {
	return &AssessmentPlanContract{ReferenceOutputTypes: []ReferenceOutputType{{
		Type: "terraform_data",
		Kind: ReferenceOutputKindManaged,
	}}}
}

func TestValidateAssessmentPlanAcceptsManagedCreateCaptures(t *testing.T) {
	for _, scenario := range []string{"initial_create", "mixed_create"} {
		t.Run(scenario, func(t *testing.T) {
			requireValidAssessmentPlan(
				t,
				"ValidateAssessmentPlan("+scenario+", managed contract)",
				managedCreateShowCapture(t, scenario),
				managedCreateContract(),
			)
		})
	}
}

func managedCreateOutputChange(t *testing.T, plan map[string]any) map[string]any {
	t.Helper()
	outputChanges := plan["output_changes"].(map[string]any)
	return outputChanges[infrawrightReferenceOutput].(map[string]any)
}

func managedCreatePlannedResource(t *testing.T, plan map[string]any, index string) map[string]any {
	t.Helper()
	plannedValues := plan["planned_values"].(map[string]any)
	rootModule := plannedValues["root_module"].(map[string]any)
	childModules := rootModule["child_modules"].([]any)
	child := childModules[0].(map[string]any)
	resources := child["resources"].([]any)
	for _, rawResource := range resources {
		resource := rawResource.(map[string]any)
		if resource["index"] == index {
			return resource
		}
	}
	t.Fatalf("managed create planned resource index = %q, want a matching resource", index)
	return nil
}

func managedCreateResourceChange(t *testing.T, plan map[string]any, index string) map[string]any {
	t.Helper()
	resourceChanges := plan["resource_changes"].([]any)
	for _, rawRecord := range resourceChanges {
		record := rawRecord.(map[string]any)
		if record["index"] == index {
			return record
		}
	}
	t.Fatalf("managed create resource change index = %q, want a matching change", index)
	return nil
}

func managedCreateUnknownMask(t *testing.T, plan map[string]any) map[string]any {
	t.Helper()
	return managedCreateOutputChange(t, plan)["after_unknown"].(map[string]any)["terraform_data"].(map[string]any)
}

func TestValidateAssessmentPlanRejectsUnbackedManagedCreateUnknowns(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "replacement",
			mutate: func(plan map[string]any) {
				record := managedCreateResourceChange(t, plan, "existing")
				record["change"].(map[string]any)["actions"] = []any{"delete", "create"}
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "import",
			mutate: func(plan map[string]any) {
				record := managedCreateResourceChange(t, plan, "existing")
				record["change"].(map[string]any)["importing"] = map[string]any{"id": "external-id"}
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "unrelated_create",
			mutate: func(plan map[string]any) {
				record := managedCreateResourceChange(t, plan, "existing")
				record["address"] = `module.other.terraform_data.this["existing"]`
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "non_exact_instance_address",
			mutate: func(plan map[string]any) {
				plannedResource := managedCreatePlannedResource(t, plan, "existing")
				plannedResource["address"] = plannedResource["address"].(string) + ".trailing"
				record := managedCreateResourceChange(t, plan, "existing")
				record["address"] = record["address"].(string) + ".trailing"
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "missing_attribute_unknown_marker",
			mutate: func(plan map[string]any) {
				record := managedCreateResourceChange(t, plan, "existing")
				record["change"].(map[string]any)["after_unknown"].(map[string]any)["id"] = false
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "non_null_before",
			mutate: func(plan map[string]any) {
				record := managedCreateResourceChange(t, plan, "existing")
				record["change"].(map[string]any)["before"] = map[string]any{"id": "prior"}
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "unbacked_output_key",
			mutate: func(plan map[string]any) {
				mask := managedCreateUnknownMask(t, plan)
				delete(mask, "existing")
				mask["ghost"] = true
			},
			want: "provider-observed resource IDs",
		},
		{
			name: "unknown_after_value",
			mutate: func(plan map[string]any) {
				after := managedCreateOutputChange(t, plan)["after"].(map[string]any)
				after["terraform_data"].(map[string]any)["existing"] = "forged"
			},
			want: "provider-observed resource IDs",
		},
		{
			name: "unknown_mask_missing",
			mutate: func(plan map[string]any) {
				managedCreateOutputChange(t, plan)["after_unknown"] = false
			},
			want: "provider-observed resource IDs",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := managedCreateShowCapture(t, "initial_create")
			test.mutate(plan)
			requireAssessmentPlanErrorContaining(t, plan, managedCreateContract(), test.want)
		})
	}
}

func TestValidateAssessmentPlanPreservesKnownManagedCreateEvidence(t *testing.T) {
	plan := managedCreateShowCapture(t, "mixed_create")
	after := managedCreateOutputChange(t, plan)["after"].(map[string]any)
	after["terraform_data"].(map[string]any)["existing"] = "wrong-id"
	requireAssessmentPlanErrorContaining(
		t,
		plan,
		managedCreateContract(),
		"provider-observed resource IDs",
	)
}

func TestValidateAssessmentPlanRejectsDuplicateUnknownManagedCreateInstances(t *testing.T) {
	plan := managedCreateShowCapture(t, "initial_create")
	rootModule := plan["planned_values"].(map[string]any)["root_module"].(map[string]any)
	child := rootModule["child_modules"].([]any)[0].(map[string]any)
	resources := child["resources"].([]any)
	child["resources"] = append(resources, cloneAssessmentValue(resources[0]))
	requireAssessmentPlanErrorContaining(
		t,
		plan,
		managedCreateContract(),
		"duplicate reference-output key",
	)
}

func TestValidateAssessmentPlanUnknownOnlyManagedCreateKeepsConfigurationAuthority(t *testing.T) {
	plan := managedCreateShowCapture(t, "initial_create")
	configuration := plan["configuration"].(map[string]any)
	rootModule := configuration["root_module"].(map[string]any)
	moduleCalls := rootModule["module_calls"].(map[string]any)
	delete(moduleCalls, "terraform_data")
	requireAssessmentPlanErrorContaining(
		t,
		plan,
		managedCreateContract(),
		"empty reference output authorization requires module.terraform_data",
	)
}

func TestValidateAssessmentPlanRejectsDottedUnknownMaskAlias(t *testing.T) {
	plan := managedCreateShowCapture(t, "mixed_create")
	mask := managedCreateOutputChange(t, plan)["after_unknown"].(map[string]any)
	delete(mask, "terraform_data")
	mask["terraform_data.new"] = true
	requireAssessmentPlanErrorContaining(
		t,
		plan,
		managedCreateContract(),
		"provider-observed resource IDs",
	)
}

func TestValidateAssessmentPlanAcceptsDottedManagedCreateInstanceKey(t *testing.T) {
	plan := managedCreateShowCapture(t, "initial_create")
	plannedResource := managedCreatePlannedResource(t, plan, "existing")
	plannedResource["index"] = "new.item"
	plannedResource["address"] = `module.terraform_data.terraform_data.this["new.item"]`
	resourceChange := managedCreateResourceChange(t, plan, "existing")
	resourceChange["index"] = "new.item"
	resourceChange["address"] = `module.terraform_data.terraform_data.this["new.item"]`
	mask := managedCreateUnknownMask(t, plan)
	delete(mask, "existing")
	mask["new.item"] = true
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(dotted managed create instance key)",
		plan,
		managedCreateContract(),
	)
}

func TestValidateAssessmentPlanRejectsDottedInstanceMaskAlias(t *testing.T) {
	plan := managedCreateShowCapture(t, "initial_create")
	plannedResource := managedCreatePlannedResource(t, plan, "existing")
	plannedResource["index"] = "new.item"
	plannedResource["address"] = `module.terraform_data.terraform_data.this["new.item"]`
	resourceChange := managedCreateResourceChange(t, plan, "existing")
	resourceChange["index"] = "new.item"
	resourceChange["address"] = `module.terraform_data.terraform_data.this["new.item"]`
	outputChange := managedCreateOutputChange(t, plan)
	outputChange["after_unknown"] = map[string]any{"terraform_data.new.item": true}
	requireAssessmentPlanErrorContaining(
		t,
		plan,
		managedCreateContract(),
		"provider-observed resource IDs",
	)
}

func TestValidateAssessmentPlanNoOpOutputRejectsUnknownMaskWithCreateElsewhere(t *testing.T) {
	plan := referenceAssessmentPlan("no-op")
	plan["resource_changes"] = []any{
		map[string]any{
			"address": `module.other.sample_resource.this["new"]`,
			"index":   "new",
			"mode":    "managed",
			"type":    "sample_resource",
			"change": map[string]any{
				"actions":       []any{"create"},
				"before":        nil,
				"after":         map[string]any{},
				"after_unknown": map[string]any{"id": true},
			},
		},
	}
	managedCreateOutputChange(t, plan)["after_unknown"] = map[string]any{
		"zpa_segment_group": map[string]any{"segment_one": true},
	}
	requireAssessmentPlanErrorContaining(
		t,
		plan,
		referenceContract(),
		"output no-op must not contain unknown values",
	)
}

func TestValidateAssessmentPlanAcceptsUnknownAlternateIDOnManagedCreate(t *testing.T) {
	plan := referenceAssessmentPlan("no-op")
	plan["configuration"] = emptyReferenceAssessmentPlan()["configuration"]
	resource := managedCreatePlannedResource(t, plan, "segment_one")
	delete(resource["values"].(map[string]any), "val")

	plan["resource_changes"] = []any{
		map[string]any{
			"address": `module.zpa_segment_group.zpa_segment_group.this["segment_one"]`,
			"index":   "segment_one",
			"mode":    "managed",
			"type":    "zpa_segment_group",
			"change": map[string]any{
				"actions":       []any{"create"},
				"before":        nil,
				"after":         map[string]any{"name": "Segment One"},
				"after_unknown": map[string]any{"val": true},
			},
		},
	}
	outputName := infrawrightReferenceOutput + "_val"
	plan["planned_values"].(map[string]any)["outputs"].(map[string]any)[outputName] = map[string]any{
		"sensitive": true,
	}
	plan["output_changes"].(map[string]any)[outputName] = map[string]any{
		"actions":          []any{"create"},
		"before":           nil,
		"after":            map[string]any{"zpa_segment_group": map[string]any{}},
		"after_unknown":    map[string]any{"zpa_segment_group": map[string]any{"segment_one": true}},
		"before_sensitive": false,
		"after_sensitive":  true,
	}
	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(unknown alternate ID)", plan, referenceContract())
}
