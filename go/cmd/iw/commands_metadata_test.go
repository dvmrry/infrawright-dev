package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

func metadataCommandTestDependencies(output *bytes.Buffer) metadataCommandDependencies {
	return metadataCommandDependencies{
		absolutePath: func(value string) (string, error) { return "absolute:" + value, nil },
		packageRoot:  func() (string, error) { return "/package", nil },
		environment:  func(string) string { return "" },
		stdout:       output,
		validatePackResources: func(metadata.PackMetadata, []string) (metadata.LoadedRegistry, metadata.LoadedOverrides, error) {
			return metadata.LoadedRegistry{}, metadata.LoadedOverrides{}, nil
		},
	}
}

func requireCLIExit(t *testing.T, err error, status int, stdout bool) {
	t.Helper()
	var exit *cliExit
	if !errors.As(err, &exit) {
		t.Fatalf("error = %T %v, want *cliExit", err, err)
	}
	if exit.status != status || exit.stdout != stdout {
		t.Fatalf("cliExit = {status:%d stdout:%t}, want {status:%d stdout:%t}", exit.status, exit.stdout, status, stdout)
	}
}

func TestCheckPackCommandUsesExclusiveSelectorAndFalseyEnvironmentFallback(t *testing.T) {
	var output bytes.Buffer
	dependencies := metadataCommandTestDependencies(&output)
	rootCalls := 0
	dependencies.packageRoot = func() (string, error) {
		rootCalls++
		return "/package", nil
	}
	var got metadata.ValidatePackAuthoringOptions
	wantMetadata := metadata.PackMetadata{Root: "/validated"}
	wantNames := []string{"sample"}
	resourceValidationCalls := 0
	dependencies.validatePackAuthoring = func(options metadata.ValidatePackAuthoringOptions) (metadata.ValidatePackAuthoringResult, error) {
		got = options
		return metadata.ValidatePackAuthoringResult{Metadata: wantMetadata, Names: wantNames}, nil
	}
	dependencies.validatePackResources = func(gotMetadata metadata.PackMetadata, gotNames []string) (metadata.LoadedRegistry, metadata.LoadedOverrides, error) {
		resourceValidationCalls++
		if !reflect.DeepEqual(gotMetadata, wantMetadata) || !reflect.DeepEqual(gotNames, wantNames) {
			t.Fatalf("resource validation inputs = (%#v, %#v), want (%#v, %#v)", gotMetadata, gotNames, wantMetadata, wantNames)
		}
		return metadata.LoadedRegistry{}, metadata.LoadedOverrides{}, nil
	}
	status, err := checkPackCommandWithDependencies(
		[]string{"PACK=sample"},
		dependencies,
	)
	if err != nil || status != 0 {
		t.Fatalf("checkPackCommandWithDependencies = (%d, %v), want (0, nil)", status, err)
	}
	if rootCalls != 1 || got.Root != "absolute:"+filepath.Join("/package", "packs") {
		t.Fatalf("root calls/options = %d/%#v", rootCalls, got)
	}
	if got.Pack == nil || *got.Pack != "sample" {
		t.Fatalf("selected pack = %#v, want sample", got.Pack)
	}
	if output.String() != "validated packs: sample\n" {
		t.Fatalf("stdout = %q", output.String())
	}
	if resourceValidationCalls != 1 {
		t.Fatalf("resource validation calls = %d, want 1", resourceValidationCalls)
	}

	if _, err := checkPackCommandWithDependencies([]string{"PACK=other", "--pack", "sample"}, dependencies); err == nil || err.Error() != "check-pack accepts only one of --pack or PACK=<name>" {
		t.Fatalf("mixed check-pack selectors error = %v, want exclusive-selector usage error", err)
	}

	dependencies.packageRoot = func() (string, error) { return "", errors.New("must not resolve package root") }
	dependencies.environment = func(string) string {
		t.Fatal("explicit root read INFRAWRIGHT_PACKS")
		return ""
	}
	dependencies.absolutePath = func(value string) (string, error) { return value, nil }
	dependencies.validatePackAuthoring = func(options metadata.ValidatePackAuthoringOptions) (metadata.ValidatePackAuthoringResult, error) {
		if options.Root != "explicit" {
			t.Fatalf("explicit root = %q", options.Root)
		}
		return metadata.ValidatePackAuthoringResult{}, nil
	}
	dependencies.validatePackResources = func(metadata.PackMetadata, []string) (metadata.LoadedRegistry, metadata.LoadedOverrides, error) {
		return metadata.LoadedRegistry{}, metadata.LoadedOverrides{}, nil
	}
	output.Reset()
	if _, err := checkPackCommandWithDependencies([]string{"--root", "explicit"}, dependencies); err != nil {
		t.Fatalf("explicit check-pack: %v", err)
	}
}

func TestMetadataCommandHelpStreamAndStatusContracts(t *testing.T) {
	var output bytes.Buffer
	dependencies := metadataCommandTestDependencies(&output)
	packageRootCalls := 0
	environmentCalls := 0
	dependencies.packageRoot = func() (string, error) {
		packageRootCalls++
		return "", nil
	}
	dependencies.environment = func(string) string {
		environmentCalls++
		return ""
	}
	status, err := checkPackCommandWithDependencies([]string{"--help"}, dependencies)
	if err != nil || status != 0 {
		t.Fatalf("check-pack --help = (%d, %v), want (0, nil)", status, err)
	}
	if packageRootCalls != 0 || environmentCalls != 0 {
		t.Fatalf("check-pack help dependency calls = packageRoot:%d environment:%d, want zero", packageRootCalls, environmentCalls)
	}
	status, err = deploymentCommandWithDependencies([]string{"--help"}, dependencies)
	if err != nil || status != 0 {
		t.Fatalf("deployment --help = (%d, %v), want (0, nil)", status, err)
	}
	if packageRootCalls != 0 || environmentCalls != 0 {
		t.Fatalf("metadata help dependency calls = packageRoot:%d environment:%d, want zero", packageRootCalls, environmentCalls)
	}

	dependencies.packageRoot = func() (string, error) {
		packageRootCalls++
		return "/package", nil
	}
	status, err = checkPackSetCommandWithDependencies([]string{"--help"}, dependencies)
	if err != nil || status != 0 {
		t.Fatalf("check-pack-set --help = (%d, %v), want (0, nil)", status, err)
	}
	if packageRootCalls != 0 {
		t.Fatalf("check-pack-set --help packageRoot calls = %d, want zero", packageRootCalls)
	}
}

func TestCheckPackSetCommandPreservesExitThreeAndEnvironmentDefaults(t *testing.T) {
	var output bytes.Buffer
	dependencies := metadataCommandTestDependencies(&output)
	dependencies.environment = func(name string) string {
		return map[string]string{
			"INFRAWRIGHT_PACKS":        "/environment/packs",
			"INFRAWRIGHT_PACK_PROFILE": "/environment/profile.json",
		}[name]
	}
	var got metadata.ValidateActivePackSetOptions
	dependencies.validateActivePackSet = func(options metadata.ValidateActivePackSetOptions) (metadata.ActivePackSetResult, error) {
		got = options
		return metadata.ActivePackSetResult{Active: metadata.PackSelection{Packs: []string{"sample"}}}, nil
	}
	status, err := checkPackSetCommandWithDependencies([]string{"--catalog", "/catalog.json"}, dependencies)
	if err != nil || status != 0 {
		t.Fatalf("checkPackSetCommandWithDependencies = (%d, %v)", status, err)
	}
	if got.Root != "/environment/packs" || got.ProfilePath != "/environment/profile.json" || got.CatalogPath == nil || *got.CatalogPath != "/catalog.json" {
		t.Fatalf("options = %#v", got)
	}
	if output.String() != "validated pack set: packs=[sample] shared=[]\n" {
		t.Fatalf("validated stdout = %q", output.String())
	}

	output.Reset()
	dependencies.environment = func(string) string { return "" }
	dependencies.validateActivePackSet = func(options metadata.ValidateActivePackSetOptions) (metadata.ActivePackSetResult, error) {
		if options.Root != filepath.Join("/package", "packs") || options.ProfilePath != filepath.Join("/package", "packsets", "full.json") ||
			options.CatalogPath == nil || *options.CatalogPath != filepath.Join("/package", "packsets", "full.json") {
			t.Fatalf("falsey fallback options = %#v", options)
		}
		return metadata.ActivePackSetResult{}, nil
	}
	status, err = checkPackSetCommandWithDependencies(nil, dependencies)
	if err != nil || status != 0 {
		t.Fatalf("falsey fallback check-pack-set = (%d, %v), want (0, nil)", status, err)
	}

	output.Reset()
	dependencies.checkPackRequirements = func(metadata.CheckPackRequirementsOptions) (metadata.RequirementsResult, error) {
		return metadata.RequirementsResult{
			Missing: metadata.PackSelection{Packs: []string{"missing"}, Shared: []string{"shared"}},
		}, nil
	}
	status, err = checkPackSetCommandWithDependencies([]string{"--requirements", "/requirements.json"}, dependencies)
	if err != nil || status != 3 {
		t.Fatalf("unavailable check-pack-set = (%d, %v), want (3, nil)", status, err)
	}
	if output.String() != "requirements unavailable: packs=missing shared=shared\n" {
		t.Fatalf("unavailable stdout = %q", output.String())
	}
}

func TestDeploymentCommandRejectsExtraPositionalsBeforeLoading(t *testing.T) {
	var output bytes.Buffer
	dependencies := metadataCommandTestDependencies(&output)
	loaded := false
	dependencies.environment = func(string) string {
		t.Fatal("explicit deployment path read INFRAWRIGHT_DEPLOYMENT")
		return ""
	}
	dependencies.deploymentPath = func(options deployment.DeploymentPathOptions) (string, error) {
		if options.Explicit == nil || *options.Explicit != "/deployment.json" {
			t.Fatalf("deployment path options = %#v", options)
		}
		if options.Environment != nil {
			t.Fatalf("explicit deployment environment = %#v, want nil", options.Environment)
		}
		return *options.Explicit, nil
	}
	dependencies.loadDeployment = func(source string) (deployment.Deployment, error) {
		loaded = true
		return deployment.Deployment{}, nil
	}
	dependencies.deploymentConfigDir = func(_ deployment.Deployment, tenant string) (string, error) {
		return "config/" + tenant, nil
	}
	status, err := deploymentCommandWithDependencies(
		[]string{"--deployment", "/deployment.json", "config-dir", "tenant-a", "ignored"},
		dependencies,
	)
	if err == nil || status != 0 || output.String() != "" || loaded {
		t.Fatalf("deployment extra positional = (%d, %v, %q, loaded=%t), want usage rejection before load", status, err, output.String(), loaded)
	}

	status, err = deploymentCommandWithDependencies(
		[]string{"--deployment", "/deployment.json", "config-dir", "tenant-a"},
		dependencies,
	)
	if err != nil || status != 0 || output.String() != "config/tenant-a\n" {
		t.Fatalf("deployment config-dir = (%d, %v, %q), want success", status, err, output.String())
	}

	output.Reset()
	loaded = false
	_, err = deploymentCommandWithDependencies(
		[]string{"--deployment", "/deployment.json", "unknown"},
		dependencies,
	)
	if !loaded {
		t.Fatal("deployment did not load before rejecting unknown verb")
	}
	if err == nil || err.Error() != `unknown deployment verb "unknown"` {
		t.Fatalf("unknown verb error = %v", err)
	}

	dependencies.deploymentPath = func(deployment.DeploymentPathOptions) (string, error) {
		t.Fatal("missing verb resolved deployment path")
		return "", nil
	}
	_, err = deploymentCommandWithDependencies(nil, dependencies)
	if err == nil || err.Error() != "deployment requires a verb" {
		t.Fatalf("missing verb error = %v", err)
	}
}
