package assessment

// This file ports the original implementation: it turns the
// public root/deployment inputs into the narrow, immutable inputs consumed by
// saved-plan assessment and rechecks that root selection has not changed.

import (
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/deployment"
	"github.com/dvmrry/infrawright-dev/go/internal/envgen"
	"github.com/dvmrry/infrawright-dev/go/internal/metadata"
	"github.com/dvmrry/infrawright-dev/go/internal/posixpath"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/roots"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
	"github.com/dvmrry/infrawright-dev/go/internal/tfrender"
)

// ResolveSavedPlanAssessmentOptions supplies persisted-catalog inputs to the
// generic assessment resolver.
type ResolveSavedPlanAssessmentOptions struct {
	Workspace           string
	Deployment          deployment.Deployment
	ResourceSet         metadata.ResourceSet
	Tenant              *string
	Selectors           []string
	TerraformExecutable string
	BackendConfig       *string
	PolicyPath          *string
	ControlFiles        []controlevidence.BoundAssessmentControlFile
	TerraformShowLimits *terraformcmd.TerraformShowLimits
}

// ResolveLoadedSavedPlanAssessmentOptions supplies the active loaded pack to
// the operational assessment resolver.
type ResolveLoadedSavedPlanAssessmentOptions struct {
	Workspace           string
	Deployment          deployment.Deployment
	Root                metadata.LoadedPackRoot
	Tenant              *string
	Selectors           []string
	TerraformExecutable string
	BackendConfig       *string
	PolicyPath          *string
	ControlFiles        []controlevidence.BoundAssessmentControlFile
	TerraformShowLimits *terraformcmd.TerraformShowLimits
}

// SavedPlanAssessmentContext is the persisted-catalog context rechecked at
// both ends of a saved-plan assessment.
type SavedPlanAssessmentContext struct {
	Workspace   string
	Deployment  deployment.Deployment
	ResourceSet metadata.ResourceSet
	Tenant      *string
	Selectors   []string
}

// LoadedSavedPlanAssessmentContext is the active-pack context rechecked at
// both ends of an operational saved-plan assessment. Root is intentionally a
// shallow value copy, matching the Node source's retained LoadedPackRoot
// object rather than inventing a second pack loader snapshot.
type LoadedSavedPlanAssessmentContext struct {
	Workspace  string
	Deployment deployment.Deployment
	Root       metadata.LoadedPackRoot
	Tenant     *string
	Selectors  []string
}

// SavedPlanAssessmentRootInput is one selected saved plan and the exact input
// paths that bind it to generated configuration.
type SavedPlanAssessmentRootInput struct {
	Tenant               string
	Label                string
	Members              []string
	EnvDir               string
	SavedPlanPath        string
	FingerprintPath      string
	VarFiles             []string
	ReferenceOutputTypes []string
}

// SavedPlanAssessmentOptions is the materialized subset of assessment inputs
// produced by this parcel. Later assessment phases may add their own runtime
// limits without changing this resolver contract.
type SavedPlanAssessmentOptions struct {
	TerraformExecutable string
	Roots               []SavedPlanAssessmentRootInput
	BackendConfig       *string
	PolicyPath          *string
	ControlFiles        []controlevidence.BoundAssessmentControlFile
	Context             *SavedPlanAssessmentContext
	LoadedContext       *LoadedSavedPlanAssessmentContext
	TerraformShowLimits *terraformcmd.TerraformShowLimits
}

// ResolvedSavedPlanAssessment pairs materialized inputs with whole-root
// selection diagnostics.
type ResolvedSavedPlanAssessment struct {
	Assessment  SavedPlanAssessmentOptions
	Diagnostics []roots.WholeRootDiagnostic
}

func assessmentInputsFailure(code, message string) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: procerr.CategoryDomain,
		Message:  message,
	})
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStrings(values []string) []string {
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func copyDeploymentForAssessment(value deployment.Deployment) deployment.Deployment {
	rootConfigs := make(map[string]deployment.RootProviderConfig, len(value.Roots))
	for provider, config := range value.Roots {
		rootConfigs[provider] = deployment.RootProviderConfig{
			HasCrossStateReferences: config.HasCrossStateReferences,
			CrossStateReferences:    config.CrossStateReferences,
		}
	}
	return deployment.Deployment{
		Overlay:         value.Overlay,
		HasModuleDir:    value.HasModuleDir,
		ModuleDir:       value.ModuleDir,
		HasTfvarsFormat: value.HasTfvarsFormat,
		TfvarsFormat:    value.TfvarsFormat,
		Roots:           rootConfigs,
	}
}

func copyResourceSetForAssessment(value metadata.ResourceSet) metadata.ResourceSet {
	resources := make([]metadata.ResourceDescriptor, len(value.Resources))
	for index, resource := range value.Resources {
		resources[index] = resource
	}
	return metadata.ResourceSet{
		DeclaredProviders: cloneStrings(value.DeclaredProviders),
		Resources:         resources,
	}
}

// CopySavedPlanAssessmentContext returns the detached persisted-catalog
// context snapshot used by assessment.
func CopySavedPlanAssessmentContext(context SavedPlanAssessmentContext) SavedPlanAssessmentContext {
	return SavedPlanAssessmentContext{
		Workspace:   context.Workspace,
		Deployment:  copyDeploymentForAssessment(context.Deployment),
		ResourceSet: copyResourceSetForAssessment(context.ResourceSet),
		Tenant:      cloneString(context.Tenant),
		Selectors:   cloneStrings(context.Selectors),
	}
}

// CopyLoadedSavedPlanAssessmentContext returns the detached mutable fields of
// an active-pack context while retaining its loaded pack object by shallow
// value, exactly as the Node source does.
func CopyLoadedSavedPlanAssessmentContext(context LoadedSavedPlanAssessmentContext) LoadedSavedPlanAssessmentContext {
	return LoadedSavedPlanAssessmentContext{
		Workspace:  context.Workspace,
		Deployment: copyDeploymentForAssessment(context.Deployment),
		Root:       context.Root,
		Tenant:     cloneString(context.Tenant),
		Selectors:  cloneStrings(context.Selectors),
	}
}

func assessmentTfvarsSuffix(value deployment.Deployment) (string, error) {
	format := any("json")
	if value.HasTfvarsFormat {
		format = value.TfvarsFormat
	}
	switch format {
	case "json":
		return ".auto.tfvars.json", nil
	case "hcl":
		return ".auto.tfvars", nil
	default:
		return "", assessmentInputsFailure(
			"INVALID_DEPLOYMENT",
			"deployment tfvars_format must be 'json' or 'hcl' for assessment",
		)
	}
}

func assessmentConfigDirectory(value deployment.Deployment, tenant string) (string, error) {
	overlay, ok := value.Overlay.(string)
	if !ok {
		return "", assessmentInputsFailure(
			"INVALID_DEPLOYMENT",
			"deployment overlay must be a string for assessment",
		)
	}
	if overlay == "." {
		return posixpath.Join("config", tenant), nil
	}
	return posixpath.Join(overlay, "config", tenant), nil
}

func resolveAssessmentPath(workspace, candidate string) string {
	if filepath.IsAbs(candidate) {
		return candidate
	}
	return filepath.Join(workspace, candidate)
}

func copyDiagnosticsForAssessment(values []roots.WholeRootDiagnostic) []roots.WholeRootDiagnostic {
	copied := make([]roots.WholeRootDiagnostic, len(values))
	for index, diagnostic := range values {
		copied[index] = diagnostic
		copied[index].SelectedMembers = cloneStrings(diagnostic.SelectedMembers)
		copied[index].AdditionalMembers = cloneStrings(diagnostic.AdditionalMembers)
	}
	return copied
}

func materializeSavedPlanAssessmentRoots(
	supplied SavedPlanAssessmentContext,
) ([]SavedPlanAssessmentRootInput, []roots.WholeRootDiagnostic, error) {
	context := CopySavedPlanAssessmentContext(supplied)
	if !filepath.IsAbs(context.Workspace) {
		return nil, nil, assessmentInputsFailure(
			"INVALID_WORKSPACE",
			"assessment workspace must be absolute",
		)
	}
	materialized, err := roots.PlanRootsFromResourceSet(roots.PlanRootsOptions{
		Workspace:   context.Workspace,
		Deployment:  context.Deployment,
		ResourceSet: context.ResourceSet,
		Tenant:      context.Tenant,
		Selectors:   context.Selectors,
	})
	if err != nil {
		return nil, nil, err
	}
	selected := make([]roots.MaterializedPlanRoot, 0, len(materialized.Result.Roots))
	for _, root := range materialized.Result.Roots {
		if root.Artifacts.Tfplan.Exists {
			selected = append(selected, root)
		}
	}
	var suffix string
	if len(selected) > 0 {
		suffix, err = assessmentTfvarsSuffix(context.Deployment)
		if err != nil {
			return nil, nil, err
		}
	}
	result := make([]SavedPlanAssessmentRootInput, 0, len(selected))
	for _, root := range selected {
		configDirectory, err := assessmentConfigDirectory(context.Deployment, root.Tenant)
		if err != nil {
			return nil, nil, err
		}
		varFiles := make([]string, len(root.Members))
		for index, member := range root.Members {
			varFiles[index] = resolveAssessmentPath(
				context.Workspace,
				posixpath.Join(configDirectory, member+suffix),
			)
		}
		result = append(result, SavedPlanAssessmentRootInput{
			Tenant:          root.Tenant,
			Label:           root.Label,
			Members:         cloneStrings(root.Members),
			EnvDir:          resolveAssessmentPath(context.Workspace, root.EnvDir),
			SavedPlanPath:   resolveAssessmentPath(context.Workspace, root.Artifacts.Tfplan.Path),
			FingerprintPath: resolveAssessmentPath(context.Workspace, root.Artifacts.TfplanSources.Path),
			VarFiles:        varFiles,
		})
	}
	return result, copyDiagnosticsForAssessment(materialized.Diagnostics), nil
}

func sameStringSequence(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameAssessmentRoots(left, right []SavedPlanAssessmentRootInput) bool {
	if len(left) != len(right) {
		return false
	}
	for index, root := range left {
		other := right[index]
		if root.Tenant != other.Tenant ||
			root.Label != other.Label ||
			!sameStringSequence(root.Members, other.Members) ||
			root.EnvDir != other.EnvDir ||
			root.SavedPlanPath != other.SavedPlanPath ||
			root.FingerprintPath != other.FingerprintPath ||
			!sameStringSequence(root.VarFiles, other.VarFiles) ||
			!sameStringSequence(root.ReferenceOutputTypes, other.ReferenceOutputTypes) {
			return false
		}
	}
	return true
}

func assessmentContextChangedFailure() *procerr.ProcessFailure {
	return assessmentInputsFailure(
		"ASSESSMENT_CONTEXT_CHANGED",
		"saved-plan assessment context changed during assessment",
	)
}

// RecheckSavedPlanAssessmentContext rematerializes a persisted-catalog
// context and maps every discovery or comparison failure to the fixed,
// redacted context-change failure.
func RecheckSavedPlanAssessmentContext(
	context SavedPlanAssessmentContext,
	expectedRoots []SavedPlanAssessmentRootInput,
) (err error) {
	defer func() {
		if recover() != nil {
			err = assessmentContextChangedFailure()
		}
	}()
	current, _, materializeErr := materializeSavedPlanAssessmentRoots(context)
	if materializeErr != nil || !sameAssessmentRoots(expectedRoots, current) {
		return assessmentContextChangedFailure()
	}
	return nil
}

// MaterializeLoadedSavedPlanAssessmentRoots resolves active pack topology,
// saved-plan paths, generated var files, and cross-state output bindings.
func MaterializeLoadedSavedPlanAssessmentRoots(
	supplied LoadedSavedPlanAssessmentContext,
) ([]SavedPlanAssessmentRootInput, []roots.WholeRootDiagnostic, error) {
	context := CopyLoadedSavedPlanAssessmentContext(supplied)
	if !filepath.IsAbs(context.Workspace) {
		return nil, nil, assessmentInputsFailure(
			"INVALID_WORKSPACE",
			"assessment workspace must be absolute",
		)
	}
	selected, err := roots.LoadedPlanRoots(roots.LoadedPlanRootsOptions{
		Workspace:  context.Workspace,
		Deployment: context.Deployment,
		Root:       context.Root,
		Tenant:     context.Tenant,
		Selectors:  context.Selectors,
	})
	if err != nil {
		return nil, nil, err
	}
	fullTopology, err := roots.LoadedRootTopology(roots.LoadedRootTopologyOptions{
		Root:       context.Root,
		Deployment: context.Deployment,
		Tenant:     nil,
		Selectors:  []string{},
	})
	if err != nil {
		return nil, nil, err
	}
	referenceTopology, err := envgen.ResolveCrossStateReferenceTopology(
		envgen.CrossStateReferenceTopologyOptions{
			Deployment: context.Deployment,
			Root:       context.Root,
			Topology:   fullTopology.Topology,
		},
	)
	if err != nil {
		return nil, nil, err
	}
	result := make([]SavedPlanAssessmentRootInput, 0, len(selected.Result.Roots))
	for _, root := range selected.Result.Roots {
		if !root.Artifacts.Tfplan.Exists {
			continue
		}
		varFiles := make([]string, len(root.Members))
		for index, resourceType := range root.Members {
			paths, err := tfrender.ComputeTransformArtifactPaths(
				context.Deployment,
				resourceType,
				root.Tenant,
			)
			if err != nil {
				return nil, nil, err
			}
			varFiles[index] = resolveAssessmentPath(context.Workspace, paths.Config)
		}
		rootInput := SavedPlanAssessmentRootInput{
			Tenant:          root.Tenant,
			Label:           root.Label,
			Members:         cloneStrings(root.Members),
			EnvDir:          resolveAssessmentPath(context.Workspace, root.EnvDir),
			SavedPlanPath:   resolveAssessmentPath(context.Workspace, root.Artifacts.Tfplan.Path),
			FingerprintPath: resolveAssessmentPath(context.Workspace, root.Artifacts.TfplanSources.Path),
			VarFiles:        varFiles,
		}
		if outputTypes := referenceTopology.OutputsByRoot[root.Label]; len(outputTypes) > 0 {
			keys := make([]string, 0, len(outputTypes))
			for outputType := range outputTypes {
				keys = append(keys, outputType)
			}
			rootInput.ReferenceOutputTypes = canonjson.SortedStrings(keys)
		}
		result = append(result, rootInput)
	}
	return result, copyDiagnosticsForAssessment(selected.Diagnostics), nil
}

// RecheckLoadedSavedPlanAssessmentContext rematerializes an active-pack
// context and maps every discovery or comparison failure to the fixed,
// redacted context-change failure.
func RecheckLoadedSavedPlanAssessmentContext(
	context LoadedSavedPlanAssessmentContext,
	expectedRoots []SavedPlanAssessmentRootInput,
) (err error) {
	defer func() {
		if recover() != nil {
			err = assessmentContextChangedFailure()
		}
	}()
	current, _, materializeErr := MaterializeLoadedSavedPlanAssessmentRoots(context)
	if materializeErr != nil || !sameAssessmentRoots(expectedRoots, current) {
		return assessmentContextChangedFailure()
	}
	return nil
}

func copyControlFileForAssessment(
	file controlevidence.BoundAssessmentControlFile,
	includeLoadedFields bool,
) controlevidence.BoundAssessmentControlFile {
	result := controlevidence.BoundAssessmentControlFile{Path: file.Path}
	if file.Digest != nil {
		digest := *file.Digest
		result.Digest = &digest
	}
	if includeLoadedFields && file.Identity != nil {
		identity := *file.Identity
		result.Identity = &identity
	}
	if includeLoadedFields && file.FollowSymlinks != nil {
		followSymlinks := *file.FollowSymlinks
		result.FollowSymlinks = &followSymlinks
	}
	return result
}

func copyControlFilesForAssessment(
	files []controlevidence.BoundAssessmentControlFile,
	includeLoadedFields bool,
) []controlevidence.BoundAssessmentControlFile {
	copied := make([]controlevidence.BoundAssessmentControlFile, len(files))
	for index, file := range files {
		copied[index] = copyControlFileForAssessment(file, includeLoadedFields)
	}
	return copied
}

func copyTerraformShowLimits(value *terraformcmd.TerraformShowLimits) *terraformcmd.TerraformShowLimits {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func resolveOptionalAssessmentPath(workspace string, candidate *string) *string {
	if candidate == nil {
		return nil
	}
	resolved := resolveAssessmentPath(workspace, *candidate)
	return &resolved
}

type resolveSavedPlanAssessmentHooks struct {
	afterMaterialize func(*ResolveSavedPlanAssessmentOptions)
}

func resolveSavedPlanAssessment(
	options ResolveSavedPlanAssessmentOptions,
	hooks resolveSavedPlanAssessmentHooks,
) (ResolvedSavedPlanAssessment, error) {
	context := CopySavedPlanAssessmentContext(SavedPlanAssessmentContext{
		Workspace:   options.Workspace,
		Deployment:  options.Deployment,
		ResourceSet: options.ResourceSet,
		Tenant:      options.Tenant,
		Selectors:   options.Selectors,
	})
	// The generic resolver captures every non-topology option before its first
	// asynchronous discovery in Node. Keep that ordering even though the Go
	// discovery API is synchronous.
	captured := SavedPlanAssessmentOptions{
		TerraformExecutable: options.TerraformExecutable,
		BackendConfig:       cloneString(options.BackendConfig),
		PolicyPath:          cloneString(options.PolicyPath),
		ControlFiles:        copyControlFilesForAssessment(options.ControlFiles, false),
		Context:             &context,
		TerraformShowLimits: copyTerraformShowLimits(options.TerraformShowLimits),
	}
	materializedRoots, diagnostics, err := materializeSavedPlanAssessmentRoots(context)
	if err != nil {
		return ResolvedSavedPlanAssessment{}, err
	}
	if hooks.afterMaterialize != nil {
		hooks.afterMaterialize(&options)
	}
	captured.Roots = materializedRoots
	captured.BackendConfig = resolveOptionalAssessmentPath(context.Workspace, captured.BackendConfig)
	captured.PolicyPath = resolveOptionalAssessmentPath(context.Workspace, captured.PolicyPath)
	return ResolvedSavedPlanAssessment{
		Assessment:  captured,
		Diagnostics: diagnostics,
	}, nil
}

// ResolveSavedPlanAssessment resolves persisted topology and artifacts into
// the narrow assessment core.
func ResolveSavedPlanAssessment(
	options ResolveSavedPlanAssessmentOptions,
) (ResolvedSavedPlanAssessment, error) {
	return resolveSavedPlanAssessment(options, resolveSavedPlanAssessmentHooks{})
}

// ResolveSavedPlanAssessmentInputs returns only the materialized assessment
// options from ResolveSavedPlanAssessment.
func ResolveSavedPlanAssessmentInputs(
	options ResolveSavedPlanAssessmentOptions,
) (SavedPlanAssessmentOptions, error) {
	resolved, err := ResolveSavedPlanAssessment(options)
	if err != nil {
		return SavedPlanAssessmentOptions{}, err
	}
	return resolved.Assessment, nil
}

type resolveLoadedSavedPlanAssessmentHooks struct {
	afterMaterialize func(*ResolveLoadedSavedPlanAssessmentOptions)
}

func resolveLoadedSavedPlanAssessment(
	options ResolveLoadedSavedPlanAssessmentOptions,
	hooks resolveLoadedSavedPlanAssessmentHooks,
) (ResolvedSavedPlanAssessment, error) {
	context := CopyLoadedSavedPlanAssessmentContext(LoadedSavedPlanAssessmentContext{
		Workspace:  options.Workspace,
		Deployment: options.Deployment,
		Root:       options.Root,
		Tenant:     options.Tenant,
		Selectors:  options.Selectors,
	})
	selectedRoots, diagnostics, err := MaterializeLoadedSavedPlanAssessmentRoots(context)
	if err != nil {
		return ResolvedSavedPlanAssessment{}, err
	}
	if hooks.afterMaterialize != nil {
		hooks.afterMaterialize(&options)
	}
	// Deliberately capture these fields after materialization. The Node loaded
	// resolver has this observable timing asymmetry with its generic sibling;
	// changing it here would be a behavior change rather than cleanup.
	assessment := SavedPlanAssessmentOptions{
		TerraformExecutable: options.TerraformExecutable,
		Roots:               selectedRoots,
		BackendConfig:       resolveOptionalAssessmentPath(context.Workspace, options.BackendConfig),
		PolicyPath:          resolveOptionalAssessmentPath(context.Workspace, options.PolicyPath),
		ControlFiles:        copyControlFilesForAssessment(options.ControlFiles, true),
		LoadedContext:       &context,
		TerraformShowLimits: copyTerraformShowLimits(options.TerraformShowLimits),
	}
	return ResolvedSavedPlanAssessment{
		Assessment:  assessment,
		Diagnostics: copyDiagnosticsForAssessment(diagnostics),
	}, nil
}

// ResolveLoadedSavedPlanAssessment resolves the real active pack/deployment
// topology used by operational assessment.
func ResolveLoadedSavedPlanAssessment(
	options ResolveLoadedSavedPlanAssessmentOptions,
) (ResolvedSavedPlanAssessment, error) {
	return resolveLoadedSavedPlanAssessment(options, resolveLoadedSavedPlanAssessmentHooks{})
}
