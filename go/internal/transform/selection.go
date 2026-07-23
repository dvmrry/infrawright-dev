package transform

// selection.go ports the original implementation: resource
// selection and fail-closed referent-first reference ordering over the merged
// `references` tables from active pack manifests.

import (
	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
)

// TransformSelection ports the exported TransformSelection interface from
// the original implementation.
type TransformSelection struct {
	ResourceTypes []string
	// Notes is retained for API stability. It is always empty because a
	// reference cycle now fails closed instead of producing a recovery note.
	Notes []string
}

// MergedTransformReferences ports the exported mergedTransformReferences
// from the original implementation. root.Packs.Manifests is
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
// the original implementation. See MergedTransformReferences's
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
// the original implementation: graph[referent] is the set of
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

// referenceOrder is the unexported core of ReferenceOrder and
// SelectTransformResources. It retains the Node source's deterministic Kahn
// ordering for acyclic inputs, but deliberately fails closed if an unvalidated
// cyclic graph reaches this defensive boundary. Declared cycles are normally
// rejected earlier by metadata validation.
func referenceOrder(root metadata.LoadedPackRoot, resourceTypesInput []string) TransformSelection {
	uniqueSet := make(map[string]struct{}, len(resourceTypesInput))
	for _, resourceType := range resourceTypesInput {
		uniqueSet[resourceType] = struct{}{}
	}
	resourceTypes := canonjson.SortedStrings(mapKeys(uniqueSet))

	graph, indegree := referenceGraph(root, resourceTypes)

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
		if len(ready) == 0 {
			fail("reference order cycle detected; resolve one direction via a literal ID or operator expression")
		}
		resourceType := ready[0]
		ready = ready[1:]
		if _, ok := remaining[resourceType]; !ok {
			continue
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
	return TransformSelection{ResourceTypes: ordered, Notes: []string{}}
}

// ReferenceOrder returns a deterministic referent-first ordering. It preserves
// the exported Node API shape while deliberately replacing Node's alphabetic
// cycle recovery with a fail-closed error; Notes is therefore always empty.
func ReferenceOrder(root metadata.LoadedPackRoot, resourceTypes []string) (result TransformSelection, err error) {
	defer recoverErr(&err)
	return referenceOrder(root, resourceTypes), nil
}

// SelectTransformResources ports the exported selectTransformResources from
// the original implementation: "Expand active generated
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
// the original implementation: "Resolve the pull filename stem
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
