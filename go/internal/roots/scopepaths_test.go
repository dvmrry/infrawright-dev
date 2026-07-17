package roots

// scopepaths_test.go ports scope-paths behavior pinned by probing the
// compiled TypeScript directly (there is no node-tests/scope-paths.test.ts
// to port from -- see scopepaths.go's package doc comment). The oracle
// this file's test cases cite by scenario name is committed at
// go/internal/roots/testdata/scope_plan_probe.oracle.json, produced by
// go/internal/roots/testdata/probe/scope_plan_probe.ts against
// node-src/domain/scope-paths.ts -- see that probe script's own header
// comment for the exact esbuild/node regeneration command. Each test below
// reproduces its scenario's inputs (same catalog, same deployment shape,
// same paths) against ChangedPathScopeFromCatalog and asserts the same
// shape the oracle recorded, except for the oracle's own randomly-named
// temp-directory deployment path, which this file replaces with a fixed
// "/workspace" tree (harmless: scope-paths never touches the filesystem,
// only path algebra -- see pypath.PythonPosixRealpath's non-strict
// tolerance of nonexistent paths).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// scopePlanFixtureCatalog mirrors the CATALOG constant in
// scope_plan_probe.ts exactly (four resources: two slug-grouped, one
// derived, one non-generated) -- deliberately a separate literal from
// roots_test.go's own fixtureCatalog (which has a fifth,
// slug_group:false resource this probe's scenarios never needed), so this
// file stays independently traceable against the committed oracle without
// depending on roots_test.go's fixture shape happening to stay in sync.
func scopePlanFixtureCatalog() metadata.RootCatalog {
	return metadata.RootCatalog{
		Kind:              "infrawright.root_catalog",
		SchemaVersion:     1,
		DeclaredProviders: []string{"zpa"},
		Resources: []metadata.RootCatalogResource{
			{
				Type: "zpa_alpha_one", Product: "zpa", Provider: "zpa",
				BareName: "alpha_one", SlugLabel: strPtr("zpa_alpha"),
				Generated: true, Derived: false,
			},
			{
				Type: "zpa_alpha_two", Product: "zpa", Provider: "zpa",
				BareName: "alpha_two", SlugLabel: strPtr("zpa_alpha"),
				Generated: true, Derived: false,
			},
			{
				Type: "zpa_derived_reorder", Product: "zpa", Provider: "zpa",
				BareName: "derived_reorder", SlugLabel: strPtr("zpa_derived"),
				Generated: true, Derived: true,
			},
			{
				Type: "zpa_known_only", Product: "zpa", Provider: "zpa",
				BareName: "known_only", SlugLabel: strPtr("zpa_known"),
				Generated: false, Derived: false,
			},
		},
		SourceFiles:   []string{"zpa/pack.json", "zpa/registry.json"},
		SourcesSHA256: strings.Repeat("0", 64),
	}
}

// slugDeployment ports scope_plan_probe.ts's dotDeployment: overlay ".",
// roots.zpa.strategy "slug" (merging zpa_alpha_one/zpa_alpha_two under
// the shared "zpa_alpha" label; zpa_derived_reorder stays individual
// regardless, since derived types always keep per-resource roots).
func slugDeployment() deployment.Deployment {
	return deployment.Deployment{
		Overlay: ".",
		Roots:   map[string]deployment.RootProviderConfig{"zpa": {HasStrategy: true, Strategy: "slug"}},
	}
}

const scopeWorkspace = "/workspace"

var scopeDeploymentPath = scopeWorkspace + "/deployment.json"

// TestChangedPathScopeDotOverlayMatchesEveryKind ports the
// "dot-overlay-all-kinds" oracle scenario: one path per ChangedPathKind
// (deployment, config x2, imports, env_root, module) plus one unmatched
// path, under overlay "." and slug-grouped roots.
func TestChangedPathScopeDotOverlayMatchesEveryKind(t *testing.T) {
	scope, err := ChangedPathScopeFromCatalog(ChangedPathScopeOptions{
		Paths:          []string{scopeDeploymentPath, "config/acme/zpa_alpha_one.generated.expressions.json", "config/acme/zpa_alpha_one.expressions.json", "imports/acme/zpa_alpha_two_imports.tf", "envs/acme/zpa_alpha", "modules/zpa_alpha_two/main.tf"},
		Workspace:      scopeWorkspace,
		DeploymentPath: scopeDeploymentPath,
		Deployment:     slugDeployment(),
		Catalog:        scopePlanFixtureCatalog(),
	})
	if err != nil {
		t.Fatalf("ChangedPathScopeFromCatalog: %v", err)
	}
	if len(scope.UnmatchedPaths) != 0 {
		t.Errorf("UnmatchedPaths = %v, want empty (oracle: [])", scope.UnmatchedPaths)
	}
	if want := []string{"zpa_alpha_one", "zpa_alpha_two", "zpa_derived_reorder"}; !reflect.DeepEqual(scope.AffectedResources, want) {
		t.Errorf("AffectedResources = %v, want %v", scope.AffectedResources, want)
	}
	if len(scope.AffectedRoots) != 2 {
		t.Fatalf("AffectedRoots length = %d, want 2 (oracle: zpa_alpha, zpa_derived_reorder)", len(scope.AffectedRoots))
	}
	alpha, derived := scope.AffectedRoots[0], scope.AffectedRoots[1]
	if alpha.Label != "zpa_alpha" || !reflect.DeepEqual(alpha.Members, []string{"zpa_alpha_one", "zpa_alpha_two"}) {
		t.Errorf("AffectedRoots[0] = %+v, want label zpa_alpha with both members", alpha)
	}
	if len(alpha.Paths) != 6 {
		t.Errorf("zpa_alpha AffectedRoot.Paths length = %d, want 6 (every path touches it)", len(alpha.Paths))
	}
	if derived.Label != "zpa_derived_reorder" || !reflect.DeepEqual(derived.Paths, []string{scopeDeploymentPath}) {
		t.Errorf("AffectedRoots[1] = %+v, want label zpa_derived_reorder matched only by the deployment path", derived)
	}

	byPath := map[string]ChangedPathMatch{}
	for _, match := range scope.PathMatches {
		byPath[match.Path] = match
	}
	envMatch, ok := byPath["envs/acme/zpa_alpha"]
	if !ok {
		t.Fatal("no match for envs/acme/zpa_alpha")
	}
	if !reflect.DeepEqual(envMatch.Kinds, []ChangedPathKind{ChangedPathKindEnvRoot}) {
		t.Errorf("envs/acme/zpa_alpha Kinds = %v, want [env_root]", envMatch.Kinds)
	}
	if !reflect.DeepEqual(envMatch.Resources, []string{"zpa_alpha_one", "zpa_alpha_two"}) {
		t.Errorf("envs/acme/zpa_alpha Resources = %v, want both slug members (oracle)", envMatch.Resources)
	}
	if !reflect.DeepEqual(envMatch.Tenants, []string{"acme"}) {
		t.Errorf("envs/acme/zpa_alpha Tenants = %v, want [acme]", envMatch.Tenants)
	}
	deploymentMatch, ok := byPath[scopeDeploymentPath]
	if !ok {
		t.Fatal("no match for the deployment path itself")
	}
	if !reflect.DeepEqual(deploymentMatch.Kinds, []ChangedPathKind{ChangedPathKindDeployment}) {
		t.Errorf("deployment path Kinds = %v, want [deployment]", deploymentMatch.Kinds)
	}
	if !reflect.DeepEqual(deploymentMatch.Tenants, []string{}) {
		t.Errorf("deployment path Tenants = %v, want empty (matching every resource, not one tenant)", deploymentMatch.Tenants)
	}
}

// TestChangedPathScopeConfigSuffixLongestMatchWins ports the
// "shared-root-two-resources" and "config-suffix-longest-match" oracle
// scenarios: CONFIG_SUFFIXES is tried longest-first per path, so
// ".auto.tfvars.json" must win over ".auto.tfvars" for a name ending in
// both.
func TestChangedPathScopeConfigSuffixLongestMatchWins(t *testing.T) {
	scope, err := ChangedPathScopeFromCatalog(ChangedPathScopeOptions{
		Paths:          []string{"config/acme/zpa_alpha_one.auto.tfvars.json", "config/acme/zpa_alpha_two.auto.tfvars"},
		Workspace:      scopeWorkspace,
		DeploymentPath: scopeDeploymentPath,
		Deployment:     slugDeployment(),
		Catalog:        scopePlanFixtureCatalog(),
	})
	if err != nil {
		t.Fatalf("ChangedPathScopeFromCatalog: %v", err)
	}
	if len(scope.PathMatches) != 2 {
		t.Fatalf("PathMatches length = %d, want 2", len(scope.PathMatches))
	}
	if scope.PathMatches[0].Resources[0] != "zpa_alpha_one" || scope.PathMatches[1].Resources[0] != "zpa_alpha_two" {
		t.Errorf("PathMatches = %+v, want zpa_alpha_one then zpa_alpha_two (each stripped via its own longest matching CONFIG_SUFFIXES entry)", scope.PathMatches)
	}
	if want := []string{"zpa_alpha_one", "zpa_alpha_two"}; !reflect.DeepEqual(scope.AffectedResources, want) {
		t.Errorf("AffectedResources = %v, want %v", scope.AffectedResources, want)
	}
	if len(scope.AffectedRoots) != 1 || scope.AffectedRoots[0].Label != "zpa_alpha" {
		t.Fatalf("AffectedRoots = %+v, want a single zpa_alpha root (both resources share it)", scope.AffectedRoots)
	}
}

// TestChangedPathScopeUnnormalizedOverlayJoinsRawThenNormalizes ports the
// "unnormalized-overlay" oracle scenario: artifactRoot joins the deployment
// overlay VERBATIM via pythonPosixJoin (never pre-normalized), and it is
// pythonRelativeUnder's own pythonPathForms normalization -- not a
// upfront join-time fixup -- that reconciles it against the
// already-normalized input path.
func TestChangedPathScopeUnnormalizedOverlayJoinsRawThenNormalizes(t *testing.T) {
	dep := deployment.Deployment{
		Overlay: "artifacts//staging/../current",
		Roots:   map[string]deployment.RootProviderConfig{"zpa": {HasStrategy: true, Strategy: "slug"}},
	}
	scope, err := ChangedPathScopeFromCatalog(ChangedPathScopeOptions{
		Paths:          []string{"artifacts//staging/../current/config/acme/zpa_alpha_one.lookup.json"},
		Workspace:      scopeWorkspace,
		DeploymentPath: scopeDeploymentPath,
		Deployment:     dep,
		Catalog:        scopePlanFixtureCatalog(),
	})
	if err != nil {
		t.Fatalf("ChangedPathScopeFromCatalog: %v", err)
	}
	want := "artifacts/current/config/acme/zpa_alpha_one.lookup.json"
	if len(scope.Paths) != 1 || scope.Paths[0] != want {
		t.Fatalf("Paths = %v, want [%s] (pythonPosixNormPath collapses staging/.. even though overlay itself is joined raw)", scope.Paths, want)
	}
	if len(scope.PathMatches) != 1 || scope.PathMatches[0].Resources[0] != "zpa_alpha_one" {
		t.Fatalf("PathMatches = %+v, want a single zpa_alpha_one match", scope.PathMatches)
	}
}

// TestChangedPathScopeExplicitModuleDirIgnoresOverlay ports the
// "explicit-module-dir" oracle scenario: an explicit deployment.module_dir
// is used verbatim, with no overlay join at all, even when overlay is
// also set and non-".".
func TestChangedPathScopeExplicitModuleDirIgnoresOverlay(t *testing.T) {
	moduleDir := "custom/modules"
	dep := deployment.Deployment{
		Overlay: ".", HasModuleDir: true, ModuleDir: moduleDir,
		Roots: map[string]deployment.RootProviderConfig{"zpa": {HasStrategy: true, Strategy: "slug"}},
	}
	scope, err := ChangedPathScopeFromCatalog(ChangedPathScopeOptions{
		Paths:          []string{"custom/modules/zpa_alpha_one/main.tf"},
		Workspace:      scopeWorkspace,
		DeploymentPath: scopeDeploymentPath,
		Deployment:     dep,
		Catalog:        scopePlanFixtureCatalog(),
	})
	if err != nil {
		t.Fatalf("ChangedPathScopeFromCatalog: %v", err)
	}
	if len(scope.PathMatches) != 1 || !reflect.DeepEqual(scope.PathMatches[0].Kinds, []ChangedPathKind{ChangedPathKindModule}) {
		t.Fatalf("PathMatches = %+v, want a single module-kind match", scope.PathMatches)
	}
	if scope.PathMatches[0].Resources[0] != "zpa_alpha_one" {
		t.Errorf("matched resource = %v, want zpa_alpha_one", scope.PathMatches[0].Resources)
	}
}

// TestChangedPathScopeNeverValidatesTenantSegments ports the
// "no-tenant-validation" oracle scenario: unlike plan-roots (which
// validates every discovered tenant directory name), scope-paths records
// the raw path segment it finds in the "tenant" position verbatim, with
// no validateTenant call anywhere in changedPathScopeFromTopology or
// scopeOnePath -- confirmed by grep over node-src/domain/scope-paths.ts
// (it has no validateTenant import at all) and by this oracle scenario,
// where "bad tenant!" (a value validateTenant would reject: it contains a
// space and an exclamation mark, neither in [A-Za-z0-9_.-]+) passes
// straight through.
func TestChangedPathScopeNeverValidatesTenantSegments(t *testing.T) {
	scope, err := ChangedPathScopeFromCatalog(ChangedPathScopeOptions{
		Paths:          []string{"config/bad tenant!/zpa_alpha_one.auto.tfvars.json"},
		Workspace:      scopeWorkspace,
		DeploymentPath: scopeDeploymentPath,
		Deployment:     slugDeployment(),
		Catalog:        scopePlanFixtureCatalog(),
	})
	if err != nil {
		t.Fatalf("ChangedPathScopeFromCatalog: %v (want success -- scope-paths does not validate tenant segments)", err)
	}
	if len(scope.PathMatches) != 1 || !reflect.DeepEqual(scope.PathMatches[0].Tenants, []string{"bad tenant!"}) {
		t.Fatalf("PathMatches = %+v, want Tenants = [\"bad tenant!\"] verbatim", scope.PathMatches)
	}
}

// TestChangedPathScopeUnmatchedPathIsNotAnError ports the
// "unmatched-path" oracle scenario: a path matching no kind lands in
// UnmatchedPaths, without failing the call.
func TestChangedPathScopeUnmatchedPathIsNotAnError(t *testing.T) {
	scope, err := ChangedPathScopeFromCatalog(ChangedPathScopeOptions{
		Paths:          []string{"completely/unrelated/path.txt"},
		Workspace:      scopeWorkspace,
		DeploymentPath: scopeDeploymentPath,
		Deployment:     slugDeployment(),
		Catalog:        scopePlanFixtureCatalog(),
	})
	if err != nil {
		t.Fatalf("ChangedPathScopeFromCatalog: %v", err)
	}
	if len(scope.PathMatches) != 0 || !reflect.DeepEqual(scope.UnmatchedPaths, []string{"completely/unrelated/path.txt"}) {
		t.Errorf("PathMatches = %+v, UnmatchedPaths = %v", scope.PathMatches, scope.UnmatchedPaths)
	}
	if len(scope.AffectedResources) != 0 || len(scope.AffectedRoots) != 0 {
		t.Errorf("expected no affected resources/roots, got %v / %v", scope.AffectedResources, scope.AffectedRoots)
	}
}

// TestChangedPathScopeInvalidInputErrorTexts ports the "non-array-paths"
// (the JSON-array guard is not reachable through this Go entry point's
// typed []string parameter -- see changedPathScopeFromTopology's doc
// comment -- so this covers only the two per-element checks the oracle
// pins), "empty-string-path", "embedded-nul-path", and
// "non-string-overlay" oracle scenarios verbatim, including exact
// code/category/message text.
func TestChangedPathScopeInvalidInputErrorTexts(t *testing.T) {
	cases := []struct {
		name     string
		paths    []string
		dep      deployment.Deployment
		code     string
		category procerr.Category
		message  string
	}{
		{
			name:     "empty-string-path",
			paths:    []string{""},
			dep:      slugDeployment(),
			code:     "INVALID_CHANGED_PATHS",
			category: procerr.CategoryDomain,
			message:  "changed path at index 0 must be a non-empty string",
		},
		{
			name:     "embedded-nul-path",
			paths:    []string{"config/acme/zpa_alpha_one\x00.auto.tfvars.json"},
			dep:      slugDeployment(),
			code:     "INVALID_CHANGED_PATHS",
			category: procerr.CategoryDomain,
			message:  "changed path at index 0 contains an embedded null character",
		},
		{
			name:     "non-string-overlay",
			paths:    []string{"config/acme/zpa_alpha_one.auto.tfvars.json"},
			dep:      deployment.Deployment{Overlay: float64(7), Roots: map[string]deployment.RootProviderConfig{}},
			code:     "INVALID_DEPLOYMENT",
			category: procerr.CategoryDomain,
			message:  "deployment overlay must be a string when paths are scoped",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ChangedPathScopeFromCatalog(ChangedPathScopeOptions{
				Paths:          c.paths,
				Workspace:      scopeWorkspace,
				DeploymentPath: scopeDeploymentPath,
				Deployment:     c.dep,
				Catalog:        scopePlanFixtureCatalog(),
			})
			pf, ok := asProcessFailure(err)
			if !ok {
				t.Fatalf("err = %v, want a *procerr.ProcessFailure", err)
			}
			if pf.Code != c.code || pf.Category != c.category || pf.Message != c.message {
				t.Errorf("failure = {code: %q, category: %q, message: %q}, want {%q, %q, %q}",
					pf.Code, pf.Category, pf.Message, c.code, c.category, c.message)
			}
		})
	}
}
