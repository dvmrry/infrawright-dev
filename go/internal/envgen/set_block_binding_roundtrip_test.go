package envgen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// ziaSetBlockSchema is the shape the downstream wave-2 blocker ran into:
// a set-nested block whose element carries a set(number) reference attribute,
// exactly zia_firewall_filtering_network_service_groups.services.
func ziaSetBlockSchema() metadata.JsonObject {
	return metadata.JsonObject{"block": metadata.JsonObject{
		"attributes": metadata.JsonObject{
			"name": metadata.JsonObject{"required": true, "type": "string"},
		},
		"block_types": metadata.JsonObject{
			"services": metadata.JsonObject{
				"nesting_mode": "set",
				"block": metadata.JsonObject{"attributes": metadata.JsonObject{
					"id": metadata.JsonObject{"required": true, "type": []any{"set", "number"}},
				}},
			},
		},
	}}
}

// TestSetBlockBindingsRoundTripThroughTheSchemaValidator pins the agreement
// between the two halves of the engine that disagreed: DeriveGeneratedBindings
// (the producer) and ValidateExpressionBindingSchemaPaths (the gen-env
// gatekeeper). The producer's complete-leaf output must pass the validator,
// and the indexed form it used to emit must still be refused -- if either
// half moves alone, this fails.
func TestSetBlockBindingsRoundTripThroughTheSchemaValidator(t *testing.T) {
	const resourceType = "zia_firewall_filtering_network_service_groups"
	context := tfrender.BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{resourceType: true, "zia_firewall_filtering_network_service": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]tfrender.TransformReferenceSpec{
			"services.id": {NameField: "name", Referent: "zia_firewall_filtering_network_service"},
		},
		ResourceRoots: map[string]string{
			resourceType:                             resourceType,
			"zia_firewall_filtering_network_service": "zia_firewall_filtering_network_service",
		},
		SetBlockFields: map[string]int{"services.id": 0},
	}
	items := map[string]map[string]any{
		"iot_device_services": {
			"name": "IoT Device Services",
			"services": []any{
				map[string]any{"id": []any{json.Number("123"), json.Number("456")}},
			},
		},
	}
	result, err := tfrender.DeriveGeneratedBindings(context, items,
		map[string]map[string]string{"zia_firewall_filtering_network_service": {"123": "service_one", "456": "service_two"}},
		resourceType,
	)
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings: %v", err)
	}
	if len(result.Resources) == 0 {
		t.Fatal("DeriveGeneratedBindings bound nothing; the round-trip has nothing to prove")
	}
	bindings, err := ParseExpressionBindings(map[string]any{"resources": result.Resources}, resourceType)
	if err != nil {
		t.Fatalf("ParseExpressionBindings(produced bindings): %v", err)
	}
	if err := ValidateExpressionBindingSchemaPaths(ziaSetBlockSchema(), resourceType, bindings); err != nil {
		t.Fatalf("ValidateExpressionBindingSchemaPaths(produced bindings) = %v; the producer emitted a form gen-env refuses -- the wave-2 blocker", err)
	}

	// The form the producer used to emit stays refused: an indexed path into
	// a set block names nothing.
	indexed, err := ParseExpressionBindings(map[string]any{"resources": map[string]any{
		resourceType + ".iot_device_services": map[string]any{
			"services[0].id": map[string]any{"expression": "[1]", "reason": "regression probe"},
		},
	}}, resourceType)
	if err != nil {
		t.Fatalf("ParseExpressionBindings(indexed probe): %v", err)
	}
	err = ValidateExpressionBindingSchemaPaths(ziaSetBlockSchema(), resourceType, indexed)
	if err == nil || !strings.Contains(err.Error(), "bind the complete block leaf") {
		t.Fatalf("ValidateExpressionBindingSchemaPaths(indexed set path) = %v, want the complete-block-leaf refusal", err)
	}

	// And the produced binding applies: the whole services value becomes the
	// expression sentinel, which is what the generated root renders.
	applied, err := ApplyExpressionBindings(map[string]any{
		"iot_device_services": map[string]any{
			"name": "IoT Device Services",
			"services": []any{
				map[string]any{"id": []any{json.Number("123"), json.Number("456")}},
			},
		},
	}, bindings)
	if err != nil {
		t.Fatalf("ApplyExpressionBindings(complete leaf): %v", err)
	}
	item := applied["iot_device_services"].(map[string]any)
	if _, isExpression := item["services"].(*HclExpression); !isExpression {
		t.Fatalf("applied services = %#v, want the expression sentinel replacing the whole block value", item["services"])
	}
}

// TestCompositeExpressionAllowlist pins the extended v1 grammar from both
// sides. The composite forms exist solely so a complete set-block leaf can be
// expressed; they are closed literals plus the already-allowed selector
// atoms, and everything that could widen the injection surface this
// allowlist exists to close must stay refused.
func TestCompositeExpressionAllowlist(t *testing.T) {
	allowed := []string{
		`[{ id = [data.terraform_remote_state.a.outputs.iw_reference_ids.a["k"], 456] }]`,
		`[{ id = [456] }]`,
		`[{ id = [456], description = "kept literal", enabled = true, note = null }]`,
		`[{ nested = { id = [1, 2] } }]`,
		`[{ id = [-1.5, 2e10, 0.25] }]`,
		"[]",
		`[{ }]`,
	}
	for _, expression := range allowed {
		if _, err := ValidateExpression(expression, "test"); err != nil {
			t.Errorf("ValidateExpression(%q) = %v, want allowed", expression, err)
		}
	}
	refused := []string{
		// Function calls and bare identifiers are not literals.
		`[{ id = evil() }]`,
		`[{ id = someident }]`,
		`[{ id = [trueish] }]`,
		// var. and local. stay excluded from composites, as the ported
		// element grammar excluded them from lists.
		`[{ id = var.sneaky }]`,
		`[{ id = local.sneaky }]`,
		// Trailing garbage after a balanced composite.
		`[{ id = [1] }] + evil()`,
		// Template interpolation inside a composite string member.
		"[{ id = \"${evil}\" }]",
		// Malformed shapes.
		`[{ id = }]`,
		`[{ = [1] }]`,
		`[{ id [1] }]`,
		`[{ id = [1],, }]`,
		`{ id = [1] }`,
		`[{ id = [01] }]`,
		`[{ id = [1.] }]`,
		`[{ id = [1e] }]`,
	}
	for _, expression := range refused {
		if _, err := ValidateExpression(expression, "test"); err == nil {
			t.Errorf("ValidateExpression(%q) = nil, want refused", expression)
		}
	}
}

// TestRemoteStateReferenceLeafMatchingStaysDeclared pins both sides of the
// leaf-covers-declared rule in the gen-env edge validator: a binding at a set
// block leaf is accepted exactly when a declared reference field lies beneath
// it and every identity component matches; everything else stays refused.
func TestRemoteStateReferenceLeafMatchingStaysDeclared(t *testing.T) {
	index := newRemoteStateReferenceValidationIndex(
		CrossStateReferenceTopology{Edges: []CrossStateReferenceEdge{{
			Field: "workload_groups.id", Referrer: "zia_url_filtering_rules",
			ReferrerRoot: "zia_url_filtering_rules",
			Referent:     "zia_workload_groups", ReferentRoot: "zia_workload_groups",
		}}},
		map[string]roots.RootTopologyRoot{
			"zia_workload_groups": {Label: "zia_workload_groups", Members: []string{"zia_workload_groups"}},
		},
	)
	reference := func(field, referent, root string) boundRemoteStateReference {
		return boundRemoteStateReference{
			RemoteStateReference: RemoteStateReference{Root: root, ResourceType: referent, Key: "k"},
			Field:                field,
			Referrer:             "zia_url_filtering_rules",
		}
	}
	if err := validateRemoteStateReferences(index, "zia_url_filtering_rules",
		[]boundRemoteStateReference{reference("workload_groups", "zia_workload_groups", "zia_workload_groups")},
	); err != nil {
		t.Errorf("complete-leaf binding above the declared field = %v, want accepted", err)
	}
	if err := validateRemoteStateReferences(index, "zia_url_filtering_rules",
		[]boundRemoteStateReference{reference("workload_groups.id", "zia_workload_groups", "zia_workload_groups")},
	); err != nil {
		t.Errorf("exact declared field = %v, want accepted", err)
	}
	for name, bad := range map[string]boundRemoteStateReference{
		"undeclared sibling leaf": reference("labels", "zia_workload_groups", "zia_workload_groups"),
		"leaf above the wrong referent": {
			RemoteStateReference: RemoteStateReference{Root: "zia_workload_groups", ResourceType: "zia_rule_labels", Key: "k"},
			Field:                "workload_groups",
			Referrer:             "zia_url_filtering_rules",
		},
		"prefix of the block name, not a path boundary": reference("workload", "zia_workload_groups", "zia_workload_groups"),
		// The identity must pin the referrer: another resource type binding
		// the same leaf name does not inherit an edge declared for someone
		// else. This is the vector the earlier membership check cannot catch,
		// because the referent genuinely lives in the referenced root.
		"another referrer borrowing the declared edge": {
			RemoteStateReference: RemoteStateReference{Root: "zia_workload_groups", ResourceType: "zia_workload_groups", Key: "k"},
			Field:                "workload_groups",
			Referrer:             "zia_dlp_web_rules",
		},
	} {
		if err := validateRemoteStateReferences(index, "zia_url_filtering_rules", []boundRemoteStateReference{bad}); err == nil {
			t.Errorf("%s = nil, want refused", name)
		}
	}
}
