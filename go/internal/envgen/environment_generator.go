package envgen

// environment_generator.go ports the original implementation:
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
//     go/internal/posixpath (which ports the DIFFERENT domain/paths.ts, used
//     elsewhere for tenant/config/imports/envs directory derivation, not
//     for this file's plain path.join/path.relative/path.isAbsolute calls).
//     No existing Go package ports node:path itself, so a small local POSIX
//     port (path.Join/path.IsAbs delegate directly to the stdlib "path"
//     package already used by go/internal/tfrender for the same reason;
//     path.relative has no stdlib POSIX-only equivalent, hence
//     nodePathRelative) is the least-surprising home for it.
//   - HclFormatter (the original implementation) -> LOCALLY DEFINED: the
//     sibling modulesgen port of that TS file is off-limits to this task
//     (per this port's brief); environment-generator.ts's only dependency
//     on it is this one function-type alias, reproduced verbatim here.
//   - REFERENCE_BACKEND_VARIABLE (the original implementation) ->
//     LOCALLY DEFINED: reference-backend.ts itself (a bounded-read/
//     azurerm-backend-config validator) is out of this port's three-file
//     scope; only its one exported constant is needed here.
//   - deploymentConfigDir/deploymentEnvsDir/deploymentModuleDir/
//     deploymentReferenceBindingMode/deploymentTfvarsFormat ->
//     deployment.DeploymentConfigDir/DeploymentEnvsDir/DeploymentModuleDir/
//     DeploymentReferenceBindingMode/DeploymentTfvarsFormat
//   - loadedRootTopology/validateTenant -> roots.LoadedRootTopology/
//     roots.ValidateTenant
//   - transformArtifactPaths (the original implementation) ->
//     tfrender.ComputeTransformArtifactPaths
//   - renderHclQuotedString (the original implementation) ->
//     tfrender.RenderHclQuotedString
//   - LoadedPackRoot/LoadedResourceMetadata -> metadata.LoadedPackRoot/
//     metadata.LoadedResourceMetadata
//   - applyExpressionBindings (its validation half; the transformed-items
//     result is not ported -- see ValidateExpressionBindingTargets)/
//     expressionModuleTargets/
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
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
	"github.com/dvmrry/infrawright-dev/go/internal/transform"
	"github.com/dvmrry/infrawright-dev/go/internal/transformrun"
)

// tokenFieldSegmentPattern mirrors the producer's identifier-segment rule
// for dotted reference-field names (tfrender's identifierSegmentPattern).
var tokenFieldSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// expressionBindingsTF ports EXPRESSION_BINDINGS_TF from
// the original implementation.
const expressionBindingsTF = "expression_bindings.tf"

const (
	staleDisabled  = "stale generated bindings ignored (cross_state_references disabled); rerun make transform to remove %s"
	staleNonmember = "stale generated binding ignored (target %s not in root members); rerun make transform to remove %s"
	cycleRemedy    = "resolve one direction via a literal ID or operator expression"
)

// ReferenceBackendVariable ports REFERENCE_BACKEND_VARIABLE from
// the original implementation (see this file's package doc
// comment for why it is reproduced locally rather than imported),
// renamed to the iw_ prefix. Generation emits only this name; the
// plan-side projection also sets the legacy TF_VAR alias so roots
// generated before the rename keep planning.
const ReferenceBackendVariable = "iw_remote_state_backend_config"

// HclFormatter matches the HclFormatter type from
// the original implementation (see this file's package doc comment for
// why it is reproduced locally rather than imported).
type HclFormatter func(source string) (string, error)

// GeneratedEnvironmentRoot is the Go analogue of one element of the
// `roots` array in the EnvironmentGenerationResult interface from
// the original implementation.
type GeneratedEnvironmentRoot struct {
	Label   string
	Members []string
	Path    string
}

// EnvironmentGenerationResult is the Go analogue of the
// EnvironmentGenerationResult interface in
// the original implementation. Backend is nil for the TS
// source's `backend: string | null` being null.
type EnvironmentGenerationResult struct {
	Roots   []GeneratedEnvironmentRoot
	Backend *string
}

// EnvironmentRemoteState is the Go analogue of the EnvironmentRemoteState
// interface in the original implementation.
type EnvironmentRemoteState struct {
	Label     string
	LocalPath string
}

// boundRemoteStateReference is the Go analogue of the
// BoundRemoteStateReference interface in
// the original implementation.
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
// path-deriving domain packages (e.g. go/internal/posixpath) are already
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
// the original implementation: "Match Python os.path.exists:
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
// the original implementation.
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
// the original implementation.
func resourceMetadata(root metadata.LoadedPackRoot, resourceType string) (metadata.LoadedResourceMetadata, error) {
	selected, ok := root.Resources[resourceType]
	if !ok {
		return metadata.LoadedResourceMetadata{}, fmt.Errorf("unknown active resource %s", resourceType)
	}
	return selected, nil
}

// providerOf ports providerOf from
// the original implementation.
func providerOf(root metadata.LoadedPackRoot, resourceType string) (string, error) {
	res, err := resourceMetadata(root, resourceType)
	if err != nil {
		return "", err
	}
	return res.Provider, nil
}

// variableName ports variableName from
// the original implementation.
func variableName(topology roots.RootTopology, resourceType string) string {
	if topology.ResourceRoots[resourceType] == resourceType {
		return "items"
	}
	return resourceType + "_items"
}

// tenantEnvironmentDirectory ports tenantEnvironmentDirectory from
// the original implementation. outputRoot nil matches the TS
// source's `outputRoot === undefined`.
func tenantEnvironmentDirectory(dep deployment.Deployment, tenant string, outputRoot *string) (string, error) {
	if outputRoot == nil {
		return deployment.DeploymentEnvsDir(dep, tenant)
	}
	return path.Join(*outputRoot, tenant), nil
}

// environmentRootDirectory ports environmentRootDirectory from
// the original implementation.
func environmentRootDirectory(dep deployment.Deployment, tenant, label string, outputRoot *string) (string, error) {
	tenantDirectory, err := tenantEnvironmentDirectory(dep, tenant, outputRoot)
	if err != nil {
		return "", err
	}
	return path.Join(tenantDirectory, label), nil
}

// moduleSource ports moduleSource from
// the original implementation.
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
// the original implementation.
func expressionLocal(label, resourceType string) string {
	if label == resourceType {
		return "iw_expression_bound_items"
	}
	return "iw_" + resourceType + "_expression_bound_items"
}

// renderRemoteStateBlocks ports renderRemoteStateBlocks from
// the original implementation. backend nil matches the TS
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
// the original implementation.
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
// the original implementation's renderEnvironmentMain accepts.
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
// the original implementation: "Render one complete
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
// the original implementation.
func RenderEnvironmentExpressionBindings(bindings []ExpressionBinding, label, resourceType string, topology roots.RootTopology) (string, error) {
	return RenderExpressionBindingsHcl(bindings, RenderExpressionBindingsHclOptions{
		ItemsVariable: variableName(topology, resourceType),
		LocalName:     expressionLocal(label, resourceType),
	})
}

// renderRootExpressionBindings ports the local renderRootExpressionBindings
// from the original implementation.
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

// canonicalRemoteStateSelectorPattern matches exactly the remote-state
// selector shape the binding grammar admits (see
// ExpressionRemoteStateReferences), capturing the referent type and the
// quoted key. Renderer-side resolver wrapping relies on this strictness:
// the grammar guarantees no other text can match. Both output-name
// spellings are admitted because a committed generated-bindings cache
// written before the iw_ rename embeds the legacy one and wins outright
// until its next transform stale-cleans it.
var canonicalRemoteStateSelectorPattern = regexp.MustCompile(
	`data\.terraform_remote_state\.[A-Za-z_][A-Za-z0-9_]*\.outputs\.(?:iw|infrawright)_reference_ids\.([A-Za-z_][A-Za-z0-9_]*)\[("(?:[^"\\]|\\.)*")\]`,
)

// committedTokenLeaf is one qualified reference token found in a member's
// committed config: the leaf-granular unit every render-time gate keys on.
// Path is empty for tokens found in HCL-format configs, which cannot be
// leaf-addressed without an HCL parser; those are covered value-level.
type committedTokenLeaf struct {
	ResourceType string
	ItemKey      string
	Path         string
	Token        string
	Referent     string
	Key          string
}

// scannedMember is one member's parsed committed items, retained so the
// dropped-edge orphan gate can re-walk them without a second parse.
type scannedMember struct {
	resourceType string
	items        map[string]any
}

// enumerateCommittedReferenceTokens finds every qualified reference token
// in the members' committed configs, structurally and leaf-granular for
// JSON tfvars (the same reference-field walk the producer uses) and
// value-granular for HCL tfvars (quoted strings validated against the
// referent's committed lookup, so an innocent dotted string is never counted
// as a token). Detection is deliberately independent of the deployment's
// binding mode -- it reads the pack's declared references directly -- so
// committed tokens are still seen, and refused loudly upstream, when
// cross-state binding has been switched off after the fact.
//
// Every member's config is read, not only the ones the CURRENT pack declares
// a reference edge for: an edge the pack has since retired leaves committed
// tokens behind at a field nothing enumerates, and skipping the file would
// hand those tokens straight to the module's opaque variable. The
// edge-independent half of that -- the dropped-edge orphan gate -- runs here
// too, so a broken token contract is refused before any binding is loaded.
func enumerateCommittedReferenceTokens(
	dep deployment.Deployment,
	tenant string,
	members []string,
	root metadata.LoadedPackRoot,
	topology roots.RootTopology,
) ([]committedTokenLeaf, error) {
	references := transform.MergedTransformReferences(root)
	configDirectory, err := deployment.DeploymentConfigDir(dep, tenant)
	if err != nil {
		return nil, err
	}
	lookupKeySet, err := committedBookKeys(configDirectory)
	if err != nil {
		return nil, err
	}
	var leaves []committedTokenLeaf
	var scanned []scannedMember
	for _, resourceType := range members {
		config, err := configFile(dep, tenant, resourceType)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(config)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if !strings.HasSuffix(config, ".json") {
			// Tokens are a JSON-format contract. An HCL config cannot be
			// leaf-verified, so a committed token in one is refused outright
			// -- fail closed, never a silent passthrough -- with no dependence
			// on binding mode.
			if claimed := hclCommittedTokenValue(string(raw), lookupKeySet); claimed != "" {
				return nil, fmt.Errorf(
					"%s is HCL-format tfvars and contains the reference-token-shaped value %q; tokenised configs are supported for JSON tfvars only -- convert the deployment's tfvars_format or restore literal IDs",
					config, claimed,
				)
			}
			// The lookup-membership rule cannot classify a referent with no
			// committed lookup, and silence there is not safe while the pack
			// edge is still DECLARED: the token would ride var.<items> to a
			// string-typed provider field with no gate having run.
			if referent, value := hclUnindexedReferentValue(
				string(raw), references[resourceType], lookupKeySet,
			); value != "" {
				return nil, fmt.Errorf(
					"%s is HCL-format tfvars and contains %q while %s has no committed lookup to decode it; tokenised configs are supported for JSON tfvars only -- convert the deployment's tfvars_format, restore literal IDs, or re-transform %s so its lookup is committed",
					config, value, referent, referent,
				)
			}
			continue
		}
		items, err := jsonConfigItems(raw, config, variableName(topology, resourceType))
		if err != nil {
			return nil, err
		}
		if items == nil {
			continue
		}
		if fields := references[resourceType]; len(fields) > 0 {
			leaves = append(leaves, jsonConfigTokenLeaves(items, resourceType, fields)...)
		}
		scanned = append(scanned, scannedMember{resourceType: resourceType, items: items})
	}
	if err := assertNoOrphanedTokenClaims(scanned, leaves, lookupKeySet); err != nil {
		return nil, err
	}
	return leaves, nil
}

// committedBookKeys inventories every lookup committed under the tenant's config
// directory -- at the current lookups/ location or the legacy top-level one --
// and reads each one's decodable key set.
//
// The inventory is deliberately NOT restricted to types the current pack
// declares a reference edge to: a retired edge removes the pack's record of a
// referent without removing the lookup, and that lookup is the only surviving
// evidence that committed values naming it are tokens. Conversely, a pack
// referent with no committed lookup contributes nothing -- with no key set there
// is nothing to validate a claim against, and shape alone is not evidence.
func committedBookKeys(configDirectory string) (map[string]map[string]bool, error) {
	referents, err := tfrender.CommittedLookupReferents(configDirectory)
	if err != nil {
		return nil, err
	}
	return tfrender.CommittedLookupKeys(configDirectory, referents)
}

// assertNoOrphanedTokenClaims is the dropped-edge gate. It walks EVERY string
// leaf of every member's committed items -- not just the leaves the current
// pack declares a reference edge for -- and refuses any value that decodes as
// a reference token no current edge governs.
//
// The disambiguator is lookup membership, not shape: a value counts as a token
// claim only when its prefix names a type with a committed lookup AND its
// remainder is a key that lookup decodes. That is exactly the set of values a
// past transform could have minted, so an innocent dotted string
// ("zpa_segment_group.note", where "note" is no lookup key) stays ignored.
//
// Lookup membership is only a sound signal because the producer keeps it one.
// Two guards in tfrender make "a decodable token stays decodable" an enforced
// invariant across pipeline operations: tokenDependents refuses to RETIRE a
// lookup while committed configs still reference its type, and
// assertNoLookupKeyStranding refuses to publish a lookup update that would DROP a
// key committed configs still name. The second was added because the first
// alone was not enough -- an ordinary referent re-transform shrinks the key
// set without removing the lookup, and the orphaned token went blind
// (adversarial-review finding, round 5). Neither guard exempts anybody: not a
// dependent the same run also selects (the runners publish per type and
// continue past a later skip or failure), and not the compiling type's own
// config, which is checked both as committed on disk and as this compile's
// pending output.
//
// The residual, named rather than claimed away: a lookup deleted or edited by
// hand OUTSIDE the pipeline. Detection then degrades to the pack's own edge
// metadata, which is exactly what a dropped edge removes.
//
// Refusal, never resolution: with no current edge there is no declared
// referrer->referent relationship to bind, and inventing one would be exactly
// the undeclared-edge shape validateRemoteStateReferences exists to reject.
// The remedy is a re-transform (which restores a literal ID) or restoring the
// pack edge.
func assertNoOrphanedTokenClaims(
	scanned []scannedMember,
	leaves []committedTokenLeaf,
	lookupKeySet map[string]map[string]bool,
) error {
	if len(lookupKeySet) == 0 {
		return nil
	}
	enumerated := make(map[string]bool, len(leaves))
	for _, leaf := range leaves {
		enumerated[leafIdentity(leaf.ResourceType, leaf.ItemKey, leaf.Path)] = true
	}
	for _, member := range scanned {
		for _, itemKey := range canonjson.SortedStrings(mapKeysAny(member.items)) {
			item, ok := member.items[itemKey].(map[string]any)
			if !ok {
				continue
			}
			var failure error
			collectStringLeaves(item, "", func(path, text string) {
				if failure != nil {
					return
				}
				referent, claimed := tokenClaimReferent(text, lookupKeySet)
				if !claimed || enumerated[leafIdentity(member.resourceType, itemKey, path)] {
					return
				}
				failure = fmt.Errorf(
					"committed token %q at %s.%s.%s is governed by no current pack reference edge, but %s still has a committed lookup that decodes it; re-run transform/adopt to restore a literal id, or restore the pack's reference edge for this field (an unresolvable token must never reach the module boundary)",
					text, member.resourceType, itemKey, path, referent,
				)
			})
			if failure != nil {
				return failure
			}
		}
	}
	return nil
}

func leafIdentity(resourceType, itemKey, path string) string {
	return resourceType + "\x00" + itemKey + "\x00" + path
}

// tokenClaimReferent reports the referent a committed value claims, using the
// producer's own split (an identifier segment, a dot, a remainder that may
// itself contain dots) plus lookup membership.
func tokenClaimReferent(value string, lookupKeySet map[string]map[string]bool) (string, bool) {
	dot := strings.IndexByte(value, '.')
	if dot <= 0 {
		return "", false
	}
	referent := value[:dot]
	if !tokenFieldSegmentPattern.MatchString(referent) {
		return "", false
	}
	keys, indexed := lookupKeySet[referent]
	if !indexed || !keys[value[dot+1:]] {
		return "", false
	}
	return referent, true
}

// collectStringLeaves reports every string leaf under value with the SAME
// path convention collectTokenLeaves uses, so the two walks address one leaf
// identically: intermediate lists are indexed, and a terminal list of strings
// reports the list's own path (the path a binding for it is written at).
func collectStringLeaves(value any, concretePath string, report func(path, text string)) {
	switch typed := value.(type) {
	case string:
		report(concretePath, typed)
	case map[string]any:
		for _, name := range canonjson.SortedStrings(mapKeysAny(typed)) {
			childPath := name
			if concretePath != "" {
				childPath = concretePath + "." + name
			}
			collectStringLeaves(typed[name], childPath, report)
		}
	case []any:
		for index, child := range typed {
			if text, ok := child.(string); ok {
				report(concretePath, text)
				continue
			}
			collectStringLeaves(child, fmt.Sprintf("%s[%d]", concretePath, index), report)
		}
	}
}

// hclCommittedTokenValue reports the first quoted string in an HCL config
// that a committed lookup decodes as a reference token -- prefix names a type
// with a lookup, remainder is a key that lookup decodes -- or "" when none
// exists. Referents are visited in sorted order so the reported value is
// deterministic across runs.
//
// Lookup membership is required here for the same reason the JSON scan requires
// it, and the omission was a real defect: a legitimate optional field such as
// a segment group's own `description` may hold a dotted string that opens with
// a pack referent type's name, and refusing on shape alone made an ordinary
// HCL deployment ungenerable (adversarial-review finding, round 5).
//
// A token whose key no lookup decodes is therefore NOT refused here. That case
// is not silent: the producer refuses to publish a lookup update that would drop
// a key committed configs still name (tfrender's assertNoLookupKeyStranding), so
// a decodable token stays decodable through every pipeline operation.
func hclCommittedTokenValue(text string, lookupKeySet map[string]map[string]bool) string {
	for _, referent := range canonjson.SortedStrings(mapKeysOfBookKeys(lookupKeySet)) {
		keys := lookupKeySet[referent]
		prefix := `"` + referent + `.`
		offset := 0
		for {
			index := strings.Index(text[offset:], prefix)
			if index < 0 {
				break
			}
			start := offset + index + 1
			end := strings.IndexByte(text[start:], '"')
			if end < 0 {
				break
			}
			value := text[start : start+end]
			if keys[value[len(referent)+1:]] {
				return value
			}
			offset = start + end
		}
	}
	return ""
}

// hclUnindexedReferentValue reports the first quoted string in an HCL config
// that opens with a referent THIS MEMBER declares a reference edge to and
// whose lookup is not committed anywhere, or ("", "") when none exists.
//
// This lane matches on shape alone, and must: with no lookup there is no key set
// to check membership against. It is the fail-closed path for an active edge
// whose referent has not been transformed yet -- without it, an HCL token at a
// declared reference field produced no claim, left the root untokenised, and
// reached the module's opaque variable ungated (adversarial-review finding,
// round 6).
//
// Confinement is MEMBER-level, not field-level, and deliberately so: an HCL
// document cannot be parsed here, so there is no way to tell which field a
// quoted value sits at. What narrows the surface is the candidate set -- only
// referents that fields the CURRENT pack declares on THIS member point at, and
// only those with no lookup anywhere. A member declaring no such edge is never
// scanned by this lane at all, and a indexed referent is left to the
// membership-based refusal above.
//
// So the accepted false-positive surface is: any quoted value ANYWHERE in an
// edge-declaring member's own config that happens to open with its declared
// referent's type name and a dot. That is wider than the reference field
// alone, and it is the price of shape-only matching over unparsed HCL. The
// remedy -- transform the referent so its lookup is committed -- is the
// operation such a tree needs anyway.
func hclUnindexedReferentValue(
	text string,
	fields map[string]any,
	lookupKeySet map[string]map[string]bool,
) (string, string) {
	unindexed := map[string]bool{}
	for _, raw := range fields {
		referent, ok := referentOfFieldSpec(raw)
		if !ok {
			continue
		}
		if _, indexed := lookupKeySet[referent]; !indexed {
			unindexed[referent] = true
		}
	}
	for _, referent := range canonjson.SortedStrings(mapKeysBoolSetGeneric(unindexed)) {
		prefix := `"` + referent + `.`
		index := strings.Index(text, prefix)
		if index < 0 {
			continue
		}
		start := index + 1
		end := strings.IndexByte(text[start:], '"')
		if end < 0 {
			continue
		}
		return referent, text[start : start+end]
	}
	return "", ""
}

func mapKeysOfBookKeys(m map[string]map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// jsonConfigItems parses one member's committed JSON tfvars and returns its
// item map, or nil when the file carries no such map. Split out of
// jsonConfigTokenLeaves so the reference-leaf enumeration and the
// dropped-edge orphan gate read one parse of one file rather than two.
func jsonConfigItems(raw []byte, config, itemsVariable string) (map[string]any, error) {
	parsed, err := canonjson.ParseDataJSONLosslessly(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", config, err)
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, nil
	}
	items, ok := object[itemsVariable].(map[string]any)
	if !ok {
		return nil, nil
	}
	return items, nil
}

// jsonConfigTokenLeaves walks one JSON config's declared reference fields
// with the producer's traversal rules (identifier-segment dotted paths,
// arrays fanned at every level) and reports each string leaf carrying the
// field's referent prefix.
func jsonConfigTokenLeaves(
	items map[string]any,
	resourceType string,
	fields map[string]any,
) []committedTokenLeaf {
	var leaves []committedTokenLeaf
	for _, field := range canonjson.SortedStrings(mapKeysAny(fields)) {
		if _, ok := referentOfFieldSpec(fields[field]); !ok {
			continue
		}
		segments := strings.Split(field, ".")
		for _, segment := range segments {
			if !tokenFieldSegmentPattern.MatchString(segment) {
				segments = []string{field}
				break
			}
		}
		for _, itemKey := range canonjson.SortedStrings(mapKeysAny(items)) {
			item, ok := items[itemKey].(map[string]any)
			if !ok {
				continue
			}
			collectTokenLeaves(item, segments, "", func(path, token string) {
				dot := strings.IndexByte(token, '.')
				leaves = append(leaves, committedTokenLeaf{
					ResourceType: resourceType, ItemKey: itemKey, Path: path,
					Token: token, Referent: token[:dot], Key: token[dot+1:],
				})
			})
		}
	}
	return leaves
}

// tokenShapedValue mirrors the producer's tokenShaped rule exactly: an
// identifier segment, a dot, a remainder -- so detection and minting can
// never classify the same string differently.
func tokenShapedValue(s string) bool {
	dot := strings.IndexByte(s, '.')
	return dot > 0 && tokenFieldSegmentPattern.MatchString(s[:dot])
}

// collectTokenLeaves mirrors the producer's substitution walk: descend by
// identifier segments, fan arrays at every level, and report terminal
// string values carrying the referent prefix. List-element leaves report
// the list's own path, matching the path bindings are addressed at.
func collectTokenLeaves(container map[string]any, segments []string, concretePath string, report func(path, token string)) {
	head := segments[0]
	leafPath := head
	if concretePath != "" {
		leafPath = concretePath + "." + head
	}
	// Any token-SHAPED string at a reference leaf is reported, not only the
	// field's currently-declared referent: a pack referent reassignment
	// leaves committed tokens carrying the old prefix, and those must fail
	// the coverage gate loudly rather than pass invisibly (adversarial
	// review finding). Reference-leaf values are never innocently dotted
	// (corpus audit, 2026-07-30), so shape alone is decisive here.
	if len(segments) == 1 {
		switch value := container[head].(type) {
		case []any:
			for _, child := range value {
				if text, ok := child.(string); ok && tokenShapedValue(text) {
					report(leafPath, text)
				}
			}
		case string:
			if tokenShapedValue(value) {
				report(leafPath, value)
			}
		}
		return
	}
	next, present := container[head]
	if !present {
		return
	}
	collectTokenLeavesThrough(next, segments[1:], leafPath, report)
}

func collectTokenLeavesThrough(value any, rest []string, concretePath string, report func(path, token string)) {
	switch typed := value.(type) {
	case map[string]any:
		collectTokenLeaves(typed, rest, concretePath, report)
	case []any:
		for index, child := range typed {
			if child == nil {
				continue
			}
			collectTokenLeavesThrough(child, rest, fmt.Sprintf("%s[%d]", concretePath, index), report)
		}
	}
}

// referentOfFieldSpec extracts the referent from one pack references entry,
// with the same shape-tolerance ResolveCrossStateReferenceTopology applies.
//
// Deliberately looser than transformrun.TransformReferenceSpecs, the seam
// transform's minting substitution and this package's own render-derivation
// (deriveGeneratedBindingLayer) both build on: that seam requires a field to
// declare BOTH referent and name_field before it is eligible to mint or bind
// a token. This function -- feeding jsonConfigTokenLeaves (committed-leaf
// enumeration for the totality gate) -- only needs a field's declared
// referent type to detect a token-shaped value, not whether the field is
// currently mintable, so it accepts referent-only entries too.
//
// The asymmetry is safe, not merely tolerated: a referent-only field (no
// name_field) can make jsonConfigTokenLeaves enumerate a leaf that
// transformrun.TransformReferenceSpecs has no entry for and therefore
// derivation can never bind -- but transform's own minting substitution is
// gated by that identical referent+name_field spec, so it could never have
// written a token-shaped value at such a field to begin with; the leaf this
// function over-enumerates is one production could never produce. If it
// somehow did, assertTokenLeavesCovered's totality gate refuses loudly for
// lacking a covering binding rather than passing it through silently.
//
// Over-enumeration is the safe direction; UNDER-enumeration is not, and this
// function cannot see the case that matters: a field whose reference entry
// the current pack has REMOVED entirely. No spec, loose or strict, reaches a
// leaf its own metadata no longer mentions, so that direction is closed
// elsewhere -- by assertNoOrphanedTokenClaims, which walks every string leaf
// and keys on committed-lookup membership instead of pack metadata.
func referentOfFieldSpec(raw any) (string, bool) {
	specification, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	referent, ok := specification["referent"].(string)
	return referent, ok && referent != ""
}

func mapKeysAny(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// assertTokenLeavesCovered is the render-time totality gate: every
// committed token must have a covering binding that actually RESOLVES it.
// A generated binding covers a token leaf only when its expression carries
// that token's canonical selector -- path adjacency alone is not enough,
// or a stale binding at the same path could silently resolve a different
// referent or key than the committed value (adversarial-review finding).
// Operator-owned bindings are exempt: an operator replacing a token's leaf
// by hand is asserting intent, exactly as with any other override.
func assertTokenLeavesCovered(
	label string,
	leaves []committedTokenLeaf,
	bindingsByType map[string][]ExpressionBinding,
	operatorIdentitiesByType map[string]map[string]bool,
) error {
	for _, leaf := range leaves {
		bindings := bindingsByType[leaf.ResourceType]
		operators := operatorIdentitiesByType[leaf.ResourceType]
		// Committed caches written before the iw_ rename spell the legacy
		// output name inside their selectors; either spelling proves the
		// binding resolves this leaf's token.
		needle := InfrawrightReferenceOutput + "." + leaf.Referent + `["` + leaf.Key + `"]`
		legacyNeedle := LegacyInfrawrightReferenceOutput + "." + leaf.Referent + `["` + leaf.Key + `"]`
		covered := false
		for _, binding := range bindings {
			if binding.Key != leaf.ItemKey || !tfrender.BindingPathCovers(binding.Path, leaf.Path) {
				continue
			}
			if operators[bindingIdentity(binding)] ||
				strings.Contains(binding.Expression, needle) ||
				strings.Contains(binding.Expression, legacyNeedle) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf(
				"root %s: committed token %q at %s.%s.%s has no binding that resolves it; re-run transform/adopt (a stale or divergent token must never reach the module boundary unresolved)",
				label, leaf.Token, leaf.ResourceType, leaf.ItemKey, leaf.Path,
			)
		}
	}
	return nil
}

// wrapResolverFallbacks rewrites every canonical remote-state selector in
// the surviving generated bindings into the lookup-first resolver form --
// try(<selector>, local.iw_reference_lookup_<referent>[<key>]) -- so
// state truth wins whenever it exists and the committed lookup serves any
// referent not yet applied, retiring automatically once it is. The
// selector arm keeps whichever output-name spelling the binding carried
// (a pre-rename cache's legacy selector still reads a pre-rename applied
// state), while the fallback arm always names the current lookup local,
// because that local is emitted by this same generation run. Operator
// bindings are never wrapped: an operator who wrote a remote-state
// reference by hand is asserting intent, and that assertion keeps failing
// loudly.
func wrapResolverFallbacks(
	bindingsByType map[string][]ExpressionBinding,
	operatorIdentitiesByType map[string]map[string]bool,
) {
	rewriteResolverFallbacks(bindingsByType, operatorIdentitiesByType, nil)
}

// rewriteResolverFallbacks applies the token resolver policy after state
// probing. A state-aware absent data referent cannot keep a
// terraform_remote_state selector: Terraform reads the data block before
// try() evaluates its expression, so the missing state would fail the plan.
// Such selectors are therefore rewritten to the lookup-only arm. Generated
// referents and usable data referents retain the ordinary try(remote, lookup)
// resolver, and operator-authored bindings remain untouched.
func rewriteResolverFallbacks(
	bindingsByType map[string][]ExpressionBinding,
	operatorIdentitiesByType map[string]map[string]bool,
	lookupOnlyReferents map[string]bool,
) {
	for resourceType, bindings := range bindingsByType {
		operators := operatorIdentitiesByType[resourceType]
		for i, binding := range bindings {
			if operators[bindingIdentity(binding)] {
				continue
			}
			bindings[i].Expression = canonicalRemoteStateSelectorPattern.ReplaceAllStringFunc(
				binding.Expression,
				func(selector string) string {
					parts := canonicalRemoteStateSelectorPattern.FindStringSubmatch(selector)
					if len(parts) != 3 {
						return selector
					}
					if lookupOnlyReferents[parts[1]] {
						return "local.iw_reference_lookup_" + parts[1] + "[" + parts[2] + "]"
					}
					return "try(" + selector + ", local.iw_reference_lookup_" + parts[1] + "[" + parts[2] + "])"
				},
			)
		}
	}
}

// warnUnwrappedLegacySelectors flags the one migration state the iw_ rename
// does not bridge in-language. An untokenised root's committed bindings
// cache wins outright and is never try()-wrapped (the byte-for-byte cache
// bridge), so a cache still spelling the legacy output name resolves only
// while each referent's applied state keeps publishing that name -- the
// referent's first post-rename apply renames the output and the referrer's
// next plan fails on the selector. The remedy is re-transforming the
// referrer (tokenising the config and retiring the cache); this warning
// names it at render time, before Terraform discovers it. Operator bindings
// are exempt for the same reason they are never wrapped: a hand-written
// remote-state reference is asserted intent.
func warnUnwrappedLegacySelectors(
	bindingsByType map[string][]ExpressionBinding,
	operatorIdentitiesByType map[string]map[string]bool,
	onDiagnostic func(string),
) {
	needle := ".outputs." + LegacyInfrawrightReferenceOutput + "."
	resourceTypes := make([]string, 0, len(bindingsByType))
	for resourceType := range bindingsByType {
		resourceTypes = append(resourceTypes, resourceType)
	}
	for _, resourceType := range canonjson.SortedStrings(resourceTypes) {
		operators := operatorIdentitiesByType[resourceType]
		count := 0
		for _, binding := range bindingsByType[resourceType] {
			if operators[bindingIdentity(binding)] {
				continue
			}
			if strings.Contains(binding.Expression, needle) {
				count++
			}
		}
		if count > 0 {
			onDiagnostic(fmt.Sprintf(
				"NOTE bindings: %s: %d generated binding(s) still reference the legacy %s output unwrapped; they resolve only while each referent's applied state keeps the legacy name -- re-run transform for %s before re-applying its referents",
				resourceType, count, LegacyInfrawrightReferenceOutput, resourceType,
			))
		}
	}
}

// referenceLookupLocals renders one plan-time lookup local per referent the
// wrapped resolvers name: a fileexists-guarded read of the committed lookup
// sidecar, preferring its id_by_key map and inverting key_by_id for lookups
// written before that field existed. The renderer emits only this
// expression -- the values stay in the committed artifact and are read
// where Terraform runs, never inlined here.
func referenceLookupLocals(
	dep deployment.Deployment,
	tenant, environmentDirectory string,
	referentTypes []string,
) (string, error) {
	if len(referentTypes) == 0 {
		return "", nil
	}
	lines := []string{"locals {"}
	for _, referent := range referentTypes {
		paths, err := tfrender.ComputeTransformArtifactPaths(dep, referent, tenant)
		if err != nil {
			return "", err
		}
		// The plan-time file() expression must name a file that actually
		// exists at generation time: the current path (config/<tenant>/
		// lookups/<type>.lookup.json) wins whenever it is present, but a
		// tenant that has not re-transformed since the Part B lookup
		// migration still only has the legacy path
		// (config/<tenant>/<type>.lookup.json) on disk, and pointing the
		// expression at the (absent) current path would make the
		// fileexists() guard always false -- silently dropping the lookup
		// fallback at plan time rather than reading it. Neither path
		// existing (a genuinely missing lookup) falls through to the current
		// path: fileexists() already guards that case at plan time, and any
		// tighter failure belongs to the totality/refusal gates, not here.
		bookPath := paths.Lookup
		if !fileExists(paths.Lookup) && fileExists(paths.LegacyLookup) {
			bookPath = paths.LegacyLookup
		}
		relative := nodePathRelative(environmentDirectory, bookPath)
		// The path rides inside a live "${path.module}/..." interpolation, so
		// it must not itself need escaping; deployment layouts that would are
		// refused rather than mis-quoted.
		if strings.ContainsAny(relative, "\"\\$%") {
			return "", fmt.Errorf("lookup sidecar path %s cannot be embedded in a reference lookup expression", relative)
		}
		quoted := `"${path.module}/` + relative + `"`
		lookup := "jsondecode(file(" + quoted + "))"
		lines = append(lines,
			fmt.Sprintf("  iw_reference_lookup_%s = fileexists(%s) ? try(%s.id_by_key, { for id, k in %s.key_by_id : k => id }) : {}",
				referent, quoted, lookup, lookup),
		)
	}
	lines = append(lines, "}", "")
	return strings.Join(lines, "\n"), nil
}

// configFile ports the local configFile helper from
// the original implementation.
func configFile(dep deployment.Deployment, tenant, resourceType string) (string, error) {
	paths, err := tfrender.ComputeTransformArtifactPaths(dep, resourceType, tenant)
	if err != nil {
		return "", err
	}
	return paths.Config, nil
}

// configReference ports the local configReference helper from
// the original implementation.
func configReference(dep deployment.Deployment, tenant, resourceType, environmentDirectory string) (string, error) {
	file, err := configFile(dep, tenant, resourceType)
	if err != nil {
		return "", err
	}
	return nodePathRelative(environmentDirectory, file), nil
}

// operatorBindingsFile ports the local operatorBindingsFile helper from
// the original implementation.
func operatorBindingsFile(dep deployment.Deployment, tenant, resourceType string) (string, error) {
	configDirectory, err := deployment.DeploymentConfigDir(dep, tenant)
	if err != nil {
		return "", err
	}
	return path.Join(configDirectory, resourceType+".expressions.json"), nil
}

// generatedBindingsFile ports the local generatedBindingsFile helper from
// the original implementation.
func generatedBindingsFile(dep deployment.Deployment, tenant, resourceType string) (string, error) {
	paths, err := tfrender.ComputeTransformArtifactPaths(dep, resourceType, tenant)
	if err != nil {
		return "", err
	}
	return paths.GeneratedBindings, nil
}

// validateBindingsAgainstConfig ports the local
// validateBindingsAgainstConfig helper from
// the original implementation.
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
	return ValidateExpressionBindingTargets(itemsObject, bindings)
}

// filterGeneratedBindings ports the local filterGeneratedBindings helper
// from the original implementation.
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
// the original implementation. Unlike most of this file's
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

// remoteStateReferenceValidationIndex is an immutable, invocation-scoped view
// of the topology and pack-declared cross-state edges used by every generated
// root's binding validation.
type remoteStateReferenceValidationIndex struct {
	declared       map[string]bool
	declaredFields map[string][]string
	rootsByLabel   map[string]roots.RootTopologyRoot
}

// remoteStateDeclaredEdgeKey preserves the Node port's exact composite key for
// matching a generated binding to its pack-declared reference edge.
func remoteStateDeclaredEdgeKey(
	referrer string,
	referrerRoot string,
	field string,
	referent string,
	referentRoot string,
) string {
	return referrer + "\x00" + referrerRoot + "\x00" + field + "\x00" + referent + "\x00" + referentRoot
}

// newRemoteStateReferenceValidationIndex builds the shared validation maps once
// for a GenerateEnvironmentRoots invocation.
func newRemoteStateReferenceValidationIndex(
	crossState CrossStateReferenceTopology,
	rootsByLabel map[string]roots.RootTopologyRoot,
) remoteStateReferenceValidationIndex {
	declared := make(map[string]bool, len(crossState.Edges))
	declaredFields := make(map[string][]string, len(crossState.Edges))
	for _, edge := range crossState.Edges {
		key := remoteStateDeclaredEdgeKey(
			edge.Referrer,
			edge.ReferrerRoot,
			edge.Field,
			edge.Referent,
			edge.ReferentRoot,
		)
		declared[key] = true
		identity := remoteStateDeclaredEdgeIdentity(edge.Referrer, edge.ReferrerRoot, edge.Referent, edge.ReferentRoot)
		declaredFields[identity] = append(declaredFields[identity], edge.Field)
	}
	return remoteStateReferenceValidationIndex{
		declared:       declared,
		declaredFields: declaredFields,
		rootsByLabel:   rootsByLabel,
	}
}

// remoteStateDeclaredEdgeIdentity keys an edge by everything except its
// field, so a binding can be matched against the declared fields of one
// referrer/referent pair.
func remoteStateDeclaredEdgeIdentity(referrer, referrerRoot, referent, referentRoot string) string {
	return referrer + "\x00" + referrerRoot + "\x00" + referent + "\x00" + referentRoot
}

// validateRemoteStateReferences ports the local
// validateRemoteStateReferences helper from
// the original implementation. The Go port receives a shared
// immutable index so a multi-root generation does not rebuild the complete
// topology and declared-edge maps for every generated root.
func validateRemoteStateReferences(
	index remoteStateReferenceValidationIndex,
	currentRoot string,
	references []boundRemoteStateReference,
) error {
	if len(references) == 0 {
		return nil
	}
	for _, reference := range references {
		target, ok := index.rootsByLabel[reference.Root]
		if !ok {
			return fmt.Errorf("cross-state binding targets unknown root %s", reference.Root)
		}
		if reference.Root == currentRoot {
			return fmt.Errorf("cross-state binding in %s targets its own state; use a module binding", currentRoot)
		}
		if !containsString(target.Members, reference.ResourceType) {
			return fmt.Errorf("cross-state binding expects %s in root %s", reference.ResourceType, reference.Root)
		}
		key := remoteStateDeclaredEdgeKey(
			reference.Referrer,
			currentRoot,
			reference.Field,
			reference.ResourceType,
			reference.Root,
		)
		// A binding may sit exactly on a declared field, or on a block leaf
		// above one. The leaf form is not a loosening but the only legal
		// spelling for a set-nested block: set members have no stable order,
		// the schema-path validator refuses an indexed path into one and
		// advises binding the complete block leaf, and the complete-leaf
		// binding carries the declared field beneath it. Referrer, referent,
		// and both roots stay pinned; only the field comparison admits
		// leaf-covers-declared.
		accepted := index.declared[key]
		if !accepted {
			identity := remoteStateDeclaredEdgeIdentity(
				reference.Referrer, currentRoot, reference.ResourceType, reference.Root,
			)
			for _, declaredField := range index.declaredFields[identity] {
				if strings.HasPrefix(declaredField, reference.Field+".") {
					accepted = true
					break
				}
			}
		}
		if !accepted {
			return fmt.Errorf(
				"cross-state binding %s.%s to %s in root %s is not declared by pack reference metadata",
				reference.Referrer, reference.Field, reference.ResourceType, reference.Root,
			)
		}
	}
	return nil
}

// loadBindingLayers ports the local loadBindingLayers helper from
// the original implementation, extended with the render-derivation bridge:
// the committed generated-bindings file is a CACHE of a pure function of
// (committed token, pack edges, provider schema, lookup), so when it is absent
// and the member's config carries tokens the generated layer is derived here
// instead. The three arms are deliberately exclusive and ordered:
//
//   - file present: today's exact path, byte for byte, whatever the file
//     says. A tree mid-migration keeps rendering from its committed cache
//     until transform stops writing one, and a cache that has gone stale
//     still fails through the same gates it always did rather than being
//     silently corrected under the operator.
//   - file absent, config carries tokens: derive -- the TOKENS, and only
//     them. Committed tokens name their referent and key outright, so the
//     derivation reads only committed artifacts and reproduces what
//     transform would have cached for those leaves. The trigger is
//     necessarily per resource type, so a mixed config drags its raw-ID
//     leaves through the deriver as well; BindingContext.TokensOnly is what
//     keeps them literal (see deriveGeneratedBindingLayer).
//   - file absent, no tokens: nothing. Raw-ID derivation stays
//     transform-only: an old-shape leaf carries a tenant ID that only the
//     lookup can translate, so deriving here would make an unchanged tree's
//     emitted root depend on lookup contents -- a silent behaviour change for
//     every tree that has not re-transformed.
//
// Byte parity between the first two arms is exact for an all-token config
// (pinned by TestDerivedBindingsMatchCommittedCacheByteForByte). For a mixed
// config they differ on purpose: the committed cache carries whatever raw-ID
// bindings the transform that wrote it derived and the bridge keeps serving
// them, while derivation emits token bindings only. A mixed tree reconciles
// the two by re-transforming.
//
// carriesTokens is the caller's per-type slice of the root's committed-token
// enumeration, not a second scan: one scan decides both this bridge and the
// totality gate, so the two can never disagree about what a token is.
func loadBindingLayers(
	dep deployment.Deployment,
	members []string,
	onDiagnostic func(string),
	resource metadata.LoadedResourceMetadata,
	tenant string,
	root metadata.LoadedPackRoot,
	topology roots.RootTopology,
	carriesTokens bool,
) ([]ExpressionBinding, map[string]bool, error) {
	// Operator bindings are resolved first so the caller can tell which merged
	// identities an operator owns. Probing a generated binding an operator
	// overrides would be wrong twice over: a probe error on its referent would
	// abort generation over a binding that is never emitted, and an absent
	// referent would report a fallback that never happened. The returned
	// identity set is what lets the state filter, which runs after every
	// validation gate, leave operator-owned bindings alone.
	operator, err := operatorBindingsFile(dep, tenant, resource.Type)
	if err != nil {
		return nil, nil, err
	}
	var operatorBindings []ExpressionBinding
	if fileExists(operator) {
		operatorBindings, err = LoadExpressionBindings(operator, resource.Type)
		if err != nil {
			return nil, nil, err
		}
	}
	overridden := make(map[string]bool, len(operatorBindings))
	for _, binding := range operatorBindings {
		overridden[bindingIdentity(binding)] = true
	}

	var layers [][]ExpressionBinding
	generated, err := generatedBindingsFile(dep, tenant, resource.Type)
	if err != nil {
		return nil, nil, err
	}
	mode := deployment.DeploymentReferenceBindingMode(dep, resource.Provider)
	switch {
	case fileExists(generated):
		if mode != deployment.ReferenceBindingDisabled {
			loaded, err := LoadExpressionBindings(generated, resource.Type)
			if err != nil {
				return nil, nil, err
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
	// Disabled mode derives nothing for the same reason it ignores a
	// committed cache. Tokens under a disabled mode are refused outright a
	// few gates later, naming the mismatch; deriving here would only race
	// that refusal with a resolver it must never emit.
	case carriesTokens && mode != deployment.ReferenceBindingDisabled:
		derived, err := deriveGeneratedBindingLayer(dep, root, topology, resource, tenant, onDiagnostic)
		if err != nil {
			return nil, nil, err
		}
		// No member filter runs over a derived layer: filterGeneratedBindings
		// exists to drop bindings a since-changed topology stranded in a
		// COMMITTED file, and a layer computed from this run's config and this
		// run's topology has no such staleness to carry.
		if len(derived) > 0 {
			layers = append(layers, derived)
		}
	}
	if len(operatorBindings) > 0 {
		layers = append(layers, operatorBindings)
	}
	return MergeExpressionBindingLayers(layers), overridden, nil
}

// deriveGeneratedBindingLayer recomputes one member's generated bindings
// from committed artifacts alone: the pack's declared reference edges, the
// referrer's provider schema (which decides set-block leaves), the
// deployment topology, the committed config items, and the referents'
// committed lookups. It is the same tfrender.DeriveGeneratedBindings transform
// runs, over the same BindingContext transform builds, so a tokenised leaf's
// expression is identical by construction rather than by agreement.
//
// The context differs from transform's in exactly one bit -- TokensOnly --
// and that bit governs only leaves still carrying raw tenant IDs, which
// render-derivation must leave literal. Over a fully tokenised config the two
// contexts are indistinguishable.
//
// The derived result is rendered and re-parsed through the very functions
// the committed file travels through (RenderGeneratedBindings ->
// ParseDataJSONLosslessly -> ParseExpressionBindings) rather than converted
// directly. That costs one JSON round trip per member and buys the parity
// contract outright: the bridge path and this path differ only by whether
// the bytes were written to disk in between, so every validation, escaping,
// and ordering rule the loader enforces lands identically on both.
func deriveGeneratedBindingLayer(
	dep deployment.Deployment,
	root metadata.LoadedPackRoot,
	topology roots.RootTopology,
	resource metadata.LoadedResourceMetadata,
	tenant string,
	onDiagnostic func(string),
) ([]ExpressionBinding, error) {
	references := transformrun.TransformReferenceSpecs(root, resource)
	if len(references) == 0 {
		return nil, nil
	}
	schema, err := root.LoadResourceSchema(resource.Type)
	if err != nil {
		return nil, err
	}
	context, err := transformrun.TransformBindingContext(
		dep, root, resource, topology.ResourceRoots, references, schema,
	)
	if err != nil {
		return nil, err
	}
	// The derivation trigger is per resource type, so one tokenised item pulls
	// every sibling in the same config through the deriver -- including leaves
	// still carrying raw tenant IDs. Those bind at transform, never here: see
	// tfrender.BindingContext.TokensOnly for why resolving one at render would
	// make an unchanged tree's emitted root a function of lookup contents.
	context.TokensOnly = true
	config, err := configFile(dep, tenant, resource.Type)
	if err != nil {
		return nil, err
	}
	items, err := loadConfigItems(config, variableName(topology, resource.Type))
	if err != nil {
		return nil, err
	}
	// The lookups are read from the config directory transform writes them to,
	// through transform's own resolver, so a referent whose lookup is missing
	// derives nothing and is reported -- never bound past.
	keyMaps, err := tfrender.LookupKeyMaps(path.Dir(config), references)
	if err != nil {
		return nil, err
	}
	result, err := tfrender.DeriveGeneratedBindings(context, items, keyMaps, resource.Type)
	if err != nil {
		return nil, err
	}
	// Same channel and same prefix transform publishes its derivation notes
	// on, so a bound/skipped accounting reads identically whichever half of
	// the pipeline produced it.
	for _, message := range result.Notes {
		onDiagnostic("NOTE bindings: " + message)
	}
	if len(result.Resources) == 0 {
		return nil, nil
	}
	rendered, err := tfrender.RenderGeneratedBindings(result.Resources)
	if err != nil {
		return nil, err
	}
	value, err := canonjson.ParseDataJSONLosslessly(rendered)
	if err != nil {
		return nil, err
	}
	return ParseExpressionBindings(value, resource.Type)
}

// loadConfigItems reads one member's committed JSON tfvars into the item map
// the deriver consumes -- the same lossless parse every other render-time
// reader of this file uses, so numbers keep their source lexemes and the
// derived expressions cannot drift from the ones transform derived over the
// pre-render values. A non-object item is skipped rather than refused: the
// schema and config gates downstream own that judgement, and they read the
// same file.
func loadConfigItems(config, itemsVariable string) (map[string]map[string]any, error) {
	raw, err := os.ReadFile(config)
	if err != nil {
		return nil, err
	}
	parsed, err := canonjson.ParseDataJSONLosslessly(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", config, err)
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, nil
	}
	items, ok := object[itemsVariable].(map[string]any)
	if !ok {
		return nil, nil
	}
	output := make(map[string]map[string]any, len(items))
	for key, value := range items {
		if record, ok := value.(map[string]any); ok {
			output[key] = record
		}
	}
	return output, nil
}

// filterStatelessBindings drops generated bindings whose referenced root has no
// usable state, in place, across every resource type in one root.
//
// It runs after the schema, config, cycle, and topology gates, never before.
// Dropping a binding is a fallback only because the tfvars literal underneath
// survives; evidence that is independently invalid -- a target leaf that does
// not exist, a path outside the provider schema, a referenced root that is not
// in the topology -- has no literal to fall back to, and filtering it first
// converts those refusals into a silent "fell back to the literal value".
// Validating the full merged set first is what stops state-awareness from
// laundering malformed generated evidence.
//
// A resource type whose bindings are all dropped is removed outright, so no
// empty overlay is written and no data block is emitted for it.
func filterStatelessBindings(
	bindingsByType map[string][]ExpressionBinding,
	operatorIdentitiesByType map[string]map[string]bool,
	probe StateProbe,
	onDiagnostic func(string),
) error {
	resourceTypes := make([]string, 0, len(bindingsByType))
	for resourceType := range bindingsByType {
		resourceTypes = append(resourceTypes, resourceType)
	}
	for _, resourceType := range canonjson.SortedStrings(resourceTypes) {
		bindings := bindingsByType[resourceType]
		operators := operatorIdentitiesByType[resourceType]
		candidates := make([]ExpressionBinding, 0, len(bindings))
		for _, binding := range bindings {
			if !operators[bindingIdentity(binding)] {
				candidates = append(candidates, binding)
			}
		}
		kept, err := filterStatelessGeneratedBindings(candidates, resourceType, probe, onDiagnostic)
		if err != nil {
			return err
		}
		keptIdentities := make(map[string]bool, len(kept))
		for _, binding := range kept {
			keptIdentities[bindingIdentity(binding)] = true
		}
		// Rebuild by walking the merged order rather than concatenating, so
		// filtering cannot reorder what MergeExpressionBindingLayers produced.
		surviving := make([]ExpressionBinding, 0, len(bindings))
		for _, binding := range bindings {
			identity := bindingIdentity(binding)
			if operators[identity] || keptIdentities[identity] {
				surviving = append(surviving, binding)
			}
		}
		if len(surviving) == 0 {
			delete(bindingsByType, resourceType)
			continue
		}
		bindingsByType[resourceType] = surviving
	}
	return nil
}

// cyclePathWithinRoot ports the local cyclePath helper from
// the original implementation (a DFS over expression-binding
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
// the original implementation.
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
// the original implementation's renderEnvironmentReadme
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
// the original implementation.
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
// the original implementation's renderEnvironmentSmokeTest
// accepts. ConfigFormat is "json" or "hcl", matching the TS source's
// literal union.
type RenderEnvironmentSmokeTestOptions struct {
	Backend               *string
	ConfigFormat          string
	Deployment            deployment.Deployment
	EnvironmentDirectory  string
	HasExpressionBindings bool
	HasConfig             map[string]bool
	Label                 string
	Members               []string
	Root                  metadata.LoadedPackRoot
	RemoteStateReferences []RemoteStateReference
	Tenant                string
	Topology              roots.RootTopology
}

// RenderEnvironmentSmokeTest ports the exported renderEnvironmentSmokeTest
// from the original implementation.
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
	var configured []string
	if options.ConfigFormat == "json" {
		for _, resourceType := range members {
			if options.HasConfig[resourceType] {
				configured = append(configured, resourceType)
			}
		}
	}

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
				// The mocked reference ID is a numeric string on purpose.
				// Terraform converts "20090001" to a number where the bound
				// attribute is number-typed (ZIA set-block ids) and leaves it
				// a string where it is string-typed (ZPA ids), so one
				// sentinel plans everywhere. A non-numeric sentinel fails
				// conversion at plan time for every number-typed reference.
				lines = append(lines, fmt.Sprintf("          %s = \"20090001\"", quoted))
			}
			lines = append(lines, "        }")
		}
		lines = append(lines, "      }", "    }", "  }", "}")
	}
	if len(remoteRoots) != 0 {
		lines = append(lines, "")
	}
	lines = append(lines, fmt.Sprintf(`mock_provider "%s" {}`, provider))
	// Expression overlays address configured item keys directly, so an empty
	// items map is not a valid plan input when a JSON config plan can replace it.
	// Retain the empty fallback when no runnable config plan can be emitted.
	if !options.HasExpressionBindings || len(configured) == 0 {
		lines = append(lines,
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
	return strings.Join(lines, "\n") + "\n", nil
}

// GenerateEnvironmentRootsOptions bundles GenerateEnvironmentRoots's
// parameters, the Go analogue of the inline options-object parameter type
// the original implementation's generateEnvironmentRoots
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
	// StateAware enables probing each referenced root's Terraform state
	// before a generated cross-state binding is kept. When false (the
	// default) generation never reads state, so its output cannot depend on
	// any tfstate contents.
	StateAware bool
	// StateProbe overrides how StateAware resolves a referenced root's
	// state. Nil consults StateProbeFor, then the local prober. It is the
	// direct injection seam for tests and library callers.
	StateProbe StateProbe
	// StateProbeFor builds the probe after the backend is resolved (flag or
	// .backend marker) -- the CLI's seam, since only generation can see the
	// marker. Returning (nil, nil) declines, selecting the local prober;
	// returning an error aborts the run, because a probe that cannot be
	// built must not degrade references. Consulted only when StateProbe is
	// nil.
	StateProbeFor func(backend *string) (StateProbe, error)
	Tenant        string
}

// GenerateEnvironmentRoots ports the exported generateEnvironmentRoots from
// the original implementation: "Generate deterministic
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
	remoteStateValidation := newRemoteStateReferenceValidationIndex(crossState, rootsByLabel)
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
	// A nil probe means generation never reads state, so its output cannot
	// depend on any tfstate contents. Resolved after the backend marker
	// because the backend determines where state lives.
	var probe StateProbe
	if options.StateAware {
		probe = options.StateProbe
		if probe == nil && options.StateProbeFor != nil {
			built, err := options.StateProbeFor(backend)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			probe = built
		}
		if probe == nil {
			// The local prober reads tenantDirectory/<label>/terraform.tfstate
			// directly and therefore only serves roots on local state. It
			// remains the default for library callers and tests; the CLI's
			// factory supplies a backend-backed prober when the resolved
			// backend is remote.
			probe = localStateProbe(tenantDirectory)
		}
		probe = memoizedStateProbe(probe)
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

		// The committed-token scan runs once, before any binding is loaded,
		// because two decisions now key on it: whether an absent
		// generated-bindings cache may be derived for a member (below) and
		// whether the root's token gates apply at all (further down). One scan
		// means the bridge and the totality gate can never disagree about what
		// counts as a token; it also means a config whose token contract is
		// broken outright -- token-shaped values in HCL tfvars, unparseable
		// JSON -- is refused before the renderer reads anything derived from it.
		tokenLeaves, err := enumerateCommittedReferenceTokens(
			options.Deployment, options.Tenant, members, options.Root, topology,
		)
		if err != nil {
			return EnvironmentGenerationResult{}, err
		}
		tokensPresent := len(tokenLeaves) > 0
		tokenisedTypes := map[string]bool{}
		for _, leaf := range tokenLeaves {
			tokenisedTypes[leaf.ResourceType] = true
		}

		bindingsByType := map[string][]ExpressionBinding{}
		operatorIdentitiesByType := map[string]map[string]bool{}
		for _, resourceType := range members {
			res, err := resourceMetadata(options.Root, resourceType)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			bindings, operatorIdentities, err := loadBindingLayers(
				options.Deployment, members, onDiagnostic, res, options.Tenant,
				options.Root, topology, tokenisedTypes[resourceType],
			)
			if err != nil {
				return EnvironmentGenerationResult{}, err
			}
			operatorIdentitiesByType[resourceType] = operatorIdentities
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
		lookupReferentTypes := map[string]bool{}
		if tokensPresent {
			for _, reference := range remoteStateReferences {
				lookupReferentTypes[reference.ResourceType] = true
			}
		}
		// Topology is validated against the unfiltered set: a binding naming a
		// root outside this topology is malformed evidence, not a root awaiting
		// apply, and must be refused whether or not state-awareness would drop
		// it for want of state.
		if err := validateRemoteStateReferences(remoteStateValidation, selectedRoot.Label, remoteStateReferences); err != nil {
			return EnvironmentGenerationResult{}, err
		}
		// Detection is binding-mode-independent on purpose: committed tokens
		// under a since-disabled mode would otherwise pass bare var.<name>
		// straight through the root's opaque variable.
		if tokensPresent && bindingMode != deployment.ReferenceBindingCrossState {
			return EnvironmentGenerationResult{}, fmt.Errorf(
				"root %s: committed config carries reference tokens but cross-state reference binding is disabled; re-run transform/adopt under the current mode to restore literal IDs, or re-enable cross_state_references",
				selectedRoot.Label,
			)
		}
		// Every gate has now run against the full merged set. Only here, with
		// the evidence proven well formed, may bindings be dropped for want of
		// state -- and the reference list is rebuilt from the survivors so no
		// data block outlives its binding. Tokenised generated referents remain
		// exempt because their resolver carries the lookup fallback in-language;
		// tokenised data referents are the narrow exception and are probed so an
		// unusable state can remove the remote-state read before rendering.
		lookupOnlyDataReferents := map[string]bool{}
		if probe != nil {
			if tokensPresent {
				for _, reference := range remoteStateReferences {
					if !dataReferent(options.Root, reference.ResourceType) {
						continue
					}
					result, err := probe(reference.Root, reference.ResourceType)
					if err != nil {
						return EnvironmentGenerationResult{}, err
					}
					if !result.Usable {
						lookupOnlyDataReferents[reference.ResourceType] = true
					}
				}
			} else {
				if err := filterStatelessBindings(bindingsByType, operatorIdentitiesByType, probe, onDiagnostic); err != nil {
					return EnvironmentGenerationResult{}, err
				}
			}
			if bindingMode == deployment.ReferenceBindingCrossState {
				remoteStateReferences, err = remoteStateReferencesForBindings(bindingsByType)
				if err != nil {
					return EnvironmentGenerationResult{}, err
				}
			}
		}
		// The totality gate runs against the final surviving bindings,
		// before wrapping (coverage matching reads the canonical selector
		// grammar): every committed token needs a covering binding, or an
		// unbound token would pass the root's opaque variable and -- on a
		// string-typed provider field -- flow to the provider as a literal.
		if tokensPresent {
			if err := assertTokenLeavesCovered(selectedRoot.Label, tokenLeaves, bindingsByType, operatorIdentitiesByType); err != nil {
				return EnvironmentGenerationResult{}, err
			}
		}
		// The wrap happens after every consumer of the canonical selector
		// grammar has parsed the unwrapped expressions (topology validation,
		// reference extraction, coverage): from here on the strings are
		// renderer output, not evidence.
		if tokensPresent {
			rewriteResolverFallbacks(bindingsByType, operatorIdentitiesByType, lookupOnlyDataReferents)
			if len(lookupOnlyDataReferents) > 0 && bindingMode == deployment.ReferenceBindingCrossState {
				remoteStateReferences, err = remoteStateReferencesForBindings(bindingsByType)
				if err != nil {
					return EnvironmentGenerationResult{}, err
				}
			}
		} else {
			warnUnwrappedLegacySelectors(bindingsByType, operatorIdentitiesByType, onDiagnostic)
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
			if tokensPresent {
				lookups, err := referenceLookupLocals(
					options.Deployment, options.Tenant, directory,
					canonjson.SortedStrings(mapKeysBoolSetGeneric(lookupReferentTypes)),
				)
				if err != nil {
					return EnvironmentGenerationResult{}, err
				}
				if lookups != "" {
					rendered = lookups + "\n" + rendered
				}
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
			EnvironmentDirectory: directory, HasConfig: hasConfig, HasExpressionBindings: len(bindingsByType) > 0,
			Label: selectedRoot.Label, Members: members,
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
