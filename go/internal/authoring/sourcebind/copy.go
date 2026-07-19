package sourcebind

import (
	"errors"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

func cloneVerifiedState(state *verifiedState) VerifiedSnapshot {
	return VerifiedSnapshot{
		Manifest:              cloneSourceProvenance(state.Manifest),
		ManifestBytes:         cloneBytes(state.ManifestBytes),
		ManifestSHA256:        state.ManifestSHA256,
		Provider:              cloneCapturedTree(state.Provider),
		ProviderModule:        cloneCapturedFiles(state.ProviderModule),
		SDKs:                  cloneCapturedTrees(state.SDKs),
		TerraformSchema:       cloneCapturedFile(state.TerraformSchema),
		OpenAPI:               cloneOpenAPIStatus(state.OpenAPI),
		InputProvenance:       cloneInputProvenance(state.InputProvenance),
		InputProvenanceBytes:  cloneBytes(state.InputProvenanceBytes),
		InputProvenanceSHA256: state.InputProvenanceSHA256,
	}
}

func cloneCapturedFile(file CapturedFile) CapturedFile {
	return CapturedFile{Path: file.Path, Bytes: cloneBytes(file.Bytes), SHA256: file.SHA256}
}

func cloneCapturedFiles(files []CapturedFile) []CapturedFile {
	if files == nil {
		return nil
	}
	result := make([]CapturedFile, len(files))
	for index, file := range files {
		result[index] = cloneCapturedFile(file)
	}
	return result
}

func cloneCapturedTree(tree CapturedTree) CapturedTree {
	return CapturedTree{ModulePath: tree.ModulePath, Files: cloneCapturedFiles(tree.Files)}
}

func cloneCapturedTrees(trees map[string]CapturedTree) map[string]CapturedTree {
	if trees == nil {
		return nil
	}
	result := make(map[string]CapturedTree, len(trees))
	for modulePath, tree := range trees {
		result[modulePath] = cloneCapturedTree(tree)
	}
	return result
}

func cloneOpenAPIStatus(status OpenAPIStatus) OpenAPIStatus {
	return OpenAPIStatus{
		Available: status.Available,
		Files:     cloneCapturedFiles(status.Files),
		Err:       cloneStatusError(status.Err),
	}
}

func cloneStatusError(err error) error {
	if err == nil {
		return nil
	}
	var sourceError *Error
	if errors.As(err, &sourceError) {
		copied := *sourceError
		return &copied
	}
	return errors.New(err.Error())
}

func cloneSourceProvenance(value contracts.SourceProvenance) contracts.SourceProvenance {
	result := value
	result.Provider.Files = cloneFileBindings(value.Provider.Files)
	result.ProviderModule.GoSum = cloneFileBindingPointer(value.ProviderModule.GoSum)
	result.ProviderModule.LocalReplaces = cloneLocalReplaces(value.ProviderModule.LocalReplaces)
	result.SDKs = cloneSDKBindings(value.SDKs)
	result.Selection = cloneSelection(value.Selection)
	if value.OpenAPI != nil {
		openAPI := *value.OpenAPI
		openAPI.LocalRefs = cloneFileBindings(value.OpenAPI.LocalRefs)
		result.OpenAPI = &openAPI
	}
	return result
}

func cloneInputProvenance(value contracts.InputProvenance) contracts.InputProvenance {
	result := value
	result.SourceManifestSHA256 = cloneStringPointer(value.SourceManifestSHA256)
	if value.SourceManifest != nil {
		manifest := cloneSourceProvenance(*value.SourceManifest)
		result.SourceManifest = &manifest
	}
	if value.UnverifiedObservation != nil {
		observation := *value.UnverifiedObservation
		observation.ProviderFiles = cloneFileBindings(value.UnverifiedObservation.ProviderFiles)
		observation.SDKs = cloneUnverifiedSDKs(value.UnverifiedObservation.SDKs)
		observation.Selection = cloneSelection(value.UnverifiedObservation.Selection)
		result.UnverifiedObservation = &observation
	}
	return result
}

func cloneFileBindings(values []contracts.FileBinding) []contracts.FileBinding {
	if values == nil {
		return nil
	}
	return append([]contracts.FileBinding(nil), values...)
}

func cloneFileBindingPointer(value *contracts.FileBinding) *contracts.FileBinding {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneLocalReplaces(values []contracts.LocalModuleReplaceBinding) []contracts.LocalModuleReplaceBinding {
	if values == nil {
		return nil
	}
	result := make([]contracts.LocalModuleReplaceBinding, len(values))
	for index, value := range values {
		result[index] = value
		result[index].ModuleVersion = cloneStringPointer(value.ModuleVersion)
	}
	return result
}

func cloneSDKBindings(values []contracts.SDKSourceBinding) []contracts.SDKSourceBinding {
	if values == nil {
		return nil
	}
	result := make([]contracts.SDKSourceBinding, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Revision = cloneStringPointer(value.Revision)
		result[index].TreeSHA256 = cloneStringPointer(value.TreeSHA256)
		result[index].Files = cloneFileBindings(value.Files)
	}
	return result
}

func cloneSelection(value contracts.SelectionBinding) contracts.SelectionBinding {
	result := value
	result.ResourceTypes = append([]string(nil), value.ResourceTypes...)
	if value.Filters == nil {
		return result
	}
	result.Filters = make([]contracts.SelectionFilterBinding, len(value.Filters))
	for index, filter := range value.Filters {
		result.Filters[index] = filter
		result.Filters[index].Values = append([]string(nil), filter.Values...)
	}
	return result
}

func cloneUnverifiedSDKs(values []contracts.UnverifiedSDKObservation) []contracts.UnverifiedSDKObservation {
	if values == nil {
		return nil
	}
	result := make([]contracts.UnverifiedSDKObservation, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Files = cloneFileBindings(value.Files)
	}
	return result
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}
