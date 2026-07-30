package tfrender

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
)

func setBlockBindingContext(setBlockFields map[string]int) BindingContext {
	return BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"zia_firewall_filtering_network_service_groups": true, "zia_firewall_filtering_network_service": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"services.id": {NameField: "name", Referent: "zia_firewall_filtering_network_service"},
		},
		ResourceRoots: map[string]string{
			"zia_firewall_filtering_network_service_groups": "zia_firewall_filtering_network_service_groups",
			"zia_firewall_filtering_network_service":        "zia_firewall_filtering_network_service",
		},
		SetBlockFields: setBlockFields,
	}
}

// TestDeriveGeneratedBindingsSetBlockBindsTheCompleteLeaf is the downstream
// wave-2 blocker in miniature. ZIA declares services as a set-nested block, a
// set member has no stable order, and gen-env's schema-path validator refuses
// services[0].id with "bind the complete block leaf". The producer must emit
// that form: one binding at the block itself, whose expression reproduces the
// whole block value with the reference ids resolved.
func TestDeriveGeneratedBindingsSetBlockBindsTheCompleteLeaf(t *testing.T) {
	items := map[string]map[string]any{
		"iot_device_services": {
			"name": "IoT Device Services",
			"services": []any{
				map[string]any{"id": []any{json.Number("123"), json.Number("456")}},
			},
		},
	}
	lookupKeys := map[string]map[string]string{
		"zia_firewall_filtering_network_service": {"123": "service_one"},
	}
	result, err := DeriveGeneratedBindings(
		setBlockBindingContext(map[string]int{"services.id": 0}),
		items, lookupKeys, "zia_firewall_filtering_network_service_groups",
	)
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings(set block): %v", err)
	}
	fields, ok := result.Resources["zia_firewall_filtering_network_service_groups.iot_device_services"].(map[string]any)
	if !ok {
		t.Fatalf("resources = %#v, want the iot_device_services item bound", result.Resources)
	}
	binding, ok := fields["services"].(map[string]any)
	if !ok {
		t.Fatalf("bindings = %#v, want one binding at the complete block leaf \"services\"", fields)
	}
	wantExpression := "[{ id = [" +
		"data.terraform_remote_state.zia_firewall_filtering_network_service.outputs.infrawright_reference_ids.zia_firewall_filtering_network_service[\"service_one\"]" +
		", 456] }]"
	if got := binding["expression"]; got != wantExpression {
		t.Errorf("expression = %q, want %q", got, wantExpression)
	}
	for path := range fields {
		if strings.Contains(path, "[") {
			t.Errorf("binding path %q indexes into a set block; the validator refuses that form", path)
		}
	}
}

// TestDeriveGeneratedBindingsSetBlockLiteralsAreFaithfullyTyped pins the
// literal rendering inside the complete-leaf expression: an unresolved
// numeric id stays a number and a sibling member is reproduced exactly. The
// expression replaces the whole block value, so a member it reshapes is
// configuration rewritten without being asked.
func TestDeriveGeneratedBindingsSetBlockLiteralsAreFaithfullyTyped(t *testing.T) {
	items := map[string]map[string]any{
		"group_one": {
			"services": []any{
				map[string]any{
					"id":          []any{json.Number("999")},
					"description": "kept literal",
					"enabled":     true,
				},
			},
		},
	}
	// Nothing resolves: no binding may be emitted at all, and nothing indexed.
	result, err := DeriveGeneratedBindings(
		setBlockBindingContext(map[string]int{"services.id": 0}),
		items,
		map[string]map[string]string{"zia_firewall_filtering_network_service": {"other": "x"}},
		"zia_firewall_filtering_network_service_groups",
	)
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings(nothing resolves): %v", err)
	}
	if len(result.Resources) != 0 {
		t.Errorf("resources = %#v, want none when no id resolves", result.Resources)
	}

	// One id resolves and one does not: the sibling members ride along and
	// the unresolved id stays a literal -- faithfully typed. The unresolved
	// numeric is the load-bearing case: the expression replaces the whole
	// block value, so a number quoted into a string here rewrites the type
	// of configuration the binding never touched.
	items["group_one"]["services"] = []any{
		map[string]any{
			"id":          []any{json.Number("999"), json.Number("777")},
			"description": "kept literal",
			"enabled":     true,
		},
	}
	resolved, err := DeriveGeneratedBindings(
		setBlockBindingContext(map[string]int{"services.id": 0}),
		items,
		map[string]map[string]string{"zia_firewall_filtering_network_service": {"999": "svc"}},
		"zia_firewall_filtering_network_service_groups",
	)
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings(one resolves): %v", err)
	}
	fields := resolved.Resources["zia_firewall_filtering_network_service_groups.group_one"].(map[string]any)
	expression := fields["services"].(map[string]any)["expression"].(string)
	for _, required := range []string{`description = "kept literal"`, "enabled = true"} {
		if !strings.Contains(expression, required) {
			t.Errorf("expression %q is missing the faithful literal %q", expression, required)
		}
	}
	if !strings.Contains(expression, ", 777]") {
		t.Errorf("expression %q is missing the unresolved id as an unquoted number literal", expression)
	}
	if strings.Contains(expression, `"777"`) || strings.Contains(expression, `"999"`) {
		t.Errorf("expression %q renders a numeric id as a string", expression)
	}
}

// TestDeriveGeneratedBindingsListBlocksKeepIndexedPaths pins the other half
// of the contract: a list-nested block has ordered members, indexed paths
// through it are valid, and the ZPA demo shape must not change.
func TestDeriveGeneratedBindingsListBlocksKeepIndexedPaths(t *testing.T) {
	context := BindingContext{
		Derived:   map[string]bool{},
		Generated: map[string]bool{"zpa_app_connector_group": true, "zpa_server_group": true},
		Mode:      deployment.ReferenceBindingCrossState,
		References: map[string]TransformReferenceSpec{
			"server_groups.id": {NameField: "name", Referent: "zpa_server_group"},
		},
		ResourceRoots: map[string]string{
			"zpa_app_connector_group": "zpa_app_connector_group",
			"zpa_server_group":        "zpa_server_group",
		},
		// server_groups is nesting-mode list: absent from SetBlockFields.
		SetBlockFields: map[string]int{},
	}
	items := map[string]map[string]any{
		"connector_one": {
			"server_groups": []any{map[string]any{"id": []any{"sg-1"}}},
		},
	}
	result, err := DeriveGeneratedBindings(context, items,
		map[string]map[string]string{"zpa_server_group": {"sg-1": "server_one"}},
		"zpa_app_connector_group",
	)
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings(list block): %v", err)
	}
	fields := result.Resources["zpa_app_connector_group.connector_one"].(map[string]any)
	if _, ok := fields["server_groups[0].id"]; !ok {
		t.Errorf("bindings = %#v, want the indexed path preserved for a list block", fields)
	}
}

// TestDeriveGeneratedBindingsSetBlockUnderAListParent pins the deeper shape:
// a set block nested inside a list block binds at the concrete list index
// followed by the complete set leaf -- indexes are valid up to the set
// boundary and forbidden after it.
func TestDeriveGeneratedBindingsSetBlockUnderAListParent(t *testing.T) {
	context := setBlockBindingContext(map[string]int{"rules.services.id": 1})
	context.References = map[string]TransformReferenceSpec{
		"rules.services.id": {NameField: "name", Referent: "zia_firewall_filtering_network_service"},
	}
	items := map[string]map[string]any{
		"group_one": {
			"rules": []any{
				map[string]any{"services": []any{map[string]any{"id": []any{json.Number("123")}}}},
			},
		},
	}
	result, err := DeriveGeneratedBindings(context, items,
		map[string]map[string]string{"zia_firewall_filtering_network_service": {"123": "svc"}},
		"zia_firewall_filtering_network_service_groups",
	)
	if err != nil {
		t.Fatalf("DeriveGeneratedBindings(set under list): %v", err)
	}
	fields := result.Resources["zia_firewall_filtering_network_service_groups.group_one"].(map[string]any)
	if _, ok := fields["rules[0].services"]; !ok {
		t.Errorf("bindings = %#v, want rules[0].services -- indexed to the list, complete at the set", fields)
	}
}
