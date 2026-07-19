package contracts

// SourceTrust is the qualification state defined by
// docs/go-authoring-port-roadmap.md §3.2.1.
type SourceTrust string

const (
	// SourceTrustVerified means all manifest identities and bytes were verified as
	// required by docs/go-authoring-port-roadmap.md §3.2.1.
	SourceTrustVerified SourceTrust = "verified"
	// SourceTrustUnverified identifies an explicitly requested diagnostic run that
	// is ineligible for readiness under docs/go-authoring-port-roadmap.md §3.2.1.
	SourceTrustUnverified SourceTrust = "unverified"
)

// FileBinding pins one portable relative file to bytes as required by
// docs/go-authoring-port-roadmap.md §3.2.1.
type FileBinding struct {
	// Path is a slash-separated path relative to its explicitly supplied source root.
	Path string `json:"path"`
	// SHA256 is the lowercase hexadecimal SHA-256 of the file bytes.
	SHA256 string `json:"sha256"`
}

// ProviderSourceBinding pins the provider tree described by
// docs/go-authoring-port-roadmap.md §3.2.1.
type ProviderSourceBinding struct {
	// Repository is the provider repository identity.
	Repository string `json:"repository"`
	// ModulePath is the provider's declared Go module path.
	ModulePath string `json:"module_path"`
	// Revision is the pinned provider revision.
	Revision string `json:"revision"`
	// TreeSHA256 is the deterministic provider-tree digest.
	TreeSHA256 string `json:"tree_sha256"`
	// Files is the complete analyzed provider-file binding set.
	Files []FileBinding `json:"files"`
}

// ProviderModuleBinding binds the provider module-resolution inputs required by
// docs/go-authoring-port-roadmap.md §3.2.1.
type ProviderModuleBinding struct {
	// GoMod pins the provider go.mod bytes.
	GoMod FileBinding `json:"go_mod"`
	// GoSum pins the provider go.sum bytes when the module has one.
	GoSum *FileBinding `json:"go_sum"`
	// LocalReplaces records portable local Go module replacements and their bound SDK targets.
	LocalReplaces []LocalModuleReplaceBinding `json:"local_replaces"`
}

// LocalModuleReplaceBinding preserves a local provider go.mod replacement
// without checkout-root leakage under docs/go-authoring-port-roadmap.md §3.2.1.
type LocalModuleReplaceBinding struct {
	// ModulePath is the module path on the left side of the replace directive.
	ModulePath string `json:"module_path"`
	// ModuleVersion is the optional version on the left side of the replace directive.
	ModuleVersion *string `json:"module_version"`
	// LocalPath is a normalized slash-separated path relative to the provider root.
	LocalPath string `json:"local_path"`
}

// SDKSourceBinding pins one SDK source tree described by
// docs/go-authoring-port-roadmap.md §3.2.1.
type SDKSourceBinding struct {
	// ModulePath is the SDK's declared Go module path.
	ModulePath string `json:"module_path"`
	// ModuleVersion is the provider-bound SDK module version.
	ModuleVersion string `json:"module_version"`
	// Repository is the SDK repository identity.
	Repository string `json:"repository"`
	// Revision is the SDK repository revision when available.
	Revision *string `json:"revision"`
	// TreeSHA256 is the deterministic SDK module-tree digest when available.
	TreeSHA256 *string `json:"tree_sha256"`
	// Files is the complete analyzed SDK-file binding set.
	Files []FileBinding `json:"files"`
}

// SelectionFilterBinding records one normalized selection input from
// docs/go-authoring-port-roadmap.md §3.2.1.
type SelectionFilterBinding struct {
	// Name is the stable filter name.
	Name string `json:"name"`
	// Values are the normalized filter values in deterministic order.
	Values []string `json:"values"`
}

const (
	// SelectionFilterReviewedNotApplicable is the reserved exact authorization
	// list for resources excluded from endpoint applicability by human review.
	SelectionFilterReviewedNotApplicable = "reviewed_not_applicable"
)

// SelectionBinding defines the resource table covered by evidence under
// docs/go-authoring-port-roadmap.md §3.2.1.
type SelectionBinding struct {
	// ResourceTypes is the sorted, unique set of selected Terraform resource types.
	ResourceTypes []string `json:"resource_types"`
	// Filters records every normalized selection/filter input.
	Filters []SelectionFilterBinding `json:"filters"`
}

// SourceProvenance is the closed verified source-provenance-v1 manifest from
// docs/go-authoring-port-roadmap.md §3.2.1.
type SourceProvenance struct {
	// Kind is always infrawright.source_provenance.
	Kind string `json:"kind"`
	// SchemaVersion is always 1.
	SchemaVersion int `json:"schema_version"`
	// Provider binds the verified provider tree.
	Provider ProviderSourceBinding `json:"provider"`
	// ProviderModule binds the verified go.mod/go.sum inputs.
	ProviderModule ProviderModuleBinding `json:"provider_module"`
	// TerraformSchema binds the verified provider schema.
	TerraformSchema FileBinding `json:"terraform_schema"`
	// SDKs binds every SDK source tree used by analysis.
	SDKs []SDKSourceBinding `json:"sdks"`
	// Selection binds the resource/filter inputs defining the evidence table.
	Selection SelectionBinding `json:"selection"`
	// OpenAPI optionally binds adapter inputs without changing provider/SDK source trust.
	OpenAPI *OpenAPIInputBinding `json:"openapi"`
}

// OpenAPIInputBinding pins optional adapter files while keeping their
// availability isolated from source trust under docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIInputBinding struct {
	// Document pins the explicitly selected OpenAPI document.
	Document FileBinding `json:"document"`
	// LocalRefs pins the sorted local reference files allowed by the adapter.
	LocalRefs []FileBinding `json:"local_refs"`
}

// UnverifiedSDKObservation records SDK module/files without repository or
// revision claims under docs/go-authoring-port-roadmap.md §3.2.1.
type UnverifiedSDKObservation struct {
	// ModulePath is the locally observed Go module identity.
	ModulePath string `json:"module_path"`
	// ModuleVersion is the locally observed provider-bound SDK module version.
	ModuleVersion string `json:"module_version"`
	// Files are portable observed file/hash pairs.
	Files []FileBinding `json:"files"`
}

// UnverifiedSourceObservation is the intentionally non-qualifying input shape
// from docs/go-authoring-port-roadmap.md §3.2.1.
type UnverifiedSourceObservation struct {
	// ProviderModulePath is the locally observed provider module path.
	ProviderModulePath string `json:"provider_module_path"`
	// ProviderFiles are portable observed provider file/hash pairs.
	ProviderFiles []FileBinding `json:"provider_files"`
	// TerraformSchema is the locally observed schema binding.
	TerraformSchema FileBinding `json:"terraform_schema"`
	// SDKs are module/file observations without repository or revision claims.
	SDKs []UnverifiedSDKObservation `json:"sdks"`
	// Selection binds the diagnostic resource/filter table.
	Selection SelectionBinding `json:"selection"`
}

// InputProvenance is the verified/unverified emitted union required for
// input-provenance.json by docs/go-authoring-port-roadmap.md §§3.2.1 and 3.5.
type InputProvenance struct {
	// Kind is always infrawright.input_provenance.
	Kind string `json:"kind"`
	// SchemaVersion is always 1.
	SchemaVersion int `json:"schema_version"`
	// SourceTrust selects exactly one union branch.
	SourceTrust SourceTrust `json:"source_trust"`
	// SourceManifestSHA256 is present only in verified mode.
	SourceManifestSHA256 *string `json:"source_manifest_sha256"`
	// SourceManifest is present only in verified mode.
	SourceManifest *SourceProvenance `json:"source_manifest"`
	// UnverifiedObservation is present only in explicit unverified mode.
	UnverifiedObservation *UnverifiedSourceObservation `json:"unverified_observation"`
}

// SourceLocationOrigin identifies the explicit bound source tree containing a
// portable location.
type SourceLocationOrigin string

const (
	// SourceLocationProvider identifies the manifest-bound provider source tree.
	SourceLocationProvider SourceLocationOrigin = "provider"
	// SourceLocationSDK identifies one exact manifest-bound SDK module source tree.
	SourceLocationSDK SourceLocationOrigin = "sdk"
)

// SourceLocation is an unambiguous portable source position required by
// docs/go-authoring-port-roadmap.md §§3.1–3.2.
type SourceLocation struct {
	// Origin selects the provider tree or one exact SDK module tree.
	Origin SourceLocationOrigin `json:"origin"`
	// SDKModulePath is present exactly when Origin is sdk.
	SDKModulePath *string `json:"sdk_module_path"`
	// Path is slash-separated and relative to the selected source tree.
	Path string `json:"path"`
	// Function is the declared function containing the source position, or nil
	// for a package-scope binding such as provider registration.
	Function *string `json:"function"`
	// Line is the one-based source line.
	Line int `json:"line"`
	// Column is the one-based source column.
	Column int `json:"column"`
}

// SourceSymbol binds a provider registration or Read callback under
// docs/go-authoring-port-roadmap.md §3.1.
type SourceSymbol struct {
	// PackagePath is the fully qualified Go package containing the declaration.
	PackagePath string `json:"package_path"`
	// Symbol is the stable registry binding or statically resolved declaration name.
	Symbol string `json:"symbol"`
	// Location is the portable declaration or binding position.
	Location SourceLocation `json:"location"`
}

// SourceCallKind is the closed Read-rooted call-step vocabulary from
// docs/go-authoring-port-roadmap.md §§3.1–3.2.
type SourceCallKind string

const (
	// CallProviderHelper is a statically reachable same-package provider helper.
	CallProviderHelper SourceCallKind = "provider_helper"
	// CallSDKPackageFunction is a statically resolved imported SDK package function.
	CallSDKPackageFunction SourceCallKind = "sdk_package_function"
	// CallSDKReceiverMethod is a statically proven SDK receiver method.
	CallSDKReceiverMethod SourceCallKind = "sdk_receiver_method"
	// CallSDKSourceMissing is a provider call into an SDK package whose source
	// owner is absent from the exact input manifest.
	CallSDKSourceMissing SourceCallKind = "sdk_source_missing"
	// CallRawHTTP is a direct provider or SDK request-construction call.
	CallRawHTTP SourceCallKind = "raw_http"
	// CallUnresolvedDispatch is a truthful partial-chain interface or dynamic dispatch.
	CallUnresolvedDispatch SourceCallKind = "unresolved_dispatch"
)

// SourceCallStep is one ordered Read-rooted call step required by
// docs/go-authoring-port-roadmap.md §§3.1–3.2.
type SourceCallStep struct {
	// Kind identifies how the callee was resolved.
	Kind SourceCallKind `json:"kind"`
	// Symbol is the source spelling retained for diagnostics.
	Symbol string `json:"symbol"`
	// ImportPath is set only for resolved SDK calls or a terminal missing-SDK call.
	ImportPath *string `json:"import_path"`
	// Caller is the declaration containing this call site.
	Caller SourceSymbol `json:"caller"`
	// Callee is the resolved declaration, and is nil only for terminal raw HTTP
	// construction, unresolved dispatch, or missing SDK source.
	Callee *SourceSymbol `json:"callee"`
	// Location is the portable provider or SDK call site.
	Location SourceLocation `json:"location"`
}

// SDKCallEvidence pins the SDK symbol reached by a provider Read chain under
// docs/go-authoring-port-roadmap.md §3.2.
type SDKCallEvidence struct {
	// ModulePath is the pinned SDK module identity.
	ModulePath string `json:"module_path"`
	// ModuleVersion is the provider-bound SDK module version.
	ModuleVersion string `json:"module_version"`
	// PackagePath is the imported SDK package path.
	PackagePath string `json:"package_path"`
	// Symbol is the resolved package function or receiver method.
	Symbol string `json:"symbol"`
	// Location is the portable SDK declaration position.
	Location SourceLocation `json:"location"`
}

// HTTPEndpointOrigin identifies where a source-backed request is constructed
// under docs/go-authoring-port-roadmap.md §3.3.
type HTTPEndpointOrigin string

const (
	// EndpointOriginProvider means provider source constructs the request directly.
	EndpointOriginProvider HTTPEndpointOrigin = "provider"
	// EndpointOriginSDK means pinned SDK source constructs the request.
	EndpointOriginSDK HTTPEndpointOrigin = "sdk"
)

// HTTPEndpointEvidence is one recoverable source-backed endpoint from
// docs/go-authoring-port-roadmap.md §§3.2–3.3.
type HTTPEndpointEvidence struct {
	// Origin identifies whether provider or SDK source constructs the request.
	Origin HTTPEndpointOrigin `json:"origin"`
	// Method is the recovered uppercase HTTP method.
	Method string `json:"method"`
	// PathTemplate is the recovered path template and is not a checkout path.
	PathTemplate string `json:"path_template"`
	// Location is the portable request-construction position.
	Location SourceLocation `json:"location"`
}

// SourceReasonCode is the closed fail-closed reason vocabulary required by
// docs/go-authoring-port-roadmap.md §3.3.
type SourceReasonCode string

const (
	// ReasonProviderSourceMissing means the pinned provider source is absent.
	ReasonProviderSourceMissing SourceReasonCode = "provider_source_missing"
	// ReasonSDKSourceMissing means a required pinned SDK source tree is absent.
	ReasonSDKSourceMissing SourceReasonCode = "sdk_source_missing"
	// ReasonReadCallbackUnresolved means the selected registration's Read callback is unresolved.
	ReasonReadCallbackUnresolved SourceReasonCode = "read_callback_unresolved"
	// ReasonCallChainUnresolved means the Read-rooted call chain cannot be completed.
	ReasonCallChainUnresolved SourceReasonCode = "call_chain_unresolved"
	// ReasonEndpointNotRecovered means an SDK call is known but no endpoint is recoverable.
	ReasonEndpointNotRecovered SourceReasonCode = "endpoint_not_recovered"
	// ReasonDynamicDispatch means dynamic dispatch prevents static resolution.
	ReasonDynamicDispatch SourceReasonCode = "dynamic_dispatch"
	// ReasonDynamicMethod means the HTTP method cannot be reduced.
	ReasonDynamicMethod SourceReasonCode = "dynamic_method"
	// ReasonDynamicPath means the HTTP path cannot be reduced.
	ReasonDynamicPath SourceReasonCode = "dynamic_path"
	// ReasonMultipleCandidates means multiple viable source outcomes remain.
	ReasonMultipleCandidates SourceReasonCode = "multiple_viable_candidates"
	// ReasonReviewedNotApplicable records the reviewed exclusion from endpoint analysis.
	ReasonReviewedNotApplicable SourceReasonCode = "reviewed_not_applicable"
)

// SourceClassification is the closed seven-state source partition defined by
// docs/go-authoring-port-roadmap.md §3.3.
type SourceClassification string

const (
	// SourceObservedHTTP means one Read-rooted chain reaches one source-backed endpoint.
	SourceObservedHTTP SourceClassification = "observed_http"
	// SourceObservedSDKCall means one Read-rooted chain reaches an SDK symbol but no endpoint.
	SourceObservedSDKCall SourceClassification = "observed_sdk_call"
	// SourceAmbiguous means multiple viable source chains remain.
	SourceAmbiguous SourceClassification = "ambiguous"
	// SourceDynamic means a request exists but its method or path is not reducible.
	SourceDynamic SourceClassification = "dynamic"
	// SourceUnresolved means source exists but the Read-rooted chain cannot be resolved.
	SourceUnresolved SourceClassification = "unresolved"
	// SourceNoSource means required pinned provider or SDK source is absent.
	SourceNoSource SourceClassification = "no_source"
	// SourceNotApplicable means reviewed endpoint analysis does not apply.
	SourceNotApplicable SourceClassification = "not_applicable"
)

// SourceEvidenceRow is one authoritative resource row from
// docs/go-authoring-port-roadmap.md §3.3.
type SourceEvidenceRow struct {
	// Classification is exactly one member of the seven-state source partition.
	Classification SourceClassification `json:"classification"`
	// LegacyMapped is true only for verified observed_http rows.
	LegacyMapped bool `json:"legacy_mapped"`
	// ProviderRegistration is the Terraform registry binding, when available.
	ProviderRegistration *SourceSymbol `json:"provider_registration"`
	// ReadCallback is the resolved Read callback, when available.
	ReadCallback *SourceSymbol `json:"read_callback"`
	// Chains preserves every ordered viable Read-rooted provider→SDK chain.
	Chains []SourceEvidenceChain `json:"chains"`
	// ReasonCode explains every non-success classification.
	ReasonCode *SourceReasonCode `json:"reason_code"`
}

// SourceEvidenceChain preserves one ordered viable provider→SDK→HTTP chain
// under docs/go-authoring-port-roadmap.md §§3.1–3.3.
type SourceEvidenceChain struct {
	// Steps are the ordered statically reachable calls beginning at Read.
	Steps []SourceCallStep `json:"steps"`
	// SDKCall is the optional pinned SDK declaration reached by the chain.
	SDKCall *SDKCallEvidence `json:"sdk_call"`
	// Endpoint is the optional single source-backed HTTP endpoint.
	Endpoint *HTTPEndpointEvidence `json:"endpoint"`
	// ReasonCode records an endpoint-not-recovered or dynamic chain outcome.
	ReasonCode *SourceReasonCode `json:"reason_code"`
}

// SourceClassificationCounts records every member of the closed partition in
// docs/go-authoring-port-roadmap.md §3.3.
type SourceClassificationCounts struct {
	// ObservedHTTP counts observed_http rows.
	ObservedHTTP int `json:"observed_http"`
	// ObservedSDKCall counts observed_sdk_call rows.
	ObservedSDKCall int `json:"observed_sdk_call"`
	// Ambiguous counts ambiguous rows.
	Ambiguous int `json:"ambiguous"`
	// Dynamic counts dynamic rows.
	Dynamic int `json:"dynamic"`
	// Unresolved counts unresolved rows.
	Unresolved int `json:"unresolved"`
	// NoSource counts no_source rows.
	NoSource int `json:"no_source"`
	// NotApplicable counts not_applicable rows.
	NotApplicable int `json:"not_applicable"`
}

// CoverageState selects the exact coverage representation required by
// docs/go-authoring-port-roadmap.md §3.3.
type CoverageState string

const (
	// CoverageRatio means numerator/denominator is the exact endpoint coverage.
	CoverageRatio CoverageState = "ratio"
	// CoverageNotApplicable means the denominator is zero and no percentage exists.
	CoverageNotApplicable CoverageState = "not_applicable"
)

// ExactCoverage avoids a rounded floating-point coverage claim under
// docs/go-authoring-port-roadmap.md §3.3.
type ExactCoverage struct {
	// State distinguishes a real ratio from a zero-denominator result.
	State CoverageState `json:"state"`
	// Numerator is the exact endpoint-observed count.
	Numerator int `json:"numerator"`
	// Denominator is the exact applicable-resource count.
	Denominator int `json:"denominator"`
}

// SourceSummary contains only recomputed source totals from
// docs/go-authoring-port-roadmap.md §3.3.
type SourceSummary struct {
	// SelectedTotal is the number of authoritative resource rows.
	SelectedTotal int `json:"selected_total"`
	// ApplicableTotal excludes only not_applicable rows.
	ApplicableTotal int `json:"applicable_total"`
	// SourceCallObservedTotal counts observed_http, observed_sdk_call, and dynamic rows.
	SourceCallObservedTotal int `json:"source_call_observed_total"`
	// EndpointObservedTotal counts only observed_http rows.
	EndpointObservedTotal int `json:"endpoint_observed_total"`
	// ClassificationCounts is the complete seven-state partition.
	ClassificationCounts SourceClassificationCounts `json:"classification_counts"`
	// EndpointCoverage is the exact, non-floating coverage representation.
	EndpointCoverage ExactCoverage `json:"endpoint_coverage"`
}

// OpenAPIDocumentState is the report-level optional-adapter state from
// docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIDocumentState string

const (
	// OpenAPIAbsent means no OpenAPI input was selected.
	OpenAPIAbsent OpenAPIDocumentState = "absent"
	// OpenAPIUsable means the required document portions are valid.
	OpenAPIUsable OpenAPIDocumentState = "usable"
	// OpenAPIDegraded means comparison is possible despite an isolated document defect.
	OpenAPIDegraded OpenAPIDocumentState = "degraded"
	// OpenAPIUnavailable means the explicit document cannot be used for comparison.
	OpenAPIUnavailable OpenAPIDocumentState = "unavailable"
)

// OpenAPIReasonCode is the closed adapter diagnostic reason vocabulary from
// docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIReasonCode string

const (
	// OpenAPIReasonUnreadable means the explicit document could not be read.
	OpenAPIReasonUnreadable OpenAPIReasonCode = "unreadable"
	// OpenAPIReasonMalformed means the explicit document is malformed.
	OpenAPIReasonMalformed OpenAPIReasonCode = "malformed"
	// OpenAPIReasonInvalidRoot means the document root shape is invalid.
	OpenAPIReasonInvalidRoot OpenAPIReasonCode = "invalid_root"
	// OpenAPIReasonLocalRefUnresolved means a required explicit local ref failed.
	OpenAPIReasonLocalRefUnresolved OpenAPIReasonCode = "local_ref_unresolved"
	// OpenAPIReasonDegradedOperation means an unrelated operation is invalid.
	OpenAPIReasonDegradedOperation OpenAPIReasonCode = "degraded_unrelated_operation"
)

// OpenAPIComparisonState is the closed six-state comparison partition from
// docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIComparisonState string

const (
	// ComparisonNotAttempted means document state prevented comparison.
	ComparisonNotAttempted OpenAPIComparisonState = "not_attempted"
	// ComparisonNotComparable means the source row has no observed endpoint.
	ComparisonNotComparable OpenAPIComparisonState = "not_comparable"
	// ComparisonCorroborated means source and one OpenAPI operation agree.
	ComparisonCorroborated OpenAPIComparisonState = "corroborated"
	// ComparisonMissingPath means no operation exists for the observed endpoint.
	ComparisonMissingPath OpenAPIComparisonState = "missing_path"
	// ComparisonAmbiguous means multiple OpenAPI operations remain viable.
	ComparisonAmbiguous OpenAPIComparisonState = "ambiguous"
	// ComparisonConflict means trusted identities assert different endpoints.
	ComparisonConflict OpenAPIComparisonState = "conflict"
)

// OpenAPIComparisonBasis records the source-backed identity that permits a
// comparison claim under docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIComparisonBasis string

const (
	// ComparisonBasisSourceEndpoint compares against the observed source endpoint.
	ComparisonBasisSourceEndpoint OpenAPIComparisonBasis = "source_endpoint"
	// ComparisonBasisTrustedSharedIdentity is a trusted shared source/OpenAPI identity.
	ComparisonBasisTrustedSharedIdentity OpenAPIComparisonBasis = "trusted_shared_identity"
	// ComparisonBasisExplicitBinding is a reviewed explicit source/OpenAPI binding.
	ComparisonBasisExplicitBinding OpenAPIComparisonBasis = "explicit_binding"
)

// OpenAPIOperationCandidate preserves one operation used in comparison under
// docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIOperationCandidate struct {
	// OperationID is the optional OpenAPI operationId.
	OperationID *string `json:"operation_id"`
	// Method is the normalized uppercase HTTP method.
	Method string `json:"method"`
	// PathTemplate is the normalized OpenAPI path template.
	PathTemplate string `json:"path_template"`
}

// OpenAPIComparisonRow is one resource comparison row from
// docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIComparisonRow struct {
	// State is exactly one member of the six-state comparison partition.
	State OpenAPIComparisonState `json:"state"`
	// Basis is required for corroborated, ambiguous, and conflict outcomes.
	Basis *OpenAPIComparisonBasis `json:"basis"`
	// BasisReference identifies the reviewed shared identity or explicit binding.
	BasisReference *string `json:"basis_reference"`
	// Operations preserves the exact viable/corroborating/conflicting operations.
	Operations []OpenAPIOperationCandidate `json:"operations"`
}

// OpenAPIComparisonCounts records the complete comparison partition from
// docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIComparisonCounts struct {
	// NotAttempted counts not_attempted rows.
	NotAttempted int `json:"not_attempted"`
	// NotComparable counts not_comparable rows.
	NotComparable int `json:"not_comparable"`
	// Corroborated counts corroborated rows.
	Corroborated int `json:"corroborated"`
	// MissingPath counts missing_path rows.
	MissingPath int `json:"missing_path"`
	// Ambiguous counts ambiguous rows.
	Ambiguous int `json:"ambiguous"`
	// Conflict counts conflict rows.
	Conflict int `json:"conflict"`
}

// OpenAPIComparisonSummary contains recomputed comparison totals from
// docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIComparisonSummary struct {
	// ComparisonEligibleTotal equals the number of observed_http source rows.
	ComparisonEligibleTotal int `json:"comparison_eligible_total"`
	// DegradedComparisonTotal annotates every eligible row only in degraded mode.
	DegradedComparisonTotal int `json:"degraded_comparison_total"`
	// ComparisonCounts is the complete six-state partition.
	ComparisonCounts OpenAPIComparisonCounts `json:"comparison_counts"`
}

// OpenAPIDiagnosticsReport is the isolated optional-adapter accounting required by
// docs/go-authoring-port-roadmap.md §3.6.
type OpenAPIDiagnosticsReport struct {
	// Kind is always infrawright.openapi_diagnostics.
	Kind string `json:"kind"`
	// SchemaVersion is always 1.
	SchemaVersion int `json:"schema_version"`
	// SourceTrust must equal the source report trust state.
	SourceTrust SourceTrust `json:"source_trust"`
	// SourceManifestSHA256 must equal the source report manifest digest.
	SourceManifestSHA256 *string `json:"source_manifest_sha256"`
	// SourceReportSHA256 binds the exact isolated source report bytes.
	SourceReportSHA256 string `json:"source_report_sha256"`
	// DocumentState is the single report-level document state.
	DocumentState OpenAPIDocumentState `json:"document_state"`
	// ReasonCode is required only for degraded or unavailable document states.
	ReasonCode *OpenAPIReasonCode `json:"reason_code"`
	// Comparisons has exactly the same resource keys as the source table.
	Comparisons map[string]OpenAPIComparisonRow `json:"comparisons"`
	// Summary is recomputed only from Comparisons and the source table.
	Summary OpenAPIComparisonSummary `json:"summary"`
}

// SourceEvidenceReport is the closed source-only aggregate contract defined by
// docs/go-authoring-port-roadmap.md §3.3.
type SourceEvidenceReport struct {
	// Kind is always infrawright.source_evidence_report.
	Kind string `json:"kind"`
	// SchemaVersion is always 1.
	SchemaVersion int `json:"schema_version"`
	// SourceTrust controls qualification and legacy_mapped semantics.
	SourceTrust SourceTrust `json:"source_trust"`
	// SourceManifestSHA256 binds verified report rows to their manifest.
	SourceManifestSHA256 *string `json:"source_manifest_sha256"`
	// InputProvenanceSHA256 binds the exact emitted input-provenance.json bytes.
	InputProvenanceSHA256 string `json:"input_provenance_sha256"`
	// Resources is the authoritative resource-keyed source table.
	Resources map[string]SourceEvidenceRow `json:"resources"`
	// Summary is recomputed from Resources.
	Summary SourceSummary `json:"summary"`
}
