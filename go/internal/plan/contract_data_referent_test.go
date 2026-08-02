package plan

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

const dataReferenceType = "sample_groups_data"

const terraformRemoteStateReferenceType = "terraform_remote_state"

const providerDoubleReferenceType = "capture_item"

var providerDoubleCaptureTimestamp = regexp.MustCompile(`"timestamp":"[^"]*"`)

func dataReferenceAssessmentPlan(resources []any, configurationMode string, ids map[string]any) map[string]any {
	configurationAddress := "data." + dataReferenceType + ".items"
	configurationResource := map[string]any{
		"address": configurationAddress,
		"mode":    "data",
		"type":    dataReferenceType,
		"name":    "items",
	}
	if configurationMode == "managed" {
		configurationResource = map[string]any{
			"address": dataReferenceType + ".this",
			"mode":    "managed",
			"type":    dataReferenceType,
			"name":    "this",
		}
	}
	value := map[string]any{dataReferenceType: ids}
	plannedRoot := map[string]any{
		"child_modules": []any{
			map[string]any{
				"address":   "module." + dataReferenceType,
				"resources": resources,
			},
		},
	}
	if configurationMode == "data" {
		plannedRoot = map[string]any{}
	}
	plan := map[string]any{
		"format_version": "1.2",
		"complete":       true,
		"errored":        false,
		"planned_values": map[string]any{
			"outputs": map[string]any{
				infrawrightReferenceOutput: map[string]any{
					"sensitive": true,
					"value":     value,
				},
			},
			"root_module": plannedRoot,
		},
		"configuration": map[string]any{
			"root_module": map[string]any{
				"module_calls": map[string]any{
					dataReferenceType: map[string]any{
						"module": map[string]any{
							"resources": []any{configurationResource},
						},
					},
				},
			},
		},
		"resource_changes": []any{},
		"output_changes": map[string]any{
			infrawrightReferenceOutput: map[string]any{
				"actions":          []any{"create"},
				"before":           nil,
				"after":            value,
				"before_sensitive": false,
				"after_sensitive":  true,
				"after_unknown":    false,
			},
		},
	}
	if configurationMode == "data" {
		plan["prior_state"] = map[string]any{
			"values": map[string]any{
				"root_module": map[string]any{
					"child_modules": []any{
						map[string]any{
							"address":   "module." + dataReferenceType,
							"resources": resources,
						},
					},
				},
			},
		}
	}
	return plan
}

func dataReferenceResource(mode, address, index string, id any, resourceType string) map[string]any {
	name := "this"
	if mode == "data" {
		name = "items"
	}
	return map[string]any{
		"address": address,
		"index":   index,
		"mode":    mode,
		"name":    name,
		"type":    resourceType,
		"values":  map[string]any{"id": id, "name": "Group One"},
	}
}

func validDataReferenceResource(index string, id any) map[string]any {
	return dataReferenceResource(
		"data",
		`module.sample_groups_data.data.sample_groups_data.items["`+index+`"]`,
		index,
		id,
		dataReferenceType,
	)
}

func validDataReferencePlan(id any) map[string]any {
	return dataReferenceAssessmentPlan(
		[]any{validDataReferenceResource("group_one", id)},
		"data",
		map[string]any{"group_one": id},
	)
}

func testQualifiedPlanAttestation(refresh bool) *PlanCreationAttestation {
	refreshArgument := "-refresh=true"
	if !refresh {
		refreshArgument = "-refresh=false"
	}
	return &PlanCreationAttestation{
		FormatVersion:    PlanCreationAttestationVersion,
		TerraformVersion: "1.15.4",
		PlanArgv:         []string{"plan", "-input=false", refreshArgument, "-out=tfplan"},
		Refresh:          refresh,
		PlanSHA256:       strings.Repeat("a", 64),
	}
}

func dataReferenceContract() *AssessmentPlanContract {
	return dataReferenceContractFor(dataReferenceType)
}

func dataReferenceContractFor(resourceType string) *AssessmentPlanContract {
	return &AssessmentPlanContract{
		ReferenceOutputTypes: []ReferenceOutputType{{
			Type: resourceType, Kind: ReferenceOutputKindData,
		}},
		PlanAttestation: testQualifiedPlanAttestation(true),
	}
}

func requireAssessmentPlanErrorContaining(t *testing.T, planValue any, contract *AssessmentPlanContract, want string) {
	t.Helper()
	err := ValidateAssessmentPlan(planValue, contract)
	if err == nil {
		t.Fatalf("ValidateAssessmentPlan(%v) error = nil, want an error containing %q", planValue, want)
	}
	var failure *AssessmentPlanError
	if !errors.As(err, &failure) {
		t.Fatalf("ValidateAssessmentPlan(%v) error = %v (%T), want *AssessmentPlanError", planValue, err, err)
	}
	if !strings.Contains(failure.Error(), want) {
		t.Errorf("ValidateAssessmentPlan(%v) error = %q, want substring %q", planValue, failure.Error(), want)
	}
}

func offlineTerraformShowCapture(t *testing.T, scenario string) map[string]any {
	t.Helper()
	fixtureDirectory := filepath.Join("testdata", "offline_remote_state_capture")
	if scenario != "" {
		fixtureDirectory = filepath.Join(fixtureDirectory, scenario)
	}
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

func providerDoubleShowCaptureBytes(t *testing.T, scenario string) []byte {
	t.Helper()
	fixtureDirectory := filepath.Join("testdata", "provider_double_capture", scenario)
	raw, err := os.ReadFile(filepath.Join(fixtureDirectory, "show.json"))
	if err != nil {
		// Committed captures are mandatory promotion evidence: a missing
		// fixture FAILS (regenerate with make regen-plan-captures); it never skips,
		// so weakening the fixture set cannot pass the focused gate.
		t.Fatalf("ReadFile(%s/show.json) error = %v, want the committed capture (run make regen-plan-captures)", fixtureDirectory, err)
	}
	return raw
}

func parseProviderDoubleShowCapture(t *testing.T, scenario string, raw []byte) map[string]any {
	t.Helper()
	fixtureDirectory := filepath.Join("testdata", "provider_double_capture", scenario)
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

func providerDoubleShowCapture(t *testing.T, scenario string) map[string]any {
	t.Helper()
	return parseProviderDoubleShowCapture(t, scenario, providerDoubleShowCaptureBytes(t, scenario))
}

func providerDoubleCaptureWithoutTimestamp(t *testing.T, raw []byte) []byte {
	t.Helper()
	matches := providerDoubleCaptureTimestamp.FindAllIndex(raw, -1)
	if len(matches) != 1 {
		t.Fatalf("provider-double capture timestamp matches = %d, want exactly one", len(matches))
	}
	return providerDoubleCaptureTimestamp.ReplaceAll(raw, []byte(`"timestamp":"<timestamp>"`))
}

func providerDoubleDataReferenceContract() *AssessmentPlanContract {
	return &AssessmentPlanContract{
		ReferenceOutputTypes: []ReferenceOutputType{{
			Type: providerDoubleReferenceType,
			Kind: ReferenceOutputKindData,
		}},
		PlanAttestation: testQualifiedPlanAttestation(true),
	}
}

func TestValidateAssessmentPlanAcceptsProviderDoubleCaptures(t *testing.T) {
	for _, scenario := range []string{
		"initial_create",
		"refresh_id_change",
		"no_op",
		"empty_for_each",
	} {
		t.Run(scenario, func(t *testing.T) {
			show := providerDoubleShowCapture(t, scenario)
			if scenario == "initial_create" && providerDoubleResourceCount(t, show) != 2 {
				t.Fatal("initial_create capture must carry two items (regenerate with make regen-plan-captures)")
			}
			requireValidAssessmentPlan(
				t,
				"ValidateAssessmentPlan(provider-double "+scenario+")",
				show,
				providerDoubleDataReferenceContract(),
			)
		})
	}
}

func providerDoubleResourceCount(t *testing.T, show map[string]any) int {
	t.Helper()
	priorState, ok := show["prior_state"].(map[string]any)
	if !ok {
		return 0
	}
	values, ok := priorState["values"].(map[string]any)
	if !ok {
		return 0
	}
	root, ok := values["root_module"].(map[string]any)
	if !ok {
		return 0
	}
	children, ok := root["child_modules"].([]any)
	if !ok || len(children) != 1 {
		return 0
	}
	child, ok := children[0].(map[string]any)
	if !ok {
		return 0
	}
	resources, ok := child["resources"].([]any)
	if !ok {
		return 0
	}
	return len(resources)
}

func TestValidateAssessmentPlanRejectsProviderDoubleRekeyCapture(t *testing.T) {
	show := providerDoubleShowCapture(t, "rekey_refusal")
	requireAssessmentPlanErrorContaining(
		t,
		show,
		providerDoubleDataReferenceContract(),
		"provider-observed resource IDs",
	)
}

func TestValidateAssessmentPlanProviderDoubleRefreshFlagIndependenceProof(t *testing.T) {
	refreshFalseRaw := providerDoubleShowCaptureBytes(t, "refresh_false")
	refreshFalse := parseProviderDoubleShowCapture(t, "refresh_false", refreshFalseRaw)
	refreshFalseContract := providerDoubleDataReferenceContract()
	refreshFalseContract.PlanAttestation = testQualifiedPlanAttestation(false)
	// Terraform 1.15.4 reads known-input data sources during both plans. The
	// refresh=false capture is therefore accepted as provider-observed evidence;
	// matching bytes after timestamp normalization are the empirical proof that
	// the flag does not create a different observation lane here.
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(provider-double refresh_false)",
		refreshFalse,
		refreshFalseContract,
	)

	refreshTrueRaw := providerDoubleShowCaptureBytes(t, "refresh_true")
	refreshTrue := parseProviderDoubleShowCapture(t, "refresh_true", refreshTrueRaw)
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(provider-double refresh_true)",
		refreshTrue,
		providerDoubleDataReferenceContract(),
	)

	refreshFalseTimestamp, falseTimestampOK := refreshFalse["timestamp"].(string)
	refreshTrueTimestamp, trueTimestampOK := refreshTrue["timestamp"].(string)
	if !falseTimestampOK || !trueTimestampOK || refreshFalseTimestamp == "" || refreshTrueTimestamp == "" {
		t.Fatalf("refresh flag proof timestamps = (%#v, %#v), want non-empty strings", refreshFalse["timestamp"], refreshTrue["timestamp"])
	}
	// The byte comparison is only meaningful once BOTH captures pin the full
	// semantic matrix of the independence claim; otherwise a degenerate pair
	// (e.g. two empty error documents) would compare equal and prove nothing.
	assertRefreshPairSemantics(t, "refresh_false", refreshFalse)
	assertRefreshPairSemantics(t, "refresh_true", refreshTrue)
	if diff := cmp.Diff(
		string(providerDoubleCaptureWithoutTimestamp(t, refreshTrueRaw)),
		string(providerDoubleCaptureWithoutTimestamp(t, refreshFalseRaw)),
	); diff != "" {
		t.Errorf("refresh=false and refresh=true capture bytes differ beyond timestamp (-refresh_true +refresh_false):\n%s", diff)
	}
}

// providerDoubleRefreshPair pins the deterministic provider-double IDs the
// refresh pair must observe: v1 was applied, v2 is observed during both
// plans (the refresh flag does not create a different observation lane).
const (
	providerDoubleRefreshPairBeforeID = "8a3fc945636370e1"
	providerDoubleRefreshPairAfterID  = "018da47922f5094d"
)

func assertRefreshPairSemantics(t *testing.T, scenario string, show map[string]any) {
	t.Helper()
	if got, _ := show["terraform_version"].(string); got != "1.15.4" {
		t.Errorf("%s terraform_version = %#v, want 1.15.4", scenario, show["terraform_version"])
	}
	if got, _ := show["format_version"].(string); got != "1.2" {
		t.Errorf("%s format_version = %#v, want exactly 1.2", scenario, show["format_version"])
	}
	if complete, _ := show["complete"].(bool); !complete {
		t.Errorf("%s complete = %#v, want true", scenario, show["complete"])
	}
	if errored, _ := show["errored"].(bool); errored {
		t.Errorf("%s errored = %#v, want false", scenario, show["errored"])
	}
	// Exactly the postcondition check the data module declares must be
	// present and passing at both the top level and every instance: an
	// absent checks section would silently drop the postcondition evidence.
	checks, _ := show["checks"].([]any)
	if len(checks) != 1 {
		t.Errorf("%s checks length = %d, want exactly the data-module postcondition check", scenario, len(checks))
	} else {
		check, _ := checks[0].(map[string]any)
		checkAddress, _ := check["address"].(map[string]any)
		if display, _ := checkAddress["to_display"].(string); display != "module.capture_item.data.capture_item.items" {
			t.Errorf("%s check address = %#v, want the data-module postcondition", scenario, checkAddress["to_display"])
		}
		if status, _ := check["status"].(string); status != "pass" {
			t.Errorf("%s check status = %#v, want pass", scenario, check["status"])
		}
		instances, _ := check["instances"].([]any)
		if len(instances) != 1 {
			t.Errorf("%s check instances = %d, want 1", scenario, len(instances))
		}
		for _, rawInstance := range instances {
			instance, _ := rawInstance.(map[string]any)
			if status, _ := instance["status"].(string); status != "pass" {
				t.Errorf("%s check instance status = %#v, want pass", scenario, instance["status"])
			}
		}
	}
	outputChanges, _ := show["output_changes"].(map[string]any)
	change, _ := outputChanges["iw_reference_ids"].(map[string]any)
	actions, _ := change["actions"].([]any)
	if len(actions) != 1 || actions[0] != "update" {
		t.Errorf("%s iw_reference_ids actions = %#v, want [update]", scenario, change["actions"])
	}
	wantBefore := map[string]any{"capture_item": map[string]any{"group_one": providerDoubleRefreshPairBeforeID}}
	wantAfter := map[string]any{"capture_item": map[string]any{"group_one": providerDoubleRefreshPairAfterID}}
	if diff := cmp.Diff(wantBefore, change["before"]); diff != "" {
		t.Errorf("%s output before mismatch (-want +got):\n%s", scenario, diff)
	}
	if diff := cmp.Diff(wantAfter, change["after"]); diff != "" {
		t.Errorf("%s output after mismatch (-want +got):\n%s", scenario, diff)
	}
	priorIDs := providerDoublePriorStateDataIDs(t, show)
	wantPrior := map[string]any{
		`module.capture_item.data.capture_item.items["group_one"]`: providerDoubleRefreshPairAfterID,
	}
	if diff := cmp.Diff(wantPrior, priorIDs); diff != "" {
		t.Errorf("%s prior-state data IDs mismatch (-want +got):\n%s", scenario, diff)
	}
	// The planned output must already carry the v2 map: a capture whose
	// planned value contradicts its output change is not coherent evidence.
	plannedValues, _ := show["planned_values"].(map[string]any)
	plannedOutputs, _ := plannedValues["outputs"].(map[string]any)
	plannedReference, _ := plannedOutputs["iw_reference_ids"].(map[string]any)
	if diff := cmp.Diff(wantAfter, plannedReference["value"]); diff != "" {
		t.Errorf("%s planned output value mismatch (-want +got):\n%s", scenario, diff)
	}
	// The prior-state resource itself is pinned field-for-field: provider,
	// schema version, requested name, and the deterministic observed ID.
	priorResource := providerDoublePriorStateResource(t, show, `module.capture_item.data.capture_item.items["group_one"]`)
	if provider, _ := priorResource["provider_name"].(string); provider != "registry.terraform.io/infrawright/capture" {
		t.Errorf("%s prior resource provider_name = %#v, want the provider double", scenario, priorResource["provider_name"])
	}
	if schemaVersion, _ := priorResource["schema_version"].(json.Number); schemaVersion != json.Number("0") {
		t.Errorf("%s prior resource schema_version = %#v, want 0", scenario, priorResource["schema_version"])
	}
	priorValues, _ := priorResource["values"].(map[string]any)
	wantValues := map[string]any{"id": providerDoubleRefreshPairAfterID, "name": "Location Group"}
	if diff := cmp.Diff(wantValues, priorValues); diff != "" {
		t.Errorf("%s prior resource values mismatch (-want +got):\n%s", scenario, diff)
	}
	if planned := plannedDataResourceCount(show); planned != 0 {
		t.Errorf("%s planned data resources = %d, want 0", scenario, planned)
	}
	if changes := nonNoOpResourceChangeCount(show); changes != 0 {
		t.Errorf("%s non-no-op resource changes = %d, want 0", scenario, changes)
	}
}

func providerDoublePriorStateResource(t *testing.T, show map[string]any, address string) map[string]any {
	t.Helper()
	priorState, _ := show["prior_state"].(map[string]any)
	values, _ := priorState["values"].(map[string]any)
	root, _ := values["root_module"].(map[string]any)
	children, _ := root["child_modules"].([]any)
	for _, rawChild := range children {
		child, _ := rawChild.(map[string]any)
		resources, _ := child["resources"].([]any)
		for _, rawResource := range resources {
			resource, _ := rawResource.(map[string]any)
			if resource["address"] == address {
				return resource
			}
		}
	}
	t.Fatalf("prior-state resource %s not found", address)
	return nil
}

func providerDoublePriorStateDataIDs(t *testing.T, show map[string]any) map[string]any {
	t.Helper()
	ids := map[string]any{}
	priorState, _ := show["prior_state"].(map[string]any)
	values, _ := priorState["values"].(map[string]any)
	root, _ := values["root_module"].(map[string]any)
	children, _ := root["child_modules"].([]any)
	for _, rawChild := range children {
		child, _ := rawChild.(map[string]any)
		resources, _ := child["resources"].([]any)
		for _, rawResource := range resources {
			resource, _ := rawResource.(map[string]any)
			if resource["mode"] != "data" {
				continue
			}
			resourceValues, _ := resource["values"].(map[string]any)
			address, _ := resource["address"].(string)
			ids[address] = resourceValues["id"]
		}
	}
	return ids
}

func plannedDataResourceCount(show map[string]any) int {
	planned, _ := show["planned_values"].(map[string]any)
	root, _ := planned["root_module"].(map[string]any)
	count := 0
	children, _ := root["child_modules"].([]any)
	for _, rawChild := range children {
		child, _ := rawChild.(map[string]any)
		resources, _ := child["resources"].([]any)
		count += len(resources)
	}
	resources, _ := root["resources"].([]any)
	return count + len(resources)
}

func nonNoOpResourceChangeCount(show map[string]any) int {
	changes, _ := show["resource_changes"].([]any)
	count := 0
	for _, rawChange := range changes {
		change, _ := rawChange.(map[string]any)
		inner, _ := change["change"].(map[string]any)
		actions, _ := inner["actions"].([]any)
		if len(actions) == 1 && actions[0] == "no-op" {
			continue
		}
		count++
	}
	return count
}

func TestValidateAssessmentPlanDataNoOpDoesNotRequireAuthorization(t *testing.T) {
	plan := providerDoubleShowCapture(t, "no_op")
	delete(plan, "prior_state")
	contract := providerDoubleDataReferenceContract()
	contract.PlanAttestation = nil
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(provider-double no-op without evidence)",
		plan,
		contract,
	)
}

func TestValidateAssessmentPlanDataAuthorizationAttestationBoundary(t *testing.T) {
	contract := dataReferenceContract()
	contract.PlanAttestation = nil
	requireAssessmentPlanErrorContaining(
		t,
		validDataReferencePlan("101"),
		contract,
		"plan creation attestation",
	)

	contract = dataReferenceContract()
	contract.PlanAttestation = testQualifiedPlanAttestation(false)
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(data reference with refresh=false attestation)",
		validDataReferencePlan("101"),
		contract,
	)
}

func TestValidateAssessmentPlanManagedAuthorizationChecksPresentAttestation(t *testing.T) {
	contract := referenceContract()
	contract.PlanAttestation = testQualifiedPlanAttestation(false)
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(managed reference with refresh=false attestation)",
		referenceAssessmentPlan("create"),
		contract,
	)
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(managed reference without attestation)",
		referenceAssessmentPlan("create"),
		referenceContract(),
	)
}

func TestValidateAssessmentPlanReferenceOutputModeAuthorization(t *testing.T) {
	managed := referenceAssessmentPlan("create")
	managedChild := managed["planned_values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)
	managedChild["resources"].([]any)[0] = dataReferenceResource(
		"data",
		`module.zpa_segment_group.data.zpa_segment_group.items["segment_one"]`,
		"segment_one",
		"72059380790653545",
		"zpa_segment_group",
	)
	requireAssessmentPlanErrorContaining(t, managed, referenceContract(), "unauthorized mode")

	managedForData := validDataReferencePlan("101")
	dataChild := managedForData["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)
	dataChild["resources"].([]any)[0] = dataReferenceResource(
		"managed",
		`module.sample_groups_data.sample_groups_data.this["group_one"]`,
		"group_one",
		"101",
		dataReferenceType,
	)
	requireAssessmentPlanErrorContaining(t, managedForData, dataReferenceContract(), "unauthorized mode")

	mixed := validDataReferencePlan(json.Number("101"))
	mixedChild := mixed["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)
	mixedChild["resources"] = []any{
		validDataReferenceResource("group_one", json.Number("101")),
		dataReferenceResource(
			"managed",
			`module.sample_groups_data.sample_groups_data.this["group_two"]`,
			"group_two",
			"102",
			dataReferenceType,
		),
	}
	mixed["planned_values"].(map[string]any)["outputs"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)["value"] = map[string]any{
		dataReferenceType: map[string]any{
			"group_one": json.Number("101"),
			"group_two": "102",
		},
	}
	mixed["output_changes"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)["after"] = map[string]any{
		dataReferenceType: map[string]any{
			"group_one": json.Number("101"),
			"group_two": "102",
		},
	}
	requireAssessmentPlanErrorContaining(t, mixed, dataReferenceContract(), "unauthorized mode")

	managedEmpty := emptyReferenceAssessmentPlan()
	managedEmptyConfig := managedEmpty["configuration"].(map[string]any)["root_module"].(map[string]any)["module_calls"].(map[string]any)["zpa_segment_group"].(map[string]any)["module"].(map[string]any)["resources"].([]any)
	managedEmptyConfig[0] = map[string]any{
		"address": "data.zpa_segment_group.items",
		"mode":    "data",
		"type":    "zpa_segment_group",
		"name":    "items",
	}
	requireAssessmentPlanErrorContaining(t, managedEmpty, referenceContract(), "empty reference output authorization requires")

	dataEmpty := dataReferenceAssessmentPlan([]any{}, "managed", map[string]any{})
	requireAssessmentPlanErrorContaining(t, dataEmpty, dataReferenceContract(), "empty reference output authorization requires")
	dataEmptyValid := dataReferenceAssessmentPlan([]any{}, "data", map[string]any{})
	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(valid empty data reference)", dataEmptyValid, dataReferenceContract())

	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(valid managed reference)", referenceAssessmentPlan("create"), referenceContract())
	requireValidAssessmentPlan(t, "ValidateAssessmentPlan(valid data reference)", validDataReferencePlan(json.Number("101")), dataReferenceContract())
}

func TestValidateAssessmentPlanDoesNotAuthorizeResourceChanges(t *testing.T) {
	plan := dataReferenceAssessmentPlan([]any{}, "data", map[string]any{})
	resource := validDataReferenceResource("group_one", json.Number("101"))
	plan["resource_changes"] = []any{
		map[string]any{
			"address": resource["address"],
			"type":    dataReferenceType,
			"change": map[string]any{
				"actions": []any{"read"},
				"before":  nil,
				"after":   resource["values"],
			},
		},
	}
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(resource_changes-only data evidence)",
		plan,
		dataReferenceContract(),
	)
}

func twoItemDataReferencePlan() map[string]any {
	return dataReferenceAssessmentPlan(
		[]any{
			validDataReferenceResource("group_a", "id1"),
			validDataReferenceResource("group_b", "id2"),
		},
		"data",
		map[string]any{"group_a": "id1", "group_b": "id2"},
	)
}

func setDataReferenceOutputAfter(plan map[string]any, ids map[string]any) {
	change := plan["output_changes"].(map[string]any)[infrawrightReferenceOutput].(map[string]any)
	change["after"] = map[string]any{dataReferenceType: ids}
}

func setDataReferencePriorOutput(plan map[string]any, ids map[string]any) {
	priorValues := plan["prior_state"].(map[string]any)["values"].(map[string]any)
	priorValues["outputs"] = map[string]any{
		infrawrightReferenceOutput: map[string]any{
			"sensitive": true,
			"value":     map[string]any{dataReferenceType: ids},
		},
	}
}

func TestValidateAssessmentPlanBindsDataReferenceKeysToPriorStateIDs(t *testing.T) {
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(two-item exact data reference positive)",
		twoItemDataReferencePlan(),
		dataReferenceContract(),
	)

	for _, test := range []struct {
		name  string
		after map[string]any
	}{
		{
			name: "two-item swap",
			after: map[string]any{
				"group_a": "id2",
				"group_b": "id1",
			},
		},
		{
			name: "renamed keys",
			after: map[string]any{
				"renamed_a": "id1",
				"renamed_b": "id2",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := twoItemDataReferencePlan()
			setDataReferenceOutputAfter(plan, test.after)
			setDataReferencePriorOutput(plan, test.after)
			requireAssessmentPlanErrorContaining(
				t,
				plan,
				dataReferenceContract(),
				"provider-observed resource IDs",
			)
		})
	}
}

func TestValidateAssessmentPlanDataReferenceEvidenceIsExactAndScalar(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "address_index_mismatch",
			mutate: func(plan map[string]any) {
				resource := plan["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["address"] = `module.sample_groups_data.data.sample_groups_data.items["real_key"]`
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "trailing_address_material",
			mutate: func(plan map[string]any) {
				resource := plan["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["address"] = `module.sample_groups_data.data.sample_groups_data.items["group_one"].trailing`
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "boolean_id",
			mutate: func(plan map[string]any) {
				resource := plan["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["values"].(map[string]any)["id"] = false
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "object_id",
			mutate: func(plan map[string]any) {
				resource := plan["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["values"].(map[string]any)["id"] = map[string]any{"nested": true}
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "array_id",
			mutate: func(plan map[string]any) {
				resource := plan["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["values"].(map[string]any)["id"] = []any{"nested"}
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "null_id",
			mutate: func(plan map[string]any) {
				resource := plan["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["values"].(map[string]any)["id"] = nil
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "wrong_type",
			mutate: func(plan map[string]any) {
				resource := plan["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["type"] = "other_data_source"
			},
			want: "engine reference output does not match provider-observed resource IDs",
		},
		{
			name: "wrong_name",
			mutate: func(plan map[string]any) {
				resource := plan["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["name"] = "other"
			},
			want: "invalid reference-output resource instance",
		},
		{
			name: "wrong_module_address",
			mutate: func(plan map[string]any) {
				resource := plan["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)["resources"].([]any)[0].(map[string]any)
				resource["address"] = `module.other.data.sample_groups_data.items["group_one"]`
			},
			want: "invalid reference-output resource instance",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validDataReferencePlan(json.Number("101"))
			test.mutate(plan)
			requireAssessmentPlanErrorContaining(t, plan, dataReferenceContract(), test.want)
		})
	}

	for _, test := range []struct {
		name string
		id   any
	}{
		{name: "string_id", id: "101"},
		{name: "numeric_id", id: json.Number("101")},
	} {
		t.Run("valid_"+test.name, func(t *testing.T) {
			requireValidAssessmentPlan(t, "ValidateAssessmentPlan(valid data ID)", validDataReferencePlan(test.id), dataReferenceContract())
		})
	}

	duplicate := validDataReferencePlan(json.Number("101"))
	duplicateChild := duplicate["prior_state"].(map[string]any)["values"].(map[string]any)["root_module"].(map[string]any)["child_modules"].([]any)[0].(map[string]any)
	duplicateChild["resources"] = []any{
		validDataReferenceResource("group_one", json.Number("101")),
		validDataReferenceResource("group_one", json.Number("102")),
	}
	requireAssessmentPlanErrorContaining(t, duplicate, dataReferenceContract(), "duplicate reference-output key")
}

// TestValidateAssessmentPlanRejectsOfflineTerraformConfiguredDefaults pins the
// builtin terraform_remote_state fixture as a negative. Its configured
// defaults.id is not provider-observed prior_state evidence: the initial-create
// prior_state root is empty, while the configured defaults object exists only
// in planned_values. It must not be promoted into a positive data-reference
// contract.
func TestValidateAssessmentPlanRejectsOfflineTerraformConfiguredDefaults(t *testing.T) {
	requireAssessmentPlanErrorContaining(
		t,
		offlineTerraformShowCapture(t, "initial_create"),
		dataReferenceContractFor(terraformRemoteStateReferenceType),
		"engine reference output does not match provider-observed resource IDs",
	)
}

func TestValidateAssessmentPlanAcceptsOfflineTerraformPriorStateOnlyNoOp(t *testing.T) {
	plan := offlineTerraformShowCapture(t, "")
	plannedValues := plan["planned_values"].(map[string]any)
	plannedRoot := plannedValues["root_module"].(map[string]any)
	if len(plannedRoot) != 0 {
		t.Fatalf("offline refreshed/no-op planned_values.root_module = %#v, want empty authoritative container", plannedRoot)
	}
	priorState := plan["prior_state"].(map[string]any)
	priorValues := priorState["values"].(map[string]any)
	priorRoot := priorValues["root_module"].(map[string]any)
	priorChildren := priorRoot["child_modules"].([]any)
	if len(priorChildren) != 1 {
		t.Fatalf("offline refreshed/no-op prior_state child_modules = %#v, want one data child", priorRoot["child_modules"])
	}
	priorChild := priorChildren[0].(map[string]any)
	priorResources := priorChild["resources"].([]any)
	if len(priorResources) != 1 {
		t.Fatalf("offline refreshed/no-op prior_state resources = %#v, want one data resource", priorChild["resources"])
	}
	priorResource := priorResources[0].(map[string]any)
	if priorResource["mode"] != "data" || priorResource["type"] != terraformRemoteStateReferenceType {
		t.Fatalf("offline refreshed/no-op prior_state resource = %#v, want builtin data resource", priorResource)
	}
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(real offline terraform prior-state-only no-op)",
		plan,
		dataReferenceContractFor(terraformRemoteStateReferenceType),
	)
}

func TestValidateAssessmentPlanAcceptsOfflineTerraformEmptyForEach(t *testing.T) {
	requireValidAssessmentPlan(
		t,
		"ValidateAssessmentPlan(real offline terraform empty for_each data shape)",
		offlineTerraformShowCapture(t, "empty_for_each"),
		dataReferenceContractFor(terraformRemoteStateReferenceType),
	)
}

func TestValidateAssessmentPlanDoesNotAuthorizePlannedValuesOrResourceChanges(t *testing.T) {
	plan := validDataReferencePlan(json.Number("101"))
	priorState := plan["prior_state"].(map[string]any)
	priorValues := priorState["values"].(map[string]any)
	priorRoot := priorValues["root_module"].(map[string]any)
	priorRoot["child_modules"] = []any{}
	plannedValues := plan["planned_values"].(map[string]any)
	plannedRoot := plannedValues["root_module"].(map[string]any)
	plannedRoot["child_modules"] = []any{
		map[string]any{
			"address":   "module." + dataReferenceType,
			"resources": []any{validDataReferenceResource("group_one", json.Number("101"))},
		},
	}
	plan["resource_changes"] = []any{
		map[string]any{
			"address": "module." + dataReferenceType + ".data." + dataReferenceType + `.items["group_one"]`,
			"type":    dataReferenceType,
			"change": map[string]any{
				"actions": []any{"read"},
				"before":  nil,
				"after":   map[string]any{"id": json.Number("101")},
			},
		},
	}
	requireAssessmentPlanErrorContaining(
		t,
		plan,
		dataReferenceContract(),
		"engine reference output does not match provider-observed resource IDs",
	)
}
