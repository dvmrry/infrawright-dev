package deployment

// deployment_test.go ports node-tests/deployment.test.ts's library-level
// vectors: missing/empty/omitted-key defaulting, tfvars_format raw-field
// preservation, fail-closed malformed-root-configuration vectors,
// cross-state reference mode defaults, the "__proto__" non-special-case
// check, and the path-helper contract (overlay/module_dir/tfvars_format/
// tenant-root/config-dir/imports-dir/envs-dir/pulls-dir).

import (
	"os"
	"path/filepath"
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
		`{"roots": {"zpa": {"strategy": "surprise"}}}`,
		`{"roots": {"zpa": {"groups": {"empty": []}}}}`,
		`{"roots": {"zpa": {"cross_state_references": "yes"}}}`,
		`{"roots": {"zpa": {"bind_references": true, "cross_state_references": true}}}`,
		`{"roots": {"zpa": {"unknown": true}}}`,
	}
	for _, c := range cases {
		writeDeployment(t, dir, c)
		if _, err := LoadDeployment(deploymentPath); err == nil {
			t.Errorf("LoadDeployment(%s) = nil error, want failure", c)
		}
	}
}

func TestCrossStateReferenceModeIsExplicitAndPreservesLegacyDefaults(t *testing.T) {
	dir := t.TempDir()
	deploymentPath := writeDeployment(t, dir, `{
		"roots": {
			"zia": {"cross_state_references": true},
			"zpa": {"bind_references": true}
		}
	}`)
	loaded, err := LoadDeployment(deploymentPath)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}
	if got := DeploymentReferenceBindingMode(loaded, "zia"); got != ReferenceBindingCrossState {
		t.Errorf("DeploymentReferenceBindingMode(zia) = %v, want cross_state", got)
	}
	if got := DeploymentReferenceBindingMode(loaded, "zpa"); got != ReferenceBindingSameRoot {
		t.Errorf("DeploymentReferenceBindingMode(zpa) = %v, want same_root", got)
	}
	if got := DeploymentReferenceBindingMode(loaded, "zcc"); got != ReferenceBindingDisabled {
		t.Errorf("DeploymentReferenceBindingMode(zcc) = %v, want disabled", got)
	}
}

func TestDeploymentDictionariesDoNotTreatPrototypeNamesSpecially(t *testing.T) {
	dir := t.TempDir()
	deploymentPath := writeDeployment(t, dir,
		`{"roots":{"zpa":{"groups":{"__proto__":["zpa_alpha_one"]}}}}`)
	loaded, err := LoadDeployment(deploymentPath)
	if err != nil {
		t.Fatalf("LoadDeployment: %v", err)
	}
	if _, ok := loaded.Roots["zpa"]; !ok {
		t.Fatalf("Roots = %v, want a \"zpa\" entry", loaded.Roots)
	}
	members, ok := loaded.Roots["zpa"].Groups["__proto__"]
	if !ok {
		t.Fatalf("Roots[zpa].Groups = %v, want a \"__proto__\" entry", loaded.Roots["zpa"].Groups)
	}
	if len(members) != 1 || members[0] != "zpa_alpha_one" {
		t.Errorf("Roots[zpa].Groups[__proto__] = %v, want [zpa_alpha_one]", members)
	}

	writeDeployment(t, dir,
		`{"roots":{"zpa":{"groups":{"__proto__":["one"],"__proto__":["two"]}}}}`)
	if _, err := LoadDeployment(deploymentPath); err == nil {
		t.Errorf("LoadDeployment(duplicate __proto__ key) = nil error, want failure (duplicate JSON key)")
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
// Cwd) asymmetry documented on DeploymentPath: no equivalent node-tests
// vector exists (deploymentPath itself is untested at the library level
// in node-tests/deployment.test.ts, which only exercises loadDeployment
// and the overlay/dir accessors), so this test was written directly
// against node-src/domain/deployment.ts's source to pin the exact
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
