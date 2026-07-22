package adopt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type terraformImportStructureFixture struct {
	Plan  json.RawMessage `json:"plan"`
	State json.RawMessage `json:"state"`
}

func loadTerraformImportStructureFixture(t *testing.T) terraformImportStructureFixture {
	t.Helper()
	path := filepath.Join("..", "..", "..", "tests", "fixtures", "terraform-import-structure-v1.15.4.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed Terraform structural fixture: %v", err)
	}
	var fixture terraformImportStructureFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode committed Terraform structural fixture: %v", err)
	}
	return fixture
}

func TestCommittedTerraformImportStructureFixtureDecodesAndExtractsExactly(t *testing.T) {
	fixture := loadTerraformImportStructureFixture(t)
	typedPlan, rawPlan, err := DecodeOraclePlan(fixture.Plan)
	if err != nil {
		t.Fatalf("DecodeOraclePlan(fixture): %v", err)
	}
	if typedPlan.Complete == nil || !*typedPlan.Complete || rawPlan["complete"] != true {
		t.Fatalf("fixture complete gate typed=%v raw=%#v", typedPlan.Complete, rawPlan["complete"])
	}
	address := "terraform_data.fixture"
	objects, err := ExtractAcceptedPlanState(
		fixture.Plan,
		map[string]string{address: "fixture"},
		map[string]string{address: "structural-fixture-id"},
		"terraform.io/builtin/terraform",
		"terraform_data",
	)
	if err != nil {
		t.Fatalf("ExtractAcceptedPlanState(fixture): %v", err)
	}
	if object := objects["fixture"]; object.Address != address || object.Values["id"] != "structural-fixture-id" {
		t.Fatalf("accepted fixture object = %#v", object)
	}

	_, rawState, err := DecodeOracleState(fixture.State)
	if err != nil {
		t.Fatalf("DecodeOracleState(fixture): %v", err)
	}
	state, err := exactBatchStateObjects(rawState, map[string]expectedOracleInstance{
		address: {Key: "fixture", ProviderName: "terraform.io/builtin/terraform", ResourceType: "terraform_data"},
	})
	if err != nil {
		t.Fatalf("exactBatchStateObjects(fixture): %v", err)
	}
	if object := state["terraform_data"]["fixture"]; object.Address != address || object.Values["id"] != "structural-fixture-id" {
		t.Fatalf("state fixture object = %#v", object)
	}
}
