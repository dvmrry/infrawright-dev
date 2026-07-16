package metadata

// rootcatalog.go ports node-src/metadata/root-catalog.ts:
// buildRootCatalog/renderRootCatalog -- provider selection, longest-prefix
// slug derivation, the tri-state slug_group field, the NUL-framed source
// digest, and canonical rendering via go/internal/canonjson.
//
// RootCatalog/RootCatalogResource are local Go structs ported minimally
// from the RootCatalog/RootCatalogResource interfaces in
// node-src/domain/types.ts (this package does not import that file, or
// build a general domain-types package, since these two shapes are all
// this port's scope needs from it); rootCatalogToValue below converts a
// built RootCatalog into a go/internal/canonjson.Value tree for rendering,
// which is the more faithful analogue of the Node source's own `as unknown
// as JsonValue` cast in renderRootCatalog.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
)

// RootCatalogResource ports the RootCatalogResource interface from
// node-src/domain/types.ts.
type RootCatalogResource struct {
	Type      string
	Product   string
	Provider  string
	BareName  string
	SlugLabel *string // nil renders as JSON null
	Generated bool
	Derived   bool
	// SlugGroup is nil when the source registry entry had no slug_group
	// key at all -- a genuine three-state field (absent / false / true)
	// matching the Node source's `slug_group?: boolean` optional property,
	// which resourceShape only ever sets when
	// Object.hasOwn(loaded.registry, "slug_group") is true. A nil
	// SlugGroup renders by omitting the "slug_group" key entirely (see
	// rootCatalogToValue), not by emitting a JSON null.
	SlugGroup *bool
}

// RootCatalog ports the RootCatalog interface from
// node-src/domain/types.ts.
type RootCatalog struct {
	Kind              string
	SchemaVersion     int
	DeclaredProviders []string
	Resources         []RootCatalogResource
	SourceFiles       []string
	SourcesSHA256     string
}

// selectedProviders ports selectedProviders from
// node-src/metadata/root-catalog.ts. An empty (or nil) requested list
// means "every provider declared by the loaded pack root", matching the
// Node source's `requested === undefined || requested.length === 0` check.
func selectedProviders(root LoadedPackRoot, requested []string) []string {
	declaredSet := make(map[string]struct{}, len(root.Packs.ProviderPrefixes))
	for _, provider := range root.Packs.ProviderPrefixes {
		declaredSet[provider] = struct{}{}
	}
	declared := canonjson.SortedStrings(setKeys(declaredSet))

	var selected []string
	if len(requested) == 0 {
		selected = declared
	} else {
		requestedSet := make(map[string]struct{}, len(requested))
		for _, provider := range requested {
			requestedSet[provider] = struct{}{}
		}
		selected = canonjson.SortedStrings(setKeys(requestedSet))
	}

	var unknown []string
	for _, provider := range selected {
		if _, ok := declaredSet[provider]; !ok {
			unknown = append(unknown, provider)
		}
	}
	if len(unknown) > 0 {
		failf("unknown provider(s): %s", strings.Join(unknown, ", "))
	}
	return selected
}

// matchingPrefix ports matchingPrefix from
// node-src/metadata/root-catalog.ts: the longest provider prefix owned by
// provider under which resourceType falls, or nil if none matches. Ties
// (multiple same-length candidate prefixes for the same provider) are
// broken alphabetically rather than by the Node source's Object.entries
// insertion order, since this package's map[string]string
// PackMetadata.ProviderPrefixes representation does not preserve that
// order; every committed pack declares exactly one prefix per provider, so
// this tie-break is unreachable in this port's gate.
func matchingPrefix(root LoadedPackRoot, resourceType, provider string) *string {
	var candidates []string
	for prefix, owner := range root.Packs.ProviderPrefixes {
		if owner == provider && strings.HasPrefix(resourceType, prefix) {
			candidates = append(candidates, prefix)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sorted := canonjson.SortedStrings(candidates)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	return &sorted[0]
}

// resourceShape ports resourceShape from
// node-src/metadata/root-catalog.ts.
func resourceShape(root LoadedPackRoot, resourceType string) RootCatalogResource {
	loaded, ok := root.Resources[resourceType]
	if !ok {
		failf("unknown active resource type %s", resourceType)
	}
	prefix := matchingPrefix(root, resourceType, loaded.Provider)

	var bareName string
	var slugLabel *string
	if prefix == nil {
		bareName = resourceType
	} else {
		bareName = resourceType[len(*prefix):]
		slugPart := bareName
		if index := strings.IndexByte(bareName, '_'); index >= 0 {
			slugPart = bareName[:index]
		}
		label := *prefix + slugPart
		slugLabel = &label
	}

	derived := false
	if deriveValue, hasDerive := loaded.Registry["derive"]; hasDerive && deriveValue != nil {
		derived = true
	}
	generated := false
	if g, ok := loaded.Registry["generate"].(bool); ok {
		generated = g
	}

	shape := RootCatalogResource{
		Type:      resourceType,
		Product:   loaded.Product,
		Provider:  loaded.Provider,
		BareName:  bareName,
		SlugLabel: slugLabel,
		Generated: generated,
		Derived:   derived,
	}
	if slugGroupValue, hasSlugGroup := loaded.Registry["slug_group"]; hasSlugGroup {
		isTrue := slugGroupValue == true
		shape.SlugGroup = &isTrue
	}
	return shape
}

// portableRelative ports portableRelative from
// node-src/metadata/root-catalog.ts: file's path relative to root, with
// forward slashes regardless of OS path separator.
func portableRelative(root, file string) string {
	relative, err := filepath.Rel(root, file)
	if err != nil {
		return file
	}
	return filepath.ToSlash(relative)
}

// sourceEvidence ports sourceEvidence from
// node-src/metadata/root-catalog.ts: the sorted, portable-relative list of
// manifest/registry files backing providers, and a SHA-256 digest framing
// each file's relative path and content with NUL bytes.
func sourceEvidence(root LoadedPackRoot, providers map[string]struct{}) ([]string, string) {
	var selectedPaths []string
	for _, manifest := range root.Packs.Manifests {
		owned := false
		for _, provider := range manifest.ProviderPrefixes {
			if _, ok := providers[provider]; ok {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		selectedPaths = append(selectedPaths, manifest.Path)
		registryPath := filepath.Join(manifest.Directory, "registry.json")
		if isFile(registryPath) {
			selectedPaths = append(selectedPaths, registryPath)
		}
	}
	sort.Slice(selectedPaths, func(i, j int) bool {
		return portableRelative(root.Root, selectedPaths[i]) < portableRelative(root.Root, selectedPaths[j])
	})

	hasher := sha256.New()
	files := make([]string, 0, len(selectedPaths))
	for _, file := range selectedPaths {
		relative := portableRelative(root.Root, file)
		content, err := os.ReadFile(file)
		if err != nil {
			failf("failed to read %s: %s", file, err.Error())
		}
		files = append(files, relative)
		hasher.Write([]byte(relative))
		hasher.Write([]byte{0})
		hasher.Write(content)
		hasher.Write([]byte{0})
	}
	return files, hex.EncodeToString(hasher.Sum(nil))
}

// buildRootCatalog ports buildRootCatalog from
// node-src/metadata/root-catalog.ts.
func buildRootCatalog(root LoadedPackRoot, requestedProviders []string) RootCatalog {
	providers := selectedProviders(root, requestedProviders)
	providerSet := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		providerSet[provider] = struct{}{}
	}

	resourceTypes := make([]string, 0, len(root.Resources))
	for resourceType := range root.Resources {
		resourceTypes = append(resourceTypes, resourceType)
	}
	resourceTypes = canonjson.SortedStrings(resourceTypes)

	resources := make([]RootCatalogResource, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		loaded, ok := root.Resources[resourceType]
		if !ok || !providerSetHas(providerSet, loaded.Provider) {
			continue
		}
		resources = append(resources, resourceShape(root, resourceType))
	}

	files, digest := sourceEvidence(root, providerSet)
	return RootCatalog{
		Kind:              "infrawright.root_catalog",
		SchemaVersion:     1,
		DeclaredProviders: providers,
		Resources:         resources,
		SourceFiles:       files,
		SourcesSHA256:     digest,
	}
}

func providerSetHas(set map[string]struct{}, provider string) bool {
	_, ok := set[provider]
	return ok
}

// BuildRootCatalog ports buildRootCatalog from
// node-src/metadata/root-catalog.ts.
func BuildRootCatalog(root LoadedPackRoot, requestedProviders []string) (catalog RootCatalog, err error) {
	defer recoverMetadataError(&err)
	return buildRootCatalog(root, requestedProviders), nil
}

// rootCatalogToValue converts a built RootCatalog into a
// go/internal/canonjson.Value tree for rendering -- the Go analogue of
// renderRootCatalog's `buildRootCatalog(...) as unknown as JsonValue` cast
// in node-src/metadata/root-catalog.ts, since this package represents a
// RootCatalog as a typed Go struct rather than a dynamic value tree.
func rootCatalogToValue(catalog RootCatalog) canonjson.Value {
	resources := make([]any, 0, len(catalog.Resources))
	for _, resource := range catalog.Resources {
		object := map[string]any{
			"bare_name": resource.BareName,
			"derived":   resource.Derived,
			"generated": resource.Generated,
			"product":   resource.Product,
			"provider":  resource.Provider,
			"type":      resource.Type,
		}
		if resource.SlugLabel != nil {
			object["slug_label"] = *resource.SlugLabel
		} else {
			object["slug_label"] = nil
		}
		if resource.SlugGroup != nil {
			object["slug_group"] = *resource.SlugGroup
		}
		resources = append(resources, object)
	}

	declaredProviders := make([]any, 0, len(catalog.DeclaredProviders))
	for _, provider := range catalog.DeclaredProviders {
		declaredProviders = append(declaredProviders, provider)
	}

	sourceFiles := make([]any, 0, len(catalog.SourceFiles))
	for _, file := range catalog.SourceFiles {
		sourceFiles = append(sourceFiles, file)
	}

	return map[string]any{
		"declared_providers": declaredProviders,
		"kind":               catalog.Kind,
		"resources":          resources,
		"schema_version":     float64(catalog.SchemaVersion),
		"source_files":       sourceFiles,
		"sources_sha256":     catalog.SourcesSHA256,
	}
}

// renderRootCatalog ports renderRootCatalog from
// node-src/metadata/root-catalog.ts.
func renderRootCatalog(root LoadedPackRoot, requestedProviders []string) string {
	catalog := buildRootCatalog(root, requestedProviders)
	rendered, err := canonjson.Render(rootCatalogToValue(catalog))
	if err != nil {
		failf("failed to render root catalog: %s", err.Error())
	}
	return rendered
}

// RenderRootCatalog ports renderRootCatalog from
// node-src/metadata/root-catalog.ts.
func RenderRootCatalog(root LoadedPackRoot, requestedProviders []string) (rendered string, err error) {
	defer recoverMetadataError(&err)
	return renderRootCatalog(root, requestedProviders), nil
}
