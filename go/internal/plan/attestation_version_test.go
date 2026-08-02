package plan

import "testing"

// TestQualifiedTerraformVersionIsExact pins the promotion invariant: only
// releases the committed capture matrix has been run under are qualified.
// The 1.15.x-range acceptance this replaces would pass 1.15.0/1.15.999;
// these tables fail against that behavior.
func TestQualifiedTerraformVersionIsExact(t *testing.T) {
	attestation := func(version string) PlanCreationAttestation {
		a := *testQualifiedPlanAttestation(true)
		a.TerraformVersion = version
		return a
	}
	planSHA := testQualifiedPlanAttestation(true).PlanSHA256
	for _, version := range []string{"1.15.4"} {
		if err := validatePlanCreationAttestation(attestation(version), planSHA); err != nil {
			t.Errorf("validatePlanCreationAttestation(%s) error = %v, want nil", version, err)
		}
	}
	for _, version := range []string{"1.15.0", "1.15.3", "1.15.5", "1.15.999", "1.14.9", "1.16.0"} {
		if err := validatePlanCreationAttestation(attestation(version), planSHA); err == nil {
			t.Errorf("validatePlanCreationAttestation(%s) error = nil, want capture-qualification refusal", version)
		}
	}
	for _, testCase := range []struct {
		line string
		ok   bool
	}{
		{"Terraform v1.15.4", true},
		{"Terraform v1.15.0", false},
		{"Terraform v1.15.5", false},
		{"Terraform v1.15.999", false},
		{"Terraform v1.14.9", false},
		{"Terraform v1.16.0", false},
	} {
		_, err := terraformVersionFromOutput([]byte(testCase.line + "\non darwin_arm64\n"))
		if testCase.ok && err != nil {
			t.Errorf("terraformVersionFromOutput(%q) error = %v, want nil", testCase.line, err)
		}
		if !testCase.ok && err == nil {
			t.Errorf("terraformVersionFromOutput(%q) error = nil, want capture-qualification refusal", testCase.line)
		}
	}
}
