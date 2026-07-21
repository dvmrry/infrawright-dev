package plan

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

const (
	lifecycleTestResource = "zia_url_categories"
	lifecycleTestSecond   = "zia_admin_users"
	lifecycleTestDerived  = "zpa_policy_access_rule_reorder"
)

type lifecycleFakeTerraform struct {
	initialized  []PlanTerraformRequest
	planned      []PlanTerraformRequest
	onInitialize func(PlanTerraformRequest) error
	onPlan       func(PlanTerraformRequest) error
}

func cloneLifecycleRequest(request PlanTerraformRequest) PlanTerraformRequest {
	cloned := request
	if request.Environment != nil {
		cloned.Environment = cloneEnvironment(request.Environment)
	}
	cloned.VarFiles = append([]string(nil), request.VarFiles...)
	return cloned
}

func (fake *lifecycleFakeTerraform) Initialize(request PlanTerraformRequest) error {
	fake.initialized = append(fake.initialized, cloneLifecycleRequest(request))
	if fake.onInitialize != nil {
		return fake.onInitialize(request)
	}
	return nil
}

func (fake *lifecycleFakeTerraform) Plan(request PlanTerraformRequest) error {
	fake.planned = append(fake.planned, cloneLifecycleRequest(request))
	if request.Save {
		if err := os.WriteFile(filepath.Join(request.Directory, "tfplan"), []byte("opaque-plan"), 0o666); err != nil {
			return err
		}
	}
	if fake.onPlan != nil {
		return fake.onPlan(request)
	}
	return nil
}

func lifecycleTestDeployment() deployment.Deployment {
	return deployment.Deployment{
		Overlay: ".",
		Roots:   map[string]deployment.RootProviderConfig{},
	}
}

func lifecycleTestRoot(resources map[string]metadata.JsonObject) metadata.LoadedPackRoot {
	loaded := make(map[string]metadata.LoadedResourceMetadata, len(resources))
	prefixes := make(map[string]string)
	for resourceType, registry := range resources {
		provider := strings.SplitN(resourceType, "_", 2)[0]
		prefixes[provider+"_"] = provider
		loaded[resourceType] = metadata.LoadedResourceMetadata{
			Type:     resourceType,
			Product:  provider,
			Provider: provider,
			Registry: registry,
		}
	}
	return metadata.LoadedPackRoot{
		Packs:     metadata.PackMetadata{ProviderPrefixes: prefixes},
		Resources: loaded,
	}
}

func lifecycleTestOrdinaryRoot() metadata.LoadedPackRoot {
	return lifecycleTestRoot(map[string]metadata.JsonObject{
		lifecycleTestResource: {"generate": true, "product": "zia"},
	})
}

func writeLifecycleText(t *testing.T, filePath, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(filePath), err)
	}
	if err := os.WriteFile(filePath, []byte(text), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", filePath, err)
	}
}

func lifecycleTestEnvDirectory(workspace, tenant, label string) string {
	return filepath.Join(workspace, "envs", tenant, label)
}

func lifecycleTestConfigPath(workspace, tenant, resourceType, extension string) string {
	return filepath.Join(workspace, "config", tenant, resourceType+extension)
}

func writeLifecycleRoot(
	t *testing.T,
	workspace, tenant, label string,
	members []string,
	backend *string,
	referenceVariable bool,
) string {
	t.Helper()
	directory := lifecycleTestEnvDirectory(workspace, tenant, label)
	lines := make([]string, 0)
	if backend != nil {
		lines = append(lines, "terraform {", `  backend "`+*backend+`" {}`, "}", "")
	}
	for _, resourceType := range members {
		moduleDirectory := filepath.Join(workspace, "modules", resourceType)
		writeLifecycleText(t, filepath.Join(moduleDirectory, "main.tf"), "# module\n")
		relative, err := filepath.Rel(directory, moduleDirectory)
		if err != nil {
			t.Fatalf("filepath.Rel(%q, %q) error: %v", directory, moduleDirectory, err)
		}
		lines = append(lines,
			`module "`+resourceType+`" {`,
			`  source = "`+filepath.ToSlash(relative)+`"`,
			`  items = var.`+resourceType+`_items`,
			"}",
			"",
		)
	}
	if referenceVariable {
		lines = append(lines,
			`variable "`+ReferenceBackendVariable+`" {`,
			"  type = any",
			"}",
		)
	}
	writeLifecycleText(t, filepath.Join(directory, "main.tf"), strings.Join(lines, "\n")+"\n")
	return directory
}

func requireLifecycleFailure(t *testing.T, err error, code string) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T(%v), want *procerr.ProcessFailure with code %q", err, err, code)
	}
	if failure.Code != code {
		t.Errorf("ProcessFailure.Code = %q, want %q", failure.Code, code)
	}
	return failure
}

func TestCreatePlanTerraformEmitsExactCommands(t *testing.T) {
	timeout := int64(12_345)
	adapter := CreatePlanTerraform(CreatePlanTerraformOptions{
		Environment: map[string]string{"BASE": "base", "OVERRIDE": "base"},
		Limits: &terraformcmd.TerraformCommandLimits{
			TimeoutMs:      &timeout,
			MaxStdoutBytes: 123,
			MaxStderrBytes: 456,
		},
		TerraformExecutable: "/terraform",
	}).(*planTerraformAdapter)
	var calls []terraformcmd.TerraformCommandOptions
	adapter.run = func(options terraformcmd.TerraformCommandOptions) (terraformcmd.TerraformCommandResult, error) {
		calls = append(calls, options)
		return terraformcmd.TerraformCommandResult{}, nil
	}
	backendConfig := "/workspace/backend.hcl"
	backendKey := "tenant/grouped.tfstate"
	request := PlanTerraformRequest{
		BackendConfig: &backendConfig,
		BackendKey:    &backendKey,
		Directory:     "/workspace/envs/tenant/grouped",
		Environment:   map[string]string{"OVERRIDE": "request", "REQUEST": "value"},
		Save:          true,
		VarFiles:      []string{"/workspace/a.tfvars", "/workspace/b.tfvars"},
	}
	if err := adapter.Initialize(request); err != nil {
		t.Fatalf("PlanTerraform.Initialize(%+v) error: %v", request, err)
	}
	if err := adapter.Plan(request); err != nil {
		t.Fatalf("PlanTerraform.Plan(%+v) error: %v", request, err)
	}
	if len(calls) != 2 {
		t.Fatalf("Terraform command calls = %d, want 2", len(calls))
	}
	wantInitArgv := []string{
		"init", "-input=false", "-reconfigure",
		"-backend-config=/workspace/backend.hcl",
		"-backend-config=key=tenant/grouped.tfstate",
	}
	if !reflect.DeepEqual(calls[0].Argv, wantInitArgv) {
		t.Errorf("Initialize argv = %#v, want %#v", calls[0].Argv, wantInitArgv)
	}
	wantPlanArgv := []string{
		"plan", "-input=false",
		"-var-file=/workspace/a.tfvars",
		"-var-file=/workspace/b.tfvars",
		"-out=tfplan",
	}
	if !reflect.DeepEqual(calls[1].Argv, wantPlanArgv) {
		t.Errorf("Plan argv = %#v, want %#v", calls[1].Argv, wantPlanArgv)
	}
	wantEnvironment := map[string]string{"BASE": "base", "OVERRIDE": "request", "REQUEST": "value"}
	for index, call := range calls {
		if !reflect.DeepEqual(call.Environment, wantEnvironment) {
			t.Errorf("Terraform command call %d environment = %#v, want %#v", index, call.Environment, wantEnvironment)
		}
		if call.CWD != request.Directory {
			t.Errorf("Terraform command call %d CWD = %q, want %q", index, call.CWD, request.Directory)
		}
	}
	if calls[0].Output != terraformcmd.TerraformCommandOutputInheritStderr {
		t.Errorf("Initialize output = %q, want %q", calls[0].Output, terraformcmd.TerraformCommandOutputInheritStderr)
	}
	if calls[1].Output != terraformcmd.TerraformCommandOutputInherit {
		t.Errorf("Plan output = %q, want %q", calls[1].Output, terraformcmd.TerraformCommandOutputInherit)
	}
}

func TestSameLifecycleEnvironmentComparesKeysAndValues(t *testing.T) {
	if sameLifecycleEnvironment(map[string]string{"left": ""}, map[string]string{"right": ""}) {
		t.Error("sameLifecycleEnvironment(distinct empty-valued keys) = true, want false")
	}
	if !sameLifecycleEnvironment(map[string]string{"key": "value"}, map[string]string{"key": "value"}) {
		t.Error("sameLifecycleEnvironment(equal maps) = false, want true")
	}
}

func TestRequireBackendConfiguration(t *testing.T) {
	directory := t.TempDir()
	writeLifecycleText(t, filepath.Join(directory, "main.tf"), "terraform {\r\n  backend \"azurerm\" {}\r\n}\r\n")
	err := RequireBackendConfiguration(nil, directory, "zia")
	requireLifecycleFailure(t, err, "BACKEND_CONFIG_REQUIRED")
	backend := "/backend.hcl"
	if err := RequireBackendConfiguration(&backend, directory, "zia"); err != nil {
		t.Errorf("RequireBackendConfiguration(configured) error: %v", err)
	}
	writeLifecycleText(t, filepath.Join(directory, "main.tf"), string([]byte{0xff}))
	err = RequireBackendConfiguration(nil, directory, "zia")
	requireLifecycleFailure(t, err, "INVALID_UTF8")
}

func TestPlanEnvironmentRootsSavesExactPrivatePairAndCleanRemovesOnlyPair(t *testing.T) {
	workspace := t.TempDir()
	directory := writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, nil, false)
	config := lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars.json")
	writeLifecycleText(t, config, `{"zia_url_categories_items":{}}`+"\n")
	writeLifecycleText(t, filepath.Join(directory, "tfplan"), "stale-plan")
	writeLifecycleText(t, filepath.Join(directory, "tfplan.sources"), "stale-sources\n")
	writeLifecycleText(t, filepath.Join(directory, "report.json"), "{}\n")
	writeLifecycleText(t, filepath.Join(directory, ".terraform.lock.hcl"), "# lock\n")
	fake := &lifecycleFakeTerraform{}
	var diagnostics []string

	result, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
		Deployment:   lifecycleTestDeployment(),
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root:         lifecycleTestOrdinaryRoot(),
		Save:         true,
		Selectors:    []string{lifecycleTestResource},
		Tenant:       "tenant",
		Terraform:    fake,
		Workspace:    workspace,
	})
	if err != nil {
		t.Fatalf("PlanEnvironmentRoots(saved local plan) error: %v", err)
	}
	if result.Planned != 1 {
		t.Errorf("PlanEnvironmentRoots(saved local plan).Planned = %d, want 1", result.Planned)
	}
	if len(fake.initialized) != 1 || len(fake.planned) != 1 {
		t.Fatalf("Terraform calls = (%d init, %d plan), want (1, 1)", len(fake.initialized), len(fake.planned))
	}
	if !reflect.DeepEqual(fake.planned[0].VarFiles, []string{config}) {
		t.Errorf("Plan request VarFiles = %#v, want [%q]", fake.planned[0].VarFiles, config)
	}
	if fake.planned[0].BackendConfig != nil {
		t.Errorf("Plan request BackendConfig = %v, want nil", *fake.planned[0].BackendConfig)
	}
	if !reflect.DeepEqual(diagnostics, []string{"== plan " + lifecycleTestResource}) {
		t.Errorf("Plan diagnostics = %#v, want [\"== plan %s\"]", diagnostics, lifecycleTestResource)
	}
	sources, err := os.ReadFile(filepath.Join(directory, "tfplan.sources"))
	if err != nil {
		t.Fatalf("os.ReadFile(tfplan.sources) error: %v", err)
	}
	if !regexp.MustCompile(`^\{"sha256": "[0-9a-f]{64}", "version": 2\}\n$`).Match(sources) {
		t.Errorf("tfplan.sources = %q, want exact fingerprint-v2 normal form", sources)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(directory, "tfplan"))
		if err != nil {
			t.Fatalf("os.Stat(tfplan) error: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("tfplan mode = %#o, want 0600", got)
		}
	}

	var cleanDiagnostics []string
	cleaned, err := CleanPlans(CleanPlansOptions{
		Deployment:   lifecycleTestDeployment(),
		OnDiagnostic: func(message string) { cleanDiagnostics = append(cleanDiagnostics, message) },
		Root:         lifecycleTestOrdinaryRoot(),
		Selectors:    []string{lifecycleTestResource},
		Tenant:       lifecycleStringPointer("tenant"),
		Workspace:    workspace,
	})
	if err != nil {
		t.Fatalf("CleanPlans(saved pair) error: %v", err)
	}
	if cleaned.Removed != 1 {
		t.Errorf("CleanPlans(saved pair).Removed = %d, want 1", cleaned.Removed)
	}
	wantCleanDiagnostics := []string{
		"removed envs/tenant/" + lifecycleTestResource + "/tfplan",
		"removed envs/tenant/" + lifecycleTestResource + "/tfplan.sources",
		"1 stale plan(s) removed",
	}
	if !reflect.DeepEqual(cleanDiagnostics, wantCleanDiagnostics) {
		t.Errorf("CleanPlans diagnostics = %#v, want %#v", cleanDiagnostics, wantCleanDiagnostics)
	}
	if got, err := os.ReadFile(filepath.Join(directory, "report.json")); err != nil || string(got) != "{}\n" {
		t.Errorf("report.json after CleanPlans = (%q, %v), want (%q, nil)", got, err, "{}\n")
	}
	if got, err := os.ReadFile(filepath.Join(directory, ".terraform.lock.hcl")); err != nil || string(got) != "# lock\n" {
		t.Errorf(".terraform.lock.hcl after CleanPlans = (%q, %v), want (%q, nil)", got, err, "# lock\n")
	}
}

func lifecycleStringPointer(value string) *string {
	return &value
}

func TestPlanEnvironmentRootsRemoteBackendUsesAbsoluteConfigAndStateKey(t *testing.T) {
	workspace := t.TempDir()
	backendType := "azurerm"
	writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, &backendType, false)
	writeLifecycleText(t, lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars.json"), `{"zia_url_categories_items":{}}`+"\n")
	writeLifecycleText(t, filepath.Join(workspace, "backend.hcl"), `storage_account_name = "example"`+"\n")

	_, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
		Deployment: lifecycleTestDeployment(), Root: lifecycleTestOrdinaryRoot(),
		Selectors: []string{lifecycleTestResource}, Tenant: "tenant",
		Terraform: &lifecycleFakeTerraform{}, Workspace: workspace,
	})
	requireLifecycleFailure(t, err, "BACKEND_CONFIG_REQUIRED")

	backendRelative := "backend.hcl"
	fake := &lifecycleFakeTerraform{}
	_, err = PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
		BackendConfig: &backendRelative,
		Deployment:    lifecycleTestDeployment(),
		Root:          lifecycleTestOrdinaryRoot(),
		Selectors:     []string{lifecycleTestResource},
		Tenant:        "tenant",
		Terraform:     fake,
		Workspace:     workspace,
	})
	if err != nil {
		t.Fatalf("PlanEnvironmentRoots(remote backend) error: %v", err)
	}
	wantBackend := filepath.Join(workspace, "backend.hcl")
	if fake.initialized[0].BackendConfig == nil || *fake.initialized[0].BackendConfig != wantBackend {
		t.Errorf("Initialize BackendConfig = %v, want %q", fake.initialized[0].BackendConfig, wantBackend)
	}
	wantKey := "tenant/" + lifecycleTestResource + ".tfstate"
	if fake.initialized[0].BackendKey == nil || *fake.initialized[0].BackendKey != wantKey {
		t.Errorf("Initialize BackendKey = %v, want %q", fake.initialized[0].BackendKey, wantKey)
	}
}

func TestPlanEnvironmentRootsCrossStateReferenceEnvironmentFailsClosedOnInitRace(t *testing.T) {
	workspace := t.TempDir()
	backendType := "azurerm"
	writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, &backendType, true)
	writeLifecycleText(t, lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars.json"), `{"zia_url_categories_items":{}}`+"\n")
	backend := filepath.Join(workspace, "backend.json")
	writeLifecycleText(t, backend, `{"container_name":"tfstate","storage_account_name":"example","use_azuread_auth":true}`)
	fake := &lifecycleFakeTerraform{}
	fake.onInitialize = func(PlanTerraformRequest) error {
		writeLifecycleText(t, backend, `{"container_name":"changed","storage_account_name":"example","use_azuread_auth":true}`)
		return nil
	}

	_, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
		BackendConfig: &backend,
		Deployment:    lifecycleTestDeployment(),
		Root:          lifecycleTestOrdinaryRoot(),
		Selectors:     []string{lifecycleTestResource},
		Tenant:        "tenant",
		Terraform:     fake,
		Workspace:     workspace,
	})
	requireLifecycleFailure(t, err, "INIT_INPUTS_CHANGED")
	if len(fake.planned) != 0 {
		t.Errorf("Plan calls after cross-state init race = %d, want 0", len(fake.planned))
	}
}

func TestPlanEnvironmentRootsRejectsMixedEscapeSurrogateBeforeTerraform(t *testing.T) {
	workspace := t.TempDir()
	backendType := "azurerm"
	writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, &backendType, true)
	writeLifecycleText(t, lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars.json"), `{"zia_url_categories_items":{}}`+"\n")
	backend := filepath.Join(workspace, "backend.json")
	writeLifecycleText(t, backend, `{"tenant_id":"\ud800\n\udc00"}`)
	fake := &lifecycleFakeTerraform{}

	_, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
		BackendConfig: &backend,
		Deployment:    lifecycleTestDeployment(),
		Root:          lifecycleTestOrdinaryRoot(),
		Selectors:     []string{lifecycleTestResource},
		Tenant:        "tenant",
		Terraform:     fake,
		Workspace:     workspace,
	})
	requireLifecycleFailure(t, err, "INVALID_REFERENCE_BACKEND_CONFIG")
	if len(fake.initialized) != 0 || len(fake.planned) != 0 {
		t.Errorf("Terraform calls = (%d init, %d plan), want (0, 0)", len(fake.initialized), len(fake.planned))
	}
}

func TestPlanEnvironmentRootsSelectsHCLAndKeepsSelectionsSingleton(t *testing.T) {
	t.Run("hcl", func(t *testing.T) {
		workspace := t.TempDir()
		writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, nil, false)
		hcl := lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars")
		writeLifecycleText(t, hcl, lifecycleTestResource+"_items = {}\n")
		dep := lifecycleTestDeployment()
		dep.HasTfvarsFormat = true
		dep.TfvarsFormat = "hcl"
		fake := &lifecycleFakeTerraform{}
		if _, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
			Deployment: dep, Root: lifecycleTestOrdinaryRoot(), Selectors: []string{lifecycleTestResource},
			Tenant: "tenant", Terraform: fake, Workspace: workspace,
		}); err != nil {
			t.Fatalf("PlanEnvironmentRoots(hcl) error: %v", err)
		}
		if !reflect.DeepEqual(fake.planned[0].VarFiles, []string{hcl}) {
			t.Errorf("Plan request VarFiles = %#v, want [%q]", fake.planned[0].VarFiles, hcl)
		}
	})

	t.Run("selected_singleton_does_not_require_unselected_config", func(t *testing.T) {
		workspace := t.TempDir()
		writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, nil, false)
		writeLifecycleText(t, lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars.json"), `{"zia_url_categories_items":{}}`+"\n")
		dep := lifecycleTestDeployment()
		root := lifecycleTestRoot(map[string]metadata.JsonObject{
			lifecycleTestResource: {"generate": true, "product": "zia"},
			lifecycleTestSecond:   {"generate": true, "product": "zia"},
		})
		fake := &lifecycleFakeTerraform{}
		var diagnostics []string
		result, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
			Deployment: dep, Root: root, Selectors: []string{lifecycleTestResource},
			Tenant: "tenant", Terraform: fake, Workspace: workspace,
			OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		})
		if err != nil {
			t.Fatalf("PlanEnvironmentRoots(singleton selection) error: %v", err)
		}
		if result.Planned != 1 {
			t.Errorf("PlanEnvironmentRoots(singleton selection).Planned = %d, want 1", result.Planned)
		}
		if len(fake.initialized) != 1 || len(fake.planned) != 1 {
			t.Errorf("Terraform calls = (%d init, %d plan), want (1, 1)", len(fake.initialized), len(fake.planned))
		}
		for _, diagnostic := range diagnostics {
			if strings.Contains(diagnostic, "WHOLE_ROOT_SELECTION") || strings.Contains(diagnostic, "selects whole root") {
				t.Errorf("PlanEnvironmentRoots(singleton selection) diagnostic = %q, want no whole-root selection diagnostic", diagnostic)
			}
		}
	})
}

func TestPlanEnvironmentRootsMutationAndTerraformFailuresRemoveSavedPair(t *testing.T) {
	tests := []struct {
		name     string
		wantCode string
		prepare  func(t *testing.T, workspace, config string, fake *lifecycleFakeTerraform)
	}{
		{
			name: "init_mutation", wantCode: "INIT_INPUTS_CHANGED",
			prepare: func(t *testing.T, workspace, _ string, fake *lifecycleFakeTerraform) {
				fake.onInitialize = func(PlanTerraformRequest) error {
					writeLifecycleText(t, filepath.Join(workspace, "modules", lifecycleTestResource, "main.tf"), "# changed during init\n")
					return nil
				}
			},
		},
		{
			name: "plan_mutation", wantCode: "PLAN_INPUTS_CHANGED",
			prepare: func(t *testing.T, _ string, config string, fake *lifecycleFakeTerraform) {
				fake.onPlan = func(PlanTerraformRequest) error {
					writeLifecycleText(t, config, `{"zia_url_categories_items":{"changed":{}}}`+"\n")
					return nil
				}
			},
		},
		{
			name: "terraform_failure",
			prepare: func(_ *testing.T, _ string, _ string, fake *lifecycleFakeTerraform) {
				fake.onPlan = func(PlanTerraformRequest) error { return errors.New("fake plan failed") }
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			directory := writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, nil, false)
			config := lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars.json")
			writeLifecycleText(t, config, `{"zia_url_categories_items":{}}`+"\n")
			fake := &lifecycleFakeTerraform{}
			test.prepare(t, workspace, config, fake)
			_, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
				Deployment: lifecycleTestDeployment(), Root: lifecycleTestOrdinaryRoot(), Save: true,
				Selectors: []string{lifecycleTestResource}, Tenant: "tenant", Terraform: fake, Workspace: workspace,
			})
			if test.wantCode == "" {
				if err == nil || err.Error() != "fake plan failed" {
					t.Errorf("PlanEnvironmentRoots(terraform failure) error = %v, want fake plan failed", err)
				}
			} else {
				requireLifecycleFailure(t, err, test.wantCode)
			}
			for _, name := range []string{"tfplan", "tfplan.sources"} {
				if _, statErr := os.Stat(filepath.Join(directory, name)); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("os.Stat(%s) error = %v, want os.ErrNotExist", name, statErr)
				}
			}
		})
	}
}

func TestPlanEnvironmentRootsFailedInitAndMissingSavedOutputRemovePartialArtifacts(t *testing.T) {
	for _, phase := range []string{"init", "missing_plan"} {
		t.Run(phase, func(t *testing.T) {
			workspace := t.TempDir()
			directory := writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, nil, false)
			writeLifecycleText(t, lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars.json"), `{"zia_url_categories_items":{}}`+"\n")
			fake := &lifecycleFakeTerraform{}
			if phase == "init" {
				fake.onInitialize = func(PlanTerraformRequest) error {
					writeLifecycleText(t, filepath.Join(directory, "tfplan"), "partial-plan")
					writeLifecycleText(t, filepath.Join(directory, "tfplan.sources"), "partial-sources")
					return errors.New("fake init failed")
				}
			} else {
				fake.onPlan = func(request PlanTerraformRequest) error {
					if err := os.Remove(filepath.Join(request.Directory, "tfplan")); err != nil {
						return err
					}
					return nil
				}
			}
			_, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
				Deployment: lifecycleTestDeployment(), Root: lifecycleTestOrdinaryRoot(), Save: true,
				Selectors: []string{lifecycleTestResource}, Tenant: "tenant", Terraform: fake, Workspace: workspace,
			})
			if phase == "init" {
				if err == nil || err.Error() != "fake init failed" {
					t.Errorf("PlanEnvironmentRoots(failed init) error = %v, want fake init failed", err)
				}
			} else {
				requireLifecycleFailure(t, err, "MISSING_SAVED_PLAN")
			}
			for _, name := range []string{"tfplan", "tfplan.sources"} {
				if _, statErr := os.Stat(filepath.Join(directory, name)); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("os.Stat(%s) error = %v, want os.ErrNotExist", name, statErr)
				}
			}
		})
	}
}

func TestPlanEnvironmentRootsNoConfigReportsSkipAndNoRoots(t *testing.T) {
	workspace := t.TempDir()
	writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, nil, false)
	var diagnostics []string
	_, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
		Deployment:   lifecycleTestDeployment(),
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root:         lifecycleTestOrdinaryRoot(), Selectors: []string{lifecycleTestResource},
		Tenant: "tenant", Terraform: &lifecycleFakeTerraform{}, Workspace: workspace,
	})
	requireLifecycleFailure(t, err, "NO_ROOTS_PLANNED")
	want := []string{"skip " + lifecycleTestResource + " (no config/tenant/" + lifecycleTestResource + ".auto.tfvars.json)"}
	if !reflect.DeepEqual(diagnostics, want) {
		t.Errorf("PlanEnvironmentRoots(no config) diagnostics = %#v, want %#v", diagnostics, want)
	}
}

func TestPlanEnvironmentRootsImportsOnlySkipsDerivedRoot(t *testing.T) {
	workspace := t.TempDir()
	directory := lifecycleTestEnvDirectory(workspace, "tenant", lifecycleTestDerived)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(derived root) error: %v", err)
	}
	writeLifecycleText(t, filepath.Join(directory, "tfplan"), "seeded-plan")
	writeLifecycleText(t, filepath.Join(directory, "tfplan.sources"), "seeded-sources\n")
	writeLifecycleText(t, lifecycleTestConfigPath(workspace, "tenant", lifecycleTestDerived, ".auto.tfvars.json"), `{"items":{}}`+"\n")
	root := lifecycleTestRoot(map[string]metadata.JsonObject{
		lifecycleTestDerived: {"generate": true, "derive": map[string]any{"from": "zpa_policy_access_rule"}, "product": "zpa"},
	})
	fake := &lifecycleFakeTerraform{}
	var diagnostics []string
	_, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
		Deployment: lifecycleTestDeployment(), ImportsOnly: true,
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root:         root, Save: true, Selectors: []string{lifecycleTestDerived}, Tenant: "tenant",
		Terraform: fake, Workspace: workspace,
	})
	requireLifecycleFailure(t, err, "NO_ROOTS_PLANNED")
	want := []string{"skip " + lifecycleTestDerived + " (IMPORTS_ONLY: derived/non-importable member " + lifecycleTestDerived + ")"}
	if !reflect.DeepEqual(diagnostics, want) {
		t.Errorf("PlanEnvironmentRoots(imports-only) diagnostics = %#v, want %#v", diagnostics, want)
	}
	if len(fake.initialized) != 0 {
		t.Errorf("Initialize calls after imports-only skip = %d, want 0", len(fake.initialized))
	}
	if got, readErr := os.ReadFile(filepath.Join(directory, "tfplan")); readErr != nil || string(got) != "seeded-plan" {
		t.Errorf("tfplan after imports-only skip = (%q, %v), want seeded-plan", got, readErr)
	}
	if got, readErr := os.ReadFile(filepath.Join(directory, "tfplan.sources")); readErr != nil || string(got) != "seeded-sources\n" {
		t.Errorf("tfplan.sources after imports-only skip = (%q, %v), want seeded-sources", got, readErr)
	}
}

func TestPlanEnvironmentRootsNonSavePreservesSeededPair(t *testing.T) {
	workspace := t.TempDir()
	directory := writeLifecycleRoot(t, workspace, "tenant", lifecycleTestResource, []string{lifecycleTestResource}, nil, false)
	writeLifecycleText(t, lifecycleTestConfigPath(workspace, "tenant", lifecycleTestResource, ".auto.tfvars.json"), `{"zia_url_categories_items":{}}`+"\n")
	writeLifecycleText(t, filepath.Join(directory, "tfplan"), "seeded-plan")
	writeLifecycleText(t, filepath.Join(directory, "tfplan.sources"), "seeded-sources\n")

	result, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
		Deployment: lifecycleTestDeployment(), Root: lifecycleTestOrdinaryRoot(),
		Selectors: []string{lifecycleTestResource}, Tenant: "tenant",
		Terraform: &lifecycleFakeTerraform{}, Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("PlanEnvironmentRoots(non-save) error: %v", err)
	}
	if result.Planned != 1 {
		t.Errorf("PlanEnvironmentRoots(non-save).Planned = %d, want 1", result.Planned)
	}
	if got, readErr := os.ReadFile(filepath.Join(directory, "tfplan")); readErr != nil || string(got) != "seeded-plan" {
		t.Errorf("tfplan after non-save plan = (%q, %v), want seeded-plan", got, readErr)
	}
	if got, readErr := os.ReadFile(filepath.Join(directory, "tfplan.sources")); readErr != nil || string(got) != "seeded-sources\n" {
		t.Errorf("tfplan.sources after non-save plan = (%q, %v), want seeded-sources", got, readErr)
	}
}

func TestPlanEnvironmentRootsLaterFailurePreservesEarlierCompletedPair(t *testing.T) {
	workspace := t.TempDir()
	first := lifecycleTestSecond
	second := lifecycleTestResource
	firstDirectory := writeLifecycleRoot(t, workspace, "tenant", first, []string{first}, nil, false)
	secondDirectory := writeLifecycleRoot(t, workspace, "tenant", second, []string{second}, nil, false)
	writeLifecycleText(t, lifecycleTestConfigPath(workspace, "tenant", first, ".auto.tfvars.json"), `{"zia_admin_users_items":{}}`+"\n")
	writeLifecycleText(t, lifecycleTestConfigPath(workspace, "tenant", second, ".auto.tfvars.json"), `{"zia_url_categories_items":{}}`+"\n")
	root := lifecycleTestRoot(map[string]metadata.JsonObject{
		first:  {"generate": true, "product": "zia"},
		second: {"generate": true, "product": "zia"},
	})
	fake := &lifecycleFakeTerraform{}
	fake.onPlan = func(request PlanTerraformRequest) error {
		if request.Directory == secondDirectory {
			return errors.New("later plan failed")
		}
		return nil
	}
	var diagnostics []string
	_, err := PlanEnvironmentRoots(PlanEnvironmentRootsOptions{
		Deployment:   lifecycleTestDeployment(),
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root:         root, Save: true, Selectors: []string{second, first}, Tenant: "tenant",
		Terraform: fake, Workspace: workspace,
	})
	if err == nil || err.Error() != "later plan failed" {
		t.Errorf("PlanEnvironmentRoots(later failure) error = %v, want later plan failed", err)
	}
	if len(fake.initialized) != 2 || len(fake.planned) != 2 {
		t.Fatalf("Terraform calls = (%d init, %d plan), want (2, 2)", len(fake.initialized), len(fake.planned))
	}
	if fake.planned[0].Directory != firstDirectory || fake.planned[1].Directory != secondDirectory {
		t.Errorf("Plan directory order = [%q, %q], want [%q, %q]", fake.planned[0].Directory, fake.planned[1].Directory, firstDirectory, secondDirectory)
	}
	wantDiagnostics := []string{"== plan " + first, "== plan " + second}
	if !reflect.DeepEqual(diagnostics, wantDiagnostics) {
		t.Errorf("Plan diagnostics = %#v, want %#v", diagnostics, wantDiagnostics)
	}
	for _, name := range []string{"tfplan", "tfplan.sources"} {
		if _, statErr := os.Stat(filepath.Join(firstDirectory, name)); statErr != nil {
			t.Errorf("os.Stat(first %s) error = %v, want completed pair preserved", name, statErr)
		}
		if _, statErr := os.Stat(filepath.Join(secondDirectory, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("os.Stat(second %s) error = %v, want os.ErrNotExist", name, statErr)
		}
	}
}

func TestCleanPlansWithoutTenantRemovesPairsAcrossTenants(t *testing.T) {
	workspace := t.TempDir()
	for _, tenant := range []string{"alpha", "beta"} {
		directory := lifecycleTestEnvDirectory(workspace, tenant, lifecycleTestResource)
		writeLifecycleText(t, filepath.Join(directory, "tfplan"), tenant)
		writeLifecycleText(t, filepath.Join(directory, "tfplan.sources"), "{}\n")
	}
	var diagnostics []string
	result, err := CleanPlans(CleanPlansOptions{
		Deployment:   lifecycleTestDeployment(),
		OnDiagnostic: func(message string) { diagnostics = append(diagnostics, message) },
		Root:         lifecycleTestOrdinaryRoot(), Selectors: []string{lifecycleTestResource},
		Workspace: workspace,
	})
	if err != nil {
		t.Fatalf("CleanPlans(all tenants) error: %v", err)
	}
	if result.Removed != 2 {
		t.Errorf("CleanPlans(all tenants).Removed = %d, want 2", result.Removed)
	}
	if got := diagnostics[len(diagnostics)-1]; got != "2 stale plan(s) removed" {
		t.Errorf("CleanPlans(all tenants) final diagnostic = %q, want %q", got, "2 stale plan(s) removed")
	}
}

func TestRemoveSavedPlanArtifactsDoesNotRemoveDirectories(t *testing.T) {
	directory := t.TempDir()
	planDirectory := filepath.Join(directory, "tfplan")
	if err := os.Mkdir(planDirectory, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) error: %v", planDirectory, err)
	}
	if _, err := RemoveSavedPlanArtifacts(directory); err == nil {
		t.Fatal("RemoveSavedPlanArtifacts(directory tfplan) error = nil, want unlink failure")
	}
	if info, err := os.Stat(planDirectory); err != nil || !info.IsDir() {
		t.Errorf("os.Stat(%q) = (%v, %v), want existing directory", planDirectory, info, err)
	}
}
