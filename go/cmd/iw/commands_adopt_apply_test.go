package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/adopt"
	"github.com/dvmrry/infrawright-dev/go/internal/assessment"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
)

type blockDCommandFakeImportTerraform struct {
	initialized []adopt.ImportStagingTerraformRequest
	listed      []adopt.ImportStagingTerraformRequest
}

func (fake *blockDCommandFakeImportTerraform) Initialize(request adopt.ImportStagingTerraformRequest) error {
	fake.initialized = append(fake.initialized, request)
	return nil
}

func (fake *blockDCommandFakeImportTerraform) ListState(request adopt.ImportStagingTerraformRequest) (adopt.ImportStagingStateResult, error) {
	fake.listed = append(fake.listed, request)
	return adopt.ImportStagingStateResult{Success: true, Stdout: ""}, nil
}

type blockDCommandFakeApplyTerraform struct {
	initialized []plan.PlanTerraformRequest
	shown       []assessment.ExactPlanApplyShowRequest
	applied     []assessment.ExactPlanApplyRequest
}

func (fake *blockDCommandFakeApplyTerraform) Initialize(request plan.PlanTerraformRequest) error {
	fake.initialized = append(fake.initialized, request)
	return nil
}

func (fake *blockDCommandFakeApplyTerraform) Show(request assessment.ExactPlanApplyShowRequest) (assessment.ExactPlanApplyShownPlan, error) {
	fake.shown = append(fake.shown, request)
	return assessment.ExactPlanApplyShownPlan{}, nil
}

func (fake *blockDCommandFakeApplyTerraform) Apply(request assessment.ExactPlanApplyRequest) error {
	fake.applied = append(fake.applied, request)
	return nil
}

func blockDCommandTestDependencies(t *testing.T) blockDCommandDependencies {
	t.Helper()
	policy, err := metadata.NewDriftPolicy(nil, "D5 command test")
	if err != nil {
		t.Fatalf("metadata.NewDriftPolicy: %v", err)
	}
	return blockDCommandDependencies{
		packageRoot: func() (string, error) { return "/package", nil },
		environment: func() map[string]string {
			return map[string]string{
				"INFRAWRIGHT_PACKS":        "/environment/packs",
				"INFRAWRIGHT_PACK_PROFILE": "/environment/profile.json",
				"TF":                       "/environment/terraform",
				"BUILD_SOURCEBRANCH":       "refs/heads/feature/test",
			}
		},
		deploymentPath: func(map[string]string) (string, error) { return "deployment.json", nil },
		currentDirectory: func() (string, error) {
			return filepath.Join(t.TempDir(), "workspace"), nil
		},
		loadPack: func(metadata.LoadPackRootOptions) (metadata.LoadedPackRoot, error) {
			return metadata.LoadedPackRoot{}, nil
		},
		loadDeployment: func(string) (deployment.Deployment, error) {
			return deployment.Deployment{}, nil
		},
		loadBoundDeployment: func(path string, _ controlevidence.BindOptions) (deployment.BoundAssessmentDeployment, error) {
			return deployment.BoundAssessmentDeployment{
				File: controlevidence.BoundAssessmentControlFile{Path: path},
			}, nil
		},
		loadAdoptionPolicy: func(metadata.LoadedPackRoot, *string) (*metadata.DriftPolicy, error) {
			return policy, nil
		},
		currentApplyBranch: func(assessment.CurrentApplyBranchOptions) string { return "feature/test" },
		stderr:             &bytes.Buffer{},
	}
}

func TestAdoptCommandComposesOptionsAndResolvesTerraformOnlyWhenLoaded(t *testing.T) {
	dependencies := blockDCommandTestDependencies(t)
	resolveCalls := 0
	dependencies.resolveTerraformExecutable = func(selected string, environment map[string]string) (string, error) {
		resolveCalls++
		if selected != "/explicit/terraform" || environment["TF"] != "/environment/terraform" {
			t.Fatalf("resolver input = %q, %#v", selected, environment)
		}
		return selected, nil
	}
	stateCreates := 0
	dependencies.createStateLoader = func(options adopt.DefaultAdoptionLoaderOptions) (adopt.AdoptionStateLoader, error) {
		stateCreates++
		if options.TerraformExecutable != "/explicit/terraform" {
			t.Fatalf("state loader executable = %q", options.TerraformExecutable)
		}
		return func(adopt.AdoptionStateRequest) (map[string]adopt.OracleStateObject, error) {
			return map[string]adopt.OracleStateObject{}, nil
		}, nil
	}
	dependencies.createBatchStateLoader = func(adopt.DefaultAdoptionLoaderOptions) (adopt.AdoptionBatchStateLoader, error) {
		t.Fatal("batch loader was constructed by the per-resource fixture")
		return nil, errors.New("unreachable")
	}
	dependencies.runAdoptBatch = func(options adopt.RunAdoptBatchOptions) (adopt.AdoptBatchResult, error) {
		if resolveCalls != 0 {
			t.Fatal("Terraform resolved before the runner requested state")
		}
		if options.InputDirectory != "input" || options.Tenant != "tenant" ||
			!reflect.DeepEqual(options.Selectors, []string{"one", "two"}) {
			t.Fatalf("adopt options = %+v", options)
		}
		if _, err := options.StateLoader(adopt.AdoptionStateRequest{}); err != nil {
			t.Fatalf("StateLoader: %v", err)
		}
		return adopt.AdoptBatchResult{Failed: []string{"two"}}, nil
	}

	status, err := adoptCommandWithDependencies([]string{
		"--in", "input", "--tenant", "tenant", "--resource", "one", "--resource", "two",
		"--terraform", "/explicit/terraform",
	}, dependencies)
	if err != nil || status != 1 {
		t.Fatalf("adopt command = (%d, %v), want (1, nil)", status, err)
	}
	if resolveCalls != 1 || stateCreates != 1 {
		t.Fatalf("lazy calls = resolve %d, state create %d; want 1, 1", resolveCalls, stateCreates)
	}
}

func TestImportStagingCommandsPreserveStateAwareLazyBoundary(t *testing.T) {
	for _, stateAware := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "state_aware"}[stateAware], func(t *testing.T) {
			dependencies := blockDCommandTestDependencies(t)
			resolveCalls := 0
			fake := &blockDCommandFakeImportTerraform{}
			dependencies.resolveTerraformExecutable = func(selected string, _ map[string]string) (string, error) {
				resolveCalls++
				return selected, nil
			}
			dependencies.createImportTerraform = func(adopt.ImportStagingTerraformOptions) adopt.ImportStagingTerraform {
				return fake
			}
			dependencies.stageImports = func(options adopt.StageImportsOptions) (adopt.StageImportsResult, error) {
				if options.StateAware != stateAware || options.Tenant != "tenant" {
					t.Fatalf("stage options = %+v", options)
				}
				if !stateAware {
					if options.Terraform != nil {
						t.Fatal("ordinary staging received a Terraform adapter")
					}
					return adopt.StageImportsResult{}, nil
				}
				if resolveCalls != 0 {
					t.Fatal("state-aware Terraform resolved before first adapter call")
				}
				request := adopt.ImportStagingTerraformRequest{Directory: "/ephemeral", Tenant: "tenant"}
				if err := options.Terraform.Initialize(request); err != nil {
					t.Fatalf("Initialize: %v", err)
				}
				if _, err := options.Terraform.ListState(request); err != nil {
					t.Fatalf("ListState: %v", err)
				}
				return adopt.StageImportsResult{}, nil
			}
			arguments := []string{"--tenant", "tenant"}
			if stateAware {
				arguments = append(arguments, "--state-aware", "--terraform", "/fake/terraform")
			}
			status, err := stageImportsCommandWithDependencies(arguments, dependencies)
			if err != nil || status != 0 {
				t.Fatalf("stage command = (%d, %v)", status, err)
			}
			wantResolve := 0
			if stateAware {
				wantResolve = 1
			}
			if resolveCalls != wantResolve {
				t.Errorf("resolve calls = %d, want %d", resolveCalls, wantResolve)
			}
		})
	}

	dependencies := blockDCommandTestDependencies(t)
	dependencies.unstageImports = func(options adopt.UnstageImportsOptions) (adopt.UnstageImportsResult, error) {
		if options.Tenant != "tenant" || !reflect.DeepEqual(options.Selectors, []string{"one"}) {
			t.Fatalf("unstage options = %+v", options)
		}
		return adopt.UnstageImportsResult{}, nil
	}
	if status, err := unstageImportsCommandWithDependencies(
		[]string{"--tenant", "tenant", "--resource", "one"}, dependencies,
	); err != nil || status != 0 {
		t.Fatalf("unstage command = (%d, %v)", status, err)
	}
}

func TestApplyCommandComposesResolvedControlsAndUsesOnlyInjectedTerraform(t *testing.T) {
	dependencies := blockDCommandTestDependencies(t)
	workspace, err := dependencies.currentDirectory()
	if err != nil {
		t.Fatal(err)
	}
	dependencies.currentDirectory = func() (string, error) { return workspace, nil }
	resolveCalls := 0
	fake := &blockDCommandFakeApplyTerraform{}
	dependencies.resolveTerraformExecutable = func(selected string, _ map[string]string) (string, error) {
		resolveCalls++
		if selected != "/fake/terraform" {
			t.Fatalf("selected Terraform = %q", selected)
		}
		return selected, nil
	}
	dependencies.createApplyTerraform = func(options assessment.CreateExactPlanApplyTerraformOptions) (assessment.ExactPlanApplyTerraform, error) {
		if options.TerraformExecutable != "/fake/terraform" {
			t.Fatalf("adapter executable = %q", options.TerraformExecutable)
		}
		return fake, nil
	}
	dependencies.applyExactSavedPlans = func(options assessment.ExactPlanApplyOptions) (assessment.ExactPlanApplyResult, error) {
		if resolveCalls != 0 {
			t.Fatal("Terraform resolved before exact Apply requested it")
		}
		if options.Workspace != workspace || options.Tenant == nil || *options.Tenant != "tenant" {
			t.Fatalf("Apply options = %+v", options)
		}
		if !options.AllowDestroy || !options.AllowNonMain || !options.AllowPlanChanges {
			t.Errorf("Apply allow flags = destroy:%t non-main:%t plan-changes:%t, want all true",
				options.AllowDestroy, options.AllowNonMain, options.AllowPlanChanges)
		}
		if options.MainBranch == nil || *options.MainBranch != "trunk" {
			t.Errorf("main branch = %v, want trunk", options.MainBranch)
		}
		if got, want := options.Selectors, []string{"one"}; !reflect.DeepEqual(got, want) {
			t.Errorf("selectors = %v, want %v", got, want)
		}
		if got, want := *options.BackendConfig, filepath.Join(workspace, "backend.hcl"); got != want {
			t.Errorf("backend config = %q, want %q", got, want)
		}
		if got, want := *options.PolicyPath, filepath.Join(workspace, "policy.json"); got != want {
			t.Errorf("policy path = %q, want %q", got, want)
		}
		if branch := options.CurrentBranch(); branch != "feature/test" {
			t.Errorf("current branch = %q", branch)
		}
		inputs, err := options.LoadInputs()
		if err != nil || len(inputs.ControlFiles) != 1 {
			t.Fatalf("LoadInputs = (%+v, %v)", inputs, err)
		}
		if err := options.Terraform.Initialize(plan.PlanTerraformRequest{Directory: "/ephemeral"}); err != nil {
			t.Fatal(err)
		}
		if _, err := options.Terraform.Show(assessment.ExactPlanApplyShowRequest{Directory: "/ephemeral"}); err != nil {
			t.Fatal(err)
		}
		if err := options.Terraform.Apply(assessment.ExactPlanApplyRequest{Directory: "/ephemeral"}); err != nil {
			t.Fatal(err)
		}
		return assessment.ExactPlanApplyResult{Applied: 1}, nil
	}
	status, err := applyCommandWithDependencies([]string{
		"--tenant", "tenant", "--resource", "one", "--backend-config", "backend.hcl",
		"--policy", "policy.json", "--allow-destroy", "--allow-non-main", "--allow-plan-changes",
		"--main-branch", "trunk", "--terraform", "/fake/terraform",
	}, dependencies)
	if err != nil || status != 0 {
		t.Fatalf("apply command = (%d, %v)", status, err)
	}
	if resolveCalls != 1 || len(fake.applied) != 1 {
		t.Fatalf("fake execution = resolve %d, apply %d; want 1, 1", resolveCalls, len(fake.applied))
	}
}

func TestBlockDCommandUsageContract(t *testing.T) {
	dependencies := blockDCommandTestDependencies(t)
	for name, invoke := range map[string]func() (int, error){
		"adopt": func() (int, error) { return adoptCommandWithDependencies([]string{"--in", "input"}, dependencies) },
		"stage": func() (int, error) { return stageImportsCommandWithDependencies(nil, dependencies) },
		"unstage rejects stage flag": func() (int, error) {
			return unstageImportsCommandWithDependencies([]string{"--tenant", "tenant", "--state-aware"}, dependencies)
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := invoke()
			var exit *cliExit
			if !errors.As(err, &exit) || exit.status != 2 {
				t.Fatalf("error = %T(%v), want usage cliExit", err, err)
			}
		})
	}
}
