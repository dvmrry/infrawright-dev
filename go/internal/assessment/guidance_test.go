package assessment

import (
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func guidanceRoot(data map[string]any) metadata.LoadedPackRoot {
	pack := "sample"
	return metadata.LoadedPackRoot{
		Packs: metadata.PackMetadata{
			Manifests: []metadata.PackManifest{{
				Name:             "sample",
				Directory:        "/packs/sample",
				Path:             "/packs/sample/pack.json",
				Data:             data,
				ProviderPrefixes: map[string]string{"sample_": "sample"},
				ProviderSources:  map[string]string{"sample": "example/sample"},
			}},
			ProviderPrefixes: map[string]string{"sample_": "sample"},
			ProviderSources:  map[string]string{"sample": "example/sample"},
			ProviderOwners:   map[string]string{"sample": "sample"},
		},
		Resources: map[string]metadata.LoadedResourceMetadata{
			"sample_resource": {
				Type:     "sample_resource",
				Product:  "sample",
				Provider: "sample",
				Pack:     &pack,
				Registry: map[string]any{"generate": true},
			},
		},
	}
}

func guidanceRecords(records ...map[string]any) []any {
	result := make([]any, len(records))
	for index, record := range records {
		result[index] = record
	}
	return result
}

func guidancePlan() map[string]any {
	return map[string]any{
		"resource_changes": guidanceRecords(map[string]any{
			"address": `sample_resource.this["one"]`,
			"type":    "sample_resource",
			"change": map[string]any{
				"actions": []any{"update"},
				"before": map[string]any{
					"terraform_labels": map[string]any{},
					"rules":            []any{map[string]any{"id": json.Number("0")}},
					"settings":         []any{map[string]any{"mode": "old"}},
				},
				"after": map[string]any{
					"terraform_labels": map[string]any{"goog-terraform-provisioned": "true"},
					"rules":            []any{map[string]any{"id": json.Number("1")}},
					"settings":         []any{map[string]any{"mode": "new"}},
				},
			},
		}),
	}
}

func guidanceFindings() []PlanFinding {
	return []PlanFinding{{
		Status:  Blocked,
		Source:  "resource_changes",
		Address: `sample_resource.this["one"]`,
		Actions: []string{"update"},
		Paths: []PlanPath{
			{"rules", 0, "id"},
			{"settings", 0, "mode"},
			{"terraform_labels", "goog-terraform-provisioned"},
		},
	}}
}

func collectGuidance(data map[string]any) AssessmentGuidanceGroup {
	return CollectAssessmentGuidance(CollectAssessmentGuidanceOptions{
		Source:   NewAssessmentGuidanceSource(guidanceRoot(data)),
		Tenant:   "tenant",
		Label:    "sample_resource",
		Members:  []string{"sample_resource"},
		Plan:     guidancePlan(),
		Findings: guidanceFindings(),
	})
}

func TestCollectAssessmentGuidanceJoinsOriginalPackLanes(t *testing.T) {
	data := map[string]any{
		"provider_config": map[string]any{
			"requirements": guidanceRecords(map[string]any{
				"id":         "sample_attribution",
				"setting":    "add_attribution",
				"value":      false,
				"reason":     "provider default",
				"plan_paths": []any{"terraform_labels.goog-terraform-provisioned"},
				"remediation": map[string]any{
					"kind":     "provider_argument",
					"mode":     "required_external",
					"evidence": "provider.md",
				},
			}),
		},
		"absent_defaults": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id":             "sample_zero_id",
				"path":           "rules[].id",
				"kind":           "provider_absent_placeholder",
				"observed_value": json.Number("0"),
				"action":         "manual_review_required",
				"evidence":       "absent.md",
				"reason":         "provider placeholder",
				"resource_type":  "sample_resource",
			}),
		},
		"dynamic_schema": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id":                          "sample_dynamic_mode",
				"provider_version_constraint": "1.0.0",
				"path":                        "settings[].mode",
				"kind":                        "provider_observed_projection_unsafe",
				"ownership":                   "unknown",
				"action":                      "manual_review_required",
				"evidence":                    "dynamic.md",
				"reason":                      "dynamic field",
				"resource_type":               "sample_resource",
			}),
		},
	}
	got := collectGuidance(data)
	want := AssessmentGuidanceGroup{
		Tenant: "tenant",
		Label:  "sample_resource",
		Entries: []map[string]any{
			{
				"lane": "provider_config", "provider": "sample", "resource_type": "sample_resource",
				"address": `sample_resource.this["one"]`, "source": "resource_changes",
				"matched_plan_path": "terraform_labels.goog-terraform-provisioned",
				"finding_path":      "terraform_labels.goog-terraform-provisioned",
				"status_effect":     guidanceStatusEffect, "setting": "add_attribution",
				"expected_value": false, "mode": "required_external", "reason": "provider default",
				"evidence": "provider.md",
			},
			{
				"lane": "absent_default", "provider": "sample", "resource_type": "sample_resource",
				"address": `sample_resource.this["one"]`, "source": "resource_changes",
				"matched_plan_path": "rules[].id", "finding_path": "rules[0].id",
				"status_effect": guidanceStatusEffect, "rule": "sample_zero_id",
				"kind": "provider_absent_placeholder", "action": "manual_review_required",
				"observed_value": json.Number("0"), "reason": "provider placeholder", "evidence": "absent.md",
			},
			{
				"lane": "dynamic_schema", "provider": "sample", "resource_type": "sample_resource",
				"address": `sample_resource.this["one"]`, "source": "resource_changes",
				"matched_plan_path": "settings[].mode", "finding_path": "settings[0].mode",
				"status_effect": guidanceStatusEffect, "rule": "sample_dynamic_mode",
				"kind": "provider_observed_projection_unsafe", "ownership": "unknown",
				"action": "manual_review_required", "provider_version_constraint": "1.0.0",
				"reason": "dynamic field", "evidence": "dynamic.md",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CollectAssessmentGuidance(all lanes) = %#v, want %#v", got, want)
	}
}

func TestAbsentDefaultMatchingDistinguishesBooleansFromNumbers(t *testing.T) {
	data := map[string]any{
		"absent_defaults": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id": "sample_false_id", "path": "rules[].id",
				"kind": "provider_absent_placeholder", "observed_value": false,
				"action": "manual_review_required", "evidence": "absent.md",
				"reason": "wrong JSON type", "resource_type": "sample_resource",
			}),
		},
	}
	if got := collectGuidance(data).Entries; len(got) != 0 {
		t.Errorf("CollectAssessmentGuidance(observed false vs numeric zero).Entries = %#v, want []", got)
	}
}

func TestProviderGuidanceRetainsLosslessNumericDefault(t *testing.T) {
	data := map[string]any{
		"provider_config": map[string]any{
			"requirements": guidanceRecords(map[string]any{
				"id": "sample_numeric_default", "setting": "numeric_default",
				"value": json.Number("1.0"), "reason": "provider numeric default",
				"plan_paths": []any{"terraform_labels.goog-terraform-provisioned"},
				"remediation": map[string]any{
					"kind": "provider_argument", "mode": "renderable_default", "evidence": "provider.md",
					"safety": map[string]any{
						"non_sensitive": true, "not_tenant_specific": true, "not_destructive": true,
					},
				},
			}),
		},
	}
	entries := collectGuidance(data).Entries
	if len(entries) != 1 {
		t.Fatalf("CollectAssessmentGuidance(lossless 1.0).Entries = %#v, want one entry", entries)
	}
	if got, want := entries[0]["expected_value"], json.Number("1.0"); got != want {
		t.Errorf("CollectAssessmentGuidance(lossless 1.0).expected_value = %#v, want %#v", got, want)
	}
}

func TestMalformedLaneCannotSuppressAnotherLane(t *testing.T) {
	data := map[string]any{
		"provider_config": map[string]any{
			"requirements": guidanceRecords(map[string]any{
				"id": "malformed", "reason": "missing setting",
				"plan_paths":  []any{"terraform_labels.goog-terraform-provisioned"},
				"remediation": map[string]any{"mode": "required_external"},
			}),
		},
		"dynamic_schema": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id": "sample_dynamic_mode", "provider_version_constraint": "1.0.0",
				"path": "settings[].mode", "kind": "provider_observed_projection_unsafe",
				"ownership": "unknown", "action": "manual_review_required",
				"evidence": "dynamic.md", "reason": "dynamic field", "resource_type": "sample_resource",
			}),
		},
	}
	entries := collectGuidance(data).Entries
	if len(entries) != 1 || entries[0]["lane"] != "dynamic_schema" {
		t.Errorf("CollectAssessmentGuidance(malformed provider lane).Entries = %#v, want only dynamic_schema", entries)
	}
}

func TestUnmatchedDeepProviderValueCannotSuppressMatchedGuidance(t *testing.T) {
	var deeplyNested any = "unsafe-unmatched-leaf"
	for range 66 {
		deeplyNested = []any{deeplyNested}
	}
	data := map[string]any{
		"provider_config": map[string]any{
			"requirements": guidanceRecords(
				map[string]any{
					"id": "matched", "setting": "matched_setting", "value": false,
					"reason":     "matched provider setting",
					"plan_paths": []any{"terraform_labels.goog-terraform-provisioned"},
					"remediation": map[string]any{
						"kind": "provider_argument", "mode": "required_external", "evidence": "matched.md",
					},
				},
				map[string]any{
					"id": "unmatched_deep", "setting": "unmatched_setting", "value": deeplyNested,
					"reason": "unmatched provider setting", "plan_paths": []any{"settings[].missing"},
					"remediation": map[string]any{
						"kind": "provider_argument", "mode": "required_external", "evidence": "unmatched.md",
					},
				},
			),
		},
	}
	entries := collectGuidance(data).Entries
	if len(entries) != 1 || entries[0]["setting"] != "matched_setting" {
		t.Errorf("CollectAssessmentGuidance(unmatched deeply nested value).Entries = %#v, want matched_setting", entries)
	}
}

func TestProviderGuidanceClonesExpectedValuePerCandidate(t *testing.T) {
	data := map[string]any{
		"provider_config": map[string]any{
			"requirements": guidanceRecords(map[string]any{
				"id": "candidate_copies", "setting": "candidate_copies",
				"value":  map[string]any{"nested": []any{"original"}},
				"reason": "candidate copies", "plan_paths": []any{"settings[].mode"},
				"remediation": map[string]any{
					"kind": "provider_argument", "mode": "required_external", "evidence": "provider.md",
				},
			}),
		},
	}
	plan := map[string]any{
		"resource_changes": guidanceRecords(map[string]any{
			"address": `sample_resource.this["one"]`, "type": "sample_resource",
			"change": map[string]any{
				"actions": []any{"update"},
				"before": map[string]any{"settings": []any{
					map[string]any{"mode": "before-zero"}, map[string]any{"mode": "before-one"},
				}},
				"after": map[string]any{"settings": []any{
					map[string]any{"mode": "after-zero"}, map[string]any{"mode": "after-one"},
				}},
			},
		}),
	}
	entries, err := providerConfigGuidance(
		NewAssessmentGuidanceSource(guidanceRoot(data)),
		plan,
		"sample_resource",
	)
	if err != nil {
		t.Fatalf("providerConfigGuidance(two candidates) error = %v, want nil", err)
	}
	if len(entries) != 2 {
		t.Fatalf("providerConfigGuidance(two candidates) entries = %#v, want two", entries)
	}
	first := entries[0]["expected_value"].(map[string]any)["nested"].([]any)
	second := entries[1]["expected_value"].(map[string]any)["nested"].([]any)
	first[0] = "mutated"
	if got, want := second[0], any("original"); got != want {
		t.Errorf("providerConfigGuidance(two candidates) second expected value after first mutation = %#v, want %#v", got, want)
	}
}

func TestLaneValidationRejectsSemanticallyInvalidEvidence(t *testing.T) {
	base := func(id string) map[string]any {
		return map[string]any{
			"id": id, "provider_version_constraint": "1.0.0", "path": "settings[].mode",
			"kind": "provider_observed_projection_unsafe", "ownership": "unknown",
			"action": "manual_review_required", "evidence": "dynamic.md",
			"reason": "invalid", "resource_type": "sample_resource",
		}
	}
	badMatrix := base("bad_matrix")
	badMatrix["kind"] = "raw_api_only_provider_blind"
	badMatrix["ownership"] = "user_owned"
	badUnknown := base("bad_unknown")
	badUnknown["invented"] = true
	bareWildcard := base("bare_wildcard")
	bareWildcard["path"] = "settings.*.mode"
	duplicateA := base("duplicate")
	duplicateB := base("duplicate_again")
	typeScope := base("type_scope")
	prefixScope := base("prefix_scope")
	delete(prefixScope, "resource_type")
	prefixScope["resource_prefix"] = "sample_"
	tests := []struct {
		name  string
		rules []any
	}{
		{name: "kind_ownership_matrix", rules: guidanceRecords(badMatrix)},
		{name: "unknown_key", rules: guidanceRecords(badUnknown)},
		{name: "bare_wildcard", rules: guidanceRecords(bareWildcard)},
		{name: "duplicate_identity", rules: guidanceRecords(duplicateA, duplicateB)},
		{name: "overlapping_scopes", rules: guidanceRecords(typeScope, prefixScope)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := map[string]any{"dynamic_schema": map[string]any{"rules": test.rules}}
			if got := collectGuidance(data).Entries; len(got) != 0 {
				t.Errorf("CollectAssessmentGuidance(%s invalid lane).Entries = %#v, want []", test.name, got)
			}
		})
	}
}

func TestMalformedOtherProviderDoesNotSuppressValidLane(t *testing.T) {
	valid := guidanceRoot(map[string]any{
		"dynamic_schema": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id": "sample_dynamic_mode", "provider_version_constraint": "1.0.0",
				"path": "settings[].mode", "kind": "provider_observed_projection_unsafe",
				"ownership": "unknown", "action": "manual_review_required",
				"evidence": "dynamic.md", "reason": "dynamic field", "resource_type": "sample_resource",
			}),
		},
	})
	valid.Packs.ProviderPrefixes["other_"] = "other"
	valid.Packs.Manifests = append(valid.Packs.Manifests, metadata.PackManifest{
		Name: "other", Data: map[string]any{
			"dynamic_schema": map[string]any{
				"rules": guidanceRecords(map[string]any{"provider": "other", "invented": true}),
			},
		},
		ProviderPrefixes: map[string]string{"other_": "other"},
	})
	result := CollectAssessmentGuidance(CollectAssessmentGuidanceOptions{
		Source: NewAssessmentGuidanceSource(valid), Tenant: "tenant", Label: "sample_resource",
		Members: []string{"sample_resource"}, Plan: guidancePlan(), Findings: guidanceFindings(),
	})
	if len(result.Entries) != 1 || result.Entries[0]["rule"] != "sample_dynamic_mode" {
		t.Errorf("CollectAssessmentGuidance(malformed other provider).Entries = %#v, want sample_dynamic_mode", result.Entries)
	}
}

func TestGuidanceSourceIsDetachedFromLoadedRoot(t *testing.T) {
	data := map[string]any{
		"provider_config": map[string]any{
			"requirements": guidanceRecords(map[string]any{
				"id": "sample_attribution", "setting": "add_attribution", "value": false,
				"reason": "provider default", "plan_paths": []any{"terraform_labels.goog-terraform-provisioned"},
				"remediation": map[string]any{
					"kind": "provider_argument", "mode": "required_external", "evidence": "provider.md",
				},
			}),
		},
	}
	root := guidanceRoot(data)
	source := NewAssessmentGuidanceSource(root)
	root.Resources["sample_resource"] = metadata.LoadedResourceMetadata{Provider: "mutated"}
	root.Packs.ProviderPrefixes["sample_"] = "mutated"
	requirement := data["provider_config"].(map[string]any)["requirements"].([]any)[0].(map[string]any)
	requirement["setting"] = "mutated"
	requirement["remediation"].(map[string]any)["mode"] = "diagnostic_only"
	result := CollectAssessmentGuidance(CollectAssessmentGuidanceOptions{
		Source: source, Tenant: "tenant", Label: "sample_resource", Members: []string{"sample_resource"},
		Plan: guidancePlan(), Findings: guidanceFindings(),
	})
	if len(result.Entries) != 1 || result.Entries[0]["setting"] != "add_attribution" {
		t.Errorf("CollectAssessmentGuidance(detached source).Entries = %#v, want original add_attribution", result.Entries)
	}
}

func TestGuidancePathsPreserveSchemaAndConcreteForms(t *testing.T) {
	plan := map[string]any{
		"resource_drift": guidanceRecords(map[string]any{
			"address": `sample_resource.this["one"]`, "type": "sample_resource",
			"change": map[string]any{
				"actions": []any{"no-op", "update"},
				"before": map[string]any{"rules": []any{
					map[string]any{"labels": map[string]any{"a.b": "old"}},
					map[string]any{"labels": map[string]any{"a.b": "old"}},
				}},
				"after": map[string]any{"rules": []any{
					map[string]any{"labels": map[string]any{"a.b": "new"}},
					map[string]any{"labels": map[string]any{"a.b": "new"}},
				}},
			},
		}),
	}
	data := map[string]any{
		"dynamic_schema": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id": "quoted_map_key", "provider_version_constraint": "1.0.0",
				"path": `rules[].labels["a.b"]`, "kind": "provider_observed_projection_unsafe",
				"ownership": "unknown", "action": "manual_review_required",
				"evidence": "dynamic.md", "reason": "quoted key", "resource_type": "sample_resource",
			}),
		},
	}
	findings := []PlanFinding{{
		Status: Blocked, Source: "resource_drift", Address: `sample_resource.this["one"]`, Actions: []string{"update"},
		Paths: []PlanPath{{"rules", 1, "labels", "a.b"}, {"rules", 0, "labels", "a.b"}},
	}}
	result := CollectAssessmentGuidance(CollectAssessmentGuidanceOptions{
		Source: NewAssessmentGuidanceSource(guidanceRoot(data)), Tenant: "tenant", Label: "sample_resource",
		Members: []string{"sample_resource"}, Plan: plan, Findings: findings,
	})
	// planRecords emits one schema-matched annotation for each concrete plan
	// candidate, then joinBlockedFindings joins each annotation to each blocked
	// concrete path with that schema. The report layer performs the final exact-
	// entry deduplication, so the source collector deliberately returns this
	// two-by-two cross product just like Node.
	if got, want := len(result.Entries), 4; got != want {
		t.Fatalf("CollectAssessmentGuidance(two list indexes).Entries length = %d, want %d: %#v", got, want, result.Entries)
	}
	for index, wantPath := range []string{
		`rules[1].labels.a.b`, `rules[1].labels.a.b`,
		`rules[0].labels.a.b`, `rules[0].labels.a.b`,
	} {
		if got := result.Entries[index]["matched_plan_path"]; got != "rules[].labels.a.b" {
			t.Errorf("CollectAssessmentGuidance(two list indexes).Entries[%d].matched_plan_path = %#v, want %q", index, got, "rules[].labels.a.b")
		}
		if got := result.Entries[index]["finding_path"]; got != wantPath {
			t.Errorf("CollectAssessmentGuidance(two list indexes).Entries[%d].finding_path = %#v, want %q", index, got, wantPath)
		}
	}
}

func TestUnrepresentableGuidanceFailsClosedWithinLane(t *testing.T) {
	values := []struct {
		name  string
		value any
	}{
		{name: "negative_zero", value: math.Copysign(0, -1)},
		{name: "unsafe_float_integer", value: float64(9007199254740992)},
		{name: "invalid_lossless_token", value: json.Number("01")},
		{name: "non_json_type", value: make(chan int)},
	}
	for _, test := range values {
		t.Run(test.name, func(t *testing.T) {
			data := map[string]any{
				"provider_config": map[string]any{
					"requirements": guidanceRecords(map[string]any{
						"id": "sample_value", "setting": "sample_value", "value": test.value,
						"reason": "provider value", "plan_paths": []any{"terraform_labels.goog-terraform-provisioned"},
						"remediation": map[string]any{
							"kind": "provider_argument", "mode": "required_external", "evidence": "provider.md",
						},
					}),
				},
			}
			if got := collectGuidance(data).Entries; len(got) != 0 {
				t.Errorf("CollectAssessmentGuidance(%s unrepresentable value).Entries = %#v, want []", test.name, got)
			}
		})
	}
}

func TestGuidanceTextUsesJavaScriptTrimAndDoesNotLeakMalformedValues(t *testing.T) {
	data := map[string]any{
		"dynamic_schema": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id": "\ufeffsample_dynamic_mode\ufeff", "provider_version_constraint": "\ufeff1.0.0\ufeff",
				"path": "\ufeffsettings[].mode\ufeff", "kind": "\ufeffprovider_observed_projection_unsafe\ufeff",
				"ownership": "\ufeffunknown\ufeff", "action": "\ufeffmanual_review_required\ufeff",
				"evidence": "\ufeffdynamic.md\ufeff", "reason": "\ufeffdynamic field\ufeff",
				"resource_type": "sample_resource",
			}),
		},
	}
	entries := collectGuidance(data).Entries
	if len(entries) != 1 {
		t.Fatalf("CollectAssessmentGuidance(FEFF-trimmed dynamic rule).Entries = %#v, want one", entries)
	}
	if got, want := entries[0]["rule"], "sample_dynamic_mode"; got != want {
		t.Errorf("CollectAssessmentGuidance(FEFF-trimmed dynamic rule).rule = %#v, want %q", got, want)
	}
	if got, want := entries[0]["provider_version_constraint"], "1.0.0"; got != want {
		t.Errorf("CollectAssessmentGuidance(FEFF-trimmed dynamic rule).provider_version_constraint = %#v, want %q", got, want)
	}
}

func TestAfterUnknownPathCanProduceGuidance(t *testing.T) {
	data := map[string]any{
		"dynamic_schema": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id": "unknown_mode", "provider_version_constraint": "1.0.0",
				"path": "settings[].mode", "kind": "provider_observed_projection_unsafe",
				"ownership": "unknown", "action": "manual_review_required",
				"evidence": "dynamic.md", "reason": "unknown after apply", "resource_type": "sample_resource",
			}),
		},
	}
	plan := map[string]any{
		"resource_changes": guidanceRecords(map[string]any{
			"address": `sample_resource.this["one"]`, "type": "sample_resource",
			"change": map[string]any{
				"actions": []any{"update"},
				"before":  map[string]any{"settings": []any{map[string]any{"mode": "same"}}},
				"after":   map[string]any{"settings": []any{map[string]any{"mode": "same"}}},
				"after_unknown": map[string]any{
					"settings": []any{map[string]any{"mode": true}},
				},
			},
		}),
	}
	findings := []PlanFinding{{
		Status: Blocked, Source: "resource_changes", Address: `sample_resource.this["one"]`,
		Actions: []string{"update"}, Paths: []PlanPath{{"settings", 0, "mode"}},
	}}
	result := CollectAssessmentGuidance(CollectAssessmentGuidanceOptions{
		Source: NewAssessmentGuidanceSource(guidanceRoot(data)), Tenant: "tenant", Label: "sample_resource",
		Members: []string{"sample_resource"}, Plan: plan, Findings: findings,
	})
	if len(result.Entries) != 1 || result.Entries[0]["rule"] != "unknown_mode" {
		t.Errorf("CollectAssessmentGuidance(after_unknown path).Entries = %#v, want unknown_mode", result.Entries)
	}
}

func TestGuidanceRequiresUpdateCandidatesAndBlockedFindings(t *testing.T) {
	data := map[string]any{
		"dynamic_schema": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id": "sample_dynamic_mode", "provider_version_constraint": "1.0.0",
				"path": "settings[].mode", "kind": "provider_observed_projection_unsafe",
				"ownership": "unknown", "action": "manual_review_required",
				"evidence": "dynamic.md", "reason": "dynamic field", "resource_type": "sample_resource",
			}),
		},
	}
	tests := []struct {
		name     string
		actions  []any
		status   PlanStatus
		wantSize int
	}{
		{name: "blocked_update", actions: []any{"update"}, status: Blocked, wantSize: 1},
		{name: "blocked_create", actions: []any{"create"}, status: Blocked, wantSize: 0},
		{name: "tolerated_update", actions: []any{"update"}, status: Tolerated, wantSize: 0},
		{name: "clean_update", actions: []any{"update"}, status: Clean, wantSize: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := guidancePlan()
			plan["resource_changes"].([]any)[0].(map[string]any)["change"].(map[string]any)["actions"] = test.actions
			findings := guidanceFindings()
			findings[0].Status = test.status
			result := CollectAssessmentGuidance(CollectAssessmentGuidanceOptions{
				Source: NewAssessmentGuidanceSource(guidanceRoot(data)), Tenant: "tenant", Label: "sample_resource",
				Members: []string{"sample_resource"}, Plan: plan, Findings: findings,
			})
			if got := len(result.Entries); got != test.wantSize {
				t.Errorf("CollectAssessmentGuidance(%s eligibility).Entries length = %d, want %d: %#v", test.name, got, test.wantSize, result.Entries)
			}
		})
	}
}

func TestMalformedGuidanceIsDroppedWithoutValueLeakage(t *testing.T) {
	secret := "guidance-secret-728d"
	data := map[string]any{
		"absent_defaults": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id": "secret_rule", "path": "rules[].id", "kind": "provider_absent_placeholder",
				"observed_value": json.Number("0"), "action": "manual_review_required",
				"evidence": secret, "reason": secret, "resource_type": "sample_resource", "invented": secret,
			}),
		},
		"dynamic_schema": map[string]any{
			"rules": guidanceRecords(map[string]any{
				"id": "sample_dynamic_mode", "provider_version_constraint": "1.0.0",
				"path": "settings[].mode", "kind": "provider_observed_projection_unsafe",
				"ownership": "unknown", "action": "manual_review_required",
				"evidence": "dynamic.md", "reason": "dynamic field", "resource_type": "sample_resource",
			}),
		},
	}
	entries := collectGuidance(data).Entries
	if len(entries) != 1 || entries[0]["rule"] != "sample_dynamic_mode" {
		t.Fatalf("CollectAssessmentGuidance(secret malformed lane).Entries = %#v, want only sample_dynamic_mode", entries)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("json.Marshal(CollectAssessmentGuidance(secret malformed lane).Entries) error = %v, want nil", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Errorf("CollectAssessmentGuidance(secret malformed lane).Entries = %s, want malformed value absent", encoded)
	}
}

func TestRealPackRootSuppliesAllGuidanceLanes(t *testing.T) {
	repository, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("filepath.Abs(../../..) error = %v, want nil", err)
	}
	profile := filepath.Join(repository, "packsets", "full.json")
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot: filepath.Join(repository, "packs"), ProfilePath: &profile, CatalogPath: &profile,
	})
	if err != nil {
		t.Fatalf("metadata.LoadPackRoot(full) error = %v, want nil", err)
	}
	tests := []struct {
		resourceType string
		provider     string
		before       map[string]any
		after        map[string]any
		path         PlanPath
		wantLane     string
		wantKey      string
		wantValue    string
	}{
		{
			resourceType: "google_bigquery_dataset", provider: "google",
			before:   map[string]any{"terraform_labels": map[string]any{}},
			after:    map[string]any{"terraform_labels": map[string]any{"goog-terraform-provisioned": "true"}},
			path:     PlanPath{"terraform_labels", "goog-terraform-provisioned"},
			wantLane: "provider_config", wantKey: "setting", wantValue: "add_terraform_attribution_label",
		},
		{
			resourceType: "aws_cloudwatch_log_group", provider: "aws",
			before: map[string]any{"name_prefix": ""}, after: map[string]any{"name_prefix": nil},
			path: PlanPath{"name_prefix"}, wantLane: "absent_default",
			wantKey: "rule", wantValue: "aws_cloudwatch_log_group_empty_name_prefix",
		},
		{
			resourceType: "cloudflare_dns_record", provider: "cloudflare",
			before: map[string]any{"data": map[string]any{"flags": "old"}},
			after:  map[string]any{"data": map[string]any{"flags": "new"}},
			path:   PlanPath{"data", "flags"}, wantLane: "dynamic_schema",
			wantKey: "rule", wantValue: "cloudflare_dns_record_data_flags_dynamic",
		},
	}
	resources := make(map[string]metadata.LoadedResourceMetadata, len(root.Resources)+len(tests))
	for resourceType, resource := range root.Resources {
		resources[resourceType] = resource
	}
	for _, test := range tests {
		resources[test.resourceType] = metadata.LoadedResourceMetadata{
			Type: test.resourceType, Product: test.provider, Provider: test.provider,
		}
	}
	root.Resources = resources
	source := NewAssessmentGuidanceSource(root)
	for _, test := range tests {
		t.Run(test.resourceType, func(t *testing.T) {
			address := test.resourceType + `.this["one"]`
			result := CollectAssessmentGuidance(CollectAssessmentGuidanceOptions{
				Source: source, Tenant: "tenant", Label: test.resourceType, Members: []string{test.resourceType},
				Plan: map[string]any{
					"resource_changes": guidanceRecords(map[string]any{
						"address": address, "type": test.resourceType,
						"change": map[string]any{"actions": []any{"update"}, "before": test.before, "after": test.after},
					}),
				},
				Findings: []PlanFinding{{
					Status: Blocked, Source: "resource_changes", Address: address,
					Actions: []string{"update"}, Paths: []PlanPath{test.path},
				}},
			})
			if len(result.Entries) != 1 {
				t.Fatalf("CollectAssessmentGuidance(%s real pack).Entries = %#v, want one", test.resourceType, result.Entries)
			}
			if got := result.Entries[0]["lane"]; got != test.wantLane {
				t.Errorf("CollectAssessmentGuidance(%s real pack).lane = %#v, want %q", test.resourceType, got, test.wantLane)
			}
			if got := result.Entries[0][test.wantKey]; got != test.wantValue {
				t.Errorf("CollectAssessmentGuidance(%s real pack).%s = %#v, want %q", test.resourceType, test.wantKey, got, test.wantValue)
			}
		})
	}
}
