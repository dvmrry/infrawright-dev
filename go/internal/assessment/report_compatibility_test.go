package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const planReportCompatibilitySHA256 = "67959aeae67903936fad37defb617b27c3f7e8df283d46675bc89cb898b1206c"

type planReportCompatibilityCase struct {
	Name        string `json:"name"`
	OutputBytes string `json:"output_bytes"`
}

type planReportCompatibilityFixture struct {
	SchemaVersion int `json:"schema_version"`
	FloatCase     struct {
		Name        string `json:"name"`
		OutputBytes string `json:"output_bytes"`
		Token       string `json:"token"`
	} `json:"float_case"`
	PathCase struct {
		Input  []PlanPath `json:"input"`
		Name   string     `json:"name"`
		Output []string   `json:"output"`
	} `json:"path_case"`
	ReportCases []planReportCompatibilityCase `json:"report_cases"`
}

func loadPlanReportCompatibility(t *testing.T) planReportCompatibilityFixture {
	t.Helper()
	fixturePath := filepath.Join("testdata", "plan_report_compatibility.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", fixturePath, err)
	}
	digest := sha256.Sum256(fixtureBytes)
	if got := hex.EncodeToString(digest[:]); got != planReportCompatibilitySHA256 {
		t.Fatalf("SHA256(%q) = %q, want %q", fixturePath, got, planReportCompatibilitySHA256)
	}
	var fixture planReportCompatibilityFixture
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("json.Unmarshal(%q) error: %v", fixturePath, err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("%s schema_version = %d, want 1", fixturePath, fixture.SchemaVersion)
	}
	if got, want := len(fixture.ReportCases), 3; got != want {
		t.Fatalf("%s report cases = %d, want %d", fixturePath, got, want)
	}
	return fixture
}

func TestPlanReportCompatibility(t *testing.T) {
	fixture := loadPlanReportCompatibility(t)
	if fixture.PathCase.Name != "concrete-plan-paths" {
		t.Fatalf("path case name = %q, want %q", fixture.PathCase.Name, "concrete-plan-paths")
	}
	gotPaths := make([]string, len(fixture.PathCase.Input))
	for index, input := range fixture.PathCase.Input {
		gotPaths[index] = FormatConcretePlanPath(input)
	}
	if !reflect.DeepEqual(gotPaths, fixture.PathCase.Output) {
		t.Errorf("FormatConcretePlanPath(compatibility inputs) = %#v, want %#v", gotPaths, fixture.PathCase.Output)
	}
	for _, status := range []PlanStatus{Clean, Tolerated, Blocked} {
		report := buildReportForTest(t, status)
		got, err := RenderAssessmentReport(report)
		if err != nil {
			t.Fatalf("RenderAssessmentReport(%q) error: %v", status, err)
		}
		want := findPlanReportCompatibilityCase(t, fixture, string(status)).OutputBytes
		if got != want {
			t.Errorf("RenderAssessmentReport(%q) = %q, want %q", status, got, want)
		}
	}

	floatReport, err := BuildSavedPlanAssessmentReport(BuildSavedPlanAssessmentReportOptions{
		Mode: AssertAdoptable,
		Request: AssessmentReportRequest{
			Tenant: reportStringPointer("tenant"),
			Policy: reportStringPointer("policy.json"),
		},
		Core:     reportCore(Blocked),
		Guidance: reportGuidance(Blocked, json.Number("1.0")),
	})
	if err != nil {
		t.Fatalf("BuildSavedPlanAssessmentReport(float compatibility) error: %v", err)
	}
	got, err := RenderAssessmentReport(floatReport)
	if err != nil {
		t.Fatalf("RenderAssessmentReport(float compatibility) error: %v", err)
	}
	if fixture.FloatCase.Name != "guidance-float-provenance" || fixture.FloatCase.Token != "1.0" || got != fixture.FloatCase.OutputBytes {
		t.Errorf("RenderAssessmentReport(float compatibility) = %q, want token %q and bytes %q", got, fixture.FloatCase.Token, fixture.FloatCase.OutputBytes)
	}
}

func findPlanReportCompatibilityCase(t *testing.T, fixture planReportCompatibilityFixture, name string) planReportCompatibilityCase {
	t.Helper()
	var matches []planReportCompatibilityCase
	for _, candidate := range fixture.ReportCases {
		if candidate.Name == name {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("plan report compatibility case %q count = %d, want 1", name, len(matches))
	}
	return matches[0]
}
