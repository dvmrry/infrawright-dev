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

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/refedges"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
)

// InfrawrightReferenceOutput names the engine-owned cross-state ID output
// generated roots publish (it ports INFRAWRIGHT_REFERENCE_OUTPUT from the
// original implementation, renamed to the iw_ prefix).
// LegacyInfrawrightReferenceOutput is the pre-rename spelling, still
// present in states applied before the rename and inside committed
// generated-bindings caches; readers accept both names, and only
// generation emits the current one.
const InfrawrightReferenceOutput = "iw_reference_ids"

// LegacyInfrawrightReferenceOutput is accepted wherever already-written
// artifacts are read; see InfrawrightReferenceOutput.
const LegacyInfrawrightReferenceOutput = "infrawright_reference_ids"

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

// dataReferent reports the registry marker used by environment-generation
// paths outside the cross-state topology walk.
func dataReferent(root metadata.LoadedPackRoot, resourceType string) bool {
	resource, ok := root.Resources[resourceType]
	if !ok {
		return false
	}
	dataOnly, _ := resource.Registry["data_referent"].(bool)
	return dataOnly
}

// addToSet is shared by the expression-binding graph helpers in
// environment_generator.go; cross-state edge qualification uses refedges.
func addToSet(values map[string]map[string]bool, key, value string) {
	set, ok := values[key]
	if !ok {
		set = map[string]bool{}
		values[key] = set
	}
	set[value] = true
}

func indexOfString(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
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

// CrossStateReferenceTopologyOptions bundles CrossStateReferenceTopology's
// parameters, the Go analogue of the inline options-object parameter type
// the original implementation's crossStateReferenceTopology
// accepts.
type CrossStateReferenceTopologyOptions struct {
	Deployment deployment.Deployment
	Root       metadata.LoadedPackRoot
	Topology   roots.RootTopology
}

// crossStateReferenceTopology ports the exported crossStateReferenceTopology
// from the original implementation: "Resolve the pack-declared
// edges that cross deployment state boundaries."
func crossStateReferenceTopology(options CrossStateReferenceTopologyOptions) CrossStateReferenceTopology {
	resolved, err := refedges.Resolve(refedges.Options{
		Deployment:    options.Deployment,
		Root:          options.Root,
		ResourceRoots: options.Topology.ResourceRoots,
	})
	if err != nil {
		// Keep envgen's existing panic/recover error surface and exact refusal
		// strings while the neutral package owns the qualification walk.
		bindingsFail("%s", err.Error())
	}
	edges := make([]CrossStateReferenceEdge, len(resolved.Edges))
	for index, edge := range resolved.Edges {
		edges[index] = CrossStateReferenceEdge{
			Field:        edge.Field,
			Referrer:     edge.Referrer,
			ReferrerRoot: edge.ReferrerRoot,
			Referent:     edge.Referent,
			ReferentRoot: edge.ReferentRoot,
		}
	}
	return CrossStateReferenceTopology{
		Edges:              edges,
		DependenciesByRoot: resolved.DependenciesByRoot,
		OutputsByRoot:      resolved.OutputsByRoot,
	}
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
