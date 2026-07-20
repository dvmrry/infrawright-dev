package transformrun

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

// TransformReferenceSpecsForAdopt exposes transformReferenceSpecs to the
// adoption runner without duplicating transform-runner metadata semantics.
func TransformReferenceSpecsForAdopt(
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

// TransformBindingContextForAdopt exposes the ordinary transform runner's
// binding-context derivation to adoption.
func TransformBindingContextForAdopt(
	dep deployment.Deployment,
	root metadata.LoadedPackRoot,
	resource metadata.LoadedResourceMetadata,
	resourceRoots map[string]string,
	references map[string]tfrender.TransformReferenceSpec,
) tfrender.BindingContext {
	return transformBindingContext(dep, root, resource, resourceRoots, references)
}
