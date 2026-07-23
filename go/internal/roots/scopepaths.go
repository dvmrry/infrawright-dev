// Package roots maps changed filesystem paths to generated resources and
// singleton state roots for the scope-paths command.
package roots

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/posixpath"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// configSuffixes ports the CONFIG_SUFFIXES tuple from
// the original implementation.
var configSuffixes = []string{
	".generated.expressions.json",
	".expressions.json",
	".auto.tfvars.json",
	".auto.tfvars",
	".lookup.json",
}

// importSuffixes ports the IMPORT_SUFFIXES tuple from
// the original implementation.
var importSuffixes = []string{"_imports.tf", "_moves.tf"}

// deploymentError panics with a *procerr.ProcessFailure carrying code
// "INVALID_DEPLOYMENT" and category domain. the original implementation
// and the original implementation each define their own local
// `deploymentError` helper with this identical {code, category} shape --
// only the message text passed at each call site differs -- so this Go
// port consolidates the two into one package-level function, the same way
// roots.go's own domainError/domainErrorCode is already shared plumbing
// across everything in this package (see this file's package doc comment
// for the broader "siblings in one package" rationale). planroots.go calls
// this too.
func deploymentError(message string) {
	panic(procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "INVALID_DEPLOYMENT",
		Category: procerr.CategoryDomain,
		Message:  message,
	}))
}

// internalError panics with a *procerr.ProcessFailure carrying code
// "INVALID_OPERATION_RESULT" and category internal, ported from
// the original implementation's internalError. Both call sites
// (scopeOnePath's resourceRoots lookup and changedPathScopeFromTopology's
// rootsByLabel lookup) are defensive: they fire only if a resource or
// root label that a RootTopology itself vouches for turns out to be
// missing from that same RootTopology, which a topology built by
// rootTopologyFromIndex can never actually produce -- kept for structural
// parity with the TS source's own defensive checks, not because either
// branch is reachable through this port's public entry points.
func internalError(message string) {
	panic(procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "INVALID_OPERATION_RESULT",
		Category: procerr.CategoryInternal,
		Message:  message,
	}))
}

// keysOfSet returns set's members in unspecified order. Callers needing a
// deterministic order pass the result through canonjson.SortedStrings.
func keysOfSet(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}

// ChangedPathKind ports the ChangedPathKind string-literal union from
// the original implementation.
type ChangedPathKind string

// The five ChangedPathKind literals from the original implementation.
const (
	ChangedPathKindConfig     ChangedPathKind = "config"
	ChangedPathKindDeployment ChangedPathKind = "deployment"
	ChangedPathKindEnvRoot    ChangedPathKind = "env_root"
	ChangedPathKindImports    ChangedPathKind = "imports"
	ChangedPathKindModule     ChangedPathKind = "module"
)

// ChangedPathMatch ports the ChangedPathMatch interface from
// the original implementation.
type ChangedPathMatch struct {
	Path      string
	Kinds     []ChangedPathKind
	Tenants   []string
	Resources []string
	Roots     []string
}

// AffectedRoot ports the AffectedRoot interface from
// the original implementation.
type AffectedRoot struct {
	Label            string
	Provider         *string
	Members          []string
	MatchedResources []string
	Paths            []string
}

// ChangedPathScope ports the ChangedPathScope interface from
// the original implementation. Like RootTopology/WholeRootDiagnostic in
// roots.go, this type carries no JSON struct tags: canonical rendering
// (the original implementation's renderLegacyChangedPathScope
// in the TS source) is a CLI-layer concern this port's roots/scope-paths/
// plan-roots slice does not include -- see this file's doc comment.
type ChangedPathScope struct {
	Kind              string
	SchemaVersion     int
	Paths             []string
	PathMatches       []ChangedPathMatch
	UnmatchedPaths    []string
	AffectedResources []string
	AffectedRoots     []AffectedRoot
}

// artifactRoot ports artifactRoot from the original implementation.
func artifactRoot(dep deployment.Deployment, kind string) string {
	overlay, ok := dep.Overlay.(string)
	if !ok {
		deploymentError("deployment overlay must be a string when paths are scoped")
	}
	if overlay == "." {
		return kind
	}
	return posixpath.Join(overlay, kind)
}

// scopePathsModuleRoot ports moduleRoot from
// the original implementation. Named scopePathsModuleRoot (rather than
// moduleRoot) to keep it unambiguous alongside planroots.go's own,
// differently-scoped helpers, even though nothing in this package
// currently collides.
func scopePathsModuleRoot(dep deployment.Deployment) string {
	if dep.HasModuleDir {
		moduleDir, ok := dep.ModuleDir.(string)
		if !ok {
			deploymentError("deployment module_dir must be a string when paths are scoped")
		}
		return moduleDir
	}
	overlay, ok := dep.Overlay.(string)
	if !ok {
		deploymentError("deployment overlay must be a string when paths are scoped")
	}
	if overlay == "." {
		return "modules"
	}
	return posixpath.Join(overlay, "modules", "default")
}

// resourceFromArtifact ports resourceFromArtifact from
// the original implementation.
func resourceFromArtifact(name string, suffixes []string, resources map[string]struct{}) (string, bool) {
	longestFirst := append([]string(nil), suffixes...)
	sort.SliceStable(longestFirst, func(i, j int) bool { return len(longestFirst[i]) > len(longestFirst[j]) })
	for _, suffix := range longestFirst {
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		resource := name[:len(name)-len(suffix)]
		if _, ok := resources[resource]; ok {
			return resource, true
		}
	}
	return "", false
}

// scopeOnePathOptions bundles scopeOnePath's parameters, the Go analogue
// of the inline options-object parameter type
// the original implementation's scopeOnePath accepts.
type scopeOnePathOptions struct {
	path           string
	workspace      string
	deploymentPath string
	deployment     deployment.Deployment
	resources      map[string]struct{}
	rootsByLabel   map[string]RootTopologyRoot
	resourceRoots  map[string]string
}

// scopeOnePath ports scopeOnePath from the original implementation. A
// nil return is the Go analogue of the TS function's `| null` return: the
// path matched nothing.
func scopeOnePath(options scopeOnePathOptions) *ChangedPathMatch {
	matchedResources := map[string]struct{}{}
	kinds := map[string]struct{}{}
	tenants := map[string]struct{}{}

	if posixpath.SameContractPath(options.path, options.deploymentPath, options.workspace) {
		for resource := range options.resources {
			matchedResources[resource] = struct{}{}
		}
		kinds[string(ChangedPathKindDeployment)] = struct{}{}
	}

	if relative, ok := posixpath.RelativeUnder(options.path, artifactRoot(options.deployment, "config"), options.workspace); ok && len(relative) == 2 {
		if resource, found := resourceFromArtifact(relative[1], configSuffixes, options.resources); found {
			matchedResources[resource] = struct{}{}
			tenants[relative[0]] = struct{}{}
			kinds[string(ChangedPathKindConfig)] = struct{}{}
		}
	}

	if relative, ok := posixpath.RelativeUnder(options.path, artifactRoot(options.deployment, "imports"), options.workspace); ok && len(relative) == 2 {
		if resource, found := resourceFromArtifact(relative[1], importSuffixes, options.resources); found {
			matchedResources[resource] = struct{}{}
			tenants[relative[0]] = struct{}{}
			kinds[string(ChangedPathKindImports)] = struct{}{}
		}
	}

	if relative, ok := posixpath.RelativeUnder(options.path, artifactRoot(options.deployment, "envs"), options.workspace); ok && len(relative) >= 2 {
		if root, found := options.rootsByLabel[relative[1]]; found {
			for _, member := range root.Members {
				matchedResources[member] = struct{}{}
			}
			tenants[relative[0]] = struct{}{}
			kinds[string(ChangedPathKindEnvRoot)] = struct{}{}
		}
	}

	if relative, ok := posixpath.RelativeUnder(options.path, scopePathsModuleRoot(options.deployment), options.workspace); ok && len(relative) > 0 {
		resource := relative[0]
		if _, found := options.resources[resource]; found {
			matchedResources[resource] = struct{}{}
			kinds[string(ChangedPathKindModule)] = struct{}{}
		}
	}

	if len(matchedResources) == 0 {
		return nil
	}
	resources := canonjson.SortedStrings(keysOfSet(matchedResources))
	rootLabels := map[string]struct{}{}
	for _, resource := range resources {
		label, ok := options.resourceRoots[resource]
		if !ok {
			internalError("generated resource '" + resource + "' has no logical root")
		}
		rootLabels[label] = struct{}{}
	}
	kindNames := canonjson.SortedStrings(keysOfSet(kinds))
	kindsList := make([]ChangedPathKind, len(kindNames))
	for i, name := range kindNames {
		kindsList[i] = ChangedPathKind(name)
	}
	return &ChangedPathMatch{
		Path:      options.path,
		Kinds:     kindsList,
		Tenants:   canonjson.SortedStrings(keysOfSet(tenants)),
		Resources: resources,
		Roots:     canonjson.SortedStrings(keysOfSet(rootLabels)),
	}
}

// changedPathScopeOptions bundles changedPathScopeFromTopology's
// parameters, the Go analogue of the inline options-object parameter type
// the original implementation's changedPathScopeFromTopology accepts.
type changedPathScopeOptions struct {
	paths          []string
	workspace      string
	deploymentPath string
	deployment     deployment.Deployment
	topology       RootTopology
}

// changedPathScopeFromTopology ports changedPathScopeFromTopology from
// the original implementation.
//
// The TS source's own first check, `if (!Array.isArray(options.paths))`
// (raising INVALID_CHANGED_PATHS "changed paths must be a JSON array or
// repeated --path"), has no reachable branch here: this function's
// `paths []string` parameter is already statically guaranteed to be a
// string slice by Go's type system, the same way the original source treedomain/
// scope-paths.ts's own `paths: readonly string[]` static type already
// guarantees it there too -- the TS runtime check exists only to defend
// against a caller that bypasses that static type (e.g. a CLI layer
// casting freshly-decoded, unvalidated JSON). A future Go CLI-argument-
// decoding layer (the Go runtime contract's slice 9, `internal/cli`) that
// decodes raw `--paths-json` input into an `any` before it becomes a
// `[]string` is exactly where that check's Go analogue belongs, and it
// MUST raise this identical code/category/message when the decoded value
// is not an array of strings; nothing in this file (or the current
// exported ChangedPathScopeFromResourceSet/ChangedPathScopeLoaded entry
// points) can trigger it, so no test here exercises it either.
func changedPathScopeFromTopology(options changedPathScopeOptions) ChangedPathScope {
	normalized := make([]string, 0, len(options.paths))
	for index, candidate := range options.paths {
		if candidate == "" {
			domainErrorCode(fmt.Sprintf("changed path at index %d must be a non-empty string", index), "INVALID_CHANGED_PATHS")
		}
		if strings.ContainsRune(candidate, 0) {
			domainErrorCode(fmt.Sprintf("changed path at index %d contains an embedded null character", index), "INVALID_CHANGED_PATHS")
		}
		normalized = append(normalized, posixpath.Normalize(candidate))
	}
	paths := canonjson.SortedStrings(keysOfSet(stringSet(normalized)))

	topology := options.topology
	rootsByLabel := make(map[string]RootTopologyRoot, len(topology.Roots))
	for _, root := range topology.Roots {
		rootsByLabel[root.Label] = root
	}
	resources := make(map[string]struct{}, len(topology.ResourceRoots))
	for resourceType := range topology.ResourceRoots {
		resources[resourceType] = struct{}{}
	}

	pathMatches := make([]ChangedPathMatch, 0, len(paths))
	unmatchedPaths := make([]string, 0, len(paths))
	for _, changedPath := range paths {
		match := scopeOnePath(scopeOnePathOptions{
			path:           changedPath,
			workspace:      options.workspace,
			deploymentPath: options.deploymentPath,
			deployment:     options.deployment,
			resources:      resources,
			rootsByLabel:   rootsByLabel,
			resourceRoots:  topology.ResourceRoots,
		})
		if match == nil {
			unmatchedPaths = append(unmatchedPaths, changedPath)
		} else {
			pathMatches = append(pathMatches, *match)
		}
	}

	affectedResourceSet := map[string]struct{}{}
	for _, match := range pathMatches {
		for _, resource := range match.Resources {
			affectedResourceSet[resource] = struct{}{}
		}
	}
	affectedResources := canonjson.SortedStrings(keysOfSet(affectedResourceSet))

	rootPaths := map[string]map[string]struct{}{}
	rootResources := map[string]map[string]struct{}{}
	for _, match := range pathMatches {
		for _, label := range match.Roots {
			if rootPaths[label] == nil {
				rootPaths[label] = map[string]struct{}{}
			}
			rootPaths[label][match.Path] = struct{}{}
			if rootResources[label] == nil {
				rootResources[label] = map[string]struct{}{}
			}
			for _, resource := range match.Resources {
				if topology.ResourceRoots[resource] == label {
					rootResources[label][resource] = struct{}{}
				}
			}
		}
	}
	affectedRootLabels := make([]string, 0, len(rootPaths))
	for label := range rootPaths {
		affectedRootLabels = append(affectedRootLabels, label)
	}
	affectedRootLabels = canonjson.SortedStrings(affectedRootLabels)

	affectedRoots := make([]AffectedRoot, 0, len(affectedRootLabels))
	for _, label := range affectedRootLabels {
		root, ok := rootsByLabel[label]
		if !ok {
			internalError("logical root '" + label + "' is missing from topology")
		}
		affectedRoots = append(affectedRoots, AffectedRoot{
			Label:            label,
			Provider:         root.Provider,
			Members:          root.Members,
			MatchedResources: canonjson.SortedStrings(keysOfSet(rootResources[label])),
			Paths:            canonjson.SortedStrings(keysOfSet(rootPaths[label])),
		})
	}

	return ChangedPathScope{
		Kind:              "infrawright.changed_path_scope",
		SchemaVersion:     1,
		Paths:             paths,
		PathMatches:       pathMatches,
		UnmatchedPaths:    unmatchedPaths,
		AffectedResources: affectedResources,
		AffectedRoots:     affectedRoots,
	}
}

// ChangedPathScopeOptions bundles ChangedPathScopeFromResourceSet's
// parameters, the Go analogue of the inline options-object parameter type
// the original implementation's changedPathScope accepts.
type ChangedPathScopeOptions struct {
	Paths          []string
	Workspace      string
	DeploymentPath string
	Deployment     deployment.Deployment
	ResourceSet    metadata.ResourceSet
}

// ChangedPathScopeFromResourceSet ports changedPathScope from
// the original implementation. Named ChangedPathScopeFromResourceSet
// (rather than ChangedPathScope, which the ChangedPathScope struct type
// above already claims) for the same function/type name-clash reason
// roots.go's RootTopologyFromResourceSet is not named RootTopology -- see that
// function's doc comment.
func ChangedPathScopeFromResourceSet(options ChangedPathScopeOptions) (scope ChangedPathScope, err error) {
	defer recoverProcessFailure(&err)
	topology := rootTopologyFromIndex(rootTopologyFromIndexOptions{
		index:     indexResourceSet(options.ResourceSet),
		dep:       options.Deployment,
		tenant:    nil,
		selectors: []string{},
	}).Topology
	return changedPathScopeFromTopology(changedPathScopeOptions{
		paths:          options.Paths,
		workspace:      options.Workspace,
		deploymentPath: options.DeploymentPath,
		deployment:     options.Deployment,
		topology:       topology,
	}), nil
}

// ChangedPathScopeLoadedOptions bundles ChangedPathScopeLoaded's
// parameters, the Go analogue of the inline options-object parameter type
// the original implementation's changedPathScopeLoaded accepts.
type ChangedPathScopeLoadedOptions struct {
	Paths          []string
	Workspace      string
	DeploymentPath string
	Deployment     deployment.Deployment
	Root           metadata.LoadedPackRoot
}

// ChangedPathScopeLoaded ports changedPathScopeLoaded from
// the original implementation: "Scope changed paths against the same
// live pack metadata used by the CLI."
func ChangedPathScopeLoaded(options ChangedPathScopeLoadedOptions) (scope ChangedPathScope, err error) {
	defer recoverProcessFailure(&err)
	topology := rootTopologyFromIndex(rootTopologyFromIndexOptions{
		index:     indexLoadedPackRoot(options.Root),
		dep:       options.Deployment,
		tenant:    nil,
		selectors: []string{},
	}).Topology
	return changedPathScopeFromTopology(changedPathScopeOptions{
		paths:          options.Paths,
		workspace:      options.Workspace,
		deploymentPath: options.DeploymentPath,
		deployment:     options.Deployment,
		topology:       topology,
	}), nil
}
