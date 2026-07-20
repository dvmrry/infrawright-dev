package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const (
	planCommandFirstResource  = "zia_admin_users"
	planCommandSecondResource = "zia_url_categories"
	planCommandRootLabel      = "zia_pair"
)

type planCommandFakeTerraform struct {
	initialized []plan.PlanTerraformRequest
	planned     []plan.PlanTerraformRequest
}

func clonePlanCommandRequest(request plan.PlanTerraformRequest) plan.PlanTerraformRequest {
	cloned := request
	if request.BackendConfig != nil {
		value := *request.BackendConfig
		cloned.BackendConfig = &value
	}
	if request.BackendKey != nil {
		value := *request.BackendKey
		cloned.BackendKey = &value
	}
	if request.Environment != nil {
		cloned.Environment = make(map[string]string, len(request.Environment))
		for key, value := range request.Environment {
			cloned.Environment[key] = value
		}
	}
	cloned.VarFiles = append([]string(nil), request.VarFiles...)
	return cloned
}

func (fake *planCommandFakeTerraform) Initialize(request plan.PlanTerraformRequest) error {
	fake.initialized = append(fake.initialized, clonePlanCommandRequest(request))
	return nil
}

func (fake *planCommandFakeTerraform) Plan(request plan.PlanTerraformRequest) error {
	fake.planned = append(fake.planned, clonePlanCommandRequest(request))
	if request.Save {
		return os.WriteFile(filepath.Join(request.Directory, "tfplan"), []byte("opaque-plan"), 0o666)
	}
	return nil
}

func planCommandTestRoot() metadata.LoadedPackRoot {
	resources := make(map[string]metadata.LoadedResourceMetadata)
	for _, resourceType := range []string{planCommandFirstResource, planCommandSecondResource} {
		resources[resourceType] = metadata.LoadedResourceMetadata{
			Type:     resourceType,
			Product:  "zia",
			Provider: "zia",
			Registry: metadata.JsonObject{"generate": true, "product": "zia"},
		}
	}
	return metadata.LoadedPackRoot{
		Packs: metadata.PackMetadata{
			ProviderPrefixes: map[string]string{"zia_": "zia"},
		},
		Resources: resources,
	}
}

func planCommandTestDeployment() deployment.Deployment {
	return deployment.Deployment{
		Overlay: ".",
		Roots: map[string]deployment.RootProviderConfig{
			"zia": {
				HasGroups: true,
				Groups: map[string][]string{
					planCommandRootLabel: {planCommandFirstResource, planCommandSecondResource},
				},
			},
		},
	}
}

func writePlanCommandText(t *testing.T, filePath, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(filePath), err)
	}
	if err := os.WriteFile(filePath, []byte(text), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", filePath, err)
	}
}

func preparePlanCommandWorkspace(t *testing.T, workspace, tenant string) string {
	t.Helper()
	directory := filepath.Join(workspace, "envs", tenant, planCommandRootLabel)
	var mainText strings.Builder
	for _, resourceType := range []string{planCommandFirstResource, planCommandSecondResource} {
		moduleDirectory := filepath.Join(workspace, "modules", resourceType)
		writePlanCommandText(t, filepath.Join(moduleDirectory, "main.tf"), "# fixture module\n")
		relative, err := filepath.Rel(directory, moduleDirectory)
		if err != nil {
			t.Fatalf("filepath.Rel(%q, %q) error: %v", directory, moduleDirectory, err)
		}
		fmt.Fprintf(
			&mainText,
			"module %q {\n  source = %q\n  items = var.%s_items\n}\n\n",
			resourceType,
			filepath.ToSlash(relative),
			resourceType,
		)
		writePlanCommandText(
			t,
			filepath.Join(workspace, "config", tenant, resourceType+".auto.tfvars.json"),
			fmt.Sprintf("{%q:{}}\n", resourceType+"_items"),
		)
	}
	writePlanCommandText(t, filepath.Join(directory, "main.tf"), mainText.String())
	return directory
}

func planCommandTestDependencies() planCommandDependencies {
	return planCommandDependencies{
		createPlanTerraform: func(plan.CreatePlanTerraformOptions) plan.PlanTerraform {
			return &planCommandFakeTerraform{}
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
		planEnvironmentRoots: func(plan.PlanEnvironmentRootsOptions) (plan.PlanRunResult, error) {
			return plan.PlanRunResult{}, nil
		},
		cleanPlans: func(plan.CleanPlansOptions) (plan.CleanPlansResult, error) {
			return plan.CleanPlansResult{}, nil
		},
		resolveTerraformExecutable: func(selected string, _ map[string]string) (string, error) {
			return selected, nil
		},
		stderr: &bytes.Buffer{},
	}
}

func TestPlanCommandComposesExactOptionsAndResolvesTerraformLazily(t *testing.T) {
	dependencies := planCommandTestDependencies()
	environmentCalls := 0
	dependencies.environment = func() map[string]string {
		environmentCalls++
		return map[string]string{
			"BASE":                     "base",
			"INFRAWRIGHT_PACKS":        "/environment/packs",
			"INFRAWRIGHT_PACK_PROFILE": "/environment/profile.json",
			"TF":                       "/environment/terraform",
		}
	}
	var gotPack packOptionDefaults
	var gotDeployment string
	dependencies.loadPackAndDeployment = func(
		pack packOptionDefaults,
		deploymentPath string,
	) (metadata.LoadedPackRoot, deployment.Deployment, error) {
		gotPack = pack
		gotDeployment = deploymentPath
		return metadata.LoadedPackRoot{}, deployment.Deployment{}, nil
	}
	var gotPlanOptions plan.PlanEnvironmentRootsOptions
	dependencies.planEnvironmentRoots = func(options plan.PlanEnvironmentRootsOptions) (plan.PlanRunResult, error) {
		gotPlanOptions = options
		options.OnDiagnostic("first diagnostic")
		options.OnDiagnostic("second diagnostic")
		request := plan.PlanTerraformRequest{Directory: "/workspace/env"}
		if err := options.Terraform.Initialize(request); err != nil {
			return plan.PlanRunResult{}, err
		}
		if err := options.Terraform.Plan(request); err != nil {
			return plan.PlanRunResult{}, err
		}
		return plan.PlanRunResult{Planned: 1}, nil
	}
	resolveCalls := 0
	dependencies.resolveTerraformExecutable = func(selected string, environment map[string]string) (string, error) {
		resolveCalls++
		if got, want := selected, "/flag/terraform-two"; got != want {
			t.Errorf("Terraform selection = %q, want %q", got, want)
		}
		if got, want := environment["BASE"], "base"; got != want {
			t.Errorf("Terraform environment BASE = %q, want %q", got, want)
		}
		return "/resolved/terraform", nil
	}
	fakeTerraform := &planCommandFakeTerraform{}
	createCalls := 0
	dependencies.createPlanTerraform = func(options plan.CreatePlanTerraformOptions) plan.PlanTerraform {
		createCalls++
		if got, want := options.TerraformExecutable, "/resolved/terraform"; got != want {
			t.Errorf("CreatePlanTerraform TerraformExecutable = %q, want %q", got, want)
		}
		if got, want := options.Environment["BASE"], "base"; got != want {
			t.Errorf("CreatePlanTerraform Environment[BASE] = %q, want %q", got, want)
		}
		return fakeTerraform
	}
	stderr := &bytes.Buffer{}
	dependencies.stderr = stderr

	status, err := planCommandWithDependencies([]string{
		"--tenant", "old-tenant",
		"--resource", planCommandFirstResource,
		"--backend-config", "old-backend.hcl",
		"--terraform", "/flag/terraform-one",
		"--tenant", "tenant",
		"--resource", planCommandSecondResource,
		"--backend-config", "backend.hcl",
		"--terraform", "/flag/terraform-two",
		"--imports-only",
		"--save",
		"--root", "/explicit/packs",
		"--profile", "/explicit/profile.json",
		"--catalog", "/explicit/catalog.json",
		"--deployment", "/explicit/deployment.json",
	}, dependencies)
	if err != nil {
		t.Fatalf("planCommandWithDependencies(...) error: %v", err)
	}
	if status != 0 {
		t.Errorf("planCommandWithDependencies(...) status = %d, want 0", status)
	}
	if got, want := gotPack, (packOptionDefaults{
		root: "/explicit/packs", profile: "/explicit/profile.json", catalog: "/explicit/catalog.json",
	}); got != want {
		t.Errorf("loaded pack options = %+v, want %+v", got, want)
	}
	if got, want := gotDeployment, "/explicit/deployment.json"; got != want {
		t.Errorf("loaded deployment path = %q, want %q", got, want)
	}
	if got, want := gotPlanOptions.Tenant, "tenant"; got != want {
		t.Errorf("PlanEnvironmentRoots Tenant = %q, want %q", got, want)
	}
	if got, want := gotPlanOptions.Selectors, []string{planCommandFirstResource, planCommandSecondResource}; !reflect.DeepEqual(got, want) {
		t.Errorf("PlanEnvironmentRoots Selectors = %#v, want %#v", got, want)
	}
	if !gotPlanOptions.ImportsOnly || !gotPlanOptions.Save {
		t.Errorf("PlanEnvironmentRoots ImportsOnly/Save = %t/%t, want true/true", gotPlanOptions.ImportsOnly, gotPlanOptions.Save)
	}
	if gotPlanOptions.BackendConfig == nil || *gotPlanOptions.BackendConfig != "backend.hcl" {
		t.Errorf("PlanEnvironmentRoots BackendConfig = %v, want backend.hcl", gotPlanOptions.BackendConfig)
	}
	if got, want := gotPlanOptions.Workspace, "/workspace"; got != want {
		t.Errorf("PlanEnvironmentRoots Workspace = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "first diagnostic\nsecond diagnostic\n"; got != want {
		t.Errorf("plan command stderr = %q, want %q", got, want)
	}
	if got, want := environmentCalls, 2; got != want {
		t.Errorf("environment snapshot calls = %d, want %d", got, want)
	}
	if resolveCalls != 1 || createCalls != 1 {
		t.Errorf("Terraform resolve/create calls = %d/%d, want 1/1", resolveCalls, createCalls)
	}
	if got, want := len(fakeTerraform.initialized), 1; got != want {
		t.Fatalf("Terraform Initialize calls = %d, want %d", got, want)
	}
	if got, want := len(fakeTerraform.planned), 1; got != want {
		t.Fatalf("Terraform Plan calls = %d, want %d", got, want)
	}
}

func TestPlanCliOptionsPreservesDefaultsAndEnvironmentFallbacks(t *testing.T) {
	dependencies := planCommandTestDependencies()
	dependencies.environment = func() map[string]string {
		return map[string]string{
			"INFRAWRIGHT_DEPLOYMENT":   "/environment/deployment.json",
			"INFRAWRIGHT_PACKS":        "",
			"INFRAWRIGHT_PACK_PROFILE": "/environment/profile.json",
		}
	}
	dependencies.deploymentPath = func(environment map[string]string) (string, error) {
		if got, want := environment["INFRAWRIGHT_DEPLOYMENT"], "/environment/deployment.json"; got != want {
			t.Errorf("deployment environment = %q, want %q", got, want)
		}
		return environment["INFRAWRIGHT_DEPLOYMENT"], nil
	}
	options, err := planCliOptionsWithDependencies(commandInput{
		Flags: commandFlags{}, Options: map[string][]string{"--tenant": {"tenant"}},
	}, dependencies)
	if err != nil {
		t.Fatalf("planCliOptionsWithDependencies(defaults) error: %v", err)
	}
	wantPack := packOptionDefaults{
		root:    filepath.Join("/package", "packs"),
		profile: "/environment/profile.json",
		catalog: filepath.Join("/package", "packsets", "full.json"),
	}
	if got := options.pack; got != wantPack {
		t.Errorf("plan CLI default pack options = %+v, want %+v", got, wantPack)
	}
	if got, want := options.deployment, "/environment/deployment.json"; got != want {
		t.Errorf("plan CLI default deployment = %q, want %q", got, want)
	}
	if options.backendConfig != nil || options.terraform != nil || options.importsOnly || options.save {
		t.Errorf("plan CLI optional values = %+v, want omitted/false", options)
	}

	_, err = planCliOptionsWithDependencies(commandInput{Flags: commandFlags{}, Options: map[string][]string{}}, dependencies)
	var exit *cliExit
	if !errors.As(err, &exit) {
		t.Fatalf("planCliOptionsWithDependencies(missing tenant) error = %T(%v), want *cliExit", err, err)
	}
	if got, want := exit.message, "plan requires --tenant"; got != want {
		t.Errorf("missing tenant error = %q, want %q", got, want)
	}
}

func TestPlanCommandDoesNotResolveTerraformWhenLifecycleDoesNotInitialize(t *testing.T) {
	dependencies := planCommandTestDependencies()
	resolveCalls := 0
	createCalls := 0
	dependencies.resolveTerraformExecutable = func(string, map[string]string) (string, error) {
		resolveCalls++
		return "/terraform", nil
	}
	dependencies.createPlanTerraform = func(plan.CreatePlanTerraformOptions) plan.PlanTerraform {
		createCalls++
		return &planCommandFakeTerraform{}
	}
	status, err := planCommandWithDependencies([]string{"--tenant", "tenant"}, dependencies)
	if err != nil {
		t.Fatalf("planCommandWithDependencies(zero lifecycle Terraform calls) error: %v", err)
	}
	if status != 0 {
		t.Errorf("planCommandWithDependencies(zero lifecycle Terraform calls) status = %d, want 0", status)
	}
	if resolveCalls != 0 || createCalls != 0 {
		t.Errorf("Terraform resolve/create calls = %d/%d, want 0/0", resolveCalls, createCalls)
	}
}

func TestPlanAndCleanPlansCommandsPreserveSavedPairContract(t *testing.T) {
	workspace := t.TempDir()
	directory := preparePlanCommandWorkspace(t, workspace, "tenant")
	writePlanCommandText(t, filepath.Join(directory, "unrelated.txt"), "keep\n")
	backendConfig := filepath.Join(workspace, "backend.hcl")
	writePlanCommandText(t, backendConfig, "storage_account_name = \"fixture\"\n")

	dependencies := planCommandTestDependencies()
	dependencies.packageRoot = func() (string, error) { return "/package", nil }
	dependencies.currentDirectory = func() (string, error) { return workspace, nil }
	dependencies.environment = func() map[string]string {
		return map[string]string{"BASE": "base", "TF": "/selected/from-env"}
	}
	dependencies.loadPackAndDeployment = func(
		packOptionDefaults,
		string,
	) (metadata.LoadedPackRoot, deployment.Deployment, error) {
		return planCommandTestRoot(), planCommandTestDeployment(), nil
	}
	dependencies.planEnvironmentRoots = plan.PlanEnvironmentRoots
	dependencies.cleanPlans = plan.CleanPlans
	resolveCalls := 0
	dependencies.resolveTerraformExecutable = func(selected string, _ map[string]string) (string, error) {
		resolveCalls++
		if got, want := selected, "/selected/from-env"; got != want {
			t.Errorf("ResolveTerraformExecutable selected = %q, want %q", got, want)
		}
		return "/resolved/terraform", nil
	}
	fakeTerraform := &planCommandFakeTerraform{}
	createCalls := 0
	dependencies.createPlanTerraform = func(options plan.CreatePlanTerraformOptions) plan.PlanTerraform {
		createCalls++
		if got, want := options.TerraformExecutable, "/resolved/terraform"; got != want {
			t.Errorf("CreatePlanTerraform TerraformExecutable = %q, want %q", got, want)
		}
		return fakeTerraform
	}
	stderr := &bytes.Buffer{}
	dependencies.stderr = stderr
	common := []string{
		"--root", "/packs",
		"--profile", "/profile.json",
		"--catalog", "/catalog.json",
		"--deployment", "/deployment.json",
		"--tenant", "tenant",
		"--resource", planCommandSecondResource,
	}
	planArguments := append(append([]string(nil), common...), "--backend-config", backendConfig, "--save")
	status, err := planCommandWithDependencies(planArguments, dependencies)
	if err != nil {
		t.Fatalf("planCommandWithDependencies(saved grouped root) error: %v", err)
	}
	if status != 0 {
		t.Errorf("planCommandWithDependencies(saved grouped root) status = %d, want 0", status)
	}
	wantPlanDiagnostics := "NOTE: selecting " + planCommandSecondResource +
		" selects whole root " + planCommandRootLabel +
		"; also operating on " + planCommandFirstResource + "\n" +
		"== plan " + planCommandRootLabel + "\n"
	if got, want := stderr.String(), wantPlanDiagnostics; got != want {
		t.Errorf("plan command stderr = %q, want %q", got, want)
	}
	if resolveCalls != 1 || createCalls != 1 {
		t.Errorf("Terraform resolve/create calls = %d/%d, want 1/1", resolveCalls, createCalls)
	}
	if got, want := len(fakeTerraform.initialized), 1; got != want {
		t.Fatalf("Terraform Initialize calls = %d, want %d", got, want)
	}
	if got, want := len(fakeTerraform.planned), 1; got != want {
		t.Fatalf("Terraform Plan calls = %d, want %d", got, want)
	}
	request := fakeTerraform.planned[0]
	if got, want := request.Directory, directory; got != want {
		t.Errorf("Terraform Plan Directory = %q, want %q", got, want)
	}
	wantVarFiles := []string{
		filepath.Join(workspace, "config", "tenant", planCommandFirstResource+".auto.tfvars.json"),
		filepath.Join(workspace, "config", "tenant", planCommandSecondResource+".auto.tfvars.json"),
	}
	if got := request.VarFiles; !reflect.DeepEqual(got, wantVarFiles) {
		t.Errorf("Terraform Plan VarFiles = %#v, want %#v", got, wantVarFiles)
	}
	if !request.Save {
		t.Error("Terraform Plan Save = false, want true")
	}
	if request.BackendConfig == nil || *request.BackendConfig != backendConfig {
		t.Errorf("Terraform Plan BackendConfig = %v, want %q", request.BackendConfig, backendConfig)
	}
	if request.BackendKey == nil || *request.BackendKey != "tenant/"+planCommandRootLabel+".tfstate" {
		t.Errorf("Terraform Plan BackendKey = %v, want tenant/%s.tfstate", request.BackendKey, planCommandRootLabel)
	}
	planPath := filepath.Join(directory, "tfplan")
	sourcesPath := filepath.Join(directory, "tfplan.sources")
	if got, err := os.ReadFile(planPath); err != nil || string(got) != "opaque-plan" {
		t.Errorf("os.ReadFile(%q) = %q, %v, want opaque-plan, nil", planPath, got, err)
	}
	sources, err := os.ReadFile(sourcesPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", sourcesPath, err)
	}
	if !regexp.MustCompile(`^\{"sha256": "[0-9a-f]{64}", "version": 2\}\n$`).Match(sources) {
		t.Errorf("saved sources bytes = %q, want v2 fingerprint line", sources)
	}
	if runtime.GOOS != "windows" {
		for _, filePath := range []string{planPath, sourcesPath} {
			info, err := os.Stat(filePath)
			if err != nil {
				t.Fatalf("os.Stat(%q) error: %v", filePath, err)
			}
			if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
				t.Errorf("os.Stat(%q).Mode = %04o, want %04o", filePath, got, want)
			}
		}
	}

	stderr.Reset()
	status, err = cleanPlansCommandWithDependencies(common, dependencies)
	if err != nil {
		t.Fatalf("cleanPlansCommandWithDependencies(saved grouped root) error: %v", err)
	}
	if status != 0 {
		t.Errorf("cleanPlansCommandWithDependencies(saved grouped root) status = %d, want 0", status)
	}
	wantCleanDiagnostics := strings.Join([]string{
		"NOTE: selecting " + planCommandSecondResource + " selects whole root " + planCommandRootLabel +
			"; also operating on " + planCommandFirstResource,
		"removed envs/tenant/" + planCommandRootLabel + "/tfplan",
		"removed envs/tenant/" + planCommandRootLabel + "/tfplan.sources",
		"1 stale plan(s) removed",
		"",
	}, "\n")
	if got := stderr.String(); got != wantCleanDiagnostics {
		t.Errorf("clean-plans command stderr = %q, want %q", got, wantCleanDiagnostics)
	}
	for _, filePath := range []string{planPath, sourcesPath} {
		if _, err := os.Stat(filePath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("os.Stat(%q) error = %v, want os.ErrNotExist", filePath, err)
		}
	}
	unrelated := filepath.Join(directory, "unrelated.txt")
	if got, err := os.ReadFile(unrelated); err != nil || string(got) != "keep\n" {
		t.Errorf("os.ReadFile(%q) = %q, %v, want keep\\n, nil", unrelated, got, err)
	}
	if resolveCalls != 1 || createCalls != 1 {
		t.Errorf("Terraform resolve/create calls after clean-plans = %d/%d, want unchanged 1/1", resolveCalls, createCalls)
	}
}

func TestPlanCommandUsesExactTerraformArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the operational Terraform execution platform excludes Windows")
	}
	workspace := t.TempDir()
	directory := preparePlanCommandWorkspace(t, workspace, "tenant")
	executable := filepath.Join(workspace, "terraform-fake")
	logPath := filepath.Join(workspace, "terraform.log")
	writePlanCommandText(t, executable, strings.Join([]string{
		"#!/bin/sh",
		`printf '%s|%s\n' "$PWD" "$*" >> "$FAKE_TF_LOG"`,
		`if [ "$1" = "plan" ]; then`,
		`  for argument in "$@"; do`,
		`    case "$argument" in -out=*) printf '%s' 'opaque-plan' > "${argument#-out=}";; esac`,
		`  done`,
		`fi`,
		"exit 0",
		"",
	}, "\n"))
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatalf("os.Chmod(%q) error: %v", executable, err)
	}

	dependencies := defaultPlanCommandDependencies()
	dependencies.packageRoot = func() (string, error) { return "/package", nil }
	dependencies.currentDirectory = func() (string, error) { return workspace, nil }
	dependencies.environment = func() map[string]string {
		return map[string]string{"FAKE_TF_LOG": logPath, "TF": executable}
	}
	dependencies.loadPackAndDeployment = func(
		packOptionDefaults,
		string,
	) (metadata.LoadedPackRoot, deployment.Deployment, error) {
		return planCommandTestRoot(), planCommandTestDeployment(), nil
	}
	dependencies.stderr = &bytes.Buffer{}
	status, err := planCommandWithDependencies([]string{
		"--tenant", "tenant",
		"--resource", planCommandSecondResource,
		"--save",
		"--root", "/packs",
		"--profile", "/profile.json",
		"--catalog", "/catalog.json",
		"--deployment", "/deployment.json",
	}, dependencies)
	if err != nil {
		t.Fatalf("planCommandWithDependencies(fake Terraform) error: %v", err)
	}
	if status != 0 {
		t.Errorf("planCommandWithDependencies(fake Terraform) status = %d, want 0", status)
	}
	firstConfig := filepath.Join(
		workspace,
		"config",
		"tenant",
		planCommandFirstResource+".auto.tfvars.json",
	)
	secondConfig := filepath.Join(
		workspace,
		"config",
		"tenant",
		planCommandSecondResource+".auto.tfvars.json",
	)
	physicalDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks(%q) error: %v", directory, err)
	}
	wantLog := strings.Join([]string{
		physicalDirectory + "|init -input=false",
		physicalDirectory + "|plan -input=false -var-file=" + firstConfig +
			" -var-file=" + secondConfig + " -out=tfplan",
		"",
	}, "\n")
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", logPath, err)
	}
	if got := string(logBytes); got != wantLog {
		t.Errorf("fake Terraform argv log = %q, want %q", got, wantLog)
	}
}

func TestCleanPlansCommandAllowsNoTenantAndRejectsDuplicateTenant(t *testing.T) {
	dependencies := planCommandTestDependencies()
	var gotOptions plan.CleanPlansOptions
	dependencies.cleanPlans = func(options plan.CleanPlansOptions) (plan.CleanPlansResult, error) {
		gotOptions = options
		options.OnDiagnostic("0 stale plan(s) removed")
		return plan.CleanPlansResult{}, nil
	}
	stderr := &bytes.Buffer{}
	dependencies.stderr = stderr
	status, err := cleanPlansCommandWithDependencies([]string{
		"--resource", planCommandFirstResource,
		"--resource", planCommandSecondResource,
	}, dependencies)
	if err != nil {
		t.Fatalf("cleanPlansCommandWithDependencies(no tenant) error: %v", err)
	}
	if status != 0 {
		t.Errorf("cleanPlansCommandWithDependencies(no tenant) status = %d, want 0", status)
	}
	if gotOptions.Tenant != nil {
		t.Errorf("CleanPlans Tenant = %q, want nil", *gotOptions.Tenant)
	}
	if got, want := gotOptions.Selectors, []string{planCommandFirstResource, planCommandSecondResource}; !reflect.DeepEqual(got, want) {
		t.Errorf("CleanPlans Selectors = %#v, want %#v", got, want)
	}
	if got, want := stderr.String(), "0 stale plan(s) removed\n"; got != want {
		t.Errorf("clean-plans stderr = %q, want %q", got, want)
	}

	_, err = cleanPlansCommandWithDependencies([]string{
		"--tenant", "one",
		"--tenant", "two",
	}, dependencies)
	var exit *cliExit
	if !errors.As(err, &exit) {
		t.Fatalf("cleanPlansCommandWithDependencies(duplicate tenant) error = %T(%v), want *cliExit", err, err)
	}
	if got, want := exit.message, "--tenant may be specified only once"; got != want {
		t.Errorf("duplicate tenant error = %q, want %q", got, want)
	}
}

func TestPlanCommandFailureRetainsLegacyCallerClassification(t *testing.T) {
	dependencies := planCommandTestDependencies()
	dependencies.planEnvironmentRoots = func(plan.PlanEnvironmentRootsOptions) (plan.PlanRunResult, error) {
		return plan.PlanRunResult{}, procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "INVALID_TENANT",
			Category: procerr.CategoryDomain,
			Message:  "tenant must be non-empty",
		})
	}
	_, err := legacyPlanLifecycleCommand(func() (int, error) {
		return planCommandWithDependencies([]string{"--tenant", ""}, dependencies)
	})
	var exit *cliExit
	if !errors.As(err, &exit) {
		t.Fatalf("legacyPlanLifecycleCommand(plan invalid tenant) error = %T(%v), want *cliExit", err, err)
	}
	if got, want := exit.status, 2; got != want {
		t.Errorf("legacy plan invalid-tenant status = %d, want %d", got, want)
	}
	if got, want := exit.message, "tenant must be non-empty"; got != want {
		t.Errorf("legacy plan invalid-tenant message = %q, want %q", got, want)
	}
}

func TestLazyPlanTerraformRejectsPlanBeforeInitialization(t *testing.T) {
	adapter := &lazyPlanTerraform{
		create:      func(plan.CreatePlanTerraformOptions) plan.PlanTerraform { return &planCommandFakeTerraform{} },
		environment: func() map[string]string { return map[string]string{} },
		resolve: func(string, map[string]string) (string, error) {
			return "/terraform", nil
		},
	}
	err := adapter.Plan(plan.PlanTerraformRequest{})
	if got, want := err.Error(), "Terraform plan adapter was used before initialization"; got != want {
		t.Errorf("lazyPlanTerraform.Plan(before Initialize) error = %q, want %q", got, want)
	}
}
