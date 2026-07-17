package collectors

import (
	"fmt"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
)

// authority.go ports node-src/collectors/authority.ts: resolving selected
// resource adapters through pack-owned provider sources. The pack root
// declares its provider source, while the caller chooses the concrete
// source-to-adapter bindings it is willing to execute. The adapter still
// owns authentication and URL composition; registry metadata can select
// only paths under that adapter's fixed provider host.

// CollectorAdapterAuthorities ports the CollectorAdapterAuthorities
// interface from node-src/collectors/authority.ts.
type CollectorAdapterAuthorities struct {
	ByProviderSource map[string]CollectorAdapter
}

// ResolveCollectorAdaptersOptions ports the options bag
// resolveCollectorAdapters accepts in node-src/collectors/authority.ts.
type ResolveCollectorAdaptersOptions struct {
	Authorities   CollectorAdapterAuthorities
	ResourceTypes []string
	Root          metadata.LoadedPackRoot
}

// manifestNamed returns the manifest named name from manifests, ported
// from the `manifest.find((item) => item.name === owner)` lookup in
// node-src/collectors/authority.ts.
func manifestNamed(manifests []metadata.PackManifest, name string) (metadata.PackManifest, bool) {
	for _, manifest := range manifests {
		if manifest.Name == name {
			return manifest, true
		}
	}
	return metadata.PackManifest{}, false
}

// ResolveCollectorAdapters ports resolveCollectorAdapters from
// node-src/collectors/authority.ts.
func ResolveCollectorAdapters(options ResolveCollectorAdaptersOptions) (map[string]CollectorAdapter, error) {
	selected := make(map[string]CollectorAdapter)
	sourcesByProduct := make(map[string]string)
	for _, resourceType := range canonjson.SortedStrings(options.ResourceTypes) {
		resource, ok := options.Root.Resources[resourceType]
		if !ok {
			return nil, fmt.Errorf("selected fetch resource %s is not active", jsonQuote(resourceType))
		}
		product, provider := resource.Product, resource.Provider
		owner, ok := options.Root.Packs.ProviderOwners[provider]
		if !ok {
			return nil, fmt.Errorf(
				"selected fetch resource %s uses provider %s without an owning pack",
				jsonQuote(resourceType), jsonQuote(provider),
			)
		}
		manifest, ok := manifestNamed(options.Root.Packs.Manifests, owner)
		if !ok {
			return nil, fmt.Errorf(
				"selected fetch resource %s uses provider %s without a loaded owning pack",
				jsonQuote(resourceType), jsonQuote(provider),
			)
		}
		providerSource, ok := manifest.ProviderSources[provider]
		if !ok {
			return nil, fmt.Errorf(
				"pack %s owns selected fetch resource %s through provider %s without a provider source",
				jsonQuote(owner), jsonQuote(resourceType), jsonQuote(provider),
			)
		}
		adapter, ok := options.Authorities.ByProviderSource[providerSource]
		if !ok {
			return nil, fmt.Errorf(
				"selected fetch resource %s uses provider source %s and product %s, but a matching collector adapter is not available; use a caller with a matching injected Node adapter",
				jsonQuote(resourceType), jsonQuote(providerSource), jsonQuote(product),
			)
		}
		if adapter.Product != product {
			return nil, fmt.Errorf(
				"selected fetch resource %s declares product %s, but provider source %s is bound to collector product %s",
				jsonQuote(resourceType), jsonQuote(product), jsonQuote(providerSource), jsonQuote(adapter.Product),
			)
		}
		if priorSource, ok := sourcesByProduct[product]; ok && priorSource != providerSource {
			return nil, fmt.Errorf(
				"selected fetch product %s spans provider sources %s and %s; one product cannot borrow authority across providers",
				jsonQuote(product), jsonQuote(priorSource), jsonQuote(providerSource),
			)
		}
		sourcesByProduct[product] = providerSource
		selected[product] = adapter
	}
	return selected, nil
}
