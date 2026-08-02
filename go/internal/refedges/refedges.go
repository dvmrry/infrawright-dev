// Package refedges resolves the qualified cross-state reference edges shared
// by environment generation and plan-roots projection.
package refedges

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// Edge describes one qualified reference field whose referent is in another
// deployment root.
type Edge struct {
	Field        string
	Referrer     string
	ReferrerRoot string
	Referent     string
	ReferentRoot string
}

// Topology is the qualified cross-state reference graph. The two maps use
// presence-only sets, matching the representation used by envgen.
type Topology struct {
	Edges              []Edge
	DependenciesByRoot map[string]map[string]bool
	OutputsByRoot      map[string]map[string]bool
}

// Options supplies the loaded pack metadata, deployment binding policy, and
// resource-to-root mapping needed to qualify reference edges. The package is
// intentionally independent of roots and envgen so both callers consume the
// same walk without introducing an import cycle.
type Options struct {
	Deployment    deployment.Deployment
	Root          metadata.LoadedPackRoot
	ResourceRoots map[string]string
}

// Resolve returns the deterministic, fail-closed cross-state reference
// topology for options. Active manifests are merged in their loaded order;
// a later active manifest replaces an earlier declaration of the same
// referrer field.
func Resolve(options Options) (Topology, error) {
	edges := []Edge{}
	dependenciesByRoot := map[string]map[string]bool{}
	outputsByRoot := map[string]map[string]bool{}

	references := mergedReferences(options.Root)
	for _, referrer := range canonjson.SortedStrings(mapKeysOfReferences(references)) {
		referrerResource, ok := options.Root.Resources[referrer]
		if !ok {
			continue
		}
		if dataReferent(options.Root, referrer) {
			return Topology{}, fmt.Errorf(
				"cross-state reference referrer %s is a data referent and cannot declare references",
				referrer,
			)
		}
		if deployment.DeploymentReferenceBindingMode(options.Deployment, referrerResource.Provider) != deployment.ReferenceBindingCrossState {
			continue
		}
		if !generatedNonDerived(options.Root, referrer) {
			return Topology{}, fmt.Errorf(
				"cross-state reference referrer %s must be a generated non-derived resource",
				referrer,
			)
		}
		referrerRoot, ok := options.ResourceRoots[referrer]
		if !ok {
			return Topology{}, fmt.Errorf("cross-state reference referrer %s has no deployment root", referrer)
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
			if !generatedNonDerived(options.Root, referent) && !dataReferent(options.Root, referent) {
				return Topology{}, fmt.Errorf(
					"cross-state reference %s.%s targets %s, which is not a generated non-derived resource or data referent",
					referrer, field, referent,
				)
			}
			referentRoot, ok := options.ResourceRoots[referent]
			if !ok {
				return Topology{}, fmt.Errorf(
					"cross-state reference %s.%s targets %s, which has no deployment root",
					referrer, field, referent,
				)
			}
			if referrerRoot == referentRoot {
				continue
			}
			edges = append(edges, Edge{
				Field: field, Referrer: referrer, ReferrerRoot: referrerRoot,
				Referent: referent, ReferentRoot: referentRoot,
			})
			addToSet(dependenciesByRoot, referrerRoot, referentRoot)
			addToSet(outputsByRoot, referentRoot, referent)
		}
	}

	if cycle := cyclePath(dependenciesByRoot); cycle != nil {
		return Topology{}, fmt.Errorf(
			"cross-state reference cycle detected: %s; resolve one direction via a literal ID or operator expression",
			strings.Join(cycle, " -> "),
		)
	}
	sortEdges(edges)
	return Topology{
		Edges:              edges,
		DependenciesByRoot: dependenciesByRoot,
		OutputsByRoot:      outputsByRoot,
	}, nil
}

func mergedReferences(root metadata.LoadedPackRoot) map[string]map[string]any {
	active := make(map[string]struct{}, len(root.Active.Packs))
	for _, name := range root.Active.Packs {
		active[name] = struct{}{}
	}
	referencesByResource := make(map[string]map[string]any)
	for _, manifest := range root.Packs.Manifests {
		if _, ok := active[manifest.Name]; !ok {
			continue
		}
		references, ok := manifest.Data["references"].(map[string]any)
		if !ok {
			continue
		}
		for resourceType, fieldsValue := range references {
			fields, ok := fieldsValue.(map[string]any)
			if !ok {
				continue
			}
			target, ok := referencesByResource[resourceType]
			if !ok {
				target = make(map[string]any, len(fields))
			}
			for field, reference := range fields {
				target[field] = reference
			}
			referencesByResource[resourceType] = target
		}
	}
	return referencesByResource
}

func mapKeys(m map[string]any) []string {
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

func generatedNonDerived(root metadata.LoadedPackRoot, resourceType string) bool {
	resource, ok := root.Resources[resourceType]
	if !ok {
		return false
	}
	generated, _ := resource.Registry["generate"].(bool)
	if !generated {
		return false
	}
	return !canonjson.IsJSONRecord(resource.Registry["derive"])
}

func dataReferent(root metadata.LoadedPackRoot, resourceType string) bool {
	resource, ok := root.Resources[resourceType]
	if !ok {
		return false
	}
	dataOnly, _ := resource.Registry["data_referent"].(bool)
	return dataOnly
}

func addToSet(values map[string]map[string]bool, key, value string) {
	set, ok := values[key]
	if !ok {
		set = map[string]bool{}
		values[key] = set
	}
	set[value] = true
}

func cyclePath(dependencies map[string]map[string]bool) []string {
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

func mapKeysBoolSet(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func sortEdges(edges []Edge) {
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
