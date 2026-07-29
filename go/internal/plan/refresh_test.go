package plan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

type refreshFakeTerraform struct {
	initialized  []PlanTerraformRequest
	planned      []RefreshPlanRequest
	shown        []RefreshShowRequest
	applied      []RefreshApplyRequest
	planJSON     string
	snapshotPath string
}

func (fake *refreshFakeTerraform) Initialize(request PlanTerraformRequest) error {
	fake.initialized = append(fake.initialized, request)
	return nil
}

func (fake *refreshFakeTerraform) PlanRefreshOnly(request RefreshPlanRequest) error {
	fake.planned = append(fake.planned, request)
	fake.snapshotPath = request.SnapshotPath
	return os.WriteFile(request.SnapshotPath, []byte("saved-refresh-plan"), 0o600)
}

func (fake *refreshFakeTerraform) Show(request RefreshShowRequest) (canonjson.Value, error) {
	fake.shown = append(fake.shown, request)
	return canonjson.ParseDataJSONLosslessly(fake.planJSON)
}

func (fake *refreshFakeTerraform) Apply(request RefreshApplyRequest) error {
	fake.applied = append(fake.applied, request)
	return nil
}

func refreshPlanJSON(changes, drift string) string {
	return refreshPlanJSONWith(`"complete":true,"errored":false,`, changes, drift)
}

// refreshPlanJSONWith carries the envelope a real Terraform plan always has.
// Omitting complete/errored made every fixture accept a plan the guard must
// refuse, so the state-only cases have to supply them to fail for their own
// reason rather than on the envelope. envelope replaces those keys outright
// rather than shadowing them, so no fixture depends on duplicate-key ordering.
func refreshPlanJSONWith(envelope, changes, drift string) string {
	return `{"format_version":"1.2","terraform_version":"1.15.4",` + envelope +
		`"resource_changes":[` + changes + `],"resource_drift":[` + drift + `]}`
}

func refreshRecord(address, actions string) string {
	return `{"address":"` + address + `","type":"zia_url_categories",` +
		`"change":{"actions":[` + actions + `],"before":{},"after":{}}}`
}

func newRefreshWorkspace(t *testing.T) (string, string) {
	t.Helper()
	workspace := t.TempDir()
	writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, nil, false)
	config := lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars.json")
	writeLifecycleText(t, config, `{"zia_url_categories_items":{}}`+"\n")
	return workspace, config
}

func runRefresh(
	t *testing.T,
	workspace string,
	fake *refreshFakeTerraform,
) (RefreshRunResult, []string, error) {
	t.Helper()
	diagnostics := make([]string, 0)
	result, err := RefreshEnvironmentRoots(RefreshEnvironmentRootsOptions{
		Deployment:   lifecycleTestDeployment(),
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root:         lifecycleTestOrdinaryRoot(),
		Selectors:    []string{lifecycleTestResource},
		Tenant:       "tenant",
		Terraform:    fake,
		Workspace:    workspace,
	})
	return result, diagnostics, err
}

func TestRefreshEnvironmentRootsReconcilesStateAndReportsWhatMoved(t *testing.T) {
	workspace, config := newRefreshWorkspace(t)
	fake := &refreshFakeTerraform{planJSON: refreshPlanJSON(
		refreshRecord("zia_url_categories.one", `"no-op"`),
		refreshRecord("zia_url_categories.one", `"update"`)+","+
			refreshRecord("zia_url_categories.two", `"delete"`),
	)}

	result, diagnostics, err := runRefresh(t, workspace, fake)
	if err != nil {
		t.Fatalf("RefreshEnvironmentRoots(state-only plan) error = %v, want nil", err)
	}
	if result.Refreshed != 1 || result.Reconciled != 2 {
		t.Errorf(
			"RefreshEnvironmentRoots(state-only plan) = %+v, want {Refreshed:1 Reconciled:2}",
			result,
		)
	}
	if len(fake.initialized) != 1 || len(fake.planned) != 1 ||
		len(fake.shown) != 1 || len(fake.applied) != 1 {
		t.Fatalf(
			"Terraform calls = (%d init, %d plan, %d show, %d apply), want (1, 1, 1, 1)",
			len(fake.initialized), len(fake.planned), len(fake.shown), len(fake.applied),
		)
	}
	if !reflect.DeepEqual(fake.planned[0].VarFiles, []string{config}) {
		t.Errorf("PlanRefreshOnly VarFiles = %#v, want [%q]", fake.planned[0].VarFiles, config)
	}
	// Verification and apply must see the same open descriptor, so the
	// artifact cannot be swapped between the two.
	if fake.shown[0].SnapshotFile == nil || fake.applied[0].SnapshotFile != fake.shown[0].SnapshotFile {
		t.Errorf(
			"apply snapshot = %p, want the descriptor shown for verification (%p)",
			fake.applied[0].SnapshotFile, fake.shown[0].SnapshotFile,
		)
	}
	wantDiagnostics := []string{
		"== refresh " + lifecycleTestResource,
		`   reconciled zia_url_categories.one update`,
		`   reconciled zia_url_categories.two delete`,
	}
	if !reflect.DeepEqual(diagnostics, wantDiagnostics) {
		t.Errorf("refresh diagnostics = %#v, want %#v", diagnostics, wantDiagnostics)
	}
	// The saved plan lives outside the workspace and does not survive the run.
	if _, err := os.Stat(fake.snapshotPath); !os.IsNotExist(err) {
		t.Errorf("os.Stat(%q) error = %v, want not-exist", fake.snapshotPath, err)
	}
	if strings.HasPrefix(fake.snapshotPath, workspace) {
		t.Errorf("refresh snapshot %q is inside the workspace %q", fake.snapshotPath, workspace)
	}
}

func TestRefreshEnvironmentRootsRefusesAnyPlanThatWouldWriteRemoteState(t *testing.T) {
	tests := []struct {
		name    string
		changes string
	}{
		{"update", refreshRecord("zia_url_categories.this", `"update"`)},
		{"create", refreshRecord("zia_url_categories.this", `"create"`)},
		{"delete", refreshRecord("zia_url_categories.this", `"delete"`)},
		{"replace", refreshRecord("zia_url_categories.this", `"delete","create"`)},
		{
			name: "no_op_carrying_an_import",
			changes: `{"address":"zia_url_categories.this","type":"zia_url_categories",` +
				`"change":{"actions":["no-op"],"importing":{"id":"existing"}}}`,
		},
		{
			name: "one_offender_among_no_ops",
			changes: refreshRecord("zia_url_categories.clean", `"no-op"`) + "," +
				refreshRecord("zia_url_categories.dirty", `"update"`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace, _ := newRefreshWorkspace(t)
			fake := &refreshFakeTerraform{planJSON: refreshPlanJSON(test.changes, "")}

			_, _, err := runRefresh(t, workspace, fake)
			requireLifecycleFailure(t, err, "REFRESH_PLAN_NOT_STATE_ONLY")
			// The refusal is worth nothing if the apply already ran.
			if len(fake.applied) != 0 {
				t.Errorf("Terraform apply calls = %d, want 0 on a refused plan", len(fake.applied))
			}
		})
	}
}

// TestRefreshEnvironmentRootsRefusesRemoteIntentOutsideResourceChanges covers
// the sections a guard that reads only resource_changes cannot see. Every case
// here carries a clean no-op resource change, so the plan passes the
// per-resource walk and is refused solely on the section under test.
func TestRefreshEnvironmentRootsRefusesRemoteIntentOutsideResourceChanges(t *testing.T) {
	invocation := `{"address":"action.zia_activation.this","type":"zia_activation"}`
	clean := `"complete":true,"errored":false,`
	tests := []struct {
		name     string
		envelope string
		code     string
	}{
		{"action_invocations", clean + `"action_invocations":[` + invocation + `],`, "REFRESH_PLAN_NOT_STATE_ONLY"},
		{"deferred_action_invocations", clean + `"deferred_action_invocations":[` + invocation + `],`, "REFRESH_PLAN_NOT_STATE_ONLY"},
		{"deferred_changes", clean + `"deferred_changes":[{"reason":"provider_config_unknown"}],`, "REFRESH_PLAN_NOT_STATE_ONLY"},
		{"incomplete_plan", `"complete":false,"errored":false,`, "INVALID_REFRESH_PLAN"},
		{"errored_plan", `"complete":true,"errored":true,`, "INVALID_REFRESH_PLAN"},
		{"missing_envelope", ``, "INVALID_REFRESH_PLAN"},
		{"action_invocations_not_an_array", clean + `"action_invocations":{},`, "INVALID_REFRESH_PLAN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace, _ := newRefreshWorkspace(t)
			fake := &refreshFakeTerraform{planJSON: refreshPlanJSONWith(
				test.envelope,
				refreshRecord("zia_url_categories.this", `"no-op"`),
				"",
			)}

			_, _, err := runRefresh(t, workspace, fake)
			requireLifecycleFailure(t, err, test.code)
			if len(fake.applied) != 0 {
				t.Errorf("Terraform apply calls = %d, want 0 on a refused plan", len(fake.applied))
			}
		})
	}
}

// TestRefreshEnvironmentRootsRefusesUnreadableResourceChanges pins the
// difference between absent and malformed. A resource_changes that is present
// but not an array must not read as zero changes.
func TestRefreshEnvironmentRootsRefusesUnreadableResourceChanges(t *testing.T) {
	workspace, _ := newRefreshWorkspace(t)
	fake := &refreshFakeTerraform{planJSON: `{"format_version":"1.2",` +
		`"terraform_version":"1.15.4","complete":true,"errored":false,` +
		`"resource_changes":{"zia_url_categories.this":{"actions":["delete"]}}}`}

	_, _, err := runRefresh(t, workspace, fake)
	requireLifecycleFailure(t, err, "INVALID_REFRESH_PLAN")
	if len(fake.applied) != 0 {
		t.Errorf("Terraform apply calls = %d, want 0 on a refused plan", len(fake.applied))
	}
}

func TestRefreshEnvironmentRootsAppliesWhenNothingDrifted(t *testing.T) {
	workspace, _ := newRefreshWorkspace(t)
	fake := &refreshFakeTerraform{planJSON: refreshPlanJSON(
		refreshRecord("zia_url_categories.this", `"no-op"`),
		"",
	)}

	result, diagnostics, err := runRefresh(t, workspace, fake)
	if err != nil {
		t.Fatalf("RefreshEnvironmentRoots(no drift) error = %v, want nil", err)
	}
	if result.Refreshed != 1 || result.Reconciled != 0 || len(fake.applied) != 1 {
		t.Errorf(
			"RefreshEnvironmentRoots(no drift) = %+v with %d apply(s), want {1 0} with 1",
			result, len(fake.applied),
		)
	}
	wantDiagnostics := []string{
		"== refresh " + lifecycleTestResource,
		"   state already matches reality",
	}
	if !reflect.DeepEqual(diagnostics, wantDiagnostics) {
		t.Errorf("refresh diagnostics = %#v, want %#v", diagnostics, wantDiagnostics)
	}
}

func TestRefreshEnvironmentRootsReportsNoRootsWhenConfigIsMissing(t *testing.T) {
	workspace := t.TempDir()
	writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, nil, false)
	fake := &refreshFakeTerraform{planJSON: refreshPlanJSON("", "")}

	_, diagnostics, err := runRefresh(t, workspace, fake)
	requireLifecycleFailure(t, err, "NO_ROOTS_REFRESHED")
	if len(fake.initialized) != 0 {
		t.Errorf("Terraform init calls = %d, want 0 when no root has inputs", len(fake.initialized))
	}
	if len(diagnostics) == 0 || !strings.HasPrefix(diagnostics[0], "skip ") {
		t.Errorf("refresh diagnostics = %#v, want a skip diagnostic", diagnostics)
	}
}

func TestRefreshPlanArgvIsRefreshOnlyAndSaved(t *testing.T) {
	snapshotPath := filepath.Join(t.TempDir(), "tfplan.refresh")
	got := refreshPlanArgv(RefreshPlanRequest{
		SnapshotPath: snapshotPath,
		VarFiles:     []string{"/one.auto.tfvars.json", "/two.auto.tfvars.json"},
	})
	want := []string{
		"plan", "-input=false", "-refresh-only",
		"-var-file=/one.auto.tfvars.json",
		"-var-file=/two.auto.tfvars.json",
		"-out=" + snapshotPath,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refreshPlanArgv() = %#v, want %#v", got, want)
	}
}

func TestCreateRefreshTerraformRequiresAnExplicitEnvironment(t *testing.T) {
	if _, err := CreateRefreshTerraform(CreateRefreshTerraformOptions{
		TerraformExecutable: "/missing/terraform",
	}); err == nil {
		t.Error("CreateRefreshTerraform(nil environment) error = nil, want a refusal")
	}
	if _, err := CreateRefreshTerraform(CreateRefreshTerraformOptions{
		Environment:         map[string]string{},
		TerraformExecutable: "/missing/terraform",
	}); err != nil {
		t.Errorf("CreateRefreshTerraform(empty environment) error = %v, want nil", err)
	}
}
