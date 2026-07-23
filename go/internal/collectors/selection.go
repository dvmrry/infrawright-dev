package collectors

import (
	"fmt"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// selection.go ports node-src/collectors/selection.ts: expanding collector
// selectors (product names, resource types, or derived-resource types)
// against the active pack registry -- the original resource authority, not
// anything the fetch engine derives on its own.

// hasFetchEntry reports whether resource declares a fetch entry, i.e. its
// registry metadata's "fetch" key is a JSON object. Ports the
// `isObject(resource.registry.fetch)` filter that both fetchResourceTypes
// and fetchProducts apply in node-src/collectors/selection.ts.
func hasFetchEntry(resource metadata.LoadedResourceMetadata) bool {
	return canonjson.IsJSONRecord(resource.Registry["fetch"])
}

// fetchResourceTypes ports the unexported fetchResourceTypes from
// node-src/collectors/selection.ts.
func fetchResourceTypes(root metadata.LoadedPackRoot) []string {
	types := make([]string, 0, len(root.Resources))
	for _, resource := range root.Resources {
		if hasFetchEntry(resource) {
			types = append(types, resource.Type)
		}
	}
	return canonjson.SortedStrings(types)
}

// FetchProducts ports fetchProducts from node-src/collectors/selection.ts:
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
// accepts in node-src/collectors/selection.ts.
type SelectFetchResourcesOptions struct {
	Root      metadata.LoadedPackRoot
	Selectors []string
}

// SelectFetchResources ports selectFetchResources from
// node-src/collectors/selection.ts: expand collector selectors using the
// original registry as the only resource authority. Product selectors
// expand to all of that product's fetch entries; derived resources select
// their fetch-bearing source.
func SelectFetchResources(options SelectFetchResourcesOptions) ([]string, error) {
	fetchable := fetchResourceTypes(options.Root)
	if len(options.Selectors) == 0 {
		return fetchable, nil
	}

	fetchableSet := toSet(fetchable)
	products := FetchProducts(options.Root)
	productSet := toSet(products)
	selected := make(map[string]struct{})
	unknown := make(map[string]struct{})

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
		unknown[selector] = struct{}{}
	}

	if len(unknown) > 0 {
		return nil, fmt.Errorf(
			"unknown resource type(s)/product(s): %s\nvalid products: %s\nvalid resources: %s",
			strings.Join(canonjson.SortedStrings(setKeys(unknown)), ", "),
			strings.Join(products, ", "),
			strings.Join(fetchable, ", "),
		)
	}
	return canonjson.SortedStrings(setKeys(selected)), nil
}
