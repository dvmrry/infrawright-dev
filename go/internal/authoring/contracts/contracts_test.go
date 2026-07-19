package contracts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const testSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestSourceProvenanceVerifiedManifestAndPortablePaths(t *testing.T) {
	provenance := validProvenance()
	rendered, err := RenderSourceProvenance(provenance)
	if err != nil {
		t.Fatalf("RenderSourceProvenance(valid) error = %v, want nil", err)
	}
	if strings.Contains(rendered, "source_trust") || strings.Contains(rendered, "/private/") {
		t.Errorf("RenderSourceProvenance(valid) leaked trust/local-root data:\n%s", rendered)
	}
	decoded, err := DecodeSourceProvenance([]byte(rendered))
	if err != nil {
		t.Fatalf("DecodeSourceProvenance(rendered) error = %v, want nil", err)
	}
	second, err := RenderSourceProvenance(decoded)
	if err != nil {
		t.Fatalf("RenderSourceProvenance(decoded) error = %v, want nil", err)
	}
	if second != rendered {
		t.Errorf("RenderSourceProvenance round trip differs\ngot:\n%s\nwant:\n%s", second, rendered)
	}

	for _, badPath := range []string{"/private/tmp/provider.go", `C:\\provider\\resource.go`, "../resource.go", "provider/evil\x00.go"} {
		mutated := validProvenance()
		mutated.Provider.Files[0].Path = badPath
		if err := ValidateSourceProvenance(mutated); err == nil {
			t.Errorf("ValidateSourceProvenance(path=%q) error = nil, want portable-path error", badPath)
		}
	}
}

func TestSourceProvenanceSchemaIsClosedAndDeterministic(t *testing.T) {
	rendered, err := RenderSourceProvenance(validProvenance())
	if err != nil {
		t.Fatalf("RenderSourceProvenance(valid) error = %v, want nil", err)
	}
	invalid := strings.Replace(rendered, "{\n", "{\n  \"source_trust\": \"unverified\",\n", 1)
	var first string
	for attempt := 0; attempt < 20; attempt++ {
		_, err := DecodeSourceProvenance([]byte(invalid))
		if err == nil {
			t.Fatal("DecodeSourceProvenance(unverified manifest) error = nil, want closed-schema error")
		}
		var contractErr *ContractError
		if !errors.As(err, &contractErr) || contractErr.Code != ErrorInvalidStructure {
			t.Fatalf("DecodeSourceProvenance(unverified manifest) error = %T %v, want ErrorInvalidStructure", err, err)
		}
		if attempt == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Errorf("DecodeSourceProvenance deterministic error attempt %d = %q, want %q", attempt, err, first)
		}
	}
}

func TestSourceProvenanceBindsPortableLocalReplace(t *testing.T) {
	provenance := validProvenance()
	provenance.ProviderModule.LocalReplaces = []LocalModuleReplaceBinding{
		{ModulePath: "example.test/sdk", LocalPath: "../synthetic-sdk"},
	}
	if err := ValidateSourceProvenance(provenance); err != nil {
		t.Fatalf("ValidateSourceProvenance(portable local replace) error = %v, want nil", err)
	}

	absolute := validProvenance()
	absolute.ProviderModule.LocalReplaces = []LocalModuleReplaceBinding{
		{ModulePath: "example.test/sdk", LocalPath: "/private/tmp/synthetic-sdk"},
	}
	if err := ValidateSourceProvenance(absolute); err == nil {
		t.Error("ValidateSourceProvenance(absolute local replace) error = nil, want path-leakage error")
	}

	unbound := validProvenance()
	unbound.ProviderModule.LocalReplaces = []LocalModuleReplaceBinding{
		{ModulePath: "example.test/unbound", LocalPath: "../unbound"},
	}
	if err := ValidateSourceProvenance(unbound); err == nil {
		t.Error("ValidateSourceProvenance(unbound local replace) error = nil, want SDK-binding error")
	}
}

func TestPublicProvenanceRejectsInvalidGoModuleIdentitiesWithoutReflection(t *testing.T) {
	tests := []struct {
		name    string
		invalid string
		check   func() error
	}{
		{
			name:    "provider module path",
			invalid: "Bad Provider Path",
			check: func() error {
				provenance := validProvenance()
				provenance.Provider.ModulePath = "Bad Provider Path"
				return ValidateSourceProvenance(provenance)
			},
		},
		{
			name:    "SDK major version mismatch",
			invalid: "v1.2.3",
			check: func() error {
				provenance := validProvenance()
				provenance.SDKs[0].ModulePath = "example.test/sdk/v2"
				return ValidateSourceProvenance(provenance)
			},
		},
		{
			name:    "unverified provider module path",
			invalid: "Bad Observation Path",
			check: func() error {
				input := validUnverifiedInputProvenance()
				input.UnverifiedObservation.ProviderModulePath = "Bad Observation Path"
				return ValidateInputProvenance(input)
			},
		},
		{
			name:    "unverified SDK version",
			invalid: "not-a-version",
			check: func() error {
				input := validUnverifiedInputProvenance()
				input.UnverifiedObservation.SDKs[0].ModuleVersion = "not-a-version"
				return ValidateInputProvenance(input)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.check()
			if err == nil {
				t.Fatalf("module validation for %s error = nil, want error", test.name)
			}
			if strings.Contains(err.Error(), test.invalid) {
				t.Errorf("module validation for %s error = %q, must not reflect invalid input", test.name, err)
			}
		})
	}
}

func TestInputProvenanceTrustUnionAndOptionalOpenAPIBindings(t *testing.T) {
	manifest := validProvenance()
	manifest.OpenAPI = &OpenAPIInputBinding{
		Document:  FileBinding{Path: "openapi/api.yaml", SHA256: testSHA},
		LocalRefs: []FileBinding{{Path: "openapi/components.yaml", SHA256: testSHA}},
	}
	renderedManifest, err := RenderSourceProvenance(manifest)
	if err != nil {
		t.Fatalf("RenderSourceProvenance(manifest) error = %v, want nil", err)
	}
	verified := InputProvenance{
		Kind:                 "infrawright.input_provenance",
		SchemaVersion:        1,
		SourceTrust:          SourceTrustVerified,
		SourceManifestSHA256: stringPointer(sha256Text([]byte(renderedManifest))),
		SourceManifest:       &manifest,
	}
	rendered, err := RenderInputProvenance(verified)
	if err != nil {
		t.Fatalf("RenderInputProvenance(verified) error = %v, want nil", err)
	}
	if _, err := DecodeInputProvenance([]byte(rendered)); err != nil {
		t.Errorf("DecodeInputProvenance(verified rendered) error = %v, want nil", err)
	}
	digestMismatch := verified
	digestMismatch.SourceManifestSHA256 = stringPointer(testSHA)
	if err := ValidateInputProvenance(digestMismatch); err == nil {
		t.Error("ValidateInputProvenance(verified manifest digest mismatch) error = nil, want digest error")
	}

	unverified := InputProvenance{
		Kind:          "infrawright.input_provenance",
		SchemaVersion: 1,
		SourceTrust:   SourceTrustUnverified,
		UnverifiedObservation: &UnverifiedSourceObservation{
			ProviderModulePath: "example.test/provider",
			ProviderFiles:      []FileBinding{{Path: "provider/provider.go", SHA256: testSHA}},
			TerraformSchema:    FileBinding{Path: "schema/provider.json", SHA256: testSHA},
			SDKs:               []UnverifiedSDKObservation{},
			Selection:          SelectionBinding{ResourceTypes: []string{}, Filters: []SelectionFilterBinding{}},
		},
	}
	if _, err := RenderInputProvenance(unverified); err != nil {
		t.Errorf("RenderInputProvenance(unverified) error = %v, want nil", err)
	}
	unverified.SourceManifestSHA256 = stringPointer(testSHA)
	if err := ValidateInputProvenance(unverified); err == nil {
		t.Error("ValidateInputProvenance(unverified with manifest digest) error = nil, want trust-union error")
	}
}

func TestSourceEvidenceFailClosedRegressions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SourceEvidenceReport)
	}{
		{
			name: "endpoint and reason in one chain",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_observed_http"]
				row.Chains[0].ReasonCode = reasonPointer(ReasonDynamicPath)
				report.Resources["resource_observed_http"] = row
			},
		},
		{
			name: "no source carries endpoint chain",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_no_source"]
				observed := report.Resources["resource_observed_http"]
				row.Chains = observed.Chains
				report.Resources["resource_no_source"] = row
			},
		},
		{
			name: "unresolved carries SDK evidence",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_unresolved"]
				sdk := report.Resources["resource_observed_sdk"].Chains[0]
				sdk.ReasonCode = reasonPointer(ReasonCallChainUnresolved)
				row.Chains = []SourceEvidenceChain{sdk}
				report.Resources["resource_unresolved"] = row
			},
		},
		{
			name: "unsorted ambiguous chains",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_ambiguous"]
				row.Chains[0], row.Chains[1] = row.Chains[1], row.Chains[0]
				report.Resources["resource_ambiguous"] = row
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validSevenStateReport()
			test.mutate(&report)
			if err := ValidateSourceEvidenceReport(report); err == nil {
				t.Errorf("ValidateSourceEvidenceReport(%s) error = nil, want fail-closed error", test.name)
			}
		})
	}
}

func TestUnresolvedDispatchPreservesPartialEvidenceWithoutOverclaim(t *testing.T) {
	unresolved := validSevenStateReport()
	row := unresolved.Resources["resource_unresolved"]
	row.Chains[0].Steps[0].Kind = CallUnresolvedDispatch
	row.Chains[0].Steps[0].Symbol = "unresolvedReader.Read"
	row.Chains[0].Steps[0].Callee = nil
	unresolved.Resources["resource_unresolved"] = row
	if err := ValidateSourceEvidenceReport(unresolved); err != nil {
		t.Fatalf("ValidateSourceEvidenceReport(unresolved_dispatch) error = %v, want nil", err)
	}
	rendered, err := RenderSourceEvidenceReport(unresolved)
	if err != nil {
		t.Fatalf("RenderSourceEvidenceReport(unresolved_dispatch) error = %v, want nil", err)
	}
	if _, err := DecodeSourceEvidenceReport([]byte(rendered)); err != nil {
		t.Errorf("DecodeSourceEvidenceReport(unresolved_dispatch rendered) error = %v, want nil", err)
	}

	dynamic := validSevenStateReport()
	dynamicRow := dynamic.Resources["resource_dynamic"]
	dynamicRow.Chains[0].Steps[0].Kind = CallUnresolvedDispatch
	dynamicRow.Chains[0].Steps[0].Callee = nil
	dynamicRow.Chains[0].Steps = dynamicRow.Chains[0].Steps[:1]
	dynamicRow.Chains[0].ReasonCode = reasonPointer(ReasonDynamicDispatch)
	dynamic.Resources["resource_dynamic"] = dynamicRow
	if err := ValidateSourceEvidenceReport(dynamic); err != nil {
		t.Errorf("ValidateSourceEvidenceReport(dynamic unresolved_dispatch) error = %v, want nil", err)
	}

	ambiguous := validSevenStateReport()
	ambiguousRow := ambiguous.Resources["resource_ambiguous"]
	partialA := SourceEvidenceChain{
		Steps: []SourceCallStep{{
			Kind: CallUnresolvedDispatch, Symbol: "aDispatch.Read",
			Caller:   *ambiguousRow.ReadCallback,
			Location: sourceLocation("provider/resource_widget.go", "resourceWidgetRead"),
		}},
		ReasonCode: reasonPointer(ReasonCallChainUnresolved),
	}
	partialB := partialA
	partialB.Steps = []SourceCallStep{{
		Kind: CallUnresolvedDispatch, Symbol: "bDispatch.Read",
		Caller:   *ambiguousRow.ReadCallback,
		Location: sourceLocation("provider/resource_widget.go", "resourceWidgetRead"),
	}}
	ambiguousRow.Chains = []SourceEvidenceChain{partialA, partialB}
	ambiguous.Resources["resource_ambiguous"] = ambiguousRow
	if err := ValidateSourceEvidenceReport(ambiguous); err != nil {
		t.Errorf("ValidateSourceEvidenceReport(ambiguous unresolved_dispatch) error = %v, want nil", err)
	}

	tests := []struct {
		name   string
		mutate func(*SourceEvidenceReport)
	}{
		{
			name: "import path",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_unresolved"]
				row.Chains[0].Steps[0].Kind = CallUnresolvedDispatch
				importPath := "example.test/dynamic"
				row.Chains[0].Steps[0].ImportPath = &importPath
				report.Resources["resource_unresolved"] = row
			},
		},
		{
			name: "observed http",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_observed_http"]
				row.Chains[0].Steps[0].Kind = CallUnresolvedDispatch
				report.Resources["resource_observed_http"] = row
			},
		},
		{
			name: "observed SDK call",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_observed_sdk"]
				row.Chains[0].Steps = append([]SourceCallStep{{
					Kind: CallUnresolvedDispatch, Symbol: "unresolvedReader.Read",
					Location: sourceLocation("provider/resource_widget.go", "resourceWidgetRead"),
				}}, row.Chains[0].Steps...)
				report.Resources["resource_observed_sdk"] = row
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validSevenStateReport()
			test.mutate(&report)
			if err := ValidateSourceEvidenceReport(report); err == nil {
				t.Errorf("ValidateSourceEvidenceReport(unresolved_dispatch %s) error = nil, want fail-closed error", test.name)
			}
		})
	}
}

func TestSourceReportBindsExactInputProvenance(t *testing.T) {
	report := validSevenStateReport()
	input := bindReportToVerifiedInput(t, &report)
	if err := ValidateSourceEvidenceReportAgainstInput(report, input); err != nil {
		t.Fatalf("ValidateSourceEvidenceReportAgainstInput(valid) error = %v, want nil", err)
	}
	report.InputProvenanceSHA256 = testSHA
	if err := ValidateSourceEvidenceReportAgainstInput(report, input); err == nil {
		t.Error("ValidateSourceEvidenceReportAgainstInput(mismatch) error = nil, want digest error")
	}
}

func TestReviewedNotApplicableSelectionAuthorizationIsExact(t *testing.T) {
	report := validSevenStateReport()
	input := bindReportToVerifiedInput(t, &report)
	if err := ValidateSourceEvidenceReportAgainstInput(report, input); err != nil {
		t.Fatalf("ValidateSourceEvidenceReportAgainstInput(valid reviewed authorization) error = %v, want nil", err)
	}

	t.Run("generic filters remain opaque", func(t *testing.T) {
		candidate := validSevenStateReport()
		candidateInput := bindReportToVerifiedInput(t, &candidate)
		candidateInput.SourceManifest.Selection.Filters = append(
			candidateInput.SourceManifest.Selection.Filters,
			SelectionFilterBinding{Name: "suffix", Values: []string{"_widget"}},
		)
		candidateInput = rebindReportToInput(t, &candidate, candidateInput)
		if err := ValidateSourceEvidenceReportAgainstInput(candidate, candidateInput); err != nil {
			t.Errorf("ValidateSourceEvidenceReportAgainstInput(generic filter) error = %v, want nil", err)
		}
	})

	t.Run("not applicable row without filter", func(t *testing.T) {
		candidate := validSevenStateReport()
		candidateInput := bindReportToVerifiedInput(t, &candidate)
		candidateInput.SourceManifest.Selection.Filters = []SelectionFilterBinding{{Name: "prefix", Values: []string{"example_"}}}
		candidateInput = rebindReportToInput(t, &candidate, candidateInput)
		if err := ValidateSourceEvidenceReportAgainstInput(candidate, candidateInput); err == nil {
			t.Error("ValidateSourceEvidenceReportAgainstInput(not_applicable without authorization) error = nil, want exact-authorization error")
		}
	})

	t.Run("filter value not selected", func(t *testing.T) {
		candidate := validSevenStateReport()
		candidateInput := bindReportToVerifiedInput(t, &candidate)
		filters := candidateInput.SourceManifest.Selection.Filters
		filters[1].Values = append(filters[1].Values, "resource_not_selected")
		if err := ValidateSourceEvidenceReportAgainstInput(candidate, candidateInput); err == nil {
			t.Error("ValidateSourceEvidenceReportAgainstInput(unselected reviewed authorization) error = nil, want selection error")
		}
	})

	t.Run("filter value row remains applicable", func(t *testing.T) {
		candidate := validSevenStateReport()
		candidateInput := bindReportToVerifiedInput(t, &candidate)
		candidateInput.SourceManifest.Selection.Filters[1].Values = []string{"resource_observed_http"}
		candidateInput = rebindReportToInput(t, &candidate, candidateInput)
		if err := ValidateSourceEvidenceReportAgainstInput(candidate, candidateInput); err == nil {
			t.Error("ValidateSourceEvidenceReportAgainstInput(authorization for applicable row) error = nil, want correspondence error")
		}
	})

	t.Run("new not applicable row absent from filter", func(t *testing.T) {
		candidate := validSevenStateReport()
		candidateInput := bindReportToVerifiedInput(t, &candidate)
		row := candidate.Resources["resource_no_source"]
		row.Classification = SourceNotApplicable
		row.ReasonCode = reasonPointer(ReasonReviewedNotApplicable)
		candidate.Resources["resource_no_source"] = row
		candidate.Summary.ClassificationCounts.NoSource--
		candidate.Summary.ClassificationCounts.NotApplicable++
		candidate.Summary.ApplicableTotal--
		candidate.Summary.EndpointCoverage.Denominator--
		if err := ValidateSourceEvidenceReport(candidate); err != nil {
			t.Fatalf("ValidateSourceEvidenceReport(additional not_applicable row) error = %v, want nil before input join", err)
		}
		if err := ValidateSourceEvidenceReportAgainstInput(candidate, candidateInput); err == nil {
			t.Error("ValidateSourceEvidenceReportAgainstInput(not_applicable absent from filter) error = nil, want correspondence error")
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*InputProvenance)
	}{
		{
			name: "duplicate reserved filter",
			mutate: func(candidateInput *InputProvenance) {
				candidateInput.SourceManifest.Selection.Filters = append(
					candidateInput.SourceManifest.Selection.Filters,
					SelectionFilterBinding{Name: SelectionFilterReviewedNotApplicable, Values: []string{"resource_not_applicable"}},
				)
			},
		},
		{
			name: "duplicate reserved value",
			mutate: func(candidateInput *InputProvenance) {
				values := candidateInput.SourceManifest.Selection.Filters[1].Values
				candidateInput.SourceManifest.Selection.Filters[1].Values = append(values, values[0])
			},
		},
		{
			name: "empty reserved filter",
			mutate: func(candidateInput *InputProvenance) {
				candidateInput.SourceManifest.Selection.Filters[1].Values = []string{}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := validSevenStateReport()
			candidateInput := bindReportToVerifiedInput(t, &candidate)
			test.mutate(&candidateInput)
			if err := ValidateSourceEvidenceReportAgainstInput(candidate, candidateInput); err == nil {
				t.Errorf("ValidateSourceEvidenceReportAgainstInput(%s) error = nil, want filter error", test.name)
			}
		})
	}
}

func TestSourceReportExactSelectionAndSourceIdentityBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SourceEvidenceReport)
	}{
		{
			name: "dropped row",
			mutate: func(report *SourceEvidenceReport) {
				delete(report.Resources, "resource_not_applicable")
				report.Summary.SelectedTotal--
				report.Summary.ClassificationCounts.NotApplicable--
			},
		},
		{
			name: "added row",
			mutate: func(report *SourceEvidenceReport) {
				report.Resources["resource_added"] = report.Resources["resource_not_applicable"]
				report.Summary.SelectedTotal++
				report.Summary.ClassificationCounts.NotApplicable++
			},
		},
		{
			name: "forged replacement key",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_no_source"]
				delete(report.Resources, "resource_no_source")
				report.Resources["resource_forged"] = row
			},
		},
		{
			name: "unbound provider path",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_dynamic"]
				row.ReadCallback.Location.Path = "provider/forged.go"
				report.Resources["resource_dynamic"] = row
			},
		},
		{
			name: "unbound SDK source path",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_observed_sdk"]
				row.Chains[0].SDKCall.Location.Path = "widgets/forged.go"
				report.Resources["resource_observed_sdk"] = row
			},
		},
		{
			name: "wrong SDK version",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_observed_sdk"]
				row.Chains[0].SDKCall.ModuleVersion = "v1.2.4"
				report.Resources["resource_observed_sdk"] = row
			},
		},
		{
			name: "unbound SDK package",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_observed_sdk"]
				unbound := "example.test/unbound/widgets"
				row.Chains[0].Steps[0].ImportPath = &unbound
				row.Chains[0].SDKCall.PackagePath = unbound
				report.Resources["resource_observed_sdk"] = row
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validSevenStateReport()
			input := bindReportToVerifiedInput(t, &report)
			test.mutate(&report)
			if err := ValidateSourceEvidenceReportAgainstInput(report, input); err == nil {
				t.Errorf("ValidateSourceEvidenceReportAgainstInput(%s) error = nil, want exact-binding error", test.name)
			}
		})
	}
}

func TestUnverifiedReportStillBindsObservedModuleVersionAndFiles(t *testing.T) {
	report := validSevenStateReport()
	input := bindReportToUnverifiedInput(t, &report)
	if err := ValidateSourceEvidenceReportAgainstInput(report, input); err != nil {
		t.Fatalf("ValidateSourceEvidenceReportAgainstInput(valid unverified binding) error = %v, want nil", err)
	}

	row := report.Resources["resource_observed_sdk"]
	row.Chains[0].SDKCall.ModuleVersion = "v1.2.4"
	report.Resources["resource_observed_sdk"] = row
	if err := ValidateSourceEvidenceReportAgainstInput(report, input); err == nil {
		t.Error("ValidateSourceEvidenceReportAgainstInput(unverified wrong SDK version) error = nil, want identity-binding error")
	}

	missingAuthorization := validSevenStateReport()
	unverifiedInput := bindReportToUnverifiedInput(t, &missingAuthorization)
	unverifiedInput.UnverifiedObservation.Selection.Filters = []SelectionFilterBinding{}
	unverifiedInput = rebindReportToInput(t, &missingAuthorization, unverifiedInput)
	if err := ValidateSourceEvidenceReportAgainstInput(missingAuthorization, unverifiedInput); err == nil {
		t.Error("ValidateSourceEvidenceReportAgainstInput(unverified missing reviewed authorization) error = nil, want exact-authorization error")
	}
}

func TestSDKSourceMissingPreservesProviderCallsiteWithoutInventedSDKBinding(t *testing.T) {
	report := validSDKSourceMissingReport()
	if err := ValidateSourceEvidenceReport(report); err != nil {
		t.Fatalf("ValidateSourceEvidenceReport(valid sdk_source_missing) error = %v, want nil", err)
	}
	rendered, err := RenderSourceEvidenceReport(report)
	if err != nil {
		t.Fatalf("RenderSourceEvidenceReport(valid sdk_source_missing) error = %v, want nil", err)
	}
	if _, err := DecodeSourceEvidenceReport([]byte(rendered)); err != nil {
		t.Errorf("DecodeSourceEvidenceReport(valid sdk_source_missing) error = %v, want nil", err)
	}
	input := bindReportToVerifiedInput(t, &report)
	if err := ValidateSourceEvidenceReportAgainstInput(report, input); err != nil {
		t.Errorf("ValidateSourceEvidenceReportAgainstInput(sdk_source_missing callsite) error = %v, want nil", err)
	}
	if got, want := report.Summary.SourceCallObservedTotal, 3; got != want {
		t.Errorf("validSDKSourceMissingReport().Summary.SourceCallObservedTotal = %d, want %d", got, want)
	}
	if viableChain(report.Resources["resource_no_source"].Chains[0]) {
		t.Error("viableChain(sdk_source_missing) = true, want false")
	}

	t.Run("bound SDK owner", func(t *testing.T) {
		candidate := validSDKSourceMissingReport()
		boundInput := bindReportToVerifiedInput(t, &candidate)
		row := candidate.Resources["resource_no_source"]
		row.Chains[0].Steps[0].ImportPath = stringPointer("example.test/sdk/widgets")
		candidate.Resources["resource_no_source"] = row
		if err := ValidateSourceEvidenceReport(candidate); err != nil {
			t.Fatalf("ValidateSourceEvidenceReport(bound-owner claim shape) error = %v, want nil before input join", err)
		}
		if err := ValidateSourceEvidenceReportAgainstInput(candidate, boundInput); err == nil {
			t.Error("ValidateSourceEvidenceReportAgainstInput(bound-owner sdk_source_missing) error = nil, want false-claim error")
		}
	})

	tests := []struct {
		name   string
		mutate func(*SourceEvidenceReport)
	}{
		{
			name: "nil import",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				row.Chains[0].Steps[0].ImportPath = nil
				candidate.Resources["resource_no_source"] = row
			},
		},
		{
			name: "invalid import",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				row.Chains[0].Steps[0].ImportPath = stringPointer("example.test/sdk//widgets")
				candidate.Resources["resource_no_source"] = row
			},
		},
		{
			name: "nonterminal",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				row.Chains[0].Steps = append(row.Chains[0].Steps, SourceCallStep{
					Kind: CallRawHTTP, Symbol: "client.NewRequest", Caller: *row.ReadCallback,
					Location: sourceLocation("provider/resource_widget.go", "resourceWidgetRead"),
				})
				candidate.Resources["resource_no_source"] = row
			},
		},
		{
			name: "SDK callsite",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				row.Chains[0].Steps[0].Location = sdkSourceLocation("example.test/sdk", "widgets/get.go", "Get")
				candidate.Resources["resource_no_source"] = row
			},
		},
		{
			name: "callee",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				callee := sdkSourceSymbol("example.test/sdk", "widgets/get.go", "Get", "Get")
				row.Chains[0].Steps[0].Callee = &callee
				candidate.Resources["resource_no_source"] = row
			},
		},
		{
			name: "SDK call evidence",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				callee := sdkSourceSymbol("example.test/sdk", "widgets/get.go", "Get", "Get")
				row.Chains[0].SDKCall = sdkCallPointer(SDKCallEvidence{
					ModulePath: "example.test/sdk", ModuleVersion: "v1.2.3",
					PackagePath: callee.PackagePath, Symbol: callee.Symbol, Location: callee.Location,
				})
				candidate.Resources["resource_no_source"] = row
			},
		},
		{
			name: "endpoint evidence",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				row.Chains[0].Endpoint = &HTTPEndpointEvidence{
					Origin: EndpointOriginProvider, Method: "GET", PathTemplate: "/widgets/{id}",
					Location: row.Chains[0].Steps[0].Location,
				}
				candidate.Resources["resource_no_source"] = row
			},
		},
		{
			name: "wrong row reason",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				row.ReasonCode = reasonPointer(ReasonProviderSourceMissing)
				candidate.Resources["resource_no_source"] = row
			},
		},
		{
			name: "wrong chain reason",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				row.Chains[0].ReasonCode = reasonPointer(ReasonCallChainUnresolved)
				candidate.Resources["resource_no_source"] = row
			},
		},
		{
			name: "wrong classification",
			mutate: func(candidate *SourceEvidenceReport) {
				row := candidate.Resources["resource_no_source"]
				row.Classification = SourceUnresolved
				candidate.Resources["resource_no_source"] = row
				candidate.Summary.ClassificationCounts.NoSource--
				candidate.Summary.ClassificationCounts.Unresolved++
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validSDKSourceMissingReport()
			test.mutate(&candidate)
			if err := ValidateSourceEvidenceReport(candidate); err == nil {
				t.Errorf("ValidateSourceEvidenceReport(sdk_source_missing %s) error = nil, want fail-closed error", test.name)
			}
		})
	}
}

func TestSourceEvidenceSevenStatePartitionAndTrustProjection(t *testing.T) {
	report := validSevenStateReport()
	if err := ValidateSourceEvidenceReport(report); err != nil {
		t.Fatalf("ValidateSourceEvidenceReport(valid seven-state report) error = %v, want nil", err)
	}
	rendered, err := RenderSourceEvidenceReport(report)
	if err != nil {
		t.Fatalf("RenderSourceEvidenceReport(valid) error = %v, want nil", err)
	}
	if strings.Contains(rendered, "openapi") {
		t.Errorf("RenderSourceEvidenceReport(valid) contains OpenAPI bytes:\n%s", rendered)
	}
	if _, err := DecodeSourceEvidenceReport([]byte(rendered)); err != nil {
		t.Errorf("DecodeSourceEvidenceReport(rendered) error = %v, want nil", err)
	}

	unverified := validSevenStateReport()
	unverified.SourceTrust = SourceTrustUnverified
	unverified.SourceManifestSHA256 = nil
	row := unverified.Resources["resource_observed_http"]
	row.LegacyMapped = false
	unverified.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(unverified); err != nil {
		t.Errorf("ValidateSourceEvidenceReport(valid unverified) error = %v, want nil", err)
	}
	row.LegacyMapped = true
	unverified.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(unverified); err == nil {
		t.Error("ValidateSourceEvidenceReport(unverified legacy_mapped=true) error = nil, want trust-projection error")
	}
}

func TestSourceEvidenceClassificationShapesFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SourceEvidenceReport)
	}{
		{
			name: "observed without endpoint",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_observed_http"]
				row.Chains[0].Endpoint = nil
				report.Resources["resource_observed_http"] = row
			},
		},
		{
			name: "create-only style unrooted dynamic",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_dynamic"]
				row.ReadCallback = nil
				report.Resources["resource_dynamic"] = row
			},
		},
		{
			name: "ambiguous with one candidate",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_ambiguous"]
				row.Chains = row.Chains[:1]
				report.Resources["resource_ambiguous"] = row
			},
		},
		{
			name: "absolute source location",
			mutate: func(report *SourceEvidenceReport) {
				row := report.Resources["resource_observed_http"]
				row.Chains[0].Endpoint.Location.Path = "/tmp/sdk/request.go"
				report.Resources["resource_observed_http"] = row
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validSevenStateReport()
			test.mutate(&report)
			if err := ValidateSourceEvidenceReport(report); err == nil {
				t.Errorf("ValidateSourceEvidenceReport(%s) error = nil, want semantic error", test.name)
			}
		})
	}
}

func TestSourceLocationsDistinguishPackageBindingsAndFunctionCalls(t *testing.T) {
	report := validSevenStateReport()
	if report.Resources["resource_observed_http"].ProviderRegistration.Location.Function != nil {
		t.Fatal("validSevenStateReport() provider registration function != nil, want package-scope binding")
	}

	functionScoped := validSevenStateReport()
	row := functionScoped.Resources["resource_observed_http"]
	row.ProviderRegistration.Location.Function = stringPointer("Provider")
	functionScoped.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(functionScoped); err != nil {
		t.Errorf("ValidateSourceEvidenceReport(function-scoped registration) error = %v, want nil", err)
	}

	missingCallbackFunction := validSevenStateReport()
	row = missingCallbackFunction.Resources["resource_observed_http"]
	row.ReadCallback.Location.Function = nil
	missingCallbackFunction.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(missingCallbackFunction); err == nil {
		t.Error("ValidateSourceEvidenceReport(package-scope Read callback) error = nil, want function-scope error")
	}
}

func TestEndpointEvidenceRequiresMatchingRawHTTPRequestConstruction(t *testing.T) {
	if err := ValidateSourceEvidenceReport(validSevenStateReport()); err != nil {
		t.Fatalf("ValidateSourceEvidenceReport(valid direct and SDK raw_http chains) error = %v, want nil", err)
	}

	directWithoutRaw := validSevenStateReport()
	row := directWithoutRaw.Resources["resource_observed_http"]
	row.Chains[0].Steps = row.Chains[0].Steps[:1]
	directWithoutRaw.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(directWithoutRaw); err == nil {
		t.Error("ValidateSourceEvidenceReport(direct endpoint without raw_http) error = nil, want evidence error")
	}
	directMismatchedLocation := validSevenStateReport()
	row = directMismatchedLocation.Resources["resource_observed_http"]
	row.Chains[0].Endpoint.Location.Column++
	directMismatchedLocation.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(directMismatchedLocation); err == nil {
		t.Error("ValidateSourceEvidenceReport(direct endpoint/raw_http location mismatch) error = nil, want evidence error")
	}

	sdkMismatchedLocation := validSevenStateReport()
	row = sdkMismatchedLocation.Resources["resource_ambiguous"]
	row.Chains[0].Endpoint.Location.Line++
	sdkMismatchedLocation.Resources["resource_ambiguous"] = row
	if err := ValidateSourceEvidenceReport(sdkMismatchedLocation); err == nil {
		t.Error("ValidateSourceEvidenceReport(SDK endpoint/raw_http location mismatch) error = nil, want evidence error")
	}

	sdkWithoutRaw := validSevenStateReport()
	row = sdkWithoutRaw.Resources["resource_ambiguous"]
	row.Chains[0].Steps = row.Chains[0].Steps[:1]
	sdkWithoutRaw.Resources["resource_ambiguous"] = row
	if err := ValidateSourceEvidenceReport(sdkWithoutRaw); err == nil {
		t.Error("ValidateSourceEvidenceReport(SDK endpoint without raw_http) error = nil, want evidence error")
	}

	endpointless := validSevenStateReport().Resources["resource_observed_sdk"]
	if chainHasCallKind(endpointless.Chains[0], CallRawHTTP) {
		t.Error("validSevenStateReport() observed_sdk_call contains raw_http, want endpoint-less classification without it")
	}

	trailingStep := validSevenStateReport()
	row = trailingStep.Resources["resource_observed_http"]
	row.Chains[0].Steps = append(row.Chains[0].Steps, row.Chains[0].Steps[0])
	trailingStep.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(trailingStep); err == nil {
		t.Error("ValidateSourceEvidenceReport(endpoint with step after raw_http) error = nil, want terminal-step error")
	}
}

func TestDynamicRequestEvidenceRequiresTruthfulTerminalStep(t *testing.T) {
	withoutRaw := validSevenStateReport()
	row := withoutRaw.Resources["resource_dynamic"]
	row.Chains[0].Steps = row.Chains[0].Steps[:1]
	withoutRaw.Resources["resource_dynamic"] = row
	if err := ValidateSourceEvidenceReport(withoutRaw); err == nil {
		t.Error("ValidateSourceEvidenceReport(dynamic_path without terminal raw_http) error = nil, want source-call evidence error")
	}

	trailingHelper := validSevenStateReport()
	row = trailingHelper.Resources["resource_dynamic"]
	row.Chains[0].Steps = append(row.Chains[0].Steps, row.Chains[0].Steps[0])
	trailingHelper.Resources["resource_dynamic"] = row
	if err := ValidateSourceEvidenceReport(trailingHelper); err == nil {
		t.Error("ValidateSourceEvidenceReport(dynamic_path with trailing helper) error = nil, want terminal raw_http error")
	}

	dispatchWithoutUnresolved := validSevenStateReport()
	row = dispatchWithoutUnresolved.Resources["resource_dynamic"]
	row.Chains[0].ReasonCode = reasonPointer(ReasonDynamicDispatch)
	dispatchWithoutUnresolved.Resources["resource_dynamic"] = row
	if err := ValidateSourceEvidenceReport(dispatchWithoutUnresolved); err == nil {
		t.Error("ValidateSourceEvidenceReport(dynamic_dispatch without terminal unresolved_dispatch) error = nil, want evidence error")
	}
}

func TestReadRootedEdgesRejectCreateDecoysAndBrokenAdjacency(t *testing.T) {
	createDecoy := validSevenStateReport()
	row := createDecoy.Resources["resource_observed_http"]
	create := sourceSymbol("provider/resource_widget.go", "resourceWidgetCreate", "resourceWidgetCreate")
	row.Chains[0].Steps[0].Caller = create
	row.Chains[0].Steps[0].Location = sourceLocation("provider/resource_widget.go", "resourceWidgetCreate")
	createDecoy.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(createDecoy); err == nil {
		t.Error("ValidateSourceEvidenceReport(Create-only decoy) error = nil, want Read-root error")
	}

	brokenAdjacency := validSevenStateReport()
	row = brokenAdjacency.Resources["resource_observed_http"]
	other := sourceSymbol("provider/resource_widget.go", "otherHelper", "otherHelper")
	row.Chains[0].Steps[1].Caller = other
	row.Chains[0].Steps[1].Location = sourceLocation("provider/resource_widget.go", "otherHelper")
	brokenAdjacency.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(brokenAdjacency); err == nil {
		t.Error("ValidateSourceEvidenceReport(broken caller/callee adjacency) error = nil, want edge error")
	}

	packageDecoy := validSevenStateReport()
	row = packageDecoy.Resources["resource_observed_http"]
	row.Chains[0].Steps[1].Caller.PackagePath = "example.test/provider/forged"
	packageDecoy.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(packageDecoy); err == nil {
		t.Error("ValidateSourceEvidenceReport(package-path-only adjacency decoy) error = nil, want full-symbol edge error")
	}

	unscoped := validSevenStateReport()
	row = unscoped.Resources["resource_observed_http"]
	row.ReadCallback.Location.Origin = ""
	unscoped.Resources["resource_observed_http"] = row
	if err := ValidateSourceEvidenceReport(unscoped); err == nil {
		t.Error("ValidateSourceEvidenceReport(unscoped legacy location) error = nil, want closed-origin error")
	}
}

func TestSourceBindingsRejectCollisionsAndOverlappingSDKOwnership(t *testing.T) {
	collidingPath := validSevenStateReport()
	input := bindReportToVerifiedInput(t, &collidingPath)
	input.SourceManifest.Provider.Files = append(input.SourceManifest.Provider.Files,
		FileBinding{Path: "widgets/get.go", SHA256: testSHA})
	input = rebindReportToInput(t, &collidingPath, input)
	row := collidingPath.Resources["resource_observed_sdk"]
	row.Chains[0].SDKCall.Location = sourceLocation("widgets/get.go", "Get")
	collidingPath.Resources["resource_observed_sdk"] = row
	if err := ValidateSourceEvidenceReportAgainstInput(collidingPath, input); err == nil {
		t.Error("ValidateSourceEvidenceReportAgainstInput(provider/SDK colliding path) error = nil, want SDK-origin error")
	}

	wrongModule := validSevenStateReport()
	row = wrongModule.Resources["resource_observed_sdk"]
	row.Chains[0].SDKCall.Location.SDKModulePath = stringPointer("example.test/other")
	wrongModule.Resources["resource_observed_sdk"] = row
	if err := ValidateSourceEvidenceReport(wrongModule); err == nil {
		t.Error("ValidateSourceEvidenceReport(SDKCall wrong location module) error = nil, want module-binding error")
	}

	crossSDK := validSevenStateReport()
	row = crossSDK.Resources["resource_observed_sdk"]
	firstCallee := *row.Chains[0].Steps[0].Callee
	otherCallee := sdkSourceSymbol("example.test/other-sdk", "client/get.go", "Get", "Get")
	otherImport := "example.test/other-sdk/client"
	row.Chains[0].Steps = append(row.Chains[0].Steps, SourceCallStep{
		Kind: CallSDKReceiverMethod, Symbol: "client.Get", ImportPath: &otherImport,
		Caller: firstCallee, Callee: &otherCallee,
		Location: sdkSourceLocation("example.test/sdk", "widgets/get.go", "Get"),
	})
	crossSDK.Resources["resource_observed_sdk"] = row
	if err := ValidateSourceEvidenceReport(crossSDK); err == nil {
		t.Error("ValidateSourceEvidenceReport(cross-SDK transition) error = nil, want explicit fail-closed error")
	}

	overlapping := validSevenStateReport()
	input = bindReportToVerifiedInput(t, &overlapping)
	nestedRevision := "nested-revision"
	input.SourceManifest.SDKs = append(input.SourceManifest.SDKs, SDKSourceBinding{
		ModulePath: "example.test/sdk/widgets", ModuleVersion: "v1.0.0",
		Repository: "https://example.test/nested-sdk.git", Revision: &nestedRevision,
		Files: []FileBinding{{Path: "get.go", SHA256: testSHA}},
	})
	input = rebindReportToInput(t, &overlapping, input)
	row = overlapping.Resources["resource_observed_sdk"]
	// Both the parent module's widgets/get.go and nested module's get.go declare
	// package example.test/sdk/widgets. Longest-prefix ownership must select nested.
	row.Chains[0].Steps[0].ImportPath = stringPointer("example.test/sdk/widgets")
	overlapping.Resources["resource_observed_sdk"] = row
	if err := ValidateSourceEvidenceReportAgainstInput(overlapping, input); err == nil {
		t.Error("ValidateSourceEvidenceReportAgainstInput(overlapping parent SDK claim) error = nil, want nested-module ownership error")
	}

	slashBoundary := validSevenStateReport()
	input = bindReportToVerifiedInput(t, &slashBoundary)
	siblingRevision := "sibling-revision"
	input.SourceManifest.SDKs = append(input.SourceManifest.SDKs, SDKSourceBinding{
		ModulePath: "example.test/sdkplus", ModuleVersion: "v1.0.0",
		Repository: "https://example.test/sdkplus.git", Revision: &siblingRevision,
		Files: []FileBinding{{Path: "client/get.go", SHA256: testSHA}},
	})
	row = slashBoundary.Resources["resource_observed_sdk"]
	entry := sdkSourceSymbol("example.test/sdkplus", "client/get.go", "Get", "Get")
	importPath := "example.test/sdkplus/client"
	row.Chains[0].Steps[0].ImportPath = &importPath
	row.Chains[0].Steps[0].Callee = &entry
	row.Chains[0].SDKCall = sdkCallPointer(SDKCallEvidence{
		ModulePath: "example.test/sdkplus", ModuleVersion: "v1.0.0",
		PackagePath: entry.PackagePath, Symbol: entry.Symbol, Location: entry.Location,
	})
	slashBoundary.Resources["resource_observed_sdk"] = row
	input = rebindReportToInput(t, &slashBoundary, input)
	if err := ValidateSourceEvidenceReportAgainstInput(slashBoundary, input); err != nil {
		t.Errorf("ValidateSourceEvidenceReportAgainstInput(slash-boundary sibling SDK) error = %v, want nil", err)
	}

	wrongEntry := validSevenStateReport()
	input = bindReportToVerifiedInput(t, &wrongEntry)
	row = wrongEntry.Resources["resource_observed_sdk"]
	row.Chains[0].SDKCall.Symbol = "DifferentEntry"
	wrongEntry.Resources["resource_observed_sdk"] = row
	if err := ValidateSourceEvidenceReportAgainstInput(wrongEntry, input); err == nil {
		t.Error("ValidateSourceEvidenceReportAgainstInput(SDKCall not first SDK callee) error = nil, want entry-identity error")
	}
}

func TestStrictDecodersRejectNoncanonicalInputAndReportBytes(t *testing.T) {
	report := validSevenStateReport()
	input := bindReportToVerifiedInput(t, &report)
	renderedInput, err := RenderInputProvenance(input)
	if err != nil {
		t.Fatalf("RenderInputProvenance(canonical regression) error = %v, want nil", err)
	}
	renderedReport, err := RenderSourceEvidenceReport(report)
	if err != nil {
		t.Fatalf("RenderSourceEvidenceReport(canonical regression) error = %v, want nil", err)
	}
	for _, test := range []struct {
		name   string
		decode func([]byte) error
		data   string
	}{
		{name: "input whitespace", data: " " + renderedInput, decode: func(data []byte) error { _, err := DecodeInputProvenance(data); return err }},
		{name: "input key order", data: reverseTopLevelObject(t, renderedInput), decode: func(data []byte) error { _, err := DecodeInputProvenance(data); return err }},
		{name: "report whitespace", data: " " + renderedReport, decode: func(data []byte) error { _, err := DecodeSourceEvidenceReport(data); return err }},
		{name: "report key order", data: reverseTopLevelObject(t, renderedReport), decode: func(data []byte) error { _, err := DecodeSourceEvidenceReport(data); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.decode([]byte(test.data)); err == nil {
				t.Errorf("strict decode of %s error = nil, want noncanonical-byte error", test.name)
			}
		})
	}
}

func TestOpenAPIDiagnosticsPartitionsAreIsolatedAndCrossJoined(t *testing.T) {
	source := validSevenStateReport()
	tests := []struct {
		name        string
		diagnostics OpenAPIDiagnosticsReport
	}{
		{name: "absent", diagnostics: validAbsentDiagnostics(source)},
		{name: "usable", diagnostics: validUsableDiagnostics(source)},
		{name: "degraded", diagnostics: validDegradedDiagnostics(source)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateOpenAPIDiagnosticsReport(test.diagnostics, source); err != nil {
				t.Fatalf("ValidateOpenAPIDiagnosticsReport(%s) error = %v, want nil", test.name, err)
			}
			rendered, err := RenderOpenAPIDiagnosticsReport(test.diagnostics, source)
			if err != nil {
				t.Fatalf("RenderOpenAPIDiagnosticsReport(%s) error = %v, want nil", test.name, err)
			}
			if _, err := DecodeOpenAPIDiagnosticsReport([]byte(rendered), source); err != nil {
				t.Errorf("DecodeOpenAPIDiagnosticsReport(%s rendered) error = %v, want nil", test.name, err)
			}
		})
	}

	mismatched := validAbsentDiagnostics(source)
	delete(mismatched.Comparisons, "resource_no_source")
	if err := ValidateOpenAPIDiagnosticsReport(mismatched, source); err == nil {
		t.Error("ValidateOpenAPIDiagnosticsReport(mismatched keys) error = nil, want cross-resource-key error")
	}
	digestMismatch := validAbsentDiagnostics(source)
	digestMismatch.SourceReportSHA256 = testSHA
	if err := ValidateOpenAPIDiagnosticsReport(digestMismatch, source); err == nil {
		t.Error("ValidateOpenAPIDiagnosticsReport(source digest mismatch) error = nil, want cross-artifact error")
	}

	conflict := validUsableDiagnostics(source)
	row := conflict.Comparisons["resource_observed_http"]
	row.State = ComparisonConflict
	row.Basis = nil
	row.Operations = []OpenAPIOperationCandidate{{Method: "POST", PathTemplate: "/different"}}
	conflict.Comparisons["resource_observed_http"] = row
	conflict.Summary.ComparisonCounts = OpenAPIComparisonCounts{NotComparable: 6, Conflict: 1}
	if err := ValidateOpenAPIDiagnosticsReport(conflict, source); err == nil {
		t.Error("ValidateOpenAPIDiagnosticsReport(conflict without trusted basis) error = nil, want evidence-basis error")
	}
	basis := ComparisonBasisExplicitBinding
	binding := "reviewed-binding:resource_observed_http"
	row.Basis = &basis
	row.BasisReference = &binding
	conflict.Comparisons["resource_observed_http"] = row
	if err := ValidateOpenAPIDiagnosticsReport(conflict, source); err != nil {
		t.Errorf("ValidateOpenAPIDiagnosticsReport(conflict with explicit binding) error = %v, want nil", err)
	}
}

func TestOpenAPIComparisonEvidenceFailClosedRegressions(t *testing.T) {
	source := validSevenStateReport()
	operationA := OpenAPIOperationCandidate{Method: "GET", PathTemplate: "/a"}
	operationB := OpenAPIOperationCandidate{Method: "GET", PathTemplate: "/b"}
	sourceBasis := ComparisonBasisSourceEndpoint
	trustedBasis := ComparisonBasisTrustedSharedIdentity
	trustedReference := "shared-identity:widget-read"
	operationID := "getWidget"

	tests := []struct {
		name string
		row  OpenAPIComparisonRow
	}{
		{
			name: "unsorted operations",
			row: OpenAPIComparisonRow{
				State: ComparisonAmbiguous, Basis: &sourceBasis,
				Operations: []OpenAPIOperationCandidate{operationB, operationA},
			},
		},
		{
			name: "duplicate operations",
			row: OpenAPIComparisonRow{
				State: ComparisonAmbiguous, Basis: &sourceBasis,
				Operations: []OpenAPIOperationCandidate{operationA, operationA},
			},
		},
		{
			name: "trusted conflict missing operation id",
			row: OpenAPIComparisonRow{
				State: ComparisonConflict, Basis: &trustedBasis, BasisReference: &trustedReference,
				Operations: []OpenAPIOperationCandidate{{Method: "POST", PathTemplate: "/different"}},
			},
		},
		{
			name: "trusted conflict missing basis reference",
			row: OpenAPIComparisonRow{
				State: ComparisonConflict, Basis: &trustedBasis,
				Operations: []OpenAPIOperationCandidate{{OperationID: &operationID, Method: "POST", PathTemplate: "/different"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := validUsableDiagnostics(source)
			diagnostics.Comparisons["resource_observed_http"] = test.row
			diagnostics.Summary.ComparisonCounts = OpenAPIComparisonCounts{NotComparable: 6}
			switch test.row.State {
			case ComparisonAmbiguous:
				diagnostics.Summary.ComparisonCounts.Ambiguous = 1
			case ComparisonConflict:
				diagnostics.Summary.ComparisonCounts.Conflict = 1
			}
			if err := ValidateOpenAPIDiagnosticsReport(diagnostics, source); err == nil {
				t.Errorf("ValidateOpenAPIDiagnosticsReport(%s) error = nil, want evidence error", test.name)
			}
		})
	}
}

func TestOpenAPIAmbiguityAndConflictUseObservedEndpointComparator(t *testing.T) {
	source := validSevenStateReport()
	basis := ComparisonBasisSourceEndpoint
	diagnostics := validUsableDiagnostics(source)
	diagnostics.Comparisons["resource_observed_http"] = OpenAPIComparisonRow{
		State: ComparisonAmbiguous,
		Basis: &basis,
		Operations: []OpenAPIOperationCandidate{
			{Method: "GET", PathTemplate: "/widgets/{id}"},
			{Method: "GET", PathTemplate: "/widgets/{widget_id}"},
		},
	}
	diagnostics.Summary.ComparisonCounts = OpenAPIComparisonCounts{NotComparable: 6, Ambiguous: 1}
	if err := ValidateOpenAPIDiagnosticsReport(diagnostics, source); err != nil {
		t.Fatalf("ValidateOpenAPIDiagnosticsReport(parameter-name ambiguity) error = %v, want nil", err)
	}

	unrelated := diagnostics
	unrelated.Comparisons = cloneComparisons(diagnostics.Comparisons)
	row := unrelated.Comparisons["resource_observed_http"]
	row.Operations = []OpenAPIOperationCandidate{
		{Method: "GET", PathTemplate: "/other/{id}"},
		{Method: "GET", PathTemplate: "/widgets/{id}"},
	}
	unrelated.Comparisons["resource_observed_http"] = row
	if err := ValidateOpenAPIDiagnosticsReport(unrelated, source); err == nil {
		t.Error("ValidateOpenAPIDiagnosticsReport(unrelated ambiguity) error = nil, want viable-match error")
	}

	conflict := validUsableDiagnostics(source)
	conflictBasis := ComparisonBasisExplicitBinding
	conflictReference := "reviewed-binding:resource_observed_http"
	conflict.Comparisons["resource_observed_http"] = OpenAPIComparisonRow{
		State:          ComparisonConflict,
		Basis:          &conflictBasis,
		BasisReference: &conflictReference,
		Operations:     []OpenAPIOperationCandidate{{Method: "GET", PathTemplate: "/widgets/{id}"}},
	}
	conflict.Summary.ComparisonCounts = OpenAPIComparisonCounts{NotComparable: 6, Conflict: 1}
	if err := ValidateOpenAPIDiagnosticsReport(conflict, source); err == nil {
		t.Error("ValidateOpenAPIDiagnosticsReport(same-endpoint conflict) error = nil, want positive-difference error")
	}
}

func TestZeroRowSourceAndOpenAPIPartitions(t *testing.T) {
	source := SourceEvidenceReport{
		Kind:                  "infrawright.source_evidence_report",
		SchemaVersion:         1,
		SourceTrust:           SourceTrustUnverified,
		InputProvenanceSHA256: testSHA,
		Resources:             map[string]SourceEvidenceRow{},
		Summary: SourceSummary{
			EndpointCoverage: ExactCoverage{State: CoverageNotApplicable},
		},
	}
	if err := ValidateSourceEvidenceReport(source); err != nil {
		t.Fatalf("ValidateSourceEvidenceReport(zero rows) error = %v, want nil", err)
	}
	for _, state := range []OpenAPIDocumentState{OpenAPIAbsent, OpenAPIUsable} {
		diagnostics := OpenAPIDiagnosticsReport{
			Kind:          "infrawright.openapi_diagnostics",
			SchemaVersion: 1,
			SourceTrust:   SourceTrustUnverified,
			DocumentState: state,
			Comparisons:   map[string]OpenAPIComparisonRow{},
		}
		renderedSource, _ := RenderSourceEvidenceReport(source)
		diagnostics.SourceReportSHA256 = sha256Text([]byte(renderedSource))
		if err := ValidateOpenAPIDiagnosticsReport(diagnostics, source); err != nil {
			t.Errorf("ValidateOpenAPIDiagnosticsReport(zero rows, %s) error = %v, want nil", state, err)
		}
	}
}

func validProvenance() SourceProvenance {
	revision := "sdk-revision"
	return SourceProvenance{
		Kind:          "infrawright.source_provenance",
		SchemaVersion: 1,
		Provider: ProviderSourceBinding{
			Repository: "https://example.test/provider.git",
			ModulePath: "example.test/provider",
			Revision:   "provider-revision",
			TreeSHA256: testSHA,
			Files: []FileBinding{
				{Path: "provider/provider.go", SHA256: testSHA},
				{Path: "provider/resource_widget.go", SHA256: testSHA},
			},
		},
		ProviderModule: ProviderModuleBinding{
			GoMod:         FileBinding{Path: "go.mod", SHA256: testSHA},
			GoSum:         &FileBinding{Path: "go.sum", SHA256: testSHA},
			LocalReplaces: []LocalModuleReplaceBinding{},
		},
		OpenAPI:         nil,
		TerraformSchema: FileBinding{Path: "schema/provider.json", SHA256: testSHA},
		SDKs: []SDKSourceBinding{
			{
				ModulePath:    "example.test/sdk",
				ModuleVersion: "v1.2.3",
				Repository:    "https://example.test/sdk.git",
				Revision:      &revision,
				Files:         []FileBinding{{Path: "widgets/get.go", SHA256: testSHA}},
			},
		},
		Selection: SelectionBinding{
			ResourceTypes: []string{"example_widget"},
			Filters:       []SelectionFilterBinding{{Name: "prefix", Values: []string{"example_"}}},
		},
	}
}

func validSevenStateReport() SourceEvidenceReport {
	registration := func() *SourceSymbol {
		return &SourceSymbol{
			PackagePath: "example.test/provider/provider",
			Symbol:      "resourceWidget",
			Location:    packageSourceLocation("provider/provider.go"),
		}
	}
	read := sourceSymbol("provider/resource_widget.go", "resourceWidgetRead", "resourceWidgetRead")
	helper := sourceSymbol("provider/resource_widget.go", "readWidget", "readWidget")
	providerStep := SourceCallStep{
		Kind:     CallProviderHelper,
		Symbol:   "readWidget",
		Caller:   read,
		Callee:   &helper,
		Location: sourceLocation("provider/resource_widget.go", "resourceWidgetRead"),
	}
	sdkImport := "example.test/sdk/widgets"
	sdkGet := sdkSourceSymbol("example.test/sdk", "widgets/get.go", "Get", "Get")
	sdkGetV2 := sdkSourceSymbol("example.test/sdk", "widgets/get.go", "GetV2", "GetV2")
	sdkStep := func(callee SourceSymbol) SourceCallStep {
		return SourceCallStep{
			Kind:       CallSDKPackageFunction,
			Symbol:     "widgets." + callee.Symbol,
			ImportPath: &sdkImport,
			Caller:     read,
			Callee:     &callee,
			Location:   sourceLocation("provider/resource_widget.go", "resourceWidgetRead"),
		}
	}
	sdkCall := func(callee SourceSymbol) SDKCallEvidence {
		return SDKCallEvidence{
			ModulePath:    "example.test/sdk",
			ModuleVersion: "v1.2.3",
			PackagePath:   callee.PackagePath,
			Symbol:        callee.Symbol,
			Location:      callee.Location,
		}
	}
	providerRawHTTP := SourceCallStep{
		Kind:     CallRawHTTP,
		Symbol:   "client.NewRequest",
		Caller:   helper,
		Location: sourceLocation("provider/resource_widget.go", "readWidget"),
	}
	sdkRawHTTP := func(caller SourceSymbol) SourceCallStep {
		return SourceCallStep{
			Kind:     CallRawHTTP,
			Symbol:   "client.NewRequest",
			Caller:   caller,
			Location: sdkSourceLocation("example.test/sdk", "widgets/get.go", *caller.Location.Function),
		}
	}
	methodGet := "GET"
	pathOne := "/widgets/{id}"
	pathTwo := "/v2/widgets/{id}"
	reasons := map[SourceClassification]SourceReasonCode{
		SourceObservedSDKCall: ReasonEndpointNotRecovered,
		SourceAmbiguous:       ReasonMultipleCandidates,
		SourceDynamic:         ReasonDynamicPath,
		SourceUnresolved:      ReasonCallChainUnresolved,
		SourceNoSource:        ReasonProviderSourceMissing,
		SourceNotApplicable:   ReasonReviewedNotApplicable,
	}
	resources := map[string]SourceEvidenceRow{
		"resource_observed_http": {
			Classification:       SourceObservedHTTP,
			LegacyMapped:         true,
			ProviderRegistration: registration(),
			ReadCallback:         &read,
			Chains: []SourceEvidenceChain{{
				Steps: []SourceCallStep{providerStep, providerRawHTTP},
				Endpoint: &HTTPEndpointEvidence{
					Origin: EndpointOriginProvider, Method: "GET", PathTemplate: "/widgets/{id}",
					Location: sourceLocation("provider/resource_widget.go", "readWidget"),
				},
			}},
		},
		"resource_observed_sdk": {
			Classification:       SourceObservedSDKCall,
			ProviderRegistration: registration(),
			ReadCallback:         &read,
			Chains: []SourceEvidenceChain{{
				Steps: []SourceCallStep{sdkStep(sdkGet)}, SDKCall: sdkCallPointer(sdkCall(sdkGet)),
				ReasonCode: reasonPointer(reasons[SourceObservedSDKCall]),
			}},
		},
		"resource_ambiguous": {
			Classification:       SourceAmbiguous,
			ProviderRegistration: registration(),
			ReadCallback:         &read,
			ReasonCode:           reasonPointer(reasons[SourceAmbiguous]),
			Chains: []SourceEvidenceChain{
				{Steps: []SourceCallStep{sdkStep(sdkGet), sdkRawHTTP(sdkGet)}, SDKCall: sdkCallPointer(sdkCall(sdkGet)), Endpoint: &HTTPEndpointEvidence{Origin: EndpointOriginSDK, Method: methodGet, PathTemplate: pathOne, Location: sdkSourceLocation("example.test/sdk", "widgets/get.go", "Get")}},
				{Steps: []SourceCallStep{sdkStep(sdkGetV2), sdkRawHTTP(sdkGetV2)}, SDKCall: sdkCallPointer(sdkCall(sdkGetV2)), Endpoint: &HTTPEndpointEvidence{Origin: EndpointOriginSDK, Method: methodGet, PathTemplate: pathTwo, Location: sdkSourceLocation("example.test/sdk", "widgets/get.go", "GetV2")}},
			},
		},
		"resource_dynamic": {
			Classification:       SourceDynamic,
			ProviderRegistration: registration(),
			ReadCallback:         &read,
			Chains:               []SourceEvidenceChain{{Steps: []SourceCallStep{providerStep, providerRawHTTP}, ReasonCode: reasonPointer(reasons[SourceDynamic])}},
		},
		"resource_unresolved": {
			Classification:       SourceUnresolved,
			ProviderRegistration: registration(),
			ReadCallback:         &read,
			ReasonCode:           reasonPointer(reasons[SourceUnresolved]),
			Chains:               []SourceEvidenceChain{{Steps: []SourceCallStep{providerStep}, ReasonCode: reasonPointer(reasons[SourceUnresolved])}},
		},
		"resource_no_source": {
			Classification: SourceNoSource,
			Chains:         []SourceEvidenceChain{},
			ReasonCode:     reasonPointer(reasons[SourceNoSource]),
		},
		"resource_not_applicable": {
			Classification: SourceNotApplicable,
			Chains:         []SourceEvidenceChain{},
			ReasonCode:     reasonPointer(reasons[SourceNotApplicable]),
		},
	}
	counts := SourceClassificationCounts{
		ObservedHTTP: 1, ObservedSDKCall: 1, Ambiguous: 1, Dynamic: 1,
		Unresolved: 1, NoSource: 1, NotApplicable: 1,
	}
	manifestSHA := testSHA
	return SourceEvidenceReport{
		Kind:                  "infrawright.source_evidence_report",
		SchemaVersion:         1,
		SourceTrust:           SourceTrustVerified,
		SourceManifestSHA256:  &manifestSHA,
		InputProvenanceSHA256: testSHA,
		Resources:             resources,
		Summary: SourceSummary{
			SelectedTotal:           7,
			ApplicableTotal:         6,
			SourceCallObservedTotal: 3,
			EndpointObservedTotal:   1,
			ClassificationCounts:    counts,
			EndpointCoverage: ExactCoverage{
				State: CoverageRatio, Numerator: 1, Denominator: 6,
			},
		},
	}
}

func validSDKSourceMissingReport() SourceEvidenceReport {
	report := validSevenStateReport()
	row := report.Resources["resource_observed_http"]
	missingImport := "example.test/missing-sdk/widgets"
	row.Classification = SourceNoSource
	row.LegacyMapped = false
	row.ReasonCode = reasonPointer(ReasonSDKSourceMissing)
	row.Chains = []SourceEvidenceChain{{
		Steps: []SourceCallStep{{
			Kind: CallSDKSourceMissing, Symbol: "widgets.Get", ImportPath: &missingImport,
			Caller:   *row.ReadCallback,
			Location: sourceLocation("provider/resource_widget.go", "resourceWidgetRead"),
		}},
		ReasonCode: reasonPointer(ReasonSDKSourceMissing),
	}}
	report.Resources["resource_no_source"] = row
	return report
}

func validAbsentDiagnostics(source SourceEvidenceReport) OpenAPIDiagnosticsReport {
	comparisons := make(map[string]OpenAPIComparisonRow, len(source.Resources))
	for resource := range source.Resources {
		comparisons[resource] = OpenAPIComparisonRow{State: ComparisonNotAttempted, Operations: []OpenAPIOperationCandidate{}}
	}
	diagnostics := OpenAPIDiagnosticsReport{
		Kind:                 "infrawright.openapi_diagnostics",
		SchemaVersion:        1,
		SourceTrust:          source.SourceTrust,
		SourceManifestSHA256: source.SourceManifestSHA256,
		DocumentState:        OpenAPIAbsent,
		Comparisons:          comparisons,
		Summary: OpenAPIComparisonSummary{
			ComparisonEligibleTotal: 1,
			ComparisonCounts:        OpenAPIComparisonCounts{NotAttempted: 7},
		},
	}
	renderedSource, _ := RenderSourceEvidenceReport(source)
	diagnostics.SourceReportSHA256 = sha256Text([]byte(renderedSource))
	return diagnostics
}

func validUsableDiagnostics(source SourceEvidenceReport) OpenAPIDiagnosticsReport {
	diagnostics := validAbsentDiagnostics(source)
	diagnostics.DocumentState = OpenAPIUsable
	for resource, row := range source.Resources {
		state := ComparisonNotComparable
		if row.Classification == SourceObservedHTTP {
			state = ComparisonCorroborated
		}
		comparison := OpenAPIComparisonRow{State: state, Operations: []OpenAPIOperationCandidate{}}
		if state == ComparisonCorroborated {
			basis := ComparisonBasisSourceEndpoint
			comparison.Basis = &basis
			comparison.Operations = []OpenAPIOperationCandidate{{Method: "GET", PathTemplate: "/widgets/{id}"}}
		}
		diagnostics.Comparisons[resource] = comparison
	}
	diagnostics.Summary.ComparisonCounts = OpenAPIComparisonCounts{NotComparable: 6, Corroborated: 1}
	return diagnostics
}

func validDegradedDiagnostics(source SourceEvidenceReport) OpenAPIDiagnosticsReport {
	diagnostics := validUsableDiagnostics(source)
	reason := OpenAPIReasonDegradedOperation
	diagnostics.DocumentState = OpenAPIDegraded
	diagnostics.ReasonCode = &reason
	diagnostics.Summary.DegradedComparisonTotal = 1
	return diagnostics
}

func sourceSymbol(path, function, symbol string) SourceSymbol {
	return SourceSymbol{
		PackagePath: packagePathForFile("example.test/provider", path),
		Symbol:      symbol,
		Location:    sourceLocation(path, function),
	}
}

func sourceLocation(path, function string) SourceLocation {
	return SourceLocation{
		Origin: SourceLocationProvider, Path: path,
		Function: stringPointer(function), Line: 10, Column: 2,
	}
}

func packageSourceLocation(path string) SourceLocation {
	return SourceLocation{Origin: SourceLocationProvider, Path: path, Line: 10, Column: 2}
}

func sdkSourceSymbol(modulePath, path, function, symbol string) SourceSymbol {
	return SourceSymbol{
		PackagePath: packagePathForFile(modulePath, path),
		Symbol:      symbol,
		Location:    sdkSourceLocation(modulePath, path, function),
	}
}

func sdkSourceLocation(modulePath, path, function string) SourceLocation {
	return SourceLocation{
		Origin: SourceLocationSDK, SDKModulePath: stringPointer(modulePath), Path: path,
		Function: stringPointer(function), Line: 10, Column: 2,
	}
}

func sdkCallPointer(call SDKCallEvidence) *SDKCallEvidence {
	return &call
}

func bindReportToVerifiedInput(t *testing.T, report *SourceEvidenceReport) InputProvenance {
	t.Helper()
	manifest := validProvenance()
	manifest.Selection.ResourceTypes = sortedMapKeys(report.Resources)
	manifest.Selection.Filters = append(
		manifest.Selection.Filters,
		reviewedNotApplicableFilter(report)...,
	)
	renderedManifest, err := RenderSourceProvenance(manifest)
	if err != nil {
		t.Fatalf("RenderSourceProvenance(report binding) error = %v, want nil", err)
	}
	manifestSHA := sha256Text([]byte(renderedManifest))
	input := InputProvenance{
		Kind:                 "infrawright.input_provenance",
		SchemaVersion:        1,
		SourceTrust:          SourceTrustVerified,
		SourceManifestSHA256: &manifestSHA,
		SourceManifest:       &manifest,
	}
	bindReportToInput(t, report, input)
	return input
}

func bindReportToUnverifiedInput(t *testing.T, report *SourceEvidenceReport) InputProvenance {
	t.Helper()
	report.SourceTrust = SourceTrustUnverified
	report.SourceManifestSHA256 = nil
	row := report.Resources["resource_observed_http"]
	row.LegacyMapped = false
	report.Resources["resource_observed_http"] = row
	input := validUnverifiedInputProvenance()
	input.UnverifiedObservation.Selection.ResourceTypes = sortedMapKeys(report.Resources)
	input.UnverifiedObservation.Selection.Filters = reviewedNotApplicableFilter(report)
	bindReportToInput(t, report, input)
	return input
}

func reviewedNotApplicableFilter(report *SourceEvidenceReport) []SelectionFilterBinding {
	values := make([]string, 0)
	for _, resource := range sortedMapKeys(report.Resources) {
		if report.Resources[resource].Classification == SourceNotApplicable {
			values = append(values, resource)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return []SelectionFilterBinding{{Name: SelectionFilterReviewedNotApplicable, Values: values}}
}

func validUnverifiedInputProvenance() InputProvenance {
	return InputProvenance{
		Kind:          "infrawright.input_provenance",
		SchemaVersion: 1,
		SourceTrust:   SourceTrustUnverified,
		UnverifiedObservation: &UnverifiedSourceObservation{
			ProviderModulePath: "example.test/provider",
			ProviderFiles: []FileBinding{
				{Path: "provider/provider.go", SHA256: testSHA},
				{Path: "provider/resource_widget.go", SHA256: testSHA},
			},
			TerraformSchema: FileBinding{Path: "schema/provider.json", SHA256: testSHA},
			SDKs: []UnverifiedSDKObservation{{
				ModulePath: "example.test/sdk", ModuleVersion: "v1.2.3",
				Files: []FileBinding{{Path: "widgets/get.go", SHA256: testSHA}},
			}},
			Selection: SelectionBinding{
				ResourceTypes: []string{},
				Filters:       []SelectionFilterBinding{},
			},
		},
	}
}

func cloneComparisons(source map[string]OpenAPIComparisonRow) map[string]OpenAPIComparisonRow {
	cloned := make(map[string]OpenAPIComparisonRow, len(source))
	for resource, row := range source {
		cloned[resource] = row
	}
	return cloned
}

func reverseTopLevelObject(t *testing.T, input string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		t.Fatalf("json.Unmarshal(reverseTopLevelObject input) error = %v, want nil", err)
	}
	keys := sortedMapKeys(fields)
	var output strings.Builder
	output.WriteByte('{')
	for index := len(keys) - 1; index >= 0; index-- {
		if index != len(keys)-1 {
			output.WriteByte(',')
		}
		key, err := json.Marshal(keys[index])
		if err != nil {
			t.Fatalf("json.Marshal(reverseTopLevelObject key) error = %v, want nil", err)
		}
		output.Write(key)
		output.WriteByte(':')
		output.Write(fields[keys[index]])
	}
	output.WriteByte('}')
	return output.String()
}

func bindReportToInput(t *testing.T, report *SourceEvidenceReport, input InputProvenance) {
	t.Helper()
	renderedInput, err := RenderInputProvenance(input)
	if err != nil {
		t.Fatalf("RenderInputProvenance(report binding) error = %v, want nil", err)
	}
	report.SourceManifestSHA256 = input.SourceManifestSHA256
	report.InputProvenanceSHA256 = sha256Text([]byte(renderedInput))
}

func rebindReportToInput(t *testing.T, report *SourceEvidenceReport, input InputProvenance) InputProvenance {
	t.Helper()
	if input.SourceTrust == SourceTrustVerified {
		renderedManifest, err := RenderSourceProvenance(*input.SourceManifest)
		if err != nil {
			t.Fatalf("RenderSourceProvenance(rebinding) error = %v, want nil", err)
		}
		input.SourceManifestSHA256 = stringPointer(sha256Text([]byte(renderedManifest)))
	}
	bindReportToInput(t, report, input)
	return input
}

func reasonPointer(reason SourceReasonCode) *SourceReasonCode {
	return &reason
}

func stringPointer(value string) *string {
	return &value
}
