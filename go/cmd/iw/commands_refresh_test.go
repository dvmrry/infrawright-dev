package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/spf13/pflag"
)

type refreshCommandFakeTerraform struct{}

func (fake *refreshCommandFakeTerraform) Initialize(plan.PlanTerraformRequest) error { return nil }
func (fake *refreshCommandFakeTerraform) PlanRefreshOnly(plan.RefreshPlanRequest) error {
	return nil
}

func (fake *refreshCommandFakeTerraform) Show(plan.RefreshShowRequest) (canonjson.Value, error) {
	return nil, nil
}
func (fake *refreshCommandFakeTerraform) Apply(plan.RefreshApplyRequest) error { return nil }

func refreshCommandTestDependencies() refreshCommandDependencies {
	return refreshCommandDependencies{
		createRefreshTerraform: func(plan.CreateRefreshTerraformOptions) (plan.RefreshTerraform, error) {
			return &refreshCommandFakeTerraform{}, nil
		},
		currentDirectory: func() (string, error) { return "/workspace", nil },
		deploymentPath: func(map[string]string) (string, error) {
			return "/workspace/deployment.json", nil
		},
		environment: func() map[string]string { return map[string]string{} },
		loadPackAndDeployment: func(packOptionDefaults, string) (metadata.LoadedPackRoot, deployment.Deployment, error) {
			return metadata.LoadedPackRoot{}, deployment.Deployment{}, nil
		},
		packageRoot: func() (string, error) { return "/package", nil },
		refreshEnvironmentRoots: func(plan.RefreshEnvironmentRootsOptions) (plan.RefreshRunResult, error) {
			return plan.RefreshRunResult{}, nil
		},
		stderr: &bytes.Buffer{},
	}
}

func TestRefreshCommandComposesExactOptionsAndResolvesTerraformLazily(t *testing.T) {
	dependencies := refreshCommandTestDependencies()
	dependencies.environment = func() map[string]string {
		return map[string]string{
			"INFRAWRIGHT_PACKS":        "/environment/packs",
			"INFRAWRIGHT_PACK_PROFILE": "/environment/profile.json",
			"TF":                       "/environment/terraform",
		}
	}
	resolveCalls := 0
	dependencies.resolveTerraformExecutable = func(selected string, _ map[string]string) (string, error) {
		resolveCalls++
		return selected, nil
	}
	var got plan.RefreshEnvironmentRootsOptions
	dependencies.refreshEnvironmentRoots = func(
		options plan.RefreshEnvironmentRootsOptions,
	) (plan.RefreshRunResult, error) {
		got = options
		return plan.RefreshRunResult{Refreshed: 2, Reconciled: 5}, nil
	}
	stderr := &bytes.Buffer{}
	dependencies.stderr = stderr

	status, err := refreshCommandWithDependencies([]string{
		"--tenant", "prod",
		"--resource", "zia_url_categories",
		"--resource", "zpa_application_segment",
		"--backend-config", "backend.hcl",
	}, dependencies)
	if status != 0 || err != nil {
		t.Fatalf("refresh command = (%d, %v), want (0, nil)", status, err)
	}
	if got.Tenant != "prod" || got.Workspace != "/workspace" {
		t.Errorf("refresh options tenant/workspace = %q/%q, want prod//workspace", got.Tenant, got.Workspace)
	}
	wantSelectors := []string{"zia_url_categories", "zpa_application_segment"}
	if !reflect.DeepEqual(got.Selectors, wantSelectors) {
		t.Errorf("refresh options Selectors = %#v, want %#v", got.Selectors, wantSelectors)
	}
	if got.BackendConfig == nil || *got.BackendConfig != "backend.hcl" {
		t.Errorf("refresh options BackendConfig = %v, want backend.hcl", got.BackendConfig)
	}
	// A run that selects no root must never demand a Terraform binary, so the
	// executable is resolved on first use rather than at composition.
	if resolveCalls != 0 {
		t.Errorf("Terraform resolutions before first use = %d, want 0", resolveCalls)
	}
	if want := "2 root(s) refreshed; 5 resource(s) reconciled\n"; !strings.HasSuffix(stderr.String(), want) {
		t.Errorf("refresh summary = %q, want it to end with %q", stderr.String(), want)
	}
}

func TestRefreshCommandRequiresTenantAndRejectsPositionals(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		message   string
	}{
		{"missing_tenant", []string{}, "refresh requires --tenant"},
		{
			"positional", []string{"--tenant", "prod", "extra"},
			"refresh does not accept positional arguments",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := refreshCommandWithDependencies(test.arguments, refreshCommandTestDependencies())
			var exit *cliExit
			if !errors.As(err, &exit) || exit.status != 2 || exit.message != test.message {
				t.Fatalf("refresh command error = %T(%v), want usage exit 2 %q", err, err, test.message)
			}
		})
	}
}

// The refresh path is safe because of what the operation can do, not because
// an operator vouched for it. A permission flag here would be the habituation
// risk the path exists to remove, so its absence is pinned.
func TestRefreshCommandExposesNoOverrideFlag(t *testing.T) {
	command := newRefreshCobraCommand(refreshCommandTestDependencies())
	command.Flags().VisitAll(func(flag *pflag.Flag) {
		if strings.HasPrefix(flag.Name, "allow-") {
			t.Errorf("iw refresh exposes --%s, want no override flag on this path", flag.Name)
		}
	})
}
