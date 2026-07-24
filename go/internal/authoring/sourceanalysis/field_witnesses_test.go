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
	if len(ports.AcceptanceConfigs) != 1 || ports.AcceptanceConfigs[0].Occurrences != 3 ||
		ports.AcceptanceConfigs[0].ParentInstances != 1 {
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
	if len(end.AcceptanceConfigs) != 1 || end.AcceptanceConfigs[0].Occurrences != 1 ||
		end.AcceptanceConfigs[0].ParentInstances != 3 ||
		!containsString(end.AcceptanceConfigs[0].Values, "5005") {
		t.Errorf("src_tcp_ports[].end config witnesses = %#v, want one end across three parent blocks", end.AcceptanceConfigs)
	}
	if len(end.AcceptanceChecks) != 0 {
		t.Errorf("src_tcp_ports[].end checks = %#v, want no invented round-trip assertion", end.AcceptanceChecks)
	}
	if _, exists := resource.Fields["spoofed"]; exists {
		t.Error("fields contain Set call on non-ResourceData receiver")
	}
	if !hasFieldWitnessDiagnostic(resource.Diagnostics, "read_back_key_dynamic") {
		t.Errorf("resource diagnostics = %#v, want dynamic d.Set key surfaced", resource.Diagnostics)
	}
}

func TestAnalyzeUnverifiedFieldWitnessesSurfacesSchemaConflict(t *testing.T) {
	conflicting := strings.Replace(fieldWitnessSchema, `"computed": true`, `"computed": false`, 1)
	report, err := AnalyzeUnverifiedFieldWitnesses(context.Background(), fieldWitnessInputs(t, conflicting, false))
	if err != nil {
		t.Fatalf("AnalyzeUnverifiedFieldWitnesses() error = %v, want nil", err)
	}
	tag := requireFieldWitness(t, report.Resources[fieldWitnessResource], "tag")
	if tag.Disposition != FieldWitnessConflicting {
		t.Fatalf("tag disposition = %q, want conflicting", tag.Disposition)
	}
	if len(tag.Conflicts) == 0 {
		t.Fatal("tag conflicts are empty, want explicit computed disagreement")
	}
	if len(tag.AcceptanceConfigs) != 0 || len(tag.AcceptanceChecks) != 0 {
		t.Errorf("tag acceptance witnesses = (%#v, %#v), want absent tests to remain silence", tag.AcceptanceConfigs, tag.AcceptanceChecks)
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
		ReadContext: resourceNetworkServicesRead,
		Schema: map[string]*schema.Schema{
			"tag": getCloudFirewallNetworkServicesTag(),
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

type fakeResourceData struct{}

func (fakeResourceData) Set(string, any) error { return nil }

func resourceNetworkServicesRead(_ context.Context, d *schema.ResourceData, _ any) diag.Diagnostics {
	var resp struct {
		Tag string
		SrcTCPPorts []int
	}
	var decoy fakeResourceData
	_ = decoy.Set("spoofed", resp.Tag)
	dynamicKey := "dynamic"
	_ = d.Set(dynamicKey, resp.Tag)
	_ = d.Set("tag", resp.Tag)
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
			"  src_tcp_ports {\n    start = 5000\n  }\n"+
			"  src_tcp_ports {\n    start = 5001\n  }\n"+
			"  src_tcp_ports {\n    start = 5002\n    end = 5005\n  }\n"+
			"}\n"+
			"data \"%s\" \"test\" { id = \"${%s.test.id}\" }\n"+
			"`, \"zia_firewall_filtering_network_service\", \"zia_firewall_filtering_network_service\", \"zia_firewall_filtering_network_service\")\n}\n\n"+
			"func acceptanceChecks() {\n"+
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
