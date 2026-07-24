package sourceanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

const fieldWitnessResource = "zia_firewall_filtering_network_service"

func TestAnalyzeUnverifiedFieldWitnessesCorroboratesProviderBehavior(t *testing.T) {
	report, err := AnalyzeUnverifiedFieldWitnesses(context.Background(), fieldWitnessInputs(t, fieldWitnessSchema, true))
	if err != nil {
		t.Fatalf("AnalyzeUnverifiedFieldWitnesses() error = %v, want nil", err)
	}
	if report.SourceTrust != contracts.SourceTrustUnverified {
		t.Fatalf("AnalyzeUnverifiedFieldWitnesses().SourceTrust = %q, want unverified", report.SourceTrust)
	}
	resource, ok := report.Resources[fieldWitnessResource]
	if !ok {
		t.Fatalf("AnalyzeUnverifiedFieldWitnesses().Resources lacks %q", fieldWitnessResource)
	}

	tag := requireFieldWitness(t, resource, "tag")
	if tag.Disposition != FieldWitnessCorroborated {
		t.Errorf("tag disposition = %q, want corroborated", tag.Disposition)
	}
	if tag.Assessment.Declaration != FieldDeclarationConsistent || tag.Assessment.Read != FieldReadObserved ||
		tag.Assessment.Acceptance != FieldAcceptanceSilent {
		t.Errorf("tag assessment = %#v, want consistent declaration, observed Read, and silent acceptance", tag.Assessment)
	}
	if len(tag.ProviderSchemas) != 1 || !tag.ProviderSchemas[0].Optional || !tag.ProviderSchemas[0].Computed {
		t.Fatalf("tag provider schemas = %#v, want optional+computed declaration", tag.ProviderSchemas)
	}
	if !containsString(tag.ProviderSchemas[0].Validators, "validation.StringLenBetween(0, 255)") {
		t.Errorf("tag validators = %#v, want StringLenBetween witness", tag.ProviderSchemas[0].Validators)
	}
	if len(tag.ReadBacks) != 1 || tag.ReadBacks[0].Expression != "resp.Tag" {
		t.Errorf("tag read-backs = %#v, want d.Set from resp.Tag", tag.ReadBacks)
	}
	if len(tag.AcceptanceConfigs) != 0 || len(tag.AcceptanceChecks) != 0 {
		t.Errorf("tag acceptance witnesses = (%#v, %#v), want silence", tag.AcceptanceConfigs, tag.AcceptanceChecks)
	}

	ports := requireFieldWitness(t, resource, "src_tcp_ports")
	if len(ports.ReadBacks) != 1 || ports.ReadBacks[0].Expression != "flattenNetworkPorts(resp.SrcTCPPorts)" {
		t.Errorf("src_tcp_ports read-backs = %#v, want flattening d.Set", ports.ReadBacks)
	}
	if ports.Assessment.Read != FieldReadShapeConsistent || ports.Assessment.Write != FieldWriteObserved ||
		ports.Assessment.Acceptance != FieldAcceptanceConfiguredAndAsserted {
		t.Errorf("src_tcp_ports assessment = %#v, want shape-consistent Read, observed write, and configured+asserted acceptance", ports.Assessment)
	}
	if len(ports.AcceptanceConfigs) != 1 || ports.AcceptanceConfigs[0].Occurrences != 3 ||
		ports.AcceptanceConfigs[0].ParentInstances != 1 ||
		ports.AcceptanceConfigs[0].Syntax != acceptanceConfigSyntaxBlock {
		t.Errorf("src_tcp_ports config witnesses = %#v, want three blocks", ports.AcceptanceConfigs)
	}
	if len(ports.AcceptanceChecks) != 1 || ports.AcceptanceChecks[0].Path != "src_tcp_ports.#" ||
		ports.AcceptanceChecks[0].Expected != "3" || !ports.AcceptanceChecks[0].ResourceAddressStatic ||
		ports.AcceptanceChecks[0].ResourceAddress != "zia_firewall_filtering_network_service.test" {
		t.Errorf("src_tcp_ports checks = %#v, want count assertion", ports.AcceptanceChecks)
	}

	end := requireFieldWitness(t, resource, "src_tcp_ports[].end")
	if end.Disposition != FieldWitnessCorroborated {
		t.Errorf("src_tcp_ports[].end disposition = %q, want corroborated", end.Disposition)
	}
	if end.Assessment.Acceptance != FieldAcceptanceConfigured {
		t.Errorf("src_tcp_ports[].end acceptance assessment = %q, want configured", end.Assessment.Acceptance)
	}
	if len(end.AcceptanceConfigs) != 1 || end.AcceptanceConfigs[0].Occurrences != 1 ||
		end.AcceptanceConfigs[0].ParentInstances != 3 ||
		!containsString(end.AcceptanceConfigs[0].Values, "5005") {
		t.Errorf("src_tcp_ports[].end config witnesses = %#v, want one end across three parent blocks", end.AcceptanceConfigs)
	}
	if len(end.AcceptanceChecks) != 0 {
		t.Errorf("src_tcp_ports[].end checks = %#v, want no invented round-trip assertion", end.AcceptanceChecks)
	}

	ipAddresses := requireFieldWitness(t, resource, "ip_addresses")
	if ipAddresses.Disposition != FieldWitnessCorroborated {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(ip_addresses).Disposition = %q, want corroborated", ipAddresses.Disposition)
	}
	if len(ipAddresses.AcceptanceConfigs) != 1 || ipAddresses.AcceptanceConfigs[0].Occurrences != 1 {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(ip_addresses).AcceptanceConfigs = %#v, want one attribute declaration", ipAddresses.AcceptanceConfigs)
	}
	if len(ipAddresses.AcceptanceConfigs) == 1 && ipAddresses.AcceptanceConfigs[0].Syntax != acceptanceConfigSyntaxAttribute {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(ip_addresses).AcceptanceConfigs[0].Syntax = %q, want attribute", ipAddresses.AcceptanceConfigs[0].Syntax)
	}
	if len(ipAddresses.AcceptanceChecks) != 1 || ipAddresses.AcceptanceChecks[0].Path != "ip_addresses.#" ||
		ipAddresses.AcceptanceChecks[0].Expected != "3" {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(ip_addresses).AcceptanceChecks = %#v, want collection count assertion", ipAddresses.AcceptanceChecks)
	}
	if len(ipAddresses.Conflicts) != 0 {
		t.Errorf("AnalyzeUnverifiedFieldWitnesses(ip_addresses).Conflicts = %#v, want attribute occurrences kept distinct from collection cardinality", ipAddresses.Conflicts)
	}
	if _, exists := resource.Fields["spoofed"]; exists {
		t.Error("fields contain Set call on non-ResourceData receiver")
	}
	if _, exists := resource.Fields["depends_on"]; exists {
		t.Error("fields contain Terraform resource meta-argument")
	}
	if !hasFieldWitnessDiagnostic(resource.Diagnostics, "read_back_key_dynamic") {
		t.Errorf("resource diagnostics = %#v, want dynamic d.Set key surfaced", resource.Diagnostics)
	}
	tagReview := requireFieldWitnessReviewItem(t, resource, "tag")
	if tagReview.Priority != FieldWitnessReviewHigh ||
		!containsString(tagReview.ReasonCodes, "write_input_absent") ||
		!containsString(tagReview.ReasonCodes, "optional_computed_round_trip") ||
		!containsString(tagReview.ReasonCodes, "validator_write_behavior") {
		t.Errorf("tag review = %#v, want high-priority Optional+Computed write-path guidance", tagReview)
	}
}

func TestAnalyzeUnverifiedFieldWitnessesSurfacesSchemaConflict(t *testing.T) {
	conflicting := strings.Replace(fieldWitnessSchema, `"computed": true`, `"computed": false`, 1)
	report, err := AnalyzeUnverifiedFieldWitnesses(context.Background(), fieldWitnessInputs(t, conflicting, false))
	if err != nil {
		t.Fatalf("AnalyzeUnverifiedFieldWitnesses() error = %v, want nil", err)
	}
	resource := report.Resources[fieldWitnessResource]
	tag := requireFieldWitness(t, resource, "tag")
	if tag.Disposition != FieldWitnessConflicting {
		t.Fatalf("tag disposition = %q, want conflicting", tag.Disposition)
	}
	if len(tag.Conflicts) == 0 {
		t.Fatal("tag conflicts are empty, want explicit computed disagreement")
	}
	if len(tag.AcceptanceConfigs) != 0 || len(tag.AcceptanceChecks) != 0 {
		t.Errorf("tag acceptance witnesses = (%#v, %#v), want absent tests to remain silence", tag.AcceptanceConfigs, tag.AcceptanceChecks)
	}
	review := requireFieldWitnessReviewItem(t, resource, "tag")
	if review.Priority != FieldWitnessReviewHigh {
		t.Errorf("tag review priority = %q, want high", review.Priority)
	}
	if !containsString(review.ReasonCodes, "schema_flag_mismatch") || !containsString(review.ReasonCodes, "witness_conflict") {
		t.Errorf("tag review reasons = %#v, want classified schema conflict", review.ReasonCodes)
	}
	if len(review.Details) == 0 || review.SuggestedValidation == "" {
		t.Errorf("tag review guidance = (%#v, %q), want conflict details and next validation", review.Details, review.SuggestedValidation)
	}
}

func TestAnalyzeUnverifiedFieldWitnessesSurfacesDeclarationTypeConflict(t *testing.T) {
	conflicting := strings.Replace(fieldWitnessSchema, `"tag": {"type": "string"`, `"tag": {"type": "number"`, 1)
	report, err := AnalyzeUnverifiedFieldWitnesses(context.Background(), fieldWitnessInputs(t, conflicting, false))
	if err != nil {
		t.Fatalf("AnalyzeUnverifiedFieldWitnesses() error = %v, want nil", err)
	}
	resource := report.Resources[fieldWitnessResource]
	tag := requireFieldWitness(t, resource, "tag")
	if tag.Disposition != FieldWitnessConflicting || tag.Assessment.Declaration != FieldDeclarationConflicting {
		t.Fatalf("tag = disposition %q assessment %#v, want declaration conflict", tag.Disposition, tag.Assessment)
	}
	if !containsSubstring(tag.Conflicts, "declaration kind differs") {
		t.Errorf("tag conflicts = %#v, want explicit declaration kind mismatch", tag.Conflicts)
	}
	review := requireFieldWitnessReviewItem(t, resource, "tag")
	if !containsString(review.ReasonCodes, "schema_type_mismatch") {
		t.Errorf("tag review = %#v, want schema_type_mismatch", review)
	}
}

func TestFieldWitnessReviewQueueRanksAndExplainsReview(t *testing.T) {
	optional := true
	falseValue := false
	fields := map[string]FieldWitness{
		"corroborated": {
			Disposition:     FieldWitnessCorroborated,
			TerraformSchema: &TerraformSchemaFieldWitness{Optional: &optional},
			ReadBacks:       []ReadBackFieldWitness{{Expression: "resp.Corroborated"}},
		},
		"dynamic_id": {
			Disposition:     FieldWitnessUntested,
			TerraformSchema: &TerraformSchemaFieldWitness{Optional: &optional},
		},
		"plain": {
			Disposition:     FieldWitnessUntested,
			TerraformSchema: &TerraformSchemaFieldWitness{Optional: &optional},
		},
		"schema_conflict": {
			Disposition:     FieldWitnessConflicting,
			TerraformSchema: &TerraformSchemaFieldWitness{Computed: &falseValue},
			ProviderSchemas: []ProviderSchemaFieldWitness{{Computed: true}},
			Conflicts:       []string{"schema_conflict computed is false in Terraform schema and true in provider schema"},
		},
		"validated_tag": {
			Disposition: FieldWitnessUntested,
			ProviderSchemas: []ProviderSchemaFieldWitness{{
				Optional:   true,
				Computed:   true,
				Validators: []string{"validation.StringLenBetween(0, 255)"},
			}},
		},
	}
	diagnostics := []FieldWitnessDiagnostic{{
		Code:      "provider_field_schema_unresolved",
		FieldPath: "dynamic_id",
		Message:   "dynamic_id: field value is not statically recoverable",
	}}

	queue := fieldWitnessReviewQueue(fields, diagnostics)
	wantPaths := []string{"schema_conflict", "validated_tag", "dynamic_id", "plain"}
	if len(queue) != len(wantPaths) {
		t.Fatalf("fieldWitnessReviewQueue() = %#v, want %d items", queue, len(wantPaths))
	}
	for index, wantPath := range wantPaths {
		if queue[index].FieldPath != wantPath {
			t.Errorf("fieldWitnessReviewQueue()[%d].FieldPath = %q, want %q", index, queue[index].FieldPath, wantPath)
		}
	}
	if queue[0].Priority != FieldWitnessReviewHigh || !containsString(queue[0].ReasonCodes, "schema_flag_mismatch") {
		t.Errorf("schema conflict review = %#v, want high classified conflict", queue[0])
	}
	if queue[1].Priority != FieldWitnessReviewHigh ||
		!containsString(queue[1].ReasonCodes, "optional_computed_round_trip") ||
		!containsString(queue[1].ReasonCodes, "validator_write_behavior") {
		t.Errorf("validated tag review = %#v, want high Optional+Computed validator guidance", queue[1])
	}
	if queue[2].Priority != FieldWitnessReviewMedium ||
		!containsString(queue[2].ReasonCodes, "source_analysis_incomplete") ||
		containsString(queue[2].ReasonCodes, "evidence_silence") {
		t.Errorf("dynamic field review = %#v, want parser limitation distinct from silence", queue[2])
	}
	if queue[3].Priority != FieldWitnessReviewLow || !containsString(queue[3].ReasonCodes, "evidence_silence") {
		t.Errorf("plain field review = %#v, want low evidence-silence guidance", queue[3])
	}
	if !containsString(queue[3].AbsentWitnessClasses, "provider_schema") || queue[3].SuggestedValidation == "" {
		t.Errorf("plain field review = %#v, want explicit gaps and next validation", queue[3])
	}
}

func TestFinalizeFieldWitnessSurfacesComparableBlockCountConflict(t *testing.T) {
	accumulated := &fieldWitnessAccumulator{
		acceptanceConfigs: []AcceptanceConfigFieldWitness{{
			Occurrences: 2,
			Syntax:      acceptanceConfigSyntaxBlock,
		}},
		acceptanceChecks: []AcceptanceCheckFieldWitness{{
			Path:     "ports.#",
			Expected: "3",
		}},
	}
	witness := finalizeFieldWitness("ports", accumulated)
	if witness.Disposition != FieldWitnessConflicting {
		t.Errorf("finalizeFieldWitness(ports).Disposition = %q, want conflicting", witness.Disposition)
	}
	if len(witness.Conflicts) != 1 || witness.Conflicts[0] != "ports acceptance check expects block count 3 but config declares 2 blocks" {
		t.Errorf("finalizeFieldWitness(ports).Conflicts = %#v, want comparable block-count conflict", witness.Conflicts)
	}
	queue := fieldWitnessReviewQueue(map[string]FieldWitness{"ports": witness}, nil)
	if len(queue) != 1 || !containsString(queue[0].ReasonCodes, "acceptance_block_count_mismatch") {
		t.Errorf("fieldWitnessReviewQueue(ports) = %#v, want classified acceptance block-count conflict", queue)
	}
}

func TestAnalyzeUnverifiedFieldWitnessesIsDeterministic(t *testing.T) {
	inputs := fieldWitnessInputs(t, fieldWitnessSchema, true)
	first, err := AnalyzeUnverifiedFieldWitnesses(context.Background(), inputs)
	if err != nil {
		t.Fatalf("first AnalyzeUnverifiedFieldWitnesses() error = %v, want nil", err)
	}
	second, err := AnalyzeUnverifiedFieldWitnesses(context.Background(), inputs)
	if err != nil {
		t.Fatalf("second AnalyzeUnverifiedFieldWitnesses() error = %v, want nil", err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("json.Marshal(first) error = %v, want nil", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("json.Marshal(second) error = %v, want nil", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Errorf("field witness JSON differs across identical runs:\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
}

func TestAnalyzeUnverifiedFieldWitnessesHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AnalyzeUnverifiedFieldWitnesses(ctx, sourcebind.UnverifiedInputs{}); err == nil {
		t.Error("AnalyzeUnverifiedFieldWitnesses(cancelled context) error = nil, want cancellation")
	}
}

func requireFieldWitness(t *testing.T, resource FieldWitnessResource, path string) FieldWitness {
	t.Helper()
	field, ok := resource.Fields[path]
	if !ok {
		t.Fatalf("resource fields lack %q: %#v", path, resource.Fields)
	}
	return field
}

func requireFieldWitnessReviewItem(t *testing.T, resource FieldWitnessResource, path string) FieldWitnessReviewItem {
	t.Helper()
	for _, item := range resource.ReviewQueue {
		if item.FieldPath == path {
			return item
		}
	}
	t.Fatalf("resource review queue lacks %q: %#v", path, resource.ReviewQueue)
	return FieldWitnessReviewItem{}
}

func fieldWitnessInputs(t *testing.T, schemaJSON string, includeAcceptance bool) sourcebind.UnverifiedInputs {
	t.Helper()
	root := t.TempDir()
	files := []string{"main.go", "zia/provider.go", "zia/resource_zia_fw_filtering_network_services.go"}
	writeFieldWitnessFile(t, root, "main.go", `package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"example.invalid/terraform-provider-zia/zia"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{ProviderFunc: zia.ZIAProvider})
}
`)
	writeFieldWitnessFile(t, root, "zia/provider.go", `package zia

import "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

func ZIAProvider() *schema.Provider {
	p := &schema.Provider{ResourcesMap: map[string]*schema.Resource{
		"zia_firewall_filtering_network_service": resourceFWNetworkServices(),
	}}
	return p
}
`)
	writeFieldWitnessFile(t, root, "zia/resource_zia_fw_filtering_network_services.go", `package zia

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceFWNetworkServices() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceNetworkServicesCreate,
		ReadContext: resourceNetworkServicesRead,
		Schema: map[string]*schema.Schema{
			"tag": getCloudFirewallNetworkServicesTag(),
			"ip_addresses": {Type: schema.TypeSet, Optional: true, Elem: &schema.Schema{Type: schema.TypeString}},
			"src_tcp_ports": resourceNetworkPortsSchema(),
		},
	}
}

func getCloudFirewallNetworkServicesTag() *schema.Schema {
	return &schema.Schema{
		Type: schema.TypeString,
		Optional: true,
		Computed: true,
		ValidateFunc: validation.StringLenBetween(0, 255),
	}
}

func resourceNetworkPortsSchema() *schema.Schema {
	return &schema.Schema{
		Type: schema.TypeSet,
		Optional: true,
		Elem: &schema.Resource{Schema: map[string]*schema.Schema{
			"start": {Type: schema.TypeInt, Optional: true, ValidateFunc: validation.IntBetween(1, 65535)},
			"end": {Type: schema.TypeInt, Optional: true, ValidateFunc: validation.IntBetween(1, 65535)},
		}},
	}
}

func flattenNetworkPorts(_ []int) []any {
	return []any{map[string]any{"start": 5000, "end": 5005}}
}

type fakeResourceData struct{}

func (fakeResourceData) Set(string, any) error { return nil }

func resourceNetworkServicesCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	_ = d.Get("ip_addresses")
	_ = d.Get("src_tcp_ports")
	return resourceNetworkServicesRead(ctx, d, meta)
}

func resourceNetworkServicesRead(_ context.Context, d *schema.ResourceData, _ any) diag.Diagnostics {
	var resp struct {
		Tag          string
		IPAddresses  []string
		SrcTCPPorts []int
	}
	var decoy fakeResourceData
	_ = decoy.Set("spoofed", resp.Tag)
	dynamicKey := "dynamic"
	_ = d.Set(dynamicKey, resp.Tag)
	_ = d.Set("tag", resp.Tag)
	_ = d.Set("ip_addresses", resp.IPAddresses)
	if err := d.Set("src_tcp_ports", flattenNetworkPorts(resp.SrcTCPPorts)); err != nil {
		return diag.FromErr(err)
	}
	return nil
}
`)
	if includeAcceptance {
		files = append(files, "zia/resource_zia_fw_filtering_network_services_test.go")
		writeFieldWitnessFile(t, root, "zia/resource_zia_fw_filtering_network_services_test.go", "package zia\n\n"+
			"import (\n\t\"fmt\"\n\t\"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource\"\n)\n\n"+
			"func acceptanceConfig() string {\n\treturn fmt.Sprintf(`\n"+
			"resource \"%s\" \"test\" {\n"+
			"  depends_on = []\n"+
			"  ip_addresses = [\"192.0.2.1\", \"192.0.2.2\", \"192.0.2.3\"]\n"+
			"  src_tcp_ports {\n    start = 5000\n  }\n"+
			"  src_tcp_ports {\n    start = 5001\n  }\n"+
			"  src_tcp_ports {\n    start = 5002\n    end = 5005\n  }\n"+
			"}\n"+
			"data \"%s\" \"test\" { id = \"${%s.test.id}\" }\n"+
			"`, \"zia_firewall_filtering_network_service\", \"zia_firewall_filtering_network_service\", \"zia_firewall_filtering_network_service\")\n}\n\n"+
			"func acceptanceChecks() {\n"+
			"\t_ = resource.TestCheckResourceAttr(\"zia_firewall_filtering_network_service.test\", \"ip_addresses.#\", \"3\")\n"+
			"\t_ = resource.TestCheckResourceAttr(\"zia_firewall_filtering_network_service.test\", \"src_tcp_ports.#\", \"3\")\n"+
			"\t_ = resource.TestCheckResourceAttr(\"other_resource.test\", \"src_tcp_ports.#\", \"99\")\n}\n")
	}
	writeFieldWitnessFile(t, root, "provider-schema.json", schemaJSON)

	inputs, err := sourcebind.LoadUnverified(context.Background(), sourcebind.UnverifiedRoots{
		ProviderRoot:       root,
		ProviderModulePath: "example.invalid/terraform-provider-zia",
		ProviderFiles:      files,
		SchemaRoot:         root,
		TerraformSchema:    "provider-schema.json",
		SDKRoots:           map[string]string{},
		SDKFiles:           map[string][]string{},
		SDKVersions:        map[string]string{},
		Selection: contracts.SelectionBinding{
			ResourceTypes: []string{fieldWitnessResource},
			Filters:       []contracts.SelectionFilterBinding{},
		},
	})
	if err != nil {
		t.Fatalf("sourcebind.LoadUnverified() error = %v, want nil", err)
	}
	return inputs
}

func writeFieldWitnessFile(t *testing.T, root, name, contents string) {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", name, err)
	}
	if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", name, err)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasFieldWitnessDiagnostic(values []FieldWitnessDiagnostic, code string) bool {
	for _, value := range values {
		if value.Code == code {
			return true
		}
	}
	return false
}

const fieldWitnessSchema = `{
  "format_version": "1.0",
  "provider_schemas": {
    "registry.terraform.io/zscaler/zia": {
      "resource_schemas": {
        "zia_firewall_filtering_network_service": {
          "block": {
            "attributes": {
              "ip_addresses": {"type": ["set", "string"], "optional": true},
              "tag": {"type": "string", "optional": true, "computed": true}
            },
            "block_types": {
              "src_tcp_ports": {
                "nesting_mode": "set",
                "block": {
                  "attributes": {
                    "start": {"type": "number", "optional": true},
                    "end": {"type": "number", "optional": true}
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}
`
