package deployment

// deployment_test.go ports the original test corpus's library-level
// vectors: missing/empty/omitted-key defaulting, tfvars_format raw-field
// preservation, fail-closed malformed-root-configuration vectors,
// cross-state reference mode defaults, the "__proto__" non-special-case
// check, and the path-helper contract (overlay/module_dir/tfvars_format/
// tenant-root/config-dir/imports-dir/envs-dir/pulls-dir).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeDeployment(t *testing.T, dir, content string) string {
	t.Helper()
	deploymentPath := filepath.Join(dir, "deployment.json")
	if err := os.WriteFile(deploymentPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return deploymentPath
}

func TestLoadDeploymentPreservesMissingAndEmptyDefaults(t *testing.T) {
	dir := t.TempDir()
	deploymentPath := filepath.Join(dir, "deployment.json")

	loaded, err := LoadDeployment(deploymentPath)
	if err != nil {
		t.Fatalf("LoadDeployment (missing file): %v", err)
	}
	if loaded.Overlay != "." {
		t.Errorf("Overlay = %v, want \".\"", loaded.Overlay)
	}
	if loaded.HasModuleDir {
		t.Errorf("HasModuleDir = true, want false")
	}
	if len(loaded.Roots) != 0 {
		t.Errorf("Roots = %v, want empty", loaded.Roots)
	}

	writeDeployment(t, dir, " \n\t")
	loaded, err = LoadDeployment(deploymentPath)
	if err != nil {
		t.Fatalf("LoadDeployment (whitespace-only file): %v", err)
	}
	if loaded.Overlay != "." {
		t.Errorf("Overlay = %v, want \".\"", loaded.Overlay)
	}
	if loaded.HasModuleDir {
		t.Errorf("HasModuleDir = true, want false")
	}
	if len(loaded.Roots) != 0 {
		t.Errorf("Roots = %v, want empty", loaded.Roots)
	}
}

func TestLoadDeploymentDefaultsOmittedOverlayAndModuleDir(t *testing.T) {
	dir := t.TempDir()
	deploymentPath := writeDeployment(t, dir, `{"roots": {}}`)

	loaded, err := LoadDeployment(deploymentPath)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}
	if loaded.Overlay != "." {
		t.Errorf("Overlay = %v, want \".\"", loaded.Overlay)
	}
	if loaded.HasModuleDir {
		t.Errorf("HasModuleDir = true, want false")
	}
	if len(loaded.Roots) != 0 {
		t.Errorf("Roots = %v, want empty", loaded.Roots)
	}
}

func TestLoadDeploymentPreservesTfvarsFormatRawField(t *testing.T) {
	dir := t.TempDir()
	deploymentPath := writeDeployment(t, dir, `{"tfvars_format": "hcl"}`)
	loaded, err := LoadDeployment(deploymentPath)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}
	if loaded.TfvarsFormat != "hcl" {
		t.Errorf("TfvarsFormat = %v, want \"hcl\"", loaded.TfvarsFormat)
	}

	writeDeployment(t, dir, `{"tfvars_format": "future"}`)
	loaded, err = LoadDeployment(deploymentPath)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}
	if loaded.TfvarsFormat != "future" {
		t.Errorf("TfvarsFormat = %v, want \"future\" (raw field preserves any value)", loaded.TfvarsFormat)
	}
	// The raw field preserves "future", but the validating accessor
	// rejects it (only "json"/"hcl" are valid tfvars_format values).
	if _, err := DeploymentTfvarsFormat(loaded); err == nil {
		t.Errorf("DeploymentTfvarsFormat(tfvars_format=\"future\") = nil error, want failure")
	}
}

func TestLoadDeploymentFailsClosedOnMalformedRootConfiguration(t *testing.T) {
	dir := t.TempDir()
	deploymentPath := filepath.Join(dir, "deployment.json")
	cases := []string{
		`[]`,
		`{"roots": []}`,
		`{"roots": {"zpa": []}}`,
		`{"roots": {"zpa": {"cross_state_references": "yes"}}}`,
		`{"roots": {"zpa": {"unknown": true}}}`,
	}
	for _, c := range cases {
		writeDeployment(t, dir, c)
		if _, err := LoadDeployment(deploymentPath); err == nil {
			t.Errorf("LoadDeployment(%s) = nil error, want failure", c)
		}
	}
}

func TestCrossStateReferenceModeDefaultsToCrossState(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name           string
		content        string
		provider       string
		wantMode       ReferenceBindingMode
		wantHasConfig  bool
		wantHasSetting bool
	}{
		{"absent roots", `{}`, "zpa", ReferenceBindingCrossState, false, false},
		{"absent provider", `{"roots":{}}`, "zpa", ReferenceBindingCrossState, false, false},
		{"absent setting", `{"roots":{"zpa":{}}}`, "zpa", ReferenceBindingCrossState, true, false},
		{"explicit true", `{"roots":{"zpa":{"cross_state_references":true}}}`, "zpa", ReferenceBindingCrossState, true, true},
		{"explicit false", `{"roots":{"zpa":{"cross_state_references":false}}}`, "zpa", ReferenceBindingDisabled, true, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			deploymentPath := writeDeployment(t, dir, test.content)
			loaded, err := LoadDeployment(deploymentPath)
			if err != nil {
				t.Fatalf("LoadDeployment(%s): %v", test.content, err)
			}
			if got := DeploymentReferenceBindingMode(loaded, test.provider); got != test.wantMode {
				t.Errorf("DeploymentReferenceBindingMode(%q) = %v, want %v", test.provider, got, test.wantMode)
			}
			config, hasConfig := loaded.Roots[test.provider]
			if hasConfig != test.wantHasConfig {
				t.Fatalf("Roots[%q] present = %t, want %t", test.provider, hasConfig, test.wantHasConfig)
			}
			if hasConfig && config.HasCrossStateReferences != test.wantHasSetting {
				t.Errorf("Roots[%q].HasCrossStateReferences = %t, want %t", test.provider, config.HasCrossStateReferences, test.wantHasSetting)
			}
		})
	}
}

func TestHandBuiltDeploymentReferenceModeDefaultsToCrossState(t *testing.T) {
	cases := []struct {
		name       string
		deployment Deployment
		want       ReferenceBindingMode
	}{
		{"nil roots", Deployment{}, ReferenceBindingCrossState},
		{"empty roots", Deployment{Roots: map[string]RootProviderConfig{}}, ReferenceBindingCrossState},
		{"provider without setting", Deployment{Roots: map[string]RootProviderConfig{"zpa": {}}}, ReferenceBindingCrossState},
		{"explicit true", Deployment{Roots: map[string]RootProviderConfig{"zpa": {HasCrossStateReferences: true, CrossStateReferences: true}}}, ReferenceBindingCrossState},
		{"explicit false", Deployment{Roots: map[string]RootProviderConfig{"zpa": {HasCrossStateReferences: true, CrossStateReferences: false}}}, ReferenceBindingDisabled},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := DeploymentReferenceBindingMode(test.deployment, "zpa"); got != test.want {
				t.Errorf("DeploymentReferenceBindingMode(%#v, %q) = %v, want %v", test.deployment, "zpa", got, test.want)
			}
		})
	}
}

func TestRetiredRootFieldsFailWithFieldSpecificRoadmapPointer(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct {
		name, input, field string
	}{
		{"strategy", `{"roots":{"zpa":{"strategy":"slug"}}}`, "strategy"},
		{"groups", `{"roots":{"zpa":{"groups":{}}}}`, "groups"},
		{"bind", `{"roots":{"zpa":{"bind_references":false}}}`, "bind_references"},
		{"sorted first", `{"roots":{"zpa":{"strategy":"slug","groups":{}}}}`, "groups"},
	} {
		t.Run(test.name, func(t *testing.T) {
			deploymentPath := writeDeployment(t, dir, test.input)
			if _, err := LoadDeployment(deploymentPath); err == nil ||
				!strings.Contains(err.Error(), "roots.zpa."+test.field+" has been removed; see docs/state-topology.md") {
				t.Fatalf("LoadDeployment = %v, want field-specific retirement error for %s", err, test.field)
			}
		})
	}
}

func TestStateTopologyRetiredFieldDocumentationMatchesValidation(t *testing.T) {
	documentPath := filepath.Join("..", "..", "..", "docs", "state-topology.md")
	documentBytes, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", documentPath, err)
	}
	document := string(documentBytes)
	retiredSentence := regexp.MustCompile(`Deployment root\nentries reject these retired fields: ([^.]+)\.`).FindStringSubmatch(document)
	if len(retiredSentence) != 2 {
		t.Fatalf("%s has no parseable retired deployment-root field list", documentPath)
	}
	matches := regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(retiredSentence[1], -1)
	gotRetired := make([]string, 0, len(matches))
	for _, match := range matches {
		gotRetired = append(gotRetired, match[1])
	}
	wantRetired := []string{"strategy", "groups", "bind_references"}
	if strings.Join(gotRetired, ",") != strings.Join(wantRetired, ",") {
		t.Fatalf("documented retired deployment-root fields = %v, want %v", gotRetired, wantRetired)
	}
	dir := t.TempDir()
	for _, field := range gotRetired {
		content := `{"roots":{"zpa":{"` + field + `":false}}}`
		deploymentPath := writeDeployment(t, dir, content)
		if _, err := LoadDeployment(deploymentPath); err == nil || !strings.Contains(err.Error(), "roots.zpa."+field+" has been removed") {
			t.Errorf("LoadDeployment(%s) error = %v, want documented retirement error for %q", content, err, field)
		}
	}
	if !strings.Contains(document, "`cross_state_references`, a boolean that defaults to enabled") {
		t.Errorf("%s does not document the live cross_state_references default", documentPath)
	}
	deploymentPath := writeDeployment(t, dir, `{"roots":{"zpa":{"cross_state_references":true}}}`)
	if _, err := LoadDeployment(deploymentPath); err != nil {
		t.Errorf("LoadDeployment(cross_state_references=true) error = %v, want documented live field accepted", err)
	}
}

func TestActiveTopologyRunbooksDoNotAdvertiseRetiredConfiguration(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..", "..")
	documents := []string{
		filepath.Join(repositoryRoot, "docs", "adoption-command-surface.md"),
		filepath.Join(repositoryRoot, "docs", "integration-validation.md"),
		filepath.Join(repositoryRoot, "docs", "provider-labs", "cross-state-reference-qualification.md"),
	}
	forbidden := []string{
		`"strategy"`, `"groups"`, `"bind_references"`,
		"## Grouped Env Roots", "opt-in singleton-state reference mode",
		"Cross-state references remain opt-in", "cross_state_references` | Optional boolean, default `false`",
		"selects whole root",
	}
	for _, documentPath := range documents {
		documentBytes, err := os.ReadFile(documentPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error: %v", documentPath, err)
		}
		document := string(documentBytes)
		for _, stale := range forbidden {
			if strings.Contains(document, stale) {
				t.Errorf("%s still advertises retired topology text %q", documentPath, stale)
			}
		}
	}
}

func TestDeploymentPathHelpersPreserveTheOperationalOverlayContract(t *testing.T) {
	dir := t.TempDir()
	deploymentPath := writeDeployment(t, dir, `{
		"overlay": "estate/prod",
		"module_dir": "estate/modules/pinned",
		"tfvars_format": "hcl"
	}`)
	loaded, err := LoadDeployment(deploymentPath)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}

	if overlay, err := DeploymentOverlay(loaded); err != nil || overlay != "estate/prod" {
		t.Errorf("DeploymentOverlay = (%v, %v), want (estate/prod, nil)", overlay, err)
	}
	if moduleDir, err := DeploymentModuleDir(loaded); err != nil || moduleDir != "estate/modules/pinned" {
		t.Errorf("DeploymentModuleDir = (%v, %v), want (estate/modules/pinned, nil)", moduleDir, err)
	}
	if format, err := DeploymentTfvarsFormat(loaded); err != nil || format != "hcl" {
		t.Errorf("DeploymentTfvarsFormat = (%v, %v), want (hcl, nil)", format, err)
	}
	if root, err := DeploymentTenantRoot(loaded, "tenant-a"); err != nil || root != "estate/prod" {
		t.Errorf("DeploymentTenantRoot = (%v, %v), want (estate/prod, nil)", root, err)
	}
	if got, err := DeploymentConfigDir(loaded, "tenant-a"); err != nil || got != filepath.Join("estate", "prod", "config", "tenant-a") {
		t.Errorf("DeploymentConfigDir = (%v, %v)", got, err)
	}
	if got, err := DeploymentImportsDir(loaded, "tenant-a"); err != nil || got != filepath.Join("estate", "prod", "imports", "tenant-a") {
		t.Errorf("DeploymentImportsDir = (%v, %v)", got, err)
	}
	if got, err := DeploymentEnvsDir(loaded, "tenant-a"); err != nil || got != filepath.Join("estate", "prod", "envs", "tenant-a") {
		t.Errorf("DeploymentEnvsDir = (%v, %v)", got, err)
	}
	if got := DeploymentPullsDir("tenant-a"); got != filepath.Join("pulls", "tenant-a") {
		t.Errorf("DeploymentPullsDir = %v", got)
	}
}

// TestDeploymentPathEnvSemantics ports the `||` (falsy-fallback, for
// Explicit and the environment variable) vs `??` (nullish-fallback, for
// Cwd) asymmetry documented on DeploymentPath: no equivalent compatibility tests
// vector exists (deploymentPath itself is untested at the library level
// in the original test corpus, which only exercises loadDeployment
// and the overlay/dir accessors), so this test was written directly
// against the original implementation's source to pin the exact
// semantics down for the Go port.
func TestDeploymentPathEnvSemantics(t *testing.T) {
	empty := ""
	cwd := "/somewhere"

	// An explicit-but-empty Explicit is skipped just like an omitted one,
	// falling through to the environment variable (`||`, not `??`).
	got, err := DeploymentPath(DeploymentPathOptions{
		Explicit:    &empty,
		Environment: map[string]string{"INFRAWRIGHT_DEPLOYMENT": "/from-env/deployment.json"},
	})
	if err != nil || got != "/from-env/deployment.json" {
		t.Errorf("DeploymentPath(Explicit=\"\") = (%v, %v), want (/from-env/deployment.json, nil)", got, err)
	}

	// An empty-string environment variable is likewise skipped, falling
	// through to the cwd-based default.
	got, err = DeploymentPath(DeploymentPathOptions{
		Environment: map[string]string{"INFRAWRIGHT_DEPLOYMENT": ""},
		Cwd:         &cwd,
	})
	if err != nil || got != "/somewhere/deployment.json" {
		t.Errorf("DeploymentPath(env=\"\") = (%v, %v), want (/somewhere/deployment.json, nil)", got, err)
	}

	// An explicit-but-empty Cwd is used as-is (`??`, not `||`): it is NOT
	// replaced by the working directory.
	got, err = DeploymentPath(DeploymentPathOptions{
		Environment: map[string]string{},
		Cwd:         &empty,
	})
	if err != nil || got != "deployment.json" {
		t.Errorf("DeploymentPath(Cwd=\"\") = (%v, %v), want (deployment.json, nil)", got, err)
	}

	// A non-empty Explicit wins outright, regardless of the environment.
	explicit := "/explicit/deployment.json"
	got, err = DeploymentPath(DeploymentPathOptions{
		Explicit:    &explicit,
		Environment: map[string]string{"INFRAWRIGHT_DEPLOYMENT": "/from-env/deployment.json"},
	})
	if err != nil || got != "/explicit/deployment.json" {
		t.Errorf("DeploymentPath(Explicit set) = (%v, %v), want (/explicit/deployment.json, nil)", got, err)
	}
}
