// Package roots derives singleton-state v2 topology from persisted catalogs
// or loaded packs. It retains the established tenant, selector, path, and
// output shapes from node-src/domain/roots.ts, but Go now owns topology:
// every generated resource type is one state unit whose label is the type and
// whose members list contains only that type. The frozen Node implementation
// remains v1 provenance and is not an oracle for these v2 bytes.
package roots

import (
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/pypath"
)

// validTenant defines the accepted tenant path segment.
var validTenant = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// domainError panics with a *procerr.ProcessFailure carrying category
// "domain" and the given code (defaulting to "INVALID_ROOT_CONFIGURATION",
// the Go analogue of node-src/domain/roots.ts's `code =
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

// validateTenant ports validateTenant from node-src/domain/roots.ts,
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

// ValidateTenant ports validateTenant from node-src/domain/roots.ts.
func ValidateTenant(tenant string) (err error) {
	defer recoverProcessFailure(&err)
	validateTenant(tenant)
	return nil
}

// catalogIndex is the Go analogue of the CatalogIndex interface in
// node-src/domain/roots.ts: the private, pre-computed lookup shape
// resolveRoots/expandResources/rootTopologyFromIndex share, built once per
// call by indexCatalog (from a persisted metadata.RootCatalog) or
// indexLoadedPackRoot (directly from a metadata.LoadedPackRoot).
type catalogIndex struct {
	resources map[string]metadata.RootCatalogResource
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

// indexCatalog ports indexCatalog from node-src/domain/roots.ts.
func indexCatalog(catalog metadata.RootCatalog) catalogIndex {
	resources := make(map[string]metadata.RootCatalogResource, len(catalog.Resources))
	for _, resource := range catalog.Resources {
		resources[resource.Type] = resource
	}
	var generated []string
	for _, resource := range catalog.Resources {
		if !resource.Generated {
			continue
		}
		generated = append(generated, resource.Type)
	}
	return catalogIndex{
		resources: resources,
		generated: stringSet(generated),
		providers: stringSet(catalog.DeclaredProviders),
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
// loadedResourceShape in node-src/domain/roots.ts (`Object.entries(...)
// .filter(...).sort(([left],[right]) => right.length - left.length)`).
// Ties among same-length candidate prefixes are broken alphabetically
// (canonjson.SortedStrings) rather than by the Node source's
// Object.entries insertion order, mirroring
// go/internal/metadata/rootcatalog.go's matchingPrefix and its doc
// comment's rationale: every committed pack declares exactly one prefix
// per provider, so this tie-break is unreachable in this port's gate.
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
// node-src/domain/roots.ts.
func loadedResourceShape(root metadata.LoadedPackRoot, resourceType string) metadata.RootCatalogResource {
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

	return metadata.RootCatalogResource{
		Type:      resourceType,
		Product:   resource.Product,
		Provider:  resource.Provider,
		BareName:  bareName,
		Generated: generated,
		Derived:   derived,
	}
}

// indexLoadedPackRoot ports indexLoadedPackRoot from
// node-src/domain/roots.ts: "Build the existing root resolver's private
// index directly from pack metadata."
func indexLoadedPackRoot(root metadata.LoadedPackRoot) catalogIndex {
	resourceTypes := make([]string, 0, len(root.Resources))
	for resourceType := range root.Resources {
		resourceTypes = append(resourceTypes, resourceType)
	}
	resourceTypes = canonjson.SortedStrings(resourceTypes)

	resources := make(map[string]metadata.RootCatalogResource, len(resourceTypes))
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

	return catalogIndex{
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
// providers must still belong to the loaded catalog.
func resolveRoots(dep deployment.Deployment, index catalogIndex) resolution {
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

// expandResources ports expandResources from node-src/domain/roots.ts.
func expandResources(selectors []string, index catalogIndex) []string {
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

// ExpandCatalogResources ports expandCatalogResources from
// node-src/domain/roots.ts.
func ExpandCatalogResources(catalog metadata.RootCatalog, selectors []string) (types []string, err error) {
	defer recoverProcessFailure(&err)
	return expandResources(selectors, indexCatalog(catalog)), nil
}

// ExpandLoadedResources ports expandLoadedResources from
// node-src/domain/roots.ts: "Expand transform selectors without
// constructing or persisting a root catalog."
func ExpandLoadedResources(root metadata.LoadedPackRoot, selectors []string) (types []string, err error) {
	defer recoverProcessFailure(&err)
	return expandResources(selectors, indexLoadedPackRoot(root)), nil
}

// RootTopologyRoot ports the RootTopologyRoot interface from
// node-src/domain/types.ts.
type RootTopologyRoot struct {
	Label    string
	Provider *string
	Members  []string
	EnvDir   *string
}

// RootTopologyDirectories ports the anonymous `directories` shape nested in
// the RootTopology interface in node-src/domain/types.ts.
type RootTopologyDirectories struct {
	Config  string
	Imports string
	Envs    string
}

// RootTopology ports the RootTopology interface from
// node-src/domain/types.ts.
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
// node-src/domain/types.ts.
type WholeRootDiagnostic struct {
	Level             string
	Code              string
	Message           string
	SelectedMembers   []string
	Root              string
	AdditionalMembers []string
}

// RootTopologyResult bundles rootTopologyFromIndex's two return values
// (node-src/domain/roots.ts's ported functions all return `{ topology,
// diagnostics }`) into a single Go return value.
type RootTopologyResult struct {
	Topology    RootTopology
	Diagnostics []WholeRootDiagnostic
}

// tenantPath ports tenantPath from node-src/domain/roots.ts. It is
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
	relative := pypath.PythonPosixJoin(kind, tenant)
	if overlay == "." {
		return relative
	}
	return pypath.PythonPosixJoin(overlay, relative)
}

// rootTopologyFromIndexOptions bundles rootTopologyFromIndex's parameters,
// the Go analogue of the inline options-object parameter type
// node-src/domain/roots.ts's rootTopologyFromIndex accepts.
type rootTopologyFromIndexOptions struct {
	index     catalogIndex
	dep       deployment.Deployment
	tenant    *string
	selectors []string
}

// rootTopologyFromIndex ports rootTopologyFromIndex from
// node-src/domain/roots.ts.
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
			dir := pypath.PythonPosixJoin(tenantPath(options.dep, *options.tenant, "envs"), label)
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
// the inline options-object parameter type node-src/domain/roots.ts's
// rootTopology accepts.
type RootTopologyOptions struct {
	Catalog    metadata.RootCatalog
	Deployment deployment.Deployment
	// Tenant is nil for the Node source's `tenant: null`.
	Tenant    *string
	Selectors []string
}

// RootTopologyFromCatalog ports rootTopology from
// node-src/domain/roots.ts. Named RootTopologyFromCatalog rather than
// RootTopology (which the RootTopology struct type above already claims)
// since Go, unlike TypeScript, does not allow a function and a type to
// share one exported name in the same package.
func RootTopologyFromCatalog(options RootTopologyOptions) (result RootTopologyResult, err error) {
	defer recoverProcessFailure(&err)
	return rootTopologyFromIndex(rootTopologyFromIndexOptions{
		dep:       options.Deployment,
		index:     indexCatalog(options.Catalog),
		tenant:    options.Tenant,
		selectors: options.Selectors,
	}), nil
}

// LoadedRootTopologyOptions bundles LoadedRootTopology's parameters, the Go
// analogue of the inline options-object parameter type
// node-src/domain/roots.ts's loadedRootTopology accepts.
type LoadedRootTopologyOptions struct {
	Root       metadata.LoadedPackRoot
	Deployment deployment.Deployment
	// Tenant is nil for the Node source's `tenant: null`.
	Tenant    *string
	Selectors []string
}

// LoadedRootTopology ports loadedRootTopology from
// node-src/domain/roots.ts: "Resolve roots from the same pack metadata
// object used by generators." This is the runner's actual entry point
// (docs/go-runtime-plan.md's roots/scope-paths/plan-roots/environment-
// generation slice calls this, not RootTopologyFromCatalog) -- everything
// else in this package exists to support it (or to mirror
// node-src/domain/roots.ts's other exports faithfully alongside it).
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
