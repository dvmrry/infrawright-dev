package metadata

// loader.go ports node-src/metadata/loader.ts: loadPackRoot, which resolves
// a pack root (plus optional profile/catalog) into a fully validated pack
// universe -- pack metadata, registry, overrides, and the per-resource-type
// view root-catalog.go's renderer builds on. See packs.go's file doc
// comment for this package's exported-wrapper/unexported-implementation
// convention.

import "fmt"

// LoadedResourceMetadata ports the LoadedResourceMetadata interface from
// node-src/metadata/loader.ts. Pack is nil when no manifest declares the
// resource's provider (matching the Node source's `pack: string | null`).
type LoadedResourceMetadata struct {
	Type     string
	Product  string
	Provider string
	Pack     *string
	Registry JsonObject
	Override JsonObject // nil when no override document exists for this resource type
}

// LoadedPackRoot ports the LoadedPackRoot interface from
// node-src/metadata/loader.ts. Profile is nil when loadPackRoot was called
// without a profile path (matching the Node source's `profile:
// PackSetDocument | null`). Where the Node interface carries
// loadProviderSchema/loadResourceMainOverride/loadResourceSchema as
// closure-valued fields, this type exposes the same operations as methods
// on *LoadedPackRoot instead.
type LoadedPackRoot struct {
	Root      string
	Profile   *PackSetDocument
	Active    PackSelection
	Packs     PackMetadata
	Registry  LoadedRegistry
	Overrides LoadedOverrides
	Resources map[string]LoadedResourceMetadata
}

// LoadProviderSchema ports the loadProviderSchema method the Node source's
// loadPackRoot attaches to its returned LoadedPackRoot.
func (root *LoadedPackRoot) LoadProviderSchema(provider string) (ProviderSchema, error) {
	return LoadProviderSchema(root.Packs, provider)
}

// LoadResourceMainOverride ports the loadResourceMainOverride method the
// Node source's loadPackRoot attaches to its returned LoadedPackRoot,
// including its unknown-resource-type guard (a TypeError in the Node
// source, an ordinary error here).
func (root *LoadedPackRoot) LoadResourceMainOverride(resourceType string) (*string, error) {
	if _, ok := root.Resources[resourceType]; !ok {
		return nil, fmt.Errorf("unknown active resource type %s", jsonQuote(resourceType))
	}
	return LoadResourceMainOverride(root.Packs, resourceType)
}

// LoadResourceSchema ports the loadResourceSchema method the Node source's
// loadPackRoot attaches to its returned LoadedPackRoot.
func (root *LoadedPackRoot) LoadResourceSchema(resourceType string) (JsonObject, error) {
	return LoadResourceSchema(root.Packs, resourceType)
}

func ownerForProvider(metadata PackMetadata, provider string) (PackManifest, bool) {
	owner, ok := metadata.ProviderOwners[provider]
	if !ok {
		return PackManifest{}, false
	}
	for _, manifest := range metadata.Manifests {
		if manifest.Name == owner {
			return manifest, true
		}
	}
	return PackManifest{}, false
}

func resourceMap(metadata PackMetadata, registry LoadedRegistry, overrides LoadedOverrides) map[string]LoadedResourceMetadata {
	resources := make(map[string]LoadedResourceMetadata, len(registry.Entries))
	for resourceType, entry := range registry.Entries {
		provider := ProviderForResource(metadata, resourceType)
		product, ok := entry["product"].(string)
		if !ok {
			// Unreachable given validateRegistry's prior validation
			// (product is always a required non-empty string by the time
			// an entry reaches here); ported as a fail() rather than the
			// Node source's bare `throw new TypeError(...)` purely so it
			// composes with this package's panic/recover convention (see
			// validation.go) instead of crashing the process outright.
			failf("%s registry product is not a string", resourceType)
		}
		var pack *string
		if manifest, found := ownerForProvider(metadata, provider); found {
			name := manifest.Name
			pack = &name
		}
		var override JsonObject
		if o, hasOverride := overrides.Entries[resourceType]; hasOverride {
			override = o
		}
		resources[resourceType] = LoadedResourceMetadata{
			Type:     resourceType,
			Product:  product,
			Provider: provider,
			Pack:     pack,
			Registry: entry,
			Override: override,
		}
	}
	return resources
}

// LoadPackRootOptions ports the options bag loadPackRoot accepts in
// node-src/metadata/loader.ts. ProfilePath nil means the optional
// `profilePath` field was omitted (load every manifest under PacksRoot with
// no profile/catalog cross-check); CatalogPath nil means `catalogPath` was
// omitted (only meaningful when ProfilePath is set).
type LoadPackRootOptions struct {
	PacksRoot   string
	ProfilePath *string
	CatalogPath *string
}

// loadPackRoot ports loadPackRoot from node-src/metadata/loader.ts.
func loadPackRoot(options LoadPackRootOptions) LoadedPackRoot {
	var metadata PackMetadata
	var profile *PackSetDocument
	var active PackSelection
	if options.ProfilePath == nil {
		metadata = loadPackMetadata(options.PacksRoot)
		active = activePackSelection(options.PacksRoot)
	} else {
		result := validateActivePackSet(ValidateActivePackSetOptions{
			ProfilePath: *options.ProfilePath,
			Root:        options.PacksRoot,
			CatalogPath: options.CatalogPath,
		})
		metadata = result.Metadata
		profileValue := result.Profile
		profile = &profileValue
		active = result.Active
	}
	registry := loadRegistry(metadata, active.Packs)
	overrides := loadOverrides(metadata, active.Packs)
	validateUnsupportedProviderScopes(metadata, registry)
	resources := resourceMap(metadata, registry, overrides)
	return LoadedPackRoot{
		Root:      metadata.Root,
		Profile:   profile,
		Active:    active,
		Packs:     metadata,
		Registry:  registry,
		Overrides: overrides,
		Resources: resources,
	}
}

// LoadPackRoot ports loadPackRoot from node-src/metadata/loader.ts.
func LoadPackRoot(options LoadPackRootOptions) (root LoadedPackRoot, err error) {
	defer recoverMetadataError(&err)
	return loadPackRoot(options), nil
}
