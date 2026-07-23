package roots

// planroots_test.go exercises artifact-state classification, discovery,
// selector handling, and tenant validation against temporary env trees.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// writePlanRootsFixture builds a representative envs tree:
//
//   - envs/acme/zpa_alpha_one:     tfplan + tfplan.sources -> "complete"
//   - envs/acme/zpa_derived_reorder: tfplan only            -> "incomplete"
//   - envs/other/zpa_alpha_one:    neither artifact         -> "absent"
//   - envs/acme/unknown_root:      tfplan, but "unknown_root" names no
//     topology root label at all -- must never appear in any result.
func writePlanRootsFixture(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	mustMkdirAll(t, filepath.Join(workspace, "envs/acme/zpa_alpha_one"))
	mustWriteFile(t, filepath.Join(workspace, "envs/acme/zpa_alpha_one/tfplan"), "plan")
	mustWriteFile(t, filepath.Join(workspace, "envs/acme/zpa_alpha_one/tfplan.sources"), "sources")

	mustMkdirAll(t, filepath.Join(workspace, "envs/acme/zpa_derived_reorder"))
	mustWriteFile(t, filepath.Join(workspace, "envs/acme/zpa_derived_reorder/tfplan"), "plan-only")

	mustMkdirAll(t, filepath.Join(workspace, "envs/other/zpa_alpha_one"))

	mustMkdirAll(t, filepath.Join(workspace, "envs/acme/unknown_root"))
	mustWriteFile(t, filepath.Join(workspace, "envs/acme/unknown_root/tfplan"), "plan")
	return workspace
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func rootByLabel(t *testing.T, roots []MaterializedPlanRoot, tenant, label string) MaterializedPlanRoot {
	t.Helper()
	for _, root := range roots {
		if root.Tenant == tenant && root.Label == label {
			return root
		}
	}
	t.Fatalf("no materialized root for tenant=%s label=%s in %+v", tenant, label, roots)
	return MaterializedPlanRoot{}
}

// TestPlanRootsClassifiesArtifactStateAndSkipsUnknownRootDirectories ports
// the "complete-incomplete-absent-and-unknown-root-across-tenants" oracle
// scenario: three materialized roots (complete/incomplete/absent) across
// two tenants, and confirms envs/acme/unknown_root -- a directory naming
// no topology root label -- never surfaces anywhere in the result.
func TestPlanRootsClassifiesArtifactStateAndSkipsUnknownRootDirectories(t *testing.T) {
	workspace := writePlanRootsFixture(t)
	result, err := PlanRootsFromResourceSet(PlanRootsOptions{
		Workspace:   workspace,
		Deployment:  singletonDeployment(),
		ResourceSet: scopePlanFixtureResourceSet(),
		Tenant:      nil,
		Selectors:   []string{},
	})
	if err != nil {
		t.Fatalf("PlanRootsFromResourceSet: %v", err)
	}
	if len(result.Result.Roots) != 3 {
		t.Fatalf("Roots length = %d, want 3 (oracle: complete zpa_alpha/acme, incomplete zpa_derived_reorder/acme, absent zpa_alpha/other)", len(result.Result.Roots))
	}

	complete := rootByLabel(t, result.Result.Roots, "acme", "zpa_alpha_one")
	if complete.ArtifactState != ArtifactStateComplete {
		t.Errorf("acme/zpa_alpha ArtifactState = %v, want complete", complete.ArtifactState)
	}
	if !complete.Artifacts.Tfplan.Exists || !complete.Artifacts.TfplanSources.Exists {
		t.Errorf("acme/zpa_alpha Artifacts = %+v, want both existing", complete.Artifacts)
	}
	if !reflect.DeepEqual(complete.Members, []string{"zpa_alpha_one"}) {
		t.Errorf("acme/zpa_alpha_one Members = %v, want singleton member", complete.Members)
	}
	if complete.EnvDir != "envs/acme/zpa_alpha_one" {
		t.Errorf("acme/zpa_alpha_one EnvDir = %q, want envs/acme/zpa_alpha_one", complete.EnvDir)
	}

	incomplete := rootByLabel(t, result.Result.Roots, "acme", "zpa_derived_reorder")
	if incomplete.ArtifactState != ArtifactStateIncomplete {
		t.Errorf("acme/zpa_derived_reorder ArtifactState = %v, want incomplete", incomplete.ArtifactState)
	}
	if !incomplete.Artifacts.Tfplan.Exists || incomplete.Artifacts.TfplanSources.Exists {
		t.Errorf("acme/zpa_derived_reorder Artifacts = %+v, want tfplan present / sources absent", incomplete.Artifacts)
	}

	absent := rootByLabel(t, result.Result.Roots, "other", "zpa_alpha_one")
	if absent.ArtifactState != ArtifactStateAbsent {
		t.Errorf("other/zpa_alpha ArtifactState = %v, want absent", absent.ArtifactState)
	}
	if absent.Artifacts.Tfplan.Exists || absent.Artifacts.TfplanSources.Exists {
		t.Errorf("other/zpa_alpha Artifacts = %+v, want neither existing", absent.Artifacts)
	}

	for _, root := range result.Result.Roots {
		if root.Label == "unknown_root" {
			t.Fatalf("envs/acme/unknown_root leaked into the result: %+v", root)
		}
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %+v, want none (no selectors -> no partial-group selection)", result.Diagnostics)
	}
}

// TestPlanRootsTenantScopingExcludesOtherTenants ports the
// "tenant-scoped-excludes-other-tenants" oracle scenario: requesting
// tenant "acme" returns only its two roots, never the "other" tenant's.
func TestPlanRootsTenantScopingExcludesOtherTenants(t *testing.T) {
	workspace := writePlanRootsFixture(t)
	tenant := "acme"
	result, err := PlanRootsFromResourceSet(PlanRootsOptions{
		Workspace:   workspace,
		Deployment:  singletonDeployment(),
		ResourceSet: scopePlanFixtureResourceSet(),
		Tenant:      &tenant,
		Selectors:   []string{},
	})
	if err != nil {
		t.Fatalf("PlanRootsFromResourceSet: %v", err)
	}
	if len(result.Result.Roots) != 2 {
		t.Fatalf("Roots length = %d, want 2", len(result.Result.Roots))
	}
	for _, root := range result.Result.Roots {
		if root.Tenant != "acme" {
			t.Errorf("root %+v has tenant != acme", root)
		}
	}
	if result.Result.Request.Tenant == nil || *result.Result.Request.Tenant != "acme" {
		t.Errorf("Request.Tenant = %v, want acme", result.Result.Request.Tenant)
	}
}

// TestPlanRootsPartialSelectorMaterializesSingletonRoot verifies that an
// unscoped selector discovers the matching singleton root for every tenant.
func TestPlanRootsPartialSelectorMaterializesSingletonRoot(t *testing.T) {
	workspace := writePlanRootsFixture(t)
	result, err := PlanRootsFromResourceSet(PlanRootsOptions{
		Workspace:   workspace,
		Deployment:  singletonDeployment(),
		ResourceSet: scopePlanFixtureResourceSet(),
		Tenant:      nil,
		Selectors:   []string{"zpa_alpha_one"},
	})
	if err != nil {
		t.Fatalf("PlanRootsFromResourceSet: %v", err)
	}
	if len(result.Result.Roots) != 2 {
		t.Fatalf("Roots length = %d, want 2 (zpa_alpha_one under both acme and other)", len(result.Result.Roots))
	}
	for _, root := range result.Result.Roots {
		if root.Label != "zpa_alpha_one" || !reflect.DeepEqual(root.Members, []string{"zpa_alpha_one"}) {
			t.Errorf("root %+v: want singleton zpa_alpha_one root", root)
		}
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %+v, want none for singleton selection", result.Diagnostics)
	}
}

// TestPlanRootsUnknownSelectorFailsClosed ports the
// "unknown-selector-fails-closed" oracle scenario, including exact
// code/category/message text.
func TestPlanRootsUnknownSelectorFailsClosed(t *testing.T) {
	workspace := writePlanRootsFixture(t)
	_, err := PlanRootsFromResourceSet(PlanRootsOptions{
		Workspace:   workspace,
		Deployment:  singletonDeployment(),
		ResourceSet: scopePlanFixtureResourceSet(),
		Tenant:      nil,
		Selectors:   []string{"not_a_real_resource"},
	})
	pf, ok := asProcessFailure(err)
	if !ok {
		t.Fatalf("err = %v, want a *procerr.ProcessFailure", err)
	}
	if pf.Code != "UNKNOWN_RESOURCE_SELECTOR" || pf.Message != "unknown or non-generated resource selector(s): not_a_real_resource" {
		t.Errorf("failure = %+v, want UNKNOWN_RESOURCE_SELECTOR with the oracle's exact message", pf)
	}
}

// TestPlanRootsInvalidRequestedTenantFailsClosed ports the
// "invalid-tenant-fails-closed" oracle scenario, including exact
// code/category/message text.
func TestPlanRootsInvalidRequestedTenantFailsClosed(t *testing.T) {
	workspace := writePlanRootsFixture(t)
	tenant := "../escape"
	_, err := PlanRootsFromResourceSet(PlanRootsOptions{
		Workspace:   workspace,
		Deployment:  singletonDeployment(),
		ResourceSet: scopePlanFixtureResourceSet(),
		Tenant:      &tenant,
		Selectors:   []string{},
	})
	pf, ok := asProcessFailure(err)
	if !ok {
		t.Fatalf("err = %v, want a *procerr.ProcessFailure", err)
	}
	wantMessage := "TENANT must match [A-Za-z0-9_.-]+ and not be . or .. (got '../escape')"
	if pf.Code != "INVALID_TENANT" || pf.Message != wantMessage {
		t.Errorf("failure = %+v, want INVALID_TENANT %q", pf, wantMessage)
	}
}

// TestPlanRootsNonexistentEnvsDirectoryYieldsNoRootsNotAnError ports the
// "nonexistent-envs-directory" oracle scenario: envBase's own directory
// missing entirely is not an error, just an empty result (planRootIsDirectory
// guards the readdir that would otherwise fail).
func TestPlanRootsNonexistentEnvsDirectoryYieldsNoRootsNotAnError(t *testing.T) {
	workspace := t.TempDir() // deliberately empty: no envs/ at all
	result, err := PlanRootsFromResourceSet(PlanRootsOptions{
		Workspace:   workspace,
		Deployment:  singletonDeployment(),
		ResourceSet: scopePlanFixtureResourceSet(),
		Tenant:      nil,
		Selectors:   []string{},
	})
	if err != nil {
		t.Fatalf("PlanRootsFromResourceSet: %v", err)
	}
	if len(result.Result.Roots) != 0 {
		t.Errorf("Roots = %+v, want empty", result.Result.Roots)
	}
}

// TestPlanRootsInvalidTenantDirectoryNameIsToleratedUnlessSelected is not
// a ported oracle scenario (the probe never exercised it) -- it is this
// Go port's own regression pin for the subtlest behavior
// planRootsFromTopologies's doc comment calls out: an invalid on-disk
// tenant directory name is validated LAZILY. discover() does not filter
// tenant directory names at all (only the <label> subdirectory names
// underneath them, against known root labels); validateTenant(entry.tenant)
// runs only for entries whose root label survived the `selectedLabels`
// filter. So a "bad tenant" directory containing an env root that is
// NEVER selected must not fail the call at all, while selecting that same
// root DOES fail it. This mirrors
// the original test corpus's own "plan-root discovery validates
// only selected recognized tenant roots" test (mined during this port's
// vector search, see planroots.go's package doc comment), which exercises
// the identical shape against the retired Python oracle.
func TestPlanRootsInvalidTenantDirectoryNameIsToleratedUnlessSelected(t *testing.T) {
	workspace := t.TempDir()
	mustMkdirAll(t, filepath.Join(workspace, "envs/bad tenant/zpa_alpha_one"))
	mustWriteFile(t, filepath.Join(workspace, "envs/bad tenant/zpa_alpha_one/tfplan"), "plan")
	mustWriteFile(t, filepath.Join(workspace, "envs/bad tenant/zpa_alpha_one/tfplan.sources"), "sources")

	// A selector that does not touch zpa_alpha at all: the "bad tenant"
	// directory's zpa_alpha subdirectory is discovered (its label is a
	// real topology root) but its label is not in selectedLabels, so
	// validateTenant never runs for it, and the call succeeds with no
	// materialized roots.
	ignored, err := PlanRootsFromResourceSet(PlanRootsOptions{
		Workspace:   workspace,
		Deployment:  singletonDeployment(),
		ResourceSet: scopePlanFixtureResourceSet(),
		Tenant:      nil,
		Selectors:   []string{"zpa_derived_reorder"},
	})
	if err != nil {
		t.Fatalf("PlanRootsFromResourceSet (unselected label): %v, want success (the invalid tenant directory's root was never selected)", err)
	}
	if len(ignored.Result.Roots) != 0 {
		t.Errorf("Roots = %+v, want empty", ignored.Result.Roots)
	}

	// Selecting zpa_alpha_one DOES reach the "bad tenant" directory's
	// zpa_alpha root, so validateTenant("bad tenant") now runs and fails
	// the whole call.
	_, err = PlanRootsFromResourceSet(PlanRootsOptions{
		Workspace:   workspace,
		Deployment:  singletonDeployment(),
		ResourceSet: scopePlanFixtureResourceSet(),
		Tenant:      nil,
		Selectors:   []string{"zpa_alpha_one"},
	})
	pf, ok := asProcessFailure(err)
	if !ok || pf.Code != "INVALID_TENANT" || !strings.Contains(pf.Message, "bad tenant") {
		t.Fatalf("PlanRootsFromResourceSet (selected label): err = %v, want INVALID_TENANT mentioning 'bad tenant'", err)
	}
}

// TestLoadedPlanRootsHasNoUpfrontSelectorPrecheck is not a ported oracle
// scenario -- it pins the asymmetry PlanRootsFromResourceSet's own doc
// comment documents: unlike PlanRootsFromResourceSet (which calls
// expandResources on the raw catalog index before ever building a
// topology, "historical explicit validation" ported verbatim from
// plan-roots.ts's own comment), LoadedPlanRoots has no such precheck --
// the original implementation's loadedPlanRoots never calls
// expandLoadedResources. Both still fail on an unknown selector (via the
// `selected` rootTopologyFromIndex call's own expandResources), so this
// only proves the CODE PATH differs, not that the outward behavior does;
// it exists so a future edit that "cleans up" the asymmetry away (by
// adding or removing the precheck from just one of the two) gets caught.
func TestLoadedPlanRootsHasNoUpfrontSelectorPrecheck(t *testing.T) {
	root, cleanup := loadPlanRootsFixturePackRoot(t)
	defer cleanup()
	workspace := t.TempDir()
	// No roots.zpa entry here (the synthetic pack root declares a
	// "sample" provider, not "zpa"): this test only needs a loaded pack
	// root distinct from scopePlanFixtureResourceSet's ResourceSet, so an
	// empty deployment is enough to reach the selector-expansion failure.
	_, err := LoadedPlanRoots(LoadedPlanRootsOptions{
		Workspace:  workspace,
		Deployment: deployment.Deployment{Overlay: ".", Roots: map[string]deployment.RootProviderConfig{}},
		Root:       root,
		Tenant:     nil,
		Selectors:  []string{"not_a_real_resource"},
	})
	pf, ok := asProcessFailure(err)
	if !ok || pf.Code != "UNKNOWN_RESOURCE_SELECTOR" {
		t.Fatalf("err = %v, want UNKNOWN_RESOURCE_SELECTOR (raised by rootTopologyFromIndex's own expandResources, not an upfront precheck)", err)
	}
}

// loadPlanRootsFixturePackRoot builds a minimal on-disk pack root loadable
// via metadata.LoadPackRoot, purely so LoadedPlanRoots (which takes a
// metadata.LoadedPackRoot, not a metadata.ResourceSet) has something to
// exercise; its actual resource shape does not matter for
// TestLoadedPlanRootsHasNoUpfrontSelectorPrecheck; only that
// "not_a_real_resource" is not among its generated resources.
func loadPlanRootsFixturePackRoot(t *testing.T) (metadata.LoadedPackRoot, func()) {
	t.Helper()
	directory := t.TempDir()
	pack := filepath.Join(directory, "sample")
	mustMkdirAll(t, pack)
	mustWriteFile(t, filepath.Join(pack, "pack.json"), `{"provider_prefixes":{"sample_":"sample"}}`)
	mustWriteFile(t, filepath.Join(pack, "registry.json"), `{"sample_resource":{"generate":true,"product":"sample"}}`)
	loaded, err := metadata.LoadPackRoot(metadata.LoadPackRootOptions{PacksRoot: directory})
	if err != nil {
		t.Fatalf("LoadPackRoot: %v", err)
	}
	return loaded, func() {}
}
