package metadata

// loader.go resolves a pack root plus an optional exact profile into a fully
// validated pack universe: pack metadata, registry, overrides, and the
// per-resource-type view consumed by the runtime. See packs.go's file doc
// comment for this package's exported-wrapper/unexported-implementation
// convention.
//
// providerSchemaCache (below) is a Go-only addition with no Node
// counterpart: the original implementation delegates every method call to
// loadProviderSchema/loadResourceSchema, whose readJson path re-reads and
// re-parses the provider schema on every lookup. This port's callers
// (modulesgen.buildModuleContext,
// adopt.ProjectProviderState) call LoadedPackRoot.LoadResourceSchema once
// per module and once per adopted object respectively, so without a cache
// a large adoption run re-reads and re-decodes the same handful of
// provider schema JSON files thousands of times. See each type's doc
// comment for the caching contract.
import (
	"fmt"
	"sync"
)

// LoadedResourceMetadata ports the LoadedResourceMetadata interface from
// the original implementation. Pack is nil when no manifest declares the
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
// the original implementation. Profile is nil when loadPackRoot was called
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

	// schemaCache backs LoadProviderSchema/LoadResourceSchema with a lazy,
	// per-provider memo scoped to this LoadedPackRoot (see
	// providerSchemaCache's doc comment). It is a pointer specifically so
	// that copying a LoadedPackRoot by value -- which happens throughout
	// this codebase, e.g. every time the LoadedPackRoot a single
	// loadPackRoot call produced is assigned into another struct's
	// LoadedPackRoot-typed field, or into a *LoadedPackRoot-typed field via
	// &root -- still shares one cache with the original: only a fresh call
	// to loadPackRoot starts a new cache. (A zero-value LoadedPackRoot built
	// directly by a test, bypassing loadPackRoot, leaves this nil; every
	// method below falls back to the uncached package-level function in
	// that case, so correctness never depends on how the root was built.)
	// It is never a package-level variable, so two independently loaded
	// pack roots never share entries, and a root's cache is freed with it.
	schemaCache *providerSchemaCache
}

// providerSchemaCache memoizes loadProviderSchema's result per provider
// name for one LoadedPackRoot, so that a run touching the same provider's
// schema many times (once per generated module, once per adopted object)
// re-reads and re-decodes the underlying schema JSON at most once per
// provider rather than once per call.
//
// Concurrency: cache population uses one sync.Once per provider key, so
// concurrent callers requesting different providers proceed in parallel,
// concurrent callers requesting the same provider block on the same
// in-flight load, and every caller past the first observes the exact
// (ProviderSchema, error) pair the first caller computed -- including a
// cached failure, which is safe here only because loadProviderSchema's
// outcome is a pure function of the immutable schema file on disk within
// one process's lifetime (see LoadProviderSchema's own doc comment for
// why a cached error is not a behavior change). mu only ever guards
// inserting a new *schemaCacheEntry into entries, never the load itself.
//
// Immutability: the cached ProviderSchema (and every JsonObject reachable
// through its Data/ResourceSchemas fields) must never be mutated by a
// caller, since every caller after the first receives the identical
// value, not a copy. This is not merely assumed: every call site reachable
// from LoadResourceSchema/LoadProviderSchema was audited (resources.go,
// terraformschema.go, modulesgen/generator.go, adopt/state_project.go,
// adopt/generated_config_schema.go, transformrun/runner.go,
// envgen/environment_generator.go, envgen/expression_bindings.go,
// transform/kernel.go) and every one of them only reads through the
// schema (classifying attributes, walking blocks, building separate
// output structures) -- none assigns into a schema-derived map or slice.
type providerSchemaCache struct {
	mu      sync.Mutex
	entries map[string]*schemaCacheEntry
}

// schemaCacheEntry holds one provider's memoized load: sync.Once ensures
// the closure passed to once.Do runs at most once no matter how many
// goroutines call it concurrently (Go's sync.Once marks itself done even
// if the wrapped function panics, but the closure here calls the exported
// LoadProviderSchema, which already recovers its own panics into a
// returned error -- see recoverMetadataError -- so no panic ever reaches
// once.Do in practice).
type schemaCacheEntry struct {
	once   sync.Once
	schema ProviderSchema
	err    error
}

func newProviderSchemaCache() *providerSchemaCache {
	return &providerSchemaCache{entries: make(map[string]*schemaCacheEntry)}
}

// load returns provider's schema, computing and memoizing it on the first
// call for that provider (per this cache instance) and returning the
// memoized (schema, err) pair -- including a memoized error -- on every
// subsequent call.
func (c *providerSchemaCache) load(metadata PackMetadata, provider string) (ProviderSchema, error) {
	c.mu.Lock()
	entry, ok := c.entries[provider]
	if !ok {
		entry = &schemaCacheEntry{}
		c.entries[provider] = entry
	}
	c.mu.Unlock()

	entry.once.Do(func() {
		entry.schema, entry.err = LoadProviderSchema(metadata, provider)
	})
	return entry.schema, entry.err
}

// LoadProviderSchema ports the loadProviderSchema method the Node source's
// loadPackRoot attaches to its returned LoadedPackRoot, additionally
// memoizing the result per provider for this root's lifetime (see
// providerSchemaCache). root.schemaCache is nil only for a LoadedPackRoot
// built directly (e.g. a test constructing the struct literal rather than
// calling LoadPackRoot), in which case this falls back to the uncached
// package-level call so correctness never depends on how the root was
// constructed.
func (root *LoadedPackRoot) LoadProviderSchema(provider string) (ProviderSchema, error) {
	if root.schemaCache == nil {
		return LoadProviderSchema(root.Packs, provider)
	}
	return root.schemaCache.load(root.Packs, provider)
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
// loadPackRoot attaches to its returned LoadedPackRoot. This is deliberately
// not a thin call to the package-level LoadResourceSchema function (unlike
// LoadResourceMainOverride above): that function calls the unexported,
// uncached loadProviderSchema directly, which would bypass this root's
// schemaCache entirely. Routing through root.LoadProviderSchema instead
// means every caller of this method -- modulesgen.buildModuleContext,
// adopt.ProjectProviderState, adopt's generated-config policy checks,
// transformrun's per-resource-type transform, and envgen's expression-
// binding validation -- gets the memoized schema for free, with no change
// at any of those call sites.
//
// The "not in schema" failure below still goes through failf plus this
// method's own deferred recoverMetadataError, exactly like
// loadResourceSchema's original panic/recover round-trip (see
// resources.go), rather than a bare fmt.Errorf: at least one caller
// (assessment.runnerSafeFailure, go/internal/assessment/runner.go)
// type-switches on *metadata.MetadataError specifically to classify a
// failure as INVALID_ASSESSMENT_INPUT/CategoryRequest, so this error's
// concrete type is part of its observable behavior, not just its message
// text. schema.ResourceSchemas lookups from the provider-schema cache never
// panic on their own, so recoverMetadataError here only ever fires for this
// method's own failf call.
func (root *LoadedPackRoot) LoadResourceSchema(resourceType string) (schema JsonObject, err error) {
	defer recoverMetadataError(&err)
	provider := ProviderForResource(root.Packs, resourceType)
	providerSchema, loadErr := root.LoadProviderSchema(provider)
	if loadErr != nil {
		return nil, loadErr
	}
	resource, ok := providerSchema.ResourceSchemas[resourceType]
	if !ok {
		failf("resource type %s not in %s schema", jsonQuote(resourceType), provider)
	}
	return resource, nil
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

// LoadPackRootOptions selects the pack root and, when provided, the exact
// profile that the installed root must match.
type LoadPackRootOptions struct {
	PacksRoot   string
	ProfilePath *string
}

// loadPackRoot ports loadPackRoot from the original implementation.
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
		Root:        metadata.Root,
		Profile:     profile,
		Active:      active,
		Packs:       metadata,
		Registry:    registry,
		Overrides:   overrides,
		Resources:   resources,
		schemaCache: newProviderSchemaCache(),
	}
}

// LoadPackRoot ports loadPackRoot from the original implementation.
func LoadPackRoot(options LoadPackRootOptions) (root LoadedPackRoot, err error) {
	defer recoverMetadataError(&err)
	return loadPackRoot(options), nil
}
