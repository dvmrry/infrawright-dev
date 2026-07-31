package transformrun

// seams.go exposes the transform runner's pack-metadata and binding-context
// derivation to the other in-tree consumers that must reproduce transform's
// classifications exactly -- adoption (which binds real tenant data) and
// gen-env (which, since the generated-bindings cache became optional, derives
// the same bindings at render time). Every function here is a read-only
// delegation to the production runner's own helper: a second implementation
// of any of these decisions is precisely the two-halves disagreement the
// set-block field map exists to remove, so callers import this rather than
// reconstructing a BindingContext of their own.

import (
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// ShouldUnescapeForTransform exposes the ordinary transform runner's
// manifest-derived HTML-unescape decision to authoring diagnostics. Keeping
// this as a read-only seam prevents a second interpretation of
// unescape_products outside the production runner.
func ShouldUnescapeForTransform(root metadata.LoadedPackRoot, resourceType string) bool {
	return shouldUnescape(root, resourceType)
}

// TransformReferenceSpecs exposes transformReferenceSpecs without
// duplicating transform-runner metadata semantics: the merged pack
// references for one resource type, narrowed to the entries carrying both a
// referent and a name field.
func TransformReferenceSpecs(
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
) map[string]tfrender.TransformReferenceSpec {
	return transformReferenceSpecs(root, resource)
}

// TransformLookupNameFieldForAdopt exposes transformLookupNameField to the
// adoption runner without creating a second lookup-lifecycle implementation.
func TransformLookupNameFieldForAdopt(
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
	dep deployment.Deployment,
) (*string, error) {
	return transformLookupNameField(root, resource, dep)
}

// TransformHasInferredLookupLifecycleForAdopt exposes the ordinary transform
// runner's inferred-lookup lifecycle decision to adoption.
func TransformHasInferredLookupLifecycleForAdopt(
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
) bool {
	return transformHasInferredLookupLifecycle(root, resource)
}

// TransformBindingContext exposes the ordinary transform runner's
// binding-context derivation to adoption and to render-time derivation. The
// schema carries the set-block field map, so both callers classify block
// nesting exactly as the transform runner does -- gen-env deriving a
// different set-block index than transform would silently change which leaf
// a binding lands on, which is the whole reason this is one implementation.
func TransformBindingContext(
	dep deployment.Deployment,
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
	resourceRoots map[string]string,
	references map[string]tfrender.TransformReferenceSpec,
	schema metadata.JsonObject,
) (tfrender.BindingContext, error) {
	return transformBindingContext(dep, root, resource, resourceRoots, references, schema)
}
