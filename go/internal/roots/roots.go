// Package roots derives singleton-state topology from loaded packs. Every
// generated resource type is one state unit whose label and only member are
// the resource type.
package roots

import (
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/posixpath"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// validTenant defines the accepted tenant path segment.
var validTenant = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// domainError panics with a *procerr.ProcessFailure carrying category
// "domain" and the given code (defaulting to "INVALID_ROOT_CONFIGURATION",
// the Go analogue of the original implementation's `code =
// "INVALID_ROOT_CONFIGURATION"` default parameter), typed there as
// returning `never` because it always throws. See recoverProcessFailure
// for how every exported entry point in this package converts the panic
// back into a normal error return.
func domainError(message string) {
	domainErrorCode(message, "INVALID_ROOT_CONFIGURATION")
}

func domainErrorCode(message, code string) {
	panic(procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: procerr.CategoryDomain,
		Message:  message,
	}))
}

// recoverProcessFailure is deferred by every exported entry point in this
// package (as `defer recoverProcessFailure(&err)`) to convert a recovered
// *procerr.ProcessFailure panic (see domainError) into a normal error
// return. Any other recovered value is re-panicked, since it indicates a
// genuine bug rather than an expected validation failure -- the same
// convention go/internal/deployment's recoverProcessFailure and
// go/internal/metadata's recoverMetadataError follow for their own panic
// types.
func recoverProcessFailure(err *error) {
	if r := recover(); r != nil {
		if pf, ok := r.(*procerr.ProcessFailure); ok {
			*err = pf
			return
		}
		panic(r)
	}
}

// validateTenant ports validateTenant from the original implementation,
// including its exact error text (asserted verbatim by this package's
// ported tests, per this port's validation-message-parity requirement).
func validateTenant(tenant string) {
	if !validTenant.MatchString(tenant) || tenant == "." || tenant == ".." {
		domainErrorCode(
			"TENANT must match [A-Za-z0-9_.-]+ and not be . or .. (got '"+tenant+"')",
			"INVALID_TENANT",
		)
	}
}

// ValidateTenant ports validateTenant from the original implementation.
func ValidateTenant(tenant string) (err error) {
	defer recoverProcessFailure(&err)
	validateTenant(tenant)
	return nil
}

// resourceIndex is the private, pre-computed lookup shape shared by the root
// resolver. It is built from the caller's in-memory ResourceSet or directly
// from the authoritative LoadedPackRoot; it is not a persisted catalog.
type resourceIndex struct {
	resources map[string]metadata.ResourceDescriptor
	generated map[string]struct{}
	providers map[string]struct{}
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

// indexResourceSet builds the root resolver's lookup shape from an in-memory
// resource set supplied by the caller.
func indexResourceSet(resourceSet metadata.ResourceSet) resourceIndex {
	resources := make(map[string]metadata.ResourceDescriptor, len(resourceSet.Resources))
	for _, resource := range resourceSet.Resources {
		resources[resource.Type] = resource
	}
	var generated []string
	for _, resource := range resourceSet.Resources {
		if !resource.Generated {
			continue
		}
		generated = append(generated, resource.Type)
	}
	return resourceIndex{
		resources: resources,
		generated: stringSet(generated),
		providers: stringSet(resourceSet.DeclaredProviders),
	}
}

// isJSObjectLike reports whether value is the sort of JSON value JS's
// `typeof value === "object" && value !== null` test accepts: a JSON
// object OR a JSON array (JS's typeof does not distinguish them; only the
// explicit `!== null` check in the ported source excludes JSON null,
// separately handled below). loadedResourceShape's `derived` computation
// below intentionally preserves this JS quirk (an array-valued "derive"
// registry key would count as "derived" too, exactly as it would under
// the Node source's literal typeof check) rather than narrowing it to
// "must be a JSON object", since this is a byte/behavior-parity port, not
// an improvement pass.
func isJSObjectLike(value any) bool {
	switch value.(type) {
	case map[string]any:
		return true
	case []any:
		return true
	default:
		return false
	}
}

// matchingPrefix ports the provider-prefix longest-match lookup inline in
// loadedResourceShape in the original implementation (`Object.entries(...)
// .filter(...).sort(([left],[right]) => right.length - left.length)`).
// Ties among same-length candidate prefixes are broken alphabetically
// (canonjson.SortedStrings). Every committed pack declares exactly one prefix
// per provider, so this tie-break is unreachable for validated pack metadata.
func matchingPrefix(providerPrefixes map[string]string, resourceType, provider string) (string, bool) {
	var candidates []string
	for prefix, owner := range providerPrefixes {
		if owner == provider && strings.HasPrefix(resourceType, prefix) {
			candidates = append(candidates, prefix)
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	sorted := canonjson.SortedStrings(candidates)
	longest := sorted[0]
	for _, candidate := range sorted[1:] {
		if len(candidate) > len(longest) {
			longest = candidate
		}
	}
	return longest, true
}

// loadedResourceShape ports loadedResourceShape from
// the original implementation.
func loadedResourceShape(root metadata.LoadedPackRoot, resourceType string) metadata.ResourceDescriptor {
	resource, ok := root.Resources[resourceType]
	if !ok {
		domainError("unknown active resource type '" + resourceType + "'")
	}
	prefix, ok := matchingPrefix(root.Packs.ProviderPrefixes, resourceType, resource.Provider)
	if !ok {
		domainError("resource type " + resourceType + " has no declared prefix for provider " + resource.Provider)
	}
	bareName := resourceType[len(prefix):]
	generated, _ := resource.Registry["generate"].(bool)
	derive, hasDerive := resource.Registry["derive"]
	derived := generated && hasDerive && isJSObjectLike(derive)

	return metadata.ResourceDescriptor{
		Type:      resourceType,
		Product:   resource.Product,
		Provider:  resource.Provider,
		BareName:  bareName,
		Generated: generated,
		Derived:   derived,
	}
}

// indexLoadedPackRoot builds the root resolver's lookup shape directly from
// authoritative pack metadata.
func indexLoadedPackRoot(root metadata.LoadedPackRoot) resourceIndex {
	resourceTypes := make([]string, 0, len(root.Resources))
	for resourceType := range root.Resources {
		resourceTypes = append(resourceTypes, resourceType)
	}
	resourceTypes = canonjson.SortedStrings(resourceTypes)

	resources := make(map[string]metadata.ResourceDescriptor, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		resources[resourceType] = loadedResourceShape(root, resourceType)
	}

	var generated []string
	for _, resourceType := range resourceTypes {
		resource := resources[resourceType]
		if !resource.Generated {
			continue
		}
		generated = append(generated, resourceType)
	}

	providers := make([]string, 0, len(root.Packs.ProviderPrefixes))
	for _, provider := range root.Packs.ProviderPrefixes {
		providers = append(providers, provider)
	}

	return resourceIndex{
		resources: resources,
		generated: stringSet(generated),
		providers: stringSet(providers),
	}
}

// resolution is the fixed singleton state-unit mapping consumed by
// rootTopologyFromIndex.
type resolution struct {
	labelsToMembers map[string][]string
	typeToLabel     map[string]string
}

// resolveRoots creates one root for every generated type. Deployment root
// options no longer influence topology after grouping retirement, but named
// providers must still belong to the loaded resource set.
func resolveRoots(dep deployment.Deployment, index resourceIndex) resolution {
	providerNames := make([]string, 0, len(dep.Roots))
	for provider := range dep.Roots {
		providerNames = append(providerNames, provider)
	}
	for _, provider := range canonjson.SortedStrings(providerNames) {
		if _, ok := index.providers[provider]; !ok {
			domainError("roots." + provider + " is not a declared provider prefix value")
		}
	}

	res := resolution{
		labelsToMembers: make(map[string][]string),
		typeToLabel:     make(map[string]string),
	}
	generatedTypes := make([]string, 0, len(index.generated))
	for resourceType := range index.generated {
		generatedTypes = append(generatedTypes, resourceType)
	}
	for _, resourceType := range canonjson.SortedStrings(generatedTypes) {
		res.labelsToMembers[resourceType] = []string{resourceType}
		res.typeToLabel[resourceType] = resourceType
	}
	return res
}

// expandResources resolves type and product selectors to generated resources.
func expandResources(selectors []string, index resourceIndex) []string {
	if len(selectors) == 0 {
		generatedTypes := make([]string, 0, len(index.generated))
		for resourceType := range index.generated {
			generatedTypes = append(generatedTypes, resourceType)
		}
		return canonjson.SortedStrings(generatedTypes)
	}
	selected := make(map[string]struct{})
	var unknown []string
	for _, selector := range selectors {
		if _, ok := index.generated[selector]; ok {
			selected[selector] = struct{}{}
			continue
		}
		if _, ok := index.resources[selector]; ok {
			unknown = append(unknown, selector)
			continue
		}
		var productMatches []string
		for _, resource := range index.resources {
			if resource.Generated && resource.Product == selector {
				productMatches = append(productMatches, resource.Type)
			}
		}
		if len(productMatches) > 0 {
			for _, match := range productMatches {
				selected[match] = struct{}{}
			}
			continue
		}
		if slash := strings.IndexByte(selector, '/'); slash >= 0 {
			provider := selector[:slash]
			bare := selector[slash+1:]
			var pathMatches []string
			for _, resource := range index.resources {
				if resource.Generated && resource.Provider == provider && resource.BareName == bare {
					pathMatches = append(pathMatches, resource.Type)
				}
			}
			if len(pathMatches) > 0 {
				for _, match := range pathMatches {
					selected[match] = struct{}{}
				}
				continue
			}
		}
		unknown = append(unknown, selector)
	}
	if len(unknown) > 0 {
		domainErrorCode(
			"unknown or non-generated resource selector(s): "+strings.Join(canonjson.SortedStrings(unknown), ", "),
			"UNKNOWN_RESOURCE_SELECTOR",
		)
	}
	selectedList := make([]string, 0, len(selected))
	for resourceType := range selected {
		selectedList = append(selectedList, resourceType)
	}
	return canonjson.SortedStrings(selectedList)
}

// ExpandResourceSet ports expandCatalogResources from
// the original implementation.
func ExpandResourceSet(resourceSet metadata.ResourceSet, selectors []string) (types []string, err error) {
	defer recoverProcessFailure(&err)
	return expandResources(selectors, indexResourceSet(resourceSet)), nil
}

// ExpandLoadedResources ports expandLoadedResources from
// the original implementation: "Expand transform selectors without
// constructing or persisting a root catalog."
func ExpandLoadedResources(root metadata.LoadedPackRoot, selectors []string) (types []string, err error) {
	defer recoverProcessFailure(&err)
	return expandResources(selectors, indexLoadedPackRoot(root)), nil
}

// RootTopologyRoot ports the RootTopologyRoot interface from
// the original implementation.
type RootTopologyRoot struct {
	Label    string
	Provider *string
	Members  []string
	EnvDir   *string
}

// RootTopologyDirectories ports the anonymous `directories` shape nested in
// the RootTopology interface in the original implementation.
type RootTopologyDirectories struct {
	Config  string
	Imports string
	Envs    string
}

// RootTopology ports the RootTopology interface from
// the original implementation.
type RootTopology struct {
	Kind          string
	SchemaVersion int
	Tenant        *string
	Selectors     []string
	Directories   *RootTopologyDirectories
	Roots         []RootTopologyRoot
	ResourceRoots map[string]string
}

// WholeRootDiagnostic ports the WholeRootDiagnostic interface from
// the original implementation.
type WholeRootDiagnostic struct {
	Level             string
	Code              string
	Message           string
	SelectedMembers   []string
	Root              string
	AdditionalMembers []string
}

// RootTopologyResult bundles rootTopologyFromIndex's two return values
// (the original implementation's ported functions all return `{ topology,
// diagnostics }`) into a single Go return value.
type RootTopologyResult struct {
	Topology    RootTopology
	Diagnostics []WholeRootDiagnostic
}

// tenantPath ports tenantPath from the original implementation. It is
// distinct from, and deliberately does not call,
// go/internal/deployment's own (unexported) tenant-path helper: the two
// raise different malformed-overlay error text for what is, not
// coincidentally, the same underlying check (compare this function's
// error message with deployment.DeploymentConfigDir's), because that is
// exactly what the two Node source files (deployment.ts's
// deploymentTenantPath vs. roots.ts's tenantPath) each independently do.
func tenantPath(dep deployment.Deployment, tenant string, kind string) string {
	overlay, ok := dep.Overlay.(string)
	if !ok {
		domainError("deployment overlay must be a string when tenant paths are requested")
	}
	relative := posixpath.Join(kind, tenant)
	if overlay == "." {
		return relative
	}
	return posixpath.Join(overlay, relative)
}

// rootTopologyFromIndexOptions bundles the private topology resolver inputs.
type rootTopologyFromIndexOptions struct {
	index     resourceIndex
	dep       deployment.Deployment
	tenant    *string
	selectors []string
}

// rootTopologyFromIndex resolves the selected resources into singleton roots.
func rootTopologyFromIndex(options rootTopologyFromIndexOptions) RootTopologyResult {
	if options.tenant != nil {
		validateTenant(*options.tenant)
	}
	index := options.index
	res := resolveRoots(options.dep, index)
	selectedResources := expandResources(options.selectors, index)
	selected := stringSet(selectedResources)

	var labels []string
	if len(options.selectors) == 0 {
		labels = make([]string, 0, len(res.labelsToMembers))
		for label := range res.labelsToMembers {
			labels = append(labels, label)
		}
		labels = canonjson.SortedStrings(labels)
	} else {
		labelSet := make(map[string]struct{})
		var ordered []string
		for _, resourceType := range selectedResources {
			label, ok := res.typeToLabel[resourceType]
			if !ok {
				domainError("unknown generated resource type '" + resourceType + "'")
			}
			if _, seen := labelSet[label]; !seen {
				labelSet[label] = struct{}{}
				ordered = append(ordered, label)
			}
		}
		labels = canonjson.SortedStrings(ordered)
	}

	var diagnostics []WholeRootDiagnostic
	topologyRoots := make([]RootTopologyRoot, 0, len(labels))
	for _, label := range labels {
		members := canonjson.SortedStrings(res.labelsToMembers[label])
		var selectedMembers, additionalMembers []string
		for _, member := range members {
			if _, ok := selected[member]; ok {
				selectedMembers = append(selectedMembers, member)
			} else {
				additionalMembers = append(additionalMembers, member)
			}
		}
		if len(selectedMembers) > 0 && len(additionalMembers) > 0 {
			diagnostics = append(diagnostics, WholeRootDiagnostic{
				Level: "note",
				Code:  "WHOLE_ROOT_SELECTION",
				Message: "selecting " + strings.Join(selectedMembers, ", ") +
					" selects whole root " + label + "; also operating on " + strings.Join(additionalMembers, ", "),
				SelectedMembers:   selectedMembers,
				Root:              label,
				AdditionalMembers: additionalMembers,
			})
		}
		var provider *string
		if len(members) > 0 {
			if resource, ok := index.resources[members[0]]; ok {
				p := resource.Provider
				provider = &p
			}
		}
		var envDir *string
		if options.tenant != nil {
			dir := posixpath.Join(tenantPath(options.dep, *options.tenant, "envs"), label)
			envDir = &dir
		}
		topologyRoots = append(topologyRoots, RootTopologyRoot{
			Label:    label,
			Provider: provider,
			Members:  members,
			EnvDir:   envDir,
		})
	}

	resourceRoots := make(map[string]string)
	for _, root := range topologyRoots {
		for _, member := range root.Members {
			resourceRoots[member] = root.Label
		}
	}

	var directories *RootTopologyDirectories
	if options.tenant != nil {
		directories = &RootTopologyDirectories{
			Config:  tenantPath(options.dep, *options.tenant, "config"),
			Imports: tenantPath(options.dep, *options.tenant, "imports"),
			Envs:    tenantPath(options.dep, *options.tenant, "envs"),
		}
	}

	selectors := options.selectors
	if selectors == nil {
		selectors = []string{}
	}

	return RootTopologyResult{
		Topology: RootTopology{
			Kind:          "infrawright.root_topology",
			SchemaVersion: 1,
			Tenant:        options.tenant,
			Selectors:     selectors,
			Directories:   directories,
			Roots:         topologyRoots,
			ResourceRoots: resourceRoots,
		},
		Diagnostics: diagnostics,
	}
}

// RootTopologyOptions bundles RootTopology's parameters, the Go analogue of
// the inline options-object parameter type the original implementation's
// rootTopology accepts.
type RootTopologyOptions struct {
	ResourceSet metadata.ResourceSet
	Deployment  deployment.Deployment
	// Tenant is nil for the Node source's `tenant: null`.
	Tenant    *string
	Selectors []string
}

// RootTopologyFromResourceSet ports rootTopology from
// the original implementation. Named RootTopologyFromResourceSet rather than
// RootTopology (which the RootTopology struct type above already claims)
// since Go, unlike TypeScript, does not allow a function and a type to
// share one exported name in the same package.
func RootTopologyFromResourceSet(options RootTopologyOptions) (result RootTopologyResult, err error) {
	defer recoverProcessFailure(&err)
	return rootTopologyFromIndex(rootTopologyFromIndexOptions{
		dep:       options.Deployment,
		index:     indexResourceSet(options.ResourceSet),
		tenant:    options.Tenant,
		selectors: options.Selectors,
	}), nil
}

// LoadedRootTopologyOptions bundles LoadedRootTopology's parameters, the Go
// analogue of the inline options-object parameter type
// the original implementation's loadedRootTopology accepts.
type LoadedRootTopologyOptions struct {
	Root       metadata.LoadedPackRoot
	Deployment deployment.Deployment
	// Tenant is nil for the Node source's `tenant: null`.
	Tenant    *string
	Selectors []string
}

// LoadedRootTopology ports loadedRootTopology from
// the original implementation: "Resolve roots from the same pack metadata
// object used by generators." This is the runner's actual entry point
// (the Go runtime contract's roots/scope-paths/plan-roots/environment-
// generation slice calls this, not RootTopologyFromResourceSet) -- everything
// else in this package exists to support it (or to mirror
// the original implementation's other exports faithfully alongside it).
//
// Tenant validation happens twice on a non-nil Tenant, exactly as the Node
// source's loadedRootTopology validates before calling
// rootTopologyFromIndex, which validates again at its own start: this
// double validation is preserved deliberately rather than "optimized away"
// as a redundant check, per this port's non-goal of behavior changes
// (including harmless-looking ones) during porting.
func LoadedRootTopology(options LoadedRootTopologyOptions) (result RootTopologyResult, err error) {
	defer recoverProcessFailure(&err)
	if options.Tenant != nil {
		validateTenant(*options.Tenant)
	}
	return rootTopologyFromIndex(rootTopologyFromIndexOptions{
		dep:       options.Deployment,
		index:     indexLoadedPackRoot(options.Root),
		tenant:    options.Tenant,
		selectors: options.Selectors,
	}), nil
}
