package openapiadapter

import (
	"reflect"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/authoring/contracts"
)

func TestComparisonRowsExactParameterMissingAndAmbiguous(t *testing.T) {
	t.Parallel()
	endpoint := func(method, path string) contracts.SourceEvidenceRow {
		return contracts.SourceEvidenceRow{Classification: contracts.SourceObservedHTTP, Chains: []contracts.SourceEvidenceChain{{Endpoint: &contracts.HTTPEndpointEvidence{Method: method, PathTemplate: path}}}}
	}
	source := contracts.SourceEvidenceReport{Resources: map[string]contracts.SourceEvidenceRow{
		"exact": endpoint("GET", "/things/{id}"), "parameter": endpoint("GET", "/things/{id}"), "missing": endpoint("DELETE", "/things"), "other": {Classification: contracts.SourceNoSource},
	}}
	operations := []Operation{{Method: "GET", PathTemplate: "/things/{id}"}, {Method: "GET", PathTemplate: "/things/{thing}"}, {Method: "GET", PathTemplate: "/things/{other}"}}
	rows := comparisonRows(source, contracts.OpenAPIUsable, operations)
	if rows["missing"].State != contracts.ComparisonMissingPath {
		t.Errorf("missing state = %q, want missing_path", rows["missing"].State)
	}
	if rows["other"].State != contracts.ComparisonNotComparable {
		t.Errorf("other state = %q, want not_comparable", rows["other"].State)
	}
	if rows["exact"].State != contracts.ComparisonAmbiguous || rows["parameter"].State != contracts.ComparisonAmbiguous {
		t.Errorf("parameter candidates = %#v, want ambiguous", rows)
	}
	for _, row := range rows {
		if row.State == contracts.ComparisonConflict {
			t.Errorf("comparisonRows minted conflict: %#v", row)
		}
	}
}

func TestComparisonRowsCompletePartitionAndLiteralCandidateOrdering(t *testing.T) {
	t.Parallel()
	endpoint := func(method, path string) contracts.SourceEvidenceRow {
		return contracts.SourceEvidenceRow{Classification: contracts.SourceObservedHTTP, Chains: []contracts.SourceEvidenceChain{{Endpoint: &contracts.HTTPEndpointEvidence{Method: method, PathTemplate: path}}}}
	}
	source := contracts.SourceEvidenceReport{Resources: map[string]contracts.SourceEvidenceRow{
		"exact":          endpoint("GET", "/exact"),
		"parameter_only": endpoint("GET", "/parameter/{id}"),
		"missing_method": endpoint("DELETE", "/exact"),
		"missing_path":   endpoint("GET", "/does-not-exist"),
		"ambiguous":      endpoint("GET", "/ambiguous/{id}"),
		"not_observed":   {Classification: contracts.SourceNoSource},
	}, Summary: contracts.SourceSummary{SelectedTotal: 6, ClassificationCounts: contracts.SourceClassificationCounts{ObservedHTTP: 5}}}
	operations := []Operation{
		{Method: "GET", PathTemplate: "/exact", OperationID: stringPointer("exact")},
		{Method: "GET", PathTemplate: "/parameter/{thing}", OperationID: stringPointer("normalized")},
		{Method: "GET", PathTemplate: "/ambiguous/{z}", OperationID: stringPointer("z")},
		{Method: "GET", PathTemplate: "/ambiguous/{a}", OperationID: stringPointer("a")},
	}
	rows := comparisonRows(source, contracts.OpenAPIUsable, operations)
	for resource, want := range map[string]contracts.OpenAPIComparisonState{
		"exact":          contracts.ComparisonCorroborated,
		"parameter_only": contracts.ComparisonCorroborated,
		"missing_method": contracts.ComparisonMissingPath,
		"missing_path":   contracts.ComparisonMissingPath,
		"ambiguous":      contracts.ComparisonAmbiguous,
		"not_observed":   contracts.ComparisonNotComparable,
	} {
		if got := rows[resource].State; got != want {
			t.Errorf("comparisonRows(%s).State = %q, want %q", resource, got, want)
		}
	}
	if got, want := rows["parameter_only"].Operations, []contracts.OpenAPIOperationCandidate{{Method: "GET", PathTemplate: "/parameter/{id}", OperationID: stringPointer("normalized")}}; !reflect.DeepEqual(got, want) {
		t.Errorf("comparisonRows(parameter-name normalization).Operations = %#v, want %#v", got, want)
	}
	if got, want := rows["ambiguous"].Operations, []contracts.OpenAPIOperationCandidate{{Method: "GET", PathTemplate: "/ambiguous/{a}", OperationID: stringPointer("a")}, {Method: "GET", PathTemplate: "/ambiguous/{z}", OperationID: stringPointer("z")}}; !reflect.DeepEqual(got, want) {
		t.Errorf("comparisonRows(ambiguous literal ordering).Operations = %#v, want %#v", got, want)
	}
	gotSummary := summary(source, contracts.OpenAPIUsable, rows)
	wantCounts := contracts.OpenAPIComparisonCounts{NotComparable: 1, Corroborated: 2, MissingPath: 2, Ambiguous: 1}
	if got := gotSummary.ComparisonCounts; got != wantCounts {
		t.Errorf("summary(complete partition).ComparisonCounts = %#v, want %#v", got, wantCounts)
	}
	if got, want := gotSummary.ComparisonEligibleTotal, 5; got != want {
		t.Errorf("summary(complete partition).ComparisonEligibleTotal = %d, want %d", got, want)
	}
	if got, want := gotSummary.DegradedComparisonTotal, 0; got != want {
		t.Errorf("summary(usable partition).DegradedComparisonTotal = %d, want %d", got, want)
	}
	if got, want := gotSummary.ComparisonCounts.Conflict, 0; got != want {
		t.Errorf("summary(complete partition).Conflict = %d, want %d", got, want)
	}
	if got, want := uniqueOperations([]Operation{{Method: "GET", PathTemplate: "/shared"}, {Method: "GET", PathTemplate: "/shared"}}), []Operation{{Method: "GET", PathTemplate: "/shared"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("uniqueOperations(shared endpoint) = %#v, want %#v", got, want)
	}
}

func TestComparisonRowsNormalizesSoleParameterCandidate(t *testing.T) {
	t.Parallel()
	source := contracts.SourceEvidenceReport{Resources: map[string]contracts.SourceEvidenceRow{"resource": {Classification: contracts.SourceObservedHTTP, Chains: []contracts.SourceEvidenceChain{{Endpoint: &contracts.HTTPEndpointEvidence{Method: "GET", PathTemplate: "/things/{id}"}}}}}}
	rows := comparisonRows(source, contracts.OpenAPIUsable, []Operation{{Method: "GET", PathTemplate: "/things/{thing}"}})
	row := rows["resource"]
	if row.State != contracts.ComparisonCorroborated || row.Operations[0].PathTemplate != "/things/{id}" {
		t.Errorf("sole parameter row = %#v, want normalized corroboration", row)
	}
	counts := summary(source, contracts.OpenAPIUsable, rows).ComparisonCounts
	if counts.Corroborated != 1 || counts.Conflict != 0 {
		t.Errorf("summary = %#v, want one corroborated and no conflict", counts)
	}
}
