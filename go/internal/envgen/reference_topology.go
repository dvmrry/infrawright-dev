package envgen

// reference_topology.go ports the original implementation: the
// cross-state reference DAG derived from pack-declared reference metadata,
// with cycle detection.
//
// TS-import mapping (see this package's port report for the full table):
//   - LoadedPackRoot                 -> metadata.LoadedPackRoot
//   - isObject                       -> canonjson.IsJSONRecord (identical
//     record-vs-array-vs-scalar semantics; see expression_bindings.go's
//     doc comment for why this port consolidates on the canonjson helper
//     rather than redefining a local isObject/record duplicate per file)
//   - comparePythonStrings/sortedStrings -> canonjson.ComparePythonStrings/
//     canonjson.SortedStrings
//   - deploymentReferenceBindingMode -> deployment.DeploymentReferenceBindingMode
//   - mergedTransformReferences      -> transform.MergedTransformReferences
//   - Deployment, RootTopology       -> deployment.Deployment,
//     roots.RootTopology (RootTopology.ResourceRoots plays the
//     `resource_roots` field's role)
//
// Errors: reference-topology.ts throws plain TypeErrors, exactly like
// expression-bindings.ts; this file reuses that file's bindingsFail/
// recoverBindingsError panic convention rather than defining a second,
// identical one (both TS sources are ported into this one Go package).
import (
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
)

// InfrawrightReferenceOutput ports INFRAWRIGHT_REFERENCE_OUTPUT from
// the original implementation.
const InfrawrightReferenceOutput = "infrawright_reference_ids"

// CrossStateReferenceEdge is the Go analogue of the CrossStateReferenceEdge
// interface in the original implementation.
type CrossStateReferenceEdge struct {
	Field        string
	Referrer     string
	ReferrerRoot string
	Referent     string
	ReferentRoot string
}

// CrossStateReferenceTopology is the Go analogue of the
// CrossStateReferenceTopology interface in
// the original implementation. DependenciesByRoot/OutputsByRoot
// use this port's usual presence-only string-set representation
// (map[string]map[string]bool), the same convention
// go/internal/tfrender/transform_artifacts.go's BindingContext already
// establishes for a TS `ReadonlySet<string>`.
type CrossStateReferenceTopology struct {
	Edges              []CrossStateReferenceEdge
	DependenciesByRoot map[string]map[string]bool
	OutputsByRoot      map[string]map[string]bool
}

func mapKeysBoolSet(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func mapKeysOfReferences(m map[string]map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// CrossStateDependencyClosure ports the exported
// crossStateDependencyClosure from the original implementation:
// "Expand selected state roots through their complete referent dependency
// set." Never fails (no TS throw sites), so unlike most of this package's
// other exports it returns a plain []string, no error.
func CrossStateDependencyClosure(selectedRoots []string, dependenciesByRoot map[string]map[string]bool) []string {
	selected := map[string]bool{}
	for _, root := range selectedRoots {
		selected[root] = true
	}
	pending := canonjson.SortedStrings(mapKeysBoolSet(selected))
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		for _, dependency := range canonjson.SortedStrings(mapKeysBoolSet(dependenciesByRoot[current])) {
			if selected[dependency] {
				continue
			}
			selected[dependency] = true
			pending = append(pending, dependency)
			sort.Slice(pending, func(i, j int) bool {
				return canonjson.ComparePythonStrings(pending[i], pending[j]) < 0
			})
		}
	}
	return canonjson.SortedStrings(mapKeysBoolSet(selected))
}

// generatedNonDerived ports the local generatedNonDerived helper from
// the original implementation. Note this uses
// canonjson.IsJSONRecord (the port of metadata/validation.ts's isObject,
// which excludes arrays), deliberately distinct from
// go/internal/roots/roots.go's isJSObjectLike (which treats arrays as
// object-like too, for a different call site's JS `typeof` quirk) -- the
// two Node source files import two different helpers for a reason, and
// this port preserves that distinction rather than consolidating them.
func generatedNonDerived(root metadata.LoadedPackRoot, resourceType string) bool {
	resource, ok := root.Resources[resourceType]
	if !ok {
		return false
	}
	generate, _ := resource.Registry["generate"].(bool)
	if !generate {
		return false
	}
	return !canonjson.IsJSONRecord(resource.Registry["derive"])
}

// addToSet ports the local `add` helper from
// the original implementation.
func addToSet(values map[string]map[string]bool, key, value string) {
	set, ok := values[key]
	if !ok {
		set = map[string]bool{}
		values[key] = set
	}
	set[value] = true
}

// cyclePathAcrossRoots ports the local cyclePath helper from
// the original implementation (a plain DFS three-color cycle
// detector), named to avoid colliding with environment-generator.go's own,
// differently-scoped cyclePath port of the same-named TS helper in
// environment-generator.ts (that one walks expression-binding module
// targets within one root's members, not deployment-root dependency
// edges -- the two TS files each define their own local cyclePath, and this
// Go package keeps that separation with distinct names).
func cyclePathAcrossRoots(dependencies map[string]map[string]bool) []string {
	const (
		stateVisiting = "visiting"
		stateDone     = "done"
	)
	state := map[string]string{}
	var stack []string
	var visit func(string) []string
	visit = func(root string) []string {
		state[root] = stateVisiting
		stack = append(stack, root)
		for _, dependency := range canonjson.SortedStrings(mapKeysBoolSet(dependencies[root])) {
			if state[dependency] == stateVisiting {
				start := indexOfString(stack, dependency)
				found := append([]string{}, stack[start:]...)
				found = append(found, dependency)
				return found
			}
			if state[dependency] == "" {
				if found := visit(dependency); found != nil {
					return found
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[root] = stateDone
		return nil
	}
	nodes := map[string]bool{}
	for root, targets := range dependencies {
		nodes[root] = true
		for target := range targets {
			nodes[target] = true
		}
	}
	for _, root := range canonjson.SortedStrings(mapKeysBoolSet(nodes)) {
		if state[root] != "" {
			continue
		}
		if found := visit(root); found != nil {
			return found
		}
	}
	return nil
}

func indexOfString(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

// CrossStateReferenceTopologyOptions bundles CrossStateReferenceTopology's
// parameters, the Go analogue of the inline options-object parameter type
// the original implementation's crossStateReferenceTopology
// accepts.
type CrossStateReferenceTopologyOptions struct {
	Deployment deployment.Deployment
	Root       metadata.LoadedPackRoot
	Topology   roots.RootTopology
}

func sortCrossStateEdges(edges []CrossStateReferenceEdge) {
	sort.SliceStable(edges, func(i, j int) bool {
		if c := canonjson.ComparePythonStrings(edges[i].Referrer, edges[j].Referrer); c != 0 {
			return c < 0
		}
		if c := canonjson.ComparePythonStrings(edges[i].Field, edges[j].Field); c != 0 {
			return c < 0
		}
		return canonjson.ComparePythonStrings(edges[i].Referent, edges[j].Referent) < 0
	})
}

// crossStateReferenceTopology ports the exported crossStateReferenceTopology
// from the original implementation: "Resolve the pack-declared
// edges that cross deployment state boundaries."
func crossStateReferenceTopology(options CrossStateReferenceTopologyOptions) CrossStateReferenceTopology {
	edges := []CrossStateReferenceEdge{}
	dependenciesByRoot := map[string]map[string]bool{}
	outputsByRoot := map[string]map[string]bool{}

	references := transform.MergedTransformReferences(options.Root)
	for _, referrer := range canonjson.SortedStrings(mapKeysOfReferences(references)) {
		referrerResource, ok := options.Root.Resources[referrer]
		if !ok || deployment.DeploymentReferenceBindingMode(options.Deployment, referrerResource.Provider) != deployment.ReferenceBindingCrossState {
			continue
		}
		if !generatedNonDerived(options.Root, referrer) {
			bindingsFail("cross-state reference referrer %s must be a generated non-derived resource", referrer)
		}
		referrerRoot, ok := options.Topology.ResourceRoots[referrer]
		if !ok {
			bindingsFail("cross-state reference referrer %s has no deployment root", referrer)
		}
		fields := references[referrer]
		for _, field := range canonjson.SortedStrings(mapKeys(fields)) {
			specification, ok := fields[field].(map[string]any)
			if !ok {
				continue
			}
			referentValue, hasReferent := specification["referent"]
			referent, isString := referentValue.(string)
			if !hasReferent || !isString {
				continue
			}
			if !generatedNonDerived(options.Root, referent) {
				bindingsFail(
					"cross-state reference %s.%s targets %s, which is not a generated non-derived resource",
					referrer, field, referent,
				)
			}
			referentRoot, ok := options.Topology.ResourceRoots[referent]
			if !ok {
				bindingsFail(
					"cross-state reference %s.%s targets %s, which has no deployment root",
					referrer, field, referent,
				)
			}
			if referrerRoot == referentRoot {
				continue
			}
			edges = append(edges, CrossStateReferenceEdge{
				Field: field, Referrer: referrer, ReferrerRoot: referrerRoot,
				Referent: referent, ReferentRoot: referentRoot,
			})
			addToSet(dependenciesByRoot, referrerRoot, referentRoot)
			addToSet(outputsByRoot, referentRoot, referent)
		}
	}

	if cycle := cyclePathAcrossRoots(dependenciesByRoot); cycle != nil {
		bindingsFail(
			"cross-state reference cycle detected: %s; resolve one direction via a literal ID or operator expression",
			strings.Join(cycle, " -> "),
		)
	}
	sortCrossStateEdges(edges)
	return CrossStateReferenceTopology{Edges: edges, DependenciesByRoot: dependenciesByRoot, OutputsByRoot: outputsByRoot}
}

// ResolveCrossStateReferenceTopology ports crossStateReferenceTopology from
// the original implementation. Named ResolveCrossStateReferenceTopology
// rather than CrossStateReferenceTopology (which the CrossStateReferenceTopology
// struct type above already claims) since Go, unlike TypeScript, does not
// allow a function and a type to share one exported name in the same
// package -- the same naming split go/internal/roots/roots.go's
// RootTopologyFromResourceSet/LoadedRootTopology already applies to its own
// RootTopology type/function pair.
func ResolveCrossStateReferenceTopology(options CrossStateReferenceTopologyOptions) (result CrossStateReferenceTopology, err error) {
	defer recoverBindingsError(&err)
	return crossStateReferenceTopology(options), nil
}
