package assessment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/artifacts"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/plan"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

func assessmentString(value string) *string {
	return &value
}

func assessmentResourceSet(resources ...metadata.ResourceDescriptor) metadata.ResourceSet {
	return metadata.ResourceSet{
		DeclaredProviders: []string{"zpa"},
		Resources:         resources,
	}
}

func singletonAssessmentResourceSet() metadata.ResourceSet {
	return assessmentResourceSet(metadata.ResourceDescriptor{
		Type:      "zpa_sample",
		Product:   "zpa",
		Provider:  "zpa",
		BareName:  "sample",
		Generated: true,
	})
}

func writeAssessmentPlan(t *testing.T, workspace, tenant, label string) string {
	t.Helper()
	envDir := filepath.Join(workspace, "envs", tenant, label)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error: %v", envDir, err)
	}
	planPath := filepath.Join(envDir, "tfplan")
	if err := os.WriteFile(planPath, []byte("plan\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", planPath, err)
	}
	return envDir
}

func requireAssessmentInputFailure(
	t *testing.T,
	err error,
	code string,
	message string,
) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T %v, want *procerr.ProcessFailure with code %q", err, err, code)
	}
	if failure.Code != code {
		t.Errorf("ProcessFailure.Code = %q, want %q", failure.Code, code)
	}
	if failure.Category != procerr.CategoryDomain {
		t.Errorf("ProcessFailure.Category = %q, want %q", failure.Category, procerr.CategoryDomain)
	}
	if failure.Message != message {
		t.Errorf("ProcessFailure.Message = %q, want %q", failure.Message, message)
	}
	return failure
}

func assessmentRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	directory := filepath.Dir(thisFile)
	for {
		if _, packsErr := os.Stat(filepath.Join(directory, "packs", "full.packset.json")); packsErr == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatalf("assessmentRepoRoot() walked to filesystem root from %q", thisFile)
		}
		directory = parent
	}
}

func sortedRegistryTypes(registry metadata.JsonObject) []string {
	types := make([]string, 0, len(registry))
	for resourceType := range registry {
		types = append(types, resourceType)
	}
	sort.Strings(types)
	return types
}

func loadedAssessmentPack(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	directory := t.TempDir()
	pack := filepath.Join(directory, "sample")
	write := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal(%q) error: %v", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error: %v", path, err)
		}
	}
	write(filepath.Join(pack, "pack.json"), metadata.JsonObject{
		"provider_prefixes": metadata.JsonObject{"zia_": "zia", "zpa_": "zpa"},
		"provider_sources":  metadata.JsonObject{"zia": "example/zia", "zpa": "example/zpa"},
		"references": metadata.JsonObject{
			"zpa_application_segment": metadata.JsonObject{
				"segment_group_id": metadata.JsonObject{"name_field": "name", "referent": "zpa_segment_group"},
			},
		},
	})
	registry := metadata.JsonObject{}
	for _, resourceType := range []string{
		"zia_admin_users", "zia_url_categories", "zia_workload_groups",
		"zpa_application_segment", "zpa_sample", "zpa_segment_group",
	} {
		product, _, _ := strings.Cut(resourceType, "_")
		registry[resourceType] = metadata.JsonObject{"generate": true, "product": product}
	}
	write(filepath.Join(pack, "registry.json"), registry)
	// Every registered resource type gets a provider schema, because in a
	// materialised pack it has one. A registry entry without a schema is not a
	// thinner version of production, it is a shape production cannot take, and
	// a fixture built that way hides every consumer that reads attribute types
	// -- the assessment classifier among them.
	//
	// zia_url_categories carries the pair the set-typed walk turns on:
	// db_categorized_urls is a set and urls is an ordered list, on one
	// resource. Anything that decides set-ness from the values rather than the
	// schema gets one of the two wrong.
	stringAttribute := metadata.JsonObject{"type": "string", "optional": true}
	collection := func(kind string) metadata.JsonObject {
		return metadata.JsonObject{"type": []any{kind, "string"}, "optional": true}
	}
	resourceSchemas := map[string]metadata.JsonObject{
		"zia_url_categories": {"block": metadata.JsonObject{"attributes": metadata.JsonObject{
			"configured_name":     stringAttribute,
			"db_categorized_urls": collection("set"),
			"keywords":            collection("set"),
			"urls":                collection("list"),
		}}},
	}
	providerSchemas := map[string]metadata.JsonObject{}
	for _, resourceType := range sortedRegistryTypes(registry) {
		provider, _, _ := strings.Cut(resourceType, "_")
		schema, ok := resourceSchemas[resourceType]
		if !ok {
			schema = metadata.JsonObject{"block": metadata.JsonObject{"attributes": metadata.JsonObject{
				"name": stringAttribute,
			}}}
		}
		if _, seen := providerSchemas[provider]; !seen {
			providerSchemas[provider] = metadata.JsonObject{}
		}
		providerSchemas[provider][resourceType] = schema
	}
	for provider, schemas := range providerSchemas {
		write(filepath.Join(pack, "schemas", "provider", provider+".json"), metadata.JsonObject{
			"resource_schemas": schemas,
		})
	}
	profile := filepath.Join(directory, "profile.json")
	write(profile, metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1, "packs": []string{"sample"}, "shared": []string{},
	})
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{
		PacksRoot:   directory,
		ProfilePath: &profile,
	})
	if err != nil {
		t.Fatalf("metadata.LoadPackRoot(synthetic assessment pack) error: %v", err)
	}
	return root
}

func installedAssessmentPack(t *testing.T) metadata.LoadedPackRoot {
	t.Helper()
	packsRoot := filepath.Join(assessmentRepoRoot(t), "packs")
	entries, err := os.ReadDir(packsRoot)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error: %v", packsRoot, err)
	}
	packs := make([]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "_shared" {
			packs = append(packs, entry.Name())
		}
	}
	shared := []any{}
	sharedRoot := filepath.Join(packsRoot, "_shared")
	sharedEntries, err := os.ReadDir(sharedRoot)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("os.ReadDir(%q) error: %v", sharedRoot, err)
	}
	for _, entry := range sharedEntries {
		if entry.IsDir() {
			shared = append(shared, entry.Name())
		}
	}
	profile := filepath.Join(t.TempDir(), "installed.packset.json")
	data, err := json.Marshal(metadata.JsonObject{
		"kind": metadata.PackSetKind, "version": 1, "packs": packs, "shared": shared,
	})
	if err != nil {
		t.Fatalf("json.Marshal(installed pack set) error: %v", err)
	}
	if err := os.WriteFile(profile, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error: %v", profile, err)
	}
	root, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: packsRoot, ProfilePath: &profile})
	if err != nil {
		t.Fatalf("metadata.LoadPackRoot(installed packs) error: %v", err)
	}
	return root
}

func TestResolveSavedPlanAssessmentInputsMaterializesJSONAndHCL(t *testing.T) {
	for _, format := range []string{"json", "hcl"} {
		t.Run(format, func(t *testing.T) {
			workspace := t.TempDir()
			envDir := writeAssessmentPlan(t, workspace, "tenant", "zpa_sample")
			backend := "backend/../backend.hcl"
			policy := "/absolute/../policy.json"
			resolved, err := ResolveSavedPlanAssessmentInputs(ResolveSavedPlanAssessmentOptions{
				Workspace: workspace,
				Deployment: deployment.Deployment{
					Overlay:         ".",
					HasTfvarsFormat: true,
					TfvarsFormat:    format,
					Roots:           map[string]deployment.RootProviderConfig{},
				},
				ResourceSet:         singletonAssessmentResourceSet(),
				Tenant:              assessmentString("tenant"),
				Selectors:           []string{},
				TerraformExecutable: "/opt/terraform",
				BackendConfig:       &backend,
				PolicyPath:          &policy,
			})
			if err != nil {
				t.Fatalf("ResolveSavedPlanAssessmentInputs(format=%q) error: %v", format, err)
			}
			extension := ".auto.tfvars"
			if format == "json" {
				extension += ".json"
			}
			wantRoots := []SavedPlanAssessmentRootInput{{
				Tenant:          "tenant",
				Label:           "zpa_sample",
				Members:         []string{"zpa_sample"},
				EnvDir:          envDir,
				SavedPlanPath:   filepath.Join(envDir, "tfplan"),
				FingerprintPath: filepath.Join(envDir, "tfplan.sources"),
				VarFiles: []string{filepath.Join(
					workspace,
					"config",
					"tenant",
					"zpa_sample"+extension,
				)},
			}}
			if !reflect.DeepEqual(resolved.Roots, wantRoots) {
				t.Errorf("ResolveSavedPlanAssessmentInputs(format=%q).Roots = %#v, want %#v", format, resolved.Roots, wantRoots)
			}
			if got, want := *resolved.BackendConfig, filepath.Join(workspace, "backend.hcl"); got != want {
				t.Errorf("ResolveSavedPlanAssessmentInputs(format=%q).BackendConfig = %q, want %q", format, got, want)
			}
			if got, want := *resolved.PolicyPath, policy; got != want {
				t.Errorf("ResolveSavedPlanAssessmentInputs(format=%q).PolicyPath = %q, want unchanged absolute path %q", format, got, want)
			}
			if got, want := resolved.TerraformExecutable, "/opt/terraform"; got != want {
				t.Errorf("ResolveSavedPlanAssessmentInputs(format=%q).TerraformExecutable = %q, want %q", format, got, want)
			}
		})
	}
}

func TestResolveSavedPlanAssessmentInputsUsesSingletonRootPathsAndOrdering(t *testing.T) {
	workspace := t.TempDir()
	for _, resourceType := range []string{
		"zpa_alpha_one",
		"zpa_alpha_two",
		"zpa_beta_one",
		"zpa_beta_two",
	} {
		writeAssessmentPlan(t, workspace, "tenant", resourceType)
	}
	catalog := assessmentResourceSet(
		metadata.ResourceDescriptor{Type: "zpa_alpha_two", Product: "zpa", Provider: "zpa", BareName: "alpha_two", Generated: true},
		metadata.ResourceDescriptor{Type: "zpa_beta_one", Product: "zpa", Provider: "zpa", BareName: "beta_one", Generated: true},
		metadata.ResourceDescriptor{Type: "zpa_alpha_one", Product: "zpa", Provider: "zpa", BareName: "alpha_one", Generated: true},
		metadata.ResourceDescriptor{Type: "zpa_beta_two", Product: "zpa", Provider: "zpa", BareName: "beta_two", Generated: true},
	)
	resolved, err := ResolveSavedPlanAssessmentInputs(ResolveSavedPlanAssessmentOptions{
		Workspace:           workspace,
		Deployment:          deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
		ResourceSet:         catalog,
		Tenant:              assessmentString("tenant"),
		Selectors:           []string{},
		TerraformExecutable: "/opt/terraform",
	})
	if err != nil {
		t.Fatalf("ResolveSavedPlanAssessmentInputs(ordering) error: %v", err)
	}
	want := make([]SavedPlanAssessmentRootInput, 0, 4)
	for _, resourceType := range []string{
		"zpa_alpha_one",
		"zpa_alpha_two",
		"zpa_beta_one",
		"zpa_beta_two",
	} {
		envDir := filepath.Join(workspace, "envs", "tenant", resourceType)
		want = append(want, SavedPlanAssessmentRootInput{
			Tenant: "tenant", Label: resourceType,
			Members:         []string{resourceType},
			EnvDir:          envDir,
			SavedPlanPath:   filepath.Join(envDir, "tfplan"),
			FingerprintPath: filepath.Join(envDir, "tfplan.sources"),
			VarFiles:        []string{filepath.Join(workspace, "config", "tenant", resourceType+".auto.tfvars.json")},
		})
	}
	if !reflect.DeepEqual(resolved.Roots, want) {
		t.Errorf("ResolveSavedPlanAssessmentInputs(ordering).Roots = %#v, want %#v", resolved.Roots, want)
	}
}

func TestResolveSavedPlanAssessmentInputsDefersTfvarsValidationUntilPlanSelected(t *testing.T) {
	workspace := t.TempDir()
	invalidDeployment := deployment.Deployment{
		Overlay:         ".",
		HasTfvarsFormat: true,
		TfvarsFormat:    "unsupported",
		Roots:           map[string]deployment.RootProviderConfig{},
	}
	options := ResolveSavedPlanAssessmentOptions{
		Workspace:           workspace,
		Deployment:          invalidDeployment,
		ResourceSet:         singletonAssessmentResourceSet(),
		Tenant:              assessmentString("tenant"),
		Selectors:           []string{},
		TerraformExecutable: "/opt/terraform",
	}
	resolved, err := ResolveSavedPlanAssessmentInputs(options)
	if err != nil {
		t.Fatalf("ResolveSavedPlanAssessmentInputs(no plans, invalid tfvars format) error: %v", err)
	}
	if len(resolved.Roots) != 0 {
		t.Errorf("ResolveSavedPlanAssessmentInputs(no plans, invalid tfvars format).Roots = %#v, want empty", resolved.Roots)
	}

	writeAssessmentPlan(t, workspace, "tenant", "zpa_sample")
	_, err = ResolveSavedPlanAssessmentInputs(options)
	requireAssessmentInputFailure(
		t,
		err,
		"INVALID_DEPLOYMENT",
		"deployment tfvars_format must be 'json' or 'hcl' for assessment",
	)

	options.Deployment.TfvarsFormat = nil
	_, err = ResolveSavedPlanAssessmentInputs(options)
	requireAssessmentInputFailure(
		t,
		err,
		"INVALID_DEPLOYMENT",
		"deployment tfvars_format must be 'json' or 'hcl' for assessment",
	)
}

func TestGenericResolverSnapshotsBeforeDiscoveryAndCopiesControlEvidence(t *testing.T) {
	workspace := t.TempDir()
	envDir := writeAssessmentPlan(t, workspace, "tenant", "zpa_sample")
	backend := "backend.hcl"
	policy := "policy.json"
	digest := artifacts.StableFileDigest{SHA256: strings.Repeat("a", 64), Size: 17}
	identity := artifacts.StableFileIdentity{Dev: 4, Ino: 8}
	followSymlinks := false
	limits := terraformcmd.TerraformShowLimits{TimeoutMs: 100, MaxStdoutBytes: 200, MaxStderrBytes: 300}
	options := ResolveSavedPlanAssessmentOptions{
		Workspace: workspace,
		Deployment: deployment.Deployment{
			Overlay:         ".",
			HasTfvarsFormat: true,
			TfvarsFormat:    "json",
			Roots:           map[string]deployment.RootProviderConfig{},
		},
		ResourceSet:         singletonAssessmentResourceSet(),
		Tenant:              assessmentString("tenant"),
		Selectors:           []string{"zpa_sample"},
		TerraformExecutable: "/original/terraform",
		BackendConfig:       &backend,
		PolicyPath:          &policy,
		ControlFiles: []controlevidence.BoundAssessmentControlFile{{
			Path: "controls.json", Digest: &digest, Identity: &identity, FollowSymlinks: &followSymlinks,
		}},
		TerraformShowLimits: &limits,
	}
	resolved, err := resolveSavedPlanAssessment(options, resolveSavedPlanAssessmentHooks{
		afterMaterialize: func(current *ResolveSavedPlanAssessmentOptions) {
			current.Workspace = "/mutated-workspace"
			current.Deployment.Overlay = "mutated"
			current.Deployment.TfvarsFormat = "hcl"
			current.ResourceSet.Resources[0].Type = "zpa_mutated"
			current.Selectors[0] = "zpa_mutated"
			current.TerraformExecutable = "/mutated/terraform"
			current.BackendConfig = nil
			current.PolicyPath = nil
			current.ControlFiles[0].Path = "mutated-control.json"
			current.ControlFiles[0].Digest.SHA256 = strings.Repeat("b", 64)
			current.TerraformShowLimits.TimeoutMs = 999
		},
	})
	if err != nil {
		t.Fatalf("resolveSavedPlanAssessment(snapshot timing) error: %v", err)
	}
	assessment := resolved.Assessment
	if got, want := assessment.TerraformExecutable, "/original/terraform"; got != want {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).TerraformExecutable = %q, want %q", got, want)
	}
	if got, want := assessment.Roots[0].EnvDir, envDir; got != want {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).Roots[0].EnvDir = %q, want %q", got, want)
	}
	if got, want := *assessment.BackendConfig, filepath.Join(workspace, "backend.hcl"); got != want {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).BackendConfig = %q, want %q", got, want)
	}
	if got, want := *assessment.PolicyPath, filepath.Join(workspace, "policy.json"); got != want {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).PolicyPath = %q, want %q", got, want)
	}
	if assessment.Context == nil || assessment.LoadedContext != nil {
		t.Fatalf("resolveSavedPlanAssessment(snapshot timing) contexts = generic:%v loaded:%v, want generic only", assessment.Context != nil, assessment.LoadedContext != nil)
	}
	if got, want := assessment.Context.Workspace, workspace; got != want {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).Context.Workspace = %q, want %q", got, want)
	}
	if got, want := assessment.Context.ResourceSet.Resources[0].Type, "zpa_sample"; got != want {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).Context.ResourceSet.Resources[0].Type = %q, want %q", got, want)
	}
	if got, want := assessment.Context.Selectors, []string{"zpa_sample"}; !reflect.DeepEqual(got, want) {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).Context.Selectors = %#v, want %#v", got, want)
	}
	if got, want := len(assessment.ControlFiles), 1; got != want {
		t.Fatalf("resolveSavedPlanAssessment(snapshot timing).ControlFiles length = %d, want %d", got, want)
	}
	control := assessment.ControlFiles[0]
	if got, want := control.Path, "controls.json"; got != want {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).ControlFiles[0].Path = %q, want %q", got, want)
	}
	if got, want := control.Digest.SHA256, strings.Repeat("a", 64); got != want {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).ControlFiles[0].Digest.SHA256 = %q, want %q", got, want)
	}
	if control.Identity != nil || control.FollowSymlinks != nil {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).ControlFiles[0] loaded-only fields = identity:%v follow:%v, want both omitted", control.Identity, control.FollowSymlinks)
	}
	if got, want := assessment.TerraformShowLimits.TimeoutMs, int64(100); got != want {
		t.Errorf("resolveSavedPlanAssessment(snapshot timing).TerraformShowLimits.TimeoutMs = %d, want %d", got, want)
	}
}

func TestLoadedResolverPreservesPostDiscoveryCaptureAsymmetry(t *testing.T) {
	workspace := t.TempDir()
	root := loadedAssessmentPack(t)
	backend := "before-backend.hcl"
	policy := "before-policy.json"
	digest := artifacts.StableFileDigest{SHA256: strings.Repeat("a", 64), Size: 17}
	identity := artifacts.StableFileIdentity{Dev: 7, Ino: 11}
	followSymlinks := false
	limits := terraformcmd.TerraformShowLimits{TimeoutMs: 10, MaxStdoutBytes: 20, MaxStderrBytes: 30}
	changedBackend := "after-backend.hcl"
	changedPolicy := "after-policy.json"
	changedDigest := artifacts.StableFileDigest{SHA256: strings.Repeat("b", 64), Size: 19}
	changedIdentity := artifacts.StableFileIdentity{Dev: 13, Ino: 17}
	changedFollowSymlinks := true
	changedLimits := terraformcmd.TerraformShowLimits{TimeoutMs: 40, MaxStdoutBytes: 50, MaxStderrBytes: 60}
	resolved, err := resolveLoadedSavedPlanAssessment(ResolveLoadedSavedPlanAssessmentOptions{
		Workspace: workspace,
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots:   map[string]deployment.RootProviderConfig{},
		},
		Root:                root,
		Tenant:              assessmentString("tenant"),
		Selectors:           []string{},
		TerraformExecutable: "/before/terraform",
		BackendConfig:       &backend,
		PolicyPath:          &policy,
		ControlFiles: []controlevidence.BoundAssessmentControlFile{{
			Path: "before-control.json", Digest: &digest, Identity: &identity, FollowSymlinks: &followSymlinks,
		}},
		TerraformShowLimits: &limits,
	}, resolveLoadedSavedPlanAssessmentHooks{
		afterMaterialize: func(current *ResolveLoadedSavedPlanAssessmentOptions) {
			current.Workspace = "/mutated-workspace"
			current.Deployment = deployment.Deployment{Overlay: "mutated", Roots: map[string]deployment.RootProviderConfig{}}
			current.Selectors = []string{"zpa_segment_group"}
			current.TerraformExecutable = "/after/terraform"
			current.BackendConfig = &changedBackend
			current.PolicyPath = &changedPolicy
			current.ControlFiles = []controlevidence.BoundAssessmentControlFile{{
				Path: "after-control.json", Digest: &changedDigest, Identity: &changedIdentity, FollowSymlinks: &changedFollowSymlinks,
			}}
			current.TerraformShowLimits = &changedLimits
		},
	})
	if err != nil {
		t.Fatalf("resolveLoadedSavedPlanAssessment(snapshot timing) error: %v", err)
	}
	assessment := resolved.Assessment
	if got, want := assessment.TerraformExecutable, "/after/terraform"; got != want {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).TerraformExecutable = %q, want post-discovery %q", got, want)
	}
	if got, want := *assessment.BackendConfig, filepath.Join(workspace, changedBackend); got != want {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).BackendConfig = %q, want post-discovery %q", got, want)
	}
	if got, want := *assessment.PolicyPath, filepath.Join(workspace, changedPolicy); got != want {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).PolicyPath = %q, want post-discovery %q", got, want)
	}
	if assessment.LoadedContext == nil || assessment.Context != nil {
		t.Fatalf("resolveLoadedSavedPlanAssessment(snapshot timing) contexts = generic:%v loaded:%v, want loaded only", assessment.Context != nil, assessment.LoadedContext != nil)
	}
	if got, want := assessment.LoadedContext.Workspace, workspace; got != want {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).LoadedContext.Workspace = %q, want pre-discovery %q", got, want)
	}
	if got, want := assessment.LoadedContext.Deployment.Overlay, any("."); got != want {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).LoadedContext.Deployment.Overlay = %#v, want pre-discovery %#v", got, want)
	}
	if got := assessment.LoadedContext.Selectors; len(got) != 0 {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).LoadedContext.Selectors = %#v, want pre-discovery empty selectors", got)
	}
	if got, want := len(assessment.ControlFiles), 1; got != want {
		t.Fatalf("resolveLoadedSavedPlanAssessment(snapshot timing).ControlFiles length = %d, want %d", got, want)
	}
	control := assessment.ControlFiles[0]
	if got, want := control.Path, "after-control.json"; got != want {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).ControlFiles[0].Path = %q, want %q", got, want)
	}
	if got, want := *control.Digest, changedDigest; got != want {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).ControlFiles[0].Digest = %#v, want %#v", got, want)
	}
	if got, want := *control.Identity, changedIdentity; got != want {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).ControlFiles[0].Identity = %#v, want %#v", got, want)
	}
	if got, want := *control.FollowSymlinks, changedFollowSymlinks; got != want {
		t.Errorf("resolveLoadedSavedPlanAssessment(snapshot timing).ControlFiles[0].FollowSymlinks = %t, want %t", got, want)
	}
	changedDigest.SHA256 = strings.Repeat("c", 64)
	changedIdentity.Ino = 23
	changedFollowSymlinks = false
	changedLimits.TimeoutMs = 70
	if got, want := control.Digest.SHA256, strings.Repeat("b", 64); got != want {
		t.Errorf("resolved loaded control digest after caller mutation = %q, want detached %q", got, want)
	}
	if got, want := control.Identity.Ino, uint64(17); got != want {
		t.Errorf("resolved loaded control identity after caller mutation = %d, want detached %d", got, want)
	}
	if got, want := *control.FollowSymlinks, true; got != want {
		t.Errorf("resolved loaded followSymlinks after caller mutation = %t, want detached %t", got, want)
	}
	if got, want := assessment.TerraformShowLimits.TimeoutMs, int64(40); got != want {
		t.Errorf("resolved loaded TerraformShowLimits after caller mutation = %d, want detached %d", got, want)
	}
}

func TestLoadedAssessmentDefaultsToSortedCrossStateReferenceOutputs(t *testing.T) {
	workspace := t.TempDir()
	envDir := writeAssessmentPlan(t, workspace, "tenant", "zpa_segment_group")
	root := loadedAssessmentPack(t)
	resolved, err := ResolveLoadedSavedPlanAssessment(ResolveLoadedSavedPlanAssessmentOptions{
		Workspace: workspace,
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots:   map[string]deployment.RootProviderConfig{},
		},
		Root:                root,
		Tenant:              assessmentString("tenant"),
		Selectors:           []string{"zpa_segment_group"},
		TerraformExecutable: "/opt/terraform",
	})
	if err != nil {
		t.Fatalf("ResolveLoadedSavedPlanAssessment(default cross-state outputs) error: %v", err)
	}
	wantRoots := []SavedPlanAssessmentRootInput{{
		Tenant:          "tenant",
		Label:           "zpa_segment_group",
		Members:         []string{"zpa_segment_group"},
		EnvDir:          envDir,
		SavedPlanPath:   filepath.Join(envDir, "tfplan"),
		FingerprintPath: filepath.Join(envDir, "tfplan.sources"),
		VarFiles: []string{
			filepath.Join(workspace, "config", "tenant", "zpa_segment_group.auto.tfvars.json"),
		},
		ReferenceOutputTypes: []plan.ReferenceOutputType{{
			Type: "zpa_segment_group", Kind: plan.ReferenceOutputKindManaged,
		}},
	}}
	if !reflect.DeepEqual(resolved.Assessment.Roots, wantRoots) {
		t.Errorf("ResolveLoadedSavedPlanAssessment(default cross-state outputs).Roots = %#v, want %#v", resolved.Assessment.Roots, wantRoots)
	}
	if len(resolved.Diagnostics) != 0 {
		t.Errorf("ResolveLoadedSavedPlanAssessment(default cross-state outputs).Diagnostics = %#v, want no whole-root diagnostic for a singleton selection", resolved.Diagnostics)
	}
	if err := os.Remove(filepath.Join(envDir, "tfplan")); err != nil {
		t.Fatalf("os.Remove(default cross-state tfplan) error: %v", err)
	}
	err = RecheckLoadedSavedPlanAssessmentContext(*resolved.Assessment.LoadedContext, resolved.Assessment.Roots)
	requireAssessmentInputFailure(
		t,
		err,
		"ASSESSMENT_CONTEXT_CHANGED",
		"saved-plan assessment context changed during assessment",
	)
}

func TestLoadedAssessmentProjectsDataReferenceOutputKindFromMetadata(t *testing.T) {
	workspace := t.TempDir()
	writeAssessmentPlan(t, workspace, "tenant", "zpa_segment_group")
	root := loadedAssessmentPack(t)
	root.Resources["zpa_segment_group"].Registry["data_referent"] = true
	resolved, err := ResolveLoadedSavedPlanAssessment(ResolveLoadedSavedPlanAssessmentOptions{
		Workspace: workspace,
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots:   map[string]deployment.RootProviderConfig{},
		},
		Root:                root,
		Tenant:              assessmentString("tenant"),
		Selectors:           []string{"zpa_segment_group"},
		TerraformExecutable: "/opt/terraform",
	})
	if err != nil {
		t.Fatalf("ResolveLoadedSavedPlanAssessment(data referent metadata) error = %v, want nil", err)
	}
	want := []plan.ReferenceOutputType{{
		Type: "zpa_segment_group", Kind: plan.ReferenceOutputKindData,
	}}
	if got := resolved.Assessment.Roots[0].ReferenceOutputTypes; !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveLoadedSavedPlanAssessment(data referent metadata).ReferenceOutputTypes = %#v, want %#v", got, want)
	}
}

func TestAssessmentContextRechecksAreExactAndRedacted(t *testing.T) {
	workspace := t.TempDir()
	envDir := writeAssessmentPlan(t, workspace, "tenant", "zpa_sample")
	resolved, err := ResolveSavedPlanAssessmentInputs(ResolveSavedPlanAssessmentOptions{
		Workspace: workspace,
		Deployment: deployment.Deployment{
			Overlay: ".",
			Roots:   map[string]deployment.RootProviderConfig{},
		},
		ResourceSet:         singletonAssessmentResourceSet(),
		Tenant:              assessmentString("tenant"),
		Selectors:           []string{},
		TerraformExecutable: "/opt/terraform",
	})
	if err != nil {
		t.Fatalf("ResolveSavedPlanAssessmentInputs(recheck setup) error: %v", err)
	}
	if err := RecheckSavedPlanAssessmentContext(*resolved.Context, resolved.Roots); err != nil {
		t.Errorf("RecheckSavedPlanAssessmentContext(unchanged) error: %v", err)
	}
	if err := os.Remove(filepath.Join(envDir, "tfplan")); err != nil {
		t.Fatalf("os.Remove(generic tfplan) error: %v", err)
	}
	err = RecheckSavedPlanAssessmentContext(*resolved.Context, resolved.Roots)
	failure := requireAssessmentInputFailure(
		t,
		err,
		"ASSESSMENT_CONTEXT_CHANGED",
		"saved-plan assessment context changed during assessment",
	)
	if strings.Contains(failure.Error(), workspace) || strings.Contains(failure.Error(), "tfplan") {
		t.Errorf("RecheckSavedPlanAssessmentContext(changed) error = %q, want no path disclosure", failure.Error())
	}

	secretContext := *resolved.Context
	secretContext.Workspace = "relative/secret-workspace"
	err = RecheckSavedPlanAssessmentContext(secretContext, resolved.Roots)
	failure = requireAssessmentInputFailure(
		t,
		err,
		"ASSESSMENT_CONTEXT_CHANGED",
		"saved-plan assessment context changed during assessment",
	)
	if strings.Contains(failure.Error(), "secret-workspace") {
		t.Errorf("RecheckSavedPlanAssessmentContext(invalid workspace) error = %q, want redacted", failure.Error())
	}
}

func TestAssessmentRootEqualityTreatsMissingReferenceOutputsAsEmptyAndChecksOrder(t *testing.T) {
	base := []SavedPlanAssessmentRootInput{{
		Tenant: "tenant", Label: "root", Members: []string{"a", "b"},
		EnvDir: "/env", SavedPlanPath: "/env/tfplan", FingerprintPath: "/env/tfplan.sources",
		VarFiles: []string{"/a", "/b"},
	}}
	explicitEmpty := []SavedPlanAssessmentRootInput{{
		Tenant: "tenant", Label: "root", Members: []string{"a", "b"},
		EnvDir: "/env", SavedPlanPath: "/env/tfplan", FingerprintPath: "/env/tfplan.sources",
		VarFiles: []string{"/a", "/b"}, ReferenceOutputTypes: []plan.ReferenceOutputType{},
	}}
	if !sameAssessmentRoots(base, explicitEmpty) {
		t.Error("sameAssessmentRoots(nil reference outputs, empty reference outputs) = false, want true")
	}
	cloneRoots := func(values []SavedPlanAssessmentRootInput) []SavedPlanAssessmentRootInput {
		cloned := make([]SavedPlanAssessmentRootInput, len(values))
		for index, value := range values {
			cloned[index] = value
			cloned[index].Members = cloneStrings(value.Members)
			cloned[index].VarFiles = cloneStrings(value.VarFiles)
			cloned[index].ReferenceOutputTypes = cloneReferenceOutputTypes(value.ReferenceOutputTypes)
		}
		return cloned
	}
	reordered := cloneRoots(explicitEmpty)
	reordered[0].Members = []string{"b", "a"}
	if sameAssessmentRoots(base, reordered) {
		t.Error("sameAssessmentRoots(original members, reordered members) = true, want false")
	}
	withOutputs := cloneRoots(explicitEmpty)
	withOutputs[0].ReferenceOutputTypes = []plan.ReferenceOutputType{
		{Type: "zpa_server_group", Kind: plan.ReferenceOutputKindManaged},
		{Type: "zpa_segment_group", Kind: plan.ReferenceOutputKindManaged},
	}
	reversedOutputs := cloneRoots(explicitEmpty)
	reversedOutputs[0].ReferenceOutputTypes = []plan.ReferenceOutputType{
		{Type: "zpa_segment_group", Kind: plan.ReferenceOutputKindManaged},
		{Type: "zpa_server_group", Kind: plan.ReferenceOutputKindManaged},
	}
	if sameAssessmentRoots(withOutputs, reversedOutputs) {
		t.Error("sameAssessmentRoots(reference output order mismatch) = true, want false")
	}
}

func TestAssessmentResolversRejectRelativeWorkspace(t *testing.T) {
	_, err := ResolveSavedPlanAssessmentInputs(ResolveSavedPlanAssessmentOptions{
		Workspace:           "relative/workspace",
		Deployment:          deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
		ResourceSet:         singletonAssessmentResourceSet(),
		TerraformExecutable: "/opt/terraform",
	})
	requireAssessmentInputFailure(t, err, "INVALID_WORKSPACE", "assessment workspace must be absolute")

	_, _, err = MaterializeLoadedSavedPlanAssessmentRoots(LoadedSavedPlanAssessmentContext{
		Workspace:  "relative/workspace",
		Deployment: deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
		Root:       metadata.LoadedPackRoot{},
		Selectors:  []string{},
	})
	requireAssessmentInputFailure(t, err, "INVALID_WORKSPACE", "assessment workspace must be absolute")
}
