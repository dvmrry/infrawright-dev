package collectors

import (
	"fmt"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// selection.go ports the original implementation: expanding collector
// selectors (product names, resource types, or derived-resource types)
// against the active pack registry -- the original resource authority, not
// anything the fetch engine derives on its own.

// hasFetchEntry reports whether resource declares a fetch entry, i.e. its
// registry metadata's "fetch" key is a JSON object. Ports the
// `isObject(resource.registry.fetch)` filter that both fetchResourceTypes
// and fetchProducts apply in the original implementation.
func hasFetchEntry(resource metadata.LoadedResourceMetadata) bool {
	return canonjson.IsJSONRecord(resource.Registry["fetch"])
}

// fetchResourceTypes ports the unexported fetchResourceTypes from
// the original implementation.
func fetchResourceTypes(root metadata.LoadedPackRoot) []string {
	types := make([]string, 0, len(root.Resources))
	for _, resource := range root.Resources {
		if hasFetchEntry(resource) {
			types = append(types, resource.Type)
		}
	}
	return canonjson.SortedStrings(types)
}

// FetchProducts ports fetchProducts from the original implementation:
// the active product names that own at least one fetch entry.
func FetchProducts(root metadata.LoadedPackRoot) []string {
	seen := make(map[string]struct{})
	for _, resource := range root.Resources {
		if hasFetchEntry(resource) {
			seen[resource.Product] = struct{}{}
		}
	}
	return canonjson.SortedStrings(setKeys(seen))
}

// SelectFetchResourcesOptions ports the options bag selectFetchResources
// accepts in the original implementation.
type SelectFetchResourcesOptions struct {
	Root      metadata.LoadedPackRoot
	Selectors []string
}

// SkippedFetchSelector is a selector the registry declares unfetchable on
// purpose, with the reason the registry gives.
type SkippedFetchSelector struct {
	Type   string
	Reason string
}

// SelectFetchResources ports selectFetchResources from
// the original implementation: expand collector selectors using the
// original registry as the only resource authority. Product selectors
// expand to all of that product's fetch entries; derived resources select
// their fetch-bearing source.
func SelectFetchResources(options SelectFetchResourcesOptions) ([]string, error) {
	selected, _, err := SelectFetchResourcesWithSkips(options)
	return selected, err
}

// SelectFetchResourcesWithSkips additionally reports the selectors the
// registry declares unfetchable on purpose.
//
// A registry entry may say "fetch": false with a fetch_skip_reason, which is
// how a type states it has no API object to pull. Selection used to route
// those to the unknown-selector refusal, so asking for one produced "unknown
// resource type" -- untrue, since the type is known and its entry says exactly
// why it cannot be fetched. Declaring the fact and then ignoring it leaves the
// declaration decorative: whoever writes the reason still cannot fetch a
// product without hitting a refusal that does not mention it.
func SelectFetchResourcesWithSkips(
	options SelectFetchResourcesOptions,
) ([]string, []SkippedFetchSelector, error) {
	fetchable := fetchResourceTypes(options.Root)
	if len(options.Selectors) == 0 {
		return fetchable, []SkippedFetchSelector{}, nil
	}

	fetchableSet := toSet(fetchable)
	products := FetchProducts(options.Root)
	productSet := toSet(products)
	selected := make(map[string]struct{})
	unknown := make(map[string]struct{})
	skipped := make(map[string]string)

	for _, selector := range options.Selectors {
		if _, ok := productSet[selector]; ok {
			for _, resource := range options.Root.Resources {
				if resource.Product == selector && hasFetchEntry(resource) {
					selected[resource.Type] = struct{}{}
				}
			}
			continue
		}

		if _, ok := fetchableSet[selector]; ok {
			selected[selector] = struct{}{}
			continue
		}

		if resource, ok := options.Root.Resources[selector]; ok {
			if derive, ok := resource.Registry["derive"].(map[string]any); ok {
				if from, ok := derive["from"].(string); ok {
					if _, ok := fetchableSet[from]; ok {
						selected[from] = struct{}{}
					} else {
						unknown[from] = struct{}{}
					}
					continue
				}
			}
		}
		if reason, declared := declaredUnfetchable(options.Root, selector); declared {
			skipped[selector] = reason
			continue
		}
		unknown[selector] = struct{}{}
	}

	if len(unknown) > 0 {
		return nil, nil, fmt.Errorf(
			"unknown resource type(s)/product(s): %s\nvalid products: %s\nvalid resources: %s",
			strings.Join(canonjson.SortedStrings(setKeys(unknown)), ", "),
			strings.Join(products, ", "),
			strings.Join(fetchable, ", "),
		)
	}
	skippedSelectors := make([]SkippedFetchSelector, 0, len(skipped))
	skippedNames := make([]string, 0, len(skipped))
	for selector := range skipped {
		skippedNames = append(skippedNames, selector)
	}
	for _, selector := range canonjson.SortedStrings(skippedNames) {
		skippedSelectors = append(skippedSelectors, SkippedFetchSelector{
			Type: selector, Reason: skipped[selector],
		})
	}
	return canonjson.SortedStrings(setKeys(selected)), skippedSelectors, nil
}

// declaredUnfetchable reports the reason a registry entry gives for having no
// fetch block. Only an explicit "fetch": false with a reason counts: an entry
// that merely lacks a fetch block is the silent gap check-config exists to
// refuse, and must keep reaching the unknown-selector error rather than being
// quietly skipped here.
func declaredUnfetchable(root metadata.LoadedPackRoot, selector string) (string, bool) {
	resource, known := root.Resources[selector]
	if !known {
		return "", false
	}
	allowed, isBool := resource.Registry["fetch"].(bool)
	if !isBool || allowed {
		return "", false
	}
	reason, ok := resource.Registry["fetch_skip_reason"].(string)
	if !ok || reason == "" {
		return "", false
	}
	return reason, true
}
