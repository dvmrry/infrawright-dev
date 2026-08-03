package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type exactDataRootPlanTerraform struct {
	initialized []plan.PlanTerraformRequest
	planned     []plan.PlanTerraformRequest
}

func (fake *exactDataRootPlanTerraform) Initialize(request plan.PlanTerraformRequest) error {
	fake.initialized = append(fake.initialized, request)
	return nil
}

func (fake *exactDataRootPlanTerraform) Plan(request plan.PlanTerraformRequest) error {
	fake.planned = append(fake.planned, request)
	return nil
}

type exactDataRootRefreshTerraform struct {
	initialized []plan.PlanTerraformRequest
	planned     []plan.RefreshPlanRequest
	shown       int
	applied     int
}

func (fake *exactDataRootRefreshTerraform) Initialize(request plan.PlanTerraformRequest) error {
	fake.initialized = append(fake.initialized, request)
	return nil
}

func (fake *exactDataRootRefreshTerraform) PlanRefreshOnly(request plan.RefreshPlanRequest) error {
	fake.planned = append(fake.planned, request)
	return os.WriteFile(request.SnapshotPath, []byte("refresh snapshot"), 0o600)
}

func (fake *exactDataRootRefreshTerraform) Show(plan.RefreshShowRequest) (canonjson.Value, error) {
	fake.shown++
	return canonjson.ParseControlJSON(`{"format_version":"1.2","terraform_version":"1.0","complete":true,"errored":false,"resource_changes":[],"resource_drift":[]}`)
}

func (fake *exactDataRootRefreshTerraform) Apply(plan.RefreshApplyRequest) error {
	fake.applied++
	return nil
}

func prepareExactDataRootLifecycleWorkspace(t *testing.T) defaultTransformDataFixture {
	t.Helper()
	fixture := newDefaultTransformDataFixture(t)
	rootDirectory := filepath.Join(fixture.workspace, "envs", "tenant", "sample_groups_data")
	writeBlockC4File(t, filepath.Join(rootDirectory, "main.tf"), []byte("# data root\n"), 0o600)
	config, err := filepath.Abs(filepath.Join(fixture.workspace, "config", "tenant", "sample_groups_data.auto.tfvars.json"))
	if err != nil {
		t.Fatalf("filepath.Abs(data root config): %v", err)
	}
	writeBlockC4JSON(t, config, map[string]any{"sample_groups_data_items": map[string]any{}})
	return fixture
}

func TestPlanLifecycleExactDataSelectorPlansOnlyDataRoot(t *testing.T) {
	fixture := prepareExactDataRootLifecycleWorkspace(t)
	fake := &exactDataRootPlanTerraform{}
	result, err := plan.PlanEnvironmentRoots(plan.PlanEnvironmentRootsOptions{
		Deployment:   fixture.dep,
		OnDiagnostic: func(string) {},
		Root:         fixture.root, Save: false, Selectors: []string{"sample_groups_data"},
		Tenant: "tenant", Terraform: fake, Workspace: fixture.workspace,
	})
	if err != nil {
		t.Fatalf("PlanEnvironmentRoots(exact data selector): %v", err)
	}
	if result.Planned != 1 || len(fake.initialized) != 1 || len(fake.planned) != 1 {
		t.Fatalf("PlanEnvironmentRoots(exact data selector) result/calls = %#v/%d/%d, want one data root", result, len(fake.initialized), len(fake.planned))
	}
	wantDirectory := filepath.Join(fixture.workspace, "envs", "tenant", "sample_groups_data")
	if fake.planned[0].Directory != wantDirectory {
		t.Errorf("Plan exact data selector directory = %q, want %q", fake.planned[0].Directory, wantDirectory)
	}
	wantConfig := filepath.Join(fixture.workspace, "config", "tenant", "sample_groups_data.auto.tfvars.json")
	if !reflect.DeepEqual(fake.planned[0].VarFiles, []string{wantConfig}) {
		t.Errorf("Plan exact data selector var files = %#v, want [%q]", fake.planned[0].VarFiles, wantConfig)
	}
}

func TestRefreshLifecycleExactDataSelectorRefreshesOnlyDataRoot(t *testing.T) {
	fixture := prepareExactDataRootLifecycleWorkspace(t)
	fake := &exactDataRootRefreshTerraform{}
	result, err := plan.RefreshEnvironmentRoots(plan.RefreshEnvironmentRootsOptions{
		Deployment:   fixture.dep,
		OnDiagnostic: func(string) {},
		Root:         fixture.root, Selectors: []string{"sample_groups_data"},
		Tenant: "tenant", Terraform: fake, Workspace: fixture.workspace,
	})
	if err != nil {
		t.Fatalf("RefreshEnvironmentRoots(exact data selector): %v", err)
	}
	if result.Refreshed != 1 || len(fake.initialized) != 1 || len(fake.planned) != 1 || fake.shown != 1 || fake.applied != 1 {
		t.Fatalf("RefreshEnvironmentRoots(exact data selector) result/calls = %#v/%d/%d/%d/%d, want one data root", result, len(fake.initialized), len(fake.planned), fake.shown, fake.applied)
	}
	wantDirectory := filepath.Join(fixture.workspace, "envs", "tenant", "sample_groups_data")
	if fake.planned[0].Directory != wantDirectory {
		t.Errorf("Refresh exact data selector directory = %q, want %q", fake.planned[0].Directory, wantDirectory)
	}
	wantConfig := filepath.Join(fixture.workspace, "config", "tenant", "sample_groups_data.auto.tfvars.json")
	if !reflect.DeepEqual(fake.planned[0].VarFiles, []string{wantConfig}) {
		t.Errorf("Refresh exact data selector var files = %#v, want [%q]", fake.planned[0].VarFiles, wantConfig)
	}
}

func TestResourcesCommandMatchesTransformLaneSelection(t *testing.T) {
	fixture := newDefaultTransformDataFixture(t)
	profile := filepath.Join(fixture.workspace, "packs", "sample.packset.json")
	t.Setenv("INFRAWRIGHT_PACKAGE_ROOT", fixture.workspace)
	baseOptions := map[string][]string{
		"--root":    {filepath.Join(fixture.workspace, "packs")},
		"--profile": {profile},
	}
	run := func(t *testing.T, selectors []string) (string, error) {
		t.Helper()
		options := map[string][]string{}
		for key, values := range baseOptions {
			options[key] = append([]string(nil), values...)
		}
		if selectors != nil {
			options["--resource"] = selectors
		}
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatalf("os.Pipe: %v", err)
		}
		saved := os.Stdout
		os.Stdout = write
		_, runErr := resourcesInput(commandInput{Options: options})
		os.Stdout = saved
		write.Close()
		captured, err := io.ReadAll(read)
		if err != nil {
			t.Fatalf("io.ReadAll: %v", err)
		}
		return string(captured), runErr
	}
	tests := []struct {
		name      string
		selectors []string
		want      string
	}{
		{name: "exact_data_selector", selectors: []string{"sample_groups_data"}, want: "sample_groups_data\n"},
		{name: "mixed_generated_and_data_selectors", selectors: []string{"sample_rule", "sample_groups_data"}, want: "sample_groups_data\nsample_rule\n"},
		{name: "implicit_batch_includes_data_referents", selectors: nil, want: "sample_groups_data\nsample_rule\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := run(t, test.selectors)
			if err != nil {
				t.Fatalf("resourcesInput(%v) error = %v, want transform-lane admission", test.selectors, err)
			}
			if output != test.want {
				t.Errorf("resourcesInput(%v) output = %q, want %q", test.selectors, output, test.want)
			}
		})
	}
	t.Run("unknown_selector_stays_classified", func(t *testing.T) {
		_, err := run(t, []string{"sample_missing"})
		var failure *procerr.ProcessFailure
		if !errors.As(err, &failure) {
			t.Fatalf("resourcesInput(unknown) error = %T(%v), want ProcessFailure", err, err)
		}
		if failure.Code != "UNKNOWN_RESOURCE_SELECTOR" || !strings.Contains(failure.Message, "sample_missing") {
			t.Errorf("resourcesInput(unknown) = %q %q, want UNKNOWN_RESOURCE_SELECTOR naming sample_missing", failure.Code, failure.Message)
		}
	})
}
