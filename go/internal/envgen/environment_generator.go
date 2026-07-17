package envgen

// environment_generator.go ports node-src/domain/environment-generator.ts:
// gen-env's env-root generation -- backend blocks, provider headers, module
// source resolution, variable wiring, expression-binding emission, and the
// generated-root file lifecycle (main.tf, expression_bindings.tf, README.md,
// tests/smoke.tftest.hcl, the tenant-level .backend marker).
//
// TS-import mapping (see this package's port report for the full table and
// the "locally defined" flags below):
//   - node:path                     -> LOCALLY DEFINED (nodePathRelative
//     below): environment-generator.ts imports Node's own "path" module
//     directly, not this repository's Python-flavored
//     go/internal/pypath (which ports the DIFFERENT domain/paths.ts, used
//     elsewhere for tenant/config/imports/envs directory derivation, not
//     for this file's plain path.join/path.relative/path.isAbsolute calls).
//     No existing Go package ports node:path itself, so a small local POSIX
//     port (path.Join/path.IsAbs delegate directly to the stdlib "path"
//     package already used by go/internal/tfrender for the same reason;
//     path.relative has no stdlib POSIX-only equivalent, hence
//     nodePathRelative) is the least-surprising home for it.
//   - HclFormatter (node-src/modules/generator.ts) -> LOCALLY DEFINED: the
//     sibling modulesgen port of that TS file is off-limits to this task
//     (per this port's brief); environment-generator.ts's only dependency
//     on it is this one function-type alias, reproduced verbatim here.
//   - REFERENCE_BACKEND_VARIABLE (node-src/domain/reference-backend.ts) ->
//     LOCALLY DEFINED: reference-backend.ts itself (a bounded-read/
//     azurerm-backend-config validator) is out of this port's three-file
//     scope; only its one exported constant is needed here.
//   - deploymentConfigDir/deploymentEnvsDir/deploymentModuleDir/
//     deploymentReferenceBindingMode/deploymentTfvarsFormat ->
//     deployment.DeploymentConfigDir/DeploymentEnvsDir/DeploymentModuleDir/
//     DeploymentReferenceBindingMode/DeploymentTfvarsFormat
//   - loadedRootTopology/validateTenant -> roots.LoadedRootTopology/
//     roots.ValidateTenant
//   - transformArtifactPaths (node-src/domain/transform-artifacts.ts) ->
//     tfrender.ComputeTransformArtifactPaths
//   - renderHclQuotedString (node-src/domain/import-moves.ts) ->
//     tfrender.RenderHclQuotedString
//   - LoadedPackRoot/LoadedResourceMetadata -> metadata.LoadedPackRoot/
//     metadata.LoadedResourceMetadata
//   - applyExpressionBindings/expressionModuleTargets/
//     expressionRemoteStateReferences/loadExpressionBindings/
//     mergeExpressionBindingLayers/renderExpressionBindingsHcl/
//     validateExpressionBindingSchemaPaths -> this package's own
//     expression_bindings.go exports (same Go package as this file)
//   - crossStateDependencyClosure/crossStateReferenceTopology/
//     INFRAWRIGHT_REFERENCE_OUTPUT -> this package's own
//     reference_topology.go exports
//
// Errors: unlike expression_bindings.go and reference_topology.go (deeply
// recursive validators that benefit from the panic/bindingsFail
// convention), this file's functions are shallow and sequential, so they
// use ordinary Go (T, error) returns throughout -- no panic/recover here.
import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// expressionBindingsTF ports EXPRESSION_BINDINGS_TF from
// node-src/domain/environment-generator.ts.
const expressionBindingsTF = "expression_bindings.tf"

const (
	staleDisabled  = "stale generated bindings ignored (bind_references disabled); rerun make transform to remove %s"
	staleNonmember = "stale generated binding ignored (target %s not in root members); rerun make transform to remove %s"
	cycleRemedy    = "resolve one direction via a literal ID or operator expression, or disable bind_references"
)

// ReferenceBackendVariable ports REFERENCE_BACKEND_VARIABLE from
// node-src/domain/reference-backend.ts (see this file's package doc
// comment for why it is reproduced locally rather than imported).
const ReferenceBackendVariable = "infrawright_remote_state_backend_config"

// HclFormatter matches the HclFormatter type from
// node-src/modules/generator.ts (see this file's package doc comment for
// why it is reproduced locally rather than imported).
type HclFormatter func(source string) (string, error)

// GeneratedEnvironmentRoot is the Go analogue of one element of the
// `roots` array in the EnvironmentGenerationResult interface from
// node-src/domain/environment-generator.ts.
type GeneratedEnvironmentRoot struct {
	Label   string
	Members []string
	Path    string
}

// EnvironmentGenerationResult is the Go analogue of the
// EnvironmentGenerationResult interface in
// node-src/domain/environment-generator.ts. Backend is nil for the TS
// source's `backend: string | null` being null.
type EnvironmentGenerationResult struct {
	Roots   []GeneratedEnvironmentRoot
	Backend *string
}

// EnvironmentRemoteState is the Go analogue of the EnvironmentRemoteState
// interface in node-src/domain/environment-generator.ts.
type EnvironmentRemoteState struct {
	Label     string
	LocalPath string
}

// boundRemoteStateReference is the Go analogue of the
// BoundRemoteStateReference interface in
// node-src/domain/environment-generator.ts.
type boundRemoteStateReference struct {
	RemoteStateReference
	Field    string
	Referrer string
}

// --- local node:path (POSIX) port; see this file's package doc comment ---

// relativeVirtualBase is nodePathRelative's stand-in for Node's
// process.cwd(): every call site in this file passes two paths built from
// the same overlay/module-dir basis (both absolute, or both relative to an
// implicit common root), so the concrete value used here cancels out of the
// relative-path result exactly the way the real cwd would for such a pair
// -- see nodePathRelative's doc comment.
const relativeVirtualBase = "/a"

// nodePathResolve mirrors one step of Node's path.posix.resolve: an
// already-absolute path is left as-is (Cleaned); a relative path is
// resolved against base first.
func nodePathResolve(base, p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Clean(base + "/" + p)
}

func nodePathSegments(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// nodePathRelative reproduces Node's (POSIX) path.relative(from, to): the
// path from `from` to `to`, expressed as a run of ".." components followed
// by `to`'s remaining components past their longest common path prefix.
// Node's own implementation resolves both arguments against
// process.cwd() first; this uses relativeVirtualBase instead, which is
// behaviorally identical for every call site in this file (see that
// constant's doc comment) and keeps this port's output independent of the
// actual process working directory, matching how this repository's other
// path-deriving domain packages (e.g. go/internal/pypath) are already
// cwd-independent by construction.
func nodePathRelative(from, to string) string {
	if from == to {
		return ""
	}
	fromResolved := nodePathResolve(relativeVirtualBase, from)
	toResolved := nodePathResolve(relativeVirtualBase, to)
	if fromResolved == toResolved {
		return ""
	}
	fromParts := nodePathSegments(fromResolved)
	toParts := nodePathSegments(toResolved)
	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	segments := make([]string, 0, (len(fromParts)-common)+(len(toParts)-common))
	for i := common; i < len(fromParts); i++ {
		segments = append(segments, "..")
	}
	segments = append(segments, toParts[common:]...)
	return strings.Join(segments, "/")
}

// --- small map/slice helpers local to this file ---

func bindingsByTypeKeys(m map[string][]ExpressionBinding) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func mapKeysOfBoolSets(m map[string]map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func mapKeysOfNestedResourceSets(m map[string]map[string]map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// fileExists ports the local `exists` helper from
// node-src/domain/environment-generator.ts: "Match Python os.path.exists:
// follow links and treat a failed stat as absent." os.Stat, like Node's
// stat (as opposed to lstat), follows symlinks, so a dangling symlink
// reports as absent here -- exactly the semantics
// generateEnvironmentRoots's stale-artifact handling (and this port's
// "dangling artifact paths" test) depends on.
func fileExists(candidate string) bool {
	_, err := os.Stat(candidate)
	return err == nil
}

// removeIfPresent ports removeIfPresent from
// node-src/domain/environment-generator.ts.
func removeIfPresent(file string) (bool, error) {
	if !fileExists(file) {
		return false, nil
	}
	if err := os.Remove(file); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// resourceMetadata ports the local `resource` helper from
// node-src/domain/environment-generator.ts.
func resourceMetadata(root metadata.LoadedPackRoot, resourceType string) (metadata.LoadedResourceMetadata, error) {
	selected, ok := root.Resources[resourceType]
	if !ok {
		return metadata.LoadedResourceMetadata{}, fmt.Errorf("unknown active resource %s", resourceType)
	}
	return selected, nil
}

// providerOf ports providerOf from
// node-src/domain/environment-generator.ts.
func providerOf(root metadata.LoadedPackRoot, resourceType string) (string, error) {
	res, err := resourceMetadata(root, resourceType)
	if err != nil {
		return "", err
	}
	return res.Provider, nil
}

// variableName ports variableName from
// node-src/domain/environment-generator.ts.
func variableName(topology roots.RootTopology, resourceType string) string {
	if topology.ResourceRoots[resourceType] == resourceType {
		return "items"
	}
	return resourceType + "_items"
}

// tenantEnvironmentDirectory ports tenantEnvironmentDirectory from
// node-src/domain/environment-generator.ts. outputRoot nil matches the TS
// source's `outputRoot === undefined`.
func tenantEnvironmentDirectory(dep deployment.Deployment, tenant string, outputRoot *string) (string, error) {
	if outputRoot == nil {
		return deployment.DeploymentEnvsDir(dep, tenant)
	}
	return path.Join(*outputRoot, tenant), nil
}

// environmentRootDirectory ports environmentRootDirectory from
// node-src/domain/environment-generator.ts.
func environmentRootDirectory(dep deployment.Deployment, tenant, label string, outputRoot *string) (string, error) {
	tenantDirectory, err := tenantEnvironmentDirectory(dep, tenant, outputRoot)
	if err != nil {
		return "", err
	}
	return path.Join(tenantDirectory, label), nil
}

// moduleSource ports moduleSource from
// node-src/domain/environment-generator.ts.
func moduleSource(dep deployment.Deployment, resourceType, environmentDirectory string) (string, error) {
	moduleDir, err := deployment.DeploymentModuleDir(dep)
	if err != nil {
		return "", err
	}
	source := nodePathRelative(environmentDirectory, path.Join(moduleDir, resourceType))
	if !strings.HasPrefix(source, "../") && !strings.HasPrefix(source, "./") && !path.IsAbs(source) {
		source = "./" + source
	}
	return source, nil
}

// expressionLocal ports expressionLocal from
// node-src/domain/environment-generator.ts.
func expressionLocal(label, resourceType string) string {
	if label == resourceType {
		return "infrawright_expression_bound_items"
	}
	return "infrawright_" + resourceType + "_expression_bound_items"
}

// renderRemoteStateBlocks ports renderRemoteStateBlocks from
// node-src/domain/environment-generator.ts. backend nil matches the TS
// source's `options.backend === undefined`.
func renderRemoteStateBlocks(backend *string, remoteStates []EnvironmentRemoteState, tenant string) (string, error) {
	if len(remoteStates) == 0 {
		return "", nil
	}
	if backend != nil && *backend != "azurerm" {
		return "", fmt.Errorf("cross-state references support local or azurerm state, not %s", jsStringify(*backend))
	}
	isAzurerm := backend != nil && *backend == "azurerm"
	var sections []string
	if isAzurerm {
		sections = append(sections,
			fmt.Sprintf(`variable "%s" {`, ReferenceBackendVariable),
			`  description = "Non-secret azurerm address fields shared by cross-state lookups."`,
			"  type        = any",
			"  sensitive   = true",
			"}", "",
		)
	}
	for _, remote := range remoteStates {
		sections = append(sections, fmt.Sprintf(`data "terraform_remote_state" "%s" {`, remote.Label))
		if isAzurerm {
			keyQuoted, err := tfrender.RenderHclQuotedString(tenant + "/" + remote.Label + ".tfstate")
			if err != nil {
				return "", err
			}
			sections = append(sections,
				`  backend = "azurerm"`,
				fmt.Sprintf("  config = merge(var.%s, {", ReferenceBackendVariable),
				fmt.Sprintf("    key = %s", keyQuoted),
				"  })",
			)
		} else {
			pathQuoted, err := tfrender.RenderHclQuotedString(remote.LocalPath)
			if err != nil {
				return "", err
			}
			sections = append(sections,
				`  backend = "local"`,
				"  config = {",
				fmt.Sprintf("    path = %s", pathQuoted),
				"  }",
			)
		}
		sections = append(sections, "}", "")
	}
	return strings.Join(sections, "\n"), nil
}

// renderReferenceOutput ports renderReferenceOutput from
// node-src/domain/environment-generator.ts.
func renderReferenceOutput(resourceTypes []string) string {
	if len(resourceTypes) == 0 {
		return ""
	}
	lines := []string{
		fmt.Sprintf(`output "%s" {`, InfrawrightReferenceOutput),
		`  description = "Minimal stable-key to provider ID map for opted-in cross-state consumers."`,
		"  sensitive   = true",
		"  value = {",
	}
	for _, resourceType := range canonjson.SortedStrings(resourceTypes) {
		lines = append(lines, fmt.Sprintf("    %s = { for key, item in module.%s.items : key => item.id }", resourceType, resourceType))
	}
	lines = append(lines, "  }", "}", "")
	return strings.Join(lines, "\n")
}

// RenderEnvironmentMainOptions bundles RenderEnvironmentMain's parameters,
// the Go analogue of the inline options-object parameter type
// node-src/domain/environment-generator.ts's renderEnvironmentMain accepts.
// Backend nil matches `backend?: string` being omitted.
type RenderEnvironmentMainOptions struct {
	Backend                *string
	Deployment             deployment.Deployment
	EnvironmentDirectory   string
	ExpressionBindingTypes []string
	Label                  string
	Members                []string
	ReferenceOutputTypes   []string
	RemoteStates           []EnvironmentRemoteState
	Root                   metadata.LoadedPackRoot
	Tenant                 string
	Topology               roots.RootTopology
}

// RenderEnvironmentMain ports the exported renderEnvironmentMain from
// node-src/domain/environment-generator.ts: "Render one complete
// deployment-selected root without touching state."
func RenderEnvironmentMain(options RenderEnvironmentMainOptions) (string, error) {
	members := canonjson.SortedStrings(options.Members)
	if len(members) == 0 {
		return "", fmt.Errorf("env root %s must contain at least one resource type", options.Label)
	}
	providerSet := map[string]bool{}
	for _, member := range members {
		provider, err := providerOf(options.Root, member)
		if err != nil {
			return "", err
		}
		providerSet[provider] = true
	}
	providers := canonjson.SortedStrings(mapKeysBoolSetGeneric(providerSet))
	if len(providers) != 1 {
		return "", fmt.Errorf("env root %s spans providers: %s", options.Label, strings.Join(providers, ", "))
	}
	provider := providers[0]
	providerSource, ok := options.Root.Packs.ProviderSources[provider]
	if !ok {
		return "", fmt.Errorf("no provider source declared for %s", provider)
	}

	var backendLines string
	if options.Backend == nil || *options.Backend == "" {
		backendLines = fmt.Sprintf(
			"  # local state — opt into remote state with\n  # make gen-env TENANT=%s BACKEND=azurerm\n",
			options.Tenant,
		)
	} else {
		backendLines = fmt.Sprintf(
			"  backend \"%s\" {\n"+
				"    # Partial configuration. Storage details come from a\n"+
				"    # work-side file at init: make plan BACKEND_CONFIG=<file>\n"+
				"    # (copy backend.conf.example). The state key is derived\n"+
				"    # per root: %s/%s.tfstate\n"+
				"  }\n",
			*options.Backend, options.Tenant, options.Label,
		)
	}

	bound := map[string]bool{}
	for _, t := range options.ExpressionBindingTypes {
		bound[t] = true
	}
	memberBlocks := make([]string, len(members))
	for i, resourceType := range members {
		name := variableName(options.Topology, resourceType)
		var items string
		if bound[resourceType] {
			items = "local." + expressionLocal(options.Label, resourceType)
		} else {
			items = "var." + name
		}
		source, err := moduleSource(options.Deployment, resourceType, options.EnvironmentDirectory)
		if err != nil {
			return "", err
		}
		memberBlocks[i] = fmt.Sprintf(
			"variable \"%s\" {\n"+
				"  # opaque at the root; the module enforces the strict type.\n"+
				"  type = any\n"+
				"}\n\n"+
				"module \"%s\" {\n"+
				"  source = \"%s\"\n"+
				"  items = %s\n"+
				"}",
			name, resourceType, source, items,
		)
	}

	remoteStateBlocks, err := renderRemoteStateBlocks(options.Backend, options.RemoteStates, options.Tenant)
	if err != nil {
		return "", err
	}
	referenceOutput := renderReferenceOutput(options.ReferenceOutputTypes)
	var rootBody string
	if len(remoteStateBlocks) == 0 && len(referenceOutput) == 0 {
		rootBody = strings.Join(memberBlocks, "\n\n") + "\n"
	} else {
		rootBody = remoteStateBlocks + strings.Join(memberBlocks, "\n\n") + "\n\n" + referenceOutput
	}

	return fmt.Sprintf(
		"# GENERATED by engine.gen_env for tenant '%s' — do not edit.\n"+
			"# Regenerate: make gen-env TENANT=%s\n\n"+
			"terraform {\n"+
			"  required_version = \">= 1.5\"\n"+
			"  required_providers {\n"+
			"    %s = {\n"+
			"      source = \"%s\"\n"+
			"    }\n"+
			"  }\n"+
			"%s"+
			"}\n\n"+
			"provider \"%s\" {\n"+
			"  # credentials via provider environment variables\n"+
			"}\n\n%s",
		options.Tenant, options.Tenant, provider, providerSource, backendLines, provider, rootBody,
	), nil
}

// mapKeysBoolSetGeneric is a small local helper distinct from
// expression_bindings.go's mapKeys (which is map[string]any-keyed) --
// this file's presence-only string sets are map[string]bool.
func mapKeysBoolSetGeneric(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// RenderEnvironmentExpressionBindings ports the exported
// renderEnvironmentExpressionBindings from
// node-src/domain/environment-generator.ts.
func RenderEnvironmentExpressionBindings(bindings []ExpressionBinding, label, resourceType string, topology roots.RootTopology) (string, error) {
	return RenderExpressionBindingsHcl(bindings, RenderExpressionBindingsHclOptions{
		ItemsVariable: variableName(topology, resourceType),
		LocalName:     expressionLocal(label, resourceType),
	})
}

// renderRootExpressionBindings ports the local renderRootExpressionBindings
// from node-src/domain/environment-generator.ts.
func renderRootExpressionBindings(label string, bindingsByType map[string][]ExpressionBinding, topology roots.RootTopology) (string, error) {
	var sections []string
	for _, resourceType := range canonjson.SortedStrings(bindingsByTypeKeys(bindingsByType)) {
		rendered, err := RenderEnvironmentExpressionBindings(bindingsByType[resourceType], label, resourceType, topology)
		if err != nil {
			return "", err
		}
		if len(rendered) > 0 {
			sections = append(sections, strings.TrimRight(rendered, " \t\n\r\v\f"))
		}
	}
	if len(sections) == 0 {
		return "", nil
	}
	return strings.Join(sections, "\n\n") + "\n", nil
}

// configFile ports the local configFile helper from
// node-src/domain/environment-generator.ts.
func configFile(dep deployment.Deployment, tenant, resourceType string) (string, error) {
	paths, err := tfrender.ComputeTransformArtifactPaths(dep, resourceType, tenant)
	if err != nil {
		return "", err
	}
	return paths.Config, nil
}

// configReference ports the local configReference helper from
// node-src/domain/environment-generator.ts.
func configReference(dep deployment.Deployment, tenant, resourceType, environmentDirectory string) (string, error) {
	file, err := configFile(dep, tenant, resourceType)
	if err != nil {
		return "", err
	}
	return nodePathRelative(environmentDirectory, file), nil
}

// operatorBindingsFile ports the local operatorBindingsFile helper from
// node-src/domain/environment-generator.ts.
func operatorBindingsFile(dep deployment.Deployment, tenant, resourceType string) (string, error) {
	configDirectory, err := deployment.DeploymentConfigDir(dep, tenant)
	if err != nil {
		return "", err
	}
	return path.Join(configDirectory, resourceType+".expressions.json"), nil
}

// generatedBindingsFile ports the local generatedBindingsFile helper from
// node-src/domain/environment-generator.ts.
func generatedBindingsFile(dep deployment.Deployment, tenant, resourceType string) (string, error) {
	paths, err := tfrender.ComputeTransformArtifactPaths(dep, resourceType, tenant)
	if err != nil {
		return "", err
	}
	return paths.GeneratedBindings, nil
}

// validateBindingsAgainstConfig ports the local
// validateBindingsAgainstConfig helper from
// node-src/domain/environment-generator.ts.
func validateBindingsAgainstConfig(bindings []ExpressionBinding, config string, onDiagnostic func(string), variableNameValue string) error {
	if !fileExists(config) {
		return fmt.Errorf("expression bindings require projected config at %s", config)
	}
	if !strings.HasSuffix(config, ".json") {
		onDiagnostic(fmt.Sprintf("skip expression binding validation for %s (hcl tfvars; validation reads json only)", config))
		return nil
	}
	raw, err := os.ReadFile(config)
	if err != nil {
		return err
	}
	data, err := canonjson.ParseDataJSONLosslessly(string(raw))
	if err != nil {
		return err
	}
	var items any
	if obj, ok := data.(map[string]any); ok {
		items = obj[variableNameValue]
	}
	itemsObject, ok := items.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must contain a %s object", config, variableNameValue)
	}
	_, err = ApplyExpressionBindings(itemsObject, bindings)
	return err
}

// filterGeneratedBindings ports the local filterGeneratedBindings helper
// from node-src/domain/environment-generator.ts.
func filterGeneratedBindings(bindings []ExpressionBinding, members map[string]bool, onDiagnostic func(string), sourcePath string) []ExpressionBinding {
	var kept []ExpressionBinding
	for _, binding := range bindings {
		var nonmembers []string
		for _, target := range ExpressionModuleTargets(binding.Expression) {
			if !members[target] {
				nonmembers = append(nonmembers, target)
			}
		}
		if len(nonmembers) > 0 {
			onDiagnostic("NOTE bindings: " + fmt.Sprintf(staleNonmember, strings.Join(nonmembers, ", "), sourcePath))
		} else {
			kept = append(kept, binding)
		}
	}
	return kept
}

// remoteStateReferencesForBindings ports the local
// remoteStateReferencesForBindings helper from
// node-src/domain/environment-generator.ts. Unlike most of this file's
// helpers, this one can fail: an operator-authored binding can pass
// validateExpression's general allowlist (any `data.<ident>.<ident>...`
// selector) while still containing a `data.terraform_remote_state.`
// prefix that does not match ExpressionRemoteStateReferences's stricter
// canonical-selector grammar -- see that function's own ported test
// ("data.terraform_remote_state.zpa_segment_group.outputs.other") for a
// concrete example reachable this way.
func remoteStateReferencesForBindings(bindingsByType map[string][]ExpressionBinding) ([]boundRemoteStateReference, error) {
	selected := map[string]boundRemoteStateReference{}
	var order []string
	for _, resourceType := range canonjson.SortedStrings(bindingsByTypeKeys(bindingsByType)) {
		for _, binding := range bindingsByType[resourceType] {
			refs, err := ExpressionRemoteStateReferences(binding.Expression)
			if err != nil {
				return nil, err
			}
			for _, reference := range refs {
				var fieldParts []string
				for _, part := range binding.PathParts {
					if name, ok := part.(string); ok {
						fieldParts = append(fieldParts, name)
					}
				}
				field := strings.Join(fieldParts, ".")
				identity := resourceType + "\x00" + binding.Path + "\x00" + reference.Root + "\x00" + reference.ResourceType + "\x00" + reference.Key
				if _, exists := selected[identity]; !exists {
					order = append(order, identity)
				}
				selected[identity] = boundRemoteStateReference{RemoteStateReference: reference, Field: field, Referrer: resourceType}
			}
		}
	}
	result := make([]boundRemoteStateReference, 0, len(order))
	for _, identity := range order {
		result = append(result, selected[identity])
	}
	sortBoundReferences(result)
	return result, nil
}

func sortBoundReferences(references []boundRemoteStateReference) {
	for i := 1; i < len(references); i++ {
		for j := i; j > 0 && compareBoundReference(references[j-1], references[j]) > 0; j-- {
			references[j-1], references[j] = references[j], references[j-1]
		}
	}
}

func compareBoundReference(left, right boundRemoteStateReference) int {
	if c := canonjson.ComparePythonStrings(left.Referrer, right.Referrer); c != 0 {
		return c
	}
	if c := canonjson.ComparePythonStrings(left.Field, right.Field); c != 0 {
		return c
	}
	if c := canonjson.ComparePythonStrings(left.Root, right.Root); c != 0 {
		return c
	}
	if c := canonjson.ComparePythonStrings(left.ResourceType, right.ResourceType); c != 0 {
		return c
	}
	return canonjson.ComparePythonStrings(left.Key, right.Key)
}

// validateRemoteStateReferences ports the local
// validateRemoteStateReferences helper from
// node-src/domain/environment-generator.ts.
func validateRemoteStateReferences(
	crossState CrossStateReferenceTopology,
	currentRoot string,
	fullTopology roots.RootTopology,
	references []boundRemoteStateReference,
) error {
	rootsByLabel := map[string]roots.RootTopologyRoot{}
	for _, r := range fullTopology.Roots {
		rootsByLabel[r.Label] = r
	}
	declared := map[string]bool{}
	for _, edge := range crossState.Edges {
		key := edge.Referrer + "\x00" + edge.ReferrerRoot + "\x00" + edge.Field + "\x00" + edge.Referent + "\x00" + edge.ReferentRoot
		declared[key] = true
	}
	for _, reference := range references {
		target, ok := rootsByLabel[reference.Root]
		if !ok {
			return fmt.Errorf("cross-state binding targets unknown root %s", reference.Root)
		}
		if reference.Root == currentRoot {
			return fmt.Errorf("cross-state binding in %s targets its own state; use a module binding", currentRoot)
		}
		if !containsString(target.Members, reference.ResourceType) {
			return fmt.Errorf("cross-state binding expects %s in root %s", reference.ResourceType, reference.Root)
		}
		key := reference.Referrer + "\x00" + currentRoot + "\x00" + reference.Field + "\x00" + reference.ResourceType + "\x00" + reference.Root
		if !declared[key] {
			return fmt.Errorf(
				"cross-state binding %s.%s to %s in root %s is not declared by pack reference metadata",
				reference.Referrer, reference.Field, reference.ResourceType, reference.Root,
			)
		}
	}
	return nil
}

// loadBindingLayers ports the local loadBindingLayers helper from
// node-src/domain/environment-generator.ts.
func loadBindingLayers(
	dep deployment.Deployment,
	members []string,
	onDiagnostic func(string),
	resource metadata.LoadedResourceMetadata,
	tenant string,
) ([]ExpressionBinding, error) {
	var layers [][]ExpressionBinding
	generated, err := generatedBindingsFile(dep, tenant, resource.Type)
	if err != nil {
		return nil, err
	}
	if fileExists(generated) {
		mode := deployment.DeploymentReferenceBindingMode(dep, resource.Provider)
		if mode != deployment.ReferenceBindingDisabled {
			loaded, err := LoadExpressionBindings(generated, resource.Type)
			if err != nil {
				return nil, err
			}
			memberSet := map[string]bool{}
			for _, m := range members {
				memberSet[m] = true
			}
			filtered := filterGeneratedBindings(loaded, memberSet, onDiagnostic, generated)
			if len(filtered) > 0 {
				layers = append(layers, filtered)
			}
		} else {
			onDiagnostic("NOTE bindings: " + fmt.Sprintf(staleDisabled, generated))
		}
	}
	operator, err := operatorBindingsFile(dep, tenant, resource.Type)
	if err != nil {
		return nil, err
	}
	if fileExists(operator) {
		loaded, err := LoadExpressionBindings(operator, resource.Type)
		if err != nil {
			return nil, err
		}
		layers = append(layers, loaded)
	}
	return MergeExpressionBindingLayers(layers), nil
}

// cyclePathWithinRoot ports the local cyclePath helper from
// node-src/domain/environment-generator.ts (a DFS over expression-binding
// module targets confined to one root's members). Named
// cyclePathWithinRoot, distinct from reference_topology.go's
// cyclePathAcrossRoots, since the two TS source files each define their
// own same-named-but-differently-scoped local cyclePath helper -- see
// cyclePathAcrossRoots's doc comment.
func cyclePathWithinRoot(edges map[string]map[string]bool, members map[string]bool) []string {
	const (
		stateVisiting = "visiting"
		stateDone     = "done"
	)
	states := map[string]string{}
	var stack []string
	var visit func(string) []string
	visit = func(node string) []string {
		states[node] = stateVisiting
		stack = append(stack, node)
		for _, target := range canonjson.SortedStrings(mapKeysBoolSetGeneric(edges[node])) {
			if !members[target] {
				continue
			}
			if states[target] == stateVisiting {
				start := indexOfString(stack, target)
				found := append([]string{}, stack[start:]...)
				found = append(found, target)
				return found
			}
			if states[target] == "" {
				if found := visit(target); found != nil {
					return found
				}
			}
		}
		stack = stack[:len(stack)-1]
		states[node] = stateDone
		return nil
	}
	for _, member := range canonjson.SortedStrings(mapKeysBoolSetGeneric(members)) {
		if states[member] != "" {
			continue
		}
		if found := visit(member); found != nil {
			return found
		}
	}
	return nil
}

// AssertNoExpressionBindingCycles ports the exported
// assertNoExpressionBindingCycles from
// node-src/domain/environment-generator.ts.
func AssertNoExpressionBindingCycles(bindingsByType map[string][]ExpressionBinding, label string, members []string) error {
	memberSet := map[string]bool{}
	for _, m := range members {
		memberSet[m] = true
	}
	edges := map[string]map[string]bool{}
	for _, resourceType := range canonjson.SortedStrings(bindingsByTypeKeys(bindingsByType)) {
		for _, binding := range bindingsByType[resourceType] {
			for _, target := range ExpressionModuleTargets(binding.Expression) {
				if memberSet[target] {
					addToSet(edges, resourceType, target)
				}
			}
		}
	}
	cycle := cyclePathWithinRoot(edges, memberSet)
	if cycle != nil {
		return fmt.Errorf("expression binding cycle detected in root %s: %s; %s", label, strings.Join(cycle, " -> "), cycleRemedy)
	}
	return nil
}

// RenderEnvironmentReadmeOptions bundles RenderEnvironmentReadme's
// parameters, the Go analogue of the inline options-object parameter type
// node-src/domain/environment-generator.ts's renderEnvironmentReadme
// accepts.
type RenderEnvironmentReadmeOptions struct {
	Deployment           deployment.Deployment
	EnvironmentDirectory string
	Label                string
	Members              []string
	Tenant               string
	Topology             roots.RootTopology
}

// RenderEnvironmentReadme ports the exported renderEnvironmentReadme from
// node-src/domain/environment-generator.ts.
func RenderEnvironmentReadme(options RenderEnvironmentReadmeOptions) (string, error) {
	members := canonjson.SortedStrings(options.Members)
	if len(members) == 1 && options.Topology.ResourceRoots[members[0]] == members[0] {
		resourceType := members[0]
		config, err := configReference(options.Deployment, options.Tenant, resourceType, options.EnvironmentDirectory)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"# %s / %s (generated env root)\n\n"+
				"Isolated Terraform root for `%s` on tenant `%s`. GENERATED — do not\n"+
				"edit (AGENTS.md rule 6); regenerate with `make gen-env TENANT=%s`.\n"+
				"Config is loaded at plan time from the tenant's config dir, relative to\n"+
				"this root: `%s`.\n",
			options.Tenant, resourceType, resourceType, options.Tenant, options.Tenant, config,
		), nil
	}
	references := make([]string, len(members))
	for i, resourceType := range members {
		config, err := configReference(options.Deployment, options.Tenant, resourceType, options.EnvironmentDirectory)
		if err != nil {
			return "", err
		}
		references[i] = fmt.Sprintf("%s=%s", variableName(options.Topology, resourceType), config)
	}
	return fmt.Sprintf(
		"# %s / %s (generated env root)\n\n"+
			"Grouped Terraform root for `%s` on tenant `%s`. GENERATED — do not\n"+
			"edit (AGENTS.md rule 6); regenerate with `make gen-env TENANT=%s`.\n"+
			"Config is loaded at plan time from the tenant's config dir, relative to\n"+
			"this root: `%s`.\n",
		options.Tenant, options.Label, strings.Join(members, ", "), options.Tenant, options.Tenant, strings.Join(references, "`, `"),
	), nil
}

// RenderEnvironmentSmokeTestOptions bundles RenderEnvironmentSmokeTest's
// parameters, the Go analogue of the inline options-object parameter type
// node-src/domain/environment-generator.ts's renderEnvironmentSmokeTest
// accepts. ConfigFormat is "json" or "hcl", matching the TS source's
// literal union.
type RenderEnvironmentSmokeTestOptions struct {
	Backend               *string
	ConfigFormat          string
	Deployment            deployment.Deployment
	EnvironmentDirectory  string
	HasConfig             map[string]bool
	Label                 string
	Members               []string
	Root                  metadata.LoadedPackRoot
	RemoteStateReferences []RemoteStateReference
	Tenant                string
	Topology              roots.RootTopology
}

// RenderEnvironmentSmokeTest ports the exported renderEnvironmentSmokeTest
// from node-src/domain/environment-generator.ts.
func RenderEnvironmentSmokeTest(options RenderEnvironmentSmokeTestOptions) (string, error) {
	members := canonjson.SortedStrings(options.Members)
	if len(members) == 0 {
		return "", fmt.Errorf("env root %s must contain at least one resource type", options.Label)
	}
	providerSet := map[string]bool{}
	for _, member := range members {
		provider, err := providerOf(options.Root, member)
		if err != nil {
			return "", err
		}
		providerSet[provider] = true
	}
	providers := canonjson.SortedStrings(mapKeysBoolSetGeneric(providerSet))
	if len(providers) != 1 {
		return "", fmt.Errorf("env root %s spans providers: %s", options.Label, strings.Join(providers, ", "))
	}
	provider := providers[0]

	lines := []string{
		"# GENERATED smoke test — the root composes and plans against a",
		fmt.Sprintf("# mocked provider; no credentials. Regenerate: make gen-env TENANT=%s", options.Tenant),
	}

	remoteByRoot := map[string]map[string]map[string]bool{}
	for _, reference := range options.RemoteStateReferences {
		resources, ok := remoteByRoot[reference.Root]
		if !ok {
			resources = map[string]map[string]bool{}
			remoteByRoot[reference.Root] = resources
		}
		keys, ok := resources[reference.ResourceType]
		if !ok {
			keys = map[string]bool{}
			resources[reference.ResourceType] = keys
		}
		keys[reference.Key] = true
	}
	remoteRoots := canonjson.SortedStrings(mapKeysOfNestedResourceSets(remoteByRoot))
	needsReferenceBackendVariable := options.Backend != nil && *options.Backend == "azurerm" && len(remoteRoots) > 0
	appendReferenceBackendVariable := func() {
		if !needsReferenceBackendVariable {
			return
		}
		lines = append(lines,
			fmt.Sprintf("    %s = {", ReferenceBackendVariable),
			`      resource_group_name  = "infrawright-test"`,
			`      storage_account_name = "infrawrighttest"`,
			`      container_name       = "tfstate"`,
			"      use_azuread_auth     = true",
			"    }",
		)
	}
	for _, root := range remoteRoots {
		lines = append(lines,
			"", "override_data {",
			fmt.Sprintf("  target = data.terraform_remote_state.%s", root),
			"  values = {", "    outputs = {",
			fmt.Sprintf("      %s = {", InfrawrightReferenceOutput),
		)
		for _, resourceType := range canonjson.SortedStrings(mapKeysOfBoolSets(remoteByRoot[root])) {
			lines = append(lines, fmt.Sprintf("        %s = {", resourceType))
			for _, key := range canonjson.SortedStrings(mapKeysBoolSetGeneric(remoteByRoot[root][resourceType])) {
				quoted, err := tfrender.RenderHclQuotedString(key)
				if err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("          %s = \"infrawright-test-reference-id\"", quoted))
			}
			lines = append(lines, "        }")
		}
		lines = append(lines, "      }", "    }", "  }", "}")
	}
	if len(remoteRoots) != 0 {
		lines = append(lines, "")
	}
	lines = append(lines,
		fmt.Sprintf(`mock_provider "%s" {}`, provider),
		"",
		`run "empty_plan" {`,
		"  command = plan",
		"",
		"  variables {",
	)
	for _, resourceType := range members {
		lines = append(lines, fmt.Sprintf("    %s = {}", variableName(options.Topology, resourceType)))
	}
	appendReferenceBackendVariable()
	lines = append(lines, "  }", "}")

	if options.ConfigFormat == "json" {
		var configured []string
		for _, resourceType := range members {
			if options.HasConfig[resourceType] {
				configured = append(configured, resourceType)
			}
		}
		if len(configured) > 0 {
			lines = append(lines, "", `run "config_plan" {`, "  command = plan", "", "  variables {")
			for _, resourceType := range configured {
				name := variableName(options.Topology, resourceType)
				reference, err := configReference(options.Deployment, options.Tenant, resourceType, options.EnvironmentDirectory)
				if err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("    %s = jsondecode(file(\"%s\")).%s", name, reference, name))
			}
			appendReferenceBackendVariable()
			lines = append(lines, "  }", "}")
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// GenerateEnvironmentRootsOptions bundles GenerateEnvironmentRoots's
// parameters, the Go analogue of the inline options-object parameter type
// node-src/domain/environment-generator.ts's generateEnvironmentRoots
// accepts. Backend/OutputRoot nil match `backend?: string`/`outputRoot?:
// string` being omitted; OnDiagnostic nil matches the TS source's
// `options.onDiagnostic ?? (() => undefined)` default.
type GenerateEnvironmentRootsOptions struct {
	Backend      *string
	Deployment   deployment.Deployment
	FormatHcl    HclFormatter
	OnDiagnostic func(string)
	OutputRoot   *string
	Root         metadata.LoadedPackRoot
	Selectors    []string
	Tenant       string
}

// GenerateEnvironmentRoots ports the exported generateEnvironmentRoots from
// node-src/domain/environment-generator.ts: "Generate deterministic
// Terraform roots and their expression overlays."
//
// Reviewer note on map/set iteration order: every Go map this function (and
// its helpers) builds in place of a TS Map/Set -- bindingsByType,
// remoteRootSet, hasConfig, crossState.OutputsByRoot, and so on -- is
// re-sorted via canonjson.SortedStrings at every point its keys are walked
// to produce output bytes or ordered diagnostics, exactly mirroring the TS
// source's own `sortedStrings(...keys())` calls at each of those same call
// sites. No emitted byte or diagnostic-ordering in this file depends on
// Go's (or JS's Map's) underlying iteration order; this was verified by
// walking every map construction against every corresponding read site
// during the port, not merely asserted.
func GenerateEnvironmentRoots(options GenerateEnvironmentRootsOptions) (EnvironmentGenerationResult, error) {
	if err := roots.ValidateTenant(options.Tenant); err != nil {
		return EnvironmentGenerationResult{}, err
	}
	onDiagnostic := options.OnDiagnostic
	if onDiagnostic == nil {
		onDiagnostic = func(string) {}
	}

	tenant := options.Tenant
	requestedResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Deployment: options.Deployment, Root: options.Root, Selectors: options.Selectors, Tenant: &tenant,
	})
	if err != nil {
		return EnvironmentGenerationResult{}, err
	}
	requestedTopology := requestedResult.Topology

	fullResult, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Deployment: options.Deployment, Root: options.Root, Selectors: []string{}, Tenant: &tenant,
	})
	if err != nil {
		return EnvironmentGenerationResult{}, err
	}
	fullTopology := fullResult.Topology

	crossState, err := ResolveCrossStateReferenceTopology(CrossStateReferenceTopologyOptions{
		Deployment: options.Deployment, Root: options.Root, Topology: fullTopology,
	})
	if err != nil {
		return EnvironmentGenerationResult{}, err
	}

	requestedLabels := make([]string, len(requestedTopology.Roots))
	for i, r := range requestedTopology.Roots {
		requestedLabels[i] = r.Label
	}
	generationLabels := CrossStateDependencyClosure(requestedLabels, crossState.DependenciesByRoot)

	rootsByLabel := map[string]roots.RootTopologyRoot{}
	for _, r := range fullTopology.Roots {
		rootsByLabel[r.Label] = r
	}
	generationRoots := make([]roots.RootTopologyRoot, 0, len(generationLabels))
	for _, label := range generationLabels {
		selected, ok := rootsByLabel[label]
		if !ok {
			return EnvironmentGenerationResult{}, fmt.Errorf("cross-state dependency root %s is absent from deployment topology", label)
		}
		generationRoots = append(generationRoots, selected)
	}
	topology := fullTopology

	tenantDirectory, err := tenantEnvironmentDirectory(options.Deployment, options.Tenant, options.OutputRoot)
	if err != nil {
		return EnvironmentGenerationResult{}, err
	}
	marker := path.Join(tenantDirectory, ".backend")
	backend := options.Backend
	if backend == nil && fileExists(marker) {
		raw, readErr := os.ReadFile(marker)
		if readErr != nil {
			return EnvironmentGenerationResult{}, readErr
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed != "" {
			backend = &trimmed
		}
	}
	if err := os.MkdirAll(tenantDirectory, 0o777); err != nil {
		return EnvironmentGenerationResult{}, err
	}
	if backend != nil && *backend != "" {
		if err := os.WriteFile(marker, []byte(*backend+"\n"), 0o666); err != nil {
			return EnvironmentGenerationResult{}, err
		}
	}

	var generated []GeneratedEnvironmentRoot
	for _, selectedRoot := range generationRoots {
		members := canonjson.SortedStrings(selectedRoot.Members)
		directory, err := environmentRootDirectory(options.Deployment, options.Tenant, selectedRoot.Label, options.OutputRoot)
		if err != nil {
			return EnvironmentGenerationResult{}, err
		}
		if err := os.MkdirAll(directory, 0o777); err != nil {
			return EnvironmentGenerationResult{}, err
		}

		bindingsByType := map[string][]ExpressionBinding{}
		for _, resourceType := range members {
			res, err := resourceMetadata(options.Root, resourceType)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			bindings, err := loadBindingLayers(options.Deployment, members, onDiagnostic, res, options.Tenant)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			if len(bindings) == 0 {
				continue
			}
			schema, err := options.Root.LoadResourceSchema(resourceType)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			if err := ValidateExpressionBindingSchemaPaths(schema, resourceType, bindings); err != nil {
				return EnvironmentGenerationResult{}, err
			}
			config, err := configFile(options.Deployment, options.Tenant, resourceType)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			if err := validateBindingsAgainstConfig(bindings, config, onDiagnostic, variableName(topology, resourceType)); err != nil {
				return EnvironmentGenerationResult{}, err
			}
			bindingsByType[resourceType] = bindings
		}
		if err := AssertNoExpressionBindingCycles(bindingsByType, selectedRoot.Label, members); err != nil {
			return EnvironmentGenerationResult{}, err
		}

		firstProvider, err := providerOf(options.Root, members[0])
		if err != nil {
			return EnvironmentGenerationResult{}, err
		}
		bindingMode := deployment.DeploymentReferenceBindingMode(options.Deployment, firstProvider)
		var remoteStateReferences []boundRemoteStateReference
		if bindingMode == deployment.ReferenceBindingCrossState {
			remoteStateReferences, err = remoteStateReferencesForBindings(bindingsByType)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
		}
		if err := validateRemoteStateReferences(crossState, selectedRoot.Label, fullTopology, remoteStateReferences); err != nil {
			return EnvironmentGenerationResult{}, err
		}

		remoteRootSet := map[string]bool{}
		for _, reference := range remoteStateReferences {
			remoteRootSet[reference.Root] = true
		}
		var remoteStates []EnvironmentRemoteState
		for _, label := range canonjson.SortedStrings(mapKeysBoolSetGeneric(remoteRootSet)) {
			remoteStates = append(remoteStates, EnvironmentRemoteState{
				Label:     label,
				LocalPath: nodePathRelative(directory, path.Join(tenantDirectory, label, "terraform.tfstate")),
			})
		}

		var mainBackend *string
		if backend != nil && *backend != "" {
			mainBackend = backend
		}
		expressionBindingTypes := canonjson.SortedStrings(bindingsByTypeKeys(bindingsByType))
		referenceOutputTypes := canonjson.SortedStrings(mapKeysBoolSetGeneric(crossState.OutputsByRoot[selectedRoot.Label]))
		mainSource, err := RenderEnvironmentMain(RenderEnvironmentMainOptions{
			Backend: mainBackend, Deployment: options.Deployment, EnvironmentDirectory: directory,
			ExpressionBindingTypes: expressionBindingTypes, Label: selectedRoot.Label, Members: members,
			ReferenceOutputTypes: referenceOutputTypes, RemoteStates: remoteStates, Root: options.Root,
			Tenant: options.Tenant, Topology: topology,
		})
		if err != nil {
			return EnvironmentGenerationResult{}, err
		}
		main, err := options.FormatHcl(mainSource)
		if err != nil {
			return EnvironmentGenerationResult{}, err
		}
		mainPath := path.Join(directory, "main.tf")
		if err := os.WriteFile(mainPath, []byte(main), 0o666); err != nil {
			return EnvironmentGenerationResult{}, err
		}
		onDiagnostic("wrote " + mainPath)

		expressionPath := path.Join(directory, expressionBindingsTF)
		if len(bindingsByType) > 0 {
			rendered, err := renderRootExpressionBindings(selectedRoot.Label, bindingsByType, topology)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			formatted, err := options.FormatHcl(rendered)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			if err := os.WriteFile(expressionPath, []byte(formatted), 0o666); err != nil {
				return EnvironmentGenerationResult{}, err
			}
			onDiagnostic("wrote " + expressionPath)
		} else {
			removed, err := removeIfPresent(expressionPath)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			if removed {
				onDiagnostic("removed stale " + expressionPath)
			}
		}

		readme, err := RenderEnvironmentReadme(RenderEnvironmentReadmeOptions{
			Deployment: options.Deployment, EnvironmentDirectory: directory, Label: selectedRoot.Label,
			Members: members, Tenant: options.Tenant, Topology: topology,
		})
		if err != nil {
			return EnvironmentGenerationResult{}, err
		}
		readmePath := path.Join(directory, "README.md")
		if err := os.WriteFile(readmePath, []byte(readme), 0o666); err != nil {
			return EnvironmentGenerationResult{}, err
		}

		testsDirectory := path.Join(directory, "tests")
		if err := os.MkdirAll(testsDirectory, 0o777); err != nil {
			return EnvironmentGenerationResult{}, err
		}
		hasConfig := map[string]bool{}
		for _, resourceType := range members {
			config, err := configFile(options.Deployment, options.Tenant, resourceType)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			hasConfig[resourceType] = fileExists(config)
		}
		tfvarsFormat, err := deployment.DeploymentTfvarsFormat(options.Deployment)
		if err != nil {
			return EnvironmentGenerationResult{}, err
		}
		var smokeReferences []RemoteStateReference
		for _, r := range remoteStateReferences {
			smokeReferences = append(smokeReferences, r.RemoteStateReference)
		}
		smokeSource, err := RenderEnvironmentSmokeTest(RenderEnvironmentSmokeTestOptions{
			Backend: mainBackend, ConfigFormat: tfvarsFormat, Deployment: options.Deployment,
			EnvironmentDirectory: directory, HasConfig: hasConfig, Label: selectedRoot.Label, Members: members,
			Root: options.Root, RemoteStateReferences: smokeReferences, Tenant: options.Tenant, Topology: topology,
		})
		if err != nil {
			return EnvironmentGenerationResult{}, err
		}
		smokeFormatted, err := options.FormatHcl(smokeSource)
		if err != nil {
			return EnvironmentGenerationResult{}, err
		}
		smokePath := path.Join(testsDirectory, "smoke.tftest.hcl")
		if err := os.WriteFile(smokePath, []byte(smokeFormatted), 0o666); err != nil {
			return EnvironmentGenerationResult{}, err
		}
		onDiagnostic("wrote " + smokePath)

		for _, resourceType := range members {
			if hasConfig[resourceType] {
				continue
			}
			file, err := configFile(options.Deployment, options.Tenant, resourceType)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			onDiagnostic(fmt.Sprintf(
				"NOTE %s: no config at %s — smoke test is STUB-only (composes + plans an empty root; does NOT exercise config). Materialize the config and re-run gen-env to upgrade it.",
				resourceType, file,
			))
		}

		generated = append(generated, GeneratedEnvironmentRoot{Label: selectedRoot.Label, Members: members, Path: directory})
	}

	result := EnvironmentGenerationResult{Roots: generated}
	if backend != nil && *backend != "" {
		result.Backend = backend
	}
	return result, nil
}
