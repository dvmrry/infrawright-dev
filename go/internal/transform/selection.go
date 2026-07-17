package transform

// selection.go ports node-src/domain/transform-selection.ts: resource
// selection and referent-first reference ordering (a Tarjan SCC over the
// merged `references` tables from active pack manifests).

import (
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
)

// TransformSelection ports the exported TransformSelection interface from
// node-src/domain/transform-selection.ts.
type TransformSelection struct {
	ResourceTypes []string
	Notes         []string
}

// MergedTransformReferences ports the exported mergedTransformReferences
// from node-src/domain/transform-selection.ts. root.Packs.Manifests is
// walked in its own (already deterministic) slice order -- unlike the
// per-manifest field walks below it, that outer order is load-bearing: a
// later active manifest's field for the same resourceType+field
// overwrites an earlier one's (see the "active pack reference tables merge
// with Python's later-field overwrite" test this function's Go port
// preserves). The per-manifest resourceType and field walks are plain,
// unordered Go map ranges because each assigns a distinct destination key
// (a resourceType, or a field within one resourceType's accumulating
// target map) independent of visitation order -- unlike the
// Object.entries(...) walks this package sorts elsewhere in this file
// (see referenceGraph/referenceCycleMembers below), no two assignments in
// these inner loops ever target the same key within a single manifest, so
// there is nothing for iteration order to affect.
func MergedTransformReferences(root metadata.LoadedPackRoot) map[string]map[string]any {
	active := make(map[string]struct{}, len(root.Active.Packs))
	for _, name := range root.Active.Packs {
		active[name] = struct{}{}
	}
	output := make(map[string]map[string]any)
	for _, manifest := range root.Packs.Manifests {
		if _, isActive := active[manifest.Name]; !isActive {
			continue
		}
		references, isRecord := manifest.Data["references"].(map[string]any)
		if !isRecord {
			continue
		}
		for resourceType, fieldsValue := range references {
			fields, isRecord := fieldsValue.(map[string]any)
			if !isRecord {
				continue
			}
			target, exists := output[resourceType]
			if !exists {
				target = make(map[string]any, len(fields))
			}
			for field, reference := range fields {
				target[field] = reference
			}
			output[resourceType] = target
		}
	}
	return output
}

// MergedTransformLookupSources ports the exported
// mergedTransformLookupSources from
// node-src/domain/transform-selection.ts. See MergedTransformReferences's
// doc comment for the manifest-order/inner-walk-order reasoning, which
// applies identically here -- except this function replaces a
// resourceType's entire lookup source wholesale on each active manifest
// that defines one (not a per-field merge), so a later manifest's
// lookup_sources entry for the same resourceType fully supersedes an
// earlier one's.
func MergedTransformLookupSources(root metadata.LoadedPackRoot) map[string]map[string]any {
	active := make(map[string]struct{}, len(root.Active.Packs))
	for _, name := range root.Active.Packs {
		active[name] = struct{}{}
	}
	output := make(map[string]map[string]any)
	for _, manifest := range root.Packs.Manifests {
		if _, isActive := active[manifest.Name]; !isActive {
			continue
		}
		lookupSources, isRecord := manifest.Data["lookup_sources"].(map[string]any)
		if !isRecord {
			continue
		}
		for resourceType, sourceValue := range lookupSources {
			if source, isRecord := sourceValue.(map[string]any); isRecord {
				output[resourceType] = source
			}
		}
	}
	return output
}

// referenceGraph ports referenceGraph from
// node-src/domain/transform-selection.ts: graph[referent] is the set of
// referrers depending on referent (referent must be ordered before them),
// and indegree[referrer] counts how many unresolved in-selection referents
// it still has.
func referenceGraph(root metadata.LoadedPackRoot, resourceTypes []string) (map[string]map[string]struct{}, map[string]int) {
	selected := make(map[string]struct{}, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		selected[resourceType] = struct{}{}
	}
	graph := make(map[string]map[string]struct{}, len(selected))
	indegree := make(map[string]int, len(selected))
	for resourceType := range selected {
		graph[resourceType] = make(map[string]struct{})
		indegree[resourceType] = 0
	}
	references := MergedTransformReferences(root)
	for _, referrer := range canonjson.SortedStrings(mapKeys(selected)) {
		fields, ok := references[referrer]
		if !ok {
			continue
		}
		for _, field := range canonjson.SortedStrings(mapKeys(fields)) {
			specification, isRecord := fields[field].(map[string]any)
			if !isRecord {
				continue
			}
			referent, isString := specification["referent"].(string)
			if !isString {
				continue
			}
			if _, ok := selected[referent]; !ok {
				continue
			}
			children := graph[referent]
			if _, already := children[referrer]; already {
				continue
			}
			children[referrer] = struct{}{}
			indegree[referrer]++
		}
	}
	return graph, indegree
}

// referenceCycleMembers ports referenceCycleMembers from
// node-src/domain/transform-selection.ts: Tarjan's SCC algorithm restricted
// to nodes, returning every node that is a member of a cycle (an SCC with
// more than one member, or a single node with a self-loop).
func referenceCycleMembers(nodes []string, graph map[string]map[string]struct{}) []string {
	selected := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		selected[node] = struct{}{}
	}
	nextIndex := 0
	indexes := make(map[string]int)
	lowlinks := make(map[string]int)
	var stack []string
	onStack := make(map[string]struct{})
	cycleMembers := make(map[string]struct{})

	var visit func(node string)
	visit = func(node string) {
		index := nextIndex
		indexes[node] = index
		lowlinks[node] = index
		nextIndex++
		stack = append(stack, node)
		onStack[node] = struct{}{}

		for _, child := range canonjson.SortedStrings(mapKeys(graph[node])) {
			if _, ok := selected[child]; !ok {
				continue
			}
			if _, visited := indexes[child]; !visited {
				visit(child)
				if lowlinks[child] < lowlinks[node] {
					lowlinks[node] = lowlinks[child]
				}
			} else if _, onStk := onStack[child]; onStk {
				if indexes[child] < lowlinks[node] {
					lowlinks[node] = indexes[child]
				}
			}
		}

		if lowlinks[node] != indexes[node] {
			return
		}
		var component []string
		for len(stack) > 0 {
			child := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			delete(onStack, child)
			component = append(component, child)
			if child == node {
				break
			}
		}
		if len(component) > 1 {
			for _, member := range component {
				cycleMembers[member] = struct{}{}
			}
		} else if _, selfLoop := graph[node][node]; selfLoop {
			cycleMembers[node] = struct{}{}
		}
	}

	for _, node := range canonjson.SortedStrings(mapKeys(selected)) {
		if _, visited := indexes[node]; !visited {
			visit(node)
		}
	}
	return canonjson.SortedStrings(mapKeys(cycleMembers))
}

// referenceOrder is the unexported core of ReferenceOrder and
// SelectTransformResources, ported from referenceOrder in
// node-src/domain/transform-selection.ts.
func referenceOrder(root metadata.LoadedPackRoot, resourceTypesInput []string) TransformSelection {
	uniqueSet := make(map[string]struct{}, len(resourceTypesInput))
	for _, resourceType := range resourceTypesInput {
		uniqueSet[resourceType] = struct{}{}
	}
	resourceTypes := canonjson.SortedStrings(mapKeys(uniqueSet))

	graph, indegree := referenceGraph(root, resourceTypes)
	cycleMembers := referenceCycleMembers(resourceTypes, graph)
	notes := []string{}
	if len(cycleMembers) > 0 {
		notes = []string{
			"NOTE: reference order cycle detected among " + strings.Join(cycleMembers, ", ") + "; breaking alphabetically\n",
		}
	}

	remaining := make(map[string]struct{}, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		remaining[resourceType] = struct{}{}
	}
	var ready []string
	for _, resourceType := range resourceTypes {
		if indegree[resourceType] == 0 {
			ready = append(ready, resourceType)
		}
	}
	ordered := []string{}
	for len(remaining) > 0 {
		var resourceType string
		if len(ready) > 0 {
			candidate := ready[0]
			ready = ready[1:]
			if _, ok := remaining[candidate]; !ok {
				continue
			}
			resourceType = candidate
		} else {
			found := ""
			for _, member := range cycleMembers {
				if _, ok := remaining[member]; ok {
					found = member
					break
				}
			}
			if found == "" {
				sortedRemaining := canonjson.SortedStrings(mapKeys(remaining))
				if len(sortedRemaining) == 0 {
					fail("reference ordering lost all remaining nodes")
				}
				found = sortedRemaining[0]
			}
			resourceType = found
		}
		delete(remaining, resourceType)
		ordered = append(ordered, resourceType)
		for _, child := range canonjson.SortedStrings(mapKeys(graph[resourceType])) {
			next := indegree[child] - 1
			indegree[child] = next
			if next == 0 {
				if _, ok := remaining[child]; ok {
					ready = append(ready, child)
				}
			}
		}
		ready = canonjson.SortedStrings(ready)
	}
	return TransformSelection{ResourceTypes: ordered, Notes: notes}
}

// ReferenceOrder ports the exported referenceOrder from
// node-src/domain/transform-selection.ts: "Match engine.ops.reference_order
// without writing its cycle note to stderr."
func ReferenceOrder(root metadata.LoadedPackRoot, resourceTypes []string) (result TransformSelection, err error) {
	defer recoverErr(&err)
	return referenceOrder(root, resourceTypes), nil
}

// SelectTransformResources ports the exported selectTransformResources from
// node-src/domain/transform-selection.ts: "Expand active generated
// selectors, then order referents before referrers."
func SelectTransformResources(root metadata.LoadedPackRoot, selectors []string) (result TransformSelection, err error) {
	defer recoverErr(&err)
	resourceTypes, expandErr := roots.ExpandLoadedResources(root, selectors)
	if expandErr != nil {
		return TransformSelection{}, expandErr
	}
	return referenceOrder(root, resourceTypes), nil
}

// TransformSourceType ports the exported transformSourceType from
// node-src/domain/transform-selection.ts: "Resolve the pull filename stem
// consumed by one generated transform resource."
func TransformSourceType(root metadata.LoadedPackRoot, resourceType string) (source string, err error) {
	defer recoverErr(&err)
	resource, ok := root.Resources[resourceType]
	generate, _ := resource.Registry["generate"].(bool)
	if !ok || !generate {
		failf("unknown or non-generated transform resource %s", jsonQuote(resourceType))
	}
	if derive, isRecord := resource.Registry["derive"].(map[string]any); isRecord {
		if from, isString := derive["from"].(string); isString {
			return from, nil
		}
	}
	return resourceType, nil
}
