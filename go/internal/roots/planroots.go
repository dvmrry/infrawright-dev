// planroots.go ports node-src/domain/plan-roots.ts: enumerating
// materialized environment roots (real on-disk envs/<tenant>/<label>
// directories) and classifying each one's tfplan/tfplan.sources artifact
// state -- the domain layer behind the `plan-roots` command. As with
// scope-paths.ts (see scopepaths.go's package doc comment), there is no
// node-tests/plan-roots.test.ts; planroots_test.go probes the compiled
// TypeScript directly (go/internal/roots/testdata/probe/scope_plan_probe.ts
// and its committed oracle) rather than porting a dedicated vector file.
package roots

import (
	"os"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/pypath"
)

// PlanRootArtifactState ports the `"absent" | "complete" | "incomplete"`
// artifact_state string-literal union from the MaterializedPlanRoot
// interface in node-src/domain/types.ts.
type PlanRootArtifactState string

// The three PlanRootArtifactState literals from node-src/domain/types.ts.
const (
	ArtifactStateAbsent     PlanRootArtifactState = "absent"
	ArtifactStateComplete   PlanRootArtifactState = "complete"
	ArtifactStateIncomplete PlanRootArtifactState = "incomplete"
)

// PlanRootArtifact ports the PlanRootArtifact interface from
// node-src/domain/types.ts.
type PlanRootArtifact struct {
	Path   string
	Exists bool
}

// MaterializedPlanRootArtifacts ports the anonymous `artifacts` shape
// nested in the MaterializedPlanRoot interface in
// node-src/domain/types.ts.
type MaterializedPlanRootArtifacts struct {
	Tfplan        PlanRootArtifact
	TfplanSources PlanRootArtifact
}

// MaterializedPlanRoot ports the MaterializedPlanRoot interface from
// node-src/domain/types.ts.
type MaterializedPlanRoot struct {
	Tenant        string
	Label         string
	Provider      *string
	Members       []string
	EnvDir        string
	ArtifactState PlanRootArtifactState
	Artifacts     MaterializedPlanRootArtifacts
}

// PlanRootsRequest ports the anonymous `request` shape nested in the
// PlanRoots interface in node-src/domain/types.ts.
type PlanRootsRequest struct {
	// Tenant is nil for the TS source's `tenant: string | null` being
	// null.
	Tenant    *string
	Selectors []string
}

// PlanRoots ports the PlanRoots interface from node-src/domain/types.ts.
// Like ChangedPathScope in scopepaths.go, this type carries no JSON
// struct tags -- see that type's doc comment for why canonical rendering
// is out of this port's scope.
type PlanRoots struct {
	Kind          string
	SchemaVersion int
	Request       PlanRootsRequest
	Roots         []MaterializedPlanRoot
}

// PlanRootsResult bundles planRoots/loadedPlanRoots's `{ result,
// diagnostics }` return shape from node-src/domain/plan-roots.ts into a
// single Go return value, the same pattern RootTopologyResult applies to
// rootTopologyFromIndex's own `{ topology, diagnostics }` shape in
// roots.go.
type PlanRootsResult struct {
	Result      PlanRoots
	Diagnostics []WholeRootDiagnostic
}

// envBase ports envBase from node-src/domain/plan-roots.ts.
func envBase(dep deployment.Deployment) string {
	overlay, ok := dep.Overlay.(string)
	if !ok {
		deploymentError("deployment overlay must be a string when plan roots are enumerated")
	}
	if overlay == "." {
		return "envs"
	}
	return pypath.PythonPosixJoin(overlay, "envs")
}

// resolveWorkspacePath ports resolveWorkspacePath from
// node-src/domain/plan-roots.ts.
func resolveWorkspacePath(workspace, candidate string) string {
	if strings.HasPrefix(candidate, "/") {
		return candidate
	}
	return pypath.PythonPosixJoin(workspace, candidate)
}

// planRootIsDirectory ports isDirectory from
// node-src/domain/plan-roots.ts. Named planRootIsDirectory (rather than
// isDirectory) purely to read unambiguously alongside this package's
// other, differently-scoped helpers.
func planRootIsDirectory(workspace, candidate string) bool {
	info, err := os.Stat(resolveWorkspacePath(workspace, candidate))
	return err == nil && info.IsDir()
}

// planRootIsFile ports isFile from node-src/domain/plan-roots.ts.
func planRootIsFile(workspace, candidate string) bool {
	info, err := os.Stat(resolveWorkspacePath(workspace, candidate))
	return err == nil && info.Mode().IsRegular()
}

// directoryNames ports directoryNames from
// node-src/domain/plan-roots.ts, including its READ_FAILED/io failure on
// any error other than the directory simply not existing (which callers
// here always guard with planRootIsDirectory first, so in practice this
// only fires for a permission error or a similar genuine I/O failure).
func directoryNames(workspace, candidate string) []string {
	entries, err := os.ReadDir(resolveWorkspacePath(workspace, candidate))
	if err != nil {
		panic(procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
			Code:     "READ_FAILED",
			Category: procerr.CategoryIO,
			Message:  "unable to enumerate materialized environment roots",
		}))
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return canonjson.SortedStrings(names)
}

// discoveredRoot is the Go analogue of the DiscoveredRoot interface in
// node-src/domain/plan-roots.ts.
type discoveredRoot struct {
	tenant string
	path   string
	root   RootTopologyRoot
}

// discoverOptions bundles discover's parameters, the Go analogue of the
// inline options-object parameter type node-src/domain/plan-roots.ts's
// discover accepts.
type discoverOptions struct {
	workspace    string
	deployment   deployment.Deployment
	tenant       *string
	rootsByLabel map[string]RootTopologyRoot
}

// discover ports discover from node-src/domain/plan-roots.ts: walk
// envs/<tenant>/<label> for every tenant directory (or just the one
// requested tenant, when non-nil), keeping only <label> subdirectories
// that name a known topology root.
func discover(options discoverOptions) []discoveredRoot {
	base := envBase(options.deployment)
	var tenantNames []string
	if options.tenant == nil {
		if planRootIsDirectory(options.workspace, base) {
			tenantNames = directoryNames(options.workspace, base)
		}
	} else {
		tenantNames = []string{*options.tenant}
	}
	discovered := make([]discoveredRoot, 0, len(tenantNames))
	for _, tenant := range tenantNames {
		tenantDir := pypath.PythonPosixJoin(base, tenant)
		if !planRootIsDirectory(options.workspace, tenantDir) {
			continue
		}
		for _, label := range directoryNames(options.workspace, tenantDir) {
			root, ok := options.rootsByLabel[label]
			if !ok {
				continue
			}
			rootPath := pypath.PythonPosixJoin(tenantDir, label)
			if planRootIsDirectory(options.workspace, rootPath) {
				discovered = append(discovered, discoveredRoot{tenant: tenant, path: rootPath, root: root})
			}
		}
	}
	return discovered
}

// planRootsFromTopologiesOptions bundles planRootsFromTopologies's
// parameters, the Go analogue of the inline options-object parameter type
// node-src/domain/plan-roots.ts's planRootsFromTopologies accepts.
type planRootsFromTopologiesOptions struct {
	workspace  string
	deployment deployment.Deployment
	tenant     *string
	selectors  []string
	all        RootTopology
	selected   RootTopologyResult
}

// planRootsFromTopologies ports planRootsFromTopologies from
// node-src/domain/plan-roots.ts.
//
// A discovered materialized root is validated (validateTenant(entry.tenant))
// and included in the result only if its label is among selectedLabels
// (the roots the *selected* topology -- selectors applied -- actually
// resolves to); an on-disk tenant directory that never matches any
// selected root's label is silently skipped without ever reaching
// validateTenant, exactly as the TS source's own `if
// (!selectedLabels.has(entry.root.label)) continue;` guard precedes its
// `validateTenant(entry.tenant)` call. This means an invalid on-disk
// tenant directory name (e.g. containing a space) is tolerated as long as
// none of its recognized root-label subdirectories happen to be selected
// -- ported deliberately, not a gap: see planroots_test.go's
// "invalid-tenant-directory-name-is-tolerated-unless-selected" case,
// which pins exactly this against the probe oracle.
func planRootsFromTopologies(options planRootsFromTopologiesOptions) PlanRootsResult {
	if options.tenant != nil {
		validateTenant(*options.tenant)
	}
	selectedLabels := make(map[string]struct{}, len(options.selected.Topology.Roots))
	for _, root := range options.selected.Topology.Roots {
		selectedLabels[root.Label] = struct{}{}
	}
	diagnosticsByLabel := make(map[string]WholeRootDiagnostic, len(options.selected.Diagnostics))
	for _, diagnostic := range options.selected.Diagnostics {
		diagnosticsByLabel[diagnostic.Root] = diagnostic
	}
	rootsByLabel := make(map[string]RootTopologyRoot, len(options.all.Roots))
	for _, root := range options.all.Roots {
		rootsByLabel[root.Label] = root
	}
	discovered := discover(discoverOptions{
		workspace:    options.workspace,
		deployment:   options.deployment,
		tenant:       options.tenant,
		rootsByLabel: rootsByLabel,
	})

	diagnostics := make([]WholeRootDiagnostic, 0)
	roots := make([]MaterializedPlanRoot, 0, len(discovered))
	for _, entry := range discovered {
		if _, ok := selectedLabels[entry.root.Label]; !ok {
			continue
		}
		validateTenant(entry.tenant)
		if diagnostic, ok := diagnosticsByLabel[entry.root.Label]; ok {
			diagnostics = append(diagnostics, diagnostic)
		}
		tfplanPath := pypath.PythonPosixJoin(entry.path, "tfplan")
		sourcesPath := pypath.PythonPosixJoin(entry.path, "tfplan.sources")
		planExists := planRootIsFile(options.workspace, tfplanPath)
		sourcesExist := planRootIsFile(options.workspace, sourcesPath)
		var artifactState PlanRootArtifactState
		switch {
		case planExists && sourcesExist:
			artifactState = ArtifactStateComplete
		case planExists || sourcesExist:
			artifactState = ArtifactStateIncomplete
		default:
			artifactState = ArtifactStateAbsent
		}
		roots = append(roots, MaterializedPlanRoot{
			Tenant:        entry.tenant,
			Label:         entry.root.Label,
			Provider:      entry.root.Provider,
			Members:       entry.root.Members,
			EnvDir:        entry.path,
			ArtifactState: artifactState,
			Artifacts: MaterializedPlanRootArtifacts{
				Tfplan:        PlanRootArtifact{Path: tfplanPath, Exists: planExists},
				TfplanSources: PlanRootArtifact{Path: sourcesPath, Exists: sourcesExist},
			},
		})
	}

	selectors := options.selectors
	if selectors == nil {
		selectors = []string{}
	}
	return PlanRootsResult{
		Result: PlanRoots{
			Kind:          "infrawright.plan_roots",
			SchemaVersion: 1,
			Request:       PlanRootsRequest{Tenant: options.tenant, Selectors: selectors},
			Roots:         roots,
		},
		Diagnostics: diagnostics,
	}
}

// PlanRootsOptions bundles PlanRootsFromCatalog's parameters, the Go
// analogue of the inline options-object parameter type
// node-src/domain/plan-roots.ts's planRoots accepts.
type PlanRootsOptions struct {
	Workspace  string
	Deployment deployment.Deployment
	Catalog    metadata.RootCatalog
	// Tenant is nil for the TS source's `tenant: string | null` being
	// null.
	Tenant    *string
	Selectors []string
}

// PlanRootsFromCatalog ports planRoots from node-src/domain/plan-roots.ts.
// Named PlanRootsFromCatalog (rather than PlanRoots, which the PlanRoots
// struct type above already claims) for the same function/type name-clash
// reason roots.go's RootTopologyFromCatalog is not named RootTopology --
// see that function's doc comment.
func PlanRootsFromCatalog(options PlanRootsOptions) (result PlanRootsResult, err error) {
	defer recoverProcessFailure(&err)
	if len(options.Selectors) > 0 {
		// Preserve the historical explicit validation before root
		// resolution -- ported verbatim, comment included, from
		// node-src/domain/plan-roots.ts's planRoots. The
		// rootTopologyFromIndex call below (building `selected`) already
		// runs the identical expandResources check on the same selectors
		// and would raise the same UNKNOWN_RESOURCE_SELECTOR failure on
		// its own; this call's result is discarded, kept only for
		// structural parity with the TS source (see this port's
		// non-goal against "optimizing away" redundant-looking checks
		// found during porting, docs/go-runtime-plan.md).
		expandResources(options.Selectors, indexCatalog(options.Catalog))
	}
	all := rootTopologyFromIndex(rootTopologyFromIndexOptions{
		index:     indexCatalog(options.Catalog),
		dep:       options.Deployment,
		tenant:    nil,
		selectors: []string{},
	})
	selected := rootTopologyFromIndex(rootTopologyFromIndexOptions{
		index:     indexCatalog(options.Catalog),
		dep:       options.Deployment,
		tenant:    nil,
		selectors: options.Selectors,
	})
	return planRootsFromTopologies(planRootsFromTopologiesOptions{
		workspace:  options.Workspace,
		deployment: options.Deployment,
		tenant:     options.Tenant,
		selectors:  options.Selectors,
		all:        all.Topology,
		selected:   selected,
	}), nil
}

// LoadedPlanRootsOptions bundles LoadedPlanRoots's parameters, the Go
// analogue of the inline options-object parameter type
// node-src/domain/plan-roots.ts's loadedPlanRoots accepts.
type LoadedPlanRootsOptions struct {
	Workspace  string
	Deployment deployment.Deployment
	Root       metadata.LoadedPackRoot
	// Tenant is nil for the TS source's `tenant: string | null` being
	// null.
	Tenant    *string
	Selectors []string
}

// LoadedPlanRoots ports loadedPlanRoots from
// node-src/domain/plan-roots.ts: "Enumerate materialized roots from the
// active pack metadata loader." Unlike PlanRootsFromCatalog, this has no
// upfront expandResources precheck -- node-src/domain/plan-roots.ts's own
// loadedPlanRoots does not call expandLoadedResources either; only
// planRoots (the persisted-RootCatalog entry point) carries that
// "historical explicit validation" duplicate.
func LoadedPlanRoots(options LoadedPlanRootsOptions) (result PlanRootsResult, err error) {
	defer recoverProcessFailure(&err)
	all := rootTopologyFromIndex(rootTopologyFromIndexOptions{
		index:     indexLoadedPackRoot(options.Root),
		dep:       options.Deployment,
		tenant:    nil,
		selectors: []string{},
	})
	selected := rootTopologyFromIndex(rootTopologyFromIndexOptions{
		index:     indexLoadedPackRoot(options.Root),
		dep:       options.Deployment,
		tenant:    nil,
		selectors: options.Selectors,
	})
	return planRootsFromTopologies(planRootsFromTopologiesOptions{
		workspace:  options.Workspace,
		deployment: options.Deployment,
		tenant:     options.Tenant,
		selectors:  options.Selectors,
		all:        all.Topology,
		selected:   selected,
	}), nil
}
