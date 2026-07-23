package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"strings"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"golang.org/x/mod/module"
)

// DecodeSourceProvenance strictly decodes and validates source-provenance-v1
// under docs/go-authoring-port-roadmap.md §3.2.1.
func DecodeSourceProvenance(data []byte) (SourceProvenance, error) {
	var provenance SourceProvenance
	if err := decodeDocument(data, sourceProvenanceContract, sourceProvenanceSchema, &provenance); err != nil {
		return SourceProvenance{}, err
	}
	if err := ValidateSourceProvenance(provenance); err != nil {
		return SourceProvenance{}, err
	}
	rendered, err := RenderSourceProvenance(provenance)
	if err != nil || string(data) != rendered {
		return SourceProvenance{}, nonCanonicalDocumentError(sourceProvenanceContract)
	}
	return provenance, nil
}

// ValidateSourceProvenance enforces the trust, identity, ordering, and portable
// path invariants in docs/go-authoring-port-roadmap.md §3.2.1.
func ValidateSourceProvenance(provenance SourceProvenance) error {
	const contract = sourceProvenanceContract
	if provenance.Kind != "infrawright.source_provenance" {
		return semanticErrorf(contract, "$.kind", "must equal infrawright.source_provenance")
	}
	if provenance.SchemaVersion != 1 {
		return semanticErrorf(contract, "$.schema_version", "must equal 1")
	}
	if err := validateProviderBinding(provenance.Provider); err != nil {
		return err
	}
	if err := validateFileBinding(provenance.ProviderModule.GoMod, "$.provider_module.go_mod"); err != nil {
		return err
	}
	if provenance.ProviderModule.GoMod.Path != "go.mod" {
		return semanticErrorf(contract, "$.provider_module.go_mod.path", "must equal go.mod")
	}
	if provenance.ProviderModule.GoSum != nil {
		if err := validateFileBinding(*provenance.ProviderModule.GoSum, "$.provider_module.go_sum"); err != nil {
			return err
		}
		if provenance.ProviderModule.GoSum.Path != "go.sum" {
			return semanticErrorf(contract, "$.provider_module.go_sum.path", "must equal go.sum")
		}
	}
	if provenance.ProviderModule.LocalReplaces == nil {
		return semanticErrorf(contract, "$.provider_module.local_replaces", "must be an array")
	}
	if err := validateFileBinding(provenance.TerraformSchema, "$.terraform_schema"); err != nil {
		return err
	}
	if provenance.SDKs == nil {
		return semanticErrorf(contract, "$.sdks", "must be an array")
	}
	for index, sdk := range provenance.SDKs {
		if err := validateSDKBinding(sdk, index); err != nil {
			return err
		}
	}
	if err := validateSortedSDKs(provenance.SDKs); err != nil {
		return err
	}
	if err := validateUnavailableSDKs(provenance.UnavailableSDKs, provenance.SDKs); err != nil {
		return err
	}
	if err := validateLocalReplaces(provenance.ProviderModule.LocalReplaces, provenance.SDKs, provenance.UnavailableSDKs); err != nil {
		return err
	}
	if provenance.OpenAPI != nil {
		if err := validateFileBinding(provenance.OpenAPI.Document, "$.openapi.document"); err != nil {
			return err
		}
		if provenance.OpenAPI.LocalRefs == nil {
			return semanticErrorf(contract, "$.openapi.local_refs", "must be an array")
		}
		if err := validateFileBindings(provenance.OpenAPI.LocalRefs, "$.openapi.local_refs"); err != nil {
			return err
		}
	}
	return validateSelection(provenance.Selection)
}

// DecodeInputProvenance strictly decodes the verified/unverified emitted union
// from docs/go-authoring-port-roadmap.md §§3.2.1 and 3.5.
func DecodeInputProvenance(data []byte) (InputProvenance, error) {
	var provenance InputProvenance
	if err := decodeDocument(data, inputProvenanceContract, inputProvenanceSchema, &provenance); err != nil {
		return InputProvenance{}, err
	}
	if err := ValidateInputProvenance(provenance); err != nil {
		return InputProvenance{}, err
	}
	rendered, err := RenderInputProvenance(provenance)
	if err != nil || string(data) != rendered {
		return InputProvenance{}, nonCanonicalDocumentError(inputProvenanceContract)
	}
	return provenance, nil
}

// ValidateInputProvenance enforces the exclusive trust union required for
// input-provenance.json by docs/go-authoring-port-roadmap.md §3.2.1.
func ValidateInputProvenance(provenance InputProvenance) error {
	if provenance.Kind != "infrawright.input_provenance" || provenance.SchemaVersion != 1 {
		return semanticErrorf(inputProvenanceContract, "$", "input provenance kind/version is invalid")
	}
	switch provenance.SourceTrust {
	case SourceTrustVerified:
		if provenance.SourceManifestSHA256 == nil || !isSHA256(*provenance.SourceManifestSHA256) ||
			provenance.SourceManifest == nil || provenance.UnverifiedObservation != nil {
			return semanticErrorf(inputProvenanceContract, "$", "verified input provenance requires only manifest and digest")
		}
		if err := ValidateSourceProvenance(*provenance.SourceManifest); err != nil {
			return remapContract(err, inputProvenanceContract)
		}
		renderedManifest, err := RenderSourceProvenance(*provenance.SourceManifest)
		if err != nil || *provenance.SourceManifestSHA256 != sha256Text([]byte(renderedManifest)) {
			return semanticErrorf(inputProvenanceContract, "$.source_manifest_sha256", "must equal the canonical source manifest digest")
		}
		return nil
	case SourceTrustUnverified:
		if provenance.SourceManifestSHA256 != nil || provenance.SourceManifest != nil || provenance.UnverifiedObservation == nil {
			return semanticErrorf(inputProvenanceContract, "$", "unverified input provenance must contain only an observation")
		}
		return remapContract(validateUnverifiedObservation(*provenance.UnverifiedObservation), inputProvenanceContract)
	default:
		return semanticErrorf(inputProvenanceContract, "$.source_trust", "must be verified or unverified")
	}
}

func remapContract(err error, contract string) error {
	if err == nil {
		return nil
	}
	var contractErr *ContractError
	if errors.As(err, &contractErr) {
		return contractError(contractErr.Code, contract, contractErr.Path, contractErr.Detail)
	}
	return err
}

func validateUnverifiedObservation(observation UnverifiedSourceObservation) error {
	if observation.ProviderModulePath == "" || observation.ProviderFiles == nil || observation.SDKs == nil {
		return semanticErrorf(sourceProvenanceContract, "$.unverified_observation", "module and file arrays are required")
	}
	if err := validateModulePath(
		observation.ProviderModulePath,
		sourceProvenanceContract,
		"$.unverified_observation.provider_module_path",
	); err != nil {
		return err
	}
	if err := validateFileBindings(observation.ProviderFiles, "$.unverified_observation.provider_files"); err != nil {
		return err
	}
	if err := validateFileBinding(observation.TerraformSchema, "$.unverified_observation.terraform_schema"); err != nil {
		return err
	}
	modules := make([]string, len(observation.SDKs))
	for index, sdk := range observation.SDKs {
		base := "$.unverified_observation.sdks[" + integerText(index) + "]"
		if sdk.ModulePath == "" || sdk.ModuleVersion == "" || sdk.Files == nil {
			return semanticErrorf(sourceProvenanceContract, base, "module, version, and files are required")
		}
		if err := validateModuleVersion(sdk.ModulePath, sdk.ModuleVersion, sourceProvenanceContract, base); err != nil {
			return err
		}
		if err := validateFileBindings(sdk.Files, base+".files"); err != nil {
			return err
		}
		modules[index] = sdk.ModulePath
	}
	if err := validateSortedUnique(modules, "$.unverified_observation.sdks", "SDK module paths"); err != nil {
		return err
	}
	return validateSelection(observation.Selection)
}

func validateLocalReplaces(replaces []LocalModuleReplaceBinding, sdks []SDKSourceBinding, unavailable []UnavailableSDKBinding) error {
	keys := make([]string, len(replaces))
	sdkModules := make(map[string]struct{}, len(sdks))
	for _, sdk := range sdks {
		sdkModules[sdk.ModulePath] = struct{}{}
	}
	unavailableModules := make(map[string]struct{}, len(unavailable))
	for _, sdk := range unavailable {
		unavailableModules[sdk.ModulePath] = struct{}{}
	}
	for index, replacement := range replaces {
		base := "$.provider_module.local_replaces[" + integerText(index) + "]"
		if replacement.ModuleVersion == nil {
			if err := validateModulePath(replacement.ModulePath, sourceProvenanceContract, base+".module_path"); err != nil {
				return err
			}
		} else {
			if err := validateModuleVersion(replacement.ModulePath, *replacement.ModuleVersion, sourceProvenanceContract, base); err != nil {
				return err
			}
		}
		if !isPortableLocalReplacePath(replacement.LocalPath) {
			return semanticErrorf(sourceProvenanceContract, base+".local_path", "must be a normalized relative slash-separated path")
		}
		if _, exists := sdkModules[replacement.ModulePath]; !exists {
			if _, unavailable := unavailableModules[replacement.ModulePath]; unavailable {
				return semanticErrorf(sourceProvenanceContract, base+".module_path", "must not name an unavailable SDK module")
			}
			return semanticErrorf(sourceProvenanceContract, base+".module_path", "must name a bound SDK module")
		}
		version := ""
		if replacement.ModuleVersion != nil {
			version = *replacement.ModuleVersion
		}
		keys[index] = replacement.ModulePath + "\x00" + version
	}
	return validateSortedUnique(keys, "$.provider_module.local_replaces", "local replacements")
}

func validateUnavailableSDKs(unavailable []UnavailableSDKBinding, bound []SDKSourceBinding) error {
	if unavailable == nil {
		return nil
	}
	modules := make([]string, len(unavailable))
	for index, sdk := range unavailable {
		base := "$.unavailable_sdks[" + integerText(index) + "]"
		if err := validateModuleVersion(sdk.ModulePath, sdk.ModuleVersion, sourceProvenanceContract, base); err != nil {
			return err
		}
		for _, source := range bound {
			if packageInModule(sdk.ModulePath, source.ModulePath) || packageInModule(source.ModulePath, sdk.ModulePath) {
				return semanticErrorf(sourceProvenanceContract, base+".module_path", "must not overlap a manifest-bound SDK module")
			}
		}
		modules[index] = sdk.ModulePath
	}
	return validateSortedUnique(modules, "$.unavailable_sdks", "unavailable SDK module paths")
}

func validateProviderBinding(provider ProviderSourceBinding) error {
	const contract = sourceProvenanceContract
	if err := validateModulePath(provider.ModulePath, contract, "$.provider.module_path"); err != nil {
		return err
	}
	if provider.Files == nil {
		return semanticErrorf(contract, "$.provider.files", "must be an array")
	}
	if provider.Repository == "" {
		return semanticErrorf(contract, "$.provider.repository", "must be non-empty")
	}
	if provider.Revision == "" {
		return semanticErrorf(contract, "$.provider.revision", "must be non-empty")
	}
	if !isSHA256(provider.TreeSHA256) {
		return semanticErrorf(contract, "$.provider.tree_sha256", "must be a lowercase SHA-256")
	}
	if len(provider.Files) == 0 {
		return semanticErrorf(contract, "$.provider.files", "must contain analyzed files")
	}
	if looksAbsolutePath(provider.Repository) {
		return semanticErrorf(contract, "$.provider.repository", "must not contain an absolute local path")
	}
	return validateFileBindings(provider.Files, "$.provider.files")
}

func validateSDKBinding(sdk SDKSourceBinding, index int) error {
	base := "$.sdks[" + integerText(index) + "]"
	if err := validateModuleVersion(sdk.ModulePath, sdk.ModuleVersion, sourceProvenanceContract, base); err != nil {
		return err
	}
	if sdk.Files == nil {
		return semanticErrorf(sourceProvenanceContract, base+".files", "must be an array")
	}
	if sdk.Repository == "" {
		return semanticErrorf(sourceProvenanceContract, base+".repository", "must be non-empty")
	}
	if sdk.Revision == nil && sdk.TreeSHA256 == nil {
		return semanticErrorf(sourceProvenanceContract, base, "requires an SDK revision or tree digest")
	}
	if len(sdk.Files) == 0 {
		return semanticErrorf(sourceProvenanceContract, base+".files", "must contain analyzed files")
	}
	if sdk.TreeSHA256 != nil && !isSHA256(*sdk.TreeSHA256) {
		return semanticErrorf(sourceProvenanceContract, base+".tree_sha256", "must be a lowercase SHA-256")
	}
	if looksAbsolutePath(sdk.Repository) {
		return semanticErrorf(sourceProvenanceContract, base+".repository", "must not contain an absolute local path")
	}
	return validateFileBindings(sdk.Files, base+".files")
}

func validateSortedSDKs(sdks []SDKSourceBinding) error {
	modules := make([]string, len(sdks))
	for index, sdk := range sdks {
		modules[index] = sdk.ModulePath
	}
	return validateSortedUnique(modules, "$.sdks", "SDK module paths")
}

func validateSelection(selection SelectionBinding) error {
	if selection.ResourceTypes == nil {
		return semanticErrorf(sourceProvenanceContract, "$.selection.resource_types", "must be an array")
	}
	if err := validateSortedUnique(selection.ResourceTypes, "$.selection.resource_types", "resource types"); err != nil {
		return err
	}
	if selection.Filters == nil {
		return semanticErrorf(sourceProvenanceContract, "$.selection.filters", "must be an array")
	}
	names := make([]string, len(selection.Filters))
	selected := make(map[string]struct{}, len(selection.ResourceTypes))
	for _, resource := range selection.ResourceTypes {
		selected[resource] = struct{}{}
	}
	for index, filter := range selection.Filters {
		base := "$.selection.filters[" + integerText(index) + "]"
		if filter.Name == "" {
			return semanticErrorf(sourceProvenanceContract, base+".name", "must be non-empty")
		}
		if filter.Values == nil {
			return semanticErrorf(sourceProvenanceContract, base+".values", "must be an array")
		}
		if filter.Name == SelectionFilterReviewedNotApplicable && len(filter.Values) == 0 {
			return semanticErrorf(sourceProvenanceContract, base+".values", "reviewed_not_applicable must authorize at least one selected resource")
		}
		for valueIndex, value := range filter.Values {
			if value == "" {
				return semanticErrorf(sourceProvenanceContract, base+".values["+integerText(valueIndex)+"]", "must be non-empty")
			}
			if looksAbsolutePath(value) {
				return semanticErrorf(sourceProvenanceContract, base+".values["+integerText(valueIndex)+"]", "must not contain an absolute local path")
			}
			if filter.Name == SelectionFilterReviewedNotApplicable {
				if _, ok := selected[value]; !ok {
					return semanticErrorf(sourceProvenanceContract, base+".values["+integerText(valueIndex)+"]", "reviewed_not_applicable values must name selected resource types")
				}
			}
		}
		if err := validateSortedUnique(filter.Values, base+".values", "filter values"); err != nil {
			return err
		}
		names[index] = filter.Name
	}
	return validateSortedUnique(names, "$.selection.filters", "filter names")
}

func validateFileBindings(files []FileBinding, base string) error {
	paths := make([]string, len(files))
	for index, file := range files {
		if err := validateFileBinding(file, base+"["+integerText(index)+"]"); err != nil {
			return err
		}
		paths[index] = file.Path
	}
	return validateSortedUnique(paths, base, "file paths")
}

func validateFileBinding(file FileBinding, base string) error {
	if !isPortableRelativePath(file.Path) {
		return semanticErrorf(sourceProvenanceContract, base+".path", "must be a clean slash-separated relative path")
	}
	if !isSHA256(file.SHA256) {
		return semanticErrorf(sourceProvenanceContract, base+".sha256", "must be a lowercase SHA-256")
	}
	return nil
}

func validateModulePath(value, contract, location string) error {
	if module.CheckPath(value) != nil {
		return semanticErrorf(contract, location, "must be a valid Go module path")
	}
	return nil
}

func validateModuleVersion(modulePath, version, contract, base string) error {
	if module.Check(modulePath, version) != nil {
		return semanticErrorf(contract, base, "module path and version must form a valid Go module identity")
	}
	return nil
}

func validateSortedUnique(values []string, location, label string) error {
	return validateSortedUniqueForContract(values, location, label, sourceProvenanceContract)
}

func validateSortedUniqueForContract(values []string, location, label, contract string) error {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return semanticErrorf(contract, location, "%s must be unique", label)
		}
	}
	if !canonjson.SameStringSequence(values, canonjson.SortedStrings(values)) {
		return semanticErrorf(contract, location, "%s must be sorted", label)
	}
	return nil
}

func isPortableRelativePath(value string) bool {
	if value == "" || value == "." || strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') || looksAbsolutePath(value) {
		return false
	}
	return path.Clean(value) == value && !strings.HasPrefix(value, "../")
}

func isPortableLocalReplacePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') || looksAbsolutePath(value) {
		return false
	}
	return path.Clean(value) == value
}

func looksAbsolutePath(value string) bool {
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") || strings.HasPrefix(strings.ToLower(value), "file:") {
		return true
	}
	return len(value) >= 3 && isASCIILetter(value[0]) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			if value[index] < 'a' || value[index] > 'f' {
				return false
			}
		}
	}
	return true
}

// DecodeSourceEvidenceReport strictly decodes and validates the source
// partition in docs/go-authoring-port-roadmap.md §3.3.
func DecodeSourceEvidenceReport(data []byte) (SourceEvidenceReport, error) {
	var report SourceEvidenceReport
	if err := decodeDocument(data, sourceReportContract, sourceReportSchema, &report); err != nil {
		return SourceEvidenceReport{}, err
	}
	if err := ValidateSourceEvidenceReport(report); err != nil {
		return SourceEvidenceReport{}, err
	}
	rendered, err := RenderSourceEvidenceReport(report)
	if err != nil || string(data) != rendered {
		return SourceEvidenceReport{}, nonCanonicalDocumentError(sourceReportContract)
	}
	return report, nil
}

// ValidateSourceEvidenceReport enforces exact source partitions, evidence
// shapes, and trust projection from docs/go-authoring-port-roadmap.md §3.3.
func ValidateSourceEvidenceReport(report SourceEvidenceReport) error {
	if report.Kind != "infrawright.source_evidence_report" {
		return semanticErrorf(sourceReportContract, "$.kind", "must equal infrawright.source_evidence_report")
	}
	if report.SchemaVersion != 1 {
		return semanticErrorf(sourceReportContract, "$.schema_version", "must equal 1")
	}
	if err := validateReportTrust(report); err != nil {
		return err
	}
	if !isSHA256(report.InputProvenanceSHA256) {
		return semanticErrorf(sourceReportContract, "$.input_provenance_sha256", "must be a lowercase SHA-256")
	}
	if report.Resources == nil {
		return semanticErrorf(sourceReportContract, "$.resources", "must be an object")
	}
	resourceKeys := sortedMapKeys(report.Resources)
	for _, resource := range resourceKeys {
		if resource == "" {
			return semanticErrorf(sourceReportContract, "$.resources", "resource keys must be non-empty")
		}
	}
	sourceCounts, err := validateSourceRows(report)
	if err != nil {
		return err
	}
	return validateSourceSummary(report.Summary, sourceCounts)
}

// ValidateSourceEvidenceReportAgainstInput binds a source report to the exact
// input-provenance.json bytes required by docs/go-authoring-port-roadmap.md §3.5.
func ValidateSourceEvidenceReportAgainstInput(report SourceEvidenceReport, input InputProvenance) error {
	if err := ValidateSourceEvidenceReport(report); err != nil {
		return err
	}
	renderedInput, err := RenderInputProvenance(input)
	if err != nil {
		return semanticErrorf(sourceReportContract, "$.input_provenance_sha256", "input provenance is invalid")
	}
	if report.InputProvenanceSHA256 != sha256Text([]byte(renderedInput)) {
		return semanticErrorf(sourceReportContract, "$.input_provenance_sha256", "must bind the exact input provenance bytes")
	}
	if report.SourceTrust != input.SourceTrust || !equalOptionalString(report.SourceManifestSHA256, input.SourceManifestSHA256) {
		return semanticErrorf(sourceReportContract, "$.source_trust", "trust and manifest digest must match input provenance")
	}
	bindings := sourceBindingsFromInput(input)
	if !canonjson.SameStringSequence(sortedMapKeys(report.Resources), bindings.resourceTypes) {
		return semanticErrorf(sourceReportContract, "$.resources", "resource keys must exactly equal the selected resource types")
	}
	return validateReportSourceBindings(report, bindings)
}

type sourceInputBindings struct {
	resourceTypes         []string
	reviewedNotApplicable []string
	providerModulePath    string
	providerFiles         map[string]struct{}
	sdks                  []sourceSDKBinding
	unavailableSDKs       []sourceUnavailableSDKBinding
}

type sourceSDKBinding struct {
	modulePath string
	version    string
	files      map[string]struct{}
}

type sourceUnavailableSDKBinding struct {
	modulePath string
	version    string
}

func sourceBindingsFromInput(input InputProvenance) sourceInputBindings {
	if input.SourceTrust == SourceTrustVerified {
		manifest := input.SourceManifest
		bindings := sourceInputBindings{
			resourceTypes:         manifest.Selection.ResourceTypes,
			reviewedNotApplicable: reviewedNotApplicableValues(manifest.Selection),
			providerModulePath:    manifest.Provider.ModulePath,
			providerFiles:         fileBindingSet(manifest.Provider.Files),
			sdks:                  make([]sourceSDKBinding, len(manifest.SDKs)),
			unavailableSDKs:       make([]sourceUnavailableSDKBinding, len(manifest.UnavailableSDKs)),
		}
		for index, sdk := range manifest.SDKs {
			bindings.sdks[index] = sourceSDKBinding{
				modulePath: sdk.ModulePath,
				version:    sdk.ModuleVersion,
				files:      fileBindingSet(sdk.Files),
			}
		}
		for index, sdk := range manifest.UnavailableSDKs {
			bindings.unavailableSDKs[index] = sourceUnavailableSDKBinding{modulePath: sdk.ModulePath, version: sdk.ModuleVersion}
		}
		return bindings
	}

	observation := input.UnverifiedObservation
	bindings := sourceInputBindings{
		resourceTypes:         observation.Selection.ResourceTypes,
		reviewedNotApplicable: reviewedNotApplicableValues(observation.Selection),
		providerModulePath:    observation.ProviderModulePath,
		providerFiles:         fileBindingSet(observation.ProviderFiles),
		sdks:                  make([]sourceSDKBinding, len(observation.SDKs)),
	}
	for index, sdk := range observation.SDKs {
		bindings.sdks[index] = sourceSDKBinding{
			modulePath: sdk.ModulePath,
			version:    sdk.ModuleVersion,
			files:      fileBindingSet(sdk.Files),
		}
	}
	return bindings
}

func reviewedNotApplicableValues(selection SelectionBinding) []string {
	for _, filter := range selection.Filters {
		if filter.Name == SelectionFilterReviewedNotApplicable {
			return append([]string(nil), filter.Values...)
		}
	}
	return nil
}

func fileBindingSet(files []FileBinding) map[string]struct{} {
	set := make(map[string]struct{}, len(files))
	for _, file := range files {
		set[file.Path] = struct{}{}
	}
	return set
}

func validateReportSourceBindings(report SourceEvidenceReport, bindings sourceInputBindings) error {
	if err := validateReviewedNotApplicableAuthorization(report, bindings.reviewedNotApplicable); err != nil {
		return err
	}
	for _, resource := range sortedMapKeys(report.Resources) {
		row := report.Resources[resource]
		base := "$.resources." + resource
		if row.ProviderRegistration != nil && !symbolIsBound(*row.ProviderRegistration, bindings) {
			return semanticErrorf(sourceReportContract, base+".provider_registration", "must name a manifest-bound provider declaration")
		}
		if row.ReadCallback != nil && !symbolIsBound(*row.ReadCallback, bindings) {
			return semanticErrorf(sourceReportContract, base+".read_callback", "must name a manifest-bound provider declaration")
		}
		for index, chain := range row.Chains {
			if err := validateChainSourceBindings(
				chain,
				bindings,
				base+".chains["+integerText(index)+"]",
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateReviewedNotApplicableAuthorization(report SourceEvidenceReport, authorized []string) error {
	authorizedSet := make(map[string]struct{}, len(authorized))
	for _, resource := range authorized {
		authorizedSet[resource] = struct{}{}
		row := report.Resources[resource]
		if row.Classification != SourceNotApplicable || !reasonIs(row.ReasonCode, ReasonReviewedNotApplicable) {
			return semanticErrorf(
				sourceReportContract,
				"$.resources."+resource,
				"reviewed_not_applicable authorization requires a matching not_applicable row",
			)
		}
	}
	for _, resource := range sortedMapKeys(report.Resources) {
		row := report.Resources[resource]
		if row.Classification != SourceNotApplicable {
			continue
		}
		if _, ok := authorizedSet[resource]; !ok {
			return semanticErrorf(
				sourceReportContract,
				"$.resources."+resource,
				"not_applicable requires exact reviewed_not_applicable selection authorization",
			)
		}
	}
	return nil
}

func validateChainSourceBindings(
	chain SourceEvidenceChain,
	bindings sourceInputBindings,
	base string,
) error {
	var firstSDK *sourceSDKBinding
	var firstSDKCallee *SourceSymbol
	for index, step := range chain.Steps {
		stepBase := base + ".steps[" + integerText(index) + "]"
		if !symbolIsBound(step.Caller, bindings) {
			return semanticErrorf(sourceReportContract, stepBase+".caller", "must name an exact manifest-bound declaration")
		}
		if !locationIsBound(step.Location, bindings) {
			return semanticErrorf(sourceReportContract, stepBase+".location", "must name its exact manifest-bound source tree and file")
		}
		if step.Callee != nil && !symbolIsBound(*step.Callee, bindings) {
			return semanticErrorf(sourceReportContract, stepBase+".callee", "must name an exact manifest-bound declaration")
		}
		if step.Kind == CallSDKPackageFunction || step.Kind == CallSDKReceiverMethod {
			sdk, ok := sdkForPackage(bindings.sdks, *step.ImportPath)
			if !ok {
				return semanticErrorf(sourceReportContract, stepBase+".import_path", "must name a package in a manifest-bound SDK module")
			}
			if step.Callee.Location.SDKModulePath == nil || *step.Callee.Location.SDKModulePath != sdk.modulePath {
				return semanticErrorf(sourceReportContract, stepBase+".callee.location.sdk_module_path", "must equal the longest-prefix SDK module for import_path")
			}
			if step.Callee.PackagePath != *step.ImportPath {
				return semanticErrorf(sourceReportContract, stepBase+".callee.package_path", "must equal import_path")
			}
			if firstSDK == nil {
				firstSDK = &sdk
				firstSDKCallee = step.Callee
			} else if firstSDK.modulePath != sdk.modulePath {
				return semanticErrorf(sourceReportContract, stepBase+".import_path", "cross-SDK call-chain transitions are unsupported and fail closed")
			}
		} else if step.Kind == CallSDKSourceMissing {
			if _, ok := sdkForPackage(bindings.sdks, *step.ImportPath); ok {
				return semanticErrorf(sourceReportContract, stepBase+".import_path", "sdk_source_missing requires no manifest-bound SDK source owner")
			}
			if _, ok := unavailableSDKForPackage(bindings.unavailableSDKs, *step.ImportPath); !ok {
				return semanticErrorf(sourceReportContract, stepBase+".import_path", "sdk_source_missing requires an explicit unavailable SDK owner")
			}
		}
	}
	if firstSDK != nil {
		if chain.SDKCall == nil || chain.SDKCall.ModulePath != firstSDK.modulePath ||
			chain.SDKCall.ModuleVersion != firstSDK.version || chain.SDKCall.PackagePath != firstSDKCallee.PackagePath ||
			chain.SDKCall.Symbol != firstSDKCallee.Symbol || !equalSourceLocation(chain.SDKCall.Location, firstSDKCallee.Location) {
			return semanticErrorf(sourceReportContract, base+".sdk_call", "must exactly identify the first provider-to-SDK callee declaration")
		}
	}

	var boundSDK sourceSDKBinding
	if chain.SDKCall != nil {
		var ok bool
		boundSDK, ok = sdkByIdentity(bindings.sdks, chain.SDKCall.ModulePath, chain.SDKCall.ModuleVersion)
		if !ok {
			return semanticErrorf(sourceReportContract, base+".sdk_call", "module path and version must equal a manifest-bound SDK identity")
		}
		if !packageInModule(chain.SDKCall.PackagePath, boundSDK.modulePath) {
			return semanticErrorf(sourceReportContract, base+".sdk_call.package_path", "must name a package in the bound SDK module")
		}
		if !fileIsBound(boundSDK.files, chain.SDKCall.Location.Path) {
			return semanticErrorf(sourceReportContract, base+".sdk_call.location.path", "must name a file in the bound SDK module")
		}
		if !chainHasSDKPackageStep(chain, chain.SDKCall.PackagePath) {
			return semanticErrorf(sourceReportContract, base+".sdk_call.package_path", "must equal a resolved SDK call-step package")
		}
	}

	if chain.Endpoint == nil {
		return nil
	}
	if chain.Endpoint.Origin == EndpointOriginProvider {
		if chain.Endpoint.Location.Origin != SourceLocationProvider || !locationIsBound(chain.Endpoint.Location, bindings) {
			return semanticErrorf(sourceReportContract, base+".endpoint.location.path", "provider endpoint must name a manifest-bound provider file")
		}
		return nil
	}
	if chain.SDKCall == nil || chain.Endpoint.Location.Origin != SourceLocationSDK ||
		chain.Endpoint.Location.SDKModulePath == nil || *chain.Endpoint.Location.SDKModulePath != boundSDK.modulePath ||
		!locationIsBound(chain.Endpoint.Location, bindings) {
		return semanticErrorf(sourceReportContract, base+".endpoint.location.path", "SDK endpoint must name a file in its manifest-bound SDK module")
	}
	return nil
}

func locationIsBound(location SourceLocation, bindings sourceInputBindings) bool {
	if location.Origin == SourceLocationProvider {
		return location.SDKModulePath == nil && fileIsBound(bindings.providerFiles, location.Path)
	}
	if location.Origin != SourceLocationSDK || location.SDKModulePath == nil {
		return false
	}
	for _, sdk := range bindings.sdks {
		if sdk.modulePath == *location.SDKModulePath {
			return fileIsBound(sdk.files, location.Path)
		}
	}
	return false
}

func symbolIsBound(symbol SourceSymbol, bindings sourceInputBindings) bool {
	if !locationIsBound(symbol.Location, bindings) {
		return false
	}
	modulePath := bindings.providerModulePath
	if symbol.Location.Origin == SourceLocationSDK {
		modulePath = *symbol.Location.SDKModulePath
	}
	return symbol.PackagePath == packagePathForFile(modulePath, symbol.Location.Path)
}

func packagePathForFile(modulePath, filePath string) string {
	directory := path.Dir(filePath)
	if directory == "." {
		return modulePath
	}
	return modulePath + "/" + directory
}

func fileIsBound(files map[string]struct{}, path string) bool {
	_, ok := files[path]
	return ok
}

func sdkByIdentity(sdks []sourceSDKBinding, modulePath, version string) (sourceSDKBinding, bool) {
	for _, sdk := range sdks {
		if sdk.modulePath == modulePath && sdk.version == version {
			return sdk, true
		}
	}
	return sourceSDKBinding{}, false
}

func sdkForPackage(sdks []sourceSDKBinding, packagePath string) (sourceSDKBinding, bool) {
	var selected sourceSDKBinding
	found := false
	for _, sdk := range sdks {
		if packageInModule(packagePath, sdk.modulePath) && (!found || len(sdk.modulePath) > len(selected.modulePath)) {
			selected = sdk
			found = true
		}
	}
	return selected, found
}

func unavailableSDKForPackage(sdks []sourceUnavailableSDKBinding, packagePath string) (sourceUnavailableSDKBinding, bool) {
	var selected sourceUnavailableSDKBinding
	found := false
	for _, sdk := range sdks {
		if packageInModule(packagePath, sdk.modulePath) && (!found || len(sdk.modulePath) > len(selected.modulePath)) {
			selected = sdk
			found = true
		}
	}
	return selected, found
}

func packageInModule(packagePath, modulePath string) bool {
	return packagePath == modulePath || strings.HasPrefix(packagePath, modulePath+"/")
}

func chainHasSDKPackageStep(chain SourceEvidenceChain, packagePath string) bool {
	for _, step := range chain.Steps {
		if (step.Kind == CallSDKPackageFunction || step.Kind == CallSDKReceiverMethod) &&
			step.ImportPath != nil && *step.ImportPath == packagePath {
			return true
		}
	}
	return false
}

func validateReportTrust(report SourceEvidenceReport) error {
	switch report.SourceTrust {
	case SourceTrustVerified:
		if report.SourceManifestSHA256 == nil || !isSHA256(*report.SourceManifestSHA256) {
			return semanticErrorf(sourceReportContract, "$.source_manifest_sha256", "verified reports require a lowercase SHA-256")
		}
	case SourceTrustUnverified:
		if report.SourceManifestSHA256 != nil {
			return semanticErrorf(sourceReportContract, "$.source_manifest_sha256", "unverified reports must not claim a manifest digest")
		}
	default:
		return semanticErrorf(sourceReportContract, "$.source_trust", "must be verified or unverified")
	}
	return nil
}

func validateSourceRows(report SourceEvidenceReport) (SourceClassificationCounts, error) {
	var counts SourceClassificationCounts
	for _, resource := range sortedMapKeys(report.Resources) {
		row := report.Resources[resource]
		if err := validateSourceEvidenceRow(resource, row); err != nil {
			return SourceClassificationCounts{}, err
		}
		switch row.Classification {
		case SourceObservedHTTP:
			counts.ObservedHTTP++
		case SourceObservedSDKCall:
			counts.ObservedSDKCall++
		case SourceAmbiguous:
			counts.Ambiguous++
		case SourceDynamic:
			counts.Dynamic++
		case SourceUnresolved:
			counts.Unresolved++
		case SourceNoSource:
			counts.NoSource++
		case SourceNotApplicable:
			counts.NotApplicable++
		default:
			return SourceClassificationCounts{}, semanticErrorf(sourceReportContract, "$.resources."+resource+".classification", "is not a recognized source classification")
		}
		wantMapped := report.SourceTrust == SourceTrustVerified && row.Classification == SourceObservedHTTP
		if row.LegacyMapped != wantMapped {
			return SourceClassificationCounts{}, semanticErrorf(sourceReportContract, "$.resources."+resource+".legacy_mapped", "must equal verified && classification == observed_http")
		}
	}
	return counts, nil
}

func validateSourceEvidenceRow(resource string, row SourceEvidenceRow) error {
	base := "$.resources." + resource
	if row.Chains == nil {
		return semanticErrorf(sourceReportContract, base+".chains", "must be an array")
	}
	if row.ProviderRegistration != nil {
		if err := validateSourceSymbol(*row.ProviderRegistration, base+".provider_registration"); err != nil {
			return err
		}
		if row.ProviderRegistration.Location.Origin != SourceLocationProvider {
			return semanticErrorf(sourceReportContract, base+".provider_registration.location.origin", "provider registration must be in provider source")
		}
	}
	if row.ReadCallback != nil {
		if err := validateSourceSymbol(*row.ReadCallback, base+".read_callback"); err != nil {
			return err
		}
		if row.ReadCallback.Location.Function == nil {
			return semanticErrorf(sourceReportContract, base+".read_callback.location.function", "must name the callback function")
		}
		if row.ReadCallback.Location.Origin != SourceLocationProvider {
			return semanticErrorf(sourceReportContract, base+".read_callback.location.origin", "Read callback must be in provider source")
		}
	}
	for index, chain := range row.Chains {
		if err := validateSourceChain(chain, base+".chains["+integerText(index)+"]"); err != nil {
			return err
		}
		if row.ReadCallback != nil && !equalSourceSymbol(chain.Steps[0].Caller, *row.ReadCallback) {
			return semanticErrorf(sourceReportContract, base+".chains["+integerText(index)+"].steps[0].caller", "must exactly equal the row Read callback")
		}
	}

	hasRoot := row.ProviderRegistration != nil && row.ReadCallback != nil
	switch row.Classification {
	case SourceObservedHTTP:
		if !hasRoot || len(row.Chains) != 1 || row.Chains[0].Endpoint == nil || row.Chains[0].ReasonCode != nil || row.ReasonCode != nil {
			return semanticErrorf(sourceReportContract, base, "observed_http requires a Read-rooted chain and one endpoint only")
		}
		if chainHasCallKind(row.Chains[0], CallUnresolvedDispatch) {
			return semanticErrorf(sourceReportContract, base+".chains", "observed_http cannot contain unresolved_dispatch")
		}
	case SourceObservedSDKCall:
		if !hasRoot || len(row.Chains) != 1 || row.Chains[0].SDKCall == nil || row.Chains[0].Endpoint != nil ||
			!reasonIs(row.Chains[0].ReasonCode, ReasonEndpointNotRecovered) || row.ReasonCode != nil {
			return semanticErrorf(sourceReportContract, base, "observed_sdk_call requires a Read-rooted SDK call and endpoint_not_recovered")
		}
		if chainHasCallKind(row.Chains[0], CallUnresolvedDispatch) {
			return semanticErrorf(sourceReportContract, base+".chains", "observed_sdk_call cannot contain unresolved_dispatch")
		}
	case SourceAmbiguous:
		if !hasRoot || !reasonIs(row.ReasonCode, ReasonMultipleCandidates) || len(row.Chains) < 2 {
			return semanticErrorf(sourceReportContract, base, "ambiguous requires at least two complete Read-rooted candidate chains")
		}
		for _, chain := range row.Chains {
			if !viableChain(chain) && !missingSDKChain(chain) {
				return semanticErrorf(sourceReportContract, base+".chains", "ambiguous chains must each preserve a viable outcome or authorized missing-SDK call")
			}
		}
		if err := validateCanonicalChains(row.Chains, base+".chains"); err != nil {
			return err
		}
	case SourceDynamic:
		if !hasRoot || len(row.Chains) != 1 || row.Chains[0].Endpoint != nil ||
			!oneOfReasons(row.Chains[0].ReasonCode, ReasonDynamicDispatch, ReasonDynamicMethod, ReasonDynamicPath) || row.ReasonCode != nil {
			return semanticErrorf(sourceReportContract, base, "dynamic requires a Read-rooted chain and one dynamic reason")
		}
		terminalKind := row.Chains[0].Steps[len(row.Chains[0].Steps)-1].Kind
		if oneOfReasons(row.Chains[0].ReasonCode, ReasonDynamicMethod, ReasonDynamicPath) && terminalKind != CallRawHTTP {
			return semanticErrorf(sourceReportContract, base+".chains[0].steps", "dynamic method/path requires terminal raw_http request construction")
		}
		if reasonIs(row.Chains[0].ReasonCode, ReasonDynamicDispatch) && terminalKind != CallUnresolvedDispatch {
			return semanticErrorf(sourceReportContract, base+".chains[0].steps", "dynamic_dispatch requires terminal unresolved_dispatch")
		}
	case SourceUnresolved:
		if row.ProviderRegistration == nil || !oneOfReasons(row.ReasonCode, ReasonReadCallbackUnresolved, ReasonCallChainUnresolved) {
			return semanticErrorf(sourceReportContract, base, "unresolved requires provider source and an unresolved reason without endpoint evidence")
		}
		if reasonIs(row.ReasonCode, ReasonReadCallbackUnresolved) && (row.ReadCallback != nil || len(row.Chains) != 0) {
			return semanticErrorf(sourceReportContract, base, "read_callback_unresolved cannot carry a callback or call chain")
		}
		if reasonIs(row.ReasonCode, ReasonCallChainUnresolved) && (row.ReadCallback == nil || len(row.Chains) != 1 || !reasonIs(row.Chains[0].ReasonCode, ReasonCallChainUnresolved)) {
			return semanticErrorf(sourceReportContract, base, "call_chain_unresolved requires a partial Read-rooted chain")
		}
		if len(row.Chains) == 1 && (row.Chains[0].Endpoint != nil || row.Chains[0].SDKCall != nil) {
			return semanticErrorf(sourceReportContract, base+".chains", "unresolved cannot carry endpoint or SDK evidence")
		}
	case SourceNoSource:
		if !oneOfReasons(row.ReasonCode, ReasonProviderSourceMissing, ReasonSDKSourceMissing) {
			return semanticErrorf(sourceReportContract, base, "no_source requires a missing-source reason without resolved source evidence")
		}
		if reasonIs(row.ReasonCode, ReasonProviderSourceMissing) &&
			(row.ProviderRegistration != nil || row.ReadCallback != nil || len(row.Chains) != 0) {
			return semanticErrorf(sourceReportContract, base, "provider_source_missing cannot carry provider source evidence")
		}
		if reasonIs(row.ReasonCode, ReasonSDKSourceMissing) {
			if !hasRoot || len(row.Chains) == 0 {
				return semanticErrorf(sourceReportContract, base, "sdk_source_missing requires provider Read-rooted call sites")
			}
			for _, chain := range row.Chains {
				if !missingSDKChain(chain) {
					return semanticErrorf(sourceReportContract, base+".chains", "sdk_source_missing retains only terminal missing-SDK call sites")
				}
			}
			if err := validateCanonicalChains(row.Chains, base+".chains"); err != nil {
				return err
			}
		}
	case SourceNotApplicable:
		if len(row.Chains) != 0 || !reasonIs(row.ReasonCode, ReasonReviewedNotApplicable) {
			return semanticErrorf(sourceReportContract, base, "not_applicable requires only the reviewed_not_applicable reason")
		}
	}
	return nil
}

func validateSourceChain(chain SourceEvidenceChain, base string) error {
	if len(chain.Steps) == 0 {
		return semanticErrorf(sourceReportContract, base+".steps", "must preserve at least one Read-rooted call step")
	}
	hasSDKStep := false
	var firstSDKCallee *SourceSymbol
	firstSDKModule := ""
	for index, step := range chain.Steps {
		if err := validateSourceCallStep(step, base+".steps["+integerText(index)+"]"); err != nil {
			return err
		}
		if index > 0 {
			previous := chain.Steps[index-1]
			if previous.Callee == nil || !equalSourceSymbol(*previous.Callee, step.Caller) {
				return semanticErrorf(sourceReportContract, base+".steps["+integerText(index)+"].caller", "must exactly equal the preceding resolved callee")
			}
		}
		if (step.Kind == CallRawHTTP || step.Kind == CallUnresolvedDispatch || step.Kind == CallSDKSourceMissing) &&
			index != len(chain.Steps)-1 {
			return semanticErrorf(sourceReportContract, base+".steps["+integerText(index)+"]", "%s must be terminal", step.Kind)
		}
		if step.Kind == CallSDKPackageFunction || step.Kind == CallSDKReceiverMethod {
			hasSDKStep = true
			if firstSDKCallee == nil {
				firstSDKCallee = step.Callee
				firstSDKModule = *step.Callee.Location.SDKModulePath
			} else if *step.Callee.Location.SDKModulePath != firstSDKModule {
				return semanticErrorf(sourceReportContract, base+".steps["+integerText(index)+"]", "cross-SDK call-chain transitions are unsupported and fail closed")
			}
		}
	}
	if chain.SDKCall != nil {
		if !hasSDKStep {
			return semanticErrorf(sourceReportContract, base+".sdk_call", "requires a resolved SDK call step")
		}
		if err := validateSDKCall(*chain.SDKCall, base+".sdk_call"); err != nil {
			return err
		}
	}
	if firstSDKCallee != nil {
		if chain.SDKCall == nil || chain.SDKCall.ModulePath != firstSDKModule ||
			chain.SDKCall.PackagePath != firstSDKCallee.PackagePath || chain.SDKCall.Symbol != firstSDKCallee.Symbol ||
			!equalSourceLocation(chain.SDKCall.Location, firstSDKCallee.Location) {
			return semanticErrorf(sourceReportContract, base+".sdk_call", "must exactly identify the first SDK callee declaration")
		}
	}
	if chain.Endpoint != nil {
		if chain.ReasonCode != nil {
			return semanticErrorf(sourceReportContract, base, "endpoint and reason_code are mutually exclusive chain outcomes")
		}
		if err := validateEndpoint(*chain.Endpoint, base+".endpoint"); err != nil {
			return err
		}
		terminal := chain.Steps[len(chain.Steps)-1]
		if terminal.Kind != CallRawHTTP {
			return semanticErrorf(sourceReportContract, base+".steps", "an endpoint requires a terminal raw_http request-construction step")
		}
		if !equalSourceLocation(chain.Endpoint.Location, terminal.Location) {
			return semanticErrorf(sourceReportContract, base+".endpoint.location", "must equal the terminal raw_http call location")
		}
		if chain.Endpoint.Origin == EndpointOriginSDK && chain.SDKCall == nil {
			return semanticErrorf(sourceReportContract, base+".sdk_call", "SDK-origin endpoint requires pinned SDK call evidence")
		}
	}
	if chain.Endpoint == nil && chain.ReasonCode == nil {
		return semanticErrorf(sourceReportContract, base, "a chain without an endpoint requires a reason_code")
	}
	if chainHasCallKind(chain, CallUnresolvedDispatch) {
		if chain.Endpoint != nil || chain.SDKCall != nil {
			return semanticErrorf(sourceReportContract, base, "unresolved_dispatch must terminate before SDK or endpoint evidence")
		}
		if !oneOfReasons(chain.ReasonCode, ReasonCallChainUnresolved, ReasonDynamicDispatch) {
			return semanticErrorf(sourceReportContract, base+".reason_code", "unresolved_dispatch requires call_chain_unresolved or dynamic_dispatch")
		}
	}
	if chainHasCallKind(chain, CallSDKSourceMissing) {
		if !missingSDKChain(chain) {
			return semanticErrorf(sourceReportContract, base, "sdk_source_missing must be a pure terminal missing-SDK chain")
		}
	} else if reasonIs(chain.ReasonCode, ReasonSDKSourceMissing) {
		return semanticErrorf(sourceReportContract, base+".steps", "sdk_source_missing reason requires a terminal sdk_source_missing call")
	}
	return nil
}

func validateCanonicalChains(chains []SourceEvidenceChain, base string) error {
	keys := make([]string, len(chains))
	for index, chain := range chains {
		value, err := typedValue(chain, sourceReportContract)
		if err != nil {
			return semanticErrorf(sourceReportContract, base+"["+integerText(index)+"]", "cannot derive deterministic chain key")
		}
		keys[index], err = canonjson.Render(value)
		if err != nil {
			return semanticErrorf(sourceReportContract, base+"["+integerText(index)+"]", "cannot render deterministic chain key")
		}
	}
	return validateSortedUniqueForContract(keys, base, "ambiguous chains", sourceReportContract)
}

func viableChain(chain SourceEvidenceChain) bool {
	return chain.Endpoint != nil ||
		(chain.SDKCall != nil && reasonIs(chain.ReasonCode, ReasonEndpointNotRecovered)) ||
		oneOfReasons(chain.ReasonCode, ReasonDynamicDispatch, ReasonDynamicMethod, ReasonDynamicPath) ||
		(chainHasCallKind(chain, CallUnresolvedDispatch) && reasonIs(chain.ReasonCode, ReasonCallChainUnresolved))
}

func missingSDKChain(chain SourceEvidenceChain) bool {
	return reasonIs(chain.ReasonCode, ReasonSDKSourceMissing) &&
		chain.Endpoint == nil && chain.SDKCall == nil &&
		len(chain.Steps) != 0 && chain.Steps[len(chain.Steps)-1].Kind == CallSDKSourceMissing &&
		!chainHasCallKind(chain, CallUnresolvedDispatch)
}

func chainHasCallKind(chain SourceEvidenceChain, kind SourceCallKind) bool {
	for _, step := range chain.Steps {
		if step.Kind == kind {
			return true
		}
	}
	return false
}

func validateSourceSymbol(symbol SourceSymbol, base string) error {
	if module.CheckImportPath(symbol.PackagePath) != nil || symbol.Symbol == "" {
		return semanticErrorf(sourceReportContract, base, "package path and symbol must be valid and non-empty")
	}
	return validateSourceLocation(symbol.Location, base+".location")
}

func validateSourceLocation(location SourceLocation, base string) error {
	switch location.Origin {
	case SourceLocationProvider:
		if location.SDKModulePath != nil {
			return semanticErrorf(sourceReportContract, base+".sdk_module_path", "provider locations must not name an SDK module")
		}
	case SourceLocationSDK:
		if location.SDKModulePath == nil || module.CheckPath(*location.SDKModulePath) != nil {
			return semanticErrorf(sourceReportContract, base+".sdk_module_path", "SDK locations require a valid Go module path")
		}
	default:
		return semanticErrorf(sourceReportContract, base+".origin", "must be provider or sdk")
	}
	if !isPortableRelativePath(location.Path) {
		return semanticErrorf(sourceReportContract, base+".path", "must be a clean slash-separated relative path")
	}
	if location.Function != nil && *location.Function == "" {
		return semanticErrorf(sourceReportContract, base+".function", "must be null or non-empty")
	}
	if location.Line < 1 || location.Column < 1 {
		return semanticErrorf(sourceReportContract, base, "line and column must be one-based")
	}
	return nil
}

func validateSourceCallStep(step SourceCallStep, base string) error {
	switch step.Kind {
	case CallProviderHelper, CallRawHTTP, CallUnresolvedDispatch:
		if step.ImportPath != nil {
			return semanticErrorf(sourceReportContract, base+".import_path", "%s step must not carry an import path", step.Kind)
		}
	case CallSDKPackageFunction, CallSDKReceiverMethod, CallSDKSourceMissing:
		if step.ImportPath == nil || module.CheckImportPath(*step.ImportPath) != nil {
			return semanticErrorf(sourceReportContract, base+".import_path", "%s step requires an import path", step.Kind)
		}
	default:
		return semanticErrorf(sourceReportContract, base+".kind", "is not a recognized call kind")
	}
	if err := validateSourceSymbol(step.Caller, base+".caller"); err != nil {
		return err
	}
	if step.Caller.Location.Function == nil {
		return semanticErrorf(sourceReportContract, base+".caller.location.function", "must name the caller function")
	}
	if step.Callee != nil {
		if err := validateSourceSymbol(*step.Callee, base+".callee"); err != nil {
			return err
		}
		if step.Callee.Location.Function == nil {
			return semanticErrorf(sourceReportContract, base+".callee.location.function", "must name the resolved callee function")
		}
	}
	switch step.Kind {
	case CallProviderHelper:
		if step.Callee == nil || step.Caller.Location.Origin != SourceLocationProvider ||
			step.Location.Origin != SourceLocationProvider || step.Callee.Location.Origin != SourceLocationProvider {
			return semanticErrorf(sourceReportContract, base, "provider_helper requires provider caller, callsite, and resolved callee")
		}
	case CallSDKPackageFunction, CallSDKReceiverMethod:
		if step.Callee == nil || step.Callee.Location.Origin != SourceLocationSDK {
			return semanticErrorf(sourceReportContract, base+".callee", "%s requires a resolved SDK callee", step.Kind)
		}
	case CallSDKSourceMissing:
		if step.Caller.Location.Origin != SourceLocationProvider || step.Location.Origin != SourceLocationProvider {
			return semanticErrorf(sourceReportContract, base, "sdk_source_missing requires a provider caller and callsite")
		}
		if step.Callee != nil {
			return semanticErrorf(sourceReportContract, base+".callee", "sdk_source_missing must not claim a resolved callee")
		}
	case CallRawHTTP, CallUnresolvedDispatch:
		if step.Callee != nil {
			return semanticErrorf(sourceReportContract, base+".callee", "%s must not claim a resolved callee", step.Kind)
		}
	}
	if step.Symbol == "" {
		return semanticErrorf(sourceReportContract, base+".symbol", "must be non-empty")
	}
	if err := validateSourceLocation(step.Location, base+".location"); err != nil {
		return err
	}
	if step.Location.Function == nil {
		return semanticErrorf(sourceReportContract, base+".location.function", "must name the containing function")
	}
	if !sameSourceScope(step.Caller.Location, step.Location) {
		return semanticErrorf(sourceReportContract, base+".location", "callsite must be inside the bound caller function")
	}
	return nil
}

func validateSDKCall(call SDKCallEvidence, base string) error {
	if err := validateModuleVersion(call.ModulePath, call.ModuleVersion, sourceReportContract, base); err != nil {
		return err
	}
	if module.CheckImportPath(call.PackagePath) != nil || call.Symbol == "" {
		return semanticErrorf(sourceReportContract, base, "package path and symbol must be valid and non-empty")
	}
	if err := validateSourceLocation(call.Location, base+".location"); err != nil {
		return err
	}
	if call.Location.Function == nil {
		return semanticErrorf(sourceReportContract, base+".location.function", "must name the SDK declaration function")
	}
	if call.Location.Origin != SourceLocationSDK || call.Location.SDKModulePath == nil || *call.Location.SDKModulePath != call.ModulePath {
		return semanticErrorf(sourceReportContract, base+".location", "must name a declaration in the claimed SDK module")
	}
	return nil
}

func validateEndpoint(endpoint HTTPEndpointEvidence, base string) error {
	if endpoint.Origin != EndpointOriginProvider && endpoint.Origin != EndpointOriginSDK {
		return semanticErrorf(sourceReportContract, base+".origin", "must be provider or sdk")
	}
	if !isUpperASCII(endpoint.Method) {
		return semanticErrorf(sourceReportContract, base+".method", "must contain uppercase ASCII letters")
	}
	if endpoint.PathTemplate == "" {
		return semanticErrorf(sourceReportContract, base+".path_template", "must be non-empty")
	}
	if err := validateSourceLocation(endpoint.Location, base+".location"); err != nil {
		return err
	}
	if endpoint.Location.Function == nil {
		return semanticErrorf(sourceReportContract, base+".location.function", "must name the request-construction function")
	}
	if endpoint.Origin == EndpointOriginProvider && endpoint.Location.Origin != SourceLocationProvider {
		return semanticErrorf(sourceReportContract, base+".location.origin", "provider endpoint requires provider source")
	}
	if endpoint.Origin == EndpointOriginSDK && endpoint.Location.Origin != SourceLocationSDK {
		return semanticErrorf(sourceReportContract, base+".location.origin", "SDK endpoint requires SDK source")
	}
	return nil
}

func equalSourceLocation(left, right SourceLocation) bool {
	return left.Origin == right.Origin && equalOptionalString(left.SDKModulePath, right.SDKModulePath) &&
		left.Path == right.Path && equalOptionalString(left.Function, right.Function) &&
		left.Line == right.Line && left.Column == right.Column
}

func equalSourceSymbol(left, right SourceSymbol) bool {
	return left.PackagePath == right.PackagePath && left.Symbol == right.Symbol &&
		equalSourceLocation(left.Location, right.Location)
}

func sameSourceScope(left, right SourceLocation) bool {
	return left.Origin == right.Origin && equalOptionalString(left.SDKModulePath, right.SDKModulePath) &&
		left.Path == right.Path && equalOptionalString(left.Function, right.Function)
}

func reasonIs(reason *SourceReasonCode, want SourceReasonCode) bool {
	return reason != nil && *reason == want
}

func oneOfReasons(reason *SourceReasonCode, wants ...SourceReasonCode) bool {
	for _, want := range wants {
		if reasonIs(reason, want) {
			return true
		}
	}
	return false
}

func isUpperASCII(value string) bool {
	if value == "" {
		return false
	}
	for index := range value {
		if value[index] < 'A' || value[index] > 'Z' {
			return false
		}
	}
	return true
}

func validateSourceSummary(summary SourceSummary, counts SourceClassificationCounts) error {
	selected := sourceCountTotal(counts)
	applicable := selected - counts.NotApplicable
	sourceCalls := counts.ObservedHTTP + counts.ObservedSDKCall + counts.Dynamic
	endpoint := counts.ObservedHTTP
	if summary.ClassificationCounts != counts {
		return semanticErrorf(sourceReportContract, "$.summary.classification_counts", "must equal counts recomputed from $.resources")
	}
	if summary.SelectedTotal != selected {
		return semanticErrorf(sourceReportContract, "$.summary.selected_total", "must equal %d", selected)
	}
	if summary.ApplicableTotal != applicable {
		return semanticErrorf(sourceReportContract, "$.summary.applicable_total", "must equal %d", applicable)
	}
	if summary.SourceCallObservedTotal != sourceCalls {
		return semanticErrorf(sourceReportContract, "$.summary.source_call_observed_total", "must equal %d", sourceCalls)
	}
	if summary.EndpointObservedTotal != endpoint {
		return semanticErrorf(sourceReportContract, "$.summary.endpoint_observed_total", "must equal %d", endpoint)
	}
	coverage := summary.EndpointCoverage
	if coverage.Numerator != endpoint || coverage.Denominator != applicable {
		return semanticErrorf(sourceReportContract, "$.summary.endpoint_coverage", "must equal the exact endpoint/applicable counts")
	}
	wantState := CoverageRatio
	if applicable == 0 {
		wantState = CoverageNotApplicable
	}
	if coverage.State != wantState {
		return semanticErrorf(sourceReportContract, "$.summary.endpoint_coverage.state", "must equal %s", wantState)
	}
	return nil
}

// DecodeOpenAPIDiagnosticsReport strictly decodes the isolated OpenAPI
// comparison artifact from docs/go-authoring-port-roadmap.md §3.6 and joins it
// to its already validated source report.
func DecodeOpenAPIDiagnosticsReport(data []byte, source SourceEvidenceReport) (OpenAPIDiagnosticsReport, error) {
	var diagnostics OpenAPIDiagnosticsReport
	if err := decodeDocument(data, openAPIDiagnosticsContract, openAPIDiagnosticsSchema, &diagnostics); err != nil {
		return OpenAPIDiagnosticsReport{}, err
	}
	if err := ValidateOpenAPIDiagnosticsReport(diagnostics, source); err != nil {
		return OpenAPIDiagnosticsReport{}, err
	}
	rendered, err := RenderOpenAPIDiagnosticsReport(diagnostics, source)
	if err != nil || string(data) != rendered {
		return OpenAPIDiagnosticsReport{}, nonCanonicalDocumentError(openAPIDiagnosticsContract)
	}
	return diagnostics, nil
}

// ValidateOpenAPIDiagnosticsReport enforces the isolated six-state partition
// and exact cross-report key join from docs/go-authoring-port-roadmap.md §3.6.
func ValidateOpenAPIDiagnosticsReport(diagnostics OpenAPIDiagnosticsReport, source SourceEvidenceReport) error {
	if err := ValidateSourceEvidenceReport(source); err != nil {
		return semanticErrorf(openAPIDiagnosticsContract, "$.source_report", "source report is invalid")
	}
	if diagnostics.Kind != "infrawright.openapi_diagnostics" {
		return semanticErrorf(openAPIDiagnosticsContract, "$.kind", "must equal infrawright.openapi_diagnostics")
	}
	if diagnostics.SchemaVersion != 1 {
		return semanticErrorf(openAPIDiagnosticsContract, "$.schema_version", "must equal 1")
	}
	if diagnostics.SourceTrust != source.SourceTrust || !equalOptionalString(diagnostics.SourceManifestSHA256, source.SourceManifestSHA256) {
		return semanticErrorf(openAPIDiagnosticsContract, "$.source_trust", "source trust and manifest digest must match the source report")
	}
	renderedSource, err := RenderSourceEvidenceReport(source)
	if err != nil {
		return semanticErrorf(openAPIDiagnosticsContract, "$.source_report_sha256", "source report cannot be rendered")
	}
	if diagnostics.SourceReportSHA256 != sha256Text([]byte(renderedSource)) {
		return semanticErrorf(openAPIDiagnosticsContract, "$.source_report_sha256", "must bind the exact source report bytes")
	}
	if diagnostics.Comparisons == nil {
		return semanticErrorf(openAPIDiagnosticsContract, "$.comparisons", "must be an object")
	}
	resourceKeys := sortedMapKeys(source.Resources)
	if !canonjson.SameStringSequence(resourceKeys, sortedMapKeys(diagnostics.Comparisons)) {
		return semanticErrorf(openAPIDiagnosticsContract, "$.comparisons", "resource keys must exactly match the source report")
	}
	counts, err := validateComparisonRows(diagnostics, source, resourceKeys)
	if err != nil {
		return err
	}
	return validateComparisonSummary(diagnostics, source, counts)
}

func validateComparisonRows(diagnostics OpenAPIDiagnosticsReport, source SourceEvidenceReport, resourceKeys []string) (OpenAPIComparisonCounts, error) {
	state := diagnostics.DocumentState
	switch state {
	case OpenAPIAbsent, OpenAPIUsable, OpenAPIDegraded, OpenAPIUnavailable:
	default:
		return OpenAPIComparisonCounts{}, semanticErrorf(openAPIDiagnosticsContract, "$.document_state", "is not recognized")
	}
	if state == OpenAPIDegraded || state == OpenAPIUnavailable {
		if !validOpenAPIReason(state, diagnostics.ReasonCode) {
			return OpenAPIComparisonCounts{}, semanticErrorf(openAPIDiagnosticsContract, "$.reason_code", "%s state requires an allowed stable reason code", state)
		}
	} else if diagnostics.ReasonCode != nil {
		return OpenAPIComparisonCounts{}, semanticErrorf(openAPIDiagnosticsContract, "$.reason_code", "%s state must not carry a reason code", state)
	}

	var counts OpenAPIComparisonCounts
	for _, resource := range resourceKeys {
		comparisonRow := diagnostics.Comparisons[resource]
		if err := validateOpenAPIComparisonEvidence(comparisonRow, source.Resources[resource], resource); err != nil {
			return OpenAPIComparisonCounts{}, err
		}
		comparison := comparisonRow.State
		sourceClassification := source.Resources[resource].Classification
		if err := validateComparisonForDocument(state, sourceClassification, comparison, resource); err != nil {
			return OpenAPIComparisonCounts{}, err
		}
		switch comparison {
		case ComparisonNotAttempted:
			counts.NotAttempted++
		case ComparisonNotComparable:
			counts.NotComparable++
		case ComparisonCorroborated:
			counts.Corroborated++
		case ComparisonMissingPath:
			counts.MissingPath++
		case ComparisonAmbiguous:
			counts.Ambiguous++
		case ComparisonConflict:
			counts.Conflict++
		}
	}
	return counts, nil
}

func validateOpenAPIComparisonEvidence(row OpenAPIComparisonRow, source SourceEvidenceRow, resource string) error {
	base := "$.comparisons." + resource
	if row.Operations == nil {
		return semanticErrorf(openAPIDiagnosticsContract, base+".operations", "must be an array")
	}
	for index, operation := range row.Operations {
		if !isUpperASCII(operation.Method) || operation.PathTemplate == "" {
			return semanticErrorf(openAPIDiagnosticsContract, base+".operations["+integerText(index)+"]", "method/path must be normalized and non-empty")
		}
		if operation.OperationID != nil && *operation.OperationID == "" {
			return semanticErrorf(openAPIDiagnosticsContract, base+".operations["+integerText(index)+"].operation_id", "must be nil or non-empty")
		}
	}
	operationKeys := make([]string, len(row.Operations))
	for index, operation := range row.Operations {
		operationID := ""
		if operation.OperationID != nil {
			operationID = *operation.OperationID
		}
		operationKeys[index] = operation.Method + "\x00" + operation.PathTemplate + "\x00" + operationID
	}
	if err := validateSortedUniqueForContract(operationKeys, base+".operations", "OpenAPI operations", openAPIDiagnosticsContract); err != nil {
		return err
	}
	switch row.State {
	case ComparisonNotAttempted, ComparisonNotComparable, ComparisonMissingPath:
		if row.Basis != nil || row.BasisReference != nil || len(row.Operations) != 0 {
			return semanticErrorf(openAPIDiagnosticsContract, base, "%s must not claim comparison evidence", row.State)
		}
	case ComparisonCorroborated:
		if !basisIs(row.Basis, ComparisonBasisSourceEndpoint) || row.BasisReference != nil || len(row.Operations) != 1 {
			return semanticErrorf(openAPIDiagnosticsContract, base, "corroborated requires one source_endpoint operation")
		}
		endpoint := sourceEndpoint(source)
		if endpoint == nil || row.Operations[0].Method != endpoint.Method || row.Operations[0].PathTemplate != endpoint.PathTemplate {
			return semanticErrorf(openAPIDiagnosticsContract, base, "corroborated operation must equal the source endpoint")
		}
	case ComparisonAmbiguous:
		if !basisIs(row.Basis, ComparisonBasisSourceEndpoint) || row.BasisReference != nil || len(row.Operations) < 2 {
			return semanticErrorf(openAPIDiagnosticsContract, base, "ambiguous requires at least two source_endpoint candidates")
		}
		endpoint := sourceEndpoint(source)
		if endpoint == nil {
			return semanticErrorf(openAPIDiagnosticsContract, base, "ambiguous requires an observed source endpoint")
		}
		for _, operation := range row.Operations {
			if !operationViableForEndpoint(operation, *endpoint) {
				return semanticErrorf(openAPIDiagnosticsContract, base+".operations", "every ambiguous operation must be a viable normalized-template match for the source endpoint")
			}
		}
	case ComparisonConflict:
		if (!basisIs(row.Basis, ComparisonBasisTrustedSharedIdentity) && !basisIs(row.Basis, ComparisonBasisExplicitBinding)) ||
			row.BasisReference == nil || strings.TrimSpace(*row.BasisReference) == "" || len(row.Operations) == 0 {
			return semanticErrorf(openAPIDiagnosticsContract, base, "conflict requires trusted_shared_identity or explicit_binding evidence")
		}
		if basisIs(row.Basis, ComparisonBasisTrustedSharedIdentity) {
			for _, operation := range row.Operations {
				if operation.OperationID == nil || *operation.OperationID == "" {
					return semanticErrorf(openAPIDiagnosticsContract, base, "trusted_shared_identity conflict requires operation_id evidence")
				}
			}
		}
		endpoint := sourceEndpoint(source)
		if endpoint == nil {
			return semanticErrorf(openAPIDiagnosticsContract, base, "conflict requires an observed source endpoint")
		}
		for _, operation := range row.Operations {
			if operationViableForEndpoint(operation, *endpoint) {
				return semanticErrorf(openAPIDiagnosticsContract, base+".operations", "conflict operations must positively differ from the source endpoint")
			}
		}
	default:
		return semanticErrorf(openAPIDiagnosticsContract, base+".state", "is not recognized")
	}
	return nil
}

func sourceEndpoint(source SourceEvidenceRow) *HTTPEndpointEvidence {
	if source.Classification != SourceObservedHTTP || len(source.Chains) != 1 {
		return nil
	}
	return source.Chains[0].Endpoint
}

// operationViableForEndpoint uses a deliberately small comparator: methods
// must match exactly and path segments must match exactly except that two
// whole-segment template parameters may use different parameter names.
func operationViableForEndpoint(operation OpenAPIOperationCandidate, endpoint HTTPEndpointEvidence) bool {
	if operation.Method != endpoint.Method {
		return false
	}
	operationSegments := strings.Split(operation.PathTemplate, "/")
	endpointSegments := strings.Split(endpoint.PathTemplate, "/")
	if len(operationSegments) != len(endpointSegments) {
		return false
	}
	for index, operationSegment := range operationSegments {
		endpointSegment := endpointSegments[index]
		if operationSegment != endpointSegment &&
			!(isTemplateParameter(operationSegment) && isTemplateParameter(endpointSegment)) {
			return false
		}
	}
	return true
}

func isTemplateParameter(segment string) bool {
	return len(segment) > 2 && segment[0] == '{' && segment[len(segment)-1] == '}' &&
		!strings.ContainsAny(segment[1:len(segment)-1], "{}")
}

func validOpenAPIReason(state OpenAPIDocumentState, reason *OpenAPIReasonCode) bool {
	if reason == nil {
		return false
	}
	if state == OpenAPIDegraded {
		return *reason == OpenAPIReasonDegradedOperation
	}
	switch *reason {
	case OpenAPIReasonUnreadable, OpenAPIReasonMalformed, OpenAPIReasonInvalidRoot, OpenAPIReasonLocalRefUnresolved:
		return true
	default:
		return false
	}
}

func basisIs(basis *OpenAPIComparisonBasis, want OpenAPIComparisonBasis) bool {
	return basis != nil && *basis == want
}

func validateComparisonForDocument(document OpenAPIDocumentState, source SourceClassification, comparison OpenAPIComparisonState, resource string) error {
	location := "$.comparisons." + resource + ".state"
	if document == OpenAPIAbsent || document == OpenAPIUnavailable {
		if comparison != ComparisonNotAttempted {
			return semanticErrorf(openAPIDiagnosticsContract, location, "%s document requires not_attempted", document)
		}
		return nil
	}
	if source != SourceObservedHTTP {
		if comparison != ComparisonNotComparable {
			return semanticErrorf(openAPIDiagnosticsContract, location, "non-observed-http source requires not_comparable")
		}
		return nil
	}
	switch comparison {
	case ComparisonCorroborated, ComparisonMissingPath, ComparisonAmbiguous, ComparisonConflict:
		return nil
	default:
		return semanticErrorf(openAPIDiagnosticsContract, location, "observed_http source requires an eligible comparison outcome")
	}
}

func validateComparisonSummary(diagnostics OpenAPIDiagnosticsReport, source SourceEvidenceReport, counts OpenAPIComparisonCounts) error {
	summary := diagnostics.Summary
	if summary.ComparisonCounts != counts {
		return semanticErrorf(openAPIDiagnosticsContract, "$.summary.comparison_counts", "must equal counts recomputed from $.comparisons")
	}
	eligible := source.Summary.ClassificationCounts.ObservedHTTP
	if summary.ComparisonEligibleTotal != eligible {
		return semanticErrorf(openAPIDiagnosticsContract, "$.summary.comparison_eligible_total", "must equal %d", eligible)
	}
	wantDegraded := 0
	if diagnostics.DocumentState == OpenAPIDegraded {
		wantDegraded = eligible
	}
	if summary.DegradedComparisonTotal != wantDegraded {
		return semanticErrorf(openAPIDiagnosticsContract, "$.summary.degraded_comparison_total", "must equal %d", wantDegraded)
	}
	if comparisonCountTotal(counts) != source.Summary.SelectedTotal {
		return semanticErrorf(openAPIDiagnosticsContract, "$.summary.comparison_counts", "six-state partition must sum to source selected_total")
	}
	return nil
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sha256Text(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func sourceCountTotal(counts SourceClassificationCounts) int {
	return counts.ObservedHTTP + counts.ObservedSDKCall + counts.Ambiguous + counts.Dynamic +
		counts.Unresolved + counts.NoSource + counts.NotApplicable
}

func comparisonCountTotal(counts OpenAPIComparisonCounts) int {
	return counts.NotAttempted + counts.NotComparable + counts.Corroborated +
		counts.MissingPath + counts.Ambiguous + counts.Conflict
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return canonjson.SortedStrings(keys)
}

func integerText(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte(value%10) + '0'
		value /= 10
	}
	return string(digits[position:])
}
