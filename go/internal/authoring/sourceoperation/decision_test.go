package sourceoperation

import (
	"context"
	"errors"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/openapiadapter"
	"github.com/dvmrry/infrawright-dev/go/internal/authoring/sourcebind"
)

func TestBundleStatusUsesValidatedSealedDiagnostics(t *testing.T) {
	input := fixtureInput(t)
	report := mustDecodeSourceReport(t, input.SourceRegistry)

	absent, err := compile(context.Background(), input, contracts.SourceTrustVerified, nil)
	if err != nil {
		t.Fatalf("compile(absent) error = %v, want nil", err)
	}
	if status, err := absent.Status(); err != nil || status.OpenAPIConflict || status.DocumentState != contracts.OpenAPIAbsent || status.ReasonCode != nil {
		t.Fatalf("Status(absent) = (%#v, %v), want absent non-conflict", status, err)
	}

	result, err := openapiadapter.Analyze(context.Background(), availableOpenAPI([]byte(usableOpenAPI)), report)
	if err != nil {
		t.Fatalf("Analyze(usable) error = %v, want nil", err)
	}
	usable, err := compile(context.Background(), input, contracts.SourceTrustVerified, &result)
	if err != nil {
		t.Fatalf("compile(usable) error = %v, want nil", err)
	}
	artifacts := usable.Artifacts()
	diagnostics := mustBundleOpenAPIDiagnostics(t, usable, report)
	for resource, sourceRow := range report.Resources {
		if sourceRow.Classification != contracts.SourceObservedHTTP {
			continue
		}
		old := diagnostics.Comparisons[resource].State
		decrementComparisonCount(&diagnostics.Summary.ComparisonCounts, old)
		basis := contracts.ComparisonBasisExplicitBinding
		reference := "fixture explicit conflict"
		diagnostics.Comparisons[resource] = contracts.OpenAPIComparisonRow{
			State:          contracts.ComparisonConflict,
			Basis:          &basis,
			BasisReference: &reference,
			Operations: []contracts.OpenAPIOperationCandidate{{
				Method:       "POST",
				PathTemplate: "/conflicting/path",
			}},
		}
		diagnostics.Summary.ComparisonCounts.Conflict++
		break
	}
	rendered, err := contracts.RenderOpenAPIDiagnosticsReport(diagnostics, report)
	if err != nil {
		t.Fatalf("RenderOpenAPIDiagnosticsReport(conflict) error = %v, want nil", err)
	}
	artifacts[len(artifacts)-1].Bytes = []byte(rendered)
	conflicting := Bundle{artifacts: artifacts, status: BundleStatus{
		DocumentState: contracts.OpenAPIUsable, OpenAPIConflict: true,
	}, sealed: true}
	status, err := conflicting.Status()
	if err != nil || !status.OpenAPIConflict {
		t.Fatalf("Status(conflict) = (%#v, %v), want conflict", status, err)
	}

	unavailableResult, err := openapiadapter.Analyze(context.Background(), sourcebind.OpenAPIStatus{Err: errors.New("unreadable")}, report)
	if err != nil {
		t.Fatalf("Analyze(unavailable) error = %v, want nil", err)
	}
	unavailable, err := compile(context.Background(), input, contracts.SourceTrustVerified, &unavailableResult)
	if err != nil {
		t.Fatalf("compile(unavailable) error = %v, want nil", err)
	}
	first, err := unavailable.Status()
	if err != nil || first.ReasonCode == nil {
		t.Fatalf("Status(unavailable) = (%#v, %v), want reason", first, err)
	}
	wantReason := *first.ReasonCode
	*first.ReasonCode = contracts.OpenAPIReasonCode("mutated")
	again, err := unavailable.Status()
	if err != nil || again.ReasonCode == nil || *again.ReasonCode != wantReason {
		t.Fatalf("Status() defensive reason copy = (%#v, %v), want %q", again, err, wantReason)
	}
}

func TestBundleStatusRejectsUnsealedOrInvalidBundle(t *testing.T) {
	if _, err := (Bundle{}).Status(); err == nil {
		t.Fatal("Status(zero) error = nil, want sealed-bundle rejection")
	}
	compiled, err := compile(context.Background(), fixtureInput(t), contracts.SourceTrustVerified, nil)
	if err != nil {
		t.Fatalf("compile(absent) error = %v, want nil", err)
	}
	manual := Bundle{artifacts: compiled.Artifacts()}
	if _, err := manual.Status(); err == nil {
		t.Fatal("Status(manual) error = nil, want sealed-bundle rejection")
	}
	broken := compiled
	broken.artifacts = broken.artifacts[:len(broken.artifacts)-1]
	if _, err := broken.Status(); err == nil {
		t.Fatal("Status(invalid sealed bundle) error = nil, want validation failure")
	}
}

func decrementComparisonCount(counts *contracts.OpenAPIComparisonCounts, state contracts.OpenAPIComparisonState) {
	switch state {
	case contracts.ComparisonNotAttempted:
		counts.NotAttempted--
	case contracts.ComparisonNotComparable:
		counts.NotComparable--
	case contracts.ComparisonCorroborated:
		counts.Corroborated--
	case contracts.ComparisonMissingPath:
		counts.MissingPath--
	case contracts.ComparisonAmbiguous:
		counts.Ambiguous--
	case contracts.ComparisonConflict:
		counts.Conflict--
	}
}
